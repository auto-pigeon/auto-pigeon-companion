package web

import (
	"net/http"
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
