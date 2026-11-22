package fixturewad

import (
	"fmt"
	"strings"
)

// The fence room (NEW_643, auto-pigeon-libraries#15) is a synthetic Quake 1
// map with a legal alpha-masked fence texture, and the two WADs that hold its
// textures. It is this project's own, authored here from nothing like the WADs
// above, so a test can carry it through the converter, the build and a real
// compiler without any map or texture somebody else made.
//
// A sealed 512×512×256 room of six FenceRoomWall brushes, one FenceRoomFence
// slab standing in it, a player start and a light: closed, so qbsp finds no
// leak and vis and light run; small, so a real compile takes well under a
// second.
const (
	// FenceRoomFence is the fence texture: `{` then an ordinary name, which
	// Quake 1 compilers and engines read as alpha-masked (palette index 255 is
	// transparent).
	FenceRoomFence = "{fence"
	// FenceRoomWall is the ordinary texture every wall uses: the control.
	FenceRoomWall = "sand"
	// FenceRoomFenceWAD and FenceRoomWallWAD are the WAD file names the map's
	// worldspawn lists, each holding the one texture of the same purpose.
	FenceRoomFenceWAD = "fence.wad"
	FenceRoomWallWAD  = "sand.wad"
	// FenceRoomFenceFaces is how many faces of the map carry the fence
	// texture: the slab's six.
	FenceRoomFenceFaces = 6
)

// FenceRoomMap returns the map text with the fence slab textured fence. Called
// with FenceRoomWall it returns the same room with an ordinary slab, which is
// the control: the two differ only in that one texture name.
func FenceRoomMap(fence string) string {
	var out strings.Builder
	out.WriteString("// NEW_643 fence room: synthetic, Auto-Pigeon Companion test fixture\n")
	out.WriteString("{\n\"classname\" \"worldspawn\"\n")
	fmt.Fprintf(&out, "\"wad\" \"%s;%s\"\n", FenceRoomFenceWAD, FenceRoomWallWAD)
	out.WriteString("\"message\" \"fence room\"\n")
	walls := [][6]int{
		{-272, -272, -16, 272, 272, 0},   // floor
		{-272, -272, 256, 272, 272, 272}, // ceiling
		{-272, -272, 0, -256, 272, 256},  // west
		{256, -272, 0, 272, 272, 256},    // east
		{-256, -272, 0, 256, -256, 256},  // south
		{-256, 256, 0, 256, 272, 256},    // north
	}
	for _, wall := range walls {
		box(&out, wall, FenceRoomWall)
	}
	box(&out, [6]int{-8, -128, 0, 8, 128, 128}, fence)
	out.WriteString("}\n")
	out.WriteString("{\n\"classname\" \"info_player_start\"\n\"origin\" \"-128 0 24\"\n\"angle\" \"0\"\n}\n")
	out.WriteString("{\n\"classname\" \"light\"\n\"origin\" \"0 0 192\"\n\"light\" \"300\"\n}\n")
	return out.String()
}

// FenceRoomWADs returns the two WADs the map lists, by file name.
func FenceRoomWADs() map[string][]byte {
	return map[string][]byte{
		FenceRoomFenceWAD: WAD(FenceRoomFence, 255),
		FenceRoomWallWAD:  WAD(FenceRoomWall, 0x50),
	}
}

// box writes one axis-aligned brush from (x1 y1 z1) to (x2 y2 z2) in the
// classic face form, each plane by three integer points, wound so its normal
// faces out — TrenchBroom's own cube.
func box(out *strings.Builder, b [6]int, texture string) {
	x1, y1, z1, x2, y2, z2 := b[0], b[1], b[2], b[3], b[4], b[5]
	faces := [6][9]int{
		{x1, y1, z1, x1, y1 + 1, z1, x1, y1, z1 + 1},
		{x1, y1, z1, x1, y1, z1 + 1, x1 + 1, y1, z1},
		{x1, y1, z1, x1 + 1, y1, z1, x1, y1 + 1, z1},
		{x2, y2, z2, x2, y2 + 1, z2, x2 + 1, y2, z2},
		{x2, y2, z2, x2 + 1, y2, z2, x2, y2, z2 + 1},
		{x2, y2, z2, x2, y2, z2 + 1, x2, y2 + 1, z2},
	}
	out.WriteString("{\n")
	for _, p := range faces {
		fmt.Fprintf(out, "( %d %d %d ) ( %d %d %d ) ( %d %d %d ) %s 0 0 0 1 1\n",
			p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7], p[8], texture)
	}
	out.WriteString("}\n")
}
