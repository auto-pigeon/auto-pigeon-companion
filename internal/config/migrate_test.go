package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Fixtures, written exactly as the two retired bootstraps wrote them. They are
// literals rather than values built from the current types on purpose: a test
// that constructs its input with today's struct cannot notice that today's
// struct stopped being able to read yesterday's file.

// launcherFixture is a config.json as auto-pigeon-launcher wrote it.
const launcherFixture = `{
  "aub_base_url": "https://aub.launcher.example",
  "port": 8789,
  "tool_cache_dir": "/cache/launcher-tools",
  "game_roots": {
    "quake": "/games/quake",
    "quake2": "/games/quake2"
  },
  "session": {
    "token": "launcher-token",
    "user_id": "u_launcher",
    "email": "launcher@example",
    "expires": "2030-01-01T00:00:00Z"
  }
}`

// companionFixture is a config.json as the first Companion bootstrap wrote it:
// a whole listen address instead of a port, and a session timestamped with when
// it was obtained rather than when it expires.
const companionFixture = `{
  "aub_base_url": "https://aub.companion.example",
  "server_addr": "127.0.0.1:8777",
  "session": {
    "token": "companion-token",
    "user_id": "u_companion",
    "email": "companion@example",
    "obtained_at": "2026-08-02T10:00:00Z"
  }
}`

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func paths(t *testing.T) (companion, launcher string) {
	t.Helper()
	root := t.TempDir()
	return filepath.Join(root, "auto-pigeon-companion", "config.json"),
		filepath.Join(root, "auto-pigeon-launcher", "config.json")
}

func TestMigrateWithNothingToDoWritesNothing(t *testing.T) {
	companion, launcher := paths(t)
	report, err := MigrateFiles(companion, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if report.Performed {
		t.Error("a machine with no config files reported a migration")
	}
	if _, err := os.Stat(companion); !os.IsNotExist(err) {
		t.Error("migration created a config file where there was none to migrate")
	}
}

// TestMigrateLauncherOnly is the upgrade path for a user who ran the Launcher
// and never the Companion.
func TestMigrateLauncherOnly(t *testing.T) {
	companion, launcher := paths(t)
	write(t, launcher, launcherFixture)

	report, err := MigrateFiles(companion, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Performed {
		t.Fatal("a Launcher config was not migrated")
	}
	if len(report.Backups) != 0 {
		t.Errorf("backed up %v; there was no Companion file to overwrite", report.Backups)
	}

	got, err := LoadFrom(companion)
	if err != nil {
		t.Fatal(err)
	}
	if got.AUBBaseURL != "https://aub.launcher.example" || got.Port != 8789 {
		t.Errorf("settings not carried over: %+v", got)
	}
	if got.Session.Token != "launcher-token" || got.Session.Email != "launcher@example" {
		t.Errorf("session not carried over: %+v", got.Session)
	}
	if got.GameRoots["quake"] != "/games/quake" || got.GameRoots["quake2"] != "/games/quake2" {
		t.Errorf("game roots not carried over: %v", got.GameRoots)
	}

	// The Launcher's own file is another installed program's configuration and
	// is never touched.
	if _, err := os.Stat(launcher); err != nil {
		t.Errorf("the Launcher config was disturbed: %v", err)
	}
}

// TestMigrateLegacyCompanionOnly is the upgrade path for a user who ran the
// first Companion bootstrap: server_addr becomes port, and the session survives
// even though it carries no expiry.
func TestMigrateLegacyCompanionOnly(t *testing.T) {
	companion, launcher := paths(t)
	write(t, companion, companionFixture)

	report, err := MigrateFiles(companion, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Performed {
		t.Fatal("a legacy Companion config was not migrated")
	}
	if len(report.Backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", report.Backups)
	}
	backup, err := os.ReadFile(report.Backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != companionFixture {
		t.Error("the backup is not the pre-migration file")
	}

	got, err := LoadFrom(companion)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 8777 {
		t.Errorf("Port = %d, want the port from server_addr", got.Port)
	}
	if got.Session.Token != "companion-token" {
		t.Errorf("the session token was lost: %+v", got.Session)
	}
	// No expiry is knowable from obtained_at, and inventing one would sign the
	// user out on a guess. Zero means "unknown", which Valid accepts.
	if !got.Session.Expires.IsZero() || !got.Session.Valid() {
		t.Errorf("session expiry was invented: %+v", got.Session)
	}
	if got.MigratedFromLauncher {
		t.Error("marked as migrated from the Launcher with no Launcher file present")
	}
}

// TestMigrateResolvesConflictsExplicitly covers the case both fixtures exist:
// the Companion's values win, every discarded value is reported, and game roots
// merge per game rather than whole-map.
func TestMigrateResolvesConflictsExplicitly(t *testing.T) {
	companion, launcher := paths(t)
	write(t, companion, companionFixture)
	write(t, launcher, launcherFixture)

	report, err := MigrateFiles(companion, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Performed {
		t.Fatal("nothing was migrated")
	}

	fields := map[string]Conflict{}
	for _, conflict := range report.Conflicts {
		fields[conflict.Field] = conflict
	}
	for _, want := range []string{"aub_base_url", "port", "session"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("conflict on %s was not reported; got %v", want, report.Conflicts)
		}
	}
	// A conflict report must never print the token itself.
	if got := fields["session"]; got.Kept == "companion-token" || got.Discarded == "launcher-token" {
		t.Errorf("the session conflict printed raw tokens: %+v", got)
	}

	got, err := LoadFrom(companion)
	if err != nil {
		t.Fatal(err)
	}
	if got.AUBBaseURL != "https://aub.companion.example" {
		t.Errorf("AUBBaseURL = %q, want the Companion's value", got.AUBBaseURL)
	}
	if got.Port != 8777 {
		t.Errorf("Port = %d, want the Companion's value", got.Port)
	}
	if got.Session.Token != "companion-token" {
		t.Errorf("Session = %+v, want the Companion's", got.Session)
	}
	// Fields only the Launcher set are carried over rather than dropped.
	if got.GameRoots["quake"] != "/games/quake" || len(got.GameRoots) != 2 {
		t.Errorf("GameRoots = %v, want both games from the Launcher", got.GameRoots)
	}
	if !got.MigratedFromLauncher {
		t.Error("the Launcher import was not marked, so a second run would repeat it")
	}
}

// TestMigrateIsIdempotent is the acceptance criterion stated directly: running
// it again changes nothing, byte for byte, and reports that it did nothing.
func TestMigrateIsIdempotent(t *testing.T) {
	companion, launcher := paths(t)
	write(t, companion, companionFixture)
	write(t, launcher, launcherFixture)

	if _, err := MigrateFiles(companion, launcher); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(companion)
	if err != nil {
		t.Fatal(err)
	}

	for run := 2; run <= 3; run++ {
		report, err := MigrateFiles(companion, launcher)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if report.Performed {
			t.Errorf("run %d reported a migration; nothing was left to do", run)
		}
		again, err := os.ReadFile(companion)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d changed the config file", run)
		}
	}
}

// TestMigrateIsDeterministic runs the same inputs twice from scratch and
// requires identical output, which is what lets a support answer about one
// machine apply to another.
func TestMigrateIsDeterministic(t *testing.T) {
	var results []string
	for run := 0; run < 2; run++ {
		companion, launcher := paths(t)
		write(t, companion, companionFixture)
		write(t, launcher, launcherFixture)
		if _, err := MigrateFiles(companion, launcher); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(companion)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, string(raw))
	}
	if results[0] != results[1] {
		t.Errorf("two runs on the same input produced different files:\n%s\n---\n%s", results[0], results[1])
	}
}

// TestMigrateRefusesAMalformedFile keeps a broken config from being replaced by
// a migration, which would destroy exactly the thing a user needs recovered.
func TestMigrateRefusesAMalformedFile(t *testing.T) {
	companion, launcher := paths(t)
	write(t, companion, "{not json")
	write(t, launcher, launcherFixture)

	if _, err := MigrateFiles(companion, launcher); err == nil {
		t.Fatal("a malformed Companion config was migrated over")
	}
	raw, err := os.ReadFile(companion)
	if err != nil || string(raw) != "{not json" {
		t.Errorf("the malformed file was modified: %q, %v", raw, err)
	}
}

// TestMigratedFileIsCurrentSchema checks the written document by its JSON keys,
// not through the Go types, so a migration that leaves a legacy key behind is
// caught.
func TestMigratedFileIsCurrentSchema(t *testing.T) {
	companion, launcher := paths(t)
	write(t, companion, companionFixture)

	if _, err := MigrateFiles(companion, launcher); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(companion)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if _, stale := keys["server_addr"]; stale {
		t.Error("the migrated file still carries server_addr")
	}
	if _, ok := keys["port"]; !ok {
		t.Error("the migrated file has no port")
	}
}

// TestBackupPermissions: the backup holds the same token as the original, so it
// gets the same permissions.
func TestBackupPermissions(t *testing.T) {
	companion, launcher := paths(t)
	write(t, companion, companionFixture)
	report, err := MigrateFiles(companion, launcher)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(report.Backups[0])
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits: Go reports every writable file as
	// 0666 there, and what keeps the file private is the ACL it inherits from the
	// user's profile directory. The bits are checked where they are the control.
	if perm := info.Mode().Perm(); perm != 0o600 && runtime.GOOS != "windows" {
		t.Errorf("backup mode = %o, want 600", perm)
	}
}
