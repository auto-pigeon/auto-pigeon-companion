package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Test helpers, and one deliberate duplication.
//
// [independentPAK] parses the PACK container a second time, by hand, without
// calling anything in this package. That is on purpose: a round-trip test where
// the reader and the writer share their offset arithmetic passes just as
// happily when both are wrong, and the acceptance criterion for this work is
// that an archive opens in something that is not this program. `unzip` supplies
// the same service for PK3 where it is installed; this is the PAK half of it,
// written from the published structure the way an engine would.

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// memberOf builds an in-memory member, so a writer test never depends on a
// filesystem.
func memberOf(path, content string) Member { return BytesMember(path, []byte(content)) }

// pakFile is one member as an independent parse recovered it.
type pakFile struct {
	Name string
	Data []byte
}

// independentPAK decodes a PACK the way the original engine does: read the
// twelve-byte header, take `dirofs`/`dirlen` as little-endian signed integers,
// walk 64-byte records, and treat the 56-byte name field as a C string.
//
// It shares no code with [ReadPAK].
func independentPAK(t *testing.T, raw []byte) []pakFile {
	t.Helper()
	if len(raw) < 12 {
		t.Fatalf("archive is %d bytes, shorter than a PACK header", len(raw))
	}
	if string(raw[0:4]) != "PACK" {
		t.Fatalf("magic is %q, want %q", raw[0:4], "PACK")
	}
	dirOffset := int64(int32(binary.LittleEndian.Uint32(raw[4:8])))
	dirLength := int64(int32(binary.LittleEndian.Uint32(raw[8:12])))
	if dirLength%64 != 0 {
		t.Fatalf("directory is %d bytes, not a multiple of 64", dirLength)
	}
	if dirOffset+dirLength > int64(len(raw)) {
		t.Fatalf("directory runs from %d for %d bytes, past the %d-byte archive", dirOffset, dirLength, len(raw))
	}
	var files []pakFile
	for offset := dirOffset; offset < dirOffset+dirLength; offset += 64 {
		record := raw[offset : offset+64]
		name := record[:56]
		if end := bytes.IndexByte(name, 0); end >= 0 {
			name = name[:end]
		} else {
			t.Fatalf("name field at %d has no terminator", offset)
		}
		position := int64(int32(binary.LittleEndian.Uint32(record[56:60])))
		length := int64(int32(binary.LittleEndian.Uint32(record[60:64])))
		if position < 0 || length < 0 || position+length > int64(len(raw)) {
			t.Fatalf("%q runs from %d for %d bytes, past the %d-byte archive", name, position, length, len(raw))
		}
		files = append(files, pakFile{Name: string(name), Data: raw[position : position+length]})
	}
	return files
}

// unzipTest runs the system `unzip -t` over a PK3, which is a reader with no
// relationship to Go's archive/zip at all. Skipped where it is not installed;
// this is the "where fixtures permit" half of the acceptance criterion.
func unzipTest(t *testing.T, path string) {
	t.Helper()
	binary, err := exec.LookPath("unzip")
	if err != nil {
		t.Skip("unzip is not installed; skipping the independent PK3 reader check")
	}
	output, err := exec.Command(binary, "-t", path).CombinedOutput()
	if err != nil {
		t.Fatalf("unzip -t %s failed: %v\n%s", path, err, output)
	}
	if !bytes.Contains(output, []byte("No errors detected")) {
		t.Fatalf("unzip -t %s did not report a clean archive:\n%s", path, output)
	}
}

// unzipList returns the member paths `unzip -Z1` reports, in the archive's own
// order, so a determinism test can assert the order a foreign reader sees.
func unzipList(t *testing.T, path string) []string {
	t.Helper()
	binary, err := exec.LookPath("unzip")
	if err != nil {
		t.Skip("unzip is not installed")
	}
	output, err := exec.Command(binary, "-Z1", path).Output()
	if err != nil {
		t.Fatalf("unzip -Z1 %s failed: %v", path, err)
	}
	var names []string
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte("\n")) {
		if len(line) > 0 {
			names = append(names, string(line))
		}
	}
	return names
}

// writeArchive runs a writer into a file and returns the path and the bytes.
func writeArchive(t *testing.T, dir, name string, members []Member, target Target) (string, []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	switch target.Format {
	case FormatPAK:
		_, err = WritePAK(file, members, target)
	case FormatPK3:
		_, err = WritePK3(file, members, target)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading %s back: %v", path, readErr)
	}
	return path, raw
}

func mustTarget(t *testing.T, id string) Target {
	t.Helper()
	target, ok := TargetByID(id)
	if !ok {
		t.Fatalf("no built-in target %q", id)
	}
	return target
}
