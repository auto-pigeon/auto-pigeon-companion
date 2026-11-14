package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
)

// NEW_515 (AUCOM #44): an editor leak test that the Companion refuses BEFORE
// compiling — here a saved Quake II map that names an external map file —
// reaches the editor as the terminal `blocked` status AUB already knows, with
// the refusal's class as its stage. Not `return_failed: the build has no
// pinned AUB map source`, not a fabricated result, and no Retry that could
// only fail again.
//
// The refusal is the REAL one: these builds stage the map through the
// converter built from ./cmd/mapio, so `canonical-map` decides, as in a
// release. The APMap→.map conversion is still the fixture's (the saved
// document carries its `.map` text), and qbsp is the test binary's stand-in.

// realWebMapio is the converter built from this commit, once per test process.
var realWebMapio = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aucom-web-mapio-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, converter.ExecutableBase)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	command := exec.Command("go", "build", "-o", path, "./cmd/mapio")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, output)
	}
	return path, nil
})

// realCanonicalConverter is the fixture converter whose `canonical-map` is the
// real one.
type realCanonicalConverter struct {
	fixtureConverter
	mapio string
}

func (c *realCanonicalConverter) Run(ctx context.Context, subcommand string, args ...string) ([]byte, error) {
	if subcommand == "canonical-map" {
		return converter.NewOverrideRunner(c.mapio).Run(ctx, subcommand, args...)
	}
	return c.fixtureConverter.Run(ctx, subcommand, args...)
}

// q2ReplayFlag puts the stand-in qbsp in its Quake II mode: it prints the
// measured ericw-tools log of the control the map names
// (`_aucom_test_q2_control`), writes that control's point file when it has
// one, and a loadable Quake II BSP when it has none — so a positive
// control reaches the Quake II verdict rules with real compiler text.
const q2ReplayFlag = "-q2-replay"

const q2ControlKey = "_aucom_test_q2_control"

// qbspRanMarker is the file the stand-in leaves in its vectors root.
const qbspRanMarker = "qbsp-ran"

// q2ReplayMain is `-q2-replay <vectors> <map> <bsp>`.
func q2ReplayMain(vectors string, args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "leak-qbsp: takes a map and a BSP")
		return 2
	}
	source, destination := args[0], args[1]
	// What a test can check: this program ran, on this map.
	_ = os.WriteFile(filepath.Join(vectors, qbspRanMarker), []byte(source), 0o600)
	contents, err := os.ReadFile(source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "leak-qbsp:", err)
		return 1
	}
	controls := fixtureValues(contents, q2ControlKey)
	if len(controls) != 1 {
		fmt.Fprintln(os.Stderr, "leak-qbsp: the map names no Quake II control")
		return 2
	}
	log, err := os.ReadFile(filepath.Join(vectors, "logs", controls[0]+".log"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "leak-qbsp:", err)
		return 2
	}
	fmt.Print(string(log))
	stem := strings.TrimSuffix(destination, filepath.Ext(destination))
	if pts, err := os.ReadFile(filepath.Join(vectors, "pts", controls[0]+".pts")); err == nil {
		_ = os.WriteFile(stem+".pts", pts, 0o600)
		return 1
	}
	_ = os.WriteFile(destination, q2fixture.BSP("{\n\"classname\" \"worldspawn\"\n}\n"), 0o600)
	return 0
}

// q2LeakToolJSON is a Quake II compile action over the stand-in, replaying
// the measured logs under the build's content root — a task-owned copy, since
// a profile names roles, never one machine's paths.
func q2LeakToolJSON(self string) []byte {
	document := map[string]any{}
	if err := json.Unmarshal(fixtureToolJSON(self), &document); err != nil {
		panic(err)
	}
	document["id"], document["name"] = "aucom.fixture.q2-leak-qbsp", "Fixture Quake II leak qbsp"
	document["capabilities"] = []map[string]any{{"id": "fixture.q2leak.compile", "title": "Compile",
		"consumes": []string{"q2.map.source"}, "produces": []string{"q2.bsp", "q2.pts"}}}
	document["actions"] = []map[string]any{{
		"id": "compile", "title": "Compile", "capability": "fixture.q2leak.compile", "executable": "tool",
		"args": []any{leakQbspFlag, q2ReplayFlag,
			map[string]any{"value": "{root.content_root}", "when": map[string]any{"root": "content_root"}},
			"{input.source_map}", "{output.bsp}"},
		"working_dir": map[string]any{"root": "workspace"},
		"inputs": []map[string]any{{"name": "source_map", "title": "Source", "role": "q2.map.source",
			"required": true, "extensions": []string{".map"}}},
		"outputs": []map[string]any{
			{"name": "bsp", "title": "BSP", "role": "q2.bsp", "path": "{option.basename}.bsp", "optional": true},
			{"name": "pts", "title": "Leak point file", "role": "q2.pts", "path": "{option.basename}.pts", "optional": true},
		},
		"options": []map[string]any{{"name": "basename", "title": "Name", "type": "text", "default": "level", "max_length": 64}},
		"roots": []map[string]any{
			{"role": "workspace", "access": "read_write", "purpose": "compile"},
			{"role": "content_root", "access": "read_write", "optional": true, "purpose": "the measured logs this stand-in replays"},
		},
		"timeout_seconds": 120,
	}}
	return encodeFixture(document)
}

func q2LeakPipelineJSON() []byte {
	return encodeFixture(map[string]any{
		"schema_version": "aucom.profile/1.1", "kind": "pipeline",
		"id": "aucom.fixture.q2-leak", "version": "1.0.0", "name": "Fixture Quake II leak test",
		"summary":      "Only the compile step, with the leak test on.",
		"publisher":    map[string]any{"name": "Auto-Pigeon tests"},
		"license":      map[string]any{"spdx": "MIT", "name": "MIT"},
		"game_profile": map[string]any{"slug": "quake2", "engine_family": "quake2"},
		"inputs": []map[string]any{{"name": "source_map", "title": "Map source", "role": "q2.map.source",
			"required": true, "extensions": []string{".map", ".apmap"}}},
		"steps": []map[string]any{{"id": "compile", "title": "Compile", "capability": "fixture.q2leak.compile",
			"tool": "aucom.fixture.q2-leak-qbsp", "inputs": []map[string]any{{"name": "source_map", "from": "pipeline.source_map"}}}},
		"outputs": []map[string]any{
			{"name": "pts", "title": "Leak point file", "role": "q2.pts", "from": "compile.pts", "optional": true},
			{"name": "bsp", "title": "BSP", "role": "q2.bsp", "from": "compile.bsp", "optional": true},
		},
	})
}

// q2RefusalMachine is a signed-in machine whose Quake II leak test is the
// fixture pipeline above, staging through the real converter. It answers
// where the stand-in leaves its marker when it runs.
func q2RefusalMachine(t *testing.T) (*machine, string) {
	t.Helper()
	mapio, err := realWebMapio()
	if err != nil {
		t.Fatalf("building the converter: %v", err)
	}
	m := newMachine(t)
	// The measured controls the stand-in replays, copied to a root of the
	// test's own.
	m.q2Vectors = t.TempDir()
	for _, file := range []string{"logs/a_sealed.log", "logs/b_gap.log", "pts/b_gap.pts"} {
		body, err := os.ReadFile(filepath.Join("..", "leakadapter", "testdata", "q2", file))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, filepath.Join(m.q2Vectors, file), string(body))
	}
	m.writeProfile("aucom.fixture.q2-leak-qbsp.json", q2LeakToolJSON(mustExecutable(t)))
	m.writeProfile("aucom.fixture.q2-leak.json", q2LeakPipelineJSON())
	m.approveAndBind("aucom.fixture.q2-leak-qbsp")
	m.server.runner = &realCanonicalConverter{mapio: mapio}
	m.signIn()
	if status, body := m.call(http.MethodPost, "/api/v1/leak-test/pipelines",
		map[string]any{"game": "quake2", "pipeline": "aucom.fixture.q2-leak"}); status != http.StatusOK {
		t.Fatalf("pinning the fixture Quake II leak test = %d: %v", status, body["error"])
	}
	return m, filepath.Join(m.q2Vectors, qbspRanMarker)
}

// q2Room is a sealed-looking Quake II room: one brush in worldspawn, naming
// the measured control the stand-in replays, and whatever entities follow.
func q2Room(control string, entities ...string) string {
	face := "( -64 -64 -16 ) ( -64 -63 -16 ) ( -64 -64 -15 ) e1u1/floor1_3 0 0 0 1 1\n"
	text := "{\n\"classname\" \"worldspawn\"\n\"" + q2ControlKey + "\" \"" + control + "\"\n{\n" +
		strings.Repeat(face, 6) + "}\n}\n"
	return text + strings.Join(entities, "")
}

// externalMapEntity is the harmless refusal vector of NEW_507A's
// k_missing_prefab: a misc_external_map naming a file that is not there.
const externalMapEntity = "{\n\"classname\" \"misc_external_map\"\n\"_external_map\" \"nothere/missing_prefab.map\"\n\"origin\" \"0 0 0\"\n}\n"

// saveQ2Map makes the backend's one map a saved Quake II APMap.
func (m *machine) saveQ2Map(mapID string, revision int, text string) {
	m.t.Helper()
	apmap, err := json.Marshal(map[string]any{"apmap_version": "1.5", "document_id": "fixture:" + mapID,
		"revision": revision, "game": "quake2", "entities": []any{}, "fixture_map": text})
	if err != nil {
		m.t.Fatal(err)
	}
	m.backend.setAsset(func(asset *fixtureAsset) {
		asset.assetID, asset.revision = mapID, revision
		asset.revisionID = fmt.Sprintf("%s-rev-%d", mapID, revision)
		asset.fileName, asset.body = "fixture.apmap", apmap
	})
}

// pressQ2Leak is the Build press for a reviewed request, and the finished build.
func (m *machine) pressQ2Leak(link aub.LeakTestLink) (string, map[string]any) {
	m.t.Helper()
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline":        "aucom.fixture.q2-leak",
		"inputs":          map[string]string{"source_map": "aub:map/" + link.AssetID + "@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
		"roots":           map[string]string{"content_root": m.q2Vectors},
	})
	if status != http.StatusAccepted {
		m.t.Fatalf("the press = %d: %v", status, body)
	}
	id, _ := body["build"].(string)
	return id, m.waitForBuild(id)
}

// leakReturnSettled reads a build's return record until it is no longer
// being sent.
func (m *machine) leakReturnSettled(id string) map[string]any {
	m.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil)
		if status != http.StatusOK {
			m.t.Fatalf("the return record = %d: %v", status, body)
		}
		if state := body["state"]; state != "pending" && state != "sending" && body["requested"] == true {
			return body
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("the return never settled: %v", body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// statusesFor is what AUB accepted for one request, in order.
func (m *machine) statusesFor(requestID string) []string {
	m.backend.mu.Lock()
	defer m.backend.mu.Unlock()
	var out []string
	for _, line := range m.backend.leakStatuses {
		if rest, ok := strings.CutPrefix(line, requestID+" "); ok {
			out = append(out, rest)
		}
	}
	return out
}

// awaitStatus waits for AUB to have accepted want for a request.
func (m *machine) awaitStatus(requestID, want string, within time.Duration) []string {
	m.t.Helper()
	deadline := time.Now().Add(within)
	for {
		got := m.statusesFor(requestID)
		for _, line := range got {
			if line == want {
				return got
			}
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("AUB never accepted %q for %s; it has %q", want, requestID, got)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func count(lines []string, want string) int {
	n := 0
	for _, line := range lines {
		if line == want {
			n++
		}
	}
	return n
}

// The issue's journey, end to end over HTTP: two requests are pending at once;
// the one whose map names an external map file is refused at staging, never
// reaches qbsp, and AUB is told `blocked map_external_file` for that request
// and nothing else; the page offers nothing to resend, and asking anyway
// sends no result. The eligible map, still pending, is the positive control:
// built afterwards, it compiles and its result is returned under its own id.
func TestALeakTestRefusedBeforeCompilingTellsTheEditorItCouldNotRun(t *testing.T) {
	m, qbspRan := q2RefusalMachine(t)

	// Two requests pending together (#35): the refused map's, then the
	// control's. The backend holds one map at a time, so each is received
	// while its own revision is the saved one, and built while it is again.
	m.saveQ2Map("n515refused", 1, q2Room("a_sealed", externalMapEntity))
	refused := m.receiveLeakLink(strings.Repeat("4", 32), "quake2")
	refusedRevision := m.backend.asset.revisionID
	m.saveQ2Map("n515control", 1, q2Room("a_sealed"))
	control := m.receiveLeakLink(strings.Repeat("5", 32), "quake2")
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); status != http.StatusOK ||
		len(body["requests"].([]any)) != 2 {
		t.Fatalf("two pending requests: %d %v", status, body)
	}
	m.awaitStatus(refused.RequestID, "received", 10*time.Second)
	m.awaitStatus(control.RequestID, "received", 10*time.Second)

	m.saveQ2Map("n515refused", 1, q2Room("a_sealed", externalMapEntity))
	if m.backend.asset.revisionID != refusedRevision {
		t.Fatal("the refused map's revision moved")
	}
	jobs := len(m.jobIDs())
	id, manifest := m.pressQ2Leak(refused)

	// The build: failed at staging, classed by the converter's own word.
	steps, _ := manifest["steps"].([]any)
	compile, _ := steps[0].(map[string]any)
	if manifest["state"] != "failed" || manifest["failure_class"] != failure.MapExternalFile ||
		!strings.Contains(manifest["error"].(string), "external map file") ||
		compile["skipped"] != true || compile["state"] != nil && compile["state"] != "" {
		t.Fatalf("the refused build: %v", manifest)
	}
	if got := len(m.jobIDs()); got != jobs {
		t.Fatalf("the refused build started %d job(s)", got-jobs)
	}
	if _, err := os.Stat(qbspRan); !os.IsNotExist(err) {
		t.Fatal("qbsp ran for a map the converter refused")
	}

	// The editor's answer: `blocked`, with the stable reason, for this request.
	record := m.leakReturnSettled(id)
	if record["state"] != "blocked" || record["reason"] != failure.MapExternalFile || record["request_id"] != refused.RequestID {
		t.Fatalf("the return record: %v", record)
	}
	statuses := m.awaitStatus(refused.RequestID, "blocked map_external_file", 10*time.Second)
	for _, line := range statuses {
		if strings.HasPrefix(line, "return") {
			t.Fatalf("the editor was told %q", statuses)
		}
	}
	m.backend.mu.Lock()
	result := m.backend.leakResult
	m.backend.mu.Unlock()
	if result != nil {
		t.Fatalf("a result was fabricated for a build that never compiled: %v", result)
	}
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil); status != http.StatusConflict {
		t.Fatalf("the result download of a refused build: %d %v", status, body)
	}
	// The Build page is told there is no result file, so it offers none, and
	// its log says the editor is told why rather than that a return failed.
	_, view := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	if leak, _ := view["leak_test"].(map[string]any); leak["not_compiled"] != failure.MapExternalFile {
		t.Fatalf("the Build page's view of a refused build: %v", view["leak_test"])
	}

	// The page's view of it says the editor was told; nothing to resend.
	deadline := time.Now().Add(10 * time.Second)
	for {
		record = m.leakReturnSettled(id)
		relay, _ := record["relay"].(map[string]any)
		if relay["state"] == "delivered" && relay["delivered"] == "blocked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page never heard the editor was told: %v", record)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Asked anyway (a crafted call, or an older page): no result, no build,
	// and the one status AUB has is not said twice.
	for range 2 {
		status, body := m.call(http.MethodPost, "/api/v1/leak-test/runs/"+id+"/return", nil)
		if status != http.StatusConflict || body["code"] != "not_compiled" || body["state"] != "blocked" {
			t.Fatalf("a resend of a refused build: %d %v", status, body)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if got := m.statusesFor(refused.RequestID); count(got, "blocked map_external_file") != 1 {
		t.Fatalf("the refusal was reported %d times: %q", count(got, "blocked map_external_file"), got)
	}
	m.backend.mu.Lock()
	result = m.backend.leakResult
	m.backend.mu.Unlock()
	if result != nil || len(m.jobIDs()) != jobs {
		t.Fatalf("a resend sent %v or built something", result)
	}

	// The second request was not touched: still pending, still only received.
	if got := m.statusesFor(control.RequestID); len(got) != 1 || got[0] != "received" {
		t.Fatalf("the other request was moved: %q", got)
	}
	if m.server.pendingLeakRequest(control.RequestID) == nil {
		t.Fatal("building one request took the other")
	}

	// The positive control: the same pipeline, an eligible map, compiled, and
	// its verdict returned under its own id.
	m.saveQ2Map("n515control", 1, q2Room("a_sealed"))
	controlID, controlManifest := m.pressQ2Leak(control)
	if controlManifest["state"] != "succeeded" {
		t.Fatalf("the control build: %v", controlManifest)
	}
	if _, err := os.Stat(qbspRan); err != nil {
		t.Fatalf("qbsp never ran for the eligible map: %v", err)
	}
	if record := m.leakReturnSettled(controlID); record["state"] != "returned" {
		t.Fatalf("the control's return: %v", record)
	}
	if _, view := m.call(http.MethodGet, "/api/v1/build/runs/"+controlID, nil); view["leak_test"].(map[string]any)["not_compiled"] != nil {
		t.Fatalf("a compiled control is called not compiled: %v", view["leak_test"])
	}
	m.awaitStatus(control.RequestID, "returned", 10*time.Second)
	m.backend.mu.Lock()
	result, resultFor := m.backend.leakResult, m.backend.leakRequestID
	m.backend.mu.Unlock()
	diagnostic, _ := result["diagnostic"].(map[string]any)
	if resultFor != control.RequestID || result["map_id"] != "n515control" || diagnostic["outcome"] != "no_leak" {
		t.Fatalf("AUB received %v for %s", result, resultFor)
	}
	if got := m.statusesFor(refused.RequestID); got[len(got)-1] != "blocked map_external_file" {
		t.Fatalf("the control's build moved the refused request: %q", got)
	}
}

// AUB away when the refusal is reported: the sender retries the SAME status
// and it lands once the server is back — no rebuild, no result, one `blocked`.
func TestARefusalReportedWhileTheServerIsAwayLandsOnceItIsBack(t *testing.T) {
	m, qbspRan := q2RefusalMachine(t)
	m.saveQ2Map("n515away", 1, q2Room("a_sealed", externalMapEntity))
	link := m.receiveLeakLink(strings.Repeat("6", 32), "quake2")
	m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
	m.awaitStatus(link.RequestID, "received", 10*time.Second)

	m.backend.mu.Lock()
	m.backend.failLeakStatus = 3
	m.backend.mu.Unlock()
	jobs := len(m.jobIDs())
	id, manifest := m.pressQ2Leak(link)
	if manifest["failure_class"] != failure.MapExternalFile {
		t.Fatalf("the refused build: %v", manifest)
	}
	if record := m.leakReturnSettled(id); record["state"] != "blocked" {
		t.Fatalf("the return record: %v", record)
	}
	statuses := m.awaitStatus(link.RequestID, "blocked map_external_file", 30*time.Second)
	m.backend.mu.Lock()
	posts, failing, result := m.backend.leakStatusPosts, m.backend.failLeakStatus, m.backend.leakResult
	m.backend.mu.Unlock()
	if failing != 0 || count(statuses, "blocked map_external_file") != 1 || result != nil ||
		len(m.jobIDs()) != jobs {
		t.Fatalf("after the outage: %d POST(s), %d refusal(s) left, statuses %q, result %v", posts, failing, statuses, result)
	}
	if _, err := os.Stat(qbspRan); !os.IsNotExist(err) {
		t.Fatal("qbsp ran")
	}
}

// A return record an older Companion wrote for a refused build — `failed`,
// with the sentence #44 found — is read as it was, and its Retry now tells the
// editor the test could not run instead of failing again.
func TestAnOlderFailedReturnOfARefusedBuildIsReportedAsBlockedOnRetry(t *testing.T) {
	m, _ := q2RefusalMachine(t)
	m.saveQ2Map("n515old", 1, q2Room("a_sealed", externalMapEntity))
	link := m.receiveLeakLink(strings.Repeat("7", 32), "quake2")
	id, _ := m.pressQ2Leak(link)
	m.leakReturnSettled(id)
	before := len(m.statusesFor(link.RequestID))

	old := leakReturn{Request: link, State: "failed", Error: "the build has no pinned AUB map source", Attempts: 1}
	if err := m.server.writeLeakReturn(id, old); err != nil {
		t.Fatal(err)
	}
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil); status != http.StatusOK ||
		body["state"] != "failed" || body["error"] != old.Error {
		t.Fatalf("the older record: %d %v", status, body)
	}
	status, body := m.call(http.MethodPost, "/api/v1/leak-test/runs/"+id+"/return", nil)
	if status != http.StatusBadGateway && status != http.StatusConflict {
		t.Fatalf("its retry: %d %v", status, body)
	}
	record := m.leakReturnSettled(id)
	if record["state"] != "blocked" || record["reason"] != failure.MapExternalFile {
		t.Fatalf("after its retry: %v", record)
	}
	m.backend.mu.Lock()
	result := m.backend.leakResult
	m.backend.mu.Unlock()
	if result != nil {
		t.Fatalf("a result was sent: %v", result)
	}
	if got := m.statusesFor(link.RequestID); len(got) < before || got[len(got)-1] != "blocked map_external_file" {
		t.Fatalf("AUB has %q", got)
	}
}

// Cancel, refusal and verdict stay three different answers. A Quake II build
// cancelled before its compile step started is `build_cancelled`, never its
// failure class; one that failed there with no class is `not_compiled`; a
// sealed and a leaking run are returned as results, whatever their exit.
func TestCancelRefusalAndVerdictAreDifferentAnswers(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	link := func(letter string) aub.LeakTestLink {
		return aub.LeakTestLink{AssetID: "saved-map", Revision: 7, ContentSHA256: strings.Repeat("a", 64),
			RequestID: strings.Repeat(letter, 32), Profile: "quake2"}
	}
	beforeCompile := func(id string, state job.State, class string) {
		manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id, EngineFamily: "quake2",
			Pipeline: build.DocumentRef{ID: "auto-pigeon.q2.leak-test"}, State: state, FailureClass: class,
			Error: "stopped before the compiler",
			Steps: []build.Step{{ID: "compile", Skipped: true, Error: "the build stopped before this step"}}}
		if err := os.MkdirAll(filepath.Join(m.builds, id), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(filepath.Join(m.builds, id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		id, letter string
		state      job.State
		class      string
		want       string
	}{
		{"20261114T000000Z-00000001", "1", job.Cancelled, failure.Cancelled, leakStageCancelled},
		{"20261114T000000Z-00000002", "2", job.Failed, failure.MapRefused, failure.MapRefused},
		{"20261114T000000Z-00000003", "3", job.Failed, "", leakStageNotCompiled},
		// A class is a token or nothing: a sentence never travels as a stage.
		{"20261114T000000Z-00000004", "8", job.Failed, "Not A Token", leakStageNotCompiled},
	} {
		beforeCompile(c.id, c.state, c.class)
		request := link(c.letter)
		record := m.server.deliverLeakResult(c.id, request)
		if record.State != leakReturnBlocked || record.Reason != c.want {
			t.Errorf("%s %s: %+v", c.state, c.class, record)
		}
		m.awaitStatus(request.RequestID, "blocked "+c.want, 10*time.Second)
	}

	for _, c := range []struct {
		id, letter string
		run        q2Run
		want       string
	}{
		{"20261114T000000Z-00000005", "9", q2Run{control: "a_sealed", state: job.Succeeded, exit: 0, bsp: ibsp38}, "no_leak"},
		{"20261114T000000Z-00000006", "b", q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"}, "leak"},
	} {
		m.q2LeakBuild(c.id, c.run)
		request := link(c.letter)
		if record := m.server.deliverLeakResult(c.id, request); record.State != "returned" {
			t.Fatalf("%s: %+v", c.run.control, record)
		}
		m.backend.mu.Lock()
		result, resultFor := m.backend.leakResult, m.backend.leakRequestID
		m.backend.mu.Unlock()
		if resultFor != request.RequestID || result["diagnostic"].(map[string]any)["outcome"] != c.want {
			t.Fatalf("%s: AUB received %v for %s", c.run.control, result, resultFor)
		}
		m.awaitStatus(request.RequestID, "returned", 10*time.Second)
	}
}
