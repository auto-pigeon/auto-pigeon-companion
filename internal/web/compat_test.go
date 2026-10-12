//go:build !windows

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
)

// NEW_323D, at the surface a person and a script actually use: Profiles, the
// pipeline lists of Build & Run, the build route and the job route give ONE
// answer about a modern profile bound to programs it was not written for, and
// none of them starts a compiler. The fixture programs record their own
// arguments, so "never invoked" is read from what they wrote, not from what
// the server said.

const (
	ericwQ1       = "auto-pigeon.ericw-tools.q1"
	fixtureLegacy = "---- %s / ericw-tools v0.18.1 ----\nusage: %s [-threads #] [-level 0-4] [-fast] bspfile"
	fixtureModern = "---- %s / ericw-tools 2.0.0-alpha11 ----\n  -logfile \"path\"               File to output logging data to."
)

type compatFixture struct {
	t   *testing.T
	m   *machine
	log string
}

// release lays out a folder the way the built-in 2.0 profile describes it —
// five programs at the top — each printing banner when started with nothing.
func (f *compatFixture) release(name, banner string) string {
	f.t.Helper()
	root := filepath.Join(f.m.dir, name)
	for _, program := range []string{"qbsp", "vis", "light", "bspinfo", "bsputil"} {
		f.program(filepath.Join(root, program), program, banner)
	}
	return root
}

func (f *compatFixture) program(path, name, banner string) {
	f.t.Helper()
	script := fmt.Sprintf("#!/bin/sh\necho \"%s|$*\" >> '%s'\nif [ $# -eq 0 ]; then\ncat <<'BANNER'\n%s\nBANNER\nexit 1\nfi\necho compiled\n",
		name, f.log, fmt.Sprintf(banner, name, name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path+".new", []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		f.t.Fatal(err)
	}
}

// compiles is how many times any fixture program was started with arguments.
func (f *compatFixture) compiles() []string {
	f.t.Helper()
	data, err := os.ReadFile(f.log)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.HasSuffix(line, "|") {
			out = append(out, line)
		}
	}
	return out
}

func (f *compatFixture) profile() map[string]any {
	f.t.Helper()
	status, body := f.m.call(http.MethodGet, "/api/v1/profiles/"+ericwQ1, nil)
	if status != http.StatusOK {
		f.t.Fatalf("profile: %d %v", status, body)
	}
	return body
}

func (f *compatFixture) pipeline(id string) map[string]any {
	f.t.Helper()
	status, body := f.m.call(http.MethodGet, "/api/v1/build/pipelines", nil)
	if status != http.StatusOK {
		f.t.Fatalf("pipelines: %d %v", status, body)
	}
	return findItem(f.t, body, "id", id)
}

func ready(body map[string]any) bool {
	state, _ := body["readiness"].(map[string]any)
	return state["ready"] == true
}

func faults(body map[string]any) []string {
	state, _ := body["readiness"].(map[string]any)
	problems, _ := state["problems"].([]any)
	var out []string
	for _, raw := range problems {
		problem, _ := raw.(map[string]any)
		fault, _ := problem["fault"].(string)
		out = append(out, fault)
	}
	return out
}

func programState(body map[string]any, executable string) (state, identity string) {
	rows, _ := body["compatibility"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["executable"] == executable {
			state, _ = row["state"].(string)
			identity, _ = row["identity"].(string)
		}
	}
	return state, identity
}

func actionFaults(body map[string]any, action string) []string {
	perAction, _ := body["action_problems"].(map[string]any)
	problems, _ := perAction[action].([]any)
	var out []string
	for _, raw := range problems {
		problem, _ := raw.(map[string]any)
		fault, _ := problem["fault"].(string)
		out = append(out, fault)
	}
	return out
}

func newCompatFixture(t *testing.T) *compatFixture {
	m := newMachine(t)
	return &compatFixture{t: t, m: m, log: filepath.Join(m.dir, "invocations.log")}
}

func (f *compatFixture) bindFolder(folder string) map[string]any {
	f.t.Helper()
	status, body := f.m.call(http.MethodPost, "/api/v1/profiles/"+ericwQ1+"/bind", map[string]any{"folder": folder})
	if status != http.StatusOK {
		f.t.Fatalf("binding %s: %d %v", folder, status, body)
	}
	return body
}

func TestAModernProfileOnLegacyProgramsIsRefusedTheSameWayEverywhere(t *testing.T) {
	f := newCompatFixture(t)
	legacy := f.release("ericw-0.18.1-laid-out-flat", fixtureLegacy)

	// Profiles: binding it is not refused — it is the user's choice, and it
	// stays visible — and the answer comes back with what each program is.
	bound := f.bindFolder(legacy)
	if ready(bound) {
		t.Fatalf("Profiles calls a 2.0 profile on 0.18.1 programs ready: %v", bound["readiness"])
	}
	for _, program := range []string{"qbsp", "vis", "light"} {
		if state, identity := programState(bound, program); state != "incompatible" || identity != "v0.18.1" {
			t.Errorf("%s: state %q identity %q", program, state, identity)
		}
	}
	if got := strings.Join(faults(bound), ","); got != "tool_incompatible,tool_incompatible,tool_incompatible" {
		t.Errorf("Profiles' problems: %s", got)
	}
	recorded, _ := bound["binding"].(map[string]any)
	if executables, _ := recorded["executables"].(map[string]any); executables["vis"] != filepath.Join(legacy, "vis") {
		t.Errorf("the chosen programs were not kept as chosen: %v", executables)
	}
	// Per action: the compilers are stopped, the inspection program nothing is
	// required of is not.
	for action, want := range map[string]string{"compile": "tool_incompatible", "vis": "tool_incompatible", "light": "tool_incompatible", "inspect": "", "check": ""} {
		if got := strings.Join(actionFaults(bound, action), ","); got != want {
			t.Errorf("action %s: %q, want %q", action, got, want)
		}
	}

	// Build & Run: every built-in Quake 1 pipeline that uses it, with the same
	// named fault, and its provider still shown.
	for _, id := range []string{"auto-pigeon.q1.fast-preview", "auto-pigeon.q1.normal", "auto-pigeon.q1.final", "auto-pigeon.q1.leak-test"} {
		pipeline := f.pipeline(id)
		if ready(pipeline) {
			t.Errorf("%s is listed ready", id)
		}
		for _, fault := range faults(pipeline) {
			if fault != "tool_incompatible" {
				t.Errorf("%s: fault %q", id, fault)
			}
		}
		steps, _ := pipeline["steps"].([]any)
		first, _ := steps[0].(map[string]any)
		if provider, _ := first["provider"].(map[string]any); provider["id"] != ericwQ1 {
			t.Errorf("%s no longer shows the provider it is bound to: %v", id, first)
		}
	}

	// The build route and the job route, as a script would call them.
	status, body := f.m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "auto-pigeon.q1.fast-preview", "inputs": map[string]string{"source_map": f.m.writeSourceMap()}})
	if status != http.StatusConflict || body["class"] != "pipeline_not_ready" {
		t.Errorf("starting the build: %d %v", status, body)
	}
	status, body = f.m.call(http.MethodPost, "/api/v1/jobs", map[string]any{
		"profile": ericwQ1, "action": "vis", "inputs": map[string]string{"bsp": f.m.writeSourceMap(), "prt": f.m.writeSourceMap()}})
	if status != http.StatusConflict || body["class"] != "tool_incompatible" {
		t.Errorf("submitting vis directly: %d %v", status, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "v0.18.1") {
		t.Errorf("the job refusal does not say what vis is: %v", body)
	}
	if ran := f.compiles(); len(ran) != 0 {
		t.Fatalf("a program was started with arguments: %v", ran)
	}
	if list, _ := f.m.callList("/api/v1/build/runs", "items"); len(list) != 0 {
		t.Errorf("a refused build left a record: %v", list)
	}

	// Nothing was approved, substituted or forgotten by any of it.
	set, err := binding.LoadFile(f.m.bindings)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := set.Find(ericwQ1)
	if local.Grant != nil || local.Executables["vis"] != filepath.Join(legacy, "vis") || local.Compatibility["vis"].State != binding.FactIncompatible {
		t.Errorf("the binding after the refusals: grant %v, vis %s, fact %+v", local.Grant, local.Executables["vis"], local.Compatibility["vis"])
	}
}

func TestRebindingAndReplacingAProgramMovesReadinessBothWays(t *testing.T) {
	f := newCompatFixture(t)
	legacy := f.release("ericw-0.18.1-laid-out-flat", fixtureLegacy)
	modern := f.release("ericw-tools-2.0.0-alpha11", fixtureModern)

	if bound := f.bindFolder(modern); !ready(bound) {
		t.Fatalf("supported programs are not ready: %v", bound["readiness"])
	}
	if state, identity := programState(f.profile(), "vis"); state != "compatible" || identity != "2.0.0-alpha11" {
		t.Fatalf("vis: %q %q", state, identity)
	}
	if !ready(f.pipeline("auto-pigeon.q1.fast-preview")) {
		t.Fatal("the pipeline is not ready on supported programs")
	}

	// One mandatory program rebound to a legacy copy: Profiles and every
	// pipeline that needs it change at once; the pipeline that does not need
	// it does not.
	status, mixed := f.m.call(http.MethodPost, "/api/v1/profiles/"+ericwQ1+"/bind",
		map[string]any{"executables": map[string]string{"vis": filepath.Join(legacy, "vis")}})
	if status != http.StatusOK || ready(mixed) {
		t.Fatalf("after rebinding vis: %d ready=%v", status, ready(mixed))
	}
	if got := strings.Join(faults(mixed), ","); got != "tool_incompatible" {
		t.Errorf("only vis is wrong, and Profiles says: %s", got)
	}
	if got := strings.Join(actionFaults(mixed, "compile"), ","); got != "" {
		t.Errorf("a wrong vis took qbsp's compile away: %s", got)
	}
	if ready(f.pipeline("auto-pigeon.q1.fast-preview")) || !ready(f.pipeline("auto-pigeon.q1.leak-test")) {
		t.Error("the full pipeline must need setup and the qbsp-only leak test must not")
	}
	summary := f.pipeline("auto-pigeon.q1.fast-preview")["readiness"].(map[string]any)["problems"].([]any)[0].(map[string]any)["summary"].(string)
	if !strings.HasPrefix(summary, "Compute visibility: ") || !strings.Contains(summary, "v0.18.1") {
		t.Errorf("the pipeline's reason does not name the stage and the version: %s", summary)
	}

	// Restored through the same route: ready again, with no restart.
	status, restored := f.m.call(http.MethodPost, "/api/v1/profiles/"+ericwQ1+"/bind",
		map[string]any{"executables": map[string]string{"vis": filepath.Join(modern, "vis")}})
	if status != http.StatusOK || !ready(restored) || !ready(f.pipeline("auto-pigeon.q1.fast-preview")) {
		t.Fatalf("after restoring vis: %d %v", status, restored["readiness"])
	}

	// Replaced where it stands, behind the Companion's back. The page does not
	// go on showing the old badge: the answer on record is about another file.
	f.program(filepath.Join(modern, "vis"), "vis", fixtureLegacy)
	stale := f.profile()
	if state, _ := programState(stale, "vis"); state != "not_checked" || ready(stale) {
		t.Fatalf("a replaced vis still reads %q, ready=%v", state, ready(stale))
	}
	if got := strings.Join(faults(f.pipeline("auto-pigeon.q1.fast-preview")), ","); got != "tool_unverified" {
		t.Errorf("the pipeline over a replaced vis: %s", got)
	}
	// Even unasked by any page, it does not run.
	status, body := f.m.call(http.MethodPost, "/api/v1/jobs", map[string]any{
		"profile": ericwQ1, "action": "vis", "inputs": map[string]string{"bsp": f.m.writeSourceMap(), "prt": f.m.writeSourceMap()}})
	if status != http.StatusConflict || body["class"] != "tool_incompatible" {
		t.Errorf("the replaced vis, submitted directly: %d %v", status, body)
	}
	// "Check these programs" asks again and says what it found.
	status, checked := f.m.call(http.MethodPost, "/api/v1/profiles/"+ericwQ1+"/check", nil)
	if state, identity := programState(checked, "vis"); status != http.StatusOK || state != "incompatible" || identity != "v0.18.1" {
		t.Fatalf("check: %d %q %q", status, state, identity)
	}
	f.program(filepath.Join(modern, "vis"), "vis", fixtureModern)
	status, checked = f.m.call(http.MethodPost, "/api/v1/profiles/"+ericwQ1+"/check", nil)
	if status != http.StatusOK || !ready(checked) || !ready(f.pipeline("auto-pigeon.q1.fast-preview")) {
		t.Fatalf("check after restoring the file: %d %v", status, checked["readiness"])
	}
	if ran := f.compiles(); len(ran) != 0 {
		t.Errorf("a program was started with arguments: %v", ran)
	}
}

// A startup with bindings an older build recorded, or programs changed while
// the Companion was closed: asked once, without a page.
func TestStartingUpAsksWhatNothingHasAsked(t *testing.T) {
	f := newCompatFixture(t)
	modern := f.release("ericw-tools-2.0.0-alpha11", fixtureModern)
	f.bindFolder(modern)
	// As an older build left it: paths, and no answers.
	if _, err := binding.Update(f.m.bindings, func(set *binding.Set) error {
		local, _ := set.Find(ericwQ1)
		local.Compatibility = nil
		return set.Put(local)
	}); err != nil {
		t.Fatal(err)
	}
	if state, _ := programState(f.profile(), "vis"); state != "not_checked" {
		t.Fatalf("with no answer on record vis reads %q", state)
	}
	f.m.server.RefreshCompatibility()
	if body := f.profile(); !ready(body) {
		t.Fatalf("after the startup check: %v", body["readiness"])
	}
	// And it does not ask again when the answers still describe the files.
	before, _ := os.ReadFile(f.log)
	f.m.server.RefreshCompatibility()
	if after, _ := os.ReadFile(f.log); len(after) != len(before) {
		t.Errorf("a second startup check probed again:\n%s", after[len(before):])
	}
	// A page poll reads; it starts nothing.
	for i := 0; i < 2; i++ {
		f.profile()
		f.pipeline("auto-pigeon.q1.fast-preview")
		f.m.call(http.MethodGet, "/api/v1/profiles", nil)
	}
	if after, _ := os.ReadFile(f.log); len(after) != len(before) {
		t.Errorf("reading the lists started a program:\n%s", after[len(before):])
	}
}

// An optional stage whose program is wrong does not stop a pipeline; a
// mandatory one does. The same tool, the same binding, two pipelines.
func TestAnOptionalStageWithTheWrongProgramDoesNotStopItsPipeline(t *testing.T) {
	f := newCompatFixture(t)
	legacy := f.release("ericw-0.18.1-laid-out-flat", fixtureLegacy)
	modern := f.release("ericw-tools-2.0.0-alpha11", fixtureModern)
	f.bindFolder(modern)
	for _, program := range []string{"vis", "light"} {
		if status, body := f.m.call(http.MethodPost, "/api/v1/profiles/"+ericwQ1+"/bind",
			map[string]any{"executables": map[string]string{program: filepath.Join(legacy, program)}}); status != http.StatusOK {
			t.Fatalf("rebinding %s: %d %v", program, status, body)
		}
	}

	raw, err := os.ReadFile("../profile/builtin/q1-fast-preview.pipeline.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	const id = "local.pipeline.optional-vis-and-light"
	document["id"], document["name"] = id, "Quake 1 — compile, then light it if you can"
	for _, raw := range document["steps"].([]any) {
		if step := raw.(map[string]any); step["id"] != "compile" {
			step["optional"] = true
		}
	}
	// What the build produces when the optional stages do not run.
	for _, raw := range document["outputs"].([]any) {
		if output := raw.(map[string]any); output["name"] == "bsp" {
			output["from"], output["role"] = "compile.bsp", "q1.bsp"
		}
	}
	f.m.writeProfile(id+".json", encodeFixture(document))
	status, body := f.m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("the optional pipeline was not installed: %d %v", status, body)
	}
	if status, body = f.m.call(http.MethodPost, "/api/v1/profiles/"+id+"/grant", map[string]any{"digest": body["digest"]}); status != http.StatusOK {
		t.Fatalf("approving it: %d %v", status, body)
	}

	if optional := f.pipeline(id); !ready(optional) {
		t.Errorf("a pipeline whose only wrong programs run optional stages needs setup: %v", optional["readiness"])
	}
	mandatory := f.pipeline("auto-pigeon.q1.fast-preview")
	if ready(mandatory) || strings.Join(faults(mandatory), ",") != "tool_incompatible,tool_incompatible" {
		t.Errorf("the pipeline that needs vis and light: %v", mandatory["readiness"])
	}
}
