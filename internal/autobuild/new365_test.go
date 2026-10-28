package autobuild

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

// NEW_365 (HITL review finding 1): a build by hand and an automatic build of
// the same revision are admitted in one step, whichever comes first. Each test
// holds one side at a barrier while the other runs, so the interleaving is the
// test's, not the scheduler's.

const q1 = "auto-pigeon.q1.normal"

// owing is a map whose auto-build is on and owes revision 5.
func owing(t *testing.T) *fixture {
	f := newFixture(t)
	f.save("m1", rev(4))
	f.svc.Enable("m1", "", q1)
	f.poll() // baseline 4
	f.save("m1", rev(5))
	f.mu.Lock()
	f.notReady[q1] = errors.New("held")
	f.mu.Unlock()
	f.poll() // 5 observed and owed, held from starting
	f.mu.Lock()
	delete(f.notReady, q1)
	f.mu.Unlock()
	if entry := f.entry("m1"); entry.Pending == nil || entry.Pending.Number != 5 {
		t.Fatalf("fixture: revision 5 is not owed: %+v", entry.Pending)
	}
	return f
}

// The sequence Build & Run used before, kept here as the reproduction: ask,
// start, then note. The poller's pass between the ask and the note starts the
// same revision, the note's refusal is only logged, and revision 5 is built
// twice.
func TestTheOldAskStartNoteSequenceBuiltOneRevisionTwice(t *testing.T) {
	f := owing(t)
	manualStarts := 0
	state, _ := Load(f.path)
	entry, _ := state.Find("m1")
	if entry.Running != nil {
		t.Fatalf("fixture: an automatic build is already running")
	}
	// — the barrier: between the ask and the start, a poller pass —
	f.poll()
	manualStarts++ // service.Start(request)
	noteErr := f.svc.NoteManual("m1", rev(5).ID)
	if !errors.Is(noteErr, ErrSameRevisionRunning) {
		t.Fatalf("the note = %v; the reproduction needs the refusal the old handler only logged", noteErr)
	}
	if automatic := len(f.builds()); automatic+manualStarts != 2 {
		t.Fatalf("builds of revision 5 = %d automatic + %d by hand, want the duplicate (2) this test reproduces", automatic, manualStarts)
	}
}

// Hand first: the reservation is written before the start, and a poller pass
// held inside that start finds nothing owed.
func TestAHandBuildReservedFirstIsNeverAlsoBuiltByThePoller(t *testing.T) {
	for _, mode := range []string{"build", "build and run"} {
		t.Run(mode, func(t *testing.T) {
			f := owing(t)
			started := 0
			err := f.svc.AdmitManual(ManualBuild{AssetID: "m1", RevisionID: rev(5).ID, PipelineID: q1, Backend: "http://aub:9190"}, func() error {
				started++
				f.poll() // the poller's pass, inside the hand build's start
				f.poll()
				return nil
			})
			if err != nil || started != 1 {
				t.Fatalf("admit = %v, started %d", err, started)
			}
			if got := f.builds(); len(got) != 0 {
				t.Fatalf("the poller also built revision 5: %+v", got)
			}
			if entry := f.entry("m1"); entry.Pending != nil || !slices.Contains(entry.ByHand, rev(5).ID) {
				t.Fatalf("entry after a hand build = pending %+v, by hand %v", entry.Pending, entry.ByHand)
			}
		})
	}
}

// Poller first: its attempt is written under the lock before its Start, and a
// hand build admitted while that Start is held is refused without starting.
func TestAHandBuildWhileTheAutomaticOneIsStartingIsRefused(t *testing.T) {
	f := owing(t)
	inStart := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.beforeStart = func() {
		once.Do(func() {
			close(inStart)
			<-release
		})
	}
	done := make(chan struct{})
	go func() { defer close(done); f.poll() }()
	<-inStart
	started := false
	err := f.svc.AdmitManual(ManualBuild{AssetID: "m1", RevisionID: rev(5).ID, PipelineID: q1}, func() error {
		started = true
		return nil
	})
	close(release)
	<-done
	if !errors.Is(err, ErrSameRevisionRunning) || started {
		t.Fatalf("admit during the automatic start = %v, started %v; want refused and not started", err, started)
	}
	if got := f.builds(); len(got) != 1 {
		t.Fatalf("builds = %+v, want the one automatic build", got)
	}
	// Another build profile of the same revision is another build.
	other := false
	if err := f.svc.AdmitManual(ManualBuild{AssetID: "m1", RevisionID: rev(5).ID, PipelineID: "local.pipeline.fast"}, func() error {
		other = true
		return nil
	}); err != nil || !other {
		t.Fatalf("a different build profile = %v, started %v; want admitted", err, other)
	}
	// Once the automatic run has ended — even before the poller noticed —
	// trying again by hand duplicates nothing.
	f.finish("run-1", RunFailed, "light failed")
	again := false
	if err := f.svc.AdmitManual(ManualBuild{AssetID: "m1", RevisionID: rev(5).ID, PipelineID: q1}, func() error {
		again = true
		return nil
	}); err != nil || !again {
		t.Fatalf("a retry after the automatic run ended = %v, started %v", err, again)
	}
}

// A start that fails gives the revision back: the poller builds it, so a
// refused submission never suppresses a revision for good.
func TestAHandBuildThatFailsToStartReleasesItsReservation(t *testing.T) {
	f := owing(t)
	err := f.svc.AdmitManual(ManualBuild{AssetID: "m1", RevisionID: rev(5).ID, PipelineID: q1}, func() error {
		return errors.New("the engine has no game folder")
	})
	if err == nil {
		t.Fatalf("a failed start was admitted")
	}
	entry := f.entry("m1")
	if slices.Contains(entry.ByHand, rev(5).ID) || entry.Pending == nil || entry.Pending.Number != 5 {
		t.Fatalf("after a failed start = by hand %v, pending %+v; want released and owed again", entry.ByHand, entry.Pending)
	}
	f.poll()
	if got := f.builds(); len(got) != 1 || got[0].revision != 5 {
		t.Fatalf("builds = %+v, want the poller to build revision 5", got)
	}
	// A revision already built by hand once stays so when a second hand
	// start fails.
	f2 := owing(t)
	if err := f2.svc.AdmitManual(ManualBuild{AssetID: "m1", RevisionID: rev(5).ID, PipelineID: q1}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	_ = f2.svc.AdmitManual(ManualBuild{AssetID: "m1", RevisionID: rev(5).ID, PipelineID: q1}, func() error { return errors.New("no") })
	if entry := f2.entry("m1"); !slices.Contains(entry.ByHand, rev(5).ID) {
		t.Fatalf("a failed second start forgot the first hand build: %v", entry.ByHand)
	}
	// A map with no auto-build: started, and given none.
	ran := false
	if err := f.svc.AdmitManual(ManualBuild{AssetID: "nobody", RevisionID: "r1"}, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("a map with no auto-build = %v, ran %v", err, ran)
	}
	if state, _ := Load(f.path); func() bool { _, found := state.Find("nobody"); return found }() {
		t.Fatalf("a map with no auto-build was given one")
	}
}
