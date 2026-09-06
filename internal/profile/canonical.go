package profile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Canonical encoding, and what a digest is allowed to mean.
//
// A digest over a profile has to answer one question: is this the document the
// user reviewed and granted? That only works if two byte sequences saying the
// same thing hash the same — otherwise reformatting a file revokes its grant —
// and if two byte sequences saying different things never do.
//
// The rules below are RFC 8785 (JSON Canonicalization Scheme) with its hardest
// part removed. JCS's number serialisation is ECMAScript's, which is a
// shortest-round-trip float algorithm and a genuine source of cross-language
// disagreement. A profile has no use for a fractional number, so this
// canonicalizer refuses one, and the number rule becomes "decimal integers,
// written the obvious way".
//
//   - Objects: members sorted by key, compared as UTF-16 code-unit sequences,
//     as JCS specifies. Members whose value is null are dropped, so "absent"
//     and "null" are the same document.
//   - Arrays: order preserved. A null element is refused.
//   - Strings: minimal JSON escaping — the short forms for the control
//     characters that have one, `\u00xx` for the rest, and raw UTF-8 for
//     everything else. Invalid UTF-8 is refused rather than replaced.
//   - Numbers: integers only.
//   - No whitespace anywhere, and no trailing newline.
//
// Unicode normalisation is *not* performed. Doing it properly needs the
// normalisation tables, which are a dependency this repository does not have,
// and doing it approximately would be worse than not doing it: two documents
// that differ only in normal form are two documents, and they hash differently.
// Authors write one spelling; the fixtures are ASCII; the rule is written down
// rather than implied.

// Canonical returns the canonical encoding of a value.
//
// It also runs [CheckPortable]: a document that cannot be digested honestly is
// one that should never have been published, and having one entry point for
// both means no caller can digest a document without the portability rules
// having been applied to it.
func Canonical(v any) ([]byte, error) {
	tree, err := toTree(v)
	if err != nil {
		return nil, err
	}
	if err := CheckPortable(tree); err != nil {
		return nil, fmt.Errorf("profile: this document is not portable: %w", err)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// canonicalUnchecked is Canonical without the portability scan. It exists for
// the diff machinery, which has to be able to render a document that has just
// been *refused* — a review that could not show the user the offending value
// would be a poor way to explain why an import was rejected.
func canonicalUnchecked(v any) ([]byte, error) {
	tree, err := toTree(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// toTree renders a value through JSON into a plain tree, so canonicalization
// has one input shape whether it was handed a typed profile or a decoded map.
func toTree(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("profile: encoding for canonicalization: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, fmt.Errorf("profile: decoding for canonicalization: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("profile: trailing content after the document")
	}
	return tree, nil
}

// Digest is the canonical form's SHA-256, as `sha256:<hex>`.
//
// The prefix is not decoration. A bare hex string is a hash of something by
// some algorithm, and the first time this file needs a second algorithm every
// stored digest will be ambiguous. Naming it costs seven bytes.
func Digest(v any) (string, error) {
	canonical, err := Canonical(v)
	if err != nil {
		return "", err
	}
	return DigestBytes(canonical), nil
}

// DigestBytes is the digest of already-canonical bytes.
func DigestBytes(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeCanonical(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		return fmt.Errorf("profile: a null appears where a value is required")
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		return writeCanonicalString(buf, v)
	case json.Number:
		return writeCanonicalNumber(buf, v)
	case []any:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			if item == nil {
				return fmt.Errorf("profile: a null appears as element %d of an array", i)
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k, item := range v {
			if item == nil {
				continue // absent and null are the same document
			}
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanonical(buf, v[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("profile: %T cannot appear in a canonical document", value)
	}
	return nil
}

func writeCanonicalNumber(buf *bytes.Buffer, n json.Number) error {
	s := n.String()
	if strings.ContainsAny(s, ".eE") {
		return fmt.Errorf("profile: the number %s is not an integer; a profile has no fractional values, and canonicalizing one is where two implementations start to disagree", s)
	}
	if _, err := n.Int64(); err != nil {
		return fmt.Errorf("profile: the number %s is not a 64-bit integer: %w", s, err)
	}
	buf.WriteString(s)
	return nil
}

// writeCanonicalString applies JCS's escaping rules.
func writeCanonicalString(buf *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("profile: a string is not valid UTF-8 and cannot be canonicalized")
	}
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
				continue
			}
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
	return nil
}

// lessUTF16 compares two strings as sequences of UTF-16 code units, which is
// the order JCS specifies. It differs from Go's byte order above U+FFFF, where
// a surrogate pair sorts below the unpaired code points that precede it in
// UTF-8 — rare in a profile key, and exactly the kind of rare that makes two
// implementations disagree about a digest a year later.
func lessUTF16(a, b string) bool {
	if a == b {
		return false
	}
	// The fast path: below U+10000 the two orders agree, and a profile's keys
	// are ASCII in practice.
	if isBMPOnly(a) && isBMPOnly(b) {
		return a < b
	}
	au, bu := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(au) && i < len(bu); i++ {
		if au[i] != bu[i] {
			return au[i] < bu[i]
		}
	}
	return len(au) < len(bu)
}

func isBMPOnly(s string) bool {
	for _, r := range s {
		if r > 0xFFFF {
			return false
		}
	}
	return true
}
