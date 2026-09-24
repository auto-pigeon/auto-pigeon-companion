package web

import (
	"io"
	"net/http"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// Report a bug (operator, 2026-09-22: "copy its behaviour, and code if
// possible, from AUG"). The dialog is AUG's: the shared incident contract,
// vendored byte for byte under assets/vendor/incident-contract, builds the one
// document the person reviews, downloads, opens as a prefilled GitHub issue or
// sends. These two routes only carry that document to AUB and AUB's answer
// back, unchanged. Nothing is sent without the person's press and the
// acknowledgement that a report is public.

func (s *Server) bugReportRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/bug-reports/status": s.handleBugReportStatus,
		"POST /api/v1/bug-reports":       s.handleBugReportSend,
	}
}

func (s *Server) handleBugReportStatus(w http.ResponseWriter, r *http.Request) {
	client := s.aubClient()
	if client == nil {
		writeJSON(w, http.StatusOK, map[string]any{"server_route": "unavailable", "reason": "no_server"})
		return
	}
	s.relayBugReport(w, r, client, http.MethodGet, aub.BugReportStatusPath, nil)
}

func (s *Server) handleBugReportSend(w http.ResponseWriter, r *http.Request) {
	client, ok := s.requireSession(w)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.relayBugReport(w, r, client, http.MethodPost, aub.BugReportPath, body)
}

func (s *Server) relayBugReport(w http.ResponseWriter, r *http.Request, client *aub.Client, method, path string, body []byte) {
	status, answer, err := client.BugReportRelay(r.Context(), method, path, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(answer)
}
