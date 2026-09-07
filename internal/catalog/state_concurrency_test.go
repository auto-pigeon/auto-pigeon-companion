package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two instances, each of which learned about a different revocation. Before the
// merge, the second write erased the first one's — silently, because the file
// is written atomically and both writes succeeded.
func TestARevocationRecordedByAnotherInstanceIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog-state.json")

	first := NewState()
	first.CatalogSerial["aucom"] = 7
	first.RevokedArtifacts["sha256:"+strings.Repeat("aa", 32)] = Revocation{
		Digest: "sha256:" + strings.Repeat("aa", 32), Reason: "backdoored", At: TestNow,
	}
	if err := SaveState(path, first); err != nil {
		t.Fatal(err)
	}

	// A second instance that started before the first one wrote: it knows
	// nothing about that revocation, and it has learned a different one.
	second := NewState()
	second.CatalogSerial["aucom"] = 9
	second.RevokedKeys["dead"] = "the key leaked"
	if err := SaveState(path, second); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := loaded.RevokedArtifacts["sha256:"+strings.Repeat("aa", 32)]; !held {
		t.Error("a revocation another instance recorded was erased by this one's write")
	}
	if _, held := loaded.RevokedKeys["dead"]; !held {
		t.Error("this instance's own key revocation was not written")
	}
	if got := loaded.CatalogSerial["aucom"]; got != 9 {
		t.Errorf("the serial ratchet is %d, want the higher of the two (9)", got)
	}
}

// The ratchet only goes one way, so a writer holding an older serial must not
// wind it back.
func TestAnOlderSerialCannotBeWrittenBackOverANewerOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog-state.json")
	newer := NewState()
	newer.CatalogSerial["aucom"] = 42
	newer.KeyringSerial["ring"] = 11
	newer.CompatibilitySerial["compat"] = 5
	if err := SaveState(path, newer); err != nil {
		t.Fatal(err)
	}
	older := NewState()
	older.CatalogSerial["aucom"] = 3
	older.KeyringSerial["ring"] = 1
	older.CompatibilitySerial["compat"] = 1
	if err := SaveState(path, older); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]int64{
		"catalog":       loaded.CatalogSerial["aucom"],
		"keyring":       loaded.KeyringSerial["ring"],
		"compatibility": loaded.CompatibilitySerial["compat"],
	} {
		if want := map[string]int64{"catalog": 42, "keyring": 11, "compatibility": 5}[name]; got != want {
			t.Errorf("the %s ratchet went backwards: %d, want %d", name, got, want)
		}
	}
}

// Concurrent writers: every revocation any of them knew about has to be in the
// file at the end, and the file has to still parse.
func TestConcurrentWritersLoseNoRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog-state.json")
	const writers = 8

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			state := NewState()
			state.CatalogSerial["aucom"] = int64(n)
			digest := "sha256:" + strings.Repeat(string(rune('a'+n)), 64)
			state.RevokedArtifacts[digest] = Revocation{Digest: digest, Reason: "n", At: TestNow}
			if err := SaveState(path, state); err != nil {
				t.Errorf("writer %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("the state file did not survive concurrent writers: %v", err)
	}
	if got := len(loaded.RevokedArtifacts); got != writers {
		t.Errorf("%d of %d revocations survived", got, writers)
	}
	if got := loaded.CatalogSerial["aucom"]; got != writers-1 {
		t.Errorf("the serial is %d, want the highest written (%d)", got, writers-1)
	}
}

// A state file that exists and will not parse is not replaced with a fresh one.
// Overwriting an unreadable ratchet is the reset that deleting the file is
// supposed to be unable to do quietly.
func TestACorruptStateFileIsNotSilentlyReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog-state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := NewState()
	state.CatalogSerial["aucom"] = 1
	if err := SaveState(path, state); err == nil {
		t.Fatal("a corrupt trust state was overwritten")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{not json" {
		t.Errorf("the corrupt file was changed: %q, %v", raw, err)
	}
}

// The lock is released, whatever happened. A leftover lock would make the next
// write wait a full timeout and then refuse.
func TestNoLockIsLeftBehindByAWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog-state.json")
	if err := SaveState(path, NewState()); err != nil {
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
	_ = time.Now
}
