package launch

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Launching goes through the job executor, like everything else.
//
// # Why this file exists
//
// Before the executor, `launch.Run` started a game itself: its own os/exec, its
// own output handling, its own idea of when a process had finished. That was
// the second execution path in the program, and the whole point of the job
// runtime is that there is one. So a launch config becomes an *engine profile*,
// and starting a game becomes submitting a job against it — the same
// resolution, the same containment checks, the same supervision, the same
// record afterwards.
//
// # This is a bridge, not the model
//
// A real engine profile is a curated document: qualified against an upstream
// release, with content layouts, all five session actions, and a publisher who
// says so. What [EngineProfile] generates from a launch config is the small
// subset a stubbed provider can honestly describe — one `play_map` action, no
// layouts, no version probe — and it exists so the migration off the second
// execution path did not have to wait for the profiles.
//
// The generated document is trusted the way a built-in one is, for the same
// reason: it was produced by this build, from configuration the user already
// has, and nobody else's bytes went into it. See [profile.Authorize].

// GeneratedIDPrefix namespaces the profiles this file synthesises, so a
// generated document can never be mistaken for one somebody published.
const GeneratedIDPrefix = "auto-pigeon.launch."

// GeneratedFor is the profile id a launch config becomes.
func GeneratedFor(game string) string {
	return GeneratedIDPrefix + idSegment(game)
}

// idSegment reduces a game name to what a profile id permits: lower-case
// letters, digits and hyphens.
func idSegment(game string) string {
	var b strings.Builder
	previousHyphen := true // Leading hyphens are not permitted.
	for _, r := range strings.ToLower(strings.TrimSpace(game)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			previousHyphen = false
		case !previousHyphen:
			b.WriteByte('-')
			previousHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// engineFamilies maps a launch config's game name onto AUB's closed
// `engine_family` vocabulary.
//
// A map rather than a guess, because the vocabulary belongs to AUB and this
// side does not get to invent members of it. A game this cannot place is a game
// whose launch config cannot become a profile, and saying so is better than
// generating a document that fails validation somewhere less obvious.
var engineFamilies = map[string]string{
	"quake":  "quake1",
	"quake1": "quake1",
	"q1":     "quake1",
	"quake2": "quake2",
	"q2":     "quake2",
	"quake3": "quake3",
	"q3":     "quake3",
}

// engineFamilyFor places a game in AUB's vocabulary.
func engineFamilyFor(game string) (string, error) {
	if family, known := engineFamilies[idSegment(game)]; known {
		return family, nil
	}
	names := make([]string, 0, len(engineFamilies))
	for name := range engineFamilies {
		names = append(names, name)
	}
	sort.Strings(names)
	return "", fmt.Errorf(
		"launch: %q is not a game the Companion can place in one of AUB's engine families; it knows: %s",
		game, strings.Join(names, ", "))
}

// EngineProfile turns one launch config into an engine profile document.
//
// The executable's own path does not go in the document: a profile that named
// `/home/you/games/quake/quakespasm` would be carrying a machine path, which
// the portability rules refuse and which would be wrong on every other machine.
// It is supplied per job as a local binding value — see [Bind].
func EngineProfile(config Config) (*profile.EngineProfile, error) {
	id := GeneratedFor(config.Game)
	if strings.TrimSpace(config.Game) == "" || id == GeneratedIDPrefix {
		return nil, fmt.Errorf("launch: %q is not a game name a profile id can be made from", config.Game)
	}
	if strings.TrimSpace(config.ExecutablePattern) == "" {
		return nil, fmt.Errorf("launch: %s has no executable pattern", config.Game)
	}

	family, err := engineFamilyFor(config.Game)
	if err != nil {
		return nil, err
	}

	args := make([]any, 0, len(config.Args))
	for _, arg := range config.Args {
		args = append(args, translateArg(arg))
	}

	document := map[string]any{
		"schema_version": profile.SchemaVersion,
		"kind":           "engine",
		"id":             id,
		"version":        "0.0.0",
		"name":           config.Game,
		"summary":        "Generated from this machine's launch configuration for " + config.Game + ".",
		"description": "The Companion generated this document from a launch config so that starting a game " +
			"goes through the same executor as everything else: the same resolution, the same containment " +
			"checks, the same supervision and the same record afterwards.\n\n" +
			"It is not a qualified engine profile. It has one action, no content layouts and no version " +
			"probe, because a stubbed launch config does not know enough to claim more. A curated profile " +
			"for this engine replaces it; it does not extend it.",
		"publisher": map[string]any{"name": "Auto-Pigeon Companion"},
		"license": map[string]any{
			"spdx": "GPL-2.0-or-later",
			"name": "GNU General Public License v2.0 or later",
			"notice": "Quake engine source ports are separate programs under the GNU GPL. This generated " +
				"profile configures one; it does not contain, link or relicense it.",
		},
		"game_profile":   map[string]any{"slug": idSegment(config.Game), "engine_family": family},
		"runtime":        idSegment(config.Game),
		"engine_version": "unknown",
		"platforms": []map[string]any{
			{"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "status": "unverified",
				"note": "generated from a launch config; nobody has qualified this engine against this build"},
		},
		"acquisition": []map[string]any{
			{"mode": "user_path", "title": "Use the copy you already have", "hint": "choose the game's install directory"},
		},
		"executables": []map[string]any{
			{"name": "engine", "title": config.Game, "file": "engine{platform.exe_suffix}"},
		},
		"actions": []map[string]any{{
			"id":           profile.ActionPlayMap,
			"title":        "Play a map",
			"session_role": string(profile.SessionClient),
			"executable":   "engine",
			"working_dir":  map[string]any{"root": profile.RootGame},
			"args":         args,
			"roots": []map[string]any{
				{"role": profile.RootGame, "access": "read", "purpose": "load the game's own data"},
			},
		}},
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("launch: generating a profile for %s: %w", config.Game, err)
	}
	return profile.DecodeEngine(encoded)
}

// translateArg rewrites a launch config's placeholders into a profile's.
//
// Two vocabularies, deliberately not merged: a launch config is AUB's shape and
// a profile is this repository's, and a rewrite that happens in one visible
// function is better than a template language that quietly serves both.
// An unrecognised placeholder is left alone, which is what [expand] does too —
// it degrades to a visibly wrong argument rather than to a launch that refuses
// to start over a placeholder the user cannot remove.
func translateArg(arg string) string {
	return strings.NewReplacer(
		"{game_root}", "{root."+profile.RootGame+"}",
		"{map}", "{runtime."+profile.RuntimeMapName+"}",
		"{os}", "{platform.os}",
		"{arch}", "{platform.arch}",
		"{exe}", "{platform.exe_suffix}",
	).Replace(arg)
}
