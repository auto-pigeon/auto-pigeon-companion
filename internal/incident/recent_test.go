package incident

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// What the page may offer to report (NEW_247H): the incidents this process
// raised, newest first, bounded, with a failed job's id — whether or not a
// telemetry backend is configured.
func TestRecentOffersRaisedIncidentsNewestFirstAndBounded(t *testing.T) {
	var nilReporter *Reporter
	if got := nilReporter.Recent(); got == nil || len(got) != 0 {
		t.Errorf("a nil reporter offers %v", got)
	}

	r := NewReporter(Config{Release: "1.150", Environment: "production"}, Options{})
	exit := 1
	JobHook(r, nil)(&job.Job{ID: "job-a", State: job.Failed, ExitCode: &exit}, "")
	JobHook(r, nil)(&job.Job{ID: "job-ok", State: job.Succeeded}, "")
	r.Capture(ReadinessDraft(errors.New("refused"), time.Second, 5*time.Second, ""))

	got := r.Recent()
	if len(got) != 2 {
		t.Fatalf("%d incidents offered, want 2 (a succeeded job raises nothing): %+v", len(got), got)
	}
	if got[0].Code != CodeReadinessFailed || got[0].JobID != "" || got[0].Subsystem != "aub.link" {
		t.Errorf("newest: %+v", got[0])
	}
	if got[1].Code != CodeJobFailed || got[1].JobID != "job-a" || got[1].Operation != "job.exit_nonzero" || got[1].Severity != SeverityError {
		t.Errorf("the failed job: %+v", got[1])
	}
	for _, entry := range got {
		if !IsCorrelationID(entry.IncidentID) || !IsCorrelationID(entry.CorrelationID) || entry.OccurredAt == "" {
			t.Errorf("ids missing: %+v", entry)
		}
	}

	for i := 0; i < RecentDepth+5; i++ {
		JobHook(r, nil)(&job.Job{ID: fmt.Sprintf("job-%02d", i), State: job.Failed, ExitCode: &exit}, "")
	}
	got = r.Recent()
	if len(got) != RecentDepth {
		t.Fatalf("%d offered, bound is %d", len(got), RecentDepth)
	}
	if got[0].JobID != fmt.Sprintf("job-%02d", RecentDepth+4) {
		t.Errorf("the newest is %q", got[0].JobID)
	}
}
