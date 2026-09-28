package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/joinintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// The editor's leak-test link goes through the ONE registered handler, is
// recorded for the Build area, and starts nothing: no network, no build.
func TestGameOpenRecordsAnEditorLeakTestLinkAndStartsNothing(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	link := "autopigeon://leaktest/map/abc123?revision=4&sha256=" + strings.Repeat("a", 64)
	if code := Run(env, []string{"game", "open", "--no-browser", link}); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	dir := filepath.Dir(env.ConfigPath)
	request, _, err := leakintent.Read(leakintent.Path(dir), time.Now().UTC())
	if err != nil || request == nil || request.AssetID != "abc123" || request.Revision != 4 {
		t.Fatalf("the request was not recorded: %+v (%v)", request, err)
	}
	if _, err := os.Stat(joinintent.Path(dir)); !os.IsNotExist(err) {
		t.Errorf("a leak-test link wrote a join intent: %v", err)
	}
	if !strings.Contains(stdout.String(), "the link is recorded") {
		t.Errorf("unexpected output:\n%s", stdout.String())
	}
}

func TestGameOpenRefusesAMalformedLeakTestLinkBeforeRecordingIt(t *testing.T) {
	env, _, stderr := testEnv(t)
	link := "autopigeon://leaktest/map/abc123?revision=4&sha256=" + strings.Repeat("a", 64) + "&then=https://example.test"
	if code := Run(env, []string{"game", "open", "--no-browser", link}); code == 0 {
		t.Fatal("a link with an extra query key was accepted")
	}
	if !strings.Contains(stderr.String(), "not a leak-test link") {
		t.Errorf("the refusal does not say why:\n%s", stderr.String())
	}
	if _, err := os.Stat(leakintent.Path(filepath.Dir(env.ConfigPath))); !os.IsNotExist(err) {
		t.Errorf("a refused link left an intent file: %v", err)
	}
}
