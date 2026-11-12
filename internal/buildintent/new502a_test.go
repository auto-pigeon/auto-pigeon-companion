package buildintent

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
)

// olderWrite is what a 1.0 Companion or link handler does: take the 1.0
// file's lock and write its one request there.
func olderWrite(legacy string, record legacyIntent, timeout time.Duration) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return lockfile.With(legacy, lockfile.Options{Program: "older-companion", Timeout: timeout}, func() error {
		return os.WriteFile(legacy, raw, 0o600)
	})
}

// NEW_502A: an older program that writes a second request between this
// program's read of the 1.0 file and its removal must not lose that request.
// The 1.0 lock is held across the whole import, so the older write waits; once
// it lands, the next read merges it.
func TestAnOlderWriteBetweenTheLegacyReadAndItsRemovalIsKept(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	legacy := filepath.Join(dir, LegacyFileName)
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	if err := olderWrite(legacy, legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &first, ReceivedAt: now,
		ExpiresAt: now.Add(Lifetime)}, time.Second); err != nil {
		t.Fatal(err)
	}
	later := legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &second, ReceivedAt: now.Add(time.Second),
		ExpiresAt: now.Add(time.Second + Lifetime), LaunchedByVersion: "1.500"}

	var inGap error
	ran := false
	afterLegacyRead = func() {
		if ran {
			return
		}
		ran = true
		inGap = olderWrite(legacy, later, 100*time.Millisecond)
	}
	defer func() { afterLegacyRead = func() {} }()

	if _, err := List(path, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the 1.0 file was not read: the test put nothing in the gap")
	}
	if inGap == nil {
		// The older program was told its request was recorded. It must be.
		if got, _ := Get(path, second.RequestID, now.Add(2*time.Second)); got == nil {
			t.Fatal("a request an older program wrote between the 1.0 read and its removal was deleted unread")
		}
	}
	if !errors.Is(inGap, lockfile.ErrBusy) {
		t.Fatalf("an older program could write the 1.0 file between its read and its removal: %v", inGap)
	}
	// The older program retries once the lock is free, and the next read
	// merges what it wrote.
	if err := olderWrite(legacy, later, time.Second); err != nil {
		t.Fatal(err)
	}
	list, err := List(path, now.Add(3*time.Second))
	if err != nil || len(list) != 2 || list[0].Request != first || list[1].Request != second {
		t.Fatalf("both requests are not pending: %+v (%v)", list, err)
	}
	if list[1].LaunchedByVersion != "1.500" {
		t.Fatalf("the later legacy request lost its receipt: %+v", list[1])
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("the merged 1.0 file was left behind: %v", err)
	}
}

// NEW_502A: when the list cannot be published the 1.0 file is not removed, and
// the next read imports it again.
func TestAFailedPublicationKeepsTheLegacyRecord(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	legacy := filepath.Join(dir, LegacyFileName)
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	if err := olderWrite(legacy, legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &first, ReceivedAt: now,
		ExpiresAt: now.Add(Lifetime)}, time.Second); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}

	failure := errors.New("injected publication failure")
	replace = func(string, string) error { return failure }
	err = Receive(path, second, now.Add(time.Second))
	replace = realReplace
	if !errors.Is(err, failure) {
		t.Fatalf("Receive with a failing publication = %v", err)
	}
	if after, err := os.ReadFile(legacy); err != nil || !bytes.Equal(after, original) {
		t.Fatalf("a failed publication changed the 1.0 file: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a failed publication left a list behind: %v", err)
	}
	if _, err := os.Stat(lockfile.PathFor(legacy)); !os.IsNotExist(err) {
		t.Fatalf("a failed publication left the 1.0 lock held: %v", err)
	}

	// The retry imports the 1.0 record again, beside the request it adds.
	if err := Receive(path, second, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	list, err := List(path, now.Add(3*time.Second))
	if err != nil || len(list) != 2 || list[0].Request != first || list[1].Request != second {
		t.Fatalf("after the retry: %+v (%v)", list, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("the imported 1.0 file was left behind: %v", err)
	}
	// Expiry is unchanged by the import: the 1.0 record keeps its own.
	if got, _ := Get(path, first.RequestID, now.Add(Lifetime)); got != nil {
		t.Fatalf("an imported request outlived its own lifetime: %+v", got)
	}
}

var realReplace = replace
