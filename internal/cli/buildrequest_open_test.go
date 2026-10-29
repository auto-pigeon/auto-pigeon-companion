package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/buildintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/joinintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// The editor's Build button opens a build-request link through the ONE
// registered handler. It is recorded for Build & Run, as a build request and
// never as a leak test, and nothing is asked of anybody: not AUB, not a build.
func TestGameOpenRecordsABuildRequestAndAsksNobody(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	var asked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	settings := config.Default()
	settings.AUBBaseURL = server.URL
	settings.Session = config.Session{Token: "test-token", Email: "a@example", UserID: "u1"}
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("c", 32)
	link := "autopigeon://build/request/" + id + "?intent=build_run"
	if code := Run(env, []string{"game", "open", "--no-browser", link}); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	dir := filepath.Dir(env.ConfigPath)
	request, _, err := buildintent.Read(buildintent.Path(dir), time.Now().UTC())
	if err != nil || request == nil || *request != (aub.BuildRequestLink{RequestID: id, Intent: aub.BuildIntentBuildRun}) {
		t.Fatalf("the request was not recorded: %+v (%v)", request, err)
	}
	// The program the link started names itself, for the acknowledgement of
	// whichever Companion serves it (NEW_368).
	if _, by, err := buildintent.Receipt(buildintent.Path(dir), time.Now().UTC()); err != nil || by != env.Version {
		t.Errorf("the launcher recorded version %q, want %q (%v)", by, env.Version, err)
	}
	for _, other := range []string{leakintent.Path(dir), joinintent.Path(dir)} {
		if _, err := os.Stat(other); !os.IsNotExist(err) {
			t.Errorf("a build-request link wrote %s: %v", filepath.Base(other), err)
		}
	}
	if asked.Load() != 0 {
		t.Errorf("opening the link made %d request(s) to the account server", asked.Load())
	}
	if !strings.Contains(stdout.String(), "the link is recorded") {
		t.Errorf("unexpected output:\n%s", stdout.String())
	}
}

// A leak-test link still lands in leakintent and nowhere else; a refused
// build-request link leaves no intent of either kind.
func TestGameOpenKeepsLeakTestsAndBuildRequestsApart(t *testing.T) {
	env, _, stderr := testEnv(t)
	dir := filepath.Dir(env.ConfigPath)
	leak := "autopigeon://leaktest/map/abc123?revision=4&sha256=" + strings.Repeat("a", 64)
	if code := Run(env, []string{"game", "open", "--no-browser", leak}); code != 0 {
		t.Fatalf("leak link: exit code = %d (stderr: %s)", code, stderr.String())
	}
	if request, _, err := leakintent.Read(leakintent.Path(dir), time.Now().UTC()); err != nil || request == nil {
		t.Fatalf("the leak test was not recorded: %+v (%v)", request, err)
	}
	if _, err := os.Stat(buildintent.Path(dir)); !os.IsNotExist(err) {
		t.Errorf("a leak-test link wrote a build-request intent: %v", err)
	}

	env, _, stderr = testEnv(t)
	dir = filepath.Dir(env.ConfigPath)
	for _, refused := range []string{
		"autopigeon://build/request/" + strings.Repeat("c", 32) + "?intent=build&exe=/bin/sh",
		"autopigeon://build/request/" + strings.Repeat("c", 32) + "?intent=leak_test",
		"autopigeon://user:pw@build/request/" + strings.Repeat("c", 32) + "?intent=build",
	} {
		if code := Run(env, []string{"game", "open", "--no-browser", refused}); code == 0 {
			t.Errorf("accepted %q", refused)
		}
	}
	if !strings.Contains(stderr.String(), "not a build-request link") {
		t.Errorf("the refusal does not say why:\n%s", stderr.String())
	}
	for _, path := range []string{buildintent.Path(dir), leakintent.Path(dir), joinintent.Path(dir)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a refused link left %s: %v", filepath.Base(path), err)
		}
	}
}
