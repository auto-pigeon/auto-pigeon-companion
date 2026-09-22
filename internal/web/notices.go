package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// Operational notices: scheduled maintenance and similar statements an
// operator publishes once, in AUB, shown here as a compact banner.
//
// # What this route is, and is not
//
// A RELAY. `GET /api/v1/notices` asks AUB's public
// `GET /api/operational-notices?surface=aucom` through the one AUB client, and
// hands the page AUB's answer as it came: the body byte for byte, the ETag,
// and AUB's clock in X-Auto-Pigeon-Server-Time. It does not parse, select or
// reorder notices — the page does that with the vendored AULIBS contract
// (assets/vendor/operational-notice-contract), so the Companion runs the same
// parser and the same selection AUP and AUG run, rather than a Go
// transcription of it.
//
// It exists at all because the page cannot reach AUB itself — its CSP allows
// only this origin — and because only this process holds the session: a
// signed-in person is asked for as `authenticated`, and may be served notices
// meant only for signed-in accounts. Signed out, or with an expired session,
// no token is sent and AUB answers with the public notices only.
//
// # Nothing is kept
//
// This process writes nothing about notices to disk. The page may cache the
// PUBLIC part of a response in its own storage (the contract's
// `cacheableResponse` removes every authenticated notice first), which is what
// keeps a planned-downtime notice on screen while AUB is down.

// noticeTimeout bounds one relay. The page polls every 60–120 seconds, so a
// request that has not answered in this long is a failed poll, not a slow one.
const noticeTimeout = 10 * time.Second

// noticeAccountHeader carries the opaque account key the page files
// dismissals under, so dismissing on one account does not hide a notice on
// another. Never an e-mail address or a token.
const noticeAccountHeader = "X-AUCOM-Notice-Account"

func (s *Server) noticesAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/notices": s.handleNotices,
	}
}

// noticeAccount is the key dismissals are filed under: the account id, or a
// digest of the e-mail for a session stored before ids were, or "" signed out.
func noticeAccount(session config.Session) string {
	if session.UserID != "" {
		return "u:" + session.UserID
	}
	if session.Email != "" {
		sum := sha256.Sum256([]byte(strings.ToLower(session.Email)))
		return "e:" + hex.EncodeToString(sum[:8])
	}
	return ""
}

func (s *Server) handleNotices(w http.ResponseWriter, r *http.Request) {
	s.adoptSessionFromDisk()
	client := s.aubClient()
	if client == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: config.ErrAUBNotConfigured.Error(), Code: "aub_not_configured"})
		return
	}
	authenticated := client.Authenticated() && !client.SessionExpired(time.Now())

	ctx, cancel := context.WithTimeout(r.Context(), noticeTimeout)
	defer cancel()
	ifNoneMatch := r.Header.Get("If-None-Match")
	result, err := client.OperationalNotices(ctx, authenticated, ifNoneMatch)
	var apiErr *aub.APIError
	if err != nil && authenticated && errors.As(err, &apiErr) && apiErr.Unauthorized() {
		// The server no longer accepts the session. Public notices are still
		// public: ask again as nobody, rather than showing nothing.
		authenticated = false
		result, err = client.OperationalNotices(ctx, false, "")
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errorBody{
			Error: "the Auto-Pigeon server's notices could not be read", Code: "notices_unavailable",
		})
		return
	}

	header := w.Header()
	header.Set("Cache-Control", "no-store")
	if result.ETag != "" {
		header.Set("ETag", result.ETag)
	}
	if result.ServerTime != "" {
		header.Set(aub.ServerTimeHeader, result.ServerTime)
	}
	if authenticated {
		header.Set(noticeAccountHeader, noticeAccount(s.config().Session))
	}
	if result.Status == http.StatusNotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if !json.Valid(result.Body) {
		writeJSON(w, http.StatusBadGateway, errorBody{
			Error: "the Auto-Pigeon server's notices were not readable", Code: "notices_unavailable",
		})
		return
	}
	header.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Body)
}
