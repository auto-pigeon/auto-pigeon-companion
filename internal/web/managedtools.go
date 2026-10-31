package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The Companion's side of the managed toolchains (NEW_367): the executor's
// version check, the binding writer and the work registry that
// internal/managedtools asks for through [managedtools.Host], and the routes
// the page and `companion tools` use.

// toolGate is held by every pipeline run for its whole length, and taken
// exclusively by an activation for the moment it rewrites the bindings. A
// pipeline that started before the switch keeps the programs it started
// with; one that starts after sees the new ones; none sees both.
type toolGate struct {
	mu    sync.Mutex
	holds int
}

func (g *toolGate) hold() func() {
	g.mu.Lock()
	g.holds++
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.holds--
			g.mu.Unlock()
		})
	}
}

func (g *toolGate) running() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.holds
}

// errToolsInUse is an activation that found a pipeline running after all.
var errToolsInUse = errors.New("a build started; the update waits for it")

// exclusive runs fn while no pipeline holds the gate, and keeps any from
// starting until it returns.
func (g *toolGate) exclusive(fn func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.holds > 0 {
		return errToolsInUse
	}
	return fn()
}

// toolHost is [managedtools.Host] for this server.
type toolHost struct{ s *Server }

// Verify runs the strict built-in profile's version check against the
// installation's programs, through the executor, before anything is bound.
func (h toolHost) Verify(_ context.Context, tool managedtools.Tool, record managedtools.InstallRecord) error {
	service, err := h.s.requireJobsService()
	if err != nil {
		return err
	}
	facts, err := service.ProbeAt(managedtools.StrictProfile(tool.ID), record.Programs)
	if err != nil {
		return err
	}
	if len(facts) == 0 {
		return fmt.Errorf("the built-in profile checks none of %s's programs", tool.Title)
	}
	for _, name := range sortedNames(facts) {
		fact := facts[name]
		switch {
		case fact.State != binding.FactCompatible:
			return fmt.Errorf("%s: %s %s", name, fact.State, fact.Detail)
		case fact.Identity != record.Banner:
			return fmt.Errorf("%s says it is %q; the release says %q", name, fact.Identity, record.Banner)
		}
	}
	return nil
}

func sortedNames(facts map[string]binding.ExecutableFact) []string {
	names := make([]string, 0, len(facts))
	for name := range facts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// managedBound reports whether the Companion may (re)bind a profile itself:
// nothing is chosen for it, or what is bound is the Companion's own
// installation. A program a person chose is theirs.
func managedBound(local binding.LocalBinding, found bool) bool {
	return !found || len(local.Executables) == 0 || local.Acquisition == profile.AcquireManagedOfficial
}

// Activate binds every profile of the tool that nobody else chose to the
// installation, in one write of bindings.json, so Quake 1 and Quake II never
// name two EricW versions. The programs' facts are cleared with the paths —
// installed is not ready until the programs answer — and asked for again at
// once; the version check that verified the installation already left them in
// the executor's cache, so asking again runs nothing.
func (h toolHost) Activate(tool managedtools.Tool, record managedtools.InstallRecord) error {
	bound, err := h.s.bindManaged(tool, record, nil)
	if err != nil {
		return err
	}
	if h.s.toolsExiting.Load() {
		// At a close the executor has already stopped: the facts stay
		// cleared, and the next start asks the programs (RefreshCompatibility)
		// before anything can run them.
		return nil
	}
	for _, id := range bound {
		if err := h.s.checkPrograms(id, false); err != nil {
			h.s.logf("managed tools: checking %s after binding %s %s: %v", id, tool.ID, record.Version, err)
		}
	}
	return nil
}

// bindManaged writes the managed bindings. only, when not nil, names the one
// profile a person asked to switch to the managed installation.
func (s *Server) bindManaged(tool managedtools.Tool, record managedtools.InstallRecord, only *string) ([]string, error) {
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	path, err := s.bindingsPath()
	if err != nil {
		return nil, err
	}
	var bound []string
	write := func() error {
		_, err := binding.Update(path, func(set *binding.Set) error {
			bound = bound[:0]
			for _, id := range tool.Profiles {
				if only != nil && *only != id {
					continue
				}
				entry, err := catalog.Lookup(id)
				if err != nil {
					continue // a build without this built-in profile
				}
				document, ok := entry.Profile.(*profile.ToolProfile)
				if !ok {
					continue
				}
				local, found := set.Find(id)
				if only == nil && !managedBound(local, found) {
					continue
				}
				if local.ProfileDigest != entry.Digest {
					local.Grant = nil
				}
				local.ProfileID = id
				local.ProfileVersion = document.Version
				local.ProfileDigest = entry.Digest
				local.Trust = entry.Trust
				local.Acquisition = profile.AcquireManagedOfficial
				local.Installs = nil
				local.Executables = map[string]string{}
				for _, declared := range document.Executables {
					if program, ok := record.Programs[declared.Name]; ok {
						local.Executables[declared.Name] = program
					}
				}
				local.Compatibility = nil
				local.ResolvedVersion, local.VersionCheckedAt = "", time.Time{}
				local.Managed = &binding.ManagedInstall{Tool: string(tool.ID), Version: record.Version,
					Tag: record.Tag, SHA256: record.ActualSHA256, Dir: record.Dir}
				local.UpdatedAt = time.Now().UTC()
				if err := set.Put(local); err != nil {
					return err
				}
				bound = append(bound, id)
			}
			return nil
		})
		return err
	}
	if err := s.toolGate.exclusive(write); err != nil {
		return nil, err
	}
	return bound, nil
}

// Busy names the work that uses dirs, and every pipeline that is running:
// a pipeline resolves its next stage's program when that stage starts.
func (h toolHost) Busy(_ managedtools.Tool, dirs []string) []string {
	var out []string
	if h.s.jobs != nil {
		for _, j := range h.s.jobs.Active() {
			if j.Command == nil {
				continue
			}
			for _, dir := range dirs {
				if within(dir, j.Command.Executable) {
					label := j.ActionTitle
					if label == "" {
						label = j.ActionID
					}
					out = append(out, label+" — "+j.ProfileID)
				}
			}
		}
	}
	if n := h.s.toolGate.running(); n > 0 {
		out = append(out, fmt.Sprintf("%d build(s) running", n))
	}
	return out
}

func within(dir, path string) bool {
	relative, err := filepath.Rel(dir, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// --- routes -------------------------------------------------------------------

func (s *Server) toolsAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/tools":                        s.handleToolsStatus,
		"POST /api/v1/tools/check":                 s.handleToolsCheck,
		"POST /api/v1/tools/install":               s.handleToolsInstall,
		"POST /api/v1/tools/{tool}/retry":          s.handleToolsRetry,
		"POST /api/v1/tools/{tool}/decide":         s.handleToolsDecide,
		"POST /api/v1/tools/{tool}/cancel-pending": s.handleToolsCancelPending,
		"POST /api/v1/tools/skipped/reset":         s.handleToolsResetSkipped,
		"POST /api/v1/checks/retry":                s.handleChecksRetry,
		"POST /api/v1/profiles/{id}/use-managed":   s.handleProfileUseManaged,
	}
}

// toolProfileRow is one built-in profile a managed tool serves, and whether
// its binding is the managed installation or something a person chose.
type toolProfileRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Managed bool   `json:"managed"`
	// Custom is a binding a person chose; the page offers the managed
	// installation as an alternative and never replaces it by itself.
	Custom  bool   `json:"custom"`
	Version string `json:"version,omitempty"`
}

type toolsBody struct {
	managedtools.Status
	Available bool                                     `json:"available"`
	Profiles  map[managedtools.ToolID][]toolProfileRow `json:"profiles"`
	// Checks is the background checks running now, and CheckFailures
	// the last failure of each (NEW_368) — with or without managed tools.
	Checks        []checkRow     `json:"checks,omitempty"`
	CheckFailures []checkFailure `json:"check_failures,omitempty"`
}

func (s *Server) toolsStatus() toolsBody {
	body := toolsBody{Profiles: map[managedtools.ToolID][]toolProfileRow{}}
	body.Checks, body.CheckFailures = s.checks()
	if s.tools == nil {
		return body
	}
	body.Available = true
	body.Status = s.tools.Status()
	set, _, _ := s.bindings()
	catalog, _ := s.catalog()
	for _, tool := range managedtools.Tools {
		for _, id := range tool.Profiles {
			row := toolProfileRow{ID: id, Name: id}
			if catalog != nil {
				if entry, err := catalog.Lookup(id); err == nil {
					row.Name = entry.Profile.Metadata().Name
				}
			}
			if set != nil {
				if local, found := set.Find(id); found && len(local.Executables) > 0 {
					row.Managed = local.Acquisition == profile.AcquireManagedOfficial
					row.Custom = !row.Managed
					if local.Managed != nil {
						row.Version = local.Managed.Version
					}
				}
			}
			body.Profiles[tool.ID] = append(body.Profiles[tool.ID], row)
		}
	}
	return body
}

func (s *Server) handleToolsStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.toolsStatus())
}

func (s *Server) requireTools(w http.ResponseWriter) bool {
	if s.tools == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New(
			"automatic build-tool setup is not running in this Companion (it is off in server mode unless started with --managed-tools on)"))
		return false
	}
	return true
}

func (s *Server) handleToolsCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireTools(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	outcomes := s.tools.CheckNow(ctx)
	body := s.toolsStatus()
	writeJSON(w, http.StatusOK, map[string]any{"outcomes": outcomes, "status": body})
}

// handleToolsInstall is a person's Install: the toolchains they approved.
func (s *Server) handleToolsInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireTools(w) {
		return
	}
	var request struct {
		Tools []managedtools.ToolID `json:"tools"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.tools.ApproveInstall(request.Tools); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toolsStatus())
}

func toolParam(w http.ResponseWriter, r *http.Request) (managedtools.ToolID, bool) {
	id := managedtools.ToolID(r.PathValue("tool"))
	if _, ok := managedtools.Find(id); !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("%q is not a managed tool", id))
		return "", false
	}
	return id, true
}

func (s *Server) handleToolsRetry(w http.ResponseWriter, r *http.Request) {
	id, ok := toolParam(w, r)
	if !ok || !s.requireTools(w) {
		return
	}
	if err := s.tools.Retry(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toolsStatus())
}

type decideRequest struct {
	Decision managedtools.Decision `json:"decision"`
	Version  string                `json:"version"`
	AssetID  int64                 `json:"asset_id"`
	SHA256   string                `json:"sha256"`
	DontAsk  bool                  `json:"dont_ask"`
}

func (s *Server) handleToolsDecide(w http.ResponseWriter, r *http.Request) {
	id, ok := toolParam(w, r)
	if !ok || !s.requireTools(w) {
		return
	}
	var request decideRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.DontAsk && request.Decision != managedtools.DecideCancel {
		writeError(w, http.StatusBadRequest, errors.New("\"don't ask again\" goes with Cancel"))
		return
	}
	err := s.tools.Decide(id, request.Decision, managedtools.BytesIdentity{
		Version: request.Version, AssetID: request.AssetID, SHA256: request.SHA256}, request.DontAsk)
	switch {
	case errors.Is(err, managedtools.ErrStaleOffer):
		writeError(w, http.StatusConflict, err)
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toolsStatus())
}

func (s *Server) handleToolsCancelPending(w http.ResponseWriter, r *http.Request) {
	id, ok := toolParam(w, r)
	if !ok || !s.requireTools(w) {
		return
	}
	if err := s.tools.CancelPending(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toolsStatus())
}

func (s *Server) handleToolsResetSkipped(w http.ResponseWriter, r *http.Request) {
	if !s.requireTools(w) {
		return
	}
	var request struct {
		Tool managedtools.ToolID `json:"tool"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &request) {
		return
	}
	if request.Tool != "" {
		if _, ok := managedtools.Find(request.Tool); !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("%q is not a managed tool", request.Tool))
			return
		}
	}
	if err := s.tools.ResetSkipped(request.Tool); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toolsStatus())
}

// handleProfileUseManaged is "Use the Companion's installation" for a
// profile somebody had pointed elsewhere: the explicit choice the Companion
// never makes on its own.
func (s *Server) handleProfileUseManaged(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tool, ok := managedtools.ForProfile(id)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s is not set up from a managed toolchain", id))
		return
	}
	if !s.requireTools(w) {
		return
	}
	record, installed := s.tools.ActiveInstall(tool.ID)
	if !installed {
		writeError(w, http.StatusConflict, fmt.Errorf("%s is not installed yet", tool.Title))
		return
	}
	if _, err := s.bindManaged(tool, record, &id); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err := s.checkPrograms(id, false); err != nil {
		s.logf("managed tools: checking %s: %v", id, err)
	}
	entry, local, err := s.profileEntry(id)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.describeCatalogEntry(entry, local))
}

// StartManagedTools runs the coordinator for as long as ctx, first applying
// an update somebody chose for a close that never happened cleanly.
func (s *Server) StartManagedTools(ctx context.Context) {
	if s.tools == nil {
		return
	}
	for _, line := range s.tools.ApplyAtStart() {
		s.logf("managed tools: %s", line)
	}
	go s.tools.Run(ctx)
}

// ApplyToolUpdatesOnExit is the close half of "Update when I close
// Companion", called once the work has ended.
func (s *Server) ApplyToolUpdatesOnExit() []string {
	if s.tools == nil {
		return nil
	}
	s.toolsExiting.Store(true)
	return s.tools.ApplyOnExit()
}

// ManagedToolsHost is this server's host for a coordinator built by the
// caller; [Server.SetManagedTools] attaches the coordinator.
func (s *Server) ManagedToolsHost() managedtools.Host { return toolHost{s: s} }

// SetManagedTools attaches the coordinator. Before the server serves.
func (s *Server) SetManagedTools(m *managedtools.Manager) { s.tools = m }
