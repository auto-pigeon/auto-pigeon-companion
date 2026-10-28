package managedtools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
)

// selfHost is the Companion side of its own update: Prove and Restart
// record what was asked; busy is what Restart finds running.
type selfHost struct {
	mu        sync.Mutex
	proved    []string
	restarted []managedtools.InstallRecord
	busy      []string
	proveErr  error
}

func (h *selfHost) Prove(_ context.Context, record managedtools.InstallRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.proved = append(h.proved, record.Version)
	return h.proveErr
}

func (h *selfHost) SelfBusy() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.busy...)
}

func (h *selfHost) RestartForUpdate(record managedtools.InstallRecord) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.busy) > 0 {
		return append([]string(nil), h.busy...), nil
	}
	h.restarted = append(h.restarted, record)
	return nil, nil
}

func (h *selfHost) restarts() []managedtools.InstallRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]managedtools.InstallRecord(nil), h.restarted...)
}

// selfRig is a rig whose coordinator also updates a running Companion.
func selfRig(t *testing.T, version string, mode managedtools.SelfMode) (*rig, *selfHost, func(...func(*managedtools.Options)) *managedtools.Manager) {
	t.Helper()
	r := newRig(t)
	r.publishBoth("1.4536", "2.5.17n-ap.3031")
	host := &selfHost{}
	self := &managedtools.SelfInstall{Version: version, Platform: mt.ThisBundlePlatform(), Mode: mode,
		Root: filepath.Join(t.TempDir(), "Auto Pigeon — bundle"), Reason: "a reason"}
	make := func(change ...func(*managedtools.Options)) *managedtools.Manager {
		return r.manager(append([]func(*managedtools.Options){func(o *managedtools.Options) {
			o.Self, o.SelfHost = self, host
		}}, change...)...)
	}
	return r, host, make
}

func (r *rig) publishCompanion(spec mt.CompanionSpec) *mt.Release {
	return r.server.Publish(mt.CompanionRepo, mt.BuildCompanion(r.t, spec))
}

func companionDownloads(s *mt.Server) int {
	n := 0
	s.Mutate(func() {
		for name, count := range s.Downloads {
			if strings.HasPrefix(name, "auto-pigeon-companion-") {
				n += count
			}
		}
	})
	return n
}

// S1: only a newer, qualified release for this platform is offered, ordered
// by number and not by page order; drafts and an unqualified build are not
// updates; nothing of the Companion is downloaded before somebody chooses.
func TestTheCompanionIsOfferedOnlyANewerQualifiedReleaseAndNothingIsDownloadedUnasked(t *testing.T) {
	r, _, make := selfRig(t, "1.318", managedtools.SelfPortable)
	r.publishCompanion(mt.CompanionSpec{Version: "1.317", Prerelease: true})
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	r.publishCompanion(mt.CompanionSpec{Version: "1.321", Prerelease: true, Verdict: "build_only"})
	r.publishCompanion(mt.CompanionSpec{Version: "1.322", Prerelease: true, Draft: true})
	// Published last, so first on the page: "1.99" is older than "1.320".
	r.publishCompanion(mt.CompanionSpec{Version: "1.99", Prerelease: true})
	m := make()
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.Companion)
	if view.Kind != managedtools.KindApplication || view.Phase != managedtools.PhaseReady || view.Version != "1.318" {
		t.Fatalf("the Companion's row: %+v", view)
	}
	if view.Offer == nil || view.Offer.Version != "1.320" || view.Offer.Sidecar == nil || view.Offer.Sidecar.Version != "1.245" {
		t.Fatalf("offer %+v; want 1.320 with its extractor", view.Offer)
	}
	var unqualified bool
	for _, verdict := range view.Newer {
		if verdict.Version == "1.321" && verdict.Reason == managedtools.ReasonNotQualified {
			unqualified = true
		}
		if verdict.Version == "1.322" {
			t.Fatalf("a draft was considered: %+v", verdict)
		}
	}
	if !unqualified {
		t.Fatalf("1.321 (build_only) is not reported as unqualified: %+v", view.Newer)
	}
	if n := companionDownloads(r.server); n != 0 {
		t.Fatalf("%d Companion archive downloads before anybody chose an update", n)
	}
	// The toolchains are still set up beside it, by the same coordinator.
	if tool(m.Status(), managedtools.EricW).Active == nil || tool(m.Status(), managedtools.Q3Map2).Active == nil {
		t.Fatal("the toolchains were not set up by the coordinator that also checks the Companion")
	}

	stable, _, makeStable := selfRig(t, "1.318", managedtools.SelfPortable)
	stable.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	ms := makeStable(func(o *managedtools.Options) { o.Channel = managedtools.ChannelStable })
	ms.Step(context.Background())
	if offer := tool(ms.Status(), managedtools.Companion).Offer; offer != nil {
		t.Fatalf("a prerelease was offered on the stable channel: %+v", offer)
	}
}

// S1: "don't ask again" is this target, this version and these bytes; the
// toolchains' offers are their own; a newer version is offered again; two
// targets can be on offer at once.
func TestSkippingTheCompanionsVersionIsExactAndSeparateFromTheTools(t *testing.T) {
	r, _, make := selfRig(t, "1.318", managedtools.SelfPortable)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	m := make()
	m.Step(context.Background())
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4540", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	m.Step(context.Background())
	status := m.Status()
	self, ericw := tool(status, managedtools.Companion), tool(status, managedtools.EricW)
	if self.Offer == nil || ericw.Offer == nil {
		t.Fatalf("two offers expected at once: companion %+v, ericw %+v", self.Offer, ericw.Offer)
	}
	if err := m.Decide(managedtools.Companion, managedtools.DecideCancel, self.Offer.Identity(), true); err != nil {
		t.Fatal(err)
	}
	status = m.Status()
	if tool(status, managedtools.Companion).Offer != nil {
		t.Fatal("the skipped Companion version is still offered")
	}
	if tool(status, managedtools.EricW).Offer == nil || len(tool(status, managedtools.EricW).Skipped) != 0 {
		t.Fatal("skipping the Companion's version touched EricW's offer")
	}
	// A new Companion process: still skipped (not a session dismissal).
	m2 := make()
	if tool(m2.Status(), managedtools.Companion).Offer != nil {
		t.Fatal("the skipped version came back in a new process")
	}
	r.publishCompanion(mt.CompanionSpec{Version: "1.323", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	m2.Step(context.Background())
	if offer := tool(m2.Status(), managedtools.Companion).Offer; offer == nil || offer.Version != "1.323" {
		t.Fatalf("a newer version is not offered after a skip: %+v", offer)
	}
}

// S1: a development build is never offered an automatic update (it cannot
// be ordered against a release), and is shown the release by hand.
func TestADevelopmentBuildIsShownTheReleaseByHandAndNeverReplaced(t *testing.T) {
	r, host, make := selfRig(t, "unknown", managedtools.SelfDevelopment)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	m := make()
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.Companion)
	if view.Offer != nil || view.Self == nil || view.Self.Manual == nil || view.Self.Manual.Version != "1.320" {
		t.Fatalf("development build: offer %+v, manual %+v", view.Offer, view.Self)
	}
	identity := view.Self.Manual.Identity()
	if err := m.Decide(managedtools.Companion, managedtools.DecideNow, identity, false); err == nil {
		t.Fatal("Update now was accepted for a development build")
	}
	m.Step(context.Background())
	if companionDownloads(r.server) != 0 || len(host.restarts()) != 0 {
		t.Fatal("a development build downloaded or restarted for an update")
	}
}

// S2/S3: a valid bundle is staged once chosen, proved, and the restart is
// asked for only when nothing runs; the record names every file of the
// bundle, the manifest included.
func TestUpdateNowStagesAProvedBundleAndRestartsOnlyWhenIdle(t *testing.T) {
	r, host, make := selfRig(t, "1.318", managedtools.SelfPortable)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	m := make()
	m.Step(context.Background())
	offer := tool(m.Status(), managedtools.Companion).Offer
	host.busy = []string{"Build Q1 room — auto-pigeon.q1.normal"}
	if err := m.Decide(managedtools.Companion, managedtools.DecideNow, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.Companion)
	if view.Pending == nil || !view.Pending.Staged || len(view.Pending.Waiting) != 1 || len(host.restarts()) != 0 {
		t.Fatalf("busy: pending %+v, restarts %d", view.Pending, len(host.restarts()))
	}
	if len(host.proved) != 1 {
		t.Fatalf("the staged bundle was proved %d times", len(host.proved))
	}
	host.mu.Lock()
	host.busy = nil
	host.mu.Unlock()
	m.Step(context.Background())
	restarts := host.restarts()
	if len(restarts) != 1 || restarts[0].Version != "1.320" {
		t.Fatalf("restarts %+v", restarts)
	}
	record := restarts[0]
	manifest, companion := managedtools.BundleLayout(mt.ThisBundlePlatform())
	paths := map[string]bool{}
	for _, member := range record.Members {
		paths[member.Path] = true
		if _, err := os.Stat(filepath.Join(record.Dir, filepath.FromSlash(member.Path))); err != nil {
			t.Fatalf("staged %s: %v", member.Path, err)
		}
	}
	for _, want := range []string{manifest, companion, "SHA256SUMS"} {
		if !paths[want] {
			t.Fatalf("the staged record does not list %s: %+v", want, record.Members)
		}
	}
	if !tool(m.Status(), managedtools.Companion).Pending.Restarting {
		t.Fatal("the row does not say it is restarting")
	}
	// Asked once: the next step does not restart again.
	m.Step(context.Background())
	if len(host.restarts()) != 1 {
		t.Fatal("the restart was asked for twice")
	}
	if _, ok := m.SelfAtExit(true); !ok {
		t.Fatal("the restart's bundle is not what the exit installs")
	}
}

// S4: "Update when I close" stages in the background, never restarts, and
// is what the close installs; a recorded result ends the choice either way.
func TestUpdateWhenICloseStagesAndIsInstalledOnlyAtTheClose(t *testing.T) {
	r, host, make := selfRig(t, "1.318", managedtools.SelfPortable)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	m := make()
	m.Step(context.Background())
	offer := tool(m.Status(), managedtools.Companion).Offer
	if err := m.Decide(managedtools.Companion, managedtools.DecideOnExit, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	if len(host.restarts()) != 0 {
		t.Fatal("Update when I close restarted the Companion")
	}
	pending := tool(m.Status(), managedtools.Companion).Pending
	if pending == nil || !pending.Staged || pending.Intent != managedtools.IntentOnExit {
		t.Fatalf("pending %+v", pending)
	}
	if _, ok := m.SelfAtExit(true); ok {
		t.Fatal("an on-close update is offered as a restart")
	}
	record, ok := m.SelfAtExit(false)
	if !ok || record.Version != "1.320" {
		t.Fatalf("the close installs %+v %v", record, ok)
	}
	// A failed replacement is not tried again by itself.
	if err := m.RecordSelfResult(managedtools.SelfResult{From: "1.318", To: "1.320", Outcome: "rolled_back",
		Detail: "did not pass its proving start", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.SelfAtExit(false); ok {
		t.Fatal("a rolled-back candidate is installed again at the next close")
	}
	view := tool(m.Status(), managedtools.Companion)
	if view.Self.Last == nil || view.Self.Last.Outcome != "rolled_back" || view.Failure == nil {
		t.Fatalf("the failure is not shown: %+v", view)
	}
	// Cancel pending before activation is available.
	if err := m.Decide(managedtools.Companion, managedtools.DecideOnExit, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelPending(managedtools.Companion); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.SelfAtExit(false); ok {
		t.Fatal("a cancelled update is installed at the close")
	}
}

// A Companion in a folder it cannot write is offered the update, which is
// downloaded and verified and then left for a person to put in place.
func TestAProtectedInstallationIsDownloadedButNeverReplacedByTheCompanion(t *testing.T) {
	r, host, make := selfRig(t, "1.318", managedtools.SelfManual)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	m := make()
	m.Step(context.Background())
	offer := tool(m.Status(), managedtools.Companion).Offer
	if offer == nil {
		t.Fatal("no offer")
	}
	if err := m.Decide(managedtools.Companion, managedtools.DecideNow, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	pending := tool(m.Status(), managedtools.Companion).Pending
	if pending == nil || !pending.Staged || !strings.Contains(pending.Manual, "replace the contents of") {
		t.Fatalf("pending %+v", pending)
	}
	if len(host.restarts()) != 0 {
		t.Fatal("a protected installation was restarted for replacement")
	}
	if _, ok := m.SelfAtExit(false); ok {
		t.Fatal("a protected installation is replaced at the close")
	}
}

// S2: a bundle that is not what its release says is refused before
// anything is staged, and the running bundle is never asked to restart.
func TestEveryCompanionBundleThatIsNotWhatItsReleaseSaysIsRefused(t *testing.T) {
	manifest, companion := managedtools.BundleLayout(mt.ThisBundlePlatform())
	cases := []struct {
		name string
		spec mt.CompanionSpec
		want string
	}{
		{"wrong extractor for this Companion", mt.CompanionSpec{ReleaseExtractorVersion: "1.246"}, "bundles extractor 1.245 and its release names 1.246"},
		{"no bundle manifest", mt.CompanionSpec{Archive: func(e map[string][]byte) map[string][]byte { delete(e, manifest); return e }}, "carries no"},
		{"missing extractor", mt.CompanionSpec{Archive: func(e map[string][]byte) map[string][]byte {
			for name := range e {
				if strings.Contains(name, "auto-pigeon-extractor") {
					delete(e, name)
				}
			}
			return e
		}}, "does not contain"},
		{"a file the manifest does not list", mt.CompanionSpec{Archive: func(e map[string][]byte) map[string][]byte { e["dependencies/extra.so"] = []byte("x"); return e }}, "does not list"},
		{"changed program bytes", mt.CompanionSpec{Archive: func(e map[string][]byte) map[string][]byte {
			changed := append([]byte(nil), e[companion]...)
			changed[len(changed)-1] ^= 0xff
			e[companion] = changed
			return e
		}}, "SHA-256 differs"},
		{"a link", mt.CompanionSpec{Archive: func(e map[string][]byte) map[string][]byte {
			e["@link:dependencies/evil"] = []byte("/etc/passwd")
			return e
		}}, "not a regular file"},
		{"names that collide without case", mt.CompanionSpec{Archive: func(e map[string][]byte) map[string][]byte { e["sha256sums"] = []byte("x"); return e }}, "ignores case"},
		{"an entry outside the folder", mt.CompanionSpec{Archive: func(e map[string][]byte) map[string][]byte { e["!../escape"] = []byte("x"); return e }}, "outside"},
		{"a bundle for another platform", mt.CompanionSpec{Bundle: func(m map[string]any) { m["platform"] = "windows-arm64" }}, "bundle and this computer needs"},
		{"a manifest that does not pair an extractor", mt.CompanionSpec{Bundle: func(m map[string]any) { delete(m, "extractor") }}, "names no extractor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, host, make := selfRig(t, "1.318", managedtools.SelfPortable)
			spec := c.spec
			spec.Version, spec.Prerelease = "1.320", true
			r.publishCompanion(spec)
			m := make()
			m.Step(context.Background())
			offer := tool(m.Status(), managedtools.Companion).Offer
			if offer == nil {
				t.Fatalf("not offered at all: %+v", tool(m.Status(), managedtools.Companion).Newer)
			}
			if err := m.Decide(managedtools.Companion, managedtools.DecideNow, offer.Identity(), false); err != nil {
				t.Fatal(err)
			}
			m.Step(context.Background())
			view := tool(m.Status(), managedtools.Companion)
			if view.Failure == nil || !strings.Contains(view.Failure.Error, c.want) {
				t.Fatalf("failure %+v; want %q", view.Failure, c.want)
			}
			if view.Pending != nil && view.Pending.Staged {
				t.Fatal("a refused bundle is staged")
			}
			if len(host.restarts()) != 0 || len(host.proved) != 0 {
				t.Fatal("a refused bundle was proved or restarted for")
			}
			entries, _ := os.ReadDir(filepath.Join(r.root, "companion"))
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".") && entry.Name() != "install.lock" {
					t.Fatalf("a refused bundle left %s", entry.Name())
				}
			}
		})
	}
	// A release that names no extractor is not an update at all.
	r, _, make := selfRig(t, "1.318", managedtools.SelfPortable)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true, NoAUE: true})
	m := make()
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.Companion)
	if view.Offer != nil || len(view.Newer) == 0 || view.Newer[0].Reason != managedtools.ReasonNoSidecar {
		t.Fatalf("a release without an extractor: offer %+v newer %+v", view.Offer, view.Newer)
	}
}

// S2: a bundle whose proving start fails is not staged and not restarted
// for; the running Companion is untouched.
func TestABundleThatFailsItsProvingStartIsNeverStaged(t *testing.T) {
	r, host, make := selfRig(t, "1.318", managedtools.SelfPortable)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	host.proveErr = errors.New("its extractor is not the one its bundle manifest lists")
	m := make()
	m.Step(context.Background())
	offer := tool(m.Status(), managedtools.Companion).Offer
	if err := m.Decide(managedtools.Companion, managedtools.DecideNow, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.Companion)
	if view.Failure == nil || !strings.Contains(view.Failure.Error, "proving") && !strings.Contains(view.Failure.Error, "version check") {
		t.Fatalf("failure %+v", view.Failure)
	}
	if len(host.restarts()) != 0 {
		t.Fatal("restarted for a bundle that failed its proving start")
	}
}

// InspectSelf: a release bundle in a writable folder is portable; the same
// bundle whose program is not the one listed, or with no manifest, is a
// development build; an unstamped version is never ordered.
func TestInspectSelfTellsAReleaseBundleFromEverythingElse(t *testing.T) {
	platform := mt.ThisBundlePlatform()
	if strings.HasPrefix(platform, "darwin-") {
		t.Skip("a macOS bundle is an application folder; covered by its layout")
	}
	root := filepath.Join(t.TempDir(), "Auto Pigeon — ünï", "auto-pigeon-companion-1.318-"+platform)
	entries := mt.CompanionEntries(t, mt.CompanionSpec{Version: "1.318"})
	for name, body := range entries {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath, companion := managedtools.BundleLayout(platform)
	exe := filepath.Join(root, filepath.FromSlash(companion))
	self := managedtools.InspectSelf(exe, "1.318", "abc", strings.Split(platform, "-")[0], strings.Split(platform, "-")[1])
	if self.Mode != managedtools.SelfPortable || self.ExtractorVersion != "1.245" || len(self.Members) != 4 {
		t.Fatalf("a release bundle: %+v", self)
	}
	if dev := managedtools.InspectSelf(exe, "unknown", "", strings.Split(platform, "-")[0], strings.Split(platform, "-")[1]); dev.Mode != managedtools.SelfDevelopment {
		t.Fatalf("an unstamped build: %+v", dev)
	}
	if err := os.WriteFile(exe, []byte("not the listed program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if changed := managedtools.InspectSelf(exe, "1.318", "", strings.Split(platform, "-")[0], strings.Split(platform, "-")[1]); changed.Mode != managedtools.SelfDevelopment ||
		!strings.Contains(changed.Reason, "not the one") {
		t.Fatalf("a changed program: %+v", changed)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(manifestPath))); err != nil {
		t.Fatal(err)
	}
	if bare := managedtools.InspectSelf(exe, "1.318", "", strings.Split(platform, "-")[0], strings.Split(platform, "-")[1]); bare.Mode != managedtools.SelfDevelopment {
		t.Fatalf("no bundle manifest: %+v", bare)
	}
}

// Found live: "Update now" for the Companion and for EricW chosen together
// must not restart the Companion while EricW is still being installed or
// waits to be switched; the restart comes after.
func TestTheCompanionRestartWaitsForAToolchainUpdateInProgress(t *testing.T) {
	r, host, make := selfRig(t, "1.318", managedtools.SelfPortable)
	r.publishCompanion(mt.CompanionSpec{Version: "1.320", Prerelease: true})
	m := make()
	m.Step(context.Background())
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4540", Prerelease: true})
	r.clock.Advance(2 * time.Hour)
	m.Step(context.Background())
	status := m.Status()
	for _, id := range []managedtools.ToolID{managedtools.EricW, managedtools.Companion} {
		if err := m.Decide(id, managedtools.DecideNow, tool(status, id).Offer.Identity(), false); err != nil {
			t.Fatal(err)
		}
	}
	// A build holds the old EricW: its switch waits, and so does the restart.
	r.host.setBusy("Build — auto-pigeon.q1.normal")
	m.Step(context.Background())
	pending := tool(m.Status(), managedtools.Companion).Pending
	if len(host.restarts()) != 0 || pending == nil || len(pending.Waiting) == 0 || !strings.Contains(strings.Join(pending.Waiting, ";"), "EricW") {
		t.Fatalf("restarted while EricW waited: restarts %d, pending %+v", len(host.restarts()), pending)
	}
	r.host.setBusy()
	m.Step(context.Background())
	if v := tool(m.Status(), managedtools.EricW).Version; v != "1.4540" {
		t.Fatalf("EricW is %s", v)
	}
	if len(host.restarts()) != 1 {
		t.Fatalf("restarts %d after EricW was switched", len(host.restarts()))
	}
}
