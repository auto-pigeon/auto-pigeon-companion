package pack

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Q2_020: Quake II's retail baseq2/pak0.pak names
// `models/monsters/tank/../ctank/skin.pcx`. Inspect — what extraction and
// verification go through — still refuses that archive; InspectForLookup reads
// it for the names an engine can be asked for, skips that one and says so, and
// keeps every structural refusal.
func TestALookupSkipsAnUnsafeNameAndKeepsTheStructuralChecks(t *testing.T) {
	dir := t.TempDir()
	files := []pakFile{
		{Name: "textures/e1u1/box3_1.wal", Data: []byte("wal")},
		{Name: "models/monsters/tank/../ctank/skin.pcx", Data: []byte("pcx")},
		{Name: "env/unit1_rt.pcx", Data: []byte("sky")},
	}
	path := filepath.Join(dir, "pak0.pak")
	if err := os.WriteFile(path, buildPAKBytes(t, files, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(path, FormatPAK, Budget{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Inspect = %v, want the unsafe path refused", err)
	}
	inspection, err := InspectForLookup(path, FormatPAK, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range inspection.Entries {
		names = append(names, entry.Path)
	}
	if strings.Join(names, ",") != "env/unit1_rt.pcx,textures/e1u1/box3_1.wal" ||
		len(inspection.Skipped) != 1 || !strings.Contains(inspection.Skipped[0], "tank/../ctank") {
		t.Fatalf("entries %v, skipped %v", names, inspection.Skipped)
	}

	// A skipped name does not excuse a record that runs past the file.
	broken := filepath.Join(dir, "broken.pak")
	raw := buildPAKBytes(t, files, func(raw []byte, records int) {
		directory := int(binary.LittleEndian.Uint32(raw[4:]))
		binary.LittleEndian.PutUint32(raw[directory+64+60:], 1<<30)
	})
	if err := os.WriteFile(broken, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectForLookup(broken, FormatPAK, Budget{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("a record past the end of the file = %v, want malformed", err)
	}
}
