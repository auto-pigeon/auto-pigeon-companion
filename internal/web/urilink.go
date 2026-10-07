package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/urischeme"
)

// The operating system's handler for `autopigeon://` links, from the page
// (`NEW_307W`).
//
// A link from the editor or from Live Games reaches this program only when the
// operating system has been told that it opens them, and until now the one way
// to say so was `companion uri register` in a terminal. The operator's own
// machine ran a current Companion with no handler at all: the editor's "Test in
// Companion" waited for ever and nothing anywhere said why. So Settings shows
// whether this Companion opens those links, and has the button.
//
// Both routes are behind Server.guard. They do exactly what the command does,
// for this user only, through the same `urischeme.Registrar` — there is no
// second writer of the registration.

// newURIRegistrar is the registrar the routes use. A variable so a test can
// act on a registry and a desktop that are not the developer's own.
var newURIRegistrar = func() *urischeme.Registrar { return &urischeme.Registrar{} }

func (s *Server) uriAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/uri":           s.handleURIStatus,
		"POST /api/v1/uri/register": s.handleURIRegister,
	}
}

func (s *Server) handleURIStatus(w http.ResponseWriter, _ *http.Request) {
	state, err := newURIRegistrar().Status()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleURIRegister(w http.ResponseWriter, _ *http.Request) {
	state, err := newURIRegistrar().Register()
	if errors.Is(err, urischeme.ErrNotPerformable) {
		// Not a failure of this program: the platform registers another way
		// (a macOS bundle's Info.plist), and the state's sentence says which.
		writeJSON(w, http.StatusOK, state)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Recorded like the first-use registration (NEW_310), so a later launch
	// knows this program's handler is the one a person chose.
	if s.updateConfig != nil && state.Handler != "" {
		if _, err := s.updateConfig(func(c *config.Config) error {
			c.URIHandler = &config.URIHandler{Executable: state.Handler, Method: state.Method,
				At: time.Now().UTC(), How: "settings"}
			return nil
		}); err != nil {
			s.logf("link handler: registered, but recording it in config.json failed: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, state)
}
