package leakintent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

const digest = "c303f8248626c39233029796b2c9e2d3d7f94e789526eaab76d1268a8288af85"

func TestARequestIsRecordedReadReplacedAndExpires(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC)
	first := aub.LeakTestLink{AssetID: "map1", Revision: 3, ContentSHA256: digest}
	if err := Receive(path, first, now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the intent file was not written: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("the intent file is not a regular file: %v", info.Mode())
	}
	// Windows reports 0666 for any writable file; what keeps it private there
	// is the ACL it inherits from the user's profile directory, which this test
	// does not check. The bits are checked where they are the control.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("the intent file is mode %v, want 0600", info.Mode().Perm())
	}
	got, received, err := Newest(path, now.Add(time.Minute))
	if err != nil || got == nil || *got != first || !received.Equal(now) {
		t.Fatalf("read %+v at %v (%v)", got, received, err)
	}
	second := aub.LeakTestLink{AssetID: "map1", Revision: 4, ContentSHA256: digest}
	if err := Receive(path, second, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Newest(path, now.Add(3*time.Minute)); got == nil || got.Revision != 4 {
		t.Fatalf("a newer link did not replace the older one: %+v", got)
	}
	if got, _, _ := Newest(path, now.Add(2*time.Minute+Lifetime)); got != nil {
		t.Fatalf("an expired request survived: %+v", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an empty intent left its file behind: %v", err)
	}
}

func TestConsumeOnlyTheReviewedReturnRequest(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Now().UTC()
	first := aub.LeakTestLink{AssetID: "map1", Revision: 3, ContentSHA256: digest,
		RequestID: strings.Repeat("a", 32)}
	if err := Receive(path, first, now); err != nil {
		t.Fatal(err)
	}
	if got, err := Consume(path, strings.Repeat("b", 32), now); err != nil || got != nil {
		t.Fatalf("older build consumed a newer request: %+v %v", got, err)
	}
	if got, _, _ := Newest(path, now); got == nil || *got != first {
		t.Fatal("wrong consume removed the pending request")
	}
	if got, err := Consume(path, first.RequestID, now); err != nil || got == nil || *got != first {
		t.Fatalf("consume returned %+v: %v", got, err)
	}
	if got, _, _ := Newest(path, now); got != nil {
		t.Fatal("consumed request remained pending")
	}
}

func TestDismissForgetsAndABadFileIsALostClick(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	now := time.Now().UTC()
	if err := Receive(path, aub.LeakTestLink{AssetID: "m", Revision: 1, ContentSHA256: digest}, now); err != nil {
		t.Fatal(err)
	}
	if err := Dismiss(path, now); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Newest(path, now); got != nil {
		t.Fatalf("dismissed request still pending: %+v", got)
	}
	if err := Receive(path, aub.LeakTestLink{AssetID: "bad/id", Revision: 1, ContentSHA256: digest}, now); err == nil {
		t.Fatal("recorded a request that is not a valid link")
	}
	tampered := `{"schema_version":"` + SchemaVersion + `","requests":[{"request":{"asset_id":"../x","revision":1,"content_sha256":"` +
		digest + `"},"received_at":"` + now.Format(time.RFC3339) + `","expires_at":"` + now.Add(time.Minute).Format(time.RFC3339) + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Newest(path, now); got != nil {
		t.Fatalf("a request that no longer parses was shown: %+v", got)
	}
}

// NEW_508, issue #20: one editor request is at most one build. A used link
// delivered again never becomes pending; it is remembered for a moment as a
// repeat that names the build which ran. A DISMISSED request was never run,
// so the same link opened again is neither pending nor "already ran".
func TestAUsedLinkDeliveredAgainIsARepeatNotASecondRequest(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	link := aub.LeakTestLink{AssetID: "map1", Revision: 3, ContentSHA256: digest, RequestID: strings.Repeat("c", 32)}
	var err error
	if err = Receive(path, link, now); err != nil {
		t.Fatal(err)
	}
	if consumed, err := Consume(path, link.RequestID, now); err != nil || consumed == nil {
		t.Fatalf("Consume: %v, %v", consumed, err)
	}
	if err = Used(path, link.RequestID, "", now); err != nil {
		t.Fatal(err)
	}

	// Delivered again while its build runs: not pending, and a repeat with no
	// build id yet.
	if err = Receive(path, link, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if pending, _, _ := Newest(path, now.Add(time.Second)); pending != nil {
		t.Fatalf("a used request became pending again: %+v", pending)
	}
	repeat, err := Repeated(path, now.Add(time.Second))
	if err != nil || repeat == nil || repeat.RequestID != link.RequestID || repeat.BuildID != "" || repeat.Revision != 3 {
		t.Fatalf("Repeated = %+v, %v", repeat, err)
	}

	// The build got its id: the repeat on screen learns it.
	if err = Used(path, link.RequestID, "build-7", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if repeat, _ = Repeated(path, now.Add(2*time.Second)); repeat == nil || repeat.BuildID != "build-7" {
		t.Fatalf("the repeat does not name the build: %+v", repeat)
	}

	// Dismissing another id's repeat leaves this one; dismissing it removes it.
	if err = DismissRepeat(path, "other", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if repeat, _ = Repeated(path, now.Add(3*time.Second)); repeat == nil {
		t.Fatal("a repeat was dismissed by another request's id")
	}
	if err = DismissRepeat(path, link.RequestID, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if repeat, _ = Repeated(path, now.Add(3*time.Second)); repeat != nil {
		t.Fatalf("a dismissed repeat is still there: %+v", repeat)
	}

	// A later delivery is a repeat again, and it expires by itself.
	if err = Receive(path, link, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if repeat, _ = Repeated(path, now.Add(time.Minute)); repeat == nil || repeat.BuildID != "build-7" {
		t.Fatalf("a later delivery is not a repeat naming the build: %+v", repeat)
	}
	if repeat, _ = Repeated(path, now.Add(time.Minute+RepeatFor)); repeat != nil {
		t.Fatalf("a repeat outlived RepeatFor: %+v", repeat)
	}

	// Dismissed, never run: delivered again it is neither pending nor a repeat.
	other := aub.LeakTestLink{AssetID: "map1", Revision: 4, ContentSHA256: digest, RequestID: strings.Repeat("d", 32)}
	later := now.Add(10 * time.Minute)
	if err = Receive(path, other, later); err != nil {
		t.Fatal(err)
	}
	if _, err = Consume(path, other.RequestID, later); err != nil {
		t.Fatal(err)
	}
	if err = Receive(path, other, later.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if repeat, _ = Repeated(path, later.Add(time.Second)); repeat != nil {
		t.Fatalf("a dismissed request reads as one that already ran: %+v", repeat)
	}
}
