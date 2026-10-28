package autobuild

import (
	"errors"
	"testing"
)

// NEW_363 (NEW_360 F1). A build profile that loses its readiness stops what
// Auto-build dispatches, without forgetting anything: the profile stays chosen,
// the saved revision stays waiting, and once the profile is set up again that
// revision is built — once.
func TestAnUnreadyProfileHoldsTheWaitingRevisionUntilItIsRepaired(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(4))
	if _, err := f.svc.Enable("m1", "", "auto-pigeon.q1.normal"); err != nil {
		t.Fatal(err)
	}
	f.poll()
	f.mu.Lock()
	f.notReady["auto-pigeon.q1.normal"] = errors.New("qbsp not set up yet")
	f.mu.Unlock()
	f.save("m1", rev(5))
	for i := 0; i < 3; i++ {
		f.poll()
	}
	if got := f.builds(); len(got) != 0 {
		t.Fatalf("an unready profile was dispatched: %+v", got)
	}
	entry := f.entry("m1")
	if entry.PipelineID != "auto-pigeon.q1.normal" || entry.Pending == nil || entry.Pending.Number != 5 || !entry.Enabled {
		t.Fatalf("readiness loss changed the choice or forgot the revision: %+v", entry)
	}

	f.mu.Lock()
	delete(f.notReady, "auto-pigeon.q1.normal")
	f.mu.Unlock()
	f.poll()
	f.poll()
	got := f.builds()
	if len(got) != 1 || got[0].revision != 5 || got[0].pipeline != "auto-pigeon.q1.normal" {
		t.Fatalf("after the repair builds = %+v, want revision 5 once with the same profile", got)
	}
}

// A person's own action is refused with the reason, rather than recorded as a
// failed build.
func TestRetryAndBuildNowAreRefusedWhileTheProfileNeedsSetup(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(4))
	f.svc.Enable("m1", "", "auto-pigeon.q1.normal")
	f.poll()
	f.mu.Lock()
	f.notReady["auto-pigeon.q1.normal"] = errors.New("qbsp not set up yet")
	f.mu.Unlock()
	if _, err := f.svc.BuildNow(t.Context(), "m1", "", ""); !errors.Is(err, ErrNotReady) {
		t.Fatalf("BuildNow = %v, want ErrNotReady", err)
	}
	if entry := f.entry("m1"); entry.Running != nil || entry.Failed != nil {
		t.Errorf("a refused BuildNow was recorded as an attempt: %+v", entry)
	}
}

// Manual Build (Build & Run with "Run after build" off) of the revision the
// poller is waiting to build: built once, by the person, and never again by
// the poller.
func TestARevisionBuiltByHandIsNotBuiltAgainByThePoller(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(4))
	f.svc.Enable("m1", "", "auto-pigeon.q1.normal")
	f.poll()
	// Saved, and built by hand before the poller saw it.
	f.save("m1", rev(5))
	if err := f.svc.NoteManual("m1", rev(5).ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		f.poll()
	}
	if got := f.builds(); len(got) != 0 {
		t.Fatalf("the poller built a revision already built by hand: %+v", got)
	}
	// Seen first, then built by hand: the waiting build is dropped.
	f.save("m1", rev(6))
	f.svc.Disable("m1")
	f.svc.Enable("m1", "", "auto-pigeon.q1.normal")
	f.poll()
	f.save("m1", rev(7))
	f.mu.Lock()
	f.notReady["auto-pigeon.q1.normal"] = errors.New("held")
	f.mu.Unlock()
	f.poll()
	if entry := f.entry("m1"); entry.Pending == nil || entry.Pending.Number != 7 {
		t.Fatalf("revision 7 is not waiting: %+v", entry.Pending)
	}
	if err := f.svc.NoteManual("m1", rev(7).ID); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.notReady, "auto-pigeon.q1.normal")
	f.mu.Unlock()
	f.poll()
	if got := f.builds(); len(got) != 0 {
		t.Fatalf("the poller built revision 7 after it was built by hand: %+v", got)
	}
	if entry := f.entry("m1"); entry.Pending != nil {
		t.Errorf("a revision built by hand is still waiting: %+v", entry.Pending)
	}
}

// And the other way round: building by hand the revision an automatic build is
// building now is refused, not started twice.
func TestBuildingByHandTheRevisionAutoBuildIsBuildingIsRefused(t *testing.T) {
	f := newFixture(t)
	f.save("m1", rev(4))
	f.svc.Enable("m1", "", "auto-pigeon.q1.normal")
	f.poll()
	f.save("m1", rev(5))
	f.poll()
	if got := f.builds(); len(got) != 1 {
		t.Fatalf("builds = %+v, want the automatic one", got)
	}
	if err := f.svc.NoteManual("m1", rev(5).ID); !errors.Is(err, ErrSameRevisionRunning) {
		t.Fatalf("NoteManual = %v, want ErrSameRevisionRunning", err)
	}
	// A map with no auto-build is not given one.
	if err := f.svc.NoteManual("m2", "rev001"); err != nil {
		t.Fatal(err)
	}
	state, _ := Load(f.path)
	if _, found := state.Find("m2"); found {
		t.Error("noting a hand build created an auto-build entry")
	}
}
