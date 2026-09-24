package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/pathpick"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// What the interface says when something is wrong.
//
// Each of these asserts on the CAUSE rather than on a status code, because a
// user acting on "something went wrong" and a user acting on "nothing on this
// machine provides fixture.compile" do different things next, and only one of
// them gets anywhere. The messages here are the ones the page renders verbatim.

func TestUnavailableToolIsNamed(t *testing.T) {
	m := newMachine(t)
	// The toolchain removed, which is the state of a machine where the compiler
	// has not been installed yet.
	if err := os.Remove(filepath.Join(m.profiles, "aucom.fixture.toolchain.json")); err != nil {
		t.Fatal(err)
	}

	status, body := m.call(http.MethodGet, "/api/v1/build/pipelines", nil)
	if status != http.StatusOK {
		t.Fatalf("pipelines = %d: %v", status, body["error"])
	}
	pipeline := findItem(t, body, "id", "aucom.fixture.pipeline")
	if pipeline["runnable"] != false {
		t.Fatal("a pipeline whose tool is not installed reported itself runnable")
	}
	missing, _ := pipeline["missing_capabilities"].([]any)
	if len(missing) != 1 || missing[0] != "fixture.compile" {
		t.Fatalf("missing_capabilities = %v, want the capability nothing provides", missing)
	}
	// And the step says which one it is, so the page can mark the stage rather
	// than only the pipeline.
	steps, _ := pipeline["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	if _, provided := step["provider"]; provided {
		t.Fatal("a step with no provider still reported one")
	}
}

func TestMissingSetupIsNamedPerAction(t *testing.T) {
	m := newMachine(t)

	status, body := m.call(http.MethodGet, "/api/v1/engines/aucom.fixture.q1-engine", nil)
	if status != http.StatusOK {
		t.Fatalf("engine = %d: %v", status, body["error"])
	}
	problems, _ := body["action_problems"].(map[string]any)
	playMap, _ := problems["play_map"].([]any)
	faults := map[string]string{}
	for _, raw := range playMap {
		problem, _ := raw.(map[string]any)
		fault, _ := problem["fault"].(string)
		summary, _ := problem["summary"].(string)
		fix, _ := problem["fix"].(string)
		faults[fault] = summary + " / " + fix
		// Every problem carries both halves: what is wrong, and what to do
		// about it. One without the other is a message that leaves somebody
		// where they started.
		if summary == "" {
			t.Errorf("the %s problem says nothing about what is wrong", fault)
		}
		if fix == "" {
			t.Errorf("the %s problem says nothing about what to do", fault)
		}
	}
	// The two an unset-up machine has, and they are separate because they are
	// two different things for the user to do.
	if _, found := faults["not_authorized"]; !found {
		t.Errorf("an unapproved engine reported no authorization problem; it reported %v", faults)
	}
	if _, found := faults["missing_engine"]; !found {
		t.Errorf("an engine with no program recorded reported no missing executable; it reported %v", faults)
	}
	// And the root it needs, separately, because setting a program and setting
	// a folder are two different things for the user to do.
	if _, found := faults["unbound_root"]; !found {
		t.Errorf("an engine with no project folder reported no unbound root; it reported %v", faults)
	}
}

func TestAnInvalidProfileIsRefusedWithTheField(t *testing.T) {
	m := newMachine(t)

	// A document that is well-formed JSON and not a profile: the version is
	// missing, and the message has to say which member.
	status, body := m.call(http.MethodPost, "/api/v1/profiles/import", map[string]any{
		"document": map[string]any{
			"schema_version": "aucom.profile/1.1",
			"kind":           "engine",
			"id":             "me.engine.broken",
			"name":           "Broken",
		},
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("importing an invalid document = %d, want 422", status)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "version") {
		t.Fatalf("the refusal does not name the member that is wrong: %q", message)
	}
	if body["valid"] != false {
		t.Fatalf("an invalid document reported valid = %v", body["valid"])
	}

	// And a document that is not JSON at all is a different message.
	response, decoded := send(t, m.server,
		request(t, m.server, http.MethodPost, "/api/v1/profiles/validate", "this is not JSON"))
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("validating a non-document = %d, want 422", response.StatusCode)
	}
	if message, _ := decoded["error"].(string); message == "" {
		t.Fatal("a non-document was refused with no reason")
	}
}

// A failed stage names the stage, the exit status and what the tool printed —
// and the build is completed rather than abandoned, so the record exists.
func TestAFailedStageIsRecorded(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()

	source := m.writeSourceMap()
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "aucom.fixture.pipeline",
		"inputs":   map[string]string{"source_map": source},
		"options":  map[string]map[string]string{"compile": {"fail": "true"}},
		"label":    "a build that fails",
	})
	if status != http.StatusAccepted {
		t.Fatalf("starting the build = %d: %v", status, body["error"])
	}
	manifest := m.waitForBuild(body["build"].(string))
	if manifest["state"] != "failed" {
		t.Fatalf("the build %v, want failed", manifest["state"])
	}
	if message, _ := manifest["error"].(string); !strings.Contains(message, "compile") {
		t.Fatalf("the build's error does not name the stage: %q", message)
	}
	steps, _ := manifest["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	if step["state"] != "failed" {
		t.Fatalf("the stage recorded state %v", step["state"])
	}
	if code, _ := step["exit_code"].(float64); code != 3 {
		t.Fatalf("the stage recorded exit status %v, want the tool's 3", step["exit_code"])
	}
	// The diagnostic rule classified what the tool printed, which is what turns
	// a wall of output into a sentence.
	diagnostics, _ := step["diagnostics"].([]any)
	if len(diagnostics) == 0 {
		t.Fatal("the failed stage recorded no findings")
	}
	stderr, _ := step["stderr"].(map[string]any)
	if stderr == nil {
		t.Fatal("the failed stage recorded no stderr summary")
	}

	// And the failure is in the history, because the manifest was written as it
	// went rather than at the end.
	status, body = m.call(http.MethodGet, "/api/v1/build/runs", nil)
	if items, _ := body["items"].([]any); len(items) != 1 {
		t.Fatalf("a failed build left %d entries in the history, want 1", len(items))
	}
}

// approveAndBindTool is the two steps the Profiles area does: read what the
// toolchain asks for and approve it, then say where its program is.
func (m *machine) approveAndBindTool() {
	m.t.Helper()
	status, body := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.toolchain", nil)
	if status != http.StatusOK {
		m.t.Fatalf("reading the toolchain = %d", status)
	}
	digest, _ := body["digest"].(string)
	status, body = m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/bind", map[string]any{
		"executables": map[string]string{"tool": mustExecutable(m.t)},
		"approve":     true, "digest": digest,
	})
	if status != http.StatusOK {
		m.t.Fatalf("binding the toolchain = %d: %v", status, body["error"])
	}
}

// --- choosing a path --------------------------------------------------------

func TestPathRoutes(t *testing.T) {
	m := newMachine(t)

	// A machine with no chooser says so, and says it in a way the page can act
	// on: 501, so the Browse button goes away and the text field stays.
	status, body := m.call(http.MethodPost, "/api/v1/paths/pick",
		map[string]string{"kind": "directory", "title": "Game folder"})
	if status != http.StatusNotImplemented {
		t.Fatalf("picking with no helper installed = %d, want 501", status)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "type the path instead") {
		t.Fatalf("the refusal does not name the fallback: %q", message)
	}
	// The settings say the same thing up front, so the page never draws a
	// button it would then have to take away.
	status, body = m.call(http.MethodGet, "/api/v1/settings", nil)
	if body["path_helper"] != nil && body["path_helper"] != "" {
		t.Fatalf("path_helper = %v on a machine with no chooser", body["path_helper"])
	}

	// Cancelling is not an error: 200, and the page draws nothing.
	m.server.picker = &pathpick.Picker{
		GOOS: "linux",
		Look: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run: func(context.Context, string, []string) ([]byte, []byte, int, error) {
			return nil, nil, 1, nil
		},
		Home: func() (string, error) { return m.dir, nil },
	}
	status, body = m.call(http.MethodPost, "/api/v1/paths/pick", map[string]string{"kind": "directory"})
	if status != http.StatusOK {
		t.Fatalf("cancelling = %d, want 200", status)
	}
	if body["cancelled"] != true {
		t.Fatalf("a cancelled dialog reported %v", body)
	}

	// A chosen directory comes back checked.
	m.server.picker = &pathpick.Picker{
		GOOS: "linux",
		Look: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run: func(context.Context, string, []string) ([]byte, []byte, int, error) {
			return []byte(m.gameRoot + "\n"), nil, 0, nil
		},
		Home: func() (string, error) { return m.dir, nil },
	}
	status, body = m.call(http.MethodPost, "/api/v1/paths/pick", map[string]string{"kind": "directory"})
	if status != http.StatusOK || body["path"] != m.gameRoot {
		t.Fatalf("picking = %d %v, want %s", status, body, m.gameRoot)
	}

	// And the typed fallback answers with the reason a path cannot be used,
	// without ever failing the request: the user is still typing.
	for _, testCase := range []struct{ path, want string }{
		{"quake", "absolute"},
		{filepath.Join(m.dir, "nowhere"), "no such file"},
	} {
		status, body = m.call(http.MethodPost, "/api/v1/paths/validate",
			map[string]string{"kind": "directory", "path": testCase.path})
		if status != http.StatusOK {
			t.Fatalf("validating %q = %d, want 200", testCase.path, status)
		}
		if body["valid"] != false {
			t.Fatalf("validating %q reported valid", testCase.path)
		}
		if message, _ := body["error"].(string); !strings.Contains(message, testCase.want) {
			t.Fatalf("validating %q said %q, want it to mention %q", testCase.path, message, testCase.want)
		}
	}
	status, body = m.call(http.MethodPost, "/api/v1/paths/validate",
		map[string]string{"kind": "directory", "path": m.gameRoot})
	if body["valid"] != true || body["path"] != m.gameRoot {
		t.Fatalf("validating a real directory reported %v", body)
	}
}

// The browser is never handed the filesystem: there is no listing route, no
// stat route and no completion route behind the guard.
func TestThereIsNoFilesystemBrowsingRoute(t *testing.T) {
	server, _ := newTestServer(t, nil)
	forbidden := map[string]bool{
		"files": true, "browse": true, "ls": true, "readdir": true,
		"stat": true, "list": true, "dir": true,
	}
	for pattern := range server.api() {
		_, path, _ := strings.Cut(pattern, " ")
		for _, segment := range strings.Split(strings.ToLower(path), "/") {
			if forbidden[segment] {
				t.Errorf("%s has a %q segment, which looks like filesystem browsing; "+
					"the page is never given the disk", pattern, segment)
			}
		}
	}
}

// The environment wins over the configuration file for the backend address —
// see config.EnvAUBBaseURL — so the settings form has to show both: what the
// file holds, which is what Save writes, and what is actually in use.
//
// One field would mean pressing Save copies the environment's value into the
// file the user never typed it into.
func TestSettingsSeparatesTheStoredAddressFromTheEffectiveOne(t *testing.T) {
	m := newMachine(t)
	t.Setenv("AUCOM_AUB_BASE_URL", "https://from-the-environment.example")

	status, body := m.call(http.MethodGet, "/api/v1/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("settings = %d", status)
	}
	if body["aub_from_environment"] != true {
		t.Fatal("the settings did not say the address comes from the environment")
	}
	if body["aub_effective_url"] != "https://from-the-environment.example" {
		t.Fatalf("aub_effective_url = %v", body["aub_effective_url"])
	}
	if body["aub_base_url"] != m.backend.url() {
		t.Fatalf("aub_base_url = %v, want the stored value %s", body["aub_base_url"], m.backend.url())
	}

	// And saving what the form is showing leaves the file's value alone.
	status, body = m.call(http.MethodPut, "/api/v1/settings", map[string]any{
		"aub_base_url": body["aub_base_url"], "port": 0, "job_concurrency": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("saving = %d: %v", status, body["error"])
	}
	if m.settings.AUBBaseURL != m.backend.url() {
		t.Fatalf("saving wrote %q into the configuration file", m.settings.AUBBaseURL)
	}
}

// A build's artifact is addressed by the pipeline's declared output name, and
// the bytes come back through the guard.
//
// The route exists because a page cannot fetch one any other way: every API
// route wants the token in a header, and a plain link cannot send one. So this
// is the whole path a Download button takes.
func TestBuildOutputIsServedByItsDeclaredName(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()

	status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "aucom.fixture.pipeline",
		"inputs":   map[string]string{"source_map": m.writeSourceMap()},
	})
	if status != http.StatusAccepted {
		t.Fatalf("starting the build = %d: %v", status, body["error"])
	}
	id, _ := body["build"].(string)
	manifest := m.waitForBuild(id)
	if manifest["state"] != "succeeded" {
		t.Fatalf("the build %v: %v", manifest["state"], manifest["error"])
	}

	response, _ := send(t, m.server, request(t, m.server, http.MethodGet,
		"/api/v1/build/runs/"+id+"/output/bsp", ""))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fetching the bsp output = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q; a build output is never rendered as a page", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, ".bsp") {
		t.Errorf("Content-Disposition = %q, want the file's own name", disposition)
	}

	// A name the pipeline does not declare, and a path in disguise, are both
	// refused by the same rule: the name selects a record, and the record
	// supplies the path.
	for _, name := range []string{"nothing-like-this", "..%2f..%2fmanifest.json"} {
		response, _ := send(t, m.server, request(t, m.server, http.MethodGet,
			"/api/v1/build/runs/"+id+"/output/"+name, ""))
		if response.StatusCode == http.StatusOK {
			t.Errorf("fetching the output %q succeeded", name)
		}
	}
}

// The trust rule, over every state rather than over the one a fixture happens
// to produce.
//
// Only `builtin` runs without a grant, and that is narrower than "vouched for":
// a `verified` document is signed by the catalogue and STILL has to be read and
// approved, because a signature says who published something and not that this
// user agreed to it. [profile.Trust.Vouched] answers the first question and
// [profile.Authorize] answers the second, and a page that showed one where the
// other belonged would tell somebody they had approved something they had not.
//
// The API reports Authorize's own answer rather than assembling one out of
// trust and grant, which is what stops the page and the executor disagreeing.
func TestReviewCannotBeSkippedForAnythingButABuiltInProfile(t *testing.T) {
	m := newMachine(t)
	catalog, err := m.server.catalog()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := catalog.Lookup("aucom.fixture.q1-engine")
	if err != nil {
		t.Fatal(err)
	}

	for _, trust := range profile.TrustStates {
		err := profile.Authorize(entry.Profile, trust, entry.Digest, nil)
		if trust == profile.TrustBuiltin {
			if err != nil {
				t.Errorf("a built-in profile was refused without a grant: %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("a %s profile ran with nothing approved", trust)
		}
	}

	// And the API's answer is that function's answer. A built-in profile needs
	// no approval; the local one beside it does.
	status, body := m.call(http.MethodGet, "/api/v1/profiles/auto-pigeon.engine.quakespasm", nil)
	if status != http.StatusOK {
		t.Fatalf("reading a built-in profile = %d", status)
	}
	if body["trust"] != "builtin" || body["authorized"] != true {
		t.Fatalf("a built-in profile reported trust=%v authorized=%v", body["trust"], body["authorized"])
	}
	if body["vouched"] != true {
		t.Fatalf("a built-in profile reported vouched=%v", body["vouched"])
	}
	status, body = m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.q1-engine", nil)
	if body["trust"] != "local" || body["authorized"] != false {
		t.Fatalf("a local profile reported trust=%v authorized=%v", body["trust"], body["authorized"])
	}
	if body["vouched"] != false {
		t.Fatalf("a local profile reported vouched=%v", body["vouched"])
	}
}
