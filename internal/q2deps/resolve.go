package q2deps

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// Where says which folder supplies a reference.
type Where string

const (
	// FromContent: the user's own content folder — what the package carries.
	FromContent Where = "content"
	// FromBase: the base game, which is never packaged.
	FromBase Where = "base"
	// Missing: neither has it.
	Missing Where = "missing"
	// FromAccount: an account texture source the map binds, at the revision
	// it binds — a file AUB's texture export carried and this machine
	// verified (Q2_015A). Packaged on AUB's provenance, with no grant asked.
	FromAccount Where = "account"
)

// Layers are the folders a reference is looked up in, in the order an engine
// finds it once the package is installed: the package's own files — the
// account's textures, then the content the package carries — and then the
// base game.
type Layers struct {
	// Account is the build's verified copy of the account textures, laid out
	// as `textures/<namespace>/<file>.wal`. Nil when the map binds none.
	Account *Index
	Content *Index
	Base    *Index
}

// Resolved is a reference and where it was found.
type Resolved struct {
	Reference
	Where Where `json:"where"`
	// Path is the virtual path that resolved, one of Paths; empty when Missing.
	Path     string   `json:"path,omitempty"`
	Location Location `json:"location"`
	// Shadows is set when a content file has the same path as a base-game
	// file: the package's copy is the one the engine loads.
	Shadows bool `json:"shadows,omitempty"`
	// ShadowedContent is the content folder's own file at the same path as an
	// account texture that won: the account's revision is what the map was
	// saved against, so it is the one packaged, and this one is named.
	ShadowedContent *Location `json:"shadowed_content,omitempty"`
	// Also are the content folder's other spellings of a reference whose
	// file depends on the renderer: a sky face is asked for as `.pcx` by a
	// paletted renderer and as `.tga` by every other (Yamagi's
	// `GetSkyImage`), so a package that carried only the first would draw no
	// sky in a GL renderer.
	Also []Location `json:"also,omitempty"`
	// Problem is why a file the engine needs cannot be used as it is.
	Problem string `json:"problem,omitempty"`
}

// Report is everything a compiled map asks for and where each is.
type Report struct {
	Map    *Map       `json:"map"`
	Files  []Resolved `json:"files"`
	Limits []string   `json:"limits,omitempty"`
}

// Resolve finds every reference of a map, the way an engine with the package
// installed as a mod over the base game would: for each path the engine tries,
// in order, the user's content (what the mod will carry) and then the base
// game. A `.md2` the content holds is read for the skins it names, which are
// resolved the same way.
func Resolve(m *Map, content, base *Index) *Report {
	return ResolveLayers(m, Layers{Content: content, Base: base})
}

// ResolveLayers is [Resolve] with the account's textures searched first: a
// path an account texture source the map binds supplies is that source's file,
// whatever the content folder or the base game also holds under it.
func ResolveLayers(m *Map, layers Layers) *Report {
	content, base := layers.Content, layers.Base
	report := &Report{Map: m, Limits: append([]string(nil), m.Limits...)}
	if layers.Account != nil {
		report.Limits = append(report.Limits, layers.Account.Limits...)
	}
	if content != nil {
		report.Limits = append(report.Limits, content.Limits...)
	}
	if base != nil {
		report.Limits = append(report.Limits, base.Limits...)
	}
	queue := append([]Reference(nil), m.References...)
	seen := map[string]bool{}
	for len(queue) > 0 {
		reference := queue[0]
		queue = queue[1:]
		key := strings.ToLower(reference.Paths[0])
		if seen[key] {
			continue
		}
		seen[key] = true
		resolved := resolveOne(reference, layers)
		if resolved.Where == FromContent && strings.EqualFold(pathExt(resolved.Path), ".md2") {
			skins, err := MD2Skins(resolved.Location.File)
			if err != nil {
				resolved.Problem = err.Error()
			}
			for _, skin := range skins {
				queue = append(queue, Reference{Kind: KindSkin, Name: skin, Paths: []string{skin},
					From: "skin of " + resolved.Path})
			}
		}
		report.Files = append(report.Files, resolved)
	}
	return report
}

func resolveOne(reference Reference, layers Layers) Resolved {
	account, content, base := layers.Account, layers.Content, layers.Base
	for i, path := range reference.Paths {
		if location, ok := account.FindLoose(path); ok {
			_, inBase := base.Find(path)
			resolved := Resolved{Reference: reference, Where: FromAccount, Path: path, Location: location, Shadows: inBase}
			if hidden, found := content.FindLoose(path); found {
				resolved.ShadowedContent = &hidden
			}
			for _, other := range reference.Paths[i+1:] {
				if also, found := account.FindLoose(other); found {
					resolved.Also = append(resolved.Also, also)
				}
			}
			return resolved
		}
		if location, ok := content.FindLoose(path); ok {
			_, inBase := base.Find(path)
			resolved := Resolved{Reference: reference, Where: FromContent, Path: path, Location: location, Shadows: inBase}
			for _, other := range reference.Paths[i+1:] {
				if also, found := content.FindLoose(other); found {
					resolved.Also = append(resolved.Also, also)
				}
			}
			return resolved
		}
		if location, ok := base.Find(path); ok {
			return Resolved{Reference: reference, Where: FromBase, Path: path, Location: location}
		}
	}
	return Resolved{Reference: reference, Where: Missing}
}

func pathExt(path string) string {
	if at := strings.LastIndexByte(path, '.'); at >= 0 && !strings.Contains(path[at:], "/") {
		return path[at:]
	}
	return ""
}

// MD2 layout: `IDP2`, version 8, then fifteen int32 counts and offsets; the
// skins are `num_skins` 64-byte names at `ofs_skins`.
const (
	md2Ident   = "IDP2"
	md2Version = 8
	maxMD2     = 32 << 20
	maxSkins   = 32
)

// MD2Skins reads the skin paths a Quake II model names in its own header.
func MD2Skins(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("q2deps: reading %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	header := make([]byte, 68)
	if _, err := io.ReadFull(file, header); err != nil {
		return nil, fmt.Errorf("q2deps: %s is too short to be a Quake II model", path)
	}
	if string(header[:4]) != md2Ident || binary.LittleEndian.Uint32(header[4:]) != md2Version {
		return nil, fmt.Errorf("q2deps: %s is not an IDP2 version 8 model", path)
	}
	count := int64(int32(binary.LittleEndian.Uint32(header[20:])))
	offset := int64(int32(binary.LittleEndian.Uint32(header[44:])))
	switch {
	case info.Size() > maxMD2:
		return nil, fmt.Errorf("q2deps: %s is %d bytes, over the %d this review reads", path, info.Size(), maxMD2)
	case count < 0 || count > maxSkins:
		return nil, fmt.Errorf("q2deps: %s names %d skins", path, count)
	case offset < 68 || offset+count*64 > info.Size():
		return nil, fmt.Errorf("q2deps: %s: its skin table lies outside the file", path)
	}
	names := make([]byte, count*64)
	if _, err := file.ReadAt(names, offset); err != nil {
		return nil, fmt.Errorf("q2deps: %s: reading its skins: %w", path, err)
	}
	var skins []string
	for i := int64(0); i < count; i++ {
		if name := strings.TrimSpace(cString(names[i*64 : (i+1)*64])); name != "" {
			skins = append(skins, name)
		}
	}
	return skins, nil
}
