package web

import (
	"net/http"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// NEW_244D, HITL: a person chooses one of the official Auto-Pigeon servers;
// typing any other address needs the Companion started with --debug.
func TestOnlyDebugModeAcceptsAnAddressThatIsNotOfficial(t *testing.T) {
	server, saved := newTestServer(t, nil)
	put := func(address string) int {
		response, _ := do(t, server, http.MethodPut, "/api/v1/settings",
			`{"aub_base_url":"`+address+`","port":0,"job_concurrency":0}`)
		return response.StatusCode
	}
	if code := put("https://beta.auto-pigeon.com/"); code != http.StatusOK {
		t.Fatalf("choosing the beta server = %d", code)
	}
	if saved.AUBBaseURL != "https://beta.auto-pigeon.com" {
		t.Errorf("saved %q", saved.AUBBaseURL)
	}
	if code := put("http://127.0.0.1:9190"); code != http.StatusForbidden {
		t.Errorf("typing a development address without --debug = %d, want 403", code)
	}
	if saved.AUBBaseURL != "https://beta.auto-pigeon.com" {
		t.Errorf("a refused address was saved: %q", saved.AUBBaseURL)
	}
	_, status := do(t, server, http.MethodGet, "/api/status", "")
	if status["debug"] != false || status["backend_label"] != "Auto-Pigeon beta" {
		t.Errorf("status = %v", status)
	}
	backends, _ := status["backends"].([]any)
	if len(backends) != len(config.OfficialBackends) {
		t.Errorf("backends = %v", backends)
	}

	debug, debugSaved := newTestServer(t, nil)
	debug.debug = true
	response, _ := do(t, debug, http.MethodPut, "/api/v1/settings",
		`{"aub_base_url":"http://127.0.0.1:9190","port":0,"job_concurrency":0}`)
	if response.StatusCode != http.StatusOK || debugSaved.AUBBaseURL != "http://127.0.0.1:9190" {
		t.Errorf("debug mode refused a development address: %d %q", response.StatusCode, debugSaved.AUBBaseURL)
	}
}

// TestTheEnvironmentsServerAddressReachesThePage: the page used to build its
// client from the file alone, so an address supplied by the environment — or
// by the .env or the config.json beside the executable, which set it — reached
// every CLI command and never the GUI.
func TestTheEnvironmentsServerAddressReachesThePage(t *testing.T) {
	t.Setenv(config.EnvAUBBaseURL, "https://beta.auto-pigeon.com")
	server, _ := newTestServer(t, nil)
	_, status := do(t, server, http.MethodGet, "/api/status", "")
	if status["aub_base_url"] != "https://beta.auto-pigeon.com" || status["backend_label"] != "Auto-Pigeon beta" {
		t.Errorf("status = %v", status)
	}
}
