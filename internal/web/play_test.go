package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
)

// The Build & Run journey, driven end to end against the fixture machine.
//
// `AUCOM/AUE/AUT 246I1`'s acceptance list, in one test each: the exact map
// revision and its texture bundle arrive together, the WAD directory reaches
// the compiler as `-wadpath`, `id1` is untouched, the level and the WADs land
// under the owned `auto-pigeon` sibling, the engine starts with structured
// argv, and a bundle AUB could not complete starts nothing at all.

// prepare signs in, approves the toolchain and the engine, and binds both —
// everything a person does once, before the journey this test is about.
func (m *machine) preparePlay(t *testing.T) {
	t.Helper()

	m.signIn()
	self := mustExecutable(t)

	_, tool := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.toolchain", nil)
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/grant",
		map[string]any{"digest": tool["digest"]}); status != http.StatusOK {
		t.Fatalf("granting the toolchain = %d: %v", status, body["error"])
	}
	m.bindExecutable("aucom.fixture.toolchain", "tool", self)

	_, engineProfile := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.q1-engine", nil)
	status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.q1-engine/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": m.gameRoot, "content_root": m.content},
		"approve":     true,
		"digest":      engineProfile["digest"],
	})
	if status != http.StatusOK {
		t.Fatalf("binding the engine = %d: %v", status, body["error"])
	}
}

func playBody(m *machine) map[string]any {
	return map[string]any{
		"asset_type":      "map",
		"asset_id":        m.backend.asset.assetID,
		"revision_id":     m.backend.asset.revisionID,
		"revision_number": m.backend.asset.revision,
		"source_file":     m.backend.asset.fileName,
		"pipeline":        "aucom.fixture.pipeline",
		"engine":          "aucom.fixture.q1-engine",
		"action":          "play_map",
		"mod":             "auto-pigeon",
		"map":             "dm1",
	}
}

// awaitPlay polls a run until it stops, the way the Activity panel does.
func (m *machine) awaitPlay(t *testing.T, id string) map[string]any {
	t.Helper()

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		status, body := m.call(http.MethodGet, "/api/v1/play/runs/"+id, nil)
		if status != http.StatusOK {
			t.Fatalf("reading run %s = %d: %v", id, status, body["error"])
		}
		if body["active"] == false {
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s never finished", id)

	return nil
}

// treeOf is every file under a directory, with its size — enough to prove a
// directory was not touched.
func treeOf(t *testing.T, root string) []string {
	t.Helper()

	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(relative)+":"+fileDigestForTest(t, path))

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)

	return out
}

func fileDigestForTest(t *testing.T, path string) string {
	t.Helper()

	digest, _, err := engine.DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return digest
}

// --- the journey -----------------------------------------------------------

func TestBuildAndRunDoesTheWholeSequenceFromOneConfirmation(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)

	id1Before := treeOf(t, filepath.Join(m.gameRoot, "id1"))

	// The review, first: it says what would happen and starts nothing.
	status, plan := m.call(http.MethodPost, "/api/v1/play/plan", playBody(m))
	if status != http.StatusOK {
		t.Fatalf("plan = %d: %v", status, plan["error"])
	}
	writes, _ := plan["writes"].(map[string]any)
	if dir, _ := writes["directory"].(string); dir != filepath.Join(m.gameRoot, "auto-pigeon") {
		t.Errorf("the plan writes into %q, want the auto-pigeon sibling", dir)
	}
	if never, _ := writes["never_writes"].(string); !strings.Contains(never, "id1") {
		t.Errorf("the plan does not say it never writes into id1: %q", never)
	}
	launch, _ := plan["launch"].(map[string]any)
	if launch["known"] != true {
		t.Fatalf("the plan could not resolve the engine command: %v", launch)
	}
	// The textures are not downloaded by a plan, so the review says they will
	// be rather than claiming what is in them.
	textures, _ := plan["textures"].(map[string]any)
	if textures["known"] != false {
		t.Errorf("a plan claimed to know a bundle it has not fetched: %v", textures)
	}

	// One confirmation.
	status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	runID, _ := started["id"].(string)
	run := m.awaitPlay(t, runID)

	if run["state"] != "succeeded" {
		t.Fatalf("the run %v at %v: %v", run["state"], run["failed_at"], run["error"])
	}

	// 1. The exact revision, and its textures, as one revision-locked unit.
	bundle, _ := run["bundle"].(map[string]any)
	if bundle == nil {
		t.Fatal("the run recorded no texture bundle")
	}
	if int(bundle["revision"].(float64)) != m.backend.asset.revision {
		t.Errorf("bundle revision = %v, want %d", bundle["revision"], m.backend.asset.revision)
	}
	if bundle["compiler_ready"] != true {
		t.Errorf("bundle compiler_ready = %v", bundle["compiler_ready"])
	}
	declared := stringsOf(bundle["wads_declared"])
	if len(declared) != 2 || declared[0] != "first.wad" || declared[1] != "second.wad" {
		t.Errorf("declaration = %v, and the order is the map's own content", declared)
	}

	// 2. `id1` is byte for byte what it was.
	if id1After := treeOf(t, filepath.Join(m.gameRoot, "id1")); !equalStrings(id1Before, id1After) {
		t.Errorf("id1 changed:\n before %v\n after  %v", id1Before, id1After)
	}

	// 3. The owned sibling holds the level, the WADs and the build manifest.
	modDir := filepath.Join(m.gameRoot, "auto-pigeon")
	for _, want := range []string{
		filepath.Join("maps", "dm1.bsp"),
		filepath.Join(engine.WADDir, "first.wad"),
		filepath.Join(engine.WADDir, "second.wad"),
		engine.BuildManifestName,
		engine.StampName,
	} {
		if _, err := os.Stat(filepath.Join(modDir, want)); err != nil {
			t.Errorf("%s is not in the staged mod: %v", want, err)
		}
	}

	// 4. Every staged file is recorded with the digest it was staged at.
	installed, _ := run["installed"].([]any)
	if len(installed) < 4 {
		t.Errorf("the run recorded %d staged file(s): %v", len(installed), installed)
	}
	for _, entry := range installed {
		file, _ := entry.(map[string]any)
		digest, _ := file["sha256"].(string)
		path, _ := file["path"].(string)
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
			t.Errorf("%s was recorded with %q, want a sha256 digest", path, digest)
		}
	}

	// 5. The engine was started with structured argv, element by element.
	launched, _ := run["launch"].(map[string]any)
	if launched == nil {
		t.Fatal("no launch was recorded")
	}
	args := stringsOf(launched["args"])
	if !containsInOrder(args, "-game", "auto-pigeon") || !containsInOrder(args, "+map", "dm1") {
		t.Errorf("argv = %v, want -game auto-pigeon and +map dm1 as separate elements", args)
	}
	for _, arg := range args {
		if strings.Contains(arg, " -") && strings.Contains(arg, "+map") {
			t.Errorf("an argument looks like a command string rather than one element: %q", arg)
		}
	}

	// 6. The compiler really read the verified bundle's directory.
	buildID, _ := run["build_id"].(string)
	if buildID == "" {
		t.Fatal("the run recorded no build")
	}
	status, buildBody := m.call(http.MethodGet, "/api/v1/build/runs/"+buildID, nil)
	if status != http.StatusOK {
		t.Fatalf("reading the build = %d: %v", status, buildBody["error"])
	}
	built, _ := buildBody["manifest"].(map[string]any)
	steps, _ := built["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	command, _ := step["command"].(map[string]any)
	commandArgs := stringsOf(command["args"])
	if !containsString(commandArgs, "-wadpath") {
		t.Errorf("the compile argv has no -wadpath: %v", commandArgs)
	}
	// And the manifest records WHICH bundle supplied it.
	roots, _ := built["roots"].([]any)
	if len(roots) != 1 {
		t.Fatalf("the build manifest records %d root(s): %v", len(roots), roots)
	}
	root, _ := roots[0].(map[string]any)
	source, _ := root["source"].(map[string]any)
	manifestBundle, _ := source["bundle"].(map[string]any)
	if manifestBundle == nil || manifestBundle["map_id"] != m.backend.asset.assetID {
		t.Errorf("the build manifest's root source = %v", source)
	}

	// 7. Every stage is in the record, with a duration.
	stages, _ := run["stages"].([]any)
	seen := map[string]bool{}
	for _, entry := range stages {
		stage, _ := entry.(map[string]any)
		state, _ := stage["state"].(string)
		seen[state] = true
		if stage["finished_at"] == nil {
			t.Errorf("the %s stage never finished", state)
		}
	}
	for _, want := range []string{
		"downloading_map", "downloading_textures", "converting",
		"compiling", "installing", "launching", "running",
	} {
		if !seen[want] {
			t.Errorf("the run has no %s stage: %v", want, seen)
		}
	}
}

// A bundle AUB could not complete stops the sequence before the extractor or a
// compiler starts, and the refusals reach the page verbatim.
func TestBuildAndRunRefusesABundleThatIsNotCompilerReady(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)
	m.backend.textures = fixtureTextureBundle(
		m.backend.asset.assetID, m.backend.asset.revision, false)

	id1Before := treeOf(t, filepath.Join(m.gameRoot, "id1"))

	status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))

	if run["state"] != "failed" || run["failed_at"] != "downloading_textures" {
		t.Fatalf("state = %v at %v: %v", run["state"], run["failed_at"], run["error"])
	}
	if message, _ := run["error"].(string); !strings.Contains(message, "quake101.wad") {
		t.Errorf("the error does not name AUB's refusal: %q", message)
	}
	if remedy, _ := run["remedy"].(string); !strings.Contains(remedy, "quake101.wad") {
		t.Errorf("the remedy does not name the missing WAD: %q", remedy)
	}
	if run["build_id"] != nil {
		t.Error("a build was started for a bundle that is not compiler-ready")
	}
	if run["launch"] != nil {
		t.Error("the engine was started for a bundle that is not compiler-ready")
	}
	// Nothing was installed, and id1 is untouched.
	if _, err := os.Stat(filepath.Join(m.gameRoot, "auto-pigeon")); err == nil {
		t.Error("a refused run created the mod directory anyway")
	}
	if after := treeOf(t, filepath.Join(m.gameRoot, "id1")); !equalStrings(id1Before, after) {
		t.Error("id1 changed during a refused run")
	}
}

// A bundle for another revision is refused, so a map saved between choosing a
// revision and building it can never be paired with the wrong textures.
func TestBuildAndRunRefusesABundleForAnotherRevision(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)
	m.backend.textures = fixtureTextureBundle(m.backend.asset.assetID, m.backend.asset.revision+1, true)

	status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))

	if run["state"] != "failed" || run["failed_at"] != "downloading_textures" {
		t.Fatalf("state = %v at %v: %v", run["state"], run["failed_at"], run["error"])
	}
}

// The second build replaces only what the Companion staged, and `id1` is still
// untouched. That is what "atomically replaces only AUCOM-owned files" means.
func TestASecondBuildAndRunReplacesOnlyWhatItStaged(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)

	status, first := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, first["error"])
	}
	if run := m.awaitPlay(t, first["id"].(string)); run["state"] != "succeeded" {
		t.Fatalf("the first run %v: %v", run["state"], run["error"])
	}

	// A file of the user's own, beside the staged ones.
	modDir := filepath.Join(m.gameRoot, "auto-pigeon")
	mine := filepath.Join(modDir, "autoexec.cfg")
	if err := os.WriteFile(mine, []byte("bind x \"impulse 9\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id1Before := treeOf(t, filepath.Join(m.gameRoot, "id1"))

	status, second := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting the second = %d: %v", status, second["error"])
	}
	if run := m.awaitPlay(t, second["id"].(string)); run["state"] != "succeeded" {
		t.Fatalf("the second run %v: %v", run["state"], run["error"])
	}

	if _, err := os.Stat(mine); err != nil {
		t.Errorf("the second run removed a file the Companion did not stage: %v", err)
	}
	if after := treeOf(t, filepath.Join(m.gameRoot, "id1")); !equalStrings(id1Before, after) {
		t.Error("id1 changed during the second run")
	}
}

// A reload recovers the same run: the record is on disk, and a restarted
// Companion reads it.
func TestAReloadShowsTheSameBuildAndRun(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)

	status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	id, _ := started["id"].(string)
	m.awaitPlay(t, id)

	restarted := m.restart()
	status, body := restarted.call(http.MethodGet, "/api/v1/play/runs/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("after a restart, reading the run = %d: %v", status, body["error"])
	}
	if body["state"] != "succeeded" || body["launch"] == nil {
		t.Errorf("the restarted Companion lost the run: %v", body)
	}
	status, list := restarted.call(http.MethodGet, "/api/v1/play/runs", nil)
	items, _ := list["items"].([]any)
	if status != http.StatusOK || len(items) != 1 {
		t.Errorf("the list after a restart = %d, %d item(s)", status, len(items))
	}
}

// --- the texture surface ---------------------------------------------------

// The page receives sanitized facts and never a credential, a download URL or
// a path on this machine.
func TestTheTextureStatusLeaksNothing(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)

	status, body := m.call(http.MethodGet,
		"/api/v1/play/textures?asset_id="+m.backend.asset.assetID+"&revision=4", nil)
	if status != http.StatusOK {
		t.Fatalf("textures = %d: %v", status, body["error"])
	}
	if body["compiler_ready"] != true {
		t.Errorf("compiler_ready = %v", body["compiler_ready"])
	}
	declared := stringsOf(body["wads_declared"])
	if len(declared) != 2 || declared[0] != "first.wad" {
		t.Errorf("wads_declared = %v", declared)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{m.backend.token, m.backend.url(), m.dir} {
		if secret != "" && strings.Contains(text, secret) {
			t.Errorf("the texture status carries %q", secret)
		}
	}
}

// A signed-out request is told to sign in rather than being given an empty
// answer that looks like "this map needs nothing".
func TestTheTextureStatusAsksForASignIn(t *testing.T) {
	m := newMachine(t)

	status, body := m.call(http.MethodGet,
		"/api/v1/play/textures?asset_id="+m.backend.asset.assetID+"&revision=4", nil)
	if status != http.StatusOK {
		t.Fatalf("textures = %d: %v", status, body["error"])
	}
	if body["known"] != false {
		t.Fatalf("a signed-out answer claimed to know the bundle: %v", body)
	}
	if message, _ := body["message"].(string); !strings.Contains(strings.ToLower(message), "sign in") {
		t.Errorf("message = %q, want a sign-in remedy", message)
	}
}

// An expired session during the run is reported with the remedy, not as a
// mysterious failure.
func TestAnExpiredSessionDuringARunSaysToSignInAgain(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)

	// The session is dropped, exactly as an expiry drops it.
	if status, body := m.call(http.MethodPost, "/api/auth/logout", nil); status != http.StatusOK {
		t.Fatalf("signing out = %d: %v", status, body["error"])
	}

	status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))
	if run["state"] != "failed" {
		t.Fatalf("state = %v", run["state"])
	}
	remedy, _ := run["remedy"].(string)
	if !strings.Contains(strings.ToLower(remedy), "signed in") {
		t.Errorf("remedy = %q, want a sign-in remedy", remedy)
	}
}

// --- the cached bundle ------------------------------------------------------

// A complete verified entry is reused with no request to the backend. That is
// what makes a retry, and an offline build, cheap and honest.
func TestASecondRunReusesTheVerifiedBundle(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)

	status, first := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, first["error"])
	}
	m.awaitPlay(t, first["id"].(string))

	// The cache holds a verified entry for this exact revision.
	cache, err := m.server.textureCache()
	if err != nil {
		t.Fatal(err)
	}
	entry, found := cache.Lookup(texturebundle.Expect{
		MapID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
	})
	if !found {
		t.Fatal("the run published no verified bundle")
	}
	if err = entry.Verify(); err != nil {
		t.Errorf("the published entry does not verify: %v", err)
	}

	// The backend now refuses everything. The second run still builds, because
	// the bundle it needs is verified and local.
	m.backend.textures = []byte("this is not a bundle")
	status, second := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting the second = %d: %v", status, second["error"])
	}
	if run := m.awaitPlay(t, second["id"].(string)); run["state"] != "succeeded" {
		t.Fatalf("the second run %v at %v: %v", run["state"], run["failed_at"], run["error"])
	}
}

// --- helpers ----------------------------------------------------------------

func stringsOf(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		out = append(out, text)
	}

	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func containsInOrder(values []string, first, second string) bool {
	for i := 0; i+1 < len(values); i++ {
		if values[i] == first && values[i+1] == second {
			return true
		}
	}

	return false
}
