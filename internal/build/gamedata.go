package build

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// Game data: what a Quake III build is allowed to read, staged.
//
// # Why the runner does this and not the caller (Q3_010)
//
// A Quake III build reads two directories of game data — the base game's and
// the user's own — and internal/q3vfs explains why the compiler is not handed
// them as they are. It is done HERE, once, for the reason every other rule about
// roots is in this package: the terminal, the Build page and an automatic
// rebuild all reach a build through [Runner.Run], and a staging step each of
// them had to remember would be a staging step one of them would not do.
//
// It is keyed on the pipeline's `engine_family`, which is the document's own
// statement of which game it builds. A Quake 1 build is not touched: `-wadpath`
// names one directory of WADs, there is no mod name to join onto it and no
// archive the compiler opens by itself.
//
// # Where the mod name comes from
//
// Not from an option called `mod`. Which option carries the name is the tool
// document's business, so it is read out of the document: the argument that
// follows a literal `-fs_game` is a template, and the option it names is the
// one. A profile somebody else wrote for another Q3Map2 build, with the option
// called something else, is staged by the same rule.

const (
	// familyQuake3 is AUB's engine family for id Tech 3, as a pipeline
	// document's `game_profile.engine_family` spells it.
	familyQuake3 = "quake3"
	// fsGameSwitch is the argument Q3Map2 takes a mod directory name after.
	fsGameSwitch = "-fs_game"
	// stagedDirName is where a build's staged game data lives, in its own
	// directory beside `input`, `stage` and `output`.
	stagedDirName = "vfs"
)

// RootFromBinding: a root the user configured for the tool, as opposed to one
// supplied for this build. Recorded for a staged build, whose manifest names
// every folder the compiler was allowed to see.
const RootFromBinding RootSourceKind = "tool_binding"

// gameDataRoles are the roots a Quake III build stages.
var gameDataRoles = []string{profile.RootGame, profile.RootContent}

// stagesGameData reports whether a pipeline's builds stage their game data.
func stagesGameData(family string) bool { return family == familyQuake3 }

// fsGameOption is the name of the option an action sends after `-fs_game`, or
// "" when the action has no such argument.
func fsGameOption(action profile.Action) string {
	for i, arg := range action.Args {
		if arg.Value != fsGameSwitch || i+1 >= len(action.Args) {
			continue
		}
		next := action.Args[i+1].Value
		if strings.HasPrefix(next, "{option.") && strings.HasSuffix(next, "}") {
			return strings.TrimSuffix(strings.TrimPrefix(next, "{option."), "}")
		}
	}
	return ""
}

// BoundPackages is what a saved map says it is built with: the game folders it
// names, and each package it binds, already fetched and verified by the caller
// (internal/q3packages) against the digest the map records.
//
// Supplied by the caller for the reason [Request.Sources] is: this package must
// not learn to talk to a backend. What it does with them is its own business —
// they are staged, by name and digest, and the manifest records each one.
type BoundPackages struct {
	// BaseRoot and ModRoot are the map's own `base_root` and `mod_root`.
	BaseRoot string
	ModRoot  string
	Packages []q3vfs.Package
}

// withModRoot gives every step the map's own mod folder when the build named
// none, and refuses a build that named a different one.
//
// The map's document says which mod folder it is built with. A build that
// silently compiled it as a plain base-game map would read none of the mod's
// packages, and one that compiled it against another mod would read the wrong
// ones; neither is the map the editor showed.
func withModRoot(request Request, steps []profile.ResolvedStep) (Request, error) {
	bound := request.Packages
	if bound == nil {
		return request, nil
	}
	if bound.BaseRoot != "" && !strings.EqualFold(bound.BaseRoot, q3vfs.BaseGame) {
		return request, failure.As(failure.ContentRefused, fmt.Errorf(
			"the map is bound to packages for the base folder %q, and this pipeline builds %q maps: "+
				"its compiler reads `%s` and would not see them", bound.BaseRoot, q3vfs.BaseGame, q3vfs.BaseGame))
	}
	if bound.ModRoot == "" || strings.EqualFold(bound.ModRoot, q3vfs.BaseGame) {
		return request, nil
	}
	options := make(map[string]map[string]string, len(request.Options)+len(steps))
	for step, values := range request.Options {
		copied := make(map[string]string, len(values))
		for name, value := range values {
			copied[name] = value
		}
		options[step] = copied
	}
	for _, resolved := range steps {
		option := fsGameOption(resolved.Action)
		if option == "" {
			continue
		}
		asked, set := request.Options[resolved.Step.ID][option]
		if !set {
			asked, set = resolved.Step.Options[option]
		}
		switch {
		case !set || asked == "":
			if options[resolved.Step.ID] == nil {
				options[resolved.Step.ID] = map[string]string{}
			}
			options[resolved.Step.ID][option] = bound.ModRoot
		case asked != bound.ModRoot:
			return request, failure.As(failure.FSGameInvalid, fmt.Errorf(
				"the map is bound to packages for the mod folder %q, and the %s step asks for %q: "+
					"build it as the map says, or change the map's mod folder in the editor",
				bound.ModRoot, resolved.Step.ID, asked))
		}
	}
	request.Options = options
	return request, nil
}

// fsGame is the one mod directory name a build's steps agree on.
//
// Three stages of one compiler, each with its own option, is three chances to
// name three mods, and a BSP compiled against one mod's shaders and lit
// against another's is not a build of anything. So they must agree, and a
// build in which they do not is refused before it stages a file.
func fsGame(request Request, steps []profile.ResolvedStep) (string, error) {
	name, first := "", ""
	for i, resolved := range steps {
		option := fsGameOption(resolved.Action)
		value := ""
		if option != "" {
			for _, spec := range resolved.Action.Options {
				if spec.Name == option {
					value = spec.Default
				}
			}
			if set, ok := resolved.Step.Options[option]; ok {
				value = set
			}
			if set, ok := request.Options[resolved.Step.ID][option]; ok {
				value = set
			}
		}
		if i == 0 {
			name, first = value, resolved.Step.ID
			continue
		}
		if value != name {
			return "", failure.As(failure.FSGameInvalid, fmt.Errorf(
				"the %s step names the mod directory %q and the %s step names %q; "+
					"every stage of one build reads the same game data, so set the same name on each",
				first, name, resolved.Step.ID, value))
		}
	}
	if err := q3vfs.CheckFSGame(name); err != nil {
		return "", err
	}
	return name, nil
}

// approvedRoots is, for each game data role a step declares, the folder the
// user approved for it: the one this build was given, else the one the tool is
// bound to.
func (r *Runner) approvedRoots(request Request, steps []profile.ResolvedStep) (map[string]string, map[string]RootSourceKind, map[string]profile.Access) {
	paths := map[string]string{}
	kinds := map[string]RootSourceKind{}
	access := map[string]profile.Access{}
	for _, resolved := range steps {
		for _, declared := range resolved.Action.Roots {
			if !containsString(gameDataRoles, declared.Role) {
				continue
			}
			access[declared.Role] = declared.Access
			if _, done := paths[declared.Role]; done {
				continue
			}
			if supplied := strings.TrimSpace(request.Roots[declared.Role]); supplied != "" {
				paths[declared.Role] = supplied
				continue
			}
			if local, bound := r.binding(resolved.Profile.Meta.ID); bound && local.Roots[declared.Role] != "" {
				paths[declared.Role] = local.Roots[declared.Role]
				kinds[declared.Role] = RootFromBinding
			}
		}
	}
	return paths, kinds, access
}

// stageGameData stages a Quake III build's game data into the build directory
// and returns the request the steps are then run with: the same request, with
// each game data root replaced by its staged directory.
//
// The manifest is given the stage and — for a root that came from the tool's
// binding rather than from the request — the record of where it came from, so
// every folder the compiler could see is named in one place.
func (r *Runner) stageGameData(request Request, steps []profile.ResolvedStep, layout layout, manifest *Manifest) (Request, error) {
	request, err := withModRoot(request, steps)
	if err != nil {
		return request, err
	}
	name, err := fsGame(request, steps)
	if err != nil {
		return request, err
	}
	var packages []q3vfs.Package
	if request.Packages != nil {
		packages = request.Packages.Packages
	}
	approved, kinds, access := r.approvedRoots(request, steps)
	for _, role := range gameDataRoles {
		if role == q3vfs.PackagesRole && len(packages) > 0 {
			// The map's own packages ARE its content: a build of a saved map
			// needs no content folder beside them.
			continue
		}
		if _, declared := access[role]; declared && approved[role] == "" {
			return request, failure.As(failure.GameDataMissing, fmt.Errorf(
				"no folder is set for %q: a Quake III build reads the base game data and your own content from "+
					"folders you choose, and the Companion supplies neither. Choose one for this build, or set it on the tool",
				role))
		}
	}
	stage, err := q3vfs.Build(q3vfs.Request{
		FSGame:   name,
		Roots:    approved,
		Dir:      filepath.Join(layout.Dir, stagedDirName),
		Packages: packages,
	})
	if err != nil {
		return request, err
	}
	manifest.GameData = stage

	roots := make(map[string]string, len(request.Roots)+len(approved))
	for role, path := range request.Roots {
		roots[role] = path
	}
	for role, path := range stage.Paths() {
		roots[role] = path
	}
	for role, kind := range kinds {
		absolute, err := filepath.Abs(approved[role])
		if err != nil {
			absolute = approved[role]
		}
		manifest.Roots = append(manifest.Roots, RootRecord{
			Role: role, Path: absolute, Access: access[role], Source: &RootSource{Kind: kind},
		})
	}
	sort.SliceStable(manifest.Roots, func(i, j int) bool { return manifest.Roots[i].Role < manifest.Roots[j].Role })

	staged := request
	staged.Roots = roots
	return staged, nil
}

// previewGameData is the same substitution for a preview, which stages
// nothing: each game data root becomes the place this build WOULD stage it.
func (r *Runner) previewGameData(request Request, steps []profile.ResolvedStep) (Request, error) {
	request, err := withModRoot(request, steps)
	if err != nil {
		return request, err
	}
	if _, err := fsGame(request, steps); err != nil {
		return request, err
	}
	_, _, access := r.approvedRoots(request, steps)
	roots := make(map[string]string, len(request.Roots)+len(access))
	for role, path := range request.Roots {
		roots[role] = path
	}
	for role := range access {
		roots[role] = filepath.Join(previewBuild, stagedDirName, role)
	}
	staged := request
	staged.Roots = roots
	return staged, nil
}
