package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// `companion build` — run a pipeline, and read what a build did afterwards.
//
// `job run` runs one action. `build run` runs a pipeline: several actions in
// order, with the files wired between them and a manifest written down. Every
// process it starts is still a job, visible to `companion job show`, with its
// own logs and its own record — the build adds ordering, wiring and evidence,
// and nothing else.

const buildUsage = `usage:
  companion build run     --pipeline <id> [--input n=path]... [--option step.name=v]... [--strict] [--json]
  companion build preview --pipeline <id> [same flags]     resolve every stage, start nothing
  companion build list    [--limit <n>] [--json]
  companion build show    <build-id> [--json]
  companion build pipelines [--json]                       what can be built on this machine
`

func runBuild(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, buildUsage)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "run":
		return buildRun(env, rest, false)
	case "preview":
		return buildRun(env, rest, true)
	case "list":
		return buildList(env, rest)
	case "show":
		return buildShow(env, rest)
	case "pipelines":
		return buildPipelines(env, rest)
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, buildUsage)
		return 0
	}
	fmt.Fprintf(env.Stderr, "error: unknown build subcommand %q\n\n", args[0])
	fmt.Fprint(env.Stderr, buildUsage)
	return 2
}

// stepOptions collects `--option <step>.<name>=<value>`.
//
// Qualified by step because a pipeline has several, and an unqualified name
// would be this command guessing which stage the user meant. The pipeline's own
// options are the defaults; these override one of them.
type stepOptions map[string]map[string]string

func (s stepOptions) String() string {
	var parts []string
	for step, options := range s {
		for name, value := range options {
			parts = append(parts, step+"."+name+"="+value)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func (s stepOptions) Set(raw string) error {
	qualified, value, found := strings.Cut(raw, "=")
	if !found || strings.TrimSpace(qualified) == "" {
		return fmt.Errorf("%q is not `step.option=value`", raw)
	}
	step, name, qualifiedOK := strings.Cut(qualified, ".")
	if !qualifiedOK || step == "" || name == "" {
		return fmt.Errorf("%q does not say which step: write `step.option=value`", raw)
	}
	if s[step] == nil {
		s[step] = map[string]string{}
	}
	if _, repeated := s[step][name]; repeated {
		return fmt.Errorf("%q was given twice", qualified)
	}
	s[step][name] = value
	return nil
}

func (s stepOptions) orNil() map[string]map[string]string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// buildsDir is where this machine's builds live: beside the jobs, because they
// are the same kind of thing — a record of work done, kept until somebody
// clears it.
func buildsDir(env *Env, settings config.Config) (string, error) {
	jobsDir, _, _, err := statePaths(env, settings)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(jobsDir), "builds"), nil
}

func openRunner(env *Env, service *job.Service, settings config.Config) (*build.Runner, error) {
	dir, err := buildsDir(env, settings)
	if err != nil {
		return nil, err
	}
	_, _, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return nil, err
	}
	return build.New(build.Options{
		Service:   service,
		Dir:       dir,
		Bindings:  binding.Lookup(bindingsPath),
		Companion: env.Version,
	})
}

func buildRun(env *Env, args []string, previewOnly bool) int {
	name := "build run"
	if previewOnly {
		name = "build preview"
	}
	set := newFlagSet(env, name)
	pipeline := set.String("pipeline", "", "pipeline profile id to build")
	label := set.String("label", "", "short name for this build in the list")
	inputs := pairs{}
	options := stepOptions{}
	set.Var(inputs, "input",
		"a pipeline input, as name=path or name=aub:<type>/<asset_id>[@<revision>][#<file>] (repeatable)")
	set.Var(options, "option", "override a step's option, as step.name=value (repeatable)")
	strict := set.Bool("strict", false, "fail the build when any stage reports an error-severity diagnostic")
	quiet := set.Bool("quiet", false, "do not mirror the tools' output to the terminal")
	asJSON := set.Bool("json", false, "print the build manifest as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *pipeline == "" {
		fmt.Fprintf(env.Stderr, "error: %s requires --pipeline\n", name)
		return 2
	}

	ctx, stop := signalContext()
	defer stop()

	service, settings, err := openJobs(ctx, env, !previewOnly, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()
	runner, err := openRunner(env, service, settings)
	if err != nil {
		return fail(env, err)
	}

	// An `aub:` input is resolved BEFORE the build starts: `@current` becomes the
	// version it resolved to, the bytes are verified against their recorded
	// digest, and what the manifest records is that exact revision. A build
	// retried tomorrow reads the same revision out of the cache rather than
	// whatever is current then.
	// The staging directory is temporary and is removed when this command ends:
	// the runner COPIES every input into the build directory (because `vis` and
	// `light` rewrite the file they are handed), so what is left here after that
	// is a second copy nobody reads. The verified originals stay in the asset
	// cache, which is where a retry finds them.
	stage, err := os.MkdirTemp("", "aucom-aub-input-")
	if err != nil {
		return fail(env, err)
	}
	defer os.RemoveAll(stage)

	resolvedInputs, sources, err := resolveAUBInputs(ctx, env, inputs.orNil(), stage)
	if err != nil {
		return fail(env, err)
	}

	request := build.Request{
		PipelineID: *pipeline,
		Inputs:     resolvedInputs,
		Sources:    sources,
		Options:    options.orNil(),
		Label:      *label,
		Strict:     *strict,
	}
	if !previewOnly && !*quiet {
		request.Mirror = env.Stdout
	}

	if previewOnly {
		manifest, err := runner.Preview(request)
		if err != nil {
			return fail(env, err)
		}
		if *asJSON {
			return printJSON(env, manifest)
		}
		printBuildPreview(env, manifest)
		for _, step := range manifest.Steps {
			if step.Error != "" {
				fmt.Fprintf(env.Stderr, "\nthis would not run yet: the %s step: %s\n", step.ID, step.Error)
				return 1
			}
		}
		return 0
	}

	manifest, runErr := runner.Run(ctx, request)
	if manifest == nil {
		return fail(env, runErr)
	}
	if *asJSON {
		if code := printJSON(env, manifest); code != 0 {
			return code
		}
	} else {
		printBuildOutcome(env, manifest)
	}
	if runErr != nil || !manifest.Succeeded() {
		return 1
	}
	return 0
}

func buildList(env *Env, args []string) int {
	set := newFlagSet(env, "build list")
	limit := set.Int("limit", 20, "how many builds to show")
	asJSON := set.Bool("json", false, "print the manifests as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	dir, err := buildsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	manifests, err := build.List(dir)
	if err != nil {
		return fail(env, err)
	}
	if *limit > 0 && len(manifests) > *limit {
		manifests = manifests[:*limit]
	}
	if *asJSON {
		return printJSON(env, manifests)
	}
	if len(manifests) == 0 {
		fmt.Fprintf(env.Stdout, "no builds yet. `companion build pipelines` lists what can be built.\n")
		return 0
	}
	for _, m := range manifests {
		fmt.Fprintf(env.Stdout, "%-26s %-10s %-32s %6dms  %s\n",
			m.BuildID, m.State, m.Pipeline.ID, m.DurationMS, m.Label)
	}
	return 0
}

func buildShow(env *Env, args []string) int {
	set := newFlagSet(env, "build show")
	asJSON := set.Bool("json", false, "print the manifest as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, "error: build show takes exactly one build id\n")
		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	dir, err := buildsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	manifest, err := build.Find(dir, rest[0])
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, manifest)
	}
	printBuildOutcome(env, manifest)
	return 0
}

func buildPipelines(env *Env, args []string) int {
	set := newFlagSet(env, "build pipelines")
	asJSON := set.Bool("json", false, "print the list as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	ctx, stop := signalContext()
	defer stop()
	service, settings, err := openJobs(ctx, env, false, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()
	runner, err := openRunner(env, service, settings)
	if err != nil {
		return fail(env, err)
	}
	entries, err := service.Catalog().List()
	if err != nil {
		return fail(env, err)
	}

	type row struct {
		ID           string   `json:"id"`
		Version      string   `json:"version"`
		Name         string   `json:"name"`
		Summary      string   `json:"summary"`
		Trust        string   `json:"trust"`
		Steps        []string `json:"steps"`
		Missing      []string `json:"missing_capabilities,omitempty"`
		Runnable     bool     `json:"runnable"`
		Capabilities []string `json:"-"`
	}
	var rows []row
	for _, entry := range entries {
		pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
		if !isPipeline {
			continue
		}
		meta := entry.Profile.Metadata()
		r := row{ID: meta.ID, Version: meta.Version, Name: meta.Name, Summary: meta.Summary, Trust: string(entry.Trust), Runnable: true}
		for _, step := range pipeline.Steps {
			r.Steps = append(r.Steps, step.ID)
			if _, _, provided := runner.Resolver().Provider(step.Capability); !provided {
				r.Missing = append(r.Missing, step.Capability)
				r.Runnable = false
			}
		}
		rows = append(rows, r)
	}
	if *asJSON {
		return printJSON(env, rows)
	}
	if len(rows) == 0 {
		fmt.Fprint(env.Stdout, "no pipeline profiles are installed.\n")
		return 0
	}
	for _, r := range rows {
		status := "ready"
		if !r.Runnable {
			status = "needs " + strings.Join(r.Missing, ", ")
		}
		fmt.Fprintf(env.Stdout, "%-32s %-8s %-9s %-24s %s\n", r.ID, r.Version, r.Trust, strings.Join(r.Steps, " -> "), status)
	}
	return 0
}

func printBuildPreview(env *Env, m *build.Manifest) {
	fmt.Fprintf(env.Stdout, "pipeline  %s %s (%s)\n", m.Pipeline.ID, m.Pipeline.Version, m.Pipeline.Trust)
	fmt.Fprintf(env.Stdout, "          %s\n", m.Pipeline.Digest)
	for _, tool := range m.Tools {
		fmt.Fprintf(env.Stdout, "tool      %s %s — %s %s\n", tool.Profile.ID, tool.Profile.Version, tool.Profile.Name, tool.ToolVersion)
	}
	for _, step := range m.Steps {
		fmt.Fprintf(env.Stdout, "\nstep %s — %s\n", step.ID, step.Title)
		fmt.Fprintf(env.Stdout, "  capability %s -> %s/%s\n", step.Capability, step.Profile.ID, step.ActionID)
		if step.Command != nil {
			fmt.Fprintf(env.Stdout, "  in %s\n  %s\n", step.Command.WorkingDir, step.Command.Shell)
		}
		if step.PreviewDifference != "" {
			fmt.Fprintf(env.Stdout, "  (%s)\n", step.PreviewDifference)
		}
		if step.Error != "" {
			fmt.Fprintf(env.Stdout, "  error: %s\n", step.Error)
		}
	}
}

func printBuildOutcome(env *Env, m *build.Manifest) {
	fmt.Fprintf(env.Stdout, "\nbuild %s — %s\n", m.BuildID, m.State)
	fmt.Fprintf(env.Stdout, "  pipeline  %s %s (%s)\n", m.Pipeline.ID, m.Pipeline.Version, m.Pipeline.Trust)
	if m.Error != "" {
		fmt.Fprintf(env.Stdout, "  error     %s\n", m.Error)
	}
	for _, tool := range m.Tools {
		fmt.Fprintf(env.Stdout, "  tool      %s %s via %s\n", tool.Profile.Name, tool.ToolVersion, orNone(string(tool.Acquisition)))
		for _, install := range tool.Installs {
			fmt.Fprintf(env.Stdout, "            pinned %s %s %s\n", install.PackageID, install.Version, install.Digest)
		}
		for _, exe := range tool.Executables {
			fmt.Fprintf(env.Stdout, "            %-9s %s\n", exe.Name, orNone(exe.SHA256))
		}
	}
	for _, input := range m.Inputs {
		fmt.Fprintf(env.Stdout, "  input     %-12s %s\n", input.Name, input.SHA256)
	}
	for _, step := range m.Steps {
		state := string(step.State)
		if step.Skipped {
			state = "skipped"
		}
		fmt.Fprintf(env.Stdout, "\n  step %-8s %-10s %6dms  job %s\n", step.ID, state, step.DurationMS, step.JobID)
		if step.Command != nil {
			fmt.Fprintf(env.Stdout, "    %s\n", step.Command.Shell)
		}
		if step.Command != nil {
			fmt.Fprintf(env.Stdout, "    preview matched: %t\n", step.PreviewMatched)
		}
		if step.Error != "" {
			fmt.Fprintf(env.Stdout, "    error: %s\n", step.Error)
		}
		for _, d := range step.Diagnostics {
			fmt.Fprintf(env.Stdout, "    %-8s %s\n", d.Severity, firstNonEmpty(d.Message, d.Raw))
			if d.Hint != "" {
				fmt.Fprintf(env.Stdout, "             %s\n", d.Hint)
			}
		}
		for _, out := range step.Outputs {
			if out.Missing {
				if !out.Optional {
					fmt.Fprintf(env.Stdout, "    %-10s not produced\n", out.Name)
				}
				continue
			}
			fmt.Fprintf(env.Stdout, "    %-10s %8d  %s\n", out.Name, out.Size, out.SHA256)
		}
	}
	if len(m.Outputs) > 0 {
		fmt.Fprint(env.Stdout, "\n  published\n")
		for _, out := range m.Outputs {
			if out.Missing {
				fmt.Fprintf(env.Stdout, "    %-12s not produced%s\n", out.Name, optionalNote(out.Optional))
				continue
			}
			fmt.Fprintf(env.Stdout, "    %-12s %s\n", out.Name, out.Path)
		}
	}
	if m.ReproducibleKey != "" {
		fmt.Fprintf(env.Stdout, "\n  recipe key       %s\n", m.ReproducibleKey)
	}
	if m.Directory != "" {
		fmt.Fprintf(env.Stdout, "  manifest         %s\n", filepath.Join(m.Directory, build.ManifestFileName))
	}
}

func optionalNote(optional bool) string {
	if optional {
		return " (optional)"
	}
	return ""
}

func orNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
