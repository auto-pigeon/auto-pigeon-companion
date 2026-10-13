package build

import (
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
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

// InterruptedNote is what a reconciled build says when its step's own
// Companion stopped. A step whose record was copied, moved or could not be
// verified says its own sentence instead (NEW_326): a copy of a store does not
// know that the Companion running the original stopped, and must not say so.
const InterruptedNote = "the Companion stopped while this build was running; nothing was run again"

// Reconcile updates an abandoned manifest in memory and reports whether it did.
// The caller saves it.
func Reconcile(m *Manifest, lookup func(id string) (*job.Job, error), now time.Time) bool {
	if m == nil || !m.State.Active() {
		return false
	}
	changed := false
	note := InterruptedNote
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
		// Everything the job measured, not only how it ended: a step read back
		// with its state and no duration printed `vis succeeded 0ms` for a vis
		// that ran 1.5 s (246I1.1, a build cancelled between two steps).
		step.StartedAt, step.FinishedAt = record.StartedAt, record.FinishedAt
		if !record.FinishedAt.IsZero() {
			step.DurationMS = record.Duration().Milliseconds()
		}
		step.ExitCode = record.ExitCode
		if record.Recovery != nil && !record.Recovery.OwnSupervisorStopped() && record.Error != "" {
			note = record.Error
		}
		changed = true
	}
	if !changed {
		return false
	}
	m.State = job.Interrupted
	m.Error = note
	if m.FinishedAt.IsZero() {
		m.FinishedAt = now.UTC()
	}
	return true
}
