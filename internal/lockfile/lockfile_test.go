package lockfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func guarded(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.json")
}

func TestTheLockFileSitsBesideTheFileItGuardsAndIsPrivate(t *testing.T) {
	path := guarded(t)
	lock, err := Acquire(path, Options{Program: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	if got, want := lock.Path(), filepath.Join(filepath.Dir(path), ".config.json.lock"); got != want {
		t.Errorf("lock path is %s, want %s", got, want)
	}
	info, err := os.Stat(lock.Path())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the lock file is mode %v, want 0600", info.Mode().Perm())
	}
}

// The refusal has to name the other holder. A lock that only says "busy" is a
// lock people delete.
func TestASecondAcquisitionIsRefusedAndSaysWhoHoldsIt(t *testing.T) {
	path := guarded(t)
	first, err := Acquire(path, Options{Program: "the GUI server"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	_, err = Acquire(path, Options{Timeout: 50 * time.Millisecond, Program: "a second instance"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquisition: got %v, want ErrBusy", err)
	}
	for _, want := range []string{"the GUI server", "process "} {
		if !contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

func TestAReleasedLockIsImmediatelyAvailable(t *testing.T) {
	path := guarded(t)
	first, err := Acquire(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(first.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock file survived the release: %v", err)
	}
	second, err := Acquire(path, Options{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("second acquisition after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestReleasingTwiceIsNotAnError(t *testing.T) {
	lock, err := Acquire(guarded(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("the second release reported %v", err)
	}
}

// A process killed mid-write leaves its lock file behind, because O_EXCL locks
// are not released by the kernel. That is the price of the primitive, and this
// is the part that pays it.
func TestAnAbandonedLockIsBrokenAndTheTakeoverIsReported(t *testing.T) {
	path := guarded(t)
	abandoned := Holder{SchemaVersion: SchemaVersion, PID: 4212, Host: "gone",
		Program: "a killed build", Nonce: "deadbeef", Since: time.Now().Add(-time.Hour)}
	raw, _ := json.Marshal(abandoned)
	if err := os.WriteFile(PathFor(path), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(PathFor(path), old, old); err != nil {
		t.Fatal(err)
	}

	lock, err := Acquire(path, Options{Stale: time.Minute, Timeout: time.Second})
	if err != nil {
		t.Fatalf("an abandoned lock was not broken: %v", err)
	}
	defer lock.Release()

	broken, took := lock.BrokeStale()
	if !took {
		t.Fatal("the takeover was not reported")
	}
	if broken.Program != "a killed build" || broken.PID != 4212 {
		t.Errorf("the displaced holder is %+v", broken)
	}
}

// The staleness window is generous on purpose: a machine under load, or one
// that was asleep, must not lose a live lock to a false positive.
func TestALiveLockIsRefreshedAndNeverJudgedAbandoned(t *testing.T) {
	path := guarded(t)
	lock, err := Acquire(path, Options{Stale: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	time.Sleep(500 * time.Millisecond)
	if _, err := Acquire(path, Options{Stale: 200 * time.Millisecond, Timeout: 50 * time.Millisecond}); !errors.Is(err, ErrBusy) {
		t.Fatalf("a refreshed lock was taken from under its holder: %v", err)
	}
}

// A process whose lock was broken must not delete the lock the next holder is
// now holding. It says it lost it instead.
func TestAHolderWhoseLockWasBrokenSaysSoAndDeletesNothing(t *testing.T) {
	path := guarded(t)
	first, err := Acquire(path, Options{Program: "first"})
	if err != nil {
		t.Fatal(err)
	}
	// Somebody broke it and took it: exactly what a stale takeover leaves behind.
	successor := Holder{SchemaVersion: SchemaVersion, PID: 999, Nonce: "0123456789abcdef",
		Program: "second", Since: time.Now()}
	raw, _ := json.Marshal(successor)
	if err := os.WriteFile(first.Path(), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := first.Release(); !errors.Is(err, ErrLost) {
		t.Fatalf("release: got %v, want ErrLost", err)
	}
	body, err := os.ReadFile(first.Path())
	if err != nil {
		t.Fatalf("the successor's lock was deleted: %v", err)
	}
	var still Holder
	if err := json.Unmarshal(body, &still); err != nil || still.Nonce != successor.Nonce {
		t.Errorf("the lock file no longer holds the successor's record: %s", body)
	}
	os.Remove(first.Path())
}

// The property the whole package exists for: whatever the interleaving, only
// one caller is inside the critical section at a time.
func TestOnlyOneWriterIsEverInsideTheCriticalSection(t *testing.T) {
	path := guarded(t)
	var inside, peak, entries int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 5; n++ {
				err := With(path, Options{Timeout: 20 * time.Second, Poll: time.Millisecond}, func() error {
					now := atomic.AddInt64(&inside, 1)
					for {
						high := atomic.LoadInt64(&peak)
						if now <= high || atomic.CompareAndSwapInt64(&peak, high, now) {
							break
						}
					}
					atomic.AddInt64(&entries, 1)
					time.Sleep(time.Millisecond)
					atomic.AddInt64(&inside, -1)
					return nil
				})
				if err != nil {
					t.Errorf("With: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&entries); got != 40 {
		t.Errorf("%d of 40 critical sections ran", got)
	}
	if got := atomic.LoadInt64(&peak); got != 1 {
		t.Errorf("%d callers were inside the critical section at once", got)
	}
}

// With releases whatever the body did, or one panicking caller holds the lock
// for a minute and every other instance reports a busy file.
func TestWithReleasesTheLockWhenTheBodyFails(t *testing.T) {
	path := guarded(t)
	boom := errors.New("boom")
	if err := With(path, Options{}, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("With returned %v", err)
	}
	if _, err := os.Stat(PathFor(path)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock survived a failing body: %v", err)
	}
}

// A read-only directory is reported here, before anything has been read into
// memory and half-changed.
func TestAnUnwritableDirectoryIsRefusedBeforeAnythingIsRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop a write on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	_, err := Acquire(filepath.Join(dir, "config.json"), Options{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("a lock was taken in a read-only directory")
	}
	if !contains(err.Error(), dir) {
		t.Errorf("the error does not name the directory: %v", err)
	}
}

// A lock file whose bytes are unreadable is still a lock. It is only ever
// broken on age — never because it failed to parse, which is a state a
// half-written file would be in.
func TestALockFileThatWillNotParseIsStillALock(t *testing.T) {
	path := guarded(t)
	if err := os.WriteFile(PathFor(path), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, Options{Timeout: 50 * time.Millisecond}); !errors.Is(err, ErrBusy) {
		t.Fatalf("an unparseable lock was ignored: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
