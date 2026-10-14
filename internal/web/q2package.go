package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2install"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2run"
)

// Package → Install → Run for a finished Quake II build (`Q2_009`).
//
// The same shape as the Quake III surface, and the same page panel: nothing is
// decided here. `internal/q2pack` plans and writes the PAK, `internal/q2install`
// puts it in a mod folder of the Companion's own beside `baseq2`, and
// `internal/q2run` starts the engine and accepts only the engine's own word
// that it opened the map from that package. `companion package map` drives the
// same three. The page names a build, a package, an installation, an engine
// and a mod folder by NAME — never a path.

func (s *Server) q2API() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/q2/engines":            s.handleQ2Engines,
		"POST /api/v1/q2/packages/preview":  s.handleQ2PackagePreview,
		"GET /api/v1/q2/packages":           s.handleQ2PackageList,
		"POST /api/v1/q2/packages":          s.handleQ2PackageCreate,
		"GET /api/v1/q2/packages/{id}":      s.handleQ2PackageGet,
		"POST /api/v1/q2/installs/preview":  s.handleQ2InstallPreview,
		"GET /api/v1/q2/installs":           s.handleQ2InstallList,
		"POST /api/v1/q2/installs":          s.handleQ2Install,
		"DELETE /api/v1/q2/installs/{id}":   s.handleQ2Uninstall,
		"POST /api/v1/q2/runs":              s.handleQ2RunStart,
		"GET /api/v1/q2/runs/{id}":          s.handleQ2RunGet,
		"POST /api/v1/q2/runs/{id}/cancel":  s.handleQ2RunCancel,
		"GET /api/v1/q2/installs/{id}/runs": s.handleQ2RunLatest,
	}
}

func (s *Server) q2Packages() (q2pack.Store, error) {
	dir, err := s.q3Dir("q2-packages")
	return q2pack.Store{Dir: dir}, err
}

func (s *Server) q2InstallsDir() (string, error) { return s.q3Dir("q2-installs") }

func q2Status(err error) int {
	switch {
	case errors.Is(err, build.ErrNotPlayable), errors.Is(err, q2pack.ErrNotQuake2):
		return http.StatusConflict
	case errors.Is(err, q2install.ErrNotInstalled), errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound
	}
	switch failure.Of(err) {
	case failure.PackageHeld, failure.InstallConflict, failure.LoadOrderShadowed, failure.ArchiveDamaged,
		failure.GameDataMissing, failure.ToolUnavailable, failure.PlatformUnsupported:
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// --- packages ------------------------------------------------------------------

type q2PackageBody struct {
	Build  string         `json:"build"`
	Name   string         `json:"name,omitempty"`
	Grants []q2pack.Grant `json:"grants,omitempty"`
	// AcceptReason accepts, in writing, what the archive will not carry.
	AcceptReason string `json:"accept_reason,omitempty"`
}

func (s *Server) q2Prepare(body q2PackageBody) (*q2pack.Plan, *build.Manifest, error) {
	if strings.TrimSpace(body.Build) == "" {
		return nil, nil, errors.New("name the build to package")
	}
	dir, err := s.buildsDir()
	if err != nil {
		return nil, nil, err
	}
	manifest, err := build.Find(dir, body.Build)
	if err != nil {
		return nil, nil, err
	}
	plan, err := q2pack.Prepare(q2pack.Request{Manifest: manifest, MapName: body.Name, Grants: body.Grants})
	return plan, manifest, err
}

func q2PlanView(plan *q2pack.Plan) map[string]any {
	view := map[string]any{"plan": plan, "maturity_message": maturity.Quake2Message}
	if err := plan.Blocked(false); err != nil {
		view["held"] = err.Error()
		view["acceptable"] = plan.Blocked(true) == nil
	}
	return view
}

func (s *Server) handleQ2PackagePreview(w http.ResponseWriter, r *http.Request) {
	var body q2PackageBody
	if !decodeJSON(w, r, &body) {
		return
	}
	plan, _, err := s.q2Prepare(body)
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	writeJSON(w, http.StatusOK, q2PlanView(plan))
}

func (s *Server) handleQ2PackageCreate(w http.ResponseWriter, r *http.Request) {
	var body q2PackageBody
	if !decodeJSON(w, r, &body) {
		return
	}
	plan, manifest, err := s.q2Prepare(body)
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	store, err := s.q2Packages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	record, existed, err := store.Create(plan, q2pack.Options{Companion: s.version, AcceptReason: body.AcceptReason, Manifest: manifest})
	if errors.Is(err, q2pack.ErrBlocked) {
		view := q2PlanView(plan)
		view["error"], view["class"] = err.Error(), failure.PackageHeld
		writeJSON(w, http.StatusConflict, view)
		return
	}
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	s.logf("q2 package %s: written from build %s, %d member(s), complete=%v", record.ID, record.Plan.BuildID,
		record.Archive.Entries, record.Complete)
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"package": record, "already_existed": existed})
}

func (s *Server) handleQ2PackageList(w http.ResponseWriter, r *http.Request) {
	store, err := s.q2Packages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	records, err := store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if wanted := r.URL.Query().Get("build"); wanted != "" {
		kept := records[:0]
		for _, record := range records {
			if record.Plan.BuildID == wanted {
				kept = append(kept, record)
			}
		}
		records = kept
	}
	if records == nil {
		records = []*q2pack.Record{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": records})
}

func (s *Server) handleQ2PackageGet(w http.ResponseWriter, r *http.Request) {
	store, err := s.q2Packages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	record, err := store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"package": record})
}

// --- engines -------------------------------------------------------------------

// handleQ2Engines lists the Quake II engines with an action that loads a map,
// with what would stop each running here — the same checker as Profiles,
// Build & Run and the executor.
func (s *Server) handleQ2Engines(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.catalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	entries, err := catalog.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	set, _, err := s.bindings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	checker := engine.Checker{Platform: currentPlatform()}
	engines := []map[string]any{}
	for _, entry := range entries {
		document, isEngine := entry.Profile.(*profile.EngineProfile)
		if !isEngine || documentFamily(document) != q2pack.FamilyQuake2 {
			continue
		}
		local, _ := set.Find(document.Meta.ID)
		actions := []map[string]any{}
		first, anyReady := "", false
		for _, action := range document.ActionList() {
			// A Quake II map is played on this computer by a client told the
			// map; hosting is not this surface.
			if !loadsAMap(action) || action.SessionRole != profile.SessionClient {
				continue
			}
			view := map[string]any{
				"id": action.ID, "title": action.Title, "session_role": action.SessionRole,
				"reports_map_load": reportsMapLoad(action), "options": []string{},
			}
			if problems := checker.Check(entry.Profile, entry.Trust, entry.Digest, local, action.ID); len(problems) > 0 {
				view["ready"], view["problem"] = false, problems.Error()
				if first == "" {
					first = problems.Error()
				}
			} else {
				view["ready"], anyReady = true, true
			}
			actions = append(actions, view)
		}
		if len(actions) == 0 {
			// FTEQW's Quake II profile is join-only, measured and documented:
			// it is not offered a local run it cannot do.
			continue
		}
		view := map[string]any{
			"id": document.Meta.ID, "name": document.Meta.Name, "actions": actions,
			"game_root": local.Roots[profile.RootGame], "set_up": local.Roots[profile.RootGame] != "" && anyReady,
		}
		if first != "" && !anyReady {
			view["problem"] = first
		} else if local.Roots[profile.RootGame] == "" {
			view["problem"] = "This engine has no game folder set on this machine yet: choose it under Profiles › Engines."
		}
		engines = append(engines, view)
	}
	sort.Slice(engines, func(i, j int) bool { return engines[i]["id"].(string) < engines[j]["id"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"engines": engines, "maturity_message": maturity.Quake2Message})
}

// --- installations -------------------------------------------------------------

type q2InstallBody struct {
	Package string `json:"package"`
	Engine  string `json:"engine"`
	// Mod is a directory NAME.
	Mod string `json:"mod,omitempty"`
}

func (s *Server) q2InstallRequest(body q2InstallBody) (q2install.Request, error) {
	store, err := s.q2Packages()
	if err != nil {
		return q2install.Request{}, err
	}
	record, err := store.Get(body.Package)
	if err != nil {
		return q2install.Request{}, err
	}
	if strings.TrimSpace(body.Engine) == "" {
		return q2install.Request{}, errors.New("choose the engine whose game folder the map is installed in")
	}
	root, err := s.gameRootFor(body.Engine)
	if err != nil {
		return q2install.Request{}, failure.As(failure.GameDataMissing, err)
	}
	dir, err := s.q2InstallsDir()
	if err != nil {
		return q2install.Request{}, err
	}
	return q2install.Request{Package: record, EngineProfileID: body.Engine, GameRoot: root, Mod: body.Mod, Dir: dir}, nil
}

func (s *Server) handleQ2InstallPreview(w http.ResponseWriter, r *http.Request) {
	var body q2InstallBody
	if !decodeJSON(w, r, &body) {
		return
	}
	request, err := s.q2InstallRequest(body)
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	installation, err := q2install.Preview(request)
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installation": installation})
}

func (s *Server) handleQ2Install(w http.ResponseWriter, r *http.Request) {
	var body q2InstallBody
	if !decodeJSON(w, r, &body) {
		return
	}
	request, err := s.q2InstallRequest(body)
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	// An engine running out of the folder being replaced is stopped first.
	if id := q2install.InstallationID(request.GameRoot, firstNonBlank(body.Mod, request.Package.Plan.GameDir)); id != "" {
		if run := s.q2runs.forInstallation(id); run != nil && run.jobID() != "" && s.jobs != nil {
			if current, getErr := s.jobs.Get(run.jobID()); getErr == nil && !current.State.Terminal() {
				writeError(w, http.StatusConflict, fmt.Errorf(
					"the engine is still running this mod folder (job %s). Stop it first", run.jobID()))
				return
			}
		}
	}
	// The request's own context: a page that goes away mid-install cancels it,
	// and a cancelled install leaves the previous one where it was.
	installation, err := q2install.Install(r.Context(), request)
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	s.logf("q2 install %s: package %s into %s", installation.ID, installation.PackageID, installation.ModDir)
	writeJSON(w, http.StatusCreated, map[string]any{"installation": installation})
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Server) handleQ2InstallList(w http.ResponseWriter, r *http.Request) {
	dir, err := s.q2InstallsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	installations, err := q2install.List(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if wanted := r.URL.Query().Get("package"); wanted != "" {
		kept := installations[:0]
		for _, installation := range installations {
			if installation.PackageID == wanted {
				kept = append(kept, installation)
			}
		}
		installations = kept
	}
	if installations == nil {
		installations = []*q2install.Installation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"installations": installations})
}

func (s *Server) handleQ2Uninstall(w http.ResponseWriter, r *http.Request) {
	dir, err := s.q2InstallsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id := r.PathValue("id")
	if run := s.q2runs.forInstallation(id); run != nil && run.jobID() != "" && s.jobs != nil {
		if current, getErr := s.jobs.Get(run.jobID()); getErr == nil && !current.State.Terminal() {
			writeError(w, http.StatusConflict, fmt.Errorf(
				"the engine is still running this installation (job %s). Stop it first", run.jobID()))
			return
		}
	}
	if err := q2install.Remove(dir, id); err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	s.logf("q2 install %s: removed", id)
	writeJSON(w, http.StatusOK, map[string]any{"removed": id})
}

// --- runs ------------------------------------------------------------------------

func (s *Server) handleQ2RunStart(w http.ResponseWriter, r *http.Request) {
	var body q3RunBody
	if !decodeJSON(w, r, &body) {
		return
	}
	service, err := s.requireJobsService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	dir, err := s.q2InstallsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	installation, err := q2install.Load(dir, body.Installation)
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	catalog, err := s.catalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	entry, err := catalog.Lookup(body.Engine)
	if err != nil {
		writeError(w, http.StatusConflict, failure.As(failure.ToolUnavailable, err))
		return
	}
	set, _, err := s.bindings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	local, _ := set.Find(body.Engine)
	if problems := (engine.Checker{Platform: currentPlatform()}).Check(entry.Profile, entry.Trust, entry.Digest, local, body.Action); len(problems) > 0 {
		writeError(w, http.StatusConflict, failure.As(failure.ToolUnavailable, problems))
		return
	}
	if previous := s.q2runs.forInstallation(installation.ID); previous != nil {
		if id := previous.jobID(); id != "" {
			if current, getErr := service.Get(id); getErr == nil && !current.State.Terminal() {
				writeError(w, http.StatusConflict, fmt.Errorf(
					"an engine is already running this installation (job %s). Stop it before starting another", id))
				return
			}
		}
	}
	wait := time.Duration(body.WaitSeconds) * time.Second
	if wait < 0 || wait > 10*time.Minute {
		writeError(w, http.StatusBadRequest, errors.New("the wait is between 0 and 600 seconds"))
		return
	}
	request := q2run.Request{Installation: installation, EngineProfileID: body.Engine, ActionID: body.Action,
		Options: body.Options, Wait: wait, Label: "Run " + installation.MapName}
	if _, err := q2run.Preview(service, request); err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	id, err := newQ3RunID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &q3Run{ID: id, Installation: installation.ID, Engine: body.Engine, Action: body.Action,
		StartedAt: time.Now().UTC(), cancel: cancel}
	request.Started = func(jobID string) {
		run.mu.Lock()
		run.job = jobID
		run.mu.Unlock()
	}
	s.q2runs.add(run)
	go func() {
		defer cancel()
		result, err := q2run.Launch(ctx, service, request)
		run.mu.Lock()
		run.done = true
		if result != nil {
			run.result = result
		}
		if err != nil {
			run.err, run.class = err.Error(), failure.Of(err)
		}
		run.mu.Unlock()
		s.saveRun(run, "q2-runs")
		if result != nil {
			s.logf("q2 run %s: map load %s from %q, engine job %s, pid %d", run.ID, result.MapLoad, result.LoadedFrom,
				result.JobID, result.PID)
		} else {
			s.logf("q2 run %s: no engine was started: %v", run.ID, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, run.view())
}

func (s *Server) handleQ2RunGet(w http.ResponseWriter, r *http.Request) {
	view, err := s.runView(s.q2runs, "q2-runs", r.PathValue("id"))
	if err != nil {
		writeError(w, q2Status(err), err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleQ2RunLatest(w http.ResponseWriter, r *http.Request) {
	s.latestRun(w, r, s.q2runs, "q2-runs")
}

func (s *Server) handleQ2RunCancel(w http.ResponseWriter, r *http.Request) {
	s.cancelRun(w, r, s.q2runs, "q2-runs")
}
