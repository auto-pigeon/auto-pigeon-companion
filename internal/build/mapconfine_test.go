package build

// `NEW_434`: a map that came from Auto-Pigeon is staged only as the copy the
// bundled converter's `canonical-map` writes (mapconfine.go). The rule itself is
// tested where it is written, in cmd/mapio (canonical_test.go: the three attack
// classes, the tokenizer models, the valid-map controls); these tests are the
// runner's half, run against the real converter built from ./cmd/mapio.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// realMapio is the converter built from this commit, once per test process.
var realMapio = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aucom-build-mapio-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, converter.ExecutableBase)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	command := exec.Command("go", "build", "-o", path, "./cmd/mapio")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, output)
	}
	return path, nil
})

// realCanonicalMap is the production CanonicalMapFunc over the real converter.
func realCanonicalMap(ctx context.Context, source, destination string) ([]string, error) {
	path, err := realMapio()
	if err != nil {
		return nil, err
	}
	return converter.CanonicalMap(ctx, converter.NewOverrideRunner(path), source, destination)
}

func testBox(texture string) string {
	face := "( -64 -64 -16 ) ( -64 -63 -16 ) ( -64 -64 -15 ) " + texture + " 0 0 0 1 1\n"
	return "{\n" + strings.Repeat(face, 6) + "}\n"
}

func testWorld(keys string, brushes ...string) string {
	return "{\n\"classname\" \"worldspawn\"\n" + keys + strings.Join(brushes, "") + "}\n"
}

var fromAUB = map[string]SourceRef{"source_map": {Backend: "http://localhost:9190", AssetType: "map",
	AssetID: "map1", RevisionID: "rev1", Revision: 1, Refetchable: true}}

// Through the runner: an input with a SourceRef — it came from Auto-Pigeon —
// is staged as the canonical copy; a file the user pointed at on their own disk
// is theirs and is staged exactly as it is. The local run is also the negative
// control: without the boundary, the bytes the compiler gets still name the
// files.
func TestAnAutoPigeonInputIsCanonicalAndALocalOneIsNot(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	vector := "// a comment the copy does not carry\n" +
		testWorld(`"wad" "/home/victim/private.wad;..\..\outside\rel.wad"`+"\n", testBox("{fence")) +
		"{\n\"classname\" \"info_null\"\n\"model\" \"models/ok.md3\"\n}\n"
	source := h.sourceMap("e1m1.map", vector)

	manifest := h.mustRun(Request{PipelineID: "test.build.pipeline",
		Inputs: map[string]string{"source_map": source}, Sources: fromAUB})
	record := inputNamed(t, manifest, "source_map")
	staged, err := os.ReadFile(record.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(staged), "/home/victim") || strings.Contains(string(staged), `..\`) ||
		strings.Contains(string(staged), "// a comment") || !strings.Contains(string(staged), `"wad" "private.wad;rel.wad"`) ||
		!strings.Contains(string(staged), " {fence ") {
		t.Fatalf("the staged Auto-Pigeon map is not the canonical copy:\n%s", staged)
	}
	if filepath.Base(record.Path) != "e1m1.map" {
		t.Fatal("the staged map lost its name, and with it the BSP's")
	}
	if original, _ := os.ReadFile(source); string(original) != vector {
		t.Fatal("the source map was written")
	}

	local := h.mustRun(Request{PipelineID: "test.build.pipeline", Inputs: map[string]string{"source_map": source}})
	untouched, err := os.ReadFile(inputNamed(t, local, "source_map").Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(untouched) != vector || !strings.Contains(string(untouched), "/home/victim/private.wad") {
		t.Fatal("negative control: a local map the user chose was rewritten")
	}

	for name, want := range map[string]struct{ text, says string }{
		"a fence-hidden external map": {testWorld("", testBox("{fence")) +
			"{\n\"classname\" \"misc_external_map\"\n\"_external_map\" \"/home/victim/private.map\"\n}\n", "_external_map"},
		"a Windows-path texture": {testWorld("", testBox(`C:\Users\Public\x.txt`)), "texture"},
		"a radlights setting":    {testWorld("\"_radlights\" \"/home/victim/x.rad\"\n", testBox("sand")), "_radlights"},
		"a climbing model":       {testWorld("") + "{\n\"classname\" \"misc_model\"\n\"model\" \"../../../etc/x.md3\"\n}\n", "model"},
	} {
		path := h.sourceMap(strings.ReplaceAll(name, " ", "-")+".map", want.text)
		_, err := h.run(Request{PipelineID: "test.build.pipeline", Inputs: map[string]string{"source_map": path}, Sources: fromAUB})
		if err == nil || !strings.Contains(err.Error(), want.says) {
			t.Fatalf("%s: an Auto-Pigeon map was compiled, or refused for something else: %v", name, err)
		}
		if strings.Contains(err.Error(), "/home/victim") || strings.Contains(err.Error(), "../../../etc") {
			t.Fatalf("%s: the refusal echoed the value: %v", name, err)
		}
	}
}

// With no converter there is no check, and a map from Auto-Pigeon is refused
// rather than compiled as it came; a local map still builds.
func TestWithNoConverterAMapFromAutoPigeonIsRefused(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	h.runner.options.CanonicalMap = nil
	source := h.sourceMap("e1m1.map", testWorld("", testBox("sand")))
	if _, err := h.run(Request{PipelineID: "test.build.pipeline", Inputs: map[string]string{"source_map": source},
		Sources: fromAUB}); !errors.Is(err, errNoMapCheck) {
		t.Fatalf("an Auto-Pigeon map was compiled with no converter to check it: %v", err)
	}
	h.mustRun(Request{PipelineID: "test.build.pipeline", Inputs: map[string]string{"source_map": source}})

	h.runner.options.CanonicalMap = func(ctx context.Context, source, destination string) ([]string, error) {
		return converter.CanonicalMap(ctx, nil, source, destination)
	}
	if _, err := h.run(Request{PipelineID: "test.build.pipeline", Inputs: map[string]string{"source_map": source},
		Sources: fromAUB}); !errors.Is(err, converter.ErrNoConverter) {
		t.Fatalf("a missing converter was not the refusal: %v", err)
	}
}

// A refusal at the compile boundary is classed by the converter's own word
// (NEW_515): an external map file and any other refused map are told apart
// on the manifest, without reading a sentence, and a missing converter is its
// own class. The compile step is never reached.
func TestARefusedMapIsClassedOnTheManifest(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	h.runner.options.CanonicalMap = realCanonicalMap
	for _, c := range []struct {
		name, text, class string
	}{
		{"external.map", testWorld("", testBox("sand")) +
			"{\n\"classname\" \"misc_external_map\"\n\"_external_map\" \"nothere/missing_prefab.map\"\n}\n",
			failure.MapExternalFile},
		{"radlights.map", testWorld(`"_radlights" "/home/victim/secret.rad"`+"\n", testBox("sand")), failure.MapRefused},
	} {
		manifest, err := h.run(Request{PipelineID: "test.build.pipeline",
			Inputs: map[string]string{"source_map": h.sourceMap(c.name, c.text)}, Sources: fromAUB})
		if err == nil || failure.Of(err) != c.class || manifest == nil || manifest.FailureClass != c.class ||
			len(manifest.Steps) == 0 || !manifest.Steps[0].Skipped || len(manifest.Inputs) != 0 {
			t.Fatalf("%s: %v %+v", c.name, err, manifest)
		}
	}
	h.runner.options.CanonicalMap = nil
	manifest, err := h.run(Request{PipelineID: "test.build.pipeline",
		Inputs: map[string]string{"source_map": h.sourceMap("plain.map", testWorld("", testBox("sand")))}, Sources: fromAUB})
	if failure.Of(err) != failure.ConverterUnavailable || manifest.FailureClass != failure.ConverterUnavailable {
		t.Fatalf("no converter: %v %+v", err, manifest)
	}
}

// `NEW_619` (AUP #331, #50), through the runner and the recording compiler: a
// map from Auto-Pigeon whose WAD list mixes separators, carries Windows forms
// and pads its `;` list reaches the compiler only as the canonical copy, whose
// list is plain file names that the approved `-wadpath` folder holds. Every
// path the compiler is handed is the build's own or that approved folder, and
// the map's other keys are kept exactly. A worldspawn key that would name a
// compiler's file or WAD folder stops the build before any compiler runs, says
// which key, and does not repeat the value.
func TestAWindowsShapedWADListReachesTheCompilerAsPlainNamesInsideTheBuild(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	wads := []string{"secret.wad", "gfx.wad", "net.wad", "up.wad", "metal.wad"}
	root := h.textureRoot(wads...)
	list := `C:secret.wad; c:/quake\id1/gfx.wad ;;\\server\share/net.wad;..\../up.wad;metal.wad;c:\quake\;`
	vector := testWorld(`"wad" "`+list+`"`+"\n"+`"message" "C:secret.wad;kept as written"`+"\n", testBox("sand"))
	request := func(name, text string) Request {
		return Request{PipelineID: "test.build.pipeline", Sources: fromAUB,
			Inputs:  map[string]string{"source_map": h.sourceMap(name, text)},
			Roots:   map[string]string{profile.RootContent: root},
			Options: map[string]map[string]string{"compile": {"basename": "win"}}}
	}
	manifest := h.mustRun(request("win.map", vector))

	inside := func(path, dir string) bool {
		relative, err := filepath.Rel(dir, path)
		return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
			!filepath.IsAbs(relative)
	}
	compile := stepNamed(t, manifest, "compile")
	if compile.Command == nil {
		t.Fatalf("the compile step ran no command: %+v", compile)
	}
	args := compile.Command.Args
	at := slices.Index(args, "-wadpath")
	if at < 0 || at+1 >= len(args) || args[at+1] != root {
		t.Fatalf("argv = %q, want -wadpath followed by the approved folder %q", args, root)
	}
	paths := 0
	for _, arg := range args {
		if !filepath.IsAbs(arg) || arg == root {
			continue
		}
		paths++
		if !inside(arg, h.builds) && !inside(arg, filepath.Join(h.dir, "jobs")) {
			t.Fatalf("the compiler was handed %q, which is neither the build's own nor the approved folder: %q", arg, args)
		}
	}
	if paths < 2 {
		t.Fatalf("argv = %q names fewer paths than the map and the BSP, so nothing was checked", args)
	}
	staged := inputNamed(t, manifest, "source_map")
	if !inside(staged.Path, h.builds) || inside(staged.Path, h.project) {
		t.Fatalf("the staged map %q is not inside the build", staged.Path)
	}

	// What the compiler read: the fixture writes the map it was handed into
	// its BSP, so the BSP holds the copy, not the downloaded bytes.
	bsp, err := os.ReadFile(outputNamed(t, compile.Outputs, "bsp").Path)
	if err != nil {
		t.Fatal(err)
	}
	read := string(bsp)
	want := `"wad" "` + strings.Join(wads, ";") + `"`
	if !strings.Contains(read, want) {
		t.Fatalf("the compiler did not read the plain WAD list %s:\n%s", want, read)
	}
	if strings.Contains(read, `\\server`) || strings.Contains(read, `c:/quake`) || strings.Contains(read, `..\`) ||
		strings.Contains(read, `"C:secret.wad;C`) {
		t.Fatalf("a path from the WAD list reached the compiler:\n%s", read)
	}
	if !strings.Contains(read, `"message" "C:secret.wad;kept as written"`) {
		t.Fatalf("a key that names no file was changed:\n%s", read)
	}
	// Each name resolves to a file inside the approved folder, and the
	// compiler listed that folder, so it is the one it searched.
	log := readJobLog(t, h, compile.JobID)
	for _, name := range wads {
		if target := filepath.Join(root, name); !inside(target, root) {
			t.Fatalf("%q resolves outside the WAD folder", name)
		} else if _, err := os.Stat(target); err != nil {
			t.Fatalf("%q is not in the approved WAD folder: %v", name, err)
		}
		if !strings.Contains(log, "wadpath holds "+name) {
			t.Fatalf("the compiler did not search the approved folder for %s: %q", name, log)
		}
	}

	// Settings that name a compiler's files or WAD folders, in Windows, UNC,
	// mixed and POSIX forms: refused before any compiler runs.
	for index, setting := range []struct{ key, value string }{
		{"_wadpath", `C:\quake\id1`},
		{"XWADPATH", `\\server\share/wads`},
		{"texturedefs", `..\defs/textures.def`},
		{"_aliasdef", "/home/victim/alias.def"},
		{"add", `c:/quake/extra.map`},
		{"_radlights", `C:lights.rad`},
	} {
		text := testWorld(`"wad" "metal.wad"`+"\n"+`"`+setting.key+`" "`+setting.value+`"`+"\n", testBox("sand"))
		manifest, err := h.run(request(fmt.Sprintf("setting%d.map", index), text))
		if err == nil || failure.Of(err) != failure.MapRefused || !strings.Contains(err.Error(), setting.key) ||
			!strings.Contains(err.Error(), "worldspawn") {
			t.Fatalf("%s: compiled, or refused for something else: %v", setting.key, err)
		}
		if strings.Contains(err.Error(), setting.value) {
			t.Fatalf("%s: the refusal repeated the value: %v", setting.key, err)
		}
		if manifest == nil || len(manifest.Steps) == 0 || !manifest.Steps[0].Skipped || manifest.Steps[0].Command != nil {
			t.Fatalf("%s: a compiler was started: %+v", setting.key, manifest)
		}
	}
}

// A compiler's arguments come from its profile, never from the map: a build of
// maps that differ only in keys that look like options starts the same programs
// with the same argument vectors.
func TestMapKeysNeverBecomeCompilerArguments(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	plain := h.mustRun(Request{PipelineID: "test.build.pipeline", Sources: fromAUB,
		Inputs: map[string]string{"source_map": h.sourceMap("a.map", testWorld("", testBox("sand")))}})
	options := testWorld("\"-threads\" \"1\"\n\"_qbsp_options\" \"-noskip -wadpath /\"\n"+
		"\"_q3map2_cmdline\" \"-fs_basepath /\"\n\"-logfile\" \"\"\n", testBox("sand"))
	if err := os.MkdirAll(filepath.Join(h.project, "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	loaded := h.mustRun(Request{PipelineID: "test.build.pipeline", Sources: fromAUB,
		Inputs: map[string]string{"source_map": h.sourceMap("b/a.map", options)}})
	if len(plain.Steps) != len(loaded.Steps) || len(plain.Steps) == 0 {
		t.Fatalf("steps: %d vs %d", len(plain.Steps), len(loaded.Steps))
	}
	shape := func(args []string) []string {
		out := make([]string, len(args))
		for index, arg := range args {
			if filepath.IsAbs(arg) {
				arg = "<path>/" + filepath.Base(arg)
			}
			out[index] = arg
		}
		return out
	}
	for index := range plain.Steps {
		a, b := plain.Steps[index].Command, loaded.Steps[index].Command
		if a == nil || b == nil || a.Executable != b.Executable || !reflect.DeepEqual(shape(a.Args), shape(b.Args)) {
			t.Fatalf("step %s started differently:\n%+v\n%+v", plain.Steps[index].ID, a, b)
		}
	}
}

// The staged copy is a new file in this build's own folder: a link in the way
// is an error, not a door, and a source that is a link is not followed.
func TestTheCanonicalCopyCannotBeRedirected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need a privilege the test account may not have")
	}
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "input")
	if err := os.MkdirAll(filepath.Join(root, "source_map"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "e1m1.map")
	if err := os.WriteFile(source, []byte(testWorld("", testBox("sand"))), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside.map")
	destination := filepath.Join(root, "source_map", "e1m1.map")
	if err := os.Symlink(outside, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAUBMap(ctx, realCanonicalMap, root, source, destination); err == nil {
		t.Fatal("the copy was written through a planted link")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("the planted link's target was created")
	}

	linked := filepath.Join(dir, "linked.map")
	if err := os.Symlink(source, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAUBMap(ctx, realCanonicalMap, root, linked, filepath.Join(root, "source_map", "x.map")); err == nil {
		t.Fatal("a source that is a link was followed")
	}

	escaped := filepath.Join(root, "escape")
	if err := os.Symlink(dir, escaped); err != nil {
		t.Fatal(err)
	}
	if err := checkAUBMapDestination(root, filepath.Join(escaped, "e1m1.map")); err == nil {
		t.Fatal("a staging folder that is a link out of the build was accepted")
	}
	for _, name := range []string{`..\evil.map`, "C:evil.map", ".hidden.map", "a\x01.map"} {
		if err := checkAUBMapDestination(root, filepath.Join(root, "source_map", name)); err == nil {
			t.Fatalf("the staged name %q was accepted", name)
		}
	}
	if err := checkAUBMapDestination(root, filepath.Join(root, "source_map", "plain.map")); err != nil {
		t.Fatalf("a plain name was refused: %v", err)
	}

	// A converter that claims a copy it did not write is caught.
	liar := func(context.Context, string, string) ([]string, error) { return nil, nil }
	if _, err := stageAUBMap(ctx, liar, root, source, filepath.Join(root, "source_map", "plain.map")); err == nil {
		t.Fatal("a copy nobody wrote was staged")
	}
}
