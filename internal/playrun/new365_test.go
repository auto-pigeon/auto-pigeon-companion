package playrun_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
)

// NEW_365: one press is one run. Two presses, two tabs, or a press and a
// retry of the same build at the same moment start it once, and the refusal
// names the run that is going.
func TestTheSameBuildIsStartedOnceHoweverManyPressesArrive(t *testing.T) {
	h := newHarness(t)
	hold := make(chan struct{})
	h.fetchMap = func(ctx context.Context, r playrun.Request) (playrun.MapResult, error) {
		<-hold
		return playrun.MapResult{}, errors.New("stopped here by the test")
	}

	var group sync.WaitGroup
	results := make([]error, 8)
	ids := make([]string, 8)
	for i := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			record, err := h.service.Start(goodRequest())
			results[i] = err
			if record != nil {
				ids[i] = record.ID
			}
		}()
	}
	group.Wait()
	started, refused := "", 0
	for i, err := range results {
		var running *playrun.AlreadyRunningError
		switch {
		case err == nil:
			if started != "" {
				t.Fatalf("two runs started: %s and %s", started, ids[i])
			}
			started = ids[i]
		case errors.As(err, &running):
			refused++
		default:
			t.Fatalf("press %d: %v", i, err)
		}
	}
	if started == "" || refused != 7 {
		t.Fatalf("started %q, refused %d; want one run and seven refusals", started, refused)
	}
	for _, err := range results {
		var running *playrun.AlreadyRunningError
		if errors.As(err, &running) && running.RunID != started {
			t.Fatalf("a refusal named %s, not the running %s", running.RunID, started)
		}
	}
	// Another build profile is another build.
	other := goodRequest()
	other.PipelineID = "auto-pigeon.q1-fast"
	otherRun, err := h.service.Start(other)
	if err != nil {
		t.Fatalf("another build profile was refused: %v", err)
	}
	close(hold)
	h.await(started)
	h.await(otherRun.ID)
	// Once it has ended, the same build can be asked for again.
	again, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatalf("the same build after the first ended: %v", err)
	}
	h.await(again.ID)
}
