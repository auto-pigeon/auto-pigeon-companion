package build

// NEW_643 (auto-pigeon-libraries#15): a Quake 1 map from Auto-Pigeon with a
// legal fence texture is built through the runner like any other: the bundled
// converter's canonical copy is what the compiler reads, it still names the
// fence on every face of the slab, its WADs are found in the approved texture
// folder and nowhere else, and a brace that is not a fence name is refused
// before any compiler starts.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

func TestAFenceMapFromAutoPigeonReachesTheCompilerWithItsTexturesAndWADs(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	root := filepath.Join(h.dir, "textures")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range fixturewad.FenceRoomWADs() {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request := func(name, text string) Request {
		return Request{PipelineID: "test.build.pipeline", Sources: fromAUB,
			Inputs:  map[string]string{"source_map": h.sourceMap(name, text)},
			Roots:   map[string]string{profile.RootContent: root},
			Options: map[string]map[string]string{"compile": {"basename": strings.TrimSuffix(name, ".map")}}}
	}

	for _, texture := range []string{fixturewad.FenceRoomFence, fixturewad.FenceRoomWall} {
		name := strings.TrimPrefix(texture, "{") + "_room.map"
		manifest := h.mustRun(request(name, fixturewad.FenceRoomMap(texture)))
		staged := inputNamed(t, manifest, "source_map")
		if staged.Path == filepath.Join(h.project, name) {
			t.Fatalf("%s: the downloaded map itself was staged, not the canonical copy", texture)
		}
		compile := stepNamed(t, manifest, "compile")
		if compile.Command == nil {
			t.Fatalf("%s: the compile step ran no command: %+v", texture, compile)
		}
		// The recorder compiler writes the map it was handed into its BSP.
		bsp, err := os.ReadFile(outputNamed(t, compile.Outputs, "bsp").Path)
		if err != nil {
			t.Fatal(err)
		}
		read := string(bsp)
		want := 0
		if texture == fixturewad.FenceRoomFence {
			want = fixturewad.FenceRoomFenceFaces
		}
		if got := strings.Count(read, " "+fixturewad.FenceRoomFence+" "); got != want {
			t.Fatalf("%s: the compiler read %d %s faces, want %d:\n%s", texture, got, fixturewad.FenceRoomFence, want, read)
		}
		if got := strings.Count(read, " "+fixturewad.FenceRoomWall+" "); got != 36+6-want {
			t.Fatalf("%s: the compiler read %d %s faces, want %d", texture, got, fixturewad.FenceRoomWall, 36+6-want)
		}
		if !strings.Contains(read, `"wad" "`+fixturewad.FenceRoomFenceWAD+";"+fixturewad.FenceRoomWallWAD+`"`) {
			t.Fatalf("%s: the compiler did not read the map's WAD list:\n%s", texture, read)
		}
		log := readJobLog(t, h, compile.JobID)
		for name := range fixturewad.FenceRoomWADs() {
			if !strings.Contains(log, "wadpath holds "+name) {
				t.Fatalf("%s: the compiler was not pointed at the folder holding %s: %q", texture, name, log)
			}
		}
	}

	// A lone brace where the texture goes is no fence: refused by the compile
	// boundary with its own failure class, and no compiler is started.
	broken := strings.Replace(fixturewad.FenceRoomMap(fixturewad.FenceRoomFence),
		fixturewad.FenceRoomFence+" 0 0 0 1 1", "{ 0 0 0 1 1", 1)
	manifest, err := h.run(request("lone_brace.map", broken))
	if err == nil || failure.Of(err) != failure.MapRefused {
		t.Fatalf("a lone brace texture compiled, or was refused for something else: %v", err)
	}
	if manifest == nil || len(manifest.Steps) == 0 || !manifest.Steps[0].Skipped || manifest.Steps[0].Command != nil {
		t.Fatalf("a compiler was started for a refused map: %+v", manifest)
	}
}
