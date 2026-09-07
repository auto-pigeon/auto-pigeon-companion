package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Getting built content somewhere the engine will look for it.
//
// A Quake engine loads content from a directory beside the base game, chosen
// with `-game`. A map project lives somewhere else entirely — in the user's
// own folders, under version control, on another drive — and `-game` does not
// take a path outside the game root. So something has to put a copy where the
// engine looks, and take it away afterwards.
//
// The dangerous half is the taking away. "Delete the mod directory" is the
// obvious implementation and it is how a program eventually deletes a directory
// somebody had their own work in — a directory the Companion staged into once,
// six months ago, and which has had files added to it by hand ever since. So
// staging writes down every file it wrote and the digest of each one, and
// cleanup removes exactly those, skipping any that have changed. What the
// Companion did not put there, it does not remove.

// StampName is the file a staged directory is recognised by. It is the record
// of what was staged, and its absence is what makes a directory not ours.
const StampName = ".auto-pigeon-staged.json"

// StampSchemaVersion versions that record.
const StampSchemaVersion = "aucom.staging/1.0"

// reservedGameDirs are the directories a Quake installation already owns.
//
// Refused as staging targets, and this is the single most important rule in the
// file: staging over `id1` would overwrite the base game a user paid for with
// whatever their project happens to contain, and the cleanup that followed
// would then remove it.
var reservedGameDirs = []string{"id1", "qw", "hipnotic", "rogue", "dopa", "rerelease"}

// StagedFile is one file the Companion wrote, and what it wrote.
type StagedFile struct {
	// Path is relative to the staged directory, POSIX-spelled.
	Path string `json:"path"`
	Size int64  `json:"size"`
	// SHA256 is what was written. Cleanup compares against it, so a file that
	// somebody changed afterwards is left alone rather than silently removed.
	SHA256 string `json:"sha256"`
}

// Stamp is the record left in a staged directory.
type Stamp struct {
	SchemaVersion string `json:"schema_version"`
	// ModName is the game directory's name, echoed so a stamp found on its own
	// still says what it belongs to.
	ModName string `json:"mod_name"`
	// CreatedDir records whether the Companion made the directory. When it did
	// not, cleanup leaves the directory itself alone.
	CreatedDir bool `json:"created_dir"`
	// Dirs are the subdirectories created, deepest last.
	Dirs  []string     `json:"dirs,omitempty"`
	Files []StagedFile `json:"files"`
	// ProfileID and JobID are provenance: which launch put this here.
	ProfileID string    `json:"profile_id,omitempty"`
	StagedAt  time.Time `json:"staged_at"`
}

// Staging is one request to put content into a game directory.
type Staging struct {
	// GameRoot is the directory that contains the base game.
	GameRoot string
	// ModName is the game directory to create beside it, and the value the
	// engine is given for `-game`.
	ModName string
	// Source is the directory whose contents are copied in. Its own name is
	// not part of the destination: what is staged is what is *inside* it.
	Source string
	// ProfileID is recorded in the stamp. Optional.
	ProfileID string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// ErrOccupied reports a target directory the Companion did not create.
var ErrOccupied = errors.New("engine: the game directory already exists and the Companion did not stage it")

// CheckModName rejects a game directory name that is not one path segment, or
// that names something an installation already owns.
func CheckModName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("engine: the game directory needs a name; it is what the engine is given for -game")
	case len(name) > 64:
		return fmt.Errorf("engine: %q is %d bytes; a game directory name is at most 64", name, len(name))
	case name == "." || name == "..":
		return fmt.Errorf("engine: %q is not a directory name", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("engine: %q contains a path separator; a game directory is one name beside the base game, not a path", name)
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.') {
			return fmt.Errorf("engine: %q contains %q; a game directory name may contain letters, digits, `.`, `_` and `-`", name, string(ch))
		}
	}
	for _, reserved := range reservedGameDirs {
		if strings.EqualFold(name, reserved) {
			return fmt.Errorf("engine: %q is a directory a Quake installation already owns; staging into it would overwrite the game and then remove it. Choose a name of your own", name)
		}
	}
	return nil
}

func (s Staging) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Dir is where the content will be staged.
func (s Staging) Dir() string { return filepath.Join(s.GameRoot, s.ModName) }

// Plan lists what staging would copy, without copying anything.
//
// The same walk the copy does, so a preview cannot disagree with the thing it
// previews.
func (s Staging) Plan() ([]string, error) {
	if err := CheckModName(s.ModName); err != nil {
		return nil, err
	}
	entries, err := walkSource(s.Source)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.rel)
	}
	return out, nil
}

// Stage copies the source into the game directory and writes the stamp.
//
// An existing directory the Companion staged before is re-staged: its previous
// files are removed first, so a map renamed between two runs does not leave the
// old one behind for the engine to load instead. An existing directory the
// Companion did not stage is refused; there is no flag to force it, because the
// value of the rule is that it has no exception.
func (s Staging) Stage() (*Stamp, error) {
	if err := CheckModName(s.ModName); err != nil {
		return nil, err
	}
	if s.GameRoot == "" {
		return nil, fmt.Errorf("engine: staging %q needs a game root; nothing on this machine says where the game is", s.ModName)
	}
	source, err := os.Stat(s.Source)
	if err != nil {
		return nil, fmt.Errorf("engine: staging %q: %w", s.ModName, err)
	}
	if !source.IsDir() {
		return nil, fmt.Errorf("engine: staging %q: %s is a file; what is staged is the contents of a directory", s.ModName, s.Source)
	}

	target := s.Dir()
	created := false
	switch info, err := os.Lstat(target); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(target, 0o755); err != nil {
			return nil, fmt.Errorf("engine: creating %s: %w", target, err)
		}
		created = true
	case err != nil:
		return nil, fmt.Errorf("engine: staging %q: %w", s.ModName, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("engine: %s is a symbolic link; the Companion stages into directories it can account for, not through links: %w", target, ErrOccupied)
	case !info.IsDir():
		return nil, fmt.Errorf("engine: %s is a file, not a game directory: %w", target, ErrOccupied)
	default:
		previous, err := ReadStamp(target)
		if err != nil {
			return nil, fmt.Errorf("engine: %s exists. Nothing there says the Companion staged it, and it will not write into a directory whose contents are somebody else's: %w", target, ErrOccupied)
		}
		if _, err := removeStamped(target, previous); err != nil {
			return nil, err
		}
		created = previous.CreatedDir
	}

	entries, err := walkSource(s.Source)
	if err != nil {
		return nil, err
	}
	stamp := &Stamp{
		SchemaVersion: StampSchemaVersion,
		ModName:       s.ModName,
		CreatedDir:    created,
		ProfileID:     s.ProfileID,
		StagedAt:      s.now(),
	}
	madeDirs := map[string]bool{}
	for _, entry := range entries {
		destination := filepath.Join(target, filepath.FromSlash(entry.rel))
		if parent := filepath.Dir(entry.rel); parent != "." {
			for _, dir := range ancestors(parent) {
				if madeDirs[dir] {
					continue
				}
				madeDirs[dir] = true
				if err := os.MkdirAll(filepath.Join(target, filepath.FromSlash(dir)), 0o755); err != nil {
					return nil, fmt.Errorf("engine: creating %s: %w", dir, err)
				}
				stamp.Dirs = append(stamp.Dirs, dir)
			}
		}
		digest, size, err := copyFile(entry.path, destination)
		if err != nil {
			return nil, err
		}
		stamp.Files = append(stamp.Files, StagedFile{Path: entry.rel, Size: size, SHA256: digest})
	}
	sort.Strings(stamp.Dirs)
	if err := writeStamp(target, stamp); err != nil {
		return nil, err
	}
	return stamp, nil
}

// Unstage removes exactly what staging put in a game directory.
//
// Files that have changed since they were staged are left where they are and
// named in the returned list. That is the whole safety property: a user who
// edited a staged file, or dropped their own file in beside it, keeps it.
func Unstage(gameRoot, modName string) (kept []string, err error) {
	if err := CheckModName(modName); err != nil {
		return nil, err
	}
	target := filepath.Join(gameRoot, modName)
	stamp, err := ReadStamp(target)
	if err != nil {
		return nil, err
	}
	kept, err = removeStamped(target, stamp)
	if err != nil {
		return kept, err
	}
	if err := os.Remove(filepath.Join(target, StampName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return kept, fmt.Errorf("engine: removing the staging record: %w", err)
	}
	if stamp.CreatedDir && len(kept) == 0 {
		// Remove, not RemoveAll: it succeeds only if the directory is empty,
		// which is the check and not a step before it.
		if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return kept, nil // Something else is in there; leaving it is right.
		}
	}
	return kept, nil
}

// removeStamped deletes the files a stamp lists, skipping any whose contents
// have changed, then the directories it created if they came out empty.
func removeStamped(target string, stamp *Stamp) (kept []string, err error) {
	for _, file := range stamp.Files {
		path := filepath.Join(target, filepath.FromSlash(file.Path))
		digest, _, err := digestFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // Already gone: the outcome the caller wanted.
		case err != nil:
			return kept, fmt.Errorf("engine: reading %s before removing it: %w", path, err)
		case digest != file.SHA256:
			kept = append(kept, file.Path)
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return kept, fmt.Errorf("engine: removing %s: %w", path, err)
		}
	}
	// Deepest first, so a directory's children are gone before it is tried.
	dirs := append([]string{}, stamp.Dirs...)
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		// Remove rather than RemoveAll, again: a directory with anything left
		// in it stays, and what is left in it is somebody else's.
		_ = os.Remove(filepath.Join(target, filepath.FromSlash(dir)))
	}
	sort.Strings(kept)
	return kept, nil
}

// ReadStamp reads a staged directory's record. A directory with no stamp is
// not one the Companion staged, and that is what the error says.
func ReadStamp(dir string) (*Stamp, error) {
	raw, err := os.ReadFile(filepath.Join(dir, StampName))
	if err != nil {
		return nil, fmt.Errorf("engine: %s has no staging record: %w", dir, err)
	}
	var stamp Stamp
	if err := json.Unmarshal(raw, &stamp); err != nil {
		return nil, fmt.Errorf("engine: the staging record in %s is unreadable: %w", dir, err)
	}
	if stamp.SchemaVersion != StampSchemaVersion {
		return nil, fmt.Errorf("engine: the staging record in %s is %q; this build writes %s", dir, stamp.SchemaVersion, StampSchemaVersion)
	}
	return &stamp, nil
}

func writeStamp(dir string, stamp *Stamp) error {
	encoded, err := json.MarshalIndent(stamp, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, StampName)
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("engine: writing the staging record: %w", err)
	}
	return nil
}

// sourceEntry is one file to stage.
type sourceEntry struct {
	// path is where it is now; rel is where it goes, POSIX-spelled.
	path string
	rel  string
}

// walkSource lists the regular files under a directory.
//
// Symbolic links are refused rather than followed or skipped. A link inside
// staged content is either a way out of the directory or a file that will not
// be there when the engine reads it, and neither is something to copy quietly.
func walkSource(source string) ([]sourceEntry, error) {
	var out []sourceEntry
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("engine: %s is a symbolic link; staged content is copied, and a link is not something that can be copied honestly", path)
		case entry.IsDir():
			return nil
		case !entry.Type().IsRegular():
			return fmt.Errorf("engine: %s is not a regular file", path)
		case filepath.Base(path) == StampName:
			// A source that already contains a stamp would produce a staged
			// directory claiming to have staged itself.
			return nil
		}
		out = append(out, sourceEntry{path: path, rel: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

// ancestors lists a relative directory's path prefixes, shallowest first.
func ancestors(dir string) []string {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	out := make([]string, 0, len(parts))
	for i := range parts {
		out = append(out, strings.Join(parts[:i+1], "/"))
	}
	return out
}

func copyFile(from, to string) (digest string, size int64, err error) {
	source, err := os.Open(from)
	if err != nil {
		return "", 0, fmt.Errorf("engine: reading %s: %w", from, err)
	}
	defer source.Close()

	destination, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, fmt.Errorf("engine: writing %s: %w", to, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, hash), source)
	closeErr := destination.Close()
	if copyErr != nil {
		return "", 0, fmt.Errorf("engine: writing %s: %w", to, copyErr)
	}
	if closeErr != nil {
		return "", 0, fmt.Errorf("engine: writing %s: %w", to, closeErr)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), written, nil
}

func digestFile(path string) (digest string, size int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), written, nil
}
