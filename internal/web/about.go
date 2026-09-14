package web

import (
	"fmt"
	"io/fs"
	"net/http"
)

// The About area: what this program is, in the same words the web site and the
// editor use.
//
// # Why the server serves a file it does not parse
//
// `content/about.md` lives in auto-pigeon-gallery, which owns the grammar for
// it — frontmatter required, heading anchors kept, every link classified. That
// grammar is implemented **once**, by that repository's own emitter, and what
// arrives here is the tree it produced: `internal/web/assets/about.json`,
// written by `auto-pigeon-tools/scripts/aup/about/emit.py --vendor`, which also
// recomputes the artefact's digest from the source bytes before copying it.
//
// This handler therefore reads bytes and writes bytes. Parsing Markdown, or
// even decoding this JSON, on the Go side would be a second reading of one
// document — and in this repository it would also mean the first external
// dependency, which is a security property here and not a style preference
// (`internal/threat`). The rendering belongs to `assets/about.js`, which is
// where every other area's presentation already lives.
//
// # Why it is a guarded route and not a static asset
//
// The static file server serves everything under `/` without a token, because a
// browser has none until it has loaded the page. The About prose is not
// sensitive, but a route with a token is the shape every other piece of content
// in this program has, and the page already holds the token; making this one
// file the exception would be the start of a second class of asset.
func (s *Server) aboutAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/about": s.handleAbout,
	}
}

// aboutAsset is where the emitted artefact lives, inside the embedded assets.
//
// It is embedded by the same `//go:embed assets` directive as the frontend, so
// a binary built without it is a binary built from a tree the emitter has not
// run in — which the test suite refuses rather than this handler papering over.
const aboutAsset = "about.json"

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	raw, err := fs.ReadFile(assetsFS(), aboutAsset)
	if err != nil {
		// Said plainly, because the alternative is an empty About area and no
		// reason given for it.
		writeError(w, http.StatusInternalServerError, fmt.Errorf(
			"this build has no About content: assets/%s is not embedded (%w). "+
				"Run `python3 scripts/aup/about/emit.py --tools-dir <aut> --vendor <companion>` "+
				"in auto-pigeon-tools and rebuild", aboutAsset, err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// The page re-reads on every visit rather than caching what the server
	// said (`app.js`), and an artefact that changed under a rebuilt binary
	// should not be answered from a stale cache.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
