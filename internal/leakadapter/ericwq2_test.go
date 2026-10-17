package leakadapter

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func readQ2(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"testdata", "q2"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The logs and point files are qbsp 1.4518's own output on the authored
// controls in testdata/q2/controls (`Q2_010`); only the directories were
// renamed. Each row is the measured run as the Companion records it: the
// step's exit, how the step ended, the fresh point file when the run left
// one and whether a BSP was collected.
func TestEricwQ2RunsAreReadByWhatTheCompilerSaidAndLeft(t *testing.T) {
	checkEricwQ2Controls(t, "logs", "pts", EricwQ2MeasuredVersion)
}

// `Q2_005B`: the candidate 1.4522's own runs of the same controls are read as
// 1.4518's are, row for row — the evidence for qualifying its exact banner.
func TestTheCandidateQbspIsReadAsTheMeasuredRelease(t *testing.T) {
	checkEricwQ2Controls(t, "logs-1.4522", "pts-1.4522", EricwQ2CandidateVersion)
	q2, _ := ForProfile(ProfileQuake2)
	if !q2.Qualified(EricwQ2CandidateVersion) || !q2.Qualified(EricwQ2MeasuredVersion) || q2.Qualified("2.0.0-alpha11-36-g0d64c054") {
		t.Fatalf("qualified versions: %v", q2.QualifiedVersions)
	}
	for _, name := range []string{"b_gap", "c_outside_light", "f_clip_cover", "f_detail_cover", "f_window_cover", "g_degenerate_plane"} {
		if readQ2(t, "pts-1.4522", name+".pts") != readQ2(t, "pts", name+".pts") {
			t.Errorf("%s: the candidate's point file is not 1.4518's", name)
		}
	}
}

func checkEricwQ2Controls(t *testing.T, logs, ptsDir, version string) {
	t.Helper()
	yes, no, zero, one := true, false, 0, 1
	cases := []struct {
		name, log, pts string
		state          string
		exit           *int
		bsp            *bool
		want           string
		points         int
		tokens         []string
	}{
		{name: "A sealed", log: "a_sealed", state: "succeeded", exit: &zero, bsp: &yes, want: OutcomeNoLeak,
			tokens: []string{EvFillCompleted, EvFinished, EvBSPWritten, EvQuake2BSPConfirmed}},
		{name: "A sealed, verbose", log: "a_sealed.verbose", state: "succeeded", exit: &zero, bsp: &yes, want: OutcomeNoLeak},
		{name: "A sealed, quiet: no fill report", log: "a_sealed.quiet", state: "succeeded", exit: &zero, bsp: &yes,
			want: OutcomeIncomplete, tokens: []string{EvFillUnreported}},
		{name: "B slot", log: "b_gap", pts: "b_gap", state: "failed", exit: &one, bsp: &no, want: OutcomeLeak, points: 10,
			tokens: []string{EvOccupantReached, EvLeakFileReported, EvRoute}},
		{name: "B slot, verbose", log: "b_gap.verbose", pts: "b_gap", state: "failed", exit: &one, bsp: &no, want: OutcomeLeak, points: 10},
		{name: "B slot, quiet", log: "b_gap.quiet", pts: "b_gap", state: "failed", exit: &one, bsp: &no, want: OutcomeLeak, points: 10},
		{name: "B slot, point file not collected", log: "b_gap", state: "failed", exit: &one, bsp: &no, want: OutcomeLeak,
			tokens: []string{EvRouteMissing}},
		{name: "C light outside", log: "c_outside_light", pts: "c_outside_light", state: "failed", exit: &one, bsp: &no,
			want: OutcomeLeak, points: 6},
		{name: "D no occupant", log: "d_no_occupant", state: "succeeded", exit: &zero, bsp: &yes, want: OutcomeNoInterior,
			tokens: []string{EvEmptyFlood, EvNoEntityInOpen}},
		{name: "D no occupant, quiet", log: "d_no_occupant.quiet", state: "succeeded", exit: &zero, bsp: &yes, want: OutcomeNoInterior},
		{name: "E all in solid", log: "e_in_solid", state: "succeeded", exit: &zero, bsp: &yes, want: OutcomeNoInterior},
		{name: "F detail cover", log: "f_detail_cover", pts: "f_detail_cover", state: "failed", exit: &one, bsp: &no, want: OutcomeLeak, points: 10},
		{name: "F clip cover", log: "f_clip_cover", pts: "f_clip_cover", state: "failed", exit: &one, bsp: &no, want: OutcomeLeak, points: 10},
		{name: "F window cover", log: "f_window_cover", pts: "f_window_cover", state: "failed", exit: &one, bsp: &no, want: OutcomeLeak, points: 10},
		{name: "G truncated", log: "g_truncated", state: "failed", exit: &one, bsp: &no, want: OutcomeIncomplete,
			tokens: []string{EvFatalError}},
		{name: "G truncated, a stale point file beside it", log: "g_truncated", pts: "i_stale_pts", state: "failed", exit: &one,
			bsp: &no, want: OutcomeIncomplete, tokens: []string{EvRouteUnexpected, EvFatalError}},
		{name: "G degenerate plane: the brush is dropped and the map leaks", log: "g_degenerate_plane", pts: "g_degenerate_plane",
			state: "failed", exit: &one, bsp: &no, want: OutcomeLeak, points: 6, tokens: []string{EvRoute, EvBrushInvalid}},
		{name: "H missing WAL", log: "h_missing_wal", state: "succeeded", exit: &zero, bsp: &yes, want: OutcomeIncomplete,
			tokens: []string{EvFillCompleted, EvFinished, EvTextureMissing}},
		{name: "I stale point file beside a sealed run", log: "i_stale_pts", pts: "i_stale_pts", state: "succeeded", exit: &zero,
			bsp: &yes, want: OutcomeIncomplete, tokens: []string{EvRouteUnexpected}},
		{name: "I sealed run, as collected (qbsp removed the .pts)", log: "i_stale_pts", state: "succeeded", exit: &zero, bsp: &yes,
			want: OutcomeNoLeak},
		// `Q2_019`: a real-size map's fill report is "[.   ]    1,723 outside leaves" — a progress prefix and a
		// thousands separator. A sealed large map was read as incomplete before.
		{name: "J sealed, more than 999 outside leaves, spinner prefix", log: "j_outside_pillars", state: "succeeded", exit: &zero,
			bsp: &yes, want: OutcomeNoLeak, tokens: []string{EvFillCompleted, EvFinished, EvBSPWritten, EvQuake2BSPConfirmed}},
	}
	for _, c := range cases {
		e := Evidence{Log: readQ2(t, logs, c.log+".log"), StepState: c.state, ExitCode: c.exit, BSP: c.bsp}
		if EricwQ2LogVersion(e.Log) != version {
			t.Errorf("%s: the log is qbsp %q, not %q", c.name, EricwQ2LogVersion(e.Log), version)
		}
		if c.pts != "" {
			e.Pointfile = readQ2(t, ptsDir, c.pts+".pts")
		}
		got := ClassifyEricwQ2(e)
		if got.Outcome != c.want || got.RoutePoints != c.points {
			t.Errorf("%s: %+v, want %s with %d points", c.name, got, c.want, c.points)
		}
		for _, token := range c.tokens {
			if !got.Has(token) {
				t.Errorf("%s: %v lacks %s", c.name, got.Evidence, token)
			}
		}
		for _, token := range got.Evidence {
			if !slices.Contains(EvidenceTokens(), token) {
				t.Errorf("%s: %q is not in the closed list", c.name, token)
			}
		}
		if got.Outcome != OutcomeNoLeak && got.Has(EvBSPWritten) {
			t.Errorf("%s: a BSP was called a pass's evidence on a run that is not one", c.name)
		}
	}
}

// Measured, both directions: the route starts at the origin the log names and
// ends in the void outside the shell. Nothing here reverses it.
func TestTheQuake2PointFileStartsAtTheOccupantTheLogNames(t *testing.T) {
	for name, occupant := range map[string][3]float64{
		"b_gap": {0, 0, 192}, "c_outside_light": {512, 0, 128}, "f_detail_cover": {0, 0, 192},
	} {
		pts := strings.Fields(readQ2(t, "pts", name+".pts"))
		if pts[0] != formatCoord(occupant[0]) || pts[1] != formatCoord(occupant[1]) || pts[2] != formatCoord(occupant[2]) {
			t.Errorf("%s: first point %v, the log names %v", name, pts[:3], occupant)
		}
		last := pts[len(pts)-3:]
		if last[0] != "-272" || last[1] != "-284" || last[2] != "-20" {
			t.Errorf("%s: last point %v is not the outside one measured", name, last)
		}
	}
	q2, _ := ForProfile(ProfileQuake2)
	if q2.Direction != DirectionOccupantFirst {
		t.Fatal(q2.Direction)
	}
}

func formatCoord(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Nothing short of a finished, qualified, Quake II run of a map whose every
// texture was found is a pass — and nothing that is not the qbsp Quake II
// log is any verdict.
func TestNothingButAFinishedQualifiedQuake2RunIsANoLeak(t *testing.T) {
	sealed := readQ2(t, "logs", "a_sealed.log")
	zero, one, yes, no := 0, 1, true, false
	pass := Evidence{Log: sealed, StepState: "succeeded", ExitCode: &zero, BSP: &yes, BSPFormat: "IBSP:38"}
	if got := ClassifyEricwQ2(pass); got.Outcome != OutcomeNoLeak {
		t.Fatalf("the sealed control: %+v", got)
	}
	cut := func(at string) string { return sealed[:strings.Index(sealed, at)] }
	for name, e := range map[string]Evidence{
		"exit 1":                 {Log: sealed, StepState: "failed", ExitCode: &one, BSP: &yes},
		"the BSP was not there":  {Log: sealed, StepState: "succeeded", ExitCode: &zero, BSP: &no},
		"a Quake 1 BSP":          {Log: sealed, StepState: "succeeded", ExitCode: &zero, BSP: &yes, BSPFormat: "BSP:29"},
		"a Qbism BSP":            {Log: strings.Replace(sealed, "Quake II BSP IBSP:38", "Qbism BSP QBSP:38", 1), StepState: "succeeded", ExitCode: &zero, BSP: &yes},
		"timed out":              {Log: sealed, StepState: "timed_out", BSP: &yes},
		"cancelled":              {Log: sealed, StepState: "cancelled"},
		"cut before the fill":    {Log: cut("---- FillOutside ----"), StepState: ""},
		"cut before Wrote":       {Log: cut("Wrote "), StepState: ""},
		"another version":        {Log: strings.Replace(sealed, EricwQ2MeasuredVersion, "2.0.0-alpha7", 1), StepState: "succeeded", ExitCode: &zero, BSP: &yes},
		"a Quake 1 run":          {Log: strings.Replace(sealed, `"q2bsp" was set to "1"`, `"bsp2" was set to "1"`, 1), StepState: "succeeded", ExitCode: &zero},
		"not a qbsp log":         {Log: "hello\n", StepState: "succeeded", ExitCode: &zero},
		"an empty log":           {Log: "", StepState: "succeeded", ExitCode: &zero},
		"a Q3Map2 log":           {Log: read(t, "logs", "a_sealed.log"), StepState: "succeeded", ExitCode: &zero},
		"a malformed point file": {Log: sealed, StepState: "succeeded", ExitCode: &zero, BSP: &yes, Pointfile: "1 2\n"},
		"a texture it could not open": {Log: strings.Replace(sealed, "WARNING: Couldn't locate texture for skip",
			"WARNING: Couldn't locate texture for skip\nWARNING: Couldn't locate texture for e1u1/floor1_1", 1),
			StepState: "succeeded", ExitCode: &zero, BSP: &yes},
	} {
		if got := ClassifyEricwQ2(e); got.Outcome != OutcomeIncomplete {
			t.Errorf("%s: %+v, want incomplete", name, got)
		}
	}

	// An unqualified version still reports a leak it SAID it found: the words
	// are the compiler's, not a guess.
	leak := strings.Replace(readQ2(t, "logs", "b_gap.log"), EricwQ2MeasuredVersion, "2.0.0-alpha7", 1)
	got := ClassifyEricwQ2(Evidence{Log: leak, Pointfile: readQ2(t, "pts", "b_gap.pts"), ExitCode: &one, StepState: "failed"})
	if got.Outcome != OutcomeLeak || !got.Has(EvVersionUnqualified) || got.RoutePoints != 10 {
		t.Errorf("an unqualified leak: %+v", got)
	}
	// And a point file that does not start where the log says the occupant is
	// is not this leak's route.
	moved := strings.Replace(readQ2(t, "pts", "b_gap.pts"), "0 0 192", "64 0 192", 1)
	got = ClassifyEricwQ2(Evidence{Log: readQ2(t, "logs", "b_gap.log"), Pointfile: moved, ExitCode: &one, StepState: "failed"})
	if got.Outcome != OutcomeLeak || !got.Has(EvRouteDisagrees) || got.RoutePoints != 0 {
		t.Errorf("a route from elsewhere: %+v", got)
	}
}

func TestABSPHeaderIsNamedTheWayQbspNamesIt(t *testing.T) {
	for want, header := range map[string][]byte{
		"IBSP:38": {'I', 'B', 'S', 'P', 38, 0, 0, 0},
		"IBSP:46": {'I', 'B', 'S', 'P', 46, 0, 0, 0},
		"QBSP:38": {'Q', 'B', 'S', 'P', 38, 0, 0, 0},
		"BSP:29":  {29, 0, 0, 0, 0, 0, 0, 0},
		"":        {'I', 'B'},
	} {
		if got := BSPFormatOf(header); got != want {
			t.Errorf("%v: %q, want %q", header, got, want)
		}
	}
}

// A Quake II pipeline the user pins is bound by role like any other: the
// point file `q2.pts`, the compiler's own text from the step that wrote it —
// never qbsp's `.log` file, which lacks the fill report.
func TestAQuake2PipelineIsBoundToTheCompileStepsOwnText(t *testing.T) {
	q2, err := ForProfile(ProfileQuake2)
	if err != nil {
		t.Fatal(err)
	}
	normal := Pipeline{ID: "local.pipeline.q2-normal", Games: []string{"quake2"}, Outputs: []PipelineOutput{
		{Name: "bsp", Role: "q2.bsp.lit", From: "light.bsp"},
		{Name: "pts", Role: "q2.pts", From: "compile.pts"},
		{Name: "compile_log", Role: "q2.compile.log", From: "compile.log"},
	}}
	binding, err := q2.Bind(normal)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Pointfile != "pts" || binding.Log != LeakLogOutput || binding.LogFrom != "compile.stdout" ||
		binding.CompileStep != "compile" || binding.BSP != "" {
		t.Errorf("the normal build as a leak test: %+v", binding)
	}
	if _, err := q2.Bind(Pipeline{ID: "q1", Games: []string{"quake1"}, Outputs: normal.Outputs}); err == nil {
		t.Error("a Quake 1 pipeline was bound for Quake II")
	}
	if _, err := q2.Bind(Pipeline{ID: "q2", Games: []string{"quake2"}, Outputs: []PipelineOutput{
		{Name: "pts", Role: "q1.pts", From: "compile.pts"}}}); err == nil {
		t.Error("a Quake 1 point file role was bound for Quake II")
	}
	if got := q2.Builtin(); got.Pointfile != "pts" || got.Log != "compile_log" || got.BSP != "bsp" || got.CompileStep != "compile" {
		t.Errorf("built in: %+v", got)
	}
}
