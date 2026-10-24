package build

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// A map that came from Auto-Pigeon names its files by name, inside the build.
//
// # Why
//
// A map's text can name files the compiler then opens: worldspawn's `wad` and
// `_wad` list texture archives, and a `misc_external_map`'s `_external_map` is a
// whole map file read as map text. ericw qbsp opens an absolute entry directly,
// joins a relative one onto each `-wadpath` — so `../../` climbs out — and loads
// `_external_map` from wherever it points. A map revision from AUB is written by
// whoever may save that map, and the Companion compiles it on somebody else's
// machine, with their files; a hosted game then uploads the BSP, which other
// readers of the map can download. Measured with fork release 1.4533: absolute
// and `../` WAD entries pulled texture data from outside the build into the BSP,
// and an absolute `_external_map` pulled in its brushes (`NEW_342` lead
// `aucom/job/map-controlled-compiler-paths-unsanitised`, repaired in `NEW_348`).
//
// So the STAGED COPY of an input that came from Auto-Pigeon — one with a
// SourceRef — is confined before any step reads it:
//
//   - a `wad`/`_wad` entry that is absolute, has a drive or scheme, or climbs
//     with `..` keeps only its file name, which is how AUB names a map's WADs
//     and how the content root is laid out (declaredwads.go places directory
//     entries that stay inside it). An archive the build does not hold simply
//     is not found, as it would not have been on another machine anyway;
//   - an `_external_map` is refused by name: nothing Auto-Pigeon stages is one;
//   - the text ends where the compiler's tokenizer can stop. ericw's
//     `parse_token` reads past the end of a buffer that ends without a newline,
//     inside a `//` comment or inside a quoted string (`NEW_348` f14b: fixed in
//     the fork, not yet in the pinned release). A missing final newline is
//     added; a quoted string still open at the end of the file is refused. A
//     `prefab.map` another workspace member uploaded reaches the compiler
//     through this door unconverted, which is why it is checked here and not
//     only in the extractor.
//
// A file the user pointed at on their own disk has no SourceRef and is staged
// exactly as it is — their own map naming their own files is not this case. The
// compiler's arguments are untouched: this changes what the map SAYS, never how
// the tool is started.

// maxConfinedMap bounds how much of a map is read to confine it. A source map
// larger than this is refused rather than compiled unchecked.
const maxConfinedMap = 256 << 20

// confineStagedMap rewrites a staged `.map` in place when it names a file
// outside the build, returning the entries it rewrote.
func confineStagedMap(stagedPath string) ([]string, error) {
	info, err := os.Stat(stagedPath)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxConfinedMap {
		return nil, fmt.Errorf("the map is %d bytes, more than the %d this build checks", info.Size(), maxConfinedMap)
	}
	text, err := os.ReadFile(stagedPath)
	if err != nil {
		return nil, err
	}
	out, rewritten, err := confineMapText(text)
	if err != nil || len(rewritten) == 0 {
		return nil, err
	}
	return rewritten, os.WriteFile(stagedPath, out, 0o600)
}

// errOpenQuote is the refusal for a quoted string the file never closes.
var errOpenQuote = errors.New("the map ends inside a quoted string, so the compiler would read past the end of " +
	"the file. Close the quote, or save the map again from the editor")

// endsInsideQuote scans the way the compiler's tokenizer does and reports
// whether the file ends inside a quote. A `//` comment runs to the end of its
// line outside a quote; a quote runs to the next unescaped `"` whatever lines
// it crosses. Inside one, `\n \' \r \t \\ \b` consume the next byte, and
// `\"` does too UNLESS a line break follows it — then the quote closes, which
// is ericw's allowance for an old editor's `"wad" "c:\foo\"`. Any other byte
// after a backslash is left alone (ericw `common/parser.cc` parse_token).
func endsInsideQuote(text []byte) bool {
	inQuote := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case inQuote && c == '\\' && i+1 < len(text):
			switch next := text[i+1]; next {
			case 'n', '\'', 'r', 't', '\\', 'b':
				i++
			case '"':
				if i+2 >= len(text) || (text[i+2] != '\r' && text[i+2] != '\n') {
					i++
				}
			}
		case inQuote && c == '"':
			inQuote = false
		case inQuote:
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '"':
			inQuote = true
		}
	}
	return inQuote
}

// errExternalMap is the refusal; the sentence names the key and what to do.
var errExternalMap = errors.New("the map names an external map file (`_external_map`), which a map from Auto-Pigeon " +
	"may not do: the compiler would read that file from this computer. Remove the misc_external_map entity, " +
	"or merge its brushes into the map")

// confineMapText is the rule over map text: entity-level key/value pairs only,
// `//` comments and brush bodies skipped, everything else byte-for-byte.
func confineMapText(text []byte) ([]byte, []string, error) {
	if endsInsideQuote(text) {
		return nil, nil, errOpenQuote
	}
	out, rewritten, err := confineMapKeys(text)
	if err != nil {
		return nil, nil, err
	}
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(append([]byte(nil), out...), '\n')
		rewritten = append(rewritten, "(added the final newline the compiler needs to stop)")
	}
	return out, rewritten, nil
}

// confineMapKeys applies the key rules: escaping WAD entries reduced to their
// file names, an `_external_map` refused.
func confineMapKeys(text []byte) ([]byte, []string, error) {
	type span struct{ start, end int } // a quoted string's contents
	var (
		depth, entity int = 0, -1
		strings_      []span
		edits         []struct {
			at       span
			value    string
			original string
		}
		rewritten []string
	)
	flush := func() error {
		for i := 0; i+1 < len(strings_); i += 2 {
			key := string(text[strings_[i].start:strings_[i].end])
			value := strings_[i+1]
			switch {
			case strings.EqualFold(key, "_external_map"):
				if strings.TrimSpace(string(text[value.start:value.end])) != "" {
					return errExternalMap
				}
			case entity == 0 && (strings.EqualFold(key, "wad") || strings.EqualFold(key, "_wad")):
				original := string(text[value.start:value.end])
				confined, changed := confineWADList(original)
				if len(changed) > 0 {
					edits = append(edits, struct {
						at       span
						value    string
						original string
					}{value, confined, original})
					rewritten = append(rewritten, changed...)
				}
			}
		}
		strings_ = strings_[:0]
		return nil
	}
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '"':
			end := i + 1
			for end < len(text) && text[end] != '"' && text[end] != '\n' {
				end++
			}
			if depth == 1 {
				strings_ = append(strings_, span{i + 1, end})
			}
			i = end
		case c == '{':
			depth++
			if depth == 1 {
				entity++
				strings_ = strings_[:0]
			}
		case c == '}':
			if depth == 1 {
				if err := flush(); err != nil {
					return nil, nil, err
				}
			}
			if depth > 0 {
				depth--
			}
		}
	}
	if depth == 1 {
		if err := flush(); err != nil {
			return nil, nil, err
		}
	}
	if len(edits) == 0 {
		return text, nil, nil
	}
	var out strings.Builder
	last := 0
	for _, edit := range edits {
		out.Write(text[last:edit.at.start])
		out.WriteString(edit.value)
		last = edit.at.end
	}
	out.Write(text[last:])
	return []byte(out.String()), rewritten, nil
}

// confineWADList keeps each `;`-separated entry that stays inside the build and
// reduces every other one to its file name, returning the entries it changed.
func confineWADList(list string) (string, []string) {
	entries := strings.Split(list, ";")
	var changed []string
	kept := entries[:0]
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		slashed := strings.ReplaceAll(trimmed, `\`, "/")
		escapes := strings.HasPrefix(slashed, "/") || strings.Contains(slashed, ":") || filepath.IsAbs(trimmed)
		for _, segment := range strings.Split(slashed, "/") {
			if segment == ".." {
				escapes = true
			}
		}
		if !escapes {
			kept = append(kept, entry)
			continue
		}
		changed = append(changed, trimmed)
		if name := path.Base(slashed); name != "" && name != "." && name != ".." && name != "/" {
			kept = append(kept, name)
		}
	}
	return strings.Join(kept, ";"), changed
}
