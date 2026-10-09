package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// Q3_010: a Quake III stage is judged by what it produced and by what the
// compiler said, not by its exit status; the compiler reads only what was
// staged for it; and a failure says what KIND it is.
//
// The compiler here is the fixture — this test binary, behaving as Q3Map2
// 2.5.17n was measured to (q3fixture_test.go). What the real program does with
// each of these inputs is in README "Quake III builds", measured on Linux.

// q3Build runs one Quake III build and returns its manifest and exit code.
func q3Build(t *testing.T, env *Env, source string, extra ...string) (*build.Manifest, int, string) {
	t.Helper()
	env, stdout, stderr := testEnvAt(t, env.ConfigPath)
	args := append([]string{"build", "run", "--json", "--quiet",
		"--pipeline", builtin.Q3FastPreview, "--input", "source_map=" + source}, extra...)
	code := Run(env, args)
	out := stdout.String()
	if !strings.Contains(out, `"schema_version"`) {
		return nil, code, stderr.String()
	}
	var manifest build.Manifest
	if err := json.Unmarshal(manifestJSON(t, out), &manifest); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return &manifest, code, stderr.String()
}

func q3Source(t *testing.T, name, marker string) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	body := syntheticQ3Map
	if marker != "" {
		body += "\n// " + marker + "\n"
	}
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return source
}

// ranSteps is how many steps became a process.
func ranSteps(m *build.Manifest) int {
	n := 0
	for _, step := range m.Steps {
		if step.JobID != "" {
			n++
		}
	}
	return n
}

func TestAQuake3BuildReadsOnlyWhatWasStagedForIt(t *testing.T) {
	env, _, _ := testEnv(t)
	contentRoot, _ := q3Content(t)
	bindQ3Map2(t, env, contentRoot, contentRoot)
	// Something beside `baseq3` in the approved folder, which is not content.
	if err := os.WriteFile(filepath.Join(contentRoot, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, code, stderr := q3Build(t, env, q3Source(t, "staged.map", ""))
	if code != 0 || manifest == nil {
		t.Fatalf("exit code = %d\n%s", code, stderr)
	}
	if manifest.SchemaVersion != build.SchemaVersion {
		t.Errorf("schema = %q", manifest.SchemaVersion)
	}
	if manifest.GameData == nil || manifest.GameData.BaseGame != "baseq3" || manifest.GameData.FSGame != "" {
		t.Fatalf("game data = %+v", manifest.GameData)
	}

	// Every base path, in every stage, is inside this build's own directory —
	// and none is the folder the user approved.
	staged := filepath.Join(manifest.Directory, "vfs") + string(filepath.Separator)
	for _, step := range manifest.Steps {
		if step.Command == nil {
			t.Fatalf("the %s step recorded no command", step.ID)
		}
		seen := 0
		for i, arg := range step.Command.Args {
			if arg != "-fs_basepath" {
				continue
			}
			seen++
			path := step.Command.Args[i+1]
			if !strings.HasPrefix(path, staged) {
				t.Errorf("%s: -fs_basepath %s is not under %s", step.ID, path, staged)
			}
		}
		if seen != 2 {
			t.Errorf("%s sent %d base paths, want 2", step.ID, seen)
		}
		if strings.Contains(strings.Join(step.Command.Args, "\x00"), contentRoot) {
			t.Errorf("%s was handed the approved folder itself: %v", step.ID, step.Command.Args)
		}
	}
	if _, err := os.Stat(filepath.Join(manifest.Directory, "vfs", "content_root", "notes.txt")); err == nil {
		t.Error("a file beside baseq3 was staged")
	}
	if _, err := os.Stat(filepath.Join(manifest.Directory, "vfs", "content_root", "baseq3", "scripts", "aucom.shader")); err != nil {
		t.Errorf("the shader script was not staged: %v", err)
	}

	// The folders came from the tool's binding, and the manifest says so.
	if len(manifest.Roots) != 2 {
		t.Fatalf("roots = %+v", manifest.Roots)
	}
	for _, root := range manifest.Roots {
		if root.Source == nil || root.Source.Kind != build.RootFromBinding || root.Path != contentRoot {
			t.Errorf("root %s = %+v", root.Role, root)
		}
	}
}

func TestAQuake3ModDirectoryIsANameEveryStageAgreesOn(t *testing.T) {
	env, _, _ := testEnv(t)
	contentRoot, _ := q3Content(t)
	bindQ3Map2(t, env, contentRoot, contentRoot)
	if err := os.MkdirAll(filepath.Join(contentRoot, "themod", "textures"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := q3Source(t, "mod.map", "")
	all := func(name string) []string {
		return []string{"--option", "compile.mod=" + name, "--option", "vis.mod=" + name, "--option", "light.mod=" + name}
	}

	t.Run("a parent reference is refused before anything is created", func(t *testing.T) {
		// The profile's own option check is the first line: `..` is made of
		// permitted characters and is still not a name.
		manifest, code, stderr := q3Build(t, env, source, all("..")...)
		if code == 0 {
			t.Fatal("`-fs_game ..` built")
		}
		if manifest != nil && ranSteps(manifest) != 0 {
			t.Fatalf("%d step(s) ran", ranSteps(manifest))
		}
		if manifest != nil && manifest.FailureClass != failure.FSGameInvalid {
			t.Errorf("class = %q", manifest.FailureClass)
		}
		_ = stderr
	})

	t.Run("a mod no approved folder has is refused, and nothing runs", func(t *testing.T) {
		manifest, code, _ := q3Build(t, env, source, all("nosuchmod")...)
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		if manifest.FailureClass != failure.FSGameNotFound || ranSteps(manifest) != 0 {
			t.Errorf("class = %q, %d step(s) ran: %s", manifest.FailureClass, ranSteps(manifest), manifest.Error)
		}
	})

	t.Run("stages that name different mods are refused", func(t *testing.T) {
		manifest, code, _ := q3Build(t, env, source, "--option", "compile.mod=themod")
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		if manifest.FailureClass != failure.FSGameInvalid || ranSteps(manifest) != 0 {
			t.Errorf("class = %q, %d step(s) ran: %s", manifest.FailureClass, ranSteps(manifest), manifest.Error)
		}
	})

	t.Run("a mod that is there is staged and named to every stage", func(t *testing.T) {
		manifest, code, stderr := q3Build(t, env, source, all("themod")...)
		if code != 0 || manifest == nil {
			t.Fatalf("exit code = %d\n%s", code, stderr)
		}
		if manifest.GameData.FSGame != "themod" {
			t.Errorf("fs_game = %q", manifest.GameData.FSGame)
		}
		for _, step := range manifest.Steps {
			joined := strings.Join(step.Command.Args, " ")
			if !strings.Contains(joined, "-fs_game themod") {
				t.Errorf("%s did not send -fs_game themod: %s", step.ID, joined)
			}
		}
		if _, err := os.Stat(filepath.Join(manifest.Directory, "vfs", "content_root", "themod")); err != nil {
			t.Errorf("the mod directory was not staged: %v", err)
		}
	})
}

func TestAQuake3StageIsJudgedByWhatItProducedAndSaid(t *testing.T) {
	bound := func(t *testing.T) (*Env, string) {
		env, _, _ := testEnv(t)
		contentRoot, _ := q3Content(t)
		bindQ3Map2(t, env, contentRoot, contentRoot)
		return env, contentRoot
	}

	// Exit 0, no BSP: the class is the compiler's own word for why.
	t.Run("a leak is classed as a leak, and its line file is published", func(t *testing.T) {
		env, _ := bound(t)
		manifest, code, _ := q3Build(t, env, q3Source(t, "leaky.map", "aucom_leak_me"))
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		if manifest.State != job.Failed || manifest.FailureClass != "leak" {
			t.Errorf("state %s, class %q: %s", manifest.State, manifest.FailureClass, manifest.Error)
		}
		if step := manifest.Steps[0]; step.ExitCode == nil || *step.ExitCode != 0 || step.FailureClass != "leak" {
			t.Errorf("the compile step: exit %v, class %q", step.ExitCode, step.FailureClass)
		}
		published := false
		for _, output := range manifest.Outputs {
			if output.Name == "lin" && !output.Missing {
				published = true
				if _, err := os.Stat(output.Path); err != nil {
					t.Errorf("the leak file is recorded and not there: %v", err)
				}
			}
			if output.Name == "bsp" && !output.Missing {
				t.Error("a leaked build published a BSP")
			}
		}
		if !published {
			t.Errorf("the leak line file was not published: %+v", manifest.Outputs)
		}
	})

	// Exit 0, BSP written, a model dropped: the profile's `fatal` rule.
	t.Run("a missing model fails the compile although it exited 0 and wrote a BSP", func(t *testing.T) {
		env, _ := bound(t)
		manifest, code, _ := q3Build(t, env, q3Source(t, "model.map", "aucom_missing_model"))
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		step := manifest.Steps[0]
		if step.State != job.Failed || step.FailureClass != "model_missing" || manifest.FailureClass != "model_missing" {
			t.Fatalf("step %s class %q, build class %q: %s", step.State, step.FailureClass, manifest.FailureClass, step.Error)
		}
		if step.ExitCode == nil || *step.ExitCode != 0 {
			t.Errorf("exit code = %v; the point is that it was 0", step.ExitCode)
		}
		if !strings.Contains(step.Error, "models/aucom/nothere.md3") {
			t.Errorf("the failure does not quote the compiler's line: %s", step.Error)
		}
		if !manifest.Steps[1].Skipped || !manifest.Steps[2].Skipped {
			t.Error("a later stage ran on a BSP with a model missing")
		}
		for _, output := range manifest.Outputs {
			if output.Name == "bsp" && !output.Missing {
				t.Error("the build published a BSP")
			}
		}
	})

	// Exit 0, every file present, one of them not what it says.
	t.Run("an output that is present and wrong fails the stage that wrote it", func(t *testing.T) {
		env, _ := bound(t)
		manifest, code, _ := q3Build(t, env, q3Source(t, "srf.map", "aucom_empty_srf"))
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		step := manifest.Steps[0]
		if step.FailureClass != failure.OutputInvalid || !strings.Contains(step.Error, `"srf"`) {
			t.Fatalf("class %q: %s", step.FailureClass, step.Error)
		}
		if !manifest.Steps[2].Skipped {
			t.Error("the lighting stage ran on an empty surface file")
		}
	})

	// A missing image stays what Q3_006 measured it to be: a warning, a BSP,
	// and a build that says it is not a complete result — now with a class.
	t.Run("a missing image is a classed warning, not a failure", func(t *testing.T) {
		env, _ := bound(t)
		source := filepath.Join(t.TempDir(), "image.map")
		body := strings.ReplaceAll(syntheticQ3Map, "aucom/floor", "aucom/nosuchtexture")
		if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		manifest, code, stderr := q3Build(t, env, source)
		if code != 0 || manifest == nil {
			t.Fatalf("exit code = %d\n%s", code, stderr)
		}
		if manifest.Warnings() == 0 || manifest.FailureClass != "" {
			t.Errorf("warnings %d, class %q", manifest.Warnings(), manifest.FailureClass)
		}
		classed := false
		for _, d := range manifest.Steps[0].Diagnostics {
			if d.Class == "shader_image_missing" {
				classed = true
			}
		}
		if !classed {
			t.Errorf("no diagnostic carries the class: %+v", manifest.Steps[0].Diagnostics)
		}
	})
}

func TestAQuake3BuildSaysWhatKindOfThingStoppedIt(t *testing.T) {
	t.Run("a damaged archive", func(t *testing.T) {
		env, _, _ := testEnv(t)
		contentRoot, vfs := q3Content(t)
		bindQ3Map2(t, env, contentRoot, contentRoot)
		if err := os.WriteFile(filepath.Join(vfs, "textures.pk3"), []byte("PK\x03\x04 cut off"), 0o600); err != nil {
			t.Fatal(err)
		}
		manifest, code, _ := q3Build(t, env, q3Source(t, "a.map", ""))
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		if manifest.FailureClass != failure.ArchiveDamaged || ranSteps(manifest) != 0 {
			t.Errorf("class %q, %d step(s) ran: %s", manifest.FailureClass, ranSteps(manifest), manifest.Error)
		}
		if !strings.Contains(manifest.Error, "textures.pk3") {
			t.Errorf("the archive is not named: %s", manifest.Error)
		}
	})

	t.Run("game data that is not there", func(t *testing.T) {
		env, _, _ := testEnv(t)
		contentRoot, vfs := q3Content(t)
		// The classic mistake: the folder chosen is `baseq3` itself.
		bindQ3Map2(t, env, vfs, contentRoot)
		manifest, code, _ := q3Build(t, env, q3Source(t, "b.map", ""))
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		if manifest.FailureClass != failure.GameDataMissing || ranSteps(manifest) != 0 {
			t.Errorf("class %q, %d step(s) ran: %s", manifest.FailureClass, ranSteps(manifest), manifest.Error)
		}
	})

	t.Run("a link out of the approved folder", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating a symbolic link needs a privilege on Windows")
		}
		env, _, _ := testEnv(t)
		contentRoot, vfs := q3Content(t)
		bindQ3Map2(t, env, contentRoot, contentRoot)
		outside := filepath.Join(t.TempDir(), "elsewhere.tga")
		if err := os.WriteFile(outside, []byte("not approved"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(vfs, "textures", "aucom", "escape.tga")); err != nil {
			t.Fatal(err)
		}
		manifest, code, _ := q3Build(t, env, q3Source(t, "c.map", ""))
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		if manifest.FailureClass != failure.ContentRefused || ranSteps(manifest) != 0 {
			t.Errorf("class %q, %d step(s) ran: %s", manifest.FailureClass, ranSteps(manifest), manifest.Error)
		}
	})

	t.Run("a compiler that is not there", func(t *testing.T) {
		env, _, _ := testEnv(t)
		contentRoot, _ := q3Content(t)
		bindQ3Map2(t, env, contentRoot, contentRoot)
		// Bound, then removed from under the binding: what an uninstalled or
		// moved NetRadiant looks like.
		settings := mustBinding(t, env, builtin.Q3Map2)
		if err := os.Remove(settings.Executables[q3ToolName]); err != nil {
			t.Fatal(err)
		}
		manifest, code, _ := q3Build(t, env, q3Source(t, "d.map", ""))
		if code == 0 || manifest == nil {
			t.Fatalf("exit code = %d", code)
		}
		if manifest.FailureClass != failure.ToolUnavailable {
			t.Errorf("class %q: %s", manifest.FailureClass, manifest.Error)
		}
	})
}

// mustBinding reads one profile's binding back out of the test's own config.
func mustBinding(t *testing.T, env *Env, profileID string) binding.LocalBinding {
	t.Helper()
	set, err := binding.LoadFile(filepath.Join(filepath.Dir(env.ConfigPath), "bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	local, found := set.Find(profileID)
	if !found {
		t.Fatalf("%s is not bound", profileID)
	}
	return local
}

// The review is where a missing folder is said. A preview that answered
// "everything is in place" for a build the run then refused is a preview that
// was not about that build (seen live, Q3_010: the Build page's third step).
func TestAQuake3PreviewRefusesWhatTheRunWouldRefuse(t *testing.T) {
	env, _, _ := testEnv(t)
	contentRoot, _ := q3Content(t)
	bindQ3Map2(t, env, contentRoot, contentRoot)
	// The content folder is taken away again: only the base game data is set.
	local := mustBinding(t, env, builtin.Q3Map2)
	bindTool(t, env, builtin.Q3Map2, local.Executables, map[string]string{"game_root": contentRoot})

	source := q3Source(t, "preview.map", "")
	env, stdout, stderr := testEnvAt(t, env.ConfigPath)
	code := Run(env, []string{"build", "preview", "--pipeline", builtin.Q3FastPreview, "--input", "source_map=" + source})
	if code == 0 {
		t.Fatalf("the preview accepted a build with no content folder:\n%s", stdout)
	}
	said := stderr.String() + stdout.String()
	if !strings.Contains(said, "content folder") || strings.Contains(said, `"content_root"`) {
		t.Errorf("the refusal does not name the folder in a person's words:\n%s", said)
	}

	// A folder given to this build is enough, and the preview shows where the
	// compiler would be pointed: the build's own staged directory.
	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	code = Run(env, []string{"build", "preview", "--pipeline", builtin.Q3FastPreview,
		"--input", "source_map=" + source, "--root", "content_root=" + contentRoot})
	if code != 0 {
		t.Fatalf("exit code = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), filepath.Join("<build>", "vfs", "content_root")) {
		t.Errorf("the preview does not show the staged directory:\n%s", stdout)
	}
}

// `Q3_012B` §2. A map names its terrain index image by bare file name
// (`alphamap`), and its authors keep that image beside the `.map`. Q3Map2
// 2.5.17n was measured to look for it in the game data only, so the build of a
// FILE map does not copy a sibling of the map anywhere: it would be compiling a
// map the compiler itself refuses in place, and a build of a saved revision —
// which has no "beside" at all — would then behave differently from a build of
// the file it was saved from. What the build owes instead is to say what kind
// of thing stopped it, and where the file has to be.
//
// Authored here: the synthetic room and a terrain entity naming `aucom_index.pcx`.
func TestAFileAMapNamesIsLookedForInTheGameDataAndItsAbsenceIsClassed(t *testing.T) {
	const terrain = "\n{\n\"classname\" \"func_group\"\n\"terrain\" \"1\"\n\"alphamap\" \"aucom_index.pcx\"\n\"layers\" \"2\"\n\"shader\" \"aucom/terrain\"\n}\n"
	source := func(t *testing.T) string {
		path := q3Source(t, "terrain.map", "")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(body, terrain...), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	finding := func(m *build.Manifest) *job.Diagnostic {
		for _, step := range m.Steps {
			for i := range step.Diagnostics {
				if step.Diagnostics[i].Class == "map_file_missing" {
					return &step.Diagnostics[i]
				}
			}
		}
		return nil
	}

	for _, beside := range []bool{false, true} {
		name := "the file is nowhere"
		if beside {
			name = "the file is beside the map, where the compiler does not look"
		}
		t.Run(name, func(t *testing.T) {
			env, _, _ := testEnv(t)
			contentRoot, _ := q3Content(t)
			bindQ3Map2(t, env, contentRoot, contentRoot)
			path := source(t)
			if beside {
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "aucom_index.pcx"), []byte("an index image"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			manifest, code, _ := q3Build(t, env, path)
			if code == 0 || manifest == nil {
				t.Fatalf("exit code = %d", code)
			}
			if manifest.State != job.Failed || manifest.FailureClass != "map_file_missing" {
				t.Errorf("state %s, class %q (want map_file_missing, not the bare %s): %s",
					manifest.State, manifest.FailureClass, failure.ToolFailed, manifest.Error)
			}
			found := finding(manifest)
			if found == nil {
				t.Fatalf("the compiler's own line is not a finding: %+v", manifest.Steps[0].Diagnostics)
			}
			if !strings.Contains(found.Raw, "aucom_index.pcx") {
				t.Errorf("the finding does not carry the line that names the file: %q", found.Raw)
			}
			if !strings.Contains(found.Message, "names") || !strings.Contains(found.Hint, "game directory") ||
				!strings.Contains(found.Hint, "beside the map") {
				t.Errorf("the finding does not say where the file has to be: %q / %q", found.Message, found.Hint)
			}
		})
	}

	t.Run("the file is in the game data, at the path the key names", func(t *testing.T) {
		env, _, _ := testEnv(t)
		contentRoot, vfs := q3Content(t)
		if err := os.WriteFile(filepath.Join(vfs, "aucom_index.pcx"), []byte("an index image"), 0o600); err != nil {
			t.Fatal(err)
		}
		bindQ3Map2(t, env, contentRoot, contentRoot)
		manifest, code, stderr := q3Build(t, env, source(t))
		if code != 0 || manifest == nil || manifest.State != job.Succeeded {
			t.Fatalf("exit code = %d: %s", code, stderr)
		}
		if finding(manifest) != nil {
			t.Error("a build that found the file still reports it missing")
		}
	})
}
