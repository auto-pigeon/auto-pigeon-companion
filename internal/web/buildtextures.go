package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
)

// The textures a Build-page build of a saved account revision compiles with
// (issue #9, NEW_505).
//
// Found: "Use in a build" → Build sent the map as an `aub:` input and nothing
// for its textures, so a Quake 1 compile opened no WAD and its review said
// "Everything is in place" over a BSP with no textures — while Build & Run of
// the same revision fetched the revision's verified texture bundle. Now the
// Build page asks the same owner (verifiedTextureBundle: one cache, keyed by
// map AND revision, re-verified on reuse, so an exact cached revision builds
// with no connection and a bundle of another revision is never used).
//
//   - Quake 1: the bundle is the texture folder, copied for this build. WADs the
//     deployment did not send (`wad_bytes_not_carried`) are the person's own
//     files: taken, by exact name, from the Textures folder they chose, and
//     recorded beside the bundle. A bundle AUB could not complete for any other
//     reason refuses the build with Build & Run's own sentence.
//   - Quake II: the bundle's account textures are laid over the content folder
//     bound in Profiles, as Build & Run does; a binding that is unavailable, or
//     a source whose files did not arrive verified, refuses the build.
//   - A folder the person chose stays the fallback: when the bundle cannot be
//     had or cannot be used, that folder is used — as before — and the log says
//     why the account's textures were not.
//
// Quake III has no AUB texture source; its bound packages are resolved by
// assetref.BoundPackages, untouched here.

// buildTexturesPrefix names the job-owned copies in the builds directory.
const buildTexturesPrefix = "build-textures-"

// accountTextures is what a saved revision's textures make of one build.
type accountTextures struct {
	Roots       map[string]string
	RootSources map[string]build.RootSource
	Q2          *build.Q2Textures
	// Dir is a job-owned copy the caller removes when the build ends; "" when
	// nothing was copied (a preview, a folder fallback, a Quake II overlay
	// that the runner stages itself).
	Dir string
	// Notes are the lines the build log carries about the textures.
	Notes []string
}

// savedMapRevision is the one input of a build that is a saved account map
// revision, with the game its APMap declared — or, for a revision saved as a
// plain `.map`, which declares none, the engine family of the pipeline that
// builds it (pipelineGame). ok is false when there is none, or when it is not
// a Quake 1 or Quake II map.
func savedMapRevision(sources map[string]build.SourceRef, conversions map[string]build.Conversion,
	inputs map[string]string, pipelineGame string) (name string, source build.SourceRef, game string, ok bool) {
	names := make([]string, 0, len(sources))
	for key := range sources {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		candidate := sources[key]
		if candidate.AssetType != aub.AssetTypeMap || candidate.AssetID == "" || candidate.Revision < 1 {
			continue
		}
		game := pipelineGame
		if conversion, converted := conversions[key]; converted {
			game = conversion.Game
		}
		switch game {
		case leakadapter.ProfileQuake1, leakadapter.ProfileQuake2:
			return key, candidate, game, inputs[key] != ""
		}
	}

	return "", build.SourceRef{}, "", false
}

// buildAccountTextures resolves the textures of the saved revision a Build-page
// build reads, if it reads one. preview copies nothing: it names the verified
// cache entry, and a build gives the compiler its own copy of the same files.
func (s *Server) buildAccountTextures(ctx context.Context, request buildRequestBody,
	sources map[string]build.SourceRef, conversions map[string]build.Conversion, inputs map[string]string,
	roots map[string]string, rootSources map[string]build.RootSource, preview bool) (accountTextures, error) {
	out := accountTextures{Roots: roots, RootSources: rootSources}
	name, source, game, ok := savedMapRevision(sources, conversions, inputs, s.pipelineGame(request.Pipeline))
	if !ok {
		return out, nil
	}
	folder, typed := roots[profile.RootContent]
	if game == leakadapter.ProfileQuake1 {
		declares, err := s.pipelineDeclaresRoot(request.Pipeline, profile.RootContent)
		if err != nil {
			return out, err
		}
		if !declares {
			out.Notes = append(out.Notes, "Textures: no step of this pipeline reads a texture folder, so the textures of "+
				"saved revision "+fmt.Sprint(source.Revision)+" are not given to the compiler, and the BSP will carry none.")
			return out, nil
		}
	}

	entry, err := s.verifiedTextureBundle(ctx, source.AssetID, source.Revision)
	var refusal *aub.TextureExportRefusal
	switch {
	case errors.As(err, &refusal) && refusal.Reason == aub.ReasonNoTextures:
		out.Notes = append(out.Notes, fmt.Sprintf("Textures: saved revision %d uses no textures; nothing was fetched.", source.Revision))
		return out, nil
	case err != nil && typed:
		out.Notes = append(out.Notes, fmt.Sprintf("Textures: the textures of saved revision %d could not be had (%v); "+
			"the folder chosen on this page is used instead: %s", source.Revision, err, folder))
		return out, nil
	case err != nil:
		return out, savedTexturesError(source.Revision, err)
	}
	if entry.Receipt.MapID != source.AssetID || entry.Receipt.Revision != source.Revision {
		return out, fmt.Errorf("the texture bundle found is for map %s revision %d, not saved revision %d of %s",
			entry.Receipt.MapID, entry.Receipt.Revision, source.Revision, source.AssetID)
	}
	ref := bundleResult(entry, s.backendAddress()).Ref
	usedSources := wadSources(entry)
	if game == leakadapter.ProfileQuake2 {
		return s.quake2AccountTextures(out, entry, ref, usedSources, source, folder, typed)
	}

	// Quake 1.
	var own []playrun.OwnWAD
	if !ref.CompilerReady {
		names, onlyNotCarried := playrun.OwnWADsNeeded(ref.CompilerRefusals)
		switch {
		case onlyNotCarried && typed:
			found, err := playrun.FindOwnWADs(folder, names)
			if err != nil {
				return out, fmt.Errorf("the Textures folder: %w", err)
			}
			var missing []string
			for _, wad := range found {
				if !wad.Found {
					missing = append(missing, wad.Name)
				}
			}
			if len(missing) > 0 {
				return out, &buildTextureRefusal{text: fmt.Sprintf("Saved revision %d needs your own copy of %s: "+
					"the deployment did not send it, and the Textures folder you chose (%s) has no %s. Choose the folder "+
					"that holds it.", source.Revision, strings.Join(names, ", "), folder, strings.Join(missing, ", "))}
			}
			own = found
		case onlyNotCarried:
			return out, &buildTextureRefusal{text: playrun.CompilerRefusalRemedy(ref.CompilerRefusals, usedSources) +
				" On this page: choose the folder that holds your copy under Textures, and only " +
				strings.Join(names, ", ") + " is taken from it."}
		case typed:
			out.Notes = append(out.Notes, fmt.Sprintf("Textures: your account says the textures of saved revision %d are "+
				"not complete enough to compile (%s); the folder chosen on this page is used instead: %s",
				source.Revision, strings.Join(ref.CompilerRefusals, "; "), folder))
			return out, nil
		default:
			return out, &buildTextureRefusal{text: fmt.Sprintf("The textures of saved revision %d are incomplete, so it "+
				"was not built. %s", source.Revision, playrun.CompilerRefusalRemedy(ref.CompilerRefusals, usedSources)),
				codes: ref.CompilerRefusals}
		}
	}

	if out.Roots == nil {
		out.Roots = map[string]string{}
	}
	if out.RootSources == nil {
		out.RootSources = map[string]build.RootSource{}
	}
	rootSource := build.RootSource{Kind: build.RootFromTextureBundle, Bundle: ref}
	dir := entry.ContentRoot
	if !preview {
		copied, err := s.stageBundleCopy(ctx, entry, inputs[name], buildTexturesPrefix)
		if err != nil {
			return out, err
		}
		for _, wad := range own {
			digest, size, err := copyOwnWAD(wad.Path, filepath.Join(copied, wad.Name))
			if err != nil {
				_ = os.RemoveAll(copied)
				return out, fmt.Errorf("copying your own %s: %w", wad.Name, err)
			}
			rootSource.OwnFiles = append(rootSource.OwnFiles,
				build.BundleFile{Path: wad.Name, Source: "own_copy", SHA256: digest, Bytes: size})
		}
		dir, out.Dir = copied, copied
	}
	out.Roots[profile.RootContent], out.RootSources[profile.RootContent] = dir, rootSource

	files := make([]string, 0, len(ref.Files))
	for _, file := range ref.Files {
		files = append(files, file.Path)
	}
	out.Notes = append(out.Notes, fmt.Sprintf("Textures: the texture bundle of saved revision %d (%d file(s): %s; bundle %s), "+
		"verified, from this computer's cache or your account.", source.Revision, len(files), strings.Join(files, ", "), ref.Digest))
	if len(own) > 0 {
		names := make([]string, 0, len(own))
		for _, wad := range own {
			names = append(names, wad.Name)
		}
		out.Notes = append(out.Notes, "Textures: completed with your own copy of "+strings.Join(names, ", ")+
			", from the folder chosen on this page; nothing else was taken from it.")
	} else if typed {
		out.Notes = append(out.Notes, "Textures: the folder chosen on this page was not used: the revision's own "+
			"texture bundle is complete.")
	}

	return out, nil
}

// quake2AccountTextures is the Quake II half: the account textures the map binds,
// laid over the content folder bound in Profiles.
func (s *Server) quake2AccountTextures(out accountTextures, entry texturebundle.Entry, ref *build.BundleRef,
	sources []playrun.WADSource, source build.SourceRef, folder string, typed bool) (accountTextures, error) {
	if remedy, err := playrun.Q2SourcesProblem(sources); err != nil {
		if typed {
			out.Notes = append(out.Notes, fmt.Sprintf("Textures: %v; the folder chosen on this page is used instead: %s",
				err, folder))
			return out, nil
		}
		return out, &buildTextureRefusal{text: remedy, codes: []string{err.Error()}}
	}
	if len(ref.Files) == 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("Textures: saved revision %d binds no account texture; the compiler "+
			"reads its .wal textures from the game folders bound to it in Profiles.", source.Revision))
		return out, nil
	}
	if typed {
		out.Notes = append(out.Notes, "Textures: the account textures are laid over the folder chosen on this page.")
	}
	out.Q2 = &build.Q2Textures{Bundle: ref, Dir: entry.ContentRoot, Files: ref.Files}
	if entry.Receipt.Licenses {
		out.Q2.Licenses = filepath.Join(entry.Dir, texturebundle.LicensesName)
	}
	var bound []string
	for _, wad := range sources {
		if wad.Staged && wad.Binding != nil {
			bound = append(bound, fmt.Sprintf("%s revision %d", wad.Name, wad.Binding.Revision))
		} else if wad.Staged {
			bound = append(bound, wad.Name)
		}
	}
	out.Notes = append(out.Notes, fmt.Sprintf("Textures: %d verified account texture file(s) of %s (export %s), laid "+
		"over the content folder bound in Profiles.", len(ref.Files), strings.Join(bound, ", "), ref.Digest))

	return out, nil
}

// buildTextureRefusal is a build the textures of its saved revision stopped:
// the sentence the page shows, and AUB's codes for Technical details.
type buildTextureRefusal struct {
	text  string
	codes []string
}

func (r *buildTextureRefusal) Error() string {
	if len(r.codes) == 0 {
		return r.text
	}
	return r.text + "\n" + strings.Join(r.codes, "; ")
}

// savedTexturesError says why a saved revision's textures could not be had, in
// the Build page's terms; the cause is kept for the status it maps to.
func savedTexturesError(revision int, err error) error {
	text := fmt.Sprintf("Saved revision %d needs its textures, and they could not be had, so nothing was built: %v. "+
		"Download the revision again while connected, or choose a folder that holds the map's textures under Textures.",
		revision, err)
	return &leakTextureFailure{cause: err, text: text}
}

// copyOwnWAD copies one of the person's own WADs into the build's copy and
// says what was copied.
func copyOwnWAD(from, to string) (string, int64, error) {
	source, err := os.Open(from)
	if err != nil {
		return "", 0, err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(target, hash), source)
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

// buildTextureStatus is the answer to a build its saved revision's textures
// stopped: 409 for a revision whose textures cannot be completed as they are
// (nothing about the request was malformed, and retrying alone will not help),
// otherwise what the account or the cache answered.
func buildTextureStatus(err error) int {
	var refusal *buildTextureRefusal
	if errors.As(err, &refusal) {
		return http.StatusConflict
	}
	return leakTextureStatus(err)
}

// pipelineGame is the engine family a build profile declares, "" when it
// declares none or cannot be read.
func (s *Server) pipelineGame(pipelineID string) string {
	catalog, err := s.catalog()
	if err != nil {
		return ""
	}
	entry, err := catalog.Lookup(pipelineID)
	if err != nil {
		return ""
	}
	pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
	if !isPipeline || pipeline.GameProfile == nil {
		return ""
	}
	return pipeline.GameProfile.EngineFamily
}
