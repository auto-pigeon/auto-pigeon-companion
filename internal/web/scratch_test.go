package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ericwScratch is the operator's own acceptance case for NEW_244D, written as
// a person fills the from-scratch form: three EricW programs, three actions
// with arguments, inputs, outputs and options, and nothing borrowed from the
// built-in ericw-tools profile.
func ericwScratchTool() map[string]any {
	return map[string]any{
		"id": "local.my-ericw", "name": "My EricW", "version": "1.0.0",
		"summary":        "qbsp, vis and light, described by hand.",
		"publisher_name": "Me", "license_spdx": "GPL-3.0-or-later",
		"scratch": map[string]any{
			"kind": "tool", "tool_version": "0.18.1",
			"executables": []map[string]any{
				{"name": "qbsp", "title": "Map compiler", "file": "bin/qbsp"},
				{"name": "vis", "title": "Visibility", "file": "bin/vis"},
				{"name": "light", "title": "Lighting", "file": "bin/light"},
			},
			"actions": []map[string]any{
				{
					"id": "bsp", "title": "Compile", "capability": "my.bsp", "executable": "qbsp",
					"args":  []string{"-wadpath", "{root.content_root}", "{input.map}", "{output.bsp}"},
					"roots": []map[string]any{{"role": "content_root", "access": "read", "purpose": "find the WADs the map names"}},
					"inputs": []map[string]any{
						{"name": "map", "title": "Map", "role": "my.map", "required": true, "extensions": []string{".map"}},
					},
					"outputs": []map[string]any{
						{"name": "bsp", "title": "BSP", "role": "my.bsp", "path": "{option.name}.bsp"},
						{"name": "prt", "title": "Portals", "role": "my.prt", "path": "{option.name}.prt", "optional": true},
					},
					"options": []map[string]any{{"name": "name", "type": "text", "default": "level"}},
				},
				{
					"id": "vis", "title": "Visibility", "capability": "my.vis", "executable": "vis",
					"args": []string{"-fast [if fast]", "{input.bsp}"},
					"inputs": []map[string]any{
						{"name": "bsp", "role": "my.bsp", "required": true, "extensions": []string{".bsp"}},
						{"name": "prt", "role": "my.prt", "required": true, "extensions": []string{".prt"}, "stage_with": "bsp"},
					},
					"outputs": []map[string]any{{"name": "bsp", "role": "my.bsp.vised", "in_place": "bsp"}},
					"options": []map[string]any{{"name": "fast", "type": "bool", "default": "false"}},
				},
			},
		},
	}
}

func TestAToolWrittenFromScratchComposesAndValidates(t *testing.T) {
	m := newMachine(t)
	status, body := m.call(http.MethodPost, "/api/v1/profiles/compose", ericwScratchTool())
	if status != http.StatusOK || body["valid"] != true {
		encoded, _ := json.MarshalIndent(body, "", " ")
		t.Fatalf("status = %d\n%s", status, encoded)
	}
}

// TestAProfileWrittenWithoutAnIdIsNamedAfterItsName: the page asks nobody for
// an id (NEW_244D), so one is derived from the name — and a copy of a built-in
// template never keeps the built-in's identity.
func TestAProfileWrittenWithoutAnIdIsNamedAfterItsName(t *testing.T) {
	m := newMachine(t)
	request := ericwScratchTool()
	delete(request, "id")
	request["name"] = "My EricW (Quake 1)"
	status, body := m.call(http.MethodPost, "/api/v1/profiles/compose", request)
	if status != http.StatusOK || body["valid"] != true || body["id"] != "local.tool.my-ericw-quake-1" {
		t.Fatalf("status = %d, valid = %v, id = %v, error = %v", status, body["valid"], body["id"], body["error"])
	}

	// Renaming before installing follows the name; an id the author wrote into
	// the document under their own namespace is left alone.
	var tree map[string]any
	encoded, _ := json.Marshal(body["document"])
	_ = json.Unmarshal(encoded, &tree)
	_, renamed := m.call(http.MethodPost, "/api/v1/profiles/compose", map[string]any{"document": tree, "name": "Renamed"})
	if renamed["id"] != "local.tool.renamed" {
		t.Errorf("renamed id = %v", renamed["id"])
	}
	tree["id"] = "me.tools.kept"
	_, kept := m.call(http.MethodPost, "/api/v1/profiles/compose", map[string]any{"document": tree, "name": "Renamed again"})
	if kept["id"] != "me.tools.kept" {
		t.Errorf("an authored id was replaced: %v", kept["id"])
	}

	_, templates := m.call(http.MethodGet, "/api/v1/profiles/templates?kind=engine", nil)
	list, _ := templates["items"].([]any)
	if len(list) == 0 {
		t.Fatal("no templates")
	}
	first, _ := list[0].(map[string]any)
	_, copied := m.call(http.MethodPost, "/api/v1/profiles/compose", map[string]any{"template": first["id"], "name": "My copy"})
	if id, _ := copied["id"].(string); id == first["id"] || !strings.HasPrefix(id, "local.") || !strings.HasSuffix(id, ".my-copy") {
		t.Errorf("a named copy of %v is %v", first["id"], copied["id"])
	}
}

// TestAScratchToolAndPipelineBuildAMap walks the whole route a person takes
// through the page, over the API the page uses: compose a tool from scratch,
// install it, approve it, bind its program by FOLDER, compose a pipeline from
// scratch whose stage sets that tool's parameter, install and approve it, and
// build. The program is this test binary playing a compiler, so the executor,
// the staging and the manifest are the real ones.
func TestAScratchToolAndPipelineBuildAMap(t *testing.T) {
	m := newMachine(t)
	self := mustExecutable(t)
	folder := filepath.Join(m.dir, "my tools")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(folder, "fakecompiler")); err != nil {
		t.Fatal(err)
	}

	install := func(request map[string]any) string {
		t.Helper()
		status, composed := m.call(http.MethodPost, "/api/v1/profiles/compose", request)
		if status != http.StatusOK || composed["valid"] != true {
			t.Fatalf("compose = %d: %v", status, composed["error"])
		}
		id, _ := composed["id"].(string)
		status, imported := m.call(http.MethodPost, "/api/v1/profiles/import",
			map[string]any{"document": composed["document"]})
		if status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("import %s = %d: %v", id, status, imported["error"])
		}
		status, detail := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
		if detail["trust"] != "local" || detail["authorized"] != false {
			t.Fatalf("a document written here arrived as %v / authorized %v", detail["trust"], detail["authorized"])
		}
		status, granted := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/grant",
			map[string]any{"digest": detail["digest"]})
		if status != http.StatusOK {
			t.Fatalf("grant %s = %d: %v", id, status, granted["error"])
		}
		return id
	}

	toolID := install(map[string]any{
		"id": "local.scratch-compiler", "name": "Scratch compiler", "version": "1.0.0",
		"summary": "A compiler described from nothing.", "publisher_name": "Me", "license_spdx": "MIT",
		"scratch": map[string]any{
			"kind": "tool", "tool_version": "1",
			"executables": []map[string]any{{"name": "cc", "title": "The compiler", "file": "fakecompiler"}},
			"actions": []map[string]any{{
				"id": "compile", "title": "Compile", "capability": "scratch.compile", "executable": "cc",
				"args":    []string{buildHelperFlag, "--fail [if fail]", "{input.source}", "{output.out}"},
				"inputs":  []map[string]any{{"name": "source", "role": "scratch.map", "required": true, "extensions": []string{"map"}}},
				"outputs": []map[string]any{{"name": "out", "role": "scratch.bsp", "path": "{option.name}.bsp"}},
				"options": []map[string]any{
					{"name": "name", "type": "text", "default": "level"},
					{"name": "fail", "type": "bool", "default": "false"},
				},
			}},
		},
	})
	status, bound := m.call(http.MethodPost, "/api/v1/profiles/"+toolID+"/bind", map[string]any{"folder": folder})
	if status != http.StatusOK {
		t.Fatalf("binding the folder = %d: %v", status, bound["error"])
	}

	pipelineID := install(map[string]any{
		"id": "local.scratch-pipeline", "name": "Scratch pipeline", "version": "1.0.0",
		"summary": "One stage, with a parameter.", "publisher_name": "Me", "license_spdx": "MIT",
		"scratch": map[string]any{
			"kind":   "pipeline",
			"inputs": []map[string]any{{"name": "map", "title": "Map", "role": "scratch.map", "required": true, "extensions": []string{".map"}}},
			"steps": []map[string]any{{
				"id": "compile", "title": "Compile it", "capability": "scratch.compile",
				"inputs":  map[string]string{"source": "pipeline.map"},
				"options": map[string]string{"name": "custom"},
			}},
			"outputs": []map[string]any{{"name": "bsp", "title": "The BSP", "role": "scratch.bsp", "from": "compile.out"}},
		},
	})

	source := filepath.Join(m.dir, "a map with spaces.map")
	writeFixtureFile(t, source, "{ \"classname\" \"worldspawn\" }\n")
	status, started := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": pipelineID, "inputs": map[string]string{"map": source}, "label": "scratch",
	})
	if status != http.StatusAccepted {
		t.Fatalf("starting the build = %d: %v", status, started["error"])
	}
	id, _ := started["build"].(string)
	deadline := time.Now().Add(60 * time.Second)
	var manifest map[string]any
	for time.Now().Before(deadline) {
		_, run := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
		manifest, _ = run["manifest"].(map[string]any)
		if run["live"] == false {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if manifest["state"] != "succeeded" {
		t.Fatalf("the scratch build ended %v: %v", manifest["state"], manifest["error"])
	}
	steps, _ := manifest["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	command, _ := step["command"].(map[string]any)
	if shell, _ := command["shell"].(string); !strings.Contains(shell, "custom.bsp") {
		t.Errorf("the pipeline's parameter did not reach the command: %q", shell)
	}
}

func TestAScratchArgumentCanBeConditionedOnAnOptionalFolder(t *testing.T) {
	arg, err := scratchArg("-wadpath [if folder content_root]")
	if err != nil {
		t.Fatal(err)
	}
	object, _ := arg.(map[string]any)
	when, _ := object["when"].(map[string]any)
	if object["value"] != "-wadpath" || when["root"] != "content_root" {
		t.Errorf("arg = %#v", arg)
	}
}
