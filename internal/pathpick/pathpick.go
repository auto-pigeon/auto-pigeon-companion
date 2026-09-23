package pathpick

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Request is one thing to ask the user for.
type Request struct {
	Kind Kind
	// Title is the dialog's own prompt. Empty gets a generic one, because a
	// file chooser with no title is a window that does not say who opened it.
	Title string
	// StartDir is where the dialog opens. A hint only: a helper that cannot
	// honour it opens wherever it likes, which is not a failure.
	StartDir string
	// Filters narrow what a file dialog shows. Best-effort and never a
	// restriction on what may be chosen: every helper here lets a determined
	// user type any name, so a caller must still validate what comes back.
	Filters []Filter
}

// Filter is one entry in a file dialog's type menu.
type Filter struct {
	Name string
	// Extensions are bare, without a dot: {"map", "bsp"}.
	Extensions []string
}

// Result is what the user chose.
type Result struct {
	// Path is absolute, cleaned and checked against the requested kind.
	Path string
	// Helper is the program that asked, for a UI that wants to say which
	// chooser it opened, and for a bug report that needs to say which one
	// misbehaved.
	Helper string
}

// ErrBusy is a second dialog asked for while one is already open.
//
// A file chooser is modal to a person, not to a process: opening a second one
// stacks two windows over each other and leaves the user picking a path for a
// question they cannot see. A page whose button is clicked twice, or reloaded
// mid-dialog, is the ordinary way that happens.
var ErrBusy = errors.New("pathpick: a file chooser is already open")

// DefaultTimeout bounds one dialog.
//
// Long, because the thing on the other end is a person who may be looking for a
// folder they installed a game into four years ago. Bounded at all, because a
// helper that never exits — a dialog opened on a display nobody is at — would
// otherwise hold the picker's lock for the life of the process.
const DefaultTimeout = 10 * time.Minute

// Runner starts a helper and reports what it printed and how it exited.
//
// A field on [Picker] rather than a direct exec call, so a test can drive every
// adapter on any platform: what this package has to get right is the argv it
// builds and the answers it accepts, and neither of those needs a real dialog.
type Runner func(ctx context.Context, name string, args []string) (stdout, stderr []byte, exitCode int, err error)

// Picker opens native file dialogs.
//
// The zero value works: it uses this machine's GOOS, exec.LookPath and a real
// subprocess. Every one of those is a field so a test can replace it.
type Picker struct {
	// GOOS is the platform whose helpers to consider. Empty means this one.
	GOOS string
	// Look reports whether a helper is installed. Nil means exec.LookPath.
	Look func(name string) (string, error)
	// Run starts a helper. Nil means a real subprocess.
	Run Runner
	// Timeout bounds one dialog. Zero means DefaultTimeout.
	Timeout time.Duration
	// Home is the directory a dialog opens in when the caller named none.
	// Nil means os.UserHomeDir.
	Home func() (string, error)

	// busy is held for the whole of one dialog. See ErrBusy.
	mu   sync.Mutex
	busy bool
}

func (p *Picker) goos() string {
	if p.GOOS != "" {
		return p.GOOS
	}
	return runtime.GOOS
}

func (p *Picker) look(name string) (string, error) {
	if p.Look != nil {
		return p.Look(name)
	}
	return exec.LookPath(name)
}

func (p *Picker) home() string {
	get := p.Home
	if get == nil {
		get = os.UserHomeDir
	}
	home, err := get()
	if err != nil {
		return ""
	}
	return home
}

// Available reports the helper this machine would use, or "" when there is
// none.
//
// The UI asks before it draws: a Browse button that opens nothing is worse than
// a text field with no button beside it, because the second tells the truth
// about what this machine can do and the first only does so after a click.
func (p *Picker) Available() string {
	for _, candidate := range adaptersFor(p.goos()) {
		if _, err := p.look(candidate.binary); err == nil {
			return candidate.binary
		}
	}
	return ""
}

// Pick opens a dialog and returns what the user chose.
//
// [ErrNoHelper] when this machine has no chooser, [ErrCancelled] when the user
// declined, [ErrBusy] when one is already open. Everything else is a helper
// that ran and went wrong, and its message carries what the helper said.
func (p *Picker) Pick(ctx context.Context, request Request) (Result, error) {
	if !request.Kind.Valid() {
		return Result{}, fmt.Errorf("pathpick: %q is not a kind of path (want %s)",
			request.Kind, kindList())
	}

	p.mu.Lock()
	if p.busy {
		p.mu.Unlock()
		return Result{}, ErrBusy
	}
	p.busy = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.busy = false
		p.mu.Unlock()
	}()

	chosen, ok := p.adapter()
	if !ok {
		return Result{}, ErrNoHelper
	}

	prepared := request
	if prepared.Title == "" {
		prepared.Title = defaultTitle(prepared.Kind)
	}
	if prepared.StartDir == "" {
		prepared.StartDir = p.home()
	}
	args, err := chosen.build(prepared)
	if err != nil {
		return Result{}, err
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := p.Run
	if run == nil {
		run = execRunner
	}
	stdout, stderr, code, err := run(runCtx, chosen.binary, args)
	output := strings.TrimSpace(string(stdout))
	if chosen.decline(code, output) {
		return Result{}, ErrCancelled
	}
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("pathpick: %s did not answer within %s", chosen.binary, timeout)
		}
		return Result{}, helperFailed(chosen.binary, fmt.Sprintf("could not be started (%v)", err), stderr)
	}
	if code != 0 {
		how := fmt.Sprintf("stopped with status %d", code)
		if code < 0 {
			// Go reports -1 for a process that ended on a signal rather than
			// exiting: the chooser crashed, or something killed it.
			how = "stopped before it answered (it ended on a signal)"
		}
		return Result{}, helperFailed(chosen.binary, how, stderr)
	}

	// The first line only. A helper asked for one path prints one path; a
	// second line is either a multiple selection this package never asks for or
	// something that is not a path at all, and taking it would be taking a
	// value nobody chose.
	line := output
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = line[:index]
	}
	if strings.TrimSpace(line) == "" {
		// Exit 0 and nothing printed. Every adapter whose cancel looks like
		// this has already been caught above, so reaching here means a helper
		// that answered successfully with no answer.
		return Result{}, fmt.Errorf("pathpick: %s exited successfully but printed no path", chosen.binary)
	}
	path, err := Check(prepared.Kind, line)
	if err != nil {
		return Result{}, fmt.Errorf("pathpick: %s returned a path that cannot be used: %w",
			chosen.binary, err)
	}
	return Result{Path: path, Helper: chosen.binary}, nil
}

func (p *Picker) adapter() (adapter, bool) {
	for _, candidate := range adaptersFor(p.goos()) {
		if _, err := p.look(candidate.binary); err == nil {
			return candidate, true
		}
	}
	return adapter{}, false
}

// helperFailed is a chooser that ran and went wrong, said so that a person can
// act on it: which chooser, what happened, the one line it printed that is not
// toolkit noise, and what to do instead.
func helperFailed(binary, how string, stderr []byte) error {
	return fmt.Errorf("%w: %s %s%s. Type the path in the box instead",
		ErrHelperFailed, binary, how, said(stderr))
}

// said is the first line of a helper's stderr that says something about the
// failure. GTK and GLib log warnings about themes and accents first — measured:
// zenity on MATE led with "Adwaita-WARNING … No known Yaru accent 'MATE'" and
// then crashed — and repeating that line as the reason would be naming the one
// thing that was not wrong.
func said(stderr []byte) string {
	for _, line := range strings.Split(string(stderr), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || toolkitNoise(line) {
			continue
		}
		return " — it said: " + line
	}
	return ""
}

func toolkitNoise(line string) bool {
	for _, marker := range []string{"-WARNING **", "-CRITICAL **", "-Message:", "-DEBUG:", "-WARNING:", "Gtk-", "GLib-", "Gdk-", "Adwaita-"} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

func defaultTitle(kind Kind) string {
	switch kind {
	case Directory:
		return "Choose a folder"
	case OpenFile:
		return "Choose a file"
	case SaveFile:
		return "Choose where to save"
	}
	return "Choose"
}

// execRunner is the real subprocess.
//
// The helper inherits no stdin: a dialog reads from the desktop, and a program
// that unexpectedly waits on a terminal the Companion may not have is a program
// that hangs. Output is read into memory because a path is a line, and the
// bound is the helper's own — none of these print anything else.
func execRunner(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
		// A helper that exited non-zero has RUN; the exit code is the answer,
		// and several of them mean "cancelled". So it is reported through the
		// code rather than as a failure to start.
		err = nil
	}
	return []byte(stdout.String()), []byte(stderr.String()), code, err
}
