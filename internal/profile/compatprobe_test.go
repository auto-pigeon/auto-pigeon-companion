package profile

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// NEW_323D: what a document may say about the programs it was written for.

func probedTool(t *testing.T, mutate func(document map[string]any)) error {
	t.Helper()
	raw, err := os.ReadFile("builtin/ericw-tools-q1.tool.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	// Somebody's own document: the built-in id is reserved.
	document["id"] = "local.tool.probed"
	if mutate != nil {
		mutate(document)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		return err
	}
	return decoded.Validate()
}

func TestACompatibilityProbeIsValidatedLikeTheRestOfTheDocument(t *testing.T) {
	if err := probedTool(t, nil); err != nil {
		t.Fatalf("the built-in document, as somebody's own: %v", err)
	}
	probe := func(document map[string]any) map[string]any {
		return document["compatibility_probe"].(map[string]any)
	}
	for name, c := range map[string]struct {
		mutate func(document map[string]any)
		want   string
	}{
		"accepts nothing": {func(d map[string]any) { probe(d)["accept"] = []any{} }, "accepts nothing"},
		"an empty rule":   {func(d map[string]any) { probe(d)["accept"] = []any{map[string]any{}} }, "accepts every program"},
		"probes nothing":  {func(d map[string]any) { probe(d)["executables"] = []any{} }, "probe of nothing"},
		"an undeclared executable": {func(d map[string]any) {
			probe(d)["executables"] = []any{map[string]any{"name": "q3map2"}}
		}, "undeclared executable"},
		"the same executable twice": {func(d map[string]any) {
			probe(d)["executables"] = []any{map[string]any{"name": "vis"}, map[string]any{"name": "vis"}}
		}, "repeats the name"},
		"no marker to read a name by": {func(d map[string]any) {
			probe(d)["parse"] = map[string]any{"kind": "after_marker"}
		}, "needs a marker"},
		"an unbounded probe":          {func(d map[string]any) { probe(d)["timeout_seconds"] = 3600 }, "between 0 and 300"},
		"a shell line as an argument": {func(d map[string]any) { probe(d)["args"] = []any{"--version; rm -rf ~"} }, ""},
		"an unknown member":           {func(d map[string]any) { probe(d)["fallback"] = "0.18.1" }, "fallback"},
	} {
		err := probedTool(t, c.mutate)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refused for another reason: %v", name, err)
		}
	}
}

// The probe is an action only by its reserved id: it cannot be declared, it is
// not listed, and it reaches nothing but the job's own folder.
func TestTheProbeActionIsNotSomethingADocumentCanDeclare(t *testing.T) {
	err := probedTool(t, func(d map[string]any) {
		actions := d["actions"].([]any)
		first := actions[0].(map[string]any)
		first["id"] = CompatibilityProbeActionPrefix + "qbsp"
	})
	if err == nil {
		t.Fatal("a document declared an action with the probe's id")
	}
	raw, _ := os.ReadFile("builtin/ericw-tools-q1.tool.json")
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	action, found := decoded.ActionByID(CompatibilityProbeActionPrefix + "light")
	if !found {
		t.Fatal("no probe action for light")
	}
	if len(action.Args)+len(action.Inputs)+len(action.Outputs)+len(action.Roots) != 0 || action.Network != nil || action.Environment != nil ||
		action.WorkingDir == nil || action.WorkingDir.Root != RootWorkspace || action.TimeoutSeconds != 10 {
		t.Errorf("the probe action reaches more than its own folder: %+v", action)
	}
}
