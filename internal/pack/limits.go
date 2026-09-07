package pack

import (
	"errors"
	"fmt"
)

// The budgets, and the shape of the attack each one is for.
//
// A bomb is not a big archive. It is a small archive that claims to be a big
// one, and the difference matters because the defence has to happen *before*
// the claim is acted on. Every check here reads a declared number out of a
// directory record and refuses on the number; none of them discovers the
// problem by running out of disk. That ordering is the whole point — an
// extractor that notices at four gigabytes has already written four gigabytes.
//
// PAK and PK3 get bombed differently:
//
//   - A **PK3** lies with its compression ratio. Sixteen zero bytes deflate to
//     nothing, so a two-kilobyte file can declare four gigabytes and be telling
//     the truth about its own format.
//   - A **PAK** cannot compress, so it lies with its *offsets* instead. The
//     directory is a list of `(position, length)` pairs and nothing says they
//     have to be distinct: a hundred thousand entries all pointing at the same
//     hundred-megabyte blob extracts to ten terabytes out of a file that is
//     honestly a hundred megabytes long. [checkPAKLayout] is what catches that,
//     and it catches it by noticing the overlap rather than by watching a
//     counter.

// MaxEntries is the ceiling on how many members one archive may have, in
// either direction.
const MaxEntries = 100_000

// MaxEntrySize is the ceiling on one member's uncompressed length.
//
// Below PAK's own 2 GiB signed-offset limit, and comfortably above the largest
// thing a Quake map package contains — an uncompressed BSP with high-resolution
// lightmaps is tens of megabytes.
const MaxEntrySize = 512 << 20

// MaxTotalSize is the ceiling on everything one archive expands to.
const MaxTotalSize = 4 << 30

// MaxCompressionRatio is how much larger than the archive its contents may be
// before the archive is treated as a claim rather than as data.
//
// Two hundred is `internal/acquire`'s number, and it is deliberately the same
// one: a toolchain tarball and a map package have the same honest range, and
// two different ceilings would be two different numbers to explain.
const MaxCompressionRatio = 200

// PAKMaxOffset is the largest position a PAK directory record can address. The
// field is a signed 32-bit integer in the published structure, so this is a
// property of the format and not a choice.
const PAKMaxOffset = 1<<31 - 1

// ErrArchiveBomb reports an archive whose declared contents are out of
// proportion to the archive itself.
var ErrArchiveBomb = errors.New("pack: archive expands out of proportion to its own size")

// ErrMalformed reports an archive whose structure does not hold together:
// truncated data, a directory that is not a whole number of records, offsets
// that point outside the file.
var ErrMalformed = errors.New("pack: malformed archive")

// Budget is the accounting one read or write is checked against.
type Budget struct {
	// MaxEntries, MaxEntrySize and MaxTotalSize are the ceilings; zero means
	// this package's own.
	MaxEntries   int
	MaxEntrySize int64
	MaxTotalSize int64
	// ArchiveSize is the container's own length, for the ratio check. Zero
	// disables the ratio check, which is correct when nothing has been written
	// yet.
	ArchiveSize int64
}

func (b Budget) maxEntries() int {
	if b.MaxEntries > 0 && b.MaxEntries < MaxEntries {
		return b.MaxEntries
	}
	return MaxEntries
}

func (b Budget) maxEntrySize() int64 {
	if b.MaxEntrySize > 0 && b.MaxEntrySize < MaxEntrySize {
		return b.MaxEntrySize
	}
	return MaxEntrySize
}

func (b Budget) maxTotalSize() int64 {
	if b.MaxTotalSize > 0 && b.MaxTotalSize < MaxTotalSize {
		return b.MaxTotalSize
	}
	return MaxTotalSize
}

// CheckDeclared applies every budget to what an archive's directory *says*
// before any of it is believed.
//
// It takes the sizes as a slice rather than accumulating through a callback so
// that the refusal happens with the whole picture available: "it has 100001
// members" is a better message than "the 100001st member was too many", and the
// total is known before the first byte is read.
func (b Budget) CheckDeclared(sizes []int64) error {
	if len(sizes) > b.maxEntries() {
		return fmt.Errorf("%w: it declares %d members, over the %d this build reads",
			ErrArchiveBomb, len(sizes), b.maxEntries())
	}
	var total int64
	for i, size := range sizes {
		if size < 0 {
			return fmt.Errorf("%w: member %d declares a negative length (%d)", ErrMalformed, i, size)
		}
		if size > b.maxEntrySize() {
			return fmt.Errorf("%w: member %d declares %d bytes, over the %d one member may expand to",
				ErrArchiveBomb, i, size, b.maxEntrySize())
		}
		// Checked before the addition rather than after it: a directory built
		// to overflow int64 is a directory whose total is meaningless once it
		// has wrapped.
		if total > b.maxTotalSize()-size {
			return fmt.Errorf("%w: its members declare more than the %d bytes this build extracts",
				ErrArchiveBomb, b.maxTotalSize())
		}
		total += size
	}
	if b.ArchiveSize > 0 {
		if limit := b.ArchiveSize * MaxCompressionRatio; limit > 0 && total > limit {
			return fmt.Errorf("%w: %d bytes of archive declare %d bytes of contents, a ratio of %d against this build's %d",
				ErrArchiveBomb, b.ArchiveSize, total, total/b.ArchiveSize, MaxCompressionRatio)
		}
	}
	return nil
}
