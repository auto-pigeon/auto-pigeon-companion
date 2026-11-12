package leakintent

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
)

// olderWrite is what a 1.0 Companion or link handler does: take the 1.0
// file's lock and write its record there.
func olderWrite(legacy string, record legacyIntent, timeout time.Duration) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return lockfile.With(legacy, lockfile.Options{Program: "older-companion", Timeout: timeout}, func() error {
		return os.WriteFile(legacy, raw, 0o600)
	})
}

func link(asset string, id byte) aub.LeakTestLink {
	return aub.LeakTestLink{AssetID: asset, Revision: 1, ContentSHA256: digest, RequestID: strings.Repeat(string(id), 32)}
}

// NEW_502A: an older program that writes between this program's read of the
// 1.0 file and its removal loses neither its pending request nor the ids it
// used. The 1.0 lock is held across the import; the older write waits, and
// the next read merges it.
func TestAnOlderLeakWriteBetweenTheLegacyReadAndItsRemovalIsKept(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	legacy := filepath.Join(dir, LegacyFileName)
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	a, b, c := link("mapa", 'a'), link("mapb", 'b'), link("mapc", 'c')
	usedEarly, usedLate := strings.Repeat("d", 32), strings.Repeat("e", 32)
	if err := olderWrite(legacy, legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &a, ReceivedAt: now,
		ExpiresAt: now.Add(Lifetime),
		Retired:   []Retired{{RequestID: usedEarly, Until: now.Add(RetiredFor), Used: true, BuildID: "build-d"}}}, time.Second); err != nil {
		t.Fatal(err)
	}
	// What the older program writes next: a new request, and an id it has
	// just built.
	later := legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &c, ReceivedAt: now.Add(time.Second),
		ExpiresAt: now.Add(time.Second + Lifetime), LaunchedByVersion: "1.500",
		Retired: []Retired{
			{RequestID: usedEarly, Until: now.Add(RetiredFor), Used: true, BuildID: "build-d"},
			{RequestID: usedLate, Until: now.Add(RetiredFor), Used: true, BuildID: "build-e"},
		}}

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

	if err := Receive(path, b, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the 1.0 file was not read: the test put nothing in the gap")
	}
	if inGap == nil {
		if got, _ := Get(path, c.RequestID, now.Add(2*time.Second)); got == nil {
			t.Fatal("a request an older program wrote between the 1.0 read and its removal was deleted unread")
		}
	}
	if !errors.Is(inGap, lockfile.ErrBusy) {
		t.Fatalf("an older program could write the 1.0 file between its read and its removal: %v", inGap)
	}
	if err := olderWrite(legacy, later, time.Second); err != nil {
		t.Fatal(err)
	}
	list, err := List(path, now.Add(3*time.Second))
	if err != nil || len(list) != 3 || list[0].Request != a || list[1].Request != b || list[2].Request != c {
		t.Fatalf("every request is not pending: %+v (%v)", list, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("the merged 1.0 file was left behind: %v", err)
	}
	// Both used ids stay used, each naming its own build.
	for id, build := range map[string]string{usedEarly: "build-d", usedLate: "build-e"} {
		if err := Receive(path, aub.LeakTestLink{AssetID: "mapu", Revision: 2, ContentSHA256: digest, RequestID: id}, now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		if got, _ := Get(path, id, now.Add(4*time.Second)); got != nil {
			t.Fatalf("used id %s became pending", id)
		}
		if repeat, _ := Repeated(path, now.Add(4*time.Second)); repeat == nil || repeat.RequestID != id || repeat.BuildID != build {
			t.Fatalf("used id %s is not a repeat naming %s: %+v", id, build, repeat)
		}
	}
}

// NEW_502A: when the list cannot be published the 1.0 file — its request and
// its used ids — stays exactly as it was, and the next read imports it again.
func TestAFailedLeakPublicationKeepsTheLegacyRecord(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	legacy := filepath.Join(dir, LegacyFileName)
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	a, b := link("mapa", 'a'), link("mapb", 'b')
	used := strings.Repeat("d", 32)
	if err := olderWrite(legacy, legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &a, ReceivedAt: now,
		ExpiresAt: now.Add(Lifetime),
		Retired:   []Retired{{RequestID: used, Until: now.Add(RetiredFor), Used: true, BuildID: "build-d"}}}, time.Second); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}

	failure := errors.New("injected publication failure")
	real := replace
	replace = func(string, string) error { return failure }
	err = Receive(path, b, now.Add(time.Second))
	replace = real
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

	if err := Receive(path, b, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	list, err := List(path, now.Add(3*time.Second))
	if err != nil || len(list) != 2 || list[0].Request != a || list[1].Request != b {
		t.Fatalf("after the retry: %+v (%v)", list, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("the imported 1.0 file was left behind: %v", err)
	}
	if err := Receive(path, aub.LeakTestLink{AssetID: "mapu", Revision: 2, ContentSHA256: digest, RequestID: used}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(path, used, now.Add(4*time.Second)); got != nil {
		t.Fatal("a used id from the 1.0 file became pending after a failed publication and its retry")
	}
	// Expiry is unchanged by the import.
	if got, _ := Get(path, a.RequestID, now.Add(Lifetime)); got != nil {
		t.Fatalf("an imported request outlived its own lifetime: %+v", got)
	}
}
