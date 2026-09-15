package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Auto-Pigeon mark in the header and as the favicon (NEW_244D, at the
// operator's request: "the logo and favicon like in AUG").
//
// It is a byte-for-byte copy of the file AUP holds and AUG copies, for AUG's
// reason (`auto-pigeon-gallery/src/assets/brand/BRAND.md`): AUP is the
// authority for the artwork, and a copy with a pinned digest is what keeps it
// from drifting without a shared package. The artwork is the project owner's
// and is not relicensed by being shipped beside this program's MIT code —
// README.md says so.
const (
	brandMarkPath   = "brand/auto-pigeon-long-tail-transparent-no-padding-128x128.png"
	brandMarkSHA256 = "0abc503cc854124965f24c7001cdd6c42e0a95f3f0ec10be5196b3af513d6ee3"
)

func TestTheBrandMarkIsThePinnedCopyOfAUPs(t *testing.T) {
	raw, err := fs.ReadFile(assetsFS(), brandMarkPath)
	if err != nil {
		t.Fatalf("the mark is not embedded: %v", err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != brandMarkSHA256 {
		t.Fatalf("the embedded mark is %s, pinned %s: re-copy it from AUP, never edit it", got, brandMarkSHA256)
	}
	// And the same as AUP's, when a workspace checkout is beside this one.
	aup := filepath.Join("..", "..", "..", "auto-pigeon", "frontend", "src", "assets", filepath.FromSlash(brandMarkPath))
	theirs, err := os.ReadFile(aup)
	if err != nil {
		t.Logf("no auto-pigeon checkout beside this one; the mark was checked against its pin only")
		return
	}
	if sha256.Sum256(theirs) != sum {
		t.Errorf("AUP's mark has changed; re-copy it and move the pin")
	}
}

func TestThePageShowsTheMarkAndUsesItAsTheFavicon(t *testing.T) {
	server, _ := newTestServer(t, nil)
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = testHost
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, r)
		return recorder
	}
	page := get("/").Body.String()
	for _, want := range []string{`rel="icon"`, `class="brand-mark"`, brandMarkPath} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not carry %s", want)
		}
	}
	mark := get("/" + brandMarkPath)
	if mark.Code != http.StatusOK || !strings.HasPrefix(mark.Header().Get("Content-Type"), "image/png") {
		t.Errorf("GET the mark = %d %q", mark.Code, mark.Header().Get("Content-Type"))
	}
}
