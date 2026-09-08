package binding

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/lockfile"
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

// Update is the only way this program changes a binding file in place.
//
// # Why atomic was not enough
//
// [SaveFile] stops a torn write. It does nothing about a lost update, and a
// binding file has two writers on any machine where the GUI server is running
// and somebody types a command: the server records a grant, `companion engine
// bind` records where an engine is, both read the file, both write it, and the
// second rename silently discards the first change. On `config.json` that costs
// a setting. Here it costs an approval — or, worse, resurrects one somebody
// withdrew a moment ago.
//
// So mutate is handed the set as it is on disk NOW, inside the cross-process
// lock, rather than whatever the caller read when it started. The signature
// makes each call site declare the binding it is changing instead of writing
// back a whole set it read minutes ago. See internal/lockfile for why the lock
// is a file and internal/config for the same shape over the configuration.
func Update(path string, mutate func(*Set) error) (*Set, error) {
	var result *Set
	err := lockfile.With(path, lockfile.Options{Program: "auto-pigeon-companion"}, func() error {
		current, err := LoadFile(path)
		if err != nil && !errors.Is(err, ErrNoFile) {
			return err
		}
		if mutate != nil {
			if err := mutate(current); err != nil {
				return err
			}
		}
		if err := SaveFile(path, current); err != nil {
			return err
		}
		result = current
		return nil
	})
	return result, err
}
