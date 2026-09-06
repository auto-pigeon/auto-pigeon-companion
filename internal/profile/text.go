package profile

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Length caps. None of these is a security property on its own — a profile is
// validated, not trusted, whatever its size — but every unbounded string in a
// document that arrives from strangers is a way to make a permission summary
// unreadable, and an unreadable permission summary is one nobody reads.
const (
	maxIDLength      = 128
	maxNameLength    = 120
	maxSummaryLength = 200
	maxTextLength    = 4000
	maxURLLength     = 512
	maxArgLength     = 1024
	maxListLength    = 256
)

// checkText validates a free-text member: valid UTF-8, no control characters,
// no bidirectional overrides, within a length cap.
//
// The bidi rule is the one that looks paranoid and is not. A right-to-left
// override inside an argument or a title renders as text in one order and is
// stored in another, which is precisely how a reviewer approves a command that
// is not the one they read. There is no legitimate use of an override character
// in a tool name or an argv element.
func checkText(c *collector, value string, max int, required bool) {
	if value == "" {
		if required {
			c.addf("is required and empty")
		}
		return
	}
	if !utf8.ValidString(value) {
		c.fixf("write the value as UTF-8", "is not valid UTF-8")
		return
	}
	if len(value) > max {
		c.fixf(fmt.Sprintf("shorten it to %d bytes or fewer", max), "is %d bytes long, over the %d-byte limit", len(value), max)
	}
	for _, r := range value {
		switch {
		case r == '\t', r == '\n', r == '\r':
			// Permitted only in the long-form description members, which pass a
			// larger cap; every caller that forbids them checks separately.
			if max < maxTextLength {
				c.fixf("keep this member to a single line", "contains a line break or tab")
				return
			}
		case unicode.IsControl(r):
			c.fixf("remove the control character", "contains the control character U+%04X", r)
			return
		case isBidiControl(r):
			c.fixf("remove the character; it makes the rendered text differ from the stored text",
				"contains the bidirectional control character U+%04X", r)
			return
		}
	}
}

// isBidiControl reports the Unicode bidirectional formatting characters — the
// embeddings, overrides and isolates.
func isBidiControl(r rune) bool {
	switch r {
	case 0x061C, 0x200E, 0x200F, 0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
		0x2066, 0x2067, 0x2068, 0x2069:
		return true
	}
	return false
}

// shellSyntax is the set of literal fragments a profile may not contain in an
// argument, an executable name, a path or an environment value.
//
// # Why reject something that is already inert
//
// The executor spawns argv directly. There is no shell anywhere in the path a
// profile's arguments take, so `;` and `$(…)` are ordinary characters and
// rejecting them buys no execution safety at all. They are rejected anyway, for
// two reasons that are about people rather than processes.
//
// The first is that a profile containing them was authored against a shell it
// is never going to get. `--out $(pwd)/x` is not dangerous here; it is *wrong*
// here, and it will produce a file with a `$(pwd)` in its name at some later
// point where the mistake is much harder to see. Failing at import is the
// cheapest place to say so.
//
// The second is review. A user approving a community profile reads a permission
// summary and a command preview. Putting `rm -rf / ;` in front of them and
// expecting them to reason correctly about argv semantics is a bad thing to ask
// of somebody who just wants to compile a map. Nothing that looks like a shell
// command gets far enough to be read.
var shellSyntax = []struct {
	fragment string
	name     string
}{
	{"$(", "command substitution"},
	{"${", "shell parameter expansion"},
	{"`", "backtick command substitution"},
	{"&&", "shell command chaining"},
	{"||", "shell command chaining"},
	{";", "a shell command separator"},
	{"|", "a shell pipe"},
	{">", "a shell redirection"},
	{"<", "a shell redirection"},
	{"\n", "a line break"},
	{"\r", "a carriage return"},
	{"\x00", "a NUL byte"},
}

// checkNoShellSyntax rejects shell control syntax in a value that becomes part
// of a command.
func checkNoShellSyntax(c *collector, value string) {
	for _, s := range shellSyntax {
		if strings.Contains(value, s.fragment) {
			c.fixf("commands are an executable and an argument array; there is no shell, so write the value literally",
				"contains %q, which is %s", s.fragment, s.name)
			return
		}
	}
}

// idRunes: lowercase letters, digits, hyphen. Segments are joined by dots.
func isIDSegment(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// checkID validates a stable profile identifier.
//
// An ID is namespaced — at least two dot-separated segments — because the flat
// namespace is a collision waiting to happen the first time two people both
// publish `qbsp`, and because a namespace is the only part of a community
// profile's identity a user can recognise at a glance. It is lowercase because
// two IDs differing only in case are the same identity to a human and different
// identities to a map, and that difference is a way to shadow a curated profile.
func checkID(c *collector, id string) {
	if id == "" {
		c.fixf("give the profile a namespaced id such as `example.tools.qbsp`", "is required and empty")
		return
	}
	if len(id) > maxIDLength {
		c.fixf(fmt.Sprintf("shorten it to %d bytes or fewer", maxIDLength), "is %d bytes long", len(id))
		return
	}
	if id != strings.ToLower(id) {
		c.fixf("write the id in lower case", "contains upper-case characters")
		return
	}
	segments := strings.Split(id, ".")
	if len(segments) < 2 {
		c.fixf("use at least two dot-separated segments, such as `example.qbsp`",
			"is not namespaced: %q has no dot", id)
		return
	}
	for _, segment := range segments {
		if !isIDSegment(segment) {
			c.fixf("segments may contain lower-case letters, digits and inner hyphens only",
				"has an invalid segment %q", segment)
			return
		}
	}
}

// checkToken validates a short internal name — an action id, an input name, an
// option name, an executable name. These appear inside template placeholders,
// so their character set is the template language's and nothing wider.
func checkToken(c *collector, name string) {
	if name == "" {
		c.addf("is required and empty")
		return
	}
	if len(name) > 64 {
		c.fixf("shorten it to 64 bytes or fewer", "is %d bytes long", len(name))
		return
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_') {
			c.fixf("names may contain lower-case letters, digits and underscores only",
				"contains %q, which a template placeholder cannot spell", string(name[i]))
			return
		}
	}
	if name[0] >= '0' && name[0] <= '9' {
		c.fixf("start the name with a letter", "starts with a digit")
	}
}

// checkURL validates a documentation or provenance URL. https only: a profile
// is read by people deciding whether to trust its author, and a plain-http link
// in that position is a link somebody else can rewrite in transit.
func checkURL(c *collector, value string, required bool) {
	if value == "" {
		if required {
			c.addf("is required and empty")
		}
		return
	}
	checkText(c, value, maxURLLength, true)
	if !strings.HasPrefix(value, "https://") {
		c.fixf("use an https:// URL", "is not an https URL")
		return
	}
	if strings.ContainsAny(value, " \t") {
		c.addf("contains whitespace")
	}
}
