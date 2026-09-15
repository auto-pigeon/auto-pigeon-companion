package build

import (
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
)

// A build left `running` by a Companion that stopped.
//
// A build is several jobs, and the job store already knows how to tell a job
// whose supervisor died from one another process is supervising: its heartbeat
// (see [job.Store.Recover]). The build manifest had nothing of the kind, so a
// kill -9 mid-compile left the job `interrupted` and its build `running` for
// ever (NEW_244D), and the page offered a Cancel for a build nothing was doing.
//
// Reconcile reads the manifest's own step jobs. When the step the manifest says
// is running belongs to a job that is no longer active, the build is over and
// nobody recorded it: it becomes [job.Interrupted], carrying that step's state,
// and nothing is run again. A step whose job is still active — another
// Companion supervising it right now — leaves the build alone, and so does a
// manifest this function cannot read a job for.

// InterruptedNote is what a reconciled build says.
const InterruptedNote = "the Companion stopped while this build was running; nothing was run again"

// Reconcile updates an abandoned manifest in memory and reports whether it did.
// The caller saves it.
func Reconcile(m *Manifest, lookup func(id string) (*job.Job, error), now time.Time) bool {
	if m == nil || !m.State.Active() {
		return false
	}
	changed := false
	for i := range m.Steps {
		step := &m.Steps[i]
		if step.JobID == "" || !step.State.Active() {
			continue
		}
		record, err := lookup(step.JobID)
		if err != nil || record.State.Active() {
			return false
		}
		step.State = record.State
		step.Error = record.Error
		step.FinishedAt = record.FinishedAt
		changed = true
	}
	if !changed {
		return false
	}
	m.State = job.Interrupted
	m.Error = InterruptedNote
	if m.FinishedAt.IsZero() {
		m.FinishedAt = now.UTC()
	}
	return true
}
