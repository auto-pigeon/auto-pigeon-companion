package managedtools

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/acquire"
)

// ArchiveManifestName is the file list every official archive carries at its
// root: every other file of the archive with its size, mode and SHA-256.
const ArchiveManifestName = "MANIFEST.json"

// archiveManifest is the part of that file this package reads.
type archiveManifest struct {
	Schema   string        `json:"schema"`
	Root     string        `json:"root"`
	Platform string        `json:"platform"`
	Version  string        `json:"version"`
	Commit   string        `json:"commit"`
	Files    []archiveFile `json:"files"`
}

type archiveFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
}

// Limits bound what one archive may put on disk. Today's archives are 25 and
// 35 files, 49 and 51 MB unpacked; these leave room for growth and none for a
// zip bomb.
type Limits struct {
	MaxFiles      int
	MaxTotal      int64
	MaxFile       int64
	MaxCompressed int64
}

// DefaultLimits are the production bounds.
var DefaultLimits = Limits{MaxFiles: 2048, MaxTotal: 1 << 30, MaxFile: 512 << 20, MaxCompressed: 512 << 20}

// ExtractError is an archive that is not what its release said. Every one is
// a refusal: nothing it contains is run, and the staging directory it was
// being written to is removed.
type ExtractError struct{ Reason string }

func (e *ExtractError) Error() string { return "managedtools: the archive was refused: " + e.Reason }

func refuse(format string, args ...any) error {
	return &ExtractError{Reason: fmt.Sprintf(format, args...)}
}

// extract unpacks a verified archive into destination, which must be an
// empty directory this package created, and checks every file it writes
// against the archive's own file list. The archive's single top-level folder
// is not kept: destination becomes the installation's root.
func extract(archivePath, destination string, tool Tool, candidate Candidate, limits Limits) (*archiveManifest, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, refuse("it is not a readable zip archive: %v", err)
	}
	defer reader.Close()

	if len(reader.File) > limits.MaxFiles {
		return nil, refuse("it holds %d entries; at most %d are accepted", len(reader.File), limits.MaxFiles)
	}
	type entry struct {
		file     *zip.File
		relative string
		dir      bool
	}
	var root string
	seen := map[string]string{}
	var entries []entry
	var claimed int64
	var manifestFile *zip.File
	for _, file := range reader.File {
		name := file.Name
		switch {
		case name == "" || strings.ContainsRune(name, 0):
			return nil, refuse("an entry has an empty or NUL-carrying name")
		case strings.ContainsRune(name, '\\'):
			return nil, refuse("the entry %q uses a backslash", name)
		case strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') || strings.HasPrefix(name, "//"):
			return nil, refuse("the entry %q is an absolute path", name)
		}
		mode := file.Mode()
		dir := mode.IsDir() || strings.HasSuffix(name, "/")
		if !dir && !mode.IsRegular() {
			// A symbolic link, a device, a pipe: none belongs in a toolchain
			// archive, and a link is how an archive writes outside itself.
			return nil, refuse("the entry %q is not a regular file or a folder (%s)", name, mode.Type())
		}
		trimmed := strings.TrimSuffix(name, "/")
		top, rest, _ := strings.Cut(trimmed, "/")
		if root == "" {
			root = top
			if err := acquire.CheckRelativePath(root); err != nil {
				return nil, refuse("its top-level folder %q %v", root, err)
			}
		}
		if top != root {
			return nil, refuse("the entry %q is outside the archive's one top-level folder %q", name, root)
		}
		if rest == "" {
			if !dir {
				return nil, refuse("the archive's top level is a file, not a folder")
			}
			continue
		}
		if err := acquire.CheckRelativePath(rest); err != nil {
			return nil, refuse("the entry %q %v", name, err)
		}
		folded := strings.ToLower(rest)
		if previous, clash := seen[folded]; clash {
			return nil, refuse("the entries %q and %q are the same file on a disk that ignores case", previous, rest)
		}
		seen[folded] = rest
		if dir {
			entries = append(entries, entry{file: file, relative: rest, dir: true})
			continue
		}
		if file.UncompressedSize64 > uint64(limits.MaxFile) {
			return nil, refuse("%s claims %d bytes; at most %d are accepted for one file", rest, file.UncompressedSize64, limits.MaxFile)
		}
		claimed += int64(file.UncompressedSize64)
		if claimed > limits.MaxTotal {
			return nil, refuse("it unpacks to more than %d bytes", limits.MaxTotal)
		}
		if rest == ArchiveManifestName {
			manifestFile = file
			continue
		}
		entries = append(entries, entry{file: file, relative: rest})
	}
	if manifestFile == nil {
		return nil, refuse("it carries no %s, so nothing says what it should contain", ArchiveManifestName)
	}
	manifest, err := readArchiveManifest(manifestFile)
	if err != nil {
		return nil, err
	}
	switch {
	case !contains(tool.ArchiveSchemas, manifest.Schema):
		return nil, refuse("its %s is %q, which this Companion cannot read", ArchiveManifestName, manifest.Schema)
	case manifest.Root != root:
		return nil, refuse("its file list names the folder %q and the archive's folder is %q", manifest.Root, root)
	case manifest.Platform != candidate.Target:
		return nil, refuse("it is a %s build and this computer needs %s", manifest.Platform, candidate.Target)
	case manifest.Version != candidate.Version:
		return nil, refuse("it is release %s and the release it was downloaded from is %s", manifest.Version, candidate.Version)
	case len(manifest.Files) == 0:
		return nil, refuse("its file list is empty")
	}
	listed := make(map[string]archiveFile, len(manifest.Files))
	for _, file := range manifest.Files {
		if err := acquire.CheckRelativePath(file.Path); err != nil {
			return nil, refuse("its file list names %q, which %v", file.Path, err)
		}
		if !hexDigest.MatchString(file.SHA256) || file.Size < 0 {
			return nil, refuse("its file list gives no SHA-256 or size for %s", file.Path)
		}
		if _, twice := listed[file.Path]; twice {
			return nil, refuse("its file list names %s twice", file.Path)
		}
		listed[file.Path] = file
	}
	present := map[string]bool{}
	for _, item := range entries {
		if item.dir {
			continue
		}
		if _, ok := listed[item.relative]; !ok {
			return nil, refuse("it contains %s, which its file list does not name", item.relative)
		}
		present[item.relative] = true
	}
	for name := range listed {
		if !present[name] {
			return nil, refuse("its file list names %s and the archive does not contain it", name)
		}
	}
	for _, program := range tool.Programs {
		name := program + exeSuffix(candidate.Target)
		if _, ok := listed[name]; !ok {
			return nil, refuse("it does not contain %s", name)
		}
	}

	// Writing. Every path is joined under destination and checked to stay
	// there, after the checks above made that impossible to fail — because a
	// check that "cannot fail" is the one that should be cheapest to keep.
	for _, item := range entries {
		target := filepath.Join(destination, filepath.FromSlash(item.relative))
		if !within(destination, target) {
			return nil, refuse("%s resolves outside the installation", item.relative)
		}
		if item.dir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := writeEntry(item.file, target, listed[item.relative], limits); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

func readArchiveManifest(file *zip.File) (*archiveManifest, error) {
	if file.UncompressedSize64 > maxManifestBytes {
		return nil, refuse("its %s is larger than %d bytes", ArchiveManifestName, maxManifestBytes)
	}
	opened, err := file.Open()
	if err != nil {
		return nil, refuse("its %s cannot be read: %v", ArchiveManifestName, err)
	}
	defer opened.Close()
	raw, err := io.ReadAll(io.LimitReader(opened, maxManifestBytes+1))
	if err != nil {
		return nil, refuse("its %s cannot be read: %v", ArchiveManifestName, err)
	}
	var manifest archiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, refuse("its %s is not JSON: %v", ArchiveManifestName, err)
	}
	return &manifest, nil
}

// writeEntry writes one file, refusing it unless its bytes are exactly the
// ones the file list names.
func writeEntry(file *zip.File, target string, expected archiveFile, limits Limits) error {
	opened, err := file.Open()
	if err != nil {
		return refuse("%s cannot be read: %v", expected.Path, err)
	}
	defer opened.Close()
	mode := os.FileMode(0o644)
	if strings.ContainsAny(expected.Mode, "1357") && len(expected.Mode) >= 3 {
		mode = 0o755
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(opened, limits.MaxFile+1))
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		var zipErr error = copyErr
		if errors.Is(copyErr, zip.ErrChecksum) || errors.Is(copyErr, io.ErrUnexpectedEOF) {
			return refuse("%s is truncated or corrupt: %v", expected.Path, zipErr)
		}
		return refuse("%s could not be unpacked: %v", expected.Path, copyErr)
	case closeErr != nil:
		return closeErr
	case written != expected.Size:
		return refuse("%s is %d bytes; its file list says %d", expected.Path, written, expected.Size)
	case hex.EncodeToString(hash.Sum(nil)) != expected.SHA256:
		return refuse("%s is not the file its file list names (SHA-256 differs)", expected.Path)
	}
	// Created with mode; an umask may have narrowed it, and an executable
	// that cannot be executed is a program that is not there.
	return os.Chmod(target, mode)
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// programPath is one of an installation's programs, as an absolute path.
func programPath(root, program, target string) string {
	return filepath.Join(root, filepath.FromSlash(path.Clean(program)+exeSuffix(target)))
}
