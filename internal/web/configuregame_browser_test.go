package web

import (
	"testing"
)

// NEW_374 L3, in a real browser against the real page: Configure a game
// follows the Install window and is never lost behind it, and nothing brings
// it back once it has no business being there. testdata/configuregame.js is
// the driver; each scenario is one page, because "once per page" and a
// person's Cancel are that page's state.
func TestConfigureAGameFollowsTheInstallWindowInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	for _, scenario := range []string{
		"after-install", "open-profiles", "install-cancelled", "install-failed", "left-while-installing",
		"stale-answer", "configured-elsewhere", "changed-request", "other-dialog", "notice-waits",
	} {
		t.Run(scenario, func(t *testing.T) {
			m := newToolsMachineAsking(t)
			drivePlayPageWith(t, m.machine, browser, "1280,900", scenario, "testdata/configuregame.js", map[string]func(){
				"/journey/tools-step":  m.step,
				"/journey/tools-fail":  func() { m.releases.Fail.Store(true) },
				"/journey/bind-engine": func() { m.bindPlayableEngine(t) },
			}, map[string]any{"scenario": scenario})
		})
	}
	// A Companion with no coordinator: nothing to wait for.
	t.Run("nothing-to-install", func(t *testing.T) {
		m := newMachine(t)
		drivePlayPage(t, m, browser, "1280,900", "nothing-to-install", "testdata/configuregame.js",
			map[string]any{"scenario": "nothing-to-install"})
	})
}
