package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
)

// `companion build`, from the outside.
//
// These run against a machine with nothing installed but the built-in
// documents, which is the state a user is in the first time they open the
// program. What that state can honestly answer is: what is here, what a build
// would run, and — for anything more — what is missing and what to do about it.

func TestBuildPipelinesListsTheBuiltInQ1Pipelines(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"build", "pipelines"}); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"auto-pigeon.q1.fast-preview",
		"auto-pigeon.q1.normal",
		"auto-pigeon.q1.final",
		"compile -> vis -> light",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing is missing %q:\n%s", want, out)
		}
	}
	// Every built-in pipeline resolves against the built-in toolchain, so none
	// of them is listed as needing something.
	if strings.Contains(out, "needs ") {
		t.Errorf("a built-in pipeline does not resolve on a fresh machine:\n%s", out)
	}
}

// The experimental Quake II pipelines are listed beside the Quake 1 ones, and
// they are listed as work in progress. A listing that offered them silently
// would be the generic `supported` badge `AUP/AUCOM 215` says must not survive.
func TestBuildPipelinesMarksTheQuake2PipelinesAsWorkInProgress(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"build", "pipelines"}); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"auto-pigeon.q2.fast-preview",
		"auto-pigeon.q2.normal",
		"Work in progress",
		maturity.Quake2Message,
		"companion feedback compatibility",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing is missing %q:\n%s", want, out)
		}
	}
	// And the sentence appears once, however many Quake II rows there are.
	if n := strings.Count(out, maturity.Quake2Message); n != 1 {
		t.Errorf("the work-in-progress sentence appears %d times; a warning printed per row stops being read", n)
	}
	// The Quake 1 rows carry no badge: a badge on everything is a badge nobody
	// reads, and Quake 1 is the qualified path.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "auto-pigeon.q1.") && strings.Contains(line, "Work in progress") {
			t.Errorf("a Quake 1 pipeline is marked work in progress:\n%s", line)
		}
	}
}

func TestBuildPipelinesAsJSON(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"build", "pipelines", "--json"}); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	var rows []struct {
		ID       string   `json:"id"`
		Steps    []string `json:"steps"`
		Runnable bool     `json:"runnable"`
		Family   string   `json:"engine_family"`
		Maturity string   `json:"maturity"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, stdout.String())
	}
	if len(rows) != 7 {
		t.Fatalf("expected the three Quake 1, two Quake II and two Quake III built-in pipelines, got %d",
			len(rows))
	}
	unfinished := map[string]int{}
	for _, row := range rows {
		if len(row.Steps) != 3 || !row.Runnable {
			t.Errorf("%s: steps=%v runnable=%t", row.ID, row.Steps, row.Runnable)
		}
		switch row.Family {
		case "quake1":
			if row.Maturity != string(maturity.Stable) {
				t.Errorf("%s is %q; the Quake 1 path is the qualified one", row.ID, row.Maturity)
			}
		case "quake2", "quake3":
			unfinished[row.Family]++
			if row.Maturity != string(maturity.WorkInProgress) {
				t.Errorf("%s is %q; %s is work in progress", row.ID, row.Maturity, row.Family)
			}
		default:
			t.Errorf("%s declares the family %q", row.ID, row.Family)
		}
	}
	if unfinished["quake2"] != 2 || unfinished["quake3"] != 2 {
		t.Errorf("%d Quake II and %d Quake III pipelines were listed",
			unfinished["quake2"], unfinished["quake3"])
	}
}

// A preview on a machine where the toolchain has not been acquired resolves the
// whole pipeline and then says what is missing, by the name of the thing to
// configure. That order matters: reviewing what a profile would run is what
// somebody does *before* installing it.
func TestBuildPreviewOnAFreshMachineSaysWhatIsMissing(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	code := Run(env, []string{
		"build", "preview",
		"--pipeline", "auto-pigeon.q1.normal",
		"--input", "source_map=level.map",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stdout: %s stderr: %s)", code, stdout.String(), stderr.String())
	}
	out := stdout.String() + stderr.String()
	for _, want := range []string{"auto-pigeon.q1.normal", "q1.bsp.compile", "tool_root"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not mention %q:\n%s", want, out)
		}
	}
}

func TestBuildRunRequiresAPipeline(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"build", "run"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--pipeline") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestBuildListIsEmptyBeforeAnythingHasBeenBuilt(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"build", "list"}); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no builds yet") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestBuildShowRefusesSomethingThatIsNotABuildID(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"build", "show", "../etc"}); code == 0 {
		t.Fatal("a path traversal was accepted as a build id")
	}
	if !strings.Contains(stderr.String(), "not a build id") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// An option override says which stage it is for, because a pipeline has
// several and an unqualified name would be the command guessing.
func TestBuildOptionOverridesAreQualifiedByStep(t *testing.T) {
	env, _, stderr := testEnv(t)
	code := Run(env, []string{
		"build", "preview",
		"--pipeline", "auto-pigeon.q1.normal",
		"--option", "threads=8",
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "step.option=value") {
		t.Errorf("the error does not say what the spelling is: %q", stderr.String())
	}
}

func TestBuildUsagePrintsForAnUnknownSubcommand(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"build", "nonsense"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "companion build run") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
