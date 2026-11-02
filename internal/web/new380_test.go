package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/retiredprofile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_380: a leak request opens ready to Build. What the page shows comes from
// the server, and the press is checked again by the server: these tests are the
// half of that a page cannot fake.

// leakMachine is a signed-in machine with a saved Quake 1 map and the fixture
// leak pipeline pinned, which is what a request needs to be ready.
func leakMachine(t *testing.T) *machine {
	t.Helper()
	m, _ := newLeakMachine(t)
	wad := fixturewad.WAD("n380_floor", 8)
	m.saveRoom("new380", 1, q1Room("new380.wad", false, "n380_floor"),
		leakBundle("new380", 1, []string{"new380.wad"}, map[string][]byte{"new380.wad": wad}))
	return m
}

func (m *machine) leakPress(link aub.LeakTestLink) (int, map[string]any) {
	m.t.Helper()
	return m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline":        "aucom.fixture.q1-leak",
		"inputs":          map[string]string{"source_map": "aub:map/" + link.AssetID + "@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	})
}

func TestTheRequestSaysWhatWillRunAndWhetherItCan(t *testing.T) {
	m := leakMachine(t)
	link := m.receiveLeakLink(strings.Repeat("1", 32), "quake1")
	jobs := len(m.jobIDs())
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
	if status != http.StatusOK {
		t.Fatalf("pending: %d %v", status, body)
	}
	readiness, _ := body["readiness"].(map[string]any)
	if readiness == nil {
		t.Fatalf("the request does not say whether its pipeline can run: %v", body)
	}
	if body["pipeline_tool"] == nil || body["pipeline_tool"] == "" {
		t.Fatalf("the request does not name the tool that runs: %v", body)
	}
	// Opening it started nothing.
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("resolving a request started %d job(s)", got-jobs)
	}
}

// An unbound compiler is said by the server, with where to fix it.
func TestAPipelineWaitingForItsToolIsSaidWithTheToolToSetUp(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	m.settings.LeakTestPipelines = map[string]string{"quake1": builtin.Q1LeakTest}
	m.server.mu.Lock()
	m.server.settings.LeakTestPipelines = m.settings.LeakTestPipelines
	m.server.mu.Unlock()
	link := m.receiveLeakLink(strings.Repeat("2", 32), "quake1")
	_, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
	readiness, _ := body["readiness"].(map[string]any)
	if readiness == nil || readiness["ready"] == true {
		t.Fatalf("an unbound compiler reads ready: %v", body)
	}
	problems, _ := readiness["problems"].([]any)
	if len(problems) == 0 || problems[0].(map[string]any)["profile"] != builtin.EricwAutoPigeonQ1 {
		t.Fatalf("the setup link would not go to the fork: %v", problems)
	}
}

// One press, one job: the request cannot be used twice, and an old delivery of
// the link cannot bring it back.
func TestAnOldDeliveryOfAUsedRequestStartsNothing(t *testing.T) {
	m := leakMachine(t)
	link := m.receiveLeakLink(strings.Repeat("3", 32), "quake1")
	run := m.startLeak(link, nil)
	if run.status != http.StatusAccepted {
		t.Fatalf("the press: %d %v", run.status, run.body)
	}
	jobs := len(m.jobIDs())
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := leakintent.Receive(leakintent.Path(dir), link, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); body["pending"] != false {
		t.Fatalf("a used request came back: %v", body)
	}
	if status, body := m.leakPress(link); status != http.StatusConflict {
		t.Fatalf("a second press: %d %v", status, body)
	}
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("%d more job(s) started", got-jobs)
	}
}

// The press is checked against the account at the press: the revision the page
// prepared can have been superseded since.
func TestThePressChecksTheSavedRevisionAgain(t *testing.T) {
	m := leakMachine(t)
	link := m.receiveLeakLink(strings.Repeat("4", 32), "quake1")
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil); status != http.StatusOK {
		t.Fatalf("pending: %d %v", status, body)
	}
	jobs := len(m.jobIDs())
	m.backend.mu.Lock()
	pinnedRevision := m.backend.asset.revisionID
	m.backend.asset.revision++ // a new save after the editor's click
	m.backend.mu.Unlock()
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline":        "aucom.fixture.q1-leak",
		"inputs":          map[string]string{"source_map": "aub:map/" + link.AssetID + "@" + pinnedRevision + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	})
	if status != http.StatusConflict {
		t.Fatalf("a press on a superseded revision: %d %v", status, body)
	}
	if body["code"] != "revision_changed" {
		t.Errorf("the refusal has no code a page can act on: %v", body)
	}
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("%d job(s) were started", got-jobs)
	}
}

func TestAPressWhileSignedOutStartsNothingAndSaysSo(t *testing.T) {
	m := leakMachine(t)
	link := m.receiveLeakLink(strings.Repeat("5", 32), "quake1")
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil); status != http.StatusOK {
		t.Fatalf("pending: %d %v", status, body)
	}
	jobs := len(m.jobIDs())
	m.server.mu.RLock()
	client := m.server.client
	m.server.mu.RUnlock()
	client.SetToken("")
	status, body := m.leakPress(link)
	if status != http.StatusConflict || body["code"] != "sign_in_required" {
		t.Fatalf("a press after signing out: %d %v", status, body)
	}
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("%d job(s) were started", got-jobs)
	}
	if m.server.pendingLeakRequest(link.RequestID) == nil {
		t.Fatal("the refused press used the request up")
	}
}

func TestAnUnavailableAccountServerBlocksThePressNotTheRequest(t *testing.T) {
	m := leakMachine(t)
	link := m.receiveLeakLink(strings.Repeat("6", 32), "quake1")
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil); status != http.StatusOK {
		t.Fatalf("pending: %d %v", status, body)
	}
	jobs := len(m.jobIDs())
	m.backend.server.Close()
	status, body := m.leakPress(link)
	if status < 400 || status == http.StatusAccepted {
		t.Fatalf("a press with the account server away: %d %v", status, body)
	}
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("%d job(s) were started", got-jobs)
	}
	if m.server.pendingLeakRequest(link.RequestID) == nil {
		t.Fatal("the refused press used the request up")
	}
}

// --- the retired catalogue -----------------------------------------------------

func TestAPinOnTheRetiredQuake3LeakTestMeansItsReplacement(t *testing.T) {
	m := unpinned(t)
	m.backend.savedAPMap("quake3")
	m.signIn()
	retired := map[string]string{"quake3": "auto-pigeon.q3.leak-test.auto-pigeon-build"}
	m.settings.LeakTestPipelines = retired
	m.server.mu.Lock()
	m.server.settings.LeakTestPipelines = retired
	m.server.mu.Unlock()
	link := m.receiveLeakLink(strings.Repeat("7", 32), "quake3")
	_, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
	if body["pipeline"] != builtin.Q3LeakTest || body["needs_pipeline"] != nil {
		t.Fatalf("the pin was not carried to the maintained pipeline: %v", body)
	}
	if _, listing := m.call(http.MethodGet, "/api/v1/leak-test/pipelines?game=quake3", nil); listing["pinned"] != builtin.Q3LeakTest {
		t.Errorf("the chooser says %v", listing["pinned"])
	}
}

// What is offered anywhere a compiler or a pipeline is chosen.
func TestNoSelectorOffersARetiredDocument(t *testing.T) {
	m := newMachine(t)
	for _, path := range []string{"/api/v1/profiles", "/api/v1/build/pipelines", "/api/v1/tools"} {
		_, body := m.call(http.MethodGet, path, nil)
		text := fmt.Sprint(body)
		for _, id := range builtin.Retired() {
			if strings.Contains(text, `id:`+id+` `) || strings.Contains(text, " "+id+"]") {
				t.Errorf("%s offers %s", path, id)
			}
		}
	}
	for _, game := range []string{"quake1", "quake2", "quake3"} {
		_, body := m.call(http.MethodGet, "/api/v1/leak-test/pipelines?game="+game, nil)
		choices, _ := body["choices"].([]any)
		builtins := 0
		for _, choice := range choices {
			if choice.(map[string]any)["builtin"] == true {
				builtins++
			}
		}
		if builtins != 1 {
			t.Errorf("%s offers %d built-in leak tests", game, builtins)
		}
	}
}

// An upstream folder bound to the retired document does not make the fork ready,
// whatever its programs are called, and the fork's page says why nothing is set.
func TestAnOldUpstreamFolderIsNotTheFork(t *testing.T) {
	m := newMachine(t)
	old := filepath.Join(m.dir, "ericw-tools-2.0.0-alpha11")
	writeFixtureFile(t, filepath.Join(old, "qbsp"), "#!/bin/sh\n")
	if _, err := binding.Update(m.bindings, func(set *binding.Set) error {
		return set.Put(binding.LocalBinding{ProfileID: "auto-pigeon.ericw-tools.q1", ProfileDigest: "sha256:" + strings.Repeat("0", 64),
			Trust: profile.TrustBuiltin, Acquisition: profile.AcquireUserPath, Executables: map[string]string{"qbsp": filepath.Join(old, "qbsp")}})
	}); err != nil {
		t.Fatal(err)
	}
	catalog, err := m.server.catalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retiredprofile.Apply(m.bindings, catalog, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodGet, "/api/v1/profiles/"+builtin.EricwAutoPigeonQ1, nil)
	if status != http.StatusOK {
		t.Fatalf("the fork's page: %d %v", status, body)
	}
	if ready(body) {
		t.Fatalf("the fork reads ready on an upstream folder: %v", body["readiness"])
	}
	if body["programs_not_carried_from"] != "auto-pigeon.ericw-tools.q1" {
		t.Errorf("the page does not say the older programs were not reused: %v", body["programs_not_carried_from"])
	}
	if pipeline := findItem(t, func() map[string]any {
		_, list := m.call(http.MethodGet, "/api/v1/build/pipelines", nil)
		return list
	}(), "id", builtin.Q1Normal); ready(pipeline) {
		t.Fatalf("the Quake 1 build reads ready on an upstream folder: %v", pipeline)
	}
}

// A build recorded before the catalogue changed still says what it ran.
func TestAHistoricalBuildStillNamesTheProfileThatBuiltIt(t *testing.T) {
	m := newMachine(t)
	id := "20261001T000000Z-deadbeef"
	dir := filepath.Join(m.builds, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id, State: job.Succeeded,
		Pipeline: build.DocumentRef{ID: builtin.Q1Normal, Version: "1.0.0"},
		Tools:    []build.ToolRecord{{Profile: build.DocumentRef{ID: "auto-pigeon.ericw-tools.q1"}, ToolVersion: "2.0.0-alpha11"}}}
	if err := manifest.Save(dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, build.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("a historical build: %d %v", status, body)
	}
	if !strings.Contains(fmt.Sprint(body), "auto-pigeon.ericw-tools.q1") || !strings.Contains(fmt.Sprint(body), "2.0.0-alpha11") {
		t.Errorf("the record no longer says what ran: %v", body)
	}
	after, err := os.ReadFile(filepath.Join(dir, build.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("reading a historical build rewrote it")
	}
}
