package aue_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
)

// fakeExtractor writes a shell script and returns an override runner for it.
// The runner-level properties — timeout, cancellation, isolation, the output
// cap — are about the process and not about where the executable came from, so
// they are tested through the cheapest path to one.
func fakeExtractor(t *testing.T, body string) *aue.ProcessRunner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake extractor is a shell script")
	}
	path := filepath.Join(t.TempDir(), "fake-aue")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}

	return aue.NewOverrideRunner(path)
}

// An invocation that never returns is stopped, and the error says so rather
// than looking like a crash. The bound is on the WHOLE run: a process printing
// one line every nine minutes keeps a per-read deadline satisfied for ever.
func TestAnInvocationThatHangsIsStoppedAndSaysSo(t *testing.T) {
	runner := fakeExtractor(t, "sleep 30\n")
	runner.Timeout = 150 * time.Millisecond
	// A shell waiting on a foreground child defers SIGTERM until that child
	// exits, so this fixture is exactly the "ignored the signal" case the grace
	// period exists for. Shortened here so the test measures the mechanism
	// rather than five seconds of it.
	runner.Grace = 200 * time.Millisecond

	started := time.Now()
	_, err := runner.Run(context.Background(), "summarize")
	elapsed := time.Since(started)

	var timeout *aue.TimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("err = %T %v, want *aue.TimeoutError", err, err)
	}
	if timeout.Subcommand != "summarize" {
		t.Errorf("timeout = %+v", timeout)
	}
	if elapsed > 3*time.Second {
		t.Errorf("the run took %s; the timeout did not stop it", elapsed)
	}
}

// A cancelled context stops the run promptly, which is what a user pressing
// Ctrl-C and a closed browser tab both look like from here.
func TestACancelledContextStopsTheRun(t *testing.T) {
	runner := fakeExtractor(t, "sleep 30\n")
	runner.Grace = 200 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	_, err := runner.Run(ctx, "summarize")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("cancellation took %s", elapsed)
	}
}

// The extractor's published contract says a supervised run ends deliberately on
// SIGTERM. This Companion sends one, so a build that installed a handler gets
// to write its terminal record instead of being killed with no reason.
func TestCancellationSendsSIGTERMBeforeAnythingHarsher(t *testing.T) {
	runner := fakeExtractor(t, `
trap 'echo terminated; exit 0' TERM
sleep 30 &
wait
`)
	runner.Timeout = 300 * time.Millisecond

	stdout, err := runner.Run(context.Background(), "summarize")
	if err == nil {
		t.Fatal("the run was not stopped")
	}
	if !strings.Contains(string(stdout), "terminated") {
		t.Errorf("the process was not given a chance to handle SIGTERM: stdout = %q", stdout)
	}
}

// Every invocation gets its own working directory, so two concurrent ones
// cannot see each other's scratch and a relative path in an argument cannot
// reach the Companion's own directory.
func TestEveryInvocationGetsItsOwnWorkingDirectory(t *testing.T) {
	runner := fakeExtractor(t, "pwd\n")

	first, err := runner.Run(context.Background(), "summarize")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.Run(context.Background(), "summarize")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(first)) == strings.TrimSpace(string(second)) {
		t.Errorf("two invocations shared a working directory: %q", first)
	}

	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(first)) == here {
		t.Errorf("the extractor ran in this program's own directory: %q", first)
	}
	// And it is gone afterwards: a job directory that outlived its job would
	// accumulate one per invocation for the life of the process.
	if _, err := os.Stat(strings.TrimSpace(string(first))); !os.IsNotExist(err) {
		t.Errorf("the job directory survived the run: %v", err)
	}
}

// The deadline the extractor's own contract names is passed in, so the process
// can end itself with a named reason rather than being signalled.
func TestTheChildIsToldItsDeadline(t *testing.T) {
	runner := fakeExtractor(t, "echo \"$AUTO_PIGEON_JOB_DEADLINE_SECONDS\"\n")
	runner.Timeout = 90 * time.Second

	stdout, err := runner.Run(context.Background(), "summarize")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdout)) != "90" {
		t.Errorf("deadline = %q, want 90", stdout)
	}
}

// A subprocess is not a trusted producer of unbounded output.
func TestOutputPastTheCapIsRefusedRatherThanBuffered(t *testing.T) {
	runner := fakeExtractor(t, "yes 0123456789012345678901234567890123456789012345678901234567890123\n")
	runner.Timeout = 30 * time.Second

	_, err := runner.Run(context.Background(), "summarize")
	if !errors.Is(err, aue.ErrOutputTooLarge) {
		t.Fatalf("err = %v, want aue.ErrOutputTooLarge", err)
	}
}

// RunJSON exists because exit 0 plus half a document decodes into a
// partially-filled struct a caller then acts on.
func TestRunJSONRefusesAnythingThatIsNotOneDocument(t *testing.T) {
	for name, body := range map[string]string{
		"a warning before the document": "echo 'note: something' \n echo '{\"a\":1}'\n",
		"two documents":                 "echo '{\"a\":1} {\"a\":2}'\n",
		"nothing at all":                "exit 0\n",
		"truncated":                     "echo '{\"a\":'\n",
	} {
		runner := fakeExtractor(t, body)
		var into map[string]any
		if err := runner.RunJSON(context.Background(), &into, "summarize"); !errors.Is(err, aue.ErrOutputNotJSON) {
			t.Errorf("%s: err = %v, want aue.ErrOutputNotJSON", name, err)
		}
	}

	runner := fakeExtractor(t, "echo '{\"a\":1}'\n")
	var into map[string]any
	if err := runner.RunJSON(context.Background(), &into, "summarize"); err != nil {
		t.Fatalf("a single document was refused: %v", err)
	}
	if into["a"] != float64(1) {
		t.Errorf("decoded = %v", into)
	}
}

// An override that is not there, or is not executable, is a sentence rather
// than an exec error.
func TestAnOverrideThatIsNotExecutableIsRefusedWithASentence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	dir := t.TempDir()
	notExecutable := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"absent":         filepath.Join(dir, "nothing-here"),
		"not executable": notExecutable,
		"a directory":    dir,
	} {
		resolver := &aue.Resolver{Dir: t.TempDir(), Override: path}
		if _, err := resolver.Resolve(context.Background()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
