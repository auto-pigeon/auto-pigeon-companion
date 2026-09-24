package builtin

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_244D: the executor builds a job's environment from nothing, so an engine
// action that opens a window must say which desktop-session variables it needs.
// None did, and on Linux no engine started as a job could reach the display.
func TestEveryEngineActionThatShowsAWindowInheritsTheDesktopSession(t *testing.T) {
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	engines := 0
	for _, entry := range entries {
		engine, ok := entry.Profile.(*profile.EngineProfile)
		if !ok {
			continue
		}
		engines++
		for _, action := range engine.ActionList() {
			var inherit []string
			if action.Environment != nil {
				inherit = action.Environment.Inherit
			}
			windowed := action.SessionRole == profile.SessionClient || action.SessionRole == profile.SessionListen
			for _, name := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "XAUTHORITY"} {
				if windowed && !slices.Contains(inherit, name) {
					t.Errorf("%s %s (%s) does not inherit %s", entry.File, action.ID, action.SessionRole, name)
				}
				if !windowed && slices.Contains(inherit, name) {
					t.Errorf("%s %s is a dedicated server and inherits %s", entry.File, action.ID, name)
				}
			}
			if strings.HasPrefix(entry.File, "vkquake") && windowed &&
				(action.Environment == nil || action.Environment.Set["APPIMAGE_EXTRACT_AND_RUN"] != "1") {
				t.Errorf("%s %s: vkQuake's Linux release is an AppImage, and a job has no PATH to find fusermount", entry.File, action.ID)
			}
		}
	}
	if engines < 10 {
		t.Fatalf("only %d engine profiles loaded", engines)
	}
}

// XAUTHORITY is allowed by name; anything else that reads like a credential is
// still refused.
func TestOnlyTheDesktopSessionEscapesTheCredentialNameFilter(t *testing.T) {
	raw, err := os.ReadFile("vkquake.engine.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Decode(raw); err != nil {
		t.Fatalf("the vkQuake profile does not validate: %v", err)
	}
	tampered := strings.Replace(string(raw), `"XAUTHORITY"`, `"GITHUB_AUTH_TOKEN"`, 1)
	if _, err := profile.Decode([]byte(tampered)); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Errorf("a profile inheriting GITHUB_AUTH_TOKEN was accepted: %v", err)
	}
}

// vkQuake 1.36.0 aborts in glibc's fortify check as it quits, outside the
// Companion too, so a normal session ends with a non-zero status. The job stays
// failed (the status is the status); the finding says what actually happened.
func TestVkQuakeNamesItsCrashOnQuit(t *testing.T) {
	entry, err := Find("auto-pigeon.engine.vkquake")
	if err != nil {
		t.Fatal(err)
	}
	const line = "*** buffer overflow detected ***: terminated"
	for _, action := range entry.Profile.ActionList() {
		found := false
		for _, rule := range action.Diagnostics {
			if rule.Matches("stderr", line) {
				found = rule.ID == "quit_crash" && rule.Severity == profile.SeverityWarning
			}
		}
		if !found {
			t.Errorf("%s: the quit crash is not named by a quit_crash warning", action.ID)
		}
	}
}
