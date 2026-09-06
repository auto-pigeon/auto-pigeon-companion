package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLoadFromMissingFileReturnsDefaults(t *testing.T) {
	settings, err := LoadFrom(filepath.Join(t.TempDir(), "absent.json"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if settings.Port != DefaultPort {
		t.Errorf("a missing file did not yield defaults: %+v", settings)
	}
	// Deliberately empty: no AUB address is compiled in — see EnvAUBBaseURL.
	if settings.AUBBaseURL != "" {
		t.Errorf("Default carries a compiled-in AUB address %q", settings.AUBBaseURL)
	}
}

// TestAUBResolution pins the three-way resolution the address rule requires:
// the environment wins, the file is next, and nothing at all is a named error
// rather than a guessed default.
func TestAUBResolution(t *testing.T) {
	if _, err := (Config{}).AUB(); !errors.Is(err, ErrAUBNotConfigured) {
		t.Errorf("an unconfigured address gave %v, want ErrAUBNotConfigured", err)
	}
	fromFile, err := Config{AUBBaseURL: "https://aub.example"}.AUB()
	if err != nil || fromFile != "https://aub.example" {
		t.Errorf("AUB() = %q, %v; want the config file's value", fromFile, err)
	}
	t.Setenv(EnvAUBBaseURL, "https://override.example")
	fromEnv, err := Config{AUBBaseURL: "https://aub.example"}.AUB()
	if err != nil || fromEnv != "https://override.example" {
		t.Errorf("AUB() = %q, %v; want the environment override", fromEnv, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Config{
		AUBBaseURL:   "https://aub.example",
		Port:         9000,
		ToolCacheDir: filepath.Join("/tmp", "tools"),
		GameRoots:    map[string]string{"quake": "/games/quake"},
		Session:      Session{Token: "token", Email: "a@example", Expires: time.Now().Add(time.Hour).Round(time.Second)},
	}

	if err := SaveTo(path, want); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.AUBBaseURL != want.AUBBaseURL || got.Port != want.Port || got.ToolCacheDir != want.ToolCacheDir {
		t.Errorf("round trip lost fields: %+v", got)
	}
	if got.GameRoots["quake"] != "/games/quake" {
		t.Errorf("GameRoots = %v", got.GameRoots)
	}
	if got.Session.Token != "token" || !got.Session.Expires.Equal(want.Session.Expires) {
		t.Errorf("Session = %+v", got.Session)
	}
}

// The file holds a session token, so its mode is part of the contract.
func TestSaveToWritesAPrivateFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveTo(path, Default()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}
}

// A config written by an older build has no "port" key. Loading it must not
// produce port 0.
func TestLoadFillsMissingFieldsFromDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"aub_base_url":"https://aub.example"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", settings.Port, DefaultPort)
	}
}

func TestSessionValid(t *testing.T) {
	cases := map[string]struct {
		session Session
		want    bool
	}{
		"no token":       {Session{}, false},
		"unknown expiry": {Session{Token: "t"}, true},
		"expired":        {Session{Token: "t", Expires: time.Now().Add(-time.Minute)}, false},
		"live":           {Session{Token: "t", Expires: time.Now().Add(time.Hour)}, true},
	}
	for name, testCase := range cases {
		if got := testCase.session.Valid(); got != testCase.want {
			t.Errorf("%s: Valid() = %v, want %v", name, got, testCase.want)
		}
	}
}

func TestToolCacheHonoursTheOverride(t *testing.T) {
	settings := Default()
	settings.ToolCacheDir = "/custom/tools"
	got, err := settings.ToolCache()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/tools" {
		t.Errorf("ToolCache() = %q", got)
	}

	defaulted, err := Default().ToolCache()
	if err != nil {
		t.Fatal(err)
	}
	// The tool cache belongs under the OS cache directory, not the config
	// directory — downloaded tool binaries are re-fetchable content.
	if base, err := os.UserCacheDir(); err == nil && filepath.Dir(filepath.Dir(defaulted)) != base {
		t.Errorf("default tool cache %q is not under %q", defaulted, base)
	}
}

// TestLoadFromMalformedFileIsAnError keeps a broken config from silently
// resetting a user's settings: saying the file is broken is recoverable,
// overwriting it is not.
func TestLoadFromMalformedFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(path); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("LoadFrom on a malformed file returned %v, want a parse error", err)
	}
}
