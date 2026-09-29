package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/autobuild"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Auto-build, as the page and the API see it (NEW_265). The rules are
// internal/autobuild's; this file is the three things it drives — AUB's answer
// about a map, the Build & Run coordinator in its build-only mode, and that
// coordinator's run records — and the routes.
//
//	GET  /api/v1/autobuild                    every map's auto-build
//	GET  /api/v1/autobuild/{asset}            one
//	POST /api/v1/autobuild/{asset}/enable     {"pipeline":"auto-pigeon.q1.normal","display_name":"dm1"}
//	POST /api/v1/autobuild/{asset}/disable
//	POST /api/v1/autobuild/{asset}/pipeline   {"pipeline":"auto-pigeon.q1.final"}
//	POST /api/v1/autobuild/{asset}/build-now  the current revision, now
//	POST /api/v1/autobuild/{asset}/retry      the revision whose build failed

func (s *Server) autobuildAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/autobuild":                    s.handleAutobuildList,
		"GET /api/v1/autobuild/{asset}":            s.handleAutobuildGet,
		"POST /api/v1/autobuild/{asset}/enable":    s.handleAutobuildEnable,
		"POST /api/v1/autobuild/{asset}/disable":   s.handleAutobuildDisable,
		"POST /api/v1/autobuild/{asset}/pipeline":  s.handleAutobuildPipeline,
		"POST /api/v1/autobuild/{asset}/build-now": s.handleAutobuildBuildNow,
		"POST /api/v1/autobuild/{asset}/retry":     s.handleAutobuildRetry,
	}
}

// autobuildPath is the state file, beside the jobs, builds and run records.
func (s *Server) autobuildPath() (string, error) {
	jobs, err := s.jobsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(jobs), "autobuild.json"), nil
}

// autobuildService is this process's one auto-build service, made on first use.
func (s *Server) autobuildService() (*autobuild.Service, error) {
	s.autobuildOnce.Do(func() {
		path, err := s.autobuildPath()
		if err != nil {
			s.autobuildErr = err
			return
		}
		s.autobuild, s.autobuildErr = autobuild.New(path, autobuild.Deps{
			Current:  s.autobuildCurrent,
			Start:    s.autobuildStart,
			RunState: s.autobuildRunState,
			Logf:     s.logf,
		})
	})
	return s.autobuild, s.autobuildErr
}

// StartAutoBuild runs the one poller until ctx ends. Called by `companion
// serve` once; never per browser tab, and never by a test that did not ask.
func (s *Server) StartAutoBuild(ctx context.Context) {
	service, err := s.autobuildService()
	if err != nil {
		s.logf("autobuild: not available: %v", err)
		return
	}
	go service.Run(ctx)
}

// autobuildCurrent asks AUB, with this person's own session, which revision
// of a map is current.
func (s *Server) autobuildCurrent(ctx context.Context, assetID string) (autobuild.Revision, string, error) {
	client := s.aubClient()
	if client == nil {
		return autobuild.Revision{}, "", fmt.Errorf("%w: no Auto-Pigeon server is chosen in Settings", autobuild.ErrAccess)
	}
	if !client.Authenticated() {
		return autobuild.Revision{}, "", fmt.Errorf("%w: not signed in", autobuild.ErrAccess)
	}
	detail, err := client.Asset(ctx, "map", assetID)
	if err != nil {
		var apiErr *aub.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusUnauthorized:
				return autobuild.Revision{}, "", fmt.Errorf("%w: %v", autobuild.ErrAccess, err)
			case http.StatusForbidden, http.StatusNotFound:
				return autobuild.Revision{}, "", fmt.Errorf("%w: %v", autobuild.ErrMissing, err)
			}
		}
		return autobuild.Revision{}, "", err
	}
	current := detail.Asset.CurrentRevision
	if current == nil {
		return autobuild.Revision{}, detail.Asset.DisplayName, errors.New("this map has no saved revision yet")
	}
	return autobuild.Revision{
		ID: current.ID, Number: current.Number, ContentSHA256: current.ContentSHA256, CreatedAt: current.CreatedAt,
	}, detail.Asset.DisplayName, nil
}

// unsafeMapName is what a Quake map name may not hold.
var unsafeMapName = regexp.MustCompile(`[^a-z0-9_-]+`)

// autobuildStart submits one ordinary build: the Build & Run coordinator,
// build-only, so the map and its textures arrive by the same authenticated,
// verified path as any Build & Run and the build is an ordinary job.
func (s *Server) autobuildStart(entry autobuild.Entry, revision autobuild.Revision) (string, error) {
	service, err := s.playService()
	if err != nil {
		return "", err
	}
	name := strings.Trim(unsafeMapName.ReplaceAllString(strings.ToLower(entry.DisplayName), "_"), "_")
	title := entry.DisplayName
	if title == "" {
		title = entry.AssetID
	}
	record, err := service.Start(playrun.Request{
		AssetType: "map", AssetID: entry.AssetID,
		RevisionID: revision.ID, RevisionNumber: revision.Number,
		PipelineID: entry.PipelineID,
		MapName:    name,
		BuildOnly:  true,
		Trigger:    "auto_build",
		Label:      fmt.Sprintf("Auto-build: %s, revision %d", title, revision.Number),
	})
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

func (s *Server) autobuildRunState(runID string) (string, string, error) {
	service, err := s.playService()
	if err != nil {
		return "", "", err
	}
	record, err := service.Get(runID)
	if err != nil {
		return "", "", err
	}
	return string(record.State), record.Error, nil
}

// autobuildView is one map's auto-build as the page shows it: the state, and
// for each attempt the job a person can open.
func (s *Server) autobuildView(service *autobuild.Service, entry autobuild.Entry) map[string]any {
	view := map[string]any{
		"asset_id": entry.AssetID, "display_name": entry.DisplayName,
		"pipeline_id": entry.PipelineID, "enabled": entry.Enabled,
		"baseline": entry.Baseline, "observed": entry.Observed, "pending": entry.Pending,
		"check_error": entry.CheckError, "failures": entry.Failures, "halted": entry.Halted,
	}
	if !entry.EnabledAt.IsZero() {
		view["enabled_at"] = entry.EnabledAt
	}
	if !entry.LastCheckAt.IsZero() {
		view["last_check_at"] = entry.LastCheckAt
	}
	if entry.Enabled && entry.Halted == "" && !entry.NextCheckAt.IsZero() {
		view["next_check_at"] = entry.NextCheckAt
	}
	// A question to AUB that has not been answered yet, for the configuration
	// on screen. One asked before the switch or the profile last changed is
	// still in flight, but its answer will not be used, so it is not shown.
	if check, asking := service.CheckingNow(entry.AssetID); asking && entry.Enabled && check.Generation == entry.Generation {
		view["checking_since"] = check.Since
	}
	for key, attempt := range map[string]*autobuild.Attempt{
		"running": entry.Running, "last_built": entry.LastBuilt, "failed": entry.Failed,
	} {
		if attempt == nil {
			continue
		}
		row := map[string]any{
			"revision": attempt.Revision, "run_id": attempt.RunID, "pipeline": attempt.Pipeline,
			"manual": attempt.Manual, "started_at": attempt.StartedAt, "state": attempt.State, "error": attempt.Error,
		}
		if !attempt.FinishedAt.IsZero() {
			row["finished_at"] = attempt.FinishedAt
		}
		if job, step := s.runJob(attempt.RunID); job != "" {
			row["job_id"], row["job_step"] = job, step
		}
		view[key] = row
	}
	return view
}

// runJob is the job a run's card should link to: the step running now, or the
// last step that ran.
func (s *Server) runJob(runID string) (string, string) {
	if runID == "" {
		return "", ""
	}
	service, err := s.playService()
	if err != nil {
		return "", ""
	}
	record, err := service.Get(runID)
	if err != nil {
		return "", ""
	}
	if record.CurrentJob != "" {
		return record.CurrentJob, record.CurrentStep
	}
	if record.BuildID == "" {
		return "", ""
	}
	dir, err := s.buildsDir()
	if err != nil {
		return "", ""
	}
	manifest, err := build.Find(dir, record.BuildID)
	if err != nil {
		return "", ""
	}
	job, step := "", ""
	for _, candidate := range manifest.Steps {
		if candidate.JobID != "" {
			job, step = candidate.JobID, candidate.ID
		}
	}
	return job, step
}

func (s *Server) requireAutobuild(w http.ResponseWriter) (*autobuild.Service, bool) {
	service, err := s.autobuildService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return nil, false
	}
	return service, true
}

func (s *Server) handleAutobuildList(w http.ResponseWriter, _ *http.Request) {
	service, ok := s.requireAutobuild(w)
	if !ok {
		return
	}
	state, err := service.State()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	items := make([]map[string]any, 0, len(state.Entries))
	for _, entry := range state.Entries {
		items = append(items, s.autobuildView(service, entry))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "now": time.Now().UTC(), "check_interval_seconds": int(autobuild.CheckInterval.Seconds()),
	})
}

func (s *Server) handleAutobuildGet(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireAutobuild(w)
	if !ok {
		return
	}
	state, err := service.State()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	entry, found := state.Find(r.PathValue("asset"))
	if !found {
		// Never switched on: a state, not an error.
		writeJSON(w, http.StatusOK, map[string]any{"asset_id": r.PathValue("asset"), "enabled": false, "now": time.Now().UTC()})
		return
	}
	view := s.autobuildView(service, *entry)
	view["now"] = time.Now().UTC()
	writeJSON(w, http.StatusOK, view)
}

// pipelineExists refuses a build profile this machine does not have, before
// anything is recorded.
func (s *Server) pipelineExists(id string) error {
	catalog, err := s.catalog()
	if err != nil {
		return err
	}
	entry, err := catalog.Lookup(id)
	if err != nil {
		return fmt.Errorf("no build profile %q is installed here", id)
	}
	if _, ok := entry.Profile.(*profile.PipelineProfile); !ok {
		return fmt.Errorf("%q is not a build profile", id)
	}
	return nil
}

func (s *Server) handleAutobuildEnable(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Pipeline    string `json:"pipeline"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	service, ok := s.requireAutobuild(w)
	if !ok {
		return
	}
	if err := s.pipelineExists(request.Pipeline); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entry, err := service.Enable(r.PathValue("asset"), request.DisplayName, request.Pipeline)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// The baseline is taken by the poller in a moment rather than at its next
	// tick — and not inside this request: a slow AUB must not hold the switch
	// (NEW_265A). The page shows the question in flight until it lands.
	service.Nudge()
	s.writeAutobuildEntry(w, service, entry.AssetID)
}

func (s *Server) handleAutobuildDisable(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireAutobuild(w)
	if !ok {
		return
	}
	if _, err := service.Disable(r.PathValue("asset")); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeAutobuildEntry(w, service, r.PathValue("asset"))
}

func (s *Server) handleAutobuildPipeline(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Pipeline string `json:"pipeline"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	service, ok := s.requireAutobuild(w)
	if !ok {
		return
	}
	if err := s.pipelineExists(request.Pipeline); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := service.SetPipeline(r.PathValue("asset"), request.Pipeline); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeAutobuildEntry(w, service, r.PathValue("asset"))
}

func (s *Server) handleAutobuildBuildNow(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Pipeline    string `json:"pipeline"`
		DisplayName string `json:"display_name"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &request) {
		return
	}
	service, ok := s.requireAutobuild(w)
	if !ok {
		return
	}
	if request.Pipeline != "" {
		if err := s.pipelineExists(request.Pipeline); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if _, err := service.BuildNow(r.Context(), r.PathValue("asset"), request.Pipeline, request.DisplayName); err != nil {
		writeError(w, autobuildStatus(err), err)
		return
	}
	s.writeAutobuildEntry(w, service, r.PathValue("asset"))
}

func (s *Server) handleAutobuildRetry(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireAutobuild(w)
	if !ok {
		return
	}
	if _, err := service.Retry(r.PathValue("asset")); err != nil {
		writeError(w, autobuildStatus(err), err)
		return
	}
	s.writeAutobuildEntry(w, service, r.PathValue("asset"))
}

func (s *Server) writeAutobuildEntry(w http.ResponseWriter, service *autobuild.Service, assetID string) {
	state, err := service.State()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	entry, found := state.Find(assetID)
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("%s has no auto-build", assetID))
		return
	}
	view := s.autobuildView(service, *entry)
	view["now"] = time.Now().UTC()
	writeJSON(w, http.StatusOK, view)
}

func autobuildStatus(err error) int {
	switch {
	case errors.Is(err, autobuild.ErrBusy):
		return http.StatusConflict
	case errors.Is(err, autobuild.ErrAccess):
		return http.StatusUnauthorized
	case errors.Is(err, autobuild.ErrMissing):
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}
