package build

import (
	"context"
	"encoding/json"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// namedPipeline is the fixture pipeline with every stage naming `tool`.
func namedPipeline(t *testing.T, id, capabilityPrefix, tool string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(fixturePipeline(t, id, capabilityPrefix), &document); err != nil {
		t.Fatal(err)
	}
	for _, step := range document["steps"].([]any) {
		step.(map[string]any)["tool"] = tool
	}
	return encode(t, document)
}

// twoProviders is two approved tools that both provide every stage's
// capability, and a pipeline naming `tool` on each stage.
func twoProviders(t *testing.T, tool string) map[string][]byte {
	return map[string][]byte{
		"a.tool.json":            fixtureTool(t, "test.build.a", "test.stage", "compile"),
		"b.tool.json":            fixtureTool(t, "test.build.b", "test.stage", "compile"),
		"pipeline.pipeline.json": namedPipeline(t, "test.build.pipeline", "test.stage", tool),
	}
}

// A stage that names its tool runs THAT tool, whichever of two approved
// providers it is, and the manifest records the exact tool and digest
// (NEW_310A §4.1).
func TestANamedStageRunsExactlyTheToolItNames(t *testing.T) {
	for _, named := range []string{"test.build.a", "test.build.b"} {
		h := newHarness(t, twoProviders(t, named))
		manifest := h.mustRun(Request{
			PipelineID: "test.build.pipeline",
			Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
		})
		entries, err := h.service.Catalog().List()
		if err != nil {
			t.Fatal(err)
		}
		digest := ""
		for _, entry := range entries {
			if entry.Profile.Metadata().ID == named {
				digest = entry.Digest
			}
		}
		for _, step := range manifest.Steps {
			if step.Profile.ID != named || step.Profile.Digest != digest {
				t.Errorf("naming %s, stage %s ran %s (%s)", named, step.ID, step.Profile.ID, step.Profile.Digest)
			}
			job, err := h.service.Store().Load(step.JobID)
			if err != nil {
				t.Fatal(err)
			}
			if job.ProfileID != named {
				t.Errorf("naming %s, the job for stage %s ran %s", named, step.ID, job.ProfileID)
			}
		}
		if len(manifest.Tools) != 1 || manifest.Tools[0].Profile.ID != named {
			t.Errorf("naming %s, the manifest's tools are %+v", named, manifest.Tools)
		}
	}
}

// A named tool that cannot answer is refused for that reason, and never
// answered by the other provider: not installed, not a tool, or without the
// capability.
func TestANamedToolThatCannotAnswerIsRefusedNeverReplaced(t *testing.T) {
	for _, c := range []struct {
		name, tool, want string
	}{
		{"missing", "test.build.gone", "is not installed"},
		{"wrong kind", "test.build.pipeline", "is a pipeline profile, not a tool"},
		{"missing capability", "test.build.c", `does not provide "test.stage.compile"`},
	} {
		documents := twoProviders(t, c.tool)
		documents["c.tool.json"] = fixtureTool(t, "test.build.c", "test.other", "compile")
		h := newHarness(t, documents)
		_, err := h.run(Request{
			PipelineID: "test.build.pipeline",
			Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
		})
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), c.tool) {
			t.Errorf("%s: %v", c.name, err)
		}
		if jobs, _ := h.service.Store().List(); len(jobs) != 0 {
			t.Errorf("%s: %d job(s) ran anyway", c.name, len(jobs))
		}
	}
}

// The named tool's approval is withdrawn while another approved tool provides
// the same capability: the build is refused, and nothing runs through the
// other provider.
func TestAWithdrawnNamedToolIsNotRunThroughAnotherProvider(t *testing.T) {
	h := newHarness(t, twoProviders(t, "test.build.b"))
	granted := h.runner.options.Bindings
	withoutB := func(id string) (binding.LocalBinding, bool) {
		local, found := granted(id)
		if id == "test.build.b" {
			local.Grant = nil
		}
		return local, found
	}
	service, err := job.NewService(job.Options{
		Store: h.service.Store(), Catalog: h.service.Catalog(), Bindings: withoutB, Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	runner, err := New(Options{Service: service, Dir: filepath.Join(h.dir, "builds2"), Bindings: withoutB})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := runner.Run(ctx, Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil {
		t.Fatal("a build ran with its named tool's approval withdrawn")
	}
	jobs, _ := service.Store().List()
	for _, j := range jobs {
		if j.ProfileID == "test.build.a" {
			t.Errorf("the stage ran through the other provider: job %s", j.ID)
		}
	}
	if manifest != nil {
		for _, step := range manifest.Steps {
			if step.Profile.ID != "" && step.Profile.ID != "test.build.b" {
				t.Errorf("stage %s recorded %s", step.ID, step.Profile.ID)
			}
		}
	}
}

// A leak test whose pinned pipeline publishes no log publishes the compile
// step's own text under the name the binding records, beside the pipeline's
// outputs (NEW_310A §4.2); an ordinary build publishes nothing extra.
func TestALeakTestPublishesTheCompileStagesTextWhenThePipelineDoesNot(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	binding := &leakadapter.Binding{Game: "quake1", Pipeline: "test.build.pipeline", Pointfile: "pts",
		Log: leakadapter.LeakLogOutput, LogFrom: "compile.stdout", CompileStep: "compile"}
	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
		LeakTest:   binding,
	})
	text := outputNamed(t, manifest.Outputs, leakadapter.LeakLogOutput)
	if text.Missing || text.Path == "" || text.SHA256 == "" || text.From != "compile.stdout" {
		t.Fatalf("the compile stage's text was not published for the leak test: %+v", text)
	}
	ordinary := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level2.map", "brushes\n")},
	})
	for _, output := range ordinary.Outputs {
		if output.Name == leakadapter.LeakLogOutput {
			t.Error("an ordinary build published the leak test's text")
		}
	}
}
