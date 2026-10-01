package q3deps

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Kind is what sort of thing a map named.
type Kind string

const (
	// KindShader is a shader or texture name from a brush face or a patch.
	KindShader Kind = "shader"
	// KindModel is a model file named by an entity's `model` key.
	KindModel Kind = "model"
	// KindSound is a sound named by an entity's `noise` key.
	KindSound Kind = "sound"
	// KindMusic is a track named by worldspawn's `music` key.
	KindMusic Kind = "music"
	// KindFile is a file somebody named outright, by the path an engine would
	// look it up under. No map says it; a caller that packages one does.
	KindFile Kind = "file"
)

// Reference is one thing a map depends on.
type Reference struct {
	// Name is the path the engine will look for, with the `textures/` prefix a
	// face name implies already applied. Slash-separated, lower-cased.
	Name string `json:"name"`
	// Raw is exactly what the map said, before normalization, so a person
	// reading the report can find the line they wrote.
	Raw  string `json:"raw,omitempty"`
	Kind Kind   `json:"kind"`
	// From says where in the map it came from, in words: `worldspawn face`,
	// `patch`, `misc_model "model"`.
	From string `json:"from"`
	// Count is how many times it appears. A texture used on four hundred faces
	// is one dependency, and saying so keeps a report readable.
	Count int `json:"count"`
	// Note is a remark about the reference itself rather than about whether it
	// resolves — the doubled `textures/` prefix is the one that matters.
	Note string `json:"note,omitempty"`
	// RuntimeOnly marks a file the compiler never opens: an entity's `model2`,
	// `noise` or `music`. It is in a map source's report because a package that
	// leaves it out is incomplete, and it is not why a compile would fail.
	RuntimeOnly bool `json:"runtime_only,omitempty"`
}

// maxMapBytes bounds a map source. A Quake III `.map` is text; the largest
// released ones are a few megabytes, and this is generous by two orders of
// magnitude. A parser with no bound is a parser a crafted file can use to fill
// memory.
const maxMapBytes = 256 << 20

// ParseMap reads a Quake III map source and returns what it references, in the
// order first seen.
func ParseMap(mapPath string) ([]Reference, error) {
	file, err := os.Open(mapPath)
	if err != nil {
		return nil, fmt.Errorf("q3deps: reading the map: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("q3deps: reading the map: %w", err)
	}
	if info.Size() > maxMapBytes {
		return nil, fmt.Errorf("q3deps: %s is %d bytes, over the %d-byte limit for a map source",
			mapPath, info.Size(), int64(maxMapBytes))
	}
	return parseMap(bufio.NewReader(file))
}

// token is one lexical item, with whether it arrived quoted — which is the only
// way to tell a `brushDef3` face name from a legacy one.
type token struct {
	text   string
	quoted bool
}

// lex splits a map source into tokens, dropping `//` comments.
//
// It is a lexer rather than a line scanner because the format is not
// line-oriented in the places that matter: a patch's control points are laid
// out across lines, and a brush written by another editor may not break where
// Radiant does.
func lex(r io.Reader) ([]token, error) {
	reader := bufio.NewReader(r)
	var (
		tokens []token
		word   strings.Builder
	)
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, token{text: word.String()})
			word.Reset()
		}
	}
	for {
		c, _, err := reader.ReadRune()
		if err == io.EOF {
			flush()
			return tokens, nil
		}
		if err != nil {
			return nil, err
		}
		switch {
		case c == '/':
			next, _, peekErr := reader.ReadRune()
			if peekErr == nil && next == '/' {
				flush()
				for {
					c, _, err := reader.ReadRune()
					if err != nil || c == '\n' {
						break
					}
				}
				continue
			}
			if peekErr == nil {
				_ = reader.UnreadRune()
			}
			word.WriteRune(c)
		case c == '"':
			flush()
			var quoted strings.Builder
			for {
				c, _, err := reader.ReadRune()
				if err != nil || c == '"' {
					break
				}
				quoted.WriteRune(c)
			}
			tokens = append(tokens, token{text: quoted.String(), quoted: true})
		case c == '{' || c == '}' || c == '(' || c == ')':
			flush()
			tokens = append(tokens, token{text: string(c)})
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			flush()
		default:
			word.WriteRune(c)
		}
	}
}

// collector accumulates references, merging repeats.
type collector struct {
	order []string
	byKey map[string]*Reference
}

func newCollector() *collector { return &collector{byKey: map[string]*Reference{}} }

func (c *collector) add(ref Reference) {
	key := string(ref.Kind) + "\x00" + ref.Name
	if existing, seen := c.byKey[key]; seen {
		existing.Count += ref.Count
		return
	}
	copied := ref
	c.byKey[key] = &copied
	c.order = append(c.order, key)
}

func (c *collector) all() []Reference {
	out := make([]Reference, 0, len(c.order))
	for _, key := range c.order {
		out = append(out, *c.byKey[key])
	}
	return out
}

func parseMap(r io.Reader) ([]Reference, error) {
	tokens, err := lex(r)
	if err != nil {
		return nil, fmt.Errorf("q3deps: reading the map: %w", err)
	}
	found := newCollector()
	i := 0
	for i < len(tokens) {
		if tokens[i].text != "{" || tokens[i].quoted {
			i++
			continue
		}
		next, err := parseEntity(tokens, i+1, found)
		if err != nil {
			return nil, err
		}
		i = next
	}
	return found.all(), nil
}

// parseEntity reads one `{ … }` entity, starting just after its brace, and
// returns the index just past its closing brace.
func parseEntity(tokens []token, i int, found *collector) (int, error) {
	keys := map[string]string{}
	type block struct{ start, end int }
	var blocks []block
	for i < len(tokens) {
		t := tokens[i]
		switch {
		case t.text == "}" && !t.quoted:
			classname := keys["classname"]
			if classname == "" {
				classname = "entity"
			}
			for _, b := range blocks {
				parseBrush(tokens[b.start:b.end], classname, found)
			}
			addEntityFiles(keys, classname, found)
			return i + 1, nil
		case t.text == "{" && !t.quoted:
			depth, j := 1, i+1
			for j < len(tokens) && depth > 0 {
				if !tokens[j].quoted {
					switch tokens[j].text {
					case "{":
						depth++
					case "}":
						depth--
					}
				}
				j++
			}
			blocks = append(blocks, block{start: i + 1, end: j - 1})
			i = j
		case t.quoted && i+1 < len(tokens) && tokens[i+1].quoted:
			keys[strings.ToLower(t.text)] = tokens[i+1].text
			i += 2
		default:
			i++
		}
	}
	return i, fmt.Errorf("q3deps: the map ends inside an entity")
}

// addEntityFiles records the entity keys that name a file.
//
// Four keys, and no more, because these are the ones that were checked against
// real maps and against the game source: `model` (a `misc_model`, which the
// compiler bakes in), `model2` (a model the ENGINE draws on a brush entity —
// `G_SpawnString("model2")` in ioquake3's `g_mover.c` — and which the compiler
// never opens), `noise` and `music`. An entity key that names a file this does
// not know about is what the limit sentence about gamecode is for.
func addEntityFiles(keys map[string]string, classname string, found *collector) {
	names := []string{"model", "model2", "noise", "music"}
	kinds := map[string]Kind{"model": KindModel, "model2": KindModel, "noise": KindSound, "music": KindMusic}
	for _, key := range names {
		kind := kinds[key]
		value := strings.TrimSpace(keys[key])
		if value == "" || strings.HasPrefix(value, "*") {
			// `*3` is an inline brush model: it is inside the BSP, not a file.
			continue
		}
		found.add(Reference{
			Name:  normalizeFile(value),
			Raw:   value,
			Kind:  kind,
			From:  fmt.Sprintf("%s %q", classname, key),
			Count: 1,
			// `model` is a misc_model, which Q3Map2 opens and bakes in. The
			// other three are read by the game and the engine only.
			RuntimeOnly: key != "model",
		})
	}
}

// parseBrush reads one brush or patch block's tokens.
func parseBrush(tokens []token, classname string, found *collector) {
	if len(tokens) == 0 {
		return
	}
	switch strings.ToLower(tokens[0].text) {
	case "patchdef2", "patchdef3":
		// `patchDef2 { <shader> ( … ) ( … ) }`: the shader is the first token
		// inside the inner block. Measured: it is written the same way a face
		// name is, without the `textures/` prefix.
		for i := 1; i < len(tokens); i++ {
			if tokens[i].text == "{" && !tokens[i].quoted && i+1 < len(tokens) {
				addSurface(found, tokens[i+1].text, classname, "patch")
				return
			}
		}
	case "brushdef", "brushdef3":
		for _, name := range faceNames(tokens[1:]) {
			addSurface(found, name, classname, "face")
		}
	default:
		for _, name := range faceNames(tokens) {
			addSurface(found, name, classname, "face")
		}
	}
}

// faceNames reads the shader name out of each face in a brush.
//
// A face is three parenthesised groups and then its name — in the legacy
// format three points, in `brushDef3` a plane and a texture matrix. Counting
// groups rather than matching a shape is what makes one reader work for both.
func faceNames(tokens []token) []string {
	var (
		names  []string
		depth  int
		closed int
	)
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t.quoted {
			if closed > 0 {
				names = append(names, t.text)
				closed = 0
			}
			continue
		}
		switch t.text {
		case "(":
			depth++
		case ")":
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				closed++
			}
		case "{", "}":
			depth, closed = 0, 0
		default:
			if depth == 0 && closed > 0 {
				names = append(names, t.text)
				closed = 0
			}
		}
	}
	return names
}

// texturesPrefix is what Q3Map2 puts in front of every face and patch name.
const texturesPrefix = "textures/"

func addSurface(found *collector, raw, classname, where string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	cleaned := normalizeFile(raw)
	note := ""
	if strings.HasPrefix(cleaned, texturesPrefix) {
		// Measured: Q3Map2 prepends `textures/` unconditionally, so a name
		// written with it already produces `textures/textures/…` — which reads
		// like a missing file and is a doubled prefix.
		note = "the name already begins with `textures/`, and Q3Map2 adds it again: " +
			"a face or patch names a shader without that prefix"
	}
	found.add(Reference{
		Name:  texturesPrefix + cleaned,
		Raw:   raw,
		Kind:  KindShader,
		From:  classname + " " + where,
		Count: 1,
		Note:  note,
	})
}

// normalizeFile puts a name into the spelling the rest of this package uses:
// forward slashes, lower case, no leading separator.
//
// Lower case because Quake III's own filesystem is case-insensitive in
// practice — the maps were authored on Windows — and a report that listed
// `Textures/Aucom/Wall` and `textures/aucom/wall` as two dependencies would be
// a report nobody trusts.
func normalizeFile(name string) string {
	cleaned := strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimPrefix(cleaned, "/")
	return strings.ToLower(path.Clean(cleaned))
}
