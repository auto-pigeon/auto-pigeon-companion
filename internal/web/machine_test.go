package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The API side of the first-run journey, driven end to end against the fixture
// machine.
//
// It is the same sequence the browser test drives through the page, and it is
// here as well for two reasons: it runs everywhere, including on a machine with
// no browser installed, and when both fail it says which half is wrong.

// call is one guarded request against the fixture machine's server.
func (m *machine) call(method, path string, body any) (int, map[string]any) {
	m.t.Helper()
	payload := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			m.t.Fatal(err)
		}
		payload = string(encoded)
	}
	response, decoded := send(m.t, m.server, request(m.t, m.server, method, path, payload))
	return response.StatusCode, decoded
}

func (m *machine) signIn() {
	m.t.Helper()
	status, body := m.call(http.MethodPost, "/api/auth/login",
		map[string]string{"email": m.backend.email, "password": "hunter2"})
	if status != http.StatusOK {
		m.t.Fatalf("sign in = %d: %v", status, body["error"])
	}
}

func TestFirstRunJourney(t *testing.T) {
	m := newMachine(t)

	// 1. Signed out, the status says so and names the backend rather than
	//    guessing one.
	status, body := m.call(http.MethodGet, "/api/status", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if body["authenticated"] != false {
		t.Fatal("a fresh machine reported itself as signed in")
	}
	if body["aub_base_url"] != m.backend.url() {
		t.Fatalf("aub_base_url = %v, want %s", body["aub_base_url"], m.backend.url())
	}

	// The library route says "not signed in" rather than "no such thing".
	if status, body := m.call(http.MethodGet, "/api/v1/library/catalog", nil); status != http.StatusUnauthorized {
		t.Fatalf("the catalogue without a session = %d (%v), want 401", status, body["error"])
	}

	// 2. Sign in.
	m.signIn()
	status, body = m.call(http.MethodGet, "/api/status", nil)
	if body["authenticated"] != true {
		t.Fatalf("after signing in, status = %v", body)
	}

	// 3. The catalogue lists the fixture map.
	status, body = m.call(http.MethodGet, "/api/v1/library/catalog", nil)
	if status != http.StatusOK {
		t.Fatalf("catalog = %d: %v", status, body["error"])
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("catalog returned %d items, want 1", len(items))
	}

	// 4. Download the exact revision. What comes back is the record, and the
	//    cached list is what proves it afterwards.
	status, body = m.call(http.MethodPost, "/api/v1/library/sync", map[string]string{
		"asset_type": m.backend.asset.assetType,
		"asset_id":   m.backend.asset.assetID,
		"revision":   m.backend.asset.revisionID,
	})
	if status != http.StatusOK {
		t.Fatalf("sync = %d: %v", status, body["error"])
	}
	if body["key"] != m.backend.asset.revisionID {
		t.Fatalf("the cache key is %v, want the revision id %s", body["key"], m.backend.asset.revisionID)
	}
	status, body = m.call(http.MethodGet, "/api/v1/library/cached", nil)
	if cached, _ := body["items"].([]any); len(cached) != 1 {
		t.Fatalf("the cached list has %d entries after a download, want 1", len(cached))
	}

	// 5. The pipeline is runnable, because the toolchain that provides its one
	//    capability is on this machine.
	status, body = m.call(http.MethodGet, "/api/v1/build/pipelines", nil)
	if status != http.StatusOK {
		t.Fatalf("pipelines = %d: %v", status, body["error"])
	}
	pipeline := findItem(t, body, "id", "aucom.fixture.pipeline")
	if pipeline["runnable"] != true {
		t.Fatalf("the fixture pipeline is not runnable: %v", pipeline["missing_capabilities"])
	}

	// 6. The tool has to be approved before it can RUN. Previewing it is
	//    allowed and must stay allowed: looking at what something would do is
	//    exactly what somebody does before deciding whether to approve it, and
	//    a preview that refused until the grant existed would have the order
	//    backwards.
	assetRef := "aub:" + m.backend.asset.assetType + "/" + m.backend.asset.assetID +
		"@" + m.backend.asset.revisionID + "#" + m.backend.asset.fileName
	buildBody := map[string]any{
		"pipeline": "aucom.fixture.pipeline",
		"inputs":   map[string]string{"source_map": assetRef},
		"label":    "the journey",
	}
	if status, body := m.call(http.MethodPost, "/api/v1/build/preview", buildBody); status != http.StatusOK {
		t.Fatalf("previewing before approving = %d (%v), want 200", status, body["error"])
	}
	status, body = m.call(http.MethodPost, "/api/v1/jobs", map[string]any{
		"profile": "aucom.fixture.toolchain", "action": "compile",
		"inputs":      map[string]string{"source_map": m.writeSourceMap()},
		"executables": map[string]string{"tool": mustExecutable(t)},
	})
	if status != http.StatusForbidden {
		t.Fatalf("running an unapproved local profile = %d (%v), want 403", status, body["error"])
	}

	// 7. Approve the toolchain, against the digest that was shown.
	status, body = m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.toolchain", nil)
	if status != http.StatusOK {
		t.Fatalf("reading the toolchain profile = %d", status)
	}
	if body["trust"] != "local" {
		t.Fatalf("a document found in the profile directory has trust %v, want local", body["trust"])
	}
	if body["authorized"] != false {
		t.Fatal("an unapproved local profile reported itself as authorized")
	}
	digest, _ := body["digest"].(string)

	// A stale digest is refused: an approval for a document that has changed
	// since it was displayed is an approval of something nobody read.
	if status, _ := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/grant",
		map[string]string{"digest": "sha256:not-the-one-you-read"}); status != http.StatusConflict {
		t.Fatalf("a grant with the wrong digest = %d, want 409", status)
	}
	status, body = m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/grant",
		map[string]string{"digest": digest})
	if status != http.StatusOK {
		t.Fatalf("granting = %d: %v", status, body["error"])
	}
	if body["authorized"] != true {
		t.Fatal("the profile is still not authorized after a grant")
	}

	// 8. Build. The toolchain's executable is recorded the same way an
	//    engine's is, through the binding.
	self := mustExecutable(t)
	m.bindExecutable("aucom.fixture.toolchain", "tool", self)
	status, body = m.call(http.MethodPost, "/api/v1/build/preview", buildBody)
	if status != http.StatusOK {
		t.Fatalf("preview = %d: %v", status, body["error"])
	}
	steps, _ := body["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("the preview has %d steps, want 1", len(steps))
	}
	step, _ := steps[0].(map[string]any)
	command, _ := step["command"].(map[string]any)
	if shell, _ := command["shell"].(string); !strings.Contains(shell, buildHelperFlag) {
		t.Fatalf("the previewed command does not run the fixture tool: %q", shell)
	}

	status, body = m.call(http.MethodPost, "/api/v1/build/runs", buildBody)
	if status != http.StatusAccepted {
		t.Fatalf("starting the build = %d: %v", status, body["error"])
	}
	buildID, _ := body["build"].(string)
	if buildID == "" {
		t.Fatal("the build was started without an id")
	}
	manifest := m.waitForBuild(buildID)
	if manifest["state"] != "succeeded" {
		t.Fatalf("the build %v: %v", manifest["state"], manifest["error"])
	}
	outputs, _ := manifest["outputs"].([]any)
	if len(outputs) == 0 {
		t.Fatal("the build produced no outputs")
	}

	// The build's provenance names the exact revision it read, and not the word
	// `current`. It is recorded against the input file itself, beside the
	// digest of the bytes.
	inputs, _ := manifest["inputs"].([]any)
	if len(inputs) == 0 {
		t.Fatal("the manifest recorded no inputs")
	}
	input, _ := inputs[0].(map[string]any)
	source, _ := input["source"].(map[string]any)
	if source == nil {
		t.Fatalf("the input %v carries no source; a build from an asset must record which revision it read", input)
	}
	if source["revision_id"] != m.backend.asset.revisionID {
		t.Fatalf("the manifest recorded revision %v, want %s", source["revision_id"], m.backend.asset.revisionID)
	}
	if source["refetchable"] != true {
		t.Fatal("an immutable revision was recorded as not refetchable")
	}

	// 9. Set the engine up and start it. The engine profile is `local` too, so
	//    this is the second place the approval cannot be skipped.
	status, body = m.call(http.MethodGet, "/api/v1/engines", nil)
	if status != http.StatusOK {
		t.Fatalf("engines = %d: %v", status, body["error"])
	}
	engine := findItem(t, body, "id", "aucom.fixture.q1-engine")
	if engine["ready"] != false {
		t.Fatal("an engine with nothing recorded reported itself ready")
	}
	problems, _ := engine["action_problems"].(map[string]any)
	playMap, _ := problems["play_map"].([]any)
	if len(playMap) == 0 {
		t.Fatal("an unbound engine reported no problems")
	}

	status, body = m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.q1-engine/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": m.gameRoot, "content_root": m.content},
		"approve":     true,
		"digest":      engine["digest"],
	})
	if status != http.StatusOK {
		t.Fatalf("binding the engine = %d: %v", status, body["error"])
	}
	if body["authorized"] != true {
		t.Fatalf("the engine is still not approved: %v", body["authorization_error"])
	}
	status, body = m.call(http.MethodGet, "/api/v1/engines/aucom.fixture.q1-engine", nil)
	if body["ready"] != true {
		t.Fatalf("the engine is still not ready: %v", body["action_problems"])
	}

	status, body = m.call(http.MethodPost, "/api/v1/jobs", map[string]any{
		"profile": "aucom.fixture.q1-engine", "action": "play_map",
		// The schema's own runtime names — see internal/profile: an engine
		// profile writes {runtime.map_name} in its argv.
		"runtime": map[string]string{"map_name": "e1m1", "mod_name": "id1"},
	})
	if status != http.StatusAccepted {
		t.Fatalf("launching = %d: %v", status, body["error"])
	}
	jobID, _ := body["id"].(string)
	finished := m.waitForJob(jobID)
	if finished["state"] != "succeeded" {
		t.Fatalf("the launch %v: %v", finished["state"], finished["error"])
	}

	// 10. And every one of those is still there after the page is reloaded,
	//     because none of it was ever only in the page.
	status, body = m.call(http.MethodGet, "/api/v1/jobs", nil)
	if jobs, _ := body["items"].([]any); len(jobs) < 2 {
		t.Fatalf("the job list has %d entries, want the compile and the launch", len(jobs))
	}
	status, body = m.call(http.MethodGet, "/api/v1/build/runs", nil)
	if builds, _ := body["items"].([]any); len(builds) != 1 {
		t.Fatalf("the build list has %d entries after one build", len(builds))
	}
}

// A custom engine, set up entirely through the wizard's forms and through JSON
// that was exported and imported again — with no change to any file in this
// repository.
//
// That last part is the point of the whole profile format. If setting up an
// engine nobody anticipated needed a Go change, the format would be decoration.
func TestCustomEngineThroughFormsAndJSON(t *testing.T) {
	m := newMachine(t)

	// 1. The wizard starts from a document that has been tested. It offers the
	//    built-ins and nothing else, because "tested" is a claim and those are
	//    the only documents this program can make it about.
	status, body := m.call(http.MethodGet, "/api/v1/profiles/templates?kind=engine", nil)
	if status != http.StatusOK {
		t.Fatalf("templates = %d: %v", status, body["error"])
	}
	template := findItem(t, body, "id", "auto-pigeon.engine.quakespasm")
	if actions, _ := template["actions"].([]any); len(actions) == 0 {
		t.Fatal("the template offers no actions")
	}

	// 2. The forms. Every one of these is a field somebody types into; nothing
	//    below writes JSON by hand.
	compose := map[string]any{
		"template":       "auto-pigeon.engine.quakespasm",
		"id":             "me.engine.my-quake",
		"name":           "My Quake build",
		"version":        "1.0.0",
		"summary":        "The build I compiled myself, with my own name for the program.",
		"publisher_name": "A mapper",
		"license_spdx":   "GPL-2.0-or-later",
		"runtime":        "my-quake",
		"engine_version": "1.2.3",
		"executables":    map[string]string{"engine": "my-quake{platform.exe_suffix}"},
		// This engine does not host anything, so those actions are left out
		// rather than left in and hoped about.
		"actions": []string{"play_map", "play_package"},
	}
	status, body = m.call(http.MethodPost, "/api/v1/profiles/compose", compose)
	if status != http.StatusOK {
		t.Fatalf("compose = %d: %v", status, body["error"])
	}
	if body["valid"] != true {
		t.Fatalf("the composed document is not valid: %v", body["error"])
	}
	if body["id"] != "me.engine.my-quake" {
		t.Fatalf("the composed id is %v", body["id"])
	}
	if actions, _ := body["actions"].([]any); len(actions) != 2 {
		t.Fatalf("the composed document has %d actions, want the two that were kept", len(actions))
	}
	// The normalized diff says what the user actually changed, which is what
	// the review step shows.
	diff, _ := body["diff"].(map[string]any)
	if diff == nil || diff["empty"] == true {
		t.Fatal("the diff against the template is empty; the wizard changed nothing")
	}
	document := body["document"]
	digest, _ := body["digest"].(string)

	// An action the template does not have is refused rather than invented: an
	// action describes something a program actually does.
	bad := map[string]any{}
	for key, value := range compose {
		bad[key] = value
	}
	bad["actions"] = []string{"play_map", "warp_to_hyperspace"}
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/compose", bad); status != http.StatusBadRequest {
		t.Fatalf("composing an action the template lacks = %d (%v), want 400", status, body["error"])
	}

	// 3. Install it. It arrives as `local` and is not approved: importing is
	//    inert, and an import that granted would delete the trust model.
	status, body = m.call(http.MethodPost, "/api/v1/profiles/import", map[string]any{"document": document})
	if status != http.StatusCreated {
		t.Fatalf("import = %d: %v", status, body["error"])
	}
	if body["trust"] != "local" {
		t.Fatalf("an imported document has trust %v, want local", body["trust"])
	}
	if body["authorized"] != false {
		t.Fatal("importing a profile approved it")
	}
	if body["digest"] != digest {
		t.Fatalf("the installed digest %v is not the composed one %s", body["digest"], digest)
	}

	// Importing the same id again is refused rather than silently replacing an
	// approved profile.
	if status, _ := m.call(http.MethodPost, "/api/v1/profiles/import",
		map[string]any{"document": document}); status != http.StatusConflict {
		t.Fatalf("a second import of the same id = %d, want 409", status)
	}

	// 4. Export it and import it somewhere else: the canonical bytes round-trip
	//    to the same digest, which is what makes an exported profile something
	//    a second machine can verify against the first.
	status, body = m.call(http.MethodGet, "/api/v1/profiles/me.engine.my-quake/document", nil)
	if status != http.StatusOK {
		t.Fatalf("export = %d: %v", status, body["error"])
	}
	if body["digest"] != digest {
		t.Fatalf("the exported digest %v is not the installed one %s", body["digest"], digest)
	}
	exported := body["document"]

	second := newMachine(t)
	status, body = second.call(http.MethodPost, "/api/v1/profiles/import", map[string]any{"document": exported})
	if status != http.StatusCreated {
		t.Fatalf("importing the export on another machine = %d: %v", status, body["error"])
	}
	if body["digest"] != digest {
		t.Fatalf("the same document has digest %v there and %s here", body["digest"], digest)
	}

	// 5. And it runs: set up, approved, previewed. The executable is a real
	//    file on this machine, which is the half the document deliberately
	//    does not carry.
	self := mustExecutable(t)
	status, body = m.call(http.MethodPost, "/api/v1/profiles/me.engine.my-quake/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": m.gameRoot, "content_root": m.content},
		"approve":     true,
		"digest":      digest,
	})
	if status != http.StatusOK {
		t.Fatalf("binding the custom engine = %d: %v", status, body["error"])
	}
	if body["authorized"] != true {
		t.Fatalf("the custom engine is still not approved: %v", body["authorization_error"])
	}

	status, body = m.call(http.MethodPost, "/api/v1/jobs/preview", map[string]any{
		"profile": "me.engine.my-quake", "action": "play_map",
		"runtime": map[string]string{"map_name": "e1m1", "mod_name": "id1"},
	})
	if status != http.StatusOK {
		t.Fatalf("previewing the custom engine = %d: %v", status, body["error"])
	}
	command, _ := body["command"].(map[string]any)
	shell, _ := command["shell"].(string)
	if !strings.Contains(shell, self) {
		t.Fatalf("the resolved command does not start the program that was bound: %q", shell)
	}
	if !strings.Contains(shell, "e1m1") {
		t.Fatalf("the resolved command does not carry the map: %q", shell)
	}

	// The Run area lists it beside the built-in engines, because there is one
	// catalog and no second list of engines to fall out of step with it.
	status, body = m.call(http.MethodGet, "/api/v1/engines", nil)
	custom := findItem(t, body, "id", "me.engine.my-quake")
	if custom["ready"] != true {
		t.Fatalf("the custom engine is not ready: %v", custom["action_problems"])
	}
}

// Everything the browser did is on disk, and a second Companion over the same
// directories finds all of it — without re-running anything.
func TestRestartPreservesStateAndRunsNothingTwice(t *testing.T) {
	m := newMachine(t)
	m.signIn()

	self := mustExecutable(t)
	status, body := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.q1-engine", nil)
	if status != http.StatusOK {
		t.Fatalf("reading the engine = %d", status)
	}
	digest, _ := body["digest"].(string)
	status, body = m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.q1-engine/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": m.gameRoot, "content_root": m.content},
		"approve":     true, "digest": digest,
	})
	if status != http.StatusOK {
		t.Fatalf("binding = %d: %v", status, body["error"])
	}
	status, body = m.call(http.MethodPost, "/api/v1/library/sync", map[string]string{
		"asset_type": m.backend.asset.assetType, "asset_id": m.backend.asset.assetID,
		"revision": m.backend.asset.revisionID,
	})
	if status != http.StatusOK {
		t.Fatalf("sync = %d: %v", status, body["error"])
	}
	status, body = m.call(http.MethodPost, "/api/v1/jobs", map[string]any{
		"profile": "aucom.fixture.q1-engine", "action": "play_map",
		"runtime": map[string]string{"map_name": "e1m1", "mod_name": "id1"},
	})
	if status != http.StatusAccepted {
		t.Fatalf("launching = %d: %v", status, body["error"])
	}
	jobID, _ := body["id"].(string)
	m.waitForJob(jobID)

	served := m.backend.count()
	before := m.jobIDs()
	if len(before) != 1 {
		t.Fatalf("the first run made %d jobs, want 1", len(before))
	}

	// A second server, over the same directories, the way a restart is.
	restarted := m.restart()

	status, body = restarted.call(http.MethodGet, "/api/status", nil)
	if body["authenticated"] != true {
		t.Fatalf("the session did not survive a restart: %v", body)
	}
	if body["email"] != m.backend.email {
		t.Fatalf("the restarted Companion reports %v as the account", body["email"])
	}

	status, body = restarted.call(http.MethodGet, "/api/v1/engines/aucom.fixture.q1-engine", nil)
	if body["ready"] != true {
		t.Fatalf("the local setup did not survive a restart: %v", body["action_problems"])
	}

	status, body = restarted.call(http.MethodGet, "/api/v1/library/cached", nil)
	if cached, _ := body["items"].([]any); len(cached) != 1 {
		t.Fatalf("the cache has %d revisions after a restart, want 1", len(cached))
	}

	after := restarted.jobIDs()
	if len(after) != len(before) {
		t.Fatalf("a restart turned %d jobs into %d: something ran again", len(before), len(after))
	}
	for index, id := range before {
		if after[index] != id {
			t.Fatalf("job %d is %s after the restart and was %s before", index, after[index], id)
		}
	}
	// And it fetched nothing to find that out: the cache and the job store are
	// read from disk.
	if got := restarted.backend.count(); got != served {
		t.Fatalf("a restart made %d extra requests to the backend", got-served)
	}
}
