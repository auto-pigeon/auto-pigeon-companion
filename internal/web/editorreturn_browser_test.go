package web

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_633 (AUCOM #37, #49), in a real browser against the real page and
// server, with the fixture compiler bound as a managed install:
// testdata/editorreturnjourney.js is the driver. A fixture, not the live
// editor: the receipt's editor address is the fake AUB's, because no AUB sends
// one yet.
func TestTheWayBackToTheEditorInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	const driver = "testdata/editorreturnjourney.js"
	requestID := strings.Repeat("a", 32)
	setup := func(t *testing.T, leaks bool, address func(string) string) *machine {
		m, _ := newLeakMachine(t)
		wad := fixturewad.WAD("n633_floor", 8)
		m.saveRoom("new633", 1, q1Room("new633.wad", leaks, "n633_floor"),
			leakBundle("new633", 1, []string{"new633.wad"}, map[string][]byte{"new633.wad": wad}))
		m.backend.mu.Lock()
		m.backend.leakEditorReturn = address
		m.backend.mu.Unlock()
		return m
	}
	editor := "http://editor.example.test/edit/map/new633/room?revision=1&leak_request=" + requestID

	// A leak found, a verified address, the managed 9.0002 install: Show in
	// editor, and the stage row names 9.0002. Then a page opened only after
	// all of that (the late reconnect) gets the same control.
	t.Run("verified-then-late", func(t *testing.T) {
		m := setup(t, true, func(string) string { return editor })
		_, _, identity := m.bindManagedFixture("9.0002")
		m.receiveLeakLink(requestID, "quake1")
		drivePlayPageWith(t, m, browser, "1280,900", "n633-verified", driver, nil, map[string]any{
			"scenario": "returned", "expect": "verified", "editor_url": editor, "control_text": "Show in editor",
			"stage_tool": "Ran fixture-qbsp 9.0002 (" + identity + ")"})
		manifests, err := build.List(m.builds)
		if err != nil || len(manifests) != 1 {
			t.Fatalf("builds after the journey: %d %v", len(manifests), err)
		}
		drivePlayPageWith(t, m, browser, "1280,900", "n633-late", driver, nil, map[string]any{
			"scenario": "late", "expect": "verified", "editor_url": editor, "control_text": "Show in editor",
			"build_id": manifests[0].BuildID, "state": string(manifests[0].State)})
	})
	// No leak and no address — today's AUB: the sentence, nothing to open.
	t.Run("missing", func(t *testing.T) {
		m := setup(t, false, nil)
		m.receiveLeakLink(requestID, "quake1")
		drivePlayPageWith(t, m, browser, "1280,900", "n633-missing", driver, nil, map[string]any{
			"scenario": "returned", "expect": "missing"})
	})
	// An address for another map: refused, said, and never a link.
	t.Run("refused", func(t *testing.T) {
		m := setup(t, false, func(id string) string {
			return "http://editor.example.test/edit/map/another-map?revision=1&leak_request=" + id
		})
		m.receiveLeakLink(requestID, "quake1")
		drivePlayPageWith(t, m, browser, "1280,900", "n633-refused", driver, nil, map[string]any{
			"scenario": "returned", "expect": "refused"})
	})
}
