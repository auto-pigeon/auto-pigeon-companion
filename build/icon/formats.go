package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// windowsSizes are the renderings in companion.exe's icon. Explorer picks the
// nearest for each view; 256 is the largest an .ico entry can describe.
var windowsSizes = []int{16, 24, 32, 48, 64, 128, 256}

// COFF machine types for the resource object.
const (
	machineAMD64 uint16 = 0x8664
	machineARM64 uint16 = 0xaa64
)

// The relocation that makes a resource data entry point at its bytes once the
// linker has placed .rsrc: an image-relative 32-bit address.
var addr32NB = map[uint16]uint16{
	machineAMD64: 0x0003, // IMAGE_REL_AMD64_ADDR32NB
	machineARM64: 0x0002, // IMAGE_REL_ARM64_ADDR32NB
}

// Resource types and the one language every entry is filed under.
const (
	rtIcon      = 3
	rtGroupIcon = 14
	langEnUS    = 0x0409
)

// buildSyso writes a COFF object with one .rsrc section holding the icons as
// RT_ICON resources (PNG-compressed, which Windows has read since Vista) and
// one RT_GROUP_ICON, ID 1, that lists them. `go build` links any
// rsrc_windows_<arch>.syso in the package directory into that target's
// executable, and Explorer shows the group with the lowest ID as its icon.
func buildSyso(machine uint16, images []sized) ([]byte, error) {
	relocType, ok := addr32NB[machine]
	if !ok {
		return nil, fmt.Errorf("no resource relocation for COFF machine %#x", machine)
	}
	n := len(images)
	if n == 0 || n > 0xffff {
		return nil, fmt.Errorf("%d icon images", n)
	}

	// Layout of the section: the three-level directory tree, then the data
	// entries, then the data itself.
	const dirHeader, dirEntry, dataEntry = 16, 8, 16
	root := 0
	iconTypes := root + dirHeader + 2*dirEntry
	groupTypes := iconTypes + dirHeader + n*dirEntry
	iconLangs := groupTypes + dirHeader + dirEntry
	groupLang := iconLangs + n*(dirHeader+dirEntry)
	entries := groupLang + dirHeader + dirEntry
	data := entries + (n+1)*dataEntry

	group := groupIcon(images)
	blobs := make([][]byte, 0, n+1)
	for _, img := range images {
		blobs = append(blobs, img.png)
	}
	blobs = append(blobs, group)
	offsets := make([]int, len(blobs))
	cursor := data
	for i, blob := range blobs {
		cursor = align(cursor, 8)
		offsets[i] = cursor
		cursor += len(blob)
	}
	section := make([]byte, align(cursor, 8))

	le := binary.LittleEndian
	directory := func(at, ids int) { le.PutUint16(section[at+14:], uint16(ids)) }
	entry := func(at int, id uint32, target int, subdirectory bool) {
		le.PutUint32(section[at:], id)
		value := uint32(target)
		if subdirectory {
			value |= 0x80000000
		}
		le.PutUint32(section[at+4:], value)
	}

	directory(root, 2) // IDs ascending: RT_ICON, then RT_GROUP_ICON
	entry(root+dirHeader, rtIcon, iconTypes, true)
	entry(root+dirHeader+dirEntry, rtGroupIcon, groupTypes, true)

	directory(iconTypes, n)
	for i := 0; i < n; i++ {
		lang := iconLangs + i*(dirHeader+dirEntry)
		entry(iconTypes+dirHeader+i*dirEntry, uint32(i+1), lang, true)
		directory(lang, 1)
		entry(lang+dirHeader, langEnUS, entries+i*dataEntry, false)
	}
	directory(groupTypes, 1)
	entry(groupTypes+dirHeader, 1, groupLang, true)
	directory(groupLang, 1)
	entry(groupLang+dirHeader, langEnUS, entries+n*dataEntry, false)

	for i, blob := range blobs {
		at := entries + i*dataEntry
		le.PutUint32(section[at:], uint32(offsets[i])) // relocated to an RVA by the linker
		le.PutUint32(section[at+4:], uint32(len(blob)))
		copy(section[offsets[i]:], blob)
	}

	// COFF: header, one section header, the section, its relocations, one
	// symbol (the section's), and an empty string table.
	const fileHeader, sectionHeader, relocation, symbol = 20, 40, 10, 18
	rawData := fileHeader + sectionHeader
	relocs := rawData + len(section)
	symbols := relocs + (n+1)*relocation

	var out bytes.Buffer
	header := make([]byte, fileHeader)
	le.PutUint16(header[0:], machine)
	le.PutUint16(header[2:], 1)
	le.PutUint32(header[8:], uint32(symbols))
	le.PutUint32(header[12:], 1)
	out.Write(header)

	sectionHead := make([]byte, sectionHeader)
	copy(sectionHead[0:8], ".rsrc")
	le.PutUint32(sectionHead[16:], uint32(len(section)))
	le.PutUint32(sectionHead[20:], uint32(rawData))
	le.PutUint32(sectionHead[24:], uint32(relocs))
	le.PutUint16(sectionHead[32:], uint16(n+1))
	le.PutUint32(sectionHead[36:], 0x40000040) // initialized data, readable
	out.Write(sectionHead)
	out.Write(section)

	for i := 0; i <= n; i++ {
		reloc := make([]byte, relocation)
		le.PutUint32(reloc[0:], uint32(entries+i*dataEntry))
		le.PutUint32(reloc[4:], 0) // symbol 0: .rsrc
		le.PutUint16(reloc[8:], relocType)
		out.Write(reloc)
	}

	sym := make([]byte, symbol)
	copy(sym[0:8], ".rsrc")
	le.PutUint16(sym[12:], 1) // section number
	sym[16] = 3               // IMAGE_SYM_CLASS_STATIC
	out.Write(sym)
	out.Write([]byte{4, 0, 0, 0}) // string table: just its own size

	return out.Bytes(), nil
}

// groupIcon is the GRPICONDIR that lists the RT_ICON entries, IDs 1..n.
func groupIcon(images []sized) []byte {
	le := binary.LittleEndian
	out := make([]byte, 6+14*len(images))
	le.PutUint16(out[2:], 1) // type: icon
	le.PutUint16(out[4:], uint16(len(images)))
	for i, img := range images {
		at := 6 + 14*i
		out[at] = byte(img.size % 256) // 0 means 256
		out[at+1] = byte(img.size % 256)
		le.PutUint16(out[at+4:], 1)  // planes
		le.PutUint16(out[at+6:], 32) // bits per pixel
		le.PutUint32(out[at+8:], uint32(len(img.png)))
		le.PutUint16(out[at+12:], uint16(i+1))
	}

	return out
}

func align(n, to int) int {
	return (n + to - 1) / to * to
}

// icnsTypes are the PNG-bearing ICNS element types by pixel size. The @2x
// types share pixel sizes with the 1x types above them, so each size maps to
// every type that holds it.
var icnsTypes = []struct {
	kind string
	size int
}{
	{"icp4", 16}, {"icp5", 32}, {"ic11", 32}, {"icp6", 64}, {"ic12", 64},
	{"ic07", 128}, {"ic08", 256}, {"ic13", 256}, {"ic09", 512}, {"ic14", 512},
	{"ic10", 1024},
}

// icnsSizes are the distinct pixel sizes the .icns needs.
func icnsSizes() []int {
	var sizes []int
	seen := map[int]bool{}
	for _, t := range icnsTypes {
		if !seen[t.size] {
			seen[t.size] = true
			sizes = append(sizes, t.size)
		}
	}

	return sizes
}

// buildICNS writes an Apple icon family: "icns", the total length, then one
// element per type — its four-character code, its length, and a PNG.
func buildICNS(images []sized) []byte {
	bySize := map[int][]byte{}
	for _, img := range images {
		bySize[img.size] = img.png
	}
	be := binary.BigEndian
	var body bytes.Buffer
	for _, t := range icnsTypes {
		data, ok := bySize[t.size]
		if !ok {
			continue
		}
		head := make([]byte, 8)
		copy(head, t.kind)
		be.PutUint32(head[4:], uint32(8+len(data)))
		body.Write(head)
		body.Write(data)
	}
	out := make([]byte, 8, 8+body.Len())
	copy(out, "icns")
	be.PutUint32(out[4:], uint32(8+body.Len()))

	return append(out, body.Bytes()...)
}
