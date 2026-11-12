package job

import (
	"sync"
	"testing"
	"time"
)

// A submission that names who reports its failure (a leak test's compile step,
// NEW_503) reaches OnFinished saying so — although the stored record, decoded
// strictly by every Companion, never carries it.
func TestTheSubmitterOfAJobsFailureReachesOnFinished(t *testing.T) {
	var mu sync.Mutex
	owners := map[string]string{}
	h := newHarness(t, fixtureProfile(t, "test.executor.exit", modeAction("exit", "exit", "3")), func(o *Options) {
		o.OnFinished = func(j *Job, _ string) {
			mu.Lock()
			defer mu.Unlock()
			owners[j.ID] = j.Request.FailureReportedBy
		}
	})
	owned := h.helperRequest("test.executor.exit", "exit")
	owned.FailureReportedBy = "build.leak_test"
	first := h.runToEnd(owned)
	second := h.runToEnd(h.helperRequest("test.executor.exit", "exit"))
	// OnFinished runs on the worker after the record is saved, so Wait may
	// return a moment before it has been told.
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		mu.Lock()
		told := len(owners)
		mu.Unlock()
		if told == 2 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if first.State != Failed || owners[first.ID] != "build.leak_test" {
		t.Fatalf("the owned job reached OnFinished as %q (%s)", owners[first.ID], first.State)
	}
	if got, ok := owners[second.ID]; !ok || got != "" {
		t.Fatalf("an ordinary job reached OnFinished owned by %q (seen %v)", got, ok)
	}
	stored, err := h.store.Load(first.ID)
	if err != nil || stored.Request.FailureReportedBy != "" {
		t.Fatalf("the stored record carries the owner: %v %+v", err, stored.Request)
	}
}
