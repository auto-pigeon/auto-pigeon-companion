package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// fakeRunner stands in for the embedded AUE binary so these tests never exec
// anything — the Runner interface exists for exactly this reason.
type fakeRunner struct {
	available bool
	stdout    []byte
	err       error
	calls     []string
}

func (runner *fakeRunner) Run(_ context.Context, subcommand string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, strings.Join(append([]string{subcommand}, args...), " "))
	return runner.stdout, runner.err
}

func (runner *fakeRunner) Available() bool { return runner.available }

func newTestServer(t *testing.T, runner *fakeRunner) *Server {
	t.Helper()
	settings := config.Default()
	settings.ServerAddr = "127.0.0.1:0"
	server, err := Listen(Options{Addr: settings.ServerAddr, Config: settings, AUE: runner, Version: "0.0.0-test"})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = server.listener.Close() })
	return server
}

func TestListenRefusesANonLoopbackAddress(t *testing.T) {
	// The server hands out an authenticated session; exposing it beyond
	// loopback is refused rather than warned about.
	_, err := Listen(Options{Addr: "0.0.0.0:0", Config: config.Default()})
	if !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("err = %v, want ErrNotLoopback", err)
	}
}

func TestListenAcceptsLoopbackAndReportsARealPort(t *testing.T) {
	server := newTestServer(t, &fakeRunner{})
	if !strings.HasPrefix(server.Addr(), "127.0.0.1:") {
		t.Fatalf("Addr = %q, want a 127.0.0.1 address", server.Addr())
	}
	if strings.HasSuffix(server.Addr(), ":0") {
		t.Fatalf("Addr = %q, want the OS-assigned port, not 0", server.Addr())
	}
	if want := "http://" + server.Addr() + "/"; server.URL() != want {
		t.Fatalf("URL = %q, want %q", server.URL(), want)
	}
}

func TestStatusReportsVersionAndExtractorAvailability(t *testing.T) {
	server := newTestServer(t, &fakeRunner{available: true})

	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}

	var payload StatusPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Version != "0.0.0-test" || !payload.AUEAvailable || payload.Authenticated {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestCrossOriginRequestsAreRejected(t *testing.T) {
	// Any page in the user's browser can reach a loopback port; this check is
	// what stops one from driving the API.
	server := newTestServer(t, &fakeRunner{})

	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestSameOriginRequestsAreAllowed(t *testing.T) {
	server := newTestServer(t, &fakeRunner{})

	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Origin", "http://"+server.Addr())
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
}

func TestLoginRejectsAnEmptyBody(t *testing.T) {
	server := newTestServer(t, &fakeRunner{})

	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}

func TestMutatingRoutesRejectGET(t *testing.T) {
	server := newTestServer(t, &fakeRunner{})

	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/auth/logout", nil))

	// The mux has no GET route for this path, so it falls through to the
	// static file server, which has no such file.
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestExtractorVersionRouteReturnsRunnerOutput(t *testing.T) {
	runner := &fakeRunner{available: true, stdout: []byte("0.2.0\n")}
	server := newTestServer(t, runner)

	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/aue/version", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["version"] != "0.2.0" {
		t.Fatalf("version = %q, want %q", payload["version"], "0.2.0")
	}
	if len(runner.calls) != 1 || runner.calls[0] != "version" {
		t.Fatalf("runner calls = %v, want [version]", runner.calls)
	}
}

func TestIndexIsServedFromTheEmbeddedAssets(t *testing.T) {
	server := newTestServer(t, &fakeRunner{})

	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Auto-Pigeon Companion") {
		t.Fatalf("body does not look like index.html: %q", recorder.Body.String())
	}
}

func TestServeStopsWhenTheContextIsCancelled(t *testing.T) {
	server := newTestServer(t, &fakeRunner{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.Serve(ctx); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}
