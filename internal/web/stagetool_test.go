package web

import (
	"os"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// A pipeline's page says which tool runs each stage. When the stage names its
// tool, that is the tool it shows — not whichever installed profile provides
// the same capability first (NEW_310, found live on Windows: a stage naming
// the user's own EricW was shown as "Run by ericw-tools 0.18.1"). A named tool
// that is not installed is said by name.
func TestAStagesPageNamesTheToolTheStageNames(t *testing.T) {
	m := newMachine(t)
	raw, err := os.ReadFile("../profile/builtin/ericw-tools-q1.tool.json")
	if err != nil {
		t.Fatal(err)
	}
	m.writeProfile("my-ericw.tool.json",
		[]byte(strings.Replace(string(raw), `"auto-pigeon.ericw-tools.q1"`, `"local.tool.my-ericw"`, 1)))
	leak, err := builtin.Find("auto-pigeon.q1.leak-test")
	if err != nil {
		t.Fatal(err)
	}
	pipeline := *leak.Profile.(*profile.PipelineProfile)
	pipeline.Steps = append([]profile.PipelineStep(nil), pipeline.Steps...)

	for _, named := range []string{builtin.EricwQ1, "local.tool.my-ericw"} {
		pipeline.Steps[0].Tool = named
		stages := m.server.describeStages(&pipeline, binding.LocalBinding{})
		tool, _ := stages[0]["tool"].(map[string]any)
		if tool["profile_id"] != named || stages[0]["named_tool"] != named {
			t.Errorf("a stage naming %s is shown as run by %v", named, tool["profile_id"])
		}
	}

	pipeline.Steps[0].Tool = "local.tool.gone"
	stages := m.server.describeStages(&pipeline, binding.LocalBinding{})
	if _, shown := stages[0]["tool"]; shown || stages[0]["named_tool"] != "local.tool.gone" {
		t.Errorf("a stage naming a tool that is not installed: %v", stages[0])
	}
}

// Build & Run asks the pipeline, resolved as a build of it would be, whether it
// reads a texture folder; the built-in Quake 1 builds do.
func TestBuildAndRunAsksThePipelineWhetherItReadsTheTextureFolder(t *testing.T) {
	m := newMachine(t)
	if reads, err := m.server.pipelineDeclaresRoot("auto-pigeon.q1.normal", "content_root"); err != nil || !reads {
		t.Errorf("the built-in Quake 1 normal build: %v %v", reads, err)
	}
	if reads, err := m.server.pipelineDeclaresRoot("auto-pigeon.q1.normal", "project_root"); err != nil || reads {
		t.Errorf("a role no step declares: %v %v", reads, err)
	}
	if _, err := m.server.pipelineDeclaresRoot("local.pipeline.gone", "content_root"); err == nil {
		t.Error("a pipeline that is not installed was answered")
	}
}
