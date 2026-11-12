package leakintent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// NEW_502, issue #35: a second request of the same kind is kept beside the
// first. Each is listed, consumed, dismissed and expires by its own id, and
// the used-link rule (#20) is unchanged.
func TestTwoRequestsOfOneKindAreBothPendingAndTakenByTheirOwnID(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	a := aub.LeakTestLink{AssetID: "mapa", Revision: 3, ContentSHA256: digest, RequestID: strings.Repeat("a", 32)}
	b := aub.LeakTestLink{AssetID: "mapb", Revision: 7, ContentSHA256: digest, RequestID: strings.Repeat("b", 32)}
	if err := Receive(path, a, now); err != nil {
		t.Fatal(err)
	}
	if err := Receive(path, b, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := List(path, now.Add(time.Minute))
	if err != nil || len(list) != 2 || list[0].Request != a || list[1].Request != b {
		t.Fatalf("both requests are not listed, oldest first: %+v (%v)", list, err)
	}
	// The first link again: still two, and its lifetime renewed.
	if err := Receive(path, a, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(path, now.Add(2*time.Minute)); len(list) != 2 {
		t.Fatalf("the same link delivered again became a third entry: %+v", list)
	}
	if list, _ := List(path, now.Add(2*time.Minute)); !list[0].ExpiresAt.Equal(now.Add(2*time.Minute + Lifetime)) {
		t.Fatalf("a re-delivered request kept its first lifetime: %v", list[0].ExpiresAt)
	}
	// Building A leaves B pending, and A's link opened again is a repeat.
	if consumed, err := Consume(path, a.RequestID, now.Add(3*time.Minute)); err != nil || consumed == nil || *consumed != a {
		t.Fatalf("Consume(A) = %+v, %v", consumed, err)
	}
	if err := Used(path, a.RequestID, "build-a", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(path, b.RequestID, now.Add(3*time.Minute)); got == nil || got.Request != b {
		t.Fatalf("building A took B too: %+v", got)
	}
	if err := Receive(path, a, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(path, a.RequestID, now.Add(4*time.Minute)); got != nil {
		t.Fatal("a used request became pending again")
	}
	if repeat, _ := Repeated(path, now.Add(4*time.Minute)); repeat == nil || repeat.RequestID != a.RequestID || repeat.BuildID != "build-a" {
		t.Fatalf("the used link is not a repeat naming its build: %+v", repeat)
	}
	// B expires by its own lifetime, not A's.
	if got, _ := Get(path, b.RequestID, now.Add(time.Minute+Lifetime)); got != nil {
		t.Fatalf("B outlived its lifetime: %+v", got)
	}
}

func TestDismissingOneRequestLeavesTheOther(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Now().UTC()
	a := aub.LeakTestLink{AssetID: "mapa", Revision: 1, ContentSHA256: digest, RequestID: strings.Repeat("a", 32)}
	b := aub.LeakTestLink{AssetID: "mapa", Revision: 1, ContentSHA256: digest, RequestID: strings.Repeat("b", 32)}
	for _, link := range []aub.LeakTestLink{a, b} {
		if err := Receive(path, link, now); err != nil {
			t.Fatal(err)
		}
	}
	if dismissed, err := Consume(path, b.RequestID, now); err != nil || dismissed == nil {
		t.Fatalf("dismiss B: %+v %v", dismissed, err)
	}
	if list, _ := List(path, now); len(list) != 1 || list[0].Request != a {
		t.Fatalf("dismissing B changed what else is pending: %+v", list)
	}
}

func TestTheListIsBoundedAndDropsTheOldest(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Now().UTC()
	for i := 0; i < PendingLimit+2; i++ {
		id := fmt.Sprintf("%032x", i+1)
		if err := Receive(path, aub.LeakTestLink{AssetID: "m", Revision: 1, ContentSHA256: digest, RequestID: id}, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := List(path, now.Add(time.Minute))
	if len(list) != PendingLimit || list[0].Request.RequestID != fmt.Sprintf("%032x", 3) {
		t.Fatalf("%d pending, first %q", len(list), list[0].Request.RequestID)
	}
}

// The version rule: a 1.0 file — what an older Companion or link handler
// writes — is imported, its used and retired ids with it, and removed. A
// request the list already retired is not brought back by it.
func TestALegacySingleRequestFileIsImportedWithItsUsedIDs(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	now := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	pending := aub.LeakTestLink{AssetID: "mapp", Revision: 2, ContentSHA256: digest, RequestID: strings.Repeat("c", 32)}
	used := strings.Repeat("d", 32)
	legacy := legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &pending, ReceivedAt: now, ExpiresAt: now.Add(Lifetime),
		LaunchedByVersion: "1.500",
		Retired:           []Retired{{RequestID: used, Until: now.Add(RetiredFor), Used: true, BuildID: "build-u"}}}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, LegacyFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// A request already pending here stays beside the imported one.
	other := aub.LeakTestLink{AssetID: "mapo", Revision: 1, ContentSHA256: digest, RequestID: strings.Repeat("e", 32)}
	if err := Receive(path, other, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	list, err := List(path, now.Add(2*time.Second))
	if err != nil || len(list) != 2 {
		t.Fatalf("after import: %+v (%v)", list, err)
	}
	if got, _ := Get(path, pending.RequestID, now.Add(2*time.Second)); got == nil || got.LaunchedByVersion != "1.500" || !got.ReceivedAt.Equal(now) {
		t.Fatalf("the legacy request lost what it carried: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, LegacyFileName)); !os.IsNotExist(err) {
		t.Fatalf("the legacy file was not removed after the import: %v", err)
	}
	// The used id stayed used: its link opened again is a repeat.
	usedLink := aub.LeakTestLink{AssetID: "mapu", Revision: 9, ContentSHA256: digest, RequestID: used}
	if err := Receive(path, usedLink, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(path, used, now.Add(3*time.Second)); got != nil {
		t.Fatal("a used id from the legacy file became pending")
	}
	if repeat, _ := Repeated(path, now.Add(3*time.Second)); repeat == nil || repeat.BuildID != "build-u" {
		t.Fatalf("the legacy used id is not a repeat naming its build: %+v", repeat)
	}
	// An older program writes its file again, naming a request this list has
	// already built: it is not brought back.
	if _, err := Consume(path, pending.RequestID, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	legacy = legacyIntent{SchemaVersion: LegacySchemaVersion, Request: &pending, ReceivedAt: now.Add(5 * time.Second), ExpiresAt: now.Add(5*time.Second + Lifetime)}
	raw, _ = json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, LegacyFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(path, pending.RequestID, now.Add(6*time.Second)); got != nil {
		t.Fatal("a retired request came back through the legacy file")
	}
	// A file that is not 1.0 is not ours to remove.
	if err := os.WriteFile(filepath.Join(dir, LegacyFileName), []byte(`{"schema_version":"something-else"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(path, now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, LegacyFileName)); err != nil {
		t.Fatalf("a file of another version was removed: %v", err)
	}
}
