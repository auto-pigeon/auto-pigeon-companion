package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLoadFromMissingFileReturnsDefaults(t *testing.T) {
	loaded, err := LoadFrom(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if loaded != Default() {
		t.Fatalf("loaded = %+v, want %+v", loaded, Default())
	}
}

func TestLoadFromMalformedFileIsAnError(t *testing.T) {
	// Silently resetting a user's settings would be worse than reporting the
	// broken file, so this must not fall back to defaults.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(path); err == nil {
		t.Fatal("LoadFrom returned no error for a malformed config")
	}
}

func TestSaveToThenLoadFromRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	original := Config{
		AUBBaseURL: "https://aub.example",
		ServerAddr: "127.0.0.1:9099",
		Session: Session{
			Token:      "token-value",
			UserID:     "abc123",
			Email:      "andrea@example",
			ObtainedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		},
	}
	if err := SaveTo(path, original); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if loaded != original {
		t.Fatalf("loaded = %+v, want %+v", loaded, original)
	}
}

func TestSaveToWritesAPrivateFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are advisory on Windows")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveTo(path, Default()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file holds an AUB session token; group- or world-readable is wrong.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %04o, want 0600", mode)
	}
}

func TestLoadFromFillsMissingFieldsWithDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"session":{"token":"t"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if loaded.AUBBaseURL != DefaultAUBBaseURL || loaded.ServerAddr != DefaultServerAddr {
		t.Fatalf("loaded = %+v, want defaults for the absent fields", loaded)
	}
	if !loaded.Session.Valid() {
		t.Fatal("session should be valid when a token is present")
	}
}
