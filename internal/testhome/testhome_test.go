package testhome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rule itself: after Isolate, every per-user directory the standard
// library hands out is inside the isolated home, the Go toolchain's are not,
// and a write to a watched real directory is noticed.
func TestIsolateMovesEveryPerUserDirectory(t *testing.T) {
	real := t.TempDir()
	for name, value := range map[string]string{
		"HOME": real, "USERPROFILE": real, "XDG_CONFIG_HOME": filepath.Join(real, ".config"),
		"XDG_CACHE_HOME": filepath.Join(real, ".cache"), "XDG_DATA_HOME": filepath.Join(real, ".local", "share"),
		"AppData": filepath.Join(real, ".config"), "LocalAppData": filepath.Join(real, ".cache"),
		"GOCACHE": "", "GOPATH": "", "GOMODCACHE": "", "GOENV": "",
	} {
		t.Setenv(name, value)
	}
	// The configuration directory Isolate watches is the one the standard
	// library resolves for this platform from the seeded home: .config on
	// Linux, Library/Application Support on macOS, AppData on Windows. Ask it
	// now, before Isolate moves the environment, instead of assuming .config.
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("the seeded home has no configuration directory: %v", err)
	}
	if rel, err := filepath.Rel(real, config); err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("the configuration directory %q is not inside the seeded home %q", config, real)
	}
	home, err := Isolate()
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()

	for name, get := range map[string]func() (string, error){
		"home": os.UserHomeDir, "config": os.UserConfigDir, "cache": os.UserCacheDir,
	} {
		directory, err := get()
		if err != nil || !home.Contains(directory) {
			t.Errorf("the %s directory is %q (%v), outside the isolated home %s", name, directory, err, home.Root)
		}
	}
	for _, name := range []string{"GOCACHE", "GOPATH", "GOMODCACHE", "GOENV"} {
		if value := os.Getenv(name); value == "" || home.Contains(value) {
			t.Errorf("%s is %q: the toolchain's directories must stay where they were", name, value)
		}
	}
	if touched := home.Touched(); len(touched) != 0 {
		t.Fatalf("nothing was written and Touched says %v", touched)
	}
	// What the two packages used to do: a lock guard in the real profile.
	watched := filepath.Join(config, program)
	guard := filepath.Join(watched, ".leak-test-intent.json.lock.guard")
	if err := os.MkdirAll(filepath.Dir(guard), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guard, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if touched := home.Touched(); len(touched) != 1 || !strings.HasPrefix(touched[0], watched+" (") {
		t.Fatalf("a write to the real configuration directory %s was not noticed: %v", watched, touched)
	}
}
