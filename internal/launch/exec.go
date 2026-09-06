package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/tools"
)

// Platform is the target a config is resolved for. It is a parameter rather
// than runtime.GOOS/GOARCH read inline so resolution for every one of the six
// build targets is testable from any one of them — path separators and the .exe
// suffix are exactly the kind of thing that is wrong only on the platform
// nobody develops on.
type Platform struct {
	GOOS   string
	GOARCH string
}

// CurrentPlatform is the running platform.
func CurrentPlatform() Platform {
	return Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

// ExeSuffix is the executable extension for the platform.
func (p Platform) ExeSuffix() string {
	if p.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// Request is everything needed to turn a Config into a process.
type Request struct {
	Config Config
	// GameRoot fills the {game_root} placeholder — where the game is installed.
	GameRoot string
	// Map fills the {map} placeholder: the map name or path to load. Optional.
	Map string
	// Platform defaults to CurrentPlatform when zero.
	Platform Platform
	// ExtraArgs are appended after the config's own arguments, for
	// pass-through flags a user supplies on the command line.
	ExtraArgs []string
}

// Plan is a resolved, ready-to-run process description. Producing it is
// separable from running it on purpose: the GUI shows a user what will be run
// before anything is spawned, and every test in this package asserts on a Plan
// rather than on a side effect.
type Plan struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	WorkingDir string   `json:"working_dir"`
}

// String renders the plan the way a shell would show it. Quoting is
// approximate — this is for display, never for re-execution, since the Companion runs the
// argv directly and never through a shell.
func (p Plan) String() string {
	parts := make([]string, 0, len(p.Args)+1)
	for _, part := range append([]string{p.Executable}, p.Args...) {
		if strings.ContainsAny(part, " \t\"") {
			part = `"` + strings.ReplaceAll(part, `"`, `\"`) + `"`
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// expand substitutes the placeholders a launch config may contain.
//
// Only these four (plus {map}) are recognised, and an unknown placeholder is
// left untouched rather than erroring: a config authored against a future Companion
// that knows more placeholders should degrade to a visibly wrong path, not to a
// launch that refuses to start with a message about a placeholder the user
// cannot remove.
func expand(value string, request Request, platform Platform) string {
	return strings.NewReplacer(
		"{game_root}", request.GameRoot,
		"{os}", platform.GOOS,
		"{arch}", platform.GOARCH,
		"{exe}", platform.ExeSuffix(),
		"{map}", request.Map,
	).Replace(value)
}

// Resolve turns a Request into a Plan without touching the filesystem.
//
// Not touching the filesystem is deliberate: it makes resolution for all six
// platforms testable anywhere, and it keeps "what would run" separate from
// "does it exist here", which Verify answers.
func Resolve(request Request) (Plan, error) {
	platform := request.Platform
	if platform.GOOS == "" {
		platform.GOOS = runtime.GOOS
	}
	if platform.GOARCH == "" {
		platform.GOARCH = runtime.GOARCH
	}

	if strings.TrimSpace(request.Config.ExecutablePattern) == "" {
		return Plan{}, fmt.Errorf("launch: %s has no executable pattern", request.Config.Game)
	}
	if strings.Contains(request.Config.ExecutablePattern, "{game_root}") && request.GameRoot == "" {
		return Plan{}, fmt.Errorf("launch: %s needs a game root directory (configure it, or pass --game-root)", request.Config.Game)
	}

	executable := expand(request.Config.ExecutablePattern, request, platform)
	// Configs are written with forward slashes because they come from a
	// server and are read by humans; Windows accepts them, but normalising
	// means the path the Companion reports matches the one a user would type.
	executable = filepath.FromSlash(executable)
	// A pattern may omit {exe} and still need the suffix on Windows.
	if suffix := platform.ExeSuffix(); suffix != "" && !strings.EqualFold(filepath.Ext(executable), suffix) {
		executable += suffix
	}

	args := make([]string, 0, len(request.Config.Args)+len(request.ExtraArgs))
	for _, arg := range request.Config.Args {
		if strings.Contains(arg, "{map}") && request.Map == "" {
			return Plan{}, fmt.Errorf("launch: %s needs a map name", request.Config.Game)
		}
		args = append(args, expand(arg, request, platform))
	}
	args = append(args, request.ExtraArgs...)

	workingDir := filepath.FromSlash(expand(request.Config.WorkingDir, request, platform))
	if workingDir == "" {
		// Quake engines resolve their game data relative to the process
		// working directory, so defaulting to the executable's own directory
		// is what makes a launch from anywhere behave like a launch from the
		// install folder.
		workingDir = filepath.Dir(executable)
	}

	return Plan{Executable: executable, Args: args, WorkingDir: workingDir}, nil
}

// Verify checks that a plan can actually run on this machine: the executable
// exists, is a regular file, and — on the Unix targets — carries an execute
// bit. Windows has no such bit, so there the mode check is skipped rather than
// faked.
func (p Plan) Verify() error {
	info, err := os.Stat(p.Executable)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("launch: %s does not exist", p.Executable)
		}
		return fmt.Errorf("launch: %s: %w", p.Executable, err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return fmt.Errorf("launch: %s is not a file", p.Executable)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("launch: %s is not executable", p.Executable)
	}
	if p.WorkingDir != "" {
		info, err := os.Stat(p.WorkingDir)
		if err != nil {
			return fmt.Errorf("launch: working directory %s: %w", p.WorkingDir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("launch: working directory %s is not a directory", p.WorkingDir)
		}
	}
	return nil
}

// Run starts the game and waits for it to exit, streaming its output.
//
// Waiting rather than detaching is the right default for both callers: the CLI
// wants the game's exit status, and the GUI wants to know when the session
// ended. A detached "launch and forget" mode can be added when something asks
// for it; guessing at it now would mean two code paths with one of them
// untested.
func Run(ctx context.Context, plan Plan, stdout, stderr io.Writer) error {
	if err := plan.Verify(); err != nil {
		return err
	}
	// Same process runner the external tools use, so stream handling and exit
	// status reporting cannot drift between the two.
	return tools.RunProcess(ctx, plan.Executable, plan.Args, plan.WorkingDir, stdout, stderr)
}
