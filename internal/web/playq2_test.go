package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
)

// q2FixtureTexture is the one texture the fixture compiler's Quake II BSP
// names; the content folder of the journey below holds it.
const q2FixtureTexture = "q2020/crate"

// fixtureQ2ToolJSON is the fixture compiler as a Quake II tool: it reads the
// base game and content folders bound to it, as EricW in Quake II mode does.
func fixtureQ2ToolJSON(self string) []byte {
	return encodeFixture(map[string]any{
		"schema_version": "aucom.profile/1.1",
		"kind":           "tool",
		"id":             "aucom.fixture.q2-toolchain",
		"version":        "1.0.0",
		"name":           "Fixture Quake II toolchain",
		"summary":        "A stand-in Quake II compiler that writes an IBSP 38 map.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"tool_version":   "0.0.0-fixture",
		"platforms": []map[string]any{
			{"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "status": "supported"},
		},
		"acquisition": []map[string]any{
			{"mode": "user_path", "title": "Point at a copy you already have", "hint": "choose the test binary"},
		},
		"capabilities": []map[string]any{
			{"id": "fixture.q2compile", "title": "Compile", "consumes": []string{"q2.map.source"},
				"produces": []string{"q2.bsp"}},
		},
		"executables": []map[string]any{
			{"name": "tool", "title": "The fixture program", "file": "tool{platform.exe_suffix}"},
		},
		"actions": []map[string]any{
			{
				"id": "compile", "title": "Compile", "capability": "fixture.q2compile",
				"executable":  "tool",
				"args":        []any{buildHelperFlag, "--q2bsp", "{input.source_map}", "{output.bsp}"},
				"working_dir": map[string]any{"root": "workspace"},
				"inputs": []map[string]any{
					{"name": "source_map", "title": "Source", "role": "q2.map.source",
						"required": true, "extensions": []string{".map"}},
				},
				"outputs": []map[string]any{
					{"name": "bsp", "title": "BSP", "role": "q2.bsp", "path": "{option.basename}.bsp"},
				},
				"options": []map[string]any{
					{"name": "basename", "title": "Name", "type": "text", "default": "level", "max_length": 64},
				},
				"roots": []map[string]any{
					{"role": "workspace", "access": "read_write", "purpose": "compile"},
					{"role": "game_root", "access": "read", "purpose": "read the base game's .wal files"},
					{"role": "content_root", "access": "read", "optional": true, "purpose": "read your own .wal files"},
				},
				"timeout_seconds": 120,
			},
		},
	})
}

func fixtureQ2PipelineJSON() []byte {
	return encodeFixture(map[string]any{
		"schema_version": "aucom.profile/1.1",
		"kind":           "pipeline",
		"id":             "aucom.fixture.q2-pipeline",
		"version":        "1.0.0",
		"name":           "Fixture Quake II build",
		"summary":        "One stage: compile a Quake II map source into a BSP.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"game_profile":   map[string]any{"slug": "quake2", "engine_family": "quake2"},
		"inputs": []map[string]any{
			{"name": "source_map", "title": "Map source", "role": "q2.map.source",
				"required": true, "extensions": []string{".map"}},
		},
		"steps": []map[string]any{
			{"id": "compile", "title": "Compile", "capability": "fixture.q2compile",
				"inputs": []map[string]any{{"name": "source_map", "from": "pipeline.source_map"}}},
		},
		"outputs": []map[string]any{
			{"name": "bsp", "title": "The compiled map", "role": "q2.bsp", "from": "compile.bsp"},
		},
	})
}

// prepareQ2Play is what a person sets up once for a Quake II Build & Run: the
// account, the compiler with its base game and content folders, and a Quake II
// engine with its game folder. The engine fixture loads the map from the
// installed PAK and says so, the way Yamagi does with fs_debug.
func (m *machine) prepareQ2Play(t *testing.T) (gameRoot, content string) {
	t.Helper()
	m.signIn()
	self := mustExecutable(t)
	// The space is deliberate: a Quake II folder is often "Quake II".
	gameRoot = filepath.Join(t.TempDir(), "Quake II")
	pak0 := q2fixture.PAK([]string{"pics/colormap.pcx"}, map[string][]byte{"pics/colormap.pcx": q2fixture.PCX()})
	if _, err := q2fixture.Write(filepath.Join(gameRoot, "baseq2"), map[string][]byte{"pak0.pak": pak0}); err != nil {
		t.Fatal(err)
	}
	content = filepath.Join(t.TempDir(), "my content")
	if _, err := q2fixture.Write(content, map[string][]byte{
		"textures/" + q2FixtureTexture + ".wal": q2fixture.WAL(q2FixtureTexture)}); err != nil {
		t.Fatal(err)
	}

	m.writeProfile("aucom.fixture.q2-toolchain.json", fixtureQ2ToolJSON(self))
	m.writeProfile("aucom.fixture.q2-pipeline.json", fixtureQ2PipelineJSON())
	entry, err := job.NewCatalog(m.profiles).Lookup("aucom.fixture.q2-toolchain")
	if err != nil {
		t.Fatal(err)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.q2-toolchain/bind", map[string]any{
		"executables": map[string]string{"tool": self},
		"roots":       map[string]string{"game_root": filepath.Join(gameRoot, "baseq2"), "content_root": content},
		"approve":     true, "digest": entry.Digest,
	}); status != http.StatusOK {
		t.Fatalf("binding the Quake II toolchain = %d: %v", status, body["error"])
	}

	// The fixture engine, told to load the map from the installed package
	// unless asked otherwise: Build & Run gives an engine no options.
	engineJSON := bytes.Replace(enginefixture.ProfileQ2JSON, []byte(`"default": "ready"`), []byte(`"default": "quake2_load"`), 1)
	if bytes.Equal(engineJSON, enginefixture.ProfileQ2JSON) {
		t.Fatal("the Quake II engine fixture no longer defaults to `ready`; update this test")
	}
	m.writeProfile("aucom.fixture.q2-engine.json", engineJSON)
	engineEntry, err := job.NewCatalog(m.profiles).Lookup(enginefixture.ProfileQ2ID)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/"+enginefixture.ProfileQ2ID+"/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": gameRoot, "content_root": m.content},
		"approve":     true, "digest": engineEntry.Digest,
	}); status != http.StatusOK {
		t.Fatalf("binding the Quake II engine = %d: %v", status, body["error"])
	}

	m.backend.asset.fileName = "q2room.map"
	// The map uses a texture of an account texture source it binds, which
	// its texture export carries (Q2_015A).
	m.backend.asset.body = []byte("// q2texture " + q2AccountTexture + "\n{ \"classname\" \"worldspawn\" }\n")
	m.backend.textures = fixtureQ2TextureBundle(m.backend.asset.assetID, m.backend.asset.revision, "bound")
	return gameRoot, content
}

func q2PlayBody(m *machine) map[string]any {
	return map[string]any{
		"asset_type": "map", "asset_id": m.backend.asset.assetID,
		"revision_id": m.backend.asset.revisionID, "revision_number": m.backend.asset.revision,
		"source_file": m.backend.asset.fileName,
		"pipeline":    "aucom.fixture.q2-pipeline",
		"engine":      enginefixture.ProfileQ2ID, "action": "play_map",
		"map": "q2room",
	}
}

// Q2_020: Build & Run for a Quake II build profile packages the map, installs
// it in a mod folder of its own and accepts the run on the engine's word —
// through the package panel's own owners, in one record. Q2_009 refused it.
func TestBuildAndRunPlaysAQuake2MapFromItsPackage(t *testing.T) {
	m := newMachine(t)
	gameRoot, _ := m.prepareQ2Play(t)
	// An installed archive with a file of the account texture's name and other
	// bytes: not the revision the map was saved against, so not what is used.
	installed := q2fixture.PAK([]string{"textures/" + q2AccountTexture + ".wal"},
		map[string][]byte{"textures/" + q2AccountTexture + ".wal": []byte("an installed copy")})
	if _, err := q2fixture.Write(filepath.Join(gameRoot, "baseq2"), map[string][]byte{"pak9.pak": installed}); err != nil {
		t.Fatal(err)
	}
	baseDigest := fileDigest(t, filepath.Join(gameRoot, "baseq2", "pak0.pak"))
	modDir := filepath.Join(gameRoot, "aup-q2room")

	// The review: a package into aup-q2room, no texture bundle, nothing run.
	status, plan := m.call(http.MethodPost, "/api/v1/play/plan", q2PlayBody(m))
	if status != http.StatusOK {
		t.Fatalf("plan = %d: %v", status, plan["error"])
	}
	writes, _ := plan["writes"].(map[string]any)
	textures, _ := plan["textures"].(map[string]any)
	if plan["packaged"] != true || writes["directory"] != modDir || textures["packaged"] != true ||
		!strings.Contains(writes["never_writes"].(string), "baseq2") || plan["package"] == nil {
		t.Fatalf("the review of a Quake II run: %v", plan)
	}

	// Another family's engine cannot play it.
	wrong := q2PlayBody(m)
	wrong["engine"] = "aucom.fixture.q1-engine"
	if status, body := m.call(http.MethodPost, "/api/v1/play/plan", wrong); status != http.StatusBadRequest ||
		!strings.Contains(body["error"].(string), "makes Quake II maps") {
		t.Errorf("a Quake 1 engine for a Quake II build = %d: %v", status, body["error"])
	}

	// No answer about the content folder's files: the package is held after
	// the compile, nothing is installed, and the remedy says what to answer.
	status, started := m.call(http.MethodPost, "/api/v1/play/runs", q2PlayBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	held := m.awaitPlay(t, started["id"].(string))
	if held["state"] != "failed" || held["failed_at"] != "installing" ||
		!strings.Contains(held["error"].(string), q2FixtureTexture+".wal") ||
		!strings.Contains(held["remedy"].(string), "may travel with the map") {
		t.Fatalf("an ungranted Quake II run: %v at %v: %v / %v", held["state"], held["failed_at"], held["error"], held["remedy"])
	}
	if _, err := os.Stat(modDir); !os.IsNotExist(err) {
		t.Errorf("a held run left %s: %v", modDir, err)
	}

	// The answer given: own work.
	granted := q2PlayBody(m)
	granted["grants"] = []map[string]any{{"loose": true, "basis": "own_work"}}
	status, started = m.call(http.MethodPost, "/api/v1/play/runs", granted)
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))
	if run["state"] != "succeeded" {
		t.Fatalf("the Quake II run %v at %v: %v (%v)", run["state"], run["failed_at"], run["error"], run["remedy"])
	}
	// The map's texture export was downloaded and verified (Q2_015A), and
	// AUB's compiler_ready — false, for the e1u1 it never sends — did not
	// stop the run.
	downloaded := false
	for _, raw := range run["stages"].([]any) {
		if stage := raw.(map[string]any); stage["state"] == "downloading_textures" {
			downloaded = strings.Contains(fmt.Sprint(stage["detail"]), q2AccountNamespace+" revision 2")
		}
	}
	if !downloaded {
		t.Errorf("the run did not download the map's account textures: %v", run["stages"])
	}
	pkg, _ := run["package"].(map[string]any)
	installation, _ := run["installation"].(map[string]any)
	launch, _ := run["launch"].(map[string]any)
	archive := filepath.Join(modDir, "q2room.pak")
	switch {
	case pkg == nil || pkg["archive"] != "q2room.pak" || pkg["complete"] != true:
		t.Errorf("package = %v", pkg)
	case installation == nil || installation["fs_game"] != "aup-q2room" || installation["mod_dir"] != modDir:
		t.Errorf("installation = %v", installation)
	case launch == nil || launch["map_load"] != "accepted" || launch["source_verified"] != true ||
		launch["loaded_from"] != filepath.ToSlash(archive):
		t.Errorf("launch = %v", launch)
	}
	if fileDigest(t, filepath.Join(gameRoot, "baseq2", "pak0.pak")) != baseDigest {
		t.Error("baseq2 changed")
	}
	// The package carries the account texture: the export's bytes, not the
	// installed archive's copy of the same name, with no grant asked for it.
	members := pakMembers(t, archive)
	if got, want := members["textures/"+q2AccountTexture+".wal"], digestOf(q2AccountWAL); got != want {
		t.Errorf("the package's %s.wal is %q, want the export's %q; members %v", q2AccountTexture, got, want, members)
	}
	if _, ok := members["docs/q2room/aub-licenses.md"]; !ok {
		t.Errorf("the export's licence summary is not in the package: %v", members)
	}

	// The package panel sees the same package, installation and running engine.
	status, body := m.call(http.MethodGet, "/api/v1/q2/installs/"+installation["id"].(string)+"/runs", nil)
	panelRun, _ := body["run"].(map[string]any)
	if status != http.StatusOK || panelRun == nil {
		t.Fatalf("the panel's newest run of the installation = %d: %v", status, body)
	}
	// While that engine runs, a second Build & Run will not replace its folder.
	// It gets that far with a backend that no longer sends a usable export:
	// the verified export of this revision is reused from the cache.
	m.backend.textures = []byte("this is not a bundle")
	status, started = m.call(http.MethodPost, "/api/v1/play/runs", granted)
	if status != http.StatusAccepted {
		t.Fatalf("starting again = %d: %v", status, started["error"])
	}
	if again := m.awaitPlay(t, started["id"].(string)); again["state"] != "failed" || again["failed_at"] != "installing" ||
		!strings.Contains(again["error"].(string), "still running") {
		t.Errorf("replacing the folder of a running engine: %v at %v: %v", again["state"], again["failed_at"], again["error"])
	}
	if status, body = m.call(http.MethodPost, "/api/v1/q2/runs/"+panelRun["id"].(string)+"/cancel", nil); status != http.StatusOK {
		t.Fatalf("stopping the engine from the panel = %d: %v", status, body["error"])
	}
	if finished := m.waitForJob(launch["job_id"].(string)); finished["state"] != "cancelled" {
		t.Errorf("the stopped engine's job is %v", finished["state"])
	}
}

// pakMembers is every member of a PAK, by path, with the digest of its bytes
// read from the archive's own directory.
func pakMembers(t *testing.T, archive string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[:4]) != "PACK" {
		t.Fatalf("%s is not a PAK", archive)
	}
	members := map[string]string{}
	offset, length := binary.LittleEndian.Uint32(data[4:]), binary.LittleEndian.Uint32(data[8:])
	for at := offset; at+64 <= offset+length && int(at+64) <= len(data); at += 64 {
		record := data[at : at+64]
		name := string(bytes.TrimRight(record[:56], "\x00"))
		start, size := binary.LittleEndian.Uint32(record[56:]), binary.LittleEndian.Uint32(record[60:])
		members[name] = digestOf(data[start : start+size])
	}
	return members
}

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Q2_015A: a revision the map binds that is no longer available stops the run
// at its textures, naming the source and what to do, before anything compiles.
func TestAQuake2RunsTexturesAreItsBoundRevisionOrNothing(t *testing.T) {
	m := newMachine(t)
	m.prepareQ2Play(t)
	granted := q2PlayBody(m)
	granted["grants"] = []map[string]any{{"loose": true, "basis": "own_work"}}

	m.backend.textures = fixtureQ2TextureBundle(m.backend.asset.assetID, m.backend.asset.revision, "bound_revision_unavailable")
	status, started := m.call(http.MethodPost, "/api/v1/play/runs", granted)
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	refused := m.awaitPlay(t, started["id"].(string))
	if refused["state"] != "failed" || refused["failed_at"] != "downloading_textures" ||
		!strings.Contains(fmt.Sprint(refused["error"]), "bound_revision_unavailable") ||
		!strings.Contains(fmt.Sprint(refused["remedy"]), q2AccountNamespace) {
		t.Fatalf("an unavailable binding: %v at %v: %v (%v)", refused["state"], refused["failed_at"], refused["error"], refused["remedy"])
	}
}
