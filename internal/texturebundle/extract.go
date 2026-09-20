package texturebundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Limits bound what an archive may cost before anything is written.
//
// A bound is not a measurement, and none of these is a guess about a real map:
// they are the sizes past which a bundle is no longer a texture export, and the
// point is that the program stops at a number somebody chose rather than at
// whatever the disk or the memory allocator does first.
type Limits struct {
	// CompressedBytes caps the archive itself.
	CompressedBytes int64
	// UncompressedBytes caps the sum of every member's declared and actual
	// size, which is what a decompression bomb inflates.
	UncompressedBytes int64
	// MemberBytes caps one member.
	MemberBytes int64
	// Members caps how many there may be.
	Members int
}

// DefaultLimits are generous for a real map's declared WAD set and far below
// anything that could exhaust a machine.
//
// A Quake 1 map's whole declared set is a few tens of megabytes of WAD; a
// texture-heavy Quake II namespace is larger and still nowhere near these.
func DefaultLimits() Limits {
	return Limits{
		CompressedBytes:   512 << 20,
		UncompressedBytes: 2 << 30,
		MemberBytes:       512 << 20,
		Members:           4096,
	}
}

// Expect is what the caller selected and what the bundle must therefore be.
//
// Revision zero means the caller pinned none, which is only correct for a
// caller that is not building: a build pins one, and a bundle whose manifest
// disagrees is refused rather than used.
type Expect struct {
	MapID    string
	Revision int
}

// ErrRevisionMismatch reports a bundle that is not the revision that was asked
// for. It is its own error because the remedy is specific: re-read the map's
// current revision and start again, never "use it anyway".
var ErrRevisionMismatch = errors.New("texturebundle: the bundle is not the map revision that was requested")

// ErrCancelled reports an extraction stopped by its context. Nothing is
// published and the temporary directory is gone.
var ErrCancelled = errors.New("texturebundle: the bundle was cancelled before it was published")

// extracted is a verified extraction in a private temporary directory, before
// it is published.
type extracted struct {
	dir      string
	manifest Manifest
	files    []Member
	licenses bool
}

// Member is one verified file on disk.
type Member struct {
	// Path is the member's archive path, POSIX-spelled and relative.
	Path string `json:"path"`
	// Source is the declared texture source the file belongs to.
	Source string `json:"source,omitempty"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// extract unpacks the archive into a new private directory and verifies it
// completely. It returns an extraction that has already passed every check, or
// an error and nothing on disk.
//
// The order of the checks is the point. The archive's own shape is checked
// before a member is opened; a member's name is checked before it is created;
// the manifest is parsed before a payload file is written; and the digests are
// compared after every byte is on disk, against what the manifest declared, not
// against what the archive's own directory claimed.
func extract(ctx context.Context, parent string, bundle []byte, want Expect, limits Limits) (*extracted, error) {
	if int64(len(bundle)) > limits.CompressedBytes {
		return nil, fmt.Errorf("texturebundle: the bundle is %d bytes and the limit is %d",
			len(bundle), limits.CompressedBytes)
	}
	reader, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return nil, fmt.Errorf("texturebundle: the bundle is not a readable archive: %w", err)
	}
	if len(reader.File) > limits.Members {
		return nil, fmt.Errorf("texturebundle: the bundle holds %d members and the limit is %d",
			len(reader.File), limits.Members)
	}

	names, err := checkNames(reader, limits)
	if err != nil {
		return nil, err
	}

	manifestEntry, found := names[ManifestName]
	if !found {
		return nil, fmt.Errorf("texturebundle: the bundle carries no %s", ManifestName)
	}
	raw, err := readMember(manifestEntry, limits.MemberBytes)
	if err != nil {
		return nil, err
	}
	manifest, err := parseManifest(raw)
	if err != nil {
		return nil, err
	}
	if err = checkExpected(manifest, want); err != nil {
		return nil, err
	}
	declared, err := declaredFiles(manifest)
	if err != nil {
		return nil, err
	}
	// Nothing undeclared, except the two documents the contract names. A
	// bundle that carried an extra executable would otherwise land in a
	// directory the build reads.
	for name := range names {
		if name == ManifestName || name == LicensesName {
			continue
		}
		if _, ok := declared[name]; !ok {
			return nil, fmt.Errorf(
				"texturebundle: the bundle carries %s, which its manifest does not declare", name)
		}
	}
	for name := range declared {
		if _, ok := names[name]; !ok {
			return nil, fmt.Errorf(
				"texturebundle: the manifest declares %s, which the bundle does not carry", name)
		}
	}

	dir, err := os.MkdirTemp(parent, "bundle-")
	if err != nil {
		return nil, fmt.Errorf("texturebundle: preparing a private directory: %w", err)
	}
	out := &extracted{dir: dir, manifest: manifest}
	fail := func(cause error) (*extracted, error) {
		_ = os.RemoveAll(dir)

		return nil, cause
	}

	content := filepath.Join(dir, ContentDir)
	if err = os.MkdirAll(content, 0o700); err != nil {
		return fail(fmt.Errorf("texturebundle: preparing a private directory: %w", err))
	}

	var total int64
	for _, name := range sortedNames(declared) {
		if ctx.Err() != nil {
			return fail(fmt.Errorf("%w: %v", ErrCancelled, ctx.Err()))
		}
		file := declared[name]
		if file.Bytes > limits.MemberBytes {
			return fail(fmt.Errorf("texturebundle: %s declares %d bytes and the per-file limit is %d",
				name, file.Bytes, limits.MemberBytes))
		}
		total += file.Bytes
		if total > limits.UncompressedBytes {
			return fail(fmt.Errorf(
				"texturebundle: this bundle's files total more than the %d bytes the Companion will extract",
				limits.UncompressedBytes))
		}
		destination, joinErr := safeJoin(content, name)
		if joinErr != nil {
			return fail(joinErr)
		}
		if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return fail(fmt.Errorf("texturebundle: creating %s: %w", filepath.Dir(destination), err))
		}
		digest, size, writeErr := writeMember(names[name], destination, limits.MemberBytes)
		if writeErr != nil {
			return fail(writeErr)
		}
		if size != file.Bytes {
			return fail(fmt.Errorf("texturebundle: %s is %d bytes and its manifest declares %d",
				name, size, file.Bytes))
		}
		if !strings.EqualFold(digest, file.SHA256) {
			return fail(fmt.Errorf("texturebundle: %s hashes to %s and its manifest declares %s",
				name, digest, file.SHA256))
		}
		out.files = append(out.files, Member{Path: name, Source: file.Source, SHA256: strings.ToLower(digest), Bytes: size})
	}

	if entry, ok := names[LicensesName]; ok {
		body, readErr := readMember(entry, limits.MemberBytes)
		if readErr != nil {
			return fail(readErr)
		}
		if err = os.WriteFile(filepath.Join(dir, LicensesName), body, 0o600); err != nil {
			return fail(fmt.Errorf("texturebundle: writing %s: %w", LicensesName, err))
		}
		out.licenses = true
	}
	if err = os.WriteFile(filepath.Join(dir, ManifestName), raw, 0o600); err != nil {
		return fail(fmt.Errorf("texturebundle: writing %s: %w", ManifestName, err))
	}

	return out, nil
}

// ContentDir is the subdirectory of a cache entry that holds the original
// files, and the directory a build is given as `content_root`.
//
// Separate from the entry directory so the entry's own records — the manifest
// and the receipt — are not inside the read-only root a compiler is pointed at.
const ContentDir = "content"

// checkExpected refuses a bundle that is not the one the caller selected.
func checkExpected(manifest Manifest, want Expect) error {
	if want.MapID != "" && manifest.MapID != want.MapID {
		return fmt.Errorf("%w: it is map %s and %s was requested",
			ErrRevisionMismatch, manifest.MapID, want.MapID)
	}
	if want.Revision > 0 && manifest.Revision != want.Revision {
		return fmt.Errorf("%w: it is revision %d and revision %d was requested",
			ErrRevisionMismatch, manifest.Revision, want.Revision)
	}

	return nil
}

// checkNames enforces every rule about what a member may be CALLED, before any
// of them is opened.
//
// Each refusal here is a specific attack or a specific portability trap:
//
//   - an absolute or drive-prefixed path escapes the extraction directory;
//   - `..` escapes it too, and `id1` is one `../..` away from a mod directory;
//   - a backslash is a separator on Windows and an ordinary character in a ZIP,
//     so a member called `a\..\..\b` is a traversal on one platform only;
//   - a symlink, hard link or device is not a texture, and following one would
//     write through it to wherever it points;
//   - a duplicate name is two members and one file, so whichever wins is
//     whichever the extractor happened to write last;
//   - a case-fold collision is a duplicate on Windows and macOS and not on
//     Linux, which is the same ambiguity with a platform in front of it.
func checkNames(reader *zip.Reader, limits Limits) (map[string]*zip.File, error) {
	names := make(map[string]*zip.File, len(reader.File))
	folded := map[string]string{}
	for _, entry := range reader.File {
		name := entry.Name
		switch {
		case strings.TrimSpace(name) == "":
			return nil, fmt.Errorf("texturebundle: the bundle holds a member with no name")
		case strings.ContainsRune(name, 0):
			return nil, fmt.Errorf("texturebundle: a member's name contains a NUL byte")
		case strings.ContainsRune(name, '\\'):
			return nil, fmt.Errorf("texturebundle: the member %q contains a backslash, which is a "+
				"path separator on Windows and an ordinary character here", name)
		case strings.HasPrefix(name, "/"), strings.HasPrefix(name, "//"):
			return nil, fmt.Errorf("texturebundle: the member %q is an absolute path", name)
		case drivePrefixed(name):
			return nil, fmt.Errorf("texturebundle: the member %q names a Windows drive", name)
		case strings.HasSuffix(name, "/"):
			return nil, fmt.Errorf("texturebundle: the member %q is a directory entry, and a bundle "+
				"declares files", name)
		}
		if cleaned := path.Clean(name); cleaned != name {
			return nil, fmt.Errorf("texturebundle: the member %q is not a plain relative path (it cleans to %q)",
				name, cleaned)
		}
		for _, segment := range strings.Split(name, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return nil, fmt.Errorf("texturebundle: the member %q has a %q path segment", name, segment)
			}
		}
		mode := entry.Mode()
		if mode&fs.ModeSymlink != 0 || mode&fs.ModeDevice != 0 || mode&fs.ModeNamedPipe != 0 ||
			mode&fs.ModeSocket != 0 || mode&fs.ModeIrregular != 0 {
			return nil, fmt.Errorf("texturebundle: the member %q is not a regular file", name)
		}
		if entry.UncompressedSize64 > uint64(limits.MemberBytes) {
			return nil, fmt.Errorf("texturebundle: the member %q declares %d bytes and the limit is %d",
				name, entry.UncompressedSize64, limits.MemberBytes)
		}
		if _, duplicate := names[name]; duplicate {
			return nil, fmt.Errorf("texturebundle: the bundle holds %q twice", name)
		}
		key := foldName(name)
		if other, collides := folded[key]; collides {
			return nil, fmt.Errorf("texturebundle: %q and %q differ only in case, and on Windows and "+
				"macOS they are one file", other, name)
		}
		folded[key] = name
		names[name] = entry
	}

	return names, nil
}

// foldName is the case-insensitive key two colliding names share.
func foldName(name string) string {
	return strings.Map(unicode.ToLower, name)
}

// drivePrefixed spots `C:\...`, `C:/...` and the bare `C:name`, all of which
// Windows resolves relative to a drive rather than to the extraction directory.
func drivePrefixed(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	letter := name[0]

	return letter >= 'A' && letter <= 'Z' || letter >= 'a' && letter <= 'z'
}

// safeJoin resolves a checked member name under a directory and proves the
// result is still inside it.
//
// The name has already passed [checkNames]; this is the second check, against
// the resolved path rather than the spelling, because the two can disagree on a
// filesystem with a link in the middle of it.
func safeJoin(dir, name string) (string, error) {
	target := filepath.Join(dir, filepath.FromSlash(name))
	relative, err := filepath.Rel(dir, target)
	if err != nil {
		return "", fmt.Errorf("texturebundle: %q does not resolve inside the bundle directory", name)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("texturebundle: %q resolves outside the bundle directory", name)
	}

	return target, nil
}

func sortedNames(files map[string]File) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	sort.Strings(out)

	return out
}

func readMember(entry *zip.File, limit int64) ([]byte, error) {
	opened, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("texturebundle: reading %s: %w", entry.Name, err)
	}
	defer opened.Close()

	body, err := io.ReadAll(io.LimitReader(opened, limit+1))
	if err != nil {
		return nil, fmt.Errorf("texturebundle: reading %s: %w", entry.Name, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("texturebundle: %s is larger than the %d-byte limit", entry.Name, limit)
	}

	return body, nil
}

// writeMember copies one member to disk, hashing as it goes and stopping at the
// limit rather than after it.
func writeMember(entry *zip.File, destination string, limit int64) (string, int64, error) {
	opened, err := entry.Open()
	if err != nil {
		return "", 0, fmt.Errorf("texturebundle: reading %s: %w", entry.Name, err)
	}
	defer opened.Close()

	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("texturebundle: writing %s: %w", entry.Name, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(opened, limit+1))
	closeErr := file.Close()
	switch {
	case copyErr != nil:
		return "", 0, fmt.Errorf("texturebundle: writing %s: %w", entry.Name, copyErr)
	case closeErr != nil:
		return "", 0, fmt.Errorf("texturebundle: writing %s: %w", entry.Name, closeErr)
	case written > limit:
		return "", 0, fmt.Errorf("texturebundle: %s decompresses past the %d-byte limit", entry.Name, limit)
	}

	return hex.EncodeToString(hash.Sum(nil)), written, nil
}
