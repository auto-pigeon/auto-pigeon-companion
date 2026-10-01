package q3run

import (
	"errors"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Preflight says, before anything is started, why an engine could not run a
// package on this machine: it is not set up, its approval is stale, the action
// is not one it has, or it does not run on this platform.
//
// It is `internal/engine`'s own checker — the one `engine check` and Build &
// Run read — with the game folder's faults left out. A run here is given the
// INSTALLATION's base path, not the binding's game folder, and whether there is
// game data in it is the engine's to say when it starts: a folder check that
// knew only the name `baseq3` would refuse a free standalone game the engine
// itself runs (measured, `Q3_011`).
func Preflight(entry job.CatalogEntry, local binding.LocalBinding, actionID string, platform profile.Platform) error {
	problems := engine.Checker{Platform: platform}.Check(entry.Profile, entry.Trust, entry.Digest, local, actionID)
	var sentences []string
	class := failure.ToolUnavailable
	for _, problem := range problems {
		switch problem.Fault {
		case engine.FaultMissingGameData, engine.FaultUnboundRoot, engine.FaultMissingRoot:
			continue
		case engine.FaultUnsupportedPlatform:
			class = failure.PlatformUnsupported
		}
		sentences = append(sentences, problem.Summary+" "+problem.Fix)
	}
	if len(sentences) == 0 {
		return nil
	}
	return failure.As(class, errors.New(strings.Join(sentences, " ")))
}
