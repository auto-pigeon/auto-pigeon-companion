package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// Q2_008: the Quake II setup rows a build through the CLI can show without a
// browser — the texinfo dependency (C6), a leak then its corrected copy (C9),
// a base folder that is the one above baseq2, and a stage token that repeats
// a switch the action manages (C7).

type q2BuildManifest struct {
	BuildID   string `json:"build_id"`
	State     string `json:"state"`
	Error     string `json:"error"`
	Directory string `json:"directory"`
	Steps     []struct {
		ID           string `json:"id"`
		State        string `json:"state"`
		JobID        string `json:"job_id"`
		Error        string `json:"error"`
		FailureClass string `json:"failure_class"`
		Skipped      bool   `json:"skipped"`
		Outputs      []struct {
			Name    string `json:"name"`
			Path    string `json:"path"`
			SHA256  string `json:"sha256"`
			Missing bool   `json:"missing"`
		} `json:"outputs"`
	} `json:"steps"`
	Outputs []struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Missing bool   `json:"missing"`
	} `json:"outputs"`
}

// q2SetupEnv binds the upstream Quake II toolchain to the fixture programs and
// to a base folder, and returns an environment to run builds in.
func q2SetupEnv(t *testing.T, game string) *Env {
	t.Helper()
	env, _, _ := testEnv(t)
	self := mustExecutable(t)
	toolDir := filepath.Join(t.TempDir(), "ericw tools 2")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exeSuffix := ""
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
	executables := map[string]string{}
	for _, name := range q2ToolNames {
		path, err := linkAsTool(self, toolDir, name, exeSuffix)
		if err != nil {
			t.Fatal(err)
		}
		executables[name] = path
	}
	bindTool(t, env, builtin.EricwQ2, executables, map[string]string{profile.RootGame: game, profile.RootContent: game})
	return env
}

func q2RunBuild(t *testing.T, env *Env, pipeline, mapText string) (q2BuildManifest, int, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "my map", "level.map")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(mapText), 0o600); err != nil {
		t.Fatal(err)
	}
	run, stdout, stderr := testEnvAt(t, env.ConfigPath)
	code := Run(run, []string{"build", "run", "--json", "--pipeline", pipeline, "--input", "source_map=" + source})
	var manifest q2BuildManifest
	if body := manifestJSON(t, stdout.String()); len(body) > 0 {
		if err := json.Unmarshal(body, &manifest); err != nil {
			t.Fatalf("the manifest is not JSON: %v\n%s", err, stdout)
		}
	}
	return manifest, code, stdout.String() + stderr.String()
}

func q2Step(m q2BuildManifest, id string) (state, err, class string, skipped bool, jobID string) {
	for _, step := range m.Steps {
		if step.ID == id {
			return step.State, step.Error, step.FailureClass, step.Skipped, step.JobID
		}
	}
	return "", "", "", false, ""
}

// C6: a compile that left no `.texinfo.json` stops the build at light, by name.
// Before Q2_008 light ran without it and the build succeeded — with, as
// measured on release 1.4518, a lightmap lit with default flags.
func TestAQuake2CompileThatLeftNoTexinfoStopsTheBuildAtLightByName(t *testing.T) {
	env := q2SetupEnv(t, gameDataRoot(t))
	broken := strings.Replace(syntheticQ2Map, `"message" "Auto-Pigeon Companion Q2 fixture"`, `"message" "aucom-no-texinfo"`, 1)
	if broken == syntheticQ2Map {
		t.Fatal("the fixture map has no message to replace")
	}
	manifest, code, out := q2RunBuild(t, env, builtin.Q2Normal, broken)
	if code == 0 || manifest.State == "succeeded" {
		t.Fatalf("a Quake II build whose compile left no texinfo succeeded:\n%s", out)
	}
	if state, _, _, _, _ := q2Step(manifest, "compile"); state != "succeeded" {
		t.Errorf("compile is %q; the dependency failure belongs to light", state)
	}
	if state, _, _, _, _ := q2Step(manifest, "vis"); state != "succeeded" {
		t.Errorf("vis is %q", state)
	}
	state, message, class, _, jobID := q2Step(manifest, "light")
	if state == "succeeded" || jobID != "" {
		t.Errorf("light ran (state %q, job %q) without the texinfo", state, jobID)
	}
	if !strings.Contains(message, `"texinfo"`) || !strings.Contains(message, "compile.texinfo") || class != "output_missing" {
		t.Errorf("light's failure does not name the missing dependency: %q (class %q)", message, class)
	}
}

// C9: a leaking map, then its corrected copy. The second build has its own
// directory and jobs; nothing of the first — its point file above all — is in
// it, and the corrected map is lit.
func TestAQuake2LeakThenItsCorrectedCopyShareNoArtifacts(t *testing.T) {
	env := q2SetupEnv(t, gameDataRoot(t))
	leaking := strings.Replace(syntheticQ2Map, `"message" "Auto-Pigeon Companion Q2 fixture"`, `"message" "aucom-leak"`, 1)
	first, code, out := q2RunBuild(t, env, builtin.Q2FastPreview, leaking)
	if code == 0 || first.State == "succeeded" {
		t.Fatalf("the leaking map built:\n%s", out)
	}
	pts := ""
	for _, output := range first.Outputs {
		if output.Name == "pts" && !output.Missing {
			pts = output.Path
		}
	}
	if pts == "" {
		t.Fatalf("the leaking build published no point file:\n%s", out)
	}

	second, code, out := q2RunBuild(t, env, builtin.Q2FastPreview, syntheticQ2Map)
	if code != 0 || second.State != "succeeded" {
		t.Fatalf("the corrected copy did not build:\n%s", out)
	}
	if second.BuildID == first.BuildID || second.Directory == first.Directory {
		t.Fatalf("the two builds share an identity: %s / %s", first.BuildID, second.BuildID)
	}
	firstJob := map[string]bool{}
	for _, step := range first.Steps {
		if step.JobID != "" {
			firstJob[step.JobID] = true
		}
	}
	for _, step := range second.Steps {
		if step.JobID == "" || firstJob[step.JobID] {
			t.Errorf("step %s of the corrected build ran as job %q", step.ID, step.JobID)
		}
		for _, output := range step.Outputs {
			if !output.Missing && !strings.HasPrefix(output.Path, second.Directory) {
				t.Errorf("step %s's %s is outside its own build: %s", step.ID, output.Name, output.Path)
			}
			if output.Name == "pts" && !output.Missing {
				t.Errorf("the corrected build has a point file: %s", output.Path)
			}
		}
	}
	for _, output := range second.Outputs {
		if output.Name == "pts" && !output.Missing {
			t.Errorf("the corrected build published a point file: %s", output.Path)
		}
	}
	if _, err := os.Stat(pts); err != nil {
		t.Errorf("the first build's point file is gone: %v", err)
	}
}

// The folder ABOVE baseq2 is refused before a compiler starts, by Build &
// Run's listing and by the build, with the same words.
func TestAQuake2BaseFolderAboveTheGameIsRefusedBeforeAnythingRuns(t *testing.T) {
	above := t.TempDir() // holds neither pak0.pak nor pics/colormap.pcx
	if err := os.MkdirAll(filepath.Join(above, "baseq2"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := q2SetupEnv(t, above)

	list, stdout, stderr := testEnvAt(t, env.ConfigPath)
	if code := Run(list, []string{"build", "pipelines"}); code != 0 {
		t.Fatalf("build pipelines: %d\n%s%s", code, stdout, stderr)
	}
	listing := stdout.String()
	if !strings.Contains(listing, builtin.Q2Normal) || !strings.Contains(listing, "pak0.pak or pics/colormap.pcx") {
		t.Errorf("the listing does not say why the Quake II pipelines cannot run:\n%s", listing)
	}

	manifest, code, out := q2RunBuild(t, env, builtin.Q2Normal, syntheticQ2Map)
	if code == 0 || manifest.State == "succeeded" {
		t.Fatalf("a build against the folder above baseq2 succeeded:\n%s", out)
	}
	if !strings.Contains(out, "pak0.pak or pics/colormap.pcx") {
		t.Errorf("the refusal does not name what the folder lacks:\n%s", out)
	}
	for _, step := range manifest.Steps {
		if step.JobID != "" && step.ID != "compile" {
			t.Errorf("step %s ran", step.ID)
		}
	}
}

// C7 through the CLI: a stage token repeating `-threads` is refused where it is
// saved; a token the action does not pass is kept.
func TestAQuake2StageTokenRepeatingAManagedSwitchIsRefusedWhereItIsSaved(t *testing.T) {
	env := q2SetupEnv(t, gameDataRoot(t))
	run, stdout, stderr := testEnvAt(t, env.ConfigPath)
	if code := Run(run, []string{"toolchain", "args", builtin.Q2Normal, "vis", "--set=-threads", "--set=8"}); code == 0 {
		t.Fatalf("a second -threads was saved for vis:\n%s%s", stdout, stderr)
	}
	if !strings.Contains(stderr.String(), "(threads)") {
		t.Errorf("the refusal does not name the parameter that sets it:\n%s", stderr)
	}
	run, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(run, []string{"toolchain", "args", builtin.Q2Normal, "vis", "--set=-nopercent"}); code != 0 {
		t.Fatalf("-nopercent was refused:\n%s%s", stdout, stderr)
	}
	manifest, code, out := q2RunBuild(t, env, builtin.Q2Normal, syntheticQ2Map)
	if code != 0 || manifest.State != "succeeded" {
		t.Fatalf("the build with -nopercent on vis failed:\n%s", out)
	}
}
