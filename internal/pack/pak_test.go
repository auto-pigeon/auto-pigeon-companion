package pack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPAKRoundTripThroughAnIndependentReader(t *testing.T) {
	dir := t.TempDir()
	target := mustTarget(t, "quake-pak")
	members := []Member{
		memberOf("maps/e1m1.bsp", "bsp bytes"),
		memberOf("gfx/palette.lmp", strings.Repeat("\x00\x11\x22", 256)),
		memberOf("progs/player.mdl", "model"),
		memberOf("sound/ambience/water1.wav", ""), // an empty member is a member
	}
	path, raw := writeArchive(t, dir, "pak0.pak", members, target)

	// The acceptance criterion: it parses in something that is not this code.
	files := independentPAK(t, raw)
	if len(files) != len(members) {
		t.Fatalf("the independent reader found %d members, want %d", len(files), len(members))
	}
	want := map[string]string{
		"maps/e1m1.bsp":             "bsp bytes",
		"gfx/palette.lmp":           strings.Repeat("\x00\x11\x22", 256),
		"progs/player.mdl":          "model",
		"sound/ambience/water1.wav": "",
	}
	for _, file := range files {
		expected, known := want[file.Name]
		if !known {
			t.Fatalf("the independent reader found a member this test did not write: %q", file.Name)
		}
		if string(file.Data) != expected {
			t.Fatalf("%s: got %d bytes, want %d", file.Name, len(file.Data), len(expected))
		}
	}
	// And in this package's own reader, with the same answer.
	inspection, err := Inspect(path, FormatPAK, Budget{})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(inspection.Entries) != len(members) {
		t.Fatalf("Inspect found %d entries, want %d", len(inspection.Entries), len(members))
	}
}

func TestPAKMembersAreSortedInTheDirectory(t *testing.T) {
	dir := t.TempDir()
	target := mustTarget(t, "quake-pak")
	_, raw := writeArchive(t, dir, "out.pak", []Member{
		memberOf("zzz.txt", "z"),
		memberOf("aaa.txt", "a"),
		memberOf("mmm.txt", "m"),
	}, target)

	var names []string
	for _, file := range independentPAK(t, raw) {
		names = append(names, file.Name)
	}
	want := []string{"aaa.txt", "mmm.txt", "zzz.txt"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("directory order is %v, want %v", names, want)
		}
	}
}

func TestPAKRefusesAnOverlongName(t *testing.T) {
	target := mustTarget(t, "quake-pak")
	long := "maps/" + strings.Repeat("a", 60) + ".bsp"
	_, err := WritePAK(&nopSeeker{}, []Member{memberOf(long, "x")}, target)
	if err == nil {
		t.Fatal("a name longer than PAK's field was accepted")
	}
	if !strings.Contains(err.Error(), "over the 55") {
		t.Fatalf("error is %v, want it to name the 55-byte limit", err)
	}
}

func TestPAKRefusesDuplicateAndCaseCollidingMembers(t *testing.T) {
	target := mustTarget(t, "quake-pak")
	for _, members := range [][]Member{
		{memberOf("maps/e1m1.bsp", "a"), memberOf("maps/e1m1.bsp", "b")},
		{memberOf("maps/e1m1.bsp", "a"), memberOf("maps/E1M1.bsp", "b")},
	} {
		if _, err := WritePAK(&nopSeeker{}, members, target); err == nil {
			t.Fatalf("%q and %q were both accepted", members[0].Path, members[1].Path)
		}
	}
}

func TestPAKRefusesAnEmptySelection(t *testing.T) {
	if _, err := WritePAK(&nopSeeker{}, nil, mustTarget(t, "quake-pak")); err == nil {
		t.Fatal("an archive with no members was written")
	}
}

func TestPAKRefusesTooManyMembersForTheTarget(t *testing.T) {
	target := mustTarget(t, "quake-pak")
	members := make([]Member, target.MaxEntries+1)
	for i := range members {
		members[i] = memberOf(pathForIndex(i), "x")
	}
	_, err := WritePAK(&nopSeeker{}, members, target)
	if err == nil {
		t.Fatalf("%d members were accepted into a target whose ceiling is %d", len(members), target.MaxEntries)
	}
	if !strings.Contains(err.Error(), "MAX_FILES_IN_PACK") {
		t.Fatalf("error is %v, want it to say where the ceiling comes from", err)
	}
}

func TestPAKRefusesAMemberThatChangedUnderneathIt(t *testing.T) {
	dir := t.TempDir()
	source := writeFile(t, dir, "map.bsp", "the bytes that were reviewed")
	// A plan recorded ten bytes; the file has more. Packaging the file anyway
	// would put a digest in the manifest for bytes nobody looked at.
	member := FileMember("maps/e1m1.bsp", source, 10)
	_, err := WritePAK(&nopSeeker{}, []Member{member}, mustTarget(t, "quake-pak"))
	if err == nil {
		t.Fatal("a member whose source had changed was packaged")
	}
	if !strings.Contains(err.Error(), "changed underneath this package") {
		t.Fatalf("error is %v", err)
	}
}

// --- reading somebody else's PAK -------------------------------------------

func TestReadPAKRefusesMalformedFixtures(t *testing.T) {
	dir := t.TempDir()
	good := buildPAKBytes(t, []pakFile{
		{Name: "maps/e1m1.bsp", Data: []byte("aaaa")},
		{Name: "gfx/palette.lmp", Data: []byte("bbbbbb")},
	}, nil)

	cases := []struct {
		name  string
		bytes []byte
		want  string
		is    error
	}{
		{
			name:  "empty file",
			bytes: nil,
			want:  "a PAK header is 12", is: ErrMalformed,
		},
		{
			name:  "wrong magic",
			bytes: append([]byte("PACX"), good[4:]...),
			want:  `not "PACK"`, is: ErrMalformed,
		},
		{
			name:  "directory length is not a whole number of records",
			bytes: patchPAK(good, 8, uint32(len(good))), // dirlen = file length
			want:  "not a whole number of 64-byte records", is: ErrMalformed,
		},
		{
			name:  "directory starts inside the header",
			bytes: patchPAK(good, 4, 4),
			want:  "inside the header", is: ErrMalformed,
		},
		{
			name:  "directory runs past the end",
			bytes: patchPAK(good, 4, uint32(len(good))),
			want:  "past the end", is: ErrMalformed,
		},
		{
			name:  "truncated after the header",
			bytes: good[:12],
			want:  "past the end", is: ErrMalformed,
		},
		{
			name: "a member runs past the end",
			bytes: buildPAKBytes(t, []pakFile{{Name: "x", Data: []byte("aaaa")}}, func(raw []byte, records int) {
				// Inflate the first record's length far past the archive.
				dirOffset := int64(int32(binary.LittleEndian.Uint32(raw[4:8])))
				binary.LittleEndian.PutUint32(raw[dirOffset+60:], 1<<20)
			}),
			want: "past the end", is: ErrMalformed,
		},
		{
			name: "a member declares a negative length",
			bytes: buildPAKBytes(t, []pakFile{{Name: "x", Data: []byte("aaaa")}}, func(raw []byte, records int) {
				dirOffset := int64(int32(binary.LittleEndian.Uint32(raw[4:8])))
				binary.LittleEndian.PutUint32(raw[dirOffset+60:], 0xFFFFFFFF)
			}),
			want: "negative length", is: ErrMalformed,
		},
		{
			name: "a member begins inside the header",
			bytes: buildPAKBytes(t, []pakFile{{Name: "x", Data: []byte("aaaa")}}, func(raw []byte, records int) {
				dirOffset := int64(int32(binary.LittleEndian.Uint32(raw[4:8])))
				binary.LittleEndian.PutUint32(raw[dirOffset+56:], 2)
			}),
			want: "inside the header", is: ErrMalformed,
		},
		{
			name:  "a member name escapes the archive",
			bytes: buildPAKBytes(t, []pakFile{{Name: "../../etc/passwd", Data: []byte("x")}}, nil),
			want:  "escapes the archive", is: ErrUnsafePath,
		},
		{
			name:  "a member name is absolute",
			bytes: buildPAKBytes(t, []pakFile{{Name: "/etc/passwd", Data: []byte("x")}}, nil),
			want:  "is absolute", is: ErrUnsafePath,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "case.pak")
			if err := os.WriteFile(path, tc.bytes, 0o644); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			_, err := Inspect(path, FormatPAK, Budget{})
			if err == nil {
				t.Fatal("the fixture was accepted")
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("error is %v, want it to wrap %v", err, tc.is)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error is %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestReadPAKRefusesAnOffsetBomb(t *testing.T) {
	// The PAK shape of a bomb: one blob of data, and a directory that claims it
	// thousands of times. The archive is honest about its own length and lies
	// about what it expands to.
	dir := t.TempDir()
	blob := bytes.Repeat([]byte("A"), 64*1024)
	var files []pakFile
	for i := 0; i < 4000; i++ {
		files = append(files, pakFile{Name: pathForIndex(i), Data: blob})
	}
	raw := buildOverlappingPAK(t, files)
	path := filepath.Join(dir, "bomb.pak")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	_, err := Inspect(path, FormatPAK, Budget{})
	if err == nil {
		t.Fatalf("a %d-byte archive declaring %d bytes of contents was accepted", len(raw), int64(len(files))*int64(len(blob)))
	}
	if !errors.Is(err, ErrArchiveBomb) {
		t.Fatalf("error is %v, want an ErrArchiveBomb", err)
	}
}

func TestReadPAKNotesDeduplicatedMembers(t *testing.T) {
	// The same overlap, at an honest ratio: two records pointing at one blob is
	// what a deduplicating writer produces, and it is reported rather than
	// refused.
	dir := t.TempDir()
	blob := bytes.Repeat([]byte("A"), 4096)
	raw := buildOverlappingPAK(t, []pakFile{
		{Name: "a.dat", Data: blob},
		{Name: "b.dat", Data: blob},
	})
	path := filepath.Join(dir, "dedup.pak")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	inspection, err := Inspect(path, FormatPAK, Budget{})
	if err != nil {
		t.Fatalf("a deduplicated PAK at an honest ratio was refused: %v", err)
	}
	if len(inspection.Notes) == 0 || !strings.Contains(strings.Join(inspection.Notes, " "), "share data") {
		t.Fatalf("notes are %v, want the overlap reported", inspection.Notes)
	}
}

func TestReadPAKRefusesAnUnterminatedName(t *testing.T) {
	dir := t.TempDir()
	raw := buildPAKBytes(t, []pakFile{{Name: "x", Data: []byte("y")}}, func(raw []byte, records int) {
		dirOffset := int64(int32(binary.LittleEndian.Uint32(raw[4:8])))
		copy(raw[dirOffset:dirOffset+56], bytes.Repeat([]byte("a"), 56))
	})
	path := filepath.Join(dir, "unterminated.pak")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	_, err := Inspect(path, FormatPAK, Budget{})
	if err == nil {
		t.Fatal("a name field with no terminator was accepted")
	}
	if !strings.Contains(err.Error(), "no terminator") {
		t.Fatalf("error is %v, want it to name the missing terminator", err)
	}
}

func TestVerifyPAKReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	target := mustTarget(t, "quake-pak")
	path, raw := writeArchive(t, dir, "good.pak", []Member{
		memberOf("a.dat", strings.Repeat("a", 100)),
		memberOf("b.dat", strings.Repeat("b", 100)),
	}, target)

	verification, err := Verify(path, FormatPAK, Budget{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.OK() || verification.Verified != 2 {
		t.Fatalf("a good archive verified as %+v", verification.Problems)
	}
	if verification.Entries[0].SHA256 != digestOf(strings.Repeat("a", 100)) {
		t.Fatalf("digest is %q", verification.Entries[0].SHA256)
	}

	// Now cut the file short, keeping the directory intact by moving it: a
	// truncated PAK usually loses its directory, so the interesting fixture is
	// one whose directory still describes data that is no longer there. What
	// catches it is the directory-overlap rule — the second member's range now
	// runs into the records themselves.
	cut := filepath.Join(dir, "cut.pak")
	if err := os.WriteFile(cut, truncateMemberData(raw, 50), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	_, cutErr := Verify(cut, FormatPAK, Budget{})
	if cutErr == nil {
		t.Fatal("a PAK whose directory outruns its data verified cleanly")
	}
	if !errors.Is(cutErr, ErrMalformed) {
		t.Fatalf("error is %v, want an ErrMalformed", cutErr)
	}
}

// --- fixture construction ---------------------------------------------------

// buildPAKBytes assembles a PACK by hand so a test can then corrupt one field
// of it. Sharing no code with the writer is the point: a fixture built by
// WritePAK could only ever be well formed.
func buildPAKBytes(t *testing.T, files []pakFile, corrupt func(raw []byte, records int)) []byte {
	t.Helper()
	var body bytes.Buffer
	body.Write(make([]byte, 12))
	positions := make([][2]int64, len(files))
	for i, file := range files {
		positions[i] = [2]int64{int64(body.Len()), int64(len(file.Data))}
		body.Write(file.Data)
	}
	dirOffset := int64(body.Len())
	record := make([]byte, 64)
	for i, file := range files {
		for j := range record {
			record[j] = 0
		}
		copy(record, file.Name)
		binary.LittleEndian.PutUint32(record[56:], uint32(positions[i][0]))
		binary.LittleEndian.PutUint32(record[60:], uint32(positions[i][1]))
		body.Write(record)
	}
	raw := body.Bytes()
	copy(raw, "PACK")
	binary.LittleEndian.PutUint32(raw[4:], uint32(dirOffset))
	binary.LittleEndian.PutUint32(raw[8:], uint32(len(files)*64))
	if corrupt != nil {
		corrupt(raw, len(files))
	}
	return raw
}

// buildOverlappingPAK writes one copy of the first file's data and points every
// record at it.
func buildOverlappingPAK(t *testing.T, files []pakFile) []byte {
	t.Helper()
	var body bytes.Buffer
	body.Write(make([]byte, 12))
	dataOffset := int64(body.Len())
	body.Write(files[0].Data)
	dirOffset := int64(body.Len())
	record := make([]byte, 64)
	for _, file := range files {
		for j := range record {
			record[j] = 0
		}
		copy(record, file.Name)
		binary.LittleEndian.PutUint32(record[56:], uint32(dataOffset))
		binary.LittleEndian.PutUint32(record[60:], uint32(len(file.Data)))
		body.Write(record)
	}
	raw := body.Bytes()
	copy(raw, "PACK")
	binary.LittleEndian.PutUint32(raw[4:], uint32(dirOffset))
	binary.LittleEndian.PutUint32(raw[8:], uint32(len(files)*64))
	return raw
}

// patchPAK overwrites one little-endian uint32 in the header.
func patchPAK(raw []byte, offset int, value uint32) []byte {
	out := append([]byte(nil), raw...)
	binary.LittleEndian.PutUint32(out[offset:], value)
	return out
}

// truncateMemberData removes bytes from the middle of the archive, leaving the
// header and the directory describing data that is short.
func truncateMemberData(raw []byte, cut int) []byte {
	dirOffset := int64(int32(binary.LittleEndian.Uint32(raw[4:8])))
	out := append([]byte(nil), raw[:dirOffset-int64(cut)]...)
	out = append(out, raw[dirOffset:]...)
	binary.LittleEndian.PutUint32(out[4:], uint32(dirOffset-int64(cut)))
	return out
}

func pathForIndex(i int) string {
	return "d/" + string(rune('a'+i/26/26%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i%26)) + itoa(i) + ".dat"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// nopSeeker is a WriteSeeker that discards everything, for the refusals that
// must happen before any output exists.
type nopSeeker struct{ at int64 }

func (n *nopSeeker) Write(p []byte) (int, error) { n.at += int64(len(p)); return len(p), nil }
func (n *nopSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case 0:
		n.at = offset
	case 1:
		n.at += offset
	}
	return n.at, nil
}
