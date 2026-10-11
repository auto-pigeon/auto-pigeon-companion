// Package q3install puts one Quake III map package where an engine will load
// it, and takes it away again.
//
// # Two targets, and why the default is not the game folder
//
// A Quake III engine loads every `.pk3` in `<fs_basepath>/<game>`. The obvious
// install is therefore a copy into the user's own `baseq3` — a directory that
// holds the game they bought, their configuration and whatever else they have
// collected in twenty-five years. This package's default does not touch it.
//
// **Managed** builds a base directory the Companion owns: the game directory
// the archive is meant for is a real directory there, holding the archive and a
// link to each thing in the user's own directory of that name; every other
// game directory of the user's is one link. The engine is pointed at the
// managed directory, reads the user's game through the links, and nothing is
// written into the user's game folder at all. Taking it away removes a
// directory this program made. The packages the saved map is bound to are
// staged there too, from the Companion's cache and by digest — packages.go.
//
// **GameFolder** is the explicit target, for somebody who wants the archive in
// their real game: ONE file, written beside what is there, never over anything,
// and recorded with its digest so that removing it removes that file only while
// it is still that file.
//
// # Load order is checked, not assumed
//
// Measured on ioquake3 1.36 (`Q3_011`): within one game directory a loose file
// beats every archive, and among archives the LATER name wins, compared without
// regard to case; the mod directory beats the base game. An archive named
// `auto-pigeon-…` therefore loses to `pak0.pk3` and to any `z…` archive beside
// it. So before anything is installed, each member of the package is looked up
// in what would be searched before it. A map that something else would supply
// is refused — the engine would load a different map and say nothing — and any
// other member that would lose is reported by name.
package q3install

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/joincontent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3packages"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// SchemaVersion versions [Installation]. 1.1 (`Q3_017A`) added `packages`: the
// archives the saved map is bound to, as the installation has them.
const SchemaVersion = "aucom.q3-install/1.1"

// schemaVersion10 is the record before bound packages were staged. It is still
// read — an installation made by an earlier build can be listed, run and
// removed — and it says nothing about packages, so installing the same package
// again replaces it with one that does.
const schemaVersion10 = "aucom.q3-install/1.0"

// RecordFileName is an installation's record, in its own directory.
const RecordFileName = "q3-install.json"

// BaseDirName is the managed base directory inside an installation's own.
const BaseDirName = "base"

// Kind is where a package is installed.
type Kind string

const (
	// Managed: a base directory the Companion owns. The default.
	Managed Kind = "managed"
	// GameFolder: one file in the user's own game folder, on their say-so.
	GameFolder Kind = "game_folder"
)

// Bounds on what an install reads of somebody's game directory.
const (
	maxEntries        = 4096
	maxArchives       = 256
	maxArchiveMembers = 200000
	maxLooseFiles     = 200000
)

// Request is one install.
type Request struct {
	// Package is the package, already checked against its archive.
	Package *q3pack.Record
	Kind    Kind
	// GameRoot is the user's game folder: the directory that CONTAINS the base
	// game directory.
	GameRoot string
	// BaseGame is the name of the base directory in THIS game folder. Empty
	// means the package's own, `baseq3`. A free standalone game names another.
	BaseGame string
	// Game is the directory the archive goes into. Empty means the package's
	// own: the mod the map was built for, or the base game.
	Game string
	// Dir is where installations are recorded, and where managed ones live.
	Dir string
	// Cache is the content-addressed store a build keeps a map's bound packages
	// in. A managed install reads them from it, by digest. Nil holds nothing:
	// a package bound to one is then refused, by name.
	Cache q3packages.Cache
	// Now is the clock. Nil means time.Now.
	Now func() time.Time

	// afterCopy runs once the archive is in place and before the record is
	// written. A test uses it to cancel at the moment an install is half done.
	afterCopy func()
}

// Archive is the installed file.
type Archive struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Path is where it is on this machine.
	Path string `json:"path"`
}

// Link is one thing in a managed base directory that stands for the user's own.
type Link struct {
	// Name is relative to the managed base directory.
	Name string `json:"name"`
	// Target is the user's file or directory it stands for.
	Target string `json:"target"`
	// Method is `symlink`, `junction-or-symlink` for a directory, or `hardlink`.
	Method string `json:"method"`
}

// Shadow is one member of the package that something else also supplies.
type Shadow struct {
	Path string `json:"path"`
	// By is what is searched before the package and has the same path.
	By string `json:"by"`
}

// LoadOrder is where the package sits in what the engine searches.
type LoadOrder struct {
	// Searched is what an engine searches before the package's archive, first
	// first, in the game directory the archive is installed into.
	Searched []string `json:"searched_before"`
	// Shadowed are members of the package the engine would take from somewhere
	// else. The map is never among them: that is a refusal.
	Shadowed []Shadow `json:"shadowed,omitempty"`
	// Overrides are members of the package that hide a file of the same path in
	// something searched AFTER it.
	Overrides []Shadow `json:"overrides,omitempty"`
	// Limits say what was not looked at.
	Limits []string `json:"limits,omitempty"`
}

// Installation is one package, installed.
type Installation struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	PackageID     string `json:"package_id"`
	BuildID       string `json:"build_id,omitempty"`
	MapName       string `json:"map_name"`
	Kind          Kind   `json:"kind"`
	// GameRoot is the user's game folder. BasePath is what the engine is given
	// for `fs_basepath`: the managed base directory, or the game folder itself.
	GameRoot string `json:"game_root"`
	BasePath string `json:"base_path"`
	BaseGame string `json:"base_game"`
	// Game is the directory the archive is in, and FSGame is what the engine is
	// given for `fs_game`: the same name. For a base game install that is the
	// base game's own name, which an engine treats as no mod at all.
	Game   string `json:"game"`
	FSGame string `json:"fs_game"`

	Archive Archive `json:"archive"`
	// CreatedGameDir records a game directory this install made in the user's
	// game folder (a mod that was not there). Removal takes it away again only
	// if it is empty.
	CreatedGameDir bool `json:"created_game_dir,omitempty"`
	// Links are what a managed base directory holds besides the archive.
	Links []Link `json:"links,omitempty"`
	// Packages are the archives the saved map is bound to: staged into a
	// managed base directory, already in the game folder, or — for a
	// game-folder install — named as something the engine will not find.
	Packages  []Package `json:"packages,omitempty"`
	LoadOrder LoadOrder `json:"load_order"`
	// Complete is the package's own: false when it lacks something an engine
	// needs. NotCarried is what.
	Complete   bool     `json:"complete"`
	NotCarried []string `json:"not_carried,omitempty"`
	// SuppliedByPackages is how many of NotCarried a bound package in this
	// installation has: files the map's archive does not carry and the engine
	// finds all the same, on this machine.
	SuppliedByPackages int       `json:"supplied_by_packages,omitempty"`
	InstalledAt        time.Time `json:"installed_at"`

	// Directory is where this record is.
	Directory string `json:"directory,omitempty"`
}

// ErrNotInstalled reports an installation that is not there.
var ErrNotInstalled = errors.New("q3install: no such installation")

// Preview says where a package would be installed, what would be searched
// before it and which bound packages would be staged with it, and writes
// nothing. It refuses what [Install] refuses, with one difference: a preview
// checks that each bound package is in the cache and is the size the build
// recorded, and the install is what hashes it.
func Preview(request Request) (*Installation, error) {
	plan, err := resolve(request)
	if err != nil {
		return nil, err
	}
	return plan.installation, nil
}

// planned is an install that has been checked and not yet performed.
type planned struct {
	installation *Installation
	// userGame is the user's own directory of the game the archive goes into,
	// which may not exist.
	userGame       string
	userGameExists bool
	// already is true when this program's own receipt says the archive is
	// installed there, with these bytes.
	already *Installation
}

func resolve(request Request) (*planned, error) {
	record := request.Package
	if record == nil || record.Plan == nil {
		return nil, errors.New("q3install: no package to install")
	}
	if request.Dir == "" {
		return nil, errors.New("q3install: nowhere to record installations")
	}
	switch request.Kind {
	case Managed, GameFolder:
	default:
		return nil, fmt.Errorf("q3install: %q is not an install target; it is %q or %q", request.Kind, Managed, GameFolder)
	}
	if strings.TrimSpace(request.GameRoot) == "" {
		return nil, failure.As(failure.GameDataMissing, errors.New(
			"q3install: this engine has no game folder set on this machine, so there is no game to install the map beside"))
	}
	root, err := filepath.Abs(request.GameRoot)
	if err != nil {
		return nil, fmt.Errorf("q3install: the game folder: %w", err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, failure.As(failure.GameDataMissing, fmt.Errorf(
			"q3install: the game folder %s is not a directory on this machine", root))
	}

	baseGame := request.BaseGame
	if baseGame == "" {
		baseGame = record.Plan.BaseGame
	}
	game := request.Game
	if game == "" {
		game = record.Plan.GameDir
		// A map built for the base game goes into THIS game folder's base
		// directory, whatever that is called. A free standalone game does not
		// call it `baseq3` — measured: ioquake3 insists on Quake III's own
		// data in a directory of that name — so the name the build used for
		// "the base game" is not the name to install under.
		if strings.EqualFold(record.Plan.GameDir, record.Plan.BaseGame) {
			game = baseGame
		}
	}
	for _, name := range []string{baseGame, game} {
		if name == "" {
			return nil, errors.New("q3install: the package names no game directory")
		}
		if err := q3vfs.CheckFSGame(name); err != nil {
			return nil, err
		}
	}
	if err := q3vfs.CheckArchiveName(record.Archive.File); err != nil {
		return nil, fmt.Errorf("q3install: the package: %w", err)
	}

	userGame := filepath.Join(root, game)
	userGameExists := false
	switch info, err := os.Stat(userGame); {
	case err == nil && info.IsDir():
		userGameExists = true
	case err == nil:
		return nil, failure.As(failure.InstallConflict, fmt.Errorf("q3install: %s is a file, not a game directory", userGame))
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("q3install: %s: %w", userGame, err)
	}
	if strings.EqualFold(game, baseGame) && !userGameExists {
		// Not created. A base game directory this program made would be an
		// empty one the engine then fails in, on a line that blames the game.
		return nil, failure.As(failure.GameDataMissing, fmt.Errorf(
			"q3install: the game folder %s has no %s directory. Choose the folder that CONTAINS %s — the "+
				"game's own data is yours to supply, and the Companion ships none", root, baseGame, baseGame))
	}

	digest := hexDigest(record.Archive.SHA256)
	installation := &Installation{
		SchemaVersion: SchemaVersion,
		ID:            installID(request.Kind, root, game, record.Archive.File, digest),
		PackageID:     record.ID,
		BuildID:       record.Plan.BuildID,
		MapName:       record.Plan.Map.Name,
		Kind:          request.Kind,
		GameRoot:      root,
		BaseGame:      baseGame,
		Game:          game,
		FSGame:        game,
		Archive:       Archive{Name: record.Archive.File, SHA256: digest, Size: record.Archive.Size},
		Complete:      record.Complete,
		NotCarried:    append([]string(nil), record.Plan.NotCarried...),
	}
	installation.Directory = filepath.Join(request.Dir, installation.ID)
	switch request.Kind {
	case Managed:
		installation.BasePath = filepath.Join(installation.Directory, BaseDirName)
		installation.Archive.Path = filepath.Join(installation.BasePath, game, record.Archive.File)
	default:
		installation.BasePath = root
		installation.Archive.Path = filepath.Join(userGame, record.Archive.File)
	}

	plan := &planned{installation: installation, userGame: userGame, userGameExists: userGameExists}
	if existing, err := Load(request.Dir, installation.ID); err == nil {
		plan.already = existing
	}

	// A file of the archive's name in the user's own game directory. In the
	// game folder it is where the archive would be written; in a managed
	// install it is a file the link beside the archive would have to share a
	// name with. Either way two different archives cannot both have that name.
	if userGameExists {
		theirs := filepath.Join(userGame, record.Archive.File)
		if info, err := os.Lstat(theirs); err == nil {
			ours := plan.already != nil && request.Kind == GameFolder
			if !ours {
				sameBytes := false
				if info.Mode().IsRegular() {
					if found, _, hashErr := hashFile(theirs); hashErr == nil && found == digest {
						sameBytes = true
					}
				}
				detail := "and it is a different file"
				if sameBytes {
					detail = "with the same bytes, and this program has no record of putting it there"
				}
				return nil, failure.As(failure.InstallConflict, fmt.Errorf(
					"q3install: %s already holds a %s, %s. It was not written over. Remove or rename it, or "+
						"install into a mod directory instead", userGame, record.Archive.File, detail))
			}
		}
	}

	packages, err := resolvePackages(request, record, root, baseGame, game, installation.BasePath)
	if err != nil {
		return nil, err
	}
	installation.Packages = packages
	order, err := loadOrder(record, installation, root)
	if err != nil {
		return nil, err
	}
	installation.LoadOrder = order
	installation.SuppliedByPackages = suppliedByPackages(installation)
	bsp := "maps/" + record.Plan.Map.Name + ".bsp"
	for _, shadow := range order.Shadowed {
		if strings.EqualFold(shadow.Path, bsp) {
			return nil, failure.As(failure.LoadOrderShadowed, fmt.Errorf(
				"q3install: %s is also supplied by %s in %s, which the engine searches BEFORE %s (measured on "+
					"ioquake3: a loose file beats every archive, and among archives the later name wins). The "+
					"engine would load that map instead and say nothing. Install into a mod directory, or remove "+
					"the other one", bsp, shadow.By, userGame, record.Archive.File))
		}
	}
	return plan, nil
}

// installID names an installation by where it is and what it is, so installing
// the same package in the same place twice is one installation.
func installID(kind Kind, root, game, archive, digest string) string {
	sum := sha256.Sum256([]byte(string(kind) + "\x00" + root + "\x00" + game + "\x00" + archive + "\x00" + digest))
	stem := strings.TrimSuffix(archive, filepath.Ext(archive))
	return stem + "-" + string(kind) + "-" + hex.EncodeToString(sum[:6])
}

// Install performs an install. A failure, or a cancelled context, leaves
// nothing behind: what this call wrote is removed before it returns.
//
// A managed installation is built as a generation of its own, beside where it
// will be, and is given its name only once the archive and every bound package
// in it have been checked. So an install that fails or is cancelled leaves the
// installation that was there before exactly as it was — still listed, still
// runnable — and one that succeeds replaces it in one rename.
func Install(ctx context.Context, request Request) (*Installation, error) {
	plan, err := resolve(request)
	if err != nil {
		return nil, err
	}
	installation := plan.installation
	if plan.already != nil && (request.Kind == GameFolder || samePackages(plan.already.Packages, installation.Packages)) {
		// The same package in the same place. Checked rather than trusted: the
		// archive, and each bound package, is re-hashed where it is.
		if err := Verify(plan.already); err == nil {
			if request.Kind == GameFolder && !samePackages(plan.already.Packages, installation.Packages) {
				// A game-folder install places no bound package; what it SAYS
				// about them follows what is in the game folder now.
				plan.already.Packages = installation.Packages
				plan.already.SuppliedByPackages = installation.SuppliedByPackages
				if err := save(plan.already.Directory, plan.already); err != nil {
					return nil, err
				}
			}
			return plan.already, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, failure.As(failure.Cancelled, err)
	}
	now := time.Now
	if request.Now != nil {
		now = request.Now
	}
	if err := os.MkdirAll(request.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("q3install: %w", err)
	}
	sweep(request.Dir, now())
	if request.Kind == Managed {
		return installManaged(ctx, request, plan, now)
	}

	if plan.already != nil {
		if err := Remove(request.Dir, plan.already.ID); err != nil {
			return nil, err
		}
	}
	undo := &undoLog{}
	fail := func(cause error) (*Installation, error) {
		if err := undo.run(); err != nil {
			return nil, fmt.Errorf("%w (and undoing it failed too: %v)", cause, err)
		}
		return nil, cause
	}
	if _, err := os.Lstat(installation.Directory); err == nil {
		// Left by an install that was interrupted before its record existed.
		// It is this program's own directory, named by this program's own id.
		if err := removeManaged(installation.Directory); err != nil {
			return nil, err
		}
	}
	if err := os.Mkdir(installation.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("q3install: %w", err)
	}
	undo.add(func() error { return removeManaged(installation.Directory) })

	if !plan.userGameExists {
		if err := os.Mkdir(plan.userGame, 0o755); err != nil {
			return fail(fmt.Errorf("q3install: creating %s: %w", plan.userGame, err))
		}
		installation.CreatedGameDir = true
		undo.add(func() error { return os.Remove(plan.userGame) })
	}
	if err := copyArchive(ctx, request.Package.ArchivePath, installation.Archive, undo); err != nil {
		return fail(err)
	}
	if request.afterCopy != nil {
		request.afterCopy()
	}
	if err := ctx.Err(); err != nil {
		return fail(failure.As(failure.Cancelled, fmt.Errorf("q3install: the install was cancelled: %w", err)))
	}
	installation.InstalledAt = now().UTC()
	if err := save(installation.Directory, installation); err != nil {
		return fail(err)
	}
	return installation, nil
}

// installManaged builds a managed installation as a generation beside its
// place, verifies it, and publishes it by renaming.
func installManaged(ctx context.Context, request Request, plan *planned, now func() time.Time) (*Installation, error) {
	installation := plan.installation
	final := installation.Directory
	if info, err := os.Lstat(final); err == nil && (info.Mode()&fs.ModeSymlink != 0 || !info.IsDir()) {
		return nil, fmt.Errorf("q3install: %s is not a directory this program made, and was left alone", final)
	}
	building, err := os.MkdirTemp(request.Dir, buildingPrefix)
	if err != nil {
		return nil, fmt.Errorf("q3install: %w", err)
	}
	undo := &undoLog{}
	undo.add(func() error { return removeManaged(building) })
	fail := func(cause error) (*Installation, error) {
		if err := undo.run(); err != nil {
			return nil, fmt.Errorf("%w (and undoing it failed too: %v)", cause, err)
		}
		return nil, cause
	}

	// Everything is built under the generation's own name and recorded under
	// the name it will have.
	rebase(installation, final, building)
	if err := buildManaged(ctx, installation, plan, undo); err != nil {
		return fail(err)
	}
	if err := stagePackages(ctx, installation, undo); err != nil {
		return fail(err)
	}
	if err := copyArchive(ctx, request.Package.ArchivePath, installation.Archive, undo); err != nil {
		return fail(err)
	}
	if request.afterCopy != nil {
		request.afterCopy()
	}
	if err := ctx.Err(); err != nil {
		return fail(failure.As(failure.Cancelled, fmt.Errorf("q3install: the install was cancelled: %w", err)))
	}
	// Checked where it is, once more, as the engine will find it.
	if err := Verify(installation); err != nil {
		return fail(err)
	}
	rebase(installation, building, final)
	installation.InstalledAt = now().UTC()
	if err := save(building, installation); err != nil {
		return fail(err)
	}

	// Published. The generation that was there is moved aside first and put
	// back if the new one cannot take its name.
	retired := ""
	if _, err := os.Lstat(final); err == nil {
		retired = filepath.Join(request.Dir, retiredPrefix+filepath.Base(building)[len(buildingPrefix):])
		if err := os.Rename(final, retired); err != nil {
			return fail(fmt.Errorf("q3install: replacing the installation that is there: %w", err))
		}
	}
	if err := os.Rename(building, final); err != nil {
		if retired != "" {
			if backErr := os.Rename(retired, final); backErr != nil {
				return fail(fmt.Errorf("q3install: publishing the installation: %w (and the previous one is now at %s: %v)",
					err, retired, backErr))
			}
		}
		return fail(fmt.Errorf("q3install: publishing the installation: %w", err))
	}
	if retired != "" {
		// Its files are this program's own. Where something still holds them
		// open and the system will not let them go, the next install's sweep
		// takes them.
		_ = removeManaged(retired)
	}
	return installation, nil
}

// rebase rewrites every path an installation records under one directory to
// the same path under another.
func rebase(installation *Installation, from, to string) {
	move := func(path string) string {
		if path == from {
			return to
		}
		if rest, inside := strings.CutPrefix(path, from+string(filepath.Separator)); inside {
			return filepath.Join(to, rest)
		}
		return path
	}
	installation.Directory = move(installation.Directory)
	installation.BasePath = move(installation.BasePath)
	installation.Archive.Path = move(installation.Archive.Path)
	for i := range installation.Packages {
		installation.Packages[i].Path = move(installation.Packages[i].Path)
	}
}

// buildManaged lays out the managed base directory. The game directory the
// archive goes into, and any other a bound package is staged in, is a real
// directory holding one link per entry of the user's own; every other game
// directory is one link.
func buildManaged(ctx context.Context, installation *Installation, plan *planned, undo *undoLog) error {
	base := installation.BasePath
	real := []string{installation.Game}
	for _, pkg := range installation.Packages {
		if pkg.State != PackageStaged {
			continue
		}
		listed := false
		for _, name := range real {
			listed = listed || strings.EqualFold(name, pkg.Game)
		}
		if !listed {
			real = append(real, pkg.Game)
		}
	}
	isReal := func(name string) bool {
		for _, game := range real {
			if strings.EqualFold(name, game) {
				return true
			}
		}
		return false
	}
	for _, game := range real {
		if err := os.MkdirAll(filepath.Join(base, game), 0o700); err != nil {
			return fmt.Errorf("q3install: %w", err)
		}
	}
	// The other game directories of the user's game folder: the base game when
	// the archive goes into a mod, and any mod beside it. One link each.
	entries, err := os.ReadDir(installation.GameRoot)
	if err != nil {
		return failure.As(failure.GameDataMissing, fmt.Errorf("q3install: reading the game folder: %w", err))
	}
	if len(entries) > maxEntries {
		return fmt.Errorf("q3install: the game folder holds more than %d entries", maxEntries)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return failure.As(failure.Cancelled, err)
		}
		name := entry.Name()
		source := filepath.Join(installation.GameRoot, name)
		info, err := os.Stat(source)
		if err != nil || !info.IsDir() || strings.HasPrefix(name, ".") || isReal(name) {
			continue
		}
		link := filepath.Join(base, name)
		if err := joincontent.LinkDir(source, link); err != nil {
			return fmt.Errorf("q3install: linking %s: %w", name, err)
		}
		installation.Links = append(installation.Links, Link{Name: name, Target: source, Method: "junction-or-symlink"})
	}
	for _, game := range real {
		if err := linkGame(ctx, installation, game); err != nil {
			return err
		}
	}
	sort.Slice(installation.Links, func(i, j int) bool { return installation.Links[i].Name < installation.Links[j].Name })
	return nil
}

// linkGame fills one real game directory of a managed base with a link per
// entry of the user's own directory of that name, so what this install adds
// can sit beside what is there without being written into it. A directory the
// user does not have stays empty.
func linkGame(ctx context.Context, installation *Installation, game string) error {
	userGame := filepath.Join(installation.GameRoot, game)
	if info, err := os.Stat(userGame); err != nil || !info.IsDir() {
		return nil
	}
	target := filepath.Join(installation.BasePath, game)
	inside, err := os.ReadDir(userGame)
	if err != nil {
		return failure.As(failure.GameDataMissing, fmt.Errorf("q3install: reading %s: %w", userGame, err))
	}
	if len(inside) > maxEntries {
		return fmt.Errorf("q3install: %s holds more than %d entries", userGame, maxEntries)
	}
	for _, entry := range inside {
		if err := ctx.Err(); err != nil {
			return failure.As(failure.Cancelled, err)
		}
		name := entry.Name()
		source := filepath.Join(userGame, name)
		info, err := os.Stat(source)
		if err != nil {
			continue // a link to something that is not there
		}
		link := filepath.Join(target, name)
		relative := filepath.ToSlash(filepath.Join(game, name))
		if info.IsDir() {
			if err := joincontent.LinkDir(source, link); err != nil {
				return fmt.Errorf("q3install: linking %s: %w", relative, err)
			}
			installation.Links = append(installation.Links, Link{Name: relative, Target: source, Method: "junction-or-symlink"})
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		method := "symlink"
		if err := os.Symlink(source, link); err != nil {
			// Never a copy. The user's game data is theirs, a copy of
			// `pak0.pk3` per install is half a gigabyte each time, and
			// "the Companion copied my game" is a sentence that must not be
			// possible. A filesystem that can make neither link gets told so.
			if linkErr := os.Link(source, link); linkErr != nil {
				return fmt.Errorf("q3install: this machine can make neither a symbolic link nor a hard link "+
					"to %s (%v; %v), and the Companion does not copy your game data. Install into a mod "+
					"directory, or into the game folder itself", source, err, linkErr)
			}
			method = "hardlink"
		}
		installation.Links = append(installation.Links, Link{Name: relative, Target: source, Method: method})
	}
	return nil
}

// copyArchive writes the archive under a temporary name beside its
// destination, checks what it wrote against the package's digest, and only
// then gives it its name — without replacing anything.
func copyArchive(ctx context.Context, source string, archive Archive, undo *undoLog) error {
	return copyVerified(ctx, source, archive, undo, func(written int64, digest string) error {
		return fmt.Errorf("q3install: the package's archive changed while it was being installed: %d bytes, "+
			"sha256 %s, and the package is %d bytes, sha256 %s", written, digest, archive.Size, archive.SHA256)
	})
}

// copyVerified is the one way this package writes a file an engine will read:
// under a temporary name, hashed as it is written, named only when the bytes
// are the ones expected, and never over anything.
func copyVerified(ctx context.Context, source string, archive Archive, undo *undoLog,
	mismatch func(written int64, digest string) error,
) error {
	dir := filepath.Dir(archive.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("q3install: %w", err)
	}
	staged, err := os.CreateTemp(dir, "."+archive.Name+".installing-*")
	if err != nil {
		return fmt.Errorf("q3install: writing into %s: %w", dir, err)
	}
	stagedPath := staged.Name()
	undo.add(func() error {
		if err := os.Remove(stagedPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
	from, err := os.Open(source)
	if err != nil {
		staged.Close()
		return fmt.Errorf("q3install: reading the package: %w", err)
	}
	defer from.Close()
	hash := sha256.New()
	written, err := copyContext(ctx, io.MultiWriter(staged, hash), from)
	if closeErr := staged.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		if ctx.Err() != nil {
			return failure.As(failure.Cancelled, fmt.Errorf("q3install: the install was cancelled: %w", ctx.Err()))
		}
		return fmt.Errorf("q3install: writing %s: %w", archive.Name, err)
	}
	if digest := hex.EncodeToString(hash.Sum(nil)); written != archive.Size || digest != archive.SHA256 {
		return mismatch(written, digest)
	}
	if err := os.Chmod(stagedPath, 0o644); err != nil {
		return fmt.Errorf("q3install: %w", err)
	}
	// A hard link and not a rename: link fails when the name is taken, where a
	// rename would replace whatever took it between the check and now.
	if err := os.Link(stagedPath, archive.Path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return failure.As(failure.InstallConflict, fmt.Errorf(
				"q3install: %s appeared while the install was running. It was not written over", archive.Path))
		}
		// A filesystem without hard links: the name was checked a moment ago,
		// and this is the fallback that cannot check it again.
		if _, statErr := os.Lstat(archive.Path); statErr == nil {
			return failure.As(failure.InstallConflict, fmt.Errorf(
				"q3install: %s appeared while the install was running. It was not written over", archive.Path))
		}
		if err := os.Rename(stagedPath, archive.Path); err != nil {
			return fmt.Errorf("q3install: placing %s: %w", archive.Name, err)
		}
	} else if err := os.Remove(stagedPath); err != nil {
		return fmt.Errorf("q3install: %w", err)
	}
	undo.add(func() error { return removeIfOurs(archive) })
	return nil
}

// copyContext is io.Copy that stops when its context is cancelled.
func copyContext(ctx context.Context, to io.Writer, from io.Reader) (int64, error) {
	return io.Copy(to, &contextReader{ctx: ctx, reader: from})
}

// contextReader stops a copy when its context is cancelled.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// undoLog is what an install wrote, removed in reverse order on failure.
type undoLog struct{ steps []func() error }

func (u *undoLog) add(step func() error) { u.steps = append(u.steps, step) }

func (u *undoLog) run() error {
	var first error
	for i := len(u.steps) - 1; i >= 0; i-- {
		if err := u.steps[i](); err != nil && first == nil && !errors.Is(err, fs.ErrNotExist) {
			first = err
		}
	}
	return first
}

// removeIfOurs removes an installed archive only while it is still the bytes
// this program installed. A file somebody replaced since is theirs.
func removeIfOurs(archive Archive) error {
	info, err := os.Lstat(archive.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("q3install: %s is no longer a regular file, and was left alone", archive.Path)
	}
	digest, _, err := hashFile(archive.Path)
	if err != nil {
		return err
	}
	if digest != archive.SHA256 {
		return fmt.Errorf("q3install: %s has changed since it was installed, and was left alone", archive.Path)
	}
	return os.Remove(archive.Path)
}

// removeManaged removes an installation's own directory.
//
// Everything in a managed base directory is a link or the archive, and
// `os.RemoveAll` removes a link without descending it — it never follows one
// into the user's game. The directory's name is checked first all the same:
// this is the one recursive removal in the package.
func removeManaged(dir string) error {
	if dir == "" || filepath.Base(dir) == "." || filepath.Dir(dir) == dir {
		return fmt.Errorf("q3install: refusing to remove %q", dir)
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("q3install: %s is not a directory this program made, and was left alone", dir)
	}
	return os.RemoveAll(dir)
}

// save writes an installation's record into dir — the installation's own
// directory, or the generation that is about to become it.
func save(dir string, installation *Installation) error {
	data, err := json.MarshalIndent(installation, "", "  ")
	if err != nil {
		return fmt.Errorf("q3install: %w", err)
	}
	path := filepath.Join(dir, RecordFileName)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("q3install: recording the install: %w", err)
	}
	return nil
}

// Load reads one installation's record.
func Load(dir, id string) (*Installation, error) {
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return nil, fmt.Errorf("q3install: %q is not an installation id", id)
	}
	directory := filepath.Join(dir, id)
	data, err := os.ReadFile(filepath.Join(directory, RecordFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		return nil, fmt.Errorf("q3install: %w", err)
	}
	var installation Installation
	if err := json.Unmarshal(data, &installation); err != nil {
		return nil, fmt.Errorf("q3install: the record of %s does not parse: %w", id, err)
	}
	if installation.SchemaVersion != SchemaVersion && installation.SchemaVersion != schemaVersion10 {
		return nil, fmt.Errorf("q3install: the record of %s is %q, and this build reads %q and %q",
			id, installation.SchemaVersion, SchemaVersion, schemaVersion10)
	}
	installation.Directory = directory
	return &installation, nil
}

// List reads every installation, newest first.
func List(dir string) ([]*Installation, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("q3install: %w", err)
	}
	var out []*Installation
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if installation, err := Load(dir, entry.Name()); err == nil {
			out = append(out, installation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstalledAt.After(out[j].InstalledAt) })
	return out, nil
}

// Verify re-checks an installation immediately before an engine is started on
// it: the archive is where it was put and is still the bytes that were put, and
// so is every bound package the engine is about to read.
func Verify(installation *Installation) error {
	info, err := os.Lstat(installation.Archive.Path)
	if err != nil {
		return fmt.Errorf("q3install: %s is no longer where it was installed: %w", installation.Archive.Name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("q3install: %s is no longer a regular file", installation.Archive.Path)
	}
	digest, size, err := hashFile(installation.Archive.Path)
	if err != nil {
		return fmt.Errorf("q3install: %w", err)
	}
	if size != installation.Archive.Size || digest != installation.Archive.SHA256 {
		return fmt.Errorf("q3install: %s is not the archive that was installed (it is %d bytes, sha256 %s, and "+
			"%d bytes, sha256 %s were installed). Install the package again",
			installation.Archive.Path, size, digest, installation.Archive.Size, installation.Archive.SHA256)
	}
	return verifyPackages(installation)
}

// Remove takes an installation away: a managed one's whole directory, or the
// one file a game-folder install wrote — and that only while it is still the
// file that was written.
func Remove(dir, id string) error {
	installation, err := Load(dir, id)
	if err != nil {
		return err
	}
	if installation.Kind == GameFolder {
		if err := removeIfOurs(installation.Archive); err != nil {
			return err
		}
		if installation.CreatedGameDir {
			// Only if it is empty: os.Remove refuses a directory that holds
			// anything, which is exactly the rule.
			_ = os.Remove(filepath.Dir(installation.Archive.Path))
		}
	}
	return removeManaged(installation.Directory)
}

// archiveMembers lists the names in a PK3's central directory. Nothing is
// decompressed.
func archiveMembers(path string) ([]string, bool, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, false, err
	}
	defer reader.Close()
	var names []string
	for i, file := range reader.File {
		if i >= maxArchiveMembers {
			return names, true, nil
		}
		if strings.HasSuffix(file.Name, "/") {
			continue
		}
		names = append(names, strings.ReplaceAll(file.Name, `\`, "/"))
	}
	return names, false, nil
}

func hexDigest(digest string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(digest), "sha256:"))
}

func hashFile(name string) (string, int64, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
