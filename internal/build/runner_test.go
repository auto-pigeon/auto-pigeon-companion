package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

func TestAPipelineRunsEveryStageAndWiresTheFilesBetweenThem(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	source := h.sourceMap("level.map", "brushes\n")

	manifest := h.mustRun(Request{PipelineID: "test.build.pipeline", Inputs: map[string]string{"source_map": source}})

	if !manifest.Succeeded() {
		t.Fatalf("the build is %s: %s", manifest.State, manifest.Error)
	}
	if len(manifest.Steps) != 3 {
		t.Fatalf("expected three steps, got %d", len(manifest.Steps))
	}
	for _, step := range manifest.Steps {
		if step.State != job.Succeeded {
			t.Errorf("the %s step is %s: %s", step.ID, step.State, step.Error)
		}
		if step.JobID == "" {
			t.Errorf("the %s step records no job id, so nothing links it to what ran", step.ID)
		}
		if _, err := h.service.Get(step.JobID); err != nil {
			t.Errorf("the %s step names job %s, which the executor does not have: %v", step.ID, step.JobID, err)
		}
	}

	// The whole point of the wiring: what came out the far end is what every
	// stage in turn wrote to.
	published := outputNamed(t, manifest.Outputs, "bsp")
	contents, err := os.ReadFile(published.Path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got := string(contents); got != "BSP:brushes\n+VIS+LIGHT" {
		t.Errorf("the published BSP is %q; each stage should have added to the last one's work", got)
	}
	if published.SHA256 == "" {
		t.Error("the published BSP has no digest")
	}
	if _, err := os.Stat(outputNamed(t, manifest.Outputs, "lit").Path); err != nil {
		t.Errorf("the companion .lit was not published: %v", err)
	}
	// The input the user gave is untouched: the build copied it.
	if data, err := os.ReadFile(source); err != nil || string(data) != "brushes\n" {
		t.Errorf("the user's own source was modified: %q, %v", data, err)
	}
}

// `vis` opens a file it was never given as an argument, and this is the test
// that says the executor put it where it looks.
func TestASidecarInputIsStagedBesideTheFileItAccompanies(t *testing.T) {
	// The workspaces are kept, because where the executor put the two files is
	// the fact under test and a cleaned-up workspace has thrown it away.
	h := newHarnessKeeping(t, standardFixtures(t), true)
	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	vis := stepNamed(t, manifest, "vis")
	if vis.State != job.Succeeded {
		t.Fatalf("vis is %s: %s — it could not find the portal file beside the BSP", vis.State, vis.Error)
	}
	if vis.Command == nil || len(vis.Command.Args) == 0 {
		t.Fatal("the vis step recorded no argv")
	}
	// The BSP as the tool was given it, and the portal file the tool opens
	// without being told to: same directory, same stem.
	handed := vis.Command.Args[len(vis.Command.Args)-1]
	if _, err := os.Stat(handed); err != nil {
		t.Fatalf("the BSP vis was handed is not there: %v", err)
	}
	sidecar := strings.TrimSuffix(handed, filepath.Ext(handed)) + ".prt"
	if _, err := os.Stat(sidecar); err != nil {
		t.Errorf("the portal file is not beside the BSP at %s: %v", sidecar, err)
	}
	if got := filepath.Base(filepath.Dir(handed)); got != "bsp" {
		t.Errorf("the BSP was staged in %q, not in its input's own directory", got)
	}
}

// Without `stage_with` the executor stages each input into its own directory,
// which is right for two unrelated files and fatal for a sidecar. This asserts
// that the format member is what makes the difference, rather than luck.
func TestWithoutStageWithTheSidecarIsSomewhereTheToolDoesNotLook(t *testing.T) {
	document := fixtureTool(t, "test.build.toolchain", "test.stage", "compile")
	// The same document with the sidecar declaration removed.
	without := strings.Replace(string(document), `,"stage_with":"bsp"`, "", 1)
	if without == string(document) {
		t.Fatal("the fixture no longer declares stage_with; this test is checking nothing")
	}
	h := newHarness(t, map[string][]byte{
		"tool.tool.json":         []byte(without),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	})
	manifest, err := h.run(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil {
		t.Fatal("the build succeeded with the portal file staged where vis does not look")
	}
	vis := stepNamed(t, manifest, "vis")
	if vis.State == job.Succeeded {
		t.Fatalf("vis succeeded: %+v", vis)
	}
	found := false
	for _, diagnostic := range vis.Diagnostics {
		if diagnostic.RuleID == "no_portals" {
			found = true
		}
	}
	if !found {
		t.Errorf("nothing said the portal file was missing; the diagnostics were %+v", vis.Diagnostics)
	}
}

// `vis` and `light` write over the file they were handed. An output declared at
// a path this document invented would name a file that does not exist.
func TestAnInPlaceOutputIsTheFileTheToolWasHanded(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	light := stepNamed(t, manifest, "light")
	if light.Command == nil {
		t.Fatal("the light step recorded no command")
	}
	handed := light.Command.Args[len(light.Command.Args)-1]
	// The job published the output out of its workspace, so the manifest's path
	// is the build's copy; what it must be a copy *of* is the file the tool was
	// given, and the digests are how that is checked.
	lit := outputNamed(t, light.Outputs, "lit")
	if lit.Missing {
		t.Fatal("the companion .lit was not collected")
	}
	if want := strings.TrimSuffix(filepath.Base(handed), ".bsp") + ".lit"; filepath.Base(lit.Path) != want {
		t.Errorf("the companion file is %s, and light wrote %s beside %s", filepath.Base(lit.Path), want, handed)
	}
	bsp := outputNamed(t, light.Outputs, "bsp")
	if bsp.Missing || bsp.SHA256 == "" {
		t.Fatalf("the rewritten BSP was not collected: %+v", bsp)
	}
}

// A leaked map, which is the case the whole failure story is written around.
func TestAFailedStageNamesItselfPublishesWhatItWroteAndSkipsTheRest(t *testing.T) {
	h := newHarness(t, map[string][]byte{
		"tool.tool.json":         fixtureTool(t, "test.build.toolchain", "test.stage", "compile-leak"),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	})
	manifest, err := h.run(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil {
		t.Fatal("a build whose first stage failed was reported as succeeding")
	}
	if manifest.Succeeded() {
		t.Fatal("the manifest says the build succeeded")
	}
	if got := manifest.FailedStep(); got != "compile" {
		t.Errorf("the failed step is reported as %q, want \"compile\"", got)
	}
	if !strings.Contains(manifest.Error, "compile") {
		t.Errorf("the build's error does not name the stage: %s", manifest.Error)
	}

	compile := stepNamed(t, manifest, "compile")
	if compile.ExitCode == nil || *compile.ExitCode != 1 {
		t.Errorf("the compile step's exit code is %v, want 1", compile.ExitCode)
	}
	// No false BSP.
	if bsp := outputNamed(t, compile.Outputs, "bsp"); !bsp.Missing {
		t.Errorf("a BSP was published for a build that failed: %+v", bsp)
	}
	if bsp := outputNamed(t, manifest.Outputs, "bsp"); !bsp.Missing {
		t.Errorf("the pipeline published a BSP for a build that failed: %+v", bsp)
	}
	// And the one artifact the user needs *is* published.
	pts := outputNamed(t, manifest.Outputs, "pts")
	if pts.Missing {
		t.Fatal("the point file was not published, and it is the thing that says where the leak is")
	}
	if _, err := os.Stat(pts.Path); err != nil {
		t.Errorf("the published point file is not there: %v", err)
	}
	// The diagnostics survive.
	found := false
	for _, diagnostic := range compile.Diagnostics {
		if diagnostic.RuleID == "leak" && diagnostic.Severity == profile.SeverityError {
			found = true
		}
	}
	if !found {
		t.Errorf("nothing classified the leak: %+v", compile.Diagnostics)
	}
	// The later stages say they never got there rather than being absent.
	for _, id := range []string{"vis", "light"} {
		step := stepNamed(t, manifest, id)
		if !step.Skipped {
			t.Errorf("the %s step is not marked skipped: %+v", id, step)
		}
	}
}

// The other half of a leak: when the compiler produces no portal file and does
// not fail, the stage that needs it has to say what is missing and who was
// meant to make it.
func TestAMissingRequiredArtifactFromAnEarlierStageIsRefusedByName(t *testing.T) {
	h := newHarness(t, map[string][]byte{
		"tool.tool.json":         fixtureTool(t, "test.build.toolchain", "test.stage", "compile-no-portals"),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	})
	manifest, err := h.run(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil {
		t.Fatal("the build succeeded without a portal file")
	}
	compile := stepNamed(t, manifest, "compile")
	if compile.State != job.Succeeded {
		t.Fatalf("the compile stage was meant to succeed and simply produce no portal file: %s", compile.Error)
	}
	vis := stepNamed(t, manifest, "vis")
	for _, phrase := range []string{"prt", "compile.prt"} {
		if !strings.Contains(vis.Error, phrase) {
			t.Errorf("the vis step's error does not mention %q: %s", phrase, vis.Error)
		}
	}
	if vis.JobID != "" {
		t.Error("a job was started for a stage whose input does not exist")
	}
	if bsp := outputNamed(t, manifest.Outputs, "bsp"); !bsp.Missing {
		t.Error("a BSP was published for a build that never finished")
	}
}

// The claim the whole preview mechanism rests on, checked rather than asserted.
func TestTheCommandPreviewIsTheCommandThatRan(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	for _, step := range manifest.Steps {
		if !step.PreviewMatched {
			t.Errorf("the %s step's preview did not match what ran: %s", step.ID, step.PreviewDifference)
		}
		if step.Command == nil || len(step.Command.Args) == 0 {
			t.Errorf("the %s step recorded no argv", step.ID)
		}
	}
}

// A preview resolves every stage, catches what it can and starts nothing.
func TestPreviewResolvesEveryStageAndStartsNothing(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	before, _ := h.service.List()

	manifest, err := h.runner.Preview(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(manifest.Steps) != 3 {
		t.Fatalf("the preview resolved %d steps", len(manifest.Steps))
	}
	for _, step := range manifest.Steps {
		if step.Error != "" {
			t.Errorf("the %s step would not run: %s", step.ID, step.Error)
		}
		if step.Command == nil {
			t.Fatalf("the %s step has no command", step.ID)
		}
		if !strings.Contains(strings.Join(step.Command.Args, " "), previewWorkspace) {
			t.Errorf("the %s step's preview has no placeholder path, so it is claiming to know a real one: %v",
				step.ID, step.Command.Args)
		}
		if step.PreviewDifference == "" {
			t.Errorf("the %s step's preview does not say it is a prediction", step.ID)
		}
	}
	after, _ := h.service.List()
	if len(after) != len(before) {
		t.Errorf("previewing started %d jobs", len(after)-len(before))
	}
	if _, err := os.Stat(h.builds); err == nil {
		t.Error("previewing created a build directory")
	}
}

// A preview is where a pipeline that would fail on its third stage fails,
// before the user has waited through two.
func TestPreviewRefusesAnOptionTheToolDoesNotDeclare(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	manifest, err := h.runner.Preview(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
		Options:    map[string]map[string]string{"light": {"invented": "yes"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	last := manifest.Steps[len(manifest.Steps)-1]
	if !strings.Contains(last.Error, "invented") {
		t.Errorf("the preview did not refuse an undeclared option: %+v", last)
	}
}

func TestTwoBuildsOfTheSameThingHaveTheSameRecipeKey(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	source := h.sourceMap("level.map", "brushes\n")
	request := Request{PipelineID: "test.build.pipeline", Inputs: map[string]string{"source_map": source}}

	first := h.mustRun(request)
	second := h.mustRun(request)
	if first.ReproducibleKey != second.ReproducibleKey {
		t.Errorf("two identical builds have different keys:\n  %s\n  %s", first.ReproducibleKey, second.ReproducibleKey)
	}
	if first.BuildID == second.BuildID {
		t.Error("two builds got the same id")
	}

	// The fixture toolchain *is* deterministic, so the outputs agree too. The
	// key does not cover them, for the reason [Manifest.ReproducibleKey] gives
	// about `light`; that they agree here is worth asserting anyway, because a
	// build system that shuffled its own wiring would show up as a difference.
	if a, b := outputNamed(t, first.Outputs, "bsp"), outputNamed(t, second.Outputs, "bsp"); a.SHA256 != b.SHA256 {
		t.Errorf("the same build produced different bytes:\n  %s\n  %s", a.SHA256, b.SHA256)
	}

	// A different input is a different build.
	changed := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("other.map", "other brushes\n")},
	})
	if changed.ReproducibleKey == first.ReproducibleKey {
		t.Error("a build of different bytes has the same key")
	}

	// So is a different option, even though the argv is unchanged by it.
	optioned := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": source},
		Options:    map[string]map[string]string{"compile": {"basename": "other"}},
	})
	if optioned.ReproducibleKey == first.ReproducibleKey {
		t.Error("a build with a different option has the same key")
	}
}

// The key has to survive being computed on a different machine, which in
// practice means it must not contain a path.
func TestTheRecipeKeyContainsNoMachinePath(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	// Recomputed from the same manifest with the roots removed: if a path had
	// survived into the key, generalizing it away would change the answer.
	restated := *manifest
	restated.computeKey(h.runner.generalizeRoots(manifest))
	if restated.ReproducibleKey != manifest.ReproducibleKey {
		t.Fatal("the key is not a function of the manifest")
	}
	elsewhere := *manifest
	elsewhere.computeKey(nil)
	if elsewhere.ReproducibleKey == manifest.ReproducibleKey {
		t.Error("the key is the same with the machine's own directories left in it, " +
			"which means either nothing was generalized or a path never reached the key at all")
	}
}

func TestCancellingABuildStopsItAndSaysSo(t *testing.T) {
	document := string(fixtureTool(t, "test.build.toolchain", "test.stage", "compile"))
	// The mode word only, so this keeps working as the compile action's argv
	// grows — it gained `-wadpath` in `AUCOM/AUE/AUT 246I1`.
	slow := strings.Replace(document, `,"compile",`, `,"sleep",`, 1)
	if slow == document {
		t.Fatal("the compile arguments were not replaced; this test is checking nothing")
	}
	h := newHarness(t, map[string][]byte{
		"tool.tool.json":         []byte(slow),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *Manifest, 1)
	go func() {
		manifest, _ := h.runner.Run(ctx, Request{
			PipelineID: "test.build.pipeline",
			Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
		})
		done <- manifest
	}()

	// Wait for the first stage to actually be running before cancelling: a
	// cancellation that raced the start would test the queue, not the stop.
	deadline := time.Now().Add(30 * time.Second)
	var running string
	for time.Now().Before(deadline) && running == "" {
		jobs, _ := h.service.List()
		for _, j := range jobs {
			if j.State == job.Running {
				running = j.ID
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if running == "" {
		cancel()
		t.Fatal("no stage ever started")
	}
	if _, err := h.service.Cancel(running); err != nil {
		cancel()
		t.Fatalf("cancelling: %v", err)
	}

	var manifest *Manifest
	select {
	case manifest = <-done:
	case <-time.After(60 * time.Second):
		cancel()
		t.Fatal("the build did not stop")
	}
	cancel()

	if manifest == nil {
		t.Fatal("a cancelled build produced no manifest")
	}
	if manifest.Succeeded() {
		t.Fatal("a cancelled build reports success")
	}
	compile := stepNamed(t, manifest, "compile")
	if compile.State != job.Cancelled {
		t.Errorf("the stopped step is %s, want %s", compile.State, job.Cancelled)
	}
	if manifest.State != job.Cancelled {
		t.Errorf("the build is %s, want %s — the stage's own answer is the build's answer", manifest.State, job.Cancelled)
	}
	if !strings.Contains(manifest.Error, "compile") {
		t.Errorf("the build does not say which stage was stopped: %s", manifest.Error)
	}
	if bsp := outputNamed(t, manifest.Outputs, "bsp"); !bsp.Missing {
		t.Error("a cancelled build published a BSP")
	}
}

// Build & Run cancels the BUILD'S CONTEXT, not a job. Before 246I1.1 that only
// stopped the waiting: the compiler ran on to completion in the background and
// the build was left `running` until something later labelled it as abandoned
// by a crash. The running job must be stopped, and the build end `cancelled`.
func TestCancellingTheBuildsContextStopsTheRunningJob(t *testing.T) {
	document := string(fixtureTool(t, "test.build.toolchain", "test.stage", "compile"))
	slow := strings.Replace(document, `,"compile",`, `,"sleep",`, 1)
	if slow == document {
		t.Fatal("the compile arguments were not replaced; this test is checking nothing")
	}
	h := newHarness(t, map[string][]byte{
		"tool.tool.json":         []byte(slow),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *Manifest, 1)
	go func() {
		manifest, _ := h.runner.Run(ctx, Request{
			PipelineID: "test.build.pipeline",
			Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
		})
		done <- manifest
	}()

	deadline := time.Now().Add(30 * time.Second)
	var running string
	for time.Now().Before(deadline) && running == "" {
		jobs, _ := h.service.List()
		for _, j := range jobs {
			if j.State == job.Running {
				running = j.ID
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if running == "" {
		t.Fatal("no stage ever started")
	}
	cancel()

	var manifest *Manifest
	select {
	case manifest = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the build did not stop")
	}
	stopped, err := h.service.Get(running)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != job.Cancelled {
		t.Errorf("the compiler job is %s after the build was cancelled, want %s", stopped.State, job.Cancelled)
	}
	if manifest == nil || manifest.State != job.Cancelled {
		t.Fatalf("the build is %v, want %s", manifest, job.Cancelled)
	}
	if !strings.Contains(manifest.Error, "cancelled") || strings.Contains(manifest.Error, InterruptedNote) {
		t.Errorf("the build's note is %q", manifest.Error)
	}
	if compile := stepNamed(t, manifest, "compile"); compile.State != job.Cancelled || compile.DurationMS == 0 {
		t.Errorf("the stopped step is %s after %d ms", compile.State, compile.DurationMS)
	}
}

// --strict is for a gate: a tool that warns and carries on is a build that
// failed, and the message says which stage and which rule.
func TestStrictFailsOnAnErrorDiagnosticTheToolItselfIgnored(t *testing.T) {
	document := string(fixtureTool(t, "test.build.toolchain", "test.stage", "warn"))
	h := newHarness(t, map[string][]byte{
		"tool.tool.json":         []byte(document),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	})
	request := Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	}
	relaxed := h.mustRun(request)
	if !relaxed.Succeeded() {
		t.Fatalf("the default build failed: %s", relaxed.Error)
	}
	compile := stepNamed(t, relaxed, "compile")
	if compile.Findings(profile.SeverityError) == 0 {
		t.Fatal("the fixture printed nothing an error rule matched; this test is checking nothing")
	}

	request.Strict = true
	strict, err := h.run(request)
	if err == nil {
		t.Fatal("--strict let a build through that reported an error")
	}
	if !strings.Contains(strict.Error, "compile") || !strings.Contains(strict.Error, "warned") {
		t.Errorf("--strict's message names neither the stage nor the rule: %s", strict.Error)
	}
	if !strict.Strict {
		t.Error("the manifest does not record that the build was strict")
	}
}

// Two providers of one capability is the failure the sample toolchain would
// have caused. It is refused and named rather than ranked.
func TestTwoProvidersOfOneCapabilityAreRefusedRatherThanRanked(t *testing.T) {
	h := newHarness(t, map[string][]byte{
		"tool.tool.json":         fixtureTool(t, "test.build.toolchain", "test.stage", "compile"),
		"second.tool.json":       fixtureTool(t, "test.build.other", "test.stage", "compile"),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	})
	_, err := h.run(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil {
		t.Fatal("a build ran with two profiles claiming the same capability")
	}
	for _, phrase := range []string{"test.build.toolchain", "test.build.other", "test.stage.compile"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Errorf("the refusal does not name %q: %v", phrase, err)
		}
	}
}

func TestAPipelineWhoseCapabilityNobodyProvidesSaysWhatIsInstalled(t *testing.T) {
	h := newHarness(t, map[string][]byte{
		"tool.tool.json":         fixtureTool(t, "test.build.toolchain", "test.other", "compile"),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	})
	_, err := h.run(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil {
		t.Fatal("a build ran with nothing providing its first capability")
	}
	if !strings.Contains(err.Error(), "test.stage.compile") || !strings.Contains(err.Error(), "test.other.compile") {
		t.Errorf("the refusal says neither what is needed nor what is installed: %v", err)
	}
}

// The manifest is the continuity record, so it exists before anything runs and
// is refreshed after every stage — not written once at the end, where a build
// that was interrupted would leave nothing behind.
func TestTheManifestIsOnDiskBeforeTheFirstStageAndAfterEachOne(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	saved, err := LoadManifest(filepath.Join(manifest.Directory, ManifestFileName))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if saved.BuildID != manifest.BuildID || saved.ReproducibleKey != manifest.ReproducibleKey {
		t.Error("the manifest on disk is not the one that was returned")
	}
	if len(saved.Steps) != 3 {
		t.Errorf("the saved manifest has %d steps", len(saved.Steps))
	}

	found, err := Find(h.builds, manifest.BuildID)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found.BuildID != manifest.BuildID {
		t.Error("Find returned a different build")
	}
	listed, err := List(h.builds)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("List found %d builds", len(listed))
	}
}

func TestAManifestFromAnotherFormatIsRefusedByName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":"aucom.build-manifest/9.0"}`), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("a manifest in an unknown format was read")
	}
	if !strings.Contains(err.Error(), "9.0") || !strings.Contains(err.Error(), SchemaVersion) {
		t.Errorf("the refusal does not name both versions: %v", err)
	}
}

// A build records what ran, which for a managed download means the digest of
// the bytes and not a version string somebody printed.
func TestTheManifestRecordsTheDigestOfEveryExecutableThatRan(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if len(manifest.Tools) != 1 {
		t.Fatalf("the manifest records %d tools", len(manifest.Tools))
	}
	tool := manifest.Tools[0]
	if tool.Profile.Digest == "" || tool.ToolVersion == "" {
		t.Errorf("the tool record is incomplete: %+v", tool)
	}
	if len(tool.Executables) != 1 {
		t.Fatalf("the tool record names %d executables", len(tool.Executables))
	}
	if tool.Executables[0].SHA256 == "" || tool.Executables[0].Size == 0 {
		t.Errorf("the executable that ran has no digest: %+v", tool.Executables[0])
	}
	if len(manifest.Inputs) != 1 || manifest.Inputs[0].SHA256 == "" {
		t.Errorf("the inputs are not recorded with digests: %+v", manifest.Inputs)
	}
}

// A pipeline is not a way around a grant.
func TestAProfileWithNoGrantCannotBeRunThroughAPipeline(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	// Rebuild the runner over a service whose bindings carry no grant.
	service, err := job.NewService(job.Options{
		Store:       h.service.Store(),
		Catalog:     h.service.Catalog(),
		Bindings:    func(string) (binding.LocalBinding, bool) { return binding.LocalBinding{}, false },
		Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("%v", err)
	}
	defer service.Close()
	runner, err := New(Options{Service: service, Dir: filepath.Join(h.dir, "builds2")})
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, err = runner.Run(ctx, Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil {
		t.Fatal("an ungranted profile ran through a pipeline")
	}
}
