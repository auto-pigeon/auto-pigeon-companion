package main

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The icon is made from the page's own 128x128 mark, the copy of AUP's that
// internal/web/brand_test.go pins; this is the same digest, so the icon and the
// page's logo cannot become two different pictures.
const sourceSHA256 = "0abc503cc854124965f24c7001cdd6c42e0a95f3f0ec10be5196b3af513d6ee3"

// testSource is the mark, from this package's directory.
var testSource = filepath.Join("..", "..", sourcePath)

func TestTheSourceIsThePagesPinnedMark(t *testing.T) {
	raw, err := os.ReadFile(testSource)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != sourceSHA256 {
		t.Fatalf("%s is %s, pinned %s", testSource, got, sourceSHA256)
	}
	if got, err := filepath.Abs(defaultSource()); err != nil || got != mustAbs(t, testSource) {
		t.Errorf("the default source is %s, want %s", got, testSource)
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}

	return abs
}

// Enlarging repeats pixels: at 2x every source pixel becomes an exact 2x2 block.
func TestEnlargingKeepsPixelArtCrisp(t *testing.T) {
	mark, err := loadMark(testSource)
	if err != nil {
		t.Fatal(err)
	}
	big := resize(mark, 256)
	for _, p := range [][2]int{{60, 20}, {64, 64}, {90, 100}} {
		want := color.NRGBAModel.Convert(mark.At(p[0], p[1]))
		for _, d := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
			if got := big.At(2*p[0]+d[0], 2*p[1]+d[1]); got != want {
				t.Fatalf("pixel %v became %v at %v, want %v", p, got, d, want)
			}
		}
	}
}

func testImages(t *testing.T, sizes []int) []sized {
	t.Helper()
	mark, err := loadMark(testSource)
	if err != nil {
		t.Fatal(err)
	}
	images, err := renderAll(mark, sizes)
	if err != nil {
		t.Fatal(err)
	}

	return images
}

// Every rendering is the size it says, and a corner that is transparent in the
// mark stays transparent: no fringe, no background.
func TestRenderingsAreTheirSizeAndKeepTransparency(t *testing.T) {
	for _, img := range testImages(t, windowsSizes) {
		decoded, err := png.Decode(bytes.NewReader(img.png))
		if err != nil {
			t.Fatal(err)
		}
		if b := decoded.Bounds(); b.Dx() != img.size || b.Dy() != img.size {
			t.Errorf("the %d rendering is %dx%d", img.size, b.Dx(), b.Dy())
		}
		if _, _, _, a := decoded.At(0, img.size-1).RGBA(); a != 0 {
			t.Errorf("the %d rendering's bottom-left corner has alpha %d", img.size, a)
		}
	}
	a, b := testImages(t, []int{48}), testImages(t, []int{48})
	if !bytes.Equal(a[0].png, b[0].png) {
		t.Error("two renderings of the same size differ; a release would not be the same bytes twice")
	}
}

// walk returns the RT_GROUP_ICON ID 1 and every RT_ICON, by ID, from a resource
// section. rva converts a data entry's address into an offset in section.
func walk(t *testing.T, section []byte, rva func(uint32) int) (group []byte, icons map[uint32][]byte) {
	t.Helper()
	le := binary.LittleEndian
	type item struct {
		id     uint32
		target uint32
	}
	entries := func(at int) []item {
		named, ids := int(le.Uint16(section[at+12:])), int(le.Uint16(section[at+14:]))
		var out []item
		for i := 0; i < named+ids; i++ {
			e := at + 16 + 8*i
			out = append(out, item{le.Uint32(section[e:]), le.Uint32(section[e+4:])})
		}

		return out
	}
	leaf := func(at uint32) []byte {
		langs := entries(int(at &^ 0x80000000))
		if len(langs) != 1 || langs[0].id != langEnUS || langs[0].target&0x80000000 != 0 {
			t.Fatalf("language level = %+v", langs)
		}
		d := int(langs[0].target)
		start, size := rva(le.Uint32(section[d:])), int(le.Uint32(section[d+4:]))

		return section[start : start+size]
	}
	icons = map[uint32][]byte{}
	for _, typ := range entries(0) {
		for _, name := range entries(int(typ.target &^ 0x80000000)) {
			switch typ.id {
			case rtIcon:
				icons[name.id] = leaf(name.target)
			case rtGroupIcon:
				if name.id == 1 {
					group = leaf(name.target)
				}
			}
		}
	}

	return group, icons
}

// checkIcons: the group lists every rendering, and each entry names an RT_ICON
// holding a PNG of the size the group says.
func checkIcons(t *testing.T, group []byte, icons map[uint32][]byte) {
	t.Helper()
	le := binary.LittleEndian
	if len(group) < 6 || le.Uint16(group[2:]) != 1 {
		t.Fatalf("no icon group: %x", group)
	}
	count := int(le.Uint16(group[4:]))
	if count != len(windowsSizes) {
		t.Fatalf("the group lists %d icons, want %d", count, len(windowsSizes))
	}
	for i := 0; i < count; i++ {
		at := 6 + 14*i
		size := int(group[at])
		if size == 0 {
			size = 256
		}
		id := uint32(le.Uint16(group[at+12:]))
		data, ok := icons[id]
		if !ok || int(le.Uint32(group[at+8:])) != len(data) {
			t.Fatalf("group entry %d names icon %d, which is missing or the wrong length", i, id)
		}
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("icon %d: %v", id, err)
		}
		if decoded.Bounds().Dx() != size {
			t.Errorf("icon %d is %d px, the group says %d", id, decoded.Bounds().Dx(), size)
		}
	}
}

// The object is what a COFF reader expects: one .rsrc section, one relocation
// per data entry of the machine's ADDR32NB type against the section symbol,
// and a directory that leads to the icons.
func TestTheResourceObjectIsWellFormed(t *testing.T) {
	images := testImages(t, windowsSizes)
	for arch, machine := range windowsMachines {
		object, err := buildSyso(machine, images)
		if err != nil {
			t.Fatal(err)
		}
		file, err := pe.NewFile(bytes.NewReader(object))
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		if file.Machine != machine || len(file.Sections) != 1 || file.Sections[0].Name != ".rsrc" {
			t.Fatalf("%s: machine %#x, sections %v", arch, file.Machine, file.Sections)
		}
		section := file.Sections[0]
		if len(section.Relocs) != len(images)+1 {
			t.Errorf("%s: %d relocations, want %d", arch, len(section.Relocs), len(images)+1)
		}
		for _, reloc := range section.Relocs {
			if reloc.Type != addr32NB[machine] || reloc.SymbolTableIndex != 0 {
				t.Errorf("%s: relocation %+v", arch, reloc)
			}
		}
		if len(file.Symbols) != 1 || file.Symbols[0].Name != ".rsrc" || file.Symbols[0].SectionNumber != 1 {
			t.Errorf("%s: symbols %+v", arch, file.Symbols)
		}
		data, err := section.Data()
		if err != nil {
			t.Fatal(err)
		}
		group, icons := walk(t, data, func(offset uint32) int { return int(offset) })
		checkIcons(t, group, icons)
	}
}

// The real test: Go's linker takes the object, and the executable it writes
// carries the icon group. Cross-compiled, so it runs on any host.
func TestGoLinksTheIconIntoAWindowsExecutable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two Windows executables")
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	images := testImages(t, windowsSizes)
	for arch, machine := range windowsMachines {
		dir := t.TempDir()
		object, err := buildSyso(machine, images)
		if err != nil {
			t.Fatal(err)
		}
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("go.mod", "module icontest\n\ngo 1.26\n")
		write("main.go", "package main\n\nfunc main() {}\n")
		write("rsrc_windows_"+arch+".syso", string(object))
		exe := filepath.Join(dir, "icontest.exe")
		cmd := exec.Command(goBin, "build", "-o", exe, ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: go build: %v\n%s", arch, err, output)
		}
		file, err := pe.Open(exe)
		if err != nil {
			t.Fatal(err)
		}
		section := file.Section(".rsrc")
		if section == nil {
			file.Close()
			t.Fatalf("%s: the executable has no .rsrc section", arch)
		}
		data, err := section.Data()
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		base := section.VirtualAddress
		group, icons := walk(t, data, func(rva uint32) int { return int(rva - base) })
		checkIcons(t, group, icons)
	}
}

// The .icns is an icon family macOS can read: its header length is the file's,
// and every element is a PNG of the size its type means.
func TestTheICNSHoldsEveryTypeAtItsSize(t *testing.T) {
	family := buildICNS(testImages(t, icnsSizes()))
	be := binary.BigEndian
	if string(family[:4]) != "icns" || int(be.Uint32(family[4:])) != len(family) {
		t.Fatalf("header %q, length %d of %d", family[:4], be.Uint32(family[4:]), len(family))
	}
	want := map[string]int{}
	for _, typ := range icnsTypes {
		want[typ.kind] = typ.size
	}
	seen := 0
	for at := 8; at < len(family); {
		kind, length := string(family[at:at+4]), int(be.Uint32(family[at+4:]))
		decoded, err := png.Decode(bytes.NewReader(family[at+8 : at+length]))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if decoded.Bounds().Dx() != want[kind] {
			t.Errorf("%s is %d px, want %d", kind, decoded.Bounds().Dx(), want[kind])
		}
		seen++
		at += length
	}
	if seen != len(icnsTypes) {
		t.Errorf("%d elements, want %d", seen, len(icnsTypes))
	}
}
