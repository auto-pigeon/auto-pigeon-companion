package web

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// The page's Package → Install → Run routes, against the real executor and the
// Q3_011 fixture (`internal/q3deps/testdata/q3011`): an original map compiled
// by the real Q3Map2, and the content it was compiled against.

const q3Fixture = "../q3deps/testdata/q3011"

// q3Build writes a finished Quake III build into the machine's build store, the
// way `build.Runner` leaves one: its content staged as a PK3, a manifest that
// records the stage, and the compiled map as its output.
func (m *machine) q3Build() (id, archiveDigest string) {
	m.t.Helper()
	id, err := build.NewID(time.Now())
	if err != nil {
		m.t.Fatal(err)
	}
	content := filepath.Join(m.t.TempDir(), "content")
	game := filepath.Join(m.t.TempDir(), "game")
	for _, dir := range []string{filepath.Join(content, "baseq3"), filepath.Join(game, "baseq3")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			m.t.Fatal(err)
		}
	}
	archive := filepath.Join(content, "baseq3", "zz_apq3011_assets.pk3")
	zipDir(m.t, filepath.Join(q3Fixture, "content"), archive)

	dir := filepath.Join(m.builds, id)
	stage, stageErr := q3vfs.Build(q3vfs.Request{
		Roots: map[string]string{profile.RootGame: game, profile.RootContent: content},
		Dir:   filepath.Join(dir, "vfs"),
	})
	if stageErr != nil {
		m.t.Fatalf("staging the fixture: %v", stageErr)
	}
	source := filepath.Join(dir, "input", "q3011_room.map")
	bsp := filepath.Join(dir, "output", "bsp", "q3011_room.bsp")
	copyInto(m.t, filepath.Join(q3Fixture, "maps", "q3011_room.map"), source)
	copyInto(m.t, filepath.Join(q3Fixture, "maps", "q3011_room.bsp"), bsp)
	manifest := &build.Manifest{
		SchemaVersion: build.SchemaVersion,
		BuildID:       id,
		Platform:      "test",
		EngineFamily:  "quake3",
		State:         job.Succeeded,
		Pipeline:      build.DocumentRef{ID: "auto-pigeon.q3.fast-preview", Version: "1.0.0", Digest: "sha256:test"},
		GameData:      stage,
		StartedAt:     time.Now().UTC(),
		Inputs:        []build.FileRecord{{Name: "source_map", Role: "q3.map.source", Path: source, SHA256: "sha256:" + fileDigest(m.t, source)}},
		Outputs:       []build.FileRecord{{Name: "bsp", Role: "q3.bsp.lit", Path: bsp, SHA256: "sha256:" + fileDigest(m.t, bsp)}},
		Steps:         []build.Step{},
		Directory:     dir,
	}
	if err := manifest.Save(dir); err != nil {
		m.t.Fatal(err)
	}
	return id, fileDigest(m.t, archive)
}

func zipDir(t *testing.T, from, archive string) {
	t.Helper()
	var names []string
	err := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative, err := filepath.Rel(from, path)
		names = append(names, filepath.ToSlash(relative))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(from, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		part, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func copyInto(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// bindQ3Engine sets the fixture Quake III engine up the way the Profiles page
// does: the program, the game folder, approved.
func (m *machine) bindQ3Engine(gameRoot string) {
	m.t.Helper()
	self, err := os.Executable()
	if err != nil {
		m.t.Fatal(err)
	}
	m.writeProfile("aucom.fixture.q3-engine.json", enginefixture.ProfileQ3JSON)
	entry, err := job.NewCatalog(m.profiles).Lookup(enginefixture.ProfileQ3ID)
	if err != nil {
		m.t.Fatal(err)
	}
	status, body := m.call(http.MethodPost, "/api/v1/profiles/"+enginefixture.ProfileQ3ID+"/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": gameRoot, "content_root": m.content},
		"approve":     true,
		"digest":      entry.Digest,
	})
	if status != http.StatusOK {
		m.t.Fatalf("binding the Quake III fixture engine = %d: %v", status, body["error"])
	}
}

func members(t *testing.T, plan map[string]any) []string {
	t.Helper()
	var out []string
	list, _ := plan["members"].([]any)
	for _, raw := range list {
		out = append(out, raw.(map[string]any)["path"].(string))
	}
	return out
}

// Build → Package → Install → Run through the routes the page calls, with every
// refusal on the way a status and a class rather than a silent nothing.
func TestTheQuake3PackageJourneyThroughThePagesRoutes(t *testing.T) {
	m := newMachine(t)
	buildID, archive := m.q3Build()

	// 1. With no answer about rights, the plan holds the map alone and says
	//    why it would not be written.
	status, body := m.call(http.MethodPost, "/api/v1/q3/packages/preview", map[string]any{"build": buildID})
	if status != http.StatusOK {
		t.Fatalf("preview = %d: %v", status, body["error"])
	}
	plan := body["plan"].(map[string]any)
	if got := members(t, plan); len(got) != 1 || got[0] != "maps/q3011_room.bsp" {
		t.Errorf("ungranted members: %v", got)
	}
	if body["held"] == nil || body["acceptable"] != true || !strings.Contains(body["maturity_message"].(string), "Work in progress") {
		t.Errorf("an ungranted plan: held=%v acceptable=%v", body["held"], body["acceptable"])
	}

	// 2. Creating it is refused, with the class and the plan.
	status, body = m.call(http.MethodPost, "/api/v1/q3/packages", map[string]any{"build": buildID})
	if status != http.StatusConflict || body["class"] != "package_held" || body["plan"] == nil {
		t.Fatalf("an ungranted create = %d class %v: %v", status, body["class"], body["error"])
	}
	// A licence that is not named is a bad request, not a held package.
	status, body = m.call(http.MethodPost, "/api/v1/q3/packages/preview", map[string]any{
		"build": buildID, "grants": []map[string]any{{"archive": archive, "basis": "licensed"}},
	})
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "names no licence") {
		t.Errorf("a licence with no name = %d: %v", status, body["error"])
	}
	// A path is not something the page may send.
	status, _ = m.call(http.MethodPost, "/api/v1/q3/packages/preview", map[string]any{"build": buildID, "game_root": "/"})
	if status != http.StatusBadRequest {
		t.Errorf("a request naming a folder = %d", status)
	}

	// 3. Granted, it is written — and written again is the same package.
	request := map[string]any{
		"build":   buildID,
		"grants":  []map[string]any{{"archive": archive, "basis": "licensed", "licence": "CC0-1.0"}},
		"include": []string{"LICENSE-apq3011.txt"},
	}
	status, body = m.call(http.MethodPost, "/api/v1/q3/packages", request)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %v", status, body["error"])
	}
	pkg := body["package"].(map[string]any)
	id := pkg["id"].(string)
	if pkg["complete"] != true || len(members(t, pkg["plan"].(map[string]any))) != 11 {
		t.Errorf("the package: complete=%v members=%v", pkg["complete"], members(t, pkg["plan"].(map[string]any)))
	}
	status, body = m.call(http.MethodPost, "/api/v1/q3/packages", request)
	if status != http.StatusOK || body["already_existed"] != true || body["package"].(map[string]any)["id"] != id {
		t.Errorf("creating it twice = %d existed=%v", status, body["already_existed"])
	}
	status, body = m.call(http.MethodGet, "/api/v1/q3/packages?build="+buildID, nil)
	if list, _ := body["packages"].([]any); status != http.StatusOK || len(list) != 1 {
		t.Errorf("the build's packages = %d: %v", status, body)
	}
	if status, _ = m.call(http.MethodGet, "/api/v1/q3/packages/..%2F"+id, nil); status == http.StatusOK {
		t.Error("a package id that is a path was read")
	}

	// 4. The engines: the fixture is listed, and not set up.
	m.writeProfile("aucom.fixture.q3-engine.json", enginefixture.ProfileQ3JSON)
	status, body = m.call(http.MethodGet, "/api/v1/q3/engines", nil)
	if status != http.StatusOK {
		t.Fatalf("engines = %d: %v", status, body["error"])
	}
	var fixture map[string]any
	for _, raw := range body["engines"].([]any) {
		if engine := raw.(map[string]any); engine["id"] == enginefixture.ProfileQ3ID {
			fixture = engine
		}
	}
	if fixture == nil || fixture["set_up"] != false || fixture["problem"] == nil {
		t.Fatalf("the unbound fixture engine: %v", fixture)
	}
	install := map[string]any{"package": id, "engine": enginefixture.ProfileQ3ID}
	status, body = m.call(http.MethodPost, "/api/v1/q3/installs", install)
	if status != http.StatusConflict || body["class"] != "game_data_missing" {
		t.Errorf("installing beside an engine with no game folder = %d class %v: %v", status, body["class"], body["error"])
	}

	// 5. Set up, with a game folder that has a base game in it.
	gameRoot := filepath.Join(t.TempDir(), "quake3")
	writeFixtureFile(t, filepath.Join(gameRoot, "baseq3", "pak0.pk3"), "PK")
	m.bindQ3Engine(gameRoot)

	// A preview writes nothing.
	status, body = m.call(http.MethodPost, "/api/v1/q3/installs/preview", install)
	if status != http.StatusOK {
		t.Fatalf("install preview = %d: %v", status, body["error"])
	}
	if list, _ := m.callList("/api/v1/q3/installs", "installations"); len(list) != 0 {
		t.Error("a preview recorded an installation")
	}
	status, body = m.call(http.MethodPost, "/api/v1/q3/installs", install)
	if status != http.StatusCreated {
		t.Fatalf("install = %d: %v", status, body["error"])
	}
	installation := body["installation"].(map[string]any)
	installID := installation["id"].(string)
	if installation["kind"] != "managed" || installation["fs_game"] != "baseq3" ||
		strings.HasPrefix(installation["base_path"].(string), gameRoot) {
		t.Errorf("the installation: %v", installation)
	}
	entries, _ := os.ReadDir(filepath.Join(gameRoot, "baseq3"))
	if len(entries) != 1 {
		t.Errorf("a managed install wrote into the game folder: %d entries", len(entries))
	}

	// 6. Run. The fixture engine names no line that reports a map load, so the
	//    honest answer is `not_observed` — with a pid and a job, not "started".
	run := map[string]any{
		"installation": installID, "engine": enginefixture.ProfileQ3ID, "action": "play_map",
		"options": map[string]string{"behaviour": "stay"},
	}
	status, body = m.call(http.MethodPost, "/api/v1/q3/runs", run)
	if status != http.StatusAccepted {
		t.Fatalf("run = %d: %v", status, body["error"])
	}
	runID := body["id"].(string)
	var done map[string]any
	for deadline := time.Now().Add(30 * time.Second); ; {
		status, done = m.call(http.MethodGet, "/api/v1/q3/runs/"+runID, nil)
		if status != http.StatusOK {
			t.Fatalf("reading the run = %d: %v", status, done["error"])
		}
		if done["state"] != "waiting" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run is still waiting: %v", done)
		}
		time.Sleep(50 * time.Millisecond)
	}
	result, _ := done["result"].(map[string]any)
	if result == nil || result["map_load"] != "not_observed" || result["signal_declared"] != false {
		t.Fatalf("the run's result: %v", done)
	}
	if pid, _ := result["pid"].(float64); pid == 0 || done["engine_running"] != true || done["job_id"] == "" {
		t.Errorf("a running engine with no pid or job: %v", done)
	}
	// The engine was given the INSTALLATION's base path, not the game folder.
	args, _ := result["args"].([]any)
	joined := ""
	for _, arg := range args {
		joined += arg.(string) + " "
	}
	if !strings.Contains(joined, installation["base_path"].(string)) || strings.Contains(joined, gameRoot+" ") {
		t.Errorf("the engine's arguments: %s", joined)
	}

	// A second Run while the engine is up is refused, and so is removing the
	// installation from under it.
	if status, body = m.call(http.MethodPost, "/api/v1/q3/runs", run); status != http.StatusConflict {
		t.Errorf("a second engine on one installation = %d: %v", status, body["error"])
	}
	if status, body = m.call(http.MethodDelete, "/api/v1/q3/installs/"+installID, nil); status != http.StatusConflict {
		t.Errorf("removing an installation an engine is running = %d: %v", status, body["error"])
	}
	// A reloaded page finds the run through the installation.
	status, body = m.call(http.MethodGet, "/api/v1/q3/installs/"+installID+"/runs", nil)
	if latest, _ := body["run"].(map[string]any); status != http.StatusOK || latest == nil || latest["id"] != runID {
		t.Errorf("the installation's newest run = %d: %v", status, body)
	}

	// 7. Stop it: the engine's job ends, and nothing is left running.
	status, body = m.call(http.MethodPost, "/api/v1/q3/runs/"+runID+"/cancel", nil)
	if status != http.StatusOK {
		t.Fatalf("stopping the engine = %d: %v", status, body["error"])
	}
	finished := m.waitForJob(done["job_id"].(string))
	if finished["state"] != "cancelled" {
		t.Errorf("the stopped engine's job is %v", finished["state"])
	}

	// 8. Remove the installation: the game folder is as it was.
	if status, body = m.call(http.MethodDelete, "/api/v1/q3/installs/"+installID, nil); status != http.StatusOK {
		t.Fatalf("removing the installation = %d: %v", status, body["error"])
	}
	entries, _ = os.ReadDir(filepath.Join(gameRoot, "baseq3"))
	if len(entries) != 1 {
		t.Errorf("after removal the game folder holds %d entries", len(entries))
	}
	if status, _ = m.call(http.MethodPost, "/api/v1/q3/runs", run); status != http.StatusNotFound {
		t.Errorf("running a removed installation = %d", status)
	}
}

func (m *machine) callList(path, key string) ([]any, int) {
	m.t.Helper()
	status, body := m.call(http.MethodGet, path, nil)
	list, _ := body[key].([]any)
	return list, status
}

// A build of another game is refused by name, and points at what does package
// it; a build that is not there is not found.
func TestTheQuake3PackageRoutesRefuseOtherBuilds(t *testing.T) {
	m := newMachine(t)
	other, _ := m.q3Build()
	manifest, err := build.Find(m.builds, other)
	if err != nil {
		t.Fatal(err)
	}
	manifest.EngineFamily = "quake1"
	if err := manifest.Save(manifest.Directory); err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodPost, "/api/v1/q3/packages/preview", map[string]any{"build": other})
	if status != http.StatusConflict || !strings.Contains(body["error"].(string), "not a Quake III build") {
		t.Errorf("a Quake 1 build = %d: %v", status, body["error"])
	}
	status, body = m.call(http.MethodPost, "/api/v1/q3/packages/preview", map[string]any{"build": "20200101T000000Z-00000000"})
	if status == http.StatusOK {
		t.Errorf("a build that is not there = %d: %v", status, body)
	}
	status, _ = m.call(http.MethodPost, "/api/v1/q3/packages/preview", map[string]any{})
	if status != http.StatusBadRequest {
		t.Errorf("no build named = %d", status)
	}
}
