package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// Q2_010: the Quake II leak test is qbsp's compile step alone, in its Quake II
// mode with its leak test on, and what it leaves — the point file and
// everything qbsp printed — is collected from the job's own fresh workspace
// whether the step succeeded or not.

// q2LeakRun runs the built-in Quake II leak test on one source.
func q2LeakRun(t *testing.T, env *Env, source string) (*build.Manifest, int) {
	t.Helper()
	manifest, code, stderr := q3Build(t, env, source, "--pipeline", builtin.Q2LeakTest)
	if manifest == nil {
		t.Fatalf("no manifest (exit %d): %s", code, stderr)
	}
	return manifest, code
}

// q2LeakOutputs reads a Quake II leak build's evidence the way the result
// envelope does: the step's own text, the point file, the BSP's header.
func q2LeakOutputs(t *testing.T, manifest *build.Manifest) leakadapter.Evidence {
	t.Helper()
	evidence := leakadapter.Evidence{}
	for _, step := range manifest.Steps {
		if step.ID == "compile" {
			evidence.ExitCode, evidence.StepState = step.ExitCode, string(step.State)
		}
	}
	for _, output := range manifest.Outputs {
		present := !output.Missing && output.Path != ""
		if output.Name == "bsp" {
			evidence.BSP = &present
		}
		if !present {
			continue
		}
		if !strings.HasPrefix(output.Path, manifest.Directory+string(os.PathSeparator)) {
			t.Errorf("%s was published outside the build: %s", output.Name, output.Path)
		}
		raw, err := os.ReadFile(output.Path)
		if err != nil {
			t.Fatalf("%s is recorded and not there: %v", output.Name, err)
		}
		switch output.Name {
		case "bsp":
			evidence.BSPFormat = leakadapter.BSPFormatOf(raw)
		case "pts":
			evidence.Pointfile = string(raw)
		case "compile_log":
			evidence.Log = string(raw)
		}
	}
	return evidence
}

// With the fixture's stand-in qbsp: one step, the Quake II switches and the
// leak test on, nothing of VIS or LIGHT, and the step's own text published as
// the log — never a Quake 1 option.
func TestTheQuake2LeakTestIsTheCompileStepAloneWithItsLeakTestOn(t *testing.T) {
	env, _, _ := testEnv(t)
	self := mustExecutable(t)
	toolDir := t.TempDir()
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
	base := gameDataRoot(t)
	bindTool(t, env, builtin.EricwQ2, executables, map[string]string{profile.RootGame: base, profile.RootContent: base})
	source := filepath.Join(t.TempDir(), "room.map")
	if err := os.WriteFile(source, []byte(syntheticQ2Map), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := q2LeakRun(t, env, source)
	if len(manifest.Steps) != 1 || manifest.Steps[0].ID != "compile" || manifest.Steps[0].Command == nil {
		t.Fatalf("the leak test is not one compile step: %+v", manifest.Steps)
	}
	args := " " + strings.Join(manifest.Steps[0].Command.Args, " ") + " "
	for _, want := range []string{" -q2bsp ", " -leaktest ", " -basedir ", " -gamedir ", " -nocolor "} {
		if !strings.Contains(args, want) {
			t.Errorf("the command lacks%s: %s", want, args)
		}
	}
	for _, never := range []string{" -bsp2 ", " -wadpath ", " -threads ", " -fast ", " -extra ", " -quiet "} {
		if strings.Contains(args, never) {
			t.Errorf("a Quake II leak test ran%s: %s", never, args)
		}
	}
	if manifest.EngineFamily != "quake2" || manifest.Pipeline.ID != builtin.Q2LeakTest {
		t.Errorf("family %q, pipeline %q", manifest.EngineFamily, manifest.Pipeline.ID)
	}
	if evidence := q2LeakOutputs(t, manifest); evidence.Log == "" {
		t.Error("the step's own text was not published as the leak test's log")
	}
	if entries, _ := os.ReadDir(filepath.Dir(source)); len(entries) != 1 {
		t.Errorf("the build wrote beside the user's source: %v", entries)
	}
}

// The authored controls, through the real pipeline and an INSTALLED qbsp.
//
// Skipped unless AUCOM_Q2_QBSP names the program and AUCOM_Q2_BASEDIR the
// base game folder holding pak0.pak: the unit suite must not need a compiler
// or the commercial game.
//
//	AUCOM_Q2_QBSP=/path/to/qbsp AUCOM_Q2_BASEDIR=/path/to/baseq2 go test ./internal/cli -run InstalledQ2Qbsp
func TestTheInstalledQ2QbspAnswersTheAuthoredControls(t *testing.T) {
	tool, base := os.Getenv("AUCOM_Q2_QBSP"), os.Getenv("AUCOM_Q2_BASEDIR")
	if tool == "" || base == "" {
		t.Skip("AUCOM_Q2_QBSP / AUCOM_Q2_BASEDIR are not set; the installed compiler is not exercised")
	}
	controls, err := filepath.Abs(filepath.Join("..", "leakadapter", "testdata", "q2", "controls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		state   job.State
		exit    int
		outcome string
		points  int
	}{
		{"a_sealed", job.Succeeded, 0, leakadapter.OutcomeNoLeak, 0},
		{"b_gap", job.Failed, 1, leakadapter.OutcomeLeak, 10},
		{"c_outside_light", job.Failed, 1, leakadapter.OutcomeLeak, 6},
		{"d_no_occupant", job.Succeeded, 0, leakadapter.OutcomeNoInterior, 0},
		{"e_in_solid", job.Succeeded, 0, leakadapter.OutcomeNoInterior, 0},
		{"f_detail_cover", job.Failed, 1, leakadapter.OutcomeLeak, 10},
		{"f_clip_cover", job.Failed, 1, leakadapter.OutcomeLeak, 10},
		{"f_window_cover", job.Failed, 1, leakadapter.OutcomeLeak, 10},
		{"g_truncated", job.Failed, 1, leakadapter.OutcomeIncomplete, 0},
		{"g_degenerate_plane", job.Failed, 1, leakadapter.OutcomeLeak, 6},
		{"h_missing_wal", job.Succeeded, 0, leakadapter.OutcomeIncomplete, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, _, _ := testEnv(t)
			bindTool(t, env, builtin.EricwQ2, map[string]string{"qbsp": tool}, map[string]string{
				profile.RootGame:    base,
				profile.RootContent: filepath.Join(controls, "content"),
			})
			// A copy: the build must never write beside the fixture.
			source := filepath.Join(t.TempDir(), c.name+".map")
			raw, err := os.ReadFile(filepath.Join(controls, "maps", c.name+".map"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			// I: a stale point file beside the user's source is never collected.
			stale := strings.TrimSuffix(source, ".map") + ".pts"
			if err := os.WriteFile(stale, []byte("1000 1000 1000\n1001 1000 1000\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest, _ := q2LeakRun(t, env, source)
			evidence := q2LeakOutputs(t, manifest)
			verdict := leakadapter.ClassifyEricwQ2(evidence)
			if manifest.State != c.state || verdict.Outcome != c.outcome || verdict.RoutePoints != c.points {
				t.Errorf("state %s, verdict %+v; want %s, %s with %d points\n%s",
					manifest.State, verdict, c.state, c.outcome, c.points, evidence.Log)
			}
			if evidence.ExitCode == nil || *evidence.ExitCode != c.exit {
				t.Errorf("exit %v, want the measured %d", evidence.ExitCode, c.exit)
			}
			if strings.Contains(evidence.Pointfile, "1000 1000 1000") {
				t.Error("the stale point file beside the source was collected")
			}
			if c.outcome == leakadapter.OutcomeNoLeak && evidence.BSPFormat != "IBSP:38" {
				t.Errorf("a pass on a BSP that is %q", evidence.BSPFormat)
			}
			if entries, _ := os.ReadDir(filepath.Dir(source)); len(entries) != 2 {
				t.Errorf("the build wrote beside the source: %v", entries)
			}
		})
	}
}
