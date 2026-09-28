package autobuild

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixture is a controllable AUB and a controllable run store.
type fixture struct {
	t     *testing.T
	mu    sync.Mutex
	now   time.Time
	path  string
	svc   *Service
	aub   map[string]Revision
	fail  map[string]error
	calls int
	// started is every build submitted, in order; runs is their states.
	started  []startCall
	runs     map[string]string
	why      map[string]string
	startErr error
}

type startCall struct {
	asset    string
	revision int
	pipeline string
	run      string
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{
		t: t, now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		path: filepath.Join(t.TempDir(), "autobuild", "state.json"),
		aub:  map[string]Revision{}, fail: map[string]error{},
		runs: map[string]string{}, why: map[string]string{},
	}
	f.svc = f.service()
	return f
}

// service builds a fresh Service over the same file — what a restarted
// Companion does.
func (f *fixture) service() *Service {
	svc, err := New(f.path, Deps{
		Current: func(_ context.Context, asset string) (Revision, string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls++
			if err := f.fail[asset]; err != nil {
				return Revision{}, "", err
			}
			revision, found := f.aub[asset]
			if !found {
				return Revision{}, "", ErrMissing
			}
			return revision, "Map " + asset, nil
		},
		Start: func(entry Entry, revision Revision) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.startErr != nil {
				return "", f.startErr
			}
			run := fmt.Sprintf("run-%d", len(f.started)+1)
			f.started = append(f.started, startCall{entry.AssetID, revision.Number, entry.PipelineID, run})
			f.runs[run] = "compiling"
			return run, nil
		},
		RunState: func(run string) (string, string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			state, found := f.runs[run]
			if !found {
				return "", "", errors.New("no such run")
			}
			return state, f.why[run], nil
		},
		Now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.now
		},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return svc
}

func rev(n int) Revision {
	return Revision{ID: fmt.Sprintf("rev%03d", n), Number: n, ContentSHA256: fmt.Sprintf("sha-%d", n)}
}

// poll advances past the check interval and ticks, as thirty seconds of a
// running Companion would.
func (f *fixture) poll() {
	f.t.Helper()
	f.mu.Lock()
	f.now = f.now.Add(CheckInterval + time.Second)
	f.mu.Unlock()
	if err := f.svc.Tick(context.Background()); err != nil {
		f.t.Fatalf("tick: %v", err)
	}
}

func (f *fixture) save(asset string, revision Revision) {
	f.mu.Lock()
	f.aub[asset] = revision
	f.mu.Unlock()
}

func (f *fixture) finish(run, state, why string) {
	f.mu.Lock()
	f.runs[run], f.why[run] = state, why
	f.mu.Unlock()
}

func (f *fixture) entry(asset string) Entry {
	f.t.Helper()
	state, err := Load(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	entry, found := state.Find(asset)
	if !found {
		f.t.Fatalf("no entry for %s", asset)
	}
	return *entry
}

func (f *fixture) builds() []startCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]startCall(nil), f.started...)
}

func TestTheRevisionCurrentWhenSwitchedOnIsTheBaselineAndIsNotBuilt(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(4))
	if _, err := f.svc.Enable("m1", "", "auto-pigeon.q1.normal"); err != nil {
		t.Fatal(err)
	}
	f.poll()
	f.poll()
	if got := f.builds(); len(got) != 0 {
		t.Fatalf("switching it on built %v", got)
	}
	entry := f.entry("m1")
	if entry.Baseline == nil || entry.Baseline.Number != 4 || entry.Observed.Number != 4 {
		t.Errorf("baseline %+v observed %+v, want revision 4", entry.Baseline, entry.Observed)
	}
	if entry.DisplayName != "Map m1" || entry.LastCheckAt.IsZero() {
		t.Errorf("the check was not recorded: %+v", entry)
	}
}

func TestOneNewRevisionIsBuiltExactlyOnceHoweverOftenItIsPolled(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(4))
	f.svc.Enable("m1", "", "auto-pigeon.q1.normal")
	f.poll()
	f.save("m1", rev(5))
	for i := 0; i < 5; i++ {
		f.poll()
	}
	got := f.builds()
	if len(got) != 1 || got[0].revision != 5 || got[0].pipeline != "auto-pigeon.q1.normal" {
		t.Fatalf("builds = %+v, want exactly one of revision 5", got)
	}
	f.finish(got[0].run, RunSucceeded, "")
	for i := 0; i < 3; i++ {
		f.poll()
	}
	if len(f.builds()) != 1 {
		t.Fatalf("a finished revision was built again: %+v", f.builds())
	}
	entry := f.entry("m1")
	if entry.LastBuilt == nil || entry.LastBuilt.Revision.Number != 5 || entry.LastBuilt.RunID != got[0].run || entry.Running != nil {
		t.Errorf("the result is not recorded: %+v", entry)
	}
}

func TestEditsDuringABuildAreCoalescedIntoTheNewestAndBuiltNext(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "p")
	f.poll()
	f.save("m1", rev(2))
	f.poll()
	first := f.builds()[0]
	for _, n := range []int{3, 4, 5} {
		f.save("m1", rev(n))
		f.poll()
	}
	if len(f.builds()) != 1 {
		t.Fatalf("a second build started while one ran: %+v", f.builds())
	}
	if entry := f.entry("m1"); entry.Pending == nil || entry.Pending.Number != 5 {
		t.Fatalf("pending = %+v, want the newest (5)", entry.Pending)
	}
	f.finish(first.run, RunSucceeded, "")
	f.poll()
	got := f.builds()
	if len(got) != 2 || got[1].revision != 5 {
		t.Fatalf("builds = %+v, want revision 5 next and nothing for 3 and 4", got)
	}
}

func TestAFailureIsShownAndNotRetriedUntilSomebodyPressesRetry(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "p")
	f.poll()
	f.save("m1", rev(2))
	f.poll()
	run := f.builds()[0].run
	f.finish(run, RunFailed, "qbsp exited with status 1")
	for i := 0; i < 4; i++ {
		f.poll()
	}
	if len(f.builds()) != 1 {
		t.Fatalf("a failed revision was retried on its own: %+v", f.builds())
	}
	entry := f.entry("m1")
	if entry.Failed == nil || entry.Failed.Revision.Number != 2 || entry.Failed.RunID != run ||
		!strings.Contains(entry.Failed.Error, "status 1") {
		t.Fatalf("the failure is not shown with its revision and run: %+v", entry.Failed)
	}
	if _, err := f.svc.Retry("m1"); err != nil {
		t.Fatal(err)
	}
	got := f.builds()
	if len(got) != 2 || got[1].revision != 2 {
		t.Fatalf("retry built %+v", got)
	}
	f.finish(got[1].run, RunSucceeded, "")
	f.poll()
	if entry := f.entry("m1"); entry.Failed != nil || entry.LastBuilt.Revision.Number != 2 {
		t.Errorf("after a successful retry: failed %+v, built %+v", entry.Failed, entry.LastBuilt)
	}
}

func TestStateSurvivesARestartAndNothingIsBuiltTwice(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "p")
	f.poll()
	f.save("m1", rev(2))
	f.poll()
	run := f.builds()[0].run

	// The Companion restarts while revision 2 builds; the coordinator's own
	// recovery records the run failed.
	f.svc = f.service()
	f.finish(run, RunFailed, "the Companion stopped while this run was compiling")
	for i := 0; i < 3; i++ {
		f.poll()
	}
	if len(f.builds()) != 1 {
		t.Fatalf("a restart rebuilt a revision: %+v", f.builds())
	}
	entry := f.entry("m1")
	if !entry.Enabled || entry.PipelineID != "p" || entry.Baseline.Number != 1 || entry.Failed == nil {
		t.Fatalf("the state did not survive the restart: %+v", entry)
	}
	// And it resumes polling: a new revision after the restart is built.
	f.save("m1", rev(3))
	f.poll()
	if got := f.builds(); len(got) != 2 || got[1].revision != 3 {
		t.Fatalf("after a restart, a new revision built %+v", got)
	}
}

func TestACrashBetweenSubmittingAndStartingIsAFailureNotASecondBuild(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "p")
	f.poll()
	// Written down as submitted, with no run: what a crash inside start leaves.
	if _, err := Update(f.path, func(state *State) error {
		entry, _ := state.Find("m1")
		entry.remember(rev(2).Key())
		entry.Running = &Attempt{Revision: rev(2), Pipeline: "p", StartedAt: f.now}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.save("m1", rev(2))
	f.svc = f.service()
	f.poll()
	f.poll()
	if len(f.builds()) != 0 {
		t.Fatalf("an interrupted submission was started again: %+v", f.builds())
	}
	if entry := f.entry("m1"); entry.Failed == nil || !strings.Contains(entry.Failed.Error, "Retry") {
		t.Errorf("the interrupted submission is not reported: %+v", entry.Failed)
	}
}

func TestDisablingStopsChecksAndBuildsButLeavesARunningBuildToFinish(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "p")
	f.poll()
	f.save("m1", rev(2))
	f.poll()
	run := f.builds()[0].run
	if _, err := f.svc.Disable("m1"); err != nil {
		t.Fatal(err)
	}
	calls := f.calls
	f.save("m1", rev(3))
	f.poll()
	f.poll()
	if f.calls != calls {
		t.Errorf("AUB was asked %d more times after Auto-build was switched off", f.calls-calls)
	}
	if entry := f.entry("m1"); entry.Running == nil || entry.Running.RunID != run {
		t.Fatalf("switching off dropped the running build: %+v", entry.Running)
	}
	f.finish(run, RunSucceeded, "")
	f.poll()
	if entry := f.entry("m1"); entry.LastBuilt == nil || entry.Running != nil || len(f.builds()) != 1 {
		t.Errorf("after switching off: built %+v running %+v builds %d", entry.LastBuilt, entry.Running, len(f.builds()))
	}
}

func TestAnUnreachableServerBacksOffVisiblyAndRecovers(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "p")
	f.poll()
	f.fail["m1"] = errors.New("dial tcp: connection refused")
	f.poll()
	entry := f.entry("m1")
	if entry.Failures != 1 || !strings.Contains(entry.CheckError, "connection refused") {
		t.Fatalf("the failure is not shown: %+v", entry)
	}
	if wait := entry.NextCheckAt.Sub(entry.LastCheckAt); wait != 2*CheckInterval {
		t.Errorf("first backoff = %s, want %s", wait, 2*CheckInterval)
	}
	// Ticks before the backoff has passed do not ask again.
	calls := f.calls
	if err := f.svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls != calls {
		t.Error("AUB was asked again before its backoff had passed")
	}
	for i := 0; i < 10; i++ {
		f.mu.Lock()
		f.now = f.now.Add(MaxBackoff)
		f.mu.Unlock()
		f.svc.Tick(context.Background())
	}
	if wait := f.entry("m1").NextCheckAt.Sub(f.entry("m1").LastCheckAt); wait != MaxBackoff {
		t.Errorf("backoff after many failures = %s, want the bound %s", wait, MaxBackoff)
	}
	delete(f.fail, "m1")
	f.save("m1", rev(2))
	f.mu.Lock()
	f.now = f.now.Add(MaxBackoff)
	f.mu.Unlock()
	f.svc.Tick(context.Background())
	entry = f.entry("m1")
	if entry.Failures != 0 || entry.CheckError != "" || len(f.builds()) != 1 {
		t.Errorf("it did not recover: %+v, builds %d", entry, len(f.builds()))
	}
}

func TestRevokedAccessMissingMapAndABadAnswerAreEachSaid(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "p")
	f.poll()

	f.fail["m1"] = fmt.Errorf("%w: HTTP 401", ErrAccess)
	f.poll()
	if entry := f.entry("m1"); !strings.Contains(entry.CheckError, "Sign in again") || entry.Halted != "" {
		t.Errorf("revoked access reads %+v", entry)
	}
	delete(f.fail, "m1")

	f.save("m1", Revision{ID: "", Number: 0})
	f.mu.Lock()
	f.now = f.now.Add(MaxBackoff)
	f.mu.Unlock()
	f.svc.Tick(context.Background())
	if entry := f.entry("m1"); !strings.Contains(entry.CheckError, "named no revision") || len(f.builds()) != 0 {
		t.Errorf("an invalid answer reads %+v", entry)
	}

	f.mu.Lock()
	delete(f.aub, "m1")
	f.now = f.now.Add(MaxBackoff)
	f.mu.Unlock()
	f.svc.Tick(context.Background())
	entry := f.entry("m1")
	if entry.Halted != HaltMissing || !strings.Contains(entry.CheckError, "not on the Auto-Pigeon server") {
		t.Fatalf("a missing map reads %+v", entry)
	}
	calls := f.calls
	f.poll()
	if f.calls != calls {
		t.Error("a map that is gone was asked about again")
	}
	// Switching it on again clears the halt and takes a new baseline.
	f.save("m1", rev(7))
	f.svc.Enable("m1", "", "p")
	f.poll()
	if entry := f.entry("m1"); entry.Halted != "" || entry.Baseline.Number != 7 || len(f.builds()) != 0 {
		t.Errorf("re-enabling: %+v", entry)
	}
}

func TestAChangedPipelineIsUsedByTheNextBuild(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "fast")
	f.poll()
	f.svc.SetPipeline("m1", "final")
	f.save("m1", rev(2))
	f.poll()
	if got := f.builds(); len(got) != 1 || got[0].pipeline != "final" {
		t.Fatalf("builds = %+v, want the changed pipeline", got)
	}
	if entry := f.entry("m1"); entry.Running.Pipeline != "final" {
		t.Errorf("the attempt records pipeline %q", entry.Running.Pipeline)
	}
}

func TestBuildNowIsExplicitAndOneAtATime(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(3))
	f.svc.Enable("m1", "", "p")
	if _, err := f.svc.BuildNow(context.Background(), "m1", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := f.builds(); len(got) != 1 || got[0].revision != 3 {
		t.Fatalf("build now = %+v", got)
	}
	if _, err := f.svc.BuildNow(context.Background(), "m1", "", ""); !errors.Is(err, ErrBusy) {
		t.Errorf("a second build while one runs answered %v", err)
	}
	f.finish(f.builds()[0].run, RunSucceeded, "")
	f.poll()
	f.poll()
	if len(f.builds()) != 1 {
		t.Errorf("the explicitly built revision was built again by the poller: %+v", f.builds())
	}
}

func TestBuildNowOnAMapNeverSwitchedOnRecordsItAndLeavesAutoBuildOff(t *testing.T) {
	f := newFixture(t)
	f.save("m2", rev(9))
	if _, err := f.svc.BuildNow(context.Background(), "m2", "", ""); err == nil {
		t.Error("build now with no build profile was accepted")
	}
	if _, err := f.svc.BuildNow(context.Background(), "m2", "p", "Two"); err != nil {
		t.Fatal(err)
	}
	entry := f.entry("m2")
	if entry.Enabled || entry.Running == nil || entry.Running.Revision.Number != 9 || entry.PipelineID != "p" {
		t.Fatalf("build now on a new map: %+v", entry)
	}
	calls := f.calls
	f.save("m2", rev(10))
	f.poll()
	if f.calls != calls || len(f.builds()) != 1 {
		t.Errorf("a map that is switched off was polled or built: calls %d builds %+v", f.calls-calls, f.builds())
	}
}

func TestAStartThatIsRefusedIsAFailureNotALoop(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(1))
	f.svc.Enable("m1", "", "p")
	f.poll()
	f.startErr = errors.New("playrun: a run needs a build profile that is installed here")
	f.save("m1", rev(2))
	f.poll()
	f.poll()
	entry := f.entry("m1")
	if entry.Failed == nil || entry.Failed.Revision.Number != 2 || entry.Running != nil {
		t.Fatalf("a refused start reads %+v", entry)
	}
}
