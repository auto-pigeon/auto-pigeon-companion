package artifactcheck

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// Quake II's BSP (NEW_329D): `IBSP` version 38, then nineteen (offset, length)
// pairs — or Qbism's extended `QBSP` 38, which a stage writes when its BSP
// format option asks for it.
//
// A container check is not enough here. qbsp writes a valid IBSP 38 that the
// engine cannot run: measured on Auto-Pigeon's large PIPES pulls (NEW_329B),
// one file had 6,512 brush sides naming a plane at or past index 32,768, which
// Yamagi Quake II 8.70 reads through a SIGNED short (`CMod_LoadBrushSides`:
// `LittleShort(in->planenum)`), so the map loaded and the player collided with
// the wrong planes; another had 69,191 brush sides and was refused ("Map has
// too many planes"). Each limit below is one `Com_Error` in that engine's
// `src/common/collision.c`, with the records `CM_InitBoxHull` adds after the
// map's own. A file over any of them is not a build anyone can play, so the
// stage that wrote it fails, naming the limit and what to change.
const (
	q2BSPVersion = 38
	q2BSPLumps   = 19
	q2BSPHeader  = 8 + q2BSPLumps*8
	// What the engine reads as a signed short in a brush side.
	q2SignedPlane = 0x8000
	// A brush-side lump is read whole for the plane check; past this it is
	// already far over the count limit and nothing more needs reading.
	q2SidesReadLimit = 1 << 22
)

const (
	q2LumpEntities    = 0
	q2LumpPlanes      = 1
	q2LumpVisibility  = 3
	q2LumpNodes       = 4
	q2LumpTexinfo     = 5
	q2LumpLeafs       = 8
	q2LumpLeafBrushes = 10
	q2LumpModels      = 13
	q2LumpBrushes     = 14
	q2LumpBrushSides  = 15
	q2LumpAreas       = 17
	q2LumpAreaPortals = 18
)

// q2Limit is one count the engine refuses past, with the records its box hull
// needs on top of the map's own.
type q2Limit struct {
	lump, record, limit, boxHull int
	name                         string
}

var q2Limits = []q2Limit{
	{q2LumpModels, 48, 1024, 0, "models"},
	{q2LumpTexinfo, 76, 8192, 0, "texture infos"},
	{q2LumpNodes, 28, 65536, 6, "nodes"},
	{q2LumpBrushes, 12, 8192, 1, "brushes"},
	{q2LumpLeafs, 28, 65536, 0, "leaves"},
	{q2LumpPlanes, 20, 65536, 12, "planes"},
	{q2LumpLeafBrushes, 2, 65536, 1, "leaf brushes"},
	{q2LumpBrushSides, 4, 65536, 6, "brush sides"},
	{q2LumpAreas, 8, 256, 0, "areas"},
	{q2LumpAreaPortals, 8, 1024, 0, "area portals"},
}

var q2ByteLimits = []struct {
	lump, limit int
	name        string
}{
	{q2LumpVisibility, 0x100000, "visibility"},
	{q2LumpEntities, 0x40000, "entity"},
}

// q2Remedy is what a person can change when a Quake II engine cannot load the map.
const q2Remedy = "; Yamagi Quake II 8.70 cannot run it as written. Make the map with fewer, larger pieces " +
	"(a pipe: fewer Sides or arc segments; SCULPT or SURFACE DEFORM: a lower Tessellation or New pieces are detail), " +
	"or split it into smaller maps"

func quake2BSP(file *os.File, size int64) string {
	if size < q2BSPHeader {
		return fmt.Sprintf("it is %d bytes, and the header alone is %d", size, q2BSPHeader)
	}
	header := make([]byte, q2BSPHeader)
	if _, err := io.ReadFull(file, header); err != nil {
		return "its header could not be read: " + err.Error()
	}
	magic := string(header[:4])
	if magic != "IBSP" && magic != "QBSP" {
		return fmt.Sprintf("it starts with %q, not IBSP or QBSP", printable(header[:4]))
	}
	if version := int32(binary.LittleEndian.Uint32(header[4:8])); version != q2BSPVersion {
		return fmt.Sprintf("it is %s version %d; Quake II is %d", magic, version, q2BSPVersion)
	}
	offset := func(lump int) int64 { return int64(int32(binary.LittleEndian.Uint32(header[8+lump*8:]))) }
	length := func(lump int) int64 { return int64(int32(binary.LittleEndian.Uint32(header[12+lump*8:]))) }
	for lump := 0; lump < q2BSPLumps; lump++ {
		if offset(lump) < 0 || length(lump) < 0 || offset(lump)+length(lump) > size {
			return fmt.Sprintf("lump %d claims bytes %d..%d of a %d-byte file: it is truncated or was not finished",
				lump, offset(lump), offset(lump)+length(lump), size)
		}
	}
	if length(q2LumpEntities) == 0 {
		return "it has no entities, and every map has a worldspawn"
	}
	if magic == "QBSP" {
		// Qbism's extended format has its own, larger limits, and only an
		// engine that reads it should be given it: the stage's own option.
		return ""
	}
	var over []string
	for _, l := range q2Limits {
		count := int(length(l.lump)) / l.record
		if count+l.boxHull > l.limit {
			margin := ""
			if l.boxHull > 0 {
				margin = fmt.Sprintf(" (and %d more for the engine's own box hull)", l.boxHull)
			}
			over = append(over, fmt.Sprintf("%d %s%s, where the engine holds %d", count, l.name, margin, l.limit))
		}
	}
	for _, l := range q2ByteLimits {
		if n := length(l.lump); n > int64(l.limit) {
			over = append(over, fmt.Sprintf("a %d-byte %s lump, where the engine holds %d", n, l.name, l.limit))
		}
	}
	if sides := length(q2LumpBrushSides); sides > 0 && sides <= q2SidesReadLimit {
		raw := make([]byte, sides)
		if _, err := file.ReadAt(raw, offset(q2LumpBrushSides)); err != nil {
			return "its brush sides could not be read: " + err.Error()
		}
		signed := 0
		for at := 0; at+4 <= len(raw); at += 4 {
			if binary.LittleEndian.Uint16(raw[at:]) >= q2SignedPlane {
				signed++
			}
		}
		if signed > 0 {
			over = append(over, fmt.Sprintf("%d brush side(s) naming a plane at or past index %d, which the engine "+
				"reads as a negative number, so the player would collide with the wrong planes", signed, q2SignedPlane))
		}
	}
	if len(over) > 0 {
		return "it is over Quake II's limits: " + strings.Join(over, "; ") + q2Remedy
	}
	return ""
}
