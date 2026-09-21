package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The UX rules `AUCOM/AUE/AUT 246I1` froze, checked against the page itself.
//
// These are deliberately checks on the SHIPPED assets rather than on a rendered
// DOM: the browser journey drives behaviour, and what these pin is the shape of
// the page — which is the half that regresses quietly, by somebody appending a
// log dump to the bottom of a working page because it was the quickest place to
// put it.

func asset(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile("assets/" + name)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

// Live progress and detailed results live in ONE Activity surface: a drawer on
// a wide window, a full-screen pane on a narrow one.
func TestActivityIsOneDrawerAndOnePane(t *testing.T) {
	page := asset(t, "index.html")
	style := asset(t, "app.css")

	for _, want := range []string{`id="activity"`, `id="activity-body"`, `id="activity-open"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %s", want)
		}
	}
	if !strings.Contains(page, `aria-live="polite"`) {
		t.Error("the Activity region does not announce its changes")
	}
	// The narrow layout is a media query on the drawer, not a second element.
	if !regexp.MustCompile(`(?s)@media \(max-width: 52rem\).*?\.activity \{`).MatchString(style) {
		t.Error("app.css has no narrow-window rule for .activity, so a phone-width window " +
			"keeps a drawer with a strip of unusable page behind it")
	}
	if !strings.Contains(style, "prefers-reduced-motion") {
		t.Error("app.css does not honour a reduced-motion preference")
	}
}

// No page ends in a dump of logs, and no job result opens a browser tab.
func TestNoResultDumpAndNoNewTab(t *testing.T) {
	play := asset(t, "play.js")

	if strings.Contains(play, "window.open(") {
		t.Error("play.js opens a browser tab. A job result belongs in Activity and in Jobs, " +
			"in this window")
	}
	// The results go into the Activity body, which is outside every area.
	if !strings.Contains(play, `$("activity-body")`) {
		t.Error("play.js does not render into the Activity surface")
	}
	// The Build & Run page's own panels end with the wizard's navigation, not
	// with an output region.
	page := asset(t, "index.html")
	area := page[strings.Index(page, `id="area-play"`):]
	area = area[:strings.Index(area, `id="area-library"`)]
	for _, forbidden := range []string{"play-output", "play-log", "play-result-log"} {
		if strings.Contains(area, forbidden) {
			t.Errorf("the Build & Run page carries %q; results belong in Activity", forbidden)
		}
	}
}

// Tabs for small fixed choices, searchable selects for the ones that grow.
// Fifty maps are not fifty tabs.
func TestModesAreTabsAndResourcesAreSelects(t *testing.T) {
	play := asset(t, "play.js")
	page := asset(t, "index.html")

	// The action — a small fixed set an engine declares — is a tab bar.
	if !strings.Contains(play, `tabsFor?.("play-action")`) {
		t.Error("the engine's action is not offered as tabs, and it is a small fixed set")
	}
	// Maps, revisions, pipelines and engines stay selects.
	for _, id := range []string{"play-map", "play-revision", "play-pipeline", "play-engine"} {
		if !strings.Contains(page, `<select id="`+id+`">`) {
			t.Errorf("%s is not a select; an unbounded list of entities must not become tabs", id)
		}
		if strings.Contains(play, `tabsFor?.("`+id+`")`) {
			t.Errorf("%s was turned into tabs", id)
		}
	}
}

// A decision is a modal that traps and returns focus; Escape is the
// non-destructive answer.
func TestADecisionIsAModalThatReturnsFocus(t *testing.T) {
	core := asset(t, "core.js")

	if !strings.Contains(core, "showModal()") {
		t.Error("confirmModal does not use <dialog>.showModal, so focus is not trapped by the " +
			"platform's own accessibility layer")
	}
	if !strings.Contains(core, "opener?.focus?.()") {
		t.Error("confirmModal does not return focus to what opened it")
	}
	if !regexp.MustCompile(`(?s)addEventListener\("cancel".*?finish\(false\)`).MatchString(core) {
		t.Error("Escape does not resolve the modal as a cancel, which is the non-destructive answer")
	}
	// And cancelling a run — which stops a compiler and removes files — asks.
	if !strings.Contains(asset(t, "play.js"), "confirmModal?.({") {
		t.Error("cancelling a run does not ask; it is a decision with a consequence")
	}
}

// Changing an identity invalidates the review, and the page says what was
// cleared and why.
func TestChangingAChoiceSaysWhatItCleared(t *testing.T) {
	play := asset(t, "play.js")

	for _, phrase := range []string{
		"You changed the map,",
		"You changed the revision,",
		"You changed the build profile,",
		"You changed the engine,",
	} {
		if !strings.Contains(play, phrase) {
			t.Errorf("nothing says %q, so a review could silently describe a previous choice", phrase)
		}
	}
	if !strings.Contains(play, "function invalidate(") {
		t.Error("play.js has no single place that throws the plan away")
	}
}

// The page is told the ordered WAD declaration means precedence, and that the
// staged WADs are not what the engine reads.
func TestThePageExplainsTheOrderAndTheWADs(t *testing.T) {
	play := asset(t, "play.js")

	if !strings.Contains(play, "Later declarations win a name two WADs both hold") {
		t.Error("the review does not say that the declaration order is precedence")
	}
	if !strings.Contains(play, "the game does not read these files at run time") {
		t.Error("the review does not draw the distinction between what the compiler reads and " +
			"what the engine reads, which is the reason the WADs are staged at all")
	}
}

// The token never reaches the page's own code, and nothing writes a local path
// into browser storage.
func TestThePageStoresNothingItShouldNot(t *testing.T) {
	play := asset(t, "play.js")

	for _, forbidden := range []string{"localStorage", "sessionStorage", "document.cookie"} {
		if strings.Contains(play, forbidden) {
			t.Errorf("play.js uses %s", forbidden)
		}
	}
}
