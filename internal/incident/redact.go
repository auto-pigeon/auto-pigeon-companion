package incident

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

//go:embed contract/redaction-rules.json
var redactionJSON []byte

type valuePattern struct {
	name        string
	replacement string
	re          *regexp.Regexp
}

var (
	redactOnce  sync.Once
	placeholder string
	droppedSet  map[string]struct{}
	deniedSubs  []string
	patterns    []valuePattern
	// skipped names a contract pattern that did not compile. Always empty in a
	// correct build; TestEveryContractPatternCompilesInRE2 asserts it.
	skipped []string
)

// jsWhitespace is JavaScript's `\s`, spelled for RE2.
//
// The contract's patterns are JavaScript regular expressions, and there `\s` is
// UNICODE whitespace; in RE2 it is ASCII only. Compiled unchanged, `[^\s'"]+`
// would run straight through a no-break space that the browser's redaction
// stops at, and the two lanes would redact the same string differently. The
// translation below reproduces the JavaScript class exactly (ECMAScript's
// WhiteSpace plus LineTerminator).
const jsWhitespace = `\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}`

// translateJS rewrites the one construct whose meaning differs between the
// JavaScript source of truth and RE2: `\s` (and `\S` outside a class).
func translateJS(pattern string) string {
	var out strings.Builder
	inClass := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			next := pattern[i+1]
			switch {
			case next == 's' && inClass:
				out.WriteString(jsWhitespace)
			case next == 's':
				out.WriteString("[" + jsWhitespace + "]")
			case next == 'S' && !inClass:
				out.WriteString("[^" + jsWhitespace + "]")
			default:
				out.WriteByte(c)
				out.WriteByte(next)
			}
			i++
			continue
		}
		switch c {
		case '[':
			if !inClass {
				inClass = true
			}
		case ']':
			if inClass {
				inClass = false
			}
		}
		out.WriteByte(c)
	}
	return out.String()
}

func loadRules() {
	redactOnce.Do(func() {
		var doc struct {
			Placeholder string   `json:"placeholder"`
			Dropped     []string `json:"dropped_keys"`
			Denied      []string `json:"denied_key_substrings"`
			Patterns    []struct {
				Name        string `json:"name"`
				Pattern     string `json:"pattern"`
				Flags       string `json:"flags"`
				Replacement string `json:"replacement"`
			} `json:"value_patterns"`
		}
		_ = json.Unmarshal(redactionJSON, &doc)
		placeholder = doc.Placeholder
		if placeholder == "" {
			placeholder = "[redacted]"
		}
		droppedSet = make(map[string]struct{}, len(doc.Dropped))
		for _, key := range doc.Dropped {
			droppedSet[strings.ToLower(key)] = struct{}{}
		}
		for _, sub := range doc.Denied {
			deniedSubs = append(deniedSubs, strings.ToLower(sub))
		}
		for _, p := range doc.Patterns {
			expr := translateJS(p.Pattern)
			// `g` is what ReplaceAll does anyway; `i` changes the expression and
			// RE2 spells it inline.
			if strings.Contains(p.Flags, "i") {
				expr = "(?i)" + expr
			}
			re, err := regexp.Compile(expr)
			if err != nil {
				skipped = append(skipped, p.Name)
				continue
			}
			patterns = append(patterns, valuePattern{name: p.Name, replacement: p.Replacement, re: re})
		}
	})
}

// RedactString applies the contract's value patterns to one string, in the
// contract's order — the order is part of the contract.
//
// Nothing here matches a bare 32-character hexadecimal string, so incident and
// correlation ids survive, which is what makes a redacted record correlatable.
func RedactString(in string) string {
	if in == "" {
		return in
	}
	loadRules()
	out := in
	for _, p := range patterns {
		out = p.re.ReplaceAllString(out, p.replacement)
	}
	return out
}

// DroppedKey reports whether a key's whole value is dropped (exact match).
func DroppedKey(name string) bool {
	loadRules()
	_, ok := droppedSet[strings.ToLower(name)]
	return ok
}

// DeniedKey reports whether a key's value is replaced with the placeholder.
func DeniedKey(name string) bool {
	loadRules()
	lower := strings.ToLower(name)
	for _, sub := range deniedSubs {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// Placeholder is what a denied value becomes.
func Placeholder() string {
	loadRules()
	return placeholder
}

const maxRedactDepth = 8

// RedactValue applies all three kinds of rule to a JSON-shaped value, with a
// bounded depth: a structure deep enough to exhaust it is not evidence anybody
// was going to read.
func RedactValue(value any) any { return redactValue(value, 0) }

func redactValue(value any, depth int) any {
	if depth > maxRedactDepth {
		return Placeholder()
	}
	switch v := value.(type) {
	case string:
		return RedactString(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if DroppedKey(key) {
				continue
			}
			if DeniedKey(key) {
				out[key] = Placeholder()
				continue
			}
			out[key] = redactValue(item, depth+1)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, redactValue(item, depth+1))
		}
		return out
	default:
		return value
	}
}
