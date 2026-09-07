package acquire

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
)

// Unpacking an archive somebody else made.
//
// Everything below is a refusal, and none of it is a repair. That is the whole
// design: an archive that names `../../../.ssh/authorized_keys`, or contains a
// symlink to `/etc`, or expands three kilobytes into four gigabytes, is not a
// malformed archive to be tidied up. It is a document that has already told you
// what it is for. Extracting a sanitised version of it means acting on it
// anyway, and leaves the interesting question — what else was in there — to
// whoever reads the logs afterwards.
//
// The digest has already been checked by the time anything here runs, so a
// hostile archive is one a *verified catalogue entry* pointed at. That is not a
// hypothetical: a compromised publisher signs exactly such an entry, and the
// signature chain is what makes revocation possible, not what makes extraction
// safe. These checks are the second half.

// ErrUnsafeArchive reports an archive member this program will not write.
var ErrUnsafeArchive = errors.New("acquire: unsafe archive")

// extraction is one unpack in progress, carrying the budgets everything is
// checked against.
type extraction struct {
	root string
	// remaining is the unpacked-byte budget: the smaller of what the catalogue
	// declared and this build's own ceiling, further bounded by a ratio
	// against the archive's own size.
	remaining int64
	entries   int
	seen      map[string]bool
	files     []FileRecord
	// executables is the declared name for each path that must end up
	// executable, so that a tool whose archive did not carry a permission bit
	// still runs.
	executables map[string]bool
}

func newExtraction(root string, archiveSize, declaredUnpacked int64, executables []string) *extraction {
	budget := declaredUnpacked
	if budget <= 0 || budget > catalog.MaxUnpackedSize {
		budget = catalog.MaxUnpackedSize
	}
	if ratio := archiveSize * catalog.MaxCompressionRatio; ratio > 0 && ratio < budget {
		budget = ratio
	}
	wanted := make(map[string]bool, len(executables))
	for _, path := range executables {
		wanted[path] = true
	}
	return &extraction{root: root, remaining: budget, seen: map[string]bool{}, executables: wanted}
}

// unpack writes one archive into a staging directory and returns what it wrote.
func unpack(kind, archivePath, root string, archiveSize, declaredUnpacked int64, executables []string) ([]FileRecord, error) {
	e := newExtraction(root, archiveSize, declaredUnpacked, executables)
	var err error
	switch kind {
	case catalog.KindZip:
		err = e.unpackZip(archivePath)
	case catalog.KindTarGz:
		err = e.unpackTarGz(archivePath)
	default:
		return nil, fmt.Errorf("acquire: %q is not an archive kind this build unpacks", kind)
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(e.files, func(i, j int) bool { return e.files[i].Path < e.files[j].Path })
	return e.files, nil
}

func (e *extraction) unpackZip(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("acquire: reading the zip archive: %w", err)
	}
	defer reader.Close()

	if len(reader.File) > catalog.MaxArchiveEntries {
		return fmt.Errorf("%w: it has %d members, over the %d limit",
			ErrUnsafeArchive, len(reader.File), catalog.MaxArchiveEntries)
	}
	for _, entry := range reader.File {
		mode := entry.Mode()
		name := entry.Name
		if strings.HasSuffix(name, "/") {
			// A directory member. Directories are created as their files need
			// them, so an empty one carries no information worth the risk of
			// treating its name as a path to create.
			if err := checkMemberName(strings.TrimSuffix(name, "/")); err != nil {
				return err
			}
			continue
		}
		if err := checkMemberName(name); err != nil {
			return err
		}
		if err := checkMemberMode(name, mode); err != nil {
			return err
		}
		file, err := entry.Open()
		if err != nil {
			return fmt.Errorf("acquire: reading %s from the archive: %w", name, err)
		}
		err = e.write(name, mode, file)
		file.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *extraction) unpackTarGz(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("acquire: reading the archive: %w", err)
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("acquire: reading the gzip stream: %w", err)
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("acquire: reading the tar stream: %w", err)
		}
		e.entries++
		if e.entries > catalog.MaxArchiveEntries {
			return fmt.Errorf("%w: it has more than the %d members this build will read",
				ErrUnsafeArchive, catalog.MaxArchiveEntries)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := checkMemberName(strings.TrimSuffix(header.Name, "/")); err != nil {
				return err
			}
			continue
		case tar.TypeReg:
			// The only kind that is written.
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("%w: %s is a link to %q, and a link is a way to write outside the archive "+
				"or to make one file two", ErrUnsafeArchive, header.Name, header.Linkname)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return fmt.Errorf("%w: %s is a device or a pipe, which a toolchain has no use for",
				ErrUnsafeArchive, header.Name)
		default:
			return fmt.Errorf("%w: %s is a tar member of type %q, which this build does not write",
				ErrUnsafeArchive, header.Name, string(header.Typeflag))
		}
		if err := checkMemberName(header.Name); err != nil {
			return err
		}
		mode := header.FileInfo().Mode()
		if err := checkMemberMode(header.Name, mode); err != nil {
			return err
		}
		if err := e.write(header.Name, mode, reader); err != nil {
			return err
		}
	}
}

// checkMemberName applies the path rules to an archive member.
func checkMemberName(name string) error {
	if err := catalog.CheckArchivePath(name); err != nil {
		return fmt.Errorf("%w: the member %q %w", ErrUnsafeArchive, name, err)
	}
	return nil
}

// checkMemberMode refuses the permission bits that mean something dangerous.
//
// setuid and setgid on a file this program is about to make executable would be
// a privilege escalation delivered by download. The sticky bit is meaningless
// on a regular file and is a sign the archive was built from something that was
// not a toolchain.
func checkMemberMode(name string, mode fs.FileMode) error {
	if mode&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symbolic link", ErrUnsafeArchive, name)
	}
	if !mode.IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrUnsafeArchive, name)
	}
	if mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
		return fmt.Errorf("%w: %s carries setuid, setgid or sticky bits", ErrUnsafeArchive, name)
	}
	return nil
}

// write extracts one member, enforcing the unpacked-size budget as it goes.
func (e *extraction) write(name string, mode fs.FileMode, source io.Reader) error {
	if e.seen[name] {
		return fmt.Errorf("%w: %s appears twice, so what ends up on disk depends on the order it is read in",
			ErrUnsafeArchive, name)
	}
	e.seen[name] = true

	destination := filepath.Join(e.root, filepath.FromSlash(name))
	// Belt and braces after checkMemberName: the path that is actually opened
	// is the one that has to be inside the root, and asserting it here means a
	// future change to the name rules cannot quietly widen this.
	if !withinRoot(e.root, destination) {
		return fmt.Errorf("%w: %s resolves outside the extraction directory", ErrUnsafeArchive, name)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("acquire: creating a directory for %s: %w", name, err)
	}

	permissions := fs.FileMode(0o600)
	if mode&0o111 != 0 || e.executables[name] {
		permissions = 0o700
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
	if err != nil {
		return fmt.Errorf("acquire: creating %s: %w", name, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(source, e.remaining+1))
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("acquire: writing %s: %w", name, copyErr)
	}
	if written > e.remaining {
		return fmt.Errorf("%w: it expands to more than the %d bytes this download is allowed to occupy",
			ErrUnsafeArchive, e.remaining)
	}
	e.remaining -= written

	e.files = append(e.files, FileRecord{
		Path:   name,
		Size:   written,
		Mode:   uint32(permissions),
		SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	})
	return nil
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

// installSingleFile is the `file` kind: the download itself is the executable.
func installSingleFile(archivePath, root, name string) (FileRecord, error) {
	destination := filepath.Join(root, filepath.FromSlash(name))
	if err := catalog.CheckArchivePath(name); err != nil {
		return FileRecord{}, fmt.Errorf("acquire: the executable name %q %w", name, err)
	}
	source, err := os.Open(archivePath)
	if err != nil {
		return FileRecord{}, fmt.Errorf("acquire: reading the download: %w", err)
	}
	defer source.Close()

	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return FileRecord{}, fmt.Errorf("acquire: creating %s: %w", name, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), source)
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return FileRecord{}, fmt.Errorf("acquire: writing %s: %w", name, copyErr)
	}
	return FileRecord{
		Path:   name,
		Size:   written,
		Mode:   uint32(fs.FileMode(0o700)),
		SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}, nil
}
