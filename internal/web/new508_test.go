package web

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// NEW_508, HITL: "remove the global green band completely". The page used to
// hold six full-width surfaces between the header and the areas — tool setup,
// service notices, the editor's two requests, "back online" and the stale-page
// bar — each pushing every area down for as long as it was shown, and one of
// them (tool checks) at every start. A smaller, recoloured or renamed strip
// would be the same defect, so this holds the STRUCTURE: nothing that takes
// room in the page's flow may stand between the header and the page.
func TestNothingSitsBetweenTheHeaderAndThePage(t *testing.T) {
	page := asset(t, "index.html")
	header := strings.Index(page, "</header>")
	body := strings.Index(page, `<div class="app-body">`)
	if header < 0 || body < header {
		t.Fatal("index.html has no header followed by the page body")
	}
	// What is between them: comments, and dialogs — which are overlays, hidden
	// until opened and out of the flow. Every element that opens there must be
	// one of those.
	between := regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(page[header+len("</header>"):body], "")
	depth := 0
	for _, tag := range regexp.MustCompile(`<(/?)(div|section|aside|p|ul|nav|header|footer)\b([^>]*)>`).FindAllStringSubmatch(between, -1) {
		if tag[1] == "/" {
			depth--
			continue
		}
		if depth == 0 && !strings.Contains(tag[3], `class="auth-modal"`) {
			t.Errorf("an element that is not a dialog stands between the header and the page: <%s%s>", tag[2], tag[3])
		}
		depth++
	}
	for _, id := range []string{"reconnect-banner", "sync-help"} {
		if strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("#%s is back; its job is the header's Sync profiles chip and the Account menu", id)
		}
	}

	inside := func(container, id string) bool {
		start := strings.Index(page, container)
		if start < 0 {
			return false
		}
		rest := page[start:]
		end := len(rest)
		for _, closer := range []string{`<section id="area-library"`, "</aside>"} {
			if at := strings.Index(rest, closer); at >= 0 && at < end {
				end = at
			}
		}
		return strings.Contains(rest[:end], `id="`+id+`"`)
	}
	// Each surface has one home, beside what it is about.
	for _, id := range []string{"leak-request", "build-request"} {
		if !inside(`<section id="area-play"`, id) {
			t.Errorf("#%s is not at the top of Build & Run", id)
		}
	}
	for _, id := range []string{"tools-setup", "notice-banner", "tools-update-badge", "activity-requests"} {
		if !inside(`<aside id="activity"`, id) {
			t.Errorf("#%s is not an Activity entry", id)
		}
	}
	// Activity is always in the header; the stale page is a card out of the flow.
	if regexp.MustCompile(`<button[^>]*id="activity-open"[^>]*\bhidden\b`).MatchString(page) {
		t.Error("the Activity button starts hidden; it is a utility that is always there")
	}
	style := asset(t, "app.css")
	stale := regexp.MustCompile(`(?s)\.stale-page\s*\{[^}]*\}`).FindString(style)
	if !strings.Contains(stale, "position: fixed") {
		t.Errorf("the stale-page card is in the page's flow, where it pushes the page down: %s", stale)
	}
	// No standing "Synced" light, and no surface painted with the accent as a
	// status ground: teal is the interactive colour.
	if strings.Contains(style, ".status-dot.synced") {
		t.Error("app.css still draws a standing Synced state")
	}
	for _, rule := range []string{".request-card", ".activity-item {", ".notice {", ".stale-page"} {
		block := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(rule) + `[^}]*\}`).FindString(style)
		if block == "" {
			t.Errorf("app.css has no %s rule", rule)
		}
		if strings.Contains(block, "background: var(--accent-soft)") {
			t.Errorf("%s is painted with the accent as a status ground: %s", rule, block)
		}
	}
}

// "Sync" always names what it syncs, and the three things it used to be
// confused with keep their own words: profiles are synced, a map revision is
// downloaded, tools and the app are checked for updates.
func TestSyncNamesItsObject(t *testing.T) {
	// What a person can read: the markup and the scripts without their comments.
	comments := regexp.MustCompile(`(?s)<!--.*?-->|/\*.*?\*/|(?m)^\s*//.*$`)
	for _, name := range []string{"index.html", "account.js", "app.js", "library.js", "tools.js", "footer.js"} {
		text := comments.ReplaceAllString(asset(t, name), "")
		for _, gone := range []string{"Sync to cloud", "Sync needed", `"Synced"`, ">Synced<"} {
			if strings.Contains(text, gone) {
				t.Errorf("%s still says %s", name, gone)
			}
		}
	}
	if !strings.Contains(asset(t, "index.html"), ">Sync profiles<") {
		t.Error("the review's button does not say Sync profiles")
	}
	if !strings.Contains(asset(t, "library.js"), `t("Download revision")`) {
		t.Error("My Maps does not offer Download revision")
	}
	if !strings.Contains(asset(t, "index.html"), ">Check for updates<") {
		t.Error("Settings does not offer Check for updates")
	}
}

// Issue #20, end to end through the page's own watch: a link whose test was
// started is never pending again, and the watch says it was opened again so
// the click is answered. Dismissing that note touches no request.
func TestAUsedLeakLinkOpenedAgainIsAnsweredNotOfferedAgain(t *testing.T) {
	m := newMachine(t)
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	path, now := leakintent.Path(dir), time.Now().UTC()
	link := aub.LeakTestLink{AssetID: "map1", Revision: 7,
		ContentSHA256: strings.Repeat("ab", 32), RequestID: strings.Repeat("e", 32)}
	if err = leakintent.Receive(path, link, now); err != nil {
		t.Fatal(err)
	}
	if consumed, err := leakintent.Consume(path, link.RequestID, now); err != nil || consumed == nil {
		t.Fatalf("Consume: %v %v", consumed, err)
	}
	if err = leakintent.Used(path, link.RequestID, "build-9", now); err != nil {
		t.Fatal(err)
	}
	if _, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); body["pending"] != false || body["repeat"] != nil {
		t.Fatalf("before the link is opened again the watch says %v", body)
	}

	// The same link, delivered again.
	if err = leakintent.Receive(path, link, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
	repeat, _ := body["repeat"].(map[string]any)
	if status != http.StatusOK || body["pending"] != false || repeat == nil ||
		repeat["request_id"] != link.RequestID || repeat["build_id"] != "build-9" || repeat["revision"] != float64(7) {
		t.Fatalf("the watch answered %d %v; want no pending request and a repeat naming build-9", status, body)
	}

	if status, _ = m.call(http.MethodPost, "/api/v1/leak-test/dismiss", map[string]any{"request_id": link.RequestID, "repeat": true}); status != http.StatusOK {
		t.Fatalf("dismissing the note: %d", status)
	}
	if _, body = m.call(http.MethodGet, "/api/v1/leak-test/request", nil); body["repeat"] != nil || body["pending"] != false {
		t.Fatalf("after dismissing, the watch says %v", body)
	}
	// And it is still used: opened a third time it is a repeat again, never pending.
	if err = leakintent.Receive(path, link, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, body = m.call(http.MethodGet, "/api/v1/leak-test/request", nil); body["pending"] != false || body["repeat"] == nil {
		t.Fatalf("opened a third time, the watch says %v", body)
	}
	if _, err = os.Stat(filepath.Join(dir, leakintent.FileName)); err != nil {
		t.Fatal(err)
	}
}
