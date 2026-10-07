package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// receive records a link for the fixture asset, as the scheme handler would.
func (m *machine) receiveLeakLink(requestID, profile string) aub.LeakTestLink {
	m.t.Helper()
	dir, err := m.server.configDir()
	if err != nil {
		m.t.Fatal(err)
	}
	link := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: requestID, Profile: profile}
	if err := leakintent.Receive(leakintent.Path(dir), link, time.Now().UTC()); err != nil {
		m.t.Fatal(err)
	}
	return link
}

// The review chooses the pipeline from the game the pinned revision's own
// bytes declare. A game with no measured compiler is unsupported — by name,
// with nothing to run — and is never reviewed as Quake 1.
func TestALeakRequestIsReviewedForTheGameTheSavedRevisionDeclares(t *testing.T) {
	for _, c := range []struct {
		game, hint         string
		pipeline, compiler string
		unsupported        bool
	}{
		{game: "quake1", pipeline: "auto-pigeon.q1.leak-test", compiler: "ericw-qbsp"},
		{game: "quake3", pipeline: "auto-pigeon.q3.leak-test", compiler: "q3map2"},
		{game: "quake3", hint: "quake3", pipeline: "auto-pigeon.q3.leak-test", compiler: "q3map2"},
		{game: "quake2", unsupported: true},
		{game: "quake2", hint: "quake2", unsupported: true},
		{game: "hexen2", unsupported: true},
	} {
		m := newMachine(t)
		m.backend.savedAPMap(c.game)
		m.signIn()
		link := m.receiveLeakLink(strings.Repeat("3", 32), c.hint)
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
		if status != http.StatusOK || body["game_profile"] != c.game {
			t.Fatalf("%s: %d %v", c.game, status, body)
		}
		if c.unsupported {
			if body["unsupported"] != true || body["pipeline"] != nil || body["source_ref"] != nil {
				t.Errorf("%s must be unsupported with nothing to run: %v", c.game, body)
			}
			if got := strings.Join(m.leakStatusesSoon(t, 1), "|"); !strings.Contains(got, link.RequestID+" blocked unsupported_profile") {
				t.Errorf("%s: the editor was told %q", c.game, got)
			}
			continue
		}
		if body["pipeline"] != c.pipeline || body["compiler"] != c.compiler || body["unsupported"] != nil ||
			!strings.Contains(body["source_ref"].(string), "@"+m.backend.asset.revisionID+"#fixture.apmap") {
			t.Errorf("%s: %v", c.game, body)
		}
	}
}

// A hint is checked against the saved revision and never used instead of it.
func TestALeakLinkWhoseProfileHintDisagreesWithTheSavedRevisionIsRefused(t *testing.T) {
	for _, c := range []struct{ game, hint string }{{"quake3", "quake1"}, {"quake1", "quake3"}, {"quake2", "quake1"}} {
		m := newMachine(t)
		m.backend.savedAPMap(c.game)
		m.signIn()
		link := m.receiveLeakLink(strings.Repeat("4", 32), c.hint)
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
		if status != http.StatusConflict || body["pipeline"] != nil {
			t.Fatalf("a %s map asked for as %s: %d %v", c.game, c.hint, status, body)
		}
		if got := strings.Join(m.leakStatusesSoon(t, 1), "|"); !strings.Contains(got, link.RequestID+" blocked profile_mismatch") {
			t.Errorf("the editor was told %q", got)
		}
	}
}

// The build start uses the same resolver on the bytes it staged. The wrong
// pipeline for the map's game, a game with no adapter and a source whose game
// cannot be read all start nothing.
func TestALeakBuildMayOnlyRunThePipelineOfTheMapsOwnGame(t *testing.T) {
	pinned := newMachine(t)
	q3 := build.Conversion{Game: "quake3"}
	if binding, err := pinned.server.leakBindingForBuild("auto-pigeon.q3.leak-test", q3, true); err != nil || binding.Game != "quake3" {
		t.Fatalf("the right pipeline: %+v %v", binding, err)
	}
	for name, c := range map[string]struct {
		pipeline  string
		game      string
		converted bool
	}{
		"a Quake III map through the Quake 1 test":  {"auto-pigeon.q1.leak-test", "quake3", true},
		"a Quake 1 map through the Quake III test":  {"auto-pigeon.q3.leak-test", "quake1", true},
		"a Quake III map through an ordinary build": {"auto-pigeon.q3.normal", "quake3", true},
		"a Quake II map":                 {"auto-pigeon.q1.leak-test", "quake2", true},
		"a map naming no game":           {"auto-pigeon.q1.leak-test", "", true},
		"a source that was not an APMap": {"auto-pigeon.q1.leak-test", "quake1", false},
	} {
		if binding, err := pinned.server.leakBindingForBuild(c.pipeline, build.Conversion{Game: c.game}, c.converted); err == nil {
			t.Errorf("%s: accepted as %+v", name, binding)
		}
	}

	// And through the route: the request is refused before a job exists, and
	// the pending request is still there for the right review.
	m := newMachine(t)
	m.backend.savedAPMap("quake3")
	m.signIn()
	link := m.receiveLeakLink(strings.Repeat("7", 32), "quake3")
	jobs := len(m.jobIDs())
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline":        "aucom.fixture.pipeline",
		"inputs":          map[string]string{"source_map": "aub:map/" + link.AssetID + "@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	})
	if status < 400 {
		t.Fatalf("a leak request through another pipeline started: %d %v", status, body)
	}
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("%d job(s) were started", got-jobs)
	}
	if pending := m.server.pendingLeakRequest(link.RequestID); pending == nil {
		t.Fatal("the refused start consumed the pending request")
	}
}

// q3LeakBuild writes a finished Quake III leak-test build the way the runner
// leaves one, from the real Q3Map2 outputs kept in internal/leakadapter.
func (m *machine) q3LeakBuild(id, control string, state job.State, withLin, withBSP bool) *build.Manifest {
	m.t.Helper()
	vectors := filepath.Join("..", "leakadapter", "testdata", "q3")
	dir := filepath.Join(m.builds, id)
	put := func(name, file string, contents []byte) build.FileRecord {
		path := filepath.Join(dir, "output", name, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			m.t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			m.t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		return build.FileRecord{Name: name, Path: path, Size: int64(len(contents)), SHA256: "sha256:" + hex.EncodeToString(sum[:])}
	}
	log, err := os.ReadFile(filepath.Join(vectors, "logs", control+".verbose.log"))
	if err != nil {
		m.t.Fatal(err)
	}
	outputs := []build.FileRecord{put("compile_log", "compile.stdout.log", log)}
	if withLin {
		lin, err := os.ReadFile(filepath.Join(vectors, "lin", control+".lin"))
		if err != nil {
			m.t.Fatal(err)
		}
		outputs = append(outputs, put("lin", "fixture.lin", lin))
	} else {
		outputs = append(outputs, build.FileRecord{Name: "lin", Missing: true, Optional: true})
	}
	if withBSP {
		outputs = append(outputs, put("bsp", "fixture.bsp", []byte("IBSP")))
	} else {
		outputs = append(outputs, build.FileRecord{Name: "bsp", Missing: true, Optional: true})
	}
	zero := 0
	manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id, EngineFamily: "quake3",
		Pipeline: build.DocumentRef{ID: "auto-pigeon.q3.leak-test"}, State: state,
		Inputs: []build.FileRecord{{Name: "source_map", Path: filepath.Join(dir, "input", "source_map", "fixture.map"),
			SHA256: "sha256:" + strings.Repeat("b", 64),
			Source: &build.SourceRef{AssetType: aub.AssetTypeMap, AssetID: "saved-map", RevisionID: "immutable-revision",
				Revision: 7, ContentSHA256: strings.Repeat("a", 64), Refetchable: true},
			Conversion: &build.Conversion{From: "apmap", To: "map", Game: "quake3", SourceSHA256: "sha256:" + strings.Repeat("a", 64)}}},
		Tools:   []build.ToolRecord{{ToolVersion: "2.5.17n-git-68ecbed"}},
		Steps:   []build.Step{{ID: "compile", State: state, ExitCode: &zero}},
		Outputs: outputs}
	if err := manifest.Save(dir); err != nil {
		m.t.Fatal(err)
	}
	return manifest
}

// The Quake III envelope names what ran, keeps the three states apart, and
// carries the Companion's reading — which a failed build still has.
func TestAQuake3LeakResultIsAOnePointOneEnvelopeFromAFailedBuild(t *testing.T) {
	m := newMachine(t)
	m.q3LeakBuild("20261003T000000Z-00000001", "b_gap", job.Failed, true, false)
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/20261003T000000Z-00000001/result", nil)
	diagnostic, _ := body["diagnostic"].(map[string]any)
	if status != http.StatusOK || body["schema_version"] != "aucom.leak-result/1.1" ||
		body["game_profile"] != "quake3" || body["compiler"] != "q3map2" ||
		body["pointfile_format"] != "q3map2-lin" || body["pointfile_direction"] != "outside_to_occupant" ||
		body["build_state"] != "failed" || body["compile_exit_code"] != float64(0) ||
		body["content_sha256"] != strings.Repeat("a", 64) || body["compiler_source_sha256"] != strings.Repeat("b", 64) ||
		diagnostic["outcome"] != "leak" || diagnostic["route_points"] != float64(3) ||
		!strings.HasPrefix(body["pointfile"].(string), "280.000000 136.000000 128.000000") ||
		!strings.Contains(body["log"].(string), "Entity leaked") {
		t.Fatalf("leaked build: %d %v", status, body)
	}
	// The BSP is evidence that it exists, never cargo.
	for key := range body {
		if strings.Contains(key, "bsp") {
			t.Errorf("the envelope carries %q", key)
		}
	}
	status, view := m.call(http.MethodGet, "/api/v1/build/runs/20261003T000000Z-00000001", nil)
	leak, _ := view["leak_test"].(map[string]any)
	if status != http.StatusOK || leak["game_profile"] != "quake3" || leak["pointfile_output"] != "lin" ||
		leak["diagnostic"].(map[string]any)["outcome"] != "leak" {
		t.Fatalf("the Build page's view: %d %v", status, leak)
	}

	for _, c := range []struct {
		id, control string
		state       job.State
		lin, bsp    bool
		want        string
	}{
		{"20261003T000000Z-00000002", "a_sealed", job.Succeeded, false, true, "no_leak"},
		// The same clean log with no BSP behind it is no verdict.
		{"20261003T000000Z-00000003", "a_sealed", job.Succeeded, false, false, "incomplete"},
		{"20261003T000000Z-00000004", "d_no_occupant", job.Failed, false, false, "no_interior"},
		{"20261003T000000Z-00000005", "d_in_solid", job.Failed, false, false, "no_interior"},
		{"20261003T000000Z-00000006", "f_malformed", job.Failed, false, false, "incomplete"},
		// A cancelled run that had said nothing about a leak.
		{"20261003T000000Z-00000007", "a_sealed", job.Cancelled, false, false, "incomplete"},
		{"20261003T000000Z-00000008", "e_patch_cover", job.Failed, true, false, "leak"},
	} {
		m.q3LeakBuild(c.id, c.control, c.state, c.lin, c.bsp)
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+c.id+"/result", nil)
		diagnostic, _ := body["diagnostic"].(map[string]any)
		if status != http.StatusOK || diagnostic["outcome"] != c.want {
			t.Errorf("%s as %s: %d %v", c.control, c.state, status, diagnostic)
		}
	}
}

// A line file is this build's or it is nothing: one named after another
// source, one changed since it was collected, and a build that ran the wrong
// game's compiler are all refused rather than returned.
func TestAQuake3LeakResultRefusesWhatIsNotThisBuildsOwn(t *testing.T) {
	m := newMachine(t)
	id := "20261003T000000Z-0000000a"
	manifest := m.q3LeakBuild(id, "b_gap", job.Failed, true, false)
	result := "/api/v1/leak-test/runs/" + id + "/result"
	save := func() {
		if err := manifest.Save(filepath.Join(m.builds, id)); err != nil {
			t.Fatal(err)
		}
	}
	lin := manifest.Outputs[1].Path

	// Named after some other map: a leftover, whatever its digest says.
	other := filepath.Join(filepath.Dir(lin), "yesterday.lin")
	if err := os.Rename(lin, other); err != nil {
		t.Fatal(err)
	}
	manifest.Outputs[1].Path = other
	save()
	if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict {
		t.Fatalf("a line file named after another source: %d %v", status, body)
	}
	if err := os.Rename(other, lin); err != nil {
		t.Fatal(err)
	}
	manifest.Outputs[1].Path = lin
	save()

	// Replaced after the build collected it.
	if err := os.WriteFile(lin, []byte("0 0 0\n1 1 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict {
		t.Fatalf("a line file changed after collection: %d %v", status, body)
	}

	// The Quake III test, run on a map that says it is Quake 1.
	manifest = m.q3LeakBuild(id, "b_gap", job.Failed, true, false)
	manifest.Inputs[0].Conversion.Game = "quake1"
	save()
	if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict ||
		!strings.Contains(body["error"].(string), "quake1") {
		t.Fatalf("a build of the wrong game: %d %v", status, body)
	}
}

// Returning a Quake III result that failed to arrive sends the same build's
// envelope again, and starts nothing.
func TestAQuake3LeakReturnIsRetriedWithoutAnotherCompile(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	id := "20261003T000000Z-0000000b"
	m.q3LeakBuild(id, "b_gap", job.Failed, true, false)
	request := aub.LeakTestLink{AssetID: "saved-map", Revision: 7, ContentSHA256: strings.Repeat("a", 64),
		RequestID: strings.Repeat("d", 32), Profile: "quake3"}
	jobs := len(m.jobIDs())
	m.backend.mu.Lock()
	m.backend.refuseLeakResult = true
	m.backend.mu.Unlock()
	if record := m.server.deliverLeakResult(id, request); record.State != "failed" {
		t.Fatalf("a refused delivery: %+v", record)
	}
	m.backend.mu.Lock()
	m.backend.refuseLeakResult = false
	m.backend.mu.Unlock()
	status, body := m.call(http.MethodPost, "/api/v1/leak-test/runs/"+id+"/return", nil)
	if status != http.StatusOK || body["state"] != "returned" {
		t.Fatalf("the retry: %d %v", status, body)
	}
	m.backend.mu.Lock()
	returned := m.backend.leakResult
	m.backend.mu.Unlock()
	if returned["schema_version"] != "aucom.leak-result/1.1" || returned["build_id"] != id ||
		returned["game_profile"] != "quake3" || returned["diagnostic"].(map[string]any)["outcome"] != "leak" {
		t.Fatalf("AUB received %v", returned)
	}
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("returning a result started %d job(s)", got-jobs)
	}
	if entries, _ := os.ReadDir(m.builds); len(entries) != 1 {
		t.Fatalf("returning a result left %d build directories", len(entries))
	}
}

// A leak test cancelled before Q3Map2 flushed a byte has nothing to return.
// The editor is told the build was stopped — not that a return failed, which
// would offer a Retry that can never succeed — and a cancelled run that DID
// print is still returned, as no verdict.
func TestACancelledLeakTestWithNoOutputTellsTheEditorItWasStopped(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	request := aub.LeakTestLink{AssetID: "saved-map", Revision: 7, ContentSHA256: strings.Repeat("a", 64),
		RequestID: strings.Repeat("e", 32), Profile: "quake3"}

	silent := "20261003T000000Z-0000000c"
	manifest := m.q3LeakBuild(silent, "a_sealed", job.Cancelled, false, false)
	manifest.Outputs[0] = build.FileRecord{Name: "compile_log", Missing: true, Optional: true}
	if err := manifest.Save(filepath.Join(m.builds, silent)); err != nil {
		t.Fatal(err)
	}
	record := m.server.deliverLeakResult(silent, request)
	if record.State != "failed" || !strings.Contains(record.Error, "cancelled") {
		t.Fatalf("a silent cancelled build: %+v", record)
	}
	m.backend.mu.Lock()
	returned := m.backend.leakResult
	m.backend.mu.Unlock()
	if returned != nil {
		t.Fatalf("a result was sent for a build with nothing to say: %v", returned)
	}
	statuses := strings.Join(m.leakStatusesSoon(t, 1), "|")
	if !strings.Contains(statuses, request.RequestID+" blocked build_cancelled") || strings.Contains(statuses, "return_failed") {
		t.Fatalf("the editor was told %q", statuses)
	}

	spoke := "20261003T000000Z-0000000d"
	m.q3LeakBuild(spoke, "a_sealed", job.Cancelled, false, false)
	second := request
	second.RequestID = strings.Repeat("f", 32)
	if record := m.server.deliverLeakResult(spoke, second); record.State != "returned" {
		t.Fatalf("a cancelled build that printed: %+v", record)
	}
	m.backend.mu.Lock()
	returned = m.backend.leakResult
	m.backend.mu.Unlock()
	if returned["build_state"] != "cancelled" || returned["diagnostic"].(map[string]any)["outcome"] != "incomplete" {
		t.Fatalf("AUB received %v", returned)
	}
}
