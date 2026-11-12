package incident

import (
	"fmt"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// JobHook is the job service's OnFinished, reporting every FAILED job as
// `aucom.job_failed`.
//
// # What a failed job's report carries, and what it never does
//
// Subsystem says which kind of job (`job.tool` for a toolchain run,
// `job.engine` for a game the user owns being started), and operation says how
// it failed (`job.exit_nonzero`, `job.timeout`, `job.start_failed`). The message
// is one of three fixed sentences with at most an exit status or a time bound
// in it. The job's own error text is NOT used: it is written for the person at
// the keyboard and names executables, directories and arguments, which are
// facts about somebody's machine. Neither are the profile id, the command, the
// working directory, the inputs, the environment, the log, the account or the
// session token — the envelope has no field for any of them, and this function
// reads none of them.
//
// Cancelled and interrupted jobs are not failures and are not reported: the
// person asked for the first, and the second is this program shutting down.
//
// onRaised, when non-nil, is told about each incident raised (the fault command
// prints it).
func JobHook(r *Reporter, onRaised func(Incident)) func(*job.Job, string) {
	return func(j *job.Job, correlation string) {
		if r == nil || j == nil || j.State != job.Failed {
			return
		}
		// Somebody else decides whether this failure is one (a leak test's
		// compile step: a found leak exits non-zero and is the answer). They
		// report it through [ReportFailedJob] when it is.
		if j.Request.FailureReportedBy != "" {
			return
		}
		inc := r.capture(JobDraft(j, correlation), j.ID)
		if onRaised != nil {
			onRaised(inc)
		}
	}
}

// ReportFailedJob raises `aucom.job_failed` for a failed job whose report was
// left to its submitter (job.Request.FailureReportedBy), once that submitter
// has decided it IS a failure — the same incident [JobHook] would have raised,
// with the same three fixed sentences and nothing else. A job that did not
// fail raises nothing; reports whether one was raised.
func ReportFailedJob(r *Reporter, j *job.Job) (Incident, bool) {
	if r == nil || j == nil || j.State != job.Failed {
		return Incident{}, false
	}
	return r.capture(JobDraft(j, ""), j.ID), true
}

// JobDraft is the incident a failed job becomes. Exposed for tests.
func JobDraft(j *job.Job, correlation string) Draft {
	subsystem := "job.tool"
	if j.SessionRole != "" {
		subsystem = "job.engine"
	}
	operation, message := "job.failed", "A supervised job failed."
	switch {
	case j.TimedOut:
		operation = "job.timeout"
		message = "A supervised job was stopped because it exceeded its time bound."
		if j.TimeoutSeconds > 0 {
			message = fmt.Sprintf("A supervised job was stopped because it exceeded its time bound of %d seconds.", j.TimeoutSeconds)
		}
	// A program that exited 0 and still failed (an output it did not write)
	// did not exit non-zero; it is the plain "failed" below (NEW_503).
	case j.ExitCode != nil && *j.ExitCode != 0:
		operation = "job.exit_nonzero"
		message = fmt.Sprintf("A supervised job exited with status %d.", *j.ExitCode)
	case j.Command == nil:
		operation = "job.start_failed"
		message = "A supervised job could not be started."
	}
	return Draft{
		Severity:    SeverityError,
		Subsystem:   subsystem,
		Code:        CodeJobFailed,
		Operation:   operation,
		Message:     message,
		Duration:    j.Duration(),
		Correlation: correlation,
		UserAction:  "Open the job in the Companion to read its log.",
		Recoverable: true,
	}
}
