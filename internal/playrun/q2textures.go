package playrun

import (
	"fmt"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
)

// The textures of a Quake II Build & Run (Q2_015A).
//
// A Quake II map names each texture as `<namespace>/<name>` and a compiler
// reads it as `textures/<namespace>/<name>.wal`. Two kinds of namespace reach
// a map saved in Auto-Pigeon:
//
//   - an ACCOUNT texture source the map binds to one exact revision
//     (`aup687697486`). AUB's map texture export carries its files with their
//     digests, and they are the only copy that is the map's: a file of the same
//     name in the base game or the user's content folder is not the revision
//     the map was saved against. The export is downloaded, verified and laid
//     over the content folder for the compiler (internal/build), and the
//     package carries the files (internal/q2pack).
//   - a namespace INSTALLED with the game (`e1u1`, in the retail pak0.pak).
//     AUB never sends those bytes, and says so: `wad_bytes_not_carried: e1u1`,
//     and `compiler_ready: false`. The compiler reads them from the base game
//     folder bound to it, and the package plan names any the base game does
//     not have.
//
// So AUB's `compiler_ready` is not this run's gate. The gate is that every
// source the map BINDS, or that is the user's own upload, arrived as verified
// files; one that did not stops the run before anything compiles, with what to
// do about it. An installed namespace is left to the base game.

// checkQ2Textures is the Quake II gate on a verified export.
func checkQ2Textures(record *Record) error {
	for _, source := range record.WADSources {
		binding := source.Binding
		switch {
		case binding != nil && binding.Status != bindingBound:
			record.Remedy = fmt.Sprintf("The map is saved against revision %d of the texture source %s, and that revision "+
				"is not available (%s). Open the map in Auto-Pigeon, attach an available revision of %s in LOAD › Textures, "+
				"save the map, and run the new revision.", binding.Revision, source.Name, binding.Status, source.Name)

			return fmt.Errorf("%w: the map binds the texture source %s to revision %d, which is not available (%s)",
				ErrNotCompilerReady, source.Name, binding.Revision, binding.Status)
		case (binding != nil || source.Origin == OriginUser) && !source.Staged:
			record.Remedy = fmt.Sprintf("The texture export did not deliver verified files for %s. Check you are still "+
				"signed in and can read that texture source, then try again.", source.Name)
			reason := "the export did not carry them"
			if source.NotSentReason != "" {
				reason = "the export did not carry them (" + source.NotSentReason + ")"
			}

			return fmt.Errorf("%w: the files of the texture source %s are not verified: %s",
				ErrNotCompilerReady, source.Name, reason)
		}
	}

	return nil
}

// bindingBound is AUB's status for a binding whose revision is available
// (internal/texturebundle.BindingBound, not imported here: playrun knows
// texture sources only as the run records them).
const bindingBound = "bound"

// describeQ2Textures is the technical sentence of a Quake II run's texture stage.
func describeQ2Textures(record *Record) string {
	var account, installed []string
	for _, source := range record.WADSources {
		switch {
		case source.Staged && source.Binding != nil:
			account = append(account, fmt.Sprintf("%s revision %d", source.Name, source.Binding.Revision))
		case source.Staged:
			account = append(account, source.Name)
		default:
			installed = append(installed, source.Name)
		}
	}
	files := 0
	if record.Bundle != nil {
		files = len(record.Bundle.Files)
	}
	sentence := fmt.Sprintf("%d verified account texture file(s)", files)
	if len(account) > 0 {
		sentence += " of " + strings.Join(account, ", ")
	}
	if record.Bundle != nil {
		sentence += ", export " + record.Bundle.Digest
	}
	if len(installed) > 0 {
		sentence += "; not sent, read from the base game folder bound to the compiler: " + strings.Join(installed, ", ")
	}

	return sentence
}

// q2TexturesFor is what the build lays over the content folder, or nil when the
// export carried nothing.
func q2TexturesFor(record *Record) *build.Q2Textures {
	if record.Bundle == nil || len(record.Bundle.Files) == 0 || record.BundleRoot == "" {
		return nil
	}

	return &build.Q2Textures{
		Bundle:   record.Bundle,
		Dir:      record.BundleRoot,
		Files:    record.Bundle.Files,
		Licenses: record.BundleLicenses,
	}
}

// accountTexturesFound fails a compile whose compiler did not find a texture of
// a source the export carried: the files were laid where it reads them, so a
// miss is a fault of this program's, never a placeholder to ship.
func (c textureCheck) accountTexturesFound(sources []WADSource) error {
	var lost []string
	for _, name := range c.Missing {
		namespace, _, nested := strings.Cut(name, "/")
		if !nested {
			continue
		}
		for _, source := range sources {
			if source.Staged && strings.EqualFold(source.Name, namespace) {
				lost = append(lost, name)

				break
			}
		}
	}
	if len(lost) == 0 {
		return nil
	}

	return fmt.Errorf("%w: the compiler did not find %d account texture(s) the export carried: %s",
		ErrNoTextures, len(lost), listSome(lost))
}
