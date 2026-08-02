package cli

import (
	"bytes"
	"strings"
	"testing"
)

// newTestEnv returns an Env writing to buffers, which is the whole reason
// commands take an Env: none of these tests spawn a subprocess.
func newTestEnv() (*Env, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return &Env{Stdout: &stdout, Stderr: &stderr, Version: "0.0.0-test"}, &stdout, &stderr
}

func TestVersionPrintsTheBuildVersion(t *testing.T) {
	env, stdout, stderr := newTestEnv()
	if code := Run(env, []string{"version"}); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if got := strings.TrimSpace(stdout.String()); got != "0.0.0-test" {
		t.Fatalf("stdout = %q, want %q", got, "0.0.0-test")
	}
}

func TestHelpGoesToStdoutWithCodeZero(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		env, stdout, stderr := newTestEnv()
		if code := Run(env, []string{arg}); code != 0 {
			t.Fatalf("%s: exit code = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout.String(), "usage:") {
			t.Fatalf("%s: stdout has no usage text: %q", arg, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("%s: stderr = %q, want empty", arg, stderr.String())
		}
	}
}

func TestUnknownCommandIsExitCodeTwo(t *testing.T) {
	env, stdout, stderr := newTestEnv()
	if code := Run(env, []string{"nope"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "nope"`) {
		t.Fatalf("stderr = %q, want an unknown-command message", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// The argument-less case is the double-click case, so it must resolve to a
// real command rather than printing usage the way AUE does. Dispatching it for
// real would start a server, so the registry invariant is what is asserted.
func TestDefaultCommandIsRegistered(t *testing.T) {
	command, ok := lookup(defaultCommand)
	if !ok {
		t.Fatalf("default command %q is not registered", defaultCommand)
	}
	if command.Run == nil {
		t.Fatalf("default command %q has no Run func", defaultCommand)
	}
}

func TestUsageListsEveryRegisteredCommand(t *testing.T) {
	usage := UsageText()
	for _, name := range Names() {
		if !strings.Contains(usage, name) {
			t.Fatalf("usage text omits %q:\n%s", name, usage)
		}
	}
}

func TestServeRejectsPositionalArguments(t *testing.T) {
	env, _, stderr := newTestEnv()
	if code := Run(env, []string{"serve", "extra"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "no positional arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestServeRejectsANonLoopbackAddress(t *testing.T) {
	// Binding beyond loopback would publish an authenticated session to the
	// local network; the refusal is a security property, not a detail.
	env, _, stderr := newTestEnv()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if code := Run(env, []string{"serve", "--addr", "0.0.0.0:0", "--no-browser"}); code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr.String(), "loopback") {
		t.Fatalf("stderr = %q, want a loopback complaint", stderr.String())
	}
}

func TestAuthWithoutASubcommandIsExitCodeTwo(t *testing.T) {
	env, _, stderr := newTestEnv()
	if code := Run(env, []string{"auth"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "companion auth login") {
		t.Fatalf("stderr = %q, want the auth usage text", stderr.String())
	}
}

func TestAuthUnknownSubcommandIsExitCodeTwo(t *testing.T) {
	env, _, stderr := newTestEnv()
	if code := Run(env, []string{"auth", "renew"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown auth subcommand "renew"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAuthLoginRequiresAnEmail(t *testing.T) {
	env, _, stderr := newTestEnv()
	if code := Run(env, []string{"auth", "login"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "requires --email") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAuthStatusReportsSignedOutOnAFreshConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	env, stdout, stderr := newTestEnv()
	if code := Run(env, []string{"auth", "status"}); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout.String(), "not signed in") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

// parseInterspersed is the reason `auth login user --email x` and
// `auth login --email x` both work; Go's flag package stops at the first
// positional on its own.
func TestParseInterspersedCollectsPositionalsAroundFlags(t *testing.T) {
	env, _, _ := newTestEnv()
	set := newFlagSet(env, "test")
	flagValue := set.String("email", "", "")
	rest, err := parseInterspersed(set, []string{"one", "--email", "a@b.c", "two"})
	if err != nil {
		t.Fatalf("parseInterspersed: %v", err)
	}
	if *flagValue != "a@b.c" {
		t.Fatalf("--email = %q, want %q", *flagValue, "a@b.c")
	}
	if len(rest) != 2 || rest[0] != "one" || rest[1] != "two" {
		t.Fatalf("positionals = %v, want [one two]", rest)
	}
}
