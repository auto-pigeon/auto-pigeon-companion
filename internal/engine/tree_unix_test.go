//go:build !windows

package engine_test

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Stopping a game stops the game, and stops what the game started, and stops
// nothing else.
//
// The last third is the part that is easy to get wrong and expensive to get
// wrong: an implementation that took down engines by name, or by executable
// path, would close somebody's other Quake — and on a machine where the
// Companion is one of several things running the same engine, that is somebody
// else's session ending because a build finished.
//
// Unix only. Killing a tree here is a process group and a negative pid; on
// Windows it is taskkill /T, which internal/job tests on its own terms.
func TestStoppingAGameTakesDownItsTreeAndNothingElse(t *testing.T) {
	h := fixtureHarness(t)

	// An unrelated engine, started by somebody else, running the same program.
	bystanderRecord := t.TempDir()
	bystander := exec.Command(os.Args[0], enginefixture.Flag,
		"--behaviour", string(enginefixture.BehaviourStay),
		"--linger", "60s",
		"--record", bystanderRecord+"/record.json")
	if err := bystander.Start(); err != nil {
		t.Fatalf("starting the unrelated engine: %v", err)
	}
	t.Cleanup(func() {
		_ = bystander.Process.Kill()
		_, _ = bystander.Process.Wait()
	})

	watched := &mirror{}
	submitted, err := h.service.SubmitWatched(job.Request{
		ProfileID: enginefixture.ProfileID,
		ActionID:  profile.ActionHostDedicated,
		Runtime:   map[string]string{"map_name": "dm3", "mod_name": "mymod"},
		Options: map[string]string{
			"behaviour": string(enginefixture.BehaviourStay),
			// One grandchild, so what is being tested is a *tree* and not a
			// single pid. A child that outlives its parent is the shape a
			// launcher script or a forking engine actually has.
			"children": "1",
		},
	}, watched)
	if err != nil {
		t.Fatalf("submitting: %v", err)
	}
	if submitted.SessionRole != profile.SessionDedicated {
		t.Errorf("a dedicated server was queued as %q", submitted.SessionRole)
	}

	// Wait for the engine to be up, and for the child it left behind.
	child := waitForChildPID(t, watched)
	if !alive(child) {
		t.Fatalf("the engine's child %d was never running", child)
	}

	if _, err := h.service.Cancel(submitted.ID); err != nil {
		t.Fatalf("cancelling: %v", err)
	}
	finished, err := h.service.Wait(context.Background(), submitted.ID)
	if err != nil {
		t.Fatalf("waiting: %v", err)
	}
	if finished.Succeeded() {
		t.Errorf("a cancelled server reported success")
	}

	deadline := time.Now().Add(15 * time.Second)
	for alive(child) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(child) {
		t.Errorf("the engine's child %d outlived the job", child)
	}
	if !alive(bystander.Process.Pid) {
		t.Errorf("stopping the job killed an unrelated engine (pid %d) running the same program", bystander.Process.Pid)
	}
}

// waitForChildPID reads the pid the fixture printed when it spawned a child.
func waitForChildPID(t *testing.T, watched *mirror) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(watched.String(), "\n") {
			if !strings.HasPrefix(line, "fixture child ") {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "fixture child ")))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the engine never reported a child process:\n%s", watched.String())
	return 0
}

// alive reports whether a process still exists. Signal 0 performs the existence
// and permission check without delivering anything.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
