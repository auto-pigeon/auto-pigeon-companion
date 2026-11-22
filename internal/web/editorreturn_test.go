package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// AUCOM #37 (NEW_633): a returned leak result offers the way back to the
// editor only when AUB's receipt for this delivery named an address for
// exactly this request. Without one the return route says `missing` and the
// page keeps its sentence; a foreign one is `refused` with its reason; a
// stored address edited on disk is checked again and refused.
func TestAReturnedLeakResultOffersTheEditorOnlyAtAVerifiedAddress(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	id := "20261122T000000Z-00000633"
	m.q2LeakBuild(id, q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap"})
	request := aub.LeakTestLink{AssetID: "saved-map", Revision: 7, ContentSHA256: strings.Repeat("a", 64),
		RequestID: strings.Repeat("9", 32), Profile: "quake2"}
	editor := "http://editor.example.test/edit/map/saved-map/hole?revision=7&leak_request=" + request.RequestID
	returned := func(address string) map[string]any {
		t.Helper()
		m.backend.mu.Lock()
		m.backend.leakEditorReturn = func(string) string { return address }
		m.backend.mu.Unlock()
		if record := m.server.deliverLeakResult(id, request); record.State != "returned" {
			t.Fatalf("the delivery: %+v", record)
		}
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil)
		if status != http.StatusOK || body["state"] != "returned" {
			t.Fatalf("the return route: %d %v", status, body)
		}
		return body
	}

	// Today's AUB: no address. The sentence stays; nothing to open.
	if body := returned(""); body["editor_return"] != "missing" || body["editor_url"] != nil {
		t.Errorf("a receipt with no address: %v", body)
	}
	if body := returned(editor); body["editor_return"] != "verified" || body["editor_url"] != editor {
		t.Errorf("a receipt naming this request's editor route: %v", body)
	} else {
		// T4: what the page is handed carries no session token, API token
		// or local folder — only the request's own identity and the address.
		raw, _ := json.Marshal(body)
		for _, private := range []string{m.backend.token, m.server.Token().Value(), m.builds, m.assets} {
			if private != "" && strings.Contains(string(raw), private) {
				t.Errorf("the return route hands the page a private value %q: %s", private, raw)
			}
		}
	}
	for address, why := range map[string]string{
		"http://editor.example.test/edit/map/another-map?revision=7&leak_request=" + request.RequestID: aub.EditorReturnOtherMap,
		"http://editor.example.test/edit/map/saved-map?revision=6&leak_request=" + request.RequestID:   aub.EditorReturnOtherRevision,
		"javascript:alert(1)": aub.EditorReturnMalformed,
	} {
		if body := returned(address); body["editor_return"] != "refused" || body["editor_refused"] != why || body["editor_url"] != nil {
			t.Errorf("a receipt naming %q: %v, want refused %q", address, body, why)
		}
	}

	// A record whose stored address was changed on disk is read again, not
	// trusted: a page is never handed it.
	returned(editor)
	path := filepath.Join(m.builds, id, leakReturnFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil || record["editor_url"] != editor {
		t.Fatalf("the stored record: %v %s", err, raw)
	}
	record["editor_url"] = "https://evil.example.test/edit/map/saved-map?revision=7&leak_request=" + request.RequestID + "&next=x"
	raw, _ = json.Marshal(record)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil)
	if status != http.StatusOK || body["editor_return"] != "refused" || body["editor_url"] != nil {
		t.Errorf("a tampered stored address: %d %v", status, body)
	}
	// A record written before NEW_633 has neither field: it reads as missing.
	delete(record, "editor_url")
	raw, _ = json.Marshal(record)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil); body["editor_return"] != "missing" {
		t.Errorf("an old record: %v", body)
	}
}
