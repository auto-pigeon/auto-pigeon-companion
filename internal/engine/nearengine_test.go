package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
)

// The Game folder suggested from an engine executable (HITL 2026-11-12, after a
// fresh Companion install): the PROFILE's data folder beside the engine, or
// beside the program subdirectory it sits in, holding a real archive of that
// game. Real directories with spaces in their names, because that is where a
// hand-installed game on Windows lives.

// tree writes files (relative path -> content) under a fresh root and returns it.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "My Games", "Quake Install")
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if body == "<dir>" {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func near(layout engine.Layout, executable string) []engine.Candidate {
	return engine.Scanner{GOOS: "linux", Lookenv: func(string) (string, bool) { return "", false }, Layout: layout}.
		NearExecutable(executable)
}

func TestTheProfilesBaseFolderNamesTheLayout(t *testing.T) {
	for base, archive := range map[string]string{"id1": "pak0.pak", "baseq2": "pak0.pak", "baseq3": "pak0.pk3", "BaseQ3": "pak0.pk3"} {
		layout, ok := engine.LayoutForBaseFolder(base)
		if !ok || layout.Archives[0] != archive {
			t.Errorf("%s: got %+v, %v", base, layout, ok)
		}
	}
	if _, ok := engine.LayoutForBaseFolder("valve"); ok {
		t.Error("a data folder with no evidence rule must not be guessed at")
	}
}

func TestEachGameIsFoundBesideItsEngine(t *testing.T) {
	cases := []struct {
		name     string
		layout   engine.Layout
		files    map[string]string
		evidence string
	}{
		{"Quake", engine.Quake1Layout, map[string]string{"vkquake.exe": "MZ", "id1/pak0.pak": "PACK"}, "id1/pak0.pak"},
		{"Quake, pak1 only", engine.Quake1Layout, map[string]string{"vkquake.exe": "MZ", "id1/pak1.pak": "PACK"}, "id1/pak1.pak"},
		{"Quake II", engine.Quake2Layout, map[string]string{"yquake2.exe": "MZ", "baseq2/pak0.pak": "PACK"}, "baseq2/pak0.pak"},
		{"Quake III", engine.Quake3Layout, map[string]string{"ioquake3.exe": "MZ", "baseq3/pak0.pk3": "PK"}, "baseq3/pak0.pk3"},
		{"Quake, other case", engine.Quake1Layout, map[string]string{"vkquake.exe": "MZ", "Id1/PAK0.PAK": "PACK"}, "Id1/PAK0.PAK"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := tree(t, c.files)
			var exe string
			for name := range c.files {
				if filepath.Ext(name) == ".exe" {
					exe = filepath.Join(root, name)
				}
			}
			found := near(c.layout, exe)
			if len(found) != 1 {
				t.Fatalf("want one candidate, got %+v", found)
			}
			got := found[0]
			if got.Path != root || got.Evidence != c.evidence || got.Where != engine.WhereBesideEngine ||
				got.Source != engine.SourceNearEngine || got.EvidencePath != filepath.FromSlash(c.evidence) {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestAnEngineUnderBinFindsTheGameAboveIt(t *testing.T) {
	root := tree(t, map[string]string{"bin/x64/ioquake3": "ELF", "baseq3/pak0.pk3": "PK"})
	found := near(engine.Quake3Layout, filepath.Join(root, "bin", "x64", "ioquake3"))
	if len(found) != 1 || found[0].Path != root || found[0].Where != engine.WhereAboveEngine {
		t.Fatalf("got %+v", found)
	}
	// An ordinary folder name is not climbed out of: the game is looked for
	// where an engine is conventionally kept, not anywhere above it.
	other := tree(t, map[string]string{"engines/ioquake3": "ELF", "baseq3/pak0.pk3": "PK"})
	if found := near(engine.Quake3Layout, filepath.Join(other, "engines", "ioquake3")); len(found) != 0 {
		t.Fatalf("climbed out of a non-program folder: %+v", found)
	}
}

func TestMissingOrWrongGameDataIsNotAGameFolder(t *testing.T) {
	cases := []struct {
		name   string
		layout engine.Layout
		files  map[string]string
	}{
		{"no data folder at all", engine.Quake1Layout, map[string]string{"vkquake.exe": "MZ"}},
		{"an empty id1", engine.Quake1Layout, map[string]string{"vkquake.exe": "MZ", "id1": "<dir>"}},
		{"an empty pak0.pak", engine.Quake1Layout, map[string]string{"vkquake.exe": "MZ", "id1/pak0.pak": ""}},
		{"a directory named pak0.pak", engine.Quake1Layout, map[string]string{"vkquake.exe": "MZ", "id1/pak0.pak/x": "y"}},
		{"Quake II data for a Quake engine", engine.Quake1Layout, map[string]string{"vkquake.exe": "MZ", "baseq2/pak0.pak": "PACK"}},
		{"Quake data for a Quake II engine", engine.Quake2Layout, map[string]string{"yquake2.exe": "MZ", "id1/pak0.pak": "PACK"}},
		{"a Quake II pak in baseq3", engine.Quake3Layout, map[string]string{"ioquake3.exe": "MZ", "baseq3/pak0.pak": "PACK"}},
		{"loose files but no archive", engine.Quake3Layout, map[string]string{"ioquake3.exe": "MZ", "baseq3/maps/q3dm1.bsp": "IBSP"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := tree(t, c.files)
			var exe string
			for name := range c.files {
				if filepath.Ext(name) == ".exe" {
					exe = filepath.Join(root, name)
				}
			}
			if found := near(c.layout, exe); len(found) != 0 {
				t.Fatalf("want nothing, got %+v", found)
			}
		})
	}
}

func TestSeveralGameFoldersAreAllOfferedForAChoice(t *testing.T) {
	// The engine's own folder holds a copy, and so does the folder above its
	// bin; and the Quake 1 re-release keeps a second layout under rerelease/.
	root := tree(t, map[string]string{
		"bin/vkquake":                  "ELF",
		"bin/id1/pak0.pak":             "PACK",
		"id1/pak0.pak":                 "PACK",
		"rerelease/id1/pak0.pak":       "PACK",
		"rerelease/id1/pak1.pak":       "PACK",
		"bin/rerelease/id1/readme.txt": "not an archive",
	})
	found := near(engine.Quake1Layout, filepath.Join(root, "bin", "vkquake"))
	want := []string{filepath.Join(root, "bin"), root, filepath.Join(root, "rerelease")}
	if len(found) != len(want) {
		t.Fatalf("want %v, got %+v", want, found)
	}
	for i, path := range want {
		if found[i].Path != path {
			t.Errorf("candidate %d: want %s, got %s", i, path, found[i].Path)
		}
	}
	if found[2].Note == "" {
		t.Error("the re-release candidate should say what it is")
	}
}

func TestTheNearEngineLookupWritesNothing(t *testing.T) {
	root := tree(t, map[string]string{"bin/yquake2": "ELF", "baseq2/pak0.pak": "PACK"})
	before := snapshot(t, root)
	near(engine.Quake2Layout, filepath.Join(root, "bin", "yquake2"))
	if after := snapshot(t, root); after != before {
		t.Fatalf("the lookup changed the tree:\nbefore %s\nafter  %s", before, after)
	}
}

func snapshot(t *testing.T, root string) string {
	t.Helper()
	var out string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out += path + " " + info.Mode().String() + " " + info.ModTime().String() + "\n"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOnlyQuake1IsLookedForInStoreLibraries(t *testing.T) {
	// A Steam library holding Quake 1 data: a Quake II scan must not report it,
	// a Quake 1 scan (the zero layout, as before) must.
	home := t.TempDir()
	common := filepath.Join(home, ".steam", "steam", "steamapps", "common", "Quake")
	if err := os.MkdirAll(filepath.Join(common, "id1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "id1", "pak0.pak"), []byte("PACK"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := func(name string) (string, bool) {
		if name == "HOME" {
			return home, true
		}
		return "", false
	}
	if found := (engine.Scanner{GOOS: "linux", Lookenv: env}).Detect(); len(found) != 1 || found[0].Source != engine.SourceSteam {
		t.Fatalf("Quake 1 Steam discovery changed: %+v", found)
	}
	if found := (engine.Scanner{GOOS: "linux", Lookenv: env, Layout: engine.Quake2Layout}).Detect(); len(found) != 0 {
		t.Fatalf("a Quake II scan reported Quake 1 data: %+v", found)
	}
}
