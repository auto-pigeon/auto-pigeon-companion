package web

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
)

// NEW_364: a profile's row and page say ONE state, and the server decides it
// from the checker's faults — never the page from a sentence. Installed,
// approved, compatible and ready are different facts, so they are different
// states rather than one green "Installed".
func TestAProfileSaysOneStateDecidedFromFaults(t *testing.T) {
	problem := func(fault engine.Fault) engine.Problem { return engine.Problem{Fault: fault, Summary: string(fault)} }
	actions := []string{"play_map", "host_listen", "host_dedicated"}
	cases := []struct {
		name  string
		in    readiness
		state string
		ready []string
	}{
		{"every action can start", readiness{Ready: true, PerAction: map[string]engine.Problems{"play_map": nil, "host_listen": nil, "host_dedicated": nil}}, "ready", nil},
		{"only play_map can start", readiness{Ready: true, PerAction: map[string]engine.Problems{
			"play_map": nil, "host_listen": {problem(engine.FaultUnboundRoot)}, "host_dedicated": {problem(engine.FaultMissingEngine)}}},
			"partly_ready", []string{"play_map"}},
		{"nothing chosen yet", readiness{Problems: engine.Problems{problem(engine.FaultMissingEngine), problem(engine.FaultUnboundRoot)}}, "needs_setup", nil},
		{"not approved", readiness{Problems: engine.Problems{problem(engine.FaultNotAuthorized)}}, "needs_approval", nil},
		{"changed since approved", readiness{Problems: engine.Problems{problem(engine.FaultStaleBinding)}}, "needs_approval", nil},
		{"a program of another version", readiness{Problems: engine.Problems{problem(engine.FaultMissingEngine), problem(engine.FaultToolIncompatible)}}, "incompatible", nil},
		{"a program that cannot run here", readiness{Problems: engine.Problems{problem(engine.FaultToolUnusable)}}, "incompatible", nil},
		{"not for this platform", readiness{Problems: engine.Problems{problem(engine.FaultUnsupportedPlatform), problem(engine.FaultNotAuthorized)}}, "unavailable", nil},
		{"a pipeline waiting for its light tool", readiness{Problems: engine.Problems{problem(engine.FaultMissingEngine)}}, "needs_setup", nil},
		{"a ready pipeline", readiness{Ready: true}, "ready", nil},
	}
	for _, c := range cases {
		got := c.in.setupState(actions)
		if got["state"] != c.state {
			t.Errorf("%s: state %v, want %s", c.name, got["state"], c.state)
		}
		ready, _ := got["ready_actions"].([]string)
		if !reflect.DeepEqual(ready, c.ready) {
			t.Errorf("%s: ready_actions %v, want %v", c.name, ready, c.ready)
		}
	}
}

// The Profiles page's own decisions — status words, stale-response fences,
// argument revision ownership, wizard drafts — run as shipped.
func TestProfilesPageDecisions(t *testing.T) {
	out, err := exec.Command("node", "testdata/profilespage.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("profilespage.check.mjs: %v\n%s", err, out)
	}
}

// A pipeline's page shows each stage's exact command: the tool that runs it,
// the stage's options, and its own arguments before the operands — a path
// with spaces one word — read from the same planner as a tool's preview and
// in the executor's order (NEW_364).
func TestAPipelineShowsEachStagesExactCommand(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	const pipeline = "/api/v1/profiles/aucom.fixture.pipeline"
	spaced := filepath.Join(m.dir, "a folder with spaces", "extra.txt")
	tokens := []string{"-nopercent", spaced}
	if status, body := m.call(http.MethodPost, pipeline+"/stage-arguments",
		map[string]any{"stage": "compile", "arguments": tokens}); status != http.StatusOK {
		t.Fatalf("saving = %d: %v", status, body["error"])
	}
	status, body := m.call(http.MethodGet, pipeline+"/commands", nil)
	if status != http.StatusOK {
		t.Fatalf("commands = %d: %v", status, body["error"])
	}
	items, _ := body["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("a pipeline shows no commands: %v", body)
	}
	compile, _ := items[0].(map[string]any)
	argv := stringsOf(compile["argv"])
	if compile["action_id"] != "compile" || compile["error"] != nil || len(argv) < 3 {
		t.Fatalf("the compile stage's command is %v", compile)
	}
	if !equalStrings(stringsOf(compile["custom_args"]), tokens) {
		t.Errorf("the stage's own arguments are %v, want %v", compile["custom_args"], tokens)
	}
	at := int(compile["custom_at"].(float64))
	if !equalStrings(argv[at:at+2], tokens) {
		t.Errorf("the tokens are not one word each at %d: %q", at, argv)
	}
}
