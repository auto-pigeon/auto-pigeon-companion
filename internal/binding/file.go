package binding

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Reading and writing the binding store as a file.
//
// Separate from binding.go, which knows the format and nothing about where it
// lives, so a caller that has bytes — a test, a future sync — never has to have
// a path.

// ErrNoFile reports that no binding file exists yet. A machine where nothing
// has been installed has none, and that is not a failure.
var ErrNoFile = errors.New("binding: no binding file")

// LoadFile reads a binding store. A missing file returns an empty set and
// ErrNoFile, so a caller that does not care about the distinction can ignore
// the error and use the value.
func LoadFile(path string) (*Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NewSet(), ErrNoFile
		}
		return NewSet(), fmt.Errorf("binding: reading %s: %w", path, err)
	}
	return Load(raw)
}

// SaveFile writes a binding store atomically.
//
// 0600 and atomic for the same reason config.json is: the file records what a
// user approved, and a half-written one is a set of grants nothing can
// honestly enforce.
func SaveFile(path string, set *Set) error {
	encoded, err := set.Marshal()
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("binding: creating %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, "bindings-*.json")
	if err != nil {
		return fmt.Errorf("binding: creating a temporary file in %s: %w", dir, err)
	}
	name := temp.Name()
	defer os.Remove(name) // No-op once the rename below has succeeded.

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("binding: securing %s: %w", name, err)
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("binding: writing %s: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("binding: closing %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("binding: replacing %s: %w", path, err)
	}
	return nil
}

// Lookup returns a function shaped for the job service: the binding for a
// profile id, read from a file each time it is asked.
//
// Re-read rather than cached, because the file records what a user approved and
// a grant added in another window has to take effect in this one. A stat and a
// small parse per job submission is not a cost worth caching away.
func Lookup(path string) func(string) (LocalBinding, bool) {
	return func(profileID string) (LocalBinding, bool) {
		set, err := LoadFile(path)
		if err != nil {
			return LocalBinding{}, false
		}
		return set.Find(profileID)
	}
}
