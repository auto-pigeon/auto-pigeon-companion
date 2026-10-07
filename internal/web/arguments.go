package web

import (
	"errors"
	"fmt"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
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
	// A build tool is where a program IS; what it is run with is said by the
	// pipeline stage that runs it (operator, 2026-10-03). Tokens recorded on a
	// tool before that still apply and can be removed here, and nothing new is
	// added. An engine is not a stage of anything and keeps its own.
	if _, isTool := entry.Profile.(*profile.ToolProfile); isTool && len(tokens) > 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"arguments are set on a pipeline stage, not on the build tool: open the pipeline that runs %s and add them to its stage "+
				"(or `companion toolchain args <pipeline> <stage> --set=<token>`). Tokens recorded here earlier still apply and can be removed", name))
		return
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

// --- a pipeline's own parameters, per stage ----------------------------------
//
// The operator's rule (2026-10-03): "in build tools you set up the paths and
// metadata of tools, in pipelines you pick a tool and add the parameters". So a
// stage of a pipeline carries its own argument tokens, recorded in THIS
// machine's binding for the pipeline — `step_arguments` — exactly as a tool's
// were: the document is not touched, a built-in pipeline stays what shipped, an
// export carries none of it, and each token is one argv element checked by
// [profile.ValidateCustomArgs] here, in the binding and when the command is
// resolved. Per stage rather than per program, so one pipeline can run the
// same tool twice with different tokens.
//
//	POST /api/v1/profiles/{id}/stage-arguments  {"stage":"compile","arguments":["-nopercent"]}
//	POST /api/v1/profiles/{id}/stage-arguments  {"stage":"compile","reset":true}
//	GET  /api/v1/profiles/{id}                  `stages`: each stage, its tool and its tokens

type stageArgumentsRequest struct {
	Stage     string   `json:"stage"`
	Arguments []string `json:"arguments"`
	Reset     bool     `json:"reset,omitempty"`
}

func (s *Server) handleProfileStageArguments(w http.ResponseWriter, r *http.Request) {
	var request stageArgumentsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	entry, _, err := s.profileEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	meta := entry.Profile.Metadata()
	pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
	if !isPipeline {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s is not a pipeline; stage arguments belong to a pipeline's stages", meta.Name))
		return
	}
	stage := strings.TrimSpace(request.Stage)
	known := false
	for _, step := range pipeline.Steps {
		known = known || step.ID == stage
	}
	if stage == "" || !known {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s has no stage called %q", meta.Name, stage))
		return
	}
	tokens := request.Arguments
	if request.Reset {
		tokens = nil
	}
	if err := profile.ValidateCustomArgs(tokens); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("the arguments for the stage %s: %w", stage, err))
		return
	}
	path, err := s.bindingsPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	local, err := binding.SetStepArguments(path, meta.ID, meta.Version, entry.Digest, entry.Trust, stage, tokens)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.describeCatalogEntry(entry, local))
}

// describeStages is a pipeline's stages as the page shows them: what each one
// needs done, which installed tool does it here, and the tokens this machine
// adds. A stage nothing installed can run says so rather than being left out.
func (s *Server) describeStages(pipeline *profile.PipelineProfile, local binding.LocalBinding) []map[string]any {
	catalog, err := s.catalog()
	if err != nil {
		return s.describeStagesFrom(pipeline, local, nil, nil)
	}
	entries, _ := catalog.List()
	set, _, _ := s.bindings()
	return s.describeStagesFrom(pipeline, local, entries, set)
}

// describeStagesFrom is [Server.describeStages] over a catalog listing and
// bindings the caller has already read.
func (s *Server) describeStagesFrom(pipeline *profile.PipelineProfile, local binding.LocalBinding,
	entries []job.CatalogEntry, set *binding.Set) []map[string]any {
	type provider struct {
		entry  map[string]any
		tokens []string
	}
	providers := map[string]provider{}
	// A stage that names its tool is run by that tool (NEW_310): keyed by
	// tool and capability, so the page never names a different provider of
	// the same capability — found live on Windows, where a stage naming the
	// user's own EricW was shown as run by the built-in one.
	byTool := map[string]provider{}
	for _, candidate := range entries {
		tool, isTool := candidate.Profile.(*profile.ToolProfile)
		if !isTool {
			continue
		}
		for _, action := range tool.Actions {
			if action.Capability == "" {
				continue
			}
			found := provider{entry: map[string]any{
				"profile_id": tool.Meta.ID, "profile_name": tool.Meta.Name,
				"action_id": action.ID, "action_title": action.Title, "executable": action.Executable,
			}}
			if set != nil {
				if toolBinding, bound := set.Find(tool.Meta.ID); bound {
					found.tokens = toolBinding.Arguments[action.Executable]
				}
			}
			if _, taken := byTool[tool.Meta.ID+"\x00"+action.Capability]; !taken {
				byTool[tool.Meta.ID+"\x00"+action.Capability] = found
			}
			if _, taken := providers[action.Capability]; !taken {
				providers[action.Capability] = found
			}
		}
	}
	stages := make([]map[string]any, 0, len(pipeline.Steps))
	for _, step := range pipeline.Steps {
		stage := map[string]any{
			"id": step.ID, "title": step.Title, "capability": step.Capability,
			"options": step.Options, "optional": step.Optional,
			"arguments": append([]string{}, local.StepArguments[step.ID]...),
		}
		if step.Tool != "" {
			stage["named_tool"] = step.Tool
		}
		found, ok := providers[step.Capability]
		if step.Tool != "" {
			found, ok = byTool[step.Tool+"\x00"+step.Capability]
		}
		if ok {
			stage["tool"] = found.entry
			// Tokens recorded on the tool itself before parameters moved to
			// pipelines: they still reach this stage, and the page says so.
			if len(found.tokens) > 0 {
				stage["tool_arguments"] = found.tokens
			}
		}
		stages = append(stages, stage)
	}
	return stages
}
