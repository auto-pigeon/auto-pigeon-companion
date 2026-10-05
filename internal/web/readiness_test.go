package web

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
)

func TestPipelineReadinessTracksRequiredProgramSetup(t *testing.T) {
	m := newMachine(t)
	check := func(want bool) {
		t.Helper()
		status, body := m.call(http.MethodGet, "/api/v1/build/pipelines", nil)
		if status != http.StatusOK {
			t.Fatalf("list: %d %v", status, body)
		}
		p := findItem(t, body, "id", "aucom.fixture.pipeline")
		ready := p["readiness"].(map[string]any)
		if ready["ready"] != want {
			t.Fatalf("pipeline readiness: %v", ready)
		}
		if !want && len(ready["problems"].([]any)) == 0 {
			t.Fatal("unavailable pipeline has no setup reason")
		}
	}
	check(false)
	status, body := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.toolchain", nil)
	if status != http.StatusOK {
		t.Fatal(body)
	}
	status, body = m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/grant", map[string]any{"digest": body["digest"]})
	if status != http.StatusOK {
		t.Fatal(body)
	}
	m.bindExecutable("aucom.fixture.toolchain", "tool", mustExecutable(t))
	check(true)
	m.bindExecutable("aucom.fixture.toolchain", "tool", filepath.Join(m.dir, "missing-program"))
	check(false)
	// A build tool's missing program is named as that program, not as an
	// engine, and its fix is not `engine bind` (NEW_310, live on qbsp).
	_, listed := m.call(http.MethodGet, "/api/v1/build/pipelines", nil)
	problem := findItem(t, listed, "id", "aucom.fixture.pipeline")["readiness"].(map[string]any)["problems"].([]any)[0].(map[string]any)
	if summary, _ := problem["summary"].(string); !strings.Contains(summary, `program "tool" recorded for`) || strings.Contains(summary, "engine recorded") {
		t.Fatalf("missing tool program summary: %v", problem)
	}
	if fix, _ := problem["fix"].(string); strings.Contains(fix, "engine bind") {
		t.Fatalf("missing tool program fix names the engine command: %v", problem)
	}
	// Naming an unavailable id directly is refused before anything starts. The
	// route is the one the Build page posts to: an earlier version of this
	// check posted to a route that does not exist, and its 405 passed.
	status, body = m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{"pipeline": "aucom.fixture.pipeline", "inputs": map[string]string{"source_map": m.writeSourceMap()}})
	if status != http.StatusConflict || body["class"] != "pipeline_not_ready" || body["readiness"] == nil {
		t.Fatalf("crafted unavailable start: %d %v", status, body)
	}
	m.bindExecutable("aucom.fixture.toolchain", "tool", mustExecutable(t))
	check(true)
}

func TestQ3ReadinessIsPerAction(t *testing.T) {
	m := newMachine(t)
	m.bindQ3Engine(m.gameRoot)
	var document map[string]any
	if err := json.Unmarshal(enginefixture.ProfileQ3JSON, &document); err != nil {
		t.Fatal(err)
	}
	executables := document["executables"].([]any)
	document["executables"] = append(executables, map[string]any{"name": "dedicated", "title": "Dedicated program", "file": "dedicated{platform.exe_suffix}"})
	for _, raw := range document["actions"].([]any) {
		action := raw.(map[string]any)
		if action["id"] == "host_dedicated" {
			action["executable"] = "dedicated"
		}
	}
	m.writeProfile("aucom.fixture.q3-engine.json", encodeFixture(document))
	// Portable edits invalidate the old approval. Approve the new exact digest.
	_, detail := m.call(http.MethodGet, "/api/v1/profiles/"+enginefixture.ProfileQ3ID, nil)
	status, body := m.call(http.MethodPost, "/api/v1/profiles/"+enginefixture.ProfileQ3ID+"/grant", map[string]any{"digest": detail["digest"]})
	if status != http.StatusOK {
		t.Fatal(body)
	}
	status, body = m.call(http.MethodGet, "/api/v1/q3/engines", nil)
	if status != http.StatusOK {
		t.Fatal(body)
	}
	for _, raw := range body["engines"].([]any) {
		e := raw.(map[string]any)
		if e["id"] != enginefixture.ProfileQ3ID {
			continue
		}
		if e["set_up"] != true {
			t.Fatalf("a playable client was disabled by its unbound dedicated server: %v", e)
		}
		for _, raw := range e["actions"].([]any) {
			a := raw.(map[string]any)
			if a["id"] == "host_dedicated" && (a["ready"] != false || a["problem"] == nil) {
				t.Fatalf("unbound dedicated action: %v", a)
			}
			if a["id"] == "play_map" && a["ready"] != true {
				t.Fatalf("client action: %v", a)
			}
		}
		return
	}
	t.Fatal("fixture engine not listed")
}
