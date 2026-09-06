package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The local job API.
//
// Versioned at /api/v1 because it is the first thing here that something other
// than this repository's own page is meant to drive: `companion job` talks to
// it, and a script may. The unversioned /api routes above it are the page's
// own and are not a contract.
//
// Every route goes through the same guard and the same [job.Service] the CLI
// uses. There is no HTTP-only path into the executor, which is what makes the
// containment and authorization rules in internal/job true for a browser as
// well as for a terminal.

// requireJobs reports the job service, or writes the reason there isn't one.
func (s *Server) requireJobs(w http.ResponseWriter) (*job.Service, bool) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("this build has no job service"))
		return nil, false
	}
	return s.jobs, true
}

// jobStatus maps a service error to the status a caller can act on.
//
// A job that does not exist and a profile that does not exist are both 404: the
// caller named something that is not there. A refused request — an option that
// is not the type it is declared as, an input outside the declared roots — is
// 400, because the request is the thing that is wrong. An ungranted profile is
// 403, because the request is fine and the answer is still no.
func jobStatus(err error) int {
	var notGranted *profile.NotGrantedError
	switch {
	case errors.Is(err, job.ErrNotFound), errors.Is(err, job.ErrNoProfile):
		return http.StatusNotFound
	case errors.As(err, &notGranted):
		return http.StatusForbidden
	case errors.Is(err, job.ErrEscapesRoot):
		return http.StatusBadRequest
	}
	var transition *job.TransitionError
	if errors.As(err, &transition) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// jobRequestBody is the submission shape. Named members rather than a raw
// [job.Request] so the wire format is this file's to keep stable.
type jobRequestBody struct {
	Profile     string            `json:"profile"`
	Action      string            `json:"action"`
	Inputs      map[string]string `json:"inputs,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
	Runtime     map[string]string `json:"runtime,omitempty"`
	Executables map[string]string `json:"executables,omitempty"`
	Roots       map[string]string `json:"roots,omitempty"`
	Label       string            `json:"label,omitempty"`
}

func (b jobRequestBody) toRequest() job.Request {
	return job.Request{
		ProfileID:   b.Profile,
		ActionID:    b.Action,
		Inputs:      b.Inputs,
		Options:     b.Options,
		Runtime:     b.Runtime,
		Executables: b.Executables,
		Roots:       b.Roots,
		Label:       b.Label,
	}
}

func (s *Server) handleJobList(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	jobs, err := service.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if state := r.URL.Query().Get("state"); state != "" {
		filtered := jobs[:0]
		for _, candidate := range jobs {
			if string(candidate.State) == state {
				filtered = append(filtered, candidate)
			}
		}
		jobs = filtered
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("limit=%q is not a count", raw))
			return
		}
		if limit < len(jobs) {
			jobs = jobs[:limit]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs})
}

func (s *Server) handleJobSubmit(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	var body jobRequestBody
	if !decodeJSON(w, r, &body) {
		return
	}
	submitted, err := service.Submit(body.toRequest())
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+submitted.ID)
	writeJSON(w, http.StatusAccepted, submitted)
}

func (s *Server) handleJobPreview(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	var body jobRequestBody
	if !decodeJSON(w, r, &body) {
		return
	}
	previewed, err := service.Preview(body.toRequest())
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, previewed)
}

func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	found, err := service.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, found)
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	cancelled, err := service.Cancel(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, cancelled)
}

func (s *Server) handleJobRetry(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	retried, err := service.Retry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+retried.ID)
	writeJSON(w, http.StatusAccepted, retried)
}

func (s *Server) handleJobLogs(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	stream := r.URL.Query().Get("stream")
	if stream == "" {
		stream = "stdout"
	}
	// The redacted user view by default. `raw=1` is the exact bytes, which a
	// caller has to ask for: it is the evidence, and it is also the copy that
	// can contain a credential a *tool* printed.
	raw := r.URL.Query().Get("raw") == "1"
	data, err := service.Logs(id, stream, raw)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	found, err := service.Get(id)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	summary := found.Stdout
	if stream == "stderr" {
		summary = found.Stderr
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job":     id,
		"stream":  stream,
		"raw":     raw,
		"summary": summary,
		"text":    string(data),
	})
}

func (s *Server) handleJobArtifacts(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	found, err := service.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": found.Artifacts})
}

func (s *Server) handleJobArtifact(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	// Resolved by the store, which checks that the recorded path is inside the
	// job's own artifact directory. Both halves of this arrive from the URL.
	path, err := service.Store().ArtifactPath(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Never sniffed and never rendered: an artifact is a build output, and a
	// browser that decided one was HTML would be running a tool's output as a
	// page on this origin.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filepath.Base(path)))
	http.ServeContent(w, r, "", info.ModTime(), file)
}

func (s *Server) handleProfileList(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	entries, err := service.Catalog().List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, describeProfile(entry))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleProfileGet(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	entry, err := service.Catalog().Lookup(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, describeProfile(entry))
}

// handleProfileValidate checks a document a caller pasted in, without importing
// it and without running anything.
//
// The whole point of the trust model is that reading a stranger's profile is
// inert, so this is the one route that takes a document body: it decodes,
// validates, digests and summarises the permissions, which is exactly what
// somebody needs in front of them before they decide to grant anything.
func (s *Server) handleProfileValidate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("reading the profile document: %w", err))
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no profile document was sent"))
		return
	}
	document, decodeErr := profile.Decode(raw)
	if decodeErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"valid": false,
			"error": decodeErr.Error(),
		})
		return
	}
	digest, err := profile.Digest(document)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	meta := document.Metadata()
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":       true,
		"id":          meta.ID,
		"version":     meta.Version,
		"kind":        meta.Kind,
		"name":        meta.Name,
		"summary":     meta.Summary,
		"digest":      digest,
		"permissions": document.Permissions(),
		// `local` because that is what a document somebody pasted is. Saying so
		// here means the caller is told what it would take to run it, rather
		// than finding out when it is refused.
		"trust":  profile.TrustLocal,
		"report": profile.PermissionReport(document, profile.TrustLocal),
	})
}

func describeProfile(entry job.CatalogEntry) map[string]any {
	meta := entry.Profile.Metadata()
	actions := make([]map[string]any, 0, len(entry.Profile.ActionList()))
	for _, action := range entry.Profile.ActionList() {
		actions = append(actions, map[string]any{
			"id":              action.ID,
			"title":           action.Title,
			"capability":      action.Capability,
			"session_role":    action.SessionRole,
			"inputs":          action.Inputs,
			"outputs":         action.Outputs,
			"options":         action.Options,
			"timeout_seconds": action.TimeoutSeconds,
		})
	}
	return map[string]any{
		"id":          meta.ID,
		"kind":        meta.Kind,
		"version":     meta.Version,
		"name":        meta.Name,
		"summary":     meta.Summary,
		"publisher":   meta.Publisher,
		"license":     meta.License,
		"trust":       entry.Trust,
		"digest":      entry.Digest,
		"source":      entry.Source,
		"permissions": entry.Profile.Permissions(),
		"actions":     actions,
	}
}

// jobAPI is the whole versioned API as a table.
//
// One table, so `jobRoutes` and any test that asserts the surface read the same
// list. A second hand-copied list of routes is a list that goes out of date.
func (s *Server) jobAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/jobs":                       s.handleJobList,
		"POST /api/v1/jobs":                      s.handleJobSubmit,
		"POST /api/v1/jobs/preview":              s.handleJobPreview,
		"GET /api/v1/jobs/{id}":                  s.handleJobGet,
		"POST /api/v1/jobs/{id}/cancel":          s.handleJobCancel,
		"POST /api/v1/jobs/{id}/retry":           s.handleJobRetry,
		"GET /api/v1/jobs/{id}/logs":             s.handleJobLogs,
		"GET /api/v1/jobs/{id}/artifacts":        s.handleJobArtifacts,
		"GET /api/v1/jobs/{id}/artifacts/{name}": s.handleJobArtifact,
		"GET /api/v1/profiles":                   s.handleProfileList,
		"GET /api/v1/profiles/{id}":              s.handleProfileGet,
		"POST /api/v1/profiles/validate":         s.handleProfileValidate,
	}
}

// jobRoutes registers the versioned API, every route behind the same guard.
func (s *Server) jobRoutes(mux *http.ServeMux) {
	for pattern, handler := range s.jobAPI() {
		mux.Handle(pattern, s.guard(handler))
	}
}
