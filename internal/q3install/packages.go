package q3install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3packages"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// # The packages a map is bound to (Q3_017A)
//
// A saved Quake III map names the packages it is built with, by digest. The
// build fetches exactly those archives into the Companion's cache, stages them
// for Q3Map2, and records each in its manifest; `q3pack` copies that identity
// into the package's plan as a source whose `origin` is `cache` or `account`.
// Until `Q3_017A` nothing carried it one step further: a managed install linked
// the user's game and placed the map's own archive, the engine was started on
// that, and every shader and image the compiler had read from a bound package
// was missing in the engine. `Q3_014A` got a textured map only by linking the
// package beside the map by hand.
//
// So an install reads the bound packages out of the package's own record —
// name, game folder, digest and size, never a texture name — finds each in the
// cache BY DIGEST, and puts it in the managed base under the folder and the
// name the binding gives. The name is part of what was bound: it decides which
// of two archives the engine reads first.
//
// # What this is not
//
// It is not redistribution. A bound package placed in a directory on this
// machine has gone nowhere; `q3pack` still writes into the map's archive only
// what a grant covers, and what is placed here is never read back into one.
// And it is not a write into the game: a game-folder install stays one file,
// and says which bound packages the engine will not find.

// The states of a bound package in an installation.
const (
	// PackageStaged: the install put it in the managed base directory.
	PackageStaged = "staged"
	// PackageInGameFolder: the user's own game directory already holds these
	// bytes under this name, so there was nothing to add.
	PackageInGameFolder = "in_game_folder"
	// PackageNotStaged: the engine will not find it. A game-folder install
	// writes one file and no more; Reason says what to do.
	PackageNotStaged = "not_staged"
)

// maxConflictsListed bounds how many paths of one bound package are named as
// shadowed or as overriding; the counts beside them are of all of them.
const maxConflictsListed = 50

// Package is one archive the saved map is bound to, as this installation has it.
type Package struct {
	// Name is the archive's file name, and Game the directory of the base path
	// it belongs in: both are the binding's, and the name decides load order.
	Name   string `json:"name"`
	Game   string `json:"game"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Origin is how the build got it: `cache` or `account`.
	Origin string `json:"origin,omitempty"`
	// RuntimeFiles is how many files an engine needs from it, by the package's
	// own review.
	RuntimeFiles int `json:"runtime_files"`
	// State is `staged`, `in_game_folder` or `not_staged`.
	State string `json:"state"`
	// Method is how a staged one stands for the cached file: `hardlink`, or
	// `copy` where the cache is on another filesystem.
	Method string `json:"method,omitempty"`
	// Path is where the engine reads it, or would.
	Path   string `json:"path"`
	Reason string `json:"reason,omitempty"`

	// Searched is what the engine searches before it in its game directory,
	// first first. Shadowed are paths it has that the engine takes from
	// something searched before it; Overrides are paths it hides in something
	// searched after. Each list names the first [maxConflictsListed]; the
	// counts are of all.
	Searched       []string `json:"searched_before,omitempty"`
	Shadowed       []Shadow `json:"shadowed,omitempty"`
	ShadowedCount  int      `json:"shadowed_count,omitempty"`
	Overrides      []Shadow `json:"overrides,omitempty"`
	OverridesCount int      `json:"overrides_count,omitempty"`

	// source is the verified file in the cache. Not part of the record.
	source string
	// members are its paths, lower-cased → as spelled.
	members map[string]string
}

// boundSources are the plan's sources that are packages the map is bound to.
func boundSources(plan *q3pack.Plan) []q3pack.Source {
	var out []q3pack.Source
	for _, source := range plan.Sources {
		if source.Kind == "archive" && source.Origin != "" {
			out = append(out, source)
		}
	}
	return out
}

// resolvePackages decides, for each package the map is bound to, where it
// belongs in what the engine reads and whether it can be put there. It writes
// nothing. A package that is not on this machine, or is not the size the build
// recorded, is refused here — before anything says an engine could be started.
func resolvePackages(request Request, record *q3pack.Record, root, baseGame, game, basePath string) ([]Package, error) {
	bound := boundSources(record.Plan)
	if len(bound) == 0 {
		return nil, nil
	}
	var out []Package
	var absent []string
	for _, source := range bound {
		if err := q3vfs.CheckArchiveName(source.Name); err != nil {
			return nil, failure.As(failure.ContentRefused, fmt.Errorf("q3install: a package the map is bound to: %w", err))
		}
		digest := hexDigest(source.SHA256)
		if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
			return nil, failure.As(failure.ContentRefused, fmt.Errorf(
				"q3install: the package records no SHA-256 for the bound package %s, so it cannot be told from another "+
					"archive of that name. Create the package again", source.Name))
		}
		// The folder the binding names, in THIS game folder: the base game's
		// directory whatever it is called here, or beside the map.
		dir := game
		if strings.EqualFold(source.Game, record.Plan.BaseGame) {
			dir = baseGame
		}
		pkg := Package{
			Name: source.Name, Game: dir, SHA256: digest, Size: source.Size,
			Origin: source.Origin, RuntimeFiles: source.RuntimeFiles,
		}
		if strings.EqualFold(dir, game) && strings.EqualFold(source.Name, record.Archive.File) {
			return nil, failure.As(failure.InstallConflict, fmt.Errorf(
				"q3install: the map is bound to a package called %s, which is the name of the map's own archive in %s: "+
					"two archives cannot both have that name", source.Name, dir))
		}
		duplicate := false
		for _, earlier := range out {
			if !strings.EqualFold(earlier.Game, dir) || !strings.EqualFold(earlier.Name, source.Name) {
				continue
			}
			if earlier.SHA256 != digest {
				return nil, failure.As(failure.ContentRefused, fmt.Errorf(
					"q3install: the map is bound to two different packages called %s under %s (sha256 %s and %s): "+
						"two different archives cannot both have that name", source.Name, dir, earlier.SHA256, digest))
			}
			duplicate = true
		}
		if duplicate {
			continue
		}

		userDir := filepath.Join(root, dir)
		theirs, err := sameNamed(userDir, source.Name)
		if err != nil {
			return nil, err
		}
		inFolder := false
		if theirs != "" {
			found, size, hashErr := hashFile(theirs)
			inFolder = hashErr == nil && found == digest && size == source.Size
		}
		switch {
		case inFolder:
			pkg.State = PackageInGameFolder
			pkg.Reason = fmt.Sprintf("%s already holds these bytes under that name", userDir)
			// In a managed base that is the link to the user's own file, under
			// the name the user's file has.
			pkg.Path = filepath.Join(basePath, dir, filepath.Base(theirs))
			pkg.source = theirs
		case request.Kind == GameFolder:
			pkg.State = PackageNotStaged
			pkg.Path = filepath.Join(userDir, source.Name)
			pkg.Reason = fmt.Sprintf("a game-folder install writes one file, the map's archive, and nothing else into "+
				"your game. The engine will not find %s unless you put that exact archive (sha256 %s) in %s yourself; "+
				"a managed install stages it for you", source.Name, digest, userDir)
			if theirs != "" {
				pkg.Reason = fmt.Sprintf("%s holds a different file called %s, and the engine will read THAT one: the "+
					"map is bound to the archive with sha256 %s. A game-folder install does not write over or beside "+
					"it; a managed install refuses it by name", userDir, filepath.Base(theirs), digest)
			}
		case theirs != "":
			return nil, failure.As(failure.InstallConflict, fmt.Errorf(
				"q3install: %s holds a %s that is not the package the map is bound to (sha256 %s). Two different "+
					"archives cannot both have that name in what the engine reads, and the engine would be given one "+
					"the map was not built with. Remove or rename the one in the game folder, or bind the map to it",
				userDir, filepath.Base(theirs), digest))
		default:
			path, size, err := cached(request.Cache, source.Game, source.Name, digest)
			if err != nil {
				absent = append(absent, fmt.Sprintf("%s (sha256 %s)", source.Name, digest))
				continue
			}
			if size != source.Size {
				return nil, failure.As(failure.ArchiveDamaged, fmt.Errorf(
					"q3install: the bound package %s in the Companion's cache is %d bytes, and the build read one of %d "+
						"bytes (sha256 %s). Nothing was installed. Build the map again so the package is fetched again",
					source.Name, size, source.Size, digest))
			}
			pkg.State = PackageStaged
			pkg.Path = filepath.Join(basePath, dir, source.Name)
			pkg.source = path
		}
		out = append(out, pkg)
	}
	if len(absent) > 0 {
		return nil, failure.As(failure.GameDataMissing, fmt.Errorf(
			"q3install: the map is bound to %s, and %s no longer on this machine: the build kept %s in the Companion's "+
				"cache and %s not there now. An engine started on this install would draw the map without %s, so "+
				"nothing was installed. Build the map again while signed in — the build fetches a bound package by its "+
				"digest and keeps it — then package and install that build",
			strings.Join(absent, ", "), plural(len(absent), "it is", "they are"), plural(len(absent), "it", "them"),
			plural(len(absent), "it is", "they are"), plural(len(absent), "it", "them")))
	}
	return out, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// cached finds one bound archive in the cache through the resolver the build
// used, with no account: an install fetches nothing, and reads only what a
// build already verified and kept.
func cached(cache q3packages.Cache, game, name, digest string) (string, int64, error) {
	if cache == nil {
		return "", 0, errors.New("no package cache")
	}
	resolved, err := q3packages.Resolve(context.Background(), nil, cache, &q3packages.Ledger{
		SchemaVersion: q3packages.Schema,
		Packages:      []q3packages.Binding{{Root: game, ArchiveName: name, ArchiveSHA256: digest}},
	})
	if err != nil {
		return "", 0, err
	}
	if len(resolved) != 1 {
		return "", 0, errors.New("not resolved")
	}
	info, err := os.Stat(resolved[0].Path)
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("the cached file is not readable: %v", err)
	}
	return resolved[0].Path, info.Size(), nil
}

// sameNamed finds a regular file in dir whose name is `name` without regard to
// case — the engine's own comparison — and returns its path, or "".
func sameNamed(dir, name string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", failure.As(failure.GameDataMissing, fmt.Errorf("q3install: reading %s: %w", dir, err))
	}
	for _, entry := range entries {
		if !strings.EqualFold(entry.Name(), name) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", nil
}

// stagePackages puts each bound package in the managed base directory and
// checks the file that is THERE against the digest and size the build
// recorded. A hard link where the cache is on the same filesystem — no second
// copy of a texture pack per install — and a verified copy where it is not.
func stagePackages(ctx context.Context, installation *Installation, undo *undoLog) error {
	for i := range installation.Packages {
		pkg := &installation.Packages[i]
		if pkg.State != PackageStaged {
			continue
		}
		if err := ctx.Err(); err != nil {
			return failure.As(failure.Cancelled, fmt.Errorf("q3install: the install was cancelled: %w", err))
		}
		if err := os.MkdirAll(filepath.Dir(pkg.Path), 0o700); err != nil {
			return fmt.Errorf("q3install: %w", err)
		}
		mismatch := func(size int64, digest string) error {
			return failure.As(failure.ArchiveDamaged, fmt.Errorf(
				"q3install: the bound package %s in the Companion's cache is %d bytes, sha256 %s, and the map was built "+
					"with one of %d bytes, sha256 %s. It was not given to the engine and nothing was installed. Build the "+
					"map again so the package is fetched again", pkg.Name, size, digest, pkg.Size, pkg.SHA256))
		}
		if err := os.Link(pkg.source, pkg.Path); err == nil {
			path := pkg.Path
			undo.add(func() error {
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				return nil
			})
			digest, size, err := hashFileContext(ctx, pkg.Path)
			if err != nil {
				if ctx.Err() != nil {
					return failure.As(failure.Cancelled, fmt.Errorf("q3install: the install was cancelled: %w", ctx.Err()))
				}
				return fmt.Errorf("q3install: reading the bound package %s: %w", pkg.Name, err)
			}
			if size != pkg.Size || digest != pkg.SHA256 {
				return mismatch(size, digest)
			}
			pkg.Method = "hardlink"
			continue
		} else if errors.Is(err, fs.ErrExist) {
			return failure.As(failure.InstallConflict, fmt.Errorf(
				"q3install: %s is already there, and it is not something this install put there", pkg.Path))
		}
		archive := Archive{Name: pkg.Name, SHA256: pkg.SHA256, Size: pkg.Size, Path: pkg.Path}
		if err := copyVerified(ctx, pkg.source, archive, undo, mismatch); err != nil {
			return err
		}
		pkg.Method = "copy"
	}
	return nil
}

// hashFileContext is hashFile that stops when its context is cancelled.
func hashFileContext(ctx context.Context, name string) (string, int64, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := copyContext(ctx, hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// verifyPackages re-checks every bound package the engine is about to read:
// where it was put, and still the bytes the map was built with.
func verifyPackages(installation *Installation) error {
	for _, pkg := range installation.Packages {
		if pkg.State != PackageStaged && pkg.State != PackageInGameFolder {
			continue
		}
		info, err := os.Stat(pkg.Path)
		if err != nil || !info.Mode().IsRegular() {
			return failure.As(failure.GameDataMissing, fmt.Errorf(
				"q3install: the bound package %s is no longer at %s, so the engine would draw the map without it. "+
					"Install the package again", pkg.Name, pkg.Path))
		}
		digest, size, err := hashFile(pkg.Path)
		if err != nil {
			return fmt.Errorf("q3install: %w", err)
		}
		if size != pkg.Size || digest != pkg.SHA256 {
			return failure.As(failure.ArchiveDamaged, fmt.Errorf(
				"q3install: %s is not the bound package that was installed (it is %d bytes, sha256 %s, and the map was "+
					"built with %d bytes, sha256 %s). Install the package again",
				pkg.Path, size, digest, pkg.Size, pkg.SHA256))
		}
	}
	return nil
}

// NotStaged names the bound packages the engine will not find in this
// installation. Empty for a managed one: that is refused instead.
func (installation *Installation) NotStaged() []string {
	var out []string
	for _, pkg := range installation.Packages {
		if pkg.State == PackageNotStaged {
			out = append(out, pkg.Game+"/"+pkg.Name)
		}
	}
	return out
}

// samePackages reports whether a recorded installation has the bound packages
// a fresh look says it should, in the same places.
func samePackages(recorded, wanted []Package) bool {
	if len(recorded) != len(wanted) {
		return false
	}
	for i := range wanted {
		a, b := recorded[i], wanted[i]
		if a.Name != b.Name || a.Game != b.Game || a.SHA256 != b.SHA256 || a.Size != b.Size || a.State != b.State {
			return false
		}
	}
	return true
}

// listedArchive is one archive of a game directory, as the engine would list it.
type listedArchive struct {
	name    string
	members []string
}

// gameListing is what an engine searches in one game directory.
type gameListing struct {
	name   string
	exists bool
	// loose is every loose file, lower-cased → as spelled.
	loose map[string]string
	// archives are sorted the engine's way: later name first, without regard
	// to case.
	archives []listedArchive
	limits   []string
}

// listGame reads one of the user's game directories: its loose files and the
// members of each archive directly in it. `except` is a file name left out —
// the map's own archive, which is placed by this install.
func listGame(dir string, except string) (*gameListing, error) {
	listing := &gameListing{name: filepath.Base(dir), loose: map[string]string{}}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return listing, nil
	}
	listing.exists = true
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, failure.As(failure.GameDataMissing, fmt.Errorf("q3install: reading %s: %w", dir, err))
	}
	var archives []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.EqualFold(filepath.Ext(name), ".pk3") && !strings.EqualFold(name, except) {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
				archives = append(archives, name)
			}
		}
	}
	if len(archives) > maxArchives {
		return nil, fmt.Errorf("q3install: %s holds more than %d PK3 archives", dir, maxArchives)
	}
	loose := 0
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		loose++
		if loose > maxLooseFiles {
			listing.limits = append(listing.limits, fmt.Sprintf(
				"the loose files of %s past the first %d", dir, maxLooseFiles))
			return filepath.SkipAll
		}
		relative, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		slashed := filepath.ToSlash(relative)
		if _, seen := listing.loose[strings.ToLower(slashed)]; !seen {
			listing.loose[strings.ToLower(slashed)] = slashed
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("q3install: reading %s: %w", dir, walkErr)
	}
	for _, name := range archives {
		names, truncated, err := archiveMembers(filepath.Join(dir, name))
		if err != nil {
			listing.limits = append(listing.limits, fmt.Sprintf("%s, which could not be read as an archive (%v)", name, err))
		} else if truncated {
			listing.limits = append(listing.limits, fmt.Sprintf("the members of %s past the first %d", name, maxArchiveMembers))
		}
		listing.archives = append(listing.archives, listedArchive{name: name, members: names})
	}
	listing.sort()
	return listing, nil
}

// add lists an archive this install places in the directory.
func (l *gameListing) add(name string, members []string) {
	l.archives = append(l.archives, listedArchive{name: name, members: members})
	l.sort()
}

func (l *gameListing) sort() {
	sort.SliceStable(l.archives, func(i, j int) bool {
		return strings.ToLower(l.archives[i].name) > strings.ToLower(l.archives[j].name)
	})
}

// place looks each member of one archive up in what the engine searches before
// it, and after it, in its game directory.
//
// The rules are the ones measured on ioquake3 (`Q3_011`): a loose file beats
// every archive, and among archives the later name wins, compared without
// regard to case.
func (l *gameListing) place(subject string, members map[string]string) (searched []string, shadowed, overrides []Shadow) {
	if l.exists {
		searched = append(searched, "loose files in "+l.name)
	}
	taken := map[string]bool{}
	for lower, member := range members {
		if relative, isLoose := l.loose[lower]; isLoose {
			taken[member] = true
			shadowed = append(shadowed, Shadow{Path: member, By: "the loose file " + relative})
		}
	}
	ours := strings.ToLower(subject)
	for _, archive := range l.archives {
		if strings.EqualFold(archive.name, subject) {
			continue
		}
		before := strings.ToLower(archive.name) > ours
		if before {
			searched = append(searched, archive.name)
		}
		for _, inside := range archive.members {
			member, isOurs := members[strings.ToLower(inside)]
			if !isOurs {
				continue
			}
			if before {
				if !taken[member] {
					taken[member] = true
					shadowed = append(shadowed, Shadow{Path: member, By: archive.name})
				}
				continue
			}
			overrides = append(overrides, Shadow{Path: member, By: archive.name})
		}
	}
	byPath := func(list []Shadow) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Path != list[j].Path {
				return list[i].Path < list[j].Path
			}
			return list[i].By < list[j].By
		})
	}
	byPath(shadowed)
	byPath(overrides)
	return searched, shadowed, overrides
}

// memberSet is an archive's paths, lower-cased → as spelled.
func memberSet(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[strings.ToLower(name)] = name
	}
	return out
}

// loadOrder says where the map's archive, and each bound package this install
// places, sits in what the engine searches — in the directory each is in.
func loadOrder(record *q3pack.Record, installation *Installation, root string) (LoadOrder, error) {
	order := LoadOrder{Limits: []string{
		"the engine's own home directory (`~/.q3a` on Linux): an engine searches it before the game folder, " +
			"and what is in it is the player's configuration, not something an install reads",
	}}
	mapMembers := map[string]string{}
	for _, member := range record.Plan.Members {
		mapMembers[strings.ToLower(member.Path)] = member.Path
	}

	listings := map[string]*gameListing{}
	var listed []*gameListing
	listingFor := func(game string) (*gameListing, error) {
		key := strings.ToLower(game)
		if listing, seen := listings[key]; seen {
			return listing, nil
		}
		except := ""
		if strings.EqualFold(game, installation.Game) {
			except = record.Archive.File
		}
		listing, err := listGame(filepath.Join(root, game), except)
		if err != nil {
			return nil, err
		}
		listings[key] = listing
		listed = append(listed, listing)
		return listing, nil
	}

	own, err := listingFor(installation.Game)
	if err != nil {
		return order, err
	}
	// What this install adds to each directory is searched like anything else
	// in it: a bound package named after the map's archive is read before it.
	for i := range installation.Packages {
		pkg := &installation.Packages[i]
		if pkg.State != PackageStaged {
			continue
		}
		names, truncated, err := archiveMembers(pkg.source)
		if err != nil {
			return order, failure.As(failure.ArchiveDamaged, fmt.Errorf(
				"q3install: the bound package %s in the Companion's cache cannot be read as an archive (%v). Nothing "+
					"was installed. Build the map again so the package is fetched again", pkg.Name, err))
		}
		if truncated {
			order.Limits = append(order.Limits, fmt.Sprintf("the members of the bound package %s past the first %d",
				pkg.Name, maxArchiveMembers))
		}
		pkg.members = memberSet(names)
		listing, err := listingFor(pkg.Game)
		if err != nil {
			return order, err
		}
		listing.add(pkg.Name, names)
	}
	staged := false
	for i := range installation.Packages {
		pkg := &installation.Packages[i]
		if pkg.State == PackageInGameFolder {
			// The user's own archive: already in the listing, and read here
			// only to say what it supplies.
			if names, _, err := archiveMembers(pkg.source); err == nil {
				pkg.members = memberSet(names)
			}
		}
		if pkg.State != PackageStaged {
			continue
		}
		staged = true
		listing := listings[strings.ToLower(pkg.Game)]
		if strings.EqualFold(pkg.Game, installation.Game) {
			// The map's own archive is in this directory too.
			with := *listing
			with.archives = append(append([]listedArchive(nil), listing.archives...),
				listedArchive{name: record.Archive.File, members: planPaths(record.Plan)})
			with.sort()
			listing = &with
		}
		searched, shadowed, overrides := listing.place(pkg.Name, pkg.members)
		pkg.Searched = searched
		pkg.ShadowedCount, pkg.OverridesCount = len(shadowed), len(overrides)
		pkg.Shadowed, pkg.Overrides = clipShadows(shadowed), clipShadows(overrides)
	}
	order.Searched, order.Shadowed, order.Overrides = own.place(record.Archive.File, mapMembers)
	for _, listing := range listed {
		order.Limits = append(order.Limits, listing.limits...)
	}
	if staged {
		order.Limits = append(order.Limits,
			"the order Q3Map2 read the bound packages in: the compiler was given them in a content folder of their "+
				"own, and an engine reads one game directory in name order, so where a bound package and the game "+
				"supply the same path the winner named here is the ENGINE's — it is listed, not assumed to match")
	}
	return order, nil
}

func planPaths(plan *q3pack.Plan) []string {
	out := make([]string, 0, len(plan.Members))
	for _, member := range plan.Members {
		out = append(out, member.Path)
	}
	return out
}

func clipShadows(list []Shadow) []Shadow {
	if len(list) > maxConflictsListed {
		return list[:maxConflictsListed]
	}
	return list
}

// suppliedByPackages counts the files the map's archive does not carry that a
// bound package in this installation has.
func suppliedByPackages(installation *Installation) int {
	count := 0
	for _, path := range installation.NotCarried {
		lower := strings.ToLower(path)
		for i := range installation.Packages {
			pkg := &installation.Packages[i]
			if pkg.State == PackageNotStaged {
				continue
			}
			if _, has := pkg.members[lower]; has {
				count++
				break
			}
		}
	}
	return count
}

// sweep removes what earlier installs left for later: a generation that was
// replaced while something still had it open, and a half-built one whose
// process died. Both are this program's own directories, by name, and a
// failure here is not a reason to fail an install.
func sweep(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, retiredPrefix):
		case strings.HasPrefix(name, buildingPrefix):
			// Somebody else's install may be building it right now.
			info, err := entry.Info()
			if err != nil || now.Sub(info.ModTime()) < staleBuilding {
				continue
			}
		default:
			continue
		}
		_ = removeManaged(filepath.Join(dir, name))
	}
}

const (
	// buildingPrefix names a generation that is being built; retiredPrefix one
	// that was replaced. Neither is an installation: [List] skips a dot name.
	buildingPrefix = ".installing-"
	retiredPrefix  = ".retired-"
	staleBuilding  = time.Hour
)
