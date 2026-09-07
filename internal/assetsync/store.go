// Package assetsync is the Companion's local cache of AUB assets: a
// content-addressed object store, a record per synced revision, and the one
// place a downloaded byte is verified before anything is allowed to read it.
//
// # Why a second cache and not internal/acquire
//
// internal/acquire is the TOOLCHAIN cache: signed catalogue artifacts, licence
// acceptances, install trees with entry points. It shares the technique used
// here — stage, verify, rename — and nothing else. What it identifies is a
// released package at a version; what this identifies is one revision of
// somebody's map, authorized per request against a backend that may revoke it.
// Two different identities, two different authorities, and folding them together
// would mean one cache whose entries mean two things.
//
// # Nothing is published unverified
//
// A download is written to a staging file while being hashed and counted, and it
// is renamed into the object store ONLY when both the digest and the length match
// what the server declared. A tampered, truncated or interrupted transfer leaves
// a staging file, which the next run deletes, and leaves the store exactly as it
// was — so a build reads either the right bytes or none, never half of a file
// that arrived during a network failure.
//
// # A revision is atomic, not just a file
//
// The record that says "this revision is here" is written LAST, after every one
// of its files has landed. A run interrupted half way through a five-file prefab
// package leaves some objects and no record, and the next sync finds the objects
// already present, verifies nothing twice, and completes. A record that exists is
// a revision that is complete.
//
// # And nothing published is ever rewritten
//
// An object is named by its own digest, so writing one twice writes the same
// bytes; a revision record is named by the revision, and a revision is immutable
// at the server. A newer remote revision is a NEW record beside the old one,
// which is what makes "an already-chosen revision does not change under a build"
// a property of the layout rather than of a rule somebody has to obey.
package assetsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion versions the on-disk revision record.
//
// A published record: it is what a build manifest's provenance points at and it
// may be read by a build of the Companion older or newer than the one that wrote
// it. So it is versioned by name and refused rather than half-read.
const SchemaVersion = "aucom.asset-revision/1.0"

// ErrNotCached is a revision this store does not hold.
var ErrNotCached = errors.New("assetsync: this revision is not in the local cache")

// ErrDigestMismatch is a transfer whose bytes are not what the server declared.
//
// It is deliberately its own error: it is the one failure that means the content
// cannot be trusted, as against a network error, which means nothing about the
// content at all.
var ErrDigestMismatch = errors.New("assetsync: the downloaded bytes are not what the server declared")

// FileRecord is one file of a cached revision.
type FileRecord struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

// RevisionRecord is one revision, complete, in the local cache.
//
// It is the whole of what a build needs to name its source afterwards: which
// asset, which exact version, whether that version can be fetched again, and the
// digest of every file it read.
type RevisionRecord struct {
	SchemaVersion string `json:"schema_version"`

	AssetType   string `json:"asset_type"`
	AssetID     string `json:"asset_id"`
	DisplayName string `json:"display_name,omitempty"`

	// RevisionID names exactly one version for ever, and is empty for an asset
	// type that keeps a counter and no per-version row. Empty is not a failure:
	// Immutable says what it means.
	RevisionID string `json:"revision_id,omitempty"`
	Revision   int    `json:"revision"`

	// Immutable reports whether RevisionID will resolve to these same bytes at
	// the server later. False for a `current_only` type, and a build that used
	// one records a digest it can check rather than a version it can re-fetch.
	Immutable bool `json:"immutable"`

	// ManifestSHA256 is the server's digest over the ordered file list: one
	// string that identifies this revision's content without re-fetching it.
	ManifestSHA256 string `json:"manifest_sha256"`

	// ContentSHA256 is the asset store's own digest for this version, when it
	// recorded one. Empty means NOT RECORDED at the server — never "no content".
	ContentSHA256 string `json:"content_sha256,omitempty"`

	AuthorUserID string `json:"author_user_id,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`

	Files      []FileRecord `json:"files"`
	TotalBytes int64        `json:"total_bytes"`

	// Source is the AUB instance this came from, and SyncedAt is when. Both are
	// facts about this machine's copy rather than about the revision, which is
	// why neither is part of any digest.
	Source   string    `json:"source"`
	SyncedAt time.Time `json:"synced_at"`
}

// Key is how this revision is addressed in the local cache.
//
// The revision id when there is one, otherwise the manifest digest — which is
// what identifies a `current_only` type's content, and which changes when that
// content does, so two versions of one Game Profile are two records rather than
// one that silently overwrote the other.
func (r RevisionRecord) Key() string {
	if r.RevisionID != "" {
		return r.RevisionID
	}

	return "content-" + r.ManifestSHA256
}

// Store is a local cache rooted at one directory.
type Store struct{ root string }

// Open prepares a store, creating the directory when it does not exist.
func Open(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("assetsync: the cache directory is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("assetsync: resolving %q: %w", root, err)
	}
	for _, dir := range []string{absolute,
		filepath.Join(absolute, "objects"),
		filepath.Join(absolute, "staging"),
		filepath.Join(absolute, "revisions")} {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("assetsync: creating %s: %w", dir, err)
		}
	}

	return &Store{root: absolute}, nil
}

// Root is the directory this store lives in.
func (s *Store) Root() string { return s.root }

// objectPath is where one digest's bytes live.
//
// Two levels of fan-out on the digest's own prefix, because a flat directory of
// tens of thousands of files is slow to list on every filesystem and unusable on
// some. A digest that is not 64 lower-case hex characters is refused rather than
// turned into a path: it arrives from a server response, and a value that could
// contain a separator must never become one.
func (s *Store) objectPath(digest string) (string, error) {
	if len(digest) != 64 {
		return "", fmt.Errorf("assetsync: %q is not a sha256 digest", digest)
	}
	for _, r := range digest {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", fmt.Errorf("assetsync: %q is not a lower-case hex digest", digest)
		}
	}

	return filepath.Join(s.root, "objects", digest[:2], digest[2:4], digest), nil
}

// Has reports whether the store already holds these bytes.
func (s *Store) Has(digest string) bool {
	path, err := s.objectPath(digest)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// Object is the path to a cached blob, or ErrNotCached.
func (s *Store) Object(digest string) (string, error) {
	path, err := s.objectPath(digest)
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(path); err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotCached, digest)
	}

	return path, nil
}

// Publish writes a reader into the object store, verifying as it goes.
//
// The digest and the length are BOTH checked, and both against what the server
// declared before a byte arrived. A digest alone would catch corruption; the
// length catches the case a digest cannot, which is a transfer that stopped early
// AND a server that declared a length it did not send — the two halves of an
// interrupted download.
//
// Nothing is renamed until both agree, so a failure leaves the store untouched.
func (s *Store) Publish(reader io.Reader, digest string, size int64) (string, error) {
	final, err := s.objectPath(digest)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(final); statErr == nil {
		// Already held. The bytes are named by their own digest, so there is
		// nothing to compare and nothing to overwrite.
		return final, nil
	}

	staging, err := os.CreateTemp(filepath.Join(s.root, "staging"), "download-*")
	if err != nil {
		return "", fmt.Errorf("assetsync: creating a staging file: %w", err)
	}
	stagingPath := staging.Name()
	// Removed on every path but a successful rename. A staging file left behind
	// by a killed process is what CleanStaging is for.
	defer func() {
		staging.Close()
		os.Remove(stagingPath)
	}()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(staging, hasher), reader)
	if err != nil {
		return "", fmt.Errorf("assetsync: reading the download: %w", err)
	}
	if err = staging.Sync(); err != nil {
		return "", fmt.Errorf("assetsync: flushing the download: %w", err)
	}
	if err = staging.Close(); err != nil {
		return "", fmt.Errorf("assetsync: closing the download: %w", err)
	}

	if size >= 0 && written != size {
		return "", fmt.Errorf("%w: %d bytes arrived, %d were declared",
			ErrDigestMismatch, written, size)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != digest {
		return "", fmt.Errorf("%w: the bytes hash to %s, %s was declared",
			ErrDigestMismatch, got, digest)
	}

	if err = os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return "", fmt.Errorf("assetsync: creating the object directory: %w", err)
	}
	if err = os.Chmod(stagingPath, 0o600); err != nil {
		return "", fmt.Errorf("assetsync: setting permissions: %w", err)
	}
	if err = os.Rename(stagingPath, final); err != nil {
		return "", fmt.Errorf("assetsync: publishing the object: %w", err)
	}

	return final, nil
}

// revisionPath is where one revision's record lives.
func (s *Store) revisionPath(assetType, assetID, key string) (string, error) {
	for _, segment := range []string{assetType, assetID, key} {
		if !safeSegment(segment) {
			return "", fmt.Errorf("assetsync: %q is not a usable cache key", segment)
		}
	}

	return filepath.Join(s.root, "revisions", assetType, assetID, key+".json"), nil
}

// safeSegment is what may become one path segment.
//
// An asset id, an asset type and a revision id all arrive from a server
// response. Restricting them to a closed character set is the whole defence: a
// value that cannot contain a separator, a dot pair or a null cannot escape the
// cache directory, and there is no cleaning step whose correctness has to be
// argued about.
func safeSegment(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}

	return true
}

// SaveRevision writes a revision record, atomically and LAST.
//
// Every file it names must already be in the object store. A record that exists
// is a revision that is complete, and that is what makes an interrupted sync
// safe: it leaves objects, which the next run finds already present, and no
// record, so nothing reads a half-fetched revision.
func (s *Store) SaveRevision(record RevisionRecord) error {
	record.SchemaVersion = SchemaVersion
	for _, file := range record.Files {
		if !s.Has(file.SHA256) {
			return fmt.Errorf("assetsync: %s is not in the object store; "+
				"a revision record is written only once every file has landed", file.Path)
		}
	}

	path, err := s.revisionPath(record.AssetType, record.AssetID, record.Key())
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("assetsync: creating the revision directory: %w", err)
	}

	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("assetsync: encoding the revision record: %w", err)
	}
	staging, err := os.CreateTemp(filepath.Dir(path), "record-*")
	if err != nil {
		return fmt.Errorf("assetsync: creating a staging record: %w", err)
	}
	stagingPath := staging.Name()
	defer func() {
		staging.Close()
		os.Remove(stagingPath)
	}()
	if _, err = staging.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("assetsync: writing the revision record: %w", err)
	}
	if err = staging.Sync(); err != nil {
		return fmt.Errorf("assetsync: flushing the revision record: %w", err)
	}
	if err = staging.Close(); err != nil {
		return fmt.Errorf("assetsync: closing the revision record: %w", err)
	}
	if err = os.Chmod(stagingPath, 0o600); err != nil {
		return fmt.Errorf("assetsync: setting permissions: %w", err)
	}

	return os.Rename(stagingPath, path)
}

// Revision reads one cached revision.
//
// This is the OFFLINE path, and it takes no client and no network: a Companion
// with no connection still builds from what it has already verified. `key` is a
// revision id, or the Key of a `current_only` record.
func (s *Store) Revision(assetType, assetID, key string) (RevisionRecord, error) {
	path, err := s.revisionPath(assetType, assetID, key)
	if err != nil {
		return RevisionRecord{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return RevisionRecord{}, fmt.Errorf("%w: %s/%s/%s", ErrNotCached, assetType, assetID, key)
	}

	record := RevisionRecord{}
	if err = json.Unmarshal(body, &record); err != nil {
		return RevisionRecord{}, fmt.Errorf("assetsync: %s is not a readable revision record: %w",
			path, err)
	}
	if record.SchemaVersion != SchemaVersion {
		return RevisionRecord{}, fmt.Errorf("assetsync: %s is %q; this build reads %q",
			path, record.SchemaVersion, SchemaVersion)
	}

	return record, nil
}

// Revisions is every cached revision of one asset, or of everything when
// assetType and assetID are empty.
func (s *Store) Revisions(assetType, assetID string) ([]RevisionRecord, error) {
	root := filepath.Join(s.root, "revisions")
	if assetType != "" {
		if !safeSegment(assetType) {
			return nil, fmt.Errorf("assetsync: %q is not a usable asset type", assetType)
		}
		root = filepath.Join(root, assetType)
	}
	if assetID != "" {
		if !safeSegment(assetID) {
			return nil, fmt.Errorf("assetsync: %q is not a usable asset id", assetID)
		}
		root = filepath.Join(root, assetID)
	}

	records := []RevisionRecord{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}

			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		record := RevisionRecord{}
		if json.Unmarshal(body, &record) != nil || record.SchemaVersion != SchemaVersion {
			// A record this build cannot read is SKIPPED rather than fatal: it is
			// one asset in a cache, and refusing to list anything because of it
			// would make an unrelated future version unusable today.
			return nil
		}
		records = append(records, record)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("assetsync: reading the revision cache: %w", err)
	}

	return records, nil
}

// Verify re-checks every file of a cached revision against its recorded digest.
//
// Used before a build reads one, and by `companion aub verify`. It is what turns
// "the file is there" into "the file is what it was when it was published" — a
// disk that lost a block, or a cache somebody edited, is caught here rather than
// by a compiler producing something strange.
func (s *Store) Verify(record RevisionRecord) error {
	for _, file := range record.Files {
		path, err := s.Object(file.SHA256)
		if err != nil {
			return fmt.Errorf("%s: %w", file.Path, err)
		}
		handle, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("assetsync: opening %s: %w", file.Path, err)
		}
		hasher := sha256.New()
		written, err := io.Copy(hasher, handle)
		handle.Close()
		if err != nil {
			return fmt.Errorf("assetsync: reading %s: %w", file.Path, err)
		}
		if written != file.Bytes {
			return fmt.Errorf("%w: %s holds %d bytes, the record says %d",
				ErrDigestMismatch, file.Path, written, file.Bytes)
		}
		if got := hex.EncodeToString(hasher.Sum(nil)); got != file.SHA256 {
			return fmt.Errorf("%w: %s hashes to %s, the record says %s",
				ErrDigestMismatch, file.Path, got, file.SHA256)
		}
	}

	return nil
}

// Materialize copies one cached revision's files into a directory under their
// declared paths, and returns them in order.
//
// A copy rather than a link: a build stages its inputs and a tool may write
// beside them, and a hard link into the cache would let a misbehaving tool
// corrupt the verified copy every other build reads.
func (s *Store) Materialize(record RevisionRecord, dir string) ([]string, error) {
	if err := s.Verify(record); err != nil {
		return nil, err
	}
	written := make([]string, 0, len(record.Files))
	for _, file := range record.Files {
		target, err := safeJoin(dir, file.Path)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("assetsync: creating %s: %w", filepath.Dir(target), err)
		}
		source, err := s.Object(file.SHA256)
		if err != nil {
			return nil, err
		}
		if err = copyFile(source, target); err != nil {
			return nil, err
		}
		written = append(written, target)
	}

	return written, nil
}

// safeJoin resolves a declared file path under a directory, refusing anything
// that would land outside it.
//
// A revision's paths come from a server response. They may legitimately contain
// a separator — `e1u1/pow12_1.wal` is an ordinary Quake II texture — so they
// cannot simply be flattened; what they may not do is escape, and this is where
// that is decided rather than assumed.
func safeJoin(dir, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("assetsync: %q is not a usable file path", name)
	}
	if filepath.IsAbs(name) || strings.Contains(name, "\x00") {
		return "", fmt.Errorf("assetsync: %q is not a usable file path", name)
	}
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("assetsync: %q escapes the staging directory", name)
	}
	target := filepath.Join(dir, cleaned)
	relative, err := filepath.Rel(dir, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("assetsync: %q escapes the staging directory", name)
	}

	return target, nil
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("assetsync: opening %s: %w", source, err)
	}
	defer in.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("assetsync: creating %s: %w", target, err)
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()

		return fmt.Errorf("assetsync: writing %s: %w", target, err)
	}

	return out.Close()
}

// CleanStaging removes staging files older than a cutoff.
//
// A staging file is what an interrupted or killed download leaves; it is never
// read by anything, so removing one can lose nothing but disk. The age bound is
// what keeps it from deleting a download another process has in flight.
func (s *Store) CleanStaging(olderThan time.Duration, now time.Time) ([]string, error) {
	dir := filepath.Join(s.root, "staging")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("assetsync: reading the staging directory: %w", err)
	}

	removed := []string{}
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr != nil || entry.IsDir() {
			continue
		}
		if now.Sub(info.ModTime()) < olderThan {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if os.Remove(path) == nil {
			removed = append(removed, path)
		}
	}

	return removed, nil
}
