package profile

import (
	"strings"
	"testing"
)

const namedStagePipeline = `{
  "schema_version": "aucom.profile/1.1", "kind": "pipeline", "id": "local.pipeline.named", "version": "1.0.0",
  "name": "Named stages", "summary": "Two stages naming one tool.",
  "publisher": {"name": "Tests"}, "license": {"spdx": "MIT", "name": "MIT"},
  "inputs": [{"name": "source_map", "title": "Map", "role": "q1.map.source", "required": true, "extensions": [".map"]}],
  "steps": [
    {"id": "compile", "title": "Compile", "capability": "q1.bsp.compile", "tool": "local.tool.my-ericw",
     "inputs": [{"name": "source_map", "from": "pipeline.source_map"}]},
    {"id": "light", "title": "Light", "capability": "q1.bsp.light", "tool": "local.tool.my-ericw",
     "inputs": [{"name": "bsp", "from": "compile.bsp"}]}
  ],
  "outputs": [{"name": "bsp", "title": "BSP", "role": "q1.bsp.lit", "from": "light.bsp"}]
}`

// A stage's named tool survives decoding and canonical encoding, is part of
// what the digest covers (so naming another tool needs a new approval), and a
// document without it reads as before (NEW_310A §4.1).
func TestAStagesNamedToolRoundTripsAndIsCoveredByTheDigest(t *testing.T) {
	pipeline, err := DecodePipeline([]byte(namedStagePipeline))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range pipeline.Steps {
		if step.Tool != "local.tool.my-ericw" {
			t.Fatalf("stage %s decoded with tool %q", step.ID, step.Tool)
		}
	}
	canonical, err := Canonical(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DecodePipeline(canonical)
	if err != nil {
		t.Fatalf("the canonical form does not decode: %v", err)
	}
	if again.Steps[1].Tool != "local.tool.my-ericw" {
		t.Fatalf("the tool was lost in the canonical form: %s", canonical)
	}
	before, _ := Digest(pipeline)
	pipeline.Steps[1].Tool = "local.tool.other-ericw"
	after, _ := Digest(pipeline)
	if before == after {
		t.Error("naming another tool did not change the digest an approval is bound to")
	}

	legacy := strings.ReplaceAll(namedStagePipeline, `"tool": "local.tool.my-ericw",`, "")
	old, err := DecodePipeline([]byte(legacy))
	if err != nil || old.Steps[0].Tool != "" {
		t.Fatalf("a document without stage tools no longer reads: %v", err)
	}

	bad := strings.Replace(namedStagePipeline, `"tool": "local.tool.my-ericw",`, `"tool": "Not An Id!",`, 1)
	if _, err := DecodePipeline([]byte(bad)); err == nil {
		t.Error("a stage tool that is not a profile id was accepted")
	}
}
