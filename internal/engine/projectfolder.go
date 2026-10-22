package engine

import (
	"fmt"
	"os"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// FillProjectFolder gives a Quake 1 or Quake II engine its project folder
// (`content_root`) when nobody named one: the Companion's own folder of the maps
// and textures it downloaded, created when it is missing. It reports whether it
// filled anything. A folder already recorded is never replaced, and a person
// changes this one in Profiles or with `engine bind --content-root` like any
// other.
//
// `NEW_330`, HITL 2026-10-22. A fresh setup recorded vkQuake's program and game
// folder, and Profiles said "Ready" while Build & Run said vkQuake needed
// setup: every built-in Quake 1 and Quake II engine's "Play a map" declares a
// project folder it reads and no argument of it uses, so a newcomer was asked
// for a folder that meant nothing to them. HITL: the project folder is where
// the Companion keeps what it downloads, it is created by the Companion, and
// the person may change it.
//
// Quake III is left alone: its project folder is where its packages are read
// from (`q3vfs.PackagesRole`), and pointing that at the download cache would
// load whatever is in it.
func FillProjectFolder(document *profile.EngineProfile, local *binding.LocalBinding, folder string) (bool, error) {
	switch document.GameProfile.EngineFamily {
	case "quake1", "quake2":
	default:
		return false, nil
	}
	if folder == "" || local.Roots[profile.RootContent] != "" || !declaresRoot(document, profile.RootContent) {
		return false, nil
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return false, fmt.Errorf("creating the project folder %s: %w", folder, err)
	}
	if local.Roots == nil {
		local.Roots = map[string]string{}
	}
	local.Roots[profile.RootContent] = folder
	return true, nil
}

func declaresRoot(document *profile.EngineProfile, role string) bool {
	for _, action := range document.Actions {
		for _, root := range action.Roots {
			if root.Role == role {
				return true
			}
		}
	}
	return false
}
