package pack

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPK3RoundTripThroughIndependentReaders(t *testing.T) {
	dir := t.TempDir()
	target := mustTarget(t, "quake3-pk3")
	members := []Member{
		memberOf("maps/q3dm17.bsp", strings.Repeat("bsp", 1000)),
		memberOf("scripts/shaders.shader", "textures/x { }"),
		memberOf("levelshots/q3dm17.tga", ""),
	}
	path, _ := writeArchive(t, dir, "pak0.pk3", members, target)

	// `unzip` is not Go's archive/zip and has no relationship to it.
	unzipTest(t, path)
	names := unzipList(t, path)
	want := []string{"levelshots/q3dm17.tga", "maps/q3dm17.bsp", "scripts/shaders.shader"}
	if len(names) != len(want) {
		t.Fatalf("unzip listed %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("unzip listed %v, want %v (sorted)", names, want)
		}
	}

	verification, err := Verify(path, FormatPK3, Budget{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.OK() || verification.Verified != 3 {
		t.Fatalf("verified %d members, problems %v", verification.Verified, verification.Problems)
	}
}

func TestPK3StoredIsAlsoReadableAndSmallerToReasonAbout(t *testing.T) {
	dir := t.TempDir()
	target, err := mustTarget(t, "quake3-pk3").With(Store)
	if err != nil {
		t.Fatalf("With(Store): %v", err)
	}
	path, _ := writeArchive(t, dir, "stored.pk3", []Member{memberOf("a.txt", strings.Repeat("a", 5000))}, target)
	unzipTest(t, path)
	if got := target.Reproducibility(); got != Portable {
		t.Fatalf("a stored PK3 claims %q reproducibility, want %q", got, Portable)
	}
}

func TestPAKRefusesACompressionRequestItCannotHonour(t *testing.T) {
	_, err := mustTarget(t, "quake-pak").With(Deflate)
	if err == nil {
		t.Fatal("a deflated PAK was accepted; the format has no compression")
	}
	if !strings.Contains(err.Error(), "stores its members verbatim") {
		t.Fatalf("error is %v", err)
	}
}

func TestPK3CarriesNoTimestampAndNoPlatform(t *testing.T) {
	// The pinned fields, read back out of the archive rather than asserted
	// about the code that wrote it.
	dir := t.TempDir()
	path, _ := writeArchive(t, dir, "x.pk3", []Member{memberOf("a.txt", "a")}, mustTarget(t, "quake3-pk3"))
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if got := file.Modified.UTC(); got != dosEpoch {
			t.Fatalf("%s carries %s, want the MS-DOS epoch %s", file.Name, got, dosEpoch)
		}
		if len(file.Extra) != 0 {
			t.Fatalf("%s carries %d bytes of extra fields; an extended timestamp is the usual culprit", file.Name, len(file.Extra))
		}
		if file.ExternalAttrs != 0 {
			t.Fatalf("%s carries external attributes %#x, which is the packer's umask", file.Name, file.ExternalAttrs)
		}
		if high := file.CreatorVersion >> 8; high != 0 {
			t.Fatalf("%s says it was made on platform %d", file.Name, high)
		}
	}
}

// --- reading somebody else's PK3 -------------------------------------------

func TestReadPK3RefusesUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../../etc/passwd", "/etc/passwd", `..\..\windows\system32\x`, "maps/e1m1\x00.bsp"} {
		path := filepath.Join(dir, "case.pk3")
		os.Remove(path)
		writeRawZip(t, path, func(w *zip.Writer) {
			part, err := w.Create(name)
			if err != nil {
				t.Fatalf("creating %q: %v", name, err)
			}
			part.Write([]byte("x"))
		})
		_, err := Inspect(path, FormatPK3, Budget{})
		if err == nil {
			t.Fatalf("a member named %q was accepted", name)
		}
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("%q produced %v, want an ErrUnsafePath", name, err)
		}
	}
}

func TestReadPK3RefusesASymlinkMember(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "link.pk3")
	writeRawZip(t, path, func(w *zip.Writer) {
		header := &zip.FileHeader{Name: "id1/link", Method: zip.Store}
		// Creator "Unix" plus S_IFLNK in the high bits is how a ZIP carries a
		// symbolic link, and how an extractor that honours it writes outside
		// the destination.
		header.CreatorVersion = 3 << 8
		header.SetMode(0o777 | os.ModeSymlink)
		part, err := w.CreateHeader(header)
		if err != nil {
			t.Fatalf("creating the link member: %v", err)
		}
		part.Write([]byte("/etc/passwd"))
	})
	_, err := Inspect(path, FormatPK3, Budget{})
	if err == nil {
		t.Fatal("a symbolic-link member was accepted")
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("error is %v, want an ErrUnsafePath", err)
	}
}

func TestReadPK3RefusesACompressionBomb(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bomb.pk3")
	// Sixteen mebibytes of zeroes deflate to a few kilobytes, which is a ratio
	// well past the ceiling and the whole trick.
	writeRawZip(t, path, func(w *zip.Writer) {
		part, err := w.Create("bomb.dat")
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		zeroes := make([]byte, 1<<20)
		for i := 0; i < 16; i++ {
			part.Write(zeroes)
		}
	})
	_, err := Inspect(path, FormatPK3, Budget{})
	if err == nil {
		t.Fatal("a compression bomb was accepted")
	}
	if !errors.Is(err, ErrArchiveBomb) {
		t.Fatalf("error is %v, want an ErrArchiveBomb", err)
	}
	if !strings.Contains(err.Error(), "ratio") {
		t.Fatalf("error is %v, want it to state the ratio it refused", err)
	}
}

func TestReadPK3RefusesTooManyMembersForTheBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "many.pk3")
	writeRawZip(t, path, func(w *zip.Writer) {
		for i := 0; i < 8; i++ {
			part, err := w.Create(pathForIndex(i))
			if err != nil {
				t.Fatalf("creating: %v", err)
			}
			part.Write([]byte("x"))
		}
	})
	_, err := Inspect(path, FormatPK3, Budget{MaxEntries: 3})
	if err == nil {
		t.Fatal("8 members were accepted against a budget of 3")
	}
	if !errors.Is(err, ErrArchiveBomb) {
		t.Fatalf("error is %v, want an ErrArchiveBomb", err)
	}
}

func TestReadPK3RefusesAMemberThatLiesAboutItsSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "liar.pk3")
	writeRawZip(t, path, func(w *zip.Writer) {
		part, err := w.Create("small.dat")
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		part.Write(bytes.Repeat([]byte("a"), 100))
	})
	// Patch the central directory to declare a gigabyte. Inspection believes
	// the directory — that is what a directory is for — and the budget is what
	// refuses it, before a byte is decompressed.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	patched := patchCentralUncompressedSize(t, raw, 1<<30)
	liar := filepath.Join(dir, "patched.pk3")
	if err := os.WriteFile(liar, patched, 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if _, err := Inspect(liar, FormatPK3, Budget{}); !errors.Is(err, ErrArchiveBomb) {
		t.Fatalf("error is %v, want an ErrArchiveBomb", err)
	}

	// A smaller lie passes inspection and is caught by verification, which is
	// the layer that reads the bytes and compares them with the claim.
	modest := patchCentralUncompressedSize(t, raw, 5000)
	modestPath := filepath.Join(dir, "modest.pk3")
	if err := os.WriteFile(modestPath, modest, 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if _, err := Inspect(modestPath, FormatPK3, Budget{}); err != nil {
		t.Fatalf("a modest lie was refused at inspection, so the verification path is untested: %v", err)
	}
	verification, err := Verify(modestPath, FormatPK3, Budget{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verification.OK() {
		t.Fatal("a member declaring 5000 bytes and producing 100 verified cleanly")
	}
	if !strings.Contains(strings.Join(verification.Problems, " "), "declares 5000 bytes and its data ends before that") {
		t.Fatalf("problems are %v", verification.Problems)
	}
}

func TestReadPK3RefusesATruncatedArchive(t *testing.T) {
	dir := t.TempDir()
	path, raw := writeArchive(t, dir, "whole.pk3", []Member{memberOf("a.txt", strings.Repeat("a", 400))}, mustTarget(t, "quake3-pk3"))
	_ = path
	cut := filepath.Join(dir, "cut.pk3")
	if err := os.WriteFile(cut, raw[:len(raw)/2], 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if _, err := Inspect(cut, FormatPK3, Budget{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("error is %v, want an ErrMalformed", err)
	}
}

func TestReadPK3NotesDirectoryMembersAndComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noted.pk3")
	writeRawZip(t, path, func(w *zip.Writer) {
		if err := w.SetComment("made by something else"); err != nil {
			t.Fatalf("comment: %v", err)
		}
		if _, err := w.Create("maps/"); err != nil {
			t.Fatalf("directory member: %v", err)
		}
		part, err := w.Create("maps/e1m1.bsp")
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		part.Write([]byte("x"))
	})
	inspection, err := Inspect(path, FormatPK3, Budget{})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(inspection.Entries) != 1 {
		t.Fatalf("got %d entries, want the directory member excluded", len(inspection.Entries))
	}
	joined := strings.Join(inspection.Notes, " | ")
	if !strings.Contains(joined, "directory records") || !strings.Contains(joined, "comment") {
		t.Fatalf("notes are %q", joined)
	}
}

func TestReadPK3RefusesADirectoryMemberThatEscapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "escape.pk3")
	writeRawZip(t, path, func(w *zip.Writer) {
		if _, err := w.Create("../../etc/"); err != nil {
			t.Fatalf("directory member: %v", err)
		}
	})
	if _, err := Inspect(path, FormatPK3, Budget{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("error is %v, want an ErrUnsafePath", err)
	}
}

// --- fixture construction ---------------------------------------------------

// writeRawZip builds a ZIP with the stock writer and no pinning, which is how a
// hostile or merely foreign archive arrives.
func writeRawZip(t *testing.T, path string, build func(*zip.Writer)) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	writer := zip.NewWriter(file)
	build(writer)
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the zip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("closing %s: %v", path, err)
	}
}

// patchCentralUncompressedSize rewrites the declared uncompressed length in
// every central-directory record, which is the field a reader allocates
// against.
//
//	central file header: sig(4) madeby(2) needed(2) flags(2) method(2)
//	                     modtime(2) moddate(2) crc(4) csize(4) usize(4) …
func patchCentralUncompressedSize(t *testing.T, raw []byte, size uint32) []byte {
	t.Helper()
	out := append([]byte(nil), raw...)
	signature := []byte{0x50, 0x4b, 0x01, 0x02}
	patched := 0
	for i := 0; i+30 <= len(out); i++ {
		if bytes.Equal(out[i:i+4], signature) {
			binary.LittleEndian.PutUint32(out[i+24:], size)
			patched++
		}
	}
	if patched == 0 {
		t.Fatal("found no central-directory record to patch")
	}
	return out
}
