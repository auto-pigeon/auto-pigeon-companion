package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
)

// NEW_365 J4/J5: which build "Play latest build" plays.

const backendA = "http://Aub.Example:9190/"

type built struct {
	id       string
	backend  string
	asset    string
	revision int
	state    job.State
	leak     bool
	family   string
	finished time.Time
}

func makeManifest(t *testing.T, dir string, b built) *Manifest {
	t.Helper()
	root := filepath.Join(dir, b.id)
	if err := os.MkdirAll(filepath.Join(root, "output", "bsp"), 0o700); err != nil {
		t.Fatal(err)
	}
	bsp := filepath.Join(root, "output", "bsp", "level.bsp")
	if err := os.WriteFile(bsp, []byte("BSP of "+b.id), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := digestFile(bsp)
	if err != nil {
		t.Fatal(err)
	}
	family := b.family
	if family == "-" {
		family = ""
	} else if family == "" {
		family = "quake1"
	}
	m := &Manifest{
		BuildID: b.id, State: b.state, EngineFamily: family,
		Pipeline:   DocumentRef{ID: "p", Name: "Q1 normal"},
		StartedAt:  b.finished.Add(-time.Minute),
		FinishedAt: b.finished,
		Inputs: []FileRecord{{Name: "source_map", Path: filepath.Join(root, "input", "Arena.map"), Source: &SourceRef{
			Backend: b.backend, AssetType: "map", AssetID: b.asset, Revision: b.revision, RevisionID: "rev" + b.id,
			DisplayName: "Arena",
		}}},
		Outputs: []FileRecord{{Name: "bsp", Path: bsp, Size: size, SHA256: digest}},
	}
	if b.leak {
		m.LeakTest = &leakadapter.Binding{Game: "quake1"}
	}
	return m
}

func at(minute int) time.Time { return time.Date(2026, 10, 28, 12, minute, 0, 0, time.UTC) }

func TestAnOlderNormalBuildIsPlayedPastANewerFailedOrLeakTest(t *testing.T) {
	dir := t.TempDir()
	manifests := []*Manifest{
		makeManifest(t, dir, built{id: "b-leak", backend: backendA, asset: "arena", revision: 7, state: job.Succeeded, leak: true, finished: at(40)}),
		makeManifest(t, dir, built{id: "b-failed", backend: backendA, asset: "arena", revision: 6, state: job.Failed, finished: at(30)}),
		makeManifest(t, dir, built{id: "b-cancel", backend: backendA, asset: "arena", revision: 6, state: job.Cancelled, finished: at(25)}),
		makeManifest(t, dir, built{id: "b-old", backend: backendA, asset: "arena", revision: 5, state: job.Succeeded, finished: at(10)}),
		makeManifest(t, dir, built{id: "b-older", backend: backendA, asset: "arena", revision: 4, state: job.Succeeded, finished: at(5)}),
		// The same name and id on another backend, newer: another map.
		makeManifest(t, dir, built{id: "b-other", backend: "http://other:9190", asset: "arena", revision: 9, state: job.Succeeded, finished: at(50)}),
	}
	got := Latest(manifests, MapIdentity{Backend: "http://aub.example:9190", AssetID: "arena"}, "quake1", nil)
	if got.State != LatestPlayable || got.BuildID != "b-old" || got.Revision != 5 {
		t.Fatalf("latest = %+v, want b-old at revision 5", got)
	}
	if got.LeakTests != 1 || got.NewerLeakTest != 7 {
		t.Fatalf("leak tests = %d, newer leak test revision = %d, want 1 and 7", got.LeakTests, got.NewerLeakTest)
	}
}

func TestOnlyLeakTestsIsNoPlayableBuild(t *testing.T) {
	dir := t.TempDir()
	manifests := []*Manifest{
		makeManifest(t, dir, built{id: "l1", backend: backendA, asset: "arena", revision: 1, state: job.Succeeded, leak: true, finished: at(1)}),
		makeManifest(t, dir, built{id: "l2", backend: backendA, asset: "arena", revision: 2, state: job.Failed, leak: true, finished: at(2)}),
	}
	got := Latest(manifests, MapIdentity{Backend: backendA, AssetID: "arena"}, "quake1", nil)
	if got.State != LatestNone || got.BuildID != "" || got.LeakTests != 2 || !strings.Contains(got.Reason, "only leak tests") {
		t.Fatalf("leak tests only = %+v", got)
	}
	if _, err := VerifyPlayable(manifests[0]); err == nil {
		t.Fatalf("a leak test verified as playable")
	}
}

func TestTiedFinishTimesAreOrderedByBuildIDEveryTime(t *testing.T) {
	dir := t.TempDir()
	a := makeManifest(t, dir, built{id: "20261028T120000Z-aaaaaaaa", backend: backendA, asset: "arena", revision: 3, state: job.Succeeded, finished: at(3)})
	b := makeManifest(t, dir, built{id: "20261028T120000Z-bbbbbbbb", backend: backendA, asset: "arena", revision: 3, state: job.Succeeded, finished: at(3)})
	for _, order := range [][]*Manifest{{a, b}, {b, a}} {
		if got := Latest(order, MapIdentity{Backend: backendA, AssetID: "arena"}, "", nil); got.BuildID != b.BuildID {
			t.Fatalf("tie = %s, want %s whatever the input order", got.BuildID, b.BuildID)
		}
	}
}

func TestAChangedOrMissingLevelIsUnavailableAndNotSilentlyReplaced(t *testing.T) {
	dir := t.TempDir()
	newest := makeManifest(t, dir, built{id: "n", backend: backendA, asset: "arena", revision: 3, state: job.Succeeded, finished: at(3)})
	older := makeManifest(t, dir, built{id: "o", backend: backendA, asset: "arena", revision: 2, state: job.Succeeded, finished: at(2)})
	want := MapIdentity{Backend: backendA, AssetID: "arena"}
	if err := os.WriteFile(newest.Outputs[0].Path, []byte("edited by hand"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Latest([]*Manifest{older, newest}, want, "quake1", nil)
	if got.State != LatestUnavailable || got.BuildID != "n" || !strings.Contains(got.Reason, "changed") {
		t.Fatalf("modified = %+v, want unavailable naming build n", got)
	}
	if err := os.Remove(newest.Outputs[0].Path); err != nil {
		t.Fatal(err)
	}
	if got = Latest([]*Manifest{older, newest}, want, "quake1", nil); got.State != LatestUnavailable || got.BuildID != "n" {
		t.Fatalf("missing = %+v", got)
	}
}

func TestUnknownFamilyAndAnotherGameAreNotOffered(t *testing.T) {
	dir := t.TempDir()
	legacy := makeManifest(t, dir, built{id: "legacy", backend: backendA, asset: "arena", revision: 1, state: job.Succeeded, family: "-", finished: at(9)})
	q2 := makeManifest(t, dir, built{id: "q2", backend: backendA, asset: "arena", revision: 1, state: job.Succeeded, family: "quake2", finished: at(8)})
	want := MapIdentity{Backend: backendA, AssetID: "arena"}
	got := Latest([]*Manifest{legacy, q2}, want, "quake1", nil)
	if got.State != LatestNone || !strings.Contains(got.Reason, "quake2") {
		t.Fatalf("for a Quake engine = %+v, want none, naming the other game", got)
	}
	if got = Latest([]*Manifest{legacy, q2}, want, "quake2", nil); got.State != LatestPlayable || got.BuildID != "q2" {
		t.Fatalf("for a Quake II engine = %+v", got)
	}
	// No family asked for: still never the legacy row of unknown game.
	if got = Latest([]*Manifest{legacy}, want, "", nil); got.State != LatestNone {
		t.Fatalf("legacy only = %+v", got)
	}
	// A build of a file on this disk names no saved map.
	local := makeManifest(t, dir, built{id: "local", backend: backendA, asset: "arena", revision: 1, state: job.Succeeded, finished: at(1)})
	local.Inputs[0].Source = nil
	if _, err := IdentityOf(local); err == nil {
		t.Fatalf("a local file build has a saved-map identity")
	}
}

func TestABuildOfALeakPipelineIsADiagnosticHoweverItWasStarted(t *testing.T) {
	dir := t.TempDir()
	// A successful build of the built-in Quake 1 leak-test pipeline that did
	// not come through an editor's request: no leak_test binding, a BSP from
	// QBSP alone. Still a diagnostic, never playable.
	direct := makeManifest(t, dir, built{id: "direct", backend: backendA, asset: "arena", revision: 6, state: job.Succeeded, finished: at(9)})
	direct.Pipeline.ID = "auto-pigeon.q1.leak-test"
	pinned := makeManifest(t, dir, built{id: "pinned", backend: backendA, asset: "arena", revision: 6, state: job.Succeeded, finished: at(8)})
	pinned.Pipeline.ID = "local.pipeline.my-leak-test"
	normal := makeManifest(t, dir, built{id: "normal", backend: backendA, asset: "arena", revision: 5, state: job.Succeeded, finished: at(1)})
	isLeak := func(id string) bool { return id == "local.pipeline.my-leak-test" }
	got := Latest([]*Manifest{direct, pinned, normal}, MapIdentity{Backend: backendA, AssetID: "arena"}, "quake1", isLeak)
	if got.State != LatestPlayable || got.BuildID != "normal" || got.LeakTests != 2 || got.NewerLeakTest != 6 {
		t.Fatalf("latest = %+v, want the normal build, two leak tests, the newer at revision 6", got)
	}
	if _, err := VerifyPlayable(direct); err == nil {
		t.Fatalf("a leak pipeline's build verified as playable")
	}
}
