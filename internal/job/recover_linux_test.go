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

func abandonedJob(t *testing.T, store *Store, process ProcessIdentity) string {
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
	if err := store.Heartbeat(id, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRecoveryStopsTheTreeACrashedCompanionLeftRunning is NEW_244D's kill -9:
// the compiler and its child outlived the Companion, and a restart marked
// nothing. Recovery now stops the verified group, and runs nothing again.
func TestRecoveryStopsTheTreeACrashedCompanionLeftRunning(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	tree := startTree(t)
	pid := tree.Process.Pid
	id := abandonedJob(t, store, ProcessIdentity{PID: pid, StartTicks: processStartTicks(pid)})

	if recovered, err := store.Recover(time.Now().UTC()); err != nil || len(recovered) != 1 {
		t.Fatalf("recovered %v (%v)", recovered, err)
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
}

// TestRecoveryNeverSignalsAProcessItCannotProveIsTheJobs: a pid whose start
// time does not match is somebody else's process that reused the number.
func TestRecoveryNeverSignalsAProcessItCannotProveIsTheJobs(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	tree := startTree(t)
	pid := tree.Process.Pid
	abandonedJob(t, store, ProcessIdentity{PID: pid, StartTicks: processStartTicks(pid) + 1})
	abandonedJob(t, store, ProcessIdentity{PID: pid})

	if recovered, err := store.Recover(time.Now().UTC()); err != nil || len(recovered) != 2 {
		t.Fatalf("recovered %v (%v)", recovered, err)
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
	id := abandonedJob(t, store, ProcessIdentity{})
	if recovered, err := store.Recover(time.Now().UTC(), id); err != nil || len(recovered) != 0 {
		t.Fatalf("recovered %v (%v) although this process supervises it", recovered, err)
	}
}
