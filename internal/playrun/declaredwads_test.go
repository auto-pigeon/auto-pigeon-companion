package playrun

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// dm2 declares `gfx/metal.wad`; the bundle holds `metal.wad`. The run's content
// root must have it at the declared path, and the shared bundle must not change
// (2026-09-23: ericw-tools 2.0 compiled dm2 with every texture missing).
func TestAWADIsPlacedWhereTheMapDeclaresIt(t *testing.T) {
	dir := t.TempDir()
	mapPath := filepath.Join(dir, "dm2.map")
	if err := os.WriteFile(mapPath, []byte("{\n\"classname\" \"worldspawn\"\n\"wad\" \"gfx/metal.wad;../../evil.wad;C:\\\\q\\\\x.wad;base.wad\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	declared, err := DeclaredWADs(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gfx/metal.wad", "../../evil.wad", "C://q//x.wad", "base.wad"}; !reflect.DeepEqual(declared, want) {
		t.Fatalf("declared = %v, want %v", declared, want)
	}

	bundle := t.TempDir()
	for _, name := range []string{"METAL.WAD", "evil.wad", "base.wad"} {
		if err := os.WriteFile(filepath.Join(bundle, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{store: store}
	record := &Record{ID: "run1", BundleRoot: bundle}
	placed, err := s.placeDeclaredWADs(record, mapPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(placed, []string{"gfx/metal.wad"}) {
		t.Fatalf("placed = %v", placed)
	}
	if record.BundleRoot == bundle {
		t.Fatal("the shared bundle was written to rather than copied")
	}
	if data, err := os.ReadFile(filepath.Join(record.BundleRoot, "gfx", "metal.wad")); err != nil || string(data) != "METAL.WAD" {
		t.Fatalf("gfx/metal.wad: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "gfx")); err == nil {
		t.Fatal("the shared bundle gained a directory")
	}

	// A second pass has nothing left to do.
	if again, err := s.placeDeclaredWADs(record, mapPath); err != nil || len(again) != 0 {
		t.Fatalf("second pass placed %v, %v", again, err)
	}
}
