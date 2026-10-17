package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
)

// q2Build writes a finished Quake II build into the machine's build store: the
// compiled map as its output, and the base game and content folders it read as
// its roots.
func (m *machine) q2Build(gameRoot string) string {
	m.t.Helper()
	id, err := build.NewID(time.Now())
	if err != nil {
		m.t.Fatal(err)
	}
	content := filepath.Join(m.t.TempDir(), "content")
	if _, err := q2fixture.Write(content, map[string][]byte{"textures/q2009/marker.wal": q2fixture.WAL("q2009/marker")}); err != nil {
		m.t.Fatal(err)
	}
	dir := filepath.Join(m.builds, id)
	bsp := filepath.Join(dir, "output", "bsp", "q2009room.bsp")
	if _, err := q2fixture.Write(filepath.Dir(bsp), map[string][]byte{
		"q2009room.bsp": q2fixture.BSP(`{ "classname" "worldspawn" }`, "q2009/marker")}); err != nil {
		m.t.Fatal(err)
	}
	manifest := &build.Manifest{
		SchemaVersion: build.SchemaVersion, BuildID: id, Platform: "test", EngineFamily: "quake2", State: job.Succeeded,
		Pipeline:  build.DocumentRef{ID: "auto-pigeon.q2.normal", Version: "1.0.0", Digest: "sha256:test"},
		StartedAt: time.Now().UTC(),
		Inputs:    []build.FileRecord{{Name: "source_map", Path: filepath.Join(dir, "input", "q2009room.map"), SHA256: "sha256:source"}},
		Roots: []build.RootRecord{{Role: profile.RootGame, Path: filepath.Join(gameRoot, "baseq2")},
			{Role: profile.RootContent, Path: content}},
		Outputs:   []build.FileRecord{{Name: "bsp", Path: bsp, SHA256: "sha256:" + fileDigest(m.t, bsp)}},
		Steps:     []build.Step{},
		Directory: dir,
	}
	if err := manifest.Save(dir); err != nil {
		m.t.Fatal(err)
	}
	return id
}

func (m *machine) bindQ2Engine(gameRoot string) {
	m.t.Helper()
	self, err := os.Executable()
	if err != nil {
		m.t.Fatal(err)
	}
	m.writeProfile("aucom.fixture.q2-engine.json", enginefixture.ProfileQ2JSON)
	entry, err := job.NewCatalog(m.profiles).Lookup(enginefixture.ProfileQ2ID)
	if err != nil {
		m.t.Fatal(err)
	}
	status, body := m.call(http.MethodPost, "/api/v1/profiles/"+enginefixture.ProfileQ2ID+"/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": gameRoot, "content_root": m.content},
		"approve":     true, "digest": entry.Digest,
	})
	if status != http.StatusOK {
		m.t.Fatalf("binding the Quake II fixture engine = %d: %v", status, body["error"])
	}
}

func (m *machine) waitForRun(api, id string) map[string]any {
	m.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; {
		status, done := m.call(http.MethodGet, api+"/runs/"+id, nil)
		if status != http.StatusOK {
			m.t.Fatalf("reading the run = %d: %v", status, done["error"])
		}
		if done["state"] != "waiting" {
			return done
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("the run is still waiting: %v", done)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Build → Package → Install → Run for a Quake II map through the page's routes:
// the PAK carries what was granted, goes into a mod folder of its own beside an
// untouched baseq2, and a run is accepted only on the engine's word that it
// opened the map from that package.
func TestTheQuake2PackageJourneyThroughThePagesRoutes(t *testing.T) {
	m := newMachine(t)
	gameRoot := filepath.Join(t.TempDir(), "Quake II")
	pak0 := q2fixture.PAK([]string{"pics/colormap.pcx"}, map[string][]byte{"pics/colormap.pcx": q2fixture.PCX()})
	if _, err := q2fixture.Write(filepath.Join(gameRoot, "baseq2"), map[string][]byte{"pak0.pak": pak0}); err != nil {
		t.Fatal(err)
	}
	baseDigest := fileDigest(t, filepath.Join(gameRoot, "baseq2", "pak0.pak"))
	buildID := m.q2Build(gameRoot)

	status, body := m.call(http.MethodPost, "/api/v1/q2/packages/preview", map[string]any{"build": buildID})
	if status != http.StatusOK || body["held"] == nil || body["acceptable"] != true ||
		!strings.Contains(body["maturity_message"].(string), "Quake II") {
		t.Fatalf("an ungranted plan = %d: %v", status, body)
	}
	if status, body = m.call(http.MethodPost, "/api/v1/q2/packages", map[string]any{"build": buildID}); status != http.StatusConflict || body["class"] != "package_held" {
		t.Fatalf("an ungranted create = %d: %v", status, body)
	}
	if status, _ = m.call(http.MethodPost, "/api/v1/q2/packages/preview", map[string]any{"build": buildID, "game_root": "/"}); status != http.StatusBadRequest {
		t.Errorf("a request naming a folder = %d", status)
	}
	request := map[string]any{"build": buildID, "name": "q2009room_test",
		"grants": []map[string]any{{"loose": true, "basis": "own_work"}}}
	status, body = m.call(http.MethodPost, "/api/v1/q2/packages", request)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %v", status, body["error"])
	}
	pkg := body["package"].(map[string]any)
	if got := strings.Join(members(t, pkg["plan"].(map[string]any)), ","); got != "maps/q2009room_test.bsp,textures/q2009/marker.wal" ||
		pkg["complete"] != true {
		t.Fatalf("the package: %s complete=%v", got, pkg["complete"])
	}
	packageID := pkg["id"].(string)

	// The fixture engine is listed with what stops it, then set up.
	m.writeProfile("aucom.fixture.q2-engine.json", enginefixture.ProfileQ2JSON)
	install := map[string]any{"package": packageID, "engine": enginefixture.ProfileQ2ID}
	if status, body = m.call(http.MethodPost, "/api/v1/q2/installs", install); status != http.StatusConflict || body["class"] != "game_data_missing" {
		t.Errorf("installing for an engine with no game folder = %d: %v", status, body)
	}
	m.bindQ2Engine(gameRoot)
	status, body = m.call(http.MethodGet, "/api/v1/q2/engines", nil)
	var fixture map[string]any
	for _, raw := range body["engines"].([]any) {
		if engine := raw.(map[string]any); engine["id"] == enginefixture.ProfileQ2ID {
			fixture = engine
		}
	}
	if status != http.StatusOK || fixture == nil || fixture["set_up"] != true {
		t.Fatalf("the set-up fixture engine = %d: %v", status, fixture)
	}
	for _, raw := range fixture["actions"].([]any) {
		if role := raw.(map[string]any)["session_role"]; role != "client" {
			t.Errorf("a %v action is offered to run a map on this computer", role)
		}
	}
	if status, body = m.call(http.MethodPost, "/api/v1/q2/installs", map[string]any{"package": packageID,
		"engine": enginefixture.ProfileQ2ID, "mod": "baseq2"}); status != http.StatusBadRequest || body["class"] != "fs_game_invalid" {
		t.Errorf("installing into baseq2 = %d: %v", status, body)
	}
	status, body = m.call(http.MethodPost, "/api/v1/q2/installs", install)
	if status != http.StatusCreated {
		t.Fatalf("install = %d: %v", status, body["error"])
	}
	installation := body["installation"].(map[string]any)
	installID := installation["id"].(string)
	archivePath := installation["archive"].(map[string]any)["path"].(string)
	if installation["fs_game"] != "aup-q2009room_test" || filepath.Dir(archivePath) != filepath.Join(gameRoot, "aup-q2009room_test") {
		t.Errorf("the installation: %v", installation)
	}

	// Run: the fixture reads the installed PAK's directory and says where it
	// found the map, the way Yamagi does with fs_debug.
	run := map[string]any{"installation": installID, "engine": enginefixture.ProfileQ2ID, "action": "play_map",
		"options": map[string]string{"behaviour": "quake2_load"}}
	status, body = m.call(http.MethodPost, "/api/v1/q2/runs", run)
	if status != http.StatusAccepted {
		t.Fatalf("run = %d: %v", status, body["error"])
	}
	runID := body["id"].(string)
	done := m.waitForRun("/api/v1/q2", runID)
	result, _ := done["result"].(map[string]any)
	if result == nil || result["map_load"] != "accepted" || result["source_verified"] != true ||
		result["loaded_from"] != filepath.ToSlash(archivePath) {
		t.Fatalf("the run: %v", done)
	}
	joined := strings.Join(func() []string {
		var out []string
		for _, arg := range result["args"].([]any) {
			out = append(out, arg.(string))
		}
		return out
	}(), " ")
	for _, want := range []string{"-datadir " + gameRoot, "+set game aup-q2009room_test", "+set fs_debug 1", "+map q2009room_test"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the engine was not given %q: %s", want, joined)
		}
	}
	// A second install into the folder the engine is running is refused, and
	// so is removing it.
	if status, body = m.call(http.MethodPost, "/api/v1/q2/installs", install); status != http.StatusConflict {
		t.Errorf("replacing a folder an engine is running = %d: %v", status, body)
	}
	if status, _ = m.call(http.MethodDelete, "/api/v1/q2/installs/"+installID, nil); status != http.StatusConflict {
		t.Errorf("removing it = %d", status)
	}
	status, body = m.call(http.MethodGet, "/api/v1/q2/installs/"+installID+"/runs", nil)
	if latest, _ := body["run"].(map[string]any); status != http.StatusOK || latest == nil || latest["id"] != runID {
		t.Errorf("the installation's newest run = %d: %v", status, body)
	}
	if status, body = m.call(http.MethodPost, "/api/v1/q2/runs/"+runID+"/cancel", nil); status != http.StatusOK {
		t.Fatalf("stopping the engine = %d: %v", status, body["error"])
	}
	if finished := m.waitForJob(done["job_id"].(string)); finished["state"] != "cancelled" {
		t.Errorf("the stopped engine's job is %v", finished["state"])
	}
	// The fixture writes its record into the content root it was given — the
	// mod folder — which is a file the Companion did not put there.
	os.Remove(filepath.Join(gameRoot, "aup-q2009room_test", "engine-record.json"))
	if status, body = m.call(http.MethodDelete, "/api/v1/q2/installs/"+installID, nil); status != http.StatusOK {
		t.Fatalf("removing the installation = %d: %v", status, body["error"])
	}
	entries, _ := os.ReadDir(gameRoot)
	if len(entries) != 1 || fileDigest(t, filepath.Join(gameRoot, "baseq2", "pak0.pak")) != baseDigest {
		t.Errorf("the game folder is not as it was: %d entries", len(entries))
	}
}

// Build & Run no longer refuses a Quake II map (Q2_009 did; Q2_020 routes it
// through the package path — TestBuildAndRunPlaysAQuake2MapFromItsPackage).
// With the built-in pipeline and Yamagi, the review now asks for what is
// actually missing on this machine: the engine's game folder.
func TestBuildAndRunTakesAQuake2MapToThePackagePath(t *testing.T) {
	m := newMachine(t)
	status, body := m.call(http.MethodPost, "/api/v1/play/plan", map[string]any{
		"asset_id": "abc", "revision_id": "def", "revision_number": 1,
		"pipeline": "auto-pigeon.q2.normal", "engine": "auto-pigeon.engine.yamagi-quake2",
		"action": "play_map", "map": "room",
	})
	message, _ := body["error"].(string)
	if status != http.StatusBadRequest || strings.Contains(message, "Package and run this map") ||
		!strings.Contains(message, "game folder") {
		t.Errorf("Build & Run for a Quake II pipeline = %d: %v", status, message)
	}
}
