package cli

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
)

// Who sets build tools up: the application, however it is started; a
// `serve` a script starts only when it asks; nobody when the environment,
// Settings or offline mode says no — and those last two still show what is
// installed (on, with a reason).
func TestWhoSetsBuildToolsUp(t *testing.T) {
	t.Setenv(EnvManagedTools, "")
	t.Setenv(config.EnvOffline, "")
	for _, c := range []struct {
		flag        string
		interactive bool
		settings    config.Config
		on          bool
		reason      string
	}{
		{"auto", true, config.Config{}, true, ""},
		{"auto", false, config.Config{}, false, "server mode"},
		{"on", false, config.Config{}, true, ""},
		{"off", true, config.Config{}, false, "--managed-tools off"},
		{"on", true, config.Config{ManagedToolsOff: true}, true, "turned off in Settings"},
	} {
		on, why, err := managedToolsMode(c.flag, c.interactive, c.settings)
		if err != nil || on != c.on || !strings.Contains(why, c.reason) {
			t.Errorf("%s interactive=%v: on=%v why=%q err=%v", c.flag, c.interactive, on, why, err)
		}
	}
	if _, _, err := managedToolsMode("sometimes", true, config.Config{}); err == nil {
		t.Error("an unknown --managed-tools value was accepted")
	}
	t.Setenv(config.EnvOffline, "1")
	if on, why, _ := managedToolsMode("on", true, config.Config{}); !on || !strings.Contains(why, config.EnvOffline) {
		t.Errorf("offline: on=%v why=%q", on, why)
	}
	t.Setenv(EnvManagedTools, "off")
	if on, _, _ := managedToolsMode("on", true, config.Config{}); on {
		t.Error("AUCOM_MANAGED_TOOLS=off did not turn it off")
	}
}

// A release build has no fixture seam: the environment variable that a
// fixture build reads changes nothing here.
func TestAReleaseBuildHasNoFixtureReleaseSource(t *testing.T) {
	t.Setenv(fixtureReleasesEnv, "http://127.0.0.1:1")
	if source, ok, err := fixtureReleaseSource(); source != nil || ok || err != nil {
		t.Fatalf("a release build honoured %s: %v %v %v", fixtureReleasesEnv, source, ok, err)
	}
}
