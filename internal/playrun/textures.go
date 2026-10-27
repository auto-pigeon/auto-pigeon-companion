package playrun

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
)

// A build whose compiler found none of the map's textures used to succeed.
//
// Measured (compile job 20260923T112326Z-7d5551dde659): ericw-tools 2.0 printed
// `addArchive: WARNING: archive '…/gfx/metal.wad' not found`, then
// `WARNING: No valid WAD filenames in worldmodel`, then `unable to find texture`
// for all 38 of dm2's textures — and exited 0. The BSP was written, installed
// and launched, and every surface was a placeholder. The Q1 tool profile's
// rules were written for the older `WARNING 01` / `WARNING 16` codes and matched
// none of it, so nothing on the page said a word.
//
// Now the compile stage reads what the diagnostic rules classified. No WAD
// opened and textures missing is a failed build; some textures missing is a
// build that goes ahead with a warning naming them. Tool textures are never
// counted: `skip` has no image in any WAD and every compiler reports it.

// ErrNoTextures is a compile whose compiler opened no WAD, so every face of the
// map would be a placeholder.
var ErrNoTextures = errors.New("playrun: the compiler found none of the map's textures")

// noWADRules are the diagnostic rule ids, across the built-in tool profiles,
// that mean "no WAD could be opened". "A texture was not found", and which
// textures are tool textures that never count, are internal/build's
// (tooltextures.go): a build and a Build & Run must agree about them.
var noWADRules = map[string]bool{"no_wad": true, "no_valid_wad": true}

// textureCheck is what one build's diagnostics say about its textures.
type textureCheck struct {
	Missing []string
	NoWAD   bool
}

func checkTextures(manifest *build.Manifest) textureCheck {
	var check textureCheck
	if manifest == nil {
		return check
	}
	seen := map[string]bool{}
	for _, step := range manifest.Steps {
		for _, d := range step.Diagnostics {
			switch {
			case noWADRules[d.RuleID]:
				check.NoWAD = true
			case build.MissingTextureRules[d.RuleID]:
				name := build.TextureNamed(d.Raw)
				if name == "" || build.IsToolTexture(name) || seen[name] {
					continue
				}
				seen[name] = true
				check.Missing = append(check.Missing, name)
			}
		}
	}
	sort.Strings(check.Missing)

	return check
}

// verdict turns the check into a failure or a warning. A failure is returned as
// an error; a warning is the sentence the run carries. A packaged (Quake II)
// run's compiler reads `.wal` files from its bound game folders, not WADs, and
// the sentence names those (Q2_020, seen live).
func (c textureCheck) verdict(packaged bool) (warning string, err error) {
	if len(c.Missing) == 0 {
		return "", nil
	}
	if packaged {
		return fmt.Sprintf("%d texture(s) were not found as a .wal in the game folders bound to the compiler, so their "+
			"surfaces took default contents and flags and will show as a placeholder: %s",
			len(c.Missing), listSome(c.Missing)), nil
	}
	if c.NoWAD {
		return "", fmt.Errorf("%w: no WAD the map names could be opened, so %d texture(s) would be placeholders (%s)",
			ErrNoTextures, len(c.Missing), listSome(c.Missing))
	}

	return fmt.Sprintf("%d texture(s) were not in any WAD the compiler opened and will show as a placeholder: %s",
		len(c.Missing), listSome(c.Missing)), nil
}

func listSome(names []string) string {
	const shown = 8
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}

	return strings.Join(names[:shown], ", ") + fmt.Sprintf(" and %d more", len(names)-shown)
}
