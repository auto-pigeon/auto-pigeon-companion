package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// NEW_247A2: "Help to connect" leads to the gallery's page on hosting a game,
// at the gallery the Auto-Pigeon server names — never an address this program
// made up, never one a request chose, and nothing at all when the server has
// not said.

func TestTheHostingHelpIsOnTheGalleryTheServerNamed(t *testing.T) {
	for _, tc := range []struct{ gallery, want string }{
		{"https://gallery.example.test", "https://gallery.example.test/help/host-a-game"},
		{"https://gallery.example.test/", "https://gallery.example.test/help/host-a-game"},
		{"http://192.168.0.33:5174", "http://192.168.0.33:5174/help/host-a-game"},
		// A gallery served under a path keeps it.
		{"https://example.test/gallery/", "https://example.test/gallery/help/host-a-game"},
		// Whatever the answer carried beyond the origin is not part of the destination.
		{"https://gallery.example.test/?next=https://evil.test#x", "https://gallery.example.test/help/host-a-game"},
		{"https://someone@gallery.example.test", "https://gallery.example.test/help/host-a-game"},
		// No gallery, or not an http(s) origin: no link rather than a guessed one.
		{"", ""},
		{"   ", ""},
		{"gallery.example.test", ""},
		{"/help", ""},
		{"javascript:alert(1)", ""},
		{"file:///etc/passwd", ""},
		{"ftp://gallery.example.test", ""},
		{"https://", ""},
		{"https://gallery.example.test:bad", ""},
	} {
		if got := hostHelpURL(tc.gallery); got != tc.want {
			t.Errorf("hostHelpURL(%q) = %q, want %q", tc.gallery, got, tc.want)
		}
	}
}

func TestSiteLinksCarryTheHostingHelpAddress(t *testing.T) {
	gallery := "https://gallery.example.test/"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != aub.SiteLinksPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"gallery_url": gallery})
	}))
	defer backend.Close()
	client, err := aub.New(backend.URL, backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	server, _ := newTestServer(t, client)

	// A query naming another destination is not read: the address is the server's.
	response, body := do(t, server, http.MethodGet, "/api/v1/site-links?host_help_url=https://evil.test", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("site-links answered %d", response.StatusCode)
	}
	if body["gallery_url"] != "https://gallery.example.test" ||
		body["host_help_url"] != "https://gallery.example.test/help/host-a-game" {
		t.Fatalf("site-links = %v", body)
	}

	// A deployment that names no usable gallery gets no help link.
	gallery = "javascript:alert(1)"
	server, _ = newTestServer(t, client)
	_, body = do(t, server, http.MethodGet, "/api/v1/site-links", "")
	if body["gallery_url"] != "" || body["host_help_url"] != "" {
		t.Fatalf("an unusable gallery still produced links: %v", body)
	}

	// No server chosen at all: nothing, and still a 200 the page can read.
	server, _ = newTestServer(t, nil)
	response, body = do(t, server, http.MethodGet, "/api/v1/site-links", "")
	if response.StatusCode != http.StatusOK || body["host_help_url"] != "" {
		t.Fatalf("with no server: %d %v", response.StatusCode, body)
	}
}
