package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/buildintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_502, issue #33: the editor's build request through the own-WADs folder
// step, in a browser — a second request arriving meanwhile, a refused folder,
// the right folder, one start naming the request — and the editor told about
// each request by its own id. testdata/buildrequestjourney.js is the driver.
func TestABuildRequestSurvivesTheOwnWADsFolderInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	previous := buildRunPoll
	buildRunPoll = 20 * time.Millisecond
	t.Cleanup(func() { buildRunPoll = previous })

	m := newMachine(t)
	m.preparePlay(t)
	m.server.runner = &fixtureConverter{}
	m.saveRoom("new502", 4, q1Room("first.wad;second.wad", false), nil)
	m.backend.textures = withheldExport(m, "").build(t)

	own, wrong := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(own, "second.wad"), fixturewad.WAD("n502_mine", 32), 0o600); err != nil {
		t.Fatal(err)
	}
	first := m.editorBuildRequest(aub.BuildIntentBuild, "quake1", nil)
	second := aub.BuildRequestLink{}
	drivePlayPageWith(t, m, browser, "1280,900", "new502-own-wads", "testdata/buildrequestjourney.js", map[string]func(){
		"/journey/receive-second": func() { second = m.editorBuildRequest(aub.BuildIntentBuild, "quake1", nil) },
	}, map[string]any{
		"first_request": first.RequestID, "second_request": nextBuildRequestID(),
		"own_folder": own, "wrong_folder": wrong,
	})

	// The editor of the first request heard `building` and an ending under its
	// id; the second's never heard `building`; the second is still pending.
	statuses := m.buildStatusesSettled()
	joined := strings.Join(statuses, "|")
	if !strings.Contains(joined, first.RequestID+" building") {
		t.Errorf("the started request's editor was not told `building`: %v", statuses)
	}
	if !strings.Contains(joined, first.RequestID+" completed") && !strings.Contains(joined, first.RequestID+" failed") {
		t.Errorf("the started request's editor was not told how it ended: %v", statuses)
	}
	if strings.Contains(joined, second.RequestID+" building") || strings.Contains(joined, second.RequestID+" cancelled") {
		t.Errorf("the other request was built or cancelled: %v", statuses)
	}
	dir, _ := m.server.configDir()
	if left, _ := buildintent.Get(buildintent.Path(dir), first.RequestID, time.Now().UTC()); left != nil {
		t.Error("the started request is still pending")
	}
	if waiting, _ := buildintent.Get(buildintent.Path(dir), second.RequestID, time.Now().UTC()); waiting == nil {
		t.Error("the other request was lost")
	}
}

// nextBuildRequestID is the id editorBuildRequest gives the next request.
func nextBuildRequestID() string {
	return formatBuildRequestID(buildRequestCount + 1)
}
