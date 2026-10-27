package web

import (
	"net/http"
	"strings"
	"testing"
)

// NEW_360, found live on Windows: the New profile form installed a pipeline and
// only then learned that one of its stage arguments repeated a switch the stage
// already passes. Setup stayed incomplete, every field was locked, and Retry
// sent the same refused token again. Composing now asks the same refusal that
// saving asks, so the form says it while the argument can still be corrected
// and nothing has been installed.
func TestARefusedStageArgumentIsRefusedWhenComposingAndNothingIsInstalled(t *testing.T) {
	m := newMachine(t)
	m.installThreeStageTools()

	request := wizardPipeline("NEW360 refused argument", "1.0.0", "aucom.fixture.stock3")
	request["stage_arguments"] = []map[string]any{
		{"stage": "vis", "arguments": []string{"-keep"}},
		{"stage": "qbsp", "arguments": []string{"-fine", buildHelperFlag}},
	}
	status, composed := m.call(http.MethodPost, "/api/v1/profiles/compose", request)
	if status != http.StatusOK || composed["valid"] != false {
		t.Fatalf("compose = %d, valid = %v; a stage argument the stage always passes must make the draft invalid", status, composed["valid"])
	}
	problems, _ := composed["problems"].([]any)
	if len(problems) != 1 {
		t.Fatalf("compose problems = %v, want one naming the stage", composed["problems"])
	}
	problem, _ := problems[0].(map[string]any)
	if problem["path"] != "stage_arguments.qbsp" || !strings.Contains(problem["message"].(string), buildHelperFlag) ||
		!strings.Contains(problem["message"].(string), "argument 2") {
		t.Fatalf("the problem is %v; it must name the stage and the refused argument", problem)
	}
	if composed["document"] == nil {
		t.Fatalf("an invalid draft must still come back so the form keeps it")
	}
	// Nothing was installed: compose writes nothing.
	if status, _ := m.call(http.MethodGet, "/api/v1/profiles/local.pipeline.new360-refused-argument", nil); status != http.StatusNotFound {
		t.Fatalf("a refused draft is installed: GET = %d", status)
	}

	// Corrected in the form, the same draft composes, installs once and its
	// arguments save — each one argv element on its own stage only.
	request["stage_arguments"] = []map[string]any{
		{"stage": "vis", "arguments": []string{"-keep"}},
		{"stage": "qbsp", "arguments": []string{"-fine", "C:\\a folder\\with spaces.txt"}},
	}
	id, _ := m.installComposed(request, false)
	for _, item := range request["stage_arguments"].([]map[string]any) {
		if status, body := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/stage-arguments", item); status != http.StatusOK {
			t.Fatalf("saving %v = %d: %v", item, status, body["error"])
		}
	}
	_, page := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
	got := map[string][]string{}
	for _, raw := range page["stages"].([]any) {
		stage := raw.(map[string]any)
		got[stage["id"].(string)] = stringsOf(stage["arguments"])
	}
	if !equalStrings(got["qbsp"], []string{"-fine", "C:\\a folder\\with spaces.txt"}) ||
		!equalStrings(got["vis"], []string{"-keep"}) || len(got["light"]) != 0 {
		t.Fatalf("the installed stages carry %v", got)
	}
}

// Saving on the installed pipeline's page still refuses the same token with the
// same words: there is one check, not two that could disagree.
func TestComposingAndSavingRefuseAStageArgumentInTheSameWords(t *testing.T) {
	m := newMachine(t)
	m.installThreeStageTools()

	id, _ := m.installComposed(wizardPipeline("NEW360 same words", "1.0.0", "aucom.fixture.stock3"), false)
	refused := map[string]any{"stage": "qbsp", "arguments": []string{buildHelperFlag}}
	status, saved := m.call(http.MethodPost, "/api/v1/profiles/"+id+"/stage-arguments", refused)
	if status != http.StatusBadRequest {
		t.Fatalf("saving a refused token = %d", status)
	}
	request := wizardPipeline("NEW360 same words", "1.0.1", "aucom.fixture.stock3")
	request["stage_arguments"] = []map[string]any{refused}
	_, composed := m.call(http.MethodPost, "/api/v1/profiles/compose", request)
	if composed["valid"] != false || composed["error"] != saved["error"] {
		t.Fatalf("compose said %q (valid %v); saving said %q", composed["error"], composed["valid"], saved["error"])
	}
}
