package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/launch"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/tools"
)

// newTestServer builds a Server wired to temporary state: a fake tool cache and
// a config that is saved into memory rather than the developer's home
// directory.
func newTestServer(t *testing.T, client *aub.Client) (*Server, *config.Config) {
	t.Helper()
	settings := config.Default()
	settings.ToolCacheDir = t.TempDir()

	saved := settings
	server, err := NewServer(Options{
		Version:    "test",
		Client:     client,
		Config:     settings,
		Manager:    tools.NewNoop(settings.ToolCacheDir),
		Provider:   launch.ExampleProvider(),
		SaveConfig: func(updated config.Config) error { saved = updated; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, &saved
}

func do(t *testing.T, server *Server, method, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	response := recorder.Result()
	decoded := map[string]any{}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
			t.Fatalf("decoding %s %s: %v", method, path, err)
		}
	}
	return response, decoded
}

func TestServesTheEmbeddedFrontend(t *testing.T) {
	server, _ := newTestServer(t, nil)

	for _, path := range []string{"/", "/app.js", "/app.css"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, recorder.Code)
		}
	}

	// The frontend must be self-contained: no CDN, no npm, nothing fetched from
	// the network. A local-only GUI that silently depends on the internet is a
	// GUI that breaks offline.
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if body := recorder.Body.String(); strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Errorf("index.html references an external URL:\n%s", body)
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

func TestLaunchConfigs(t *testing.T) {
	server, _ := newTestServer(t, nil)
	_, body := do(t, server, http.MethodGet, "/api/launch-configs", "")
	items, ok := body["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("items = %v", body["items"])
	}
}

func TestBuildRunsTheFakeToolPipeline(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, body := do(t, server, http.MethodPost, "/api/build", `{"tool":"noop","args":["--x"]}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	output, _ := body["output"].(string)
	for _, want := range []string{"resolved noop", "verified ", "done"} {
		if !strings.Contains(output, want) {
			t.Errorf("output is missing %q:\n%s", want, output)
		}
	}
}

func TestLaunchDryRunResolvesWithoutStartingAnything(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, body := do(t, server, http.MethodPost, "/api/launch",
		`{"game":"quake","map":"e1m1","game_root":"/games/quake","dry_run":true}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	if body["started"] != false {
		t.Errorf("started = %v, want false for a dry run", body["started"])
	}
	command, _ := body["command"].(string)
	if !strings.Contains(command, "e1m1") {
		t.Errorf("command = %q", command)
	}
}

func TestLaunchUnknownGameIs404(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, _ := do(t, server, http.MethodPost, "/api/launch", `{"game":"doom","dry_run":true}`)
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.StatusCode)
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, _ := do(t, server, http.MethodPost, "/api/launch", `{"game":"quake","typo":true}`)
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

// A port already in use must not stop AUL from starting.
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
