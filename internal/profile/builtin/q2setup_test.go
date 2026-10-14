package builtin

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Q2_008: what the two Quake II toolchain documents now say about the folders
// and the programs they are pointed at, and about a person's own tokens.

// q2BaseFolder is a base game folder as a Quake II install has it: a `pak0.pak`
// (here an empty file of that name — nothing of the game's) in `baseq2`, with
// spaces in every folder above it.
func q2BaseFolder(t *testing.T) (install, baseq2 string) {
	t.Helper()
	install = filepath.Join(t.TempDir(), "Quake II (retail)")
	baseq2 = filepath.Join(install, "baseq2")
	if err := os.MkdirAll(baseq2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseq2, "pak0.pak"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return install, baseq2
}

func q2Request(t *testing.T, game string, platform profile.Platform, action string) profile.Request {
	t.Helper()
	base := filepath.Join(t.TempDir(), "My Maps")
	work := filepath.Join(base, "build work", "input")
	inputs := map[string]map[string]string{
		"compile": {"source_map": filepath.Join(work, "level.map")},
		"vis":     {"bsp": filepath.Join(work, "level.bsp"), "prt": filepath.Join(work, "level.prt")},
		"light":   {"bsp": filepath.Join(work, "level.bsp"), "texinfo": filepath.Join(work, "level.texinfo.json")},
	}[action]
	return profile.Request{
		Platform: platform,
		Roots: map[string]string{
			profile.RootWorkspace:   filepath.Join(base, "build work"),
			profile.RootToolInstall: filepath.Join(base, "auto-pigeon ericw tools"),
			profile.RootGame:        game,
			profile.RootContent:     game,
		},
		Inputs: inputs,
	}
}

func TestTheQuake2BaseFolderIsTheOneThatHoldsTheGameNotTheOneAboveIt(t *testing.T) {
	install, baseq2 := q2BaseFolder(t)
	linux := profile.Platform{OS: "linux", Arch: "amd64"}
	for _, id := range []string{EricwQ2, EricwAutoPigeonQ2} {
		entry, err := Find(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"compile", "vis", "light"} {
			if _, err := profile.Resolve(entry.Profile, action, q2Request(t, baseq2, linux, action)); err != nil {
				t.Errorf("%s/%s against baseq2 itself: %v", id, action, err)
			}
			// Measured: qbsp handed the folder above exits 0 with the built-in
			// palette and no textures. Refused, by name, as game data.
			_, err := profile.Resolve(entry.Profile, action, q2Request(t, install, linux, action))
			if err == nil || failure.Of(err) != failure.GameDataMissing ||
				!strings.Contains(err.Error(), "pak0.pak or pics/colormap.pcx") || !strings.Contains(err.Error(), install) {
				t.Errorf("%s/%s against the folder above baseq2: %v (class %q)", id, action, err, failure.Of(err))
			}
		}
	}
}

func TestQuake2LightNeedsTheTexinfoTheCompileWrote(t *testing.T) {
	for _, id := range []string{EricwQ2, EricwAutoPigeonQ2} {
		entry, err := Find(id)
		if err != nil {
			t.Fatal(err)
		}
		light, _ := entry.Profile.ActionByID("light")
		required := false
		for _, input := range light.Inputs {
			if input.Name == "texinfo" {
				required = input.Required && input.StageWith == "bsp"
			}
		}
		if !required {
			t.Errorf("%s: light's texinfo is not a required input staged with the BSP — light then succeeds with default flags", id)
		}
	}
}

// C7: `-threads 4` is said through the stage's parameter, never added as a
// second switch; a switch the action does not pass is the person's own; and
// qbsp is handed nothing it was not written to take.
func TestAQuake2StageTokenNeverRepeatsASwitchTheActionManages(t *testing.T) {
	_, baseq2 := q2BaseFolder(t)
	entry, err := Find(EricwAutoPigeonQ2)
	if err != nil {
		t.Fatal(err)
	}
	linux := profile.Platform{OS: "linux", Arch: "amd64"}
	for _, action := range []string{"vis", "light"} {
		request := q2Request(t, baseq2, linux, action)
		request.ExtraArgs = []string{"-threads", "4"}
		_, err := profile.Resolve(entry.Profile, action, request)
		if err == nil || !strings.Contains(err.Error(), `parameter "Threads" (threads)`) && !strings.Contains(err.Error(), "(threads)") {
			t.Errorf("%s with -threads as a token of one's own: %v", action, err)
		}

		request = q2Request(t, baseq2, linux, action)
		request.Options = map[string]string{"threads": "4"}
		request.ExtraArgs = []string{"-nopercent"}
		invocation, err := profile.Resolve(entry.Profile, action, request)
		if err != nil {
			t.Fatalf("%s with threads=4 and -nopercent: %v", action, err)
		}
		args := invocation.Command.Args
		at := slices.Index(args, "-threads")
		if at < 0 || args[at+1] != "4" || slices.Index(args[at+1:], "-threads") >= 0 {
			t.Errorf("%s argv = %q, want -threads 4 exactly once", action, args)
		}
		if slices.Index(args, "-nopercent") < 0 || slices.Index(args, "-nopercent") > slices.Index(args, request.Inputs["bsp"]) {
			t.Errorf("%s argv = %q, want -nopercent before the BSP", action, args)
		}
	}

	compile, err := profile.Resolve(entry.Profile, "compile", q2Request(t, baseq2, linux, "compile"))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(compile.Command.Args, "-threads") {
		t.Errorf("qbsp is given -threads, which this compile action does not declare: %q", compile.Command.Args)
	}

	for _, refused := range [][]string{{"-basedir", "/elsewhere"}, {"-nocolor"}, {"-level", "2"}, {"-fast"}} {
		request := q2Request(t, baseq2, linux, "vis")
		request.ExtraArgs = refused
		if _, err := profile.Resolve(entry.Profile, "vis", request); err == nil {
			t.Errorf("vis accepted %q as a token of one's own, and the action already passes it", refused)
		}
	}
	// A negative number is a value, not a switch.
	if err := profile.ManagedSwitchConflict(profile.Action{Executable: "vis", Args: []profile.Arg{{Value: "-1"}}}, []string{"-1"}); err != nil {
		t.Errorf("a negative number was read as a switch: %v", err)
	}
}

// C8: a folder with spaces in it, on a Windows-shaped binding, is one argument
// each, the program carries the platform's suffix, and nothing is joined into
// a command line.
func TestQuake2PathsWithSpacesStayOneArgumentOnAWindowsShapedBinding(t *testing.T) {
	_, baseq2 := q2BaseFolder(t)
	entry, err := Find(EricwAutoPigeonQ2)
	if err != nil {
		t.Fatal(err)
	}
	windows := profile.Platform{OS: "windows", Arch: "amd64"}
	request := q2Request(t, baseq2, windows, "compile")
	request.ExtraArgs = []string{"-path", `C:\Quake II mods\pak dir`}
	invocation, err := profile.Resolve(entry.Profile, "compile", request)
	if err != nil {
		t.Fatal(err)
	}
	args := invocation.Command.Args
	for _, flag := range []string{"-basedir", "-gamedir"} {
		at := slices.Index(args, flag)
		if at < 0 || args[at+1] != filepath.Clean(baseq2) {
			t.Errorf("argv = %q, want %s followed by %q as one element", args, flag, baseq2)
		}
	}
	at := slices.Index(args, "-path")
	if at < 0 || args[at+1] != `C:\Quake II mods\pak dir` {
		t.Errorf("argv = %q, want the token with spaces kept whole", args)
	}
	if !strings.HasSuffix(invocation.Command.Executable, "qbsp.exe") || !strings.Contains(invocation.Command.Executable, "auto-pigeon ericw tools") {
		t.Errorf("executable %q, want bin/qbsp.exe under the folder with spaces", invocation.Command.Executable)
	}
	if args[len(args)-2] != request.Inputs["source_map"] || !strings.HasSuffix(args[len(args)-1], "level.bsp") {
		t.Errorf("argv = %q, want the map then the BSP last", args)
	}
}
