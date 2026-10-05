package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// One way to write a profile (operator, 2026-10-03): "unify Start from a tested
// profile with Write a profile from scratch — in the dropdown the default will
// be From scratch, and as you pick an item the fields are filled
// automatically."
//
// So a tested profile is no longer a second, narrower wizard that can rename a
// program and drop an action. It is a way of FILLING the one form: this file
// turns a built-in document into the same structured fields scratch.go reads,
// and the person edits them as if they had typed them — add a program, add an
// action, add a stage, change an argument.
//
// # Nothing the form cannot show is lost
//
// The form has no field for everything a document can say: an action's
// environment, the transcripts a compiler writes beside its outputs, an
// option's bounds, the platforms table, how the programs are acquired. A
// profile filled from a tested one and composed again must not quietly drop
// those — that would turn "tested" into "tested, minus the parts that made the
// live output work". So the composed document is the form's fields with every
// member the form does NOT own carried over from the profile it was filled
// from (mergeUnexpressed), matched by id or name. A program, action, stage or
// option the person removed stays removed; one they added has only what they
// typed.
//
// The proof is a test: every built-in, filled into the form and composed back
// unchanged, is the same document.

// scratchIdentity is the identity step's fields, as a tested profile fills them.
type scratchIdentity struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Summary       string `json:"summary,omitempty"`
	Description   string `json:"description,omitempty"`
	PublisherName string `json:"publisher_name,omitempty"`
	PublisherURL  string `json:"publisher_url,omitempty"`
	LicenseSPDX   string `json:"license_spdx,omitempty"`
	LicenseName   string `json:"license_name,omitempty"`
	Homepage      string `json:"homepage,omitempty"`
	Runtime       string `json:"runtime,omitempty"`
	EngineVersion string `json:"engine_version,omitempty"`
}

// handleProfileTemplateScratch is one tested profile as the form's fields.
//
//	GET /api/v1/profiles/templates/{id}/scratch
func (s *Server) handleProfileTemplateScratch(w http.ResponseWriter, r *http.Request) {
	entry, err := builtin.Find(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	tree, err := profileTree(entry.Profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	scratch, identity := scratchFromTree(tree)
	scratch.BasedOn = entry.Profile.Metadata().ID
	writeJSON(w, http.StatusOK, map[string]any{
		"id": scratch.BasedOn, "kind": scratch.Kind, "scratch": scratch, "identity": identity,
	})
}

func profileTree(document profile.Profile) (map[string]any, error) {
	encoded, err := profile.Export(document)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := json.Unmarshal(encoded, &tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// --- a document, as the form's fields ----------------------------------------

func text(node map[string]any, key string) string {
	value, _ := node[key].(string)
	return value
}

func objects(node map[string]any, key string) []map[string]any {
	list, _ := node[key].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, raw := range list {
		if item, ok := raw.(map[string]any); ok {
			out = append(out, item)
		}
	}
	return out
}

func stringList(node map[string]any, key string) []string {
	list, _ := node[key].([]any)
	out := make([]string, 0, len(list))
	for _, raw := range list {
		if item, ok := raw.(string); ok {
			out = append(out, item)
		}
	}
	return out
}

func flag(node map[string]any, key string) bool {
	value, _ := node[key].(bool)
	return value
}

// scratchFromTree is scratchTree read backwards.
func scratchFromTree(tree map[string]any) (scratchDocument, scratchIdentity) {
	identity := scratchIdentity{
		Name: text(tree, "name"), Version: text(tree, "version"), Summary: text(tree, "summary"),
		Description: text(tree, "description"), Runtime: text(tree, "runtime"), EngineVersion: text(tree, "engine_version"),
	}
	if publisher, ok := tree["publisher"].(map[string]any); ok {
		identity.PublisherName, identity.PublisherURL = text(publisher, "name"), text(publisher, "url")
	}
	if license, ok := tree["license"].(map[string]any); ok {
		identity.LicenseSPDX, identity.LicenseName = text(license, "spdx"), text(license, "name")
	}
	if source, ok := tree["source"].(map[string]any); ok {
		identity.Homepage = text(source, "homepage")
	}

	scratch := scratchDocument{Kind: text(tree, "kind"), ToolVersion: text(tree, "tool_version")}
	if game, ok := tree["game_profile"].(map[string]any); ok {
		scratch.GameFamily, scratch.GameSlug = text(game, "engine_family"), text(game, "slug")
	}
	for _, executable := range objects(tree, "executables") {
		scratch.Executables = append(scratch.Executables, scratchExecutable{
			Name: text(executable, "name"), Title: text(executable, "title"), File: text(executable, "file"),
		})
	}
	for _, action := range objects(tree, "actions") {
		filled := scratchAction{
			ID: text(action, "id"), Title: text(action, "title"), Capability: text(action, "capability"),
			Executable: text(action, "executable"), SessionRole: text(action, "session_role"),
		}
		if working, ok := action["working_dir"]; ok {
			encoded, _ := json.Marshal(working)
			_ = json.Unmarshal(encoded, &filled.WorkingDir)
		}
		if environment, ok := action["environment"]; ok {
			encoded, _ := json.Marshal(environment)
			_ = json.Unmarshal(encoded, &filled.Environment)
		}
		if seconds, ok := action["timeout_seconds"].(float64); ok {
			filled.Timeout = int(seconds)
		}
		if raw, ok := action["args"].([]any); ok {
			for _, arg := range raw {
				filled.Args = append(filled.Args, scratchArgLine(arg))
			}
		}
		for _, port := range objects(action, "inputs") {
			filled.Inputs = append(filled.Inputs, scratchPortFrom(port))
		}
		for _, output := range objects(action, "outputs") {
			filled.Outputs = append(filled.Outputs, scratchOutput{
				Name: text(output, "name"), Title: text(output, "title"), Role: text(output, "role"),
				Path: text(output, "path"), InPlace: text(output, "in_place"), Extension: text(output, "extension"), Optional: flag(output, "optional"),
			})
		}
		for _, option := range objects(action, "options") {
			item := scratchOption{
				Name: text(option, "name"), Title: text(option, "title"), Type: text(option, "type"), Default: text(option, "default"),
			}
			for _, value := range objects(option, "values") {
				item.Values = append(item.Values, text(value, "value"))
			}
			filled.Options = append(filled.Options, item)
		}
		for _, root := range objects(action, "roots") {
			if text(root, "role") == "workspace" {
				continue
			}
			filled.Roots = append(filled.Roots, scratchRoot{
				Role: text(root, "role"), Optional: flag(root, "optional"), Access: text(root, "access"), Purpose: text(root, "purpose"),
			})
		}
		scratch.Actions = append(scratch.Actions, filled)
	}
	if scratch.Kind == "pipeline" {
		for _, port := range objects(tree, "inputs") {
			scratch.Inputs = append(scratch.Inputs, scratchPortFrom(port))
		}
		for _, step := range objects(tree, "steps") {
			filled := scratchStep{
				ID: text(step, "id"), Title: text(step, "title"), Capability: text(step, "capability"),
				Inputs: map[string]string{}, Options: map[string]string{},
			}
			for _, wire := range objects(step, "inputs") {
				filled.Inputs[text(wire, "name")] = text(wire, "from")
			}
			if options, ok := step["options"].(map[string]any); ok {
				for name, value := range options {
					filled.Options[name] = fmt.Sprint(value)
				}
			}
			scratch.Steps = append(scratch.Steps, filled)
		}
		for _, output := range objects(tree, "outputs") {
			scratch.Outputs = append(scratch.Outputs, scratchPipelineOutput{
				Name: text(output, "name"), Title: text(output, "title"), Role: text(output, "role"),
				From: text(output, "from"), Optional: flag(output, "optional"),
			})
		}
	}
	return scratch, identity
}

func scratchPortFrom(port map[string]any) scratchPort {
	return scratchPort{
		Name: text(port, "name"), Title: text(port, "title"), Role: text(port, "role"),
		Required: flag(port, "required"), Extensions: stringList(port, "extensions"), StageWith: text(port, "stage_with"),
	}
}

// scratchArgLine is scratchArg read backwards: one argument as the line a
// person would type for it.
func scratchArgLine(arg any) string {
	switch typed := arg.(type) {
	case string:
		return typed
	case map[string]any:
		value := text(typed, "value")
		when, _ := typed["when"].(map[string]any)
		switch {
		case when == nil:
			return value
		case text(when, "root") != "":
			return value + " [if folder " + text(when, "root") + "]"
		case text(when, "input") != "":
			return value + " [if input " + text(when, "input") + "]"
		case text(when, "equals") != "":
			return value + " [if " + text(when, "option") + "=" + text(when, "equals") + "]"
		default:
			return value + " [if " + text(when, "option") + "]"
		}
	}
	return ""
}

// --- what the form has no field for -------------------------------------------

// formOwned is, for each place in a document, the members the form's fields
// decide. Everything else there is carried over from the profile the form was
// filled from. A member listed here that the person emptied stays empty.
var formOwned = map[string][]string{
	"": {"schema_version", "kind", "id", "name", "version", "summary",
		"game_profile", "tool_version", "executables", "actions", "capabilities", "inputs", "steps", "outputs"},
	"executables": {"name", "title", "file"},
	"actions": {"id", "title", "capability", "executable", "args", "inputs", "outputs", "options", "roots",
		"timeout_seconds", "session_role"},
	"actions.inputs":         {"name", "title", "role", "required", "extensions", "stage_with"},
	"actions.outputs":        {"name", "title", "role", "path", "in_place", "extension", "optional"},
	"actions.options":        {"name", "title", "type", "default", "values"},
	"actions.options.values": {"value"},
	"actions.roots":          {"role", "optional", "access", "purpose"},
	"capabilities":           {"id", "title", "consumes", "produces"},
	"inputs":                 {"name", "title", "role", "required", "extensions"},
	"steps":                  {"id", "title", "capability", "inputs", "options"},
	"outputs":                {"name", "title", "role", "from", "optional"},
}

// formGuessed is, for each place, the members scratch.go fills in on a
// person's behalf because the form does not ask for them: which platforms a
// tool runs on, how it is acquired, where an action runs, what a capability is
// called. Written from nothing those guesses are all there is; filled from a
// tested profile, the tested profile's own answer is the right one.
var formGuessed = map[string][]string{
	"":                       {"platforms", "acquisition", "runtime", "engine_version", "publisher", "license"},
	"actions":                {"working_dir"},
	"actions.options.values": {"title"},
	"capabilities":           {"title", "consumes", "produces"},
}

// mergeUnexpressed copies into `tree` every member of `base` the form does not
// own, at `path` and below, matching list items by id, name, role or value.
func mergeUnexpressed(tree, base map[string]any, path string) {
	owned := map[string]bool{}
	for _, name := range formOwned[path] {
		owned[name] = true
	}
	for _, name := range formGuessed[path] {
		if value, present := base[name]; present {
			tree[name] = value
		} else {
			delete(tree, name)
		}
	}
	if path == "actions" {
		mergeWorkspaceRoot(tree, base)
	}
	if path == "steps" {
		// The form keeps a stage's wiring by input name; the tested order is
		// the one a digest was computed over.
		order := map[string]int{}
		for index, wire := range objects(base, "inputs") {
			order[text(wire, "name")] = index
		}
		if wires, ok := tree["inputs"].([]any); ok {
			sort.SliceStable(wires, func(i, j int) bool {
				a, known := order[text(wires[i].(map[string]any), "name")]
				b, alsoKnown := order[text(wires[j].(map[string]any), "name")]
				return known && alsoKnown && a < b
			})
		}
	}
	names := make([]string, 0, len(base))
	for name := range base {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, present := tree[name]; !present && !owned[name] {
			tree[name] = base[name]
		}
	}
	for name, value := range tree {
		child := name
		if path != "" {
			child = path + "." + name
		}
		if _, described := formOwned[child]; !described {
			continue
		}
		items, _ := value.([]any)
		baseItems, _ := base[name].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			if item == nil {
				continue
			}
			if match := matchingItem(baseItems, item); match != nil {
				mergeUnexpressed(item, match, child)
			}
		}
	}
}

// mergeWorkspaceRoot puts an action's job-folder root back the way the tested
// profile declares it — its access, its purpose, or not at all (an engine runs
// in its game folder) — and its folders back in the tested order. The form
// lists every OTHER folder; the job folder is one it adds by itself.
func mergeWorkspaceRoot(action, base map[string]any) {
	var workspace map[string]any
	order := map[string]int{}
	for index, root := range objects(base, "roots") {
		order[text(root, "role")] = index
		if text(root, "role") == "workspace" {
			workspace = root
		}
	}
	roots := []any{}
	for _, root := range objects(action, "roots") {
		if text(root, "role") != "workspace" {
			roots = append(roots, root)
		} else if workspace != nil {
			roots = append(roots, workspace)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool {
		a, known := order[text(roots[i].(map[string]any), "role")]
		b, alsoKnown := order[text(roots[j].(map[string]any), "role")]
		return known && alsoKnown && a < b
	})
	if len(roots) == 0 {
		if _, declared := base["roots"]; !declared {
			delete(action, "roots")
			return
		}
	}
	action["roots"] = roots
}

func matchingItem(candidates []any, item map[string]any) map[string]any {
	for _, key := range []string{"id", "name", "role", "value"} {
		want := text(item, key)
		if want == "" {
			continue
		}
		for _, raw := range candidates {
			if candidate, _ := raw.(map[string]any); candidate != nil && text(candidate, key) == want {
				return candidate
			}
		}
		return nil
	}
	return nil
}

// applyBasedOn is the step composeBase takes for a form that was filled from a
// tested profile.
func applyBasedOn(tree map[string]any, basedOn string) error {
	basedOn = strings.TrimSpace(basedOn)
	if basedOn == "" {
		return nil
	}
	entry, err := builtin.Find(basedOn)
	if err != nil {
		return fmt.Errorf("the profile these fields were filled from is not a tested one: %w", err)
	}
	base, err := profileTree(entry.Profile)
	if err != nil {
		return err
	}
	if text(base, "kind") != text(tree, "kind") {
		return fmt.Errorf("%s is a %s, and this form describes a %s: choose From scratch or a tested %s",
			basedOn, text(base, "kind"), text(tree, "kind"), text(tree, "kind"))
	}
	mergeUnexpressed(tree, base, "")
	return nil
}
