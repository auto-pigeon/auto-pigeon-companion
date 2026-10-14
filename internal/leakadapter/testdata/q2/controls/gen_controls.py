#!/usr/bin/env python3
"""Authors the Q2_010 leak controls: small Quake II .map files and the WAL textures they name.

Everything here is written from scratch for this task; no third-party map or texture bytes.
Usage: gen_controls.py <out-dir>   -> <out>/maps/*.map and <out>/content/textures/q2010/*.wal

The textures carry the content and surface flags Quake II keeps in a WAL header, and every face line
repeats them in the map, the way a Quake II editor writes them; qbsp is handed <out>/content as its
-gamedir and the retail baseq2 (for the palette) as its -basedir.
"""
import os, struct, sys

TEX = "q2010/wall"

# Quake II contents and surface flags (q_shared.h).
CONTENTS_SOLID = 0x1
CONTENTS_WINDOW = 0x2
CONTENTS_PLAYERCLIP = 0x10000
CONTENTS_MONSTERCLIP = 0x20000
CONTENTS_DETAIL = 0x8000000
SURF_NODRAW = 0x80
SURF_TRANS33 = 0x10

def box(mn, mx, tex=TEX, contents=0, flags=0, value=0):
    (x0, y0, z0), (x1, y1, z1) = mn, mx
    tail = "%d %d %d" % (contents, flags, value)
    f = lambda a, b, c: "    ( %d %d %d ) ( %d %d %d ) ( %d %d %d ) %s 0 0 0 1 1 %s" % (*a, *b, *c, tex, tail)
    return "  {\n" + "\n".join([
        f((x0, y1, z1), (x1, y1, z1), (x1, y0, z1)),   # top
        f((x0, y0, z0), (x1, y0, z0), (x1, y1, z0)),   # bottom
        f((x1, y0, z1), (x1, y1, z1), (x1, y1, z0)),   # +x
        f((x0, y1, z1), (x0, y0, z1), (x0, y0, z0)),   # -x
        f((x1, y1, z1), (x0, y1, z1), (x0, y1, z0)),   # +y
        f((x0, y0, z1), (x1, y0, z1), (x1, y0, z0)),   # -y
    ]) + "\n  }"

# The room: interior -256..256 in x/y, 0..256 in z; 16-unit shell, no overlaps.
SHELL = {
    "floor":   ((-272, -272, -16), (272, 272, 0)),
    "ceiling": ((-272, -272, 256), (272, 272, 272)),
    "west":    ((-272, -272, 0), (-256, 272, 256)),
    "east":    ((256, -272, 0), (272, 272, 256)),
    "south":   ((-256, -272, 0), (256, -256, 256)),
    "north":   ((-256, 256, 0), (256, 272, 256)),
}
ALL = list(SHELL)
OPEN = [name for name in ALL if name != "east"]
# The east wall with an 8-unit slot through it, y = -4..4, floor to ceiling.
EAST_SLOTTED = [box((256, -272, 0), (272, -4, 256)), box((256, 4, 0), (272, 272, 256))]
# What stands in the slot in the F controls: a brush exactly filling it.
SLOT = ((256, -4, 0), (272, 4, 256))

def ent(classname, origin=None, **kv):
    lines = ['  "classname" "%s"' % classname]
    if origin: lines.append('  "origin" "%d %d %d"' % origin)
    lines += ['  "%s" "%s"' % item for item in kv.items()]
    return "{\n" + "\n".join(lines) + "\n}"

PLAYER = ent("info_player_start", (-128, -128, 24), angle="45")
LIGHT = ent("light", (0, 0, 192), light="300")

def world(walls, extra=(), message="Q2_010 control"):
    body = [box(*SHELL[name]) for name in walls] + list(extra)
    return '{\n  "classname" "worldspawn"\n  "message" "%s"\n' % message + "\n".join(body) + "\n}"

MAPS = {
    # A. sealed: six structural brushes, one occupant and one light inside.
    "a_sealed": [world(ALL), LIGHT, PLAYER],
    # B. the east wall with an 8-unit slot through it; same occupants.
    "b_gap": [world(OPEN, EAST_SLOTTED), LIGHT, PLAYER],
    # C. sealed, with a light standing outside the shell.
    "c_outside_light": [world(ALL), LIGHT, PLAYER, ent("light", (512, 0, 128), light="300")],
    # D. sealed, no entity at all besides the world.
    "d_no_occupant": [world(ALL)],
    # E. sealed, the only entities stand inside solid brushes.
    "e_in_solid": [world(ALL), ent("info_player_start", (264, 0, 128)), ent("light", (0, 0, 264), light="300")],
    # F. the slot, covered by a detail brush (solid + detail contents).
    "f_detail_cover": [world(OPEN, EAST_SLOTTED + [box(*SLOT, contents=CONTENTS_SOLID | CONTENTS_DETAIL)]),
                       LIGHT, PLAYER],
    # F'. the slot, covered by a player+monster clip brush (nodraw).
    "f_clip_cover": [world(OPEN, EAST_SLOTTED + [box(*SLOT, tex="q2010/clip",
                                                    contents=CONTENTS_PLAYERCLIP | CONTENTS_MONSTERCLIP,
                                                    flags=SURF_NODRAW)]),
                     LIGHT, PLAYER],
    # F''. the slot, covered by a translucent window brush.
    "f_window_cover": [world(OPEN, EAST_SLOTTED + [box(*SLOT, tex="q2010/glass", contents=CONTENTS_WINDOW,
                                                      flags=SURF_TRANS33)]),
                       LIGHT, PLAYER],
    # H. sealed, but the east wall names a texture no WAL exists for.
    "h_missing_wal": [world(OPEN, [box(*SHELL["east"], tex="q2010/nothere")]), LIGHT, PLAYER],
}

# G. unreadable sources: a file cut off mid-plane, and a face whose three points are on one line.
TRUNCATED = '{\n  "classname" "worldspawn"\n  {\n    ( 0 0 0 ) ( 1 0 \n'
def degenerate():
    good = box(*SHELL["east"])
    lines = good.split("\n")
    lines[3] = "    ( 256 -272 0 ) ( 256 -272 128 ) ( 256 -272 256 ) %s 0 0 0 1 1 0 0 0" % TEX
    return [world(OPEN, ["\n".join(lines)]), LIGHT, PLAYER]

# WAL: name[32] width height offsets[4] animname[32] flags contents value, then four mip levels.
WALS = {
    "wall": (0, CONTENTS_SOLID, 0),
    "clip": (SURF_NODRAW, CONTENTS_PLAYERCLIP | CONTENTS_MONSTERCLIP, 0),
    "glass": (SURF_TRANS33, CONTENTS_WINDOW, 0),
}

def wal(name, flags, contents, value, size=16):
    header = bytearray(100)
    header[0:len(name)] = name.encode()
    struct.pack_into("<II", header, 32, size, size)
    offset, body = 100, bytearray()
    for level in range(4):
        struct.pack_into("<I", header, 40 + level * 4, offset)
        side = size >> level
        for y in range(side):
            for x in range(side):
                body.append(16 + ((x * 4 // side + y * 4 // side) % 2) * 8)   # two greys of the palette
        offset += side * side
    struct.pack_into("<iii", header, 88, flags, contents, value)
    return bytes(header) + bytes(body)

def main(out):
    maps = os.path.join(out, "maps"); textures = os.path.join(out, "content", "textures", "q2010")
    for d in (maps, textures):
        os.makedirs(d, exist_ok=True)
    for name, entities in MAPS.items():
        with open(os.path.join(maps, name + ".map"), "w") as handle:
            handle.write("\n".join(entities) + "\n")
    with open(os.path.join(maps, "g_truncated.map"), "w") as handle:
        handle.write(TRUNCATED)
    with open(os.path.join(maps, "g_degenerate_plane.map"), "w") as handle:
        handle.write("\n".join(degenerate()) + "\n")
    for name, (flags, contents, value) in WALS.items():
        with open(os.path.join(textures, name + ".wal"), "wb") as handle:
            handle.write(wal("q2010/" + name, flags, contents, value))

if __name__ == "__main__":
    main(sys.argv[1])
