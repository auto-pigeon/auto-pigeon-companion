package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// aulibsNoticeDir is the authority, when a sibling checkout is present.
const aulibsNoticeDir = "../../../auto-pigeon-libraries/ts/operational-notice-contract"

// vendoredNoticeFiles are the files the page runs, relative to the package.
var vendoredNoticeFiles = []string{
	"src/index.mjs",
	"schema/notice-rules.json",
	"schema/operational-notices-response-1.0.schema.json",
}

// The vendored notice contract must be byte-for-byte AULIBS'.
//
// The page runs the SAME parser, selection, dismissal key, cache rule and poll
// cadence AUP and AUG run; a copy nobody compares would be a fourth sincere
// guess at them. Skipped, loudly, when no sibling checkout is present: a skip
// says "not checked here", a pass would say "checked and identical".
func TestVendoredNoticeContractIsExactlyAULIBS(t *testing.T) {
	for _, name := range vendoredNoticeFiles {
		authority := filepath.Join(aulibsNoticeDir, name)
		want, err := os.ReadFile(authority)
		if err != nil {
			t.Skipf("no auto-pigeon-libraries checkout beside this one (%v); "+
				"the vendored %s was NOT compared against the authority", err, name)
		}
		got, err := fs.ReadFile(assetsFS(), "vendor/operational-notice-contract/"+name)
		if err != nil {
			t.Fatalf("the vendored %s is missing: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("assets/vendor/operational-notice-contract/%s has drifted from %s.\n"+
				"Re-copy it; do not edit either by hand.", name, authority)
		}
	}
}

// Every relative import the vendored module makes resolves inside the
// embedded tree — the layout keeps `../schema/*.json` working when served.
func TestTheVendoredModulesImportsResolveWhenServed(t *testing.T) {
	source, err := fs.ReadFile(assetsFS(), "vendor/operational-notice-contract/src/index.mjs")
	if err != nil {
		t.Fatal(err)
	}
	imports := regexp.MustCompile(`from "(\.\.?/[^"]+)"`).FindAllStringSubmatch(string(source), -1)
	if len(imports) == 0 {
		t.Fatal("the vendored module imports nothing; the check below would be vacuous")
	}
	for _, match := range imports {
		resolved := filepath.ToSlash(filepath.Join("vendor/operational-notice-contract/src", match[1]))
		if _, err := fs.ReadFile(assetsFS(), resolved); err != nil {
			t.Errorf("index.mjs imports %s, which resolves to %s and is not embedded", match[1], resolved)
		}
	}
	page, err := fs.ReadFile(assetsFS(), "notices.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `from "./vendor/operational-notice-contract/src/index.mjs"`) {
		t.Error("notices.mjs does not import the vendored contract")
	}
}

// A module graph is refused by the browser unless the types are right: a
// module script must be JavaScript and a `with { type: "json" }` import must
// be JSON. Stated by the server, not left to the platform's MIME table.
func TestModulesAndJSONAreServedWithTheirTypes(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for path, want := range map[string]string{
		"/notices.mjs": "text/javascript",
		"/vendor/operational-notice-contract/src/index.mjs":                                       "text/javascript",
		"/vendor/operational-notice-contract/schema/notice-rules.json":                            "application/json",
		"/vendor/operational-notice-contract/schema/operational-notices-response-1.0.schema.json": "application/json",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = "127.0.0.1:1"
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d", path, recorder.Code)
			continue
		}
		if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("GET %s Content-Type = %q, want %s", path, got, want)
		}
		if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s X-Content-Type-Options = %q, want nosniff", path, got)
		}
	}
}

// The banner renders title and body as text, never as markup.
func TestTheNoticeBannerRendersTextOnly(t *testing.T) {
	source, err := fs.ReadFile(assetsFS(), "notices.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"innerHTML", "insertAdjacentHTML", "outerHTML", "document.write"} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("notices.mjs uses %s; a notice is plain text", forbidden)
		}
	}
	page, err := fs.ReadFile(assetsFS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "cannot warn about an unplanned outage") {
		t.Error("the page does not state the notice limit")
	}
}
