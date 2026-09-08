package nativeacceptance

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// The fixture this kit compiles, and the one it wears.
//
// # Why the product carries a map fixture at all
//
// The kit runs on a clean machine that has the release artifact and nothing
// else: no checkout, no Python, no test corpus, and certainly no id Software
// asset. A real compile is one of the things the operator is there to observe,
// so the source map has to arrive inside the binary or not at all.
//
// # It is a SECOND authored fixture, deliberately
//
// `auto-pigeon-tools/scripts/ericw/fixture.py` authors a sealed room too, and
// that one must stay where it is: it is the pinned oracle's control sample, and
// `auto-pigeon-tools/AGENTS.md` §3j requires it to be independent of product
// code precisely so a defect in the product cannot make the compiler look
// broken. This one is the product's own, ships in the product, and is the input
// to a claim about the product. Neither may be derived from the other, and
// neither is a copy of anything: both are written from the published `.map`
// and WAD2 layouts.
//
// # No id Software asset is used, bundled or referenced
//
// The geometry is six axis-aligned slabs written below. The texture is a
// checkerboard of two palette *indices* this file chose, packed into a WAD2
// from the published layout — a miptex carries indices and no palette, so a
// synthetic texture needs nothing but bytes we picked.
//
// # The plane triples are in the Quake outward-normal convention
//
// A face is three points and the compiler's normal is `(p0-p1) x (p2-p1)`,
// pointing OUT of the solid. `AUP 140` measured authored brushes writing the
// negative of that, and `qbsp` answered `WARNING 09: Couldn't create brush
// faces` for every one of them. The winding here is derived from an explicit
// outward normal, written out rather than borrowed.
const (
	// FixtureTexture is the one texture the fixture wears.
	FixtureTexture = "aupnative"
	// FixtureWADName is the WAD it lives in.
	FixtureWADName = "auto-pigeon-native.wad"
	// FixtureMapName is the source map.
	FixtureMapName = "auto-pigeon-native.map"

	// fixtureTextureSize is a multiple of 16, as a miptex requires.
	fixtureTextureSize = 16

	// The room. Small on purpose: this compiles in well under a second, and an
	// acceptance step that took a minute would stop being run.
	fixtureInterior = 128
	fixtureWall     = 16
	fixtureFloorZ   = 0
	fixtureCeilingZ = 128
	// Above 24, because `qbsp` fills hull 2 from the entity origins and that
	// hull's expansion raises the floor by 24 units. An origin at exactly the
	// expanded floor is inside solid there, which is a fixture defect that
	// reads as a compiler finding.
	fixtureStartHeight = 48
)

// vector is a point or a direction in map units.
type vector [3]float64

// faceBasis maps an outward normal to two in-plane directions with u x v = n.
//
// Written out rather than computed, so the convention is readable at the point
// a future reader will doubt it.
var faceBasis = []struct {
	normal vector
	u      vector
	v      vector
}{
	{vector{0, 0, 1}, vector{1, 0, 0}, vector{0, 1, 0}},
	{vector{0, 0, -1}, vector{0, 1, 0}, vector{1, 0, 0}},
	{vector{1, 0, 0}, vector{0, 1, 0}, vector{0, 0, 1}},
	{vector{-1, 0, 0}, vector{0, 0, 1}, vector{0, 1, 0}},
	{vector{0, 1, 0}, vector{0, 0, 1}, vector{1, 0, 0}},
	{vector{0, -1, 0}, vector{1, 0, 0}, vector{0, 0, 1}},
}

// box is an axis-aligned solid. low is inclusive of the minimum corner.
type box struct {
	low  vector
	high vector
}

// cornerFor is the corner a face's triple is anchored at.
func (b box) cornerFor(normal vector) vector {
	var corner vector
	for axis := 0; axis < 3; axis++ {
		if normal[axis] > 0 {
			corner[axis] = b.high[axis]
		} else {
			corner[axis] = b.low[axis]
		}
	}
	return corner
}

// offset steps from a corner along an in-plane axis to the opposite face.
func (b box) offset(point, direction vector) vector {
	result := point
	for axis := 0; axis < 3; axis++ {
		if direction[axis] != 0 {
			span := b.high[axis] - b.low[axis]
			result[axis] = point[axis] + direction[axis]*span
		}
	}
	return result
}

func formatNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func formatPoint(point vector) string {
	return fmt.Sprintf("( %s %s %s )",
		formatNumber(point[0]), formatNumber(point[1]), formatNumber(point[2]))
}

// brushText writes one brush: six faces in the outward-normal convention, with
// the classic projection every Quake 1 compiler reads.
func brushText(b box, texture string) string {
	var builder strings.Builder
	builder.WriteString("{\n")
	for _, face := range faceBasis {
		anchor := b.cornerFor(face.normal)
		p1 := anchor
		p0 := b.offset(anchor, face.u)
		p2 := b.offset(anchor, face.v)
		fmt.Fprintf(&builder, "%s %s %s %s 0 0 0 1 1\n",
			formatPoint(p0), formatPoint(p1), formatPoint(p2), texture)
	}
	builder.WriteString("}")
	return builder.String()
}

// sealedRoom is six slabs enclosing one airtight room. A leak makes `qbsp`
// complain, which is exactly what a compile of this fixture must not produce.
func sealedRoom() []box {
	outer := float64(fixtureInterior + fixtureWall)
	inner := float64(fixtureInterior)
	floor, ceiling, wall := float64(fixtureFloorZ), float64(fixtureCeilingZ), float64(fixtureWall)
	return []box{
		{vector{-outer, -outer, floor - wall}, vector{outer, outer, floor}},
		{vector{-outer, -outer, ceiling}, vector{outer, outer, ceiling + wall}},
		{vector{-outer, -outer, floor}, vector{-inner, outer, ceiling}},
		{vector{inner, -outer, floor}, vector{outer, outer, ceiling}},
		{vector{-inner, -outer, floor}, vector{inner, -inner, ceiling}},
		{vector{-inner, inner, floor}, vector{inner, outer, ceiling}},
	}
}

// FixtureMap is the whole source map: a worldspawn of brushes, a start point, a
// deathmatch start and a light.
//
// The last two are present so `qbsp` has nothing to remark on: `WARNING 06` (no
// deathmatch start) and `WARNING 19` (no entities in empty space in hull 2) are
// statements about a map's entity set rather than its geometry, and a fixture
// that produced either would teach an operator to read past a warning line.
func FixtureMap() string {
	var brushes []string
	for _, solid := range sealedRoom() {
		brushes = append(brushes, brushText(solid, FixtureTexture))
	}
	return "// Auto-Pigeon Companion native acceptance fixture — synthetic, project-authored.\n" +
		"// Geometry and texture are written by this program; no id Software asset is used.\n" +
		"{\n" +
		"\"classname\" \"worldspawn\"\n" +
		"\"wad\" \"" + FixtureWADName + "\"\n" +
		"\"message\" \"auto-pigeon companion native acceptance\"\n" +
		strings.Join(brushes, "\n") + "\n" +
		"}\n" +
		"{\n" +
		"\"classname\" \"info_player_start\"\n" +
		"\"origin\" \"-32 0 " + strconv.Itoa(fixtureFloorZ+fixtureStartHeight) + "\"\n" +
		"}\n" +
		"{\n" +
		"\"classname\" \"info_player_deathmatch\"\n" +
		"\"origin\" \"32 0 " + strconv.Itoa(fixtureFloorZ+fixtureStartHeight) + "\"\n" +
		"}\n" +
		"{\n" +
		"\"classname\" \"light\"\n" +
		"\"origin\" \"0 0 " + strconv.Itoa(fixtureCeilingZ-32) + "\"\n" +
		"\"light\" \"300\"\n" +
		"}\n"
}

// fixturePixels is a checkerboard of two palette indices this file chose.
//
// Indices, not colours: index 0 and index 15 are the first and sixteenth
// entries of whatever palette the engine is running, and this program makes no
// claim about what they look like.
func fixturePixels(size int) []byte {
	pixels := make([]byte, size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if ((x/4)+(y/4))%2 == 0 {
				pixels[y*size+x] = 0
			} else {
				pixels[y*size+x] = 15
			}
		}
	}
	return pixels
}

// mipChain is four levels, each half the last, by picking one pixel per block.
func mipChain(size int) [][]byte {
	full := fixturePixels(size)
	levels := [][]byte{full}
	for _, step := range []int{2, 4, 8} {
		side := size / step
		level := make([]byte, side*side)
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				level[y*side+x] = full[(y*step)*size+(x*step)]
			}
		}
		levels = append(levels, level)
	}
	return levels
}

// miptex is one miptex lump, from the published Quake layout: a 16-byte
// NUL-padded name, width and height as little-endian uint32, four little-endian
// uint32 offsets measured from the start of the lump, then the levels.
func miptex(name string, size int) ([]byte, error) {
	if len(name) > 15 {
		return nil, fmt.Errorf("texture name %q does not fit a 16-byte NUL-terminated field", name)
	}
	if size%16 != 0 {
		return nil, fmt.Errorf("texture size %d must be a multiple of 16", size)
	}
	levels := mipChain(size)
	header := 16 + 4 + 4 + 4*4
	lump := make([]byte, header)
	copy(lump, name)
	binary.LittleEndian.PutUint32(lump[16:], uint32(size))
	binary.LittleEndian.PutUint32(lump[20:], uint32(size))
	cursor := header
	for index, level := range levels {
		binary.LittleEndian.PutUint32(lump[24+index*4:], uint32(cursor))
		cursor += len(level)
	}
	for _, level := range levels {
		lump = append(lump, level...)
	}
	return lump, nil
}

// FixtureWAD is a WAD2 holding exactly one miptex, from the published layout:
// the magic, the lump count and the directory offset; the lump; then one
// 32-byte directory entry — position, on-disk size, in-memory size, type 0x44
// (a miptex), compression 0, two bytes of padding, and the padded name.
func FixtureWAD() ([]byte, error) {
	lump, err := miptex(FixtureTexture, fixtureTextureSize)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 12+len(lump)+32)
	out = append(out, "WAD2"...)
	out = binary.LittleEndian.AppendUint32(out, 1)
	out = binary.LittleEndian.AppendUint32(out, uint32(12+len(lump)))
	out = append(out, lump...)
	out = binary.LittleEndian.AppendUint32(out, 12)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(lump)))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(lump)))
	entryName := make([]byte, 16)
	copy(entryName, FixtureTexture)
	out = append(out, 0x44, 0, 0, 0)
	out = append(out, entryName...)
	return out, nil
}
