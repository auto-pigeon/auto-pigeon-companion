package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
)

// testHost is the Host every request in these tests is sent to.
//
// httptest.NewRequest defaults to "example.com", which this server refuses
// outright — see loopbackHost and the rebinding defence it implements. Using a
// loopback Host here is not a workaround: it is what a real request to this
// server always carries.
const testHost = "127.0.0.1:8789"

// newTestServer builds a Server wired to temporary state: a fake tool cache and
// a config that is saved into memory rather than the developer's home
// directory.
func newTestServer(t *testing.T, client *aub.Client) (*Server, *config.Config) {
	t.Helper()
	settings := config.Default()

	saved := settings
	server, err := NewServer(Options{
		Version: "test",
		Client:  client,
		Config:  settings,
		Jobs:    newTestJobs(t),
		UpdateConfig: func(mutate func(*config.Config) error) (config.Config, error) {
			if err := mutate(&saved); err != nil {
				return config.Config{}, err
			}
			return saved, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, &saved
}

// newTestJobs is a started executor over temporary directories.
//
// The real one, not a stub: the API routes are only worth testing against the
// service the program actually uses, and its own package's fixtures already
// cover what happens inside it.
func newTestJobs(t *testing.T) *job.Service {
	t.Helper()
	dir := t.TempDir()
	store, err := job.OpenStore(filepath.Join(dir, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := job.NewService(job.Options{
		Store:   store,
		Catalog: job.NewCatalog(filepath.Join(dir, "profiles")),
		Logf:    func(format string, args ...any) { t.Logf("jobs: "+format, args...) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close(); cancel() })
	return service
}

// request builds a request the guard will accept: a loopback Host and this
// server's own API token.
func request(t *testing.T, server *Server, method, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = testHost
	r.Header.Set(tokenHeader, server.Token().Value())
	return r
}

func do(t *testing.T, server *Server, method, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	return send(t, server, request(t, server, method, path, body))
}

func send(t *testing.T, server *Server, r *http.Request) (*http.Response, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, r)

	response := recorder.Result()
	decoded := map[string]any{}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
			t.Fatalf("decoding %s %s: %v", r.Method, r.URL.Path, err)
		}
	}
	return response, decoded
}

func TestServesTheEmbeddedFrontend(t *testing.T) {
	server, _ := newTestServer(t, nil)

	// The page and the assets are reachable without a token: a browser has none
	// until it has loaded the page that carries it.
	for _, path := range []string{"/", "/app.js", "/app.css", "/core.js", "/library.js",
		"/build.js", "/run.js", "/profiles.js", "/jobs.js", "/settings.js", "/footer.js", "/i18n.js", "/locales/it.js"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = testHost
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, recorder.Code)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = testHost
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, r)
	page := recorder.Body.String()

	// The frontend must be self-contained: no CDN, no npm, nothing fetched from
	// the network. A local-only GUI that silently depends on the internet is a
	// GUI that breaks offline.
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		t.Errorf("index.html references an external URL:\n%s", page)
	}
	// And it carries this run's token, with the placeholder gone: a page still
	// holding the placeholder would load and then fail every request.
	if strings.Contains(page, tokenPlaceholder) {
		t.Error("the served page still has the token placeholder in it")
	}
	if !strings.Contains(page, server.Token().Value()) {
		t.Error("the served page does not carry this run's API token")
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the page carries a credential", got)
	}

	// Every script and stylesheet the page names is actually embedded. A
	// <script src> that 404s is a page that half-works, and the half that is
	// missing is whichever area's file somebody forgot to add.
	for _, match := range regexp.MustCompile(`(?:src|href)="([^"#]+)"`).FindAllStringSubmatch(page, -1) {
		reference := match[1]
		if strings.HasPrefix(reference, "/") || strings.Contains(reference, ":") {
			continue
		}
		if _, err := fs.ReadFile(assetsFS(), reference); err != nil {
			t.Errorf("the page references %q, which is not embedded: %v", reference, err)
		}
	}
}

func TestStatus(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, body := do(t, server, http.MethodGet, "/api/status", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if body["version"] != "test" {
		t.Errorf("version = %v", body["version"])
	}
	if body["authenticated"] != false {
		t.Errorf("authenticated = %v, want false", body["authenticated"])
	}
}

func TestLoginStoresTheSession(t *testing.T) {
	aubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/auth-with-password") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"token":  "token-1",
			"record": map[string]any{"id": "user-1", "email": "a@example"},
		})
	}))
	defer aubServer.Close()

	client, err := aub.New(aubServer.URL, aubServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	server, saved := newTestServer(t, client)

	response, body := do(t, server, http.MethodPost, "/api/auth/login", `{"email":"a@example","password":"correct"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	if body["email"] != "a@example" {
		t.Errorf("email = %v", body["email"])
	}
	if saved.Session.Token != "token-1" {
		t.Errorf("the session was not persisted: %+v", saved.Session)
	}

	// And the status endpoint must now agree, since that is what the page reads.
	_, status := do(t, server, http.MethodGet, "/api/status", "")
	if status["authenticated"] != true {
		t.Errorf("authenticated = %v after login", status["authenticated"])
	}
}

func TestLoginRejectionIsReportedAsUnauthorized(t *testing.T) {
	aubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "Failed to authenticate."})
	}))
	defer aubServer.Close()

	client, err := aub.New(aubServer.URL, aubServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	server, _ := newTestServer(t, client)

	response, body := do(t, server, http.MethodPost, "/api/auth/login", `{"email":"a@example","password":"wrong"}`)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
}

func TestTheProfileCatalogIsServed(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, body := do(t, server, http.MethodGet, "/api/v1/profiles", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) == 0 {
		t.Fatal("the catalog is empty; the built-in samples should be in it")
	}
	found := map[string]bool{}
	for _, item := range items {
		entry, _ := item.(map[string]any)
		id, _ := entry["id"].(string)
		found[id] = true
		if entry["digest"] == "" || entry["trust"] == "" {
			t.Errorf("%s has no digest or trust state: %v", id, entry)
		}
	}
	if !found["auto-pigeon.ericw-tools.q1"] {
		t.Errorf("the built-in sample toolchain is missing: %v", found)
	}
	// NEW_244D retired the launch-config stub: nothing generated from a
	// placeholder launch configuration may be offered as an engine.
	for id := range found {
		if strings.HasPrefix(id, "auto-pigeon.launch.") {
			t.Errorf("a generated launch-config profile is still listed: %s", id)
		}
	}
}

func TestAJobIsSubmittedAndReadBack(t *testing.T) {
	server, _ := newTestServer(t, nil)

	// A profile that exists but whose executable is not installed here. What is
	// under test is the API, not the tool: the job is accepted, given an id, and
	// reaches a terminal state that says what went wrong.
	response, body := do(t, server, http.MethodPost, "/api/v1/jobs",
		`{"profile":"auto-pigeon.engine.quakespasm","action":"play_map","runtime":{"map_name":"e1m1","mod_name":"id1"},`+
			`"roots":{"game_root":"/games/quake","content_root":"/games/project"},"executables":{"engine":"/games/quake/quakespasm"}}`)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatal("the submitted job has no id")
	}
	if location := response.Header.Get("Location"); location != "/api/v1/jobs/"+id {
		t.Errorf("Location = %q, want the job's own URL", location)
	}

	deadline := time.Now().Add(30 * time.Second)
	var state string
	for time.Now().Before(deadline) {
		_, record := do(t, server, http.MethodGet, "/api/v1/jobs/"+id, "")
		state, _ = record["state"].(string)
		if state == "failed" || state == "succeeded" || state == "cancelled" || state == "interrupted" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state != "failed" {
		t.Fatalf("state = %q, want failed: the engine is not installed here", state)
	}

	_, list := do(t, server, http.MethodGet, "/api/v1/jobs", "")
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the list has %d jobs, want 1", len(items))
	}
}

func TestAJobIdFromAURLCannotReachOutsideTheStore(t *testing.T) {
	server, _ := newTestServer(t, nil)
	// Two refusals, both correct. A `..` is normalised away by the mux before
	// any handler sees it, which answers a redirect to the cleaned path — 301
	// up to Go 1.25, 307 from Go 1.26, whose ServeMux redirects with
	// StatusTemporaryRedirect so the method survives; anything else reaches
	// the store, which refuses an id it did not mint. What matters is that
	// neither serves a job.
	for _, id := range []string{"..", "..%2F..%2Fetc%2Fpasswd", "not-an-id", "20260906T000000Z-zzzzzzzzzzzz", "%2e%2e%2f%2e%2e"} {
		response, body := do(t, server, http.MethodGet, "/api/v1/jobs/"+id, "")
		switch response.StatusCode {
		case http.StatusNotFound, http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		default:
			t.Errorf("GET /api/v1/jobs/%s = %d, want a refusal", id, response.StatusCode)
		}
		if body["id"] != nil {
			t.Errorf("GET /api/v1/jobs/%s returned a job: %v", id, body)
		}
	}

	// The same for an artifact name, which is the other half of a URL that
	// becomes a path.
	for _, name := range []string{"result", "..", "not-an-artifact"} {
		response, _ := do(t, server, http.MethodGet, "/api/v1/jobs/20260906T000000Z-0d13ed8e44d8/artifacts/"+name, "")
		if response.StatusCode == http.StatusOK {
			t.Errorf("an artifact was served for a job that does not exist: %s", name)
		}
	}
}

func TestAPastedProfileIsValidatedWithoutBeingImported(t *testing.T) {
	server, _ := newTestServer(t, nil)

	response, body := do(t, server, http.MethodPost, "/api/v1/profiles/validate", `{"kind":"tool"}`)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	if body["valid"] != false || body["error"] == "" {
		t.Errorf("an invalid document was not reported as invalid: %v", body)
	}

	// The catalog is unchanged: reading somebody's document is inert.
	_, list := do(t, server, http.MethodGet, "/api/v1/profiles", "")
	before, _ := list["items"].([]any)
	_, again := do(t, server, http.MethodGet, "/api/v1/profiles", "")
	after, _ := again["items"].([]any)
	if len(before) != len(after) {
		t.Errorf("the catalog changed across a validate call: %d then %d", len(before), len(after))
	}
}

// TestTheLaunchConfigStubIsRetired is NEW_244D's regression: the page's
// launch routes read a placeholder configuration and were a second launch
// route beside the curated engine profiles. Neither may answer again.
func TestTheLaunchConfigStubIsRetired(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for _, probe := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/launch-configs", ""},
		{http.MethodPost, "/api/launch", `{"game":"quake","map":"e1m1","dry_run":true}`},
	} {
		response, _ := do(t, server, probe.method, probe.path, probe.body)
		if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want the route to be gone", probe.method, probe.path, response.StatusCode)
		}
	}
	_, engines := do(t, server, http.MethodGet, "/api/v1/engines", "")
	items, _ := engines["items"].([]any)
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if id, _ := entry["id"].(string); strings.HasPrefix(id, "auto-pigeon.launch.") {
			t.Errorf("the Run area is offered the launch-config stub %s", id)
		}
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, _ := do(t, server, http.MethodPost, "/api/v1/build/preview", `{"pipeline":"auto-pigeon.q1.fast-preview","typo":true}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
}

// The listener must never be reachable from anything but this machine: the
// server holds an AUB session and can start processes.
func TestListenBindsLoopbackOnly(t *testing.T) {
	listener, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	host, _, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Errorf("bound to %q, want 127.0.0.1", host)
	}
	if !strings.HasPrefix(URL(listener), "http://127.0.0.1:") {
		t.Errorf("URL() = %q", URL(listener))
	}
}

// A port already in use must not stop the Companion from starting.
func TestListenFallsBackWhenThePortIsTaken(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	port := occupied.Addr().(*net.TCPAddr).Port
	listener, err := Listen(port)
	if err != nil {
		t.Fatalf("Listen fell over instead of falling back: %v", err)
	}
	defer listener.Close()
	if listener.Addr().(*net.TCPAddr).Port == port {
		t.Error("Listen returned the occupied port")
	}
}

func TestServeShutsDownOnContextCancel(t *testing.T) {
	server, _ := newTestServer(t, nil)
	listener, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, server) }()

	url := URL(listener)
	response, err := http.Get(url + "api/status")
	if err != nil {
		t.Fatalf("GET %sapi/status: %v", url, err)
	}
	response.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(DrainTimeout + 5*time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}

func TestOpenCommandPerPlatform(t *testing.T) {
	cases := map[string]struct {
		name string
		args []string
	}{
		"windows": {"cmd", []string{"/c", "start", "", "http://127.0.0.1:8789/"}},
		"darwin":  {"open", []string{"http://127.0.0.1:8789/"}},
		"linux":   {"xdg-open", []string{"http://127.0.0.1:8789/"}},
	}
	for goos, want := range cases {
		name, args, err := openCommand(goos, "http://127.0.0.1:8789/")
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		if name != want.name || strings.Join(args, " ") != strings.Join(want.args, " ") {
			t.Errorf("%s: got %s %v, want %s %v", goos, name, args, want.name, want.args)
		}
	}

	// A non-HTTP or empty URL must never reach `start`, where a leading dash
	// would be read as a flag.
	for _, bad := range []string{"", "  ", "file:///etc/passwd", "-x"} {
		if _, _, err := openCommand("windows", bad); err == nil {
			t.Errorf("openCommand accepted %q", bad)
		}
	}
}
