package q2deps

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// The layout is id Software's published `dheader_t` for IBSP version 38: the
// four-byte ident, the version, then nineteen (offset, length) pairs. A texinfo
// record is two texture axes (eight floats), flags, value, a 32-byte texture
// name and the next texinfo of an animation chain: 76 bytes.
const (
	bspIdent        = "IBSP"
	BSPVersion      = 38
	bspLumps        = 19
	bspLumpEntities = 0
	bspLumpTexinfo  = 5
	bspTexinfoSize  = 76
	maxBSPEntities  = 16 << 20
	maxTexinfo      = 1 << 16
)

// Kind is what a reference is to an engine.
type Kind string

const (
	KindTexture Kind = "texture"
	KindSky     Kind = "sky"
	KindSound   Kind = "sound"
	KindModel   Kind = "model"
	KindSkin    Kind = "skin"
)

// Reference is one file an engine will ask for.
type Reference struct {
	Kind Kind `json:"kind"`
	// Name is what the map names: a texture name, a sky face, a `noise` value,
	// a model path, a skin path.
	Name string `json:"name"`
	// Paths are the virtual paths the engine tries, in its order. The first
	// that resolves is the one it loads.
	Paths []string `json:"paths"`
	// From says where in the map it was named.
	From string `json:"from"`
}

// Entity is one entity of the entity lump, its pairs in their order.
type Entity struct {
	Pairs [][2]string `json:"pairs"`
}

// Value is the last value of a key, which is what the game reads.
func (e Entity) Value(key string) string {
	value := ""
	for _, pair := range e.Pairs {
		if strings.EqualFold(pair[0], key) {
			value = pair[1]
		}
	}
	return value
}

// Map is what a compiled map asks of an engine.
type Map struct {
	Version    int         `json:"version"`
	Texinfo    int         `json:"texinfo"`
	Entities   []Entity    `json:"-"`
	References []Reference `json:"references"`
	// Limits is what this scan did not look at, one sentence each.
	Limits []string `json:"limits,omitempty"`
}

// SkySides are the six faces of a Quake II sky, in the engine's order.
var SkySides = []string{"rt", "bk", "lf", "ft", "up", "dn"}

// ParseBSP reads a compiled Quake II map.
func ParseBSP(path string) (*Map, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("q2deps: reading the compiled map: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("q2deps: reading the compiled map: %w", err)
	}
	size := info.Size()
	header := make([]byte, 8+bspLumps*8)
	if _, err := io.ReadFull(file, header); err != nil {
		return nil, fmt.Errorf("q2deps: %s is too short to be a compiled Quake II map", path)
	}
	if string(header[:4]) != bspIdent {
		return nil, fmt.Errorf("q2deps: %s does not begin with %q; it is not a Quake II BSP", path, bspIdent)
	}
	version := int(binary.LittleEndian.Uint32(header[4:]))
	if version != BSPVersion {
		return nil, fmt.Errorf("q2deps: %s is IBSP version %d; Quake II's is %d", path, version, BSPVersion)
	}
	lump := func(index int, record, limit int64) ([]byte, error) {
		offset := int64(int32(binary.LittleEndian.Uint32(header[8+index*8:])))
		length := int64(int32(binary.LittleEndian.Uint32(header[12+index*8:])))
		switch {
		case offset < 0 || length < 0 || offset > size || length > size-offset:
			return nil, fmt.Errorf("q2deps: %s: lump %d lies outside the file", path, index)
		case length > limit:
			return nil, fmt.Errorf("q2deps: %s: lump %d is %d bytes, over the %d this scan reads", path, index, length, limit)
		case record > 0 && length%record != 0:
			return nil, fmt.Errorf("q2deps: %s: lump %d is not a whole number of %d-byte records", path, index, record)
		}
		data := make([]byte, length)
		if _, err := file.ReadAt(data, offset); err != nil {
			return nil, fmt.Errorf("q2deps: %s: reading lump %d: %w", path, index, err)
		}
		return data, nil
	}
	texinfo, err := lump(bspLumpTexinfo, bspTexinfoSize, maxTexinfo*bspTexinfoSize)
	if err != nil {
		return nil, err
	}
	entityLump, err := lump(bspLumpEntities, 0, maxBSPEntities)
	if err != nil {
		return nil, err
	}
	entities, err := ParseEntities(cString(entityLump))
	if err != nil {
		return nil, fmt.Errorf("q2deps: %s: its entity lump does not parse: %w", path, err)
	}

	result := &Map{Version: version, Texinfo: len(texinfo) / bspTexinfoSize, Entities: entities}
	seen := map[string]bool{}
	add := func(reference Reference) {
		key := string(reference.Kind) + "\x00" + strings.ToLower(reference.Paths[0])
		if seen[key] {
			return
		}
		seen[key] = true
		result.References = append(result.References, reference)
	}
	for i := 0; i < result.Texinfo; i++ {
		record := texinfo[i*bspTexinfoSize : (i+1)*bspTexinfoSize]
		name := cString(record[40:72])
		if name == "" {
			continue
		}
		add(Reference{Kind: KindTexture, Name: name, Paths: []string{"textures/" + name + ".wal"},
			From: fmt.Sprintf("texinfo %d", i)})
	}
	for index, entity := range entities {
		class := entity.Value("classname")
		where := fmt.Sprintf("entity %d (%s)", index, class)
		if index == 0 || strings.EqualFold(class, "worldspawn") {
			if sky := strings.TrimSpace(entity.Value("sky")); sky != "" {
				for _, side := range SkySides {
					add(Reference{Kind: KindSky, Name: sky + side,
						Paths: []string{"env/" + sky + side + ".pcx", "env/" + sky + side + ".tga"},
						From:  "worldspawn sky"})
				}
			}
			if track := strings.TrimSpace(entity.Value("sounds")); track != "" {
				result.Limits = append(result.Limits, fmt.Sprintf(
					"worldspawn's sounds %q selects a music track by number; the track is the game's, not a file of this map", track))
			}
		}
		if noise := strings.TrimSpace(entity.Value("noise")); noise != "" {
			file := noise
			if !strings.Contains(strings.ToLower(file), ".wav") {
				file += ".wav"
			}
			add(Reference{Kind: KindSound, Name: noise, Paths: []string{"sound/" + file}, From: where + " noise"})
		}
		if model := strings.TrimSpace(entity.Value("model")); model != "" && !strings.HasPrefix(model, "*") {
			add(Reference{Kind: KindModel, Name: model, Paths: []string{model}, From: where + " model"})
		}
		if strings.EqualFold(class, "target_changelevel") {
			if next := entity.Value("map"); next != "" {
				result.Limits = append(result.Limits, fmt.Sprintf(
					"%s leads to the map %q, which is another map and is not packaged with this one", where, next))
			}
		}
	}
	result.Limits = append(result.Limits,
		"models and sounds the game chooses by classname (an item's model, a monster's sounds) are the game's own and come from the base game")
	return result, nil
}

// ParseEntities reads an entity lump: `{ "key" "value" … }` blocks.
func ParseEntities(text string) ([]Entity, error) {
	var entities []Entity
	var current *Entity
	var pending []string
	at := 0
	line := 1
	for at < len(text) {
		ch := text[at]
		switch {
		case ch == '\n':
			line++
			at++
		case ch == ' ' || ch == '\t' || ch == '\r':
			at++
		case ch == '{':
			if current != nil {
				return nil, fmt.Errorf("line %d: an entity opens inside another", line)
			}
			current = &Entity{}
			at++
		case ch == '}':
			if current == nil {
				return nil, fmt.Errorf("line %d: `}` closes no entity", line)
			}
			if len(pending) != 0 {
				return nil, fmt.Errorf("line %d: key %q has no value", line, pending[0])
			}
			entities = append(entities, *current)
			current = nil
			at++
		case ch == '"':
			end := strings.IndexByte(text[at+1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("line %d: a quoted string is not closed", line)
			}
			token := text[at+1 : at+1+end]
			if strings.ContainsRune(token, '\n') {
				return nil, fmt.Errorf("line %d: a quoted string spans lines", line)
			}
			at += end + 2
			if current == nil {
				return nil, fmt.Errorf("line %d: %s is outside an entity", line, strconv.Quote(token))
			}
			pending = append(pending, token)
			if len(pending) == 2 {
				current.Pairs = append(current.Pairs, [2]string{pending[0], pending[1]})
				pending = pending[:0]
			}
		default:
			return nil, fmt.Errorf("line %d: unexpected %q", line, string(ch))
		}
	}
	if current != nil {
		return nil, fmt.Errorf("the last entity is not closed")
	}
	return entities, nil
}

func cString(b []byte) string {
	if end := strings.IndexByte(string(b), 0); end >= 0 {
		b = b[:end]
	}
	return string(b)
}
