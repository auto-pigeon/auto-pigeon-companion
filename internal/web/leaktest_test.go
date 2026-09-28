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
