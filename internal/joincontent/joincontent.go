// Package joincontent is the Companion's half of a hosted game's join content —
// `AUB/AUG/AUCOM/AUT 244F`.
//
// # What a join-content package is
//
// The runtime-ready client files for ONE hosted build: the compiled BSP, an
// optional `.lit`, and anything its host explicitly declared redistributable.
// AUB stores them and serves them to a reader only through a live lease they can
// see and a map they can read. This package builds one for a host, and fetches,
// verifies and stages one for a joiner.
//
// # Four rules, and each one is a test
//
//  1. **Every rule AUB applies to a manifest is applied again here.** AUB is the
//     authority over what it stores; this machine is the authority over what it
//     writes to its own disk, and a deployment that stopped checking — a bug, an
//     old build, a hostile operator — must not be able to put `../` on anybody's
//     filesystem through a Companion. [Check] is that second check.
//  2. **The aggregate digest is recomputed.** A manifest whose files do not hash
//     to the package digest the ticket was bound to is refused, so the files a
//     joiner stages are the build the lease named, not whatever a manifest
//     request answered with.
//  3. **Bytes go through internal/assetsync.** Every file is published into the
//     one content-addressed store, which verifies size and digest on the way in.
//     There is no second download path and no second cache.
//  4. **Staging is atomic and never touches the user's own game.** A package is
//     assembled in a temporary directory beside its destination, every file is
//     re-verified as it is copied out of the store, and the finished tree is
//     renamed into place. The engine is pointed at a MANAGED base directory whose
//     base-game folders are symbolic links to the user's installation, read-only
//     by construction: nothing is ever written into the directory the user owns.
package joincontent

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
)

// Bounds, identical to AUB's published ones.
const (
	MaxFiles             = 64
	MaxFileBytes         = 128 << 20
	MaxTotalBytes        = 256 << 20
	MaxDestinationLength = 160
	MaxDestinationDepth  = 5
)

// Roles.
const (
	RoleBSP   = "bsp"
	RoleLit   = "lit"
	RoleAsset = "asset"
)

// ErrInvalid wraps every manifest refusal.
var ErrInvalid = errors.New("joincontent: the join-content manifest is refused")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

var allowedAssetRoots = map[string][]string{
	"maps":     {".ent", ".vis", ".loc"},
	"progs":    {".mdl", ".spr", ".spr32"},
	"sound":    {".wav", ".ogg", ".mp3", ".flac"},
	"music":    {".ogg", ".mp3", ".flac"},
	"gfx":      {".lmp", ".tga", ".png", ".jpg", ".jpeg", ".pcx"},
	"textures": {".tga", ".png", ".jpg", ".jpeg", ".wal", ".pcx"},
	"models":   {".mdl", ".md2", ".md3", ".iqm", ".tga", ".png", ".jpg", ".jpeg", ".skin"},
	"env":      {".tga", ".png", ".jpg", ".jpeg", ".pcx"},
}

var forbiddenExtensions = map[string]string{
	".map": "map source", ".apmap": "map source", ".pak": "a game-data archive", ".pk3": "a game-data archive",
	".pk4": "a game-data archive", ".zip": "an archive", ".exe": "an executable", ".dll": "a library",
	".so": "a library", ".dylib": "a library", ".dat": "game code", ".qvm": "game code",
	".cfg": "a config script", ".rc": "a config script", ".bat": "a script", ".sh": "a script",
	".ps1": "a script",
}

// Check applies every rule AUB applies to a manifest, and returns its files in
// canonical order. It is the rule set this machine enforces before writing.
func Check(manifest aub.JoinContentManifest) ([]aub.JoinContentFile, error) {
	if manifest.SchemaVersion != aub.JoinContentSchema {
		return nil, invalid("this Companion reads %s, and was served %q", aub.JoinContentSchema,
			manifest.SchemaVersion)
	}
	if len(manifest.Files) == 0 {
		return nil, invalid("a package has at least its compiled map")
	}
	if len(manifest.Files) > MaxFiles {
		return nil, invalid("a package has at most %d files", MaxFiles)
	}
	seen := map[string]string{}
	files := make([]aub.JoinContentFile, 0, len(manifest.Files))
	var total int64
	bsps, lits := 0, 0
	var bspStem, litStem string
	for _, file := range manifest.Files {
		if err := CheckDestination(file.Destination); err != nil {
			return nil, invalid("%s: %v", file.Destination, err)
		}
		digest := strings.ToLower(file.SHA256)
		if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
			return nil, invalid("%s: the digest is not a SHA-256", file.Destination)
		}
		if file.Bytes < 0 || file.Bytes > MaxFileBytes {
			return nil, invalid("%s: a file is at most %d bytes", file.Destination, MaxFileBytes)
		}
		total += file.Bytes
		if total > MaxTotalBytes {
			return nil, invalid("a package is at most %d bytes", MaxTotalBytes)
		}
		folded := strings.ToLower(file.Destination)
		if previous, clash := seen[folded]; clash {
			return nil, invalid("%s and %s are the same file on a case-insensitive filesystem",
				previous, file.Destination)
		}
		seen[folded] = file.Destination
		extension := strings.ToLower(path.Ext(file.Destination))
		if what, forbidden := forbiddenExtensions[extension]; forbidden {
			return nil, invalid("%s is %s, which never goes in a join package", file.Destination, what)
		}
		stem := strings.TrimSuffix(file.Destination, path.Ext(file.Destination))
		switch file.Role {
		case RoleBSP:
			bsps++
			if extension != ".bsp" || path.Dir(file.Destination) != "maps" {
				return nil, invalid("%s: the compiled map goes at maps/<name>.bsp", file.Destination)
			}
			bspStem = stem
		case RoleLit:
			lits++
			if extension != ".lit" || path.Dir(file.Destination) != "maps" {
				return nil, invalid("%s: coloured lighting goes at maps/<name>.lit", file.Destination)
			}
			litStem = stem
		case RoleAsset:
			if !file.Redistributable {
				return nil, invalid("%s: an asset its host did not declare redistributable", file.Destination)
			}
			top := strings.SplitN(file.Destination, "/", 2)[0]
			allowed, known := allowedAssetRoots[strings.ToLower(top)]
			if !known || !strings.Contains(file.Destination, "/") || !contains(allowed, extension) ||
				extension == ".bsp" || extension == ".lit" {
				return nil, invalid("%s: not a place or a kind of file a join package may carry", file.Destination)
			}
		default:
			return nil, invalid("%s: unknown role %q", file.Destination, file.Role)
		}
		file.SHA256 = digest
		files = append(files, file)
	}
	if bsps != 1 {
		return nil, invalid("a package has exactly one compiled map, and this one has %d", bsps)
	}
	if lits > 1 || (lits == 1 && !strings.EqualFold(litStem, bspStem)) {
		return nil, invalid("the coloured lighting does not belong to the compiled map")
	}
	sort.Slice(files, func(a, b int) bool { return files[a].Destination < files[b].Destination })

	return files, nil
}

// CheckDestination refuses a destination that could land outside the staged
// game directory on any of the three filesystems.
func CheckDestination(destination string) error {
	switch {
	case destination == "":
		return errors.New("no destination")
	case len(destination) > MaxDestinationLength:
		return errors.New("the destination is too long")
	case strings.HasPrefix(destination, "//"), strings.HasPrefix(destination, `\\`):
		return errors.New("a UNC path")
	case strings.HasPrefix(destination, "/"):
		return errors.New("an absolute path")
	case strings.Contains(destination, `\`):
		return errors.New("a backslash")
	case strings.Contains(destination, ":"):
		return errors.New("a drive or stream name")
	}
	segments := strings.Split(destination, "/")
	if len(segments) > MaxDestinationDepth {
		return errors.New("too deep")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return errors.New("an empty, relative or hidden segment")
		}
		for _, r := range segment {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
				r == '_', r == '-', r == '.', r == '+':
			default:
				return fmt.Errorf("the character %q", string(r))
			}
		}
	}
	if path.Clean(destination) != destination {
		return errors.New("not in clean form")
	}

	return nil
}

// Verify checks a served package against the digest a lease or ticket named: the
// rules, then the aggregate digest recomputed over what was served.
func Verify(pkg aub.JoinPackage, expected string) ([]aub.JoinContentFile, error) {
	files, err := Check(pkg.Manifest())
	if err != nil {
		return nil, err
	}
	computed := pkg.Manifest().Digest()
	if !strings.EqualFold(computed, pkg.PackageSHA256) {
		return nil, invalid("the manifest hashes to %s and was served as %s", Short(computed),
			Short(pkg.PackageSHA256))
	}
	if expected != "" && !strings.EqualFold(computed, expected) {
		return nil, invalid("this game names package %s and the manifest served is %s", Short(expected),
			Short(computed))
	}

	return files, nil
}

// Short is a digest's first twelve hex characters, for a sentence.
func Short(digest string) string {
	digest = strings.TrimPrefix(strings.ToLower(digest), "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}

	return digest
}

func contains(set []string, value string) bool {
	for _, item := range set {
		if item == value {
			return true
		}
	}

	return false
}
