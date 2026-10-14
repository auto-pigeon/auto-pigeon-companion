// Package q2install puts a Quake II map package where an engine will load it:
// one mod folder of the Companion's own, beside `baseq2`, holding the package
// and nothing else.
//
// # What it never does
//
// It never writes into `baseq2` or any other folder it did not create, never
// replaces a file of the user's, and never copies game data: the base game is
// read where it is. An install of one map never touches another map's folder —
// each map name has a mod folder of its own unless told otherwise.
//
// # How a folder is replaced
//
// A new install is written as a hidden generation beside its place, the
// archive in it is hashed and read back against the package's record, and only
// then is it renamed into place. The installation it replaces is moved aside
// first and put back if the rename fails, so a failed or cancelled install —
// or one whose archive turned out damaged — leaves the previous installation
// where it was, listed and runnable. One install at a time touches a game
// folder: a lock in the Companion's own state serialises them, so a second job
// can neither see half of the first's folder nor rename over it.
package q2install

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2pack"
)

// SchemaVersion versions [Installation].
const SchemaVersion = "aucom.q2-install/1.0"

// StampName is the file in a mod folder that says the Companion made it, and
// which installation it is. An engine reads `.pak` files and ignores it.
const StampName = ".aucom-q2-install.json"

// ErrNotInstalled reports an installation id with no record.
var ErrNotInstalled = errors.New("q2install: no such installation")

// Request is one install.
type Request struct {
	Package *q2pack.Record
	// EngineProfileID is recorded, so a run knows which engine it was for.
	EngineProfileID string
	// GameRoot is the engine's game folder: the one that holds `baseq2`.
	GameRoot string
	// Mod is the mod folder's name. Empty means the package's own default.
	Mod string
	// Dir is where installation records and locks are kept.
	Dir string

	// staged, when set, is called after the generation is written and
	// verified and before it is published: what a test uses to fail or
	// cancel an install at its last moment.
	staged func() error
}

// Archive is the installed package file.
type Archive struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Installation is one package in one engine's game folder.
type Installation struct {
	SchemaVersion   string    `json:"schema_version"`
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	PackageID       string    `json:"package_id"`
	EngineProfileID string    `json:"engine_profile_id,omitempty"`
	InstalledAt     time.Time `json:"installed_at"`

	GameRoot string `json:"game_root"`
	BaseGame string `json:"base_game"`
	// FSGame is the mod folder's name — what the engine is told with
	// `+set game` — and ModDir where it is.
	FSGame string `json:"fs_game"`
	ModDir string `json:"mod_dir"`
	// BasePath is the folder the engine is given as its data folder.
	BasePath string  `json:"base_path"`
	Archive  Archive `json:"archive"`
	MapName  string  `json:"map_name"`
	// MapMember is the BSP as the installed archive holds it, read back.
	MapMember pack.Entry         `json:"map_member"`
	Map       q2pack.MapIdentity `json:"map"`
	Members   int                `json:"members"`

	Complete   bool     `json:"complete"`
	NotCarried []string `json:"not_carried,omitempty"`
	// LoadOrder is what the engine reads before and after this folder.
	LoadOrder LoadOrder `json:"load_order"`
	// Replaced is the package this install took the place of, if any.
	Replaced string `json:"replaced,omitempty"`
}

// Shadow is one path two places supply.
type Shadow struct {
	Path string `json:"path"`
	By   string `json:"by"`
}

// LoadOrder says, in the engine's own terms, what this folder wins over.
type LoadOrder struct {
	// SearchedBefore is what the engine reads ahead of the mod folder that this
	// install knows of. Its own folders are named in Limits.
	SearchedBefore []string `json:"searched_before"`
	Shadowed       []Shadow `json:"shadowed,omitempty"`
	// Overrides are the package's members the base game also has; the
	// package's copy is the one loaded, because a mod is searched first.
	Overrides []Shadow `json:"overrides,omitempty"`
	Limits    []string `json:"limits,omitempty"`
}

// Preview says where an install would go and what it would replace, and
// writes nothing.
func Preview(request Request) (*Installation, error) {
	installation, _, err := plan(request)
	return installation, err
}

func plan(request Request) (*Installation, string, error) {
	record := request.Package
	if record == nil || record.Plan == nil {
		return nil, "", errors.New("q2install: no package")
	}
	if strings.TrimSpace(request.GameRoot) == "" {
		return nil, "", failure.As(failure.GameDataMissing, errors.New(
			"q2install: this engine has no game folder set on this machine"))
	}
	root, err := filepath.Abs(request.GameRoot)
	if err != nil {
		return nil, "", err
	}
	if info, err := os.Stat(filepath.Join(root, q2pack.BaseGameDir)); err != nil || !info.IsDir() {
		return nil, "", failure.As(failure.GameDataMissing, fmt.Errorf(
			"q2install: %s holds no %s folder, so it is not a Quake II game folder", root, q2pack.BaseGameDir))
	}
	mod := strings.TrimSpace(request.Mod)
	if mod == "" {
		mod = record.Plan.GameDir
	}
	// `baseq2` and the mission packs' folders are refused there: a package
	// is never installed into a folder the game owns.
	if err := engine.CheckModName(mod); err != nil {
		return nil, "", failure.As(failure.FSGameInvalid, err)
	}
	// A folder of another spelling is the same folder to an engine that
	// lower-cases what it cannot find, and a different one to this filesystem.
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if entry.Name() != mod && strings.EqualFold(entry.Name(), mod) {
				return nil, "", failure.As(failure.InstallConflict, fmt.Errorf(
					"q2install: %s already holds %s, which differs from %q only in capitalisation. Choose another name",
					root, entry.Name(), mod))
			}
		}
	}
	modDir := filepath.Join(root, mod)
	previous := ""
	if info, err := os.Lstat(modDir); err == nil {
		if !info.IsDir() {
			return nil, "", failure.As(failure.InstallConflict, fmt.Errorf("q2install: %s is in the way and is not a folder", modDir))
		}
		stamp, err := readStamp(modDir)
		if err != nil {
			return nil, "", failure.As(failure.InstallConflict, fmt.Errorf(
				"q2install: %s already exists and the Companion did not make it; it is left alone. Choose another name", modDir))
		}
		previous = stamp.PackageID
	}
	mapMember := "maps/" + record.Plan.Map.Name + ".bsp"
	member, ok := record.Member(mapMember)
	if !ok {
		return nil, "", failure.As(failure.ArchiveDamaged, fmt.Errorf("q2install: the package holds no %s", mapMember))
	}
	installation := &Installation{
		SchemaVersion: SchemaVersion, ID: InstallationID(root, mod), Kind: "managed",
		PackageID: record.ID, EngineProfileID: request.EngineProfileID,
		GameRoot: root, BaseGame: q2pack.BaseGameDir, FSGame: mod, ModDir: modDir, BasePath: root,
		Archive: Archive{Name: record.Archive.File, Path: filepath.Join(modDir, record.Archive.File), SHA256: record.Archive.SHA256, Size: record.Archive.Size},
		MapName: record.Plan.Map.Name, MapMember: member, Map: record.Plan.Map, Members: len(record.Entries),
		Complete: record.Complete, NotCarried: record.Plan.NotCarried, Replaced: previous,
	}
	// The mod folder is searched before baseq2, so nothing this install knows
	// of comes ahead of it. What can — a folder of the same name beside the
	// engine's program, or in its own settings folder — is outside it, and is
	// what a run's check of the engine's own word is for (measured, Q2_009).
	installation.LoadOrder = LoadOrder{
		SearchedBefore: []string{},
		Limits: []string{
			"the engine also reads a folder of this name beside its own program and in its own settings folder, ahead of this one; a run checks the engine's own word for which file it opened",
		},
	}
	for _, path := range record.Plan.Overrides {
		installation.LoadOrder.Overrides = append(installation.LoadOrder.Overrides, Shadow{Path: path, By: q2pack.BaseGameDir})
	}
	return installation, previous, nil
}

// InstallationID names the installation of one mod folder in one game folder.
func InstallationID(gameRoot, mod string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(gameRoot)))
	return "q2i-" + strings.ToLower(mod) + "-" + hex.EncodeToString(sum[:])[:10]
}

// Install writes the package into its mod folder.
func Install(ctx context.Context, request Request) (*Installation, error) {
	if request.Dir == "" {
		return nil, errors.New("q2install: no place to keep installation records")
	}
	if err := os.MkdirAll(filepath.Join(request.Dir, "locks"), 0o700); err != nil {
		return nil, err
	}
	preview, _, err := plan(request)
	if err != nil {
		return nil, err
	}
	guard := filepath.Join(request.Dir, "locks", strings.TrimPrefix(InstallationID(preview.GameRoot, "game-folder"), "q2i-"))
	lock, err := lockfile.Acquire(guard, lockfile.Options{Timeout: 30 * time.Second, Program: "companion q2 install"})
	if err != nil {
		return nil, failure.As(failure.InstallConflict, fmt.Errorf("q2install: another install is writing into %s: %w", preview.GameRoot, err))
	}
	defer lock.Release()
	// Planned again under the lock: what was there before somebody else's
	// install finished is not what is there now.
	installation, _, err := plan(request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, failure.As(failure.Cancelled, err)
	}

	suffix, err := token()
	if err != nil {
		return nil, err
	}
	staging := filepath.Join(installation.GameRoot, "."+installation.FSGame+".installing-"+suffix)
	if err := os.Mkdir(staging, 0o755); err != nil {
		return nil, fmt.Errorf("q2install: preparing %s: %w", staging, err)
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(staging)
		}
	}()
	staged := filepath.Join(staging, installation.Archive.Name)
	if err := copyFile(ctx, request.Package.ArchivePath, staged); err != nil {
		if ctx.Err() != nil {
			return nil, failure.As(failure.Cancelled, fmt.Errorf("q2install: cancelled while copying the package: %w", ctx.Err()))
		}
		return nil, fmt.Errorf("q2install: copying the package: %w", err)
	}
	if err := verifyArchive(staged, installation); err != nil {
		return nil, err
	}
	if err := writeStamp(staging, installation); err != nil {
		return nil, err
	}
	if request.staged != nil {
		if err := request.staged(); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, failure.As(failure.Cancelled, fmt.Errorf("q2install: cancelled before the package was put in place: %w", err))
	}

	retired := ""
	if _, err := os.Lstat(installation.ModDir); err == nil {
		retired = filepath.Join(installation.GameRoot, "."+installation.FSGame+".retired-"+suffix)
		if err := os.Rename(installation.ModDir, retired); err != nil {
			return nil, fmt.Errorf("q2install: moving the previous installation aside: %w", err)
		}
	}
	if err := os.Rename(staging, installation.ModDir); err != nil {
		if retired != "" {
			if restore := os.Rename(retired, installation.ModDir); restore != nil {
				return nil, fmt.Errorf("q2install: putting the package in place: %v; and putting the previous one back: %v (it is at %s)",
					err, restore, retired)
			}
		}
		return nil, fmt.Errorf("q2install: putting the package in place: %w", err)
	}
	published = true
	if retired != "" {
		os.RemoveAll(retired)
	}
	installation.InstalledAt = time.Now().UTC()
	if err := save(request.Dir, installation); err != nil {
		return nil, err
	}
	return installation, nil
}

// Verify re-checks an installation immediately before an engine is started on
// it: the folder is still the Companion's, and the archive is still the bytes
// that were installed.
func Verify(installation *Installation) error {
	stamp, err := readStamp(installation.ModDir)
	if err != nil || stamp.ID != installation.ID || stamp.PackageID != installation.PackageID {
		return failure.As(failure.InstallConflict, fmt.Errorf(
			"q2install: %s is no longer the installation of %s. Install the package again", installation.ModDir, installation.PackageID))
	}
	return verifyArchive(installation.Archive.Path, installation)
}

func verifyArchive(path string, installation *Installation) error {
	digest, size, err := hashFile(path)
	if err != nil {
		return failure.As(failure.ArchiveDamaged, fmt.Errorf("q2install: the installed package: %w", err))
	}
	if size != installation.Archive.Size || !strings.EqualFold(digest, installation.Archive.SHA256) {
		return failure.As(failure.ArchiveDamaged, fmt.Errorf(
			"q2install: %s is %d bytes, %s, and the package is %d bytes, %s", filepath.Base(path), size, digest,
			installation.Archive.Size, installation.Archive.SHA256))
	}
	verification, err := pack.Verify(path, pack.FormatPAK, pack.Budget{})
	if err != nil {
		return failure.As(failure.ArchiveDamaged, fmt.Errorf("q2install: reading the installed package back: %w", err))
	}
	if len(verification.Problems) > 0 {
		return failure.As(failure.ArchiveDamaged, fmt.Errorf("q2install: the installed package did not read back: %s",
			strings.Join(verification.Problems, "; ")))
	}
	for _, entry := range verification.Entries {
		if strings.EqualFold(entry.Path, installation.MapMember.Path) {
			if !strings.EqualFold(entry.SHA256, installation.MapMember.SHA256) || entry.Size != installation.MapMember.Size {
				return failure.As(failure.ArchiveDamaged, fmt.Errorf("q2install: the installed %s is not the package's", entry.Path))
			}
			return nil
		}
	}
	return failure.As(failure.ArchiveDamaged, fmt.Errorf("q2install: the installed package holds no %s", installation.MapMember.Path))
}

type stamp struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	PackageID     string `json:"package_id"`
	Archive       string `json:"archive"`
	SHA256        string `json:"sha256"`
}

func writeStamp(dir string, installation *Installation) error {
	data, err := json.MarshalIndent(stamp{SchemaVersion: SchemaVersion, ID: installation.ID, PackageID: installation.PackageID,
		Archive: installation.Archive.Name, SHA256: installation.Archive.SHA256}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, StampName), append(data, '\n'), 0o644)
}

func readStamp(dir string) (stamp, error) {
	var out stamp
	data, err := os.ReadFile(filepath.Join(dir, StampName))
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	if out.SchemaVersion != SchemaVersion || out.ID == "" {
		return out, errors.New("q2install: not an installation stamp this build reads")
	}
	return out, nil
}

func save(dir string, installation *Installation) error {
	data, err := json.MarshalIndent(installation, "", "  ")
	if err != nil {
		return err
	}
	temp := filepath.Join(dir, "."+installation.ID+".json.writing")
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(dir, installation.ID+".json"))
}

// Load reads one installation.
func Load(dir, id string) (*Installation, error) {
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return nil, fmt.Errorf("q2install: %q is not an installation id", id)
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotInstalled, id)
	}
	if err != nil {
		return nil, err
	}
	var installation Installation
	if err := json.Unmarshal(data, &installation); err != nil {
		return nil, fmt.Errorf("q2install: the record of %s does not parse: %w", id, err)
	}
	if installation.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("q2install: the record of %s is %q, and this build reads %q", id, installation.SchemaVersion, SchemaVersion)
	}
	return &installation, nil
}

// List reads every installation, newest first.
func List(dir string) ([]*Installation, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Installation
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if installation, err := Load(dir, strings.TrimSuffix(name, ".json")); err == nil {
			out = append(out, installation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstalledAt.After(out[j].InstalledAt) })
	return out, nil
}

// Remove takes an installation out of the game folder: the mod folder, only
// while it is still the Companion's and still holds the installed bytes. A
// folder somebody has put anything else in is left, and said so.
func Remove(dir, id string) error {
	installation, err := Load(dir, id)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(installation.ModDir); errors.Is(err, os.ErrNotExist) {
		return os.Remove(filepath.Join(dir, id+".json"))
	}
	if err := Verify(installation); err != nil {
		return err
	}
	entries, err := os.ReadDir(installation.ModDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != StampName && entry.Name() != installation.Archive.Name {
			return failure.As(failure.InstallConflict, fmt.Errorf(
				"q2install: %s also holds %s, which the Companion did not put there; the folder is left as it is",
				installation.ModDir, entry.Name()))
		}
	}
	if err := os.RemoveAll(installation.ModDir); err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, id+".json"))
}

func copyFile(ctx context.Context, from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	buffer := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			target.Close()
			return err
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			if _, err := target.Write(buffer[:n]); err != nil {
				target.Close()
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			target.Close()
			return readErr
		}
	}
	if err := target.Sync(); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}

func token() (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}
