package q3deps_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3deps"
)

// The Q3_011 fixture: generated, original, and compiled by the real Q3Map2.
// `testdata/q3011/README.md` says how, and why its compile set and its runtime
// set differ in both directions.
const (
	q3011Map     = "testdata/q3011/maps/q3011_room.map"
	q3011BSP     = "testdata/q3011/maps/q3011_room.bsp"
	q3011Content = "testdata/q3011/content"
)

func names(references []q3deps.Reference) []string {
	out := make([]string, 0, len(references))
	for _, reference := range references {
		out = append(out, string(reference.Kind)+" "+reference.Name)
	}
	sort.Strings(out)
	return out
}

// What an engine looks for is read out of the file the engine reads. The two
// lines that matter are the ones a rule about the map source could not have
// produced: the baked model's SKIN is here and its `.md3` is not, and the
// `model2` model is here although the compiler never opened it.
func TestACompiledMapNamesWhatAnEngineLoads(t *testing.T) {
	references, err := q3deps.ParseBSP(q3011BSP)
	if err != nil {
		t.Fatalf("%v", err)
	}
	want := []string{
		"model models/apq3011/beacon.md3",
		"shader models/apq3011/crate",
		"shader textures/apq3011/ceiling",
		"shader textures/apq3011/floor",
		"shader textures/apq3011/glow",
		"shader textures/apq3011/wall",
		"sound sound/apq3011/hum.wav",
	}
	if got := names(references); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the compiled map names\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// The map source names the model the compiler bakes in, and marks the three
// keys the compiler never reads.
func TestAMapSourceMarksWhatOnlyAnEngineReads(t *testing.T) {
	references, err := q3deps.ParseMap(q3011Map)
	if err != nil {
		t.Fatalf("%v", err)
	}
	runtimeOnly := map[string]bool{}
	seen := map[string]bool{}
	for _, reference := range references {
		seen[reference.Name] = true
		runtimeOnly[reference.Name] = reference.RuntimeOnly
	}
	for name, want := range map[string]bool{
		"models/apq3011/crate.md3":  false,
		"models/apq3011/beacon.md3": true,
		"sound/apq3011/hum.wav":     true,
		"textures/apq3011/wall":     false,
	} {
		if !seen[name] {
			t.Errorf("the map source does not name %s", name)
		}
		if runtimeOnly[name] != want {
			t.Errorf("%s: runtime-only is %v, want %v", name, runtimeOnly[name], want)
		}
	}
}

func filesOf(t *testing.T, report *q3deps.Report, name string) map[string]q3deps.File {
	t.Helper()
	for _, resolution := range report.Resolutions {
		if resolution.Name != name {
			continue
		}
		out := map[string]q3deps.File{}
		for _, file := range resolution.Files {
			out[file.Path] = file
		}
		return out
	}
	t.Fatalf("the report has no %s", name)
	return nil
}

// A model is not the whole dependency: its surfaces name the shaders it is
// drawn with, and a review that stopped at "the .md3 is there" called a model
// with no skin complete.
func TestAModelsOwnShadersAreRead(t *testing.T) {
	report, err := q3deps.Discover([]string{q3011Map}, q3deps.Scan{ContentRoots: []string{q3011Content}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	files := filesOf(t, report, "models/apq3011/crate.md3")
	skin, found := files["models/apq3011/crate.tga"]
	if !found || !skin.Found() || skin.Role != "model image" {
		t.Errorf("the model's own skin is not in its resolution: %+v", files)
	}

	// Without the skin, the model is what is reported missing.
	content := t.TempDir()
	copyTree(t, q3011Content, content)
	if err := os.Remove(filepath.Join(content, "models/apq3011/crate.tga")); err != nil {
		t.Fatalf("%v", err)
	}
	report, err = q3deps.Discover([]string{q3011Map}, q3deps.Scan{ContentRoots: []string{content}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, resolution := range report.Resolutions {
		if resolution.Name == "models/apq3011/crate.md3" {
			if resolution.Status != q3deps.StatusMissing || !strings.Contains(resolution.Review, "models/apq3011/crate") {
				t.Errorf("a model whose skin is missing resolved to %q: %s", resolution.Status, resolution.Review)
			}
		}
	}
}

// A model this does not read says so on the model, and a model it does read
// does not carry the sentence — a review sentence on every model is one a
// person learns to click past.
func TestOnlyAModelThatWasNotReadCarriesTheSentence(t *testing.T) {
	content := t.TempDir()
	copyTree(t, q3011Content, content)
	if err := os.WriteFile(filepath.Join(content, "models/apq3011/crate.ase"), []byte("*3DSMAX_ASCIIEXPORT 200\n"), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	source := filepath.Join(t.TempDir(), "ase.map")
	text := "{\n\"classname\" \"worldspawn\"\n}\n{\n\"classname\" \"misc_model\"\n\"model\" \"models/apq3011/crate.ase\"\n}\n" +
		"{\n\"classname\" \"misc_model\"\n\"model\" \"models/apq3011/crate.md3\"\n}\n"
	if err := os.WriteFile(source, []byte(text), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	// Everything packaged, so the only review left is the sentence itself.
	members := map[string]string{}
	for _, name := range []string{"models/apq3011/crate.ase", "models/apq3011/crate.md3", "models/apq3011/crate.tga"} {
		members[name] = filepath.Join(content, name)
	}
	report, err := q3deps.Discover([]string{source}, q3deps.Scan{Members: members})
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, resolution := range report.Resolutions {
		switch resolution.Name {
		case "models/apq3011/crate.ase":
			if !strings.Contains(resolution.Review, "`.md3` files only") {
				t.Errorf("an .ase model was not held for review: %q", resolution.Review)
			}
		case "models/apq3011/crate.md3":
			if resolution.Review != "" {
				t.Errorf("an .md3 that was read is held for review: %q", resolution.Review)
			}
		}
	}
}

// Measured with Q3Map2 2.5.17n (`Q3_011`): the compiler warns when a shader's
// editor image is absent and is silent when its stage image is, so the two are
// dependencies of different programs.
func TestAShaderImageSaysWhichProgramReadsIt(t *testing.T) {
	report, err := q3deps.Discover([]string{q3011Map}, q3deps.Scan{ContentRoots: []string{q3011Content}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	files := filesOf(t, report, "textures/apq3011/glow")
	editor, stage, script := files["textures/apq3011/glow_editor.tga"], files["textures/apq3011/glow_stage.tga"], files["scripts/apq3011.shader"]
	if !editor.CompileOnly || editor.RuntimeOnly {
		t.Errorf("the editor image: %+v", editor)
	}
	if !stage.RuntimeOnly || stage.CompileOnly {
		t.Errorf("the stage image: %+v", stage)
	}
	if script.CompileOnly || script.RuntimeOnly {
		t.Errorf("the shader script is read by both programs: %+v", script)
	}
}

// A file that is not a compiled Quake III map is refused by name rather than
// read as an empty one, which would be a package with no dependencies.
func TestAFileThatIsNotACompiledMapIsRefused(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(q3011BSP)
	if err != nil {
		t.Fatalf("%v", err)
	}
	cases := map[string][]byte{
		"empty":     {},
		"other":     []byte("IBSP\x26\x00\x00\x00" + strings.Repeat("\x00", 200)),
		"placebo":   []byte("not a bsp at all, and long enough to hold a header .................................................................................................................................."),
		"truncated": data[:len(data)/2],
	}
	for name, content := range cases {
		path := filepath.Join(dir, name+".bsp")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("%v", err)
		}
		if _, err := q3deps.ParseBSP(path); err == nil {
			t.Errorf("%s was read as a compiled map", name)
		}
	}
}

// A damaged model is said, on the model, and never read as one with no shaders.
func TestADamagedModelIsNotedRatherThanTrusted(t *testing.T) {
	content := t.TempDir()
	copyTree(t, q3011Content, content)
	model := filepath.Join(content, "models/apq3011/crate.md3")
	data, err := os.ReadFile(model)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(model, data[:150], 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	report, err := q3deps.Discover([]string{q3011Map}, q3deps.Scan{ContentRoots: []string{content}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, resolution := range report.Resolutions {
		if resolution.Name == "models/apq3011/crate.md3" && !strings.Contains(resolution.Review, "not a readable MD3") {
			t.Errorf("a truncated model is not held for review: %+v", resolution)
		}
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
}
