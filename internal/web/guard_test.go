package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/launch"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/tools"
)

// withOrigin issues a request carrying an explicit Origin header. httptest's
// NewRequest sets Host to "example.com", so "http://example.com" is the
// same-origin case and anything else is not.
func withOrigin(t *testing.T, server *Server, method, path, origin string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(""))
	request.Header.Set("Origin", origin)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder.Result()
}

func TestCrossOriginAPIRequestsAreRejected(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response := withOrigin(t, server, http.MethodGet, "/api/status", "http://evil.example")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.StatusCode)
	}
}

func TestSameOriginAndOriginlessRequestsAreAllowed(t *testing.T) {
	server, _ := newTestServer(t, nil)

	response := withOrigin(t, server, http.MethodGet, "/api/status", "http://example.com")
	if response.StatusCode != http.StatusOK {
		t.Errorf("same-origin status = %d, want 200", response.StatusCode)
	}
	// No Origin at all is a plain navigation or a curl from README.md's
	// examples, and must keep working.
	plain, _ := do(t, server, http.MethodGet, "/api/status", "")
	if plain.StatusCode != http.StatusOK {
		t.Errorf("origin-less status = %d, want 200", plain.StatusCode)
	}
}

// TestMutatingRoutesRejectGET pins the second half of the guard: the mutating
// routes are registered POST-only, so a cross-site form or <img> cannot reach
// them even before the Origin check runs.
//
// The status is 404 rather than 405 because "GET /" is registered for the
// static frontend, so an unmatched GET falls through to the file server, which
// has no such file. That is asserted rather than corrected: the property that
// matters is that no handler runs, and a route that does not announce itself to
// a probe is the better of the two answers.
func TestMutatingRoutesRejectGET(t *testing.T) {
	server, saved := newTestServer(t, nil)
	before := saved.Session
	for _, path := range []string{"/api/auth/login", "/api/auth/logout", "/api/build", "/api/launch"} {
		response, _ := do(t, server, http.MethodGet, path, "")
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (no handler reached)", path, response.StatusCode)
		}
	}
	if saved.Session != before {
		t.Error("a GET to a mutating route changed the persisted session")
	}
}

// TestAPIResponsesAreNotCached guards the Cache-Control header: several of
// these responses carry account state, and a browser holding one across runs
// would show a stale signed-in page.
func TestAPIResponsesAreNotCached(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, _ := do(t, server, http.MethodGet, "/api/status", "")
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// stubRunner is an aue.Runner that returns fixed output, so the extractor route
// is testable without a real extractor binary.
type stubRunner struct {
	output    string
	err       error
	available bool
	gotArgs   []string
}

func (r *stubRunner) Run(_ context.Context, subcommand string, args ...string) ([]byte, error) {
	r.gotArgs = append([]string{subcommand}, args...)
	if r.err != nil {
		return nil, r.err
	}
	return []byte(r.output), nil
}

func (r *stubRunner) Available() bool { return r.available }

func serverWithRunner(t *testing.T, runner aue.Runner) *Server {
	t.Helper()
	settings := config.Default()
	settings.ToolCacheDir = t.TempDir()
	server, err := NewServer(Options{
		Version:    "test",
		Config:     settings,
		AUE:        runner,
		Manager:    tools.NewNoop(settings.ToolCacheDir),
		Provider:   launch.ExampleProvider(),
		SaveConfig: func(config.Config) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestExtractorVersionRouteReturnsRunnerOutput(t *testing.T) {
	runner := &stubRunner{output: "auto-pigeon-extractor 9.9.9\n", available: true}
	server := serverWithRunner(t, runner)

	response, body := do(t, server, http.MethodGet, "/api/aue/version", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", response.StatusCode, body)
	}
	if body["version"] != "auto-pigeon-extractor 9.9.9" {
		t.Errorf("version = %v, want the trimmed runner output", body["version"])
	}
	if len(runner.gotArgs) != 1 || runner.gotArgs[0] != "version" {
		t.Errorf("runner called with %v, want exactly [version]", runner.gotArgs)
	}
}

func TestExtractorRouteWithNoRunnerIsUnavailable(t *testing.T) {
	server := serverWithRunner(t, nil)
	response, body := do(t, server, http.MethodGet, "/api/aue/version", "")
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	if !strings.Contains(body["error"].(string), aue.EnvBinaryOverride) {
		t.Errorf("error %q does not name %s", body["error"], aue.EnvBinaryOverride)
	}
}

func TestExtractorFailureIsReportedAsBadGateway(t *testing.T) {
	server := serverWithRunner(t, &stubRunner{err: errors.New("boom"), available: true})
	response, _ := do(t, server, http.MethodGet, "/api/aue/version", "")
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
}

// TestStatusReportsExtractorAvailability covers what the frontend renders
// before any extractor call is made.
func TestStatusReportsExtractorAvailability(t *testing.T) {
	for _, available := range []bool{true, false} {
		server := serverWithRunner(t, &stubRunner{available: available})
		_, body := do(t, server, http.MethodGet, "/api/status", "")
		if body["aue_available"] != available {
			t.Errorf("aue_available = %v, want %v", body["aue_available"], available)
		}
	}
}

// TestAUBRoutesWithoutAConfiguredAddress covers the address rule's user-facing
// half: no AUB address configured is a named, actionable error, never a silent
// fallback to a compiled-in default.
func TestAUBRoutesWithoutAConfiguredAddress(t *testing.T) {
	t.Setenv(config.EnvAUBBaseURL, "")
	server := serverWithRunner(t, nil)

	_, status := do(t, server, http.MethodGet, "/api/status", "")
	if status["aub_base_url"] != "" {
		t.Errorf("aub_base_url = %v, want empty when nothing is configured", status["aub_base_url"])
	}

	response, body := do(t, server, http.MethodPost, "/api/auth/login",
		`{"email":"a@example","password":"p"}`)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("login status = %d, want 503", response.StatusCode)
	}
	if !strings.Contains(body["error"].(string), config.EnvAUBBaseURL) {
		t.Errorf("error %q does not name %s", body["error"], config.EnvAUBBaseURL)
	}
}
