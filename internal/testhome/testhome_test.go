package testhome

import (
	"os"
	"path/filepath"
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
	guard := filepath.Join(real, ".config", program, ".leak-test-intent.json.lock.guard")
	if err := os.MkdirAll(filepath.Dir(guard), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guard, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if touched := home.Touched(); len(touched) != 1 {
		t.Fatalf("a write to the real configuration directory was not noticed: %v", touched)
	}
}
