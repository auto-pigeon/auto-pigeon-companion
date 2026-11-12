package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
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

// Issue #36, the other half: the command the hint names. It finds a built-in
// tool by its id, records only the folders, and refuses what it cannot mean.
func TestAcquireRootsSetsAToolsFoldersAndNothingElse(t *testing.T) {
	env := acquireEnv(t)
	const tool = "auto-pigeon.q3map2.auto-pigeon-build"
	game, project := t.TempDir(), t.TempDir()
	// A native absolute path: "/opt/..." is not absolute on Windows, and a
	// binding refuses a path that is not (TestBindingRequiresAbsolutePaths).
	executable := filepath.Join(t.TempDir(), "managed", "q3map2")

	// Programs somebody (or the Companion) recorded earlier.
	settings, err := loadSettings(env)
	if err != nil {
		t.Fatal(err)
	}
	_, _, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Update(bindingsPath, func(set *binding.Set) error {
		return set.Put(binding.LocalBinding{
			ProfileID: tool, ProfileVersion: "0.0.0", ProfileDigest: "sha256:" + strings.Repeat("0", 64),
			Trust: profile.TrustBuiltin, Acquisition: profile.AcquireManagedOfficial,
			Executables: map[string]string{"q3map2": executable},
			Arguments:   map[string][]string{"compile": {"-v"}},
		})
	}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, env, "acquire", "roots", tool, "--root", "game_root="+game, "--root", "content_root="+project)
	if code != 0 {
		t.Fatalf("acquire roots = %d\n%s%s", code, stdout, stderr)
	}
	set, err := binding.LoadFile(bindingsPath)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := set.Find(tool)
	if local.Roots[profile.RootGame] != game || local.Roots[profile.RootContent] != project {
		t.Errorf("the folders were not recorded: %v", local.Roots)
	}
	if local.Executables["q3map2"] != executable || local.Acquisition != profile.AcquireManagedOfficial ||
		len(local.Arguments["compile"]) != 1 {
		t.Errorf("setting folders changed the programs, the route or the arguments: %+v", local)
	}

	for name, args := range map[string][]string{
		"an engine's id":            {"acquire", "roots", "auto-pigeon.engine.ioquake3", "--root", "game_root=" + game},
		"a folder it does not read": {"acquire", "roots", tool, "--root", "nonsense_root=" + game},
		"a path that is not there":  {"acquire", "roots", tool, "--root", "game_root=" + filepath.Join(game, "missing")},
		"the workspace":             {"acquire", "roots", tool, "--root", "workspace=" + game},
		"no folder at all":          {"acquire", "roots", tool},
	} {
		if code, _, stderr := runCLI(t, env, args...); code == 0 || stderr == "" {
			t.Errorf("%s was accepted (exit %d): %s", name, code, stderr)
		}
	}
	after, _ := binding.LoadFile(bindingsPath)
	if kept, _ := after.Find(tool); kept.Roots[profile.RootGame] != game {
		t.Errorf("a refused command changed the recorded folder: %v", kept.Roots)
	}
}
