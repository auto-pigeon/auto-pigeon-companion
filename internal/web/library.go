package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
)

// The Library area: what is on the backend, and what is on this machine.
//
// # Two lists, always distinguishable
//
// A catalogue entry is something AUB is willing to serve; a cached revision is
// bytes this machine has already verified. They are separate routes and they
// stay separate in the page, because they answer different questions and one of
// them keeps working with the network unplugged. A single merged list would
// have to invent a state for "listed but not here", and the moment a build
// depended on that state it would be a build that fetched something the user
// thought they already had.
//
// # Nothing here chooses a version
//
// The page names an exact revision, and it is that revision the sync fetches
// and the build later reads out of the cache. `current` resolves at the server
// and the RECORD says which version it turned out to be — see
// [assetsync.Syncer.Sync]. That is what makes "the map does not change under
// the build you started" a property of the layout rather than a rule.

func (s *Server) libraryAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/library/capabilities":             s.handleLibraryCapabilities,
		"GET /api/v1/library/catalog":                  s.handleLibraryCatalog,
		"GET /api/v1/library/assets/{type}/{id}":       s.handleLibraryAsset,
		"GET /api/v1/library/assets/{type}/{id}/{rev}": s.handleLibraryRevision,
		"POST /api/v1/library/sync":                    s.handleLibrarySync,
		"GET /api/v1/library/cached":                   s.handleLibraryCached,
	}
}

// aubStatus maps a backend refusal to a status the page can act on.
//
// The two a user has to be told apart are a rejected session and an asset that
// is gone, because the remedies differ: sign in again, or ask whoever owns it.
// Everything else keeps the backend's own code — this program does not invent
// reasons for another service's answers.
func aubStatus(err error) int {
	var apiErr *aub.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Unauthorized():
			return http.StatusUnauthorized
		case apiErr.StatusCode == http.StatusNotFound:
			return http.StatusNotFound
		case apiErr.StatusCode == http.StatusForbidden:
			return http.StatusForbidden
		}
	}
	if errors.Is(err, assetsync.ErrNotCached) {
		return http.StatusNotFound
	}
	if errors.Is(err, assetsync.ErrDigestMismatch) {
		// 502: the bytes that arrived are not the bytes that were declared.
		// Nothing about the request was wrong, and nothing the user can retype
		// will fix it.
		return http.StatusBadGateway
	}
	return http.StatusBadGateway
}

// requireSession reports the AUB client only when it actually carries one.
//
// Separate from requireClient because "no address configured" and "not signed
// in" are two different first-run states with two different next steps, and a
// page that showed one message for both would send half its users to the wrong
// screen.
func (s *Server) requireSession(w http.ResponseWriter) (*aub.Client, bool) {
	client, ok := s.requireClient(w)
	if !ok {
		return nil, false
	}
	if !client.Authenticated() {
		writeError(w, http.StatusUnauthorized,
			errors.New("not signed in: sign in to auto-pigeon-backend to see your maps"))
		return nil, false
	}
	return client, true
}

func (s *Server) handleLibraryCapabilities(w http.ResponseWriter, r *http.Request) {
	client, ok := s.requireSession(w)
	if !ok {
		return
	}
	capabilities, err := client.Capabilities(r.Context())
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, capabilities)
}

func (s *Server) handleLibraryCatalog(w http.ResponseWriter, r *http.Request) {
	client, ok := s.requireSession(w)
	if !ok {
		return
	}
	query := aub.CatalogQuery{
		Scope:  r.URL.Query().Get("scope"),
		Game:   r.URL.Query().Get("game"),
		Name:   r.URL.Query().Get("name"),
		Cursor: r.URL.Query().Get("cursor"),
	}
	if types, present := r.URL.Query()["type"]; present {
		query.Types = types
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("limit=%q is not a count", raw))
			return
		}
		query.Limit = limit
	}
	page, err := client.Catalog(r.Context(), query)
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleLibraryAsset is one asset and its version history in one response.
//
// One round trip because the page shows them together: nobody picks an asset
// without then picking which version of it, and two requests would mean a
// moment in which the list has an asset selected and no versions under it.
func (s *Server) handleLibraryAsset(w http.ResponseWriter, r *http.Request) {
	client, ok := s.requireSession(w)
	if !ok {
		return
	}
	assetType, assetID := r.PathValue("type"), r.PathValue("id")
	detail, err := client.Asset(r.Context(), assetType, assetID)
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("limit=%q is not a count", raw))
			return
		}
		limit = parsed
	}

	body := map[string]any{"asset": detail.Asset, "revision_count": detail.RevisionCount}
	history, err := client.History(r.Context(), assetType, assetID, limit, 0)
	if err != nil {
		// A type with no per-version history is a normal deployment, not a
		// failure of this request: the asset is still usable, and the page
		// shows its current revision. Reported alongside rather than instead.
		body["history_error"] = err.Error()
	} else {
		body["revisions"] = history.Items
		body["revision_addressing"] = history.RevisionAddressing
		body["has_more"] = history.HasMore
	}
	body["cached"] = s.cachedKeys(assetType, assetID)
	writeJSON(w, http.StatusOK, body)
}

// handleLibraryRevision is one revision with its file list, so the page can
// show what a sync would fetch before it fetches it.
func (s *Server) handleLibraryRevision(w http.ResponseWriter, r *http.Request) {
	client, ok := s.requireSession(w)
	if !ok {
		return
	}
	revision, err := client.Revision(r.Context(),
		r.PathValue("type"), r.PathValue("id"), r.PathValue("rev"))
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, revision)
}

// cachedKeys is the cache keys this machine already holds for one asset, so the
// page can mark a revision "already here" without a second request.
func (s *Server) cachedKeys(assetType, assetID string) []string {
	store, err := s.assets()
	if err != nil {
		return nil
	}
	records, err := store.Revisions(assetType, assetID)
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(records))
	for _, record := range records {
		keys = append(keys, record.Key())
	}
	return keys
}

// handleLibrarySync fetches one exact revision into the local cache.
//
// Synchronous, and it can take a while. That is deliberate: the evidence the
// user is given afterwards is the cached-revision list, which is read off the
// disk and survives a reload — not a message that appears and goes. A sync that
// is interrupted leaves objects and no record, and running it again completes
// without re-downloading what already landed.
func (s *Server) handleLibrarySync(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AssetType string `json:"asset_type"`
		AssetID   string `json:"asset_id"`
		// Revision is an exact id. Empty or `current` asks the server which
		// version is current and records the answer.
		Revision string `json:"revision,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.AssetType == "" || request.AssetID == "" {
		writeError(w, http.StatusBadRequest,
			errors.New("a sync needs an asset type and an asset id"))
		return
	}
	client, ok := s.requireSession(w)
	if !ok {
		return
	}
	store, err := s.assets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	syncer, err := assetsync.NewSyncer(r.Context(), client, store)
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	result, err := syncer.Sync(r.Context(), request.AssetType, request.AssetID, request.Revision)
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"record":           result.Record,
		"key":              result.Record.Key(),
		"fetched":          result.Fetched,
		"reused":           result.Reused,
		"bytes_fetched":    result.BytesFetched,
		"already_complete": result.AlreadyComplete,
	})
}

// handleLibraryCached lists what this machine holds. No session, no network:
// this is the list a Companion with no connection still has.
func (s *Server) handleLibraryCached(w http.ResponseWriter, r *http.Request) {
	store, err := s.assets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	records, err := store.Revisions(r.URL.Query().Get("type"), r.URL.Query().Get("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, map[string]any{"key": record.Key(), "record": record})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "root": store.Root()})
}
