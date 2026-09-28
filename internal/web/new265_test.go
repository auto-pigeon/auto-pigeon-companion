package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/autobuild"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// NEW_265, through the HTTP surface the page uses, on the fixture machine: the
// real executor, catalog, build runner and asset cache; a fake AUB and a fake
// compiler.

// installLogToolchain adds a toolchain whose compile step declares its
// transcript as a `.log` output — EricW's shape — and a pipeline over it, then
// approves and binds it.
func (m *machine) installLogToolchain() {
	m.t.Helper()
	var tool map[string]any
	if err := json.Unmarshal(fixtureToolJSON(mustExecutable(m.t)), &tool); err != nil {
		m.t.Fatal(err)
	}
	tool["id"] = "aucom.fixture.logtool"
	tool["name"] = "Fixture toolchain with a log"
	tool["capabilities"] = []map[string]any{{"id": "fixture.logcompile", "title": "Compile",
		"consumes": []string{"q1.map.source"}, "produces": []string{"q1.bsp"}}}
	action := tool["actions"].([]any)[0].(map[string]any)
	action["capability"] = "fixture.logcompile"
	action["title"] = "Compile the map"
	action["outputs"] = []map[string]any{
		{"name": "bsp", "title": "BSP", "role": "q1.bsp", "path": "{option.basename}.bsp"},
		{"name": "log", "title": "Compiler log", "role": "q1.compile.log", "path": "{option.basename}.log", "optional": true},
	}
	m.writeProfile("aucom.fixture.logtool.json", encodeFixture(tool))

	var pipeline map[string]any
	if err := json.Unmarshal(fixturePipelineJSON(), &pipeline); err != nil {
		m.t.Fatal(err)
	}
	pipeline["id"] = "aucom.fixture.logpipeline"
	pipeline["name"] = "Fixture build with a log"
	step := pipeline["steps"].([]any)[0].(map[string]any)
	step["capability"] = "fixture.logcompile"
	step["title"] = "Compile the map"
	m.writeProfile("aucom.fixture.logpipeline.json", encodeFixture(pipeline))

	_, body := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.logtool", nil)
	status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.logtool/bind", map[string]any{
		"executables": map[string]string{"tool": mustExecutable(m.t)},
		"approve":     true, "digest": body["digest"],
	})
	if status != http.StatusOK {
		m.t.Fatalf("binding the log toolchain = %d: %v", status, body["error"])
	}
}

// startLogBuild starts the log pipeline on a local map and returns the build
// and the compile step's job once it has one.
func (m *machine) startLogBuild(fail bool) (string, string) {
	m.t.Helper()
	body := map[string]any{
		"pipeline": "aucom.fixture.logpipeline",
		"inputs":   map[string]string{"source_map": m.writeSourceMap()},
	}
	if fail {
		body["options"] = map[string]map[string]string{"compile": {"fail": "true"}}
	}
	status, started := m.call(http.MethodPost, "/api/v1/build/runs", body)
	if status != http.StatusAccepted {
		m.t.Fatalf("starting the build = %d: %v", status, started["error"])
	}
	buildID := started["build"].(string)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, run := m.call(http.MethodGet, "/api/v1/build/runs/"+buildID, nil)
		manifest, _ := run["manifest"].(map[string]any)
		steps, _ := manifest["steps"].([]any)
		if len(steps) > 0 {
			if id, _ := steps[0].(map[string]any)["job_id"].(string); id != "" {
				return buildID, id
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.t.Fatal("the compile step never got a job")
	return "", ""
}

// followOutput reads a job's output the way joboutput.js does.
func (m *machine) followOutput(jobID string) (all, whileRunning string, last map[string]any) {
	m.t.Helper()
	from, cursor := "", ""
	var text strings.Builder
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		query := url.Values{"source": {"auto"}}
		if from != "" {
			query.Set("from", from)
		}
		if cursor != "" {
			query.Set("offset", cursor)
		}
		status, chunk := m.call(http.MethodGet, "/api/v1/jobs/"+jobID+"/output?"+query.Encode(), nil)
		if status != http.StatusOK {
			m.t.Fatalf("reading the output = %d: %v", status, chunk["error"])
		}
		if chunk["reset"] == true {
			text.Reset()
		}
		source, _ := chunk["source"].(map[string]any)
		from, _ = source["id"].(string)
		text.WriteString(chunk["text"].(string))
		cursor = strings.TrimSuffix(strings.TrimSuffix(jsonNumber(chunk["next"]), ".0"), " ")
		if chunk["terminal"] != true {
			whileRunning = text.String()
		}
		if chunk["complete"] == true {
			return text.String(), whileRunning, chunk
		}
		time.Sleep(40 * time.Millisecond)
	}
	m.t.Fatalf("the output of %s never completed: %q", jobID, text.String())
	return "", "", nil
}

func jsonNumber(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestLiveOutputOfASidecarOnlyCompilerAndPerExecutableArgumentsReachTheJob(t *testing.T) {
	m := newMachine(t)
	m.installLogToolchain()

	// The person's own flags for this one program, through Profiles.
	tokens := []string{"--sidecar", "--slow=150", "--lines=8"}
	status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.logtool/arguments",
		map[string]any{"executable": "tool", "arguments": tokens})
	if status != http.StatusOK {
		t.Fatalf("saving the arguments = %d: %v", status, body["error"])
	}

	_, jobID := m.startLogBuild(true)
	all, running, last := m.followOutput(jobID)
	if !strings.Contains(running, "compile line 2") {
		t.Errorf("while the compiler ran the output held %q; real lines must appear before it stops", running)
	}
	for _, want := range []string{"argv flag --sidecar", "argv flag --slow=150", "compile line 8", "FATAL the map leaks"} {
		if !strings.Contains(all, want) {
			t.Errorf("the output is missing %q:\n%s", want, all)
		}
	}
	source, _ := last["source"].(map[string]any)
	if source["id"] != "log:log" || !strings.Contains(source["label"].(string), "level.log") {
		t.Errorf("the output was read from %v", source)
	}

	// The job says which words were the person's, and they sit before the
	// operands in the argv the program received.
	finished := m.waitForJob(jobID)
	if got := stringsOf(finished["custom_args"]); !equalStrings(got, tokens) {
		t.Errorf("the job records custom args %v, want %v", got, tokens)
	}
	command, _ := finished["command"].(map[string]any)
	args := stringsOf(command["args"])
	if len(args) < 5 || !equalStrings(args[len(args)-5:len(args)-2], tokens) {
		t.Errorf("the tokens are not before the input and output paths: %v", args)
	}
}

func TestArgumentsAreValidatedPerExecutableResettableAndSurviveARestart(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	_, before := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.toolchain", nil)

	for _, bad := range []map[string]any{
		{"executable": "tool", "arguments": []string{"{root.game_root}"}},
		{"executable": "tool", "arguments": []string{""}},
		{"executable": "tool", "arguments": []string{"-a\nb"}},
		{"executable": "vis", "arguments": []string{"-fast"}},
		{"executable": "", "arguments": []string{"-x"}},
	} {
		status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/arguments", bad)
		if status != http.StatusBadRequest || body["error"] == "" {
			t.Errorf("%v was answered %d: %v", bad, status, body["error"])
		}
	}

	spaced := filepath.Join(m.dir, "a folder with spaces", "extra.txt")
	status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/arguments",
		map[string]any{"executable": "tool", "arguments": []string{"-nopercent", spaced}})
	if status != http.StatusOK {
		t.Fatalf("saving = %d: %v", status, body["error"])
	}
	commands, _ := body["commands"].([]any)
	preview, _ := commands[0].(map[string]any)
	argv := stringsOf(preview["argv"])
	at := int(preview["custom_at"].(float64))
	if at <= 0 || at+1 >= len(argv) || argv[at] != "-nopercent" || argv[at+1] != spaced {
		t.Fatalf("the preview puts the tokens at %d of %v", at, argv)
	}
	if !strings.Contains(argv[len(argv)-2], "<source_map>") {
		t.Errorf("the tokens are not before the operands: %v", argv)
	}

	// The document is not changed; the setup is.
	_, after := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.toolchain", nil)
	if after["digest"] != before["digest"] {
		t.Errorf("saving arguments changed the profile document: %v -> %v", before["digest"], after["digest"])
	}

	// A restart reads them back from the binding.
	restarted := m.restart()
	_, again := restarted.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.toolchain", nil)
	saved := again["binding"].(map[string]any)["arguments"].(map[string]any)["tool"]
	if got := stringsOf(saved); !equalStrings(got, []string{"-nopercent", spaced}) {
		t.Errorf("after a restart the arguments are %v", got)
	}

	// Reset to default removes them.
	status, body = restarted.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/arguments",
		map[string]any{"executable": "tool", "reset": true})
	if status != http.StatusOK {
		t.Fatalf("reset = %d: %v", status, body["error"])
	}
	if arguments := body["binding"].(map[string]any)["arguments"]; arguments != nil {
		t.Errorf("after reset the binding holds %v", arguments)
	}
}

// Profiles and the Build & Run engine list give one answer, and it changes in
// both at once.
func TestProfilesAndTheEngineListAgreeOnReadiness(t *testing.T) {
	m := newMachine(t)
	self := mustExecutable(t)
	agree := func(label, id string) bool {
		t.Helper()
		_, engines := m.call(http.MethodGet, "/api/v1/engines", nil)
		listed := findItem(t, engines, "id", id)
		_, profile := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
		readiness, _ := profile["readiness"].(map[string]any)
		if readiness == nil {
			t.Fatalf("%s: the profile carries no readiness", label)
		}
		if listed["ready"] != readiness["ready"] {
			t.Errorf("%s: the engine list says ready=%v and Profiles says %v", label, listed["ready"], readiness["ready"])
		}
		if readiness["ready"] != true && len(readiness["problems"].([]any)) == 0 {
			t.Errorf("%s: not ready, and no problem is named", label)
		}
		ready, _ := readiness["ready"].(bool)
		return ready
	}

	// A built-in engine set up against an earlier version of its document —
	// vkQuake on the operator's machine — is ready when its paths are.
	const vkquake = "auto-pigeon.engine.vkquake"
	set, err := binding.LoadFile(m.bindings)
	if err != nil && !errorsIsNoFile(err) {
		t.Fatal(err)
	}
	if set == nil {
		set = binding.NewSet()
	}
	if err := set.Put(binding.LocalBinding{
		ProfileID: vkquake, ProfileVersion: "0.9.0",
		ProfileDigest: "sha256:" + strings.Repeat("1", 64), Trust: "builtin", Acquisition: "user_path",
		Executables: map[string]string{"engine": self},
		Roots:       map[string]string{"game_root": m.gameRoot},
	}); err != nil {
		t.Fatal(err)
	}
	if err := binding.SaveFile(m.bindings, set); err != nil {
		t.Fatal(err)
	}
	if !agree("stale digest, paths present", vkquake) {
		t.Error("a built-in engine whose program and game are there is called not ready")
	}
	// The binary goes missing: not ready, in both, with the reason.
	set, _ = binding.LoadFile(m.bindings)
	local, _ := set.Find(vkquake)
	local.Executables["engine"] = filepath.Join(m.dir, "moved", "vkquake")
	set.Put(local)
	binding.SaveFile(m.bindings, set)
	if agree("missing binary", vkquake) {
		t.Error("a missing engine is called ready")
	}
	_, profile := m.call(http.MethodGet, "/api/v1/profiles/"+vkquake, nil)
	problems := profile["readiness"].(map[string]any)["problems"].([]any)
	if summary, _ := problems[0].(map[string]any)["summary"].(string); !strings.Contains(summary, "moved") {
		t.Errorf("the missing requirement is not named: %v", problems)
	}

	// A user's own engine: approved and bound is ready; a withdrawn approval
	// and a changed path are not; both surfaces agree after a restart.
	const fixture = "aucom.fixture.q1-engine"
	_, document := m.call(http.MethodGet, "/api/v1/profiles/"+fixture, nil)
	m.call(http.MethodPost, "/api/v1/profiles/"+fixture+"/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": m.gameRoot, "content_root": m.content},
		"approve":     true, "digest": document["digest"],
	})
	if !agree("approved and bound", fixture) {
		t.Error("an approved, bound engine is not ready")
	}
	m.call(http.MethodPost, "/api/v1/profiles/"+fixture+"/withdraw", nil)
	if agree("approval withdrawn", fixture) {
		t.Error("an engine whose approval was withdrawn is ready")
	}
	m.call(http.MethodPost, "/api/v1/profiles/"+fixture+"/grant", map[string]any{"digest": document["digest"]})
	if !agree("approved again", fixture) {
		t.Error("approving again did not make it ready")
	}
	m = m.restart()
	if !agree("after a restart", fixture) {
		t.Error("after a restart the engine is not ready")
	}
}

// The auto-build of a hosted map, driven against the fixture AUB: baseline,
// one new revision → one build of THAT revision through the ordinary
// coordinator, repeated polls → nothing more.
func TestAutoBuildBuildsANewRevisionOnceThroughTheOrdinaryPipeline(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)

	status, body := m.call(http.MethodPost, "/api/v1/autobuild/"+m.backend.asset.assetID+"/enable",
		map[string]any{"pipeline": "aucom.fixture.pipeline", "display_name": "First Coast"})
	if status != http.StatusOK {
		t.Fatalf("switching it on = %d: %v", status, body["error"])
	}
	baseline, _ := body["baseline"].(map[string]any)
	if baseline["revision"] != float64(4) || body["running"] != nil {
		t.Fatalf("after switching on: %v", body)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/autobuild/"+m.backend.asset.assetID+"/enable",
		map[string]any{"pipeline": "no.such.pipeline"}); status != http.StatusBadRequest {
		t.Errorf("an unknown pipeline was accepted: %d %v", status, body)
	}

	// The map is saved again on the server.
	m.backend.mu.Lock()
	m.backend.asset.revisionID, m.backend.asset.revision = "rev-000005", 5
	m.backend.asset.body = []byte("{ \"classname\" \"worldspawn\" \"message\" \"revision five\" }\n")
	m.backend.mu.Unlock()

	service, err := m.server.autobuildService()
	if err != nil {
		t.Fatal(err)
	}
	due := func() {
		t.Helper()
		if _, err := autobuild.Update(service.Path(), func(state *autobuild.State) error {
			for i := range state.Entries {
				state.Entries[i].NextCheckAt = time.Time{}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := service.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	due()
	_, body = m.call(http.MethodGet, "/api/v1/autobuild/"+m.backend.asset.assetID, nil)
	running, _ := body["running"].(map[string]any)
	if running == nil {
		t.Fatalf("a new revision started no build: %v", body)
	}
	run := m.awaitPlay(t, running["run_id"].(string))
	if run["state"] != "succeeded" || run["build_only"] != true || run["launch"] != nil || run["installed"] != nil {
		t.Fatalf("the auto-build run: %v", run)
	}
	for i := 0; i < 3; i++ {
		due()
	}
	_, runs := m.call(http.MethodGet, "/api/v1/play/runs", nil)
	if items, _ := runs["items"].([]any); len(items) != 1 {
		t.Fatalf("repeated polls started %d runs, want exactly 1", len(items))
	}
	_, body = m.call(http.MethodGet, "/api/v1/autobuild/"+m.backend.asset.assetID, nil)
	built, _ := body["last_built"].(map[string]any)
	if built == nil || built["revision"].(map[string]any)["revision"] != float64(5) || built["job_id"] == "" {
		t.Fatalf("the result near the switch: %v", body)
	}

	// The pipeline received the downloaded revision 5: the compiled BSP is
	// the fixture compiler's copy of revision five's bytes.
	_, jobBody := m.call(http.MethodGet, "/api/v1/jobs/"+built["job_id"].(string), nil)
	artifacts, _ := jobBody["artifacts"].([]any)
	var bsp string
	for _, raw := range artifacts {
		artifact := raw.(map[string]any)
		if artifact["name"] == "bsp" {
			bsp, _ = artifact["path"].(string)
		}
	}
	contents, err := os.ReadFile(bsp)
	if err != nil || !strings.Contains(string(contents), "revision five") {
		t.Errorf("the build compiled %q (%v), not revision 5", contents, err)
	}

	// Switched off: no more checks.
	m.call(http.MethodPost, "/api/v1/autobuild/"+m.backend.asset.assetID+"/disable", nil)
	served := m.backend.count()
	due()
	if m.backend.count() != served {
		t.Error("AUB was asked about a map whose auto-build is off")
	}
}

// The Build & Run map list: newest saved first, a stable order for ties and
// for maps without a time, whatever order the pages arrived in.
func TestTheMapListIsNewestSavedFirst(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command("node", "testdata/mapsort.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// A running job's output is also refused for a job that does not exist, and a
// bad cursor is a 400, not a crash.
func TestTheOutputRouteRefusesWhatIsNotThere(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, _ := do(t, server, "GET", "/api/v1/jobs/20260101T000000Z-000000000000/output", "")
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("a missing job answered %d", response.StatusCode)
	}
	response, _ = do(t, server, "GET", "/api/v1/jobs/20260101T000000Z-000000000000/output?offset=x", "")
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("a bad offset answered %d", response.StatusCode)
	}
	if !job.ValidID("20260101T000000Z-000000000000") {
		t.Fatal("the fixture id is not a job id")
	}
}
