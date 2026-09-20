package playrun

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound reports a run id nothing on this machine knows.
var ErrNotFound = errors.New("playrun: no such run")

// Store holds run records on disk.
//
// One file per run, written whole and renamed into place. A half-written record
// would be a record a reload could not parse, and the whole point of the record
// is that a reload recovers the same run.
type Store struct {
	root string
	mu   sync.Mutex
}

// OpenStore prepares a store under root.
func OpenStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("playrun: a store needs a directory")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("playrun: preparing %s: %w", root, err)
	}

	return &Store{root: root}, nil
}

// Root is where this store lives.
func (s *Store) Root() string { return s.root }

// NewID is a sortable run id: the time it was created, then eight random bytes.
//
// Sortable so a directory listing is in chronological order without reading
// every file; random so two runs started in the same second are two runs.
func NewID(now time.Time) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("playrun: naming a run: %w", err)
	}

	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix), nil
}

// validID refuses anything that is not an id this store wrote. The id reaches
// the store from an HTTP path, so it is checked before it becomes a filename.
func validID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}

	return true
}

func (s *Store) path(id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("%w: %q is not a run id", ErrNotFound, id)
	}

	return filepath.Join(s.root, id+".json"), nil
}

// Save writes a record whole.
func (s *Store) Save(record *Record) error {
	path, err := s.path(record.ID)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("playrun: encoding run %s: %w", record.ID, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	temporary, err := os.CreateTemp(s.root, "run-")
	if err != nil {
		return fmt.Errorf("playrun: writing run %s: %w", record.ID, err)
	}
	name := temporary.Name()
	_, writeErr := temporary.Write(encoded)
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(name)

		return fmt.Errorf("playrun: writing run %s: %w", record.ID, errors.Join(writeErr, closeErr))
	}
	if err = os.Rename(name, path); err != nil {
		_ = os.Remove(name)

		return fmt.Errorf("playrun: writing run %s: %w", record.ID, err)
	}

	return nil
}

// Load reads one record.
func (s *Store) Load(id string) (*Record, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("playrun: reading run %s: %w", id, err)
	}
	record := &Record{}
	if err = json.Unmarshal(raw, record); err != nil {
		return nil, fmt.Errorf("playrun: reading run %s: %w", id, err)
	}
	if record.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("playrun: run %s is a %q record and this Companion writes %q",
			id, record.SchemaVersion, SchemaVersion)
	}

	return record, nil
}

// List reads every record, newest first.
func (s *Store) List() ([]*Record, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("playrun: reading %s: %w", s.root, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		names = append(names, strings.TrimSuffix(name, ".json"))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	out := make([]*Record, 0, len(names))
	for _, id := range names {
		record, loadErr := s.Load(id)
		if loadErr != nil {
			// A record this build cannot read is skipped rather than fatal: a
			// downgrade must not make the Activity list unopenable.
			continue
		}
		out = append(out, record)
	}

	return out, nil
}
