package texturebundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReceiptName is the record a published entry carries.
const ReceiptName = "bundle.json"

// stagingDir is where an extraction lives until it is published.
//
// One directory for every map, so nothing about a bundle's identity — not even
// which map it claims to belong to — is written into a path before the manifest
// has been read and checked.
const stagingDir = ".staging"

// ReceiptSchema versions that record.
const ReceiptSchema = "aucom.texture-bundle/1.0"

// Receipt is what a verified cache entry knows about itself.
//
// It is written LAST, inside the temporary directory, and the entry is
// published by renaming that directory into place. So a directory that exists
// without a readable receipt is an interrupted extraction and is treated as
// absent — there is no state in which a half-written bundle is mistaken for a
// verified one.
type Receipt struct {
	SchemaVersion string `json:"schema_version"`

	// BundleDigest is the SHA-256 of the ZIP as it arrived: the immutable
	// identity this entry is cached under.
	BundleDigest string `json:"bundle_digest"`

	MapID    string `json:"map_id"`
	Revision int    `json:"revision"`
	Game     string `json:"game,omitempty"`

	// ManifestSchema is the bundle's own schema version, recorded so an entry
	// written by an older Companion is recognisable rather than re-read.
	ManifestSchema string `json:"manifest_schema"`

	// WADsDeclared is the map's declaration order, copied so the record is
	// readable without re-parsing the manifest.
	WADsDeclared []string `json:"wads_declared,omitempty"`

	CompilerReady    bool     `json:"compiler_ready"`
	CompilerRefusals []string `json:"compiler_refusals,omitempty"`

	Files      []Member  `json:"files"`
	TotalBytes int64     `json:"total_bytes"`
	Licenses   bool      `json:"licenses"`
	VerifiedAt time.Time `json:"verified_at"`
}

// Entry is one verified bundle on this machine.
type Entry struct {
	// Dir is the entry directory: the receipt, the manifest, the licences.
	Dir string
	// ContentRoot is the read-only directory of original files, and is the
	// path a build is given for the `content_root` role.
	ContentRoot string

	Receipt  Receipt
	Manifest Manifest
}

// Digest is the entry's immutable identity.
func (e Entry) Digest() string { return e.Receipt.BundleDigest }

// CompilerReady reports whether this bundle plus the map is enough to compile.
func (e Entry) CompilerReady() bool { return e.Receipt.CompilerReady }

// Cache holds verified bundles, keyed by map, revision and bundle digest.
//
// Keyed by all three rather than by the digest alone because the question a
// caller asks is "do I already have the textures for THIS map at THIS
// revision", and answering it must not require having first downloaded the
// bundle to learn its digest. The digest is still in the key, so two different
// bundles for one revision — which AUB's exporter makes impossible and a proxy
// in front of it does not — are two entries rather than one overwriting the
// other.
type Cache struct {
	root   string
	limits Limits
	now    func() time.Time
}

// Open prepares a cache under root.
func Open(root string) (*Cache, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("texturebundle: a cache needs a directory")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("texturebundle: preparing %s: %w", root, err)
	}

	return &Cache{root: root, limits: DefaultLimits(), now: func() time.Time { return time.Now().UTC() }}, nil
}

// Root is where this cache lives.
func (c *Cache) Root() string { return c.root }

// SetLimits replaces the extraction bounds. For tests, and for nothing else:
// there is no user-facing option that widens them.
func (c *Cache) SetLimits(limits Limits) { c.limits = limits }

// SetClock replaces the clock. For tests.
func (c *Cache) SetClock(now func() time.Time) { c.now = now }

// revisionDir is where every entry for one map revision lives.
func (c *Cache) revisionDir(mapID string, revision int) (string, error) {
	if !safeSegment(mapID) {
		return "", fmt.Errorf("texturebundle: %q is not usable as a directory name", mapID)
	}
	if revision < 0 {
		return "", fmt.Errorf("texturebundle: %d is not a revision", revision)
	}

	return filepath.Join(c.root, mapID, "r"+strconv.Itoa(revision)), nil
}

// safeSegment refuses an id that is not one plain path segment.
//
// The id comes from a server. Nothing about it is this program's to trust, and
// a `..` in it would put a cache entry somewhere else entirely.
func safeSegment(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}

	return true
}

// Lookup finds a verified entry for one map revision, re-checking it.
//
// Re-checked rather than trusted: a cache entry is a directory on a disk the
// user also owns, and an hour after it was written a file in it may have been
// edited, truncated or replaced. Offline reuse is only honest if what is reused
// is verified at the moment it is reused — which is the whole of "offline reuse
// is allowed only for a bundle whose recorded map id/revision and file digests
// validate".
func (c *Cache) Lookup(want Expect) (Entry, bool) {
	dir, err := c.revisionDir(want.MapID, want.Revision)
	if err != nil {
		return Entry{}, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Entry{}, false
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), "bundle-") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		found, err := c.load(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if err = found.Verify(); err != nil {
			continue
		}
		if found.Receipt.MapID != want.MapID ||
			(want.Revision > 0 && found.Receipt.Revision != want.Revision) {
			continue
		}

		return found, true
	}

	return Entry{}, false
}

// load reads a published entry without verifying its files.
func (c *Cache) load(dir string) (Entry, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ReceiptName))
	if err != nil {
		return Entry{}, err
	}
	receipt := Receipt{}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return Entry{}, err
	}
	if receipt.SchemaVersion != ReceiptSchema {
		return Entry{}, fmt.Errorf("texturebundle: %s is a %q record and this Companion writes %q",
			dir, receipt.SchemaVersion, ReceiptSchema)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return Entry{}, err
	}
	manifest, err := parseManifest(manifestRaw)
	if err != nil {
		return Entry{}, err
	}

	return Entry{
		Dir:         dir,
		ContentRoot: filepath.Join(dir, ContentDir),
		Receipt:     receipt,
		Manifest:    manifest,
	}, nil
}

// Verify re-hashes every file the receipt records.
func (e Entry) Verify() error {
	for _, file := range e.Receipt.Files {
		target, err := safeJoin(e.ContentRoot, file.Path)
		if err != nil {
			return err
		}
		info, err := os.Stat(target)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("texturebundle: %s is missing from this cached bundle", file.Path)
		case err != nil:
			return err
		case info.Size() != file.Bytes:
			return fmt.Errorf("texturebundle: %s is %d bytes and was verified at %d",
				file.Path, info.Size(), file.Bytes)
		}
		digest, _, err := digestFile(target)
		if err != nil {
			return err
		}
		if !strings.EqualFold(digest, file.SHA256) {
			return fmt.Errorf("texturebundle: %s no longer hashes to what was verified", file.Path)
		}
	}

	return nil
}

// Publish verifies a downloaded bundle and installs it as a cache entry.
//
// Nothing is visible until everything has passed: the extraction happens in a
// `bundle-*` temporary directory inside the same revision directory — the same
// filesystem, so the publish is a rename and not a copy — and that directory is
// removed on every failure path, including cancellation.
//
// A bundle already published under the same digest is returned as it is. Two
// downloads of one revision are the same bytes, and re-extracting them would
// only create a second chance to differ.
func (c *Cache) Publish(ctx context.Context, bundle []byte, want Expect) (Entry, error) {
	digest := Digest(bundle)
	dir, err := c.revisionDir(want.MapID, want.Revision)
	if err != nil {
		return Entry{}, err
	}
	final := filepath.Join(dir, digest)
	if existing, loadErr := c.load(final); loadErr == nil && existing.Verify() == nil {
		return existing, nil
	}

	// Unpacked into the cache's own staging directory rather than into the
	// revision directory, so a bundle that turns out to be the WRONG revision
	// leaves no directory named after the revision it was not. It is on the same
	// filesystem, so the publish below is still a rename.
	staging := filepath.Join(c.root, stagingDir)
	if err = os.MkdirAll(staging, 0o700); err != nil {
		return Entry{}, fmt.Errorf("texturebundle: preparing %s: %w", staging, err)
	}
	unpacked, err := extract(ctx, staging, bundle, want, c.limits)
	if err != nil {
		return Entry{}, err
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		_ = os.RemoveAll(unpacked.dir)

		return Entry{}, fmt.Errorf("texturebundle: preparing %s: %w", dir, err)
	}
	fail := func(cause error) (Entry, error) {
		_ = os.RemoveAll(unpacked.dir)

		return Entry{}, cause
	}
	if ctx.Err() != nil {
		return fail(fmt.Errorf("%w: %v", ErrCancelled, ctx.Err()))
	}

	var total int64
	for _, file := range unpacked.files {
		total += file.Bytes
	}
	receipt := Receipt{
		SchemaVersion:    ReceiptSchema,
		BundleDigest:     digest,
		MapID:            unpacked.manifest.MapID,
		Revision:         unpacked.manifest.Revision,
		Game:             unpacked.manifest.Game,
		ManifestSchema:   unpacked.manifest.SchemaVersion,
		WADsDeclared:     unpacked.manifest.WADsDeclared,
		CompilerReady:    unpacked.manifest.CompilerReady,
		CompilerRefusals: unpacked.manifest.CompilerRefusals,
		Files:            unpacked.files,
		TotalBytes:       total,
		Licenses:         unpacked.licenses,
		VerifiedAt:       c.now(),
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fail(fmt.Errorf("texturebundle: recording the bundle: %w", err))
	}
	if err = os.WriteFile(filepath.Join(unpacked.dir, ReceiptName), encoded, 0o600); err != nil {
		return fail(fmt.Errorf("texturebundle: recording the bundle: %w", err))
	}
	// The one atomic step. Everything above it was inside a directory nothing
	// else looks in; everything below it is a published entry.
	if err = os.Rename(unpacked.dir, final); err != nil {
		// A concurrent publish of the same digest already put a verified entry
		// there. That is not a failure: the bytes are identical by definition.
		if existing, loadErr := c.load(final); loadErr == nil && existing.Verify() == nil {
			_ = os.RemoveAll(unpacked.dir)

			return existing, nil
		}

		return fail(fmt.Errorf("texturebundle: publishing the bundle: %w", err))
	}

	return Entry{
		Dir:         final,
		ContentRoot: filepath.Join(final, ContentDir),
		Receipt:     receipt,
		Manifest:    unpacked.manifest,
	}, nil
}

// Sweep removes interrupted extractions left by a killed process.
//
// A `bundle-*` directory is by construction unpublished: the publish is a
// rename to the digest name. One older than `olderThan` therefore belongs to a
// run that is not coming back.
func (c *Cache) Sweep(olderThan time.Duration) ([]string, error) {
	var removed []string
	cutoff := c.now().Add(-olderThan)
	err := filepath.WalkDir(c.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() || !strings.HasPrefix(entry.Name(), "bundle-") {
			return nil //nolint:nilerr // an unreadable directory is not this sweep's business
		}
		info, statErr := entry.Info()
		if statErr != nil || info.ModTime().After(cutoff) {
			return fs.SkipDir
		}
		if removeErr := os.RemoveAll(path); removeErr == nil {
			removed = append(removed, path)
		}

		return fs.SkipDir
	})

	return removed, err
}
