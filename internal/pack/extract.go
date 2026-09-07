package pack

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Unpacking an archive.
//
// The order of operations is the security property, so it is worth reading in
// order:
//
//  1. **Inspect.** The directory alone, budgeted. An archive that declares more
//     members, more bytes or a worse ratio than the budget allows is refused
//     here, having had nothing but its directory read.
//  2. **Refuse the whole thing, not the bad member.** Collisions, existing
//     destinations, unsafe names — all checked across every member before the
//     first byte is written. An extractor that discovers a traversal name on
//     member 300 has already written 299 files somebody now has to find.
//  3. **Resolve the destination once.** The root is resolved through any
//     symbolic links it already contains, and every member's path is checked
//     against the resolved root — so a pre-existing `maps -> /etc` in the
//     destination cannot make `maps/e1m1.bsp` land in `/etc`.
//  4. **Write with O_EXCL.** No implicit overwrite, ever, even after the
//     up-front check: between the check and the write is a window, and O_EXCL
//     is what closes it.
//  5. **Check the bytes against the declaration.** A member that produces fewer
//     or more bytes than it declared is a lie, not a discrepancy, and the
//     extraction stops.
//
// Nothing here repairs anything, for the reason in `path.go`.

// ExtractOptions is what an extraction needs beyond the archive.
type ExtractOptions struct {
	// Dest is the directory to write into. It is created if missing.
	Dest string
	// Budget bounds what the archive may declare and expand to.
	Budget Budget
	// Replace permits writing over files that are already there. Off by
	// default: extracting a package over a game directory is a thing people do
	// on purpose and a thing people do by accident, and the two are told apart
	// by whether they said so.
	Replace bool
	// Only, when non-empty, extracts just these member paths.
	Only []string
}

// ExtractResult is what an extraction wrote.
type ExtractResult struct {
	Dest  string  `json:"dest"`
	Files []Entry `json:"files"`
	Bytes int64   `json:"bytes"`
	// Skipped are members the archive holds and this run did not write,
	// because Only did not name them.
	Skipped int `json:"skipped,omitempty"`
}

// Extract unpacks an archive into a directory.
func Extract(archivePath string, format Format, options ExtractOptions) (*ExtractResult, error) {
	inspection, err := Inspect(archivePath, format, options.Budget)
	if err != nil {
		return nil, err
	}
	if len(inspection.Collisions) > 0 {
		return nil, fmt.Errorf("%w: %s. Extracting it would write one file where the archive holds two",
			ErrUnsafePath, inspection.Collisions[0].Error())
	}
	if options.Dest == "" {
		return nil, errors.New("pack: no destination directory")
	}
	if err := os.MkdirAll(options.Dest, 0o755); err != nil {
		return nil, fmt.Errorf("pack: creating %s: %w", options.Dest, err)
	}
	// Resolved once, through whatever links the destination already contains,
	// and every member is checked against the resolution rather than against
	// the name the caller typed.
	root, err := filepath.EvalSymlinks(options.Dest)
	if err != nil {
		return nil, fmt.Errorf("pack: resolving %s: %w", options.Dest, err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("pack: resolving %s: %w", options.Dest, err)
	}

	wanted := map[string]bool{}
	for _, name := range options.Only {
		wanted[name] = true
	}
	selected := make([]Entry, 0, len(inspection.Entries))
	skipped := 0
	for _, entry := range inspection.Entries {
		if len(wanted) > 0 && !wanted[entry.Path] {
			skipped++
			continue
		}
		selected = append(selected, entry)
	}
	if len(wanted) > 0 {
		for _, name := range sortedStrings(wanted) {
			if !containsPath(selected, name) {
				return nil, fmt.Errorf("pack: %s holds no member named %q", archivePath, name)
			}
		}
	}

	// Every destination, checked before anything is created.
	destinations := make([]string, len(selected))
	for i, entry := range selected {
		destination := filepath.Join(root, filepath.FromSlash(entry.Path))
		if !withinRoot(root, destination) {
			return nil, fmt.Errorf("%w: %q resolves outside the destination", ErrUnsafePath, entry.Path)
		}
		if !options.Replace {
			if _, err := os.Lstat(destination); err == nil {
				return nil, fmt.Errorf("%w: %s is already there. Pass --replace to write over what is in the destination",
					ErrWouldOverwrite, destination)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("pack: checking %s: %w", destination, err)
			}
		}
		destinations[i] = destination
	}

	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("pack: opening %s: %w", archivePath, err)
	}
	defer file.Close()

	remaining := options.Budget.maxTotalSize()
	result := &ExtractResult{Dest: root, Skipped: skipped}

	var members map[string]*zip.File
	if inspection.Format == FormatPK3 {
		reader, err := openPK3(file, inspection.Size)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", archivePath, err)
		}
		members = map[string]*zip.File{}
		for _, member := range reader.File {
			members[member.Name] = member
		}
	}

	for i, entry := range selected {
		var (
			source io.Reader
			closer io.Closer
		)
		switch inspection.Format {
		case FormatPAK:
			source = openPAKMember(file, entry)
		case FormatPK3:
			member, ok := members[entry.Path]
			if !ok {
				return nil, fmt.Errorf("%w: %q is in the central directory and not in the archive", ErrMalformed, entry.Path)
			}
			opened, err := member.Open()
			if err != nil {
				return nil, fmt.Errorf("pack: reading %q: %w", entry.Path, err)
			}
			source, closer = opened, opened
		}
		written, digest, err := writeMember(root, destinations[i], entry, source, remaining, options.Replace)
		if closer != nil {
			closer.Close()
		}
		if err != nil {
			return nil, err
		}
		remaining -= written
		entry.SHA256 = digest
		result.Files = append(result.Files, entry)
		result.Bytes += written
	}
	return result, nil
}

// writeMember writes one member, and refuses one whose bytes do not match what
// it declared.
//
// The root is passed in because resolving the destination once, before the
// loop, is not enough. `dest` itself may be a link — that is what the earlier
// EvalSymlinks handles — but so may any directory *inside* it, and a link that
// was already sitting at `dest/maps` before this program ran is a link every
// member under `maps/` would be written through. `MkdirAll` is happy to accept
// one as an existing directory, and `OpenFile` follows it. So the parent is
// resolved after it has been created and checked against the root again.
func writeMember(root, destination string, entry Entry, source io.Reader, remaining int64, replace bool) (int64, string, error) {
	if entry.Size > remaining {
		return 0, "", fmt.Errorf("%w: %q declares %d bytes and %d are left in this extraction's budget",
			ErrArchiveBomb, entry.Path, entry.Size, remaining)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return 0, "", fmt.Errorf("pack: creating %s: %w", parent, err)
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return 0, "", fmt.Errorf("pack: resolving %s: %w", parent, err)
	}
	if !withinRoot(root, resolved) {
		return 0, "", fmt.Errorf("%w: %q would be written into %s, which is outside the destination — "+
			"a directory on the way there is a symbolic link", ErrUnsafePath, entry.Path, resolved)
	}
	// The last component too. O_EXCL refuses an existing symlink on its own,
	// but the replacing path uses O_TRUNC, which follows one.
	if replace {
		if info, err := os.Lstat(destination); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return 0, "", fmt.Errorf("%w: %s is a symbolic link, and writing through it would put %q somewhere this command did not name",
				ErrUnsafePath, destination, entry.Path)
		}
	}
	// 0o644, not 0o600 and not the archive's idea: PAK carries no mode at all,
	// and a PK3's is the packer's umask, which is not information about the
	// file. Nothing extracted from a game archive is executable.
	//
	// The two paths differ in more than a flag.
	//
	// A first write is O_EXCL: no implicit overwrite ever, and O_EXCL refuses
	// an existing symlink on its own.
	//
	// A REPLACING write goes through a temporary file and a rename, and never
	// opens the destination at all. O_TRUNC would follow a HARD link — Lstat
	// cannot see one, because a hard link is not a kind of file, it is a second
	// name for the same one — so a member called `sound/x.wav` extracted over a
	// name somebody had already linked to their SSH key would write through to
	// the key. A rename replaces the directory entry instead, which is the one
	// operation that cannot write through anything, and it makes the replacing
	// path atomic as a side effect: an interrupted extraction leaves the old
	// file, never half of the new one.
	var file *os.File
	staged := ""
	if replace {
		var err error
		file, err = os.CreateTemp(parent, ".extracting-*")
		if err != nil {
			return 0, "", fmt.Errorf("pack: creating a temporary file in %s: %w", parent, err)
		}
		staged = file.Name()
		defer os.Remove(staged) // No-op once the rename below has succeeded.
		if err := file.Chmod(0o644); err != nil {
			file.Close()
			return 0, "", fmt.Errorf("pack: securing %s: %w", staged, err)
		}
	} else {
		opened, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return 0, "", fmt.Errorf("%w: %s appeared while this archive was being extracted", ErrWouldOverwrite, destination)
			}
			return 0, "", fmt.Errorf("pack: creating %s: %w", destination, err)
		}
		file = opened
	}

	written, digest, err := writeExtracted(file, source, entry)
	if err != nil {
		if staged == "" {
			os.Remove(destination)
		}
		return 0, "", err
	}
	if staged != "" {
		if err := os.Rename(staged, destination); err != nil {
			return 0, "", fmt.Errorf("pack: replacing %s: %w", destination, err)
		}
	}
	return written, digest, nil
}

// writeExtracted writes one member's bytes, closes the file, and reports the
// digest. It removes nothing: the caller knows whether the thing it opened is
// the destination or a staging file, and only the caller can clean up safely.
func writeExtracted(file *os.File, source io.Reader, entry Entry) (int64, string, error) {
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(source, entry.Size+1))
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return 0, "", fmt.Errorf("pack: writing %s: %w", file.Name(), copyErr)
	}
	if written != entry.Size {
		return 0, "", fmt.Errorf("%w: %q declares %d bytes and produced %d",
			ErrMalformed, entry.Path, entry.Size, written)
	}
	return written, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// withinRoot reports whether a path is inside a directory, after both have been
// cleaned.
func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func containsPath(entries []Entry, name string) bool {
	for _, entry := range entries {
		if entry.Path == name {
			return true
		}
	}
	return false
}

func sortedStrings(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
