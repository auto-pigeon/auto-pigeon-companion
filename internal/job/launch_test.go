package job

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_368: a tool job is background work (no console window on Windows); a
// game a person plays is not.
func TestAToolJobIsBackgroundAndAGameIsNot(t *testing.T) {
	if !background(profile.Action{}) {
		t.Error("a tool job (no session role) is not background")
	}
	for _, role := range []profile.SessionRole{profile.SessionClient, profile.SessionListen, profile.SessionDedicated} {
		if background(profile.Action{SessionRole: role}) {
			t.Errorf("a %s session would lose its windows", role)
		}
	}
}

// The program of a background job, in a folder with spaces and non-ASCII in
// its name: what it printed on each stream, its own exit status, and a
// timeout all come back exactly as before (on Windows, under CREATE_NO_WINDOW).
func TestABackgroundToolInASpacedUnicodeFolderKeepsItsOutputStatusAndTimeout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Programmi di prova — ünïcode", "bin dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "helper tool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	tool := filepath.Join(dir, name)
	copyExecutable(t, helperPath(t), tool)

	slow := modeAction("slow", "sleep", "60")
	slow.Timeout = 1
	h := newHarness(t, fixtureProfile(t, "test.executor.background",
		modeAction("ok", "echo", "ciao", "è", "fatto"),
		modeAction("fail", "stderr-exit", "4", "the compiler said no"),
		slow))
	request := func(action string) Request {
		r := h.helperRequest("test.executor.background", action)
		r.Executables = map[string]string{"helper": tool}
		return r
	}

	ok := h.runToEnd(request("ok"))
	if ok.State != Succeeded || !strings.Contains(h.mustLog(ok.ID, "stdout"), "ciao è fatto") {
		t.Fatalf("success: %s %q", ok.State, h.mustLog(ok.ID, "stdout"))
	}
	failed := h.runToEnd(request("fail"))
	if failed.State != Failed || failed.ExitCode == nil || *failed.ExitCode != 4 ||
		!strings.Contains(h.mustLog(failed.ID, "stderr"), "the compiler said no") {
		t.Fatalf("failure: %s %v %q", failed.State, failed.ExitCode, h.mustLog(failed.ID, "stderr"))
	}
	timedOut := h.runToEnd(request("slow"))
	if timedOut.State != Failed || !timedOut.TimedOut {
		t.Fatalf("timeout: %s timed out %v", timedOut.State, timedOut.TimedOut)
	}
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
