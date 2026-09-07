package pack

import (
	"fmt"
	"io"
	"os"
)

// Member is one file on its way into an archive.
//
// It carries an opener rather than a reader or a path so that the two writers
// can be tested against bytes that never touched a filesystem, and so that a
// caller may supply something that is not a file at all — the sidecar manifest,
// when a target permits it inside. Size is what the plan recorded; a member
// whose opener produces a different length is a source that changed between
// the review and the write, and both writers refuse it rather than package
// whichever half they got.
type Member struct {
	Path string
	Size int64
	// Source is where this came from, for the manifest and for error messages.
	// Not written into the archive.
	Source string
	Open   func() (io.ReadCloser, error)
}

// FileMember is a member read from a path on this machine.
func FileMember(entryPath, filePath string, size int64) Member {
	return Member{
		Path:   entryPath,
		Size:   size,
		Source: filePath,
		Open:   func() (io.ReadCloser, error) { return os.Open(filePath) },
	}
}

// BytesMember is a member this program produced itself.
func BytesMember(entryPath string, data []byte) Member {
	return Member{
		Path:   entryPath,
		Size:   int64(len(data)),
		Source: "<generated>",
		Open:   func() (io.ReadCloser, error) { return io.NopCloser(newByteReader(data)), nil },
	}
}

// checkMembers applies the rules every archive shares, before either writer
// starts: the path rules, the collision rules and the target's ceilings.
//
// Doing it here rather than inside each writer is what makes "this archive
// would not have been valid" a refusal that happens with nothing written, and
// what stops the two containers from drifting into two different definitions
// of an acceptable package.
func checkMembers(members []Member, target Target) error {
	if len(members) == 0 {
		return fmt.Errorf("pack: nothing was selected, and an archive with no members is not a package")
	}
	if target.MaxEntries > 0 && len(members) > target.MaxEntries {
		return fmt.Errorf("pack: %d members, over %s's %d — %s",
			len(members), target.ID, target.MaxEntries, target.MaxEntriesNote)
	}
	if len(members) > MaxEntries {
		return fmt.Errorf("pack: %d members, over the %d this build writes", len(members), MaxEntries)
	}
	paths := make([]string, 0, len(members))
	var sizes []int64
	for _, m := range members {
		if err := CheckEntryPath(m.Path, target.MaxNameLength); err != nil {
			return fmt.Errorf("%w: %q %w", ErrUnsafePath, m.Path, err)
		}
		if m.Size < 0 {
			return fmt.Errorf("pack: %q has a negative length (%d)", m.Path, m.Size)
		}
		paths = append(paths, m.Path)
		sizes = append(sizes, m.Size)
	}
	if collisions := FindCollisions(paths); len(collisions) > 0 {
		return fmt.Errorf("pack: %s", collisions[0].Error())
	}
	return Budget{}.CheckDeclared(sizes)
}

// byteReader is a minimal in-memory reader. bytes.Reader would do, and this
// exists only so BytesMember can hand out a fresh one per call: an archive
// writer that retries a member must not get a reader that has already been
// consumed.
type byteReader struct {
	data []byte
	at   int
}

func newByteReader(data []byte) *byteReader { return &byteReader{data: data} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.at >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.at:])
	r.at += n
	return n, nil
}
