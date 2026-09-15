package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// NEW_244D: a build's level has to land in maps/, named for the map, or no
// engine loads it; and a second level staged into the same directory replaces
// the first through the staging record rather than piling up beside it.
func TestALevelIsStagedWhereAnEngineLooks(t *testing.T) {
	dir := t.TempDir()
	game := filepath.Join(dir, "quake")
	if err := os.MkdirAll(filepath.Join(game, "id1"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "build output")
	for name, body := range map[string]string{"bsp/level.bsp": "BSP29", "lit/level.lit": "QLIT"} {
		path := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	staged, err := LevelStaging{GameRoot: game, ModName: "auto-pigeon", MapName: "dm2",
		BSP: filepath.Join(out, "bsp", "level.bsp"), Lit: filepath.Join(out, "lit", "level.lit")}.Stage()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"maps/dm2.bsp", "maps/dm2.lit"} {
		if _, err := os.Stat(filepath.Join(game, "auto-pigeon", filepath.FromSlash(want))); err != nil {
			t.Errorf("%s was not staged: %v", want, err)
		}
	}
	if len(staged.Stamp.Files) != 2 {
		t.Errorf("stamp files = %v", staged.Stamp.Files)
	}

	if _, err := (LevelStaging{GameRoot: game, ModName: "auto-pigeon", MapName: "e1m6",
		BSP: filepath.Join(out, "bsp", "level.bsp")}).Stage(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(game, "auto-pigeon", "maps", "dm2.bsp")); !os.IsNotExist(err) {
		t.Errorf("the previous level was left beside the new one: %v", err)
	}
	if _, err := (LevelStaging{GameRoot: game, ModName: "auto-pigeon", MapName: "Friday DM",
		BSP: filepath.Join(out, "bsp", "level.bsp")}).Stage(); err == nil {
		t.Error("a map name with a space was accepted")
	}
}
