package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Somebody whose home directory is `C:\Users\Zoë`, or `/home/日本語`, is not an
// edge case. A program that mangles their path is one that does not work for
// them at all, and JSON, the filesystem and the atomic write are three places it
// could happen.
func TestANonASCIIPathRoundTripsThroughTheConfigFile(t *testing.T) {
	base := t.TempDir()
	names := []string{"Zoë", "日本語", "Ω-project", "café ☕"}

	dir := filepath.Join(append([]string{base}, names...)...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Skipf("this filesystem does not take these names: %v", err)
	}
	path := filepath.Join(dir, "config.json")

	written, err := Update(path, func(current *Config) error {
		current.JobsDir = filepath.Join(dir, "jobs")
		current.GameRoots = map[string]string{"quake": filepath.Join(dir, "Quake II — Ω")}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.JobsDir != written.JobsDir {
		t.Errorf("a path did not survive the round trip:\n%+v\n%+v", loaded, written)
	}
	if loaded.GameRoots["quake"] != written.GameRoots["quake"] {
		t.Errorf("a game root did not survive: %q", loaded.GameRoots["quake"])
	}

	// The file itself has to hold the characters, not an escaped or mangled
	// approximation that some other reader would see differently.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.Contains(string(raw), name) {
			t.Errorf("config.json does not contain %q:\n%s", name, raw)
		}
	}
}

// A deeply nested path is a real Windows limit and a real annoyance elsewhere.
// What must not happen is a write that half-succeeds: either the file is there
// with everything in it, or the error names the path.
func TestADeeplyNestedConfigPathEitherWorksOrSaysWhy(t *testing.T) {
	base := t.TempDir()
	deep := base
	for i := 0; i < 24; i++ {
		deep = filepath.Join(deep, "a-directory-with-a-fairly-long-name")
	}
	path := filepath.Join(deep, "config.json")

	if _, err := Update(path, func(current *Config) error {
		current.Port = 4242
		return nil
	}); err != nil {
		if !strings.Contains(err.Error(), base) {
			t.Errorf("the error does not name the path it failed on: %v", err)
		}
		t.Skipf("this platform will not take the path: %v", err)
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("the file was written and cannot be read back: %v", err)
	}
	if loaded.Port != 4242 {
		t.Errorf("read back %+v", loaded)
	}
}
