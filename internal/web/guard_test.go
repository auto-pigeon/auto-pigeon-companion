package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
)

// withOrigin issues a request carrying an explicit Origin header, to a
// loopback Host, with a valid token — so the only thing under test is Origin.
func withOrigin(t *testing.T, server *Server, method, path, origin string) *http.Response {
	t.Helper()
	r := request(t, server, method, path, "")
	r.Header.Set("Origin", origin)
	response, _ := send(t, server, r)
	return response
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

	response := withOrigin(t, server, http.MethodGet, "/api/status", "http://"+testHost)
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

// TestAForgedHostIsRefused is the DNS-rebinding defence.
//
// A browser tricked into treating some attacker-controlled name as 127.0.0.1
// makes requests the *browser* considers same-origin: the Origin and the Host
// agree, because both are the attacker's name. The only thing that distinguishes
// them from a real local request is that the Host is not a loopback address.
func TestAForgedHostIsRefused(t *testing.T) {
	server, _ := newTestServer(t, nil)

	for _, host := range []string{"rebound.example", "rebound.example:8789", "companion.attacker.test", "192.168.0.33:8789"} {
		t.Run(host, func(t *testing.T) {
			r := request(t, server, http.MethodGet, "/api/status", "")
			r.Host = host
			// Consistent with the Host, which is exactly what a rebinding
			// attack produces: the same-origin check alone would pass this.
			r.Header.Set("Origin", "http://"+host)
			response, _ := send(t, server, r)
			if response.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", response.StatusCode)
			}
		})
	}

	// The page itself is refused too, so a rebound name cannot even be handed
	// the token that the page carries.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "rebound.example"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, r)
	if recorder.Code != http.StatusForbidden {
		t.Errorf("GET / with a forged Host = %d, want 403", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), server.Token().Value()) {
		t.Fatal("a request with a forged Host was given this run's API token")
	}

	// Every loopback spelling a user or a browser might produce still works.
	for _, host := range []string{"127.0.0.1:8789", "localhost:8789", "[::1]:8789", "127.0.0.1"} {
		r := request(t, server, http.MethodGet, "/api/status", "")
		r.Host = host
		response, _ := send(t, server, r)
		if response.StatusCode != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, response.StatusCode)
		}
	}
}

// TestTheAPINeedsItsToken: the check that actually stops another page.
func TestTheAPINeedsItsToken(t *testing.T) {
	server, _ := newTestServer(t, nil)

	for name, mutate := range map[string]func(*http.Request){
		"no token at all":       func(r *http.Request) { r.Header.Del(tokenHeader) },
		"an empty token":        func(r *http.Request) { r.Header.Set(tokenHeader, "") },
		"somebody else's token": func(r *http.Request) { r.Header.Set(tokenHeader, "not-the-token") },
		"a token in the query":  func(r *http.Request) { r.Header.Del(tokenHeader); r.URL.RawQuery = "token=" + server.Token().Value() },
	} {
		t.Run(name, func(t *testing.T) {
			r := request(t, server, http.MethodGet, "/api/status", "")
			mutate(r)
			response, body := send(t, server, r)
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.StatusCode)
			}
			message, _ := body["error"].(string)
			if !strings.Contains(message, tokenHeader) {
				t.Errorf("error = %q, want it to name the header to send", message)
			}
		})
	}

	// Authorization: Bearer works too, for a client that already has one.
	r := request(t, server, http.MethodGet, "/api/status", "")
	r.Header.Del(tokenHeader)
	r.Header.Set("Authorization", "Bearer "+server.Token().Value())
	if response, _ := send(t, server, r); response.StatusCode != http.StatusOK {
		t.Errorf("Authorization: Bearer = %d, want 200", response.StatusCode)
	}
}

// TestAPageOnAnotherSiteIsRefusedEvenWithAToken covers Sec-Fetch-Site, which is
// the browser's own account of where a request came from.
func TestAPageOnAnotherSiteIsRefusedEvenWithAToken(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for _, site := range []string{"cross-site", "same-site"} {
		r := request(t, server, http.MethodGet, "/api/status", "")
		r.Header.Set("Sec-Fetch-Site", site)
		if response, _ := send(t, server, r); response.StatusCode != http.StatusForbidden {
			t.Errorf("Sec-Fetch-Site: %s = %d, want 403", site, response.StatusCode)
		}
	}
	for _, site := range []string{"same-origin", "none"} {
		r := request(t, server, http.MethodGet, "/api/status", "")
		r.Header.Set("Sec-Fetch-Site", site)
		if response, _ := send(t, server, r); response.StatusCode != http.StatusOK {
			t.Errorf("Sec-Fetch-Site: %s = %d, want 200", site, response.StatusCode)
		}
	}
}

// TestEveryAPIRouteIsGuarded walks the registered surface rather than a list
// somebody kept up to date by hand.
func TestEveryAPIRouteIsGuarded(t *testing.T) {
	server, _ := newTestServer(t, nil)
	// The whole registered surface, from the one table the routes come from.
	// A route added without the guard cannot be added without failing here.
	patterns := make([]string, 0)
	for pattern := range server.api() {
		patterns = append(patterns, pattern)
	}
	if len(patterns) < 30 {
		t.Fatalf("only %d API routes were registered; the table looks truncated", len(patterns))
	}
	for _, pattern := range patterns {
		method, path, _ := strings.Cut(pattern, " ")
		// A concrete id, so the route matches; the guard runs before the
		// handler would find that it does not exist.
		path = strings.ReplaceAll(path, "{id}", "20260906T000000Z-0d13ed8e44d8")
		path = strings.ReplaceAll(path, "{name}", "result")
		path = strings.ReplaceAll(path, "{type}", "map")
		path = strings.ReplaceAll(path, "{rev}", "current")

		t.Run(pattern, func(t *testing.T) {
			r := request(t, server, method, path, "{}")
			r.Header.Del(tokenHeader)
			if response, _ := send(t, server, r); response.StatusCode != http.StatusUnauthorized {
				t.Errorf("untokened %s = %d, want 401", pattern, response.StatusCode)
			}

			forged := request(t, server, method, path, "{}")
			forged.Host = "rebound.example"
			if response, _ := send(t, server, forged); response.StatusCode != http.StatusForbidden {
				t.Errorf("forged-Host %s = %d, want 403", pattern, response.StatusCode)
			}
		})
	}
}

// TestMutatingRoutesRejectGET pins the other half of the guard: the mutating
// routes are registered POST-only, so a cross-site form or <img> cannot reach
// them even before the token check runs.
//
// The status is 404 rather than 405 because "GET /" is registered for the
// static frontend, so an unmatched GET falls through to the file server, which
// has no such file. That is asserted rather than corrected: the property that
// matters is that no handler runs, and a route that does not announce itself to
// a probe is the better of the two answers.
func TestMutatingRoutesRejectGET(t *testing.T) {
	server, saved := newTestServer(t, nil)
	before := saved.Session
	for _, path := range []string{"/api/auth/login", "/api/auth/logout", "/api/v1/jobs/preview"} {
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
	verified  bool
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

// Provenance is on the Runner interface because every surface that shows an
// extractor has to show whether anything verified it, and a caller holding a
// Runner must not have to type-assert to find out.
func (r *stubRunner) Provenance() aue.Provenance {
	mode := aue.ModeBundled
	if !r.verified {
		mode = aue.ModeDeveloperOverride
	}

	return aue.Provenance{Mode: mode, Verified: r.verified}
}

func serverWithRunner(t *testing.T, runner aue.Runner) *Server {
	t.Helper()
	settings := config.Default()
	server, err := NewServer(Options{
		Version: "test",
		Config:  settings,
		AUE:     runner,
		Jobs:    newTestJobs(t),
		UpdateConfig: func(mutate func(*config.Config) error) (config.Config, error) {
			current := settings
			return current, mutate(&current)
		},
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

// TestExtractorRouteWhenUnavailable covers both shapes of "this build cannot
// run AUE": no runner at all, and a runner that reports itself unavailable
// because nothing was embedded and no override is set.
func TestExtractorRouteWhenUnavailable(t *testing.T) {
	for name, runner := range map[string]aue.Runner{
		"no runner":            nil,
		"runner not available": &stubRunner{available: false},
	} {
		server := serverWithRunner(t, runner)
		response, body := do(t, server, http.MethodGet, "/api/aue/version", "")
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", name, response.StatusCode)
			continue
		}
		if !strings.Contains(body["error"].(string), aue.EnvBinaryOverride) {
			t.Errorf("%s: error %q does not name %s", name, body["error"], aue.EnvBinaryOverride)
		}
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

// TestAPageFromAnEarlierRunIsToldToReload is NEW_244D's regression: after a
// restart, an open page's token is the previous run's, and every action failed
// with advice about request headers. The refusal now carries a code the page
// turns into "reload the page", and only a token refusal carries it.
func TestAPageFromAnEarlierRunIsToldToReload(t *testing.T) {
	server, _ := newTestServer(t, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	r.Host = testHost
	r.Header.Set(tokenHeader, "a-token-from-the-previous-run")
	response, body := send(t, server, r)
	if response.StatusCode != http.StatusUnauthorized || body["code"] != codeTokenRefused {
		t.Errorf("status = %d, body = %v", response.StatusCode, body)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	r.Host = "rebound.example"
	response, body = send(t, server, r)
	if response.StatusCode != http.StatusForbidden || body["code"] != nil {
		t.Errorf("a host refusal is not a stale page: status = %d, body = %v", response.StatusCode, body)
	}
}
