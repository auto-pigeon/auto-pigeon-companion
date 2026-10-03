package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// Q3_018: the Quake III leak test is the BSP stage alone, and what it leaves —
// the line file and everything the compiler printed — is collected from the
// job's own fresh workspace whether the step succeeded or not.

// q3LeakTest runs the leak-test pipeline on one source.
func q3LeakTest(t *testing.T, env *Env, source string) (*build.Manifest, int) {
	t.Helper()
	manifest, code, stderr := q3Build(t, env, source, "--pipeline", builtin.Q3LeakTest)
	if manifest == nil {
		t.Fatalf("no manifest (exit %d): %s", code, stderr)
	}
	return manifest, code
}

// leakOutputs reads a leak build's evidence the way the result envelope does.
func leakOutputs(t *testing.T, manifest *build.Manifest) leakadapter.Evidence {
	t.Helper()
	evidence := leakadapter.Evidence{}
	for _, step := range manifest.Steps {
		if step.ID == "compile" {
			evidence.ExitCode, evidence.StepState = step.ExitCode, string(step.State)
		}
	}
	for _, output := range manifest.Outputs {
		present := !output.Missing && output.Path != ""
		switch output.Name {
		case "bsp":
			evidence.BSP = &present
		case "lin", "compile_log":
			if !present {
				continue
			}
			raw, err := os.ReadFile(output.Path)
			if err != nil {
				t.Fatalf("%s is recorded and not there: %v", output.Name, err)
			}
			if !strings.HasPrefix(output.Path, manifest.Directory+string(os.PathSeparator)) {
				t.Errorf("%s was published outside the build: %s", output.Name, output.Path)
			}
			if output.Name == "lin" {
				evidence.Pointfile = string(raw)
			} else {
				evidence.Log = string(raw)
			}
		}
	}
	return evidence
}

func TestTheQuake3LeakTestRunsTheBSPStageAloneAndKeepsWhatItSaid(t *testing.T) {
	bound := func(t *testing.T) *Env {
		env, _, _ := testEnv(t)
		contentRoot, _ := q3Content(t)
		bindQ3Map2(t, env, contentRoot, contentRoot)
		return env
	}
	argv := func(manifest *build.Manifest) string {
		if len(manifest.Steps) != 1 || manifest.Steps[0].ID != "compile" || manifest.Steps[0].Command == nil {
			t.Fatalf("the leak test is not one compile step: %+v", manifest.Steps)
		}
		return " " + strings.Join(manifest.Steps[0].Command.Args, " ") + " "
	}

	t.Run("a leak: exit 0, a failed step, a fresh line file and the log, all kept", func(t *testing.T) {
		manifest, code := q3LeakTest(t, bound(t), q3Source(t, "leaky.map", "aucom_leak_me"))
		if code == 0 || manifest.State != job.Failed {
			t.Fatalf("exit %d, state %s", code, manifest.State)
		}
		args := argv(manifest)
		for _, want := range []string{" -bsp ", " -leaktest ", " -meta ", " -threads 4 ", " -v ", " -fs_homepath "} {
			if !strings.Contains(args, want) {
				t.Errorf("the command lacks%s: %s", want, args)
			}
		}
		for _, never := range []string{" -vis ", " -light ", " -fast "} {
			if strings.Contains(args, never) {
				t.Errorf("a leak test ran%s: %s", never, args)
			}
		}
		evidence := leakOutputs(t, manifest)
		if evidence.ExitCode == nil || *evidence.ExitCode != 0 || evidence.StepState != "failed" {
			t.Errorf("exit %v, step %s: the process exit must be recorded as it was", evidence.ExitCode, evidence.StepState)
		}
		if evidence.BSP == nil || *evidence.BSP {
			t.Error("a leaked test published a BSP")
		}
		verdict := leakadapter.ClassifyQ3Map2(evidence)
		if verdict.Outcome != leakadapter.OutcomeLeak || verdict.RoutePoints != 3 || !verdict.Has(leakadapter.EvRoute) {
			t.Errorf("verdict %+v from log:\n%s", verdict, evidence.Log)
		}
	})

	t.Run("a sealed map: no leak, with the BSP as evidence", func(t *testing.T) {
		manifest, code := q3LeakTest(t, bound(t), q3Source(t, "sealed.map", ""))
		if code != 0 || manifest.State != job.Succeeded {
			t.Fatalf("exit %d, state %s: %s", code, manifest.State, manifest.Error)
		}
		argv(manifest)
		evidence := leakOutputs(t, manifest)
		if verdict := leakadapter.ClassifyQ3Map2(evidence); verdict.Outcome != leakadapter.OutcomeNoLeak {
			t.Errorf("verdict %+v from log:\n%s", verdict, evidence.Log)
		}
		if evidence.Pointfile != "" {
			t.Error("a sealed test published a line file")
		}
	})

	t.Run("nothing in open space: not tested, and not a leak", func(t *testing.T) {
		manifest, _ := q3LeakTest(t, bound(t), q3Source(t, "empty.map", "aucom_no_occupant"))
		evidence := leakOutputs(t, manifest)
		if verdict := leakadapter.ClassifyQ3Map2(evidence); manifest.State != job.Failed ||
			verdict.Outcome != leakadapter.OutcomeNoInterior || evidence.Pointfile != "" {
			t.Errorf("state %s, verdict %+v", manifest.State, verdict)
		}
	})

	// A line file left beside the user's own source by some earlier run is not
	// this job's: the job stages the one declared file into a fresh workspace.
	t.Run("a stale line file beside the source is never collected", func(t *testing.T) {
		source := q3Source(t, "sealed.map", "")
		stale := strings.TrimSuffix(source, ".map") + ".lin"
		if err := os.WriteFile(stale, []byte("1 1 1\n2 2 2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		manifest, _ := q3LeakTest(t, bound(t), source)
		evidence := leakOutputs(t, manifest)
		if evidence.Pointfile != "" {
			t.Fatalf("the stale line file was collected: %q", evidence.Pointfile)
		}
		if verdict := leakadapter.ClassifyQ3Map2(evidence); verdict.Outcome != leakadapter.OutcomeNoLeak {
			t.Errorf("verdict %+v", verdict)
		}
		if raw, err := os.ReadFile(stale); err != nil || string(raw) != "1 1 1\n2 2 2\n" {
			t.Errorf("the user's own file was touched: %q %v", raw, err)
		}
		entries, _ := os.ReadDir(filepath.Dir(source))
		if len(entries) != 2 {
			t.Errorf("the build wrote beside the user's source: %v", entries)
		}
	})

	t.Run("an ordinary Quake III build still requires its BSP", func(t *testing.T) {
		manifest, code, _ := q3Build(t, bound(t), q3Source(t, "leaky.map", "aucom_leak_me"))
		if code == 0 || manifest == nil || manifest.State != job.Failed || manifest.FailureClass != "leak" {
			t.Fatalf("the fast-preview build of a leaked map: exit %d, %+v", code, manifest)
		}
	})
}

// A Quake 1 map sent to the Quake III test, or the other way, is refused
// before anything runs: a pipeline does not read another game's map.
func TestALeakTestRefusesAMapOfAnotherGame(t *testing.T) {
	err := build.CheckConvertedGames(build.Request{Conversions: map[string]build.Conversion{
		"source_map": {Game: "quake1"}}}, "quake3")
	if err == nil || !strings.Contains(err.Error(), "quake1") {
		t.Fatalf("a Quake 1 map in a Quake III pipeline: %v", err)
	}
	if err := build.CheckConvertedGames(build.Request{Conversions: map[string]build.Conversion{
		"source_map": {Game: "quake3"}}}, "quake3"); err != nil {
		t.Fatalf("the right game: %v", err)
	}
	if err := build.CheckConvertedGames(build.Request{}, "quake3"); err != nil {
		t.Fatalf("a plain .map makes no claim: %v", err)
	}
}

// The authored controls, through the real pipeline and the INSTALLED Q3Map2.
//
// Skipped unless AUCOM_Q3MAP2 names the program: the unit suite must not need
// a compiler. `AUCOM_Q3MAP2=/path/to/usr/bin/q3map2 go test ./internal/cli -run InstalledQ3Map2`.
func TestTheInstalledQ3Map2AnswersTheAuthoredControls(t *testing.T) {
	tool := os.Getenv("AUCOM_Q3MAP2")
	if tool == "" {
		t.Skip("AUCOM_Q3MAP2 is not set; the installed compiler is not exercised")
	}
	controls, err := filepath.Abs(filepath.Join("..", "leakadapter", "testdata", "q3", "controls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		state   job.State
		outcome string
		points  int
	}{
		{"a_sealed", job.Succeeded, leakadapter.OutcomeNoLeak, 0},
		{"b_gap", job.Failed, leakadapter.OutcomeLeak, 3},
		{"c_outside_entity", job.Failed, leakadapter.OutcomeLeak, 2},
		{"d_no_occupant", job.Failed, leakadapter.OutcomeNoInterior, 0},
		{"d_in_solid", job.Failed, leakadapter.OutcomeNoInterior, 0},
		{"e_patch_cover", job.Failed, leakadapter.OutcomeLeak, 3},
		{"e_detail_cover", job.Failed, leakadapter.OutcomeLeak, 3},
		{"x_nonsolid_wall", job.Failed, leakadapter.OutcomeLeak, 3},
		{"x_missing_shader", job.Succeeded, leakadapter.OutcomeNoLeak, 0},
		{"f_malformed", job.Failed, leakadapter.OutcomeIncomplete, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, _, _ := testEnv(t)
			bindTool(t, env, builtin.Q3Map2, map[string]string{q3ToolName: tool}, map[string]string{
				profile.RootGame:    filepath.Join(controls, "gameroot"),
				profile.RootContent: filepath.Join(controls, "gameroot"),
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
			manifest, _ := q3LeakTest(t, env, source)
			evidence := leakOutputs(t, manifest)
			verdict := leakadapter.ClassifyQ3Map2(evidence)
			if manifest.State != c.state || verdict.Outcome != c.outcome || verdict.RoutePoints != c.points {
				t.Errorf("state %s, verdict %+v; want %s, %s with %d points\n%s",
					manifest.State, verdict, c.state, c.outcome, c.points, evidence.Log)
			}
			if c.name != "f_malformed" && (evidence.ExitCode == nil || *evidence.ExitCode != 0) {
				t.Errorf("exit %v, want the measured 0", evidence.ExitCode)
			}
			if c.name == "x_missing_shader" && !verdict.Has(leakadapter.EvShaderImageMissing) {
				t.Errorf("the qualification was lost: %v", verdict.Evidence)
			}
			if entries, _ := os.ReadDir(filepath.Dir(source)); len(entries) != 1 {
				t.Errorf("the build wrote beside the source: %v", entries)
			}
		})
	}
}
