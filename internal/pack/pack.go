// Package pack is the Companion's archive layer: classic Quake PAK and
// ZIP-based PK3, read and written natively.
//
// # Why this is in the repository at all
//
// Turning a compiled map into something another person can install means
// putting it in the archive the engine reads. That is a small job, and it is
// the last one, which is exactly why it is worth owning: everything upstream of
// it — the signed catalogue, the supervised executor, the build manifest — has
// been about knowing what happened, and handing the result to an unaudited
// third-party packer at the final step would throw that away. Nothing here
// shells out. No q1tools or QPakMan code was copied or translated; the formats
// are read from their published definitions, and `q1tools` remains a perfectly
// good *interactive* alternative for a person who wants one (see README).
//
// # The two formats
//
//   - **PAK** is id Software's PACK: a twelve-byte header, the file data
//     uncompressed, and a directory of fixed 64-byte records at the end. Quake
//     and Quake II use the same layout. It has no timestamps, no permissions,
//     no compression and no extension mechanism, which makes it the easiest
//     archive in the world to write reproducibly and the easiest to lie in:
//     every offset in it is a signed 32-bit integer somebody else chose.
//   - **PK3** is a ZIP with a different extension, read by Quake III and its
//     descendants. Everything ZIP can do wrong — traversal names, bomb ratios,
//     headers that disagree with their own data — it can do here.
//
// # What this package refuses
//
// The rules are in [CheckEntryPath] and they are refusals, not repairs, for the
// same reason `internal/acquire` gives: an archive member named
// `../../.ssh/authorized_keys` is not a malformed name to be tidied into
// `.ssh/authorized_keys`. It is a document that has said what it is for.
//
// # Determinism
//
// [Target] states what is promised, per format, and [Reproducibility] is
// written into the sidecar manifest so a reader never has to infer it. The
// short version: PAK and stored PK3 are byte-identical anywhere; deflated PK3
// is byte-identical for one build of the Companion, because the compressor is
// the Go standard library's and its output is a property of that library. That
// distinction is measured by golden tests rather than asserted.
package pack

import (
	"fmt"
	"sort"
	"strings"
)

// Format is an archive container this package reads and writes.
type Format string

const (
	// FormatPAK is id Software's PACK, used by Quake and Quake II.
	FormatPAK Format = "pak"
	// FormatPK3 is the ZIP container Quake III and its descendants read.
	FormatPK3 Format = "pk3"
)

// Formats is every format, in documentation order.
var Formats = []Format{FormatPAK, FormatPK3}

// Compression is the explicit policy for how member data is stored.
//
// Explicit because the choice is not an implementation detail: it decides
// whether the archive is byte-reproducible across Go versions, and PAK has no
// compression at all, so a caller asking for one is asking for something the
// format cannot do and should be told so rather than quietly given the other.
type Compression string

const (
	// Store writes member data verbatim.
	Store Compression = "store"
	// Deflate is ZIP's deflate. PK3 only.
	Deflate Compression = "deflate"
)

// Reproducibility is what a given archive's bytes are promised to be, and it is
// recorded in the sidecar manifest rather than left for a reader to assume.
type Reproducibility string

const (
	// Portable: the same normalized input produces the same bytes on any
	// machine, under any build of this program, on any operating system.
	// Nothing platform-dependent and nothing library-dependent reaches the
	// byte stream: no timestamps, no permission bits, no compressor.
	Portable Reproducibility = "portable"
	// PerBuild: the same normalized input produces the same bytes for a given
	// build of the Companion, and may differ under another one, because the
	// deflate encoder is the Go standard library's and its output is a property
	// of that library's version rather than of this program.
	PerBuild Reproducibility = "per_build"
)

// Target is a packaging destination: a format, a compression policy, and the
// engine-era constraints that make an archive one a given game can actually
// load.
//
// A target is also the only thing that can permit Auto-Pigeon's own metadata
// inside a game archive. Every built-in target refuses, and that is not
// timidity: a PAK the engine walks looking for `progs.dat` has no room for a
// file nobody asked for, and a PK3 with an `aucom/` directory in it is a
// package that leaks the tool that made it into somebody else's download. The
// manifest goes *beside* the archive. A profile-supplied target may say
// otherwise for itself, and [Plan] then records that it did.
type Target struct {
	ID     string
	Title  string
	Format Format
	// Compression is the default; a caller may override it where the format
	// allows more than one.
	Compression Compression
	// MaxEntries is the point past which the archive stops being loadable by
	// the engines this target names. Exceeding it is refused, with the number
	// and the engine in the message, because an archive that silently drops its
	// 2049th file is worse than one that was never written.
	MaxEntries int
	// MaxEntriesNote says which engine the ceiling comes from.
	MaxEntriesNote string
	// MaxNameLength is the longest member path the container can store.
	MaxNameLength int
	// AllowsMetadata permits writing the package manifest into the archive.
	AllowsMetadata bool
	// MetadataPath is where inside the archive it goes, when it is allowed.
	MetadataPath string
}

// PAKNameLength is the usable length of PAK's 56-byte name field.
//
// The field is `char name[56]` in both Quake's and Quake II's `dpackfile_t`,
// and every engine that reads it treats the contents as a C string, so the
// last byte has to be the terminator. Writing 56 bytes of name produces an
// archive that works in whichever engine happens to bound its own copy and
// overruns in the ones that do not.
const PAKNameLength = 55

// PK3NameLength bounds a PK3 member path. ZIP itself allows 65535 bytes; this
// is the shorter of what a filesystem will store on extraction and what a
// person can be shown in a review listing.
const PK3NameLength = 1024

// builtinTargets is the table `companion package targets` prints.
var builtinTargets = []Target{
	{
		ID: "quake-pak", Title: "Quake PAK (id1-compatible)",
		Format: FormatPAK, Compression: Store,
		MaxEntries:     2048,
		MaxEntriesNote: "MAX_FILES_IN_PACK in the original Quake source; modern engines raise it, id-era ones do not",
		MaxNameLength:  PAKNameLength,
	},
	{
		ID: "quake2-pak", Title: "Quake II PAK",
		Format: FormatPAK, Compression: Store,
		MaxEntries:     4096,
		MaxEntriesNote: "MAX_FILES_IN_PACK in the Quake II source",
		MaxNameLength:  PAKNameLength,
	},
	{
		ID: "quake3-pk3", Title: "Quake III PK3 (ZIP)",
		Format: FormatPK3, Compression: Deflate,
		MaxEntries:     MaxEntries,
		MaxEntriesNote: "this build's own ceiling; the format does not impose one below it",
		MaxNameLength:  PK3NameLength,
	},
}

// Targets lists the built-in packaging targets.
func Targets() []Target {
	out := make([]Target, len(builtinTargets))
	copy(out, builtinTargets)
	return out
}

// TargetByID finds a built-in target.
func TargetByID(id string) (Target, bool) {
	for _, t := range builtinTargets {
		if t.ID == id {
			return t, true
		}
	}
	return Target{}, false
}

// TargetIDs is every built-in target id, for an error message that lists the
// alternatives instead of leaving the user to guess them.
func TargetIDs() []string {
	out := make([]string, 0, len(builtinTargets))
	for _, t := range builtinTargets {
		out = append(out, t.ID)
	}
	return out
}

// Supports reports whether a compression policy is one this target's container
// can express.
func (t Target) Supports(c Compression) bool {
	switch t.Format {
	case FormatPAK:
		return c == Store
	case FormatPK3:
		return c == Store || c == Deflate
	}
	return false
}

// With returns the target with an overridden compression policy, or an error
// naming what the format can actually do.
func (t Target) With(c Compression) (Target, error) {
	if c == "" {
		return t, nil
	}
	if !t.Supports(c) {
		if t.Format == FormatPAK {
			return Target{}, fmt.Errorf("pack: %s is a PAK, and PAK stores its members verbatim; "+
				"there is no compressed PAK to write", t.ID)
		}
		return Target{}, fmt.Errorf("pack: %q is not a compression this build writes; use store or deflate", c)
	}
	t.Compression = c
	return t, nil
}

// Reproducibility says what this target's bytes are promised to be.
func (t Target) Reproducibility() Reproducibility {
	if t.Compression == Deflate {
		return PerBuild
	}
	return Portable
}

// Extension is the conventional filename suffix.
func (t Target) Extension() string { return "." + string(t.Format) }

// Entry is one member of an archive, as read from it or as planned into it.
type Entry struct {
	// Path is the member path, already normalized: `/`-separated, relative,
	// with no `.` or `..` element.
	Path string `json:"path"`
	// Size is the member's uncompressed length.
	Size int64 `json:"size"`
	// SHA256 is the digest of the member's uncompressed content, prefixed
	// `sha256:`, matching every other digest this program prints.
	SHA256 string `json:"sha256,omitempty"`
	// Compression is how it is stored inside the container.
	Compression Compression `json:"compression,omitempty"`
	// StoredSize is the on-disk length inside the container. Equal to Size for
	// a stored member.
	StoredSize int64 `json:"stored_size,omitempty"`
	// Offset is where the member's data begins in the container, for a format
	// that has one. Reported by inspection; ignored on write.
	Offset int64 `json:"offset,omitempty"`
	// CRC32 is ZIP's checksum, for a PK3 member. PAK has none.
	CRC32 uint32 `json:"crc32,omitempty"`
}

// sortEntries puts entries in the one order this package ever writes them.
//
// Byte-wise on the path, which is why [CheckEntryPath] refuses anything outside
// printable ASCII: a sort that depends on a collation table is a sort that
// depends on the machine, and the whole point of the ordering is that it does
// not.
func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
}

// FormatOf guesses a format from a filename, for a command that was given a
// path and no `--format`.
//
// A guess, and named as one: the caller is expected to say so in its error when
// the guess is what failed, because `.pak` on a ZIP is a thing people do.
func FormatOf(name string) (Format, bool) {
	switch strings.ToLower(name[strings.LastIndex(name, ".")+1:]) {
	case "pak":
		return FormatPAK, true
	case "pk3", "zip":
		return FormatPK3, true
	}
	return "", false
}
