package q3deps

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// What a COMPILED map needs, read out of the BSP itself.
//
// The map source says what the compiler looked for. That is not what an engine
// looks for, and the two differ in both directions:
//
//   - a face textured with `common/caulk` is a dependency of the compile and is
//     not drawn, so no engine ever loads that shader;
//   - a `misc_model` is baked into the BSP as triangles, so its `.md3` is a
//     dependency of the compile and the engine never opens it — but the shaders
//     its surfaces name ARE drawn, and they appear nowhere in the map source.
//
// So the runtime set is not derived from the source by a rule about names. It
// is read from the file the engine reads: the shader each DRAWN surface and
// each fog volume names, and the files the entity lump's keys name. The layout
// is id Software's published `dheader_t` for IBSP version 46.

// Limits on a BSP. The lump count and record sizes are the format's own; the
// byte bounds are this scan's.
const (
	bspIdent        = "IBSP"
	bspVersion      = 46
	bspLumps        = 17
	bspLumpEntities = 0
	bspLumpShaders  = 1
	bspLumpFogs     = 12
	bspLumpSurfaces = 13
	bspShaderSize   = 64 + 4 + 4
	bspFogSize      = 64 + 4 + 4
	bspSurfaceSize  = 104
	maxBSPEntities  = 16 << 20
	maxBSPRecords   = 1 << 20
)

// ParseBSP reads a compiled Quake III map and returns what an engine will look
// for when it loads it, in the order first seen.
func ParseBSP(bspPath string) ([]Reference, error) {
	file, err := os.Open(bspPath)
	if err != nil {
		return nil, fmt.Errorf("q3deps: reading the compiled map: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("q3deps: reading the compiled map: %w", err)
	}
	size := info.Size()

	header := make([]byte, 8+bspLumps*8)
	if _, err := io.ReadFull(file, header); err != nil {
		return nil, fmt.Errorf("q3deps: %s is too short to be a compiled Quake III map", bspPath)
	}
	if string(header[:4]) != bspIdent {
		return nil, fmt.Errorf("q3deps: %s does not begin with %q; it is not a Quake III BSP", bspPath, bspIdent)
	}
	if version := binary.LittleEndian.Uint32(header[4:]); version != bspVersion {
		return nil, fmt.Errorf("q3deps: %s is BSP version %d, and this reads version %d", bspPath, version, bspVersion)
	}
	lump := func(index int, record, limit int64) ([]byte, error) {
		offset := int64(int32(binary.LittleEndian.Uint32(header[8+index*8:])))
		length := int64(int32(binary.LittleEndian.Uint32(header[12+index*8:])))
		switch {
		case offset < 0 || length < 0 || offset > size || length > size-offset:
			return nil, fmt.Errorf("q3deps: %s: lump %d lies outside the file", bspPath, index)
		case length > limit:
			return nil, fmt.Errorf("q3deps: %s: lump %d is %d bytes, over the %d this scan reads", bspPath, index, length, limit)
		case record > 0 && length%record != 0:
			return nil, fmt.Errorf("q3deps: %s: lump %d is not a whole number of %d-byte records", bspPath, index, record)
		}
		data := make([]byte, length)
		if _, err := file.ReadAt(data, offset); err != nil {
			return nil, fmt.Errorf("q3deps: %s: reading lump %d: %w", bspPath, index, err)
		}
		return data, nil
	}

	shaders, err := lump(bspLumpShaders, bspShaderSize, maxBSPRecords*bspShaderSize)
	if err != nil {
		return nil, err
	}
	surfaces, err := lump(bspLumpSurfaces, bspSurfaceSize, maxBSPRecords*bspSurfaceSize)
	if err != nil {
		return nil, err
	}
	fogs, err := lump(bspLumpFogs, bspFogSize, maxBSPRecords*bspFogSize)
	if err != nil {
		return nil, err
	}
	entities, err := lump(bspLumpEntities, 0, maxBSPEntities)
	if err != nil {
		return nil, err
	}

	found := newCollector()
	shaderCount := len(shaders) / bspShaderSize
	addShader := func(name, from string) {
		name = normalizeFile(name)
		if name == "" || name == "." || notAFile[name] {
			return
		}
		found.add(Reference{Name: name, Raw: name, Kind: KindShader, From: from, Count: 1})
	}
	for at := 0; at+bspSurfaceSize <= len(surfaces); at += bspSurfaceSize {
		index := int(int32(binary.LittleEndian.Uint32(surfaces[at:])))
		if index < 0 || index >= shaderCount {
			return nil, fmt.Errorf("q3deps: %s: a surface names shader %d of %d", bspPath, index, shaderCount)
		}
		addShader(cString(shaders[index*bspShaderSize:index*bspShaderSize+64]), "compiled surface")
	}
	for at := 0; at+bspFogSize <= len(fogs); at += bspFogSize {
		addShader(cString(fogs[at:at+64]), "compiled fog volume")
	}

	// The entity lump is the map source's entity grammar with the brushes
	// removed, so the one parser reads both. On a compiled entity `model` is
	// `*N`, an inline brush model, which that parser already passes over; a
	// `misc_model` is gone, baked into the surfaces above; and `model2`,
	// `noise` and `music` are the files the game will ask the engine for.
	entityReferences, err := parseMap(strings.NewReader(cString(entities)))
	if err != nil {
		return nil, fmt.Errorf("q3deps: %s: its entity lump does not parse: %w", bspPath, err)
	}
	for _, reference := range entityReferences {
		found.add(reference)
	}
	return found.all(), nil
}
