package aub

import (
	"strings"
	"testing"
)

// AUCOM #37 (NEW_633): an editor address from AUB's receipt is opened only
// when it names exactly this request. Everything else is refused with a reason
// a page can tell apart, and nothing is derived from the leak link.
func TestAnEditorReturnIsUsedOnlyWhenItNamesExactlyThisRequest(t *testing.T) {
	request := LeakTestLink{AssetID: "map-one", Revision: 7, ContentSHA256: strings.Repeat("1", 64), RequestID: strings.Repeat("a", 32)}
	id := request.RequestID
	good := "https://editor.example.test/edit/map/map-one/my-map?revision=7&leak_request=" + id
	cases := []struct {
		name, raw, receipt, server, refused string
	}{
		{"verified with slug", good, id, "https://aub.example.test", ""},
		{"verified without slug", "http://192.0.2.10:5173/edit/map/map-one?leak_request=" + id + "&revision=7", id, "http://192.0.2.10:9190", ""},
		{"missing", "", id, "", EditorReturnMissing},
		{"another request's receipt", good, strings.Repeat("b", 32), "", EditorReturnOtherRequest},
		{"relative", "/edit/map/map-one?revision=7&leak_request=" + id, id, "", EditorReturnMalformed},
		{"protocol relative", "//evil.example.test/edit/map/map-one?revision=7&leak_request=" + id, id, "", EditorReturnMalformed},
		{"javascript", "javascript:alert(1)//edit/map/map-one", id, "", EditorReturnMalformed},
		{"file scheme", "file:///edit/map/map-one?revision=7&leak_request=" + id, id, "", EditorReturnMalformed},
		{"custom scheme", "autopigeon://edit/map/map-one?revision=7&leak_request=" + id, id, "", EditorReturnScheme},
		{"credentials", "https://user:pw@editor.example.test/edit/map/map-one?revision=7&leak_request=" + id, id, "", EditorReturnCredentials},
		{"downgrade", "http://editor.example.test/edit/map/map-one?revision=7&leak_request=" + id, id, "https://aub.example.test", EditorReturnDowngrade},
		{"another map", "https://editor.example.test/edit/map/map-two?revision=7&leak_request=" + id, id, "", EditorReturnOtherMap},
		{"another route", "https://editor.example.test/admin/map/map-one?revision=7&leak_request=" + id, id, "", EditorReturnOtherMap},
		{"a deeper path", "https://editor.example.test/edit/map/map-one/slug/extra?revision=7&leak_request=" + id, id, "", EditorReturnOtherMap},
		{"an odd slug", "https://editor.example.test/edit/map/map-one/..%2F..?revision=7&leak_request=" + id, id, "", EditorReturnOtherMap},
		{"another revision", "https://editor.example.test/edit/map/map-one?revision=8&leak_request=" + id, id, "", EditorReturnOtherRevision},
		{"another request in the address", "https://editor.example.test/edit/map/map-one?revision=7&leak_request=" + strings.Repeat("c", 32), id, "", EditorReturnOtherRevision},
		{"no revision", "https://editor.example.test/edit/map/map-one?leak_request=" + id, id, "", EditorReturnOtherRevision},
		{"an extra parameter", good + "&next=https://evil.example.test", id, "", EditorReturnUnexpectedQuery},
		{"a repeated parameter", good + "&revision=7", id, "", EditorReturnUnexpectedQuery},
		{"a fragment", good + "#x", id, "", EditorReturnUnexpectedQuery},
		{"too long", good + strings.Repeat("x", MaxEditorReturnURL), id, "", EditorReturnMalformed},
	}
	for _, tc := range cases {
		address, refused := VerifyEditorReturn(tc.raw, request, tc.receipt, tc.server)
		if refused != tc.refused {
			t.Errorf("%s: refused %q, want %q (address %q)", tc.name, refused, tc.refused, address)
		}
		if (refused == "") != (address != "") {
			t.Errorf("%s: an address %q came with the refusal %q", tc.name, address, refused)
		}
	}
}
