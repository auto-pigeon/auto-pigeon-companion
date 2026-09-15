package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeEnv map[string]string

func (f fakeEnv) get(key string) string { return f[key] }
func (f fakeEnv) lookup(key string) (string, bool) {
	value, ok := f[key]
	return value, ok
}
func (f fakeEnv) set(key, value string) error { f[key] = value; return nil }

func writeEnvFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dev.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestADevEnvFileSuppliesOnlyTheBackendAddress is the property that makes the
// file safe to read from a working directory: it can say where AUB is, and it
// cannot move a trust anchor, an extractor or the job store.
func TestADevEnvFileSuppliesOnlyTheBackendAddress(t *testing.T) {
	path := writeEnvFile(t, `# a local stack
export AUCOM_AUB_BASE_URL="http://localhost:9190"
AUCOM_CATALOG_ANCHORS=/tmp/evil-anchors.json
AUCOM_AUE_BINARY=/tmp/evil
AUCOM_JOBS_DIR=/tmp/elsewhere
SOMEBODY_ELSES_KEY=1
`)
	env := fakeEnv{EnvFileVariable: path}
	report, err := LoadDevEnvFile(env.get, env.lookup, env.set)
	if err != nil {
		t.Fatal(err)
	}
	if env[EnvAUBBaseURL] != "http://localhost:9190" {
		t.Errorf("AUB address = %q", env[EnvAUBBaseURL])
	}
	for _, key := range []string{"AUCOM_CATALOG_ANCHORS", "AUCOM_AUE_BINARY", "AUCOM_JOBS_DIR"} {
		if _, set := env[key]; set {
			t.Errorf("a .env file set %s", key)
		}
	}
	if !reflect.DeepEqual(report.Applied, []string{EnvAUBBaseURL}) {
		t.Errorf("applied = %v", report.Applied)
	}
	if len(report.Ignored) != 3 {
		t.Errorf("ignored = %v, want the three refused AUCOM keys named and nobody else's", report.Ignored)
	}
}

// TestAnotherProgramsDotEnvIsNotRead: a terminal sitting in the editor's
// checkout must not make the Companion read, or comment on, the editor's keys.
func TestAnotherProgramsDotEnvIsNotRead(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, DefaultEnvFile), []byte("HOST=0.0.0.0\nPORT=3666\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv{}
	report, err := LoadDevEnvFile(env.get, env.lookup, env.set)
	if err != nil || report.Path != "" || len(report.Ignored) != 0 || len(env) != 0 {
		t.Errorf("report = %+v, err = %v, env = %v", report, err, env)
	}
}

func TestTheRealEnvironmentWinsOverADevEnvFile(t *testing.T) {
	path := writeEnvFile(t, "AUCOM_AUB_BASE_URL=http://localhost:9190\n")
	env := fakeEnv{EnvFileVariable: path, EnvAUBBaseURL: "https://aub.example"}
	report, err := LoadDevEnvFile(env.get, env.lookup, env.set)
	if err != nil {
		t.Fatal(err)
	}
	if env[EnvAUBBaseURL] != "https://aub.example" {
		t.Errorf("the file overrode the environment: %q", env[EnvAUBBaseURL])
	}
	if !reflect.DeepEqual(report.Shadowed, []string{EnvAUBBaseURL}) {
		t.Errorf("shadowed = %v", report.Shadowed)
	}
}

// TestNoDevEnvFileChangesNothing is the clean machine: no file, no error, no
// address.
func TestNoDevEnvFileChangesNothing(t *testing.T) {
	t.Chdir(t.TempDir())
	env := fakeEnv{}
	report, err := LoadDevEnvFile(env.get, env.lookup, env.set)
	if err != nil {
		t.Fatal(err)
	}
	if report.Path != "" || len(env) != 0 {
		t.Errorf("report = %+v, env = %v", report, env)
	}
}

func TestANamedDevEnvFileThatIsMissingIsAnError(t *testing.T) {
	env := fakeEnv{EnvFileVariable: filepath.Join(t.TempDir(), "absent.env")}
	if _, err := LoadDevEnvFile(env.get, env.lookup, env.set); err == nil {
		t.Error("a missing file named by AUCOM_ENV_FILE was accepted silently")
	}
}

func TestADevEnvFileIsNotShell(t *testing.T) {
	entries, err := ParseEnvFile([]byte("AUCOM_AUB_BASE_URL=$(touch /tmp/x) # comment\nB='a b'\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []EnvEntry{{"AUCOM_AUB_BASE_URL", "$(touch /tmp/x)"}, {"B", "a b"}}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %#v", entries)
	}
	if _, err := ParseEnvFile([]byte("not an assignment\n")); err == nil {
		t.Error("a line that is not KEY=VALUE was accepted")
	}
}
