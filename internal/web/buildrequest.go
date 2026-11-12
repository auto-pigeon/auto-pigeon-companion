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
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
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
// in it since NEW_504 (AUCOM #28): its request is built — the same verified
// revision, conversion and Q3Map2 stages — and the build ends at the BSP.
// Packaging, installing and running it are separate actions under the build's
// result (errPlayIsNotForQuake3), so a Quake III Build & Run request is built
// and then handed to that panel, never installed as a loose level and never
// built as Quake 1.
var buildRequestGames = []string{"quake1", "quake2", "quake3"}

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

// pendingBuildRequests is every request on this machine, oldest first.
func (s *Server) pendingBuildRequests() ([]buildintent.Pending, error) {
	dir, err := s.configDir()
	if err != nil {
		return nil, err
	}
	return buildintent.List(buildintent.Path(dir), time.Now().UTC())
}

// pendingBuildRequest is the pending request with this id, or nil. Every
// question about a request names it (NEW_502): several may be pending, and an
// answer about one is never an answer about another.
func (s *Server) pendingBuildRequest(requestID string) (*aub.BuildRequestLink, time.Time, error) {
	dir, err := s.configDir()
	if err != nil {
		return nil, time.Time{}, err
	}
	item, err := buildintent.Get(buildintent.Path(dir), requestID, time.Now().UTC())
	if err != nil || item == nil {
		return nil, time.Time{}, err
	}
	request := item.Request
	return &request, item.ReceivedAt, nil
}

// buildRequestEntry is one pending request as the watch shows it.
func (s *Server) buildRequestEntry(item buildintent.Pending) map[string]any {
	return map[string]any{
		"request_id": item.Request.RequestID, "intent": item.Request.Intent, "run_after": item.Request.RunAfter(),
		"received_at": item.ReceivedAt, "relay": s.buildReqs.view(item.Request.RequestID),
	}
}

// handleBuildRequestLocal is every pending request as this machine holds it,
// oldest first, and nothing else: no AUB call, so an open page can ask every
// couple of seconds whether a link arrived. The newest is also said at the top
// level, as before NEW_502, for a page that knows only one.
func (s *Server) handleBuildRequestLocal(w http.ResponseWriter, _ *http.Request) {
	list, err := s.pendingBuildRequests()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(list) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"pending": false, "requests": []any{}})
		return
	}
	entries := make([]map[string]any, 0, len(list))
	for _, item := range list {
		entries = append(entries, s.buildRequestEntry(item))
	}
	answer := map[string]any{"pending": true, "requests": entries}
	for key, value := range entries[len(entries)-1] {
		answer[key] = value
	}
	writeJSON(w, http.StatusOK, answer)
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
		if received, by, err := buildintent.Receipt(buildintent.Path(dir), link.RequestID, time.Now().UTC()); err == nil && !received.IsZero() {
			s.acknowledgeHandoff(aub.HandoffBuild, link.RequestID, request.MapID, request.Revision, request.ContentSHA256, received, by)
		}
	}
	if request.Intent != link.Intent {
		return blocked(buildStageIntentMismatch, fmt.Errorf(
			"the link asked for %q and the account server holds %q for this request; ask the editor again", link.Intent, request.Intent))
	}
	if request.State == aub.BuildRequestBlocked && request.Stage == aub.BuildStageBuildingElsewhere {
		// The editor opened this request again (a new delivery id) and a
		// build of it started from an earlier link meanwhile (AUB NEW_368):
		// one request is one build, so nothing is offered here.
		return buildResolution{Status: http.StatusConflict, Code: "building_elsewhere",
			Err: errors.New("this request is already being built from an earlier link the editor opened; nothing was started here")}
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

// handleBuildRequestPending resolves one pending request against AUB: the one
// the page names, or — for a page that names none — the newest.
func (s *Server) handleBuildRequestPending(w http.ResponseWriter, r *http.Request) {
	named := r.URL.Query().Get("request_id")
	var (
		request  *aub.BuildRequestLink
		received time.Time
		err      error
	)
	if named != "" {
		request, received, err = s.pendingBuildRequest(named)
	} else {
		var list []buildintent.Pending
		if list, err = s.pendingBuildRequests(); err == nil && len(list) > 0 {
			newest := list[len(list)-1]
			request, received = &newest.Request, newest.ReceivedAt
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if request == nil {
		// The named request is no longer pending — started, dismissed or
		// expired. Another request that may be pending is not it, and nothing
		// about that one is said under this one's name.
		answer := map[string]any{"pending": false}
		if named != "" {
			answer["request_id"] = named
		}
		writeJSON(w, http.StatusOK, answer)
		return
	}
	base := map[string]any{"pending": true, "request_id": request.RequestID, "intent": request.Intent,
		"run_after": request.RunAfter(), "received_at": received}
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
	request, _, err := s.pendingBuildRequest(body.RequestID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if request == nil || body.RequestID == "" {
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
	pending, _, err := s.pendingBuildRequest(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if pending == nil || id == "" {
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
	// A Quake III request is built and ends at its BSP whichever the editor
	// asked for (NEW_504): its Build & Run is that build, then the package
	// panel's separate actions. Every other game is held to the request.
	family := s.playFamily(body.Pipeline)
	if family != "" && family != facts.Game {
		// Never built as another game (a Quake III map through a Quake 1
		// profile converts and then fails in a compiler that cannot read it).
		refuse("build_request_mismatch", fmt.Errorf("the editor's revision is a %s map and this build profile makes %s "+
			"maps; choose a %s build profile. Nothing was started",
			maturity.GameName(facts.Game), maturity.GameName(family), maturity.GameName(facts.Game)))
		return
	}
	quake3 := family == q3pack.FamilyQuake3
	if quake3 && !body.BuildOnly {
		refuse("build_request_mismatch", errPlayIsNotForQuake3)
		return
	}
	if !quake3 && !body.BuildOnly != pending.RunAfter() {
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
