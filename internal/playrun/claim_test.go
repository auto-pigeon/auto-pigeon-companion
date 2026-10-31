package playrun_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
)

// A run's ending, held open (NEW_371D).
//
// The terminal record, the release of the request's claim and the goroutine's
// return are three moments, and what a person can do next depends on which of
// them they fall between. Release run 41205779413 failed in one such moment: a
// page read `succeeded`, pressed again, and was told the run it had just read
// the end of was "already running".
//
// None of that is produced by waiting. These tests hold a run at the log line
// that closes it — after its terminal record, before its goroutine returns,
// which is the window — or at the store write that publishes the record, and
// release it only when they have looked. They never sleep the window open and
// never poll for it to close.

// endingLine reports whether a line is the one closing a run.
func endingLine(line string) bool {
	return strings.Contains(line, " succeeded in ") || strings.HasSuffix(line, "(build only)") ||
		strings.Contains(line, ": failed at ") || strings.HasSuffix(line, ": cancelled") ||
		strings.Contains(line, "could not be written")
}

// holdNextEnding stops the first run to end at its closing log line. reached
// closes when one has got there; release lets it go. A test that fails before
// releasing still lets it go at cleanup.
func (h *harness) holdNextEnding() (reached <-chan struct{}, release func()) {
	h.t.Helper()
	arrived, proceed := make(chan struct{}), make(chan struct{})
	var once, releasing sync.Once
	release = func() { releasing.Do(func() { close(proceed) }) }
	h.t.Cleanup(release)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logged = func(line string) {
		if endingLine(line) {
			once.Do(func() {
				close(arrived)
				<-proceed
			})
		}
	}
	return arrived, release
}

func waitFor(t *testing.T, what string, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

// holdCompile makes the compile wait for its context, or for proceed.
func (h *harness) holdCompile() (compiling <-chan struct{}, proceed func()) {
	h.t.Helper()
	started, released := make(chan struct{}), make(chan struct{})
	var once, releasing sync.Once
	proceed = func() { releasing.Do(func() { close(released) }) }
	h.t.Cleanup(proceed)
	succeed := h.buildRun
	h.buildRun = func(ctx context.Context, r build.Request, a func(*build.Manifest)) (*build.Manifest, error) {
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-released:
			return succeed(ctx, r, a)
		}
	}
	return started, proceed
}

func (h *harness) idle() {
	h.t.Helper()
	if !h.deps.Live.WaitIdle(10 * time.Second) {
		h.t.Fatalf("still running: %v", h.deps.Live.Running())
	}
}

func buildOnlyRequest() playrun.Request {
	request := goodRequest()
	request.BuildOnly = true
	return request
}

// Every exit: once the record says it ended, the same build starts again —
// while the run that ended has not finished returning.
func TestAnEndedRunCanBeAskedForAgainBeforeItsGoroutineReturns(t *testing.T) {
	for _, exit := range []struct {
		name    string
		request func() playrun.Request
		set     func(h *harness)
		end     func(h *harness, id string)
		want    playrun.State
	}{
		{name: "launched", request: goodRequest, want: playrun.Succeeded},
		{name: "built only", request: buildOnlyRequest, want: playrun.Succeeded},
		{name: "failed", request: goodRequest, want: playrun.Failed, set: func(h *harness) {
			h.buildRun = func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
				return nil, errors.New("the compiler said no")
			}
		}},
		{name: "cancelled", request: goodRequest, want: playrun.Cancelled, set: func(h *harness) {
			h.holdCompile()
		}, end: func(h *harness, id string) {
			if err := h.service.Cancel(id); err != nil {
				h.t.Fatal(err)
			}
		}},
	} {
		t.Run(exit.name, func(t *testing.T) {
			h := newHarness(t)
			succeed := h.buildRun
			if exit.set != nil {
				exit.set(h)
			}
			ending, release := h.holdNextEnding()
			first, err := h.service.Start(exit.request())
			if err != nil {
				t.Fatal(err)
			}
			if exit.end != nil {
				exit.end(h, first.ID)
			}
			waitFor(t, "the run's closing line", ending)
			h.buildRun = succeed

			if ended := h.await(first.ID); ended.State != exit.want {
				t.Fatalf("state = %s (%s), want %s", ended.State, ended.Error, exit.want)
			}
			again, err := h.service.Start(exit.request())
			if err != nil {
				t.Fatalf("the same build, after a read of the first's end: %v", err)
			}
			if again.ID == first.ID {
				t.Fatal("no new run: the request was answered with the one that ended")
			}
			if second := h.await(again.ID); second.State != playrun.Succeeded {
				t.Fatalf("the new run: %s: %s", second.State, second.Error)
			}
			release()
			h.idle()
			if still := h.await(first.ID); still.State != exit.want {
				t.Errorf("the first record changed to %s", still.State)
			}
		})
	}
}

// Retry in the same window: a new attempt linked to the old one, and pressing
// again is answered with that attempt.
func TestARetryInTheEndingWindowIsTheOneLinkedAttempt(t *testing.T) {
	for _, exit := range []struct {
		name string
		want playrun.State
		set  func(h *harness)
		end  func(h *harness, id string)
	}{
		{name: "failed", want: playrun.Failed, set: func(h *harness) {
			h.buildRun = func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
				return nil, errors.New("the compiler said no")
			}
		}},
		{name: "cancelled", want: playrun.Cancelled, set: func(h *harness) { h.holdCompile() },
			end: func(h *harness, id string) {
				if err := h.service.Cancel(id); err != nil {
					h.t.Fatal(err)
				}
			}},
	} {
		t.Run(exit.name, func(t *testing.T) {
			h := newHarness(t)
			succeed := h.buildRun
			exit.set(h)
			ending, release := h.holdNextEnding()
			first, err := h.service.Start(goodRequest())
			if err != nil {
				t.Fatal(err)
			}
			if exit.end != nil {
				exit.end(h, first.ID)
			}
			waitFor(t, "the run's closing line", ending)
			h.buildRun = succeed
			if ended := h.await(first.ID); ended.State != exit.want {
				t.Fatalf("state = %s", ended.State)
			}

			retried, err := h.service.Retry(first.ID)
			if err != nil {
				t.Fatalf("retrying a run a read says ended: %v", err)
			}
			if retried.ID == first.ID || retried.RetryOf != first.ID {
				t.Fatalf("retry %s of %q, want a new run linked to %s", retried.ID, retried.RetryOf, first.ID)
			}
			again, err := h.service.Retry(first.ID)
			if err != nil || again.ID != retried.ID {
				t.Fatalf("a second press = %+v, %v; want the retry that exists", again, err)
			}
			h.await(retried.ID)
			release()
			h.idle()
		})
	}
}

// The other half of the claim's job, across two coordinators the way
// internal/web builds one per request: while a run is working, however many
// presses arrive on however many Services, one run starts and every refusal
// names it. Another build is another build.
func TestDuplicatesAreRefusedAcrossServicesSharingOneRegistry(t *testing.T) {
	h := newHarness(t)
	other, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	compiling, proceed := h.holdCompile()

	const presses = 12
	var group sync.WaitGroup
	ids, refusals := make([]string, presses), make([]string, presses)
	for i := range presses {
		group.Add(1)
		go func() {
			defer group.Done()
			service := h.service
			if i%2 == 1 {
				service = other
			}
			record, err := service.Start(goodRequest())
			var running *playrun.AlreadyRunningError
			switch {
			case err == nil:
				ids[i] = record.ID
			case errors.As(err, &running):
				refusals[i] = running.RunID
			default:
				t.Errorf("press %d: %v", i, err)
			}
		}()
	}
	group.Wait()
	waitFor(t, "the compile", compiling)
	started := ""
	for i := range presses {
		if ids[i] == "" {
			continue
		}
		if started != "" {
			t.Fatalf("two runs started: %s and %s", started, ids[i])
		}
		started = ids[i]
	}
	if started == "" {
		t.Fatal("no run started")
	}
	for i := range presses {
		if ids[i] == "" && refusals[i] != started {
			t.Errorf("press %d was told %q, not the running %s", i, refusals[i], started)
		}
	}

	request := goodRequest()
	request.PipelineID = "auto-pigeon.q1-fast"
	different, err := other.Start(request)
	if err != nil {
		t.Fatalf("a different build was refused: %v", err)
	}
	proceed()
	h.await(started)
	h.await(different.ID)
	h.idle()
}

// Retry's "is there one yet?" and its starting one are one step for the whole
// process, not for one coordinator. Without that, the first of two presses can
// read "none yet", the second can start a retry and see it end, and the first
// then starts another: two attempts for one press of Try again.
func TestTwoServicesRetryingOneRunStartOneAttempt(t *testing.T) {
	h := newHarness(t)
	h.buildRun = func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
		return nil, errors.New("the compiler said no")
	}
	first, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	h.await(first.ID)
	h.idle()

	// The first coordinator stops inside its Retry, past "is there one yet?",
	// at the clock its start reads first.
	asked, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var armed atomic.Bool
	deps := h.deps
	deps.Now = func() time.Time {
		if armed.Load() {
			once.Do(func() {
				close(asked)
				<-resume
			})
		}
		return time.Now().UTC()
	}
	slow, err := playrun.NewService(h.store, deps)
	if err != nil {
		t.Fatal(err)
	}
	quick, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}

	armed.Store(true)
	type answer struct {
		record *playrun.Record
		err    error
	}
	slowAnswer, quickAnswer := make(chan answer, 1), make(chan answer, 1)
	go func() {
		record, err := slow.Retry(first.ID)
		slowAnswer <- answer{record, err}
	}()
	waitFor(t, "the first press reaching its start", asked)
	go func() {
		record, err := quick.Retry(first.ID)
		quickAnswer <- answer{record, err}
	}()
	// Long enough for a second press that is NOT waiting to have started and
	// ended; a second press that is waiting gives this up and is released
	// below. The result does not depend on this delay: a wrong implementation
	// is what it gives room to.
	quicker := answer{}
	select {
	case quicker = <-quickAnswer:
	case <-time.After(300 * time.Millisecond):
	}
	if quicker.record != nil {
		h.await(quicker.record.ID)
		h.idle()
	}
	close(resume)
	slower := <-slowAnswer
	if quicker.record == nil && quicker.err == nil {
		quicker = <-quickAnswer
	}
	h.idle()

	if slower.err != nil || quicker.err != nil {
		t.Fatalf("answers: %v and %v", slower.err, quicker.err)
	}
	if slower.record.ID != quicker.record.ID {
		t.Fatalf("one press of Try again became two attempts: %s and %s", slower.record.ID, quicker.record.ID)
	}
	retries := 0
	records, err := h.service.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.RetryOf == first.ID {
			retries++
		}
	}
	if retries != 1 {
		t.Fatalf("%d attempts linked to %s, want one", retries, first.ID)
	}
}

// A crowd of presses of Try again, across coordinators, while the retry works:
// one attempt, and every press is answered with it.
func TestACrowdOfRetriesAcrossServicesIsOneAttempt(t *testing.T) {
	h := newHarness(t)
	h.buildRun = func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
		return nil, errors.New("the compiler said no")
	}
	first, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	h.await(first.ID)
	h.idle()
	h.buildRun = newHarness(t).buildRun
	compiling, proceed := h.holdCompile()
	other, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}

	const presses = 12
	var group sync.WaitGroup
	answers := make([]string, presses)
	for i := range presses {
		group.Add(1)
		go func() {
			defer group.Done()
			service := h.service
			if i%2 == 1 {
				service = other
			}
			record, err := service.Retry(first.ID)
			var running *playrun.AlreadyRunningError
			switch {
			case err == nil:
				answers[i] = record.ID
			case errors.As(err, &running):
				answers[i] = running.RunID
			default:
				t.Errorf("press %d: %v", i, err)
			}
		}()
	}
	group.Wait()
	waitFor(t, "the retry's compile", compiling)
	for _, answer := range answers {
		if answer != answers[0] || answer == first.ID {
			t.Fatalf("answers %v, want one new attempt", answers)
		}
	}
	proceed()
	h.await(answers[0])
	h.idle()
	records, err := h.service.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("%d records, want the first and one retry", len(records))
	}
}

// A Cancel holding an old read must not write over what the run ended as: the
// read said `compiling`, the run then ended and its worker went away, and
// "active with no worker" is exactly what a run a stopped Companion abandoned
// looks like.
func TestACancelHoldingAnOldReadCannotRewriteTheEnding(t *testing.T) {
	for _, exit := range []struct {
		name string
		want playrun.State
		set  func(h *harness, release func())
	}{
		{name: "succeeded", want: playrun.Succeeded},
		{name: "failed", want: playrun.Failed, set: func(h *harness, _ func()) {
			h.launch = func(context.Context, playrun.Request) (playrun.LaunchRecord, error) {
				return playrun.LaunchRecord{}, errors.New("the engine said no")
			}
		}},
	} {
		t.Run(exit.name, func(t *testing.T) {
			h := newHarness(t)
			compiling, proceed := h.holdCompile()
			if exit.set != nil {
				exit.set(h, proceed)
			}
			read, resume := make(chan struct{}), make(chan struct{})
			playrun.HoldCancelAfterRead(h.service, func() {
				close(read)
				<-resume
			})
			first, err := h.service.Start(goodRequest())
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the compile", compiling)

			cancelled := make(chan error, 1)
			go func() { cancelled <- h.service.Cancel(first.ID) }()
			waitFor(t, "Cancel's read", read)
			proceed()
			if ended := h.await(first.ID); ended.State != exit.want {
				t.Fatalf("state = %s (%s)", ended.State, ended.Error)
			}
			h.idle()
			close(resume)

			if err := <-cancelled; err == nil || !strings.Contains(err.Error(), "already") {
				t.Errorf("Cancel of a run that had ended said %v", err)
			}
			if still := h.await(first.ID); still.State != exit.want {
				t.Fatalf("a stale Cancel rewrote %s as %s", exit.want, still.State)
			}
		})
	}
}

// A Cancel that arrives while the terminal record is being written waits for
// the write and answers from it.
func TestACancelDuringTheTerminalWriteAnswersFromIt(t *testing.T) {
	h := newHarness(t)
	writing, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	playrun.HoldStoreSaves(h.store, func(record *playrun.Record) error {
		if record.State.Terminal() {
			once.Do(func() {
				close(writing)
				<-proceed
			})
		}
		return nil
	})
	read := make(chan struct{})
	playrun.HoldCancelAfterRead(h.service, func() { close(read) })
	first, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the terminal write", writing)
	if record, err := h.service.Get(first.ID); err != nil || record.State.Terminal() {
		t.Fatalf("the record before its terminal write: %+v, %v", record, err)
	}

	cancelled := make(chan error, 1)
	go func() { cancelled <- h.service.Cancel(first.ID) }()
	waitFor(t, "Cancel's read", read)
	close(proceed)

	if err := <-cancelled; err == nil || !strings.Contains(err.Error(), "already succeeded") {
		t.Errorf("Cancel across the terminal write said %v", err)
	}
	if ended := h.await(first.ID); ended.State != playrun.Succeeded {
		t.Fatalf("state = %s", ended.State)
	}
	h.idle()
}

// A run nothing in this process is running is still cancellable, from a read
// that was current when it was decided on: that is the contract the stale case
// above must not cost.
func TestAnAbandonedRunIsStillCancelled(t *testing.T) {
	h := newHarness(t)
	abandoned := &playrun.Record{
		SchemaVersion: playrun.SchemaVersion, ID: "20260921T000000Z-abandoned",
		Request: goodRequest(), State: playrun.Compiling, CreatedAt: time.Now().UTC(),
		Stages: []playrun.Stage{{State: playrun.Compiling, StartedAt: time.Now().UTC()}},
	}
	if err := h.store.Save(abandoned); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Cancel(abandoned.ID); err != nil {
		t.Fatal(err)
	}
	record, err := h.service.Get(abandoned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != playrun.Cancelled || record.FinishedAt.IsZero() ||
		record.Stages[0].FinishedAt.IsZero() || record.Stages[0].Error == "" {
		t.Fatalf("the abandoned run: %+v", record)
	}
	if err := h.service.Cancel(abandoned.ID); err == nil {
		t.Fatal("cancelling it twice was accepted")
	}
}

// When the record that says how a run ended cannot be written: the claim still
// goes, the log says what happened and does not say it succeeded, and the disk
// keeps the last record that was written — which is an active one, with no
// worker, and so the same thing an abandoned run is to Cancel and to Recover.
func TestATerminalRecordThatCannotBeWrittenLeavesNoClaim(t *testing.T) {
	for _, exit := range []struct {
		name     string
		set      func(h *harness)
		end      func(h *harness, id string)
		neverSay string
	}{
		{name: "launched", neverSay: " succeeded in "},
		{name: "failed", neverSay: ": failed at ", set: func(h *harness) {
			h.buildRun = func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
				return nil, errors.New("the compiler said no")
			}
		}},
		{name: "cancelled", neverSay: ": cancelled", set: func(h *harness) { h.holdCompile() },
			end: func(h *harness, id string) {
				if err := h.service.Cancel(id); err != nil {
					h.t.Fatal(err)
				}
			}},
	} {
		t.Run(exit.name, func(t *testing.T) {
			h := newHarness(t)
			succeed := h.buildRun
			if exit.set != nil {
				exit.set(h)
			}
			var writing atomic.Bool
			writing.Store(true)
			refused := make(chan struct{}, 1)
			playrun.HoldStoreSaves(h.store, func(record *playrun.Record) error {
				if writing.Load() && record.State.Terminal() {
					select {
					case refused <- struct{}{}:
					default:
					}
					return errors.New("the disk is full")
				}
				return nil
			})
			var mu sync.Mutex
			var lines []string
			arrived, proceed := make(chan struct{}), make(chan struct{})
			var once sync.Once
			var releasing sync.Once
			release := func() { releasing.Do(func() { close(proceed) }) }
			t.Cleanup(release)
			h.mu.Lock()
			h.logged = func(line string) {
				mu.Lock()
				lines = append(lines, line)
				mu.Unlock()
				if endingLine(line) || strings.Contains(line, "writing the record") {
					once.Do(func() {
						close(arrived)
						<-proceed
					})
				}
			}
			h.mu.Unlock()

			first, err := h.service.Start(goodRequest())
			if err != nil {
				t.Fatal(err)
			}
			if exit.end != nil {
				exit.end(h, first.ID)
			}
			waitFor(t, "the failed write being logged", arrived)
			writing.Store(false)
			h.buildRun = succeed

			again, err := h.service.Start(goodRequest())
			if err != nil {
				t.Fatalf("a run that could not record its ending kept its claim: %v", err)
			}
			h.await(again.ID)
			release()
			h.idle()

			record, err := h.service.Get(first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if record.State.Terminal() {
				t.Fatalf("the disk says %s, and that write failed", record.State)
			}
			mu.Lock()
			defer mu.Unlock()
			said := strings.Join(lines, "\n")
			if !strings.Contains(said, "could not be written") || !strings.Contains(said, "the disk is full") {
				t.Errorf("the failure was not reported:\n%s", said)
			}
			for _, line := range lines {
				if strings.Contains(line, first.ID) && strings.Contains(line, exit.neverSay) &&
					!strings.Contains(line, "could not be written") {
					t.Errorf("the log reports an ending that was not recorded: %q", line)
				}
			}
			if err := h.service.Cancel(first.ID); err != nil {
				t.Fatalf("what the failed write left is not cancellable: %v", err)
			}
		})
	}
}

// The log callback runs outside every lock: from it, the registry, the store
// and another run are all reachable, on the normal path and the failed one.
func TestTheClosingLogLineCanCallBackIntoTheCoordinator(t *testing.T) {
	for _, failing := range []bool{false, true} {
		h := newHarness(t)
		if failing {
			playrun.HoldStoreSaves(h.store, func(record *playrun.Record) error {
				if record.State.Terminal() {
					return errors.New("the disk is full")
				}
				return nil
			})
		}
		var once sync.Once
		called := make(chan error, 1)
		h.mu.Lock()
		h.logged = func(line string) {
			if !endingLine(line) {
				return
			}
			once.Do(func() {
				h.deps.Live.Running()
				_, _ = h.service.List()
				_, err := h.service.Start(goodRequest())
				called <- err
			})
		}
		h.mu.Unlock()
		if _, err := h.service.Start(goodRequest()); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-called:
			if err != nil {
				t.Fatalf("starting from the closing line (failing=%v): %v", failing, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the closing line deadlocked (failing=%v)", failing)
		}
		h.idle()
	}
}

// While an ended run's goroutine is on its way out and a new run of the same
// build is going, each listing says the true thing: Running is the work, the
// new run; a Cancel and a CancelAll reach that run and leave the ended record
// alone; and WaitIdle still waits for the goroutine.
func TestListingsStayTrueWhileARunEndsAndItsSuccessorStarts(t *testing.T) {
	h := newHarness(t)
	ending, release := h.holdNextEnding()
	first, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run's closing line", ending)
	h.await(first.ID)

	compiling, _ := h.holdCompile()
	second, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatalf("the same build after the first ended: %v", err)
	}
	waitFor(t, "the second compile", compiling)

	running := h.deps.Live.Running()
	if len(running) != 1 || running[0].ID != second.ID {
		t.Fatalf("Running = %v, want only %s", running, second.ID)
	}
	if err := h.service.Cancel(first.ID); err == nil || !strings.Contains(err.Error(), "already succeeded") {
		t.Errorf("cancelling the ended run said %v", err)
	}

	// A press naming the new run's request is refused with the new run.
	_, err = h.service.Start(goodRequest())
	var refused *playrun.AlreadyRunningError
	if !errors.As(err, &refused) || refused.RunID != second.ID {
		t.Fatalf("a duplicate of the new run: %v", err)
	}

	h.deps.Live.CancelAll()
	if stopped := h.await(second.ID); stopped.State != playrun.Cancelled {
		t.Fatalf("CancelAll did not reach the new run: %s", stopped.State)
	}
	if h.deps.Live.WaitIdle(50 * time.Millisecond) {
		t.Fatal("WaitIdle said idle while a goroutine was still returning")
	}
	release()
	h.idle()
	if ended := h.await(first.ID); ended.State != playrun.Succeeded {
		t.Fatalf("the first run's record changed to %s", ended.State)
	}
}
