package leakintent

import (
	"os"
	"path/filepath"
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
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the intent file is %v, %v", info, err)
	}
	got, received, err := Read(path, now.Add(time.Minute))
	if err != nil || got == nil || *got != first || !received.Equal(now) {
		t.Fatalf("read %+v at %v (%v)", got, received, err)
	}
	second := aub.LeakTestLink{AssetID: "map1", Revision: 4, ContentSHA256: digest}
	if err := Receive(path, second, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Read(path, now.Add(3*time.Minute)); got == nil || got.Revision != 4 {
		t.Fatalf("a newer link did not replace the older one: %+v", got)
	}
	if got, _, _ := Read(path, now.Add(2*time.Minute+Lifetime)); got != nil {
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
	if got, _, _ := Read(path, now); got == nil || *got != first {
		t.Fatal("wrong consume removed the pending request")
	}
	if got, err := Consume(path, first.RequestID, now); err != nil || got == nil || *got != first {
		t.Fatalf("consume returned %+v: %v", got, err)
	}
	if got, _, _ := Read(path, now); got != nil {
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
	if got, _, _ := Read(path, now); got != nil {
		t.Fatalf("dismissed request still pending: %+v", got)
	}
	if err := Receive(path, aub.LeakTestLink{AssetID: "bad/id", Revision: 1, ContentSHA256: digest}, now); err == nil {
		t.Fatal("recorded a request that is not a valid link")
	}
	tampered := `{"schema_version":"` + SchemaVersion + `","request":{"asset_id":"../x","revision":1,"content_sha256":"` +
		digest + `"},"expires_at":"` + now.Add(time.Hour).Format(time.RFC3339) + `"}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Read(path, now); got != nil {
		t.Fatalf("a request that no longer parses was shown: %+v", got)
	}
}
