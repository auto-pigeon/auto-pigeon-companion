package pack

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
)

// The PACK container, as Quake and Quake II define it.
//
//	header (12 bytes)         directory record (64 bytes, repeated)
//	  char  magic[4]  "PACK"    char  name[56]   NUL-terminated, '/'-separated
//	  int32 dirofs              int32 filepos
//	  int32 dirlen              int32 filelen
//
// Little-endian throughout, member data stored verbatim, directory
// conventionally at the end. Both games use the identical structure, which is
// why there is one implementation and two targets rather than two of each.
//
// Three properties follow from that layout and they are the reason PAK is the
// easier of the two formats to promise anything about:
//
//   - Every integer is *signed* 32-bit, so the whole container is bounded at
//     2 GiB and every offset arithmetic here has to be checked before it is
//     performed rather than after it has wrapped.
//   - There is nowhere to put a timestamp, a permission bit, a creator name or
//     an extension record. An archive written from the same members is the same
//     archive, on every operating system, forever. That is [Portable], and it
//     is a property of the format rather than a discipline this code maintains.
//   - Nothing says two directory records may not address the same bytes. A
//     writer that deduplicates identical members produces exactly that, and so
//     does an archive built to extract to ten terabytes out of a hundred
//     honest megabytes. They are distinguished by the budget, not by the
//     overlap: see [Budget.CheckDeclared], which sums what the directory
//     *declares* and so counts a shared region once per record that claims it.

const (
	pakMagic         = "PACK"
	pakHeaderSize    = 12
	pakRecordSize    = 64
	pakNameFieldSize = 56
)

// WritePAK writes members as a PAK and returns the entries it wrote.
//
// The destination must be seekable because the directory's position is not
// known until the data has been written, and the header carries that position.
// Streaming it would mean either buffering every member in memory or writing
// the directory first at a guessed offset, and a guessed offset is how an
// archive ends up with a gap in it that differs between two runs.
func WritePAK(dst io.WriteSeeker, members []Member, target Target) ([]Entry, error) {
	if target.Format != FormatPAK {
		return nil, fmt.Errorf("pack: %s is not a PAK target", target.ID)
	}
	if err := checkMembers(members, target); err != nil {
		return nil, err
	}
	ordered := append([]Member(nil), members...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })

	if _, err := dst.Write(make([]byte, pakHeaderSize)); err != nil {
		return nil, fmt.Errorf("pack: reserving the PAK header: %w", err)
	}
	offset := int64(pakHeaderSize)

	entries := make([]Entry, 0, len(ordered))
	for _, member := range ordered {
		if offset > PAKMaxOffset-member.Size {
			return nil, fmt.Errorf("pack: %q would end past PAK's %d-byte limit; a PAK addresses its members with a signed 32-bit offset",
				member.Path, int64(PAKMaxOffset))
		}
		written, digest, err := copyMember(dst, member)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{
			Path:        member.Path,
			Size:        written,
			SHA256:      digest,
			Compression: Store,
			StoredSize:  written,
			Offset:      offset,
		})
		offset += written
	}

	dirOffset := offset
	dirLength := int64(len(entries)) * pakRecordSize
	if dirOffset > PAKMaxOffset-dirLength {
		return nil, fmt.Errorf("pack: the directory would end past PAK's %d-byte limit", int64(PAKMaxOffset))
	}
	record := make([]byte, pakRecordSize)
	for _, entry := range entries {
		for i := range record {
			record[i] = 0
		}
		// Checked in checkMembers against target.MaxNameLength, which for
		// every PAK target is PAKNameLength; asserted again because this is
		// the copy that would silently truncate.
		if len(entry.Path) > PAKNameLength {
			return nil, fmt.Errorf("pack: %q is %d bytes and PAK's name field holds %d plus a terminator",
				entry.Path, len(entry.Path), PAKNameLength)
		}
		copy(record, entry.Path)
		binary.LittleEndian.PutUint32(record[pakNameFieldSize:], uint32(entry.Offset))
		binary.LittleEndian.PutUint32(record[pakNameFieldSize+4:], uint32(entry.Size))
		if _, err := dst.Write(record); err != nil {
			return nil, fmt.Errorf("pack: writing the directory record for %q: %w", entry.Path, err)
		}
	}

	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("pack: returning to the PAK header: %w", err)
	}
	header := make([]byte, pakHeaderSize)
	copy(header, pakMagic)
	binary.LittleEndian.PutUint32(header[4:], uint32(dirOffset))
	binary.LittleEndian.PutUint32(header[8:], uint32(dirLength))
	if _, err := dst.Write(header); err != nil {
		return nil, fmt.Errorf("pack: writing the PAK header: %w", err)
	}
	if _, err := dst.Seek(0, io.SeekEnd); err != nil {
		return nil, fmt.Errorf("pack: finishing the PAK: %w", err)
	}
	return entries, nil
}

// copyMember writes one member's bytes and returns what it actually copied.
//
// The length is verified against the plan rather than trusted: a member whose
// file grew between the review and the write is not a member with a stale size,
// it is a different file, and the archive would carry a digest for bytes nobody
// looked at.
func copyMember(dst io.Writer, member Member) (int64, string, error) {
	reader, err := member.Open()
	if err != nil {
		return 0, "", fmt.Errorf("pack: reading %s for %q: %w", member.Source, member.Path, err)
	}
	defer reader.Close()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(reader, member.Size+1))
	if err != nil {
		return 0, "", fmt.Errorf("pack: copying %q: %w", member.Path, err)
	}
	if written != member.Size {
		return 0, "", fmt.Errorf("pack: %q was %d bytes when it was selected and is %d now; %s changed underneath this package",
			member.Path, member.Size, written, member.Source)
	}
	return written, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// ReadPAK parses a PAK's structure without reading any member data.
//
// Every refusal below is about the directory disagreeing with the file it is
// in. The order matters: the magic, then the directory's own bounds, then each
// record's bounds, then the budget over what the records declare — so that
// nothing is allocated on the strength of a number that has not been checked
// against the actual length of the archive.
func ReadPAK(r io.ReaderAt, size int64, budget Budget) ([]Entry, []string, error) {
	if size < pakHeaderSize {
		return nil, nil, fmt.Errorf("%w: it is %d bytes, and a PAK header is %d", ErrMalformed, size, pakHeaderSize)
	}
	header := make([]byte, pakHeaderSize)
	if _, err := r.ReadAt(header, 0); err != nil {
		return nil, nil, fmt.Errorf("%w: reading the header: %v", ErrMalformed, err)
	}
	if string(header[:4]) != pakMagic {
		return nil, nil, fmt.Errorf("%w: it starts with %q, not %q", ErrMalformed, header[:4], pakMagic)
	}
	dirOffset := int64(int32(binary.LittleEndian.Uint32(header[4:])))
	dirLength := int64(int32(binary.LittleEndian.Uint32(header[8:])))
	switch {
	case dirOffset < pakHeaderSize:
		return nil, nil, fmt.Errorf("%w: the directory is at offset %d, inside the header", ErrMalformed, dirOffset)
	case dirLength < 0:
		return nil, nil, fmt.Errorf("%w: the directory declares a negative length (%d)", ErrMalformed, dirLength)
	case dirLength%pakRecordSize != 0:
		return nil, nil, fmt.Errorf("%w: the directory is %d bytes, which is not a whole number of %d-byte records",
			ErrMalformed, dirLength, pakRecordSize)
	case dirOffset > size-dirLength:
		return nil, nil, fmt.Errorf("%w: the directory runs from %d for %d bytes, past the end of a %d-byte file",
			ErrMalformed, dirOffset, dirLength, size)
	}

	count := int(dirLength / pakRecordSize)
	if count > budget.maxEntries() {
		return nil, nil, fmt.Errorf("%w: its directory declares %d members, over the %d this build reads",
			ErrArchiveBomb, count, budget.maxEntries())
	}

	directory := make([]byte, dirLength)
	if _, err := r.ReadAt(directory, dirOffset); err != nil {
		return nil, nil, fmt.Errorf("%w: reading the directory: %v", ErrMalformed, err)
	}

	entries := make([]Entry, 0, count)
	sizes := make([]int64, 0, count)
	var notes []string
	for i := 0; i < count; i++ {
		record := directory[i*pakRecordSize : (i+1)*pakRecordSize]
		name, terminated := pakName(record[:pakNameFieldSize])
		if !terminated {
			// Refused rather than truncated to 56 bytes. Every engine reads
			// this field as a C string, so a record with no terminator is one
			// where each reader gets a different name depending on what
			// happens to follow it in memory.
			return nil, nil, fmt.Errorf("%w: directory record %d fills the whole %d-byte name field with no terminator",
				ErrMalformed, i, pakNameFieldSize)
		}
		if err := CheckEntryPath(name, PAKNameLength); err != nil {
			return nil, nil, fmt.Errorf("%w: directory record %d names a member that %w", ErrUnsafePath, i, err)
		}
		position := int64(int32(binary.LittleEndian.Uint32(record[pakNameFieldSize:])))
		length := int64(int32(binary.LittleEndian.Uint32(record[pakNameFieldSize+4:])))
		switch {
		case position < pakHeaderSize:
			return nil, nil, fmt.Errorf("%w: %q begins at %d, inside the header", ErrMalformed, name, position)
		case length < 0:
			return nil, nil, fmt.Errorf("%w: %q declares a negative length (%d)", ErrMalformed, name, length)
		case position > size-length:
			return nil, nil, fmt.Errorf("%w: %q runs from %d for %d bytes, past the end of a %d-byte file",
				ErrMalformed, name, position, length, size)
		case length > 0 && intersects(position, position+length, dirOffset, dirOffset+dirLength):
			// A member whose data overlaps the directory is the shape a
			// truncated archive takes when the directory survived: the record
			// still describes bytes that are now something else. It is also
			// how a hand-made archive hides a second reading of itself.
			return nil, nil, fmt.Errorf("%w: %q runs from %d for %d bytes, over the directory at %d",
				ErrMalformed, name, position, length, dirOffset)
		}
		entries = append(entries, Entry{
			Path: name, Size: length, Compression: Store, StoredSize: length, Offset: position,
		})
		sizes = append(sizes, length)
	}
	budget.ArchiveSize = size
	if err := budget.CheckDeclared(sizes); err != nil {
		return nil, nil, err
	}
	if shared := overlappingRanges(entries); shared > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d members share data with another member: either the writer deduplicated identical files, or the directory is claiming the same bytes twice",
			shared))
	}
	sortEntries(entries)
	return entries, notes, nil
}

// pakName reads the fixed name field, and reports whether it was terminated.
func pakName(field []byte) (string, bool) {
	for i, b := range field {
		if b == 0 {
			return string(field[:i]), true
		}
	}
	return string(field), false
}

// intersects reports whether two half-open byte ranges share a byte.
func intersects(aStart, aEnd, bStart, bEnd int64) bool {
	return aStart < bEnd && bStart < aEnd
}

// overlappingRanges counts members whose data ranges intersect another's.
func overlappingRanges(entries []Entry) int {
	ordered := append([]Entry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Offset != ordered[j].Offset {
			return ordered[i].Offset < ordered[j].Offset
		}
		return ordered[i].Size < ordered[j].Size
	})
	shared := 0
	var end int64 = -1
	for _, entry := range ordered {
		if entry.Size == 0 {
			continue
		}
		if entry.Offset < end {
			shared++
		}
		if stop := entry.Offset + entry.Size; stop > end {
			end = stop
		}
	}
	return shared
}

// openPAKMember returns a reader over one member's stored bytes.
func openPAKMember(r io.ReaderAt, entry Entry) io.Reader {
	return io.NewSectionReader(r, entry.Offset, entry.Size)
}
