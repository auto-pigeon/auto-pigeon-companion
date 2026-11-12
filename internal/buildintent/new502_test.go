package buildintent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// NEW_502, issues #33 and #35: a second build request is kept beside the first
// — whether it is another map or the same request opened again with a new
// delivery id — so the one open in Build & Run is never dropped by it.
func TestASecondBuildRequestIsKeptBesideTheFirst(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	if err := Receive(path, first, now); err != nil {
		t.Fatal(err)
	}
	if err := ReceiveFrom(path, second, "1.600", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := List(path, now.Add(time.Minute))
	if err != nil || len(list) != 2 || list[0].Request != first || list[1].Request != second {
		t.Fatalf("both requests are not listed, oldest first: %+v (%v)", list, err)
	}
	if got, _ := Get(path, first.RequestID, now.Add(time.Minute)); got == nil || got.Request != first {
		t.Fatalf("the first request is not addressable: %+v", got)
	}
	// Starting the second leaves the first, and dismissing the first leaves
	// nothing behind.
	if got, err := Consume(path, second.RequestID, now.Add(2*time.Minute)); err != nil || got == nil || *got != second {
		t.Fatalf("Consume(second) = %+v, %v", got, err)
	}
	if got, _ := Get(path, first.RequestID, now.Add(2*time.Minute)); got == nil {
		t.Fatal("starting the second request took the first")
	}
	if got, err := Consume(path, second.RequestID, now.Add(2*time.Minute)); err != nil || got != nil {
		t.Fatalf("a request was started twice: %+v %v", got, err)
	}
	if got, err := Dismiss(path, first.RequestID, now.Add(3*time.Minute)); err != nil || got == nil || *got != first {
		t.Fatalf("Dismiss(first) = %+v, %v", got, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an empty list left its file behind: %v", err)
	}
}

func TestEachBuildRequestExpiresByItsOwnLifetime(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Now().UTC()
	if err := Receive(path, first, now); err != nil {
		t.Fatal(err)
	}
	if err := Receive(path, second, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(path, now.Add(Lifetime)); len(list) != 1 || list[0].Request != second {
		t.Fatalf("at the first's expiry: %+v", list)
	}
}

// The version rule: the 1.0 file an older Companion or link handler writes is
// imported into the list and removed; an older program never reads the list.
func TestALegacySingleBuildRequestFileIsImported(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	now := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	legacy := legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &first, ReceivedAt: now, ExpiresAt: now.Add(Lifetime),
		LaunchedByVersion: "1.500"}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, LegacyFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Receive(path, second, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	list, err := List(path, now.Add(2*time.Second))
	if err != nil || len(list) != 2 {
		t.Fatalf("after import: %+v (%v)", list, err)
	}
	if received, by, _ := Receipt(path, first.RequestID, now.Add(2*time.Second)); by != "1.500" || !received.Equal(now) {
		t.Fatalf("the legacy request lost its receipt: %v %q", received, by)
	}
	if _, err := os.Stat(filepath.Join(dir, LegacyFileName)); !os.IsNotExist(err) {
		t.Fatalf("the legacy file was not removed: %v", err)
	}
	// A legacy lifetime longer than this program grants is not imported.
	long := legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &aub.BuildRequestLink{RequestID: strings.Repeat("c", 32),
		Intent: aub.BuildIntentBuild}, ReceivedAt: now, ExpiresAt: now.Add(2 * Lifetime)}
	raw, _ = json.Marshal(long)
	if err := os.WriteFile(filepath.Join(dir, LegacyFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(path, strings.Repeat("c", 32), now.Add(3*time.Second)); got != nil {
		t.Fatalf("an over-long legacy request was imported: %+v", got)
	}
}
