package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestNoticeBannerInABrowser renders the operational-notice banner in a real
// browser, from a fixture AUB whose clock is three hours BEHIND this machine's.
//
// The notice is active by the server's clock and long over by this one, so a
// banner that shows it proves the page derives phase from server time; the
// driver (testdata/notices.js) then signs in and out through the local server
// and checks visibility, dismissal per account, the critical notice's missing
// Dismiss, and that no account-only notice reaches the page's storage.
//
// It uses the same apparatus as TestFirstRunJourneyInABrowser — the real page,
// served by the real handler, with one driver script appended — and skips for
// the same reason when there is no Chrome-family browser.
func TestNoticeBannerInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	m, notices := noticeMachine(t)
	notices.offset = -3 * time.Hour

	script, err := os.ReadFile("testdata/notices.js")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{
		"publicTitle": publicNoticeTitle, "privateTitle": privateNoticeTitle,
		"email": m.backend.email, "password": "hunter2",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := &driveHandler{
		server: m.server, script: script, config: settings,
		reports: make(chan journeyReport, 1), logs: make(chan string, 256),
	}
	front := httptest.NewServer(handler)
	defer front.Close()

	profileDir, err := os.MkdirTemp("", "aucom-browser-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, browser,
		"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions",
		"--disable-background-networking", "--disable-component-update",
		"--window-size=1280,900", "--user-data-dir="+profileDir, front.URL+"/",
	)
	var output strings.Builder
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Skipf("could not start %s: %v", browser, err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = os.RemoveAll(profileDir)
	}()

	var report journeyReport
	deadline := time.After(90 * time.Second)
wait:
	for {
		select {
		case report = <-handler.reports:
			break wait
		case line := <-handler.logs:
			t.Logf("driver: %s", line)
		case <-deadline:
			t.Fatalf("the browser never reported back. Browser output:\n%s", output.String())
		}
	}
	for _, step := range report.Steps {
		if step.OK {
			t.Logf("ok   %s — %s", step.Step, step.Detail)
		} else {
			t.Errorf("FAIL %s — %s", step.Step, step.Detail)
		}
	}
	if report.Error != "" {
		t.Errorf("the banner journey stopped: %s", report.Error)
	}
	if len(report.Steps) < 10 {
		t.Errorf("the driver reported %d steps; the banner journey has more", len(report.Steps))
	}
}
