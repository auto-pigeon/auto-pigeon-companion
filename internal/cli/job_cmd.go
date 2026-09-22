package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/incident"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// `companion job` — the same executor the GUI drives, from a terminal.
//
// Not a client of the HTTP API, and not a second implementation either: both
// go through [job.Service] over the same store on disk. That is what makes
// `companion job cancel` work on a build the GUI started — the stop is a marker
// in the job's own directory, which whichever process owns it notices — and
// what makes a script's view of a job identical to the page's.

const jobUsage = `usage:
  companion job run     --profile <id> --action <id> [--input n=path]... [--option n=v]... [flags]
  companion job preview --profile <id> --action <id> [same flags]   resolve the command, start nothing
  companion job list    [--state <state>] [--limit <n>] [--json]
  companion job show    <job-id> [--json]
  companion job logs    <job-id> [--stream stdout|stderr] [--raw]
  companion job cancel  <job-id>
  companion job retry   <job-id> [--wait]
  companion job artifacts <job-id> [--json]
  companion job profiles [--json]                                   what can be run on this machine
  companion job limits  [--json]                                    what a job's logs may keep
`

func runJob(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, jobUsage)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "run":
		return jobRun(env, rest, false)
	case "preview":
		return jobRun(env, rest, true)
	case "list":
		return jobList(env, rest)
	case "show":
		return jobShow(env, rest)
	case "logs":
		return jobLogs(env, rest)
	case "cancel":
		return jobCancel(env, rest)
	case "retry":
		return jobRetry(env, rest)
	case "artifacts":
		return jobArtifacts(env, rest)
	case "profiles":
		return jobProfiles(env, rest)
	case "limits":
		return jobLimits(env, rest)
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, jobUsage)
		return 0
	default:
		fmt.Fprintf(env.Stderr, "error: unknown job subcommand %q\n\n", args[0])
		fmt.Fprint(env.Stderr, jobUsage)
		return 2
	}
}

// pairs collects repeated `--flag name=value` arguments.
//
// A flag.Value rather than a comma-separated string, because a value here can
// be a path, and a path can contain a comma.
type pairs map[string]string

func (p pairs) String() string {
	names := make([]string, 0, len(p))
	for name := range p {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+p[name])
	}
	return strings.Join(parts, " ")
}

func (p pairs) Set(raw string) error {
	name, value, found := strings.Cut(raw, "=")
	if !found || strings.TrimSpace(name) == "" {
		return fmt.Errorf("%q is not `name=value`", raw)
	}
	if _, repeated := p[name]; repeated {
		return fmt.Errorf("%q was given twice", name)
	}
	p[name] = value
	return nil
}

func (p pairs) orNil() map[string]string {
	if len(p) == 0 {
		return nil
	}
	return p
}

// jobFlags is the request shape every run/preview shares.
type jobFlags struct {
	profile     *string
	action      *string
	label       *string
	inputs      pairs
	options     pairs
	runtimes    pairs
	executables pairs
	roots       pairs
}

func registerJobFlags(set *flag.FlagSet) *jobFlags {
	f := &jobFlags{
		profile:     set.String("profile", "", "profile id to run"),
		action:      set.String("action", "", "action id within the profile"),
		label:       set.String("label", "", "short name for this job in the list"),
		inputs:      pairs{},
		options:     pairs{},
		runtimes:    pairs{},
		executables: pairs{},
		roots:       pairs{},
	}
	set.Var(f.inputs, "input", "an input file, as name=path (repeatable)")
	set.Var(f.options, "option", "an option value, as name=value (repeatable)")
	set.Var(f.runtimes, "runtime", "a launch-time value, as name=value (repeatable)")
	set.Var(f.executables, "executable", "where a declared executable is on this machine, as name=path (repeatable)")
	set.Var(f.roots, "root", "where a declared root is on this machine, as role=path (repeatable)")
	return f
}

func (f *jobFlags) request() job.Request {
	return job.Request{
		ProfileID:   *f.profile,
		ActionID:    *f.action,
		Label:       *f.label,
		Inputs:      f.inputs.orNil(),
		Options:     f.options.orNil(),
		Runtime:     f.runtimes.orNil(),
		Executables: f.executables.orNil(),
		Roots:       f.roots.orNil(),
	}
}

// openJobs builds the executor for one CLI invocation.
//
// started decides whether workers run. A read-only command opens the same
// store without them, so `companion job list` cannot start anything and cannot
// take a job away from a running server's recovery pass.
func openJobs(ctx context.Context, env *Env, started bool, logf func(string, ...any)) (*job.Service, config.Config, error) {
	settings, err := loadSettings(env)
	if err != nil {
		return nil, settings, err
	}
	jobsDir, profilesDir, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return nil, settings, err
	}
	store, err := job.OpenStore(jobsDir)
	if err != nil {
		return nil, settings, err
	}
	service, err := job.NewService(job.Options{
		Store: store,
		// Two sources, in order: documents, then the engine profiles generated
		// from this machine's launch configs. Documents win, so a curated
		// profile for a game replaces the generated one by existing.
		Catalog:     job.NewCatalog(profilesDir),
		Bindings:    binding.Lookup(bindingsPath),
		Concurrency: settings.JobConcurrency,
		// The AUB session token, so that a tool which somehow printed it does
		// not put it in a log. Read through a function: the service never holds
		// a credential of its own.
		Secrets: func() []string { return []string{settings.Session.Token} },
		// Nil for a one-shot command, which reports its own progress in its own
		// words; `serve` passes a real one, because there the operator has no
		// other view of what the executor is doing.
		Logf: logf,
		// A failed job is reported as `aucom.job_failed` — kind and outcome
		// only, never the command, its arguments or a path. See
		// internal/incident/job.go.
		OnFinished: incident.JobHook(env.incidents(settings), nil),
	})
	if err != nil {
		return nil, settings, err
	}
	if started {
		if err := service.Start(ctx); err != nil {
			return nil, settings, err
		}
	}
	return service, settings, nil
}

// statePaths resolves where the job store, the profile directory and the
// binding file live.
//
// An explicit --config path puts all three beside it. A test or a deliberate
// second profile that pointed at another config file would otherwise still
// share the real machine's jobs and grants, which is the opposite of what
// asking for a different config file means.
func statePaths(env *Env, settings config.Config) (jobsDir, profilesDir, bindingsPath string, err error) {
	if env.ConfigPath != "" {
		dir := filepath.Dir(env.ConfigPath)
		return filepath.Join(dir, "jobs"), filepath.Join(dir, "profiles"), filepath.Join(dir, "bindings.json"), nil
	}
	if jobsDir, err = settings.Jobs(); err != nil {
		return "", "", "", err
	}
	if profilesDir, err = settings.Profiles(); err != nil {
		return "", "", "", err
	}
	if bindingsPath, err = config.BindingsPath(); err != nil {
		return "", "", "", err
	}
	return jobsDir, profilesDir, bindingsPath, nil
}

// warnNotWaiting says what `--wait=false` actually does today.
//
// The executor is in this process. A command that submits a job and returns
// runs its own deferred shutdown, which cancels the job it just queued, and the
// job is recorded `interrupted` without a process ever having started. That is
// a defect, not a design — but the flag has shipped, so it is not removed from
// under a script here; it is made to tell the truth, so nobody reads "queued"
// and believes something is running.
//
// `companion engine run` has no such flag for the same reason.
func warnNotWaiting(env *Env, id string) {
	fmt.Fprintf(env.Stderr,
		"warning: --wait=false returns now, and the executor lives in this process, so job %s will be\n"+
			"         recorded as interrupted rather than run. Use `companion serve` and the local API to\n"+
			"         start something that outlives one command.\n", id)
}

func jobRun(env *Env, args []string, previewOnly bool) int {
	name := "job run"
	if previewOnly {
		name = "job preview"
	}
	set := newFlagSet(env, name)
	flags := registerJobFlags(set)
	wait := set.Bool("wait", true, "wait for the job to finish and stream its output")
	asJSON := set.Bool("json", false, "print the job record as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *flags.profile == "" || *flags.action == "" {
		fmt.Fprintf(env.Stderr, "error: %s requires --profile and --action\n", name)
		return 2
	}

	ctx, stop := signalContext()
	defer stop()

	service, _, err := openJobs(ctx, env, !previewOnly, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	if previewOnly {
		previewed, err := service.Preview(flags.request())
		if err != nil {
			return fail(env, err)
		}
		if *asJSON {
			return printJSON(env, previewed)
		}
		printPreview(env, previewed)
		if previewed.Error != "" {
			// A preview of something that is not yet granted is still a
			// preview — it is what somebody reads before granting — but the
			// exit status says it would not run as things stand.
			fmt.Fprintf(env.Stderr, "\nthis would not run yet: %s\n", previewed.Error)
			return 1
		}
		return 0
	}

	// The output goes to the terminal as it is produced *and* into the job's
	// bounded log. One execution, two readers — not a streaming mode beside a
	// recording one.
	submitted, err := service.SubmitWatched(flags.request(), env.Stdout)
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stderr, "job %s queued\n", submitted.ID)
	if !*wait {
		warnNotWaiting(env, submitted.ID)
		if *asJSON {
			return printJSON(env, submitted)
		}
		return 0
	}

	finished, err := service.Wait(ctx, submitted.ID)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		if code := printJSON(env, finished); code != 0 {
			return code
		}
	} else {
		printOutcome(env, finished)
	}
	if finished.Succeeded() {
		return 0
	}
	return 1
}

func jobList(env *Env, args []string) int {
	set := newFlagSet(env, "job list")
	state := set.String("state", "", "show only jobs in this state")
	limit := set.Int("limit", 20, "how many to show; 0 means all")
	asJSON := set.Bool("json", false, "print the records as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	service, _, err := openJobs(context.Background(), env, false, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	jobs, err := service.List()
	if err != nil {
		return fail(env, err)
	}
	if *state != "" {
		filtered := jobs[:0]
		for _, candidate := range jobs {
			if string(candidate.State) == *state {
				filtered = append(filtered, candidate)
			}
		}
		jobs = filtered
	}
	if *limit > 0 && *limit < len(jobs) {
		jobs = jobs[:*limit]
	}
	if *asJSON {
		return printJSON(env, jobs)
	}
	if len(jobs) == 0 {
		fmt.Fprintln(env.Stdout, "no jobs")
		return 0
	}
	hosting := 0
	for _, candidate := range jobs {
		fmt.Fprintf(env.Stdout, "%s  %-11s  %-9s  %s %s%s\n",
			candidate.ID, candidate.State, candidate.Duration().Round(time.Millisecond),
			candidate.ProfileID, candidate.ActionID, sessionSuffix(candidate))
		// Running, not merely active: a queued dedicated server is not
		// accepting anything yet, and this line's only job is to answer "is
		// my machine reachable right now".
		if candidate.Hosting() && candidate.State == job.Running {
			hosting++
		}
	}
	// Said once, at the end, where somebody scanning a list will see it: the
	// question "is my machine accepting connections right now" should not
	// require reading every row.
	if hosting > 0 {
		fmt.Fprintf(env.Stdout, "\n%d running job(s) are servers other people can connect to.\n", hosting)
	}
	return 0
}

func jobShow(env *Env, args []string) int {
	set := newFlagSet(env, "job show")
	asJSON := set.Bool("json", false, "print the record as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	id, code, ok := oneID(env, "show", rest)
	if !ok {
		return code
	}

	service, _, err := openJobs(context.Background(), env, false, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	found, err := service.Get(id)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, found)
	}
	printOutcome(env, found)
	return 0
}

func jobLogs(env *Env, args []string) int {
	set := newFlagSet(env, "job logs")
	stream := set.String("stream", "stdout", "stdout or stderr")
	raw := set.Bool("raw", false, "the bytes the program wrote, rather than the redacted, terminal-safe view")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	id, code, ok := oneID(env, "logs", rest)
	if !ok {
		return code
	}

	service, _, err := openJobs(context.Background(), env, false, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	data, err := service.Logs(id, *stream, *raw)
	if err != nil {
		return fail(env, err)
	}
	env.Stdout.Write(data)
	return 0
}

func jobCancel(env *Env, args []string) int {
	set := newFlagSet(env, "job cancel")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	id, code, ok := oneID(env, "cancel", rest)
	if !ok {
		return code
	}

	// Deliberately without workers: cancelling is a marker in the job's own
	// directory, and whichever process owns the job notices it. Starting an
	// executor here would be this command offering to run things.
	service, _, err := openJobs(context.Background(), env, false, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	cancelled, err := service.Cancel(id)
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "asked job %s to stop (it was %s)\n", cancelled.ID, cancelled.State)
	return 0
}

func jobRetry(env *Env, args []string) int {
	set := newFlagSet(env, "job retry")
	wait := set.Bool("wait", true, "wait for the new job to finish")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	id, code, ok := oneID(env, "retry", rest)
	if !ok {
		return code
	}

	ctx, stop := signalContext()
	defer stop()
	service, _, err := openJobs(ctx, env, true, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	retried, err := service.Retry(id)
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stderr, "job %s queued, repeating %s\n", retried.ID, id)
	if !*wait {
		warnNotWaiting(env, retried.ID)
		return 0
	}
	finished, err := service.Wait(ctx, retried.ID)
	if err != nil {
		return fail(env, err)
	}
	printOutcome(env, finished)
	if finished.Succeeded() {
		return 0
	}
	return 1
}

func jobArtifacts(env *Env, args []string) int {
	set := newFlagSet(env, "job artifacts")
	asJSON := set.Bool("json", false, "print the artifacts as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	id, code, ok := oneID(env, "artifacts", rest)
	if !ok {
		return code
	}

	service, _, err := openJobs(context.Background(), env, false, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	found, err := service.Get(id)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, found.Artifacts)
	}
	if len(found.Artifacts) == 0 {
		fmt.Fprintln(env.Stdout, "no artifacts")
		return 0
	}
	for _, artifact := range found.Artifacts {
		if artifact.Missing {
			state := "missing"
			if artifact.Optional {
				state = "not produced (optional)"
			}
			fmt.Fprintf(env.Stdout, "%-16s  %s\n", artifact.Name, state)
			continue
		}
		fmt.Fprintf(env.Stdout, "%-16s  %8d  %s  %s\n", artifact.Name, artifact.Size, artifact.SHA256, artifact.Path)
	}
	return 0
}

func jobProfiles(env *Env, args []string) int {
	set := newFlagSet(env, "job profiles")
	asJSON := set.Bool("json", false, "print the catalog as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	service, _, err := openJobs(context.Background(), env, false, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	entries, err := service.Catalog().List()
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		summaries := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			meta := entry.Profile.Metadata()
			actions := make([]string, 0)
			for _, action := range entry.Profile.ActionList() {
				actions = append(actions, action.ID)
			}
			summaries = append(summaries, map[string]any{
				"id": meta.ID, "kind": meta.Kind, "version": meta.Version, "name": meta.Name,
				"trust": entry.Trust, "digest": entry.Digest, "source": entry.Source, "actions": actions,
			})
		}
		return printJSON(env, summaries)
	}
	for _, entry := range entries {
		meta := entry.Profile.Metadata()
		actions := make([]string, 0)
		for _, action := range entry.Profile.ActionList() {
			actions = append(actions, action.ID)
		}
		fmt.Fprintf(env.Stdout, "%-40s %-7s %-9s %s\n", meta.ID, meta.Kind, entry.Trust, strings.Join(actions, ", "))
	}
	return 0
}

// jobLimits prints the bounded-capture envelope this build holds itself to.
//
// It opens no store and reads no job: the answer is a property of the program,
// which is exactly why it is worth being able to ask for. A harness measuring
// resident memory under a flood needs a bound that comes FROM the product — a
// threshold the harness picked is a threshold that passes whatever the product
// happens to do, and `AUCOM 219` left the bounded-log row unmeasured for want
// of one.
func jobLimits(env *Env, args []string) int {
	set := newFlagSet(env, "job limits")
	asJSON := set.Bool("json", false, "print the limits as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	limits := job.RetentionLimits()
	if *asJSON {
		return printJSON(env, limits)
	}
	fmt.Fprintf(env.Stdout, "kept per stream       %d bytes (head %d + tail %d)\n",
		limits.KeptBytesPerStream, limits.HeadBytes, limits.TailBytes)
	fmt.Fprintf(env.Stdout, "raw log file at most  %d bytes\n", limits.MaxLogFileBytes)
	fmt.Fprintf(env.Stdout, "read buffer           %d bytes, fixed\n", limits.ReadChunkBytes)
	fmt.Fprintf(env.Stdout, "longest line held     %d bytes\n", limits.MaxLineBytes)
	fmt.Fprintf(env.Stdout, "diagnostics kept      %d\n", limits.MaxDiagnostics)
	fmt.Fprintf(env.Stdout, "resident per stream   %d bytes while the program is still writing\n",
		limits.ResidentBytesPerStream)
	fmt.Fprintf(env.Stdout, "resident per job      %d bytes across %d streams\n",
		limits.ResidentBytesPerJob, limits.StreamsPerJob)
	fmt.Fprint(env.Stdout, "\nNone of these grows with how much a tool writes.\n")
	return 0
}

// oneID takes exactly one job id from the positionals.
func oneID(env *Env, verb string, rest []string) (string, int, bool) {
	switch len(rest) {
	case 1:
		return rest[0], 0, true
	case 0:
		fmt.Fprintf(env.Stderr, "error: job %s requires a job id\n", verb)
	default:
		fmt.Fprintf(env.Stderr, "error: job %s takes one job id, got %d\n", verb, len(rest))
	}
	return "", 2, false
}

func printJSON(env *Env, value any) int {
	encoder := json.NewEncoder(env.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fail(env, err)
	}
	return 0
}

func printPreview(env *Env, previewed *job.Job) {
	fmt.Fprintf(env.Stdout, "profile:  %s %s (%s)\n", previewed.ProfileID, previewed.ProfileVersion, previewed.Trust)
	fmt.Fprintf(env.Stdout, "action:   %s%s\n", previewed.ActionID, sessionSuffix(previewed))
	fmt.Fprintf(env.Stdout, "digest:   %s\n", previewed.ProfileDigest)
	fmt.Fprintf(env.Stdout, "workdir:  %s\n", previewed.Command.WorkingDir)
	fmt.Fprintf(env.Stdout, "command:  %s\n", previewed.Command.Shell)
	// One argument per line as well as the joined form: the joined one is
	// readable, and this one is the truth about where each element begins and
	// ends, which is the whole point of an argument array.
	fmt.Fprintln(env.Stdout, "argv:")
	for i, arg := range append([]string{previewed.Command.Executable}, previewed.Command.Args...) {
		fmt.Fprintf(env.Stdout, "  [%d] %s\n", i, arg)
	}
	if len(previewed.Command.Env) > 0 {
		fmt.Fprintln(env.Stdout, "environment:")
		for _, entry := range previewed.Command.Env {
			if entry.Source == job.EnvFromHost {
				fmt.Fprintf(env.Stdout, "  %s (inherited by name; its value is not recorded)\n", entry.Name)
				continue
			}
			fmt.Fprintf(env.Stdout, "  %s=%s\n", entry.Name, entry.Value)
		}
	}
}

// sessionSuffix labels a job that is a game session rather than a build step.
//
// It is appended everywhere a job is named, and it says "listen server" rather
// than "listen_server" for one reason: whether this machine is currently
// reachable by other people is not a detail a user should have to look up a
// vocabulary for.
func sessionSuffix(candidate *job.Job) string {
	switch candidate.SessionRole {
	case profile.SessionClient:
		return " (client)"
	case profile.SessionListen:
		return " (listen server — other people can join this machine)"
	case profile.SessionDedicated:
		return " (dedicated server — other people can join this machine)"
	}
	return ""
}

func printOutcome(env *Env, finished *job.Job) {
	fmt.Fprintf(env.Stdout, "job %s: %s — %s\n", finished.ID, finished.State, finished.State.Describe())
	fmt.Fprintf(env.Stdout, "  %s %s%s, %s\n", finished.ProfileID, finished.ActionID, sessionSuffix(finished), finished.Duration().Round(time.Millisecond))
	if finished.ExitCode != nil {
		fmt.Fprintf(env.Stdout, "  exit status %d\n", *finished.ExitCode)
	}
	if finished.Error != "" {
		fmt.Fprintf(env.Stdout, "  %s\n", finished.Error)
	}
	for _, diagnostic := range finished.Diagnostics {
		fmt.Fprintf(env.Stdout, "  [%s] %s\n", diagnostic.Severity, diagnostic.Message)
		if diagnostic.Hint != "" {
			fmt.Fprintf(env.Stdout, "         %s\n", diagnostic.Hint)
		}
	}
	for _, artifact := range finished.Artifacts {
		if artifact.Missing {
			continue
		}
		fmt.Fprintf(env.Stdout, "  artifact %s: %s\n", artifact.Name, artifact.Path)
	}
	if finished.Stdout.Truncated || finished.Stderr.Truncated {
		fmt.Fprintf(env.Stdout, "  output was truncated: %d bytes on stdout and %d on stderr were not kept\n",
			finished.Stdout.Dropped, finished.Stderr.Dropped)
	}
}
