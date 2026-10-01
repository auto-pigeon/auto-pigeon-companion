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
// directory this program made.
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
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// SchemaVersion versions [Installation].
const SchemaVersion = "aucom.q3-install/1.0"

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
	// BaseGame is the base game directory's name. Empty means the package's.
	BaseGame string
	// Game is the directory the archive goes into. Empty means the package's
	// own: the mod the map was built for, or the base game.
	Game string
	// Dir is where installations are recorded, and where managed ones live.
	Dir string
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
	Links     []Link    `json:"links,omitempty"`
	LoadOrder LoadOrder `json:"load_order"`
	// Complete is the package's own: false when it lacks something an engine
	// needs. NotCarried is what.
	Complete    bool      `json:"complete"`
	NotCarried  []string  `json:"not_carried,omitempty"`
	InstalledAt time.Time `json:"installed_at"`

	// Directory is where this record is.
	Directory string `json:"directory,omitempty"`
}

// ErrNotInstalled reports an installation that is not there.
var ErrNotInstalled = errors.New("q3install: no such installation")

// Preview says where a package would be installed and what would be searched
// before it, and writes nothing. It refuses exactly what [Install] refuses.
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

	order, err := loadOrder(record, userGame, userGameExists)
	if err != nil {
		return nil, err
	}
	installation.LoadOrder = order
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
func Install(ctx context.Context, request Request) (*Installation, error) {
	plan, err := resolve(request)
	if err != nil {
		return nil, err
	}
	installation := plan.installation
	if plan.already != nil {
		// The same package in the same place. Checked rather than trusted: the
		// archive is re-hashed where it is.
		if err := Verify(plan.already); err == nil {
			return plan.already, nil
		}
		if err := Remove(request.Dir, plan.already.ID); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, failure.As(failure.Cancelled, err)
	}
	now := time.Now
	if request.Now != nil {
		now = request.Now
	}

	undo := &undoLog{}
	fail := func(cause error) (*Installation, error) {
		if err := undo.run(); err != nil {
			return nil, fmt.Errorf("%w (and undoing it failed too: %v)", cause, err)
		}
		return nil, cause
	}

	if err := os.MkdirAll(request.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("q3install: %w", err)
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

	switch request.Kind {
	case Managed:
		if err := buildManaged(ctx, installation, plan, undo); err != nil {
			return fail(err)
		}
	default:
		if !plan.userGameExists {
			if err := os.Mkdir(plan.userGame, 0o755); err != nil {
				return fail(fmt.Errorf("q3install: creating %s: %w", plan.userGame, err))
			}
			installation.CreatedGameDir = true
			undo.add(func() error { return os.Remove(plan.userGame) })
		}
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
	if err := save(installation); err != nil {
		return fail(err)
	}
	return installation, nil
}

// buildManaged lays out the managed base directory: the target game directory
// as a real directory of links, and every other game directory as one link.
func buildManaged(ctx context.Context, installation *Installation, plan *planned, undo *undoLog) error {
	base := installation.BasePath
	target := filepath.Join(base, installation.Game)
	if err := os.MkdirAll(target, 0o700); err != nil {
		return fmt.Errorf("q3install: %w", err)
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
		if err != nil || !info.IsDir() || strings.HasPrefix(name, ".") || strings.EqualFold(name, installation.Game) {
			continue
		}
		link := filepath.Join(base, name)
		if err := joincontent.LinkDir(source, link); err != nil {
			return fmt.Errorf("q3install: linking %s: %w", name, err)
		}
		installation.Links = append(installation.Links, Link{Name: name, Target: source, Method: "junction-or-symlink"})
	}
	if !plan.userGameExists {
		return nil
	}
	// The user's own directory of the target game, one link per entry, so the
	// archive can sit beside what is there without being written into it.
	inside, err := os.ReadDir(plan.userGame)
	if err != nil {
		return failure.As(failure.GameDataMissing, fmt.Errorf("q3install: reading %s: %w", plan.userGame, err))
	}
	if len(inside) > maxEntries {
		return fmt.Errorf("q3install: %s holds more than %d entries", plan.userGame, maxEntries)
	}
	for _, entry := range inside {
		if err := ctx.Err(); err != nil {
			return failure.As(failure.Cancelled, err)
		}
		name := entry.Name()
		source := filepath.Join(plan.userGame, name)
		info, err := os.Stat(source)
		if err != nil {
			continue // a link to something that is not there
		}
		link := filepath.Join(target, name)
		relative := filepath.ToSlash(filepath.Join(installation.Game, name))
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
	sort.Slice(installation.Links, func(i, j int) bool { return installation.Links[i].Name < installation.Links[j].Name })
	return nil
}

// copyArchive writes the archive under a temporary name beside its
// destination, checks what it wrote against the package's digest, and only
// then gives it its name — without replacing anything.
func copyArchive(ctx context.Context, source string, archive Archive, undo *undoLog) error {
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
	written, err := io.Copy(io.MultiWriter(staged, hash), &contextReader{ctx: ctx, reader: from})
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
		return fmt.Errorf("q3install: the package's archive changed while it was being installed: %d bytes, "+
			"sha256 %s, and the package is %d bytes, sha256 %s", written, digest, archive.Size, archive.SHA256)
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

func save(installation *Installation) error {
	data, err := json.MarshalIndent(installation, "", "  ")
	if err != nil {
		return fmt.Errorf("q3install: %w", err)
	}
	path := filepath.Join(installation.Directory, RecordFileName)
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
	if installation.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("q3install: the record of %s is %q, and this build reads %q",
			id, installation.SchemaVersion, SchemaVersion)
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
// it: the archive is where it was put and is still the bytes that were put.
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
	return nil
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

// loadOrder looks each member of the package up in what the engine searches
// before, and after, the package's archive in its game directory.
func loadOrder(record *q3pack.Record, userGame string, exists bool) (LoadOrder, error) {
	order := LoadOrder{Limits: []string{
		"the engine's own home directory (`~/.q3a` on Linux): an engine searches it before the game folder, " +
			"and what is in it is the player's configuration, not something an install reads",
	}}
	if !exists {
		return order, nil
	}
	members := map[string]string{}
	for _, member := range record.Plan.Members {
		members[strings.ToLower(member.Path)] = member.Path
	}

	entries, err := os.ReadDir(userGame)
	if err != nil {
		return order, failure.As(failure.GameDataMissing, fmt.Errorf("q3install: reading %s: %w", userGame, err))
	}
	var archives []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.EqualFold(filepath.Ext(name), ".pk3") && !strings.EqualFold(name, record.Archive.File) {
			if info, err := os.Stat(filepath.Join(userGame, name)); err == nil && info.Mode().IsRegular() {
				archives = append(archives, name)
			}
		}
	}
	if len(archives) > maxArchives {
		return order, fmt.Errorf("q3install: %s holds more than %d PK3 archives", userGame, maxArchives)
	}
	// Later name first, without regard to case: the engine's own order.
	sort.Slice(archives, func(i, j int) bool { return strings.ToLower(archives[i]) > strings.ToLower(archives[j]) })

	// Loose files beat every archive.
	order.Searched = append(order.Searched, "loose files in "+filepath.Base(userGame))
	loose := 0
	walkErr := filepath.WalkDir(userGame, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		loose++
		if loose > maxLooseFiles {
			order.Limits = append(order.Limits, fmt.Sprintf(
				"the loose files of %s past the first %d", userGame, maxLooseFiles))
			return filepath.SkipAll
		}
		relative, relErr := filepath.Rel(userGame, path)
		if relErr != nil {
			return nil
		}
		if member, ours := members[strings.ToLower(filepath.ToSlash(relative))]; ours {
			order.Shadowed = append(order.Shadowed, Shadow{Path: member, By: "the loose file " + filepath.ToSlash(relative)})
		}
		return nil
	})
	if walkErr != nil {
		return order, fmt.Errorf("q3install: reading %s: %w", userGame, walkErr)
	}

	ours := strings.ToLower(record.Archive.File)
	shadowed := map[string]bool{}
	for _, shadow := range order.Shadowed {
		shadowed[shadow.Path] = true
	}
	for _, name := range archives {
		before := strings.ToLower(name) > ours
		if before {
			order.Searched = append(order.Searched, name)
		}
		names, truncated, err := archiveMembers(filepath.Join(userGame, name))
		if err != nil {
			order.Limits = append(order.Limits, fmt.Sprintf("%s, which could not be read as an archive (%v)", name, err))
			continue
		}
		if truncated {
			order.Limits = append(order.Limits, fmt.Sprintf("the members of %s past the first %d", name, maxArchiveMembers))
		}
		for _, inside := range names {
			member, isOurs := members[strings.ToLower(inside)]
			if !isOurs {
				continue
			}
			if before {
				if !shadowed[member] {
					shadowed[member] = true
					order.Shadowed = append(order.Shadowed, Shadow{Path: member, By: name})
				}
				continue
			}
			order.Overrides = append(order.Overrides, Shadow{Path: member, By: name})
		}
	}
	sort.Slice(order.Shadowed, func(i, j int) bool { return order.Shadowed[i].Path < order.Shadowed[j].Path })
	sort.Slice(order.Overrides, func(i, j int) bool { return order.Overrides[i].Path < order.Overrides[j].Path })
	return order, nil
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
