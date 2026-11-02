package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_380, in a real browser against the real page and server:
// testdata/leakjourney.js is the driver.
func TestALeakRequestOpensReadyToBuildInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	room := func(m *machine, revision int) {
		wad := fixturewad.WAD("n380_floor", 8)
		m.saveRoom("new380", revision, q1Room("new380.wad", false, "n380_floor"),
			leakBundle("new380", revision, []string{"new380.wad"}, map[string][]byte{"new380.wad": wad}))
	}
	for _, scenario := range []string{"ready", "held-answer", "replaced-when-ready", "saved-since", "form-changed"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := newLeakMachine(t)
			room(m, 1)
			m.receiveLeakLink(strings.Repeat("a", 32), "quake1")
			drivePlayPageWith(t, m, browser, "1280,900", scenario, "testdata/leakjourney.js", map[string]func(){
				"/journey/receive-b": func() { room(m, 2); m.receiveLeakLink(strings.Repeat("b", 32), "quake1") },
				"/journey/new-save":  func() { room(m, 2) },
			}, map[string]any{"scenario": scenario})
		})
	}
	t.Run("setup-recovery", func(t *testing.T) {
		m := newMachine(t)
		m.writeProfile("aucom.fixture.leak-qbsp.json", leakToolJSON(mustExecutable(t)))
		m.writeProfile("aucom.fixture.q1-leak.json", leakPipelineJSON())
		m.server.runner = &fixtureConverter{}
		m.signIn()
		if status, body := m.call(http.MethodPost, "/api/v1/leak-test/pipelines",
			map[string]any{"game": "quake1", "pipeline": "aucom.fixture.q1-leak"}); status != http.StatusOK {
			t.Fatalf("pinning: %d %v", status, body)
		}
		room(m, 1)
		m.receiveLeakLink(strings.Repeat("a", 32), "quake1")
		drivePlayPageWith(t, m, browser, "1280,900", "setup-recovery", "testdata/leakjourney.js", map[string]func(){
			"/journey/approve": func() { m.approveAndBind("aucom.fixture.leak-qbsp") },
		}, map[string]any{"scenario": "setup-recovery"})
	})
}
