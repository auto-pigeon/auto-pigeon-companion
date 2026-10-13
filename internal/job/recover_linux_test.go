//go:build linux

package job

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startTree starts `sh -c 'sleep 60 & wait'` in its own process group, the
// shape a compiler that forks a worker has, and returns the leader.
func startTree(t *testing.T) *exec.Cmd {
	t.Helper()
	tree := exec.Command("/bin/sh", "-c", "/bin/sleep 60 & wait")
	configureProcessGroup(tree)
	if err := tree.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-tree.Process.Pid, syscall.SIGKILL); _ = tree.Wait() })
	time.Sleep(100 * time.Millisecond)
	return tree
}

func abandonedJob(t *testing.T, store *Store, process ProcessIdentity, launch *Launch) string {
	t.Helper()
	id, err := NewID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	j := &Job{SchemaVersion: SchemaVersion, ID: id, State: Running, CreatedAt: time.Now().Add(-time.Hour).UTC(),
		Owner: Owner{PID: 999999}, Process: process}
	if err := store.Save(j); err != nil {
		t.Fatal(err)
	}
	if launch != nil {
		// Taken once the job's directory exists, as admit does, with the
		// supervisor replaced by the caller's.
		recorded := store.launch(id, "")
		recorded.Supervisor = launch.Supervisor
		j.Launch = recorded
		if err := store.Save(j); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Heartbeat(id, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return id
}

// goneSupervisor is a launch whose supervisor was a real process of this test
// that has since exited: what a crashed Companion leaves in its records.
func goneSupervisor(t *testing.T) *Launch {
	t.Helper()
	supervisor := exec.Command("/bin/sleep", "60")
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	identity := ProcessIdentity{PID: supervisor.Process.Pid, StartTicks: processStartTicks(supervisor.Process.Pid)}
	_ = supervisor.Process.Kill()
	_ = supervisor.Wait()
	return &Launch{Supervisor: identity}
}

// TestRecoveryStopsTheTreeACrashedCompanionLeftRunning is NEW_244D's kill -9:
// the compiler and its child outlived the Companion, and a restart marked
// nothing. Recovery stops the group when THIS store launched it and its
// supervisor is gone (NEW_326), and runs nothing again.
func TestRecoveryStopsTheTreeACrashedCompanionLeftRunning(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	tree := startTree(t)
	pid := tree.Process.Pid
	id := abandonedJob(t, store, ProcessIdentity{PID: pid, StartTicks: processStartTicks(pid)}, goneSupervisor(t))

	recovered, err := store.Recover(time.Now().UTC())
	if err != nil || len(recovered) != 1 || recovered[0].Outcome != RecoveryOwnedOrphanStopped || !recovered[0].Signalled {
		t.Fatalf("recovered %+v (%v)", recovered, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for processGroupAlive(pid) && time.Now().Before(deadline) {
		_, _ = syscall.Wait4(pid, nil, syscall.WNOHANG, nil)
		time.Sleep(20 * time.Millisecond)
	}
	if processGroupAlive(pid) {
		t.Error("the abandoned process group is still running after recovery")
	}
	after, _ := store.Load(id)
	if after.State != Interrupted || !strings.Contains(after.Error, "were stopped") {
		t.Errorf("state %s, error %q", after.State, after.Error)
	}
	if after.Recovery == nil || after.Recovery.Outcome != RecoveryOwnedOrphanStopped || !after.Recovery.Signalled {
		t.Errorf("the record keeps no recovery decision: %+v", after.Recovery)
	}
}

// TestRecoveryNeverSignalsAProcessItCannotProveIsTheJobs (R07): a pid whose
// start time does not match is somebody else's process that reused the number,
// and a record with no start time proves nothing. Both are read as
// unverifiable — said, not guessed — and neither is signalled.
func TestRecoveryNeverSignalsAProcessItCannotProveIsTheJobs(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	tree := startTree(t)
	pid := tree.Process.Pid
	reused := abandonedJob(t, store, ProcessIdentity{PID: pid, StartTicks: processStartTicks(pid) + 1}, goneSupervisor(t))
	unrecorded := abandonedJob(t, store, ProcessIdentity{PID: pid}, goneSupervisor(t))

	recovered, err := store.Recover(time.Now().UTC())
	if err != nil || len(recovered) != 2 {
		t.Fatalf("recovered %+v (%v)", recovered, err)
	}
	want := map[string]string{reused: "child_start_mismatch", unrecorded: "child_start_unrecorded"}
	for _, r := range recovered {
		if r.Outcome != RecoveryUnverifiable || r.Reason != want[r.ID] || r.Signalled {
			t.Errorf("%s: %+v, want unverifiable (%s), nothing signalled", r.ID, r, want[r.ID])
		}
		after, _ := store.Load(r.ID)
		if !strings.Contains(after.Error, "no program was stopped") {
			t.Errorf("%s says %q", r.ID, after.Error)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if !processGroupAlive(pid) {
		t.Error("recovery signalled a process whose identity did not match the job's")
	}
}

// TestRecoveryLeavesAJobThisProcessSupervises: a service whose own claims went
// stale (a laptop asleep past heartbeatStale) does not abandon its own build.
func TestRecoveryLeavesAJobThisProcessSupervises(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	id := abandonedJob(t, store, ProcessIdentity{}, nil)
	if recovered, err := store.Recover(time.Now().UTC(), id); err != nil || len(recovered) != 0 {
		t.Fatalf("recovered %v (%v) although this process supervises it", recovered, err)
	}
}
