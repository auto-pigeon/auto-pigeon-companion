package aub

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// LeakResultReceipt is AUB's answer to a delivered leak result.
//
// EditorReturnURL is the way back to the editor on the request's own saved
// revision (AUCOM #37). It is OPTIONAL and, at the time of NEW_633, no AUB
// sends it: the producer half — an editor route for an exact saved revision
// that adopts the result by request (AUP), and AUB building the address from
// its configured editor origin and the request it owns — is the contract
// recorded in docs/agent/aucom.leak-adapters.md. Without it the page keeps its
// sentence. With it, the address is used only after VerifyEditorReturn.
type LeakResultReceipt struct {
	RequestID       string `json:"request_id"`
	Received        bool   `json:"received"`
	EditorReturnURL string `json:"editor_return_url,omitempty"`
}

// Why an editor address was not used — tokens a page tells apart by code.
const (
	EditorReturnMissing         = "missing"          // the receipt names no address
	EditorReturnOtherRequest    = "other_request"    // the receipt is not this request's
	EditorReturnMalformed       = "malformed"        // not an absolute URL, or too long
	EditorReturnScheme          = "scheme"           // not http or https
	EditorReturnCredentials     = "credentials"      // carries a user or password
	EditorReturnDowngrade       = "downgrade"        // http from a server reached over https
	EditorReturnOtherMap        = "other_map"        // the path is not this map's editor route
	EditorReturnOtherRevision   = "other_revision"   // revision or leak_request disagree
	EditorReturnUnexpectedQuery = "unexpected_query" // anything else in the query, or a fragment
)

// MaxEditorReturnURL bounds the address; a real one is well under it.
const MaxEditorReturnURL = 2048

var editorSlug = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// VerifyEditorReturn decides whether raw, which AUB sent in the receipt for
// request, is the editor's address for exactly this request: it returns the
// address to open, or "" and the reason it was refused.
//
// What makes it verified: it came from the signed-in server in the answer to
// this request's own delivery (receiptRequest), and its shape names nothing
// but this request — `/edit/map/<asset id>[/<slug>]?revision=<n>&leak_request=<id>`.
// No part of it is taken from the leak link's free text, and nothing is
// derived: the origin is the server's word, checked never to be a scheme the
// page could be steered to, a credential or a downgrade from serverBase.
// serverBase empty skips only the downgrade check (a re-read of a stored
// address, which was checked against it when it arrived).
func VerifyEditorReturn(raw string, request LeakTestLink, receiptRequest, serverBase string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", EditorReturnMissing
	}
	if receiptRequest != request.RequestID || request.RequestID == "" {
		return "", EditorReturnOtherRequest
	}
	if len(raw) > MaxEditorReturnURL {
		return "", EditorReturnMalformed
	}
	address, err := url.Parse(raw)
	if err != nil || !address.IsAbs() || address.Host == "" || address.Opaque != "" {
		return "", EditorReturnMalformed
	}
	switch address.Scheme {
	case "http", "https":
	default:
		return "", EditorReturnScheme
	}
	if address.User != nil {
		return "", EditorReturnCredentials
	}
	if serverBase != "" {
		if base, err := url.Parse(serverBase); err == nil && base.Scheme == "https" && address.Scheme != "https" {
			return "", EditorReturnDowngrade
		}
	}
	segments := strings.Split(strings.TrimPrefix(address.EscapedPath(), "/"), "/")
	if len(segments) < 3 || len(segments) > 4 || segments[0] != "edit" || segments[1] != "map" ||
		segments[2] != request.AssetID || (len(segments) == 4 && !editorSlug.MatchString(segments[3])) {
		return "", EditorReturnOtherMap
	}
	if address.Fragment != "" || address.RawFragment != "" {
		return "", EditorReturnUnexpectedQuery
	}
	query, err := url.ParseQuery(address.RawQuery)
	if err != nil {
		return "", EditorReturnMalformed
	}
	for key, values := range query {
		if (key != "revision" && key != "leak_request") || len(values) != 1 {
			return "", EditorReturnUnexpectedQuery
		}
	}
	if query.Get("revision") != strconv.Itoa(request.Revision) || query.Get("leak_request") != request.RequestID {
		return "", EditorReturnOtherRevision
	}
	return address.String(), ""
}
