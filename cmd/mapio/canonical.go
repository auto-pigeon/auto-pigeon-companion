package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-libraries/go/mapio/mapfile"
	"github.com/auto-pigeon/auto-pigeon-libraries/go/mapio/quake3map"
)

// canonical-map is the Companion's compile boundary (`NEW_434`): a map that came
// from Auto-Pigeon reaches a compiler only as a fresh copy this program wrote
// from a parsed map. It lives here, not in the Companion, because only this
// program links go/mapio (docs/agent/aucom.extractor-acquisition.md): the
// Companion runs it on every such map before a build stages it
// (internal/build/mapconfine.go), and refuses the map when it cannot.
//
//	auto-pigeon-mapio canonical-map --input <file.map> --output <new file> [--json]
//
// # Why
//
// A map revision from AUB is written by whoever may save that map, and the
// Companion compiles it on somebody else's machine, with their files; a hosted
// game then uploads the BSP, which other readers of the map can download. A
// map's text can name files the compiler opens, and can set the compiler's own
// options:
//
//   - worldspawn's `wad`/`_wad` list texture archives; ericw qbsp opens an
//     absolute entry directly and joins a relative one onto each `-wadpath`, so
//     `../../` climbs out. A `misc_external_map`'s `_external_map` is a whole
//     map file read as map text (measured with fork release 1.4533, `NEW_342`
//     lead `aucom/job/map-controlled-compiler-paths-unsanitised`, `NEW_348`);
//   - ericw light applies EVERY worldspawn key to its own settings, leading
//     underscores stripped (`LoadEntities` → `set_setting(…, source::MAP)`), and
//     reads `radlights` as a file after that (`light_postinitialize`), so a map
//     names a file the compiler opens (`NEW_349` deferred unit);
//   - Q3Map2 resolves `model`, `model2`, `_remap*`, `_celshader`, `_skin`,
//     `_indexmap` and the shader keys through its VFS, `../` included
//     (`NEW_349` F2);
//   - a face's texture token is a file name too: ericw's Quake 1 lookup strips
//     only up to the last `/`, so a Windows token (`C:\…`, `..\..\…`) reaches
//     the loose-file search whole (`NEW_349` needs-validation, Windows).
//
// `NEW_348`'s answer was a byte scanner over the original text. It counted
// braces to find entities, and a Quake fence texture — a face token beginning
// with `{` — moved every later entity out of its sight, so the refusals it
// carried never ran (`NEW_349` N3). It modelled ericw's `//` comments and not
// its `;` ones, so the quote it guarded could still be open (`NEW_349` F9). A
// scanner that guesses at a grammar the compiler reads differently is the bug;
// `NEW_434` replaced it with this.
//
// # What this does
//
// The map is PARSED by `go/mapio/quake3map`, the Go port of Q3Map2's
// tokenizer and the reader `q3map-to-apmap` uses: it reads classic, Valve 220,
// `brushDef` and `patchDef2` brushes and the Quake 2 face tail, so it reads
// every map a Quake 1, 2 or 3 pipeline here compiles. (`go/mapio/mapfile`'s own
// parser cannot be used: it splits a fence texture `{fence` into a brace and a
// word and refuses the face.) Any fault refuses the whole map.
//
// Every token is then VALIDATED against a rule under which ericw's tokenizer
// and Q3Map2's read the same thing — the two differ on `;` (Q3Map2 ends an
// unquoted word there, ericw does not), on `#` (a comment only to Q3Map2) and on
// backslashes in quotes (an escape only to ericw) — and against the key policy
// below. Counts and lengths are bounded.
//
// The map is then SERIALIZED by writeCanonicalMap — `mapfile.Write`'s layout
// and `mapfile.FormatNumber`'s spelling, and the one piece of format code in
// this wrapper, because go/mapio has no writer for a parsed Quake III tree and
// its own writer loses the sign of a zero, which ericw keeps — into a NEW file,
// created exclusively; the Companion names one in the build's own input folder. Comments and
// whitespace are not carried; numbers are written as the shortest decimal that
// reads back to the same double, a negative zero as `-0`. The copy is parsed again and must be the same
// map, or nothing is compiled. The source file is only read: the user's
// original and AUB's document are never written.
//
// The key policy, where a key matches case-insensitively with leading
// underscores removed (ericw's own rule, and a superset of Q3Map2's):
//
//   - `_external_map`, on any entity: refused when not empty. Nothing
//     Auto-Pigeon stages is one;
//   - on worldspawn, `radlights`, `logfile`, `gamedir`, `basedir` and `path` —
//     the light settings that are files or search paths — and qbsp's
//     `wadpath`, `xwadpath`, `aliasdef`, `texturedefs` and `add`, which qbsp
//     does not take from a map today: refused when not empty. The rest of the
//     lighting vocabulary is open by design;
//   - `wad`/`_wad`: every entry is written as one plain `.wad` file name — what
//     follows its last `/`, `\` or `:`, so a drive (`C:\…`, the drive-relative
//     `C:name.wad`), a UNC share, a scheme, a stream (`host:name.wad`) and a
//     folder in front (`gfx/`, `..\`) all fall away — which is how AUB names a
//     map's WADs and how the content root is laid out (the Companion's
//     declaredwads.go). An entry naming only a folder (`c:\quake\`, `..`) opens
//     nothing and is dropped. A name that is still not a plain `.wad` file name
//     both POSIX and Windows open as written — a Windows device name, another
//     extension, a leading dot, a character Windows cannot hold or one outside
//     ASCII, over 255 bytes — refuses the map (NEW_440). An archive the build
//     does not hold simply is not found, as it would not have been on another
//     machine;
//   - the keys that name a file or shader (`model`, `model2`, `noise`, `music`,
//     `skin`, `indexmap`, `alphamap`, `shader`, `celshader`, `flareshader`,
//     `foghull`, `targetshadernewname`, and both sides of every `remap*`):
//     refused when absolute, carrying a drive, a scheme or a backslash, or
//     holding a `..` segment;
//   - every other key and value is kept exactly, in order, repeated keys
//     included. Nothing is dropped.
//
// A file the user pointed at on their own disk is not sent here: the Companion
// stages it exactly as it is — their own map naming their own files is not this
// case. A compiler's arguments come from its profile and stage, never from the
// map (the Companion builds an argument vector; no shell is involved), and this
// changes only what the map SAYS.

// Bounds. A map over one is refused rather than compiled unchecked.
const (
	maxCanonicalMap             = 128 << 20
	maxMapEntities              = 1 << 17
	maxMapBrushes               = 1 << 20 // brushes and patches, all entities together
	maxBrushFaces               = 1024    // apmap.MaxQuake3BrushFaces's figure
	maxEntityProperties         = 4096
	maxKeyBytes                 = 1024
	maxValueBytes               = 4096
	maxTextureBytes             = 63
	maxMagnitude        float64 = 1 << 53 // integers stay exact, and spelled as integers
)

// refusalExternalMap is the terminal record's `refusal` for errExternalMap.
const refusalExternalMap = "external_map"

// errExternalMap is the refusal; the sentence names the key and what to do.
var errExternalMap = errors.New("the map names an external map file (`_external_map`), which a map from Auto-Pigeon " +
	"may not do: the compiler would read that file from this computer. Remove the misc_external_map entity, " +
	"or merge its brushes into the map")

// mapRefusal is a map from Auto-Pigeon this build will not compile. It names
// where, never what: the value may be somebody's payload, and a build log is
// copied into bug reports.
type mapRefusal struct {
	entity int // -1 when the refusal is not about one entity
	line   int
	what   string
}

func (r *mapRefusal) Error() string {
	at := ""
	switch {
	case r.entity >= 0 && r.line > 0:
		at = fmt.Sprintf(" (entity %d, line %d)", r.entity, r.line)
	case r.line > 0:
		at = fmt.Sprintf(" (line %d)", r.line)
	case r.entity >= 0:
		at = fmt.Sprintf(" (entity %d)", r.entity)
	}
	return "a map from Auto-Pigeon is compiled only as a copy the Companion can read back exactly, and this one " +
		r.what + at + ". Save the map again from the editor, or correct it there"
}

func refuse(entity, line int, format string, args ...any) error {
	return &mapRefusal{entity: entity, line: line, what: fmt.Sprintf(format, args...)}
}

// canonicalMapText is the rule over map text: parse, validate, apply the key
// policy, serialize, and read the copy back. It returns the copy and the WAD
// entries it reduced to their file names.
func canonicalMapText(text []byte) ([]byte, []string, error) {
	text = bytes.TrimPrefix(text, []byte("\xEF\xBB\xBF"))
	parsed, err := quake3map.Parse(context.Background(), string(text))
	if err != nil {
		return nil, nil, err
	}
	if len(parsed.Errors) > 0 {
		first := parsed.Errors[0]
		return nil, nil, refuse(first.EntityIndex, first.Line, "does not read as a map (%s at column %d)",
			first.Code, first.Column)
	}
	changed, err := applyMapPolicy(parsed)
	if err != nil {
		return nil, nil, err
	}
	out, err := writeCanonicalMap(parsed.Entities)
	if err != nil {
		return nil, nil, err
	}
	again, err := quake3map.Parse(context.Background(), string(out))
	if err != nil {
		return nil, nil, err
	}
	if len(again.Errors) > 0 || !sameMap(parsed.Entities, again.Entities) {
		return nil, nil, refuse(-1, 0, "did not read back as the same map once written")
	}
	// sameMap compares numbers with ==, to which -0 and 0 are one; writing the
	// copy again from what it reads back must give the same bytes, signs
	// included.
	if rewritten, err := writeCanonicalMap(again.Entities); err != nil || !bytes.Equal(rewritten, out) {
		return nil, nil, refuse(-1, 0, "did not write back to the same bytes once read")
	}
	return out, changed, nil
}

// applyMapPolicy bounds the map, validates every token and applies the key
// policy in place, returning the WAD entries it reduced.
func applyMapPolicy(parsed *quake3map.Map) ([]string, error) {
	if len(parsed.Entities) > maxMapEntities {
		return nil, refuse(-1, 0, "has %d entities, more than %d", len(parsed.Entities), maxMapEntities)
	}
	var changed []string
	brushes := 0
	for index, entity := range parsed.Entities {
		properties := 0
		for position, item := range entity.Content {
			switch item := item.(type) {
			case quake3map.Property:
				properties++
				if properties > maxEntityProperties {
					return nil, refuse(index, item.Line, "has more than %d key/value pairs in one entity", maxEntityProperties)
				}
				kept, reduced, err := propertyPolicy(index, item)
				if err != nil {
					return nil, err
				}
				entity.Content[position] = kept
				changed = append(changed, reduced...)
			case *quake3map.Brush:
				brushes++
				if len(item.Faces) > maxBrushFaces {
					return nil, refuse(index, item.Line, "has a brush of %d faces, more than %d", len(item.Faces), maxBrushFaces)
				}
				for _, face := range item.Faces {
					if err := faceProblem(index, face); err != nil {
						return nil, err
					}
				}
			case *quake3map.Patch:
				brushes++
				if reason := textureProblem(item.Texture); reason != "" {
					return nil, refuse(index, item.Line, "has a patch whose texture name %s", reason)
				}
				numbers := append([]float64(nil), item.Tail...)
				for _, column := range item.ControlPoints {
					for _, point := range column {
						numbers = append(numbers, point[:]...)
					}
				}
				if !boundedNumbers(numbers) {
					return nil, refuse(index, item.Line, "has a patch with a number larger than %g", maxMagnitude)
				}
			default:
				return nil, refuse(index, 0, "holds a block this build does not know (%T)", item)
			}
			if brushes > maxMapBrushes {
				return nil, refuse(-1, 0, "has more than %d brushes and patches", maxMapBrushes)
			}
		}
	}
	return changed, nil
}

func faceProblem(entity int, face quake3map.Face) error {
	if reason := textureProblem(face.Texture); reason != "" {
		return refuse(entity, face.Line, "has a face whose texture name %s", reason)
	}
	numbers := append([]float64(nil), face.Tail...)
	for _, point := range face.Points {
		numbers = append(numbers, point[:]...)
	}
	switch {
	case face.Classic != nil:
		numbers = append(numbers, face.Classic.Shift[0], face.Classic.Shift[1], face.Classic.Rotation,
			face.Classic.Scale[0], face.Classic.Scale[1])
	case face.Valve != nil:
		numbers = append(numbers, face.Valve.U[:]...)
		numbers = append(numbers, face.Valve.V[:]...)
		numbers = append(numbers, face.Valve.Rotation, face.Valve.Scale[0], face.Valve.Scale[1])
	case face.Matrix != nil:
		numbers = append(numbers, face.Matrix[0][:]...)
		numbers = append(numbers, face.Matrix[1][:]...)
	}
	if !boundedNumbers(numbers) {
		return refuse(entity, face.Line, "has a face with a number larger than %g", maxMagnitude)
	}
	return nil
}

func boundedNumbers(values []float64) bool {
	for _, value := range values {
		if value > maxMagnitude || value < -maxMagnitude {
			return false
		}
	}
	return true
}

// textureProblem says why a face or patch texture token is not one both
// compilers read as the same relative name, or "" when it is. The rule is the
// Quake III writer's (apmap `quake3ShaderToken`) with two Quake 1 allowances: a
// leading `{` (a fence texture) and no `textures/` prefix to remove.
func textureProblem(texture string) string {
	switch {
	case texture == "":
		return "is empty"
	case len(texture) > maxTextureBytes:
		return fmt.Sprintf("is %d bytes, more than %d", len(texture), maxTextureBytes)
	case texture[0] == '#':
		return "begins with #, which Quake III's compiler reads as a comment"
	}
	for index := 0; index < len(texture); index++ {
		c := texture[index]
		switch {
		case c <= 0x20 || c >= 0x7f:
			return "holds whitespace, a control character or a non-ASCII character"
		case strings.IndexByte("\"\\;:()[]}", c) >= 0:
			return fmt.Sprintf("holds %q", c)
		case c == '{' && index > 0:
			return "holds { other than as a fence texture's first character"
		}
	}
	if strings.Contains(texture, "/*") {
		return "holds a comment opener"
	}
	if pathProblem(texture, true) != "" {
		return "is not a relative name inside the game's folders"
	}
	return ""
}

// pathProblem says why a name a compiler resolves as a file is not one inside
// the folders it was given, or "" when it is. strict also refuses an empty
// segment, a `.` segment and a trailing `/`, as a texture name never has one.
func pathProblem(name string, strict bool) string {
	switch {
	case strings.ContainsAny(name, `\:`):
		return "it holds a backslash, a drive or a scheme"
	case strings.HasPrefix(name, "/"):
		return "it is absolute"
	case strict && strings.HasSuffix(name, "/"):
		return "it ends with /"
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." || (strict && (segment == "." || segment == "")) {
			return "it climbs out with .."
		}
	}
	return ""
}

// keyName is how a key is matched: case-insensitive, leading underscores
// removed. ericw's `find_setting` strips them all; Q3Map2 compares keys
// without case.
func keyName(key string) string {
	return strings.ToLower(strings.TrimLeft(key, "_"))
}

// worldSettingFiles are the ericw settings a worldspawn key could set that are
// files or search paths: light's (`common_settings` and `light_settings`),
// which light applies from worldspawn today, and qbsp's (`wadpath`,
// `xwadpath`, `aliasdef`, `texturedefs`, `add`), which qbsp 1.4556 does not
// apply from a map but would be live the day it did (NEW_436, NEW_440). Every
// other setting a map can set is lighting, and stays open.
var worldSettingFiles = map[string]bool{
	"radlights": true, "logfile": true, "gamedir": true, "basedir": true, "path": true,
	"wadpath": true, "xwadpath": true, "aliasdef": true, "texturedefs": true, "add": true,
}

// pathKeys name a file or a shader a compiler or the engine resolves through
// its search paths.
var pathKeys = map[string]bool{
	"model": true, "model2": true, "noise": true, "music": true, "skin": true,
	"indexmap": true, "alphamap": true, "shader": true, "celshader": true,
	"flareshader": true, "foghull": true, "targetshadernewname": true,
}

// propertyPolicy validates one key/value pair and applies the key policy,
// returning the pair to write and any WAD entries it reduced.
func propertyPolicy(entity int, property quake3map.Property) (quake3map.Property, []string, error) {
	name := keyName(property.Key)
	var reduced []string
	switch {
	case name == "external_map":
		if strings.TrimSpace(property.Value) != "" {
			return property, nil, errExternalMap
		}
	case entity == 0 && worldSettingFiles[name]:
		if strings.TrimSpace(property.Value) != "" {
			return property, nil, refuse(entity, property.Line,
				"sets the compiler's %q on worldspawn, which names a file on this computer; remove the key", property.Key)
		}
	case name == "wad":
		var err error
		if property.Value, reduced, err = confineWADList(entity, property.Line, property.Value); err != nil {
			return property, nil, err
		}
	case pathKeys[name]:
		if reason := pathProblem(property.Value, false); reason != "" {
			return property, nil, refuse(entity, property.Line, "names a file with %q, and %s", property.Key, reason)
		}
	case strings.HasPrefix(name, "remap"):
		for _, side := range strings.Split(property.Value, ";") {
			if reason := pathProblem(strings.TrimSpace(side), false); reason != "" {
				return property, nil, refuse(entity, property.Line, "remaps a shader with %q, and %s", property.Key, reason)
			}
		}
	}
	for _, check := range []struct {
		what, text string
		limit      int
	}{{"key", property.Key, maxKeyBytes}, {"value", property.Value, maxValueBytes}} {
		if reason := quotedProblem(check.text, check.limit); reason != "" {
			return property, nil, refuse(entity, property.Line, "has a %s that %s", check.what, reason)
		}
	}
	if property.Key == "" {
		return property, nil, refuse(entity, property.Line, "has an empty key")
	}
	return property, reduced, nil
}

// quotedProblem says why a key or value cannot be written in quotes that both
// compilers close at the same place, or "" when it can. The reader never yields
// a `"` or a line break inside one; ericw reads a backslash before the closing
// quote as escaping it, so a string may not end in one.
func quotedProblem(text string, limit int) string {
	if len(text) > limit {
		return fmt.Sprintf("is %d bytes, more than %d", len(text), limit)
	}
	for index := 0; index < len(text); index++ {
		if c := text[index]; c < 0x20 || c == 0x7f || c == '"' {
			return "holds a control character or a quote"
		}
	}
	if strings.HasSuffix(text, `\`) {
		return "ends with a backslash, which one compiler reads as escaping the closing quote"
	}
	return ""
}

// maxWADNameBytes bounds one WAD file name: the longest name a file system
// here holds.
const maxWADNameBytes = 255

// confineWADList writes each `;`-separated entry as one plain `.wad` file name
// (NEW_440), returning the list to write and the entries it changed. An entry
// keeps what follows its last `/`, `\` or `:` — the file name whichever system
// wrote it, a drive-relative `C:name.wad` and a stream `host:name.wad`
// included; an entry naming a folder (`c:\quake\`, `..`) opens nothing and is
// dropped; a name that is still not a plain `.wad` file name refuses the map.
func confineWADList(entity, line int, list string) (string, []string, error) {
	var kept, changed []string
	for _, entry := range strings.Split(list, ";") {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		name := strings.TrimSpace(trimmed[strings.LastIndexAny(trimmed, `/\:`)+1:])
		if name == "" || name == "." || name == ".." {
			changed = append(changed, trimmed)
			continue
		}
		if reason := wadNameProblem(name); reason != "" {
			return "", nil, refuse(entity, line, "names a texture WAD that %s; a WAD is named by one plain file name "+
				"ending in .wad, which is how the build lays the map's WADs out", reason)
		}
		if name != trimmed {
			changed = append(changed, trimmed)
		}
		kept = append(kept, name)
	}
	return strings.Join(kept, ";"), changed, nil
}

// wadNameProblem says why name is not one plain `.wad` file name that a POSIX
// and a Windows qbsp both open, spelled as written, directly inside the folder
// they join it to — or "" when it is. It holds no separator by construction.
// Windows respells or redirects what this refuses: a reserved device name
// (with any extension), a trailing dot or space (dropped; ruled out by the
// `.wad` ending), and the characters it cannot hold in a name — the
// separators among them, refused here again rather than trusted to the split.
func wadNameProblem(name string) string {
	switch {
	case len(name) > maxWADNameBytes:
		return fmt.Sprintf("is %d bytes, more than %d", len(name), maxWADNameBytes)
	case name[0] == '.':
		return "begins with a dot"
	}
	for index := 0; index < len(name); index++ {
		if c := name[index]; c < 0x20 || c >= 0x7f || strings.IndexByte(`<>:"/\|?*`, c) >= 0 {
			return "holds a character outside plain ASCII or one Windows cannot hold in a file name"
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), ".wad") {
		return "is not a .wad file"
	}
	stem := strings.ToUpper(strings.TrimRight(name[:strings.IndexByte(name, '.')], " "))
	if windowsDeviceNames[stem] || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) &&
		stem[3] >= '0' && stem[3] <= '9') {
		return "is a name Windows opens as a device"
	}
	return ""
}

// windowsDeviceNames are opened by Windows as a device in any folder, whatever
// extension follows them; COM0-9 and LPT0-9 are matched in wadNameProblem.
var windowsDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true, "CLOCK$": true,
}

// writeCanonicalMap serializes parsed entities, in the layout `mapfile.Write`
// uses — one key/value pair or face per line, `{` and `}` on lines of their own
// — with every number spelled by `mapfile.FormatNumber` except negative zero.
//
// It is not `mapfile.Write` itself for two measured reasons. That writer holds
// no `brushDef` or `patchDef2`, and its number spelling writes `-0` as `0`;
// ericw keeps the sign, and on a decompiled Quake 1 map (29,897 `-0` tokens)
// the plane lump of the BSP changed with it (fork release 1.4556, NEW_434), so a
// map is written here with the sign it had.
func writeCanonicalMap(entities []*quake3map.Entity) ([]byte, error) {
	var out bytes.Buffer
	for _, entity := range entities {
		if err := writeEntity(&out, entity); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

// spell is a number as the copy writes it: the shortest decimal that reads
// back to the same double, and the sign of a zero kept.
func spell(values ...float64) string {
	spelled := make([]string, len(values))
	for index, value := range values {
		if value == 0 && math.Signbit(value) {
			spelled[index] = "-0"
			continue
		}
		spelled[index] = mapfile.FormatNumber(value)
	}
	return strings.Join(spelled, " ")
}

func writeEntity(out *bytes.Buffer, entity *quake3map.Entity) error {
	out.WriteString("{\n")
	for _, item := range entity.Content {
		switch item := item.(type) {
		case quake3map.Property:
			if quotedProblem(item.Key, maxKeyBytes) != "" || quotedProblem(item.Value, maxValueBytes) != "" {
				return refuse(entity.Index, item.Line, "has a key/value pair that cannot be written")
			}
			out.WriteString(`"` + item.Key + `" "` + item.Value + "\"\n")
		case *quake3map.Brush:
			if item.Syntax == quake3map.SyntaxBrushPrimitives {
				out.WriteString("{\nbrushDef\n{\n")
			} else {
				out.WriteString("{\n")
			}
			for _, face := range item.Faces {
				writeFace(out, face)
			}
			if item.Syntax == quake3map.SyntaxBrushPrimitives {
				out.WriteString("}\n")
			}
			out.WriteString("}\n")
		case *quake3map.Patch:
			fmt.Fprintf(out, "{\npatchDef2\n{\n%s\n( %s )\n(\n", item.Texture,
				spell(append([]float64{float64(item.Width), float64(item.Height)}, item.Tail...)...))
			for _, column := range item.ControlPoints {
				out.WriteString("(")
				for _, point := range column {
					out.WriteString(" ( " + spell(point[:]...) + " )")
				}
				out.WriteString(" )\n")
			}
			out.WriteString(")\n}\n}\n")
		default:
			return refuse(entity.Index, 0, "holds a block this build does not know (%T)", item)
		}
	}
	out.WriteString("}\n")
	return nil
}

// writeFace writes one face line in its own syntax: classic, Valve 220 or
// `brushDef`, with its Quake 2 / Quake III tail when the file had one.
func writeFace(out *bytes.Buffer, face quake3map.Face) {
	p := face.Points
	fmt.Fprintf(out, "( %s ) ( %s ) ( %s ) ", spell(p[0][:]...), spell(p[1][:]...), spell(p[2][:]...))
	switch {
	case face.Matrix != nil:
		m := face.Matrix
		fmt.Fprintf(out, "( ( %s ) ( %s ) ) %s", spell(m[0][:]...), spell(m[1][:]...), face.Texture)
	case face.Valve != nil:
		v := face.Valve
		fmt.Fprintf(out, "%s [ %s ] [ %s ] %s", face.Texture, spell(v.U[:]...), spell(v.V[:]...),
			spell(v.Rotation, v.Scale[0], v.Scale[1]))
	default:
		c := face.Classic
		fmt.Fprintf(out, "%s %s", face.Texture, spell(c.Shift[0], c.Shift[1], c.Rotation, c.Scale[0], c.Scale[1]))
	}
	if len(face.Tail) > 0 {
		out.WriteString(" " + spell(face.Tail...))
	}
	out.WriteString("\n")
}

// sameMap compares two parsed maps by what a compiler reads — every entity's
// items in order, every number, every name — and not by where they were.
func sameMap(a, b []*quake3map.Entity) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if len(a[index].Content) != len(b[index].Content) {
			return false
		}
		for position := range a[index].Content {
			if !reflect.DeepEqual(unplaced(a[index].Content[position]), unplaced(b[index].Content[position])) {
				return false
			}
		}
	}
	return true
}

// unplaced is an item with its line and column cleared.
func unplaced(item quake3map.Item) any {
	switch item := item.(type) {
	case quake3map.Property:
		item.Line, item.Column = 0, 0
		return item
	case *quake3map.Brush:
		copied := *item
		copied.Line, copied.Column = 0, 0
		copied.Faces = append([]quake3map.Face(nil), item.Faces...)
		for index := range copied.Faces {
			copied.Faces[index].Line, copied.Faces[index].Column = 0, 0
			if len(copied.Faces[index].Tail) == 0 {
				copied.Faces[index].Tail = nil
			}
		}
		return copied
	case *quake3map.Patch:
		copied := *item
		copied.Line, copied.Column = 0, 0
		return copied
	}
	return item
}

// CompileBoundary names what `canonical-map` enforces, in the protocol
// document's capabilities. A later rule that admits or refuses differently is a
// new name, so a Companion can require the one it was tested with.
const CompileBoundary = "canonical-map/1"

// canonicalResult is what `canonical-map --json` prints.
type canonicalResult struct {
	Input  string `json:"input"`
	Output string `json:"output"`
	// ReducedWADs are the worldspawn WAD entries that left the build and were
	// kept as their file names only.
	ReducedWADs []string `json:"reduced_wads"`
}

func runCanonicalMap(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("canonical-map", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "the map to read")
	output := flags.String("output", "", "the new file to write; it must not exist")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if flags.Parse(args) != nil || *input == "" || *output == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: "+Program+" canonical-map --input <file.map> --output <new file> [--json]")
		return exitUsage
	}
	// The input is read, never followed: a link is not the file the Companion
	// downloaded.
	info, err := os.Lstat(*input)
	if err != nil {
		return terminal(stderr, "io_error", "the map could not be read", err.Error())
	}
	if !info.Mode().IsRegular() {
		return terminal(stderr, "io_error", "the map could not be read", *input+" is not a regular file")
	}
	file, err := os.Open(*input)
	if err != nil {
		return terminal(stderr, "io_error", "the map could not be read", err.Error())
	}
	text, err := io.ReadAll(io.LimitReader(file, maxCanonicalMap+1))
	file.Close()
	if err != nil {
		return terminal(stderr, "io_error", "the map could not be read", err.Error())
	}
	if len(text) > maxCanonicalMap {
		return terminal(stderr, "input_too_large", "the map was refused",
			fmt.Sprintf("the map is more than the %d bytes this check reads", maxCanonicalMap))
	}
	out, reduced, err := canonicalMapText(text)
	if err != nil {
		// The reason stays `map_refused`; an external map file also says so
		// as `refusal`, a key an older reader ignores, so the editor can name
		// what to remove (NEW_515, AUCOM #44).
		refusal := ""
		if errors.Is(err, errExternalMap) {
			refusal = refusalExternalMap
		}
		return terminalRefusal(stderr, "map_refused", "the map was refused", err.Error(), refusal)
	}
	// Exclusive: a name already there — a link planted where the copy goes
	// included — is an error, never a file written through.
	created, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return terminal(stderr, "io_error", "the canonical copy could not be written", err.Error())
	}
	if _, err = created.Write(out); err == nil {
		err = created.Sync()
	}
	if closeErr := created.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(*output)
		return terminal(stderr, "io_error", "the canonical copy could not be written", err.Error())
	}
	if reduced == nil {
		reduced = []string{}
	}
	if *asJSON {
		return writeJSON(stdout, stderr, canonicalResult{Input: *input, Output: *output, ReducedWADs: reduced})
	}
	fmt.Fprintln(stdout, *output)
	return exitOK
}
