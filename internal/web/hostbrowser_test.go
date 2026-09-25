package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
)

// NEW_247A2, in a real browser: a hosted game starts Private, Public is only
// ever what somebody chose, and "Help to connect" appears exactly where a game
// everyone can see needs it — beside the listing fields, in the review and in
// Activity — at the gallery the Auto-Pigeon server named.
//
// `testdata/hostjourney.js` drives the page; the hosted-game half of AUB is
// hostedFixture below, which records every preview and registration so the
// request the page actually sent is what is asserted, not what it displayed.

// hostedFixture is the part of AUB's hosted-game contract a Build & Run
// listing touches: the vocabulary, the preview, the join-content upload, the
// registration, the heartbeat and the stop.
type hostedFixture struct {
	mu         sync.Mutex
	previews   []map[string]any
	registered []map[string]any
	stopped    []string
}

func (h *hostedFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	write := func(status int, body map[string]any) {
		body["schema_version"] = aub.HostedGameSchema
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	decode := func() map[string]any {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		return body
	}
	game := func(id string, registration map[string]any, state string) map[string]any {
		return map[string]any{"id": id, "title": registration["title"], "visibility": registration["visibility"],
			"state": state, "owned_by_me": true}
	}
	path := r.URL.Path
	switch {
	case path == aub.HostedGamePrefix+"/vocabulary":
		write(http.StatusOK, map[string]any{"lan_listings": false, "heartbeat_interval_seconds": 1})
	case path == aub.HostedGamePrefix+"/preview" && r.Method == http.MethodPost:
		body := decode()
		h.previews = append(h.previews, body)
		visibility, _ := body["visibility"].(string)
		write(http.StatusOK, map[string]any{
			"endpoint":       endpointOf(body),
			"endpoint_scope": "public", "endpoint_published": visibility != "private",
			"reachability": "unverified", "visibility": visibility,
			"audience": map[string]string{"public": "everyone", "unlisted": "people with the link",
				"private": "only you"}[visibility],
			"exposed_fields": []any{}, "join_content": map[string]any{},
		})
	case path == aub.HostedGamePrefix+"/packages" && r.Method == http.MethodGet:
		write(http.StatusNotFound, map[string]any{"message": "no such package"})
	case path == aub.HostedGamePrefix+"/packages" && r.Method == http.MethodPost:
		_, _ = io.Copy(io.Discard, r.Body)
		write(http.StatusCreated, map[string]any{"created": true, "package": map[string]any{
			"package_sha256": strings.Repeat("ab", 32), "map_revision": 4}})
	case path == aub.HostedGamePrefix && r.Method == http.MethodPost:
		body := decode()
		h.registered = append(h.registered, body)
		id := "game-" + strconv.Itoa(len(h.registered))
		write(http.StatusCreated, map[string]any{"game": game(id, body, "live"), "next_heartbeat_in_seconds": 1})
	case strings.HasSuffix(path, "/heartbeat"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, aub.HostedGamePrefix+"/"), "/heartbeat")
		write(http.StatusOK, map[string]any{"game": map[string]any{"id": id, "state": "live"},
			"next_heartbeat_in_seconds": 1})
	case strings.HasSuffix(path, "/stop"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, aub.HostedGamePrefix+"/"), "/stop")
		h.stopped = append(h.stopped, id)
		write(http.StatusOK, map[string]any{"game": map[string]any{"id": id, "state": "ended"}})
	default:
		write(http.StatusNotFound, map[string]any{"message": "no route " + r.Method + " " + path})
	}
}

// endpointOf is the endpoint a registration names, as AUB prints it.
func endpointOf(registration map[string]any) string {
	host, _ := registration["endpoint_host"].(string)
	port, _ := registration["endpoint_port"].(float64)
	return host + ":" + strconv.Itoa(int(port))
}

// visibilities is what each recorded request asked for, in order.
func visibilities(requests []map[string]any) []string {
	out := make([]string, 0, len(requests))
	for _, request := range requests {
		visibility, _ := request["visibility"].(string)
		out = append(out, visibility)
	}
	return out
}

func TestHostingIsPrivateByDefaultInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	for _, run := range []struct {
		name, window, label, gallery string
	}{
		{"a desktop window", "1280,900", "host-desktop", "https://gallery.example.test"},
		{"a narrow window", "420,900", "host-narrow", "https://gallery.example.test"},
		// A deployment that has not said where its gallery is: the page says
		// help is unavailable rather than guessing an address.
		{"a server that names no gallery", "1280,900", "host-no-gallery", ""},
	} {
		t.Run(run.name, func(t *testing.T) {
			m := newMachine(t)
			// The fixture engine keeps running when it hosts, as a real one
			// does, so the listing lives until the journey stops it.
			profile := string(enginefixture.ProfileJSON)
			at := strings.Index(profile, `"id": "host_listen"`)
			if at < 0 {
				t.Fatal("the fixture engine has no host_listen action")
			}
			rest := strings.Replace(profile[at:], `"default": "ready"`, `"default": "stay"`, 1)
			m.writeProfile("aucom.fixture.q1-engine.json", []byte(profile[:at]+rest))
			hosted := &hostedFixture{}
			m.backend.games = hosted
			m.backend.gallery = run.gallery
			m.preparePlay(t)

			drivePlayPage(t, m, browser, run.window, run.label, "testdata/hostjourney.js")

			hosted.mu.Lock()
			defer hosted.mu.Unlock()
			// The review asked for Private before anybody touched "Who can see
			// it", and for Public only after somebody chose it.
			previews := visibilities(hosted.previews)
			if len(previews) == 0 || previews[0] != aub.GamePrivate {
				t.Errorf("the first preview asked for %v; a cold page must ask for private", previews)
			}
			// Exactly two launches: the first Public because it was chosen,
			// the second — nothing chosen again — Private.
			if got := visibilities(hosted.registered); len(got) != 2 || got[0] != aub.GamePublic || got[1] != aub.GamePrivate {
				t.Errorf("the launches were registered as %v, want [public private]", got)
			}
			if len(hosted.stopped) != 2 {
				t.Errorf("listings stopped: %v, want both", hosted.stopped)
			}
		})
	}
}
