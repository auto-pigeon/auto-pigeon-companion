package build

import (
	"errors"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
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
