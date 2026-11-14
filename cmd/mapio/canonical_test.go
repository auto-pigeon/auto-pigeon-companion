package main

// `NEW_434`: a map that came from Auto-Pigeon reaches a compiler only as a copy
// this program wrote from a parsed map (canonical.go); the Companion runs
// `canonical-map` on every such map before a build stages it. The attack classes are
// the ones `NEW_349` left open against `NEW_348`'s byte scanner: (a) fence
// textures and quote/comment desynchronisation hiding an entity from the guard,
// (b) face texture tokens naming files, Windows paths included, and (c) map keys
// naming files or compiler settings. (d) is the non-regression control: valid
// Quake 1, 2 and 3 maps compile to what they said.
//
// Two tokenizer models below stand in for the compilers: ericw's `parse_token`
// (`common/parser.cc`, fork release 1.4556) and Q3Map2's `GetToken`. A vector is
// shown to be real by what the ericw model reads in the RAW text — the bytes a
// build without this boundary hands qbsp — and shown closed by the boundary's
// refusal, or by both models reading the canonical copy alike.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- models

type modelToken struct {
	text   string
	quoted bool
}

// ericwTokens is ericw's parse_token over a whole file: bytes <= 32 are space
// and a NUL ends the text; `//` or `;` at a token's start comments to the end of
// the line; a quoted token keeps `\n \' \r \t \\ \b` pairs, and `\"` too unless
// a line break follows it; an unquoted token runs to the next byte <= 32.
func ericwTokens(text string) []modelToken {
	var tokens []modelToken
	i := 0
	for {
		for i < len(text) && text[i] <= 32 {
			if text[i] == 0 {
				return tokens
			}
			i++
		}
		if i >= len(text) {
			return tokens
		}
		if text[i] == ';' || (text[i] == '/' && i+1 < len(text) && text[i+1] == '/') {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		}
		var b strings.Builder
		if text[i] == '"' {
			i++
			for i < len(text) && text[i] != '"' && text[i] != 0 {
				if text[i] == '\\' && i+1 < len(text) {
					switch text[i+1] {
					case 'n', '\'', 'r', 't', '\\', 'b':
						b.WriteByte(text[i])
						i++
					case '"':
						if i+2 >= len(text) || (text[i+2] != '\r' && text[i+2] != '\n') {
							b.WriteByte(text[i])
							i++
						}
					}
				}
				b.WriteByte(text[i])
				i++
			}
			i++
			tokens = append(tokens, modelToken{b.String(), true})
			continue
		}
		for i < len(text) && text[i] > 32 {
			b.WriteByte(text[i])
			i++
		}
		tokens = append(tokens, modelToken{b.String(), false})
	}
}

// q3map2Tokens is Q3Map2's GetToken: bytes <= 32 are space; `//`, `;` and `#`
// at a token's start comment to the end of the line and `/* */` a span; a
// quoted token ends at the next `"` with no escape; an unquoted one runs to the
// next byte <= 32 or `;`.
func q3map2Tokens(text string) []modelToken {
	var tokens []modelToken
	i := 0
	for {
		for i < len(text) && text[i] <= 32 {
			i++
		}
		if i >= len(text) {
			return tokens
		}
		c := text[i]
		if c == ';' || c == '#' || (c == '/' && i+1 < len(text) && text[i+1] == '/') {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(text) && text[i+1] == '*' {
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return tokens
			}
			i += end + 4
			continue
		}
		if c == '"' {
			end := strings.IndexByte(text[i+1:], '"')
			if end < 0 {
				return tokens
			}
			tokens = append(tokens, modelToken{text[i+1 : i+1+end], true})
			i += end + 2
			continue
		}
		start := i
		for i < len(text) && text[i] > 32 && text[i] != ';' {
			i++
		}
		tokens = append(tokens, modelToken{text[start:i], false})
	}
}

// modelEntity is what qbsp's ParseEntity takes from a token stream: key/value
// pairs at entity level, and brushes skipped to the token that is exactly `}`.
type modelEntity struct {
	pairs    [][2]string
	textures []string
}

func (e modelEntity) get(key string) (string, bool) {
	for _, pair := range e.pairs {
		if strings.EqualFold(pair[0], key) {
			return pair[1], true
		}
	}
	return "", false
}

func ericwEntities(text string) []modelEntity {
	tokens := ericwTokens(text)
	var entities []modelEntity
	for i := 0; i < len(tokens); {
		if tokens[i].text != "{" {
			i++
			continue
		}
		i++
		var entity modelEntity
		for i < len(tokens) && tokens[i].text != "}" {
			if tokens[i].text == "{" && !tokens[i].quoted {
				i++
				for i < len(tokens) && tokens[i].text != "}" {
					if tokens[i].text == ")" && i+1 < len(tokens) && tokens[i+1].text != "(" {
						entity.textures = append(entity.textures, tokens[i+1].text)
					}
					i++
				}
				i++
				continue
			}
			if i+1 < len(tokens) {
				entity.pairs = append(entity.pairs, [2]string{tokens[i].text, tokens[i+1].text})
			}
			i += 2
		}
		i++
		entities = append(entities, entity)
	}
	return entities
}

// bothModelsAgree is the property every canonical copy must have.
func bothModelsAgree(t *testing.T, name string, text []byte) {
	t.Helper()
	if e, q := ericwTokens(string(text)), q3map2Tokens(string(text)); !reflect.DeepEqual(e, q) {
		t.Fatalf("%s: ericw and Q3Map2 read the canonical copy differently:\nericw %v\nq3map2 %v", name, e, q)
	}
}

func mustCanonical(t *testing.T, name, text string) []byte {
	t.Helper()
	out, _, err := canonicalMapText([]byte(text))
	if err != nil {
		t.Fatalf("%s: refused: %v", name, err)
	}
	bothModelsAgree(t, name, out)
	return out
}

func mustRefuse(t *testing.T, name, text, says string) {
	t.Helper()
	_, _, err := canonicalMapText([]byte(text))
	if err == nil {
		t.Fatalf("%s: was not refused", name)
	}
	if !strings.Contains(err.Error(), says) {
		t.Fatalf("%s: refused, but not for %q: %v", name, says, err)
	}
}

// ---------------------------------------------------------------- fixtures

func box(texture string) string {
	return "{\n" +
		"( -64 -64 -16 ) ( -64 -63 -16 ) ( -64 -64 -15 ) " + texture + " 0 0 0 1 1\n" +
		"( -64 -64 -16 ) ( -64 -64 -15 ) ( -63 -64 -16 ) " + texture + " 0 0 0 1 1\n" +
		"( -64 -64 -16 ) ( -63 -64 -16 ) ( -64 -63 -16 ) " + texture + " 0 0 0 1 1\n" +
		"( 64 64 16 ) ( 64 65 16 ) ( 65 64 16 ) " + texture + " 0 0 0 1 1\n" +
		"( 64 64 16 ) ( 65 64 16 ) ( 64 64 17 ) " + texture + " 0 0 0 1 1\n" +
		"( 64 64 16 ) ( 64 64 17 ) ( 64 65 16 ) " + texture + " 0 0 0 1 1\n" +
		"}\n"
}

func world(keys string, brushes ...string) string {
	return "{\n\"classname\" \"worldspawn\"\n" + keys + strings.Join(brushes, "") + "}\n"
}

const externalVictim = "{\n\"classname\" \"misc_external_map\"\n\"_external_map\" \"/home/victim/private.map\"\n}\n"

// The representative valid maps, one per game.
var (
	quake1Control = "// Game: Quake\n// Format: Standard\n" + world(
		"\"wad\" \"gfx/base.wad;C:\\quake\\id1\\gfx.wad\"\n\"message\" \"The Hall\\nof Fences\"\n\"_sunlight\" \"300\"\n\"_tb_textures\" \"textures\"\n",
		box("{fence"), box("*water0"), box("+0button"), box("sky1"),
		"{\n( 0 0 0 ) ( 0 1 0 ) ( 0 0 1 ) metal1_1 [ 0 1 0 0.5 ] [ 0 0 -1 -0.25 ] 0 0.333333333333 1\n"+
			"( 0 0 0 ) ( 0 0 1 ) ( 1 0 0 ) metal1_1 [ 1 0 0 0 ] [ 0 0 -1 0 ] 0 1 1\n"+
			"( 0 0 0 ) ( 1 0 0 ) ( 0 1 0 ) metal1_1 [ 1 0 0 0 ] [ 0 -1 0 0 ] 0 1 1\n"+
			"( 32 32 32 ) ( 32 33 32 ) ( 33 32 32 ) metal1_1 [ 1 0 0 0 ] [ 0 -1 0 0 ] 0 1 1\n}\n") +
		"{\n\"classname\" \"light\"\n\"origin\" \"0 0 32\"\n\"light\" \"300\"\n\"_color\" \"1 0.5 0.25\"\n}\n" +
		"{\n\"classname\" \"func_door\"\n\"target\" \"t1\"\n\"target\" \"t2\"\n\"path\" \"p1\"\n" + box("+0button") + "}\n"

	quake2Control = world("\"sky\" \"unit1_\"\n",
		"{\n( -64 -64 -16 ) ( -64 -63 -16 ) ( -64 -64 -15 ) e1u1/floor1_1 0 0 0 1 1 0 0 0\n"+
			"( -64 -64 -16 ) ( -64 -64 -15 ) ( -63 -64 -16 ) e1u1/floor1_1 0 0 0 1 1 0 0 0\n"+
			"( -64 -64 -16 ) ( -63 -64 -16 ) ( -64 -63 -16 ) e1u1/floor1_1 0 0 0 1 1 0 0 0\n"+
			"( 64 64 16 ) ( 64 65 16 ) ( 65 64 16 ) e1u1/sky1 0 0 0 1 1 0 4 0\n"+
			"( 64 64 16 ) ( 65 64 16 ) ( 64 64 17 ) e1u1/floor1_1 0 0 0 1 1 1 0 0\n"+
			"( 64 64 16 ) ( 64 64 17 ) ( 64 65 16 ) e1u1/floor1_1 0 0 0 1 1 0 0 2147483647\n}\n") +
		"{\n\"classname\" \"info_player_start\"\n\"origin\" \"0 0 24\"\n\"angle\" \"90\"\n}\n"

	quake3Control = world("\"_remap\" \"*;textures/base_wall/concrete\"\n\"_celshader\" \"cel/ink\"\n",
		"{\nbrushDef\n{\n"+
			"( -64 -64 -16 ) ( -64 -63 -16 ) ( -64 -64 -15 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) base_wall/concrete 0 0 0\n"+
			"( -64 -64 -16 ) ( -64 -64 -15 ) ( -63 -64 -16 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) base_wall/concrete 0 0 0\n"+
			"( -64 -64 -16 ) ( -63 -64 -16 ) ( -64 -63 -16 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) base_wall/concrete 0 0 0\n"+
			"( 64 64 16 ) ( 64 65 16 ) ( 65 64 16 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) base_wall/concrete 0 0 0\n"+
			"( 64 64 16 ) ( 65 64 16 ) ( 64 64 17 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) base_wall/concrete 0 0 0\n"+
			"( 64 64 16 ) ( 64 64 17 ) ( 64 65 16 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) base_wall/concrete 0 0 0\n"+
			"}\n}\n",
		"{\npatchDef2\n{\ncurve/pipe\n( 3 3 0 0 0 )\n(\n"+
			"( ( 0 0 0 0 0 ) ( 0 64 0 0 0.5 ) ( 0 128 0 0 1 ) )\n"+
			"( ( 64 0 0 0.5 0 ) ( 64 64 32 0.5 0.5 ) ( 64 128 0 0.5 1 ) )\n"+
			"( ( 128 0 0 1 0 ) ( 128 64 0 1 0.5 ) ( 128 128 0 1 1 ) )\n"+
			")\n}\n}\n",
		// Q3Map2 reads one brush syntax per file, so no classic brush here.
		"{\nbrushDef\n{\n"+
			"( -64 -64 -48 ) ( -64 -63 -48 ) ( -64 -64 -47 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) common/caulk\n"+
			"( -64 -64 -48 ) ( -64 -64 -47 ) ( -63 -64 -48 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) common/caulk\n"+
			"( -64 -64 -48 ) ( -63 -64 -48 ) ( -64 -63 -48 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) common/caulk\n"+
			"( 64 64 -32 ) ( 64 65 -32 ) ( 65 64 -32 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) common/caulk\n"+
			"( 64 64 -32 ) ( 65 64 -32 ) ( 64 64 -31 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) common/caulk\n"+
			"( 64 64 -32 ) ( 64 64 -31 ) ( 64 65 -32 ) ( ( 0.0078125 0 0 ) ( 0 0.0078125 0 ) ) common/caulk\n"+
			"}\n}\n") +
		"{\n\"classname\" \"misc_model\"\n\"model\" \"models/mapobjects/lamp.md3\"\n\"origin\" \"0 0 0\"\n\"_remap1\" \"lamp;textures/base_light/ceil1_4\"\n}\n"
)

// ---------------------------------------------------------------- (a)

// One `{fence` brush moved NEW_348's brace counter off by six, and every later
// entity out of its sight: the external map behind it was never refused.
func TestFenceTexturesCannotHideAnExternalMap(t *testing.T) {
	vector := world("", box("{fence")) + externalVictim

	// The vector is real: what qbsp reads from the raw bytes names the file.
	entities := ericwEntities(vector)
	if len(entities) != 2 {
		t.Fatalf("the ericw model read %d entities from the vector", len(entities))
	}
	if value, _ := entities[1].get("_external_map"); value != "/home/victim/private.map" {
		t.Fatalf("the vector does not reach qbsp as an external map: %v", entities[1].pairs)
	}

	mustRefuse(t, "fence then external map", vector, "_external_map")

	// Two fence brushes kept NEW_348 from confining worldspawn's own WADs.
	wads := world("\"wad\" \"/abs/probe.wad;../../x.wad;gfx/base.wad\"\n", box("{fence"), box("{fence"))
	out, changed, err := canonicalMapText([]byte(wads))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := ericwEntities(string(out))[0].get("wad"); value != "probe.wad;x.wad;base.wad" || len(changed) != 3 {
		t.Fatalf("worldspawn's WADs after two fence brushes: %q (%v)", value, changed)
	}
	if got := ericwEntities(string(out))[0].textures; len(got) != 12 || got[0] != "{fence" {
		t.Fatalf("the fence textures did not survive: %v", got)
	}
}

// ericw reads a backslash before a quote as an escape; Q3Map2 does not. A value
// ending in one opens a string to ericw that is closed to every other reader,
// and what ericw then sees as keys a guard never saw at all.
func TestAnEscapedQuoteCannotOpenAStringOnlyTheCompilerSees(t *testing.T) {
	vector := world("") + "{\n\"classname\" \"info_null\"\n\"message\" \"x\\\" // \" \"_external_map\" \"/home/victim/private.map\"\n}\n"

	if value, _ := ericwEntities(vector)[1].get("_external_map"); value != "/home/victim/private.map" {
		t.Fatalf("the vector does not reach qbsp as an external map: %v", ericwEntities(vector)[1].pairs)
	}
	for _, token := range q3map2Tokens(vector) {
		if token.text == "_external_map" {
			t.Fatal("the vector is not a desynchronisation: Q3Map2 sees the key too")
		}
	}
	mustRefuse(t, "escaped quote", vector, "backslash")
}

// ericw's `;` comment, and its `\"` before a line break, were missing from
// NEW_348's quote scan (NEW_349 F9). Neither can leave a string open now: the
// copy is regenerated, and an open string is a parse fault.
func TestNoQuoteIsLeftOpenAndNoTextLiesBeyondTheEnd(t *testing.T) {
	for name, text := range map[string]string{
		"a ; comment flipping quote parity": world("") + "; \"\n{\n\"classname\" \"info_null\"\n\"message\" \"never closed",
		"an open quote":                     world("\"message\" \"never closed\n"),
		"a backslash as the last byte":      world("") + "{\n\"classname\" \"info_null\"\n\"message\" \"ends in \\",
		"an unterminated entity":            "{\n\"classname\" \"worldspawn\"\n",
		"a brace closed before opened":      "}\n" + world(""),
		"a word between entities":           world("") + "junk\n" + world(""),
		"a ; inside a texture token":        world("", box("metal;x")),
		"a texture token starting with #":   world("", box("#water")),
		"Doom 3 grammar":                    world("", "{\nbrushDef3\n{\n}\n}\n"),
	} {
		_, _, err := canonicalMapText([]byte(text))
		if err == nil {
			t.Fatalf("%s: was not refused", name)
		}
	}

	// A map ending without its final newline, or in a comment, is complete and
	// is staged ending in one.
	for name, text := range map[string]string{
		"no final newline":   "{\n\"classname\" \"worldspawn\"\n}",
		"a trailing comment": "{\n\"classname\" \"worldspawn\"\n}\n// the end",
	} {
		out := mustCanonical(t, name, text)
		if string(out) != "{\n\"classname\" \"worldspawn\"\n}\n" {
			t.Fatalf("%s: staged %q", name, out)
		}
	}

	// A value cannot add a line, a NUL or a quote to the copy.
	for name, value := range map[string]string{"NUL": "a\x00b", "escape": "a\x1bb", "delete": "a\x7fb"} {
		mustRefuse(t, name, world("\"message\" \""+value+"\"\n"), "control character")
	}
	// A bare carriage return ends the line to the reader, so the string is open.
	mustRefuse(t, "carriage return", world("\"message\" \"a\rb\"\n"), "does not read as a map")
}

// Everything on one line is still structure: an injected `} {` starts an
// entity, and that entity is held to every rule.
func TestEntityLineInjectionIsReadAsAnEntity(t *testing.T) {
	vector := "{\n\"classname\" \"worldspawn\"\n\"message\" \"hi\" } { \"classname\" \"misc_external_map\" \"_external_map\" \"/home/victim/private.map\"\n}\n"
	mustRefuse(t, "one-line injection", vector, "_external_map")
}

// ---------------------------------------------------------------- (b)

// A face's texture token is a file name to the compiler: ericw's Quake 1 lookup
// strips only up to the last `/`, and Windows reads `\` and `C:` as path syntax
// (NEW_349 needs-validation; the decisive Windows step is not observed here).
func TestATextureTokenIsAPlainNameInsideTheGame(t *testing.T) {
	for _, texture := range []string{
		`C:\Users\Public\x.txt`, `..\..\x.txt`, `\\server\share\x`, `C:x`, `x\y`,
		"../../x", "/etc/passwd", "e1u1/../../../x", "e1u1//x", "./x", "x/", "x/*y",
		"a;b", "#x", "(x", "x)", "[x", "x]", "x}", "a{b",
		strings.Repeat("a", 64), "caf\xc3\xa9",
	} {
		_, _, err := canonicalMapText([]byte(world("", box(texture))))
		if err == nil {
			t.Fatalf("the texture %q was not refused", texture)
		}
	}
	// A Quake III shader name is a texture token too.
	mustRefuse(t, "patch texture", world("", "{\npatchDef2\n{\n../../x\n( 3 3 0 0 0 )\n(\n"+
		"( ( 0 0 0 0 0 ) ( 0 64 0 0 0.5 ) ( 0 128 0 0 1 ) )\n( ( 64 0 0 0.5 0 ) ( 64 64 32 0.5 0.5 ) ( 64 128 0 0.5 1 ) )\n"+
		"( ( 128 0 0 1 0 ) ( 128 64 0 1 0.5 ) ( 128 128 0 1 1 ) )\n)\n}\n}\n"), "texture name")

	for _, texture := range []string{"{fence", "*water0", "+0button", "+abutton", "sky1", "e1u1/floor1_1",
		"textures/base_wall/concrete", "__TB_empty", "-0fire", "a" + strings.Repeat("b", 62)} {
		out := mustCanonical(t, texture, world("", box(texture)))
		if got := ericwEntities(string(out))[0].textures; len(got) != 6 || got[0] != texture {
			t.Fatalf("the texture %q did not survive whole: %v", texture, got)
		}
	}
}

// ---------------------------------------------------------------- (c)

// ericw light applies every worldspawn key to its own settings, leading
// underscores removed, and opens `radlights` after that. The settings that name
// a file or a search path are refused; the lighting vocabulary stays open.
func TestWorldspawnCannotSetTheCompilersFiles(t *testing.T) {
	for _, key := range []string{"radlights", "_radlights", "__RadLights", "logfile", "_gamedir", "basedir", "_path"} {
		mustRefuse(t, key, world("\""+key+"\" \"/home/victim/secret.rad\"\n"), "worldspawn")
	}
	// Relative is a file too: light opens it against its working directory.
	mustRefuse(t, "relative radlights", world("\"_radlights\" \"lights.rad\"\n"), "worldspawn")

	out := mustCanonical(t, "lighting", world("\"_sunlight\" \"300\"\n\"_sunlight_mangle\" \"0 -90 0\"\n\"_dirt\" \"1\"\n\"_bounce\" \"1\"\n\"_radlights\" \"\"\n"))
	for _, key := range []string{"_sunlight", "_sunlight_mangle", "_dirt", "_bounce"} {
		if _, kept := ericwEntities(string(out))[0].get(key); !kept {
			t.Fatalf("the lighting key %s was dropped", key)
		}
	}
	// A key named like a setting on any other entity is that entity's own.
	out = mustCanonical(t, "train path", world("")+"{\n\"classname\" \"func_train\"\n\"path\" \"corner1\"\n}\n")
	if value, _ := ericwEntities(string(out))[1].get("path"); value != "corner1" {
		t.Fatalf("an entity's own path key was changed: %q", value)
	}
}

// An external map is a file the compiler reads as map text from wherever the
// value points; nothing Auto-Pigeon stages is one, so it is refused by name.
func TestAnExternalMapReferenceIsRefused(t *testing.T) {
	for _, value := range []string{"/home/victim/private.map", "../../secret.map", "prefab.map", `C:\x.map`} {
		mustRefuse(t, value, world("")+"{\n\"classname\" \"misc_external_map\"\n\"_external_map\" \""+value+"\"\n}\n", "_external_map")
	}
	mustRefuse(t, "on worldspawn", world("\"__EXTERNAL_MAP\" \"x.map\"\n"), "_external_map")
}

// Q3Map2 resolves these keys through its VFS, `../` included (NEW_349 F2).
func TestQuake3KeysNameFilesInsideTheGame(t *testing.T) {
	for _, pair := range [][2]string{
		{"model", "../../../etc/x.md3"}, {"model", "/abs/x.md3"}, {"model2", `C:\x.md3`},
		{"_remap", "*;../../x"}, {"_remap7", "../x;textures/ok"}, {"_celshader", "../cel"},
		{"_skin", "../x.skin"}, {"_indexmap", "/tmp/terrain.tga"}, {"alphamap", "a/../../b"},
		{"_shader", "../x"}, {"_flareshader", "c:flare"}, {"_foghull", "../fog"},
		{"targetShaderNewName", "../x"}, {"noise", "../../snd.wav"}, {"music", "/x.wav"},
	} {
		mustRefuse(t, pair[0]+"="+pair[1], world("")+"{\n\"classname\" \"misc_model\"\n\""+pair[0]+"\" \""+pair[1]+"\"\n}\n", pair[0])
	}
	out := mustCanonical(t, "valid quake3 keys", world("")+"{\n\"classname\" \"misc_model\"\n\"model\" \"models/x.md3\"\n"+
		"\"_remap\" \"*;textures/a/b\"\n\"_skin\" \"1\"\n\"noise\" \"*falling1.wav\"\n}\n{\n\"classname\" \"func_door\"\n\"model\" \"*1\"\n}\n")
	if value, _ := ericwEntities(string(out))[1].get("_remap"); value != "*;textures/a/b" {
		t.Fatalf("a valid remap was changed: %q", value)
	}
}

// A WAD entry keeps only its file name, a folder in front of it included
// (NEW_440: the build's WAD folder is flat, and a subpath is a path).
func TestConfiningAMapKeepsOnlyWADNamesInsideTheBuild(t *testing.T) {
	text := world(`"wad" "/home/victim/private.wad;..\..\outside\rel.wad;gfx/base.wad;C:\quake\id1\gfx.wad;//server/share/net.wad;metal.wad"`+"\n", box("sand"))
	out, changed, err := canonicalMapText([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := ericwEntities(string(out))[0].get("wad"); value != "private.wad;rel.wad;base.wad;gfx.wad;net.wad;metal.wad" {
		t.Fatalf("wad = %q", value)
	}
	if len(changed) != 5 {
		t.Fatalf("changed = %v, want the five entries that were paths", changed)
	}
	// An old editor's `"wad" "c:\quake\"` ends in a backslash; it names a
	// folder, which is dropped before the quoting rule sees it.
	out, _, err = canonicalMapText([]byte(world(`"wad" "c:\quake\"` + "\n")))
	if err != nil {
		t.Fatalf("an old editor's trailing-backslash WAD value was refused: %v", err)
	}
	if value, _ := ericwEntities(string(out))[0].get("wad"); value != "" {
		t.Fatalf("an old editor's folder entry became %q", value)
	}
}

// ---------------------------------------------------------------- (d)

// Valid maps of every game are compiled as what they said: the copy reads
// back as the same map, writing it again changes nothing, and both compilers'
// tokenizers read it alike.
func TestValidMapsOfEveryGameKeepTheirMeaning(t *testing.T) {
	for name, text := range map[string]string{"quake1": quake1Control, "quake2": quake2Control, "quake3": quake3Control} {
		out := mustCanonical(t, name, text)
		again := mustCanonical(t, name+" again", string(out))
		if string(again) != string(out) {
			t.Fatalf("%s: the canonical copy is not a fixed point:\n%s\n---\n%s", name, out, again)
		}
		before, after := ericwEntities(text), ericwEntities(string(out))
		if len(before) != len(after) {
			t.Fatalf("%s: %d entities became %d", name, len(before), len(after))
		}
		for index := range before {
			if !reflect.DeepEqual(before[index].textures, after[index].textures) {
				t.Fatalf("%s: entity %d's textures changed:\n%v\n%v", name, index, before[index].textures, after[index].textures)
			}
			want := before[index].pairs
			if index == 0 && name == "quake1" {
				want = append([][2]string(nil), want...)
				want[1][1] = "base.wad;gfx.wad"
			}
			if !reflect.DeepEqual(want, after[index].pairs) {
				t.Fatalf("%s: entity %d's keys changed:\n%v\n%v", name, index, want, after[index].pairs)
			}
		}
	}
	// Numbers read back to the same doubles: 0.333333333333 is not rounded to 1/3.
	if out := mustCanonical(t, "numbers", quake1Control); !strings.Contains(string(out), " 0.333333333333 ") {
		t.Fatalf("a number was rewritten:\n%s", out)
	}
	if out := mustCanonical(t, "q2 flags", quake2Control); !strings.Contains(string(out), " 0 0 2147483647\n") {
		t.Fatalf("a Quake II face value was rewritten:\n%s", out)
	}
}

// ericw keeps the sign of a zero, and the plane lump of a BSP changes with it
// (a decompiled Quake 1 map, fork release 1.4556): `-0` is written as `-0`.
func TestTheSignOfAZeroIsKept(t *testing.T) {
	text := world("", strings.Replace(box("sand"), "( -64 -64 -16 ) ( -64 -63 -16 )", "( -64 -0 -16 ) ( -64 -63 -0 )", 1))
	out := mustCanonical(t, "negative zero", text)
	if !strings.Contains(string(out), "( -64 -0 -16 ) ( -64 -63 -0 )") {
		t.Fatalf("a negative zero lost its sign:\n%s", out)
	}
	if again := mustCanonical(t, "again", string(out)); string(again) != string(out) {
		t.Fatal("the copy is not a fixed point")
	}
}

func TestBoundsAreRefusalsNotTruncations(t *testing.T) {
	faces := strings.Repeat("( 0 0 0 ) ( 0 1 0 ) ( 0 0 1 ) sand 0 0 0 1 1\n", maxBrushFaces+1)
	mustRefuse(t, "faces", world("", "{\n"+faces+"}\n"), "faces")
	mustRefuse(t, "value", world("\"message\" \""+strings.Repeat("a", maxValueBytes+1)+"\"\n"), "more than")
	mustRefuse(t, "key", world("\""+strings.Repeat("k", maxKeyBytes+1)+"\" \"v\"\n"), "more than")
	mustRefuse(t, "magnitude", world("", strings.Replace(box("sand"), "-64 -64 -16", "-64 -64 1e300", 1)), "larger")
	mustRefuse(t, "empty key", world("\"\" \"v\"\n"), "empty key")
}

// ---------------------------------------------------------------- the command

// canonical-map is the contract the Companion drives: a new file or a terminal
// record, a link never followed in either direction, and a refusal that names
// where and never echoes the value.
func TestTheCanonicalMapCommandWritesANewFileOrRefuses(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	call := func(args ...string) (int, string, string) {
		var stdout, stderr strings.Builder
		code := run(context.Background(), append([]string{"canonical-map"}, args...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}

	source := write("e1m1.map", world(`"wad" "/home/victim/a.wad;gfx.wad"`+"\n", box("{fence")))
	output := filepath.Join(dir, "out.map")
	code, stdout, stderr := call("--input", source, "--output", output, "--json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var result canonicalResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Output != output ||
		!reflect.DeepEqual(result.ReducedWADs, []string{"/home/victim/a.wad"}) {
		t.Fatalf("result %s (%v)", stdout, err)
	}
	written, _ := os.ReadFile(output)
	if value, _ := ericwEntities(string(written))[0].get("wad"); value != "a.wad;gfx.wad" {
		t.Fatalf("the copy's wad is %q", value)
	}

	// The output must be new: a second run onto it is refused, and so is one
	// onto a planted link, whose target is never created.
	if code, _, stderr = call("--input", source, "--output", output); code != exitFailed || !strings.Contains(stderr, TerminalMarker) {
		t.Fatalf("an existing output was written over: exit %d %s", code, stderr)
	}
	if runtime.GOOS != "windows" {
		target := filepath.Join(dir, "elsewhere.map")
		planted := filepath.Join(dir, "planted.map")
		if err := os.Symlink(target, planted); err != nil {
			t.Fatal(err)
		}
		if code, _, _ = call("--input", source, "--output", planted); code != exitFailed {
			t.Fatal("the copy was written through a planted link")
		}
		if _, err := os.Stat(target); err == nil {
			t.Fatal("the planted link's target was created")
		}
		linked := filepath.Join(dir, "linked.map")
		if err := os.Symlink(source, linked); err != nil {
			t.Fatal(err)
		}
		if code, _, _ = call("--input", linked, "--output", filepath.Join(dir, "x.map")); code != exitFailed {
			t.Fatal("an input that is a link was followed")
		}
	}

	// A refusal is a terminal record naming the key and the line, not the value.
	refused := write("bad.map", world("\"_radlights\" \"/home/victim/secret.rad\"\n"))
	code, _, stderr = call("--input", refused, "--output", filepath.Join(dir, "bad-out.map"))
	if code != exitFailed || !strings.Contains(stderr, `"reason":"map_refused"`) || !strings.Contains(stderr, "_radlights") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.Contains(stderr, "secret.rad") {
		t.Fatalf("the refusal echoed the value: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad-out.map")); err == nil {
		t.Fatal("a refused map left a file")
	}
	// That refusal is not an external map file, and says nothing narrower.
	if strings.Contains(stderr, `"refusal"`) {
		t.Fatalf("a refusal that is not an external map file named one: %s", stderr)
	}
	// An external map file keeps the reason and narrows it (NEW_515): the
	// editor is told exactly what to remove.
	external := write("external.map", world("")+"{\n\"classname\" \"misc_external_map\"\n\"_external_map\" \"nothere/missing_prefab.map\"\n}\n")
	code, _, stderr = call("--input", external, "--output", filepath.Join(dir, "external-out.map"))
	if code != exitFailed || !strings.Contains(stderr, `"reason":"map_refused"`) || !strings.Contains(stderr, `"refusal":"external_map"`) ||
		strings.Contains(stderr, "missing_prefab") {
		t.Fatalf("an external map file: exit %d: %s", code, stderr)
	}

	if code, _, _ = call("--input", source); code != exitUsage {
		t.Fatalf("a call with no output exited %d", code)
	}
}

// The protocol says which rule canonical-map enforces, so a Companion can
// require the one it was tested with.
func TestTheProtocolNamesTheCompileBoundary(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := run(context.Background(), []string{"protocol", "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var document protocolDocument
	if err := json.Unmarshal([]byte(stdout.String()), &document); err != nil {
		t.Fatal(err)
	}
	if document.Protocol != "1.1" || document.Capabilities.CompileBoundary != CompileBoundary {
		t.Fatalf("protocol %q, compile boundary %q", document.Protocol, document.Capabilities.CompileBoundary)
	}
}
