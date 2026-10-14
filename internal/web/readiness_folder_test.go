package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Q2_008, found live: a Quake II tool bound to the folder ABOVE baseq2 read
// "ready" in Profiles, because its bspinfo action reads no game folder and one
// startable action was enough — while every pipeline that compiles with it read
// "needs setup". A recorded folder that is the wrong folder is a fact about the
// whole installation, like a recorded program that is the wrong program.
func TestAToolWhoseRecordedGameFolderIsWrongNeedsSetupEverywhere(t *testing.T) {
	document, err := profile.Decode([]byte(`{
  "schema_version": "aucom.profile/1.1", "kind": "tool", "id": "aucom.fixture.q2008", "version": "1.0.0",
  "name": "Q2_008 fixture", "summary": "two actions, one of which reads the game folder",
  "publisher": {"name": "Auto-Pigeon"}, "license": {"spdx": "MIT", "name": "MIT"},
  "compatibility": {"companion": {"at_least": "0.1.0"}},
  "tool_version": "1",
  "acquisition": [{"mode": "user_path", "title": "Use your own", "hint": "the folder holding tool"}],
  "platforms": [{"platform": {"os": "linux", "arch": "amd64"}, "status": "supported"},
                {"platform": {"os": "windows", "arch": "amd64"}, "status": "supported"},
                {"platform": {"os": "darwin", "arch": "arm64"}, "status": "supported"}],
  "executables": [{"name": "tool", "title": "Tool", "file": "tool"}],
  "capabilities": [{"id": "fixture.compile", "title": "Compile"}, {"id": "fixture.inspect", "title": "Inspect"}],
  "actions": [
    {"id": "compile", "title": "Compile", "capability": "fixture.compile", "executable": "tool", "args": ["{root.game_root}"],
     "working_dir": {"root": "workspace"},
     "roots": [{"role": "workspace", "access": "read_write", "purpose": "the job"},
               {"role": "game_root", "access": "read", "holds_any": ["pak0.pak"]}]},
    {"id": "inspect", "title": "Inspect", "capability": "fixture.inspect", "executable": "tool", "args": [],
     "working_dir": {"root": "workspace"},
     "roots": [{"role": "workspace", "access": "read_write", "purpose": "the job"}]}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	above := t.TempDir()
	game := filepath.Join(above, "baseq2")
	if err := os.MkdirAll(game, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(game, "pak0.pak"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	entry := job.CatalogEntry{Profile: document, Trust: profile.TrustBuiltin, Digest: "sha256:x"}
	local := binding.LocalBinding{ProfileID: "aucom.fixture.q2008", Executables: map[string]string{"tool": mustExecutable(t)}}

	local.Roots = map[string]string{profile.RootGame: game}
	if r := installReadiness(entry, local); !r.Ready {
		t.Fatalf("bound to baseq2 itself: %+v", r.Problems)
	}
	local.Roots = map[string]string{profile.RootGame: above}
	r := installReadiness(entry, local)
	if r.Ready || !r.Problems.Has(engine.FaultMissingGameData) {
		t.Fatalf("bound to the folder above baseq2 the tool reads ready=%v %+v", r.Ready, r.Problems)
	}
	// A build tool's folder is not set with `engine bind`, which refuses a
	// tool's id; the fix names the route that sets it.
	for _, problem := range r.Problems {
		if strings.Contains(problem.Fix, "engine bind") || !strings.Contains(problem.Fix, "--root game_root=") {
			t.Errorf("a build tool's folder fix: %q", problem.Fix)
		}
	}
	if len(r.PerAction["inspect"]) != 0 {
		t.Errorf("the action that reads no game folder is still startable on its own: %+v", r.PerAction["inspect"])
	}
}
