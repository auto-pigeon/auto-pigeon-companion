package builtin

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_244D: EricW's `-wadpath` is passed only when a texture folder is set, so
// a map naming `gfx/metal.wad` finds its WAD and the fixture beside-the-map
// case still builds with nothing set.
func TestTheBuiltInQ1CompilePassesTheTextureFolderOnlyWhenOneIsSet(t *testing.T) {
	entry, err := Find("auto-pigeon.ericw-tools.q1")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	request := profile.Request{
		Platform: profile.Platform{OS: "linux", Arch: "amd64"},
		Roots: map[string]string{
			profile.RootWorkspace:   filepath.Join(base, "workspace"),
			profile.RootToolInstall: filepath.Join(base, "ericw"),
		},
		Inputs: map[string]string{"source_map": filepath.Join(base, "workspace", "input", "dm2.map")},
	}
	without, err := profile.Resolve(entry.Profile, "compile", request)
	if err != nil {
		t.Fatalf("with no texture folder the compile must still resolve: %v", err)
	}
	if slices.Contains(without.Command.Args, "-wadpath") {
		t.Errorf("-wadpath passed with no texture folder: %v", without.Command.Args)
	}

	request.Roots[profile.RootContent] = filepath.Join(base, "quake1 sources")
	with, err := profile.Resolve(entry.Profile, "compile", request)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.Index(with.Command.Args, "-wadpath")
	if at < 0 || with.Command.Args[at+1] != request.Roots[profile.RootContent] {
		t.Errorf("argv = %v, want -wadpath followed by the texture folder as one element", with.Command.Args)
	}
}
