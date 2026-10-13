package build

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

func TestABuildWhoseStepJobWasInterruptedIsInterrupted(t *testing.T) {
	m := &Manifest{State: job.Running, Steps: []Step{
		{ID: "compile", JobID: "j1", State: job.Running},
		{ID: "vis", State: job.Queued},
	}}
	jobs := map[string]*job.Job{"j1": {State: job.Interrupted, Error: "the Companion stopped", FinishedAt: time.Now()}}
	lookup := func(id string) (*job.Job, error) {
		if j, ok := jobs[id]; ok {
			return j, nil
		}
		return nil, errors.New("no job")
	}
	if !Reconcile(m, lookup, time.Now()) {
		t.Fatal("an abandoned build was not reconciled")
	}
	if m.State != job.Interrupted || m.Steps[0].State != job.Interrupted || m.Error != InterruptedNote || m.FinishedAt.IsZero() {
		t.Errorf("manifest = %+v", m)
	}
	if Reconcile(m, lookup, time.Now()) {
		t.Error("reconciling twice changed it again")
	}
}

func TestABuildAnotherCompanionIsRunningIsLeftAlone(t *testing.T) {
	m := &Manifest{State: job.Running, Steps: []Step{{ID: "compile", JobID: "j1", State: job.Running}}}
	live := func(string) (*job.Job, error) { return &job.Job{State: job.Running}, nil }
	unknown := func(string) (*job.Job, error) { return nil, job.ErrNotFound }
	if Reconcile(m, live, time.Now()) || Reconcile(m, unknown, time.Now()) || m.State != job.Running {
		t.Errorf("a build whose job is live or unreadable was changed: %s", m.State)
	}
}

// A step read back from its job keeps what the job measured.
func TestAReconciledStepKeepsItsDuration(t *testing.T) {
	started := time.Date(2026, 9, 21, 19, 9, 21, 0, time.UTC)
	exit := 0
	m := &Manifest{State: job.Running, Steps: []Step{{ID: "vis", JobID: "j1", State: job.Running}}}
	record := &job.Job{State: job.Succeeded, StartedAt: started,
		FinishedAt: started.Add(1486 * time.Millisecond), ExitCode: &exit}
	lookup := func(string) (*job.Job, error) { return record, nil }
	if !Reconcile(m, lookup, time.Now()) {
		t.Fatal("not reconciled")
	}
	step := m.Steps[0]
	if step.DurationMS != 1486 || !step.StartedAt.Equal(started) || step.ExitCode == nil || *step.ExitCode != 0 {
		t.Fatalf("step = duration %d, started %v, exit %v", step.DurationMS, step.StartedAt, step.ExitCode)
	}
}

// A step whose record was COPIED here (NEW_326) does not make its build say
// that the Companion stopped: the copy does not know that, and the build says
// what the step's own record says instead.
func TestACopiedBuildDoesNotClaimItsCompanionStopped(t *testing.T) {
	m := &Manifest{State: job.Running, Steps: []Step{{ID: "light", JobID: "j1", State: job.Running}}}
	copied := "this record was copied, moved or restored here from another job store while it said running; " +
		"no program was stopped, and nothing here knows how that run ended"
	record := &job.Job{State: job.Interrupted, Error: copied, FinishedAt: time.Now(),
		Recovery: &job.Recovery{Outcome: job.RecoveryForeignStore, Reason: "store_is_a_copy"}}
	if !Reconcile(m, func(string) (*job.Job, error) { return record, nil }, time.Now()) {
		t.Fatal("not reconciled")
	}
	if m.State != job.Interrupted || m.Error != copied {
		t.Fatalf("the copied build says %q", m.Error)
	}
	own := &Manifest{State: job.Running, Steps: []Step{{ID: "light", JobID: "j1", State: job.Running}}}
	record.Recovery = &job.Recovery{Outcome: job.RecoveryOwnedOrphanStopped}
	record.Error = "its programs were still running with nobody supervising them, and were stopped"
	Reconcile(own, func(string) (*job.Job, error) { return record, nil }, time.Now())
	if own.Error != InterruptedNote {
		t.Fatalf("the build of a genuinely stopped Companion says %q", own.Error)
	}
}

// NEW_326, found live: a build directory COPIED with the rest of a Companion's
// state carries a manifest whose `directory` is the ORIGINAL's absolute path.
// The copy's reconcile saved to that path and rewrote the original build's
// manifest as interrupted while the original was still building. A manifest
// read from disk is where it was read from; a recorded path never sends a
// write back to the store that wrote it.
func TestACopiedManifestIsWrittenOnlyWhereItWasRead(t *testing.T) {
	original, copied := t.TempDir(), t.TempDir()
	id := "20261013T073949Z-4f6fcd01"
	m := &Manifest{SchemaVersion: SchemaVersion, BuildID: id, State: job.Running,
		Steps:     []Step{{ID: "light", JobID: "j1", State: job.Running}},
		Directory: filepath.Join(original, id)}
	for _, root := range []string{original, copied} {
		if err := os.MkdirAll(filepath.Join(root, id), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := m.Save(filepath.Join(root, id)); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(filepath.Join(original, id, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}

	read, err := Find(copied, id)
	if err != nil {
		t.Fatal(err)
	}
	if read.Directory != filepath.Join(copied, id) {
		t.Fatalf("the copied manifest says it lives in %s", read.Directory)
	}
	listed, err := List(copied)
	if err != nil || len(listed) != 1 || listed[0].Directory != filepath.Join(copied, id) {
		t.Fatalf("listed %v (%v)", listed, err)
	}
	interrupted := &job.Job{State: job.Interrupted, Error: "copied", FinishedAt: time.Now(),
		Recovery: &job.Recovery{Outcome: job.RecoveryForeignStore}}
	if !Reconcile(read, func(string) (*job.Job, error) { return interrupted, nil }, time.Now()) {
		t.Fatal("not reconciled")
	}
	if err := read.Save(read.Directory); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(original, id, ManifestFileName))
	if string(after) != string(before) {
		t.Fatalf("reconciling the copy rewrote the original's manifest:\n%s", after)
	}
	if again, _ := Find(copied, id); again.State != job.Interrupted {
		t.Errorf("the copy's own manifest is %s", again.State)
	}
}
