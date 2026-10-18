// Package q2fixture writes the small Quake II files the tests of the Quake II
// package path are made of: a compiled map with the two lumps an engine reads
// for its dependencies, a texture, a model naming its skins, a sound and an
// image. Every byte is written here; nothing is anybody else's.
//
// It is not a test file because several packages' tests use it, and it is
// imported by tests only.
package q2fixture

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BSP is an IBSP version 38 file whose entity lump is entities and whose
// texinfo lump names textures, one record each. The other seventeen lumps are
// empty, which is all a dependency scan reads.
func BSP(entities string, textures ...string) []byte {
	const lumps = 19
	header := make([]byte, 8+lumps*8)
	copy(header, "IBSP")
	binary.LittleEndian.PutUint32(header[4:], 38)
	entityLump := append([]byte(entities), 0)
	texinfo := make([]byte, 0, 76*len(textures))
	for i, name := range textures {
		record := make([]byte, 76)
		copy(record[40:72], name)
		binary.LittleEndian.PutUint32(record[72:], uint32(0xffffffff))
		_ = i
		texinfo = append(texinfo, record...)
	}
	out := append([]byte{}, header...)
	put := func(index int, data []byte) {
		binary.LittleEndian.PutUint32(out[8+index*8:], uint32(len(out)))
		binary.LittleEndian.PutUint32(out[12+index*8:], uint32(len(data)))
		out = append(out, data...)
	}
	put(0, entityLump)
	put(5, texinfo)
	for _, index := range []int{1, 2, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18} {
		binary.LittleEndian.PutUint32(out[8+index*8:], uint32(len(out)))
	}
	return out
}

// WAL is an 8×8 Quake II texture named name.
func WAL(name string) []byte {
	const width, height = 8, 8
	header := make([]byte, 100)
	copy(header, strings.TrimSuffix(name, ".wal"))
	binary.LittleEndian.PutUint32(header[32:], width)
	binary.LittleEndian.PutUint32(header[36:], height)
	offset := uint32(100)
	var body []byte
	for level := 0; level < 4; level++ {
		binary.LittleEndian.PutUint32(header[40+level*4:], offset)
		size := (width >> level) * (height >> level)
		body = append(body, make([]byte, size)...)
		offset += uint32(size)
	}
	return append(header, body...)
}

// MD2 is an IDP2 version 8 model header naming skins, with nothing else in it.
func MD2(skins ...string) []byte {
	header := make([]byte, 68)
	copy(header, "IDP2")
	binary.LittleEndian.PutUint32(header[4:], 8)
	binary.LittleEndian.PutUint32(header[20:], uint32(len(skins)))
	binary.LittleEndian.PutUint32(header[44:], 68)
	out := append([]byte{}, header...)
	for _, skin := range skins {
		name := make([]byte, 64)
		copy(name, skin)
		out = append(out, name...)
	}
	binary.LittleEndian.PutUint32(out[64:], uint32(len(out)))
	return out
}

// WAV is a tenth of a second of silence, 8-bit mono at 11025 Hz.
func WAV() []byte {
	samples := make([]byte, 1102)
	for i := range samples {
		samples[i] = 128
	}
	out := []byte("RIFF")
	out = binary.LittleEndian.AppendUint32(out, uint32(36+len(samples)))
	out = append(out, "WAVEfmt "...)
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint32(out, 11025)
	out = binary.LittleEndian.AppendUint32(out, 11025)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint16(out, 8)
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(samples)))
	return append(out, samples...)
}

// PCX is a one-pixel 8-bit image with a grey palette.
func PCX() []byte {
	header := make([]byte, 128)
	header[0], header[1], header[2], header[3] = 0x0A, 5, 1, 8
	header[65], header[66] = 1, 2
	out := append(header, 0xC2, 0x00, 0x0C)
	for i := 0; i < 256; i++ {
		out = append(out, byte(i), byte(i), byte(i))
	}
	return out
}

// PAK is a PACK archive of members, in the order given, written here from the
// format's published layout rather than by `internal/pack`, so a test of the
// writer's reader is not a test of itself.
func PAK(names []string, members map[string][]byte) []byte {
	var data, directory []byte
	for _, name := range names {
		record := make([]byte, 64)
		copy(record, name)
		binary.LittleEndian.PutUint32(record[56:], uint32(12+len(data)))
		binary.LittleEndian.PutUint32(record[60:], uint32(len(members[name])))
		data = append(data, members[name]...)
		directory = append(directory, record...)
	}
	header := []byte("PACK")
	header = binary.LittleEndian.AppendUint32(header, uint32(12+len(data)))
	header = binary.LittleEndian.AppendUint32(header, uint32(len(directory)))
	return append(append(header, data...), directory...)
}

// Write puts files under root, creating folders, and returns root. The files
// are written in name order, never map order: on a case-insensitive filesystem
// two spellings of one folder are one folder named by whichever was created
// first, and that must not change from run to run.
func Write(root string, files map[string][]byte) (string, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := files[name]
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
	}
	return root, nil
}
