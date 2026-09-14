package web

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The About area's own tests.
//
// What is worth testing here is narrow and specific: this program must serve the
// artefact **verbatim** — it does not parse it, so a test that only checked
// "the route answers 200" would pass on a handler that had quietly re-encoded
// it — and the area has to be reachable, which means the tab, the section and
// the script are all in the page the binary serves. A renderer's behaviour is
// not testable from Go and is not attempted here; `auto-pigeon-tools`' About
// lane drives the real browser for that.

func TestServesTheAboutArtefactVerbatim(t *testing.T) {
	server, _ := newTestServer(t, nil)

	embedded, err := fs.ReadFile(assetsFS(), aboutAsset)
	if err != nil {
		t.Fatalf("assets/%s is not embedded: %v — run `python3 scripts/aup/about/emit.py "+
			"--tools-dir <aut> --vendor <companion>` and rebuild", aboutAsset, err)
	}
	if len(embedded) == 0 {
		t.Fatalf("assets/%s is embedded but empty", aboutAsset)
	}

	response, _ := do(t, server, http.MethodGet, "/api/about", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/about = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the page re-reads this on every visit", got)
	}
	// Byte for byte: the handler's whole job is to not have an opinion.
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request(t, server, http.MethodGet, "/api/about", ""))
	if body := recorder.Body.Bytes(); !bytes.Equal(body, embedded) {
		t.Errorf("the served body is not the embedded artefact (%d bytes served, %d embedded)",
			len(body), len(embedded))
	}
}

func TestTheAboutArtefactIsTheGalleryTree(t *testing.T) {
	server, _ := newTestServer(t, nil)
	_, body := do(t, server, http.MethodGet, "/api/about", "")

	// The schema and the digest are the contract two other applications read the
	// same file under. Checked here so a hand-edited or half-written copy fails
	// in this repository's own suite rather than in somebody's About page.
	if got := body["schema"]; got != "auto-pigeon.site-content/1.0" {
		t.Errorf("schema = %v, want auto-pigeon.site-content/1.0", got)
	}
	digest, _ := body["digest"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
		t.Errorf("digest = %q, want a SHA-256", digest)
	}
	about, _ := body["about"].(map[string]any)
	blocks, _ := about["blocks"].([]any)
	if len(blocks) == 0 {
		t.Fatal("the artefact has no About blocks")
	}
	// The first paragraph is the answer to "what is this", which is the whole
	// reason the area exists.
	first, _ := blocks[0].(map[string]any)
	encoded, _ := json.Marshal(first)
	if !strings.Contains(string(encoded), "Auto-Pigeon is a level editor") {
		t.Errorf("the first block does not open with the description: %s", encoded)
	}
}

func TestTheAboutRouteNeedsTheRunToken(t *testing.T) {
	server, _ := newTestServer(t, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/about", nil)
	r.Host = testHost // the Host guard passes; the token is what this test is about
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, r)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/about without a token = %d, want 401", recorder.Code)
	}
}

// The other half of "render it with outbound networking disabled": the About area has no address to
// fetch from, and the page it lives on is not ALLOWED to fetch one. The policy has always said so —
// this is the assertion that keeps it saying so, because an About area is exactly the kind of page
// somebody would be tempted to let reach a gallery, a CDN or an image host.
func TestTheAboutPageCannotReachAnotherOrigin(t *testing.T) {
	server, _ := newTestServer(t, nil)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = testHost
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, r)

	policy := recorder.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "connect-src 'self'", "img-src 'self' data:"} {
		if !strings.Contains(policy, want) {
			t.Errorf("the page's policy is %q, which does not contain %q", policy, want)
		}
	}
}

func TestTheAboutAreaIsWiredIntoThePage(t *testing.T) {
	server, _ := newTestServer(t, nil)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = testHost
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, r)
	page := recorder.Body.String()

	// A tab that navigates nowhere and a section nothing draws into are the two
	// halves of "the area is missing", and each is a one-line omission.
	for _, want := range []string{`data-area="about"`, `id="area-about"`, `id="about-body"`,
		`src="about.js"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the served page does not contain %s", want)
		}
	}

	// And the renderer registers itself, which is how `app.js` reaches it.
	renderer, err := fs.ReadFile(assetsFS(), "about.js")
	if err != nil {
		t.Fatalf("about.js is not embedded: %v", err)
	}
	if !strings.Contains(string(renderer), "window.AUCOM.areas.about") {
		t.Error("about.js does not register window.AUCOM.areas.about, so navigating there would " +
			"show an empty section")
	}
	// The area list in app.js has to know the name, or the tab press falls back
	// to Library.
	app, err := fs.ReadFile(assetsFS(), "app.js")
	if err != nil {
		t.Fatalf("app.js is not embedded: %v", err)
	}
	if !strings.Contains(string(app), `"about"`) {
		t.Error(`app.js does not list "about" in areaNames, so #about would land on Library`)
	}
}
