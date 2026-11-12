package incident

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// A job whose failure its submitter reports (a leak test's compile step,
// NEW_503) raises nothing through the hook; the submitter raises the same
// incident through ReportFailedJob only when it decides it is one.
func TestADeferredJobFailureIsRaisedOnlyByItsSubmitter(t *testing.T) {
	var log lines
	reporter := NewReporter(Config{Release: "unknown", Environment: EnvironmentTest}, Options{Logf: log.logf})
	deferred := failedJob()
	deferred.Request.FailureReportedBy = "build.leak_test"
	JobHook(reporter, nil)(deferred, "")
	if len(reporter.Recent()) != 0 || strings.Contains(log.text(), "raised") {
		t.Fatalf("the hook reported a job its submitter owns:\n%s", log.text())
	}
	succeeded := failedJob()
	succeeded.State = job.Succeeded
	if _, raised := ReportFailedJob(reporter, succeeded); raised {
		t.Fatal("a job that did not fail was reported")
	}
	inc, raised := ReportFailedJob(reporter, deferred)
	recent := reporter.Recent()
	if !raised || inc.Code != CodeJobFailed || len(recent) != 1 || recent[0].JobID != deferred.ID ||
		recent[0].Operation != "job.exit_nonzero" {
		t.Fatalf("the submitter's report: %+v %v", inc, recent)
	}
}

// A program that exited 0 and still failed (an output it never wrote) is not
// reported as a non-zero exit (NEW_503: Q3Map2 exits 0 on a leaked map).
func TestAJobThatExitedZeroAndFailedIsNotANonZeroExit(t *testing.T) {
	j := failedJob()
	zero := 0
	j.ExitCode = &zero
	if draft := JobDraft(j, ""); draft.Operation == "job.exit_nonzero" || strings.Contains(draft.Message, "status 0") {
		t.Fatalf("exit 0 reported as %s: %q", draft.Operation, draft.Message)
	}
}
