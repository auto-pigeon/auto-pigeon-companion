package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/urischeme"
)

// uriEnv points the registrar at a temporary data directory. Never the real
// one: `companion uri register` changes which application opens a scheme on the
// machine it runs on, and a test that did that to a developer's desktop would
// be a test nobody could run twice.
func uriEnv(t *testing.T) (*Env, *strings.Builder, *strings.Builder, string) {
	t.Helper()
	base := t.TempDir()
	binary := filepath.Join(base, "companion")
	if err := writeExecutable(binary); err != nil {
		t.Fatal(err)
	}
	dataHome := filepath.Join(base, "data")

	env, _, _ := testEnv(t)
	var stdout, stderr strings.Builder
	env.Stdout, env.Stderr = &stdout, &stderr
	env.URIRegistrar = &urischeme.Registrar{
		GOOS: "linux", Executable: binary, DataHome: dataHome,
		Run: func(string, ...string) ([]byte, error) { return nil, nil },
	}
	return env, &stdout, &stderr, dataHome
}

func TestURIStatusRegisterAndUnregisterThroughTheCLI(t *testing.T) {
	env, stdout, stderr, dataHome := uriEnv(t)

	if code := Run(env, []string{"uri", "status"}); code != 0 {
		t.Fatalf("status exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "registered: no") {
		t.Errorf("a fresh machine reported:\n%s", stdout.String())
	}

	stdout.Reset()
	if code := Run(env, []string{"uri", "register"}); code != 0 {
		t.Fatalf("register exited %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "registered: yes") {
		t.Errorf("register reported:\n%s", out)
	}
	// The command a link runs is the one a person can read, and it has to be
	// the one that starts nothing.
	if !strings.Contains(out, "game join %u") {
		t.Errorf("the command is not shown, or is not `game join`:\n%s", out)
	}
	if strings.Contains(out, "--approve") {
		t.Errorf("the registered command approves something:\n%s", out)
	}
	if _, err := filepath.Glob(filepath.Join(dataHome, "applications", "*.desktop")); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if code := Run(env, []string{"uri", "unregister"}); code != 0 {
		t.Fatalf("unregister exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "registered: no") {
		t.Errorf("unregister reported:\n%s", stdout.String())
	}
}

// macOS declares the handler in the .app bundle, and this program does not
// perform that. It has to say so and exit 0 — a refusal dressed as a failure
// would send somebody looking for a bug.
func TestURIRegisterOnMacOSExplainsItselfRatherThanFailing(t *testing.T) {
	env, stdout, stderr, _ := uriEnv(t)
	env.URIRegistrar.GOOS = "darwin"

	if code := Run(env, []string{"uri", "register"}); code != 0 {
		t.Fatalf("register exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Info.plist") {
		t.Errorf("the explanation does not name where the declaration lives:\n%s", stdout.String())
	}
}

func TestURIWithNoSubcommandIsABadInvocation(t *testing.T) {
	env, _, stderr, _ := uriEnv(t)
	if code := Run(env, []string{"uri"}); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{"status", "register", "unregister"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the usage text does not list %q", want)
		}
	}
}
