package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The canary. Every case below puts it somewhere a careless redaction would let
// through — a query value, a userinfo password, a fragment, the text of a nested
// error — and no output may contain it.
const canary = "hunter2"

// TestAWrappedFetchErrorReadsAsAnAddressAndNotAsAnEscape is AUCOM/AUT 228's half
// of the older P2 diagnostic defect.
//
// net/url quotes the address in every *url.Error it produces, and this
// repository wraps that error under a message that names the address too. The
// field a whitespace split then yields is `"https://host/x":` — a leading quote
// a Trim removes and a trailing one it does not, because the colon is in the
// way — so what reached url.Parse ended in a quote and came back with it escaped
// into the path as `%22`. A person reading `…/keyring.json%22: dial tcp` is being
// shown an address that does not exist.
func TestAWrappedFetchErrorReadsAsAnAddressAndNotAsAnEscape(t *testing.T) {
	address := "https://catalogue.invalid/keyring.json"
	inner := &url.Error{Op: "Get", URL: address, Err: errors.New("dial tcp: no such host")}
	wrapped := fmt.Errorf("catalog: fetching %s: %w", RedactURL(address), redactError(inner))

	got := wrapped.Error()
	if strings.Contains(got, "%22") {
		t.Errorf("the message still escapes a quote into the address: %s", got)
	}
	want := "catalog: fetching https://catalogue.invalid/keyring.json: " +
		"Get https://catalogue.invalid/keyring.json: dial tcp: no such host"
	if got != want {
		t.Errorf("the message reads\n  %s\nand should read\n  %s", got, want)
	}
}

// The punctuation around an address is the reader's, and it survives; the
// quotation marks belonged to an address that is no longer there, and they do
// not. The colon after a *url.Error's URL is the one that matters: without it
// the operation and its cause run together into one unreadable line.
func TestPunctuationAroundAnAddressSurvivesTheRedaction(t *testing.T) {
	cases := map[string]string{
		`"https://host.invalid/a.json":`: "https://host.invalid/a.json:",
		`"https://host.invalid/a.json"`:  "https://host.invalid/a.json",
		`https://host.invalid/a.json,`:   "https://host.invalid/a.json,",
		`(https://host.invalid/a.json)`:  "https://host.invalid/a.json",
		`https://host.invalid/dir/`:      "https://host.invalid/dir/",
		// Not an address at all, and left exactly as it was.
		`dial`:            "dial",
		`"not-a-url":`:    `"not-a-url":`,
		`ftp://host/file`: "ftp://host/file",
	}
	for field, want := range cases {
		if got := redactField(field); got != want {
			t.Errorf("redactField(%q) = %q, want %q", field, got, want)
		}
	}
}

// A trailing slash is part of an address. Trimming it would name a different
// resource, which is why it is not in the closer set.
func TestATrailingSlashIsNotPunctuation(t *testing.T) {
	if got := redactField("https://host.invalid/base/"); got != "https://host.invalid/base/" {
		t.Errorf("a trailing slash was eaten: %q", got)
	}
}

// The four places a credential can hide in a URL, each inside a quoted, wrapped
// error. The readable address comes out; the credential never does.
//
// This is the half of the fix that must not regress. Trimming punctuation before
// parsing is safe only because everything after `?` and `#` is discarded whether
// or not a stray quote came with it — so the test asserts the discarding, not the
// trimming.
func TestNoShapeOfWrappedErrorLetsACredentialThrough(t *testing.T) {
	addresses := []string{
		"https://cdn.invalid/tool.tar.gz?X-Amz-Signature=" + canary,
		"https://user:" + canary + "@cdn.invalid/tool.tar.gz",
		"https://cdn.invalid/tool.tar.gz#" + canary,
		"https://cdn.invalid/tool.tar.gz?token=" + canary + "&expires=1",
	}
	for _, address := range addresses {
		// Nested twice, because that is how a real one arrives: net/http's
		// transport error inside a *url.Error inside this repository's own wrap.
		transport := fmt.Errorf("proxyconnect tcp: lookup %s: no such host", address)
		inner := &url.Error{Op: "Get", URL: address, Err: transport}
		wrapped := fmt.Errorf("catalog: fetching %s: %w", RedactURL(address), redactError(inner))

		got := wrapped.Error()
		if strings.Contains(got, canary) {
			t.Errorf("a credential survived the redaction of %q:\n  %s", address, got)
		}
		if strings.Contains(got, "%22") {
			t.Errorf("the redaction of %q escaped a quote into the address:\n  %s", address, got)
		}
		if !strings.Contains(got, "https://cdn.invalid/tool.tar.gz") {
			t.Errorf("the redaction of %q left nothing a person could act on:\n  %s", address, got)
		}
		if !strings.Contains(got, "[query redacted]") {
			t.Errorf("the redaction of %q does not say something was removed:\n  %s", address, got)
		}
	}
}

// The redacted error still unwraps to what it was. A caller that tests for a
// cause — the downloader does — must not be defeated by the rewriting of the
// text.
func TestRedactionRewritesTheTextAndKeepsTheCause(t *testing.T) {
	cause := errors.New("connection refused")
	inner := &url.Error{Op: "Get", URL: "https://cdn.invalid/a?token=" + canary, Err: cause}
	redacted := RedactError(inner)
	if !errors.Is(redacted, cause) {
		t.Error("the cause did not survive the redaction")
	}
	var target *url.Error
	if !errors.As(redacted, &target) {
		t.Error("the *url.Error did not survive the redaction")
	}
	if strings.Contains(redacted.Error(), canary) {
		t.Errorf("a credential survived: %s", redacted.Error())
	}
}
