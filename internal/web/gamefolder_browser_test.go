package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
)

// The Game folder suggested from the engine executable, in a real browser
// against the real page (HITL 2026-11-12). testdata/gamefolder.js is the
// driver: it picks the engine both ways a person does — typed into its field,
// and through "Use this folder" — and checks a saved and a hand-typed Game
// folder are kept, that the value after Save survives a reload, that the
// profile decides the game, and that a stale answer lands nowhere. The
// /journey/assert-* controls read the bindings file on disk, so "a suggestion
// alone records nothing" is proved by what was written, not by what the page
// shows.
//
// The harness's picker has no native helper (harness_test.go), so Browse… is
// hidden and the executable is typed: a native file chooser is not exercised
// here.
func TestTheGameFolderIsSuggestedFromTheEngineInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	m := newMachine(t)
	m.writeProfile(enginefixture.ProfileQ2ID+".json", enginefixture.ProfileQ2JSON)

	base := filepath.Join(t.TempDir(), "Games With Spaces")
	write := func(relative, body string) string {
		path := filepath.Join(base, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// The fixture engine profiles look for a program named `engine`.
	q1Exe := write("Quake/engine", "#!/bin/sh\n")
	write("Quake/id1/pak0.pak", "PACK")
	otherQ1Exe := write("Second Quake/engine", "#!/bin/sh\n")
	write("Second Quake/id1/pak0.pak", "PACK")
	bareExe := write("Lonely Engine/bin/engine", "#!/bin/sh\n")
	q2Exe := write("Quake II/bin/engine", "#!/bin/sh\n")
	write("Quake II/baseq2/pak0.pak", "PACK")
	manual := filepath.Join(base, "Typed By Hand")
	if err := os.MkdirAll(manual, 0o755); err != nil {
		t.Fatal(err)
	}
	q1Root, q2Root := filepath.Dir(q1Exe), filepath.Dir(filepath.Dir(q2Exe))

	gameRootOf := func(profileID string) string {
		set, err := binding.LoadFile(m.bindings)
		if err != nil {
			return ""
		}
		local, _ := set.Find(profileID)
		return local.Roots["game_root"]
	}
	expect := func(profileID, want, what string) func() {
		return func() {
			if got := gameRootOf(profileID); got != want {
				t.Errorf("%s: the recorded Game folder of %s is %q, want %q", what, profileID, got, want)
			}
		}
	}
	drivePlayPageWith(t, m, browser, "1280,900", "gamefolder", "testdata/gamefolder.js", map[string]func(){
		"/journey/assert-unbound":         expect("aucom.fixture.q1-engine", "", "a suggestion alone"),
		"/journey/assert-bound-suggested": expect("aucom.fixture.q1-engine", q1Root, "after Save"),
		"/journey/assert-bound-manual":    expect("aucom.fixture.q1-engine", manual, "after Save of a typed folder"),
		"/journey/assert-q2-unbound":      expect(enginefixture.ProfileQ2ID, "", "a lookup interrupted by navigation"),
	}, map[string]any{
		"q1_name": "Fixture Q1 engine", "q2_name": "Fixture Q2 engine",
		"q1_exe": q1Exe, "q1_root": q1Root, "q1_evidence": filepath.Join("id1", "pak0.pak"),
		"other_q1_exe": otherQ1Exe, "bare_exe": bareExe, "manual": manual,
		"q2_exe": q2Exe, "q2_root": q2Root, "q2_evidence": filepath.Join("baseq2", "pak0.pak"),
	})
}
