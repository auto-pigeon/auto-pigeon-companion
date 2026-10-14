package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
)

// q2Run is one finished Quake II leak-test build as the runner leaves it,
// from qbsp 1.4518's own output on the authored controls (`Q2_010`).
type q2Run struct {
	control string
	state   job.State
	exit    int
	// pts names the recorded point file to collect, empty for none.
	pts string
	// bsp is the collected BSP's bytes, nil for none.
	bsp []byte
}

var ibsp38 = []byte{'I', 'B', 'S', 'P', 38, 0, 0, 0, 0, 0, 0, 0}

func (m *machine) q2LeakBuild(id string, run q2Run) *build.Manifest {
	m.t.Helper()
	vectors := filepath.Join("..", "leakadapter", "testdata", "q2")
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
	log, err := os.ReadFile(filepath.Join(vectors, "logs", run.control+".log"))
	if err != nil {
		m.t.Fatal(err)
	}
	outputs := []build.FileRecord{put("compile_log", "compile.stdout.log", log)}
	if run.pts != "" {
		pts, err := os.ReadFile(filepath.Join(vectors, "pts", run.pts+".pts"))
		if err != nil {
			m.t.Fatal(err)
		}
		outputs = append(outputs, put("pts", "level.pts", pts))
	} else {
		outputs = append(outputs, build.FileRecord{Name: "pts", Missing: true, Optional: true})
	}
	if run.bsp != nil {
		outputs = append(outputs, put("bsp", "level.bsp", run.bsp))
	} else {
		outputs = append(outputs, build.FileRecord{Name: "bsp", Missing: true, Optional: true})
	}
	exit := run.exit
	manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id, EngineFamily: "quake2",
		Pipeline: build.DocumentRef{ID: "auto-pigeon.q2.leak-test"}, State: run.state,
		Inputs: []build.FileRecord{{Name: "source_map", Path: filepath.Join(dir, "input", "source_map", "fixture.map"),
			SHA256: "sha256:" + strings.Repeat("b", 64),
			Source: &build.SourceRef{AssetType: aub.AssetTypeMap, AssetID: "saved-map", RevisionID: "immutable-revision",
				Revision: 7, ContentSHA256: strings.Repeat("a", 64), Refetchable: true},
			Conversion: &build.Conversion{From: "apmap", To: "map", Game: "quake2", SourceSHA256: "sha256:" + strings.Repeat("a", 64)}}},
		Tools:   []build.ToolRecord{{ToolVersion: leakadapter.EricwQ2MeasuredVersion}},
		Steps:   []build.Step{{ID: "compile", State: run.state, ExitCode: &exit}},
		Outputs: outputs}
	if err := manifest.Save(dir); err != nil {
		m.t.Fatal(err)
	}
	return manifest
}

// The Quake II envelope is 1.1 with Quake II's own row — never Quake 1's 1.0 —
// keeps exit, build state and leak outcome apart, and starts its route at the
// entity qbsp reached.
func TestAQuake2LeakResultIsAOnePointOneEnvelopeFromAFailedBuild(t *testing.T) {
	m := newMachine(t)
	id := "20261014T000000Z-00000001"
	m.q2LeakBuild(id, q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"})
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil)
	diagnostic, _ := body["diagnostic"].(map[string]any)
	if status != http.StatusOK || body["schema_version"] != "aucom.leak-result/1.1" ||
		body["game_profile"] != "quake2" || body["compiler"] != "ericw-qbsp-q2" ||
		body["pointfile_format"] != "ericw-pts" || body["pointfile_direction"] != "occupant_to_outside" ||
		body["build_state"] != "failed" || body["compile_exit_code"] != float64(1) ||
		body["compiler_version"] != leakadapter.EricwQ2MeasuredVersion ||
		body["content_sha256"] != strings.Repeat("a", 64) || body["compiler_source_sha256"] != strings.Repeat("b", 64) ||
		diagnostic["outcome"] != "leak" || diagnostic["route_points"] != float64(10) ||
		!strings.HasPrefix(body["pointfile"].(string), "0 0 192\n") ||
		!strings.Contains(body["log"].(string), `Reached occupant "light" at (0 0 192)`) {
		t.Fatalf("leaked build: %d %v", status, body)
	}
	for key := range body {
		if strings.Contains(key, "bsp") {
			t.Errorf("the envelope carries %q", key)
		}
	}
	status, view := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	leak, _ := view["leak_test"].(map[string]any)
	if status != http.StatusOK || leak["game_profile"] != "quake2" || leak["compiler"] != "ericw-qbsp-q2" ||
		leak["pointfile_output"] != "pts" || leak["pointfile_direction"] != "occupant_to_outside" ||
		leak["diagnostic"].(map[string]any)["outcome"] != "leak" {
		t.Fatalf("the Build page's view: %d %v", status, leak)
	}

	q1bsp := []byte{29, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for i, c := range []struct {
		name string
		run  q2Run
		want string
	}{
		{"A sealed, a Quake II BSP collected", q2Run{control: "a_sealed", state: job.Succeeded, bsp: ibsp38}, "no_leak"},
		{"A sealed, but no BSP behind the log", q2Run{control: "a_sealed", state: job.Succeeded}, "incomplete"},
		{"A sealed, but the BSP is a Quake 1 one", q2Run{control: "a_sealed", state: job.Succeeded, bsp: q1bsp}, "incomplete"},
		{"A sealed log, but the step was killed (a timeout)", q2Run{control: "a_sealed", state: job.Failed, exit: -1, bsp: ibsp38}, "incomplete"},
		{"C light outside", q2Run{control: "c_outside_light", state: job.Failed, exit: 1, pts: "c_outside_light"}, "leak"},
		{"D no occupant: a BSP and exit 0, not tested", q2Run{control: "d_no_occupant", state: job.Succeeded, bsp: ibsp38}, "no_interior"},
		{"E all in solid", q2Run{control: "e_in_solid", state: job.Succeeded, bsp: ibsp38}, "no_interior"},
		{"F detail cover", q2Run{control: "f_detail_cover", state: job.Failed, exit: 1, pts: "f_detail_cover"}, "leak"},
		{"F clip cover", q2Run{control: "f_clip_cover", state: job.Failed, exit: 1, pts: "f_clip_cover"}, "leak"},
		{"G truncated", q2Run{control: "g_truncated", state: job.Failed, exit: 1}, "incomplete"},
		{"H missing WAL: sealed, with a BSP, still no pass", q2Run{control: "h_missing_wal", state: job.Succeeded, bsp: ibsp38}, "incomplete"},
		{"I a point file beside a sealed run", q2Run{control: "i_stale_pts", state: job.Succeeded, pts: "i_stale_pts", bsp: ibsp38}, "incomplete"},
		{"cancelled before it said anything about a leak", q2Run{control: "a_sealed", state: job.Cancelled, exit: -1}, "incomplete"},
	} {
		id := fmt.Sprintf("20261014T000000Z-%08x", 0x10000000+i)
		m.q2LeakBuild(id, c.run)
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil)
		diagnostic, _ := body["diagnostic"].(map[string]any)
		if status != http.StatusOK || diagnostic["outcome"] != c.want {
			t.Errorf("%s: %d %v", c.name, status, diagnostic)
		}
	}
}

// What is not this build's own is refused, not returned: a point file changed
// after it was collected, an output recorded outside the build, a BSP that is
// not there any more, and a build that ran the Quake II test on another game.
func TestAQuake2LeakResultRefusesWhatIsNotThisBuildsOwn(t *testing.T) {
	m := newMachine(t)
	id := "20261014T000000Z-00000002"
	result := "/api/v1/leak-test/runs/" + id + "/result"
	fresh := func(run q2Run) *build.Manifest {
		if err := os.RemoveAll(filepath.Join(m.builds, id)); err != nil {
			t.Fatal(err)
		}
		return m.q2LeakBuild(id, run)
	}
	save := func(manifest *build.Manifest) {
		if err := manifest.Save(filepath.Join(m.builds, id)); err != nil {
			t.Fatal(err)
		}
	}

	manifest := fresh(q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"})
	if err := os.WriteFile(manifest.Outputs[1].Path, []byte("0 0 192\n1 1 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict {
		t.Fatalf("a point file changed after collection: %d %v", status, body)
	}

	manifest = fresh(q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"})
	outside := filepath.Join(t.TempDir(), "level.pts")
	raw, _ := os.ReadFile(manifest.Outputs[1].Path)
	if err := os.WriteFile(outside, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest.Outputs[1].Path = outside
	save(manifest)
	if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict {
		t.Fatalf("a point file recorded outside the build: %d %v", status, body)
	}

	manifest = fresh(q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"})
	manifest.Outputs[1].Path = filepath.Join(filepath.Dir(manifest.Outputs[1].Path), "..", "..", "..", "elsewhere", "level.pts")
	save(manifest)
	if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict {
		t.Fatalf("a point file recorded with ../: %d %v", status, body)
	}

	manifest = fresh(q2Run{control: "a_sealed", state: job.Succeeded, bsp: ibsp38})
	if err := os.Remove(manifest.Outputs[2].Path); err != nil {
		t.Fatal(err)
	}
	if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict {
		t.Fatalf("a recorded BSP that is gone: %d %v", status, body)
	}

	for _, game := range []string{"quake1", "quake3"} {
		manifest = fresh(q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"})
		manifest.Inputs[0].Conversion.Game = game
		save(manifest)
		if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict ||
			!strings.Contains(body["error"].(string), game) {
			t.Fatalf("the Quake II test on a %s map: %d %v", game, status, body)
		}
	}

	// A build with no pinned AUB revision behind it is no answer to a request.
	manifest = fresh(q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"})
	manifest.Inputs[0].Source.RevisionID = ""
	save(manifest)
	if status, body := m.call(http.MethodGet, result, nil); status != http.StatusConflict {
		t.Fatalf("a build with no pinned revision: %d %v", status, body)
	}
}

// Retrying a Quake II return sends the same build's envelope and starts no
// compiler, however many times it is pressed.
func TestAQuake2LeakReturnIsRetriedWithoutAnotherCompile(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	id := "20261014T000000Z-00000003"
	m.q2LeakBuild(id, q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"})
	request := aub.LeakTestLink{AssetID: "saved-map", Revision: 7, ContentSHA256: strings.Repeat("a", 64),
		RequestID: strings.Repeat("9", 32), Profile: "quake2"}
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
	for range 6 {
		status, body := m.call(http.MethodPost, "/api/v1/leak-test/runs/"+id+"/return", nil)
		if status != http.StatusOK || body["state"] != "returned" {
			t.Fatalf("the retry: %d %v", status, body)
		}
	}
	m.backend.mu.Lock()
	returned := m.backend.leakResult
	m.backend.mu.Unlock()
	if returned["schema_version"] != "aucom.leak-result/1.1" || returned["build_id"] != id ||
		returned["game_profile"] != "quake2" || returned["compiler"] != "ericw-qbsp-q2" ||
		returned["diagnostic"].(map[string]any)["outcome"] != "leak" {
		t.Fatalf("AUB received %v", returned)
	}
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("returning a result started %d job(s)", got-jobs)
	}
	if entries, _ := os.ReadDir(m.builds); len(entries) != 1 {
		t.Fatalf("returning a result left %d build directories", len(entries))
	}
}
