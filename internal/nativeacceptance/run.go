package nativeacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Options is everything an operator's invocation decides.
//
// Every path here is one the OPERATOR named. Nothing in this package looks for
// an engine, a game, a compiler or a backend, and there is no default for any
// of them — `auto-pigeon-tools/AGENTS.md` §4 forbids a program compiling in
// where another component lives, and a "sensible default" for a game root is a
// guess about somebody's disk.
type Options struct {
	// Executable is the Companion this run drives. Empty means this program.
	Executable string
	// Work is a directory this run owns and may fill. Empty means a temporary
	// one, removed at the end.
	Work string
	// ArtifactDir is the unpacked release directory, for the notices and the
	// checksum file. Empty means the directory the executable is in.
	ArtifactDir string
	// EntryPoint is how the operator started this: posix, powershell, direct.
	EntryPoint string
	// Shell is what the entry point reported about itself.
	Shell string
	// HostArch is the machine architecture the entry point read from the
	// operating system, so an emulated artifact can be told from a native one.
	HostArch string
	// ChecksumsVerified is what the entry point did before starting this
	// program, and ChecksumDetail is its one-line account of it.
	ChecksumsVerified State
	ChecksumDetail    string
	// ToolPath is a directory holding an EricW build the operator already has.
	ToolPath string
	// EngineRoot is the engine executable the operator supplied.
	EngineRoot string
	// GameRoot is the owned game installation the operator supplied.
	GameRoot string
	// GameFamily selects which engine profile family the optional lane uses.
	GameFamily string
	// EngineProfile and EngineAction override the profile and action.
	EngineProfile string
	EngineAction  string
	// GameDeadline bounds the optional launch. A real engine opens a window
	// and waits for a person, so "it was still running" is the ready signal.
	GameDeadline time.Duration
	// Only and Skip select lanes. Only wins when both name the same lane.
	Only []string
	Skip []string
	// Now is the clock, injectable for tests.
	Now func() time.Time
	// Progress receives one line per lane as it finishes. Nil discards.
	Progress func(string)
}

// LaneIDs is every lane, in the order a run performs them.
//
// The order is load-bearing at exactly one point: `purge` is last, because it
// deletes the configuration, the cache and the build history — everything the
// lanes above it produced. A purge in the middle would make every lane after it
// a test of a fresh machine again.
var LaneIDs = []string{
	"artifact", "first-start", "uri", "profile", "toolchain",
	"compile", "engine", "jobs", "purge",
}

var laneTitles = map[string]string{
	"artifact":    "the artifact says what it is, and this machine can vouch for it",
	"first-start": "a portable first start, a migration, and an uninstall that preserves",
	"uri":         "the autopigeon:// handler, where this package owns it",
	"profile":     "a tool profile somebody wrote: import, bind, review, grant, withdraw",
	"toolchain":   "an EricW build the operator names with --tool-path",
	"compile":     "a real Quake 1 compile, VIS, LIGHT and a deterministic PAK",
	"engine":      "the engine command, previewed without game data",
	"jobs":        "cancellation, retry, and the log bound this build publishes",
	"purge":       "uninstall --purge removes what it listed, and says so",
}

// Run performs the lanes and returns the finished bundle.
//
// It never returns a nil bundle for a lane that failed: a failure is evidence,
// and evidence that is thrown away because the run stopped early is the thing
// `AGENTS.md` §3d's ledger rule exists about.
func Run(ctx context.Context, options Options) (*Bundle, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	executable := options.Executable
	if executable == "" {
		resolved, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("this program cannot find its own path, so it cannot drive itself: %w", err)
		}
		executable = resolved
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return nil, err
	}
	executable = absolute

	work := options.Work
	if work == "" {
		created, err := os.MkdirTemp("", "aucom-native-acceptance-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(created)
		work = created
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}

	artifactDir := options.ArtifactDir
	if artifactDir == "" {
		artifactDir = filepath.Dir(executable)
	}

	// The whole run happens in a HOME this run made, and that is not a
	// convenience. The purge lane deletes configuration, granted profiles and
	// their bindings; an acceptance run that did that to the operator's own
	// machine would cost them the decisions they had made. An isolated home is
	// also what makes "portable first start" a thing this can observe at all.
	home := filepath.Join(work, "home")
	state := &run{
		executable:  executable,
		work:        work,
		artifactDir: artifactDir,
		home:        home,
		options:     options,
		now:         now,
		redact:      NewRedactor(),
	}
	if err := state.prepare(); err != nil {
		return nil, err
	}

	bundle := &Bundle{
		CompanionVersion: state.version(ctx),
		Kit: KitFacts{
			EntryPoint:        entryPointOrDirect(options.EntryPoint),
			ChecksumsVerified: checksumState(options.ChecksumsVerified),
			ChecksumDetail:    state.redact.Line(options.ChecksumDetail),
			Shell:             state.redact.Line(options.Shell),
		},
	}

	for _, id := range LaneIDs {
		if !state.selected(id) {
			bundle.Lanes = append(bundle.Lanes, Lane{
				ID: id, Title: laneTitles[id], State: Skipped,
				Reason:       "the operator did not select this lane",
				Observations: []Observation{},
			})
			continue
		}
		started := now()
		lane := state.lane(ctx, id)
		lane.ID = id
		lane.Title = laneTitles[id]
		lane.ElapsedMS = now().Sub(started).Milliseconds()
		if lane.Observations == nil {
			lane.Observations = []Observation{}
		}
		if lane.State == "" {
			lane.State = summarise(lane.Observations)
		}
		bundle.Lanes = append(bundle.Lanes, lane)
		if options.Progress != nil {
			options.Progress(fmt.Sprintf("%-12s %s", lane.State, id))
		}
	}
	bundle.Game = state.game
	bundle.Finish(now())
	return bundle, nil
}

// summarise is a lane's state when it did not set one: a fail anywhere fails
// the lane, otherwise a pass anywhere passes it, otherwise the weakest thing
// that happened. Nothing is promoted — a lane of `not_available` observations
// is `not_available`, not a pass.
func summarise(observations []Observation) State {
	if len(observations) == 0 {
		return NotAvailable
	}
	sawPass := false
	for _, observation := range observations {
		if observation.State == Fail {
			return Fail
		}
		if observation.State == Pass {
			sawPass = true
		}
	}
	if sawPass {
		return Pass
	}
	for _, candidate := range []State{NotAvailable, NotApplicable, Skipped} {
		for _, observation := range observations {
			if observation.State == candidate {
				return candidate
			}
		}
	}
	return NotAvailable
}

func entryPointOrDirect(value string) string {
	switch value {
	case "posix", "powershell", "direct":
		return value
	case "":
		return "direct"
	default:
		return "direct"
	}
}

func checksumState(value State) State {
	if states[value] {
		return value
	}
	// A run that was not told is not a run that verified. `not_available` is
	// the honest answer and the merge on the development machine reads it as
	// one, rather than as a silent pass.
	return NotAvailable
}

// run is the state one invocation carries between lanes.
type run struct {
	executable  string
	work        string
	artifactDir string
	home        string
	options     Options
	now         func() time.Time
	redact      *Redactor

	env []string
	// configDir is what the product itself says its configuration directory is.
	configDir string
	// toolProfileID is the tool profile the profile lane authored, if it got
	// far enough for the jobs lane to reuse it.
	toolProfileID string
	// toolGranted records whether that profile is currently approved.
	toolGranted bool
	// compilersBound records whether the toolchain lane bound an EricW build.
	compilersBound bool
	game           []GameRow
}

func (r *run) prepare() error {
	for _, directory := range []string{
		r.home,
		filepath.Join(r.home, "config"),
		filepath.Join(r.home, "cache"),
		filepath.Join(r.home, "data"),
		filepath.Join(r.work, "project"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	// Every variable `os.UserConfigDir`, `os.UserCacheDir` and
	// `os.UserHomeDir` consult, on every platform this builds for. Setting
	// only HOME would contain a POSIX run and leave a Windows one writing to
	// the operator's real profile.
	r.env = append(os.Environ(),
		"HOME="+r.home,
		"USERPROFILE="+r.home,
		"XDG_CONFIG_HOME="+filepath.Join(r.home, "config"),
		"XDG_CACHE_HOME="+filepath.Join(r.home, "cache"),
		"XDG_DATA_HOME="+filepath.Join(r.home, "data"),
		"AppData="+filepath.Join(r.home, "config"),
		"LocalAppData="+filepath.Join(r.home, "cache"),
	)
	r.redact.Root(r.home, "home")
	r.redact.Root(r.work, "work")
	r.redact.Root(r.artifactDir, "artifact")
	r.redact.Root(filepath.Dir(r.executable), "artifact")
	r.redact.Root(r.options.ToolPath, "tool_path")
	r.redact.Root(r.options.EngineRoot, "engine")
	r.redact.Root(r.options.GameRoot, "game_root")
	return nil
}

func (r *run) selected(id string) bool {
	if len(r.options.Only) > 0 {
		for _, name := range r.options.Only {
			if name == id {
				return true
			}
		}
		return false
	}
	for _, name := range r.options.Skip {
		if name == id {
			return false
		}
	}
	return true
}

// result is one child invocation.
type result struct {
	stdout string
	stderr string
	code   int
	// timedOut records that the deadline stopped it, which is a different fact
	// from a program that chose to exit non-zero.
	timedOut bool
}

func (res result) output() string { return res.stdout + res.stderr }

// exec runs the Companion as a child process.
//
// A child rather than an in-process call, deliberately. `AUT/AUCOM 219` found
// three defects that only exist at the boundary this crosses — a usage line
// whose flags could not be parsed in the order it printed them, a success
// message naming a command that does not exist — and none of them is reachable
// from a function call. What an operator runs is a process with an argv and an
// exit status, so that is what is measured.
func (r *run) exec(ctx context.Context, timeout time.Duration, args ...string) result {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(bounded, r.executable, args...)
	command.Env = r.env
	command.Dir = r.work
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}
	if errors.Is(bounded.Err(), context.DeadlineExceeded) {
		res.timedOut = true
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		res.code = 0
	case errors.As(err, &exit):
		res.code = exit.ExitCode()
	default:
		res.code = -1
		res.stderr += err.Error()
	}
	return res
}

func (r *run) version(ctx context.Context) string {
	res := r.exec(ctx, 30*time.Second, "version")
	version := strings.TrimSpace(res.stdout)
	if version == "" {
		return "unknown"
	}
	if index := strings.IndexAny(version, "\r\n"); index >= 0 {
		version = version[:index]
	}
	return version
}

// pass, fail and the rest are the four ways an observation is recorded. They
// exist so a lane reads as a list of claims rather than as struct literals.
func pass(claim, detail string) Observation {
	return Observation{Claim: claim, State: Pass, Detail: detail}
}

func failed(claim, detail string) Observation {
	return Observation{Claim: claim, State: Fail, Detail: detail}
}

func unavailable(claim, reason string) Observation {
	return Observation{Claim: claim, State: NotAvailable, Detail: reason}
}

func inapplicable(claim, reason string) Observation {
	return Observation{Claim: claim, State: NotApplicable, Detail: reason}
}

// verdict turns a boolean into a pass or a fail with one detail each, which is
// the shape most claims here have.
func verdict(ok bool, claim, whenPass, whenFail string) Observation {
	if ok {
		return pass(claim, whenPass)
	}
	return failed(claim, whenFail)
}

// resolveConfigDir asks the product where its own configuration lives, rather
// than composing the path from the platform's conventions.
//
// The workspace's runtime-truth rule, one level out: a value the running
// program reports about itself outranks one this code derived. `uninstall`
// answers it because listing what is on the machine is exactly its job.
func (r *run) resolveConfigDir(ctx context.Context) (string, error) {
	if r.configDir != "" {
		return r.configDir, nil
	}
	res := r.exec(ctx, 60*time.Second, "uninstall", "--json")
	if res.code != 0 {
		return "", fmt.Errorf("uninstall --json exited %d", res.code)
	}
	var targets []struct {
		Label string `json:"Label"`
		Path  string `json:"Path"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &targets); err != nil {
		return "", fmt.Errorf("uninstall --json is not the listing this expects: %w", err)
	}
	for _, target := range targets {
		if target.Label == "configuration" {
			r.configDir = target.Path
			return target.Path, nil
		}
	}
	return "", errors.New("uninstall --json listed no configuration directory")
}

// hostArchMatches compares the machine architecture the entry point read from
// the operating system against the one this artifact was compiled for.
//
// This is the check that separates "an arm64 machine ran the arm64 artifact"
// from "an arm64 machine ran the amd64 artifact under emulation". Rosetta 2 and
// WOW64 both make the second work well enough that nothing else in a run would
// notice, and a bundle claiming to be native evidence for `darwin/arm64` when
// it is evidence about an emulator is exactly the false claim `AUT/AUCOM 231`
// exists to prevent.
func hostArchMatches(hostArch string) (bool, string) {
	normalised := normaliseArch(hostArch)
	if normalised == "" {
		return false, ""
	}
	return normalised == runtime.GOARCH, normalised
}

// normaliseArch maps what an operating system calls a machine onto what Go
// calls a target. An unknown name maps to nothing, and an unknown name is
// reported as unknown rather than guessed at.
func normaliseArch(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "x86_64", "amd64", "x64", "em64t":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv6l", "armv7l", "arm":
		return "arm"
	case "i386", "i486", "i586", "i686", "x86":
		return "386"
	case "riscv64":
		return "riscv64"
	case "ppc64le":
		return "ppc64le"
	case "s390x":
		return "s390x"
	case "loongarch64":
		return "loong64"
	default:
		return ""
	}
}

// knownLanes is the set LaneIDs describes, for validating --only and --skip.
func knownLanes() map[string]bool {
	known := make(map[string]bool, len(LaneIDs))
	for _, id := range LaneIDs {
		known[id] = true
	}
	return known
}

// CheckLanes reports every name in a selection that is not a lane.
func CheckLanes(names []string) []string {
	known := knownLanes()
	var unknown []string
	for _, name := range names {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// LaneTitle is what a lane claims, for a command that lists them.
func LaneTitle(id string) string { return laneTitles[id] }

// StateNames is the closed set of states, sorted, for a command that prints it.
func StateNames() []string {
	names := make([]string, 0, len(states))
	for state := range states {
		names = append(names, string(state))
	}
	sort.Strings(names)
	return names
}
