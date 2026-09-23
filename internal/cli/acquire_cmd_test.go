package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runCLI(t *testing.T, env *Env, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	local := *env
	local.Stdout = &stdout
	local.Stderr = &stderr
	code := Run(&local, args)
	return code, stdout.String(), stderr.String()
}

// acquireEnv is an Env with its own config directory. It is separate from
// cli_test.go's testEnv because these tests drive the command several times and
// want a fresh pair of buffers each time; see [runCLI].
func acquireEnv(t *testing.T) *Env {
	t.Helper()
	return &Env{
		Stdin:      strings.NewReader(""),
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Version:    "test-version",
		Lookenv:    func(string) (string, bool) { return "", false },
	}
}

// The download subcommands are gone, and each says so and what to do instead.
func TestAcquireDownloadCommandsAreGoneAndSaySo(t *testing.T) {
	env := acquireEnv(t)
	for _, command := range []string{"plan", "install", "list", "gc"} {
		code, _, errOut := runCLI(t, env, "acquire", command, "ericw-tools.q1")
		if code != 2 || !strings.Contains(errOut, "downloads no program") {
			t.Errorf("acquire %s: exit %d, stderr %q", command, code, errOut)
		}
	}
}

// `resolve` finds a profile's programs in a folder the user names, and --bind
// records them: the CLI way to set up a tool, with nothing downloaded.
func TestAcquireResolveBindsAFolderTheUserHas(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	env := acquireEnv(t)
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "qbsp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	document := "../profile/testdata/community/user-q1-toolchain.tool.json"

	code, out, errOut := runCLI(t, env, "acquire", "resolve", document, "--user-path", tools, "--bind")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "via user_path") || !strings.Contains(out, filepath.Join(tools, "qbsp")) ||
		!strings.Contains(out, "recorded in bindings.json") {
		t.Errorf("stdout = %q", out)
	}
}
