package aub

import (
	"strings"
	"testing"
)

const leakDigest = "c303f8248626c39233029796b2c9e2d3d7f94e789526eaab76d1268a8288af85"

func TestALeakTestLinkRoundTrips(t *testing.T) {
	raw := "autopigeon://leaktest/map/abc123def456ghi?revision=7&sha256=" + leakDigest
	link, err := ParseLeakTestLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if link != (LeakTestLink{AssetID: "abc123def456ghi", Revision: 7, ContentSHA256: leakDigest}) {
		t.Fatalf("parsed %+v", link)
	}
	if link.Link() != raw || !IsLeakTestLink(raw) {
		t.Fatalf("canonical spelling %q", link.Link())
	}
}

func TestALeakTestLinkCarriesOneOpaqueReturnRequest(t *testing.T) {
	requestID := strings.Repeat("a", 32)
	link := LeakTestLink{AssetID: "abc123def456ghi", Revision: 7,
		ContentSHA256: leakDigest, RequestID: requestID}
	parsed, err := ParseLeakTestLink(link.Link())
	if err != nil || parsed != link {
		t.Fatalf("parsed %+v: %v", parsed, err)
	}
}

// Every part of the link is later sent to AUB, so every part is refused here
// when it is anything but the one shape the editor writes.
func TestALeakTestLinkRefusesEverythingElse(t *testing.T) {
	base := "autopigeon://leaktest/map/abc"
	for _, raw := range []string{
		"",
		"https://leaktest/map/abc?revision=1&sha256=" + leakDigest,
		"autopigeon://join/abc",
		base,
		base + "?revision=1",
		base + "?sha256=" + leakDigest,
		base + "?revision=0&sha256=" + leakDigest,
		base + "?revision=-3&sha256=" + leakDigest,
		base + "?revision=1e3&sha256=" + leakDigest,
		base + "?revision=1000000001&sha256=" + leakDigest,
		base + "?revision=1&sha256=" + strings.ToUpper(leakDigest),
		base + "?revision=1&sha256=abc",
		base + "?revision=1&revision=2&sha256=" + leakDigest,
		base + "?revision=1&sha256=" + leakDigest + "&next=https://example.test",
		base + "?revision=1&sha256=" + leakDigest + "&request=short",
		base + "?revision=1&sha256=" + leakDigest + "&request=" + strings.Repeat("A", 32),
		base + "?revision=1&sha256=" + leakDigest + "&request=" + strings.Repeat("a", 32) + "&request=" + strings.Repeat("b", 32),
		base + "?revision=1&sha256=" + leakDigest + "#x",
		"autopigeon://leaktest/map/../x?revision=1&sha256=" + leakDigest,
		"autopigeon://leaktest/map/a/b?revision=1&sha256=" + leakDigest,
		"autopigeon://leaktest/map/user@abc?revision=1&sha256=" + leakDigest,
	} {
		if _, err := ParseLeakTestLink(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
