package build

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Account textures for a Quake II build (Q2_015A).
//
// A Quake II map saved in Auto-Pigeon can name textures of an account texture
// source — `aup687697486/red_7a099681` — bound to one exact revision of that
// source. AUB's map texture export carries those `.wal` files, each with its
// digest, as `<namespace>/<file>.wal`. The compiler looks a texture up as
// `textures/<namespace>/<file>.wal` under its `-gamedir` and then its
// `-basedir`, and it takes ONE `-gamedir`: the content folder bound to it in
// Profiles. So the account's files have to be in that one directory without
// anything being written into the user's own folder.
//
// The runner makes the directory, in the build's own folder:
//
//	q2textures/overlay/textures/<namespace>/<file>.wal   the export's files, re-verified
//	q2textures/gamedir/                                    overlay laid over the bound content folder
//
// `gamedir` is links, not copies: a symbolic link to each entry of the content
// folder (and of the overlay), with a real directory only where both have one
// of the same name — `textures/`, and a namespace both hold — and the overlay's
// file winning where both have the same path. It replaces `content_root` for
// every step, so qbsp, vis and light read the same files. Measured with the
// qbsp of ericw-tools 2.0.0-alpha7 (the built-in Quake II tool) and of the
// Auto-Pigeon build 1.4518, in Quake II mode: a `-gamedir` made this way is
// read through its links, and its loose file wins over a `-basedir` archive's
// copy of the same name, which is read only when the loose one is absent. Where
// links cannot be made (Windows without the privilege), a directory is made
// and its entries linked one level down, and a file is hard-linked or copied.
//
// Why not another search path: qbsp has `-path`, but adding it is a change to
// the built-in tool documents, whose digests every binding and grant on every
// machine is written against.
//
// The manifest records the root as [RootFromQ2TextureOverlay] with the bundle's
// identity and both halves (RootRecord.Overlay, RootRecord.Underlay), which is
// how internal/q2pack tells the account's files from the user's.

// familyQuake2 is AUB's engine family for Quake II.
const familyQuake2 = "quake2"

// q2TexturesDirName is the build subdirectory the account textures live in.
const q2TexturesDirName = "q2textures"

// maxMergedEntries bounds how many links one merged game directory may hold.
const maxMergedEntries = 250000

// Q2Textures is a verified AUB texture export a Quake II build reads its
// account textures from.
type Q2Textures struct {
	// Bundle is the export's identity, recorded on the manifest.
	Bundle *BundleRef
	// Dir is the verified export's content directory on this machine: each
	// file at its manifest path, `<namespace>/<file>.wal`.
	Dir string
	// Files are the members to stage. Each is hashed again once staged.
	Files []BundleFile
	// Licenses is the export's LICENSES.md, kept beside the overlay. Optional.
	Licenses string
}

// Q2LicensesName is where a build keeps the export's licence summary, relative
// to its q2textures directory.
const Q2LicensesName = "LICENSES.md"

// stageQ2Textures lays the account textures over the content folder and
// returns the request every step runs with.
func (r *Runner) stageQ2Textures(request Request, steps []profile.ResolvedStep, layout layout, manifest *Manifest) (Request, error) {
	textures := request.Q2Textures
	if manifest.EngineFamily != familyQuake2 {
		return request, failure.As(failure.ContentRefused, fmt.Errorf(
			"account textures for a Quake II map were given to a %q build", manifest.EngineFamily))
	}
	approved, _, access := r.approvedRoots(request, steps)
	if _, declared := access[profile.RootContent]; !declared {
		return request, failure.As(failure.ContentRefused, errors.New(
			"no step of this build profile reads a content folder, so the map's account textures cannot reach its compiler"))
	}
	dir := filepath.Join(layout.Dir, q2TexturesDirName)
	overlay := filepath.Join(dir, "overlay")
	if err := os.MkdirAll(filepath.Join(overlay, "textures"), 0o700); err != nil {
		return request, fmt.Errorf("build: creating %s: %w", overlay, err)
	}
	for _, file := range textures.Files {
		local := filepath.FromSlash(file.Path)
		if file.Path == "" || strings.Contains(file.Path, `\`) || !filepath.IsLocal(local) {
			return request, failure.As(failure.ContentRefused, fmt.Errorf(
				"the texture export names %q, which is not a plain path inside it", file.Path))
		}
		destination := filepath.Join(overlay, "textures", local)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return request, fmt.Errorf("build: %w", err)
		}
		if err := linkOrCopyFile(filepath.Join(textures.Dir, local), destination); err != nil {
			return request, fmt.Errorf("build: staging the account texture %s: %w", file.Path, err)
		}
		digest, size, err := digestFile(destination)
		if err != nil {
			return request, err
		}
		digest = strings.TrimPrefix(digest, "sha256:")
		if !strings.EqualFold(digest, strings.TrimPrefix(file.SHA256, "sha256:")) || size != file.Bytes {
			return request, failure.As(failure.ContentRefused, fmt.Errorf(
				"the account texture %s is not the file the export verified: %d bytes, sha256 %s; want %d bytes, sha256 %s",
				file.Path, size, digest, file.Bytes, file.SHA256))
		}
	}
	if textures.Licenses != "" {
		if err := linkOrCopyFile(textures.Licenses, filepath.Join(dir, Q2LicensesName)); err != nil {
			return request, fmt.Errorf("build: keeping the export's %s: %w", Q2LicensesName, err)
		}
	}

	underlay := strings.TrimSpace(approved[profile.RootContent])
	gamedir := overlay
	if underlay != "" {
		absolute, err := filepath.Abs(underlay)
		if err == nil {
			underlay = absolute
		}
		gamedir = filepath.Join(dir, "gamedir")
		budget := maxMergedEntries
		if err := mergeTree(gamedir, []string{overlay, underlay}, &budget); err != nil {
			return request, failure.As(failure.InputInvalid, fmt.Errorf(
				"build: laying the account textures over the content folder: %w", err))
		}
	}

	source := RootSource{Kind: RootFromQ2TextureOverlay, Bundle: textures.Bundle}
	record := RootRecord{Role: profile.RootContent, Path: gamedir, Access: access[profile.RootContent],
		Source: &source, Overlay: overlay, Underlay: underlay}
	replaced := false
	for i := range manifest.Roots {
		if manifest.Roots[i].Role == profile.RootContent {
			manifest.Roots[i], replaced = record, true
		}
	}
	if !replaced {
		manifest.Roots = append(manifest.Roots, record)
	}

	staged := request
	staged.Roots = make(map[string]string, len(request.Roots)+1)
	for role, path := range request.Roots {
		staged.Roots[role] = path
	}
	staged.Roots[profile.RootContent] = gamedir
	staged.RootSources = make(map[string]RootSource, len(request.RootSources)+1)
	for role, named := range request.RootSources {
		staged.RootSources[role] = named
	}
	staged.RootSources[profile.RootContent] = source
	return staged, nil
}

// mergeTree makes destination the union of layers, the first layer winning a
// name two hold, by linking rather than copying (see the package comment
// above). A name that is a directory in more than one layer becomes a real
// directory merged the same way.
func mergeTree(destination string, layers []string, budget *int) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	type found struct {
		path string
		dir  bool
	}
	byName := map[string][]found{}
	var order []string
	for _, layer := range layers {
		entries, err := os.ReadDir(layer)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Join(layer, entry.Name())
			info, err := os.Stat(path) // through a link, as the compiler reads it
			if err != nil {
				continue // a dangling link is no file
			}
			if _, seen := byName[entry.Name()]; !seen {
				order = append(order, entry.Name())
			}
			byName[entry.Name()] = append(byName[entry.Name()], found{path: path, dir: info.IsDir()})
		}
	}
	for _, name := range order {
		*budget--
		if *budget < 0 {
			return fmt.Errorf("more than %d entries to link", maxMergedEntries)
		}
		candidates := byName[name]
		target := filepath.Join(destination, name)
		if candidates[0].dir {
			var dirs []string
			for _, candidate := range candidates {
				if candidate.dir {
					dirs = append(dirs, candidate.path)
				}
			}
			if len(dirs) > 1 {
				if err := mergeTree(target, dirs, budget); err != nil {
					return err
				}
				continue
			}
		}
		if err := linkEntry(candidates[0].path, target, candidates[0].dir, budget); err != nil {
			return err
		}
	}
	return nil
}

// linkEntry makes target name source: a symbolic link, or — where the system
// will not make one — a directory of links one level down, or a hard link or
// copy of a file. A target that already exists is a name the host folds onto
// one already placed by a layer that wins it, and is left as it is.
func linkEntry(source, target string, dir bool, budget *int) error {
	err := os.Symlink(source, target)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrExist):
		return nil
	case dir:
		return mergeTree(target, []string{source}, budget)
	default:
		return linkOrCopyFile(source, target)
	}
}

// linkOrCopyFile puts source's bytes at destination: a hard link where the two
// share a filesystem, else a copy. An existing destination is replaced.
func linkOrCopyFile(source, destination string) error {
	_ = os.Remove(destination)
	if err := os.Link(source, destination); err == nil {
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(destination)
		return err
	}
	return out.Close()
}
