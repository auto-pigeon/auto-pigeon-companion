package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetPolicy puts the process-wide rule back after a test.
func resetPolicy(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		releasePolicy.Lock()
		releasePolicy.officialOnly, releasePolicy.ignored = false, nil
		releasePolicy.Unlock()
	})
}

// The Windows report (2026-09-25): a released Companion opened on a LAN address
// left in the computer's environment by an earlier development session. In a
// release started normally that variable is set aside and said so.
func TestAReleaseIgnoresTheEnvironmentsServerAddress(t *testing.T) {
	resetPolicy(t)
	t.Setenv(EnvAUBBaseURL, "http://192.168.0.33:9190")
	on, err := ApplyReleasePolicy(true, false, os.LookupEnv, os.Unsetenv)
	if err != nil || !on {
		t.Fatalf("on = %v, err = %v", on, err)
	}
	if _, set := os.LookupEnv(EnvAUBBaseURL); set {
		t.Fatal("the environment variable is still set")
	}
	if _, err := (Config{}).AUB(); !errors.Is(err, ErrAUBNotConfigured) {
		t.Errorf("AUB() = %v, want ErrAUBNotConfigured so the page offers the official servers", err)
	}
	ignored := IgnoredAddresses()
	if len(ignored) != 1 || !strings.Contains(ignored[0], "192.168.0.33:9190") ||
		!strings.Contains(ignored[0], EnvAUBBaseURL) {
		t.Errorf("ignored = %v", ignored)
	}
}

// A development build, and any build started with --debug, keep the variable:
// that is how local stacks and auto-pigeon-tools' harnesses point it.
func TestADevelopmentBuildOrDebugKeepsTheEnvironment(t *testing.T) {
	for _, c := range []struct {
		name           string
		release, debug bool
	}{{"development build", false, false}, {"release with --debug", true, true}} {
		t.Run(c.name, func(t *testing.T) {
			resetPolicy(t)
			t.Setenv(EnvAUBBaseURL, "http://192.168.0.33:9190")
			if on, _ := ApplyReleasePolicy(c.release, c.debug, os.LookupEnv, os.Unsetenv); on {
				t.Fatal("the rule is on")
			}
			if got, err := (Config{}).AUB(); err != nil || got != "http://192.168.0.33:9190" {
				t.Errorf("AUB() = %q, %v", got, err)
			}
			if saved := (Config{AUBBaseURL: "http://localhost:9190"}); saved.SavedAUB() != "http://localhost:9190" {
				t.Error("a saved development address was set aside outside the release rule")
			}
		})
	}
}

// The config.json beside the executable is the one place a release takes
// another address from, and the rule runs before it, so nothing shadows it.
func TestTheFileBesideTheProgramStillSetsTheAddressInARelease(t *testing.T) {
	resetPolicy(t)
	t.Setenv(EnvAUBBaseURL, "http://192.168.0.33:9190")
	if _, err := ApplyReleasePolicy(true, false, os.LookupEnv, os.Unsetenv); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, OverrideFileName), []byte(`{"aub_base_url":"http://10.0.0.5:9190"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := LoadExecutableOverride(filepath.Join(dir, "companion"), os.LookupEnv, os.Setenv)
	if err != nil || len(report.Applied) != 1 {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	if got, err := (Config{}).AUB(); err != nil || got != "http://10.0.0.5:9190" {
		t.Errorf("AUB() = %q, %v, want the address the file beside the program names", got, err)
	}
}

// A saved address that is not an official deployment — left by a --debug
// session or an older version — is set aside in a release; an official one is
// what a person chose, and is kept.
func TestAReleaseSetsASavedDevelopmentAddressAside(t *testing.T) {
	resetPolicy(t)
	t.Setenv(EnvAUBBaseURL, "")
	os.Unsetenv(EnvAUBBaseURL)
	SetOfficialOnly(true)
	saved := Config{AUBBaseURL: "http://192.168.0.33:9190"}
	if _, err := saved.AUB(); !errors.Is(err, ErrAUBNotConfigured) {
		t.Errorf("AUB() = %v, want ErrAUBNotConfigured", err)
	}
	if saved.SavedAUB() != "" {
		t.Errorf("SavedAUB() = %q, want it set aside", saved.SavedAUB())
	}
	if ignored := IgnoredAddresses(); len(ignored) != 1 || !strings.Contains(ignored[0], "config.json") {
		t.Errorf("ignored = %v", ignored)
	}
	official := Config{AUBBaseURL: "https://beta.auto-pigeon.com"}
	if got, err := official.AUB(); err != nil || got != "https://beta.auto-pigeon.com" {
		t.Errorf("an official choice gave %q, %v", got, err)
	}
}

func TestWantsDebug(t *testing.T) {
	for args, want := range map[string]bool{
		"":                        false,
		"--debug":                 true,
		"serve --debug":           true,
		"serve --port 0":          false,
		"--stay-running":          false,
		"job run -- --debug":      false,
		"serve --debug=false":     false,
		"serve --debug=true":      true,
		"game join --approve x y": false,
	} {
		if got := WantsDebug(strings.Fields(args)); got != want {
			t.Errorf("WantsDebug(%q) = %v, want %v", args, got, want)
		}
	}
}
