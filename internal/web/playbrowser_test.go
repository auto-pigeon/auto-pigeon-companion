package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The Build & Run journey, in a real browser, against the real page.
//
// It shares the apparatus `browser_test.go` documents — the real server with
// one extra same-origin script appended — and drives the five steps, the
// Activity drawer, the invalidation messages, the leak check and the
// compiler-refusal path. `testdata/playjourney.js` is the driver.
//
// It skips without a Chrome-family browser, for the reason the other one does:
// the API journey in play_test.go has already run, and reporting the absence of
// a browser as a defect in the Companion would be reporting the wrong thing.

// playDriveHandler is driveHandler plus the one control the refusal path needs:
// a way to make the fixture backend serve a bundle it could not complete,
// halfway through the journey.
type playDriveHandler struct {
	*driveHandler
	backend *fixtureBackend
}

func (d *playDriveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/journey/break-textures" {
		// The map is SAVED, and the new revision's textures are incomplete.
		//
		// A new revision rather than a swapped bundle, because a verified
		// bundle for a revision this machine already holds is reused offline —
		// correctly — and swapping the backend's answer under it would be
		// testing nothing. What this reproduces is the real case: somebody
		// declares a WAD the deployment cannot redistribute, saves, and comes
		// back to build.
		d.backend.asset.revision++
		d.backend.asset.revisionID = fmt.Sprintf("rev-%06d", d.backend.asset.revision)
		d.backend.textures = fixtureTextureBundle(
			d.backend.asset.assetID, d.backend.asset.revision, false)
		w.WriteHeader(http.StatusNoContent)

		return
	}
	d.driveHandler.ServeHTTP(w, r)
}

func TestBuildAndRunJourneyInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	for _, size := range []struct {
		name   string
		window string
	}{
		// Both are the FULL journey. "A narrow window remains usable" is a
		// claim about whether somebody can finish the work in one, not about
		// whether the boxes line up.
		{"a desktop window", "1280,900"},
		{"a narrow window", "420,900"},
	} {
		t.Run(size.name, func(t *testing.T) {
			runPlayJourney(t, browser, size.window)
		})
	}
}

func runPlayJourney(t *testing.T, browser, window string) {
	m := newMachine(t)
	// The toolchain and the engine are approved and bound before the journey:
	// `TestFirstRunJourneyInABrowser` already drives that half through the
	// page, and repeating it here would make this test about something else.
	m.preparePlay(t)

	script, err := os.ReadFile("testdata/playjourney.js")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{
		"email":       m.backend.email,
		"password":    "hunter2",
		"asset_id":    m.backend.asset.assetID,
		"revision_id": m.backend.asset.revisionID,
		"pipeline_id": "aucom.fixture.pipeline",
		"engine_id":   "aucom.fixture.q1-engine",
		"token":       m.backend.token,
		"aub_url":     m.backend.url(),
		"cache_dir":   m.assets,
		"api_token":   m.server.Token().Value(),
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := &playDriveHandler{
		driveHandler: &driveHandler{
			server:  m.server,
			script:  script,
			config:  settings,
			reports: make(chan journeyReport, 1),
			logs:    make(chan string, 256),
		},
		backend: m.backend,
	}
	front := httptest.NewServer(handler)
	defer front.Close()

	profileDir, err := os.MkdirTemp("", "aucom-play-browser-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, browser,
		"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions",
		"--disable-background-networking", "--disable-component-update",
		"--window-size="+window, "--user-data-dir="+profileDir,
		front.URL+"/",
	)
	var browserOutput strings.Builder
	command.Stdout = &browserOutput
	command.Stderr = &browserOutput
	if err := command.Start(); err != nil {
		t.Skipf("could not start %s: %v", browser, err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = os.RemoveAll(profileDir)
	}()

	var report journeyReport
	var lines []string
	deadline := time.After(4 * time.Minute)
collect:
	for {
		select {
		case report = <-handler.reports:
			break collect
		case line := <-handler.logs:
			lines = append(lines, line)
		case <-deadline:
			for _, line := range lines {
				t.Logf("driver: %s", line)
			}
			t.Fatalf("the browser never reported back. Browser output:\n%s", browserOutput.String())
		}
	}
	for {
		select {
		case line := <-handler.logs:
			lines = append(lines, line)

			continue
		default:
		}

		break
	}
	for _, line := range lines {
		t.Logf("driver: %s", line)
	}
	for _, step := range report.Steps {
		if step.OK {
			t.Logf("ok   %s — %s", step.Step, step.Detail)

			continue
		}
		t.Errorf("FAIL %s — %s", step.Step, step.Detail)
	}
	if report.Error != "" {
		t.Errorf("the journey stopped: %s", report.Error)
	}
	if len(report.Steps) == 0 {
		t.Fatalf("the driver reported no steps at all. Browser output:\n%s", browserOutput.String())
	}
}
