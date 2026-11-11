package aub

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

// AUB NEW_476: the legal gate's refusal is recognised by its code, and its
// acceptance address is kept only when it is an absolute http(s) URL.
func TestALegalRefusalCarriesItsAcceptanceAddress(t *testing.T) {
	for _, tc := range []struct{ given, want string }{
		{"https://beta.example.test/legal/accept", "https://beta.example.test/legal/accept"},
		{"javascript:alert(1)", ""},
		{"/legal/accept", ""},
		{"https://user:pw@evil.example.test/", ""},
	} {
		body := `{"status":403,"message":"The Terms or the notices changed.","data":{"reason":{` +
			`"code":"legal_acceptance_required","params":{"acceptance_url":"` + tc.given + `"}}}}`
		err := newAPIError("/api/companion/v1/catalog", &http.Response{
			StatusCode: http.StatusForbidden, Body: io.NopCloser(bytes.NewBufferString(body)),
		})
		apiErr, ok := err.(*APIError)
		if !ok || !apiErr.LegalAcceptanceRequired() {
			t.Fatalf("%q: not recognised as the legal gate: %#v", tc.given, err)
		}
		if apiErr.AcceptanceURL != tc.want {
			t.Errorf("%q: AcceptanceURL = %q, want %q", tc.given, apiErr.AcceptanceURL, tc.want)
		}
	}
}
