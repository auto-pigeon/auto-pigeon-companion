package pack

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Member paths, and why every rule below is a refusal.
//
// A path inside an archive is the only part of the archive that becomes a path
// on the reader's machine. Everything else is bytes in a file somebody chose to
// open; this is the part that decides *which* file. So the rules here are
// applied in both directions — nothing is written that would be refused on
// read, and nothing is read that would be refused on write — and none of them
// sanitises. `..` is not repaired into `.`, a backslash is not translated into
// a slash, and a name that differs from another only in case is not silently
// renamed. Repairing any of those means writing a file the person who reviewed
// the listing did not see.

// ErrUnsafePath reports a member path this program will not write or extract.
var ErrUnsafePath = errors.New("pack: unsafe archive path")

// CheckEntryPath applies every member-path rule.
//
// The returned error is a bare description — "is absolute", "escapes the
// archive" — so a caller can put the path and its own context in front of it,
// which is the convention `internal/catalog` established and the reason the
// messages here do not repeat the name.
func CheckEntryPath(name string, maxLength int) error {
	switch {
	case name == "":
		return errors.New("is empty")
	case len(name) > maxLength:
		return fmt.Errorf("is %d bytes, over the %d this container stores", len(name), maxLength)
	case strings.HasPrefix(name, "/"):
		return errors.New("is absolute")
	case strings.HasSuffix(name, "/"):
		return errors.New("ends in a separator, so it names a directory rather than a file")
	case strings.ContainsRune(name, '\\'):
		return errors.New("contains a backslash, which is a path separator on Windows and an ordinary filename character elsewhere")
	case len(name) > 1 && name[1] == ':':
		return errors.New("names a Windows drive")
	case strings.HasPrefix(name, "~"):
		return errors.New("starts at a home directory")
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c > 0x7e {
			if c == 0 {
				return errors.New("contains a NUL byte, which ends the name early for every engine that reads it as a C string")
			}
			return fmt.Errorf("contains the byte 0x%02x at offset %d, and this build writes printable ASCII only", c, i)
		}
	}
	for _, element := range strings.Split(name, "/") {
		switch element {
		case "":
			return errors.New("has an empty path element")
		case ".", "..":
			return errors.New("escapes the archive")
		}
		if strings.HasPrefix(element, " ") || strings.HasSuffix(element, " ") || strings.HasSuffix(element, ".") {
			return errors.New("has an element that Windows cannot store as written")
		}
		if reservedWindowsName(element) {
			return errors.New("names a reserved Windows device")
		}
	}
	if cleaned := path.Clean(name); cleaned != name {
		return errors.New("is not already in its simplest form, so what it names depends on who cleans it")
	}
	return nil
}

// Why printable ASCII, and not "valid UTF-8".
//
// Three reasons, and only the third is about safety. Engine paths are ASCII:
// Quake lowercases with a table that assumes it, and a PAK name field is bytes
// with no encoding declared at all, so there is no answer to "what does this
// PAK say" for a non-ASCII name. ZIP has two encodings and a flag that is
// frequently wrong, so a PK3 written with a UTF-8 name is a PK3 that reads
// differently in two engines. And case-collision detection — the rule below —
// is exactly correct on ASCII and merely plausible on Unicode, where two
// distinct code point sequences can be the same filename on macOS.

// reservedWindowsName reports a name Windows will not create a file for, with
// or without an extension. `aux.txt` fails the same way `aux` does.
func reservedWindowsName(element string) bool {
	base := element
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.EqualFold(base[:3], "COM") || strings.EqualFold(base[:3], "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

// CaseKey is the identity a case-insensitive filesystem or engine lookup would
// use. ASCII-only paths make [strings.ToLower] exactly that, with no locale in
// it — Turkish dotless i is the classic way this goes wrong, and it cannot
// happen here because `İ` is not a byte this package accepts.
func CaseKey(name string) string { return strings.ToLower(name) }

// Collision is two member paths that a reader would confuse for one file.
type Collision struct {
	// Kind is `duplicate` for two identical paths and `case` for two that
	// differ only in capitalisation.
	Kind string `json:"kind"`
	// Paths are the colliding members, in the order they appear.
	Paths []string `json:"paths"`
}

func (c Collision) Error() string {
	if c.Kind == "duplicate" {
		return fmt.Sprintf("%q appears %d times, so which one a reader gets depends on the order it scans in",
			c.Paths[0], len(c.Paths))
	}
	return fmt.Sprintf("%s differ only in capitalisation, which is two files on Linux and one on Windows and macOS",
		strings.Join(quoteAll(c.Paths), " and "))
}

// FindCollisions reports every duplicate and case-collision in a set of member
// paths, in a stable order.
//
// Both are refused rather than resolved, and for the same reason: an archive
// where `maps/E1M1.bsp` and `maps/e1m1.bsp` are different files is an archive
// that installs differently on the packager's machine than on the player's,
// and picking one is picking which half of the audience gets the wrong map.
func FindCollisions(paths []string) []Collision {
	type group struct {
		exact map[string]int
		order []string
	}
	byKey := map[string]*group{}
	var keys []string
	for _, p := range paths {
		key := CaseKey(p)
		g := byKey[key]
		if g == nil {
			g = &group{exact: map[string]int{}}
			byKey[key] = g
			keys = append(keys, key)
		}
		if g.exact[p] == 0 {
			g.order = append(g.order, p)
		}
		g.exact[p]++
	}
	sort.Strings(keys)

	var out []Collision
	for _, key := range keys {
		g := byKey[key]
		for _, p := range g.order {
			if g.exact[p] > 1 {
				repeated := make([]string, g.exact[p])
				for i := range repeated {
					repeated[i] = p
				}
				out = append(out, Collision{Kind: "duplicate", Paths: repeated})
			}
		}
		if len(g.order) > 1 {
			variants := append([]string(nil), g.order...)
			sort.Strings(variants)
			out = append(out, Collision{Kind: "case", Paths: variants})
		}
	}
	return out
}

func quoteAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fmt.Sprintf("%q", v)
	}
	return out
}

// NormalizeEntryPath converts a filesystem-relative path into the member path
// it would occupy, and reports what it could not make safe.
//
// The only transformation is the separator: a Windows caller walking a
// directory hands over `maps\e1m1.bsp`, and `/` is what every one of these
// containers stores. Everything else is checked, not changed.
func NormalizeEntryPath(relative string, maxLength int) (string, error) {
	name := strings.ReplaceAll(relative, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	if err := CheckEntryPath(name, maxLength); err != nil {
		return "", fmt.Errorf("%w: %q %w", ErrUnsafePath, relative, err)
	}
	return name, nil
}
