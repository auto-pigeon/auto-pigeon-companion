package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// The lost update this package's lock exists to stop. Two instances read the
// same file, each changes a different field, and both writes succeed — before
// Update, the second one silently undid the first.
func TestAChangeByAnotherInstanceIsNotUndoneByThisOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// The GUI server writes a session.
	if _, err := Update(path, func(current *Config) error {
		current.Session = Session{Token: "a-token-long-enough-to-keep", Email: "user@example.invalid"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A terminal, which read the file before that and knows nothing about it,
	// changes the job concurrency.
	settings, err := Update(path, func(current *Config) error {
		current.JobConcurrency = 3
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Session.Token == "" {
		t.Error("the session another instance stored was erased")
	}
	if settings.JobConcurrency != 3 {
		t.Error("this instance's own change was not written")
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Email != "user@example.invalid" || loaded.JobConcurrency != 3 {
		t.Errorf("the file on disk lost one of the two changes: %+v", loaded)
	}
}

// A first run has no file, and Update must not need one.
func TestUpdateOnAMachineWithNoConfigFileStartsFromTheDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings, err := Update(path, func(current *Config) error {
		if current.Port != DefaultPort {
			return errors.New("a first run was not handed the defaults")
		}
		current.AUBBaseURL = "https://aub.invalid/"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Port != DefaultPort || settings.AUBBaseURL != "https://aub.invalid/" {
		t.Errorf("wrote %+v", settings)
	}
}

// A mutation that refuses writes nothing. The file must be exactly as it was.
func TestAMutationThatFailsWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveTo(path, Config{Port: 4242, AUBBaseURL: "https://kept.invalid/"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("no")
	if _, err := Update(path, func(current *Config) error {
		current.Port = 1
		return refusal
	}); !errors.Is(err, refusal) {
		t.Fatalf("Update returned %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("a refused mutation changed the file:\n%s\n%s", before, after)
	}
}

// Concurrent writers, each incrementing the same field. Every increment has to
// land, which is only true if each read-modify-write is inside the lock.
func TestConcurrentUpdatesAllLand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	const writers, each = 6, 10

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < each; n++ {
				if _, err := Update(path, func(current *Config) error {
					current.JobConcurrency++
					return nil
				}); err != nil {
					t.Errorf("Update: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.JobConcurrency != writers*each {
		t.Errorf("%d of %d updates landed", loaded.JobConcurrency, writers*each)
	}
}

// A config directory that cannot be written is reported by name, not by a
// truncated file. This is the read-only-home case, and the antivirus case where
// a directory is briefly not writable.
func TestAnUnwritableConfigDirectoryIsReportedAndChangesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop a write on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := SaveTo(path, Config{Port: 4242}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	_, err := Update(path, func(current *Config) error {
		current.Port = 1
		return nil
	})
	if err == nil {
		t.Fatal("a read-only config directory was written to")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the error does not name the directory: %v", err)
	}
	os.Chmod(dir, 0o700)
	loaded, err := LoadFrom(path)
	if err != nil || loaded.Port != 4242 {
		t.Errorf("the stored config changed: %+v, %v", loaded, err)
	}
}

// No lock file survives a write. One that did would make the next write wait a
// full timeout and then refuse.
func TestNoLockSurvivesAWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if _, err := Update(path, func(*Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".lock") {
			t.Errorf("%s was left behind", entry.Name())
		}
	}
}
