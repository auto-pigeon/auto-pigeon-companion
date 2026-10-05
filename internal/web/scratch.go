package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"runtime"
	"strings"
)

// A profile from scratch (NEW_244D, at the operator's request).
//
// # What this is for
//
// The template wizard edits a document somebody already tested: it can rename
// a program and drop an action, and — on purpose — it cannot add one. That is
// right for "my QuakeSpasm build is called quakespasm-sdl2", and it is a wall
// for a toolchain nobody has written a profile for: a compiler with four
// programs instead of three, a pipeline for a game this build has never heard
// of, a stage with parameters of its own.
//
// # How it keeps the one profile system
//
// The page still never builds JSON. It posts STRUCTURED FIELDS — programs,
// actions with their arguments, inputs, outputs and options, pipeline steps
// with their wiring and parameters — and this file turns them into the same
// document tree the template path produces. From there the document goes
// through exactly what every other document goes through: applyComposeFields
// for identity, profile.Decode for validation, profile.Export for the canonical
// bytes and the digest, the import route to install it, the approval service to
// grant it and the binding route to say where its programs are. Nothing here
// grants, binds or trusts anything; a document composed here is `local` and is
// refused until somebody approves it.
//
// # What it deliberately cannot say
//
// Anything the schema does not define, and any path on this machine: a
// program's `file` is relative to the folder it is later bound to, and
// CheckPortable refuses an absolute one. An engine's game family is AUB's
// vocabulary (quake1, quake2, quake3) and cannot be invented here; a tool or a
// pipeline for a game outside it simply names no game profile, which the
// schema allows.

type scratchDocument struct {
	// Kind is tool, pipeline or engine.
	Kind string `json:"kind"`
	// BasedOn is the tested profile the fields were filled from (scratchfill.go),
	// empty for one written from nothing. It decides nothing the fields say:
	// it only keeps what the form has no field for.
	BasedOn string `json:"based_on,omitempty"`
	// GameFamily is optional for a tool or pipeline and required for an engine.
	GameFamily string `json:"game_family,omitempty"`
	GameSlug   string `json:"game_slug,omitempty"`

	ToolVersion string              `json:"tool_version,omitempty"`
	Executables []scratchExecutable `json:"executables,omitempty"`
	Actions     []scratchAction     `json:"actions,omitempty"`

	Inputs  []scratchPort           `json:"inputs,omitempty"`
	Steps   []scratchStep           `json:"steps,omitempty"`
	Outputs []scratchPipelineOutput `json:"outputs,omitempty"`
}

type scratchExecutable struct {
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	File  string `json:"file"`
}

type scratchAction struct {
	WorkingDir  *profile.WorkingDir        `json:"working_dir,omitempty"`
	Environment *profile.EnvironmentPolicy `json:"environment,omitempty"`

	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Capability string          `json:"capability,omitempty"`
	Executable string          `json:"executable"`
	Args       []string        `json:"args,omitempty"`
	Inputs     []scratchPort   `json:"inputs,omitempty"`
	Outputs    []scratchOutput `json:"outputs,omitempty"`
	Options    []scratchOption `json:"options,omitempty"`
	Timeout    int             `json:"timeout_seconds,omitempty"`
	// Roots are folders on this machine the program reads or writes besides
	// its job folder — a texture folder, a game directory. The document names
	// only the ROLE; where the folder is is bound on the machine, in the
	// profile's setup, like a program's path.
	Roots       []scratchRoot `json:"roots,omitempty"`
	SessionRole string        `json:"session_role,omitempty"`
}

type scratchRoot struct {
	Role     string `json:"role"`
	Optional bool   `json:"optional,omitempty"`
	Access   string `json:"access,omitempty"`
	Purpose  string `json:"purpose,omitempty"`
}

type scratchPort struct {
	Name       string   `json:"name"`
	Title      string   `json:"title,omitempty"`
	Role       string   `json:"role"`
	Required   bool     `json:"required,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
	StageWith  string   `json:"stage_with,omitempty"`
}

type scratchOutput struct {
	Extension string `json:"extension,omitempty"`
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	Role      string `json:"role"`
	Path      string `json:"path,omitempty"`
	InPlace   string `json:"in_place,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
}

type scratchOption struct {
	Name    string   `json:"name"`
	Title   string   `json:"title,omitempty"`
	Type    string   `json:"type"`
	Default string   `json:"default,omitempty"`
	Values  []string `json:"values,omitempty"`
}

type scratchStep struct {
	ID         string            `json:"id"`
	Title      string            `json:"title,omitempty"`
	Capability string            `json:"capability"`
	Inputs     map[string]string `json:"inputs,omitempty"`
	Options    map[string]string `json:"options,omitempty"`
}

type scratchPipelineOutput struct {
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	Role     string `json:"role"`
	From     string `json:"from"`
	Optional bool   `json:"optional,omitempty"`
}

// scratchPlaceholderID is what an unnamed document is called until the
// identity step names it, so a half-filled form still composes and the
// validator can say what else is missing.
const scratchPlaceholderID = "local.unnamed"

// scratchTree turns the structured fields into a document tree.
func scratchTree(scratch scratchDocument) (map[string]any, error) {
	tree := map[string]any{
		"schema_version": "aucom.profile/1.1",
		"id":             scratchPlaceholderID,
		"version":        "1.0.0",
		"name":           "Unnamed profile",
		"summary":        "Written on this machine.",
		"publisher":      map[string]any{"name": "Local"},
		"license":        map[string]any{"spdx": "NOASSERTION", "name": "Not stated"},
	}
	if scratch.GameFamily != "" {
		slug := scratch.GameSlug
		if slug == "" {
			slug = scratch.GameFamily
		}
		tree["game_profile"] = map[string]any{"slug": slug, "engine_family": scratch.GameFamily}
	}
	switch scratch.Kind {
	case "tool":
		tree["kind"] = "tool"
		tree["tool_version"] = orDefault(scratch.ToolVersion, "unknown")
		tree["platforms"] = hostPlatformRow()
		tree["acquisition"] = userPathOnly()
		tree["executables"] = scratchExecutables(scratch.Executables)
		actions, err := scratchActions(scratch.Actions, false)
		if err != nil {
			return nil, err
		}
		tree["actions"] = actions
		tree["capabilities"] = scratchCapabilities(scratch.Actions)
	case "engine":
		if scratch.GameFamily == "" {
			return nil, errors.New("an engine belongs to a game family: choose quake1, quake2 or quake3")
		}
		tree["kind"] = "engine"
		tree["runtime"] = "local"
		tree["engine_version"] = "unknown"
		tree["platforms"] = hostPlatformRow()
		tree["acquisition"] = userPathOnly()
		tree["executables"] = scratchExecutables(scratch.Executables)
		actions, err := scratchActions(scratch.Actions, true)
		if err != nil {
			return nil, err
		}
		tree["actions"] = actions
	case "pipeline":
		tree["kind"] = "pipeline"
		inputs := make([]any, 0, len(scratch.Inputs))
		for _, port := range scratch.Inputs {
			inputs = append(inputs, scratchPortTree(port, false))
		}
		tree["inputs"] = inputs
		steps := make([]any, 0, len(scratch.Steps))
		for _, step := range scratch.Steps {
			wired := make([]any, 0, len(step.Inputs))
			for _, name := range sortedKeys(step.Inputs) {
				if from := strings.TrimSpace(step.Inputs[name]); from != "" {
					wired = append(wired, map[string]any{"name": name, "from": from})
				}
			}
			entry := map[string]any{
				"id": step.ID, "title": orDefault(step.Title, step.ID),
				"capability": step.Capability, "inputs": wired,
			}
			options := map[string]any{}
			for _, name := range sortedKeys(step.Options) {
				if value := strings.TrimSpace(step.Options[name]); value != "" {
					options[name] = value
				}
			}
			if len(options) > 0 {
				entry["options"] = options
			}
			steps = append(steps, entry)
		}
		tree["steps"] = steps
		if len(scratch.Outputs) > 0 {
			outputs := make([]any, 0, len(scratch.Outputs))
			for _, output := range scratch.Outputs {
				entry := map[string]any{
					"name": output.Name, "title": orDefault(output.Title, output.Name),
					"role": output.Role, "from": output.From,
				}
				if output.Optional {
					entry["optional"] = true
				}
				outputs = append(outputs, entry)
			}
			tree["outputs"] = outputs
		}
	default:
		return nil, fmt.Errorf("a profile from scratch is a tool, a pipeline or an engine, not %q", scratch.Kind)
	}
	if err := applyScratchActionSettings(tree, scratch.Actions); err != nil {
		return nil, err
	}
	return tree, nil
}

// engineSessionRoles is the session role each engine action requires — the
// schema's own table (internal/profile's requiredSessionRole), restated for
// the one caller that fills it in on a person's behalf. A mismatch is still
// refused by the validator.
var engineSessionRoles = map[string]string{
	"play_map": "client", "play_package": "client", "join_server": "client",
	"host_listen": "listen_server", "host_dedicated": "dedicated_server",
}

func hostPlatformRow() []any {
	return []any{map[string]any{
		"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH},
		"status":   "unverified",
		"note":     "written on this machine; its author has not recorded a verification",
	}}
}

// userPathOnly is the one honest acquisition route for a document written
// here: the programs are ones the user already has, and nothing downloads or
// verifies them.
func userPathOnly() []any {
	return []any{map[string]any{
		"mode":  "user_path",
		"title": "Use a copy you already have",
		"hint":  "choose the folder that holds the programs this profile names",
	}}
}

func scratchExecutables(list []scratchExecutable) []any {
	out := make([]any, 0, len(list))
	for _, executable := range list {
		entry := map[string]any{"name": executable.Name, "file": executable.File}
		if executable.Title != "" {
			entry["title"] = executable.Title
		}
		out = append(out, entry)
	}
	return out
}

func scratchActions(list []scratchAction, engine bool) ([]any, error) {
	out := make([]any, 0, len(list))
	for _, action := range list {
		args := make([]any, 0, len(action.Args))
		for _, line := range action.Args {
			arg, err := scratchArg(line)
			if err != nil {
				return nil, fmt.Errorf("action %q: %w", action.ID, err)
			}
			if arg != nil {
				args = append(args, arg)
			}
		}
		entry := map[string]any{
			"id": action.ID, "title": orDefault(action.Title, action.ID),
			"executable":  action.Executable,
			"args":        args,
			"working_dir": map[string]any{"root": "workspace"},
		}
		roots := []any{map[string]any{
			"role": "workspace", "access": "read_write", "purpose": "run in a folder made for this job",
		}}
		for _, root := range action.Roots {
			if strings.TrimSpace(root.Role) == "" || root.Role == "workspace" {
				continue
			}
			entry := map[string]any{
				"role": strings.TrimSpace(root.Role), "access": orDefault(root.Access, "read"),
				"purpose": orDefault(root.Purpose, "read files the program needs"),
			}
			if root.Optional {
				entry["optional"] = true
			}
			roots = append(roots, entry)
		}
		entry["roots"] = roots
		if action.Capability != "" {
			entry["capability"] = action.Capability
		}
		if engine {
			// An engine action's session role follows from which of the five
			// actions it is; the page does not ask a person to know that.
			role := action.SessionRole
			if role == "" {
				role = engineSessionRoles[action.ID]
			}
			if role != "" {
				entry["session_role"] = role
			}
		}
		if len(action.Inputs) > 0 {
			inputs := make([]any, 0, len(action.Inputs))
			for _, port := range action.Inputs {
				inputs = append(inputs, scratchPortTree(port, true))
			}
			entry["inputs"] = inputs
		}
		if len(action.Outputs) > 0 {
			outputs := make([]any, 0, len(action.Outputs))
			for _, output := range action.Outputs {
				item := map[string]any{"name": output.Name, "title": orDefault(output.Title, output.Name), "role": output.Role}
				if output.InPlace != "" {
					item["in_place"] = output.InPlace
					if output.Extension != "" {
						item["extension"] = output.Extension
					}
				} else {
					item["path"] = output.Path
				}
				if output.Optional {
					item["optional"] = true
				}
				outputs = append(outputs, item)
			}
			entry["outputs"] = outputs
		}
		if len(action.Options) > 0 {
			options := make([]any, 0, len(action.Options))
			for _, option := range action.Options {
				item := map[string]any{"name": option.Name, "title": orDefault(option.Title, option.Name), "type": option.Type}
				if option.Default != "" {
					item["default"] = option.Default
				}
				if option.Type == "enum" {
					values := make([]any, 0, len(option.Values))
					for _, value := range option.Values {
						if value = strings.TrimSpace(value); value != "" {
							values = append(values, map[string]any{"value": value, "title": value})
						}
					}
					item["values"] = values
				}
				options = append(options, item)
			}
			entry["options"] = options
		}
		if action.Timeout > 0 {
			entry["timeout_seconds"] = action.Timeout
		}
		out = append(out, entry)
	}
	return out, nil
}

// scratchArg reads one argument line.
//
//	-threads                    a literal
//	{option.threads}            a placeholder the schema defines
//	-fast [if fast]             only when the boolean option `fast` is true
//	-level [if fast=false]      only when option `fast` equals `false`
//	-wadpath [if folder content_root]   only when that optional folder is set
//	-wad [if input wad]         only when the person supplied that input
//
// Nothing is split on spaces: one line is one argv element, which is what
// keeps a path with a space in it one argument and keeps a shell out of it.
func scratchArg(line string) (any, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	value, condition, found := strings.Cut(line, " [if ")
	if !found {
		return line, nil
	}
	if !strings.HasSuffix(condition, "]") {
		return nil, fmt.Errorf("argument %q: a condition is written `[if option]` or `[if option=value]`", line)
	}
	condition = strings.TrimSuffix(condition, "]")
	if role, isFolder := strings.CutPrefix(strings.TrimSpace(condition), "folder "); isFolder {
		return map[string]any{"value": strings.TrimSpace(value), "when": map[string]any{"root": strings.TrimSpace(role)}}, nil
	}
	if name, isInput := strings.CutPrefix(strings.TrimSpace(condition), "input "); isInput {
		return map[string]any{"value": strings.TrimSpace(value), "when": map[string]any{"input": strings.TrimSpace(name)}}, nil
	}
	option, equals, hasValue := strings.Cut(condition, "=")
	when := map[string]any{"option": strings.TrimSpace(option)}
	if hasValue {
		when["equals"] = strings.TrimSpace(equals)
	}
	return map[string]any{"value": strings.TrimSpace(value), "when": when}, nil
}

func scratchPortTree(port scratchPort, action bool) map[string]any {
	entry := map[string]any{"name": port.Name, "title": orDefault(port.Title, port.Name), "role": port.Role}
	if port.Required {
		entry["required"] = true
	}
	if len(port.Extensions) > 0 {
		extensions := make([]any, 0, len(port.Extensions))
		for _, extension := range port.Extensions {
			if extension = strings.TrimSpace(extension); extension != "" {
				if !strings.HasPrefix(extension, ".") {
					extension = "." + extension
				}
				extensions = append(extensions, extension)
			}
		}
		entry["extensions"] = extensions
	}
	if action && port.StageWith != "" {
		entry["stage_with"] = port.StageWith
	}
	return entry
}

// scratchCapabilities declares each capability the actions provide, with what
// it consumes and produces read off the action's own input and output roles —
// so a pipeline's check that a stage can be fed is answered by the same
// document, not by a second list somebody has to keep in step.
func scratchCapabilities(actions []scratchAction) []any {
	seen := map[string]bool{}
	out := []any{}
	for _, action := range actions {
		if action.Capability == "" || seen[action.Capability] {
			continue
		}
		seen[action.Capability] = true
		entry := map[string]any{"id": action.Capability, "title": orDefault(action.Title, action.Capability)}
		consumes, produces := []any{}, []any{}
		for _, port := range action.Inputs {
			if port.Role != "" {
				consumes = append(consumes, port.Role)
			}
		}
		for _, output := range action.Outputs {
			if output.Role != "" {
				produces = append(produces, output.Role)
			}
		}
		if len(consumes) > 0 {
			entry["consumes"] = consumes
		}
		if len(produces) > 0 {
			entry["produces"] = produces
		}
		out = append(out, entry)
	}
	return out
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// Expressed settings override a template's formerly unexpressed defaults.
// A nil field retains the tested document's original declaration.
func applyScratchActionSettings(tree map[string]any, actions []scratchAction) error {
	for _, action := range actions {
		for _, row := range objects(tree, "actions") {
			if text(row, "id") != action.ID {
				continue
			}
			for key, value := range map[string]any{"working_dir": action.WorkingDir, "environment": action.Environment} {
				if key == "working_dir" && action.WorkingDir == nil || key == "environment" && action.Environment == nil {
					continue
				}
				encoded, err := json.Marshal(value)
				if err != nil {
					return err
				}
				var fields map[string]any
				if err := json.Unmarshal(encoded, &fields); err != nil {
					return err
				}
				row[key] = fields
			}
		}
	}
	return nil
}
