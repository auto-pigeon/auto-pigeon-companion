package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

func TestLeakResultReturnsPinnedEvidenceFromAFailedLeakBuild(t *testing.T) {
	m := newMachine(t)
	id := "20260928T000000Z-deadbeef"
	buildDir := filepath.Join(m.builds, id)
	outputDir := filepath.Join(buildDir, "output")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(name, contents string) build.FileRecord {
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(contents))
		return build.FileRecord{Name: name, Path: path, SHA256: "sha256:" + hex.EncodeToString(sum[:])}
	}
	log := "---- qbsp / ericw-tools v0.18.1 ----\nInput file: level.map\n---- FillOutside ----\nLeak file written to level.pts\n"
	pts := "0 0 0\n1 0 0\n"
	logRecord, ptsRecord := put("compile.log", log), put("route.pts", pts)
	logRecord.Name, ptsRecord.Name = "compile_log", "pts"
	manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id,
		Pipeline: build.DocumentRef{ID: leakPipelineID}, State: job.Failed,
		Inputs: []build.FileRecord{{Name: "source_map", Source: &build.SourceRef{
			AssetType: aub.AssetTypeMap, AssetID: "saved-map", RevisionID: "immutable-revision", Revision: 7,
			ContentSHA256: strings.Repeat("a", 64), Refetchable: true}}},
		Tools:   []build.ToolRecord{{ToolVersion: "v0.18.1"}},
		Outputs: []build.FileRecord{logRecord, ptsRecord}}
	if err := manifest.Save(buildDir); err != nil {
		t.Fatal(err)
	}
	manifest.State = job.Resolving
	if err := manifest.Save(buildDir); err != nil {
		t.Fatal(err)
	}
	if status, _ := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil); status != http.StatusConflict {
		t.Fatalf("unfinished result: got %d, want conflict", status)
	}
	manifest.State = job.Failed
	if err := manifest.Save(buildDir); err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil)
	if status != http.StatusOK || body["schema_version"] != "aucom.leak-result/1.0" ||
		body["map_id"] != "saved-map" || body["revision"] != float64(7) ||
		body["revision_id"] != "immutable-revision" ||
		body["build_state"] != "failed" || body["log"] != log || body["pointfile"] != pts ||
		body["log_sha256"] != strings.TrimPrefix(logRecord.SHA256, "sha256:") ||
		body["pointfile_sha256"] != strings.TrimPrefix(ptsRecord.SHA256, "sha256:") {
		t.Fatalf("failed leak build result: %d %v", status, body)
	}
	m.signIn()
	requestID := strings.Repeat("a", 32)
	if err := m.server.publishLeakResult(id, requestID); err != nil {
		t.Fatalf("returning failed build: %v", err)
	}
	m.backend.mu.Lock()
	returned, gotID := m.backend.leakResult, m.backend.leakRequestID
	m.backend.mu.Unlock()
	if gotID != requestID || returned["pointfile"] != pts || returned["revision_id"] != "immutable-revision" {
		t.Fatalf("AUB received request %q result %v", gotID, returned)
	}
	manifest.Inputs[0].Source = nil
	if err := manifest.Save(buildDir); err != nil {
		t.Fatal(err)
	}
	if status, _ := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil); status != http.StatusConflict {
		t.Fatalf("un-pinned result: got %d, want conflict", status)
	}
}

func TestLeakRequestOnlyReviewsTheExactSavedRevision(t *testing.T) {
	m := newMachine(t)
	m.backend.asset.fileName = "fixture.apmap"
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	request := aub.LeakTestLink{AssetID: m.backend.asset.assetID,
		Revision: m.backend.asset.revision, ContentSHA256: m.backend.asset.digest()}
	if err := leakintent.Receive(leakintent.Path(dir), request, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending", nil)
	if status != http.StatusOK || body["sign_in_required"] != true {
		t.Fatalf("signed out: %d %v", status, body)
	}
	m.signIn()
	status, body = m.call(http.MethodGet, "/api/v1/leak-test/pending", nil)
	if status != http.StatusOK || body["revision_id"] != m.backend.asset.revisionID ||
		!strings.Contains(body["source_ref"].(string), "@"+m.backend.asset.revisionID+"#fixture.apmap") {
		t.Fatalf("pinned review: %d %v", status, body)
	}

	request.ContentSHA256 = strings.Repeat("0", 64)
	if err := leakintent.Receive(leakintent.Path(dir), request, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if status, _ := m.call(http.MethodGet, "/api/v1/leak-test/pending", nil); status != http.StatusConflict {
		t.Fatalf("changed content digest: got %d, want conflict", status)
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/dismiss", nil); status != http.StatusOK {
		t.Fatalf("dismiss: %d", status)
	}
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending", nil); status != http.StatusOK || body["pending"] != false {
		t.Fatalf("after dismiss: %d %v", status, body)
	}
}

func TestLeakOutputRefusesTamperingAndEscape(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "builds", "one", "output")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "route.pts")
	content := []byte("0 0 0\n1 0 0\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	record := build.FileRecord{Path: path, SHA256: "sha256:" + hex.EncodeToString(sum[:])}
	if got, err := readLeakOutput(filepath.Join(dir, "builds"), "one", record, 32); err != nil || got != string(content) {
		t.Fatalf("intact output: %q %v", got, err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLeakOutput(filepath.Join(dir, "builds"), "one", record, 32); err == nil {
		t.Fatal("a changed compiler output was accepted")
	}
	record.Path = filepath.Join(dir, "outside.pts")
	if _, err := readLeakOutput(filepath.Join(dir, "builds"), "one", record, 32); err == nil {
		t.Fatal("a path outside this build was accepted")
	}
	if err := os.WriteFile(record.Path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.pts")
	if err := os.Symlink(record.Path, link); err != nil {
		t.Fatal(err)
	}
	record.Path = link
	if _, err := readLeakOutput(filepath.Join(dir, "builds"), "one", record, 32); err == nil {
		t.Fatal("a symlink outside this build was accepted")
	}
}

// leakStatusesSoon waits for the relay's single sender to have posted `want`
// lines. They are sent off the request's goroutine, in order.
func (m *machine) leakStatusesSoon(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.backend.mu.Lock()
		got := append([]string(nil), m.backend.leakStatuses...)
		m.backend.mu.Unlock()
		if len(got) >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An open page asks every couple of seconds whether a link arrived
// (NEW_307W). That question is answered from this machine alone, the editor is
// told once that its request arrived, and a Dismiss removes the request it
// names — never a newer one that replaced it meanwhile.
func TestAnOpenPageSeesANewLeakRequestAndDismissesOnlyTheOneItNamed(t *testing.T) {
	m := newMachine(t)
	m.backend.asset.fileName = "fixture.apmap"
	m.signIn()
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); status != http.StatusOK || body["pending"] != false {
		t.Fatalf("nothing asked yet: %d %v", status, body)
	}
	older := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: strings.Repeat("a", 32)}
	if err := leakintent.Receive(leakintent.Path(dir), older, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	watch := func() {
		t.Helper()
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
		if status != http.StatusOK || body["pending"] != true || body["request_id"] != older.RequestID ||
			body["asset_id"] != older.AssetID || body["revision"] != float64(older.Revision) {
			t.Fatalf("the watch: %d %v", status, body)
		}
	}
	// The first sighting tells the editor, once; every later one asks nobody.
	watch()
	if got := m.leakStatusesSoon(t, 1); len(got) != 1 || got[0] != older.RequestID+" received" {
		t.Fatalf("the editor was told %v, want one `received`", got)
	}
	served := m.backend.count()
	for range 5 {
		watch()
	}
	time.Sleep(100 * time.Millisecond)
	if got := m.backend.count(); got != served {
		t.Errorf("watching an unchanged request asked the account server %d more time(s)", got-served)
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/reviewing", map[string]any{"request_id": strings.Repeat("f", 32)}); status != http.StatusConflict {
		t.Errorf("reviewing a request that is not pending: %d", status)
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/reviewing", map[string]any{"request_id": older.RequestID}); status != http.StatusOK {
		t.Errorf("reviewing the pending request: %d", status)
	}
	// The pages keep watching while the request is in review. That must not
	// say "received" again: it did, live, and put the editor back a step.
	for range 3 {
		watch()
	}
	time.Sleep(150 * time.Millisecond)
	if got := m.leakStatusesSoon(t, 2); strings.Join(got, "|") != older.RequestID+" received|"+older.RequestID+" reviewing" {
		t.Fatalf("watching a request in review told the editor %v", got)
	}

	// The editor is clicked again while the first notice is still on screen.
	newer := older
	newer.RequestID = strings.Repeat("b", 32)
	if err := leakintent.Receive(leakintent.Path(dir), newer, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodPost, "/api/v1/leak-test/dismiss", map[string]any{"request_id": older.RequestID})
	if status != http.StatusOK || body["dismissed"] != false {
		t.Fatalf("dismissing the replaced request: %d %v", status, body)
	}
	if _, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); body["request_id"] != newer.RequestID {
		t.Fatalf("the newer request did not survive an older Dismiss: %v", body)
	}
	status, body = m.call(http.MethodPost, "/api/v1/leak-test/dismiss", map[string]any{"request_id": newer.RequestID})
	if status != http.StatusOK || body["dismissed"] != true {
		t.Fatalf("dismissing the pending request: %d %v", status, body)
	}
	if _, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); body["pending"] != false {
		t.Fatalf("after dismiss: %v", body)
	}
	want := []string{older.RequestID + " received", older.RequestID + " reviewing",
		newer.RequestID + " received", newer.RequestID + " dismissed"}
	if got := m.leakStatusesSoon(t, len(want)); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the editor was told\n %v\nwant\n %v", got, want)
	}
}

// Returning a result is recorded apart from the compiler's verdict, and a
// delivery that failed is sent again without compiling anything: the retry
// carries the same build's bytes for the same request.
func TestAFailedLeakReturnIsRecordedAndRetriedWithoutAnotherBuild(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	id := "20260928T000000Z-feedface"
	buildDir := filepath.Join(m.builds, id)
	outputDir := filepath.Join(buildDir, "output")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	log := "---- qbsp ----\nLeak file written to level.pts\n"
	path := filepath.Join(outputDir, "compile.log")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(log))
	request := aub.LeakTestLink{AssetID: "saved-map", Revision: 7, ContentSHA256: strings.Repeat("a", 64),
		RequestID: strings.Repeat("c", 32)}
	manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id,
		Pipeline: build.DocumentRef{ID: leakPipelineID}, State: job.Failed,
		Inputs: []build.FileRecord{{Name: "source_map", Source: &build.SourceRef{
			AssetType: aub.AssetTypeMap, AssetID: request.AssetID, RevisionID: "immutable-revision", Revision: request.Revision,
			ContentSHA256: request.ContentSHA256, Refetchable: true}}},
		Outputs: []build.FileRecord{{Name: "compile_log", Path: path, SHA256: "sha256:" + hex.EncodeToString(sum[:])}}}
	if err := manifest.Save(buildDir); err != nil {
		t.Fatal(err)
	}
	digestOf := func() string {
		entries, err := os.ReadDir(buildDir)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		for _, entry := range entries {
			if entry.IsDir() || entry.Name() == leakReturnFile {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(buildDir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			hash.Write([]byte(entry.Name()))
			hash.Write(raw)
		}
		return hex.EncodeToString(hash.Sum(nil))
	}
	before := digestOf()
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil); status != http.StatusOK || body["requested"] != false {
		t.Fatalf("a build nobody asked for: %d %v", status, body)
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/runs/"+id+"/return", nil); status != http.StatusConflict {
		t.Fatalf("retrying a build nobody asked for: %d", status)
	}

	m.backend.mu.Lock()
	m.backend.refuseLeakResult = true
	m.backend.mu.Unlock()
	if record := m.server.deliverLeakResult(id, request); record.State != "failed" || record.Error == "" || record.Attempts != 1 {
		t.Fatalf("a refused delivery: %+v", record)
	}
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil)
	if status != http.StatusOK || body["state"] != "failed" || body["request_id"] != request.RequestID || body["error"] == "" {
		t.Fatalf("the recorded failure: %d %v", status, body)
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/runs/"+id+"/return", nil); status != http.StatusBadGateway {
		t.Fatalf("a retry while the server is still away: %d", status)
	}

	m.backend.mu.Lock()
	m.backend.refuseLeakResult = false
	m.backend.mu.Unlock()
	status, body = m.call(http.MethodPost, "/api/v1/leak-test/runs/"+id+"/return", nil)
	if status != http.StatusOK || body["state"] != "returned" || body["attempts"] != float64(3) {
		t.Fatalf("the retry that got through: %d %v", status, body)
	}
	m.backend.mu.Lock()
	gotID, returned := m.backend.leakRequestID, m.backend.leakResult
	m.backend.mu.Unlock()
	if gotID != request.RequestID || returned["build_id"] != id || returned["log"] != log {
		t.Fatalf("AUB received request %q result %v", gotID, returned)
	}
	if after := digestOf(); after != before {
		t.Fatal("returning a result changed the build's own files")
	}
	statuses := strings.Join(m.leakStatusesSoon(t, 6), "|")
	for _, want := range []string{request.RequestID + " returning", request.RequestID + " return_failed", request.RequestID + " returned"} {
		if !strings.Contains(statuses, want) {
			t.Errorf("the editor was never told %q: %s", want, statuses)
		}
	}
}
