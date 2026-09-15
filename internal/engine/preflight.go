package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Why a launch will not work, said before anything is started.
//
// The executor already refuses everything here, correctly and for the right
// reasons. What it cannot do is say it *first*: by the time it refuses, a job
// exists, and the user is reading a failed record instead of a sentence about
// their machine. These checks run before submission so the answer to "why can I
// not press play" arrives before the button does anything.
//
// Every one of them is derived from the document. Nothing in this file knows
// that Quake's data lives in `id1` — the engine profile's content layouts say
// so, and a profile for something else says something else.

// Fault classifies a problem, so a caller can react to a kind rather than to a
// sentence.
type Fault string

const (
	// FaultStaleBinding: the document changed since the binding was written.
	FaultStaleBinding Fault = "stale_binding"
	// FaultNotAuthorized: nothing has been granted for this document.
	FaultNotAuthorized Fault = "not_authorized"
	// FaultUnknownAction: the profile has no such action.
	FaultUnknownAction Fault = "unknown_action"
	// FaultUnsupportedPlatform: it has the action, and not here.
	FaultUnsupportedPlatform Fault = "unsupported_platform"
	// FaultMissingEngine: the executable the binding names is not there.
	FaultMissingEngine Fault = "missing_engine"
	// FaultMissingGameData: the game root has no game in it.
	FaultMissingGameData Fault = "missing_game_data"
	// FaultUnboundRoot: a root the action needs has no path on this machine.
	FaultUnboundRoot Fault = "unbound_root"
	// FaultMissingRoot: it has a path, and the path is not there.
	FaultMissingRoot Fault = "missing_root"
)

// Problem is one reason a launch will not work.
type Problem struct {
	Fault Fault `json:"fault"`
	// Summary says what is wrong, in one sentence, about this machine.
	Summary string `json:"summary"`
	// Fix says what to do about it. Every problem has one; a problem a user
	// cannot act on is a problem that should not have been reported to them.
	Fix string `json:"fix"`
}

func (p Problem) Error() string { return p.Summary + " " + p.Fix }

// Problems is an ordered list of them.
type Problems []Problem

// Error renders every problem, one per line, because a user repairing a setup
// wants the list and not the first item on it.
func (p Problems) Error() string {
	lines := make([]string, 0, len(p))
	for _, problem := range p {
		lines = append(lines, problem.Summary+" "+problem.Fix)
	}
	return strings.Join(lines, "\n")
}

// ErrorOrNil is nil when there is nothing wrong.
func (p Problems) ErrorOrNil() error {
	if len(p) == 0 {
		return nil
	}
	return p
}

// Has reports whether a particular fault is among them.
func (p Problems) Has(fault Fault) bool {
	for _, problem := range p {
		if problem.Fault == fault {
			return true
		}
	}
	return false
}

// Checker answers "would this launch work" against one machine.
type Checker struct {
	// Platform is the machine being checked for.
	Platform profile.Platform
	// Stat and ReadDir reach the filesystem. Nil means the real one.
	Stat    func(string) (fs.FileInfo, error)
	ReadDir func(string) ([]os.DirEntry, error)
}

func (c Checker) stat(path string) (fs.FileInfo, error) {
	if c.Stat != nil {
		return c.Stat(path)
	}
	return os.Stat(path)
}

func (c Checker) readDir(path string) ([]os.DirEntry, error) {
	if c.ReadDir != nil {
		return c.ReadDir(path)
	}
	return os.ReadDir(path)
}

// Check reports everything that would stop one action of one profile from
// running on this machine, in the order a person would fix them.
//
// trust and digest come from the catalog entry, not from the binding, because
// that is what the executor authorizes against: a binding records what a user
// approved, and who vouches for the document is a property of where the
// document came from. Passing them in rather than recomputing them is what
// stops this answer and the executor's from differing.
func (c Checker) Check(document profile.Profile, trust profile.Trust, digest string, local binding.LocalBinding, actionID string) Problems {
	var problems Problems
	meta := document.Metadata()

	if local.ProfileDigest != "" && digest != "" && local.ProfileDigest != digest {
		problems = append(problems, Problem{
			Fault: FaultStaleBinding,
			Summary: fmt.Sprintf("The recorded setup for %s is for a different version of the document: it was written against %s and the document here is %s.",
				meta.Name, short(local.ProfileDigest), short(digest)),
			Fix: "Look at what changed with `companion toolchain diff`, then bind it again; a grant covers the exact document it was given.",
		})
	}
	if err := profile.Authorize(document, trust, digest, local.Grant); err != nil {
		problems = append(problems, Problem{
			Fault:   FaultNotAuthorized,
			Summary: fmt.Sprintf("Nothing has been approved for %s on this machine: %v.", meta.Name, err),
			Fix:     "Read what it asks for with `companion toolchain show`, then approve it.",
		})
	}

	action, found := document.ActionByID(actionID)
	if !found {
		offered := make([]string, 0, len(document.ActionList()))
		for _, candidate := range document.ActionList() {
			offered = append(offered, candidate.ID)
		}
		sort.Strings(offered)
		summary := fmt.Sprintf("%s has no %q action.", meta.Name, actionID)
		fix := "This engine offers: " + strings.Join(offered, ", ") + "."
		if len(offered) == 0 {
			fix = "This document declares no actions at all."
		} else if isEngineAction(actionID) {
			// The interesting case, and the one worth spelling out: the action
			// is a real engine action and this engine does not do it. A profile
			// says so by leaving it out, so "not declared" means "this engine
			// cannot", not "somebody forgot".
			fix = fmt.Sprintf("An engine profile leaves out what its engine does not do, so %s is not something %s does. It offers: %s.",
				actionID, meta.Name, strings.Join(offered, ", "))
		}
		problems = append(problems, Problem{Fault: FaultUnknownAction, Summary: summary, Fix: fix})
		return problems
	}

	problems = append(problems, c.platformProblems(document, action)...)
	problems = append(problems, c.engineProblems(document, action, local)...)
	problems = append(problems, c.rootProblems(document, action, local)...)
	return problems
}

func isEngineAction(id string) bool {
	for _, candidate := range profile.EngineActions {
		if candidate == id {
			return true
		}
	}
	return false
}

func (c Checker) platformProblems(document profile.Profile, action profile.Action) Problems {
	var problems Problems
	if supporter, ok := document.(interface {
		SupportFor(profile.Platform) (profile.Support, string)
	}); ok {
		if status, note := supporter.SupportFor(c.Platform); status == profile.Unsupported {
			problems = append(problems, Problem{
				Fault:   FaultUnsupportedPlatform,
				Summary: fmt.Sprintf("%s does not run on %s: %s.", document.Metadata().Name, c.Platform, strings.TrimSuffix(note, ".")),
				Fix:     "Use an engine whose profile declares this platform; `companion engine list` marks each one.",
			})
		}
	}
	if len(action.Platforms) == 0 {
		return problems
	}
	for _, candidate := range action.Platforms {
		if candidate == c.Platform {
			return problems
		}
	}
	offered := make([]string, 0, len(action.Platforms))
	for _, candidate := range action.Platforms {
		offered = append(offered, candidate.String())
	}
	sort.Strings(offered)
	return append(problems, Problem{
		Fault:   FaultUnsupportedPlatform,
		Summary: fmt.Sprintf("%s offers %q on %s, and not on %s.", document.Metadata().Name, action.ID, strings.Join(offered, ", "), c.Platform),
		Fix:     "Run it on one of those, or use an engine that declares this action here.",
	})
}

// engineProblems checks that the program the binding names is where it says.
func (c Checker) engineProblems(document profile.Profile, action profile.Action, local binding.LocalBinding) Problems {
	path, ok := local.Executables[action.Executable]
	if !ok || path == "" {
		root := local.Roots[profile.RootToolInstall]
		if root == "" {
			return Problems{{
				Fault:   FaultMissingEngine,
				Summary: fmt.Sprintf("Nothing on this machine says where %s's %q executable is.", document.Metadata().Name, action.Executable),
				Fix:     "Point the Companion at the engine you have: `companion engine bind " + document.Metadata().ID + " --engine <path to the executable>`.",
			}}
		}
		file := executableFile(document, action.Executable)
		if file == "" {
			return nil
		}
		rendered := strings.ReplaceAll(file, "{platform.exe_suffix}", c.Platform.ExeSuffix())
		path = filepath.Join(root, filepath.FromSlash(rendered))
	}
	info, err := c.stat(path)
	switch {
	case err != nil:
		return Problems{{
			Fault:   FaultMissingEngine,
			Summary: fmt.Sprintf("The engine recorded for %s is not there: %s.", document.Metadata().Name, path),
			Fix:     "It was moved, renamed or uninstalled. Bind it again with `companion engine bind " + document.Metadata().ID + " --engine <path>`.",
		}}
	case info.IsDir():
		return Problems{{
			Fault:   FaultMissingEngine,
			Summary: fmt.Sprintf("The engine recorded for %s is a directory, not a program: %s.", document.Metadata().Name, path),
			Fix:     "Name the executable inside it.",
		}}
	}
	return nil
}

func executableFile(document profile.Profile, name string) string {
	engine, ok := document.(*profile.EngineProfile)
	if !ok {
		return ""
	}
	for _, candidate := range engine.Executables {
		if candidate.Name == name {
			return candidate.File
		}
	}
	return ""
}

// rootProblems checks every root the action declares, and treats the game root
// specially: an empty directory is a configured root and still not a game.
func (c Checker) rootProblems(document profile.Profile, action profile.Action, local binding.LocalBinding) Problems {
	var problems Problems
	for _, ref := range action.Roots {
		if ref.Role == profile.RootWorkspace {
			continue // created per job; it never exists in advance.
		}
		path := local.Roots[ref.Role]
		if path == "" && ref.Optional {
			continue
		}
		if path == "" {
			problems = append(problems, Problem{
				Fault:   FaultUnboundRoot,
				Summary: fmt.Sprintf("%q needs the %s, and nothing on this machine says where that is.", action.Title, phrase(ref.Role)),
				Fix:     "Set it: `companion engine bind " + document.Metadata().ID + " " + flagFor(ref.Role) + "`.",
			})
			continue
		}
		if info, err := c.stat(path); err != nil || !info.IsDir() {
			problems = append(problems, Problem{
				Fault:   FaultMissingRoot,
				Summary: fmt.Sprintf("The %s recorded for %s is not a directory: %s.", phrase(ref.Role), document.Metadata().Name, path),
				Fix:     "It was moved or removed. Set it again with `companion engine bind`.",
			})
			continue
		}
		if ref.Role == profile.RootGame {
			problems = append(problems, c.gameDataProblems(document, path)...)
		}
	}
	return problems
}

// gameDataProblems checks that a game root has a game in it.
//
// What counts as "a game" comes from the document: an engine profile's content
// layouts name the directory the base game lives in, and this looks for that.
// The Companion never obtains that data — it is a commercial release, it is not
// redistributable, and the only honest thing a program can do about a missing
// copy is say which directory it looked in.
func (c Checker) gameDataProblems(document profile.Profile, root string) Problems {
	engine, ok := document.(*profile.EngineProfile)
	if !ok {
		return nil
	}
	// Deduplicated: a profile normally declares the same base directory twice —
	// once for loose files and once for the PAK in it — and "there is no id1 or
	// id1 directory" is not a sentence anybody should be shown.
	var wanted []string
	seen := map[string]bool{}
	for _, layout := range engine.ContentLayouts {
		if layout.Root == profile.RootGame && layout.Path != "" && !seen[layout.Path] {
			seen[layout.Path] = true
			wanted = append(wanted, layout.Path)
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	var misspelled []string
	for _, want := range wanted {
		exact := filepath.Join(root, filepath.FromSlash(want))
		if info, err := c.stat(exact); err == nil && info.IsDir() {
			if empty, err := c.isEmpty(exact); err == nil && empty {
				return Problems{{
					Fault:   FaultMissingGameData,
					Summary: fmt.Sprintf("%s is there and empty, so there is no game in %s.", want, root),
					Fix:     "Point the game root at your own installed copy of the game. The Companion never downloads or copies game data.",
				}}
			}
			return nil
		}
		// A case-only mismatch is the failure that looks like nothing at all:
		// the directory is visibly present, the engine finds no game, and no
		// message anybody has read mentions capital letters.
		for _, entry := range c.entriesLike(root, want) {
			misspelled = append(misspelled, entry)
		}
	}
	if len(misspelled) > 0 {
		return Problems{{
			Fault: FaultMissingGameData,
			Summary: fmt.Sprintf("%s has %s in it, and the engine looks for %s.",
				root, strings.Join(misspelled, ", "), strings.Join(wanted, " or ")),
			Fix: "On a case-sensitive filesystem those are different directories. Rename it, or point the game root somewhere the spelling matches.",
		}}
	}
	return Problems{{
		Fault:   FaultMissingGameData,
		Summary: fmt.Sprintf("There is no %s directory in %s, so it is not an installed game.", strings.Join(wanted, " or "), root),
		Fix:     "Point the game root at your own installed copy — `companion engine detect` lists the likely places. The Companion never downloads or copies game data.",
	}}
}

func (c Checker) isEmpty(dir string) (bool, error) {
	entries, err := c.readDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func (c Checker) entriesLike(dir, want string) []string {
	entries, err := c.readDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.Name() != want && strings.EqualFold(entry.Name(), want) {
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out
}

// phrase names a root the way a user would.
func phrase(role string) string {
	switch role {
	case profile.RootGame:
		return "installed game folder"
	case profile.RootContent:
		return "project folder"
	case profile.RootToolInstall:
		return "engine folder"
	case profile.RootProject:
		return "map project folder"
	}
	return role
}

// flagFor is how `companion engine bind` is told where a root is.
//
// The two roles an engine normally needs have a flag of their own; everything
// else goes through the general `--root role=path`, which exists so that a
// user-authored profile declaring a root this build's flags do not name is
// still bindable. A Fix line naming a flag that does not exist would be worse
// than no Fix line.
func flagFor(role string) string {
	switch role {
	case profile.RootGame:
		return "--game-root <path>"
	case profile.RootContent:
		return "--content-root <path>"
	}
	return "--root " + role + "=<path>"
}

func short(digest string) string {
	if len(digest) > 19 {
		return digest[:19] + "…"
	}
	return digest
}
