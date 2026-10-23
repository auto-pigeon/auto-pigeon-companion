package web

import (
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
)

// unpinned is a machine where nobody has chosen a leak-test pipeline yet —
// the state a fresh install is in (NEW_310, HITL: no default).
func unpinned(t *testing.T) *machine {
	t.Helper()
	m := newMachine(t)
	m.settings.LeakTestPipelines = nil
	m.server.mu.Lock()
	m.server.settings.LeakTestPipelines = nil
	m.server.mu.Unlock()
	return m
}

// Nothing pinned: the review says so and names no pipeline, the page is given
// the choices, a pipeline that cannot test that game is refused, and once one
// is pinned the same request is reviewed with it — and a build of anything
// else is refused.
func TestALeakRequestForAGameWithNoPinnedPipelineAsksForOne(t *testing.T) {
	m := unpinned(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	link := m.receiveLeakLink(strings.Repeat("5", 32), "")

	status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
	if status != http.StatusOK || body["needs_pipeline"] != true || body["pipeline"] != nil {
		t.Fatalf("unpinned review: %d %v", status, body)
	}
	if _, err := m.server.leakBindingForBuild("auto-pigeon.q1.leak-test", build.Conversion{Game: "quake1"}, true); err == nil {
		t.Error("a leak build ran with nothing pinned")
	}

	status, body = m.call(http.MethodGet, "/api/v1/leak-test/pipelines?game=quake1", nil)
	if status != http.StatusOK || body["pinned"] != "" {
		t.Fatalf("choices: %d %v", status, body)
	}
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 || choices[0].(map[string]any)["id"] != "auto-pigeon.q1.leak-test" {
		t.Fatalf("the built-in leak test is not offered first: %v", choices)
	}
	for _, choice := range choices {
		if id := choice.(map[string]any)["id"].(string); strings.Contains(id, ".q3.") || strings.Contains(id, ".q2.") {
			t.Errorf("a %s pipeline is offered for Quake 1 leak tests", id)
		}
		if _, ok := choice.(map[string]any)["readiness"].(map[string]any); !ok {
			t.Errorf("a choice without readiness: %v", choice)
		}
	}

	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/pipelines",
		map[string]string{"game": "quake1", "pipeline": "auto-pigeon.q3.leak-test"}); status != http.StatusConflict {
		t.Errorf("a Quake III pipeline pinned for Quake 1: %d", status)
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/pipelines",
		map[string]string{"game": "hexen2", "pipeline": "auto-pigeon.q2.normal"}); status != http.StatusNotFound {
		t.Errorf("a pin for a game with no leak test: %d", status)
	}
	// Quake II has a leak test since Q2_010, and the Quake 1 one is not it.
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/pipelines",
		map[string]string{"game": "quake2", "pipeline": "auto-pigeon.q1.leak-test"}); status != http.StatusConflict {
		t.Errorf("the Quake 1 leak test pinned for Quake II: %d", status)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/leak-test/pipelines",
		map[string]string{"game": "quake1", "pipeline": "auto-pigeon.q1.leak-test"}); status != http.StatusOK {
		t.Fatalf("pinning the built-in leak test: %d %v", status, body)
	}
	if m.settings.LeakTestPipelines["quake1"] != "auto-pigeon.q1.leak-test" {
		t.Errorf("the pin was not saved: %v", m.settings.LeakTestPipelines)
	}

	status, body = m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
	if status != http.StatusOK || body["pipeline"] != "auto-pigeon.q1.leak-test" || body["needs_pipeline"] != nil {
		t.Fatalf("pinned review: %d %v", status, body)
	}
	if binding, err := m.server.leakBindingForBuild("auto-pigeon.q1.leak-test", build.Conversion{Game: "quake1"}, true); err != nil ||
		binding.Pointfile != "pts" || binding.CompileStep != "compile" {
		t.Errorf("the pinned build: %+v %v", binding, err)
	}
	if _, err := m.server.leakBindingForBuild("auto-pigeon.q1.normal", build.Conversion{Game: "quake1"}, true); err == nil {
		t.Error("a build of a pipeline other than the pinned one was accepted as the leak test")
	}

	// Unpinned again, the next review asks again.
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/pipelines",
		map[string]string{"game": "quake1", "pipeline": ""}); status != http.StatusOK {
		t.Fatalf("unpinning: %d", status)
	}
	if _, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil); body["needs_pipeline"] != true {
		t.Errorf("unpinned again: %v", body)
	}
}

// A pin to a pipeline that has since been removed does not run something
// else: the review asks again and says why.
func TestAPinnedLeakPipelineThatIsGoneIsAskedForAgain(t *testing.T) {
	m := unpinned(t)
	gone := map[string]string{"quake1": "local.pipeline.removed"}
	m.settings.LeakTestPipelines = gone // what config.json says, which a sign-in re-reads
	m.server.mu.Lock()
	m.server.settings.LeakTestPipelines = gone
	m.server.mu.Unlock()
	m.backend.savedAPMap("quake1")
	m.signIn()
	link := m.receiveLeakLink(strings.Repeat("6", 32), "")
	_, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
	problem, _ := body["pinned_problem"].(string)
	if body["needs_pipeline"] != true || body["pipeline"] != nil || !strings.Contains(problem, "local.pipeline.removed") {
		t.Errorf("a pin to a removed pipeline: %v", body)
	}
}

// A leak result is read from the binding its own build recorded, never from
// today's pin: a user's pipeline that published its route and its text under
// its own output names is read by those names after the pin has moved on, and
// the compiler's version is the version of the tool that ran the bound compile
// stage, not of the first tool the manifest lists (NEW_310A §4.2).
func TestALeakResultIsReadFromItsOwnBuildsBindingNotTodaysPin(t *testing.T) {
	m := newMachine(t)
	const id = "20261007T000000Z-00000042"
	manifest := m.q3LeakBuild(id, "b_gap", job.Failed, true, false)
	for i := range manifest.Outputs {
		switch manifest.Outputs[i].Name {
		case "lin":
			manifest.Outputs[i].Name = "route"
		case "compile_log":
			manifest.Outputs[i].Name = "stdout_text"
		}
	}
	manifest.Pipeline = build.DocumentRef{ID: "local.pipeline.my-q3"}
	manifest.LeakTest = &leakadapter.Binding{Game: "quake3", Pipeline: "local.pipeline.my-q3",
		Pointfile: "route", Log: "stdout_text", BSP: "bsp", CompileStep: "compile"}
	manifest.Tools = []build.ToolRecord{
		{Profile: build.DocumentRef{ID: "local.tool.other"}, ToolVersion: "9.9.9"},
		{Profile: build.DocumentRef{ID: "local.tool.my-q3map2"}, ToolVersion: "2.5.17n-git-68ecbed"},
	}
	manifest.Steps[0].Profile = build.DocumentRef{ID: "local.tool.my-q3map2"}
	if err := manifest.Save(filepath.Join(m.builds, id)); err != nil {
		t.Fatal(err)
	}
	// The pin has since moved to the built-in pipeline.
	m.server.mu.Lock()
	m.server.settings.LeakTestPipelines = map[string]string{"quake3": "auto-pigeon.q3.leak-test"}
	m.server.mu.Unlock()

	status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil)
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, body)
	}
	if pointfile, _ := body["pointfile"].(string); !strings.HasPrefix(pointfile, "280.000000 136.000000 128.000000") {
		t.Errorf("the route was not read from the build's own output name: %v", body["pointfile"])
	}
	if log, _ := body["log"].(string); !strings.Contains(log, "Entity leaked") {
		t.Errorf("the compiler's text was not read from the build's own output name")
	}
	if body["compiler_version"] != "2.5.17n-git-68ecbed" {
		t.Errorf("the compiler version is %v, not the bound compile stage's tool's", body["compiler_version"])
	}
}

// TestAnUnreadyChoiceNamesTheToolToSetUp (NEW_345): every unready leak-test
// choice said the same English sentence four times, and its setup link went to
// the PIPELINE's page, where nothing can be set up. Each problem now carries the
// tool profile, the stage and the program it is about, so the page can group
// one missing tool across pipelines and link to where it is fixed.
func TestAnUnreadyChoiceNamesTheToolToSetUp(t *testing.T) {
	m := newMachine(t)
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/pipelines?game=quake1", nil)
	if status != http.StatusOK {
		t.Fatalf("choices: %d %v", status, body)
	}
	choices, _ := body["choices"].([]any)
	unready := 0
	for _, raw := range choices {
		choice := raw.(map[string]any)
		readiness := choice["readiness"].(map[string]any)
		if readiness["ready"] == true {
			continue
		}
		unready++
		problems, _ := readiness["problems"].([]any)
		if len(problems) == 0 {
			t.Fatalf("%v is not ready and says nothing", choice["id"])
		}
		for _, raw := range problems {
			problem := raw.(map[string]any)
			profile, _ := problem["profile"].(string)
			if profile == "" || profile == choice["id"] || problem["profile_name"] == "" || problem["executable"] == "" || problem["step"] == "" {
				t.Errorf("%v: a problem that does not name the tool to set up: %v", choice["id"], problem)
			}
		}
	}
	if unready == 0 {
		t.Fatal("nothing is bound on a new machine, yet every choice is ready")
	}
}
