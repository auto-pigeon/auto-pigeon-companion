package leakadapter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"testdata", "q3"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOnlyMeasuredGamesHaveALeakAdapterAndNothingFallsBackToQuake1(t *testing.T) {
	q1, err := ForProfile("quake1")
	if err != nil || q1.PipelineID != "auto-pigeon.q1.leak-test" || q1.ResultSchema != Schema10 ||
		q1.PointfileFormat != FormatEricwPts || q1.Direction != DirectionOccupantFirst || q1.Classify != nil ||
		q1.PointfileBesideSource {
		t.Fatalf("quake1: %+v %v", q1, err)
	}
	q3, err := ForProfile("quake3")
	if err != nil || q3.PipelineID != "auto-pigeon.q3.leak-test" || q3.ResultSchema != Schema11 ||
		q3.Compiler != CompilerQ3Map2 || q3.PointfileFormat != FormatQ3Map2Lin ||
		q3.Direction != DirectionOutsideFirst || q3.Classify == nil || !q3.PointfileBesideSource {
		t.Fatalf("quake3: %+v %v", q3, err)
	}
	for _, game := range []string{"quake2", "", "unknown", "Quake1", "quake1 ", "hexen2"} {
		if adapter, err := ForProfile(game); !errors.Is(err, ErrUnsupported) || adapter.PipelineID != "" {
			t.Errorf("%q: got %+v %v, want unsupported and no pipeline", game, adapter, err)
		}
	}
	if adapter, ok := ForPipeline("auto-pigeon.q3.leak-test"); !ok || adapter.Profile != "quake3" {
		t.Fatalf("pipeline lookup: %+v %v", adapter, ok)
	}
	for _, id := range []string{"auto-pigeon.q3.normal", "auto-pigeon.q2.normal", ""} {
		if _, ok := ForPipeline(id); ok {
			t.Errorf("%q is not a leak-test pipeline", id)
		}
	}
	if strings.Join(Profiles(), ",") != "quake1,quake3" {
		t.Fatalf("profiles: %v", Profiles())
	}
}

// The logs and line files are the installed Q3Map2 2.5.17n's own output on the
// authored controls beside them; only the directories were renamed.
func TestQ3Map2RunsAreReadByWhatTheCompilerSaidAndLeft(t *testing.T) {
	yes, no, zero, one := true, false, 0, 1
	cases := []struct {
		name, lin string
		state     string
		exit      *int
		bsp       *bool
		want      string
		has, not  []string
		points    int
	}{
		{name: "a_sealed", state: "succeeded", exit: &zero, bsp: &yes, want: OutcomeNoLeak,
			has: []string{EvFillCompleted, EvFinished, EvBSPWritten}},
		{name: "b_gap", lin: "b_gap", state: "failed", exit: &zero, bsp: &no, want: OutcomeLeak,
			has: []string{EvLeakBanner, EvEntityLeaked, EvRoute}, points: 3},
		{name: "c_outside_entity", lin: "c_outside_entity", state: "failed", exit: &zero, bsp: &no, want: OutcomeLeak,
			has: []string{EvEntityLeaked, EvRoute}, points: 2},
		{name: "d_no_occupant", state: "failed", exit: &zero, bsp: &no, want: OutcomeNoInterior,
			has: []string{EvLeakBanner, EvEmptyFlood}, not: []string{EvEntityLeaked, EvRoute}},
		{name: "d_in_solid", state: "failed", exit: &zero, bsp: &no, want: OutcomeNoInterior,
			has: []string{EvEmptyFlood, EvEntityInSolid}},
		// Measured: neither a patch nor a detail brush seals the gap.
		{name: "e_patch_cover", lin: "e_patch_cover", state: "failed", exit: &zero, bsp: &no, want: OutcomeLeak, points: 3},
		{name: "e_detail_cover", lin: "e_detail_cover", state: "failed", exit: &zero, bsp: &no, want: OutcomeLeak, points: 3},
		{name: "x_nonsolid_wall", lin: "x_nonsolid_wall", state: "failed", exit: &zero, bsp: &no, want: OutcomeLeak, points: 3},
		// Sealed by a wall whose shader nothing defines: the pass is the
		// compiler's, and the qualification travels with it.
		{name: "x_missing_shader", state: "succeeded", exit: &zero, bsp: &yes, want: OutcomeNoLeak,
			has: []string{EvShaderImageMissing}},
		{name: "f_malformed", state: "failed", exit: &one, bsp: &no, want: OutcomeIncomplete,
			has: []string{EvFatalError}},
	}
	for _, c := range cases {
		for _, suffix := range []string{"", ".verbose"} {
			evidence := Evidence{Log: read(t, "logs", c.name+suffix+".log"), StepState: c.state, ExitCode: c.exit, BSP: c.bsp}
			if c.lin != "" {
				evidence.Pointfile = read(t, "lin", c.lin+".lin")
			}
			got := ClassifyQ3Map2(evidence)
			if got.Outcome != c.want || got.RoutePoints != c.points {
				t.Errorf("%s%s: got %+v, want %s with %d points", c.name, suffix, got, c.want, c.points)
			}
			for _, token := range c.has {
				if !got.Has(token) {
					t.Errorf("%s%s: evidence %v lacks %s", c.name, suffix, got.Evidence, token)
				}
			}
			for _, token := range c.not {
				if got.Has(token) {
					t.Errorf("%s%s: evidence %v must not carry %s", c.name, suffix, got.Evidence, token)
				}
			}
			if verbose := suffix != ""; verbose && c.name == "d_no_occupant" && !got.Has(EvNoEntityInOpen) {
				t.Errorf("verbose empty flood does not say why: %v", got.Evidence)
			}
			known := map[string]bool{}
			for _, token := range EvidenceTokens() {
				known[token] = true
			}
			for _, token := range got.Evidence {
				if !known[token] {
					t.Errorf("%s: %q is not in the closed evidence list", c.name, token)
				}
			}
		}
	}
}

func TestNothingButAFinishedQualifiedRunIsANoLeak(t *testing.T) {
	yes, no, zero, one := true, false, 0, 1
	sealed := read(t, "logs", "a_sealed.log")
	leaked := read(t, "logs", "b_gap.log")
	route := read(t, "lin", "b_gap.lin")
	cut := func(log, at string) string { return log[:strings.Index(log, at)] }
	cases := []struct {
		name string
		e    Evidence
		want string
		has  string
	}{
		{"exit 0 and an empty log", Evidence{ExitCode: &zero, StepState: "succeeded", BSP: &yes}, OutcomeIncomplete, EvNotQ3Map2},
		{"a log from another program", Evidence{Log: "---- qbsp / ericw-tools v0.18.1 ----\n", ExitCode: &zero}, OutcomeIncomplete, EvNotQ3Map2},
		{"cut before the fill", Evidence{Log: cut(sealed, "       28 leafs filled"), ExitCode: &zero, StepState: "succeeded", BSP: &yes}, OutcomeIncomplete, EvTruncated},
		{"cut before the BSP was written", Evidence{Log: cut(sealed, "Writing /work/input/source_map/a_sealed.bsp"), ExitCode: &zero, BSP: &yes}, OutcomeIncomplete, EvTruncated},
		{"cut before the footer", Evidence{Log: cut(sealed, "        0 seconds elapsed"), ExitCode: &zero, BSP: &yes}, OutcomeIncomplete, EvTruncated},
		{"cancelled", Evidence{Log: cut(sealed, "--- EmitMetaStats ---"), StepState: "cancelled"}, OutcomeIncomplete, EvCancelled},
		{"the BSP is not there", Evidence{Log: sealed, ExitCode: &zero, StepState: "succeeded", BSP: &no}, OutcomeIncomplete, EvBSPMissing},
		{"a stale line file beside a clean run is not a leak", Evidence{Log: sealed, ExitCode: &zero, StepState: "succeeded", BSP: &yes, Pointfile: route}, OutcomeNoLeak, EvBSPWritten},
		{"a model error failed the step", Evidence{Log: strings.Replace(sealed, "        0 areaportals", "ERROR: Unable to open file \"models/x.md3\".\n        0 areaportals", 1), ExitCode: &zero, StepState: "failed", BSP: &yes}, OutcomeIncomplete, EvStepNotSucceeded},
		{"the same, read from the log alone", Evidence{Log: strings.Replace(sealed, "        0 areaportals", "ERROR: Unable to open file \"models/x.md3\".\n        0 areaportals", 1)}, OutcomeIncomplete, EvStepNotSucceeded},
		{"non-zero exit", Evidence{Log: sealed, ExitCode: &one, BSP: &yes}, OutcomeIncomplete, EvExitNonzero},
		{"another version's clean run", Evidence{Log: strings.ReplaceAll(sealed, "2.5.17n-git-68ecbed", "2.5.16"), ExitCode: &zero, StepState: "succeeded", BSP: &yes}, OutcomeIncomplete, EvVersionUnqualified},
		{"another version's bare banner", Evidence{Log: strings.ReplaceAll(read(t, "logs", "d_no_occupant.log"), "2.5.17n-git-68ecbed", "2.5.16"), ExitCode: &zero, StepState: "failed"}, OutcomeIncomplete, EvVersionUnqualified},
		{"another version naming the entity is still a leak", Evidence{Log: strings.ReplaceAll(leaked, "2.5.17n-git-68ecbed", "2.5.16"), Pointfile: route}, OutcomeLeak, EvVersionUnqualified},
		{"a leak with exit 0 and no line file is a leak without a route", Evidence{Log: leaked, ExitCode: &zero, StepState: "failed", BSP: &no}, OutcomeLeak, EvRouteMissing},
		{"a leak with a damaged line file is a leak without a route", Evidence{Log: leaked, Pointfile: "1 2 3\n4 5\n"}, OutcomeLeak, EvRouteInvalid},
		{"a leak said before a cancel is still a leak", Evidence{Log: leaked, StepState: "cancelled", Pointfile: route}, OutcomeLeak, EvRoute},
		{"a banner with nothing after it, read alone", Evidence{Log: cut(read(t, "logs", "d_no_occupant.log"), "--- MAP LEAKED")}, OutcomeIncomplete, EvTruncated},
		{"an empty flood is never a route", Evidence{Log: read(t, "logs", "d_no_occupant.log"), Pointfile: "1 2 3\n"}, OutcomeNoInterior, EvEmptyFlood},
	}
	for _, c := range cases {
		got := ClassifyQ3Map2(c.e)
		if got.Outcome != c.want || !got.Has(c.has) {
			t.Errorf("%s: got %+v, want %s carrying %s", c.name, got, c.want, c.has)
		}
	}
}

func TestPointFilesAreFiniteTriplesWithinBounds(t *testing.T) {
	if count, err := CountPoints("\xef\xbb\xbf1 2 3\r\n\r\n  -4.5e2\t5 6  \n"); err != nil || count != 2 {
		t.Fatalf("got %d %v", count, err)
	}
	for name, text := range map[string]string{
		"one point":    "1 2 3\n",
		"two columns":  "1 2\n3 4 5\n",
		"four columns": "1 2 3 4\n5 6 7\n",
		"nan":          "NaN 0 0\n1 2 3\n",
		"infinity":     "Inf 0 0\n1 2 3\n",
		"hex float":    "0x1p4 0 0\n1 2 3\n",
		"underscore":   "1_0 0 0\n1 2 3\n",
		"overflow":     "1e999 0 0\n1 2 3\n",
		"a command":    "$(rm -rf /) 0 0\n1 2 3\n",
		"xml":          "<point> 0 0\n1 2 3\n",
		"too large":    strings.Repeat("1 2 3\n", MaxPointfileBytes/6+1),
		"too many":     strings.Repeat("1 2 3\n", MaxPointfilePoints+1),
	} {
		if count, err := CountPoints(text); err == nil {
			t.Errorf("%s: accepted with %d points", name, count)
		}
	}
}

// A pipeline the user pins is read by role (NEW_310): any output names, as
// long as it is for the game and publishes the point file and the log.
func TestAPinnedPipelineIsBoundByRoleNotByName(t *testing.T) {
	q1, err := ForProfile(ProfileQuake1)
	if err != nil {
		t.Fatal(err)
	}
	mine := Pipeline{ID: "local.pipeline.my-leak", Games: []string{"quake1"}, Outputs: []PipelineOutput{
		{Name: "route", Role: "q1.pts", From: "qbsp.pts"},
		{Name: "said", Role: StepLogRole, From: "qbsp.stdout"},
	}}
	binding, err := q1.Bind(mine)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Pointfile != "route" || binding.Log != "said" || binding.CompileStep != "qbsp" || binding.Game != "quake1" {
		t.Errorf("bound as %+v", binding)
	}

	// The game's own log role is preferred to the step's text.
	mine.Outputs = append(mine.Outputs, PipelineOutput{Name: "qbsp_log", Role: "q1.compile.log", From: "qbsp.log"})
	if binding, _ := q1.Bind(mine); binding.Log != "qbsp_log" {
		t.Errorf("log %q", binding.Log)
	}

	for name, refused := range map[string]Pipeline{
		"another game":    {ID: "x.y", Games: []string{"quake3"}, Outputs: mine.Outputs},
		"no point file":   {ID: "x.y", Games: []string{"quake1"}, Outputs: mine.Outputs[1:]},
		"no compiler log": {ID: "x.y", Games: []string{"quake1"}, Outputs: mine.Outputs[:1]},
	} {
		if _, err := q1.Bind(refused); err == nil {
			t.Errorf("%s: bound", name)
		}
	}

	// The built-in row reads its own pipeline exactly as before.
	if got := q1.Builtin(); got.Pointfile != "pts" || got.Log != "compile_log" || got.CompileStep != "compile" {
		t.Errorf("built in: %+v", got)
	}
}
