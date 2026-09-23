package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/hostgame"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joincontent"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joinintent"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joinready"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/publish"
)

// The Games area — `AUB/AUG/AUCOM/AUT 244F`.
//
// # One model, rendered
//
// Every answer about whether somebody can join is internal/joinready's report,
// computed per request through internal/hostgame's Joiner — the same one the CLI
// uses. This file adds no state of its own about readiness; what it keeps is what
// a page needs across requests: the reviews waiting for an approval, and the
// process-wide coordination that makes a double click one game.
//
// # What it reaches, and what it never does
//
// It reads AUB's listing and detail, installs an exact engine-profile publication
// through internal/publish (the one install path, recording `community` trust),
// downloads join content through internal/joincontent into the asset cache, and
// hands an approved command to the job service. Engine programs and game folders
// are chosen through the existing picker and binding routes — there is no second
// way to record where Quake is. It never downloads an engine or game data, and it
// never talks to any address but AUB's.
//
// Local building and running do not pass through here, so a signed-out Companion
// loses Games and nothing else.

func (s *Server) gamesAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/games":                                 s.handleGameList,
		"GET /api/v1/games/pending":                         s.handleGamePending,
		"POST /api/v1/games/pending/dismiss":                s.handleGamePendingDismiss,
		"GET /api/v1/games/{id}":                            s.handleGameReadiness,
		"POST /api/v1/games/{id}/content":                   s.handleGameContent,
		"POST /api/v1/games/{id}/engine-profile/review":     s.handleGameProfileReview,
		"POST /api/v1/games/{id}/engine-profile/install":    s.handleGameProfileInstall,
		"POST /api/v1/games/{id}/review":                    s.handleGameReview,
		"POST /api/v1/games/{id}/launch":                    s.handleGameLaunch,
		"POST /api/v1/games/{id}/engine-profile/approve":    s.handleGameProfileApprove,
		"GET /api/v1/games/{id}/engine-profile/permissions": s.handleGameProfilePermissions,
	}
}

// gameState is the Games area's process-wide state.
type gameState struct {
	coordination *hostgame.Coordination

	mu    sync.Mutex
	plans map[string]pendingPlan
}

type pendingPlan struct {
	gameID string
	plan   hostgame.Plan
}

func newGameState() *gameState {
	return &gameState{coordination: hostgame.NewCoordination(), plans: map[string]pendingPlan{}}
}

// joinContentTimeout bounds one join-content transfer. See the CLI's constant.
const joinContentTimeout = 10 * time.Minute

// codeSignInRequired marks a Games request made without a session, so the page
// shows its sign-in dialog rather than an error.
const codeSignInRequired = "sign_in_required"

func (s *Server) gamesSession(w http.ResponseWriter) (*aub.Client, bool) {
	client, ok := s.requireClient(w)
	if !ok {
		return nil, false
	}
	if !client.Authenticated() {
		writeJSON(w, http.StatusUnauthorized, errorBody{Code: codeSignInRequired,
			Error: "Sign in to see games. Building and running on this computer work without an account."})

		return nil, false
	}

	return client, true
}

// joiner builds a joiner for this request, over the process's coordination.
func (s *Server) joiner(client *aub.Client) (*hostgame.Joiner, error) {
	if s.jobs == nil {
		return nil, errors.New("this Companion was started without a job service, so it cannot join a game")
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	stager, err := s.joinStager()
	if err != nil {
		return nil, err
	}
	local := joinready.Local{
		Catalog: catalog,
		Bindings: func() (*binding.Set, error) {
			set, _, err := s.bindings()

			return set, err
		},
		Checker: engine.Checker{Platform: currentPlatform()},
		Stager:  stager,
	}

	return hostgame.NewSharedJoiner(client.WithTimeout(joinContentTimeout), local, s.jobs, s.games.coordination), nil
}

func (s *Server) joinStager() (*joincontent.Stager, error) {
	dir := s.paths.AssetCache
	if dir == "" {
		var err error
		if dir, err = s.config().AssetCache(); err != nil {
			return nil, err
		}
	}
	store, err := assetsync.Open(dir)
	if err != nil {
		return nil, err
	}

	return &joincontent.Stager{Root: filepath.Join(dir, "join-content"), Store: store}, nil
}

func (s *Server) configDir() (string, error) {
	if s.paths.ConfigDir != "" {
		return s.paths.ConfigDir, nil
	}

	return config.Dir()
}

// joinStatus maps a join refusal onto a status and a code the page branches on.
func joinStatus(err error) (int, string) {
	switch {
	case errors.Is(err, hostgame.ErrNotApproved):
		return http.StatusBadRequest, "not_approved"
	case errors.Is(err, hostgame.ErrPlanExpired):
		return http.StatusConflict, "review_expired"
	case errors.Is(err, hostgame.ErrGameChanged):
		return http.StatusConflict, "game_changed"
	case errors.Is(err, hostgame.ErrAlreadyDownloading):
		return http.StatusConflict, "already_downloading"
	case errors.Is(err, hostgame.ErrContentUnreadable):
		return http.StatusForbidden, "join_content_unreadable"
	case errors.Is(err, hostgame.ErrNoEngine), errors.Is(err, hostgame.ErrNotReady):
		return http.StatusConflict, "not_ready"
	case errors.Is(err, assetsync.ErrDigestMismatch), errors.Is(err, hostgame.ErrDigestMismatch),
		errors.Is(err, joincontent.ErrInvalid), errors.Is(err, joincontent.ErrStageDamaged):
		return http.StatusBadGateway, "content_refused"
	}
	var apiErr *aub.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized:
			return http.StatusUnauthorized, codeSignInRequired
		case apiErr.Reason == "hosted_game_join_ticket_stale", apiErr.Reason == "hosted_game_offline":
			return http.StatusConflict, "game_changed"
		case apiErr.StatusCode == http.StatusNotFound:
			return http.StatusNotFound, "game_not_found"
		}
	}

	return aubStatus(err), ""
}

func writeJoinError(w http.ResponseWriter, err error, report *joinready.Report) {
	status, code := joinStatus(err)
	body := map[string]any{"error": humanJoinError(err), "code": code}
	if report != nil && report.SchemaVersion != "" {
		body["readiness"] = report
	}
	writeJSON(w, status, body)
}

// humanJoinError drops the package prefix a Go error carries; the sentence after
// it is written for a person.
func humanJoinError(err error) string {
	message := err.Error()
	for _, prefix := range []string{"hostgame: ", "joincontent: ", "assetsync: ", "aub: "} {
		message = strings.ReplaceAll(message, prefix, "")
	}
	if message != "" {
		message = strings.ToUpper(message[:1]) + message[1:]
	}

	return message
}

func (s *Server) handleGameList(w http.ResponseWriter, r *http.Request) {
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	values := r.URL.Query()
	query := aub.HostedGameQuery{
		Scope:      values.Get("scope"),
		GameFamily: values.Get("game_family"),
		Region:     values.Get("region"),
		Mode:       values.Get("mode"),
		Cursor:     values.Get("cursor"),
		Limit:      25,
	}
	if query.Scope != "" && query.Scope != "public" && query.Scope != "mine" {
		writeError(w, http.StatusBadRequest, errors.New("scope is public or mine"))
		return
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, errors.New("limit is between 1 and 100"))
			return
		}
		query.Limit = limit
	}
	page, err := client.HostedGames(r.Context(), query)
	if err != nil {
		status, code := joinStatus(err)
		writeJSON(w, status, errorBody{Error: humanJoinError(err), Code: code})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleGamePending redeems a link the operating system delivered, at most once,
// and names the game it was for.
func (s *Server) handleGamePending(w http.ResponseWriter, r *http.Request) {
	dir, err := s.configDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.adoptSessionFromDisk()
	client := s.aubClient()
	redeem := func(ticketID string) (string, string, error) {
		if client == nil || !client.Authenticated() {
			return "", "", fmt.Errorf("%w: sign in first", joinintent.ErrTransient)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		resolution, err := client.ResolveJoinLink(ctx, ticketID)
		if err != nil {
			var apiErr *aub.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode == http.StatusUnauthorized {
				return "", "", fmt.Errorf("%w: %v", joinintent.ErrTransient, err)
			}

			return "", "", errors.New(humanJoinError(errors.New(apiErr.Message)))
		}

		return resolution.GameID, resolution.Title, nil
	}
	pending, err := joinintent.Take(joinintent.Path(dir), redeem, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	body := map[string]any{"pending": pending}
	if pending.Waiting && (client == nil || !client.Authenticated()) {
		body["code"] = codeSignInRequired
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleGamePendingDismiss(w http.ResponseWriter, r *http.Request) {
	dir, err := s.configDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err = joinintent.Dismiss(joinintent.Path(dir), time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": true})
}

// handleGameReadiness is one game and the readiness report for it.
func (s *Server) handleGameReadiness(w http.ResponseWriter, r *http.Request) {
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	joiner, err := s.joiner(client)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	report, err := joiner.Assess(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJoinError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"game": report.Game, "readiness": report})
}

// handleGameContent downloads, verifies and stages a game's map files.
func (s *Server) handleGameContent(w http.ResponseWriter, r *http.Request) {
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	joiner, err := s.joiner(client)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	// Not the request's context: a person who navigates away mid-download has
	// not asked for the verified files already fetched to be thrown away, and a
	// download is resumable from exactly those.
	ctx, cancel := context.WithTimeout(context.Background(), joinContentTimeout)
	defer cancel()
	report, err := joiner.DownloadContent(ctx, r.PathValue("id"), nil)
	if err != nil {
		writeJoinError(w, err, &report)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"game": report.Game, "readiness": report})
}

// engineListing is the exact publication a game's host declared, after checking
// the game still names it and this machine still lacks it.
func (s *Server) engineListing(r *http.Request, client *aub.Client) (joinready.Report, error) {
	joiner, err := s.joiner(client)
	if err != nil {
		return joinready.Report{}, err
	}
	report, err := joiner.Assess(r.Context(), r.PathValue("id"))
	if err != nil {
		return report, err
	}
	if report.Engine.ListingID == "" {
		return report, fmt.Errorf("%w: this game names no engine profile to install", hostgame.ErrNotReady)
	}

	return report, nil
}

type profileReview struct {
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Runtime         string   `json:"runtime"`
	Publisher       string   `json:"publisher,omitempty"`
	DeploymentTrust string   `json:"deployment_trust"`
	Trust           string   `json:"trust"`
	Digest          string   `json:"digest"`
	FirstInstall    bool     `json:"first_install"`
	Escalates       bool     `json:"escalates"`
	Yanked          bool     `json:"yanked"`
	YankReason      string   `json:"yank_reason,omitempty"`
	Permissions     []string `json:"permissions"`
	Report          string   `json:"report"`
}

// planEngineInstall plans the install and checks it IS the engine the game needs:
// an engine profile, for the host's runtime, that can join a server, whose id is
// the one the host declared.
func (s *Server) planEngineInstall(r *http.Request, client *aub.Client, report joinready.Report) (publish.Plan, error) {
	catalog, err := s.catalog()
	if err != nil {
		return publish.Plan{}, err
	}
	plan, err := publish.PlanInstall(r.Context(), client, catalog, report.Engine.ListingID, report.Engine.ListingVersion)
	if err != nil {
		return plan, err
	}
	document, isEngine := plan.Profile().(*profile.EngineProfile)
	switch {
	case !isEngine:
		return plan, errors.New("the host's published profile is not an engine profile")
	case !strings.EqualFold(document.Runtime, report.Game.EngineRuntime):
		return plan, fmt.Errorf("the host's published profile is for %s, and the game runs %s",
			document.Runtime, report.Game.EngineRuntime)
	case report.Game.EngineProfileRef != "" && document.ID != report.Game.EngineProfileRef:
		return plan, errors.New("the publication is not the engine profile the host declared")
	}
	if _, offers := document.ActionByID(profile.ActionJoinServer); !offers {
		return plan, errors.New("the host's published profile cannot join a server")
	}

	return plan, nil
}

func reviewOf(plan publish.Plan) profileReview {
	document := plan.Profile()
	meta := document.Metadata()
	review := profileReview{Name: meta.Name, Version: meta.Version, DeploymentTrust: plan.DeploymentTrust,
		Trust: string(plan.Trust), Digest: plan.Digest, FirstInstall: plan.FirstInstall, Escalates: plan.Escalates(),
		Yanked: plan.Yanked, YankReason: plan.YankReason, Report: profile.PermissionReport(document, plan.Trust)}
	if engineDoc, ok := document.(*profile.EngineProfile); ok {
		review.Runtime = engineDoc.Runtime
	}
	for _, permission := range plan.Permissions {
		review.Permissions = append(review.Permissions, fmt.Sprint(permission))
	}

	return review
}

func (s *Server) handleGameProfileReview(w http.ResponseWriter, r *http.Request) {
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	report, err := s.engineListing(r, client)
	if err != nil {
		writeJoinError(w, err, &report)
		return
	}
	plan, err := s.planEngineInstall(r, client, report)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"review": reviewOf(plan)})
}

// handleGameProfileInstall installs the exact publication that was reviewed, as
// community, with the approval the person just gave — or refuses, when the
// publication changed between the review and the approval.
func (s *Server) handleGameProfileInstall(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Digest  string `json:"digest"`
		Approve bool   `json:"approve"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	report, err := s.engineListing(r, client)
	if err != nil {
		writeJoinError(w, err, &report)
		return
	}
	plan, err := s.planEngineInstall(r, client, report)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if strings.TrimSpace(request.Digest) == "" || !strings.EqualFold(request.Digest, plan.Digest) {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "publication_changed",
			"error":  "The profile changed since you reviewed it. Review it again before installing.",
			"review": reviewOf(plan)})
		return
	}
	profiles, err := s.profilesDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	bindings, err := s.bindingsPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err = publish.Apply(plan, publish.InstallPaths{Profiles: profiles, Bindings: bindings}, request.Approve); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, publish.ErrDigestMismatch) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	s.handleGameReadiness(w, r)
}

// handleGameProfilePermissions is what a local engine profile asks for, so the
// page can show it before an approval.
func (s *Server) handleGameProfilePermissions(w http.ResponseWriter, r *http.Request) {
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	joiner, err := s.joiner(client)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	report, err := joiner.Assess(r.Context(), r.PathValue("id"))
	if err != nil || report.Engine.ProfileID == "" {
		writeJoinError(w, errOr(err, fmt.Errorf("%w: there is no engine profile to review", hostgame.ErrNotReady)), &report)
		return
	}
	service, err := s.approvals()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	decision, err := service.Review(report.Engine.ProfileID)
	if err != nil {
		writeError(w, approvalStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": report.Engine.Name, "trust": decision.Entry.Trust, "digest": decision.Entry.Digest,
		"report": decision.Report, "authorized": decision.Authorized,
	})
}

// handleGameProfileApprove is the existing approval service, for the engine the
// report chose, against the digest the person reviewed.
func (s *Server) handleGameProfileApprove(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Digest string `json:"digest"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	joiner, err := s.joiner(client)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	report, err := joiner.Assess(r.Context(), r.PathValue("id"))
	if err != nil || report.Engine.ProfileID == "" {
		writeJoinError(w, errOr(err, fmt.Errorf("%w: there is no engine profile to approve", hostgame.ErrNotReady)), &report)
		return
	}
	service, err := s.approvals()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err = service.Grant(report.Engine.ProfileID, request.Digest); err != nil {
		writeError(w, approvalStatus(err), err)
		return
	}
	s.handleGameReadiness(w, r)
}

// handleGameReview spends a fresh ticket, revalidates the game and previews the
// exact command. The plan is kept for PlanLifetime, under an id the page sends
// back with the approval.
func (s *Server) handleGameReview(w http.ResponseWriter, r *http.Request) {
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	joiner, err := s.joiner(client)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	gameID := r.PathValue("id")
	plan, err := joiner.Prepare(r.Context(), gameID)
	if err != nil {
		writeJoinError(w, err, &plan.Readiness)
		return
	}
	id, err := newPlanID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.games.mu.Lock()
	now := time.Now()
	for key, pending := range s.games.plans {
		if now.Sub(pending.plan.PreparedAt) > hostgame.PlanLifetime {
			delete(s.games.plans, key)
		}
	}
	s.games.plans[id] = pendingPlan{gameID: gameID, plan: plan}
	s.games.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"plan_id":    id,
		"expires_in": int(hostgame.PlanLifetime / time.Second),
		"title":      plan.Resolution.Title,
		"host":       plan.Resolution.Host,
		"endpoint":   plan.Resolution.Endpoint,
		"engine":     plan.Readiness.Engine.Name,
		"map_name":   plan.Resolution.MapName,
		"map_files":  plan.PackageSHA256 != "",
		"preview":    plan.Preview,
		"warnings":   plan.Warnings,
	})
}

// handleGameLaunch starts an approved review — once. The plan is taken out of the
// map under the lock, so a second click with the same id finds nothing and is
// told about the job the first one started.
func (s *Server) handleGameLaunch(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PlanID  string `json:"plan_id"`
		Approve bool   `json:"approve"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	client, ok := s.gamesSession(w)
	if !ok {
		return
	}
	joiner, err := s.joiner(client)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !request.Approve {
		writeJoinError(w, hostgame.ErrNotApproved, nil)
		return
	}
	s.games.mu.Lock()
	pending, found := s.games.plans[request.PlanID]
	if found && pending.gameID == r.PathValue("id") {
		delete(s.games.plans, request.PlanID)
	}
	s.games.mu.Unlock()
	if !found || pending.gameID != r.PathValue("id") {
		writeJSON(w, http.StatusConflict, errorBody{Code: "review_expired",
			Error: "That review is no longer waiting. If the game started, it is in Jobs; otherwise review the join again."})
		return
	}
	started, err := joiner.Launch(pending.plan, true)
	if errors.Is(err, hostgame.ErrAlreadyJoining) {
		writeJSON(w, http.StatusOK, map[string]any{"job": started, "already": true})
		return
	}
	if err != nil {
		writeJoinError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": started, "already": false})
}

func newPlanID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}

	return hex.EncodeToString(raw[:]), nil
}

func errOr(err, fallback error) error {
	if err != nil {
		return err
	}

	return fallback
}
