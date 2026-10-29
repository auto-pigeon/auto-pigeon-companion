//go:build unix

package selfupdate

import (
	"os"
	"path/filepath"
)

// renameDurable renames and then flushes both directories' entries, so the
// rename is on the disk before the next step depends on it.
func renameDurable(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	syncDir(filepath.Dir(from))
	if filepath.Dir(from) != filepath.Dir(to) {
		syncDir(filepath.Dir(to))
	}
	return nil
}

// syncDir flushes a directory's entries. Best effort: a filesystem that
// cannot fsync a directory has no other way to be asked.
func syncDir(dir string) {
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		handle.Close()
	}
}
