package web

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// NEW_506 (#11, item 1): a profile written from another one in the form kept
// that profile's long description — "release auto-pigeon-ericw-v1.4502,
// commit d4e4a75f…" — although its name, summary, version and programs said
// otherwise, because the form had no field for it. Every free-text member a
// starting point carries is now either a field of the form or travels with a
// field the form shows (the licence identifier), and a field the person
// empties is empty in the document.
func TestEveryInheritedFreeTextFieldIsShownOrCleared(t *testing.T) {
	m := newMachine(t)
	entries, err := builtin.Load()
	if err != nil {
		t.Fatal(err)
	}
	// The form is offered every one of them, for every tested profile.
	for _, entry := range entries {
		id := entry.Profile.Metadata().ID
		tree, err := profileTree(entry.Profile)
		if err != nil {
			t.Fatal(err)
		}
		status, body := m.call(http.MethodGet, "/api/v1/profiles/templates/"+id+"/scratch", nil)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %v", id, status, body["error"])
		}
		identity := body["identity"].(map[string]any)
		source, _ := tree["source"].(map[string]any)
		license, _ := tree["license"].(map[string]any)
		for field, want := range map[string]string{
			"description":       text(tree, "description"),
			"homepage":          text(source, "homepage"),
			"source_repository": text(source, "repository"),
			"release_notes":     text(source, "release_notes"),
			"license_url":       text(license, "url"),
			"license_notice":    text(license, "notice"),
			"license_source":    text(license, "corresponding_source"),
		} {
			if got, _ := identity[field].(string); got != want {
				t.Errorf("%s: the form is offered %s = %q, the profile says %q", id, field, got, want)
			}
		}
	}

	// A tool written from the tested EricW tool, its free text emptied.
	const base = "auto-pigeon.ericw-tools.auto-pigeon-build.q1"
	_, offered := m.call(http.MethodGet, "/api/v1/profiles/templates/"+base+"/scratch", nil)
	identity := offered["identity"].(map[string]any)
	if identity["description"] == "" || identity["release_notes"] == "" || identity["source_repository"] == "" {
		t.Fatalf("the tested tool no longer has the free text this test is about: %v", identity)
	}
	request := func(edit func(map[string]any)) map[string]any {
		fields := map[string]any{
			"name": "My EricW build", "version": "1.0.0", "summary": "My own build of the compilers.",
			"publisher_name": "Local", "license_spdx": identity["license_spdx"], "scratch": offered["scratch"],
		}
		if edit != nil {
			edit(fields)
		}
		status, composed := m.call(http.MethodPost, "/api/v1/profiles/compose", fields)
		if status != http.StatusOK || composed["valid"] != true {
			t.Fatalf("compose = %d, valid = %v: %v", status, composed["valid"], composed["error"])
		}
		// A copy under its own name is not the approved profile, and asks
		// for nothing more than it: the review must not say it does.
		diff, _ := composed["diff"].(map[string]any)
		if added, _ := diff["permissions_added"].([]any); diff["escalates"] != true || added == nil || len(added) != 0 {
			t.Errorf("a copy that changed only its text: escalates = %v, permissions_added = %v", diff["escalates"], diff["permissions_added"])
		}
		var document map[string]any
		encoded, _ := json.Marshal(composed["document"])
		_ = json.Unmarshal(encoded, &document)
		return document
	}
	emptied := request(nil)
	if description, present := emptied["description"]; present {
		t.Errorf("an emptied Description still carries the tested tool's: %q", description)
	}
	if source, present := emptied["source"]; present {
		t.Errorf("emptied Homepage, Source code and Release notes still carry the tested tool's: %v", source)
	}
	written := request(func(fields map[string]any) {
		fields["description"] = "Built from my branch."
		fields["release_notes"] = "https://example.invalid/my-build/notes"
	})
	if written["description"] != "Built from my branch." {
		t.Errorf("the typed description is %v", written["description"])
	}
	if source, _ := written["source"].(map[string]any); len(source) != 1 || source["release_notes"] != "https://example.invalid/my-build/notes" {
		t.Errorf("source = %v; want only the release notes that were typed", written["source"])
	}

	// The licence's own text travels with its identifier, which the form
	// shows — and the page names what came with it — and another identifier
	// drops all of it.
	license, _ := emptied["license"].(map[string]any)
	if identity["license_notice"] != "" && license["notice"] != identity["license_notice"] {
		t.Errorf("the same licence lost its notice: %v", license)
	}
	other := request(func(fields map[string]any) { fields["license_spdx"] = "MIT" })
	if license, _ := other["license"].(map[string]any); len(license) != 2 || license["spdx"] != "MIT" {
		t.Errorf("another licence kept the tested one's text: %v", license)
	}
}

// NEW_506 (#10): a stage switched to another tool keeps its wiring, its
// parameters and this machine's own arguments — in the composed document, in
// the exact commands the pipeline's page previews and in the run. Written
// over the installed pipeline (Replace), the new version's digest is not the
// approved one: the old approval does not carry, approving it with the old
// digest is refused, and no tool's approval or binding moves.
func TestASwitchedStageKeepsItsWiringInThePreviewAndTheRunAndIsApprovedAgain(t *testing.T) {
	m := newMachine(t)
	m.installThreeStageTools()
	m.approveAndBind("aucom.fixture.fork3")
	id, first := m.installComposed(wizardPipeline("Switched build", "1.0.0", "aucom.fixture.stock3"), false)
	tokens := []string{"--lines=1", "--note=a b"}
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/stage-arguments",
		map[string]any{"stage": "qbsp", "arguments": tokens}); status != http.StatusOK {
		t.Fatalf("saving stage arguments = %d: %v", status, body["error"])
	}
	m.grant(id)

	tools := func() string {
		var out []string
		for _, tool := range []string{"aucom.fixture.stock3", "aucom.fixture.fork3"} {
			_, page := m.call(http.MethodGet, "/api/v1/profiles/"+tool, nil)
			encoded, _ := json.Marshal(map[string]any{"id": tool, "digest": page["digest"], "authorized": page["authorized"], "binding": page["binding"], "trust": page["trust"]})
			out = append(out, string(encoded))
		}
		return strings.Join(out, "\n")
	}
	before := tools()

	// Reopened in the form and its compile stage given the fork, as the page
	// posts it once stageswitch.js has kept everything.
	_, reopened := m.call(http.MethodGet, "/api/v1/profiles/templates/"+id+"/scratch", nil)
	scratch := reopened["scratch"].(map[string]any)
	compile := scratch["steps"].([]any)[0].(map[string]any)
	compile["tool"] = "aucom.fixture.fork3"
	request := wizardPipeline("Switched build", "1.0.1", "")
	request["scratch"] = scratch
	again, second := m.installComposed(request, true)
	if again != id || second["digest"] == first["digest"] {
		t.Fatalf("1.0.1 installed as %s, digest %v (1.0.0: %v)", again, second["digest"], first["digest"])
	}
	var document map[string]any
	encoded, _ := json.Marshal(second["document"])
	_ = json.Unmarshal(encoded, &document)
	step := objects(document, "steps")[0]
	wiring, _ := json.Marshal(map[string]any{"tool": step["tool"], "inputs": step["inputs"], "options": step["options"]})
	if want := `{"inputs":[{"from":"pipeline.map","name":"source_map"}],"options":{"basename":"room","mode":"fast"},"tool":"aucom.fixture.fork3"}`; string(wiring) != want {
		t.Errorf("the switched stage in the composed document is\n %s\nwant\n %s", wiring, want)
	}

	// Trust and approval: local, not approved, the old digest refused.
	_, page := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
	if page["authorized"] != false || page["trust"] != "local" || page["digest"] != second["digest"] {
		t.Errorf("1.0.1: authorized = %v, trust = %v, digest = %v", page["authorized"], page["trust"], page["digest"])
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/grant", map[string]any{"digest": first["digest"]}); status == http.StatusOK {
		t.Error("1.0.1 was approved with 1.0.0's digest")
	}
	if _, page := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil); page["authorized"] != false {
		t.Error("a refused stale approval still approved the pipeline")
	}
	if after := tools(); after != before {
		t.Errorf("a tool's digest, approval, binding or trust moved:\n%s\n---\n%s", before, after)
	}

	// The page saves the form's stage arguments after installing.
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/stage-arguments",
		map[string]any{"stage": "qbsp", "arguments": reopened["stage_arguments"].(map[string]any)["qbsp"]}); status != http.StatusOK {
		t.Fatalf("saving the stage arguments again = %d: %v", status, body["error"])
	}
	m.grant(id)

	// The preview: the fork runs the compile stage, with its parameter, its
	// own arguments one word each, and the input wired to the map.
	_, commands := m.call(http.MethodGet, "/api/v1/profiles/"+id+"/commands", nil)
	items, _ := commands["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("commands = %v", commands)
	}
	preview := items[0].(map[string]any)
	if preview["tool"] != "aucom.fixture.fork3" || items[1].(map[string]any)["tool"] != "aucom.fixture.stock3" {
		t.Errorf("the compile stage's preview names another tool: %v", preview)
	}
	argv := stringsOf(preview["argv"])
	at := int(preview["custom_at"].(float64))
	if !equalStrings(stringsOf(preview["custom_args"]), tokens) || at+2 > len(argv) || !equalStrings(argv[at:at+2], tokens) {
		t.Errorf("the compile stage's own arguments in the preview: custom = %v at %d, argv = %q", preview["custom_args"], at, argv)
	}

	// The run: the fork, then the stock vis and light, each reading the one
	// before, and the manifest names the version and digest that ran.
	manifest, published := m.runPipeline(id)
	source, _ := os.ReadFile(m.writeSourceMap())
	if want := "BSP:BSP:BSP:" + string(source); published != want {
		t.Errorf("the switched pipeline's stages did not each read the one before: %q", published)
	}
	var ran []string
	for _, raw := range manifest["steps"].([]any) {
		ran = append(ran, raw.(map[string]any)["profile"].(map[string]any)["id"].(string))
	}
	if got := strings.Join(ran, " "); got != "aucom.fixture.fork3 aucom.fixture.stock3 aucom.fixture.stock3" {
		t.Errorf("the stages were run by %s", got)
	}
	if pipeline := manifest["pipeline"].(map[string]any); pipeline["version"] != "1.0.1" || pipeline["digest"] != second["digest"] {
		t.Errorf("the build says it ran %v %v", pipeline["version"], pipeline["digest"])
	}
}
