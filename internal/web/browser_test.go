package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
)

// The first-run journey, in a real browser, against the real page.
//
// # Why this exists as well as TestFirstRunJourney
//
// That test drives the API. This one drives what a person actually touches: the
// buttons, the forms, the focus, the DOM the page builds from those responses.
// Between them they answer two different questions — "does the Companion do the
// right thing" and "can somebody make it" — and only the second one catches a
// selector that no longer matches, a handler that was never wired up, or an
// area that renders nothing because a field was renamed.
//
// # How it drives without a browser-automation dependency
//
// There is no Selenium, no chromedp, no WebSocket client, and no npm. The test
// wraps the real server with a handler that does three small things:
//
//   - serves the real page with ONE extra <script src="/journey/drive.js">
//     appended, which the page's own Content-Security-Policy already permits
//     because it is same-origin;
//   - serves that script, which is testdata/journey.js;
//   - accepts the report it POSTs back.
//
// The script therefore runs in the real document, after the real application
// scripts, and drives them by clicking real elements. Everything a browser
// contributes — layout, event dispatch, fetch, the CSP, focus — is genuinely
// there, and the whole apparatus is one handler and a text file.
//
// # And why it skips rather than fails without a browser
//
// A developer's machine and a CI runner may have no Chrome. Skipping is honest:
// the API journey still ran, and a test that failed here would be reporting the
// absence of a browser as a defect in the Companion. AUCOM_TEST_BROWSER names
// one explicitly.

// browserCandidates are the executables this looks for, in order.
var browserCandidates = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
	"microsoft-edge", "chrome",
}

func findBrowser(t *testing.T) string {
	t.Helper()
	if named := os.Getenv("AUCOM_TEST_BROWSER"); named != "" {
		path, err := exec.LookPath(named)
		if err != nil {
			t.Fatalf("AUCOM_TEST_BROWSER=%q: %v", named, err)
		}
		return path
	}
	for _, candidate := range browserCandidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

// journeyStep is one thing the driver script did, as it reported it.
type journeyStep struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type journeyReport struct {
	Steps []journeyStep `json:"steps"`
	Error string        `json:"error"`
}

// driveHandler wraps the Companion's own handler with the three test routes.
type driveHandler struct {
	server  *Server
	script  []byte
	config  []byte
	reports chan journeyReport
	logs    chan string
}

func (d *driveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/journey/drive.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(d.script)
		return
	case "/journey/config":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(d.config)
		return
	case "/journey/log":
		var line struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&line)
		select {
		case d.logs <- line.Text:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
		return
	case "/journey/report":
		var report journeyReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			report.Error = "the driver sent an unreadable report: " + err.Error()
		}
		select {
		case d.reports <- report:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		// The real page, with the driver appended. Captured through the real
		// handler so the token substitution, the headers and the CSP are all
		// exactly what a browser normally receives.
		recorder := httptest.NewRecorder()
		d.server.ServeHTTP(recorder, r)
		page := recorder.Body.String()
		page = strings.Replace(page, "</body>",
			`<script src="/journey/drive.js"></script></body>`, 1)
		for name, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(recorder.Code)
		_, _ = w.Write([]byte(page))
		return
	}
	d.server.ServeHTTP(w, r)
}

// TestFirstRunJourneyInABrowser runs the whole journey at two window sizes.
//
// Both are the FULL journey rather than a layout check, because "narrow windows
// remain usable" is a claim about whether somebody can finish the work in one,
// not about whether the boxes line up. A build and a launch completed at 420
// pixels is the evidence; a screenshot is not.
func TestFirstRunJourneyInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	for _, size := range []struct {
		name   string
		window string
		width  int
	}{
		{"a desktop window", "1280,900", 1280},
		{"a narrow window", "420,900", 420},
	} {
		t.Run(size.name, func(t *testing.T) {
			runBrowserJourney(t, browser, size.window, size.width)
		})
	}
}

func runBrowserJourney(t *testing.T, browser, window string, width int) {
	m := newMachine(t)
	// A second pipeline with the same validated provider lets the browser
	// exercise switching between runnable choices rather than disabled ones.
	second := strings.ReplaceAll(string(fixturePipelineJSON()), "aucom.fixture.pipeline", "aucom.fixture.pipeline-second")
	second = strings.ReplaceAll(second, "Fixture build", "Fixture second build")
	m.writeProfile("second-pipeline.json", []byte(second))
	script, err := os.ReadFile("testdata/journey.js")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{
		"email":       m.backend.email,
		"password":    "hunter2",
		"asset_name":  m.backend.asset.name,
		"asset_id":    m.backend.asset.assetID,
		"tool_id":     "aucom.fixture.toolchain",
		"engine_id":   "aucom.fixture.q1-engine",
		"pipeline_id": "aucom.fixture.pipeline",
		"tool_path":   mustExecutable(t),
		"game_root":   m.gameRoot,
		"content":     m.content,
		"viewport":    width,
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := &driveHandler{
		server:  m.server,
		script:  script,
		config:  settings,
		reports: make(chan journeyReport, 1),
		logs:    make(chan string, 256),
	}
	// httptest listens on 127.0.0.1, so the Host and Origin the browser sends
	// are loopback and the guard is satisfied exactly as it is in real use.
	front := httptest.NewServer(handler)
	defer front.Close()

	// Not t.TempDir: its cleanup runs while the browser may still be writing
	// into the profile, and the removal then fails and fails the test for a
	// reason that has nothing to do with the Companion.
	profileDir, err := os.MkdirTemp("", "aucom-browser-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, browser,
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-component-update",
		"--window-size="+window,
		"--user-data-dir="+profileDir,
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
	deadline := time.After(3 * time.Minute)
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
	// Drain whatever arrived alongside the report.
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

	// And the state it left behind is on the server, not in the page: this is
	// the same question a reload asks.
	if err := m.expectDurable(); err != nil {
		t.Error(err)
	}
	// Play this build staged a level where an engine loads it, with the record
	// `engine unstage` reads (NEW_244D).
	for _, name := range []string{filepath.Join("maps", "e1m1.bsp"), engine.StampName} {
		if _, err := os.Stat(filepath.Join(m.gameRoot, "auto-pigeon", name)); err != nil {
			t.Errorf("Play this build left no %s in the game directory: %v", name, err)
		}
	}
}

// expectDurable checks that what the browser did is recorded where a reload
// would find it.
func (m *machine) expectDurable() error {
	status, body := m.call(http.MethodGet, "/api/v1/build/runs", nil)
	if status != http.StatusOK {
		return fmt.Errorf("reading the builds afterwards = %d", status)
	}
	builds, _ := body["items"].([]any)
	if len(builds) == 0 {
		return errors.New("the browser built nothing that survived the page")
	}
	status, body = m.call(http.MethodGet, "/api/v1/library/cached", nil)
	if cached, _ := body["items"].([]any); len(cached) == 0 {
		return errors.New("the browser downloaded nothing that survived the page")
	}
	status, body = m.call(http.MethodGet, "/api/v1/jobs", nil)
	if jobs, _ := body["items"].([]any); len(jobs) == 0 {
		return errors.New("the browser started nothing that survived the page")
	}
	return nil
}
