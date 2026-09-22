package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixtureNotices is AUB's operational-notice route, as the contract describes
// it: a public notice for everybody, an account-only one for a caller whose
// token AUB accepts, an ETag per visibility, and the server's clock in a
// header.
type fixtureNotices struct {
	mu sync.Mutex
	// token is the session AUB accepts; any other Authorization is refused
	// with 401 when refuseStale is set, and ignored (public) otherwise.
	token       string
	refuseStale bool
	// broken makes the route answer something that is not JSON.
	broken bool
	// seen records what each request carried.
	seen []noticeRequest
	// offset shifts the server's clock from this machine's, to prove the page
	// uses the server's.
	offset time.Duration
}

type noticeRequest struct {
	Surface       string
	Authorization string
	IfNoneMatch   string
}

const (
	publicNoticeTitle  = "Planned maintenance tonight"
	privateNoticeTitle = "Account-only canary 7f3a"
)

func instant(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func (f *fixtureNotices) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, noticeRequest{
		Surface: r.URL.Query().Get("surface"), Authorization: r.Header.Get("Authorization"),
		IfNoneMatch: r.Header.Get("If-None-Match"),
	})
	broken, offset, token, refuseStale := f.broken, f.offset, f.token, f.refuseStale
	f.mu.Unlock()

	visibility := "public"
	if auth := r.Header.Get("Authorization"); auth != "" {
		if auth == token {
			visibility = "authenticated"
		} else if refuseStale {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "invalid token"})
			return
		}
	}
	now := time.Now().Add(offset)
	etag := `"n1-` + map[string]string{"public": "0123456789abcdef", "authenticated": "fedcba9876543210"}[visibility] + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Auto-Pigeon-Server-Time", instant(now))
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if broken {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>proxy error</html>"))
		return
	}
	notices := []map[string]any{{
		"id": "aaaaaaaaaaaaaa1", "revision": 2, "title": publicNoticeTitle,
		"body":      "The server restarts for an upgrade. <b>not markup</b>",
		"severity":  "maintenance",
		"show_from": instant(now.Add(-time.Hour)), "starts_at": instant(now.Add(-time.Minute)),
		"ends_at": instant(now.Add(2 * time.Hour)), "visibility": "public", "dismissible": true,
	}}
	if visibility == "authenticated" {
		notices = append(notices, map[string]any{
			"id": "bbbbbbbbbbbbbb2", "revision": 1, "title": privateNoticeTitle, "body": "",
			"severity":  "critical",
			"show_from": instant(now.Add(-time.Hour)), "starts_at": instant(now.Add(-time.Minute)),
			"ends_at": instant(now.Add(time.Hour)), "visibility": "authenticated", "dismissible": false,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schema": "auto-pigeon-operational-notices/1.0", "server_time": instant(now),
		"surface": "aucom", "visibility": visibility, "poll_after_seconds": 90, "notices": notices,
	})
}

func (f *fixtureNotices) requests() []noticeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]noticeRequest(nil), f.seen...)
}

func noticeMachine(t *testing.T) (*machine, *fixtureNotices) {
	t.Helper()
	m := newMachine(t)
	notices := &fixtureNotices{token: m.backend.token}
	m.backend.notices = notices
	return m, notices
}

// noticeCall is GET /api/v1/notices with optional request headers, returning
// the status, the headers and the raw body.
func (m *machine) noticeCall(headers map[string]string) (int, http.Header, []byte) {
	m.t.Helper()
	r := request(m.t, m.server, http.MethodGet, "/api/v1/notices", "")
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	m.server.ServeHTTP(recorder, r)
	response := recorder.Result()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, response.Header, body
}

func TestNoticesAreRelayedAnonymouslyWhenSignedOut(t *testing.T) {
	m, notices := noticeMachine(t)

	status, header, body := m.noticeCall(nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/notices = %d", status)
	}
	seen := notices.requests()
	if len(seen) != 1 || seen[0].Surface != "aucom" || seen[0].Authorization != "" {
		t.Fatalf("AUB was asked %+v; want one anonymous request for surface=aucom", seen)
	}
	if !strings.Contains(string(body), publicNoticeTitle) || strings.Contains(string(body), privateNoticeTitle) {
		t.Errorf("signed out, the relay answered %s", body)
	}
	if header.Get("ETag") == "" || header.Get("X-Auto-Pigeon-Server-Time") == "" {
		t.Errorf("the ETag and the server's clock were not relayed: %v", header)
	}
	if header.Get(noticeAccountHeader) != "" {
		t.Error("signed out, the relay still named an account")
	}
	if header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", header.Get("Cache-Control"))
	}
}

func TestNoticesUseTheSessionAndAreNeverWrittenToDisk(t *testing.T) {
	m, notices := noticeMachine(t)
	m.signIn()

	status, header, body := m.noticeCall(nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/notices = %d", status)
	}
	seen := notices.requests()
	if last := seen[len(seen)-1]; last.Authorization != m.backend.token {
		t.Fatalf("signed in, AUB was asked without the session: %+v", last)
	}
	if !strings.Contains(string(body), privateNoticeTitle) {
		t.Errorf("signed in, the account-only notice is missing: %s", body)
	}
	account := header.Get(noticeAccountHeader)
	if account == "" || strings.Contains(account, "@") || strings.Contains(account, m.backend.token) {
		t.Errorf("the dismissal account key is %q; want an opaque id, never an address or a token", account)
	}

	// Nothing about an authenticated notice may reach this machine's disk.
	err := filepath.Walk(m.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(data), privateNoticeTitle) {
			t.Errorf("%s holds an account-only notice", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoticesAreConditional(t *testing.T) {
	m, notices := noticeMachine(t)
	_, header, _ := m.noticeCall(nil)
	etag := header.Get("ETag")

	status, header, body := m.noticeCall(map[string]string{"If-None-Match": etag})
	if status != http.StatusNotModified {
		t.Fatalf("a repeat with the current ETag = %d, want 304", status)
	}
	if len(body) != 0 || header.Get("X-Auto-Pigeon-Server-Time") == "" {
		t.Errorf("a 304 must be empty and still carry the server's clock: body=%q header=%v", body, header)
	}
	if seen := notices.requests(); seen[len(seen)-1].IfNoneMatch != etag {
		t.Errorf("If-None-Match was not passed to AUB: %+v", seen[len(seen)-1])
	}
}

func TestAStaleSessionStillGetsThePublicNotices(t *testing.T) {
	m, notices := noticeMachine(t)
	m.signIn()
	notices.mu.Lock()
	notices.token, notices.refuseStale = "a-different-session", true
	notices.mu.Unlock()

	status, header, body := m.noticeCall(nil)
	if status != http.StatusOK || !strings.Contains(string(body), publicNoticeTitle) {
		t.Fatalf("a refused session = %d %s; want the public notices", status, body)
	}
	if header.Get(noticeAccountHeader) != "" {
		t.Error("answered as nobody, the relay still named an account")
	}
}

func TestNoticeFailuresAreNamedAndNeverPassedThrough(t *testing.T) {
	m, notices := noticeMachine(t)
	notices.mu.Lock()
	notices.broken = true
	notices.mu.Unlock()
	status, body := m.call(http.MethodGet, "/api/v1/notices", nil)
	if status != http.StatusBadGateway || body["code"] != "notices_unavailable" {
		t.Errorf("an unreadable answer = %d %v; want 502 notices_unavailable", status, body)
	}

	// A deployment without the route.
	m.backend.notices = nil
	status, body = m.call(http.MethodGet, "/api/v1/notices", nil)
	if status != http.StatusBadGateway || body["code"] != "notices_unavailable" {
		t.Errorf("a 404 from AUB = %d %v; want 502 notices_unavailable", status, body)
	}

	// No server chosen.
	server, _ := newTestServer(t, nil)
	response, decoded := do(t, server, http.MethodGet, "/api/v1/notices", "")
	if response.StatusCode != http.StatusServiceUnavailable || decoded["code"] != "aub_not_configured" {
		t.Errorf("no server = %d %v; want 503 aub_not_configured", response.StatusCode, decoded)
	}
}
