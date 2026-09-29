package autobuild

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// NEW_265A: a slow AUB must not hold up a person's action, and an answer that
// arrives after that action must not undo it. Every wait here has a deadline,
// so a regression fails instead of hanging the suite.

// prompt is how long a person's action may take while AUB is held. The
// actions are a read-change-write of a small local file; before NEW_265A they
// waited for the whole question to AUB (up to 20 s per due map).
const prompt = time.Second

// deadline bounds every other wait in these tests.
const deadline = 10 * time.Second

// gate holds the questions to AUB about chosen maps until released. The
// answer is taken when the question is asked, so what it releases is the
// answer AUB gave THEN, however the map changed afterwards.
type gate struct {
	mu       sync.Mutex
	held     map[string]chan struct{}
	entered  chan string
	inFlight int
	maxSeen  int
	calls    map[string]int
	once     map[string]bool
}

// holdCurrent wraps the fixture's AUB in a gate on svc. With once, only the
// first question about each held map waits; later ones pass.
func holdCurrent(f *fixture, svc *Service, once bool, assets ...string) *gate {
	g := &gate{held: map[string]chan struct{}{}, entered: make(chan string, 64), calls: map[string]int{}, once: map[string]bool{}}
	for _, asset := range assets {
		g.held[asset] = make(chan struct{})
	}
	inner := svc.deps.Current
	svc.deps.Current = func(ctx context.Context, asset string) (Revision, string, error) {
		if limit, ok := ctx.Deadline(); !ok || time.Until(limit) > checkTimeout {
			f.t.Errorf("a question about %s was asked without the %s bound (deadline %v, %v)", asset, checkTimeout, limit, ok)
		}
		revision, name, err := inner(ctx, asset)
		g.mu.Lock()
		g.calls[asset]++
		release, held := g.held[asset]
		if held && once && g.once[asset] {
			held = false
		}
		g.once[asset] = true
		g.inFlight++
		if g.inFlight > g.maxSeen {
			g.maxSeen = g.inFlight
		}
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			g.inFlight--
			g.mu.Unlock()
		}()
		g.entered <- asset
		if held {
			select {
			case <-release:
			case <-ctx.Done():
				return Revision{}, "", ctx.Err()
			}
		}
		return revision, name, err
	}
	return g
}

func (g *gate) release(asset string) { close(g.held[asset]) }

func (g *gate) count(asset string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[asset]
}

// await waits for the question about asset to be in flight.
func (g *gate) await(t *testing.T, asset string) {
	t.Helper()
	timer := time.After(deadline)
	for {
		select {
		case got := <-g.entered:
			if got == asset {
				return
			}
		case <-timer:
			t.Fatalf("the question about %s was never asked", asset)
		}
	}
}

// background runs fn and returns a channel closed when it returns.
func background(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

func wait(t *testing.T, done <-chan struct{}, within time.Duration, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("%s did not finish within %s", what, within)
	}
}

// timed runs a person's action and fails if it is not prompt.
func timed(t *testing.T, what string, action func() error) time.Duration {
	t.Helper()
	var err error
	began := time.Now()
	wait(t, background(func() { err = action() }), prompt, what+" while AUB was held")
	took := time.Since(began)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	t.Logf("%s took %s while a question to AUB was held", what, took)
	return took
}

func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

// baselined is m1..mN switched on with their baseline taken at revision 1.
func baselined(t *testing.T, assets ...string) *fixture {
	f := newFixture(t)
	for _, asset := range assets {
		f.save(asset, rev(1))
		if _, err := f.svc.Enable(asset, "", "fast"); err != nil {
			t.Fatal(err)
		}
	}
	f.poll()
	// More maps than MaxConcurrentChecks take more than one pass.
	for pass := 0; pass < len(assets); pass++ {
		if err := f.svc.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, asset := range assets {
		if entry := f.entry(asset); entry.Baseline == nil || entry.Baseline.Number != 1 {
			t.Fatalf("%s baseline %+v", asset, entry.Baseline)
		}
	}
	return f
}

func TestSwitchingOffWhileAUBIsSlowIsPromptAndItsAnswerIsNotUsed(t *testing.T) {
	for _, via := range []string{"page", "cli"} {
		t.Run(via, func(t *testing.T) {
			f := baselined(t, "m1")
			g := holdCurrent(f, f.svc, false, "m1")
			actor := f.svc
			if via == "cli" {
				// A second Service over the same file: another process.
				actor = f.service()
			}
			f.save("m1", rev(2))
			f.advance(CheckInterval + time.Second)
			ticked := background(func() { f.svc.Tick(context.Background()) })
			g.await(t, "m1")
			if check, found := f.svc.CheckingNow("m1"); !found || check.Generation != f.entry("m1").Generation {
				t.Errorf("the question in flight is not reported: %+v %v", check, found)
			}

			timed(t, "Disable ("+via+")", func() error { _, err := actor.Disable("m1"); return err })
			if entry := f.entry("m1"); entry.Enabled {
				t.Fatal("Disable returned but the switch is still on")
			}

			g.release("m1")
			wait(t, ticked, deadline, "the tick")
			entry := f.entry("m1")
			if entry.Enabled || entry.Pending != nil || entry.Running != nil || len(f.builds()) != 0 {
				t.Fatalf("the late answer (revision 2) acted on a map switched off: %+v, builds %+v", entry, f.builds())
			}
			if entry.Observed == nil || entry.Observed.Number != 1 {
				t.Errorf("the late answer was recorded: observed %+v", entry.Observed)
			}
			if _, found := f.svc.CheckingNow("m1"); found {
				t.Error("the answered question is still reported in flight")
			}
			// Off stays off.
			calls := g.count("m1")
			f.save("m1", rev(3))
			f.poll()
			if g.count("m1") != calls || len(f.builds()) != 0 {
				t.Errorf("a map switched off was asked about or built: %d more questions, builds %+v", g.count("m1")-calls, f.builds())
			}
		})
	}
}

func TestAProfileChangedWhileAUBIsSlowIsTheOneTheNextBuildUses(t *testing.T) {
	for _, via := range []string{"page", "cli"} {
		t.Run(via, func(t *testing.T) {
			f := baselined(t, "m1")
			g := holdCurrent(f, f.svc, false, "m1")
			actor := f.svc
			if via == "cli" {
				actor = f.service()
			}
			f.save("m1", rev(2))
			f.advance(CheckInterval + time.Second)
			ticked := background(func() { f.svc.Tick(context.Background()) })
			g.await(t, "m1")

			timed(t, "SetPipeline ("+via+")", func() error { _, err := actor.SetPipeline("m1", "final"); return err })
			g.release("m1")
			wait(t, ticked, deadline, "the tick")
			if got := f.builds(); len(got) != 0 {
				t.Fatalf("the answer asked under the old profile started %+v", got)
			}
			if entry := f.entry("m1"); entry.PipelineID != "final" || !entry.Enabled {
				t.Fatalf("the profile change was overwritten: %+v", entry)
			}
			// The map is still due: the next pass asks again, under the new
			// profile, and builds revision 2 with it — nothing was lost.
			if err := f.svc.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := f.builds()
			if len(got) != 1 || got[0].revision != 2 || got[0].pipeline != "final" {
				t.Fatalf("builds = %+v, want one of revision 2 with the new profile", got)
			}
			if entry := f.entry("m1"); entry.Running == nil || entry.Running.Pipeline != "final" {
				t.Errorf("the attempt records %+v", entry.Running)
			}
		})
	}
}

func TestOffAndOnAgainWhileAUBIsSlowTakesAFreshBaseline(t *testing.T) {
	for _, via := range []string{"page", "cli"} {
		t.Run(via, func(t *testing.T) {
			f := baselined(t, "m1")
			g := holdCurrent(f, f.svc, false, "m1")
			actor := f.svc
			if via == "cli" {
				actor = f.service()
			}
			f.save("m1", rev(2))
			f.advance(CheckInterval + time.Second)
			ticked := background(func() { f.svc.Tick(context.Background()) })
			g.await(t, "m1")

			timed(t, "Disable ("+via+")", func() error { _, err := actor.Disable("m1"); return err })
			timed(t, "Enable ("+via+")", func() error { _, err := actor.Enable("m1", "", "final"); return err })
			// Revision 3 is saved while the old answer (revision 2) is held.
			f.save("m1", rev(3))
			g.release("m1")
			wait(t, ticked, deadline, "the tick")
			if entry := f.entry("m1"); entry.Baseline != nil || entry.Pending != nil || len(f.builds()) != 0 {
				t.Fatalf("the answer asked before switching off and on became %+v / pending %+v, builds %+v",
					entry.Baseline, entry.Pending, f.builds())
			}
			if err := f.svc.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			entry := f.entry("m1")
			if entry.Baseline == nil || entry.Baseline.Number != 3 || len(f.builds()) != 0 {
				t.Fatalf("the fresh baseline is %+v, builds %+v; want revision 3, unbuilt", entry.Baseline, f.builds())
			}
			f.save("m1", rev(4))
			f.poll()
			if got := f.builds(); len(got) != 1 || got[0].revision != 4 || got[0].pipeline != "final" {
				t.Fatalf("after the fresh baseline: builds %+v, want exactly one of revision 4 with \"final\"", got)
			}
		})
	}
}

func TestThreeMapsOneHeldAnswerTheOthersAreCheckedBuiltAndChangedPromptly(t *testing.T) {
	f := baselined(t, "m1", "m2", "m3")
	g := holdCurrent(f, f.svc, true, "m1")
	for _, asset := range []string{"m1", "m2", "m3"} {
		f.save(asset, rev(2))
	}
	f.advance(CheckInterval + time.Second)
	ticked := background(func() { f.svc.Tick(context.Background()) })
	g.await(t, "m1")

	// The two healthy maps are answered and built while m1's answer is held.
	limit := time.Now().Add(deadline)
	for len(f.builds()) < 2 {
		if time.Now().After(limit) {
			t.Fatalf("healthy maps were not built while one answer was held: %+v", f.builds())
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, build := range f.builds() {
		if build.asset == "m1" || build.revision != 2 {
			t.Fatalf("unexpected build while m1 was held: %+v", build)
		}
	}
	timed(t, "SetPipeline on m3", func() error { _, err := f.svc.SetPipeline("m3", "final"); return err })
	timed(t, "Disable on m2", func() error { _, err := f.svc.Disable("m2"); return err })

	// Build now on m1 while the poller's question about it is still held:
	// one build of revision 2, and the poller's late answer adds none.
	timed(t, "Build now on m1", func() error {
		_, err := f.svc.BuildNow(context.Background(), "m1", "", "")
		return err
	})
	g.release("m1")
	wait(t, ticked, deadline, "the tick")
	perMap := map[string]int{}
	for _, build := range f.builds() {
		perMap[build.asset]++
	}
	if perMap["m1"] != 1 || perMap["m2"] != 1 || perMap["m3"] != 1 {
		t.Fatalf("builds per map = %v, want exactly one each: %+v", perMap, f.builds())
	}
	if entry := f.entry("m1"); entry.Pending != nil {
		t.Errorf("m1's late answer queued another build: pending %+v", entry.Pending)
	}
	if entry := f.entry("m2"); entry.Enabled || entry.Running == nil {
		t.Errorf("m2: switched off %v, its running build kept %+v", !entry.Enabled, entry.Running)
	}
}

func TestQuestionsInFlightAreBoundedNeverOverlapAndDoNotStarveHealthyMaps(t *testing.T) {
	assets := []string{"m1", "m2", "m3", "m4", "m5", "m6"}
	f := baselined(t, assets...)
	g := holdCurrent(f, f.svc, false, assets...)
	f.advance(CheckInterval + time.Second)

	// A pass as Run makes it: it does not wait for its answers.
	if _, err := f.svc.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxConcurrentChecks; i++ {
		select {
		case <-g.entered:
		case <-time.After(deadline):
			t.Fatalf("only %d questions were asked", i)
		}
	}
	// Another pass while all slots are held asks nothing more, and nothing
	// twice.
	if _, err := f.svc.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	seen, asked := g.maxSeen, len(g.calls)
	for asset, n := range g.calls {
		if n != 1 {
			t.Errorf("%s was asked %d times at once", asset, n)
		}
	}
	g.mu.Unlock()
	t.Logf("with %d maps due and every answer held: %d questions in flight (bound %d)", len(assets), seen, MaxConcurrentChecks)
	if seen != MaxConcurrentChecks || asked != MaxConcurrentChecks {
		t.Fatalf("in flight %d, asked about %d maps; want the bound %d", seen, asked, MaxConcurrentChecks)
	}

	// m1 stays slow; the rest answer. The maps that waited are asked at the
	// next pass, and a healthy map is asked again on its own cadence while m1
	// is still held — and m1 is never asked twice at once.
	for _, asset := range assets[1:] {
		g.release(asset)
	}
	limit := time.Now().Add(deadline)
	for {
		f.svc.tick(context.Background())
		if g.count("m5") == 1 && g.count("m6") == 1 {
			break
		}
		if time.Now().After(limit) {
			t.Fatalf("the maps left waiting for a slot were never asked: %v", g.calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, asset := range assets[1:] {
		for f.entry(asset).LastCheckAt.IsZero() || !f.entry(asset).NextCheckAt.After(f.now) {
			if time.Now().After(limit) {
				t.Fatalf("%s's answer was not recorded", asset)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	f.advance(CheckInterval + time.Second)
	for g.count("m2") < 2 {
		f.svc.tick(context.Background())
		if time.Now().After(limit) {
			t.Fatalf("a healthy map was starved by a slow one: %v", g.calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := g.count("m1"); n != 1 {
		t.Errorf("the slow map was asked %d times; a second question overlapped the first", n)
	}
	g.release("m1")
	wait(t, background(f.svc.inflight.Wait), deadline, "the questions in flight")
}

func TestStoppingTheCompanionCancelsAHeldQuestionAndRecordsNothing(t *testing.T) {
	f := baselined(t, "m1")
	g := holdCurrent(f, f.svc, false, "m1")
	f.advance(CheckInterval + time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	ran := background(func() { f.svc.Run(ctx) })
	g.await(t, "m1")
	before := f.entry("m1")
	cancel()
	wait(t, ran, deadline, "Run, after its context ended with a question in flight")
	after := f.entry("m1")
	if after.Failures != 0 || after.CheckError != "" || !after.LastCheckAt.Equal(before.LastCheckAt) {
		t.Errorf("a cancelled question was recorded as AUB's failure: %+v", after)
	}
	if _, found := f.svc.CheckingNow("m1"); found {
		t.Error("the cancelled question is still reported in flight")
	}
}

func TestASubmissionBeingStartedIsNotMistakenForACrash(t *testing.T) {
	f := baselined(t, "m1")
	started, proceed := make(chan struct{}), make(chan struct{})
	inner := f.svc.deps.Start
	f.svc.deps.Start = func(entry Entry, revision Revision) (string, error) {
		close(started)
		<-proceed
		return inner(entry, revision)
	}
	f.save("m1", rev(2))
	built := background(func() {
		if _, err := f.svc.BuildNow(context.Background(), "m1", "", ""); err != nil {
			t.Errorf("build now: %v", err)
		}
	})
	select {
	case <-started:
	case <-time.After(deadline):
		t.Fatal("the build was never started")
	}
	// Written down, run not yet recorded: this process's pass and another
	// process's pass both leave it alone.
	f.advance(time.Second)
	if err := f.svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := f.service()
	if err := other.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if entry := f.entry("m1"); entry.Running == nil || entry.Failed != nil {
		t.Fatalf("a submission being started was judged a crash: running %+v failed %+v", entry.Running, entry.Failed)
	}
	close(proceed)
	wait(t, built, deadline, "build now")
	if entry := f.entry("m1"); entry.Running == nil || entry.Running.RunID == "" || len(f.builds()) != 1 {
		t.Fatalf("the run was not recorded: %+v builds %+v", entry.Running, f.builds())
	}
}

func TestAnotherProcesssSubmissionThatNeverRecordedItsRunIsReportedAfterTheGrace(t *testing.T) {
	f := baselined(t, "m1")
	if _, err := Update(f.path, func(state *State) error {
		entry, _ := state.Find("m1")
		entry.remember(rev(2).Key())
		entry.Running = &Attempt{Revision: rev(2), Pipeline: "fast", StartedAt: f.now, Submitter: "4242-deadbeef"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.save("m1", rev(2))
	f.svc = f.service()
	f.advance(time.Second)
	f.svc.Tick(context.Background())
	if entry := f.entry("m1"); entry.Running == nil || entry.Failed != nil {
		t.Fatalf("judged interrupted within the grace: %+v", entry)
	}
	f.advance(submitGrace)
	f.poll()
	entry := f.entry("m1")
	if entry.Running != nil || entry.Failed == nil || len(f.builds()) != 0 {
		t.Fatalf("after the grace: running %+v failed %+v builds %+v; want a failure and no second build", entry.Running, entry.Failed, f.builds())
	}
}

func TestRetryAndBuildNowStayExplicitWhenAutoBuildIsOff(t *testing.T) {
	f := baselined(t, "m1")
	f.save("m1", rev(2))
	f.poll()
	f.finish(f.builds()[0].run, RunFailed, "qbsp exited with status 1")
	f.poll()
	if _, err := f.svc.Disable("m1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		f.poll()
	}
	if len(f.builds()) != 1 {
		t.Fatalf("a failed revision was retried on its own: %+v", f.builds())
	}
	if _, err := f.svc.Retry("m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Retry("m1"); !errors.Is(err, ErrBusy) {
		t.Errorf("a second retry while one runs answered %v", err)
	}
	if got := f.builds(); len(got) != 2 || got[1].revision != 2 || f.entry("m1").Enabled {
		t.Fatalf("retry with Auto-build off: builds %+v, entry %+v", got, f.entry("m1"))
	}
}
