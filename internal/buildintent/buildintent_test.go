package buildintent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

var (
	first  = aub.BuildRequestLink{RequestID: strings.Repeat("a", 32), Intent: aub.BuildIntentBuild}
	second = aub.BuildRequestLink{RequestID: strings.Repeat("b", 32), Intent: aub.BuildIntentBuildRun}
)

func TestARequestIsRecordedPrivatelyReadReplacedAndExpires(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	if err := Receive(path, first, now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the intent file was not written: %v", err)
	}
	// Windows reports 0666 for any writable file; the ACL inherited from the
	// profile directory keeps it private there, which this does not check.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("the intent file is mode %v, want 0600", info.Mode().Perm())
	}
	got, received, err := Read(path, now.Add(time.Minute))
	if err != nil || got == nil || *got != first || !received.Equal(now) {
		t.Fatalf("read %+v at %v (%v)", got, received, err)
	}
	if err := Receive(path, second, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Read(path, now.Add(3*time.Minute)); got == nil || *got != second {
		t.Fatalf("a newer link did not replace the older one: %+v", got)
	}
	if got, _, _ := Read(path, now.Add(2*time.Minute+Lifetime-time.Second)); got == nil {
		t.Fatal("the request expired before its lifetime")
	}
	if got, _, _ := Read(path, now.Add(2*time.Minute+Lifetime)); got != nil {
		t.Fatalf("an expired request survived: %+v", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an empty intent left its file behind: %v", err)
	}
}

func TestConsumeAndDismissTakeOnlyTheNamedRequest(t *testing.T) {
	path := Path(t.TempDir())
	now := time.Now().UTC()
	if err := Receive(path, second, now); err != nil {
		t.Fatal(err)
	}
	for name, take := range map[string]func(string, string, time.Time) (*aub.BuildRequestLink, error){
		"consume": Consume, "dismiss": Dismiss} {
		if got, err := take(path, first.RequestID, now); err != nil || got != nil {
			t.Fatalf("%s of an older id took the newer request: %+v %v", name, got, err)
		}
		if got, err := take(path, "", now); err != nil || got != nil {
			t.Fatalf("%s of no id took the request: %+v %v", name, got, err)
		}
		if got, _, _ := Read(path, now); got == nil || *got != second {
			t.Fatalf("a %s of another id removed the pending request", name)
		}
	}
	if got, err := Consume(path, second.RequestID, now); err != nil || got == nil || *got != second {
		t.Fatalf("consume returned %+v: %v", got, err)
	}
	if got, _, _ := Read(path, now); got != nil {
		t.Fatal("a consumed request remained pending")
	}
	if got, err := Consume(path, second.RequestID, now); err != nil || got != nil {
		t.Fatalf("a request was consumed twice: %+v %v", got, err)
	}
	if err := Receive(path, first, now); err != nil {
		t.Fatal(err)
	}
	if got, err := Dismiss(path, first.RequestID, now); err != nil || got == nil || *got != first {
		t.Fatalf("dismiss returned %+v: %v", got, err)
	}
	if got, _, _ := Read(path, now); got != nil {
		t.Fatal("a dismissed request remained pending")
	}
}

// What is on disk is validated again on every read: a tampered request, or one
// given a longer life than this program grants, is a lost click.
func TestABadFileIsALostClick(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	now := time.Now().UTC()
	if err := Receive(path, aub.BuildRequestLink{RequestID: "../x", Intent: "build"}, now); err == nil {
		t.Fatal("recorded a request that is not a valid link")
	}
	if err := Receive(path, aub.BuildRequestLink{RequestID: first.RequestID, Intent: "leak_test"}, now); err == nil {
		t.Fatal("recorded an unknown intent")
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stamp := func(at time.Time) string { return at.Format(time.RFC3339) }
	for _, tampered := range []string{
		`{"schema_version":"` + SchemaVersion + `","request":{"request_id":"` + strings.Repeat("A", 32) +
			`","intent":"build"},"received_at":"` + stamp(now) + `","expires_at":"` + stamp(now.Add(time.Minute)) + `"}`,
		`{"schema_version":"` + SchemaVersion + `","request":{"request_id":"` + first.RequestID +
			`","intent":"build_run&exe=x"},"received_at":"` + stamp(now) + `","expires_at":"` + stamp(now.Add(time.Minute)) + `"}`,
		`{"schema_version":"` + SchemaVersion + `","request":{"request_id":"` + first.RequestID +
			`","intent":"build"},"received_at":"` + stamp(now) + `","expires_at":"` + stamp(now.Add(48*time.Hour)) + `"}`,
		`{"schema_version":"` + leakintent.SchemaVersion + `","request":{"request_id":"` + first.RequestID +
			`","intent":"build"},"received_at":"` + stamp(now) + `","expires_at":"` + stamp(now.Add(time.Minute)) + `"}`,
		`not json`,
	} {
		write(tampered)
		if got, _, _ := Read(path, now); got != nil {
			t.Errorf("a request that does not validate was shown: %+v from %s", got, tampered)
		}
	}
}

// The two intents are two files: a build request never lands where a leak test
// is read, and a leak test is never read as a build request.
func TestABuildRequestAndALeakTestAreDifferentFiles(t *testing.T) {
	dir := t.TempDir()
	if Path(dir) == leakintent.Path(dir) || SchemaVersion == leakintent.SchemaVersion {
		t.Fatal("the build-request intent shares the leak-test intent's file or schema")
	}
	now := time.Now().UTC()
	if err := Receive(Path(dir), first, now); err != nil {
		t.Fatal(err)
	}
	if got, _, err := leakintent.Read(leakintent.Path(dir), now); err != nil || got != nil {
		t.Fatalf("a build request was read as a leak test: %+v %v", got, err)
	}
}
