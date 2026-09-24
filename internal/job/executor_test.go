package job

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// writeAction is the fixture that produces an artifact: it writes the text of
// an option into a declared output.
func writeAction() fixtureAction {
	return fixtureAction{
		ID:         "write",
		Title:      "Write a file",
		Executable: "helper",
		Args: []any{
			helperFlag, "write", "{output.result}", "{option.text}",
		},
		Outputs: []map[string]any{
			{"name": "result", "title": "The written file", "role": "test.result", "path": "out/result.txt"},
		},
		Options: []map[string]any{
			{"name": "text", "title": "Text", "type": "text", "default": "hello", "max_length": 64},
		},
		Roots: []map[string]any{
			{"role": "workspace", "access": "read_write", "purpose": "write the result"},
		},
		Timeout: 60,
	}
}

func modeAction(id string, args ...any) fixtureAction {
	return fixtureAction{
		ID:         id,
		Title:      "Fixture action " + id,
		Executable: "helper",
		Args:       append([]any{helperFlag}, args...),
		Roots: []map[string]any{
			{"role": "workspace", "access": "read_write", "purpose": "scratch space"},
		},
		Timeout: 60,
	}
}

func TestASuccessfulJobRecordsWhatItRanAndPublishesItsOutputs(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.executor.write", writeAction()))

	request := h.helperRequest("test.executor.write", "write")
	request.Options = map[string]string{"text": "written-by-the-fixture"}
	finished := h.runToEnd(request)

	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	if finished.ExitCode == nil || *finished.ExitCode != 0 {
		t.Errorf("exit code = %v, want 0", finished.ExitCode)
	}
	if finished.ProfileDigest == "" || !strings.HasPrefix(finished.ProfileDigest, "sha256:") {
		t.Errorf("profile digest = %q, want a sha256 digest", finished.ProfileDigest)
	}
	if finished.ProfileID != "test.executor.write" || finished.ActionID != "write" {
		t.Errorf("recorded %s/%s, want test.executor.write/write", finished.ProfileID, finished.ActionID)
	}
	if finished.Command == nil {
		t.Fatal("no command was recorded")
	}
	if finished.Command.Digest == "" {
		t.Error("the command has no digest")
	}
	if len(finished.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(finished.Artifacts))
	}
	artifact := finished.Artifacts[0]
	if artifact.Missing {
		t.Fatal("the declared output was not produced")
	}
	content, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatalf("reading the published artifact: %v", err)
	}
	if string(content) != "written-by-the-fixture" {
		t.Errorf("artifact content = %q", content)
	}
	if artifact.SHA256 == "" || artifact.Size != int64(len(content)) {
		t.Errorf("artifact digest %q size %d, want both recorded", artifact.SHA256, artifact.Size)
	}

	// The workspace is gone and the artifact is not: that is the contract a
	// successful job publishes under.
	if _, err := os.Stat(finished.Workspace); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the workspace survived a successful job: %v", err)
	}
	if _, err := os.Stat(artifact.Path); err != nil {
		t.Errorf("the artifact did not survive cleanup: %v", err)
	}
}

func TestANonzeroExitIsAFailureCarryingTheProgramsOwnStatus(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.executor.exit", modeAction("exit", "exit", "3")))

	finished := h.runToEnd(h.helperRequest("test.executor.exit", "exit"))

	if finished.State != Failed {
		t.Fatalf("state = %s, want failed", finished.State)
	}
	if finished.ExitCode == nil || *finished.ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", finished.ExitCode)
	}
	if !strings.Contains(finished.Error, "status 3") {
		t.Errorf("error = %q, want it to name the exit status", finished.Error)
	}
	// The output before the failure is kept: it is usually the reason.
	if got := h.mustLog(finished.ID, "stdout"); !strings.Contains(got, "about to exit 3") {
		t.Errorf("stdout = %q, want the program's own output", got)
	}
}

// vkQuake 1.36.0 aborts while quitting and its AppImage exits 127. The profile
// names the line only that quit prints, and a non-zero exit after it is a
// stop; the same status with no such line is still the failure it always was.
func TestAQuitThatCrashesAfterItsCleanStopLineIsAStop(t *testing.T) {
	action := func(id string, args ...any) fixtureAction {
		a := modeAction(id, args...)
		a.Diagnostics = []map[string]any{{
			"id": "quit_crash", "stream": "stderr", "match": "buffer overflow detected",
			"severity": "warning", "message": "It crashed while quitting.", "clean_stop": true,
		}}
		return a
	}
	h := newHarness(t, fixtureProfile(t, "test.executor.quit",
		action("quit", "stderr-exit", "127", "*** buffer overflow detected ***: terminated"),
		action("crash", "exit", "127")))

	quit := h.runToEnd(h.helperRequest("test.executor.quit", "quit"))
	if quit.State != Succeeded {
		t.Fatalf("state = %s (%s), want succeeded: the quit line was printed", quit.State, quit.Error)
	}
	if quit.ExitCode == nil || *quit.ExitCode != 127 {
		t.Errorf("exit code = %v, want the program's own 127 kept as evidence", quit.ExitCode)
	}
	last := quit.History[len(quit.History)-1]
	if !strings.Contains(last.Note, "clean stop") || !strings.Contains(last.Note, "127") {
		t.Errorf("last history note = %q, want it to say clean stop and the status", last.Note)
	}

	crash := h.runToEnd(h.helperRequest("test.executor.quit", "crash"))
	if crash.State != Failed {
		t.Fatalf("state = %s, want failed: nothing proved a normal quit", crash.State)
	}
}

func TestACleanStopRuleCannotBeAnError(t *testing.T) {
	action := modeAction("quit", "exit", "1")
	action.Diagnostics = []map[string]any{{
		"id": "quit", "match": "x", "severity": "error", "clean_stop": true,
	}}
	_, err := profile.Decode(fixtureProfile(t, "test.executor.badquit", action))
	if err == nil || !strings.Contains(err.Error(), "clean_stop") {
		t.Fatalf("decode error = %v, want one naming clean_stop", err)
	}
}

func TestAMissingExecutableSaysWhatToDoAboutIt(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.executor.missing", modeAction("run", "echo", "hello")))

	request := h.helperRequest("test.executor.missing", "run")
	request.Executables = map[string]string{"helper": filepath.Join(t.TempDir(), "not-installed")}
	finished := h.runToEnd(request)

	if finished.State != Failed {
		t.Fatalf("state = %s, want failed", finished.State)
	}
	if finished.ExitCode != nil {
		t.Errorf("exit code = %v, want none: no process ever existed", *finished.ExitCode)
	}
	if !strings.Contains(finished.Error, "does not exist") || !strings.Contains(finished.Error, "acquire the tool") {
		t.Errorf("error = %q, want it to say the tool is missing and what to do", finished.Error)
	}
}

func TestATimeoutStopsTheProgramAndSaysSo(t *testing.T) {
	action := modeAction("slow", "sleep", "60")
	action.Timeout = 1
	h := newHarness(t, fixtureProfile(t, "test.executor.timeout", action))

	started := time.Now()
	finished := h.runToEnd(h.helperRequest("test.executor.timeout", "slow"))
	elapsed := time.Since(started)

	if finished.State != Failed {
		t.Fatalf("state = %s, want failed", finished.State)
	}
	if !finished.TimedOut {
		t.Error("timed_out was not recorded")
	}
	if !strings.Contains(finished.Error, "did not finish within") {
		t.Errorf("error = %q, want it to name the timeout", finished.Error)
	}
	if elapsed > 30*time.Second {
		t.Errorf("the timeout took %s to take effect", elapsed)
	}
}

func TestCancellationReachesAJobAnotherProcessIsSupervising(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.executor.cancel", modeAction("slow", "sleep", "60")))

	submitted, err := h.service.Submit(h.helperRequest("test.executor.cancel", "slow"))
	if err != nil {
		t.Fatalf("submitting: %v", err)
	}
	waitForState(t, h, submitted.ID, Running)

	// Deliberately through the store rather than the service: this is the path
	// a `companion job cancel` typed in another terminal takes.
	other, err := OpenStore(h.store.Root())
	if err != nil {
		t.Fatalf("opening a second view of the store: %v", err)
	}
	if err := other.RequestCancel(submitted.ID); err != nil {
		t.Fatalf("requesting cancellation: %v", err)
	}

	finished := h.waitFor(submitted.ID)
	if finished.State != Cancelled {
		t.Fatalf("state = %s, want cancelled (error: %s)", finished.State, finished.Error)
	}
	if finished.FinishedAt.IsZero() {
		t.Error("a cancelled job has no finish time")
	}
	// Running -> cancelling -> cancelled, every step one the state machine
	// allows. A stop used to jump running -> cancelled, which finish() could
	// only force, and the log said so on every Stop from the page.
	var states []State
	for _, event := range finished.History {
		if strings.HasPrefix(event.Note, "forced: ") {
			t.Errorf("the stop forced a transition: %s", event.Note)
		}
		states = append(states, event.State)
	}
	if n := len(states); n < 3 || states[n-3] != Running || states[n-2] != Cancelling || states[n-1] != Cancelled {
		t.Errorf("history = %v, want it to end running, cancelling, cancelled", states)
	}
}

func TestAChildThatOutlivesItsParentDoesNotHangTheJob(t *testing.T) {
	// The helper starts a grandchild that holds the output pipe for a minute
	// and then exits immediately itself. A supervisor waiting for end-of-file
	// on that pipe waits for the grandchild, which is the hang this guards.
	h := newHarness(t, fixtureProfile(t, "test.executor.children", modeAction("spawn", "spawn", "60")))

	started := time.Now()
	finished := h.runToEnd(h.helperRequest("test.executor.children", "spawn"))
	elapsed := time.Since(started)

	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("the job took %s: the orphaned child held it open", elapsed)
	}
}

func TestAnOutputFloodIsBoundedInMemoryAndOnDisk(t *testing.T) {
	const flooded = 4 << 20 // Well past head+tail, and quick to produce.
	action := modeAction("flood", "flood", "4194304")
	action.Timeout = 120
	h := newHarness(t, fixtureProfile(t, "test.executor.flood", action))

	finished := h.runToEnd(h.helperRequest("test.executor.flood", "flood"))

	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	if finished.Stdout.Bytes < flooded {
		t.Errorf("counted %d bytes, want at least %d: the count is of what the program wrote, not what was kept",
			finished.Stdout.Bytes, flooded)
	}
	if !finished.Stdout.Truncated || finished.Stdout.Dropped == 0 {
		t.Errorf("a %d-byte flood was not recorded as truncated: %+v", flooded, finished.Stdout)
	}
	if limit := int64(headBytes + tailBytes); finished.Stdout.Stored > limit {
		t.Errorf("stored %d bytes, over the %d-byte bound", finished.Stdout.Stored, limit)
	}
	raw, err := h.service.Logs(finished.ID, "stdout", true)
	if err != nil {
		t.Fatalf("reading the raw log: %v", err)
	}
	if int64(len(raw)) > int64(headBytes+tailBytes)+1024 {
		t.Errorf("the raw log is %d bytes, over the bound plus its elision marker", len(raw))
	}
	if !strings.Contains(string(raw), "bytes of output were not kept") {
		t.Error("the truncated log does not say that it is truncated")
	}
}

func TestInvalidUTF8SurvivesRawAndIsRepairedForTheReader(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.executor.utf8", modeAction("garbage", "invalid-utf8")))

	finished := h.runToEnd(h.helperRequest("test.executor.utf8", "garbage"))
	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}

	raw, err := h.service.Logs(finished.ID, "stdout", true)
	if err != nil {
		t.Fatalf("reading the raw log: %v", err)
	}
	// The evidence is intact: the exact bytes the program wrote.
	for _, want := range []byte{0xff, 0xfe, 0x80, 0x1b} {
		if !strings.ContainsRune(string(raw), rune(want)) && !containsByte(raw, want) {
			t.Errorf("the raw log lost the byte %#x", want)
		}
	}

	view := h.mustLog(finished.ID, "stdout")
	if containsByte([]byte(view), 0x1b) {
		t.Error("the user view kept an escape byte, which a terminal would obey")
	}
	if !strings.Contains(view, "start") || !strings.Contains(view, "end") {
		t.Errorf("the user view lost the readable text: %q", view)
	}
	if !strings.ContainsRune(view, '\uFFFD') {
		t.Errorf("invalid UTF-8 was not replaced in the user view: %q", view)
	}
}

func containsByte(data []byte, want byte) bool {
	for _, b := range data {
		if b == want {
			return true
		}
	}
	return false
}

func TestTheProcessInheritsNothingItWasNotGiven(t *testing.T) {
	t.Setenv("AUCOM_TEST_SECRET", "a-secret-that-must-not-be-inherited")
	h := newHarness(t, fixtureProfile(t, "test.executor.env", modeAction("env", "dump-env")))

	finished := h.runToEnd(h.helperRequest("test.executor.env", "env"))
	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	environment := h.mustLog(finished.ID, "stdout")

	if strings.Contains(environment, "AUCOM_TEST_SECRET") {
		t.Error("the process inherited an environment variable nothing declared")
	}
	if strings.Contains(environment, "PATH=") && runtime.GOOS != "windows" {
		t.Error("the process was given PATH, which makes `run this program` mean `run whatever is first on a search path`")
	}
	// What it does get: a home and a temporary directory inside its own job.
	for _, want := range []string{"HOME=", "TMPDIR=", "LC_ALL=C"} {
		if !strings.Contains(environment, want) {
			t.Errorf("the environment is missing %s:\n%s", want, environment)
		}
	}
	for _, line := range strings.Split(environment, "\n") {
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch name {
		case "HOME", "TMPDIR", "TEMP", "TMP", "USERPROFILE":
			if err := within(filepath.Dir(finished.Workspace), value); err != nil {
				t.Errorf("%s is %q, which is outside the job's own directory", name, value)
			}
		}
	}
}
