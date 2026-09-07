package acquire

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

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// InstallSchemaVersion versions the record written beside a cache entry.
const InstallSchemaVersion = "aucom.install/1.0"

const (
	entriesDir  = "entries"
	stagingDir  = "staging"
	payloadDir  = "files"
	installFile = "install.json"
)

// FileRecord is one extracted file, as it was when it was installed.
//
// The mode is recorded as well as the digest because "the same bytes, now
// executable" is a real change and a hash alone would not see it.
type FileRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"`
}

// Install is everything known about one cache entry, written when it is created
// and never afterwards.
//
// It is the reason offline use needs no catalogue: every fact a decision
// depends on — which digest, which signer, which catalogue serial, which
// licence — was established at install time and is recorded here. A caller
// re-deriving them from a catalogue it fetched later would be answering a
// different question, because the catalogue can have changed.
type Install struct {
	SchemaVersion string `json:"schema_version"`
	// Digest is the artifact's, and it names this entry's directory.
	Digest string `json:"digest"`

	PackageID string `json:"package_id"`
	Version   string `json:"version"`
	Name      string `json:"name"`
	Program   string `json:"program,omitempty"`

	Platform     profile.Platform `json:"platform"`
	Kind         string           `json:"kind"`
	Size         int64            `json:"size"`
	UnpackedSize int64            `json:"unpacked_size,omitempty"`

	License            profile.License `json:"license"`
	RequiresAcceptance bool            `json:"requires_acceptance,omitempty"`
	Source             profile.Source  `json:"source"`
	// Aggregation is [catalog.Aggregation], copied in so that a record read on
	// its own still says what the downloaded program's relationship to this one
	// is.
	Aggregation string `json:"aggregation"`

	// Who vouched, and for which published state of the world.
	Signer        string `json:"signer"`
	CatalogID     string `json:"catalog_id"`
	CatalogSerial int64  `json:"catalog_serial"`
	CatalogDigest string `json:"catalog_digest"`
	KeyringDigest string `json:"keyring_digest"`
	// SourceURL is where it came from, already redacted: scheme, host and path
	// and nothing else. A pre-signed URL's query string is a credential, and a
	// record on disk is a place credentials do not go.
	SourceURL string `json:"source_url"`

	InstalledAt time.Time `json:"installed_at"`
	// Root is the directory, relative to the entry's `files/`, that becomes
	// the profile's `tool_root`.
	Root string `json:"root,omitempty"`
	// Executables are the paths, relative to `files/`, that this entry
	// declared executable. They are what is re-checked on every use.
	Executables []string     `json:"executables"`
	Files       []FileRecord `json:"files"`
}

// ToolRoot is the absolute directory a profile's own executable paths resolve
// under. It is what a managed download actually delivers: the catalogue says
// where the bytes are and which directory inside them is the install, and the
// profile says what is in it.
func (i *Install) ToolRoot(entryPath string) string {
	if i.Root == "" {
		return filepath.Join(entryPath, payloadDir)
	}
	return filepath.Join(entryPath, payloadDir, filepath.FromSlash(i.Root))
}

// ErrNotInstalled reports that no cache entry has a digest.
var ErrNotInstalled = errors.New("acquire: not installed")

// ErrTampered reports that a cache entry no longer matches its install record.
// Its own error because it is not a cache miss: something changed a file this
// program was going to execute, and re-downloading over it silently would erase
// the only evidence.
var ErrTampered = errors.New("acquire: a cached file has changed since it was installed")

// Cache is the content-addressed store of downloaded packages.
//
// Addressed by the artifact's digest, not by name and version, and that is what
// makes every other property work. Two pinned versions of the same tool are two
// directories that cannot collide. A rebuilt release published under the same
// version number is a different entry, so it cannot silently replace the one a
// user reviewed. And an entry's identity is checkable from the entry itself,
// which is what makes tamper detection possible at all.
type Cache struct {
	root string
}

// OpenCache prepares a cache rooted at dir.
func OpenCache(dir string) (*Cache, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("acquire: the cache needs a directory")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("acquire: resolving the cache directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absolute, entriesDir), 0o700); err != nil {
		return nil, fmt.Errorf("acquire: creating %s: %w", absolute, err)
	}
	if err := os.MkdirAll(filepath.Join(absolute, stagingDir), 0o700); err != nil {
		return nil, fmt.Errorf("acquire: creating %s: %w", absolute, err)
	}
	return &Cache{root: absolute}, nil
}

// Root is the directory the cache lives in.
func (c *Cache) Root() string { return c.root }

// entryName is the directory name for a digest. `sha256:<hex>` is not a
// filename on Windows, so the colon becomes a dash and the algorithm stays
// visible.
func entryName(digest string) (string, error) {
	algorithm, hexDigest, ok := strings.Cut(digest, ":")
	if !ok || algorithm != "sha256" || len(hexDigest) != 64 {
		return "", fmt.Errorf("acquire: %q is not a sha256:<hex> digest", digest)
	}
	if _, err := hex.DecodeString(hexDigest); err != nil {
		return "", fmt.Errorf("acquire: %q is not a sha256:<hex> digest", digest)
	}
	return algorithm + "-" + hexDigest, nil
}

// EntryPath is the directory holding one cache entry.
func (c *Cache) EntryPath(digest string) (string, error) {
	name, err := entryName(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.root, entriesDir, name), nil
}

// Has reports whether an entry exists at all. It says nothing about whether its
// contents still match — that is [Cache.Use] and [Cache.VerifyEntry].
func (c *Cache) Has(digest string) bool {
	path, err := c.EntryPath(digest)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(path, installFile))
	return err == nil
}

// Load reads an entry's install record.
func (c *Cache) Load(digest string) (*Install, error) {
	path, err := c.EntryPath(digest)
	if err != nil {
		return nil, err
	}
	return readInstall(filepath.Join(path, installFile))
}

func readInstall(path string) (*Install, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotInstalled, filepath.Base(filepath.Dir(path)))
		}
		return nil, fmt.Errorf("acquire: reading %s: %w", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var install Install
	if err := decoder.Decode(&install); err != nil {
		return nil, fmt.Errorf("acquire: reading %s: %w", path, err)
	}
	if install.SchemaVersion != InstallSchemaVersion {
		return nil, fmt.Errorf("acquire: %s is %q; this build reads %q", path, install.SchemaVersion, InstallSchemaVersion)
	}
	return &install, nil
}

// List returns every entry, newest first.
func (c *Cache) List() ([]*Install, error) {
	entries, err := os.ReadDir(filepath.Join(c.root, entriesDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("acquire: listing %s: %w", c.root, err)
	}
	var installs []*Install
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		install, err := readInstall(filepath.Join(c.root, entriesDir, entry.Name(), installFile))
		if err != nil {
			if errors.Is(err, ErrNotInstalled) {
				// A staging directory another process is filling in right now,
				// or the remains of one. Not an error.
				continue
			}
			return nil, err
		}
		installs = append(installs, install)
	}
	sort.Slice(installs, func(i, j int) bool {
		if installs[i].InstalledAt.Equal(installs[j].InstalledAt) {
			return installs[i].Digest < installs[j].Digest
		}
		return installs[i].InstalledAt.After(installs[j].InstalledAt)
	})
	return installs, nil
}

// Use returns an entry's `tool_root`, having first checked that the files it
// declares executable are still the files that were installed.
//
// The check is on every use, not only on install, and it covers what is about
// to be executed. That is the difference between "this was verified once" and
// "this is verified": a cache lives in a directory the user's own account can
// write to, and so can everything else running as that user.
//
// It re-hashes the declared executables rather than every file. A full re-hash
// of a toolchain on every build would be slow enough that somebody would turn
// it off, and the declared executables are the files whose contents this
// program is about to hand to the operating system. [Cache.VerifyEntry] checks
// everything, and is what `companion acquire verify` runs.
func (c *Cache) Use(digest string) (*Install, string, error) {
	install, err := c.Load(digest)
	if err != nil {
		return nil, "", err
	}
	path, err := c.EntryPath(digest)
	if err != nil {
		return nil, "", err
	}
	if err := c.verifyPaths(install, path, install.Executables); err != nil {
		return nil, "", err
	}
	return install, install.ToolRoot(path), nil
}

// VerifyPaths re-checks specific files inside an entry, relative to its
// `tool_root`.
//
// It exists because the catalogue and the profile know different halves: the
// catalogue declares which files must be executable, and the profile declares
// which of them this action is about to run. A profile that names a file the
// catalogue did not is exactly the case worth checking, and it is checked here.
func (c *Cache) VerifyPaths(digest string, relative []string) error {
	install, err := c.Load(digest)
	if err != nil {
		return err
	}
	path, err := c.EntryPath(digest)
	if err != nil {
		return err
	}
	rooted := make([]string, 0, len(relative))
	for _, name := range relative {
		if install.Root == "" {
			rooted = append(rooted, name)
			continue
		}
		rooted = append(rooted, install.Root+"/"+name)
	}
	return c.verifyPaths(install, path, rooted)
}

// verifyPaths checks a set of paths relative to the entry's `files/` directory
// against the install record.
func (c *Cache) verifyPaths(install *Install, entryPath string, paths []string) error {
	files := make(map[string]FileRecord, len(install.Files))
	for _, file := range install.Files {
		files[file.Path] = file
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, relative := range sorted {
		record, known := files[relative]
		if !known {
			return fmt.Errorf("%w: %s is not in the install record for %s %s",
				ErrTampered, relative, install.PackageID, install.Version)
		}
		if err := verifyFile(filepath.Join(entryPath, payloadDir, filepath.FromSlash(relative)), record); err != nil {
			return err
		}
	}
	return nil
}

// VerifyEntry re-hashes everything in an entry and reports the first file that
// has changed, gone missing or appeared.
func (c *Cache) VerifyEntry(digest string) (*Install, error) {
	install, err := c.Load(digest)
	if err != nil {
		return nil, err
	}
	path, err := c.EntryPath(digest)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(path, payloadDir)
	expected := make(map[string]FileRecord, len(install.Files))
	for _, file := range install.Files {
		expected[file.Path] = file
		if err := verifyFile(filepath.Join(root, filepath.FromSlash(file.Path)), file); err != nil {
			return nil, err
		}
	}
	// An added file matters as much as a changed one: a directory on the
	// toolchain's own path is a fine place to leave something.
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if _, known := expected[filepath.ToSlash(relative)]; !known {
			return fmt.Errorf("%w: %s appeared in %s %s after it was installed",
				ErrTampered, filepath.ToSlash(relative), install.PackageID, install.Version)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return install, nil
}

func verifyFile(path string, record FileRecord) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s is gone", ErrTampered, path)
		}
		return fmt.Errorf("acquire: checking %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is no longer a regular file", ErrTampered, path)
	}
	if info.Size() != record.Size {
		return fmt.Errorf("%w: %s is %d bytes and was installed at %d", ErrTampered, path, info.Size(), record.Size)
	}
	if runtimeModeMatters() && uint32(info.Mode().Perm()) != record.Mode {
		return fmt.Errorf("%w: %s is mode %04o and was installed as %04o",
			ErrTampered, path, info.Mode().Perm(), fs.FileMode(record.Mode).Perm())
	}
	digest, err := digestFile(path)
	if err != nil {
		return err
	}
	if digest != record.SHA256 {
		return fmt.Errorf("%w: %s has different contents from the ones that were installed", ErrTampered, path)
	}
	return nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("acquire: reading %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("acquire: reading %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// stage creates a private staging directory for an install in progress.
func (c *Cache) stage() (string, error) {
	dir, err := os.MkdirTemp(filepath.Join(c.root, stagingDir), "install-")
	if err != nil {
		return "", fmt.Errorf("acquire: creating a staging directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, payloadDir), 0o700); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("acquire: creating a staging directory: %w", err)
	}
	return dir, nil
}

// commit writes the install record into a staged directory and renames the
// whole thing into place.
//
// One rename, and everything the entry needs is inside the directory before it
// happens. That is what makes a concurrent install safe without a lock: two
// processes stage independently and one rename wins. The loser finds the entry
// already there, checks it, and uses it — which is the correct outcome, because
// both were installing the same bytes: the directory is named after the digest.
func (c *Cache) commit(staged string, install *Install) (string, error) {
	install.SchemaVersion = InstallSchemaVersion
	encoded, err := json.MarshalIndent(install, "", "  ")
	if err != nil {
		return "", fmt.Errorf("acquire: encoding the install record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staged, installFile), append(encoded, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("acquire: writing the install record: %w", err)
	}
	final, err := c.EntryPath(install.Digest)
	if err != nil {
		return "", err
	}
	if err := os.Rename(staged, final); err != nil {
		if c.Has(install.Digest) {
			// Somebody else got there first with the same bytes.
			os.RemoveAll(staged)
			return final, nil
		}
		return "", fmt.Errorf("acquire: installing %s: %w", install.PackageID, err)
	}
	return final, nil
}

// Remove deletes one entry.
func (c *Cache) Remove(digest string) error {
	path, err := c.EntryPath(digest)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("acquire: removing %s: %w", path, err)
	}
	return nil
}

// CleanStaging removes staging directories left behind by an interrupted
// install.
//
// Safe at any time and against any concurrent installer, because a staged
// directory is only ever renamed *out* of here: an installer that is still
// working owns a directory this will delete under it, so the age bound is what
// keeps that from happening. An interrupted download leaves nothing behind that
// is executable — the staging area is never on anybody's path — so this is
// disk hygiene, not a repair.
func (c *Cache) CleanStaging(olderThan time.Duration, now time.Time) ([]string, error) {
	dir := filepath.Join(c.root, stagingDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("acquire: listing %s: %w", dir, err)
	}
	var removed []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < olderThan {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return removed, fmt.Errorf("acquire: removing %s: %w", path, err)
		}
		removed = append(removed, entry.Name())
	}
	return removed, nil
}

// runtimeModeMatters reports whether file permission bits are meaningful here.
// On Windows they are not: Go reports a synthesised mode, and comparing it
// against what was recorded on another platform, or after a copy, is a false
// tamper report.
func runtimeModeMatters() bool { return goos() != "windows" }
