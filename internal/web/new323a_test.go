package web

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// NEW_323A: a pipeline stage's tool can be changed without emptying the stage,
// and a document is not ready because it parses.
//
// The comparison itself is the page's (assets/stageswitch.js) and its table is
// testdata/stageswitch.check.mjs, run against the shipped files. What is here
// is everything after the page: the documents a wizard produces go through the
// real compose, import, approval, readiness and build routes of the fixture
// machine, with the fixture compiler really started three times.

func TestChangingAStagesToolIsAComparisonNotAReset(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable; the stage-switch table was not run")
	}
	out, err := exec.Command("node", "testdata/stageswitch.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// threeStageTool is a toolchain with a compiler, a visibility pass and a
// lighting pass, each the fixture program: every stage reads the one before
// and prefixes what it read, so the final file says how many stages ran and in
// what order.
func threeStageTool(id, name string) []byte {
	var tool map[string]any
	if err := json.Unmarshal(fixtureToolJSON(""), &tool); err != nil {
		panic(err)
	}
	tool["id"], tool["name"] = id, name
	tool["capabilities"] = []map[string]any{
		{"id": "fixture3.compile", "title": "Compile", "consumes": []string{"q1.map.source"}, "produces": []string{"q1.bsp"}},
		{"id": "fixture3.vis", "title": "Visibility", "consumes": []string{"q1.bsp"}, "produces": []string{"q1.bsp"}},
		{"id": "fixture3.light", "title": "Light", "consumes": []string{"q1.bsp"}, "produces": []string{"q1.bsp"}},
	}
	roots := []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "compile"}}
	stage := func(id, capability, input, role, extension, suffix string, options []map[string]any) map[string]any {
		return map[string]any{
			"id": id, "title": strings.ToUpper(id[:1]) + id[1:], "capability": capability, "executable": "tool",
			"args":        []any{buildHelperFlag, "{input." + input + "}", "{output.bsp}"},
			"working_dir": map[string]any{"root": "workspace"},
			"inputs":      []map[string]any{{"name": input, "title": "In", "role": role, "required": true, "extensions": []string{extension}}},
			"outputs":     []map[string]any{{"name": "bsp", "title": "BSP", "role": "q1.bsp", "path": "{option.basename}" + suffix + ".bsp"}},
			"options": append([]map[string]any{
				{"name": "basename", "title": "Name", "type": "text", "default": "level", "max_length": 64}}, options...),
			"roots": roots, "timeout_seconds": 120,
		}
	}
	tool["actions"] = []map[string]any{
		stage("compile", "fixture3.compile", "source_map", "q1.map.source", ".map", "", []map[string]any{
			{"name": "mode", "title": "Mode", "type": "enum", "default": "full", "values": []map[string]any{{"value": "full"}, {"value": "fast"}}},
		}),
		stage("vis", "fixture3.vis", "bsp", "q1.bsp", ".bsp", ".vis", []map[string]any{
			{"name": "level", "title": "Level", "type": "integer", "default": "4", "minimum": 0, "maximum": 4},
		}),
		stage("light", "fixture3.light", "bsp", "q1.bsp", ".bsp", ".lit", nil),
	}
	return encodeFixture(tool)
}

// installThreeStageTools puts two builds of the same toolchain on the machine.
// The first is approved and bound; the second is only installed, which is the
// state a tool is in the moment its profile has been written.
func (m *machine) installThreeStageTools() {
	m.t.Helper()
	m.writeProfile("aucom.fixture.stock3.json", threeStageTool("aucom.fixture.stock3", "Stock toolchain"))
	m.writeProfile("aucom.fixture.fork3.json", threeStageTool("aucom.fixture.fork3", "Fork toolchain"))
	m.approveAndBind("aucom.fixture.stock3")
}

func (m *machine) approveAndBind(id string) {
	m.t.Helper()
	_, body := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
	status, body := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/bind", map[string]any{
		"executables": map[string]string{"tool": mustExecutable(m.t)}, "approve": true, "digest": body["digest"],
	})
	if status != http.StatusOK {
		m.t.Fatalf("binding %s = %d: %v", id, status, body["error"])
	}
}

// wizardPipeline is what the New profile form posts for a three-stage
// pipeline: fields, not a document.
func wizardPipeline(name, version, tool string) map[string]any {
	return map[string]any{
		"name": name, "version": version, "summary": "Compile, vis and light.",
		"publisher_name": "Local", "license_spdx": "NOASSERTION",
		"scratch": map[string]any{
			"kind": "pipeline", "game_family": "quake1",
			"inputs": []map[string]any{{"name": "map", "title": "Map", "role": "q1.map.source", "required": true, "extensions": []string{".map"}}},
			"steps": []map[string]any{
				{"id": "qbsp", "title": "Compile", "capability": "fixture3.compile", "tool": tool,
					"inputs": map[string]string{"source_map": "pipeline.map"}, "options": map[string]string{"mode": "fast", "basename": "room"}},
				{"id": "vis", "title": "Visibility", "capability": "fixture3.vis", "tool": "aucom.fixture.stock3",
					"inputs": map[string]string{"bsp": "qbsp.bsp"}, "options": map[string]string{"level": "2", "basename": "room"}},
				{"id": "light", "title": "Light", "capability": "fixture3.light", "tool": "aucom.fixture.stock3",
					"inputs": map[string]string{"bsp": "vis.bsp"}, "options": map[string]string{"basename": "room"}},
			},
			"outputs": []map[string]any{{"name": "bsp", "title": "BSP", "role": "q1.bsp", "from": "light.bsp"}},
		},
	}
}

// installComposed sends a form through compose and import, as the page does.
func (m *machine) installComposed(request map[string]any, replace bool) (id string, composed map[string]any) {
	m.t.Helper()
	status, composed := m.call(http.MethodPost, "/api/v1/profiles/compose", request)
	if status != http.StatusOK || composed["valid"] != true {
		m.t.Fatalf("compose = %d, valid = %v: %v", status, composed["valid"], composed["error"])
	}
	status, body := m.call(http.MethodPost, "/api/v1/profiles/import", map[string]any{"document": composed["document"], "replace": replace})
	if status != http.StatusCreated {
		m.t.Fatalf("import = %d: %v", status, body["error"])
	}
	return body["id"].(string), composed
}

func (m *machine) grant(id string) {
	m.t.Helper()
	_, body := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
	if status, granted := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/grant", map[string]any{"digest": body["digest"]}); status != http.StatusOK {
		m.t.Fatalf("approving %s = %d: %v", id, status, granted["error"])
	}
}

func (m *machine) pipelineReadiness(id string) (bool, string) {
	m.t.Helper()
	_, body := m.call(http.MethodGet, "/api/v1/build/pipelines", nil)
	item := findItem(m.t, body, "id", id)
	readiness, _ := item["readiness"].(map[string]any)
	encoded, _ := json.Marshal(readiness["problems"])
	return readiness["ready"] == true, string(encoded)
}

// runPipeline builds a local map with a pipeline and returns the finished
// manifest and the bytes of its published BSP.
func (m *machine) runPipeline(id string) (map[string]any, string) {
	m.t.Helper()
	status, started := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": id, "inputs": map[string]string{"map": m.writeSourceMap()},
	})
	if status != http.StatusAccepted {
		m.t.Fatalf("starting %s = %d: %v", id, status, started["error"])
	}
	manifest := m.waitForBuild(started["build"].(string))
	var published string
	for _, raw := range manifest["outputs"].([]any) {
		output := raw.(map[string]any)
		if output["name"] == "bsp" {
			path, _ := output["path"].(string)
			if !filepath.IsAbs(path) {
				path = filepath.Join(m.builds, started["build"].(string), path)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				m.t.Fatalf("the published BSP: %v", err)
			}
			published = string(contents)
		}
	}
	return manifest, published
}

// The journey NEW_323 could not finish without rebuilding a pipeline by hand:
// write a pipeline in the form, install it, open it again, change the tool of
// its first stage to another build of the same compiler, install that as its
// own profile, and build with it.
func TestAPipelineReopenedWithAnotherToolKeepsItsWiringAndBuilds(t *testing.T) {
	m := newMachine(t)
	m.installThreeStageTools()

	original, composed := m.installComposed(wizardPipeline("Three stage build", "1.0.0", "aucom.fixture.stock3"), false)
	// Installed is not approved, and the review said so. Whether it can build
	// is the build list's answer, and the review gave the same one.
	setup := composed["setup"].(map[string]any)
	listed, _ := m.pipelineReadiness(original)
	if setup["approved"] != false || setup["ready"] != listed {
		t.Fatalf("the review said setup = %v; the build list says ready = %v", setup, listed)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/"+original+"/stage-arguments",
		map[string]any{"stage": "qbsp", "arguments": []string{"--lines=1", "--note=$(touch pwned); rm -rf ."}}); status != http.StatusOK {
		t.Fatalf("saving stage arguments = %d: %v", status, body["error"])
	}
	m.grant(original)
	if ready, why := m.pipelineReadiness(original); !ready {
		t.Fatalf("the approved pipeline over a bound tool is not ready: %s", why)
	}

	// Open it again in the form: it is offered as a starting point, and every
	// field comes back — wiring, parameters and this machine's own arguments.
	_, templates := m.call(http.MethodGet, "/api/v1/profiles/templates?kind=pipeline", nil)
	if listed := findItem(t, templates, "id", original); listed["installed"] != true {
		t.Fatalf("the installed pipeline is not offered as an installed starting point: %v", listed)
	}
	status, reopened := m.call(http.MethodGet, "/api/v1/profiles/templates/"+original+"/scratch", nil)
	if status != http.StatusOK {
		t.Fatalf("reopening = %d: %v", status, reopened["error"])
	}
	scratch := reopened["scratch"].(map[string]any)
	steps := scratch["steps"].([]any)
	first := steps[0].(map[string]any)
	encoded, _ := json.Marshal(steps)
	for _, want := range []string{`"source_map":"pipeline.map"`, `"mode":"fast"`, `"bsp":"qbsp.bsp"`, `"level":"2"`, `"bsp":"vis.bsp"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("the reopened form lost %s: %s", want, encoded)
		}
	}
	arguments, _ := json.Marshal(reopened["stage_arguments"])
	if string(arguments) != `{"qbsp":["--lines=1","--note=$(touch pwned); rm -rf ."]}` {
		t.Errorf("the reopened form's stage arguments = %s", arguments)
	}

	// The tool change, as the page posts it after stageswitch.js kept
	// everything: the one field that differs is the tool.
	first["tool"] = "aucom.fixture.fork3"
	clone, _ := m.installComposed(map[string]any{
		"name": "Three stage build with the fork", "version": "1.0.0", "summary": "Compile with the fork, then vis and light.",
		"publisher_name": "Local", "license_spdx": "NOASSERTION", "scratch": scratch,
	}, false)
	if clone == original {
		t.Fatalf("a profile under another name took the original's id %s", original)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/"+clone+"/stage-arguments",
		map[string]any{"stage": "qbsp", "arguments": reopened["stage_arguments"].(map[string]any)["qbsp"]}); status != http.StatusOK {
		t.Fatalf("saving the clone's stage arguments = %d: %v", status, body["error"])
	}
	m.grant(clone)

	// Approved, valid, wired — and NOT ready: the fork's program was never
	// chosen. The same answer in the Build list and on the profile's page.
	if ready, why := m.pipelineReadiness(clone); ready || !strings.Contains(why, "Compile") {
		t.Fatalf("a pipeline over an unbound tool: ready = %v, problems = %s", ready, why)
	}
	_, page := m.call(http.MethodGet, "/api/v1/profiles/"+clone, nil)
	if page["readiness"].(map[string]any)["ready"] != false {
		t.Error("Profiles calls the pipeline ready while Build & Run does not")
	}
	if status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": clone, "inputs": map[string]string{"map": m.writeSourceMap()}}); status == http.StatusAccepted {
		t.Fatalf("a build started over a tool that is not set up: %v", body)
	}
	m.approveAndBind("aucom.fixture.fork3")
	if ready, why := m.pipelineReadiness(clone); !ready {
		t.Fatalf("after the fork is approved and bound the pipeline is still not ready: %s", why)
	}

	// Build with the clone, and with the original as the control.
	manifest, published := m.runPipeline(clone)
	control, controlPublished := m.runPipeline(original)
	source, _ := os.ReadFile(m.writeSourceMap())
	if want := "BSP:BSP:BSP:" + string(source); published != want || controlPublished != want {
		t.Errorf("three stages did not each read the one before:\n clone   %q\n control %q", published, controlPublished)
	}
	tools := func(manifest map[string]any) []string {
		var out []string
		for _, raw := range manifest["steps"].([]any) {
			step := raw.(map[string]any)
			if step["state"] != "succeeded" {
				t.Errorf("stage %v ended %v", step["id"], step["state"])
			}
			out = append(out, step["profile"].(map[string]any)["id"].(string))
		}
		return out
	}
	if got := strings.Join(tools(manifest), " "); got != "aucom.fixture.fork3 aucom.fixture.stock3 aucom.fixture.stock3" {
		t.Errorf("the clone's stages were run by %s", got)
	}
	if got := strings.Join(tools(control), " "); got != "aucom.fixture.stock3 aucom.fixture.stock3 aucom.fixture.stock3" {
		t.Errorf("the original's stages were run by %s", got)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "pwned")); err == nil {
		t.Error("a stage argument was run as a command")
	}
}

// What the old Tool choice produced — a stage with nothing wired — is not a
// valid profile on a machine that has the tool, in the form or from a request
// that never saw the form, and a refusal installs nothing.
func TestAnEmptiedStageIsRefusedAndNothingIsInstalled(t *testing.T) {
	m := newMachine(t)
	m.installThreeStageTools()
	before, _ := os.ReadDir(m.profiles)

	for name, edit := range map[string]func(step map[string]any){
		"a stage with nothing wired": func(step map[string]any) { step["inputs"] = map[string]string{} },
		"an input the tool does not declare": func(step map[string]any) {
			step["inputs"] = map[string]string{"source_map": "pipeline.map", "wad": "pipeline.map"}
		},
		"a parameter the tool does not have":   func(step map[string]any) { step["options"] = map[string]string{"threads": "8"} },
		"an enum value the tool does not have": func(step map[string]any) { step["options"] = map[string]string{"mode": "final"} },
		"a shell expression as a parameter":    func(step map[string]any) { step["options"] = map[string]string{"basename": "$(id)"} },
	} {
		request := wizardPipeline("Refused "+name, "1.0.0", "aucom.fixture.stock3")
		edit(request["scratch"].(map[string]any)["steps"].([]map[string]any)[0])
		status, composed := m.call(http.MethodPost, "/api/v1/profiles/compose", request)
		if status != http.StatusOK || composed["valid"] != false || composed["error"] == "" {
			t.Errorf("%s: compose = %d, valid = %v", name, status, composed["valid"])
			continue
		}
		if _, has := composed["setup"]; has {
			t.Errorf("%s: a refused document was given a setup answer", name)
		}
		// The document compose built, sent straight to import.
		status, body := m.call(http.MethodPost, "/api/v1/profiles/import", map[string]any{"document": composed["document"]})
		if status != http.StatusUnprocessableEntity {
			t.Errorf("%s: import = %d: %v", name, status, body)
		}
	}
	after, _ := os.ReadDir(m.profiles)
	if len(after) != len(before) {
		t.Errorf("a refusal left a file behind: %d profile files before, %d after", len(before), len(after))
	}

	// A stage whose tool is NOT installed is a different thing: nothing can be
	// said about its wiring, so it installs — and is not ready, by name.
	request := wizardPipeline("Waiting for a tool", "1.0.0", "aucom.fixture.absent")
	id, composed := m.installComposed(request, false)
	setup := composed["setup"].(map[string]any)
	problems, _ := json.Marshal(setup["problems"])
	if setup["ready"] != false || !strings.Contains(string(problems), "aucom.fixture.absent") {
		t.Errorf("a pipeline over a tool that is not installed: setup = %v", setup)
	}
	m.grant(id)
	if ready, why := m.pipelineReadiness(id); ready || !strings.Contains(why, "aucom.fixture.absent") {
		t.Errorf("ready = %v, problems = %s", ready, why)
	}
}

// A new version of a pipeline is a new document: the build that ran with the
// old one still says the old one, the same version cannot be rewritten, and no
// other profile's binding moves.
func TestANewPipelineVersionLeavesOldBuildsAndOtherBindingsAlone(t *testing.T) {
	m := newMachine(t)
	m.installThreeStageTools()
	m.approveAndBind("aucom.fixture.fork3")
	id, first := m.installComposed(wizardPipeline("Versioned build", "1.0.0", "aucom.fixture.stock3"), false)
	m.grant(id)
	old, _ := m.runPipeline(id)

	others := func() string {
		_, body := m.call(http.MethodGet, "/api/v1/profiles", nil)
		var out []string
		for _, raw := range body["items"].([]any) {
			item := raw.(map[string]any)
			if item["id"] == id {
				continue
			}
			encoded, _ := json.Marshal(map[string]any{"id": item["id"], "digest": item["digest"], "binding": item["binding"], "authorized": item["authorized"]})
			out = append(out, string(encoded))
		}
		return strings.Join(out, "\n")
	}
	before := others()

	// The same version with another tool is refused, with or without Replace.
	changed := wizardPipeline("Versioned build", "1.0.0", "aucom.fixture.fork3")
	_, composed := m.call(http.MethodPost, "/api/v1/profiles/compose", changed)
	for _, replace := range []bool{false, true} {
		if status, _ := m.call(http.MethodPost, "/api/v1/profiles/import", map[string]any{"document": composed["document"], "replace": replace}); status != http.StatusConflict {
			t.Errorf("rewriting 1.0.0 with replace = %v: status %d, want 409", replace, status)
		}
	}
	// 1.0.1 replaces it, and needs its own approval.
	again, second := m.installComposed(wizardPipeline("Versioned build", "1.0.1", "aucom.fixture.fork3"), true)
	if again != id || second["digest"] == first["digest"] {
		t.Fatalf("1.0.1 installed as %s with digest %v", again, second["digest"])
	}
	if _, page := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil); page["authorized"] != false || page["version"] != "1.0.1" {
		t.Errorf("1.0.1 carries 1.0.0's approval: authorized = %v, version = %v", page["authorized"], page["version"])
	}
	if after := others(); after != before {
		t.Errorf("another profile's digest, binding or approval moved:\n%s\n---\n%s", before, after)
	}
	_, kept := m.call(http.MethodGet, "/api/v1/build/runs/"+old["build_id"].(string), nil)
	manifest := kept["manifest"].(map[string]any)
	pipeline := manifest["pipeline"].(map[string]any)
	if pipeline["version"] != "1.0.0" || pipeline["digest"] != first["digest"] {
		t.Errorf("the old build now says it ran %v %v", pipeline["version"], pipeline["digest"])
	}
	if step := manifest["steps"].([]any)[0].(map[string]any); step["profile"].(map[string]any)["id"] != "aucom.fixture.stock3" {
		t.Errorf("the old build's first stage now names %v", step["profile"])
	}
}

// A tool written in the form is not ready when installed, or when approved:
// it is ready when its program is somewhere that exists.
func TestAToolIsReadyOnlyWhenItsProgramsAre(t *testing.T) {
	m := newMachine(t)
	id, composed := m.installComposed(ericwScratchTool(), false)
	setup := composed["setup"].(map[string]any)
	if setup["ready"] != false || setup["approved"] != false {
		t.Fatalf("the review of a tool nobody set up said %v", setup)
	}
	ready := func() (bool, string) {
		_, body := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
		readiness := body["readiness"].(map[string]any)
		encoded, _ := json.Marshal(readiness["problems"])
		return readiness["ready"] == true, string(encoded)
	}
	_, body := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
	digest := body["digest"]
	m.grant(id)
	if is, why := ready(); is {
		t.Fatalf("approved with no program chosen, and called ready: %s", why)
	}
	// A program that is not there is refused by name, and nothing is recorded.
	missing := filepath.Join(m.dir, "no such folder", "qbsp")
	status, refused := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/bind", map[string]any{
		"executables": map[string]string{"qbsp": missing, "vis": missing, "light": missing}, "digest": digest})
	if status == http.StatusOK {
		t.Fatalf("a program that does not exist was accepted: %v", refused)
	}
	if message, _ := refused["error"].(string); !strings.Contains(message, "no such folder") {
		t.Errorf("the refusal does not name the path: %q", message)
	}
	if is, _ := ready(); is {
		t.Error("ready after a refused path")
	}
	// The same form again with a real program: now it is.
	program := mustExecutable(t)
	status, bound := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/bind", map[string]any{
		"executables": map[string]string{"qbsp": program, "vis": program, "light": program}, "digest": digest})
	if status != http.StatusOK {
		t.Fatalf("binding a real program = %d: %v", status, bound["error"])
	}
	if is, why := ready(); !is {
		t.Errorf("approved and bound, and not ready: %s", why)
	}
}
