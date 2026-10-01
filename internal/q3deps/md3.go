package q3deps

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
)

// What an MD3 model names inside itself.
//
// A `misc_model` is a file, and the file is not the whole dependency: each of
// its surfaces carries the names of the shaders it is drawn with, as text, in a
// fixed place. Q3Map2 looks every one of them up when it bakes the model into
// the map, and an engine looks them up when it draws a model an entity carries
// at runtime. A review that stopped at "the `.md3` is there" called a model
// whose skin was missing complete.
//
// Only MD3 is read. It is id Software's published format — `md3Header_t`,
// `md3Surface_t` and `md3Shader_t` in the Quake III source — and it is the one
// a Quake III map package carries. An `.ase` or an `.obj` is text in another
// grammar, and those keep the sentence that says they were not read.

// Limits on a model file. MD3_MAX_SURFACES and MD3_MAX_SHADERS are the engine's
// own; the byte bound is this scan's.
const (
	maxModelBytes  = 64 << 20
	md3MaxSurfaces = 32
	md3MaxShaders  = 256
	md3Ident       = "IDP3"
	md3Version     = 15
	md3NameLength  = 64
)

// modelShaders returns the shader names a model's surfaces carry, normalized
// the way an engine looks them up: without the image extension a modelling tool
// usually leaves on. It returns nil, and no error, for a model that is not an
// MD3 — which is "not read", and the caller says so.
func modelShaders(model located) ([]string, error) {
	if !strings.EqualFold(path.Ext(model.Path), ".md3") {
		return nil, nil
	}
	reader, err := openShaderScript(model.Source)
	if err != nil {
		return nil, fmt.Errorf("the model %s could not be opened to read its shader names", model.Path)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxModelBytes+1))
	if err != nil {
		return nil, fmt.Errorf("the model %s could not be read: %v", model.Path, err)
	}
	if len(data) > maxModelBytes {
		return nil, fmt.Errorf("the model %s is over the %d bytes this scan reads of a model", model.Path, maxModelBytes)
	}
	names, err := parseMD3Shaders(data)
	if err != nil {
		return nil, fmt.Errorf("the model %s is not a readable MD3 (%v), so its shader names are not in this report",
			model.Path, err)
	}
	return names, nil
}

// parseMD3Shaders reads the shader names out of an MD3 file's bytes.
func parseMD3Shaders(data []byte) ([]string, error) {
	// md3Header_t: ident, version, name[64], flags, numFrames, numTags,
	// numSurfaces, numSkins, ofsFrames, ofsTags, ofsSurfaces, ofsEnd.
	const headerSize = 4 + 4 + md3NameLength + 9*4
	if len(data) < headerSize {
		return nil, fmt.Errorf("it is %d bytes, shorter than an MD3 header", len(data))
	}
	if string(data[:4]) != md3Ident {
		return nil, fmt.Errorf("it does not begin with %q", md3Ident)
	}
	int32At := func(offset int) int { return int(int32(binary.LittleEndian.Uint32(data[offset:]))) }
	if version := int32At(4); version != md3Version {
		return nil, fmt.Errorf("it is MD3 version %d and this reads version %d", version, md3Version)
	}
	surfaces := int32At(8 + md3NameLength + 3*4)
	offset := int32At(8 + md3NameLength + 7*4)
	if surfaces < 0 || surfaces > md3MaxSurfaces {
		return nil, fmt.Errorf("it declares %d surfaces; an MD3 has at most %d", surfaces, md3MaxSurfaces)
	}

	// md3Surface_t: ident, name[64], flags, numFrames, numShaders, numVerts,
	// numTriangles, ofsTriangles, ofsShaders, ofsSt, ofsXyzNormals, ofsEnd.
	const surfaceSize = 4 + md3NameLength + 10*4
	var names []string
	seen := map[string]bool{}
	for surface := 0; surface < surfaces; surface++ {
		if offset < 0 || offset > len(data)-surfaceSize {
			return nil, fmt.Errorf("surface %d lies outside the file", surface)
		}
		base := offset
		shaders := int32At(base + 4 + md3NameLength + 2*4)
		shadersAt := int32At(base + 4 + md3NameLength + 6*4)
		end := int32At(base + 4 + md3NameLength + 9*4)
		if shaders < 0 || shaders > md3MaxShaders {
			return nil, fmt.Errorf("surface %d declares %d shaders; an MD3 surface has at most %d",
				surface, shaders, md3MaxShaders)
		}
		// md3Shader_t: name[64], shaderIndex.
		const shaderSize = md3NameLength + 4
		for shader := 0; shader < shaders; shader++ {
			at := base + shadersAt + shader*shaderSize
			if shadersAt < 0 || at < 0 || at > len(data)-shaderSize {
				return nil, fmt.Errorf("a shader of surface %d lies outside the file", surface)
			}
			name := cString(data[at : at+md3NameLength])
			if name == "" || notAFile[strings.ToLower(name)] {
				continue
			}
			normalized := stripImageExtension(normalizeFile(name))
			if !seen[normalized] {
				seen[normalized] = true
				names = append(names, normalized)
			}
		}
		if end <= 0 {
			return nil, fmt.Errorf("surface %d declares no end", surface)
		}
		offset = base + end
	}
	return names, nil
}

// cString is a fixed-width name field up to its terminator.
func cString(field []byte) string {
	for i, b := range field {
		if b == 0 {
			return string(field[:i])
		}
	}
	return string(field)
}

// stripImageExtension removes the extension the way the renderer does before it
// looks a shader up (`COM_StripExtension` in `R_FindShader`): a model that says
// `models/x/skin.tga` is drawn with the shader `models/x/skin`.
func stripImageExtension(name string) string {
	if extension := path.Ext(name); extension != "" && !strings.Contains(extension, "/") {
		return strings.TrimSuffix(name, extension)
	}
	return name
}
