package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LevelStaging puts one level where a Quake engine looks for it:
// `<game root>/<mod>/maps/<map>.bsp`, with its `.lit` beside it when there is
// one. It is [Staging] over a directory laid out that way, so the record, the
// refusal to write into somebody else's directory, the rollback and
// `engine unstage` are the ones every other staging has.
type LevelStaging struct {
	GameRoot  string
	ModName   string
	MapName   string
	BSP       string
	Lit       string
	ProfileID string
	Now       func() time.Time
}

// CheckMapName refuses a map name an engine could not be told on `+map`.
func CheckMapName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("engine: a map name is 1 to 64 characters, not %q", name)
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return fmt.Errorf("engine: %q contains %q; a map name may contain lower-case letters, digits, `_` and `-`", name, string(ch))
		}
	}
	return nil
}

// Stage copies the level into place and returns the staging record.
func (l LevelStaging) Stage() (*StageResult, error) {
	if err := CheckMapName(l.MapName); err != nil {
		return nil, err
	}
	layout, err := os.MkdirTemp("", "aucom-level-")
	if err != nil {
		return nil, fmt.Errorf("engine: preparing the level: %w", err)
	}
	defer os.RemoveAll(layout)
	maps := filepath.Join(layout, "maps")
	if err := os.MkdirAll(maps, 0o700); err != nil {
		return nil, fmt.Errorf("engine: preparing the level: %w", err)
	}
	if _, _, err := copyFile(l.BSP, filepath.Join(maps, l.MapName+".bsp")); err != nil {
		return nil, err
	}
	if l.Lit != "" {
		if _, _, err := copyFile(l.Lit, filepath.Join(maps, l.MapName+".lit")); err != nil {
			return nil, err
		}
	}
	return Staging{GameRoot: l.GameRoot, ModName: l.ModName, Source: layout, ProfileID: l.ProfileID, Now: l.Now}.Stage()
}
