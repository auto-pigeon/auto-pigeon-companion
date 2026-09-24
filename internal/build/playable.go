package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// A finished build as something an engine can play.
//
// A build publishes its level as `output/bsp/<file>.bsp` beside `output/lit/…`,
// which is a layout about the build's records. A Quake engine reads
// `<game directory>/maps/<name>.bsp`. Staging a build's output directory as it
// stood produced `<mod>/bsp/level.bsp`, which no engine loads (NEW_244D, with
// the operator's own vkQuake), so playing what you just built needed a
// hand-made `maps/` folder. PlayableLevel is the one place that says which
// files of a build are the level and what the engine should call it.

// Playable is a build's level: the BSP, its coloured lighting when the build
// made one, and the map name an engine is told to load.
type Playable struct {
	BSP     string
	Lit     string
	MapName string
}

// ErrNotPlayable reports a build with no level an engine could load.
var ErrNotPlayable = errors.New("this build has no finished level to play")

// PlayableLevel reads a manifest for the level it produced.
func PlayableLevel(m *Manifest) (Playable, error) {
	if m == nil {
		return Playable{}, ErrNotPlayable
	}
	if m.State != job.Succeeded {
		return Playable{}, fmt.Errorf("the build %s: %w", m.State, ErrNotPlayable)
	}
	var level Playable
	for _, output := range m.Outputs {
		if output.Path == "" || output.Missing {
			continue
		}
		switch {
		case output.Name == "bsp" || strings.EqualFold(filepath.Ext(output.Path), ".bsp") && level.BSP == "":
			level.BSP = output.Path
		case output.Name == "lit":
			level.Lit = output.Path
		}
	}
	if level.BSP == "" {
		return Playable{}, fmt.Errorf("no .bsp among its outputs: %w", ErrNotPlayable)
	}
	if _, err := os.Stat(level.BSP); err != nil {
		return Playable{}, fmt.Errorf("its level is no longer on this machine (%s): %w", level.BSP, ErrNotPlayable)
	}
	if level.Lit != "" {
		if _, err := os.Stat(level.Lit); err != nil {
			level.Lit = ""
		}
	}
	source := ""
	for _, input := range m.Inputs {
		if input.Name == "source_map" || source == "" {
			source = input.Path
		}
	}
	level.MapName = MapName(source)
	return level, nil
}

// MapName is what an engine is told to load for a source file: its name
// without the extension, in the letters, digits, `_` and `-` every Quake engine
// accepts after `+map`. `Friday DM.map` is `friday_dm`; nothing usable is
// `level`.
func MapName(source string) string {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" || source == "" {
		return "level"
	}
	if len(name) > 56 {
		name = name[:56]
	}
	return name
}
