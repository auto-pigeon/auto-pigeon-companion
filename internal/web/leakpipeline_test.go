package web

import (
	"net/http"
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
		map[string]string{"game": "quake2", "pipeline": "auto-pigeon.q2.normal"}); status != http.StatusNotFound {
		t.Errorf("a pin for a game with no leak test: %d", status)
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
