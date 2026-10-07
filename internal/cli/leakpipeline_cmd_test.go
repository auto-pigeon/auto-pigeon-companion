package cli

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
)

// `companion build leak-pipeline` is the page's chooser for a terminal: it
// shows nothing pinned by default, offers the built-in first, pins only an
// eligible pipeline, keeps other settings, and unpins (NEW_310A, CLI parity).
func TestTheLeakPipelineCanBeChosenFromTheTerminal(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if _, err := updateSettings(env, func(c *config.Config) error {
		c.AUBBaseURL = "http://aub.example.test:9190"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if code := Run(env, []string{"build", "leak-pipeline", "--game", "quake1"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "nothing pinned") || !strings.Contains(out, "auto-pigeon.q1.leak-test") ||
		strings.Index(out, "auto-pigeon.q1.leak-test") > strings.Index(out, "auto-pigeon.q1.normal") {
		t.Fatalf("the choices: %s", out)
	}

	stderr.Reset()
	if code := Run(env, []string{"build", "leak-pipeline", "--game", "quake1", "--pin", "auto-pigeon.q3.leak-test"}); code == 0 ||
		!strings.Contains(stderr.String(), "cannot be the quake1 leak test") {
		t.Fatalf("a Quake III pipeline was pinned for Quake 1: %s", stderr.String())
	}
	stderr.Reset()
	if code := Run(env, []string{"build", "leak-pipeline", "--game", "quake1", "--pin", "local.pipeline.gone"}); code == 0 ||
		!strings.Contains(stderr.String(), "is not installed") {
		t.Fatalf("an uninstalled pipeline was pinned: %s", stderr.String())
	}
	stderr.Reset()
	if code := Run(env, []string{"build", "leak-pipeline", "--game", "quake2"}); code == 0 {
		t.Fatal("a game with no leak test was answered")
	}

	stdout.Reset()
	if code := Run(env, []string{"build", "leak-pipeline", "--game", "quake1", "--pin", "auto-pigeon.q1.leak-test"}); code != 0 {
		t.Fatalf("pinning: %s", stderr.String())
	}
	saved, err := config.LoadFrom(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LeakTestPipelines["quake1"] != "auto-pigeon.q1.leak-test" || saved.AUBBaseURL != "http://aub.example.test:9190" {
		t.Fatalf("saved: pin %v, AUB %q", saved.LeakTestPipelines, saved.AUBBaseURL)
	}
	if code := Run(env, []string{"build", "leak-pipeline", "--game", "quake1", "--unpin"}); code != 0 {
		t.Fatalf("unpinning: %s", stderr.String())
	}
	if saved, _ := config.LoadFrom(env.ConfigPath); saved.LeakTestPipelines["quake1"] != "" {
		t.Fatalf("still pinned: %v", saved.LeakTestPipelines)
	}
}
