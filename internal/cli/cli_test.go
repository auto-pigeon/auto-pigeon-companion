package cli

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/enginefixture"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/web"
)

// The fixture profiles below run this test binary as their external program,
// selected by an argument rather than an environment variable — the executor
// deliberately passes none of its own environment to a job, which is the
// property under test elsewhere and so cannot be relied on here.
//
// See internal/job's helper_test.go, which does the same for the same reason.
const helperFlag = "-aucom-test-helper"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == helperFlag && os.Args[2] == "echo" {
		fmt.Println(strings.Join(os.Args[3:], " "))
		os.Exit(0)
	}
	// The engine fixture, so `companion engine run` can be tested against a
	// program that writes down the command line it was given. One
	// implementation of it, in internal/enginefixture, dispatched to from here.
	if len(os.Args) > 1 && os.Args[1] == enginefixture.Flag {
		os.Exit(enginefixture.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

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
// TestJobRunSupervisesARealProcess is the CLI's half of the executor: the same
// service the GUI drives, reached from a terminal, running a real program.
//
// The program is this test binary, re-executed — see internal/job's fixtures
// for why. Here it is reached through a profile document written to the
// invocation's own profile directory, which is the path a user's own profile
// takes.
func TestJobRunSupervisesARealProcess(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeFixtureProfile(t, env)

	code := Run(env, []string{"job", "run",
		"--profile", "test.cli.echo", "--action", "say",
		"--executable", "helper=" + selfPath(t),
		"--option", "text=hello-from-the-cli"})
	if code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	// The program's output reached the terminal live, and the job's own
	// summary followed it.
	if !strings.Contains(stdout.String(), "hello-from-the-cli") {
		t.Errorf("stdout does not carry the program's output:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "succeeded") {
		t.Errorf("stdout does not report the outcome:\n%s", stdout.String())
	}

	// And the job is in the store, readable by a second invocation.
	listEnv, listOut, _ := testEnv(t)
	listEnv.ConfigPath = env.ConfigPath
	if code := Run(listEnv, []string{"job", "list"}); code != 0 {
		t.Fatalf("job list exit code = %d", code)
	}
	if !strings.Contains(listOut.String(), "succeeded") {
		t.Errorf("job list does not show the job:\n%s", listOut.String())
	}
}

// TestJobPreviewShowsTheArgvAndStartsNothing.
func TestJobPreviewShowsTheArgvAndStartsNothing(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeFixtureProfile(t, env)

	code := Run(env, []string{"job", "preview",
		"--profile", "test.cli.echo", "--action", "say",
		"--executable", "helper=" + selfPath(t),
		"--option", "text=previewed"})
	if code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	for _, want := range []string{"argv:", "previewed", "test.cli.echo", "digest:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the preview is missing %q:\n%s", want, stdout.String())
		}
	}

	listEnv, listOut, _ := testEnv(t)
	listEnv.ConfigPath = env.ConfigPath
	Run(listEnv, []string{"job", "list"})
	if !strings.Contains(listOut.String(), "no jobs") {
		t.Errorf("previewing left a job behind:\n%s", listOut.String())
	}
}

// TestJobRunReportsAFailureWithExitCodeOne: a job that did not succeed is exit
// status 1, which is what a script branches on.
func TestJobRunReportsAFailureWithExitCodeOne(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeFixtureProfile(t, env)

	code := Run(env, []string{"job", "run",
		"--profile", "test.cli.echo", "--action", "say",
		"--executable", "helper=" + filepath.Join(t.TempDir(), "not-installed")})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr.String())
	}
}

func TestJobArgumentErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no subcommand":       {"job"},
		"unknown subcommand":  {"job", "nonsense"},
		"run with no profile": {"job", "run", "--action", "say"},
		"run with no action":  {"job", "run", "--profile", "x"},
		"show with no id":     {"job", "show"},
		"show with two ids":   {"job", "show", "a", "b"},
		"a malformed pair":    {"job", "run", "--profile", "x", "--action", "y", "--option", "no-equals-sign"},
	} {
		t.Run(name, func(t *testing.T) {
			env, stdout, _ := testEnv(t)
			if code := Run(env, args); code == 0 {
				t.Errorf("exit code = 0, want non-zero (stdout: %q)", stdout.String())
			}
		})
	}
}

// TestJobProfilesListsWhatCanBeRun.
func TestJobProfilesListsWhatCanBeRun(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"job", "profiles"}); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	// The built-in samples and the profiles generated from this machine's
	// launch configs, from the one chained catalog.
	for _, want := range []string{"auto-pigeon.ericw-tools.q1", "auto-pigeon.launch.quake"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the catalog is missing %q:\n%s", want, stdout.String())
		}
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

	// The running server publishes its token beside the config file it is
	// using, which is how a `companion job` in another terminal reaches it.
	// Reading it here exercises exactly that path.
	tokenPath := web.TokenPath(filepath.Dir(env.ConfigPath))
	token, err := web.ReadToken(tokenPath)
	if err != nil {
		t.Fatalf("reading the published token: %v", err)
	}

	// Without it, nothing: the token is the check that stops another page.
	unauthenticated, err := http.Get(url + "api/status")
	if err != nil {
		t.Fatalf("GET %sapi/status: %v", url, err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without a token = %d, want 401", unauthenticated.StatusCode)
	}

	authenticated, err := http.NewRequest(http.MethodGet, url+"api/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	authenticated.Header.Set("X-AUCOM-Token", token)
	response, err := http.DefaultClient.Do(authenticated)
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

	// The token file goes with the server: a credential left behind names a
	// server that is not listening.
	if _, err := web.ReadToken(tokenPath); !errors.Is(err, web.ErrNoToken) {
		t.Errorf("the token file survived shutdown: %v", err)
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

// --- fixtures ---------------------------------------------------------------

// selfPath is this test binary, which the fixture profile runs as its external
// program. See internal/job's helper for the whole reasoning.
func selfPath(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	return path
}

// writeFixtureProfile installs a profile beside this invocation's config file,
// and a binding granting it, so a `local` document is allowed to run.
//
// Both halves are needed and that is the point: a document alone is inert until
// somebody has approved what it asks for.
func writeFixtureProfile(t *testing.T, env *Env) {
	t.Helper()
	dir := filepath.Dir(env.ConfigPath)
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	document := []byte(`{
	  "schema_version": "aucom.profile/1.0",
	  "kind": "tool",
	  "id": "test.cli.echo",
	  "version": "1.0.0",
	  "name": "CLI test tool",
	  "summary": "A fixture profile that runs the test binary as an external program.",
	  "publisher": { "name": "Auto-Pigeon tests" },
	  "license": { "spdx": "MIT", "name": "MIT" },
	  "tool_version": "0.0.0-fixture",
	  "platforms": [{ "platform": { "os": "` + runtime.GOOS + `", "arch": "` + runtime.GOARCH + `" }, "status": "supported" }],
	  "acquisition": [{ "mode": "user_path", "title": "Point at a copy you already have", "hint": "choose the test binary" }],
	  "executables": [{ "name": "helper", "title": "The fixture program", "file": "helper{platform.exe_suffix}" }],
	  "actions": [{
	    "id": "say",
	    "title": "Say something",
	    "executable": "helper",
	    "args": ["-aucom-test-helper", "echo", "{option.text}"],
	    "options": [{ "name": "text", "title": "Text", "type": "text", "default": "hello", "max_length": 64 }],
	    "roots": [{ "role": "workspace", "access": "read_write", "purpose": "scratch space" }],
	    "timeout_seconds": 60
	  }]
	}`)
	path := filepath.Join(profiles, "fixture.tool.json")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}

	decoded, err := profile.Decode(document)
	if err != nil {
		t.Fatalf("the fixture profile is invalid: %v", err)
	}
	digest, err := profile.Digest(decoded)
	if err != nil {
		t.Fatal(err)
	}
	set := binding.NewSet()
	if err := set.Put(binding.LocalBinding{
		ProfileID:     "test.cli.echo",
		ProfileDigest: digest,
		Trust:         profile.TrustLocal,
		Grant: &profile.Grant{
			ProfileID: "test.cli.echo",
			Version:   "1.0.0",
			Digest:    digest,
			Trust:     profile.TrustLocal,
			Granted:   profile.PermissionIDs(decoded),
			GrantedAt: time.Now().UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := binding.SaveFile(filepath.Join(dir, "bindings.json"), set); err != nil {
		t.Fatal(err)
	}
}
