package build

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
)

func TestAFinishedBuildNamesItsLevel(t *testing.T) {
	dir := t.TempDir()
	bsp := filepath.Join(dir, "level.bsp")
	if err := os.WriteFile(bsp, []byte("BSP29"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{State: job.Succeeded,
		Inputs:  []FileRecord{{Name: "source_map", Path: "/x/input/source_map/Friday DM.apmap"}},
		Outputs: []FileRecord{{Name: "bsp", Path: bsp}, {Name: "lit", Path: filepath.Join(dir, "gone.lit")}}}
	level, err := PlayableLevel(m)
	if err != nil {
		t.Fatal(err)
	}
	if level.BSP != bsp || level.Lit != "" || level.MapName != "friday_dm" {
		t.Errorf("level = %+v", level)
	}
	m.State = job.Failed
	if _, err := PlayableLevel(m); !errors.Is(err, ErrNotPlayable) {
		t.Errorf("a failed build was playable: %v", err)
	}
	if MapName("") != "level" || MapName("dm2.map") != "dm2" {
		t.Errorf("MapName: %q %q", MapName(""), MapName("dm2.map"))
	}
}
