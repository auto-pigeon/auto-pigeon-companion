package web

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
)

// The Game folder suggested from an engine executable, at the API (HITL
// 2026-11-12). The page asks `GET /api/v1/engines/detect?profile=…&executable=…`
// and fills the field from the answer; only Save these paths records it. These
// prove the lookup is the PROFILE's game, near the engine only, and writes
// nothing — the browser half is gamefolder_browser_test.go.

// gameInstall lays a hand-installed game out under a folder with spaces in its
// name: the engine program, and the profile's data folder with one archive.
func gameInstall(t *testing.T, exeRelative, archiveRelative string) (root, executable string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "Program Files", "My Quake")
	executable = filepath.Join(root, filepath.FromSlash(exeRelative))
	for path, body := range map[string]string{executable: "#!/bin/sh\n", filepath.Join(root, filepath.FromSlash(archiveRelative)): "PACK"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, executable
}

func detectNear(t *testing.T, m *machine, profileID, executable string) map[string]any {
	t.Helper()
	query := url.Values{"profile": {profileID}, "executable": {executable}}
	response, body := do(t, m.server, "GET", "/api/v1/engines/detect?"+query.Encode(), "")
	if response.StatusCode != 200 {
		t.Fatalf("detect answered %d: %v", response.StatusCode, body)
	}
	return body
}

func TestTheGameFolderIsSuggestedForTheProfilesGame(t *testing.T) {
	m := newMachine(t)
	m.writeProfile(enginefixture.ProfileQ2ID+".json", enginefixture.ProfileQ2JSON)
	m.writeProfile("aucom.fixture.q3-engine.json", enginefixture.ProfileQ3JSON)
	for _, c := range []struct {
		profile, exe, archive, evidence string
	}{
		{"aucom.fixture.q1-engine", "vkquake.exe", "id1/pak0.pak", "id1/pak0.pak"},
		{enginefixture.ProfileQ2ID, "bin/yquake2", "baseq2/pak0.pak", "baseq2/pak0.pak"},
		{"aucom.fixture.q3-engine", "ioquake3.x86_64", "baseq3/pak0.pk3", "baseq3/pak0.pk3"},
	} {
		t.Run(c.profile, func(t *testing.T) {
			root, executable := gameInstall(t, c.exe, c.archive)
			body := detectNear(t, m, c.profile, executable)
			items, _ := body["items"].([]any)
			if len(items) != 1 {
				t.Fatalf("want one suggestion, got %v", body)
			}
			item := items[0].(map[string]any)
			if item["path"] != root || item["evidence"] != c.evidence || item["source"] != "near_engine" {
				t.Errorf("got %v", item)
			}
			looked := body["looked_for"].(map[string]any)
			if looked["base_dir"] != filepath.Dir(filepath.FromSlash(c.evidence)) {
				t.Errorf("looked_for %v", looked)
			}
		})
	}
}

func TestAnotherGamesDataIsNotSuggested(t *testing.T) {
	m := newMachine(t)
	m.writeProfile(enginefixture.ProfileQ2ID+".json", enginefixture.ProfileQ2JSON)
	// Quake 1 data beside an engine whose profile is Quake II: nothing, and the
	// answer says what it looked for so the page can say so.
	_, executable := gameInstall(t, "fteqw.exe", "id1/pak0.pak")
	body := detectNear(t, m, enginefixture.ProfileQ2ID, executable)
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Fatalf("suggested another game's data: %v", body)
	}
	looked := body["looked_for"].(map[string]any)
	if looked["base_dir"] != "baseq2" || len(looked["archives"].([]any)) == 0 {
		t.Errorf("looked_for %v", looked)
	}
}

func TestTheSuggestionWritesNoBinding(t *testing.T) {
	m := newMachine(t)
	_, executable := gameInstall(t, "vkquake.exe", "id1/pak0.pak")
	before, _ := os.ReadFile(m.bindings)
	detectNear(t, m, "aucom.fixture.q1-engine", executable)
	query := url.Values{"profile": {"aucom.fixture.q1-engine"}}
	if response, body := do(t, m.server, "GET", "/api/v1/engines/detect?"+query.Encode(), ""); response.StatusCode != 200 {
		t.Fatalf("Look for installed games answered %d: %v", response.StatusCode, body)
	}
	after, _ := os.ReadFile(m.bindings)
	if string(before) != string(after) {
		t.Fatalf("a lookup changed the bindings file:\nbefore %q\nafter  %q", before, after)
	}
}

func TestTheLookupRefusesWhatItCannotUse(t *testing.T) {
	m := newMachine(t)
	for path, want := range map[string]int{
		"/api/v1/engines/detect?profile=no.such.engine":                                                     404,
		"/api/v1/engines/detect?profile=aucom.fixture.q1-engine&executable=relative/vkquake":                400,
		"/api/v1/engines/detect?profile=aucom.fixture.q1-engine&executable=" + url.QueryEscape(t.TempDir()): 400,
	} {
		if response, body := do(t, m.server, "GET", path, ""); response.StatusCode != want {
			t.Errorf("%s: want %d, got %d %v", path, want, response.StatusCode, body)
		}
	}
}
