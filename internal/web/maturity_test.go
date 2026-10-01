package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
)

// The local API is where the page learns that a game is unfinished, so this is
// where "the badge appears wherever Quake II can be selected, loaded, edited,
// exported or compiled" is either true or not.

// Every pipeline row carries the statement, and the Quake II ones carry the
// sentence. A page that had to know which families are unfinished would be a
// second copy of that list.
func TestThePipelineListingCarriesTheWorkInProgressStatement(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, body := do(t, server, http.MethodGet, "/api/v1/build/pipelines", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) == 0 {
		t.Fatal("no pipelines were listed")
	}
	seenQ1, seenQ2 := 0, 0
	for _, raw := range items {
		row := raw.(map[string]any)
		state, _ := row["maturity"].(map[string]any)
		if state == nil {
			t.Fatalf("%v carries no maturity", row["id"])
		}
		switch row["engine_family"] {
		case "quake1":
			seenQ1++
			if state["state"] != string(maturity.Stable) {
				t.Errorf("%v is %v; the Quake 1 path is the qualified one", row["id"], state["state"])
			}
			if state["work_in_progress"] != false || state["message"] != "" {
				t.Errorf("%v carries a work-in-progress statement", row["id"])
			}
		case "quake2", "quake3":
			seenQ2++
			want := maturity.Quake2Message
			if row["engine_family"] == "quake3" {
				want = maturity.Quake3Message
			}
			if state["work_in_progress"] != true {
				t.Errorf("%v is not marked work in progress", row["id"])
			}
			if state["message"] != want {
				t.Errorf("%v carries %q", row["id"], state["message"])
			}
			if state["feedback_invited"] != true {
				t.Errorf("%v does not invite a compatibility report", row["id"])
			}
		default:
			t.Errorf("%v declares the family %v", row["id"], row["engine_family"])
		}
	}
	if seenQ1 == 0 || seenQ2 == 0 {
		t.Errorf("the listing had %d Quake 1 and %d unfinished-family pipelines", seenQ1, seenQ2)
	}
}

// And so does every profile and every engine, on the routes the Profiles and
// Run areas read.
func TestProfileAndEngineListingsCarryTheStatement(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for _, path := range []string{"/api/v1/profiles", "/api/v1/engines"} {
		response, body := do(t, server, http.MethodGet, path, "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d", path, response.StatusCode)
		}
		items, _ := body["items"].([]any)
		if len(items) == 0 {
			t.Fatalf("%s listed nothing", path)
		}
		quake2 := 0
		for _, raw := range items {
			row := raw.(map[string]any)
			state, _ := row["maturity"].(map[string]any)
			if state == nil {
				t.Fatalf("%s: %v carries no maturity", path, row["id"])
			}
			if row["engine_family"] == "quake2" {
				quake2++
				if state["message"] != maturity.Quake2Message {
					t.Errorf("%s: %v carries %q", path, row["id"], state["message"])
				}
			}
		}
		if quake2 == 0 {
			t.Errorf("%s listed no Quake II document, so this test checked nothing", path)
		}
	}
}

func postFeedback(t *testing.T, server *Server, request map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return do(t, server, http.MethodPost, "/api/v1/feedback/compatibility", string(encoded))
}

// Nothing is attached unless the request says so, and the response says what
// was attached rather than leaving the page to remember.
func TestTheReportRouteAttachesNothingByDefault(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, body := postFeedback(t, server, map[string]any{
		"engine_family": "quake2",
		"summary":       "the areaportal did not seal",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	if body["sent"] != false {
		t.Error("the response does not say that nothing was sent")
	}
	report := body["report"].(map[string]any)
	for _, member := range []string{"versions", "operation", "profiles", "diagnostics"} {
		if _, present := report[member]; present {
			t.Errorf("%q was attached with no consent", member)
		}
	}
	shared := report["shared"].(map[string]any)
	for name, value := range shared {
		if value != false {
			t.Errorf("shared.%s = %v with no consent", name, value)
		}
	}
	document, _ := body["document"].(string)
	if !strings.Contains(document, "the areaportal did not seal") {
		t.Errorf("the document does not carry the user's own words:\n%s", document)
	}
}

// Consent attaches what it names, and the versions that arrive are this build's
// and this platform's — never a hostname, a user name or a path, because there
// is nowhere to put one.
func TestTheReportRouteAttachesWhatWasConsentedTo(t *testing.T) {
	server, _ := newTestServer(t, nil)
	_, body := postFeedback(t, server, map[string]any{
		"engine_family": "quake2",
		"summary":       "the areaportal did not seal",
		"share":         map[string]any{"versions": true, "profiles": true},
		"profiles": []map[string]any{
			{"role": "tool", "id": "auto-pigeon.ericw-tools.q2", "version": "1.0.0", "tool_version": "2.0.0-alpha7"},
		},
	})
	report := body["report"].(map[string]any)
	versions, _ := report["versions"].(map[string]any)
	if versions == nil || versions["companion"] != "test" {
		t.Errorf("the versions did not arrive: %v", report["versions"])
	}
	if versions["platform"] == "" {
		t.Error("the platform did not arrive")
	}
	profiles, _ := report["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("%d profiles arrived", len(profiles))
	}
	if _, present := report["diagnostics"]; present {
		t.Error("diagnostics arrived without consent")
	}
}

// A report is offered for an unfinished family and refused for anything else.
// The refusal is the point: a route that composed a document about the user's
// machine for any family would be a general-purpose collector.
func TestTheReportRouteRefusesAFamilyWithNothingToReport(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for _, family := range []string{"quake1", "doom", ""} {
		response, body := postFeedback(t, server, map[string]any{
			"engine_family": family,
			"summary":       "something",
		})
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status = %d, body = %v", family, response.StatusCode, body)
		}
	}
}

// The refusal a user is most likely to run into, and the one that matters: a
// description with a path in it does not get quietly cleaned up.
func TestTheReportRouteRefusesAPathInsteadOfScrubbingIt(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, body := postFeedback(t, server, map[string]any{
		"engine_family": "quake2",
		"summary":       "the areaportal did not seal",
		"description":   "the map is at /home/andrea/maps/unreleased/level.map",
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", response.StatusCode, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "absolute path") {
		t.Errorf("the refusal does not say what is wrong: %v", body["error"])
	}
}

// The page's own half of the same requirement. These are static checks, and
// they are worth having because the alternative — a browser test for every
// surface — is what nobody runs.
func TestThePageCanRenderTheStatementAndOfferTheReport(t *testing.T) {
	assets := assetsFS()
	read := func(name string) string {
		t.Helper()
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatalf("%v", err)
		}
		return string(data)
	}
	core := read("core.js")
	for _, want := range []string{"function maturityNote", "function maturityBadge", "openCompatibilityReport"} {
		if !strings.Contains(core, want) {
			t.Errorf("core.js has no %s", want)
		}
	}
	// One renderer. An area that formatted the sentence itself is the drift
	// this test exists to catch.
	for _, area := range []string{"build.js", "run.js", "profiles.js"} {
		body := read(area)
		if !strings.Contains(body, "maturityNote") {
			t.Errorf("%s does not render the work-in-progress note", area)
		}
		if !strings.Contains(body, "openCompatibilityReport") {
			t.Errorf("%s does not offer the compatibility report", area)
		}
		if strings.Contains(body, "Work in progress. Core editing") {
			t.Errorf("%s writes the sentence itself instead of rendering the one the server sent", area)
		}
	}
	page := read("index.html")
	for _, want := range []string{"feedback-panel", "feedback-share-versions", "feedback-share-diagnostics"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %s", want)
		}
	}
	// Every consent control starts clear in the markup as well as in the
	// script: a `checked` attribute here would make the opt-in a formality.
	for _, line := range strings.Split(page, "\n") {
		if strings.Contains(line, "feedback-share-") && strings.Contains(line, "checked") {
			t.Errorf("a consent checkbox is ticked in the markup:\n%s", strings.TrimSpace(line))
		}
	}
}

// A work-in-progress note is removed from the element it is inserted into.
//
// The Build page put the note beside the build's title — inside the panel's
// head — and cleaned up with `#build-current-panel > .wip`, a CHILD selector
// that could not reach it. Nothing was ever removed, so each poll of a running
// build added another copy: the operator saw the Quake III warning three times
// on one result (2026-10-01; `Q3_007` had recorded the same). A selector written
// against the page's structure breaks the next time the structure moves, so
// the cleanup is now relative to the insertion point, and this pins that no
// area goes back to guessing where its own note is.
func TestAWorkInProgressNoteIsRemovedFromWhereItIsPut(t *testing.T) {
	assets := assetsFS()
	for _, area := range []string{"build.js", "run.js", "profiles.js", "play.js", "games.js"} {
		data, err := fs.ReadFile(assets, area)
		if err != nil {
			t.Fatalf("%v", err)
		}
		for number, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, ".wip") || !strings.Contains(line, "remove()") {
				continue
			}
			if strings.Contains(line, "document.querySelectorAll(") {
				t.Errorf("%s:%d removes stale notes by a document-wide selector, which is how the note came to be "+
					"shown once per poll; remove them from the element the note is inserted into:\n%s",
					area, number+1, strings.TrimSpace(line))
			}
		}
	}
	build, err := fs.ReadFile(assets, "build.js")
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{
		`$("build-current-title").parentElement.querySelectorAll(".wip")`,
		`$("build-pipeline-note").parentElement.querySelectorAll(".wip")`,
	} {
		if !strings.Contains(string(build), want) {
			t.Errorf("build.js does not clean up beside the element it inserts after: %s", want)
		}
	}
}
