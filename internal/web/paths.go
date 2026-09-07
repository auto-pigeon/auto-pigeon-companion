package web

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/pathpick"
)

// Choosing a path, from a page that is not allowed to look at the disk.
//
// # What these two routes are for
//
// The Companion asks for directories constantly — where Quake is installed,
// where a project lives, where a package should be written. A browser cannot
// answer any of those: `<input type=file>` yields bytes and a bare name, never
// a path, and never a directory at all. See internal/pathpick for the whole
// argument and for the adapters.
//
// # And what they are deliberately not
//
// There is no directory listing here, no stat, no "does this exist" probe, no
// completion. Those would each turn the page into a filesystem browser, and a
// filesystem browser reachable over HTTP is the thing this design is careful
// not to build: the loopback guard stops another origin driving it, but the
// smaller the surface behind that guard, the less a mistake in it can cost.
//
// What the page can do is ask the user, through the desktop's own chooser, and
// receive the one path they picked. What it can do when there is no chooser is
// send a path the user typed, to be validated. Both answers go through
// [pathpick.Check], and neither reveals anything the user did not choose.

func (s *Server) pathAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"POST /api/v1/paths/pick":     s.handlePathPick,
		"POST /api/v1/paths/validate": s.handlePathValidate,
	}
}

type pathRequest struct {
	// Kind is `directory`, `open-file` or `save-file`.
	Kind string `json:"kind"`
	// Title is what the dialog asks. The page supplies it because the page
	// knows which field the user clicked beside.
	Title string `json:"title,omitempty"`
	// StartDir is where to open. A hint; an unusable one is dropped rather
	// than refused, because a stale remembered directory must not stop a user
	// opening a dialog.
	StartDir string `json:"start_dir,omitempty"`
	// Path is the typed answer, for the validate route.
	Path string `json:"path,omitempty"`
	// Filters narrow a file dialog's type menu.
	Filters []struct {
		Name       string   `json:"name"`
		Extensions []string `json:"extensions"`
	} `json:"filters,omitempty"`
}

func (s *Server) handlePathPick(w http.ResponseWriter, r *http.Request) {
	var request pathRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	kind := pathpick.Kind(request.Kind)
	if !kind.Valid() {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("%q is not a kind of path this can ask for", request.Kind))
		return
	}
	picked := pathpick.Request{Kind: kind, Title: request.Title}
	// A start directory that is no longer there is not an error: the user
	// moved or deleted it, and the dialog should still open.
	if request.StartDir != "" {
		if resolved, err := pathpick.Check(pathpick.Directory, request.StartDir); err == nil {
			picked.StartDir = resolved
		}
	}
	for _, filter := range request.Filters {
		picked.Filters = append(picked.Filters,
			pathpick.Filter{Name: filter.Name, Extensions: filter.Extensions})
	}

	// The request's context, so closing the tab or navigating away closes the
	// dialog with it rather than leaving a window over the user's desktop
	// belonging to a page that has gone.
	result, err := s.picker.Pick(r.Context(), picked)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{
			"path": result.Path, "helper": result.Helper, "cancelled": false,
		})
	case errors.Is(err, pathpick.ErrCancelled):
		// 200, not an error status. Nothing went wrong: a person was asked a
		// question and declined to answer it, and a page that showed a red
		// message for that would be arguing with them.
		writeJSON(w, http.StatusOK, map[string]any{"cancelled": true, "helper": result.Helper})
	case errors.Is(err, pathpick.ErrNoHelper):
		// 501: this installation cannot do it, and the page's answer is to
		// show its text field. Distinguished from a helper that failed,
		// because the two need different things from the user.
		writeError(w, http.StatusNotImplemented, err)
	case errors.Is(err, pathpick.ErrBusy):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

// handlePathValidate checks a path the user typed.
//
// The same [pathpick.Check] the dialog's answer goes through, for the same
// reason: neither is trusted, and having one function decide what a usable path
// is means a text field and a file chooser cannot disagree about it.
func (s *Server) handlePathValidate(w http.ResponseWriter, r *http.Request) {
	var request pathRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	kind := pathpick.Kind(request.Kind)
	if !kind.Valid() {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("%q is not a kind of path this can check", request.Kind))
		return
	}
	resolved, err := pathpick.Check(kind, request.Path)
	if err != nil {
		// 200 with `valid: false`: the caller asked a question and got an
		// answer. The field it is filling in is not in an error state yet —
		// the user is still typing.
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "path": resolved})
}

// checkDirectory and checkOpenFile are [pathpick.Check] for the two cases every
// other handler in this package needs: a directory a user supplied, and a file
// a user supplied. Named here so no handler reaches for its own idea of what a
// usable path is.
func checkDirectory(path string) (string, error) {
	return pathpick.Check(pathpick.Directory, path)
}

func checkOpenFile(path string) (string, error) {
	return pathpick.Check(pathpick.OpenFile, path)
}
