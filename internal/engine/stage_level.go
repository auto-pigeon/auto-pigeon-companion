package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// WADs are the map's texture sources, copied into `<mod>/wads/` beside the
	// level, preserving each file's own safe relative path.
	//
	// A Quake 1 BSP normally EMBEDS its compiled textures, so nothing here is a
	// claim that the engine needs these files to load the map. They are copied
	// because the operator asked for the relevant source bundle to be kept with
	// the demo mod, and because a staged tree that carries the WADs the compile
	// read is a tree somebody can inspect and rebuild from. `AUCOM/AUE/AUT
	// 246I1`, and the UI says the same thing in the same words.
	WADs []StagedSource

	// BuildManifest is the build's own `manifest.json`, copied to the root of
	// the mod under [BuildManifestName]. It is the record of what produced the
	// BSP sitting beside it.
	BuildManifest string
}

// StagedSource is one file staged beside the level.
type StagedSource struct {
	// Path is where the file goes, relative to the `wads` directory and
	// POSIX-spelled. It is the archive path the bundle declared, so a Quake II
	// namespace directory survives.
	Path string
	// Source is the file to copy.
	Source string
}

// BuildManifestName is the build record copied into a staged mod.
const BuildManifestName = "aucom-build-manifest.json"

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
	// The WADs, under `wads/`, each at the relative path the bundle declared.
	// Checked here rather than trusted: these names came out of an archive, and
	// [Staging] is about to copy whatever this directory holds into somebody's
	// game folder.
	for _, wad := range l.WADs {
		destination, err := safeRelative(filepath.Join(layout, WADDir), wad.Path)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return nil, fmt.Errorf("engine: preparing the level: %w", err)
		}
		if _, _, err = copyFile(wad.Source, destination); err != nil {
			return nil, err
		}
	}
	if l.BuildManifest != "" {
		if _, _, err := copyFile(l.BuildManifest, filepath.Join(layout, BuildManifestName)); err != nil {
			return nil, err
		}
	}

	return Staging{GameRoot: l.GameRoot, ModName: l.ModName, Source: layout, ProfileID: l.ProfileID, Now: l.Now}.Stage()
}

// WADDir is the subdirectory of a staged mod that holds the map's texture
// sources.
const WADDir = "wads"

// safeRelative resolves a staged file's relative path under a directory and
// proves the result is still inside it.
//
// The paths reach here from an AUB bundle, which internal/texturebundle has
// already checked in full. This is the second check, in the package that does
// the writing, because a rule enforced only in the package that happens to call
// this one today is a rule the next caller will not have.
func safeRelative(dir, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("engine: %q is not a usable relative path for a staged file", name)
	}
	target := filepath.Join(dir, filepath.FromSlash(name))
	relative, err := filepath.Rel(dir, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("engine: %q resolves outside the staged mod", name)
	}

	return target, nil
}
