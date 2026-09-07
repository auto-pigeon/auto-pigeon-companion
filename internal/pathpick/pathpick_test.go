package pathpick

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a Runner that answers a fixed way and remembers the argv.
type recorder struct {
	stdout   string
	stderr   string
	exitCode int
	err      error

	mu     sync.Mutex
	binary string
	args   []string
	calls  int
	block  chan struct{}
}

func (r *recorder) run(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	r.mu.Lock()
	r.binary, r.args, r.calls = name, args, r.calls+1
	block := r.block
	r.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, nil, 0, ctx.Err()
		}
	}
	return []byte(r.stdout), []byte(r.stderr), r.exitCode, r.err
}

func (r *recorder) argv() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.args...)
}

// present is a Look that reports exactly the named helpers as installed.
func present(names ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, name := range names {
		set[name] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

func home(t *testing.T) func() (string, error) {
	t.Helper()
	dir := t.TempDir()
	return func() (string, error) { return dir, nil }
}

// A machine with no chooser is a normal machine, and the caller has to be able
// to tell that from a chooser that failed — otherwise the UI cannot know
// whether to offer a text field or report a fault.
func TestPickWithoutAHelperReportsErrNoHelper(t *testing.T) {
	picker := &Picker{GOOS: "linux", Look: present(), Home: home(t)}
	if _, err := picker.Pick(context.Background(), Request{Kind: Directory}); !errors.Is(err, ErrNoHelper) {
		t.Fatalf("Pick with no helper installed = %v, want ErrNoHelper", err)
	}
	if got := picker.Available(); got != "" {
		t.Fatalf("Available with no helper installed = %q, want empty", got)
	}
}

// Every platform this program ships to, and what it would run there. The point
// is that a Linux CI runner checks the Windows and macOS argv too: those are
// the two nobody can try by hand here.
func TestAdapterPerPlatform(t *testing.T) {
	for _, testCase := range []struct {
		goos      string
		installed []string
		want      string
	}{
		{"linux", []string{"zenity", "kdialog"}, "zenity"},
		{"linux", []string{"kdialog"}, "kdialog"},
		{"darwin", []string{"osascript"}, "osascript"},
		{"windows", []string{"powershell"}, "powershell"},
		{"js", []string{"zenity"}, ""},
	} {
		picker := &Picker{GOOS: testCase.goos, Look: present(testCase.installed...), Home: home(t)}
		if got := picker.Available(); got != testCase.want {
			t.Errorf("Available on %s with %v = %q, want %q",
				testCase.goos, testCase.installed, got, testCase.want)
		}
	}
}

// Cancellation is not an error condition, and each platform spells it
// differently: the Unix helpers exit 1, and the PowerShell script exits 0
// having printed nothing at all.
func TestCancellation(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		goos   string
		helper string
		runner *recorder
	}{
		{"zenity closed", "linux", "zenity", &recorder{exitCode: 1}},
		{"zenity timed out", "linux", "zenity", &recorder{exitCode: 5}},
		{"kdialog closed", "linux", "kdialog", &recorder{exitCode: 1}},
		{"osascript raised -128", "darwin", "osascript", &recorder{exitCode: 1,
			stderr: "execution error: User canceled. (-128)"}},
		{"powershell returned Cancel", "windows", "powershell", &recorder{exitCode: 0, stdout: ""}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			picker := &Picker{
				GOOS: testCase.goos,
				Look: present(testCase.helper),
				Run:  testCase.runner.run,
				Home: home(t),
			}
			_, err := picker.Pick(context.Background(), Request{Kind: Directory})
			if !errors.Is(err, ErrCancelled) {
				t.Fatalf("Pick = %v, want ErrCancelled", err)
			}
		})
	}
}

func TestPickReturnsTheChosenDirectory(t *testing.T) {
	dir := t.TempDir()
	runner := &recorder{stdout: dir + "\n"}
	picker := &Picker{GOOS: "linux", Look: present("zenity"), Run: runner.run, Home: home(t)}

	result, err := picker.Pick(context.Background(), Request{Kind: Directory, Title: "Game folder"})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if result.Path != dir {
		t.Errorf("Path = %q, want %q", result.Path, dir)
	}
	if result.Helper != "zenity" {
		t.Errorf("Helper = %q, want zenity", result.Helper)
	}
	argv := strings.Join(runner.argv(), " ")
	for _, want := range []string{"--file-selection", "--directory", "--title=Game folder"} {
		if !strings.Contains(argv, want) {
			t.Errorf("zenity argv %q is missing %q", argv, want)
		}
	}
}

// A helper is a subprocess, and a subprocess's stdout is not a path until it
// has been checked. Every one of these is something a wrapper script, a shell
// alias or a misbehaving helper could produce.
func TestPickRefusesWhatAHelperReturns(t *testing.T) {
	existing := t.TempDir()
	for _, testCase := range []struct {
		name   string
		stdout string
		want   string
	}{
		{"a relative path", "quake/id1", "absolute"},
		{"nothing at all", "   ", "printed no path"},
		{"a path that is not there", filepath.Join(existing, "absent"), "no such file"},
		{"a file where a directory was asked for", "", "a directory was asked for"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stdout := testCase.stdout
			if testCase.name == "a file where a directory was asked for" {
				file := filepath.Join(existing, "pak0.pak")
				if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				stdout = file
			}
			runner := &recorder{stdout: stdout}
			picker := &Picker{GOOS: "linux", Look: present("zenity"), Run: runner.run, Home: home(t)}
			_, err := picker.Pick(context.Background(), Request{Kind: Directory})
			if err == nil {
				t.Fatalf("Pick accepted %q", stdout)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Pick error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

// Only the first line. A helper that prints two paths was asked for one, and
// taking the second would be taking a value nobody chose.
func TestPickTakesOnlyTheFirstLine(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	runner := &recorder{stdout: first + "\n" + second + "\n"}
	picker := &Picker{GOOS: "linux", Look: present("zenity"), Run: runner.run, Home: home(t)}
	result, err := picker.Pick(context.Background(), Request{Kind: Directory})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if result.Path != first {
		t.Fatalf("Path = %q, want the first line %q", result.Path, first)
	}
}

// Two dialogs at once would stack two windows over each other, and the person
// underneath would be answering a question they cannot see.
func TestSecondDialogIsRefusedWhileOneIsOpen(t *testing.T) {
	release := make(chan struct{})
	runner := &recorder{stdout: t.TempDir(), block: release}
	picker := &Picker{GOOS: "linux", Look: present("zenity"), Run: runner.run, Home: home(t)}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := picker.Pick(context.Background(), Request{Kind: Directory})
		done <- err
	}()
	<-started
	// Wait until the first Pick is actually inside the runner, so the test is
	// not racing the goroutine's scheduling.
	deadline := time.Now().Add(2 * time.Second)
	for {
		runner.mu.Lock()
		calls := runner.calls
		runner.mu.Unlock()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first Pick never reached the helper")
		}
		time.Sleep(time.Millisecond)
	}

	if _, err := picker.Pick(context.Background(), Request{Kind: Directory}); !errors.Is(err, ErrBusy) {
		t.Fatalf("the second Pick = %v, want ErrBusy", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the first Pick: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("the helper ran %d times, want 1", runner.calls)
	}
}

// A helper that never answers must not hold the lock for the life of the
// process; the caller's own cancellation has to reach it too.
func TestPickStopsWhenTheCallerGivesUp(t *testing.T) {
	runner := &recorder{block: make(chan struct{})}
	picker := &Picker{GOOS: "linux", Look: present("zenity"), Run: runner.run, Home: home(t)}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	if _, err := picker.Pick(ctx, Request{Kind: Directory}); err == nil {
		t.Fatal("Pick returned successfully after its context was cancelled")
	}
	// And the picker is usable again afterwards.
	runner.mu.Lock()
	runner.block, runner.stdout = nil, t.TempDir()
	runner.mu.Unlock()
	if _, err := picker.Pick(context.Background(), Request{Kind: Directory}); err != nil {
		t.Fatalf("the picker did not recover from a cancelled dialog: %v", err)
	}
}

func TestPickReportsAHelperThatFailed(t *testing.T) {
	runner := &recorder{exitCode: 2, stderr: "zenity: cannot open display\nsecond line"}
	picker := &Picker{GOOS: "linux", Look: present("zenity"), Run: runner.run, Home: home(t)}
	_, err := picker.Pick(context.Background(), Request{Kind: Directory})
	if err == nil {
		t.Fatal("Pick reported no error for a helper that exited 2")
	}
	if !strings.Contains(err.Error(), "cannot open display") {
		t.Fatalf("Pick error = %v, want it to carry what zenity said", err)
	}
	if strings.Contains(err.Error(), "second line") {
		t.Fatalf("Pick error = %v, want only the first line of stderr", err)
	}
}

// The argv for the two platforms this test suite can never actually run on.
func TestWindowsAndDarwinArgv(t *testing.T) {
	dir := t.TempDir()
	mapFile := filepath.Join(dir, "start.map")
	if err := os.WriteFile(mapFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	windows := &recorder{stdout: mapFile}
	picker := &Picker{GOOS: "windows", Look: present("powershell"), Run: windows.run, Home: home(t)}
	if _, err := picker.Pick(context.Background(), Request{
		Kind: OpenFile, Title: "Pick a map", StartDir: dir,
		Filters: []Filter{{Name: "Quake map sources", Extensions: []string{"map"}}},
	}); err != nil {
		t.Fatalf("Pick on windows: %v", err)
	}
	argv := strings.Join(windows.argv(), " ")
	for _, want := range []string{"-NoProfile", "-STA", "OpenFileDialog",
		"'Pick a map'", "Quake map sources|*.map", "System.Windows.Forms.DialogResult]::OK"} {
		if !strings.Contains(argv, want) {
			t.Errorf("powershell argv %q is missing %q", argv, want)
		}
	}

	darwin := &recorder{stdout: dir}
	picker = &Picker{GOOS: "darwin", Look: present("osascript"), Run: darwin.run, Home: home(t)}
	if _, err := picker.Pick(context.Background(), Request{
		Kind: Directory, Title: `The "id1" folder`, StartDir: dir,
	}); err != nil {
		t.Fatalf("Pick on darwin: %v", err)
	}
	argv = strings.Join(darwin.argv(), " ")
	for _, want := range []string{"choose folder with prompt", `\"id1\"`, "default location POSIX file"} {
		if !strings.Contains(argv, want) {
			t.Errorf("osascript argv %q is missing %q", argv, want)
		}
	}
}

// A title with a quote in it is a title, not a way to change what a helper is
// asked. Neither of these two languages tolerates a naive concatenation.
func TestTitlesAreQuotedForTheirLanguage(t *testing.T) {
	if got := powerShellString(`it's $HOME`); got != `'it''s $HOME'` {
		t.Errorf("powerShellString = %s", got)
	}
	if got := appleString(`a "quoted" \ path`); got != `"a \"quoted\" \\ path"` {
		t.Errorf("appleString = %s", got)
	}
}
