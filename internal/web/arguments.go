package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// A person's own argument tokens, per executable (NEW_265).
//
// Profiles declare every knob they know about as a typed option, and that
// stays the default. What this adds is the escape hatch a map maker needs for
// the flag the profile's author did not think of: a list of argv tokens for one
// program, recorded in this machine's binding beside where the program is. The
// document is not touched — a built-in profile stays exactly what shipped, an
// exported profile carries nothing of it, and a Companion update that ships a
// new document keeps it — and nothing is ever joined into a command line or
// handed to a shell: each token is one argv element, validated by
// [profile.ValidateCustomArgs] here, again in the binding, and again when a
// command is resolved.
//
//	POST /api/v1/profiles/{id}/arguments  {"executable":"qbsp","arguments":["-nopercent"]}
//	POST /api/v1/profiles/{id}/arguments  {"executable":"qbsp","reset":true}
//	GET  /api/v1/profiles/{id}/commands   every action's effective command, and where the tokens went

type argumentsRequest struct {
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
	// Reset removes this executable's tokens: back to the profile's own
	// command.
	Reset bool `json:"reset,omitempty"`
}

func (s *Server) handleProfileArguments(w http.ResponseWriter, r *http.Request) {
	var request argumentsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	entry, _, err := s.profileEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	meta := entry.Profile.Metadata()
	name := strings.TrimSpace(request.Executable)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name the program these arguments are for"))
		return
	}
	if !declaredExecutables(entry.Profile)[name] {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s declares no program called %q", meta.Name, name))
		return
	}
	tokens := request.Arguments
	if request.Reset {
		tokens = nil
	}
	if err := profile.ValidateCustomArgs(tokens); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("the arguments for %s: %w", name, err))
		return
	}

	path, err := s.bindingsPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	local, err := binding.SetArguments(path, meta.ID, meta.Version, entry.Digest, entry.Trust, name, tokens)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	body := s.describeCatalogEntry(entry, local)
	body["commands"] = effectiveCommands(entry.Profile, local)
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleProfileCommands(w http.ResponseWriter, r *http.Request) {
	entry, local, err := s.profileEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": effectiveCommands(entry.Profile, local)})
}

// effectiveCommands is [binding.EffectiveCommands] as the page reads it.
func effectiveCommands(document profile.Profile, local binding.LocalBinding) []map[string]any {
	items := make([]map[string]any, 0)
	for _, command := range binding.EffectiveCommands(document, local, currentPlatform()) {
		item := map[string]any{"action_id": command.ActionID, "title": command.Title, "executable": command.Executable}
		if command.Error != "" {
			item["error"] = command.Error
		} else {
			item["argv"] = command.Argv
			item["custom_args"] = command.CustomArgs
			if len(command.CustomArgs) > 0 {
				item["custom_at"] = command.CustomAt
			}
		}
		items = append(items, item)
	}
	return items
}
