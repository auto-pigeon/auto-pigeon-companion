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
	"strings"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
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
