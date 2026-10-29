package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/buildintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// The editor's normal build request: `autopigeon://build/request/<id>`.
//
// The URI handler only records the link (internal/buildintent). These routes
// resolve it against AUB with the Companion's own session, check that the
// revision it names is the exact immutable one, read the game out of that
// revision's own bytes, and hand it to Build & Run's existing review. Nothing
// here starts anything: the person presses Build or Build & Run, and that one
// start — POST /api/v1/play/runs with `build_request_id` — consumes the request.
//
// It is not a leak test and shares none of the leak flow's files, routes,
// statuses or pipelines. The build it makes is an ordinary saved-map build, so
// it is the "latest playable build" like any other (build.Latest).
//
// All routes are behind Server.guard.
func (s *Server) buildRequestAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/build-request/request":  s.handleBuildRequestLocal,
		"GET /api/v1/build-request/pending":  s.handleBuildRequestPending,
		"POST /api/v1/build-request/review":  s.handleBuildRequestReview,
		"POST /api/v1/build-request/dismiss": s.handleBuildRequestDismiss,
	}
}

// buildRequestGames is what Build & Run builds from a saved map. Quake III is
// not in it: Build & Run refuses a Quake III build profile (errPlayIsNotForQuake3),
// so a request for one is answered "unsupported" here instead of being taken
// into a review that could only refuse it — and never built as Quake 1.
var buildRequestGames = []string{"quake1", "quake2"}

func buildRequestGameSupported(game string) bool {
	for _, supported := range buildRequestGames {
		if game == supported {
			return true
		}
	}
	return false
}

// buildRequestTools says which build profile the page prefers when a request
// is adopted and nothing is chosen yet: one whose every stage runs the
// Auto-Pigeon build of the game's compiler, else the game's built-in normal
// build. A preference only — every ready profile stays offered.
func buildRequestTools(game string) (tool, normal string) {
	switch game {
	case "quake1":
		return builtin.EricwAutoPigeonQ1, builtin.Q1Normal
	case "quake2":
		return builtin.EricwAutoPigeonQ2, builtin.Q2Normal
	case "quake3":
		return builtin.Q3Map2AutoPigeon, builtin.Q3Normal
	}
	return "", ""
}

// pendingBuildRequest is the request on this machine, or nil.
func (s *Server) pendingBuildRequest() (*aub.BuildRequestLink, time.Time, error) {
	dir, err := s.configDir()
	if err != nil {
		return nil, time.Time{}, err
	}
	return buildintent.Read(buildintent.Path(dir), time.Now().UTC())
}

// handleBuildRequestLocal is the pending request as this machine holds it, and
// nothing else: no AUB call, so an open page can ask every couple of seconds
// whether a link arrived.
func (s *Server) handleBuildRequestLocal(w http.ResponseWriter, _ *http.Request) {
	request, received, err := s.pendingBuildRequest()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if request == nil {
		writeJSON(w, http.StatusOK, map[string]any{"pending": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pending": true, "request_id": request.RequestID, "intent": request.Intent,
		"run_after": request.RunAfter(), "received_at": received, "relay": s.buildReqs.view(request.RequestID),
	})
}

// buildResolution is what asking AUB about a request came to. Exactly one of
// Facts, SignIn, NotFound, Unsupported and Err is set.
type buildResolution struct {
	Facts       *buildRequestFacts
	SignIn      bool
	NotFound    bool
	Unsupported string // the game, when it is one Build & Run does not build
	Name        string
	Status      int
	Code        string
	Err         error
}

// resolveBuildRequest asks AUB what a request is about, with this Companion's
// own session, and checks it: the intent the link carried, the exact immutable
// revision by this Companion's own read of it, and the game from the pinned
// bytes. A refusal is said to the editor as `blocked`, with a token for why.
func (s *Server) resolveBuildRequest(ctx context.Context, link aub.BuildRequestLink) buildResolution {
	s.adoptSessionFromDisk()
	client := s.aubClient()
	if client == nil || !client.Authenticated() || client.SessionExpired(time.Now()) {
		return buildResolution{SignIn: true}
	}
	failed := func(err error) buildResolution {
		var refused *aub.APIError
		if errors.As(err, &refused) && refused.Unauthorized() && refused.Reason != reasonLegalAcceptanceRequired {
			return buildResolution{SignIn: true}
		}
		return buildResolution{Status: aubStatus(err), Err: err}
	}
	blocked := func(stage string, err error) buildResolution {
		s.buildReqs.report(link, aub.BuildRequestBlocked, stage, "", "")
		return buildResolution{Status: http.StatusConflict, Code: "blocked", Err: err}
	}

	request, err := client.BuildRequest(ctx, link.RequestID)
	if err != nil {
		var refused *aub.APIError
		if errors.As(err, &refused) && refused.StatusCode == http.StatusNotFound {
			// Unknown, expired, another account's, or a map no longer
			// readable: AUB says them alike on purpose. The intent is kept,
			// so a sign-in to the right account or a retry can resolve it.
			return buildResolution{NotFound: true}
		}
		return failed(err)
	}
	// This server has the request: it says so, and what it is, whatever it
	// then makes of it (NEW_368).
	if dir, err := s.configDir(); err == nil {
		if received, by, err := buildintent.Receipt(buildintent.Path(dir), time.Now().UTC()); err == nil {
			s.acknowledgeHandoff(aub.HandoffBuild, link.RequestID, request.MapID, request.Revision, request.ContentSHA256, received, by)
		}
	}
	if request.Intent != link.Intent {
		return blocked(buildStageIntentMismatch, fmt.Errorf(
			"the link asked for %q and the account server holds %q for this request; ask the editor again", link.Intent, request.Intent))
	}
	if aub.BuildRequestTerminal(request.State) || request.State == aub.BuildRequestBlocked {
		// Nothing is posted: AUB stores nothing after an ending.
		return buildResolution{Status: http.StatusConflict, Code: "closed",
			Err: fmt.Errorf("this request is already %s; ask the editor again", request.State)}
	}
	revision, err := client.Revision(ctx, aub.AssetTypeMap, request.MapID, request.RevisionID)
	if err != nil {
		return failed(err)
	}
	if revision.AssetID != request.MapID || revision.AssetType != aub.AssetTypeMap || revision.ID != request.RevisionID ||
		revision.Number != request.Revision || revision.ContentSHA256 != request.ContentSHA256 || !revision.Immutable {
		return blocked(buildStageRevisionMismatch, errors.New(
			"the saved map revision is not the one the request names, or cannot be pinned; ask the editor again"))
	}
	var source string
	for _, file := range revision.Files {
		if strings.EqualFold(filepath.Ext(file.Path), ".apmap") {
			if source != "" {
				return blocked(buildStageNoSource, errors.New("this revision has more than one APMap source"))
			}
			source = file.Path
		}
	}
	if source == "" {
		return blocked(buildStageNoSource, errors.New("this revision has no APMap source"))
	}
	game, err := s.savedMapGame(ctx, client, request.MapID, revision.ID, source)
	if err != nil {
		return buildResolution{Status: aubStatus(err), Err: fmt.Errorf("reading this revision's game: %w", err)}
	}
	if request.Game != "" && request.Game != game {
		return blocked(buildStageProfileMismatch, fmt.Errorf(
			"the editor asked for a %s build, and this saved revision is a %s map; ask the editor again", request.Game, game))
	}
	name := request.MapName
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	if !buildRequestGameSupported(game) {
		s.buildReqs.report(link, aub.BuildRequestBlocked, buildStageUnsupported, "", "")
		return buildResolution{Unsupported: game, Name: name}
	}
	facts := buildRequestFacts{Link: link, AssetID: request.MapID, Revision: revision.Number, RevisionID: revision.ID,
		ContentSHA256: revision.ContentSHA256, SourceFile: source, Name: name, Game: game, ExpiresAt: request.ExpiresAt}
	s.buildReqs.resolvedAs(facts, request.Sequence, request.State)
	return buildResolution{Facts: &facts}
}

// handleBuildRequestPending resolves the pending request against AUB.
func (s *Server) handleBuildRequestPending(w http.ResponseWriter, r *http.Request) {
	request, received, err := s.pendingBuildRequest()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if request == nil {
		writeJSON(w, http.StatusOK, map[string]any{"pending": false})
		return
	}
	base := map[string]any{"pending": true, "request_id": request.RequestID, "intent": request.Intent,
		"run_after": request.RunAfter(), "received_at": received}
	// A page names the request it is asking about. When another replaced it
	// meanwhile the answer says so and resolves nothing: facts about B shown
	// under A would be facts about the wrong map.
	if named := r.URL.Query().Get("request_id"); named != "" && named != request.RequestID {
		base["replaced"] = true
		writeJSON(w, http.StatusOK, base)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	resolution := s.resolveBuildRequest(ctx, *request)
	base["relay"] = s.buildReqs.view(request.RequestID)
	switch {
	case resolution.SignIn:
		base["sign_in_required"] = true
	case resolution.NotFound:
		base["not_found"] = true
	case resolution.Err != nil:
		writeJSON(w, resolution.Status, errorBody{Error: resolution.Err.Error(), Code: resolution.Code})
		return
	case resolution.Unsupported != "":
		base["unsupported"], base["game_profile"], base["supported_profiles"] = true, resolution.Unsupported, buildRequestGames
		base["name"] = resolution.Name
	default:
		facts := resolution.Facts
		// The editor is told once that the request arrived and was found.
		s.buildReqs.receivedOnce(*request)
		base["relay"] = s.buildReqs.view(request.RequestID)
		tool, normal := buildRequestTools(facts.Game)
		for key, value := range map[string]any{
			"asset_id": facts.AssetID, "revision": facts.Revision, "revision_id": facts.RevisionID,
			"content_sha256": facts.ContentSHA256, "source_file": facts.SourceFile, "name": facts.Name,
			"game_profile": facts.Game, "expires_at": facts.ExpiresAt,
			"preferred_tool": tool, "normal_pipeline": normal,
		} {
			base[key] = value
		}
	}
	writeJSON(w, http.StatusOK, base)
}

// handleBuildRequestReview records that a person took the request into the
// review. It starts nothing.
func (s *Server) handleBuildRequestReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	request, _, err := s.pendingBuildRequest()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if request == nil || body.RequestID == "" || request.RequestID != body.RequestID {
		writeError(w, http.StatusConflict, errors.New("that build request is no longer pending; ask the editor again"))
		return
	}
	s.buildReqs.report(*request, aub.BuildRequestReviewing, "", "", "")
	writeJSON(w, http.StatusOK, map[string]any{"reviewing": true})
}

// handleBuildRequestDismiss forgets the request a person dismissed — that one,
// named by the page, so a newer click is not the one forgotten — and tells the
// editor it was cancelled.
func (s *Server) handleBuildRequestDismiss(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	dir, err := s.configDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	dismissed, err := buildintent.Dismiss(buildintent.Path(dir), body.RequestID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if dismissed != nil {
		s.buildReqs.report(*dismissed, aub.BuildRequestCancelled, buildStageDismissed, "", "")
	}
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": dismissed != nil})
}

// startForBuildRequest is a Build & Run start that names the editor's request.
//
// It starts only the exact request that is pending, for the exact revision it
// was resolved to, and only what the editor asked for: a build-only request
// never starts an engine. One start per request — the check, the start and the
// consuming of the intent are one step — and a second press for the same
// request is pointed at the run the first one started. A start refused here
// starts nothing and leaves the request pending.
func (s *Server) startForBuildRequest(w http.ResponseWriter, r *http.Request, body playRequestBody) {
	relay := s.buildReqs
	relay.starting.Lock()
	defer relay.starting.Unlock()
	id := body.BuildRequestID
	refuse := func(code string, err error) {
		writeJSON(w, http.StatusConflict, errorBody{Error: err.Error(), Code: code})
	}
	if runID := relay.startedRun(id); runID != "" {
		if s.playRunLive(runID) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": playrun.ErrAlreadyRunning.Error(),
				"code": "already_running", "run_id": runID})
			return
		}
		refuse("build_request_used", errors.New("this editor request was already built; nothing was started. Press Build in the editor again for another build"))
		return
	}
	pending, _, err := s.pendingBuildRequest()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if pending == nil || pending.RequestID != id {
		refuse("build_request_mismatch", errors.New("that editor request is no longer pending; nothing was started"))
		return
	}
	facts := relay.resolved(id)
	if facts == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		resolution := s.resolveBuildRequest(ctx, *pending)
		cancel()
		if facts = resolution.Facts; facts == nil {
			refuse("build_request_unresolved", errors.New("the editor's request could not be checked with the account server; nothing was started"))
			return
		}
	}
	if (body.AssetType != "" && body.AssetType != aub.AssetTypeMap) || body.AssetID != facts.AssetID ||
		body.RevisionID != facts.RevisionID || body.RevisionNumber != facts.Revision {
		refuse("build_request_mismatch", errors.New("this is not the map revision the editor asked to build; nothing was started"))
		return
	}
	if !body.BuildOnly != pending.RunAfter() {
		if pending.RunAfter() {
			refuse("build_request_mismatch", errors.New("the editor asked for Build & Run; switch Run after build on to build this request"))
		} else {
			refuse("build_request_mismatch", errors.New("the editor asked for a build only, so it never starts a game; switch Run after build off to build this request"))
		}
		return
	}
	request, status, err := s.playRequest(body)
	if err != nil {
		writeError(w, status, err)
		return
	}
	service, err := s.playService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	record, err := s.admitPlay(request, func() (*playrun.Record, error) { return service.Start(request) })
	if s.refusedDuplicate(w, err) {
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// The run exists: the request is its, whatever happens to the intent now.
	if dir, err := s.configDir(); err == nil {
		if _, err := buildintent.Consume(buildintent.Path(dir), id, time.Now().UTC()); err != nil {
			s.logf("build request %s: the started request could not be removed from this machine: %v", shortLeakID(id), err)
		}
	}
	relay.startedAs(*pending, record.ID)
	relay.report(*pending, aub.BuildRequestBuilding, string(record.State), record.BuildID, record.ID)
	go s.watchBuildRequestRun(*pending, record.ID)
	writeJSON(w, http.StatusAccepted, playView(record, time.Now().UTC()))
}
