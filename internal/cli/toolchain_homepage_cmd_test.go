package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command-line half of the Homepage field: one of your own toolchains gets
// a new patch version with the homepage, and is to be approved again; a
// built-in one is refused with what to do instead.
func TestToolchainHomepageEditsYourOwnAndRefusesABuiltin(t *testing.T) {
	env := acquireEnv(t)
	profiles := filepath.Join(filepath.Dir(env.ConfigPath), "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile("../profile/testdata/community/user-q1-toolchain.tool.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profiles, "mine.json")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	id := "example.andrea.q1-compile"

	code, out, errOut := runCLI(t, env, "toolchain", "homepage", id, "https://example.org/tools")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "homepage https://example.org/tools") || !strings.Contains(out, "approved again") {
		t.Errorf("stdout = %q", out)
	}
	written, _ := os.ReadFile(path)
	if !strings.Contains(string(written), `"homepage":"https://example.org/tools"`) {
		t.Errorf("the document on disk does not carry the homepage:\n%s", written)
	}

	code, _, errOut = runCLI(t, env, "toolchain", "homepage", "auto-pigeon.engine.vkquake", "https://example.org/x")
	if code == 0 || !strings.Contains(errOut, "New profile") {
		t.Errorf("a built-in: exit %d, stderr %q", code, errOut)
	}
}
