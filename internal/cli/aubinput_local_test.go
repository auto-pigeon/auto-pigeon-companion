package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A local `.apmap` build input is converted from a copy in the build's stage (Q3_004): the
// conversion writes `converted-<name>/` beside what it reads, and that must never be the user's own
// directory. Account inputs and non-APMap inputs pass through unchanged.
func TestLocalAPMapInputsAreCopiedIntoTheStage(t *testing.T) {
	source, stage := t.TempDir(), t.TempDir()
	apmap := filepath.Join(source, "room.apmap")
	wad := filepath.Join(source, "metal.wad")
	for _, path := range []string{apmap, wad} {
		if err := os.WriteFile(path, []byte(`{"game":"quake3"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inputs := map[string]string{"source_map": apmap, "wad": wad, "other": "aub:map/abc@r1"}
	resolved := map[string]string{"source_map": apmap, "wad": wad, "other": filepath.Join(stage, "abc.apmap")}
	out, err := stageLocalAPMaps(inputs, resolved, stage)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stage, "local-source_map", "room.apmap"); out["source_map"] != want {
		t.Fatalf("source_map = %q, want %q", out["source_map"], want)
	}
	if data, err := os.ReadFile(out["source_map"]); err != nil || string(data) != `{"game":"quake3"}` {
		t.Fatalf("staged copy = %q, %v", data, err)
	}
	if out["wad"] != wad || out["other"] != resolved["other"] {
		t.Errorf("a non-APMap or account input moved: %v", out)
	}
	if entries, _ := os.ReadDir(source); len(entries) != 2 {
		t.Errorf("the source directory now holds %d entries", len(entries))
	}
}
