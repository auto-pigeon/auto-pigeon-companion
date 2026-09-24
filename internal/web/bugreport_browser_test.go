package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/incident"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// TestBugReportDialogInABrowser drives Report a bug in a real browser
// (NEW_247H): a cold report offers the two types and only the Companion's
// areas with nothing chosen and Review refused; the field labels follow the
// type; the review shows the three labels the prefilled link also carries;
// a report about a failed job starts as a bug in the contract's area, and the
// person corrects the area before Review. The driver is testdata/bugreport.js.
//
// Same apparatus as TestFirstRunJourneyInABrowser; skips without a
// Chrome-family browser for the same reason.
func TestBugReportDialogInABrowser(t *testing.T) {
	browser := findBrowser(t)
	if browser == "" {
		t.Skip("no Chrome-family browser on this machine; set AUCOM_TEST_BROWSER to name one")
	}
	reporter := incident.NewReporter(incident.Config{Release: "1.150", Environment: "production"}, incident.Options{})
	exit := 2
	incident.JobHook(reporter, nil)(&job.Job{ID: "job-failed-1", State: job.Failed, ExitCode: &exit}, "")
	reporter.Capture(incident.ReadinessDraft(errors.New("connection refused"), time.Second, 5*time.Second, ""))
	server, err := NewServer(Options{Version: "test", Jobs: newTestJobs(t), Incidents: reporter.Recent})
	if err != nil {
		t.Fatal(err)
	}

	script, err := os.ReadFile("testdata/bugreport.js")
	if err != nil {
		t.Fatal(err)
	}
	handler := &driveHandler{
		server: server, script: script, config: []byte("{}"),
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
		t.Errorf("the bug-report journey stopped: %s", report.Error)
	}
	if len(report.Steps) < 24 {
		t.Errorf("the driver reported %d steps; the bug-report journey has more", len(report.Steps))
	}
}
