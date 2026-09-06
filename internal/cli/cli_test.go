package cli

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// testEnv is an Env pointed entirely at temporary state: buffers instead of the
// process streams, and a config file in a temp directory instead of the
// developer's own.
func testEnv(t *testing.T) (*Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := &Env{
		Stdout:     &stdout,
		Stderr:     &stderr,
		Version:    "test-version",
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Lookenv:    func(string) (string, bool) { return "", false },
	}
	return env, &stdout, &stderr
}

func TestVersion(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run(env, []string{"version"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "test-version" {
		t.Errorf("stdout = %q", got)
	}
}

func TestHelpGoesToStdoutWithCodeZero(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"--help"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q; an explicit help request is not an error", stderr.String())
	}
	// Every registered command must appear, which is what stops a new
	// subcommand from being added without documentation.
	for _, name := range Names() {
		if !strings.Contains(stdout.String(), name) {
			t.Errorf("usage text is missing %q:\n%s", name, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "start the local GUI") {
		t.Error("usage text does not document the no-subcommand GUI mode")
	}
}

func TestUnknownCommandIsExitTwo(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"nonsense"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q; an error's usage text belongs on stderr", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "nonsense"`) {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestBadFlagIsExitTwo(t *testing.T) {
	env, _, _ := testEnv(t)
	if code := Run(env, []string{"build", "--no-such-flag"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

// The pipeline end to end through the CLI, with the fake tool.
func TestBuildRunsTheFakeTool(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	settings := config.Default()
	settings.ToolCacheDir = t.TempDir()
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatal(err)
	}

	if code := Run(env, []string{"build", "--", "--example"}); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	for _, want := range []string{"resolved noop", "verified ", "args: --example", "done"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout.String())
		}
	}
}

// The registry is empty until real tools are chosen, so any other tool name is
// an honest failure rather than a silent no-op.
func TestBuildWithAnUnregisteredToolFails(t *testing.T) {
	env, _, stderr := testEnv(t)
	settings := config.Default()
	settings.ToolCacheDir = t.TempDir()
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatal(err)
	}

	if code := Run(env, []string{"build", "--tool", "qbsp"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown tool") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestLaunchDryRun(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	code := Run(env, []string{"launch", "quake", "--map", "e1m1", "--game-root", "/games/quake", "--dry-run"})
	if code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	line := strings.TrimSpace(stdout.String())
	if !strings.Contains(line, "quakespasm") || !strings.Contains(line, "e1m1") {
		t.Errorf("stdout = %q", line)
	}
	if runtime.GOOS == "windows" && !strings.Contains(line, ".exe") {
		t.Errorf("stdout = %q, want a .exe on Windows", line)
	}
}

func TestLaunchUsesTheConfiguredGameRoot(t *testing.T) {
	env, stdout, _ := testEnv(t)
	settings := config.Default()
	settings.GameRoots = map[string]string{"quake": filepath.FromSlash("/opt/quake")}
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatal(err)
	}

	if code := Run(env, []string{"launch", "quake", "--map", "e1m1", "--dry-run"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), filepath.FromSlash("/opt/quake")) {
		t.Errorf("stdout = %q, want the configured game root", stdout.String())
	}
}

func TestLaunchArgumentErrors(t *testing.T) {
	cases := map[string][]string{
		"no game":        {"launch"},
		"two games":      {"launch", "quake", "quake2"},
		"unknown game":   {"launch", "doom", "--dry-run"},
		"missing map":    {"launch", "quake", "--game-root", "/games/quake", "--dry-run"},
		"no game root":   {"launch", "quake", "--map", "e1m1", "--dry-run"},
		"bad auth verb":  {"auth", "nonsense"},
		"auth with none": {"auth"},
	}
	for name, args := range cases {
		env, stdout, _ := testEnv(t)
		if code := Run(env, args); code == 0 {
			t.Errorf("%s: exit code = 0, want non-zero (stdout: %q)", name, stdout.String())
		}
	}
}

func TestAuthStatusAndLogout(t *testing.T) {
	env, stdout, _ := testEnv(t)
	settings := config.Default()
	settings.Session = config.Session{Token: "token-1", Email: "a@example", Expires: time.Now().Add(time.Hour)}
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatal(err)
	}

	if code := Run(env, []string{"auth", "status"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "signed in: yes (a@example)") {
		t.Errorf("stdout = %q", stdout.String())
	}

	stdout.Reset()
	if code := Run(env, []string{"auth", "logout"}); code != 0 {
		t.Fatalf("logout exit code = %d", code)
	}
	// The message must not claim more than logout does: AUB's tokens stay valid.
	if !strings.Contains(stdout.String(), "locally") {
		t.Errorf("logout message overclaims: %q", stdout.String())
	}
	after, err := config.LoadFrom(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Session.Token != "" {
		t.Errorf("the session survived logout: %+v", after.Session)
	}
}

func TestAuthLoginRequiresAnEmail(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"auth", "login"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--email") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestAuthLoginTakesThePasswordFromTheEnvironment(t *testing.T) {
	env, _, stderr := testEnv(t)
	env.Lookenv = func(name string) (string, bool) {
		if name == PasswordEnv {
			return "correct", true
		}
		return "", false
	}
	// Points at a port nothing is listening on, so the login fails at the
	// network rather than at argument handling — which is what this asserts:
	// the password was accepted from the environment and a request was made.
	settings := config.Default()
	settings.AUBBaseURL = "http://127.0.0.1:1"
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatal(err)
	}

	if code := Run(env, []string{"auth", "login", "--email", "a@example"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), "no password") {
		t.Errorf("the environment password was not used: %q", stderr.String())
	}
}

func TestAuthLoginReadsAPipedPassword(t *testing.T) {
	env, _, stderr := testEnv(t)
	env.Stdin = strings.NewReader("correct")
	settings := config.Default()
	settings.AUBBaseURL = "http://127.0.0.1:1"
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatal(err)
	}

	if code := Run(env, []string{"auth", "login", "--email", "a@example"}); code != 1 {
		t.Fatalf("exit code = %d, want 1 (a connection failure)", code)
	}
	if strings.Contains(stderr.String(), "password is empty") {
		t.Errorf("a password with no trailing newline was not read: %q", stderr.String())
	}
}

// GUI mode: no subcommand starts the server and opens the browser. The browser
// opener is replaced, and the server is stopped by signalling this process,
// which is the same path Ctrl-C takes.
func TestNoSubcommandStartsTheServerAndOpensTheBrowser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGTERM to send on Windows")
	}

	env, stdout, stderr := testEnv(t)
	opened := make(chan string, 1)
	env.OpenBrowser = func(url string) error {
		opened <- url
		return nil
	}

	done := make(chan int, 1)
	go func() { done <- Run(env, nil) }()

	var url string
	select {
	case url = <-opened:
	case <-time.After(10 * time.Second):
		t.Fatalf("the browser was never opened (stdout: %s, stderr: %s)", stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("opened %q, want a loopback URL", url)
	}

	response, err := http.Get(url + "api/status")
	if err != nil {
		t.Fatalf("GET %sapi/status: %v", url, err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d", response.StatusCode)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d (stderr: %s)", code, stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the server did not stop on SIGTERM")
	}
}

// TestUsageListsEveryRegisteredCommand is what stops a new subcommand from
// being invisible: UsageText is generated from the registry, and this asserts
// the generation rather than a hand-copied list.
func TestUsageListsEveryRegisteredCommand(t *testing.T) {
	usage := UsageText()
	for _, name := range Names() {
		if !strings.Contains(usage, name) {
			t.Errorf("the usage text does not mention %q", name)
		}
	}
}

func TestExtractorArgumentErrors(t *testing.T) {
	for _, args := range [][]string{
		{"extractor"},
		{"extractor", "nonsense"},
	} {
		env, _, stderr := testEnv(t)
		if code := Run(env, args); code != 2 {
			t.Errorf("Run(%v) = %d, want 2 (%s)", args, code, stderr)
		}
	}
}
