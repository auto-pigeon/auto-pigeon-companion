package artifactcheck

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bsp builds the smallest structurally valid Quake III BSP, then lets a test
// damage it.
func bsp(mutate func([]byte) []byte) []byte {
	const header = 8 + 17*8
	entities := []byte("{\n\"classname\" \"worldspawn\"\n}\n\x00")
	models := make([]byte, 40)
	out := make([]byte, header)
	copy(out, "IBSP")
	binary.LittleEndian.PutUint32(out[4:], 46)
	end := uint32(header + len(entities) + len(models))
	for lump := 0; lump < 17; lump++ {
		offset, length := end, uint32(0)
		switch lump {
		case 0:
			offset, length = header, uint32(len(entities))
		case 7:
			offset, length = uint32(header+len(entities)), uint32(len(models))
		}
		binary.LittleEndian.PutUint32(out[8+lump*8:], offset)
		binary.LittleEndian.PutUint32(out[12+lump*8:], length)
	}
	out = append(append(out, entities...), models...)
	if mutate != nil {
		out = mutate(out)
	}
	return out
}

const portals = "PRT1\n2\n1\n1\n4 0 1 0 (0 0 0 ) (0 0 1 ) (0 1 1 ) (0 1 0 ) \n4 0 (0 0 0 ) (0 0 1 ) (0 1 1 ) (0 1 0 ) \n"

// Every case here is a file Q3Map2 2.5.17n was measured reading (Q3_010): what
// it exits with is beside each, and the ones marked `exit 0` are the reason
// this package exists.
func TestAFileIsReadAsWhatItsRoleSays(t *testing.T) {
	for _, c := range []struct {
		name, role string
		body       []byte
		reason     string // "" means valid
	}{
		{"a compiled BSP", "q3.bsp", bsp(nil), ""},
		{"a BSP a later stage appended to", "q3.bsp.lit", append(bsp(nil), "lightmaps"...), ""},
		{"a placeholder that only starts with the magic", "q3.bsp", []byte("IBSP\x2e\x00\x00\x00 not a bsp\n"), "the header alone"},
		{"garbage where a BSP should be (vis: exit 1)", "q3.bsp.vised", append([]byte("garbage\n"), make([]byte, 200)...), "not IBSP"},
		{"a Quake II BSP", "q3.bsp", bsp(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[4:], 38); return b }), "version 38"},
		{"a BSP cut off before its last lump", "q3.bsp", bsp(func(b []byte) []byte { return b[:len(b)-20] }), "truncated"},
		{"a BSP with no entities", "q3.bsp", bsp(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[12:], 0); return b }), "no entities"},

		{"a portal file", "q3.prt", []byte(portals), ""},
		{"garbage where a portal file should be (vis: exit 1)", "q3.prt", []byte("garbage\n"), "not PRT1"},
		{"an empty portal file (vis: exit 1)", "q3.prt", nil, "empty"},
		{"a portal file cut off mid-list (vis: exit 1)", "q3.prt", []byte("PRT1\n2\n3\n0\n4 0 1 0 (0 0 0 )\n"), "truncated"},
		{"a portal file with a word for a count", "q3.prt", []byte("PRT1\n2\nmany\n0\n"), "not a number"},

		{"a surface file", "q3.srf", []byte("default\n{\n\tcastShadows 1\n}\n"), ""},
		{"an EMPTY surface file (light: exit 0, lit BSP written)", "q3.srf", nil, "empty"},
		{"garbage where a surface file should be (light: exit 0)", "q3.srf", []byte("garbage {\n"), "not `default`"},
		{"a surface file whose block never opens", "q3.srf", []byte("default\n"), "never opens"},

		{"a leak line file", "q3.lin", []byte("0.5 -12 64\n1 2 3\n"), ""},
		{"an empty leak file", "q3.lin", []byte("\n"), "leads nowhere"},
		{"a leak file of words", "q3.lin", []byte("the map leaks\n"), "not a number"},

		{"a map source with a leading comment", "q3.map.source", []byte("// entity 0\n{\n\"classname\" \"worldspawn\"\n}\n"), ""},
		{"an APMap handed over as a map source", "q3.map.source", []byte(`"apmap_version": "1.5"`), "opens an entity"},
		{"an empty map source", "q3.map.source", nil, "empty"},

		{"a role nothing here reads", "q1.bsp", []byte("anything"), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, c.body, 0o600); err != nil {
				t.Fatal(err)
			}
			err := Check(c.role, path)
			switch {
			case c.reason == "" && err != nil:
				t.Fatalf("refused a valid file: %v", err)
			case c.reason != "" && err == nil:
				t.Fatalf("accepted it; want a refusal mentioning %q", c.reason)
			case c.reason != "" && !IsInvalid(err):
				t.Fatalf("the refusal is not a content verdict: %v", err)
			case c.reason != "" && !strings.Contains(err.Error(), c.reason):
				t.Fatalf("the refusal %q does not say %q", err, c.reason)
			}
		})
	}
}

// A file that cannot be opened is not a content verdict: the caller has to be
// able to tell "this is not a BSP" from "there was nothing to read".
func TestAnUnreadableFileIsNotAContentVerdict(t *testing.T) {
	err := Check("q3.bsp", filepath.Join(t.TempDir(), "absent.bsp"))
	if err == nil || IsInvalid(err) {
		t.Fatalf("err = %v; want a read error that is not an Invalid", err)
	}
}

// An excerpt of somebody else's file is quoted into a message a page will
// render, so it carries no control bytes.
func TestAQuotedExcerptCarriesNoControlBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.prt")
	if err := os.WriteFile(path, []byte("\x1b[31mPRT\x00\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Check("q3.prt", path)
	if err == nil {
		t.Fatal("accepted it")
	}
	for _, r := range err.Error() {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("the message carries the control byte %#x: %q", r, err.Error())
		}
	}
}

func TestKnownListsTheRolesThatAreRead(t *testing.T) {
	want := "q3.bsp q3.bsp.lit q3.bsp.vised q3.lin q3.map.source q3.prt q3.srf"
	if got := strings.Join(Known(), " "); got != want {
		t.Errorf("Known() = %q, want %q", got, want)
	}
}
