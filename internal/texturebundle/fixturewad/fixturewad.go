// Package fixturewad writes tiny, independently authored Quake WAD2 files for
// tests.
//
// A test of how texture sources are verified, staged and credited needs WAD
// bytes, and must never borrow them: copying a real texture into this
// repository to test the rules about redistributing textures would be the
// thing those rules exist to prevent. So the bytes are made here, from nothing
// — one 16×16 single-colour miptex per file — and are this project's own.
package fixturewad

import (
	"bytes"
	"encoding/binary"
)

// side is the texture's width and height. Quake wants a multiple of 16.
const side = 16

// WAD returns a valid WAD2 holding one miptex called texture, every pixel of it
// the palette index colour. Two calls with the same arguments return the same
// bytes; a different name or colour returns a different file with a different
// digest.
func WAD(texture string, colour byte) []byte {
	// The miptex lump: a 40-byte header, then four mip levels.
	lump := &bytes.Buffer{}
	name := [16]byte{}
	copy(name[:15], texture)
	lump.Write(name[:])
	put := func(value uint32) { _ = binary.Write(lump, binary.LittleEndian, value) }
	put(side)
	put(side)
	offset := uint32(40)
	for level := 0; level < 4; level++ {
		put(offset)
		offset += uint32((side >> level) * (side >> level))
	}
	for level := 0; level < 4; level++ {
		lump.Write(bytes.Repeat([]byte{colour}, (side>>level)*(side>>level)))
	}

	// The file: header, the lump, then a one-entry directory.
	const headerBytes = 12
	out := &bytes.Buffer{}
	out.WriteString("WAD2")
	word := func(value int32) { _ = binary.Write(out, binary.LittleEndian, value) }
	word(1)
	word(int32(headerBytes + lump.Len()))
	out.Write(lump.Bytes())
	word(headerBytes)
	word(int32(lump.Len()))
	word(int32(lump.Len()))
	out.Write([]byte{'D', 0, 0, 0}) // type miptex, uncompressed, two bytes of padding
	out.Write(name[:])

	return out.Bytes()
}
