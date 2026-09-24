// Package fsshare holds the three file operations whose meaning differs on
// Windows: replacing a file, reading one, and telling a busy file from a
// broken one.
//
// On Linux and macOS a rename replaces its target even while somebody has it
// open, and a reader never sees anything but the old or the new file. Windows
// refuses both while another handle is open without FILE_SHARE_DELETE — and Go
// opens every file without it — answering ERROR_SHARING_VIOLATION or
// ERROR_ACCESS_DENIED (the latter also for a file whose deletion is pending).
// Those answers mean "busy for a moment", not "impossible": another goroutine
// or process is reading the job record, or releasing the lock. So Replace and
// ReadFile retry them for a bounded time and return every other error at once.
// Everywhere else they are exactly os.Rename and os.ReadFile.
package fsshare

import (
	"os"
	"time"
)

// Patience is how long Replace and ReadFile keep retrying a busy file. The
// holders are other readers and writers of the same small record, who keep it
// open for microseconds; a file busy for longer than this is reported.
const Patience = 2 * time.Second

// Replace renames from to to, replacing to, retrying while to is busy.
func Replace(from, to string) error {
	return retry(func() error { return os.Rename(from, to) })
}

// ReadFile reads path, retrying while it is busy.
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := retry(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

// Remove deletes path, retrying while it is busy. A file that is already gone
// is reported as os.Remove reports it.
func Remove(path string) error {
	return retry(func() error { return os.Remove(path) })
}

// IsBusy reports whether err is the operating system saying the file is in use
// by another handle right now — a reason to wait, never a reason to give up.
// Always false outside Windows.
func IsBusy(err error) bool { return isBusy(err) }

func retry(operation func() error) error {
	deadline := time.Now().Add(Patience)
	wait := time.Millisecond
	for {
		err := operation()
		if err == nil || !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(wait)
		if wait < 50*time.Millisecond {
			wait *= 2
		}
	}
}
