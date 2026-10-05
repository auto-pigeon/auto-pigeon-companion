package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3install"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3run"
)

// Package → Install → Run for a finished Quake III build.
//
// # The same three packages the terminal drives
//
// Nothing is decided here. `internal/q3pack` plans and writes the archive,
// `internal/q3install` puts it beside a game, and `internal/q3run` starts the
// engine and waits for its own word about the map. `companion package map` calls
// the same three with the same arguments, which is what keeps "everything the
// page can do, the terminal can do too" true for this surface: a refusal the
// page shows is a refusal the command prints, with the same class.
//
// # What the page may name
//
// A build by its id, a package by its id, an installation by its id, an engine
// by its profile id, and a game directory by its NAME. Never a path: where the
// game folder is comes from the engine's own setup on this machine, exactly as
// it does for Build & Run (`gameRootFor`). A request that could name a folder
// could name any folder.
//
// # A run outlives its request
//
// Starting an engine and waiting for it to say the map loaded can take a
// minute. The POST returns as soon as the run has an id; the page polls it. The
// engine itself is an ordinary job in the job service — it is in Jobs, it has a
// log, and Quit stops it — so the only thing held here is the wait and what it
// observed, which is written beside the installations when it ends so a
// reloaded page shows the last run rather than nothing.

func (s *Server) q3API() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/q3/engines":            s.handleQ3Engines,
		"POST /api/v1/q3/packages/preview":  s.handleQ3PackagePreview,
		"GET /api/v1/q3/packages":           s.handleQ3PackageList,
		"POST /api/v1/q3/packages":          s.handleQ3PackageCreate,
		"GET /api/v1/q3/packages/{id}":      s.handleQ3PackageGet,
		"POST /api/v1/q3/installs/preview":  s.handleQ3InstallPreview,
		"GET /api/v1/q3/installs":           s.handleQ3InstallList,
		"POST /api/v1/q3/installs":          s.handleQ3Install,
		"DELETE /api/v1/q3/installs/{id}":   s.handleQ3Uninstall,
		"POST /api/v1/q3/runs":              s.handleQ3RunStart,
		"GET /api/v1/q3/runs/{id}":          s.handleQ3RunGet,
		"POST /api/v1/q3/runs/{id}/cancel":  s.handleQ3RunCancel,
		"GET /api/v1/q3/installs/{id}/runs": s.handleQ3RunLatest,
	}
}

// q3Dir is a directory beside the builds: the packages, the installations and
// the runs are what a build becomes next.
func (s *Server) q3Dir(name string) (string, error) {
	builds, err := s.buildsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(builds), name), nil
}

func (s *Server) q3Packages() (q3pack.Store, error) {
	dir, err := s.q3Dir("q3-packages")
	return q3pack.Store{Dir: dir}, err
}

func (s *Server) q3InstallsDir() (string, error) { return s.q3Dir("q3-installs") }

// q3Status is the HTTP status a refusal from these packages gets. A conflict
// with what is already on the machine — a held package, a file in the way, a
// map something else would supply — is 409; a thing that is not there is 404;
// the rest is a request that cannot be honoured as written.
func q3Status(err error) int {
	switch {
	case errors.Is(err, build.ErrNotPlayable), errors.Is(err, q3pack.ErrNotQuake3):
		return http.StatusConflict
	case errors.Is(err, q3install.ErrNotInstalled), errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound
	}
	switch failure.Of(err) {
	case failure.PackageHeld, failure.InstallConflict, failure.LoadOrderShadowed:
		return http.StatusConflict
	case failure.GameDataMissing, failure.ToolUnavailable, failure.PlatformUnsupported:
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// --- packages ------------------------------------------------------------------

// q3PackageBody is what the page sends to plan or write a package.
type q3PackageBody struct {
	Build   string         `json:"build"`
	Name    string         `json:"name,omitempty"`
	Grants  []q3pack.Grant `json:"grants,omitempty"`
	Include []string       `json:"include,omitempty"`
	// AcceptReason accepts, in writing, what the archive will not carry.
	AcceptReason string `json:"accept_reason,omitempty"`
}

func (s *Server) q3Prepare(body q3PackageBody) (*q3pack.Prepared, error) {
	if strings.TrimSpace(body.Build) == "" {
		return nil, errors.New("name the build to package")
	}
	dir, err := s.buildsDir()
	if err != nil {
		return nil, err
	}
	manifest, err := build.Find(dir, body.Build)
	if err != nil {
		return nil, err
	}
	return q3pack.Prepare(q3pack.Request{
		Manifest: manifest, MapName: body.Name, Grants: body.Grants, Include: body.Include,
	})
}

// q3PlanView is a plan as the page reads it: the plan, and whether it would be
// written as it stands.
func q3PlanView(plan *q3pack.Plan) map[string]any {
	view := map[string]any{"plan": plan, "maturity_message": maturity.Quake3Message}
	if err := plan.Blocked(false); err != nil {
		view["held"] = err.Error()
		// Whether writing it is a matter of accepting what it lacks, or of
		// something a reason cannot accept.
		view["acceptable"] = plan.Blocked(true) == nil
	}
	return view
}

func (s *Server) handleQ3PackagePreview(w http.ResponseWriter, r *http.Request) {
	var body q3PackageBody
	if !decodeJSON(w, r, &body) {
		return
	}
	prepared, err := s.q3Prepare(body)
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	defer prepared.Close()
	writeJSON(w, http.StatusOK, q3PlanView(prepared.Plan))
}

func (s *Server) handleQ3PackageCreate(w http.ResponseWriter, r *http.Request) {
	var body q3PackageBody
	if !decodeJSON(w, r, &body) {
		return
	}
	prepared, err := s.q3Prepare(body)
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	defer prepared.Close()
	store, err := s.q3Packages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	record, existed, err := store.Create(prepared, q3pack.Options{Companion: s.version, AcceptReason: body.AcceptReason})
	if errors.Is(err, q3pack.ErrBlocked) {
		// The plan travels with the refusal: a page that was told only "held"
		// would have to ask again to say why.
		view := q3PlanView(prepared.Plan)
		view["error"], view["class"] = err.Error(), failure.PackageHeld
		writeJSON(w, http.StatusConflict, view)
		return
	}
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	s.logf("q3 package %s: written from build %s, %d member(s), complete=%v", record.ID, record.Plan.BuildID,
		record.Archive.Entries, record.Complete)
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"package": record, "already_existed": existed})
}

func (s *Server) handleQ3PackageList(w http.ResponseWriter, r *http.Request) {
	store, err := s.q3Packages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	records, err := store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// One build's packages, when the page asks for them.
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
		records = []*q3pack.Record{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": records})
}

func (s *Server) handleQ3PackageGet(w http.ResponseWriter, r *http.Request) {
	store, err := s.q3Packages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	record, err := store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"package": record})
}

// --- engines -------------------------------------------------------------------

// handleQ3Engines lists the Quake III engines this machine has profiles for,
// with what would stop each one running a package here and the actions that
// load a map.
func (s *Server) handleQ3Engines(w http.ResponseWriter, r *http.Request) {
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
	engines := []map[string]any{}
	for _, entry := range entries {
		document, isEngine := entry.Profile.(*profile.EngineProfile)
		if !isEngine || documentFamily(document) != q3pack.FamilyQuake3 {
			continue
		}
		local, _ := set.Find(document.Meta.ID)
		actions := []map[string]any{}
		ready := ""
		anyReady := false
		for _, action := range document.ActionList() {
			if !loadsAMap(action) {
				continue
			}
			// The settings this action declares, by name: the page offers a
			// field only to an action that reads it, and sends nothing else.
			options := []string{}
			for _, option := range action.Options {
				options = append(options, option.Name)
			}
			view := map[string]any{
				"id": action.ID, "title": action.Title, "session_role": action.SessionRole,
				"reports_map_load": reportsMapLoad(action), "options": options,
			}
			if problem := q3run.Preflight(entry, local, action.ID, currentPlatform()); problem != nil {
				view["ready"] = false
				view["problem"] = problem.Error()
				if ready == "" {
					ready = problem.Error()
				}
			}
			if _, blocked := view["problem"]; !blocked {
				view["ready"] = true
				anyReady = true
			}
			actions = append(actions, view)
		}
		engine := map[string]any{
			"id": document.Meta.ID, "name": document.Meta.Name, "actions": actions,
			"game_root": local.Roots[profile.RootGame], "set_up": local.Roots[profile.RootGame] != "" && anyReady,
		}
		if ready != "" && !anyReady {
			engine["problem"] = ready
		} else if local.Roots[profile.RootGame] == "" {
			engine["problem"] = "This engine has no game folder set on this machine yet: choose it under Profiles › Engines."
		}
		engines = append(engines, engine)
	}
	sort.Slice(engines, func(i, j int) bool { return engines[i]["id"].(string) < engines[j]["id"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"engines": engines, "maturity_message": maturity.Quake3Message})
}

func loadsAMap(action profile.Action) bool {
	for _, arg := range action.Args {
		if strings.Contains(arg.Value, "{runtime."+profile.RuntimeMapName+"}") {
			return true
		}
	}
	return false
}

func reportsMapLoad(action profile.Action) bool {
	for _, rule := range action.Diagnostics {
		if rule.Signal == profile.SignalMapLoaded {
			return true
		}
	}
	return false
}

// --- installations -------------------------------------------------------------

// q3InstallBody is what the page sends to install a package. The game folder is
// not in it: it is the engine's own setup.
type q3InstallBody struct {
	Package string `json:"package"`
	Engine  string `json:"engine"`
	// Into is `managed` (the default) or `game_folder`.
	Into string `json:"into,omitempty"`
	// Mod and BaseGame are directory NAMES.
	Mod      string `json:"mod,omitempty"`
	BaseGame string `json:"base_game,omitempty"`
}

func (s *Server) q3InstallRequest(body q3InstallBody) (q3install.Request, error) {
	store, err := s.q3Packages()
	if err != nil {
		return q3install.Request{}, err
	}
	record, err := store.Get(body.Package)
	if err != nil {
		return q3install.Request{}, err
	}
	if strings.TrimSpace(body.Engine) == "" {
		return q3install.Request{}, errors.New("choose the engine whose game folder the map is installed beside")
	}
	root, err := s.gameRootFor(body.Engine)
	if err != nil {
		return q3install.Request{}, failure.As(failure.GameDataMissing, err)
	}
	dir, err := s.q3InstallsDir()
	if err != nil {
		return q3install.Request{}, err
	}
	kind := q3install.Kind(body.Into)
	if kind == "" {
		kind = q3install.Managed
	}
	return q3install.Request{
		Package: record, Kind: kind, GameRoot: root, Game: body.Mod, BaseGame: body.BaseGame, Dir: dir,
	}, nil
}

func (s *Server) handleQ3InstallPreview(w http.ResponseWriter, r *http.Request) {
	var body q3InstallBody
	if !decodeJSON(w, r, &body) {
		return
	}
	request, err := s.q3InstallRequest(body)
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	installation, err := q3install.Preview(request)
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installation": installation})
}

func (s *Server) handleQ3Install(w http.ResponseWriter, r *http.Request) {
	var body q3InstallBody
	if !decodeJSON(w, r, &body) {
		return
	}
	request, err := s.q3InstallRequest(body)
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	// The request's own context: a page that goes away mid-install cancels it,
	// and a cancelled install removes what it wrote.
	installation, err := q3install.Install(r.Context(), request)
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	s.logf("q3 install %s: package %s as %s, fs_game %s", installation.ID, installation.PackageID,
		installation.Kind, installation.FSGame)
	writeJSON(w, http.StatusCreated, map[string]any{"installation": installation})
}

func (s *Server) handleQ3InstallList(w http.ResponseWriter, r *http.Request) {
	dir, err := s.q3InstallsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	installations, err := q3install.List(dir)
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
		installations = []*q3install.Installation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"installations": installations})
}

func (s *Server) handleQ3Uninstall(w http.ResponseWriter, r *http.Request) {
	dir, err := s.q3InstallsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id := r.PathValue("id")
	// An engine running out of it is stopped first. Removing a directory a
	// process has open would leave an engine drawing a map that is gone.
	if run := s.q3runs.forInstallation(id); run != nil && run.jobID() != "" && s.jobs != nil {
		if current, getErr := s.jobs.Get(run.jobID()); getErr == nil && !current.State.Terminal() {
			writeError(w, http.StatusConflict, fmt.Errorf(
				"the engine is still running this installation (job %s). Stop it first", run.jobID()))
			return
		}
	}
	if err := q3install.Remove(dir, id); err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	s.logf("q3 install %s: removed", id)
	writeJSON(w, http.StatusOK, map[string]any{"removed": id})
}

// --- runs ------------------------------------------------------------------------

// q3RunBody is what the page sends to run an installation.
type q3RunBody struct {
	Installation string            `json:"installation"`
	Engine       string            `json:"engine"`
	Action       string            `json:"action"`
	Options      map[string]string `json:"options,omitempty"`
	// WaitSeconds bounds the wait for the engine's word; zero means the default.
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

// q3Run is one run this process is waiting on, or has finished waiting on.
type q3Run struct {
	ID           string    `json:"id"`
	Installation string    `json:"installation"`
	Engine       string    `json:"engine"`
	Action       string    `json:"action"`
	StartedAt    time.Time `json:"started_at"`

	mu     sync.Mutex
	job    string
	done   bool
	result *q3run.Result
	err    string
	class  string
	cancel context.CancelFunc
}

func (r *q3Run) jobID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.job
}

// view is the run as the page reads it.
func (r *q3Run) view() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	view := map[string]any{
		"id": r.ID, "installation": r.Installation, "engine": r.Engine, "action": r.Action,
		"started_at": r.StartedAt, "state": "waiting", "job_id": r.job,
	}
	if r.done {
		view["state"] = "done"
		if r.err != "" {
			view["state"], view["error"], view["class"] = "failed", r.err, r.class
		}
		if r.result != nil {
			view["result"] = r.result
		}
	}
	return view
}

// q3Runs is the runs this process knows.
type q3Runs struct {
	mu   sync.Mutex
	runs map[string]*q3Run
}

func newQ3Runs() *q3Runs { return &q3Runs{runs: map[string]*q3Run{}} }

func (q *q3Runs) add(run *q3Run) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.runs[run.ID] = run
}

func (q *q3Runs) get(id string) *q3Run {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.runs[id]
}

// forInstallation is the newest run of an installation in this process.
func (q *q3Runs) forInstallation(id string) *q3Run {
	q.mu.Lock()
	defer q.mu.Unlock()
	var newest *q3Run
	for _, run := range q.runs {
		if run.Installation == id && (newest == nil || run.StartedAt.After(newest.StartedAt)) {
			newest = run
		}
	}
	return newest
}

// cancelWaiting stops every wait in progress. The engines themselves are jobs,
// and the job service stops those.
func (q *q3Runs) cancelWaiting() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, run := range q.runs {
		if run.cancel != nil {
			run.cancel()
		}
	}
}

func newQ3RunID() (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random), nil
}

func (s *Server) handleQ3RunStart(w http.ResponseWriter, r *http.Request) {
	var body q3RunBody
	if !decodeJSON(w, r, &body) {
		return
	}
	service, err := s.requireJobsService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	dir, err := s.q3InstallsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	installation, err := q3install.Load(dir, body.Installation)
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	// Why this engine could not run it here, said before anything starts.
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
	if err := q3run.Preflight(entry, local, body.Action, currentPlatform()); err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	// One engine per installation at a time: a second Run on an installation
	// whose engine is still up would start a second process on the same files.
	if previous := s.q3runs.forInstallation(installation.ID); previous != nil {
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
	request := q3run.Request{
		Installation: installation, EngineProfileID: body.Engine, ActionID: body.Action,
		Options: body.Options, Wait: wait, Label: "Run " + installation.MapName,
	}
	// Resolved once before the wait begins, so a request the engine profile
	// cannot turn into a command is refused here, as a response, rather than
	// becoming a run that fails a moment later.
	if _, err := q3run.Preview(service, request); err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	id, err := newQ3RunID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Not the request's context: the wait must survive the response.
	ctx, cancel := context.WithCancel(context.Background())
	run := &q3Run{
		ID: id, Installation: installation.ID, Engine: body.Engine, Action: body.Action,
		StartedAt: time.Now().UTC(), cancel: cancel,
	}
	request.Started = func(jobID string) {
		run.mu.Lock()
		run.job = jobID
		run.mu.Unlock()
	}
	s.q3runs.add(run)
	go func() {
		defer cancel()
		result, err := q3run.Launch(ctx, service, request)
		run.mu.Lock()
		run.done, run.result = true, result
		if err != nil {
			run.err, run.class = err.Error(), failure.Of(err)
		}
		run.mu.Unlock()
		s.saveQ3Run(run)
		if result != nil {
			s.logf("q3 run %s: map load %s, engine job %s, pid %d", run.ID, result.MapLoad, result.JobID, result.PID)
		} else {
			s.logf("q3 run %s: no engine was started: %v", run.ID, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, run.view())
}

// saveQ3Run writes a finished run where a reloaded page, or a restarted
// Companion, can find it.
func (s *Server) saveQ3Run(run *q3Run) {
	dir, err := s.q3Dir("q3-runs")
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.logf("q3 run %s: recording it: %v", run.ID, err)
		return
	}
	data, err := json.MarshalIndent(run.view(), "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, run.ID+".json"), append(data, '\n'), 0o600); err != nil {
		s.logf("q3 run %s: recording it: %v", run.ID, err)
	}
}

// q3RunView is a run by id: the one this process holds, else the record a
// previous one left.
func (s *Server) q3RunView(id string) (map[string]any, error) {
	if !job.ValidID(id) && !validQ3RunID(id) {
		return nil, fmt.Errorf("%q is not a run id", id)
	}
	if run := s.q3runs.get(id); run != nil {
		return s.withEngineState(run.view()), nil
	}
	dir, err := s.q3Dir("q3-runs")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, fmt.Errorf("no such run: %w", os.ErrNotExist)
	}
	var view map[string]any
	if err := json.Unmarshal(data, &view); err != nil {
		return nil, err
	}
	return s.withEngineState(view), nil
}

func validQ3RunID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		ch := id[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
			return false
		}
	}
	return true
}

// withEngineState adds what the engine job is doing NOW. A run's result is what
// was observed when the wait ended; whether the engine is still up is a fact
// about this moment, read from the job service every time.
func (s *Server) withEngineState(view map[string]any) map[string]any {
	jobID, _ := view["job_id"].(string)
	if jobID == "" || s.jobs == nil {
		return view
	}
	current, err := s.jobs.Get(jobID)
	if err != nil {
		return view
	}
	view["engine_state"] = current.State
	view["engine_running"] = !current.State.Terminal()
	if current.Process.PID != 0 {
		view["engine_pid"] = current.Process.PID
	}
	return view
}

func (s *Server) handleQ3RunGet(w http.ResponseWriter, r *http.Request) {
	view, err := s.q3RunView(r.PathValue("id"))
	if err != nil {
		writeError(w, q3Status(err), err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleQ3RunLatest is the newest run of an installation: what a reloaded page
// asks for, so it shows the engine that is running rather than a blank panel.
func (s *Server) handleQ3RunLatest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if run := s.q3runs.forInstallation(id); run != nil {
		writeJSON(w, http.StatusOK, map[string]any{"run": s.withEngineState(run.view())})
		return
	}
	dir, err := s.q3Dir("q3-runs")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	entries, _ := os.ReadDir(dir)
	var newest map[string]any
	newestName := ""
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() <= newestName {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var view map[string]any
		if json.Unmarshal(data, &view) != nil || view["installation"] != id {
			continue
		}
		newest, newestName = view, entry.Name()
	}
	if newest == nil {
		writeJSON(w, http.StatusOK, map[string]any{"run": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": s.withEngineState(newest)})
}

// handleQ3RunCancel stops a run: the wait when it is still waiting, and the
// engine when it is running. The same cancel a job's own Stop button performs.
func (s *Server) handleQ3RunCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.q3runs.get(id)
	jobID := ""
	if run != nil {
		run.mu.Lock()
		waiting, cancel := !run.done, run.cancel
		jobID = run.job
		run.mu.Unlock()
		if waiting && cancel != nil {
			// The wait stops the engine itself, and records that it did.
			cancel()
			writeJSON(w, http.StatusAccepted, map[string]any{"cancelling": id})
			return
		}
	} else {
		view, err := s.q3RunView(id)
		if err != nil {
			writeError(w, q3Status(err), err)
			return
		}
		jobID, _ = view["job_id"].(string)
	}
	if jobID == "" || s.jobs == nil {
		writeError(w, http.StatusConflict, errors.New("this run started no engine, so there is nothing to stop"))
		return
	}
	stopped, err := s.jobs.Cancel(jobID)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": jobID, "engine_state": stopped.State})
}
