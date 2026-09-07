package pack

import (
	"archive/zip"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"time"
)

// The PK3 container: a ZIP, read by Quake III and everything descended from it.
//
// # What has to be pinned to make two runs agree
//
// ZIP is an extensible format written by dozens of implementations, and almost
// everything it records about a file is a property of the machine that packed
// it rather than of the file. Left alone, `archive/zip` writes the modification
// time it is given, an Info-ZIP extended-timestamp extra field beside it, a
// creator-version byte naming the packing platform, and external attributes
// carrying Unix permission bits. Every one of those differs between two
// machines, and three of them differ between two runs on the same machine.
//
// So each is set explicitly, and the reasons are worth having written down:
//
//   - **Modification time** is the MS-DOS epoch, 1980-01-01 00:00:00, written
//     into the legacy date and time fields. `FileHeader.Modified` is left at
//     its zero value on purpose: `archive/zip` appends the extended-timestamp
//     extra field if and only if that field is set, so leaving it zero is how
//     the extra field is kept out. There is nowhere else a timestamp can hide.
//   - **Extra fields** are otherwise empty. An engine reads none of them.
//   - **CreatorVersion** is zero, which `archive/zip` completes to "version 2.0,
//     MS-DOS". Not because the archive is from MS-DOS, but because that is the
//     one value that says nothing about the machine.
//   - **ExternalAttrs** is zero. A PK3 is read by an engine that does not
//     consult permission bits, and this package's own extractor sets the mode
//     it wants; carrying the packer's umask into a download is leaking a
//     machine setting into a published artifact.
//   - **Member order** is byte-wise on the path, as everywhere else here.
//
// # The one thing that is not pinned
//
// The deflate encoder. Its output is a property of the Go standard library, not
// of this program, and a future Go release may legitimately produce a smaller
// stream for the same bytes. So a deflated PK3 is [PerBuild]: byte-identical
// for one build of the Companion, on any operating system, and not promised
// across two. A caller who needs the stronger promise asks for [Store], which
// puts no compressor in the path at all and is [Portable] like PAK. The
// difference is stated in the sidecar manifest rather than left to be inferred,
// and `TestPK3StoredIsPortableAcrossPlatformInputs` is what keeps it honest.

// dosEpoch is the earliest instant MS-DOS's packed date can express, and the
// timestamp every member written by this package carries.
//
// Not the Unix epoch: 1970 is not representable, and `archive/zip` would encode
// it as some other date. Not "now", for the obvious reason. Not the source
// file's own mtime, which is the tempting choice and the wrong one — it makes
// the archive a record of when the packager's checkout happened.
var dosEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// msDOSDate and msDOSTime are dosEpoch in the packed 16-bit fields ZIP stores:
// date is `(year-1980)<<9 | month<<5 | day`, time is `hour<<11 | minute<<5 |
// second/2`.
const (
	msDOSDate = uint16(1<<5 | 1) // 1980-01-01
	msDOSTime = uint16(0)        // 00:00:00
)

// WritePK3 writes members as a PK3 and returns the entries it wrote.
func WritePK3(dst io.Writer, members []Member, target Target) ([]Entry, error) {
	if target.Format != FormatPK3 {
		return nil, fmt.Errorf("pack: %s is not a PK3 target", target.ID)
	}
	if err := checkMembers(members, target); err != nil {
		return nil, err
	}
	method := uint16(zip.Deflate)
	if target.Compression == Store {
		method = zip.Store
	}

	ordered := append([]Member(nil), members...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })

	writer := zip.NewWriter(dst)
	// Register the compressor explicitly at the default level rather than
	// inheriting whatever archive/zip's default happens to be, so that a change
	// to that default is not a silent change to every archive this program has
	// ever written.
	writer.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.DefaultCompression)
	})

	entries := make([]Entry, 0, len(ordered))
	for _, member := range ordered {
		header := &zip.FileHeader{
			Name:   member.Path,
			Method: method,
			// See the pinning notes above. Modified stays zero.
			ModifiedDate:   msDOSDate,
			ModifiedTime:   msDOSTime,
			CreatorVersion: 0,
			ExternalAttrs:  0,
		}
		part, err := writer.CreateHeader(header)
		if err != nil {
			return nil, fmt.Errorf("pack: starting %q in the PK3: %w", member.Path, err)
		}
		written, digest, err := copyMember(part, member)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{
			Path:        member.Path,
			Size:        written,
			SHA256:      digest,
			Compression: target.Compression,
		})
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("pack: finishing the PK3: %w", err)
	}
	return entries, nil
}

// ReadPK3 parses a PK3's central directory without decompressing anything.
//
// The bomb check happens here, against the sizes the central directory
// declares, and that is the only place it can happen: by the time a member is
// being decompressed the archive has already been believed. A member whose
// actual decompressed length disagrees with what it declared is caught later,
// by [verifyPK3Member], and is treated as a lie rather than as a discrepancy.
func ReadPK3(r io.ReaderAt, size int64, budget Budget) ([]Entry, []string, error) {
	reader, err := zip.NewReader(r, size)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: it is not a readable ZIP: %v", ErrMalformed, err)
	}
	if len(reader.File) > budget.maxEntries() {
		return nil, nil, fmt.Errorf("%w: it declares %d members, over the %d this build reads",
			ErrArchiveBomb, len(reader.File), budget.maxEntries())
	}

	entries := make([]Entry, 0, len(reader.File))
	sizes := make([]int64, 0, len(reader.File))
	var notes []string
	directories := 0
	for _, file := range reader.File {
		name := file.Name
		if isZipDirectory(file) {
			// A directory member carries no data. It is not extracted —
			// directories are created as the files inside them need them — but
			// its name is still checked, because a directory called
			// `../../etc` is the same statement of intent as a file called one.
			directories++
			if err := CheckEntryPath(trimTrailingSlash(name), PK3NameLength); err != nil {
				return nil, nil, fmt.Errorf("%w: the directory member %q %w", ErrUnsafePath, name, err)
			}
			continue
		}
		if err := CheckEntryPath(name, PK3NameLength); err != nil {
			return nil, nil, fmt.Errorf("%w: the member %q %w", ErrUnsafePath, name, err)
		}
		if mode := file.Mode(); !mode.IsRegular() {
			return nil, nil, fmt.Errorf("%w: %q is not a regular file (mode %s); a symbolic link or a device in an archive is a way to write outside it",
				ErrUnsafePath, name, mode)
		}
		declared := int64(file.UncompressedSize64)
		if file.UncompressedSize64 > 1<<62 {
			return nil, nil, fmt.Errorf("%w: %q declares %d bytes", ErrArchiveBomb, name, file.UncompressedSize64)
		}
		compression := Store
		if file.Method == zip.Deflate {
			compression = Deflate
		} else if file.Method != zip.Store {
			return nil, nil, fmt.Errorf("%w: %q uses compression method %d, which this build does not decompress",
				ErrMalformed, name, file.Method)
		}
		offset, err := file.DataOffset()
		if err != nil {
			// Not fatal: the offset is reported for inspection, and a header
			// this build cannot locate is still a member it can read through
			// archive/zip.
			offset = 0
		}
		entries = append(entries, Entry{
			Path:        name,
			Size:        declared,
			Compression: compression,
			StoredSize:  int64(file.CompressedSize64),
			Offset:      offset,
			CRC32:       file.CRC32,
		})
		sizes = append(sizes, declared)
	}
	budget.ArchiveSize = size
	if err := budget.CheckDeclared(sizes); err != nil {
		return nil, nil, err
	}
	if directories > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d members are directory records, which carry no data and are not extracted", directories))
	}
	if reader.Comment != "" {
		notes = append(notes, "the archive carries a ZIP comment, which no engine reads")
	}
	sortEntries(entries)
	return entries, notes, nil
}

func isZipDirectory(file *zip.File) bool {
	return len(file.Name) > 0 && file.Name[len(file.Name)-1] == '/'
}

func trimTrailingSlash(name string) string {
	for len(name) > 0 && name[len(name)-1] == '/' {
		name = name[:len(name)-1]
	}
	return name
}

// openPK3 returns the stock reader, for the paths that need member data.
func openPK3(r io.ReaderAt, size int64) (*zip.Reader, error) {
	reader, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: it is not a readable ZIP: %v", ErrMalformed, err)
	}
	return reader, nil
}

// verifyPK3Member decompresses one member and checks it against what it
// declared: the length, the CRC the ZIP itself carries, and a SHA-256 for the
// caller.
//
// The length check is the one that matters. `archive/zip` verifies the CRC on
// its own when a member is read to EOF, but only for the bytes it actually
// produced — a member that declares four gigabytes and stops after three
// kilobytes has a correct CRC over three kilobytes. The declared size is the
// number a caller allocated against, so it is the number that has to be true.
func verifyPK3Member(file *zip.File, limit int64) (int64, string, error) {
	reader, err := file.Open()
	if err != nil {
		return 0, "", fmt.Errorf("pack: reading %q: %w", file.Name, err)
	}
	defer reader.Close()

	hash := sha256.New()
	sum := crc32.NewIEEE()
	written, err := io.Copy(io.MultiWriter(hash, sum), io.LimitReader(reader, limit+1))
	if err != nil {
		// archive/zip compares against the declared length itself and reports
		// a short stream as an unexpected EOF. That is the same fault this
		// function is here to name, so it is named rather than passed on:
		// "unexpected EOF" is a symptom and "its data ends before that" is the
		// diagnosis. The count is deliberately not quoted here — archive/zip
		// discards the partial read on its way out, so `written` at this point
		// is not a number anybody should be shown.
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, "", fmt.Errorf("%w: %q declares %d bytes and its data ends before that",
				ErrMalformed, file.Name, file.UncompressedSize64)
		}
		return 0, "", fmt.Errorf("pack: reading %q: %w", file.Name, err)
	}
	if written > limit {
		return 0, "", fmt.Errorf("%w: %q expands past the %d bytes left in this archive's budget",
			ErrArchiveBomb, file.Name, limit)
	}
	if uint64(written) != file.UncompressedSize64 {
		return 0, "", fmt.Errorf("%w: %q declares %d bytes and produces %d",
			ErrMalformed, file.Name, file.UncompressedSize64, written)
	}
	if sum.Sum32() != file.CRC32 {
		return 0, "", fmt.Errorf("%w: %q does not match its own checksum", ErrMalformed, file.Name)
	}
	return written, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
