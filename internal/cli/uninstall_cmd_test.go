package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/urischeme"
)

// uninstallEnv points every directory at a temporary tree named the way the
// real ones are, and fills each with a file so the listing has something to
// measure.
func uninstallEnv(t *testing.T) (*Env, map[string]string) {
	t.Helper()
	base := t.TempDir()
	dirs := map[string]string{
		"config": filepath.Join(base, "config", config.AppDirName),
		"jobs":   filepath.Join(base, "cache", config.AppDirName, "jobs"),
		"assets": filepath.Join(base, "cache", config.AppDirName, "assets"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "something"), []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	env, _, _ := testEnv(t)
	env.ConfigPath = filepath.Join(dirs["config"], "config.json")
	// Never the real one: the handler lives in the desktop configuration of
	// whichever machine runs this test.
	dataHome := filepath.Join(base, "data")
	env.URIRegistrar = &urischeme.Registrar{
		GOOS: "linux", DataHome: dataHome,
		Run: func(string, ...string) ([]byte, error) { return nil, nil },
	}
	if err := config.SaveTo(env.ConfigPath, config.Config{
		Port:          config.DefaultPort,
		JobsDir:       dirs["jobs"],
		AssetCacheDir: dirs["assets"],
	}); err != nil {
		t.Fatal(err)
	}
	return env, dirs
}

// The default is a listing. "Where does this keep things" is the question most
// people actually have, and answering it must not be the same act as answering
// "delete it".
func TestUninstallShowsWhatItWouldDeleteAndDeletesNothingWithoutConfirmation(t *testing.T) {
	env, dirs := uninstallEnv(t)
	var stdout, stderr strings.Builder
	env.Stdout, env.Stderr = &stdout, &stderr

	if code := Run(env, []string{"uninstall"}); code != 0 {
		t.Fatalf("exit code = %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, dir := range dirs {
		if !strings.Contains(out, dir) {
			t.Errorf("the listing does not mention %s:\n%s", dir, out)
		}
	}
	for _, want := range []string{
		"Nothing has been deleted",
		"--purge --confirm",
		// What is lost is decisions as well as bytes, and a person deciding
		// needs to be told that.
		"bindings and grants",
		"The program itself is never removed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not say %q:\n%s", want, out)
		}
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s was deleted by a listing: %v", dir, err)
		}
	}

	// --purge alone is a bad invocation, not a deletion.
	stdout.Reset()
	stderr.Reset()
	if code := Run(env, []string{"uninstall", "--purge"}); code != 2 {
		t.Fatalf("--purge without --confirm exited %d", code)
	}
	if !strings.Contains(stderr.String(), "--confirm") {
		t.Errorf("the refusal does not name --confirm: %s", stderr.String())
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s was deleted without --confirm: %v", dir, err)
		}
	}
}

// The rule is by name, and deliberately blunt. If somebody pointed
// AUCOM_JOBS_DIR at ~/projects, purging must not delete ~/projects — and the
// honest answer is to say so rather than to guess which directories are safe.
func TestUninstallPurgeRemovesOnlyThisProgramsDirectories(t *testing.T) {
	env, dirs := uninstallEnv(t)

	// A directory the user configured, whose name is their own.
	theirs := filepath.Join(t.TempDir(), "my-quake-projects")
	if err := os.MkdirAll(theirs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(theirs, "e1m1.map"), []byte("their work"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Update(env.ConfigPath, func(current *config.Config) error {
		current.JobsDir = theirs
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	env.Stdout, env.Stderr = &stdout, &stderr
	if code := Run(env, []string{"uninstall", "--purge", "--confirm"}); code != 0 {
		t.Fatalf("exit code = %d: %s", code, stderr.String())
	}
	out := stdout.String()

	if _, err := os.Stat(filepath.Join(theirs, "e1m1.map")); err != nil {
		t.Errorf("a configured directory that is not this program's was deleted: %v", err)
	}
	if !strings.Contains(out, "NOT REMOVED") {
		t.Errorf("the run does not say a directory was left alone:\n%s", out)
	}
	for _, name := range []string{"config", "assets"} {
		if _, err := os.Stat(dirs[name]); !os.IsNotExist(err) {
			t.Errorf("%s survived --purge: %v", dirs[name], err)
		}
	}
	if !strings.Contains(out, "The program itself was not touched") {
		t.Errorf("the run does not say the program was left in place:\n%s", out)
	}
}

// The listing is machine-readable too, because "what would this delete" is a
// question a packaging script asks as well as a person.
func TestUninstallJSONNamesEveryDirectoryAndWhetherItIsThere(t *testing.T) {
	env, dirs := uninstallEnv(t)
	var stdout, stderr strings.Builder
	env.Stdout, env.Stderr = &stdout, &stderr

	if code := Run(env, []string{"uninstall", "--json"}); code != 0 {
		t.Fatalf("exit code = %d: %s", code, stderr.String())
	}
	var listed []struct {
		Label   string `json:"Label"`
		Path    string `json:"Path"`
		Present bool   `json:"Present"`
		Refused string `json:"Refused"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &listed); err != nil {
		t.Fatalf("the listing is not JSON: %v\n%s", err, stdout.String())
	}
	if len(listed) != len(dirs) {
		t.Errorf("listed %d directories, want %d: %+v", len(listed), len(dirs), listed)
	}
	for _, entry := range listed {
		if !entry.Present {
			t.Errorf("%s is there and was reported absent", entry.Path)
		}
		if entry.Refused != "" {
			t.Errorf("%s was refused: %s", entry.Path, entry.Refused)
		}
	}
}

func TestUninstallIsInTheUsageText(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run(env, []string{"--help"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{"uninstall", "uri", "security", "release"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the usage text does not list %q", want)
		}
	}
}

// writeExecutable makes a file the registrar will accept as a program.
func writeExecutable(path string) error {
	return os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)
}
