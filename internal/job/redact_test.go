package job

import (
	"strings"
	"testing"
)

func TestACredentialDoesNotSurviveIntoWhatAUserReads(t *testing.T) {
	const token = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjEyMyIsImV4cCI6OTk5fQ.Zm9vYmFyYmF6cXV4"
	redactor := NewRedactor(token, "a-literal-session-secret")

	for name, sample := range map[string]string{
		"the literal token this program holds": "GET /api/x\nAuthorization: Bearer " + token,
		"a literal secret from configuration":  "connecting with a-literal-session-secret now",
		"a token this program never saw":       "server said: eyJraWQiOiJ4In0.eyJzdWIiOiJhbm90aGVyIn0.c2lnbmF0dXJlaGVyZQ",
		"a password in a URL":                  "cloning https://someone:hunter2@git.example/repo.git",
		"a credential in a query string":       "GET /v1/things?access_token=abcd1234efgh&page=2",
		"a bare bearer header":                 "authorization: Bearer abcdefghijklmnop",
		"an api key in an argument":            "--api-key=sk-live-0123456789abcdef",
	} {
		t.Run(name, func(t *testing.T) {
			got := redactor.Redact(sample)
			for _, secret := range []string{token, "a-literal-session-secret", "hunter2", "abcd1234efgh",
				"abcdefghijklmnop", "sk-live-0123456789abcdef", "c2lnbmF0dXJlaGVyZQ"} {
				if strings.Contains(sample, secret) && strings.Contains(got, secret) {
					t.Errorf("%q survived redaction:\n  in:  %s\n  out: %s", secret, sample, got)
				}
			}
			if !strings.Contains(got, redactionMarker) {
				t.Errorf("nothing was marked as redacted: %s", got)
			}
		})
	}

	// What is not a credential is left alone. A redactor that ate the log would
	// be worse than no redactor: the log is the evidence.
	plain := "qbsp: 1024 faces, 37 leaks, /home/you/maps/level.map"
	if got := redactor.Redact(plain); got != plain {
		t.Errorf("ordinary output was changed:\n  in:  %s\n  out: %s", plain, got)
	}
}

func TestTheUserViewRemovesWhatWouldSteerATerminal(t *testing.T) {
	raw := []byte("compiling\x1b[2K\x1b[31m error \x1b[0m\r\ndone\x07\n")
	view := NewRedactor().UserView(raw)

	for _, forbidden := range []byte{0x1b, 0x07} {
		if strings.ContainsRune(view, rune(forbidden)) {
			t.Errorf("the user view kept the control byte %#x: %q", forbidden, view)
		}
	}
	for _, want := range []string{"compiling", "error", "done"} {
		if !strings.Contains(view, want) {
			t.Errorf("the user view lost %q: %q", want, view)
		}
	}
	if !strings.Contains(view, "\n") {
		t.Errorf("the user view lost its line breaks: %q", view)
	}
}

func TestAShortValueIsNotTreatedAsASecret(t *testing.T) {
	// Redacting a two-character "secret" would replace it everywhere and leave
	// a log nobody can read, protecting nothing.
	redactor := NewRedactor("ab", "   ", "")
	const line = "about to build abc"
	if got := redactor.Redact(line); got != line {
		t.Errorf("a short literal was redacted:\n  in:  %s\n  out: %s", line, got)
	}
}
