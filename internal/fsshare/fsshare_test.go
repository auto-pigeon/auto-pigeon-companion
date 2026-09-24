package fsshare

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
)

// Many writers replacing one record while many readers read it: every write
// lands and every read sees a whole record, on every platform. On Windows this
// is exactly the traffic that made a bare os.Rename fail.
func TestReplaceAndReadSurviveConcurrentReadersAndWriters(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "record.json")
	if err := os.WriteFile(target, []byte("0000"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for writer := 0; writer < 8; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				temp, err := os.CreateTemp(dir, ".write-*")
				if err != nil {
					errs <- err
					return
				}
				temp.WriteString("abcd")
				temp.Close()
				if err := Replace(temp.Name(), target); err != nil {
					errs <- err
					return
				}
			}
		}(writer)
	}
	for reader := 0; reader < 8; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				data, err := ReadFile(target)
				if err != nil {
					errs <- err
					return
				}
				if len(data) != 4 {
					errs <- errors.New("a reader saw a partial record: " + string(data))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestOnlyASharingAnswerIsBusy(t *testing.T) {
	if IsBusy(nil) || IsBusy(os.ErrNotExist) || IsBusy(errors.New("x")) {
		t.Fatal("an ordinary error was called busy")
	}
	wrapped := &os.PathError{Op: "open", Path: "x", Err: syscall.Errno(32)}
	if got, want := IsBusy(wrapped), runtime.GOOS == "windows"; got != want {
		t.Fatalf("IsBusy(sharing violation) = %v on %s, want %v", got, runtime.GOOS, want)
	}
	// A missing file is never retried: ReadFile answers at once.
	if _, err := ReadFile(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadFile(absent) = %v", err)
	}
}
