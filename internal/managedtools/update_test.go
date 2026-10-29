package managedtools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
)

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// installed is a rig with both tools at a first version, and a newer EricW
// published and checked: an offer is on the table.
func offered(t *testing.T) (*rig, *managedtools.Manager, *managedtools.Candidate) {
	t.Helper()
	r := newRig(t)
	r.publishBoth("1.4536", "2.5.17n-ap.3031")
	m := r.manager()
	m.Step(context.Background())
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4540", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Offer == nil || view.Offer.Version != "1.4540" || view.Active.Version != "1.4536" {
		t.Fatalf("no offer of 1.4540 over 1.4536: %+v", view)
	}
	if tool(m.Status(), managedtools.Q3Map2).Offer != nil {
		t.Fatal("q3map2 has an offer nobody published")
	}
	return r, m, view.Offer
}

// M4: Cancel dismisses for this run; it is back in the next one; checked
// Cancel suppresses exactly these bytes; a newer version is offered again;
// two tools do not share what was skipped.
func TestCancelAndDontAskAgainSuppressOnlyThisVersionOfThisTool(t *testing.T) {
	r, m, offer := offered(t)
	if err := m.Decide(managedtools.EricW, managedtools.DecideCancel, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	if tool(m.Status(), managedtools.EricW).Offer != nil {
		t.Fatal("a cancelled offer is shown again in the same run")
	}
	r.clock.Advance(2 * time.Hour)
	m.Step(context.Background())
	if tool(m.Status(), managedtools.EricW).Offer != nil {
		t.Fatal("a cancelled offer came back at the next hourly check of the same run")
	}
	next := r.manager() // the next launch
	if view := tool(next.Status(), managedtools.EricW); view.Offer == nil || view.Offer.Version != "1.4540" {
		t.Fatal("a cancelled offer without \"don't ask again\" did not come back at the next launch")
	}

	if err := next.Decide(managedtools.EricW, managedtools.DecideCancel, offer.Identity(), true); err != nil {
		t.Fatal(err)
	}
	if tool(r.manager().Status(), managedtools.EricW).Offer != nil {
		t.Fatal("a skipped version is offered at the next launch")
	}
	skipped := tool(next.Status(), managedtools.EricW).Skipped
	if len(skipped) != 1 || skipped[0] != offer.Identity() {
		t.Fatalf("skipped %+v; want exactly the offered bytes", skipped)
	}
	if len(tool(next.Status(), managedtools.Q3Map2).Skipped) != 0 {
		t.Fatal("skipping an EricW version skipped something for q3map2")
	}

	// q3map2's own offer is unaffected.
	r.publish(q3map2Repo, mt.Spec{Tool: managedtools.Q3Map2, Version: "2.5.17n-ap.3040", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	next.Step(context.Background())
	if tool(next.Status(), managedtools.Q3Map2).Offer == nil {
		t.Fatal("q3map2's update was suppressed by an EricW skip")
	}
	// A genuinely newer EricW is offered.
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4541", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	next.Step(context.Background())
	if view := tool(next.Status(), managedtools.EricW); view.Offer == nil || view.Offer.Version != "1.4541" {
		t.Fatal("a newer version than the skipped one was not offered")
	}
	// Settings' reset forgets the skip.
	if err := next.ResetSkipped(managedtools.EricW); err != nil {
		t.Fatal(err)
	}
	if len(tool(next.Status(), managedtools.EricW).Skipped) != 0 {
		t.Fatal("reset left a skipped version")
	}
}

// M4: a decision about bytes that are no longer on offer is refused.
func TestADecisionAboutAnOfferThatChangedIsRefused(t *testing.T) {
	_, m, offer := offered(t)
	stale := offer.Identity()
	stale.SHA256 = strings.Repeat("0", 64)
	if err := m.Decide(managedtools.EricW, managedtools.DecideNow, stale, false); !errors.Is(err, managedtools.ErrStaleOffer) {
		t.Fatalf("a decision about other bytes: %v", err)
	}
}

// M4/integrity: identical version, different bytes, is an event, never an
// install.
func TestAVersionRepublishedWithOtherBytesIsAnIntegrityEventAndNeverInstalled(t *testing.T) {
	r, m, offer := offered(t)
	if err := m.Decide(managedtools.EricW, managedtools.DecideCancel, offer.Identity(), true); err != nil {
		t.Fatal(err)
	}
	// 1.4540 is published again with different bytes.
	r.server.Mutate(func() { r.server.Releases[ericwRepo] = r.server.Releases[ericwRepo][1:] })
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4540", Prerelease: true,
		Archive: func(e map[string][]byte) map[string][]byte {
			e["README.md"] = []byte("fixture release 1.4540\n")
			return e
		},
		ArchiveManifest: func(m map[string]any) { m["commit"] = strings.Repeat("d", 40) }})
	r.clock.Advance(2 * time.Hour)
	before := archiveDownloads(r.server)
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Integrity == nil || view.Integrity.Seen.Version != "1.4540" || view.Offer != nil {
		t.Fatalf("a republished version is not an integrity event: %+v", view)
	}
	if archiveDownloads(r.server) != before || view.Active.Version != "1.4536" {
		t.Fatal("republished bytes were downloaded or activated")
	}
}

// M6: Update now stages while work runs, activates only once it ends, and
// keeps the previous installation for the work that pinned it.
func TestUpdateNowWaitsForWorkAndKeepsThePreviousInstallation(t *testing.T) {
	r, m, offer := offered(t)
	r.host.setBusy("Build — q1-normal")
	if err := m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Active.Version != "1.4536" || view.Pending == nil || !view.Pending.Staged || len(view.Pending.Waiting) != 1 {
		t.Fatalf("while a build runs: active %s, pending %+v", view.Active.Version, view.Pending)
	}
	oldDir := view.Active.Dir
	r.host.setBusy()
	m.Step(context.Background())
	view = tool(m.Status(), managedtools.EricW)
	if view.Active.Version != "1.4540" || view.Pending != nil || view.Previous == nil || view.Previous.Dir != oldDir {
		t.Fatalf("after the build: %+v", view)
	}
	if _, err := os.Stat(filepath.Join(oldDir, "install.json")); err != nil {
		t.Fatal("the previous installation was removed; it is the rollback")
	}
	if len(m.Status().Notices) == 0 || !strings.Contains(m.Status().Notices[len(m.Status().Notices)-1].Text, "1.4540") {
		t.Fatal("no Ready notice for the update")
	}
}

// M6: a candidate whose programs fail their check is not activated, is not
// left installed, and the prior version stays.
func TestAFailedVersionCheckLeavesThePriorVersionActive(t *testing.T) {
	r, m, offer := offered(t)
	r.host.verifyErr = errors.New("qbsp printed nothing")
	if err := m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Active.Version != "1.4536" || view.Failure == nil || !strings.Contains(view.Failure.Error, "version check") {
		t.Fatalf("a failed check: %+v", view)
	}
	if _, err := os.Stat(filepath.Join(r.root, "ericw-tools", "1.4540-"+offer.SHA256[:8])); !os.IsNotExist(err) {
		t.Fatal("an installation that failed its check was left on disk")
	}
	// Retrying after the cause is gone succeeds.
	r.host.verifyErr = nil
	_ = m.Retry(managedtools.EricW)
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Active.Version != "1.4540" {
		t.Fatalf("retry: %+v", view)
	}
}

// M6: a binding write that fails leaves the prior installation active.
func TestAFailedActivationLeavesThePriorVersion(t *testing.T) {
	r, m, offer := offered(t)
	r.host.failBind = errors.New("bindings.json is locked")
	_ = m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false)
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Active.Version != "1.4536" || view.Failure == nil {
		t.Fatalf("a failed activation: %+v", view)
	}
}

// M7: Update when I close stages now and activates only at a normal exit; a
// crash in between is applied at the next start, not reported as done.
func TestUpdateOnCloseAppliesAtExitOrAtTheNextStartAfterACrash(t *testing.T) {
	r, m, offer := offered(t)
	if err := m.Decide(managedtools.EricW, managedtools.DecideOnExit, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Active.Version != "1.4536" || view.Pending == nil || !view.Pending.Staged || view.Pending.Intent != managedtools.IntentOnExit {
		t.Fatalf("staged for close: %+v", view.Pending)
	}
	// A closed page is not a closed Companion: nothing changes on a step.
	m.Step(context.Background())
	if tool(m.Status(), managedtools.EricW).Active.Version != "1.4536" {
		t.Fatal("an update for the close was applied while the Companion ran")
	}
	// The process is killed: ApplyOnExit never runs. The next start applies it.
	next := r.manager()
	if got := next.ApplyAtStart(); len(got) != 1 || !strings.Contains(got[0], "1.4540") {
		t.Fatalf("ApplyAtStart: %v", got)
	}
	if tool(next.Status(), managedtools.EricW).Active.Version != "1.4540" {
		t.Fatal("not applied at the next start")
	}
}

func TestUpdateOnCloseAppliesAtANormalExit(t *testing.T) {
	_, m, offer := offered(t)
	_ = m.Decide(managedtools.EricW, managedtools.DecideOnExit, offer.Identity(), false)
	m.Step(context.Background())
	if got := m.ApplyOnExit(); len(got) != 1 || !strings.Contains(got[0], "updated to 1.4540") {
		t.Fatalf("ApplyOnExit: %v", got)
	}
}

// Cancel pending activation drops the intent and does not re-offer at once.
func TestCancellingAPendingUpdateKeepsTheActiveVersionAndDoesNotReofferAtOnce(t *testing.T) {
	r, m, offer := offered(t)
	r.host.setBusy("a compile")
	_ = m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false)
	m.Step(context.Background())
	if err := m.CancelPending(managedtools.EricW); err != nil {
		t.Fatal(err)
	}
	r.host.setBusy()
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Active.Version != "1.4536" || view.Pending != nil || view.Offer != nil {
		t.Fatalf("after cancelling: %+v", view)
	}
}

// Retention: never more than the bound, never the active or previous one.
func TestOldInstallationsArePrunedButNeverTheActiveOrPrevious(t *testing.T) {
	r, m, _ := offered(t)
	for _, version := range []string{"1.4541", "1.4542", "1.4543"} {
		r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: version, Prerelease: true})
		r.clock.Advance(2 * time.Hour)
		m.Step(context.Background())
		offer := tool(m.Status(), managedtools.EricW).Offer
		if offer == nil {
			t.Fatalf("no offer of %s", version)
		}
		_ = m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false)
		m.Step(context.Background())
		m.Step(context.Background())
	}
	view := tool(m.Status(), managedtools.EricW)
	if view.Active.Version != "1.4543" || view.Previous == nil || view.Previous.Version != "1.4542" {
		t.Fatalf("active %s previous %+v", view.Active.Version, view.Previous)
	}
	entries, _ := os.ReadDir(filepath.Join(r.root, "ericw-tools"))
	var installs []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			installs = append(installs, entry.Name())
		}
	}
	if len(installs) > 3 {
		t.Fatalf("%d installations kept: %v", len(installs), installs)
	}
}

// A disabled coordinator checks and installs nothing.
func TestTurnedOffNothingIsCheckedOrInstalled(t *testing.T) {
	r := newRig(t)
	r.publishBoth("1.4536", "2.5.17n-ap.3031")
	m := r.manager(func(o *managedtools.Options) { o.Disabled = true })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m.Run(ctx)
	if r.server.Requests[ericwRepo] != 0 || tool(m.Status(), managedtools.EricW).Phase != managedtools.PhaseDisabled {
		t.Fatal("a disabled coordinator asked GitHub or reads otherwise")
	}
}

// Run returns when its context ends, and polls no faster than its tick.
func TestTheCoordinatorStopsWithItsContextAndDoesNotSpin(t *testing.T) {
	r := newRig(t)
	r.server.Fail.Store(true)
	m := r.manager(func(o *managedtools.Options) { o.Now = time.Now; o.Tick = 50 * time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	time.Sleep(500 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if n := r.server.Requests[ericwRepo]; n > 2 {
		t.Fatalf("%d requests in half a second while failing; the backoff did not hold", n)
	}
}

// 01_NEW_368 §4: once the archive is in, the row says what is actually being
// waited for — the version check, then the work an update waits for — and
// never goes on showing a transfer, with or without its byte counts. After
// the wait it resumes: ready, nothing pending.
func TestTheStatusSaysWhatAnUpdateIsWaitingForNotADownload(t *testing.T) {
	r, m, offer := offered(t)
	hold := make(chan struct{})
	r.host.mu.Lock()
	r.host.verifyHold, r.host.busy = hold, []string{"Compile the map — auto-pigeon.ericw-tools.q1"}
	r.host.mu.Unlock()
	if err := m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.Step(context.Background()); close(done) }()
	waitFor(t, func() bool { return tool(m.Status(), managedtools.EricW).Phase == managedtools.PhaseVerifying })
	view := tool(m.Status(), managedtools.EricW)
	if view.Done != 0 || view.Total != 0 || view.Version != "1.4540" {
		t.Fatalf("while the programs are checked: %+v", view)
	}
	close(hold)
	<-done
	m.Step(context.Background())
	view = tool(m.Status(), managedtools.EricW)
	if view.Phase != managedtools.PhaseReady || view.Version != "1.4536" || view.Done != 0 || view.Total != 0 {
		t.Fatalf("while the update waits for work: %+v", view)
	}
	if view.Pending == nil || !view.Pending.Staged || len(view.Pending.Waiting) != 1 {
		t.Fatalf("the wait is not said: %+v", view.Pending)
	}
	r.host.mu.Lock()
	r.host.busy = nil
	r.host.mu.Unlock()
	m.Step(context.Background())
	view = tool(m.Status(), managedtools.EricW)
	if view.Phase != managedtools.PhaseReady || view.Version != "1.4540" || view.Pending != nil {
		t.Fatalf("after the work ended: %+v", view)
	}
}
