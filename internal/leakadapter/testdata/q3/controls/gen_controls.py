#!/usr/bin/env python3
"""Authors the Q3_018 leak controls: small Quake III .map files, one shader script, one image.

Everything here is written from scratch for this task; no third-party map or texture bytes.
Usage: gen_controls.py <out-dir>   -> <out>/maps/*.map and <out>/gameroot/baseq3/{scripts,textures}
"""
import os, struct, sys

TEX = "q3018/wall"

def box(mn, mx, tex=TEX, flags="0 0 0"):
    (x0, y0, z0), (x1, y1, z1) = mn, mx
    f = lambda a, b, c: "    ( %d %d %d ) ( %d %d %d ) ( %d %d %d ) %s 0 0 0 0.5 0.5 %s" % (*a, *b, *c, tex, flags)
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

def patch(rows, tex=TEX):
    w, h = len(rows), len(rows[0])
    out = ["  {", "    patchDef2", "    {", "      " + tex, "      ( %d %d 0 0 0 )" % (w, h), "      ("]
    for i, row in enumerate(rows):
        out.append("        ( " + " ".join("( %g %g %g %g %g )" % (x, y, z, i / (w - 1), j / (h - 1))
                                         for j, (x, y, z) in enumerate(row)) + " )")
    out += ["      )", "    }", "  }"]
    return "\n".join(out)

# An arch standing inside the room: curved, touches nothing structural.
ARCH = [[(x, y, z) for y in (-64, 0, 64)] for (x, z) in ((-96, 16), (-96, 112), (0, 144), (96, 112), (96, 16))]
# A bulged sheet standing in the east wall's place (x = 256..272, bulging to 288): it covers the gap.
COVER = [[(x, y, z) for z in (0, 128, 256)] for (x, y) in ((264, -256), (288, 0), (264, 256))]

def ent(classname, origin=None, **kv):
    lines = ['  "classname" "%s"' % classname]
    if origin: lines.append('  "origin" "%d %d %d"' % origin)
    lines += ['  "%s" "%s"' % item for item in kv.items()]
    return "{\n" + "\n".join(lines) + "\n}"

PLAYER = ent("info_player_deathmatch", (-128, -128, 32), angle="45")
LIGHT = ent("light", (0, 0, 192), light="300")

def world(walls, extra=(), message="Q3_018 control"):
    body = [box(*SHELL[name]) for name in walls] + list(extra)
    return '{\n  "classname" "worldspawn"\n  "message" "%s"\n' % message + "\n".join(body) + "\n}"

ALL = list(SHELL)
OPEN = [name for name in ALL if name != "east"]
DETAIL = "134217728 0 0"   # CONTENTS_DETAIL

MAPS = {
    # A. sealed: six structural brushes, one occupant, one interior patch.
    "a_sealed": [world(ALL, [patch(ARCH)]), LIGHT, PLAYER],
    # B. the east wall removed; same occupant, same resources.
    "b_gap": [world(OPEN, [patch(ARCH)]), LIGHT, PLAYER],
    # C. sealed, with a second eligible point entity standing outside the room.
    "c_outside_entity": [world(ALL, [patch(ARCH)]), LIGHT, PLAYER, ent("info_player_deathmatch", (512, 0, 32))],
    # D. sealed, no entity at all besides the world.
    "d_no_occupant": [world(ALL, [patch(ARCH)])],
    # D'. sealed, the only entities stand inside solid brushes.
    "d_in_solid": [world(ALL, [patch(ARCH)]), ent("info_player_deathmatch", (264, 0, 128)),
                   ent("light", (0, 0, 264), light="300")],
    # E. the gap, visually covered by a curved patch.
    "e_patch_cover": [world(OPEN, [patch(ARCH), patch(COVER)]), LIGHT, PLAYER],
    # E'. the gap, covered by a detail-only brush where the wall was.
    "e_detail_cover": [world(OPEN, [patch(ARCH), box(*SHELL["east"], flags=DETAIL)]), LIGHT, PLAYER],
    # Sealed, but one wall names a shader nothing defines.
    "x_missing_shader": [world([n for n in ALL if n != "east"],
                               [patch(ARCH), box(*SHELL["east"], tex="q3018/nothere")]), LIGHT, PLAYER],
    # Sealed, but one wall uses a nodraw/nonsolid authored shader.
    "x_nonsolid_wall": [world(OPEN, [patch(ARCH), box(*SHELL["east"], tex="q3018/ghost")]), LIGHT, PLAYER],
}

SHADER = """// Q3_018 authored control materials. Written for this test; nothing here is game data.
textures/q3018/wall
{
	qer_editorimage textures/q3018/wall.tga
	{
		map textures/q3018/wall.tga
		rgbGen identity
	}
}

textures/q3018/ghost
{
	qer_editorimage textures/q3018/wall.tga
	surfaceparm nonsolid
	surfaceparm trans
	surfaceparm nodraw
}
"""

def tga(path, size=16):
    header = struct.pack("<BBBHHBHHHHBB", 0, 0, 2, 0, 0, 0, 0, 0, size, size, 24, 0x20)
    pixels = bytearray()
    for y in range(size):
        for x in range(size):
            v = 150 if (x // 4 + y // 4) % 2 else 110
            pixels += bytes((v, v, v + 20))
    with open(path, "wb") as handle:
        handle.write(header + bytes(pixels))

def main(out):
    maps = os.path.join(out, "maps"); base = os.path.join(out, "gameroot", "baseq3")
    for d in (maps, os.path.join(base, "scripts"), os.path.join(base, "textures", "q3018")):
        os.makedirs(d, exist_ok=True)
    for name, entities in MAPS.items():
        with open(os.path.join(maps, name + ".map"), "w") as handle:
            handle.write("\n".join(entities) + "\n")
    with open(os.path.join(maps, "f_malformed.map"), "w") as handle:
        handle.write('{\n  "classname" "worldspawn"\n  {\n    ( 0 0 0 ) ( 1 0 \n')
    with open(os.path.join(base, "scripts", "q3018.shader"), "w") as handle:
        handle.write(SHADER)
    with open(os.path.join(base, "scripts", "shaderlist.txt"), "w") as handle:
        handle.write("q3018\n")
    tga(os.path.join(base, "textures", "q3018", "wall.tga"))

if __name__ == "__main__":
    main(sys.argv[1])
