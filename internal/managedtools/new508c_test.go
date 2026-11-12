package managedtools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/compat"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// NEW_508C. C1: Cancel is not suppression — per target, for the Companion and
// each toolchain. The offer stays available, it is not announced again in this
// run, and the next start announces it again even when the release list answers
// "not modified". "Don't notify me" hides exactly those bytes until another
// release arrives, and one target's answer never touches another's.
func TestCancelKeepsEachOfferAvailableAndTheNextStartAnnouncesItAgainEvenOnAnUnchangedList(t *testing.T) {
	r, _, start := selfRig(t, "1.318", managedtools.SelfPortable)
	m := start()
	m.Step(context.Background())
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4570", Prerelease: true})
	r.publish(q3map2Repo, mt.Spec{Tool: managedtools.Q3Map2, Version: "2.5.17n-ap.3040", Prerelease: true})
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	m.Step(context.Background())
	targets := []managedtools.ToolID{managedtools.Companion, managedtools.EricW, managedtools.Q3Map2}
	for _, cancelled := range targets {
		t.Run(string(cancelled), func(t *testing.T) {
			m := start()
			view := tool(m.Status(), cancelled)
			if view.Offer == nil || view.OfferDismissed {
				t.Fatalf("no fresh offer for %s at a new start: %+v", cancelled, view)
			}
			if err := m.Decide(cancelled, managedtools.DecideCancel, view.Offer.Identity(), false); err != nil {
				t.Fatal(err)
			}
			status := m.Status()
			for _, other := range targets {
				got := tool(status, other)
				if got.Offer == nil {
					t.Fatalf("%s: Cancel on %s made the offer unavailable", other, cancelled)
				}
				if got.OfferDismissed != (other == cancelled) {
					t.Errorf("%s: dismissed=%v after a Cancel on %s", other, got.OfferDismissed, cancelled)
				}
			}
			// The same run's hourly check (an unchanged list: 304) does not
			// announce it again.
			r.clock.Advance(2 * time.Hour)
			m.Step(context.Background())
			if got := tool(m.Status(), cancelled); !got.OfferDismissed {
				t.Fatal("an hourly check in the same run announced a cancelled offer again")
			}
			// The next real start, on a list GitHub answers "not modified".
			next := start()
			r.clock.Advance(2 * time.Hour)
			next.Step(context.Background())
			if got := tool(next.Status(), cancelled); got.Offer == nil || got.OfferDismissed {
				t.Fatalf("the next start did not announce the cancelled offer again: %+v", got)
			}
		})
	}
	// Don't notify me: exactly those bytes, for that target only, and across
	// a restart; a newer release is announced.
	m = start()
	ericw := tool(m.Status(), managedtools.EricW)
	if err := m.Decide(managedtools.EricW, managedtools.DecideCancel, ericw.Offer.Identity(), true); err != nil {
		t.Fatal(err)
	}
	restarted := start()
	if got := tool(restarted.Status(), managedtools.EricW); got.Offer != nil {
		t.Fatalf("a version a person asked not to be told about is offered after a restart: %+v", got.Offer)
	}
	for _, other := range []managedtools.ToolID{managedtools.Q3Map2, managedtools.Companion} {
		if got := tool(restarted.Status(), other); got.Offer == nil || got.OfferDismissed {
			t.Errorf("%s lost its offer when EricW's was suppressed", other)
		}
	}
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4571", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	restarted.Step(context.Background())
	if got := tool(restarted.Status(), managedtools.EricW); got.Offer == nil || got.Offer.Version != "1.4571" {
		t.Fatalf("a newer release than the suppressed one is not offered: %+v", got.Offer)
	}
}

// C3: one Update checks every target and brings each to its newest verified
// release — a missing toolchain installed, an outdated one updated, the
// Companion restarted for its own update only after both compilers are active.
func TestOneUpdateInstallsAndUpdatesBothToolchainsBeforeTheCompanionRestarts(t *testing.T) {
	r, host, start := selfRig(t, "1.318", managedtools.SelfPortable)
	m := start(func(o *managedtools.Options) { o.AskFirst = true })
	if err := m.ApproveInstall([]managedtools.ToolID{managedtools.EricW}); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background()) // EricW 1.4566 installed; q3map2 waits for a person
	if view := tool(m.Status(), managedtools.Q3Map2); view.Active != nil || !view.AwaitingInstall {
		t.Fatalf("q3map2 should wait for a person: %+v", view)
	}
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4570", Prerelease: true})
	r.publish(q3map2Repo, mt.Spec{Tool: managedtools.Q3Map2, Version: "2.5.17n-ap.3040", Prerelease: true})
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	r.clock.Advance(2 * time.Minute)

	steps, err := m.UpdateAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[managedtools.ToolID]managedtools.UpdateAction{
		managedtools.EricW: managedtools.UpdateUpdating, managedtools.Q3Map2: managedtools.UpdateInstalling,
		managedtools.Companion: managedtools.UpdateUpdating,
	}
	versions := map[managedtools.ToolID]string{managedtools.EricW: "1.4570", managedtools.Q3Map2: "2.5.17n-ap.3040", managedtools.Companion: "1.320"}
	if len(steps) != 3 {
		t.Fatalf("steps %+v; want one per target", steps)
	}
	for _, step := range steps {
		if step.Action != want[step.Tool] || step.To != versions[step.Tool] || !step.Checked {
			t.Errorf("%s: %+v; want %s to %s, checked", step.Tool, step, want[step.Tool], versions[step.Tool])
		}
	}
	// The coordinator's order: a toolchain install or switch is work the
	// Companion's restart waits for.
	m.Step(context.Background())
	status := m.Status()
	for _, id := range []managedtools.ToolID{managedtools.EricW, managedtools.Q3Map2} {
		if view := tool(status, id); view.Active == nil || view.Active.Version != versions[id] {
			t.Fatalf("%s is not active at %s after one Update: %+v", id, versions[id], view)
		}
	}
	waitFor(t, func() bool { m.Step(context.Background()); return len(host.restarts()) == 1 })
	if got := host.restarts()[0].Version; got != "1.320" {
		t.Fatalf("restarted for %s; want 1.320", got)
	}
	// Nothing is offered twice: a second Update finds everything current but
	// the Companion, which is restarting.
	r.clock.Advance(2 * time.Minute)
	again, err := m.UpdateAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range again {
		if step.Tool != managedtools.Companion && step.Action != managedtools.UpdateCurrent {
			t.Errorf("a second Update: %+v; want current", step)
		}
	}
}

// C3: the restart really waits for a toolchain the Update is still installing.
func TestTheCompanionsRestartWaitsForAToolchainTheUpdateIsInstalling(t *testing.T) {
	r, host, start := selfRig(t, "1.318", managedtools.SelfPortable)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	m := start(func(o *managedtools.Options) { o.AskFirst = true })
	if _, err := m.UpdateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	hold := make(chan struct{})
	r.server.Mutate(func() { r.server.Hold = hold })
	done := make(chan struct{})
	go func() { m.Step(context.Background()); close(done) }()
	waitFor(t, func() bool { return archiveDownloads(r.server) >= 1 })
	if len(host.restarts()) != 0 {
		t.Fatal("the Companion restarted while a compiler was downloading")
	}
	close(hold)
	<-done
	waitFor(t, func() bool { m.Step(context.Background()); return len(host.restarts()) == 1 })
	for _, id := range []managedtools.ToolID{managedtools.EricW, managedtools.Q3Map2} {
		if view := tool(m.Status(), id); view.Active == nil {
			t.Fatalf("%s was not installed before the restart: %+v", id, view)
		}
	}
}

// C3: an Update repairs a deleted installation, says "current" for the newest,
// and says why a target is unavailable instead of doing nothing.
func TestAnUpdateRepairsADeletedInstallationAndSaysWhatItCannotDo(t *testing.T) {
	r := newRig(t)
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4566", Prerelease: true})
	// q3map2 publishes no build for this computer.
	r.publish(q3map2Repo, mt.Spec{Tool: managedtools.Q3Map2, Version: "2.5.17n-ap.3031", Prerelease: true, Targets: []string{"plan9-mips"}})
	m := r.manager()
	m.Step(context.Background())
	active := tool(m.Status(), managedtools.EricW).Active
	if active == nil {
		t.Fatal("EricW was not installed")
	}
	r.clock.Advance(2 * time.Minute)
	steps, err := m.UpdateAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byTool := map[managedtools.ToolID]managedtools.UpdateStep{}
	for _, step := range steps {
		byTool[step.Tool] = step
	}
	if got := byTool[managedtools.EricW]; got.Action != managedtools.UpdateCurrent || got.To != "1.4566" {
		t.Errorf("the newest installed: %+v; want current", got)
	}
	if got := byTool[managedtools.Q3Map2]; got.Action != managedtools.UpdateUnavailable || !strings.Contains(got.Detail, "no build for this computer") {
		t.Errorf("no build for this platform: %+v; want unavailable, with the reason", got)
	}

	if err := os.RemoveAll(active.Dir); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(2 * time.Minute)
	steps, err = m.UpdateAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.Tool == managedtools.EricW && step.Action != managedtools.UpdateInstalling {
			t.Errorf("a deleted installation: %+v; want installed again", step)
		}
	}
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Active == nil || view.Active.Version != "1.4566" {
		t.Fatalf("the deleted installation was not repaired: %+v", view)
	}
}

// C3: the Update is the catalogue's, not two hard-coded rows: an official
// target added to the list is checked and installed by the same operation.
func TestAnotherApprovedCatalogueRowIsUpdatedWithoutWiringOfItsOwn(t *testing.T) {
	r := newRig(t)
	r.publishBoth("1.4566", "2.5.17n-ap.3031")
	saved := append([]managedtools.Tool(nil), managedtools.Tools...)
	t.Cleanup(func() { managedtools.Tools = saved })
	extra := saved[1] // a third row shaped like q3map2, in its own repository
	extra.ID, extra.Title, extra.Repo = "q3map2-next", "q3map2 next (test row)", "q3map2-next"
	managedtools.Tools = append(managedtools.Tools, extra)
	r.server.Publish(extra.Owner+"/"+extra.Repo, mt.Build(t, mt.Spec{Tool: managedtools.Q3Map2, Version: "2.5.17n-ap.3050", Prerelease: true}))
	m := r.manager(func(o *managedtools.Options) {
		o.AskFirst = true
		o.Probes[extra.ID] = o.Probes[managedtools.Q3Map2]
	})
	steps, err := m.UpdateAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, step := range steps {
		if step.Tool == extra.ID {
			found = step.Action == managedtools.UpdateInstalling && step.To == "2.5.17n-ap.3050"
		}
	}
	if !found {
		t.Fatalf("the third row was not part of the Update: %+v", steps)
	}
	m.Step(context.Background())
	if view := tool(m.Status(), extra.ID); view.Active == nil || view.Active.Version != "2.5.17n-ap.3050" {
		t.Fatalf("the third row was not installed: %+v", view)
	}
}

// C5: an installation in use that its own built-in profile now refuses — the
// shape of a pre-1.4535 EricW a newer Companion names as not to be built with —
// is replaced by the newest verified release without anybody being asked, and
// nothing older is ever chosen.
func TestAnInstallationItsProfileRefusesIsReplacedByTheNewestVerifiedRelease(t *testing.T) {
	r := newRig(t)
	r.publishBoth("1.4566", "2.5.17n-ap.3031")
	m := r.manager()
	m.Step(context.Background())
	active := tool(m.Status(), managedtools.EricW).Active
	// What the program in use prints, rewritten to a refused release's.
	rewriteBanner(t, active.Dir, "2.0.0-alpha11-36-g2f25d6c2+auto-pigeon.1.4533")
	view := tool(m.Status(), managedtools.EricW)
	if !strings.Contains(view.Repair, "refuses") {
		t.Fatalf("a refused installation in use is not reported: %+v", view)
	}
	// A newer release, then an older one published later (first on the page).
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4570", Prerelease: true})
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4568", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	m.Step(context.Background())
	m.Step(context.Background())
	view = tool(m.Status(), managedtools.EricW)
	if view.Active == nil || view.Active.Version != "1.4570" || view.Repair != "" {
		t.Fatalf("the refused installation was not replaced by the newest (1.4570): %+v", view)
	}
	if view.Previous == nil || view.Previous.Version != "1.4566" {
		t.Errorf("the replaced installation is not kept to go back to: %+v", view.Previous)
	}
}

// C4: a Companion whose tools folder is the implicit one beside its bundle
// installs again INSIDE it what an earlier copy left elsewhere (a per-user
// folder, or the old place of a moved folder); a folder somebody chose is left
// as it is; the old files are never removed by this.
func TestAnInstallationOutsideTheImplicitToolsFolderIsInstalledAgainInsideIt(t *testing.T) {
	r := newRig(t)
	r.publishBoth("1.4566", "2.5.17n-ap.3031")
	elsewhere := r.manager() // the per-user folder of an earlier copy
	elsewhere.Step(context.Background())
	old := tool(elsewhere.Status(), managedtools.EricW).Active
	bundle := filepath.Join(t.TempDir(), "My Companion folder", managedtools.BuildToolsDirName)

	chosen := r.manager(func(o *managedtools.Options) { o.ToolsRoot = bundle })
	if view := tool(chosen.Status(), managedtools.EricW); view.Repair != "" {
		t.Fatalf("a chosen folder second-guessed: %+v", view.Repair)
	}

	beside := r.manager(func(o *managedtools.Options) { o.ToolsRoot, o.ImplicitRoot = bundle, true })
	if view := tool(beside.Status(), managedtools.EricW); !strings.Contains(view.Repair, "outside this Companion's tools folder") {
		t.Fatalf("the installation elsewhere is not reported: %+v", view)
	}
	r.clock.Advance(2 * time.Minute)
	beside.Step(context.Background())
	for _, id := range []managedtools.ToolID{managedtools.EricW, managedtools.Q3Map2} {
		view := tool(beside.Status(), id)
		if view.Active == nil || !strings.HasPrefix(view.Active.Dir, bundle) || view.Repair != "" {
			t.Fatalf("%s is not in the bundle's folder: %+v", id, view)
		}
	}
	if _, err := managedtools.ReadInstall(old.Dir); err != nil {
		t.Errorf("the earlier installation was touched: %v", err)
	}
}

// C4: a tools folder the Companion cannot write is said, with its path and a
// remedy, before anything is downloaded.
func TestAToolsFolderThatCannotBeWrittenIsSaidWithItsPath(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only folder needs POSIX permissions and a user they apply to")
	}
	parent := filepath.Join(t.TempDir(), "read only")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	r := newRig(t)
	root := filepath.Join(parent, "bundle", managedtools.BuildToolsDirName)
	m := r.manager(func(o *managedtools.Options) { o.ToolsRoot, o.ImplicitRoot = root, true })
	status := m.Status()
	if !strings.Contains(status.RootError, root) || !strings.Contains(status.RootError, "Settings") {
		t.Fatalf("root error %q; want the path and a remedy", status.RootError)
	}
	if _, err := os.Stat(filepath.Join(parent, "bundle")); !os.IsNotExist(err) {
		t.Fatalf("asking whether the folder can be written created something: %v", err)
	}
	if fine := r.manager().Status().RootError; fine != "" {
		t.Fatalf("a writable folder reads %q", fine)
	}
}

// C5: the newest official EricW is accepted by BOTH built-in fork profiles as
// they ship, although they were written for 1.4556: the accepted identity is
// the family (2.x, +auto-pigeon.), never an exact release. The live 1.4560
// banner (release-manifest.json, 2026-11-12) and one far beyond it are
// compatible; a refused release stays refused; a wrong family stays refused.
func TestTheNewestOfficialEricWIsAcceptedByProfilesWrittenForAnEarlierRelease(t *testing.T) {
	usage := "usage: vis [-threads n] [-logfile] [-basedir] [-gamedir] [-fast] [-level n] -q2bsp -leaktest\n"
	cases := []struct {
		banner string
		want   binding.FactState
	}{
		{"2.0.0-alpha11-74-gc8a1cfaf+auto-pigeon.1.4560", binding.FactCompatible},
		{"2.0.0-alpha12-3-g0000000+auto-pigeon.1.4999", binding.FactCompatible},
		{"2.0.0-alpha11-36-g2f25d6c2+auto-pigeon.1.4533", binding.FactIncompatible},
		{"2.0.0-alpha11", binding.FactIncompatible},
	}
	for _, id := range []string{builtin.EricwAutoPigeonQ1, builtin.EricwAutoPigeonQ2} {
		entry, err := builtin.Find(id)
		if err != nil {
			t.Fatal(err)
		}
		document := entry.Profile.(*profile.ToolProfile)
		if !strings.HasSuffix(document.ToolVersion, "1.4556") {
			t.Logf("%s now declares %s", id, document.ToolVersion)
		}
		for _, c := range cases {
			for _, executable := range []string{"qbsp", "vis", "light"} {
				state, _, _, detail := compat.Classify(document.CompatibilityProbe, executable, "ericw-tools "+c.banner+"\n"+usage, "")
				if state != c.want {
					t.Errorf("%s %s %s: %s (%s); want %s", id, executable, c.banner, state, detail, c.want)
				}
			}
		}
	}
	probes, err := managedtools.BuiltinProbes()
	if err != nil {
		t.Fatal(err)
	}
	if !probes[managedtools.EricW].Accepts("2.0.0-alpha11-74-gc8a1cfaf+auto-pigeon.1.4560") {
		t.Fatal("the managed install would refuse the newest official EricW")
	}
}

func rewriteBanner(t *testing.T, dir, banner string) {
	t.Helper()
	path := filepath.Join(dir, managedtools.InstallRecordName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["version_banner"] = banner
	out, _ := json.MarshalIndent(record, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
