package web

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
)

// NEW_330 (Q2_020 finding 2, confirmed at 4cca06b): Build & Run filtered
// engines by family only for a Quake II build, in the page — so a Quake 1
// pipeline offered Yamagi as "Ready to start" and a Quake III pipeline offered
// vkQuake and Yamagi. Which engine can play what a build profile makes is the
// two documents' own word (`game_profile.engine_family`), decided once on the
// server: the pipeline listing carries it and a run that disagrees is refused.
func TestAnEngineFitsOnlyAPipelineOfItsOwnFamily(t *testing.T) {
	m := newMachine(t)
	m.writeProfile(enginefixture.ProfileQ2ID+".json", enginefixture.ProfileQ2JSON)
	m.writeProfile(enginefixture.ProfileQ3ID+".json", enginefixture.ProfileQ3JSON)
	// A7: a person's own engine whose NAME says Quake and whose declaration
	// says Quake III. The declaration decides.
	custom := bytes.Replace(enginefixture.ProfileQ3JSON, []byte(`"`+enginefixture.ProfileQ3ID+`"`), []byte(`"my.own.quake-engine"`), 1)
	custom = bytes.Replace(custom, []byte(`"name": "`), []byte(`"name": "Quake (my build) `), 1)
	m.writeProfile("my.own.quake-engine.json", custom)

	status, listing := m.call(http.MethodGet, "/api/v1/build/pipelines", nil)
	if status != http.StatusOK {
		t.Fatalf("listing pipelines = %d: %v", status, listing["error"])
	}
	var fixture map[string]any
	for _, item := range listing["items"].([]any) {
		if row := item.(map[string]any); row["id"] == "aucom.fixture.pipeline" {
			fixture = row
		}
	}
	if fixture == nil {
		t.Fatal("the fixture pipeline is not listed")
	}
	fit, _ := fixture["engine_fit"].(map[string]any)
	if fit == nil {
		t.Fatalf("a pipeline row says nothing about which engines can play what it makes: %v", fixture)
	}
	want := map[string]bool{
		"aucom.fixture.q1-engine": true,
		enginefixture.ProfileQ2ID: false,
		enginefixture.ProfileQ3ID: false,
		"my.own.quake-engine":     false,
	}
	for id, fits := range want {
		verdict, _ := fit[id].(map[string]any)
		if verdict == nil {
			t.Errorf("no verdict for %s: %v", id, fit)
			continue
		}
		if verdict["fits"] != fits {
			t.Errorf("%s fits = %v, want %v (%v)", id, verdict["fits"], fits, verdict["reason"])
		}
		reason, _ := verdict["reason"].(string)
		if !fits && !strings.Contains(reason, "Quake maps") {
			t.Errorf("%s is refused without saying what the build makes: %q", id, reason)
		}
	}

	// The run is refused on the same word, before anything is fetched or run.
	m.preparePlay(t)
	body := playBody(m)
	body["engine"] = enginefixture.ProfileQ2ID
	status, refused := m.call(http.MethodPost, "/api/v1/play/plan", body)
	if status != http.StatusBadRequest {
		t.Fatalf("a Quake 1 build with a Quake II engine planned = %d (%v), want 400", status, refused)
	}
	if message, _ := refused["error"].(string); !strings.Contains(message, "Quake II") {
		t.Errorf("the refusal does not say what the engine plays: %q", message)
	}
}

// NEW_330, HITL 2026-10-22: a fresh setup recorded an engine's program and game
// folder, Profiles said "Ready", and Build & Run said "Play a map" needed a
// project folder no argument uses. The project folder is the Companion's own —
// where it keeps the maps and textures it downloads — created for the engine
// when nobody named one, and changeable like any folder.
func TestAnEngineSetUpWithItsProgramAndGameFolderCanPlayAMap(t *testing.T) {
	m := newMachine(t)
	self := mustExecutable(t)
	_, document := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.q1-engine", nil)
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.q1-engine/bind", map[string]any{
		"executables": map[string]string{"engine": self},
		"roots":       map[string]string{"game_root": m.gameRoot},
		"approve":     true,
		"digest":      document["digest"],
	}); status != http.StatusOK {
		t.Fatalf("binding = %d: %v", status, body["error"])
	}
	_, described := m.call(http.MethodGet, "/api/v1/engines/aucom.fixture.q1-engine", nil)
	problems, _ := described["action_problems"].(map[string]any)
	if play, _ := problems["play_map"].([]any); len(play) != 0 {
		t.Errorf("Play a map still needs setup after the program and the game folder: %v", play)
	}
	bound, _ := described["binding"].(map[string]any)
	roots, _ := bound["roots"].(map[string]any)
	if roots["content_root"] != m.assets {
		t.Errorf("the project folder is %v, want the Companion's own %s", roots["content_root"], m.assets)
	}
}
