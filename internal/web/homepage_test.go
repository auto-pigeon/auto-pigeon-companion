package web

import (
	"net/http"
	"strings"
	"testing"
)

// A profile card's Homepage button is the document's `source.homepage`: the
// list and the templates carry it, and the New-profile form writes it.
func TestAProfileCarriesItsProgramsHomepage(t *testing.T) {
	m := newMachine(t)

	status, body := m.call(http.MethodGet, "/api/v1/profiles", nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d: %v", status, body["error"])
	}
	if got := findItem(t, body, "id", "auto-pigeon.engine.vkquake")["homepage"]; got != "https://github.com/Novum/vkQuake" {
		t.Errorf("vkQuake's homepage = %v", got)
	}
	status, body = m.call(http.MethodGet, "/api/v1/profiles/templates", nil)
	if status != http.StatusOK {
		t.Fatalf("templates = %d: %v", status, body["error"])
	}
	if got := findItem(t, body, "id", "auto-pigeon.engine.quakespasm")["homepage"]; got != "https://quakespasm.sourceforge.net/" {
		t.Errorf("the QuakeSpasm template's homepage = %v", got)
	}

	compose := map[string]any{
		"template": "auto-pigeon.engine.quakespasm", "id": "me.engine.my-quake", "name": "My Quake build",
		"homepage": "https://example.org/my-quake",
	}
	status, body = m.call(http.MethodPost, "/api/v1/profiles/compose", compose)
	if status != http.StatusOK || body["valid"] != true {
		t.Fatalf("compose = %d, valid %v: %v", status, body["valid"], body["error"])
	}
	document, _ := body["document"].(map[string]any)
	source, _ := document["source"].(map[string]any)
	if source["homepage"] != "https://example.org/my-quake" {
		t.Fatalf("source = %v, want the homepage the form gave", source)
	}

	compose["homepage"] = "javascript:alert(1)"
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/compose", compose); status == http.StatusOK && body["valid"] == true {
		t.Fatal("a javascript: homepage composed as valid")
	}
}

// A profile of this machine's own can have its homepage changed from its page:
// a new patch version, a new digest, and so an approval to give again. A
// built-in one cannot, and the refusal says what to do instead.
func TestAnInstalledProfilesHomepageIsEditedAsANewVersion(t *testing.T) {
	m := newMachine(t)
	status, body := m.call(http.MethodPost, "/api/v1/profiles/compose", map[string]any{
		"template": "auto-pigeon.engine.quakespasm", "id": "me.engine.my-quake", "name": "My Quake build",
	})
	if status != http.StatusOK || body["valid"] != true {
		t.Fatalf("compose = %d: %v", status, body["error"])
	}
	if status, body = m.call(http.MethodPost, "/api/v1/profiles/import", map[string]any{"document": body["document"]}); status != http.StatusCreated {
		t.Fatalf("import = %d: %v", status, body["error"])
	}
	digest := body["digest"]

	status, body = m.call(http.MethodPost, "/api/v1/profiles/me.engine.my-quake/homepage",
		map[string]any{"homepage": "https://example.org/my-quake"})
	if status != http.StatusOK {
		t.Fatalf("homepage = %d: %v", status, body["error"])
	}
	if body["homepage"] != "https://example.org/my-quake" || body["version"] != "1.0.1" || body["was_version"] != "1.0.0" {
		t.Errorf("homepage %v, version %v (was %v)", body["homepage"], body["version"], body["was_version"])
	}
	if body["digest"] == digest || body["authorized"] == true {
		t.Errorf("digest %v (was %v), authorized %v: an edited document is a new one to approve", body["digest"], digest, body["authorized"])
	}

	if status, body = m.call(http.MethodPost, "/api/v1/profiles/me.engine.my-quake/homepage",
		map[string]any{"homepage": "javascript:alert(1)"}); status != http.StatusUnprocessableEntity {
		t.Errorf("a javascript: homepage = %d: %v", status, body["error"])
	}
	status, body = m.call(http.MethodPost, "/api/v1/profiles/auto-pigeon.engine.vkquake/homepage",
		map[string]any{"homepage": "https://example.org/x"})
	if status != http.StatusConflict {
		t.Errorf("editing a built-in = %d, want 409", status)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "New profile") {
		t.Errorf("the refusal does not say what to do instead: %v", body["error"])
	}
}
