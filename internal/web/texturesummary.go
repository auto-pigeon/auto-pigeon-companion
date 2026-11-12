package web

import (
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3packages"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
)

// What a map card says about its textures (issue #6, NEW_505).
//
// The card read `wads_declared` and nothing else, so every revision whose
// textures are not a Quake 1 WAD list — a Quake II map bound to an account
// texture package, a Quake III map bound to packages — read "none declared",
// which says the map uses nothing. It now says what the map is bound to, from
// the most authoritative document this machine has for it:
//
//   - the verified texture manifest of that exact revision, when it is in the
//     cache — what a build of it would read, checked file by file, offline;
//   - otherwise the account's own answer (`texture-requirements`, the latest
//     revision), which names every source and whether it resolves;
//   - for a cached Quake III revision, the package ledger in its APMap.
//
// "None declared" is said only when the document declares nothing at all, and
// a source that cannot be had is "unavailable", never folded into "none".

// The summary states a card distinguishes.
const (
	texturesVerified     = "verified"        // every source is on this computer, verified for this revision
	texturesAvailable    = "available"       // the account resolves every source; a build fetches them
	texturesNeedsOwn     = "needs_own_files" // complete except files the deployment does not send
	texturesUnavailable  = "unavailable"     // a source is missing, private, or its bound revision is gone
	texturesNoneDeclared = "none_declared"   // the map declares no texture source at all
	texturesGameFolders  = "game_folders"    // Quake III: read from the game folders and its bound packages
)

// The per-source states.
const (
	packageVerified    = "verified"
	packageAvailable   = "available"
	packageOwnCopy     = "own_copy_needed"
	packageFromGame    = "from_game"
	packageUnavailable = "unavailable"
	packageMissing     = "missing"
	packageBound       = "bound"
)

// texturePackage is one source a revision declares or is bound to.
type texturePackage struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	Origin   string `json:"origin,omitempty"`
	State    string `json:"state"`
	Revision int    `json:"revision,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// textureSummary is what a card shows.
type textureSummary struct {
	// From is `verified_cache`, `account` or `package_ledger`.
	From     string           `json:"from"`
	Revision int              `json:"revision,omitempty"`
	Game     string           `json:"game,omitempty"`
	State    string           `json:"state"`
	Packages []texturePackage `json:"packages"`
	// Refusals are AUB's codes for an incomplete manifest, for Details.
	Refusals []string `json:"refusals,omitempty"`
}

// summaryFromManifest summarizes a verified cache entry: what a build of that
// exact revision reads, with no network.
func summaryFromManifest(entry texturebundle.Entry) textureSummary {
	game := entry.Manifest.Game
	out := textureSummary{From: "verified_cache", Revision: entry.Receipt.Revision, Game: game,
		Packages: []texturePackage{}, Refusals: entry.Receipt.CompilerRefusals}
	notCarried := map[string]bool{}
	if names, only := playrun.OwnWADsNeeded(entry.Receipt.CompilerRefusals); only {
		for _, name := range names {
			notCarried[name] = true
		}
	}
	for _, source := range wadSources(entry) {
		item := texturePackage{Name: source.Name, Origin: source.Origin, State: packageVerified}
		if source.Binding != nil {
			item.Revision = source.Binding.Revision
		}
		switch {
		case source.Binding != nil && source.Binding.Status != texturebundle.BindingBound:
			item.State, item.Detail = packageUnavailable, source.Binding.Status
		case source.Staged:
		case source.Origin == playrun.OriginInstalled && game == leakadapter.ProfileQuake2:
			item.State = packageFromGame
		case notCarried[baseName(source.Name)]:
			// AUB names a declared WAD it does not send — withheld, or held by
			// nobody — `wad_bytes_not_carried`, and a build takes the person's
			// own copy for exactly those (#9), so the card says the same.
			item.State, item.Detail = packageOwnCopy, source.NotSentReason
			if item.Detail == "" && source.Origin == "" {
				item.Detail = "nothing in your account supplies it"
			}
		case source.Origin == playrun.OriginInstalled:
			item.State, item.Detail = packageUnavailable, source.NotSentReason
		default:
			item.State, item.Detail = packageMissing, source.NotSentReason
		}
		out.Packages = append(out.Packages, item)
	}
	out.State = overallState(out.Packages, len(entry.Receipt.WADsDeclared), texturesVerified)
	// The account's own word on a Quake 1 bundle is the gate a build applies:
	// a manifest it calls incomplete is not "verified" whatever its files say.
	if game != leakadapter.ProfileQuake2 && !entry.Receipt.CompilerReady && out.State == texturesVerified {
		out.State = texturesUnavailable
		if len(notCarried) > 0 {
			out.State = texturesNeedsOwn
		}
	}
	if out.State == texturesVerified || out.State == texturesNoneDeclared {
		out.Refusals = nil
	}
	return out
}

// summaryFromRequirements summarizes the account's answer for the latest
// revision; verified marks the sources this machine already holds verified.
func summaryFromRequirements(view aub.MapTextures, verified map[string]bool) textureSummary {
	out := textureSummary{From: "account", Revision: view.Revision, Game: view.Game, Packages: []texturePackage{}}
	if len(view.Requirements) == 0 {
		// An account that answers with the declaration alone (before the
		// requirements list): the names it declares, unresolved here, and a
		// build fetches them.
		for _, name := range view.WADsDeclared {
			item := texturePackage{Name: name, Kind: "wad", State: packageAvailable}
			if verified[name] {
				item.State = packageVerified
			}
			out.Packages = append(out.Packages, item)
		}
	}
	for _, requirement := range view.Requirements {
		item := texturePackage{Name: requirement.Name, Kind: requirement.Kind, Origin: requirement.Origin,
			Revision: requirement.Revision, State: packageAvailable}
		switch {
		case requirement.Status == "unsupported_game":
			continue
		case requirement.Binding != nil && requirement.Binding.Status != texturebundle.BindingBound:
			item.State, item.Detail, item.Revision = packageUnavailable, requirement.Binding.Status, requirement.Binding.Revision
		case requirement.Binding != nil:
			item.State, item.Revision = packageBound, requirement.Binding.Revision
		case requirement.Status == "missing" && view.Game == leakadapter.ProfileQuake1:
			// A declared WAD nothing in the account supplies: its export says
			// `wad_bytes_not_carried`, and a build takes the person's own copy.
			item.State, item.Detail = packageOwnCopy, "nothing in your account supplies it"
		case requirement.Status != "resolved" && requirement.Status != "":
			item.State, item.Detail = packageMissing, requirement.Status
		case requirement.Origin == playrun.OriginInstalled && !requirement.Carried():
			if view.Game == leakadapter.ProfileQuake2 {
				item.State = packageFromGame
			} else {
				item.State, item.Detail = packageOwnCopy, requirement.NotCarriedReason()
			}
		case requirement.Origin == playrun.OriginUser && requirement.Visibility == "private" && !requirement.Mine &&
			requirement.BoundMapID == "":
			item.State, item.Detail = packageUnavailable, "private to its owner"
		}
		if verified[requirement.Name] && (item.State == packageAvailable || item.State == packageBound) {
			item.State = packageVerified
		}
		out.Packages = append(out.Packages, item)
	}
	if len(out.Packages) == 0 && view.Game == "quake3" {
		out.State = texturesGameFolders
		return out
	}
	out.State = overallState(out.Packages, len(view.WADsDeclared), texturesAvailable)
	return out
}

// summaryFromLedger is a cached Quake III revision: the packages its APMap is
// bound to. Whether each archive is in the package cache is a build's to check.
func summaryFromLedger(revision int, ledger *q3packages.Ledger) textureSummary {
	out := textureSummary{From: "package_ledger", Revision: revision, Game: "quake3", State: texturesGameFolders,
		Packages: []texturePackage{}}
	for _, binding := range ledger.Packages {
		out.Packages = append(out.Packages, texturePackage{Name: binding.ArchiveName, Kind: "package",
			Origin: binding.Root, State: packageBound})
	}
	return out
}

// overallState is the card's one word over every source. ok is what "all
// fine" is called for this document: verified for a cache entry, available
// for the account's answer.
func overallState(packages []texturePackage, declared int, ok string) string {
	if len(packages) == 0 {
		if declared > 0 {
			// Declared and nothing resolved: never "none declared".
			return texturesUnavailable
		}
		return texturesNoneDeclared
	}
	state := ok
	for _, item := range packages {
		switch item.State {
		case packageUnavailable, packageMissing:
			return texturesUnavailable
		case packageOwnCopy:
			state = texturesNeedsOwn
		case packageAvailable, packageBound:
			if ok == texturesVerified {
				state = texturesAvailable
			}
		}
	}
	return state
}

// verifiedSources are the source names a verified cache entry holds staged.
func verifiedSources(entry texturebundle.Entry) map[string]bool {
	out := map[string]bool{}
	for _, source := range wadSources(entry) {
		if source.Staged {
			out[source.Name] = true
		}
	}
	return out
}

// cachedLedger reads the package ledger of a cached revision's APMap, nil when
// it has none or the file cannot be read.
func cachedLedger(store *assetsync.Store, record assetsync.RevisionRecord) *q3packages.Ledger {
	for _, file := range record.Files {
		if !strings.EqualFold(filepath.Ext(file.Path), ".apmap") {
			continue
		}
		path, err := store.Object(file.SHA256)
		if err != nil {
			return nil
		}
		ledger, err := q3packages.ReadLedger(path)
		if err != nil {
			return nil
		}
		return ledger
	}
	return nil
}

func baseName(declared string) string {
	if i := strings.LastIndexAny(declared, `/\`); i >= 0 {
		return declared[i+1:]
	}
	return declared
}
