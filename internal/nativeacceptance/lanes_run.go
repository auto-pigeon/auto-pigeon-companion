package nativeacceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// --- compile ----------------------------------------------------------------

func (r *run) laneCompile(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	if !r.compilersBound {
		lane.State = NotAvailable
		lane.Reason = "no EricW build is bound on this machine, so there is nothing to compile with. " +
			"Name one with --tool-path."
		return lane
	}

	project := filepath.Join(r.work, "project")
	sourceMap := filepath.Join(project, FixtureMapName)
	wadPath := filepath.Join(project, FixtureWADName)
	wad, err := FixtureWAD()
	if err != nil {
		lane.State = Fail
		lane.Reason = r.redact.Line(err.Error())
		return lane
	}
	if err := os.WriteFile(sourceMap, []byte(FixtureMap()), 0o644); err != nil {
		lane.State = Fail
		lane.Reason = r.redact.Line(err.Error())
		return lane
	}
	if err := os.WriteFile(wadPath, wad, 0o644); err != nil {
		lane.State = Fail
		lane.Reason = r.redact.Line(err.Error())
		return lane
	}
	add(pass("the source map and its texture are written by this program",
		fmt.Sprintf("a sealed room and one synthetic WAD2 texture, %d bytes of map and %d of wad; "+
			"no id Software asset is used", len(FixtureMap()), len(wad))))

	preview := r.exec(ctx, 120*time.Second, "build", "preview", "--pipeline", Q1PipelineID,
		"--input", "source_map="+sourceMap, "--input", "wad="+wadPath)
	missing := []string{}
	for _, stage := range []string{"compile", "vis", "light"} {
		if !strings.Contains(preview.stdout, "step "+stage) {
			missing = append(missing, stage)
		}
	}
	add(verdict(preview.code == 0 && len(missing) == 0,
		"the pipeline resolves compile, VIS and LIGHT before anything runs",
		"three steps",
		"missing: "+strings.Join(missing, ", ")))

	build := r.exec(ctx, 30*time.Minute, "build", "run", "--quiet", "--json",
		"--pipeline", Q1PipelineID,
		"--input", "source_map="+sourceMap, "--input", "wad="+wadPath)
	var manifest struct {
		BuildID string `json:"build_id"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal([]byte(build.stdout), &manifest); err != nil || manifest.State != "succeeded" {
		add(failed("a real Quake 1 compile, VIS and LIGHT succeed",
			fmt.Sprintf("exit %d, state %q: %s", build.code, manifest.State,
				r.redact.Line(build.output()))))
		return lane
	}
	add(pass("a real Quake 1 compile, VIS and LIGHT succeed", "build "+manifest.BuildID))

	first := filepath.Join(r.work, "first.pak")
	second := filepath.Join(r.work, "second.pak")
	one := r.exec(ctx, 300*time.Second, "package", "create", "--target", "quake-pak",
		"--build", manifest.BuildID, "--out", first)
	two := r.exec(ctx, 300*time.Second, "package", "create", "--target", "quake-pak",
		"--build", manifest.BuildID, "--out", second)
	if one.code != 0 || two.code != 0 {
		add(failed("a PAK is produced from the build", r.redact.Line(one.output()+two.output())))
		return lane
	}
	firstDigest, firstErr := fileDigest(first)
	secondDigest, secondErr := fileDigest(second)
	add(verdict(firstErr == nil && secondErr == nil && firstDigest == secondDigest,
		"the PAK writer produces the same bytes twice",
		"sha256 "+shortDigest(firstDigest),
		"the two archives differ"))

	verify := r.exec(ctx, 300*time.Second, "package", "verify", first)
	add(verdict(verify.code == 0,
		"every member of the archive reads back",
		r.redact.Line(firstLine(verify.stdout)),
		r.redact.Line(verify.output())))
	return lane
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// --- engine -----------------------------------------------------------------

// defaultEngineProfile is the fallback profile per family: the one that sends
// only what id Software's own engine documented, so a preview is about the
// command line rather than about one port's extensions.
var defaultEngineProfile = map[string]string{
	"quake1": "auto-pigeon.engine.q1-generic",
	"quake2": "auto-pigeon.engine.q2-generic",
	"quake3": "auto-pigeon.engine.q3-generic",
}

func (r *run) laneEngine(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	family := r.options.GameFamily
	if family == "" {
		family = "quake1"
	}
	profileID := r.options.EngineProfile
	if profileID == "" {
		profileID = defaultEngineProfile[family]
	}
	if profileID == "" {
		lane.State = NotAvailable
		lane.Reason = "no engine profile is known for the family " + family
		return lane
	}
	action := r.options.EngineAction
	if action == "" {
		action = "play_map"
	}

	list := r.exec(ctx, 60*time.Second, "engine", "list")
	add(verdict(list.code == 0 && strings.Contains(list.stdout, profileID),
		"this build ships engine profiles and says what each claims here",
		"including "+profileID,
		r.redact.Line(list.output())))

	// `engine check` exits non-zero when something would stop the action, which
	// is what it is for. What is asserted is that the refusal is ACTIONABLE:
	// it names the command that would fix it. Requiring exit 0 here would be
	// asserting that a machine with no engine on it is ready to run one.
	unbound := r.exec(ctx, 60*time.Second, "engine", "check", profileID, "--action", action)
	add(verdict(strings.Contains(unbound.output(), "companion engine bind"),
		"with nothing recorded, the preflight names what is missing and what would set it",
		"it named `companion engine bind`",
		r.redact.Line(unbound.output())))

	// The engine and the game root. Either the operator's own, or a placeholder
	// this run made — and the difference is recorded, because a preview against
	// a placeholder proves the argv and nothing about anybody's installation.
	enginePath := r.options.EngineRoot
	gameRoot := r.options.GameRoot
	supplied := enginePath != "" && gameRoot != ""
	if enginePath == "" {
		placeholder := filepath.Join(r.work, "placeholder-engine")
		if err := writePlaceholder(placeholder); err != nil {
			add(failed("an engine and a game root are recorded so a command can be composed",
				r.redact.Line(err.Error())))
			return lane
		}
		enginePath = placeholder
		r.redact.Root(placeholder, "engine")
	}
	if gameRoot == "" {
		gameRoot = filepath.Join(r.work, "game-root")
		if err := os.MkdirAll(filepath.Join(gameRoot, "id1"), 0o755); err != nil {
			add(failed("an engine and a game root are recorded so a command can be composed",
				r.redact.Line(err.Error())))
			return lane
		}
		r.redact.Root(gameRoot, "game_root")
	}
	content := filepath.Join(r.work, "project")

	bind := r.exec(ctx, 120*time.Second, "engine", "bind", profileID,
		"--engine", enginePath, "--game-root", gameRoot, "--content-root", content, "--approve")
	if bind.code != 0 {
		add(failed("an engine and a game root are recorded so a command can be composed",
			r.redact.Line(bind.output())))
		return lane
	}
	add(pass("an engine and a game root are recorded so a command can be composed",
		"recorded from paths named on the command line; nothing was detected and written down"))

	started := r.now()
	preview := r.exec(ctx, 120*time.Second, "engine", "preview", profileID,
		"--action", action, "--map", "start", "--mod", "id1")
	shape := r.commandShape(preview.stdout)
	row := GameRow{
		Row: "preview", Family: family, ProfileID: profileID,
		ProfileVersion: r.profileVersion(ctx, profileID),
		Platform:       Target(), CommandShape: shape,
		ElapsedMS: r.now().Sub(started).Milliseconds(),
	}
	if preview.code == 0 && len(shape) > 0 {
		row.State, row.Signal = Pass, "printed"
		row.Detail = "the command was composed and nothing was started"
		if !supplied {
			row.Detail += "; against a placeholder program this run created"
		}
		add(pass("the engine command is previewed without any game data",
			fmt.Sprintf("%d arguments, nothing started", len(shape))))
	} else {
		row.State, row.Signal = Fail, "not_printed"
		row.Detail = r.redact.Line(preview.output())
		add(failed("the engine command is previewed without any game data", row.Detail))
	}
	r.game = append(r.game, row)

	r.game = append(r.game, r.launchRow(ctx, family, profileID, action, supplied))
	if !supplied {
		add(inapplicable("an owned installation actually starts and reaches a ready signal",
			"no engine and game root were supplied, so nothing was started. "+
				"This row stays manual_pending in the matrix until an operator supplies both."))
	} else {
		last := r.game[len(r.game)-1]
		add(Observation{
			Claim:  "an owned installation actually starts and reaches a ready signal",
			State:  last.State,
			Detail: last.Signal + ": " + last.Detail,
		})
	}
	return lane
}

// launchRow is the optional owned-game row.
//
// `AUT/AUCOM 231`: no game byte is copied, archived, hashed or uploaded, and
// nothing about the installation is retained. What is recorded is the family,
// the profile and its version, this platform, the command SHAPE with paths
// replaced, the signal, and how long it took.
func (r *run) launchRow(ctx context.Context, family, profileID, action string, supplied bool) GameRow {
	row := GameRow{
		Row: "launch", Family: family, ProfileID: profileID,
		ProfileVersion: r.profileVersion(ctx, profileID),
		Platform:       Target(),
	}
	if !supplied {
		row.State, row.Signal = NotAvailable, "not_run"
		row.Detail = "no owned engine and game root were supplied"
		row.CommandShape = []string{}
		return row
	}
	deadline := r.options.GameDeadline
	if deadline <= 0 {
		deadline = 25 * time.Second
	}
	started := r.now()
	// A real engine opens a window and waits for a person, so "it was still
	// running when the deadline came" IS the ready signal. An engine that
	// exits before it is asked to has not started a game.
	res := r.exec(ctx, deadline, "engine", "run", profileID,
		"--action", action, "--map", "start", "--mod", "id1")
	row.ElapsedMS = r.now().Sub(started).Milliseconds()
	row.CommandShape = r.commandShape(res.output())
	switch {
	case res.timedOut:
		row.State, row.Signal = Pass, "ready:still_running_at_deadline"
		row.Detail = fmt.Sprintf("it was still running after %s and was stopped", deadline)
	case res.code == 0:
		row.State, row.Signal = Pass, "exited:0"
		row.Detail = "it started and exited cleanly before the deadline"
	default:
		row.State, row.Signal = Fail, "exited:"+strconv.Itoa(res.code)
		row.Detail = r.redact.Line(res.output())
	}
	return row
}

// commandShape reads the argv out of a preview and replaces every path with the
// role it plays. It is the only thing in this package that carries a command
// line into a document, and it carries a SHAPE: the program is `<engine>`, the
// roots are `<game_root>` and `<work>`, and anything else that still looks like
// a path becomes `<path>`.
// The slice is never nil, so the member is always a list in the document. A
// `null` where an array is declared is a shape a consumer has to have an
// opinion about, and the first merge of a real bundle refused one for it.
func (r *run) commandShape(output string) []string {
	shape := []string{}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[") {
			continue
		}
		closing := strings.Index(trimmed, "]")
		if closing < 0 {
			continue
		}
		if _, err := strconv.Atoi(trimmed[1:closing]); err != nil {
			continue
		}
		shape = append(shape, r.redact.Text(strings.TrimSpace(trimmed[closing+1:])))
	}
	return shape
}

func (r *run) profileVersion(ctx context.Context, id string) string {
	res := r.exec(ctx, 60*time.Second, "engine", "show", id)
	for _, line := range strings.Split(res.stdout, "\n") {
		if strings.Contains(line, "("+id+")") {
			fields := strings.Fields(line)
			for index, field := range fields {
				if strings.HasPrefix(field, "("+id) && index > 0 {
					return fields[index-1]
				}
			}
		}
	}
	return "unknown"
}

func writePlaceholder(path string) error {
	// An empty program that is never started: `engine preview` composes a
	// command line and runs nothing, which is the whole claim being made.
	return os.WriteFile(path, []byte("this file is never executed\n"), 0o755)
}

// --- jobs -------------------------------------------------------------------

func (r *run) laneJobs(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	if r.toolProfileID == "" {
		lane.State = NotAvailable
		lane.Reason = "the profile lane did not run, so there is no profile to run a job from"
		return lane
	}
	review, ok := r.review(ctx)
	if !ok {
		lane.State = NotAvailable
		lane.Reason = "the acceptance profile could not be reviewed"
		return lane
	}
	grant := r.exec(ctx, 60*time.Second, "profile", "grant", ToolProfileID,
		"--digest="+review.Digest, "--approve")
	if grant.code != 0 {
		lane.State = NotAvailable
		lane.Reason = "the acceptance profile could not be approved again: " + r.redact.Line(grant.output())
		return lane
	}
	add(pass("an approval withdrawn in the lane above can be given again",
		"the same document, approved by its own digest"))

	// Cancellation. `job run` waits for the job, so the stop has to come from a
	// second invocation — which is the arrangement the product documents: the
	// stop is a marker in the job's own directory that whichever process owns
	// it notices.
	add(r.checkCancel(ctx))

	// Retry.
	quick := r.exec(ctx, 120*time.Second, "job", "run", "--profile", ToolProfileID,
		"--action", "noise", "--option", "lines=5")
	quickID := queuedJobID(quick)
	if quick.code != 0 || quickID == "" {
		add(failed("a small job succeeds", r.redact.Line(quick.output())))
		return lane
	}
	retry := r.exec(ctx, 120*time.Second, "job", "retry", quickID, "--wait")
	retriedID := queuedJobID(retry)
	add(verdict(retry.code == 0 && retriedID != "" && retriedID != quickID,
		"a finished job can be retried, and the retry is a record of its own",
		"a second job id, so the record of the first attempt survives",
		fmt.Sprintf("exit %d, retry id %q", retry.code, retriedID)))

	// The bounded resource smoke: the log bound this build publishes, enforced
	// on a flood rather than graphed. `AUCOM/AUT 229`'s rule, on the operator's
	// own machine.
	for _, observation := range r.checkLogBound(ctx) {
		add(observation)
	}
	return lane
}

func (r *run) checkCancel(ctx context.Context) Observation {
	command := exec.Command(r.executable, "job", "run", "--profile", ToolProfileID,
		"--action", "wait", "--option", "seconds=60")
	command.Env = r.env
	command.Dir = r.work
	if err := command.Start(); err != nil {
		return failed("a running job is cancelled, and the record says it was cancelled",
			r.redact.Line(err.Error()))
	}
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()

	jobID := ""
	for attempt := 0; attempt < 60 && jobID == ""; attempt++ {
		time.Sleep(250 * time.Millisecond)
		jobID = r.latestJob(ctx, "running")
	}
	if jobID == "" {
		return failed("a running job is cancelled, and the record says it was cancelled",
			"no job reached the running state within 15s")
	}
	cancel := r.exec(ctx, 60*time.Second, "job", "cancel", jobID)
	if cancel.code != 0 {
		return failed("a running job is cancelled, and the record says it was cancelled",
			r.redact.Line(cancel.output()))
	}
	state := ""
	for attempt := 0; attempt < 60; attempt++ {
		time.Sleep(250 * time.Millisecond)
		if record, ok := r.jobRecord(ctx, jobID); ok {
			state = record.State
			if state != "running" && state != "queued" {
				break
			}
		}
	}
	return verdict(state == "cancelled",
		"a running job is cancelled, and the record says it was cancelled",
		"state cancelled",
		"the record says "+state)
}

func (r *run) checkLogBound(ctx context.Context) []Observation {
	limits, ok := r.limits(ctx)
	if !ok {
		return []Observation{unavailable("a flood is bounded on disk and says what it dropped",
			"`job limits --json` could not be read, and this build's own numbers are the only bound worth measuring against")}
	}
	// Enough to overrun the per-stream bound many times over on both streams.
	lines := int(limits.KeptBytesPerStream/64) * 8
	if lines < 20000 {
		lines = 20000
	}
	res := r.exec(ctx, 20*time.Minute, "job", "run", "--profile", ToolProfileID,
		"--action", "noise", "--option", "lines="+strconv.Itoa(lines))
	jobID := queuedJobID(res)
	if res.code != 0 || jobID == "" {
		return []Observation{failed("a flood is bounded on disk and says what it dropped",
			r.redact.Line(res.output()))}
	}
	record, ok := r.jobRecord(ctx, jobID)
	if !ok {
		return []Observation{failed("a flood is bounded on disk and says what it dropped",
			"the job record could not be read back")}
	}
	jobsDir := r.jobsDir(ctx)
	observations := []Observation{
		verdict(record.Stdout.Truncated && record.Stderr.Truncated,
			"a flood is recorded as truncated on both streams",
			fmt.Sprintf("%d bytes written, %d stored", record.Stdout.Bytes+record.Stderr.Bytes,
				record.Stdout.Stored+record.Stderr.Stored),
			"the record says it was not truncated"),
		verdict(record.Stdout.Lines > 0,
			"the record counts the lines it saw",
			fmt.Sprintf("%d lines", record.Stdout.Lines),
			fmt.Sprintf("%d bytes recorded as 0 lines", record.Stdout.Bytes)),
	}
	for _, stream := range []struct {
		name string
		log  streamLog
	}{{"stdout", record.Stdout}, {"stderr", record.Stderr}} {
		if stream.log.File == "" || jobsDir == "" {
			observations = append(observations, unavailable(
				"the "+stream.name+" log is bounded on disk",
				"the record names no log file, or this run could not resolve the job directory"))
			continue
		}
		// `file` is the log's NAME inside the job's own directory, not a path,
		// and the directory is one the product names for itself.
		info, err := os.Stat(filepath.Join(jobsDir, jobID, stream.log.File))
		if err != nil {
			observations = append(observations, unavailable(
				"the "+stream.name+" log is bounded on disk", "the log file could not be measured"))
			continue
		}
		observations = append(observations, verdict(info.Size() <= limits.MaxLogFileBytes,
			"the "+stream.name+" log is bounded on disk",
			fmt.Sprintf("%d bytes against a %d-byte bound", info.Size(), limits.MaxLogFileBytes),
			fmt.Sprintf("%d bytes over a %d-byte bound", info.Size(), limits.MaxLogFileBytes)))
	}
	logs := r.exec(ctx, 120*time.Second, "job", "logs", jobID, "--stream", "stdout", "--raw")
	observations = append(observations, verdict(strings.Contains(logs.stdout, "were not kept"),
		"a truncated log says deterministically what it dropped",
		"the truncation marker is in the log",
		"no truncation marker in a truncated log"))
	return observations
}

// jobsDir is where job records and their raw logs live, as the product itself
// reports it. Composing it from the platform's cache convention would be this
// code holding a second opinion about a directory the program owns.
func (r *run) jobsDir(ctx context.Context) string {
	targets, err := r.uninstallTargets(ctx)
	if err != nil {
		return ""
	}
	for _, target := range targets {
		if target.Label == "build history" {
			return target.Path
		}
	}
	return ""
}

type streamLog struct {
	Bytes     int64  `json:"bytes"`
	Stored    int64  `json:"stored"`
	Dropped   int64  `json:"dropped"`
	Lines     int64  `json:"lines"`
	Truncated bool   `json:"truncated"`
	File      string `json:"file"`
}

type jobRecord struct {
	ID     string    `json:"id"`
	State  string    `json:"state"`
	Stdout streamLog `json:"stdout"`
	Stderr streamLog `json:"stderr"`
}

type jobLimits struct {
	KeptBytesPerStream int64 `json:"kept_bytes_per_stream"`
	MaxLogFileBytes    int64 `json:"max_log_file_bytes"`
}

func (r *run) limits(ctx context.Context) (jobLimits, bool) {
	res := r.exec(ctx, 60*time.Second, "job", "limits", "--json")
	if res.code != 0 {
		return jobLimits{}, false
	}
	var limits jobLimits
	if err := json.Unmarshal([]byte(res.stdout), &limits); err != nil {
		return jobLimits{}, false
	}
	return limits, limits.MaxLogFileBytes > 0
}

func (r *run) jobRecord(ctx context.Context, id string) (jobRecord, bool) {
	if id == "" {
		return jobRecord{}, false
	}
	res := r.exec(ctx, 60*time.Second, "job", "show", id, "--json")
	if res.code != 0 {
		return jobRecord{}, false
	}
	var record jobRecord
	if err := json.Unmarshal([]byte(res.stdout), &record); err != nil {
		return jobRecord{}, false
	}
	return record, true
}

// queuedJobID reads the id out of what `job run` and `job retry` announce.
//
// The announcement is the id's one unambiguous source. Reading it back off
// `job list` instead is how the first run of this lane came to assert the log
// bound against a five-line job: the listing's order is the store's business,
// two jobs can share a second, and "the newest record" is a guess where "the id
// this invocation printed" is a fact.
func queuedJobID(res result) string {
	for _, line := range strings.Split(res.stderr+res.stdout, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// `job retry` says "job <id> queued, repeating <id>", so the word is
		// read without whatever punctuation follows it.
		if len(fields) >= 3 && fields[0] == "job" && strings.TrimRight(fields[2], ",.") == "queued" {
			return fields[1]
		}
	}
	return ""
}

// latestJob is the newest job id, optionally in one state.
func (r *run) latestJob(ctx context.Context, state string) string {
	args := []string{"job", "list", "--json", "--limit", "5"}
	if state != "" {
		args = append(args, "--state", state)
	}
	res := r.exec(ctx, 60*time.Second, args...)
	if res.code != 0 {
		return ""
	}
	var records []jobRecord
	if err := json.Unmarshal([]byte(res.stdout), &records); err != nil {
		return ""
	}
	for _, record := range records {
		if record.ID != "" {
			return record.ID
		}
	}
	return ""
}

// --- purge ------------------------------------------------------------------

func (r *run) lanePurge(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	before, err := r.uninstallTargets(ctx)
	if err != nil {
		lane.State = NotAvailable
		lane.Reason = r.redact.Line(err.Error())
		return lane
	}
	present := countPresent(before)
	add(verdict(present > 0,
		"by now this run has put things on the machine, and the uninstaller lists them",
		fmt.Sprintf("%d of %d locations present", present, len(before)),
		"nothing was present to remove, so a purge would prove nothing"))

	purge := r.exec(ctx, 300*time.Second, "uninstall", "--purge", "--confirm")
	if purge.code != 0 {
		add(failed("uninstall --purge --confirm removes them", r.redact.Line(purge.output())))
		return lane
	}
	after, err := r.uninstallTargets(ctx)
	if err != nil {
		add(failed("uninstall --purge --confirm removes them", r.redact.Line(err.Error())))
		return lane
	}
	remaining := countPresent(after)
	add(verdict(remaining == 0,
		"uninstall --purge --confirm removes them",
		fmt.Sprintf("%d locations remain", remaining),
		fmt.Sprintf("%d of %d locations survived the purge", remaining, len(after))))

	// The program itself is not removed, and the product says so. A purge that
	// deleted the binary would be a purge nobody could report the result of.
	if _, err := os.Stat(r.executable); err == nil {
		add(pass("the program itself is not removed: whatever installed it owns it",
			"the executable is still where it was"))
	} else {
		add(failed("the program itself is not removed: whatever installed it owns it",
			"the executable is gone"))
	}
	return lane
}
