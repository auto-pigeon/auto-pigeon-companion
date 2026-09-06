package job

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Two views of the same output, and why there have to be two.
//
// The raw log is what the program wrote: the exact bytes, invalid UTF-8 and
// all. That is the evidence. A compiler that emits a filename in the user's own
// locale encoding, or a truncated multi-byte sequence at a buffer boundary, is
// telling you something, and a log that silently repaired it has destroyed the
// only copy of the thing you needed to see.
//
// The user view is what a person reads: valid UTF-8, no control characters
// steering their terminal, and no credentials. It is derived from the raw log
// and never replaces it.
//
// Redaction is a safety net, not a boundary. The boundary is that the executor
// is never given a credential in the first place — no AUB token reaches a
// profile, a request, an argument array or an environment. This exists because
// a *tool* can print one it found by other means, and because a redacted log is
// the one a user attaches to a bug report.

// redactionMarker is what replaces a secret. Fixed and obvious, so a reader can
// tell a redaction from a value that happened to look like one.
const redactionMarker = "[redacted]"

var (
	// A JSON Web Token: three base64url segments, the first of which starts
	// with the encoding of `{"`. AUB issues these, and a tool that was handed
	// one would print it whole.
	jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}`)
	// An authorization header takes the whole rest of its line: the value is
	// the credential whatever scheme it names, and matching only the first word
	// after the colon leaves `Bearer <token>` with the token still in it.
	authHeaderPattern = regexp.MustCompile(`(?i)\b(proxy-authorization|authorization)\s*:[^\n]*`)
	// An HTTP credential outside a header, however it is spelled.
	bearerPattern = regexp.MustCompile(`(?i)\b(bearer|token)\s+\S+`)
	// A credential in a URL's query string or in a `key=value` argument.
	assignedSecretPattern = regexp.MustCompile(`(?i)\b([a-z0-9_-]*(?:token|secret|password|passwd|api[_-]?key|credential|session)[a-z0-9_-]*)\s*[=:]\s*("?)([^\s"&]+)`)
	// A password in a URL's userinfo.
	urlUserinfoPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+):([^\s@/]+)@`)
)

// Redactor removes credentials from text on its way to a person.
//
// Literals come from the running program — the AUB session token, when there is
// one — and are matched exactly. The patterns catch what a tool printed that
// this program never saw. Both are needed: a literal cannot catch a token the
// Companion does not hold, and a pattern cannot catch a token that does not
// look like one.
type Redactor struct {
	literals []string
}

// NewRedactor builds a redactor over a set of literal secrets. Empty and
// very short strings are ignored: redacting every occurrence of a two-character
// "secret" would destroy the log and protect nothing.
func NewRedactor(literals ...string) *Redactor {
	kept := make([]string, 0, len(literals))
	for _, literal := range literals {
		if len(strings.TrimSpace(literal)) >= 8 {
			kept = append(kept, literal)
		}
	}
	// Longest first, so a token that contains another one is replaced whole
	// rather than leaving its tail behind.
	sort.Slice(kept, func(i, j int) bool { return len(kept[i]) > len(kept[j]) })
	return &Redactor{literals: kept}
}

// Redact removes credentials from one string.
func (r *Redactor) Redact(text string) string {
	if r != nil {
		for _, literal := range r.literals {
			text = strings.ReplaceAll(text, literal, redactionMarker)
		}
	}
	text = jwtPattern.ReplaceAllString(text, redactionMarker)
	text = urlUserinfoPattern.ReplaceAllString(text, "$1:"+redactionMarker+"@")
	text = authHeaderPattern.ReplaceAllString(text, "$1: "+redactionMarker)
	text = bearerPattern.ReplaceAllStringFunc(text, func(match string) string {
		keyword := strings.Fields(match)[0]
		return keyword + " " + redactionMarker
	})
	text = assignedSecretPattern.ReplaceAllString(text, "$1=$2"+redactionMarker)
	return text
}

// RedactAll applies Redact to a slice, returning a new one.
func (r *Redactor) RedactAll(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = r.Redact(value)
	}
	return out
}

// UserView turns raw program output into something safe to render.
//
// Three separate problems, in order. Invalid UTF-8 becomes U+FFFD, because a
// JSON encoder would otherwise produce mojibake or refuse the document.
// Control characters other than tab are removed, because a log viewer that
// honours ANSI escapes hands the program control of the reader's terminal — a
// tool can move the cursor, repaint what is above it, and make a build that
// failed look like one that passed. And credentials are redacted last, after
// the text has stopped being able to hide them inside an escape sequence.
func (r *Redactor) UserView(raw []byte) string {
	text := string(raw)
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "\uFFFD")
	}
	text = stripControl(text)
	return r.Redact(text)
}

// stripControl removes control characters, keeping tab, newline and carriage
// return, which are layout rather than control.
func stripControl(text string) string {
	if strings.IndexFunc(text, isStrippedControl) < 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if isStrippedControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isStrippedControl(r rune) bool {
	switch r {
	case '\t', '\n', '\r':
		return false
	}
	// C0, DEL and C1. C1 matters because a lone 0x9B is a control sequence
	// introducer in its own right on terminals that decode it.
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}
