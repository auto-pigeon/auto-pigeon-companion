package joincontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// Staging layout, under the asset cache:
//
//	join-content/
//	  <first 16 hex of the package digest>/
//	    staged.json               what was staged, and from which package (the whole digest)
//	    b/                        the engine's base directory (-basedir)
//	      <base-game dir> -> the user's own, as a symbolic link, created at launch
//	      <game dir>/maps/x.bsp   the package's files (-game <game dir>)
//
// One directory per PACKAGE, so two games on the same build share a stage and a
// rebuilt package is a different directory: a stage is never edited in place,
// which is what makes "is this staged" a question with a stable answer.
//
// # Why the path is short, and why that is measured rather than tidy
//
// vkQuake 1.36.0 keeps the first 255 characters of its command line — the whole
// line, its own executable path included — and executes whatever `+` commands
// survive. Measured in 244F's native run: with a stage at
// `…/join-content/<64 hex>/base`, `+map aut244f` arrived as `+ma`, the engine
// reported `Unknown command "ma"` and played its demo loop instead. A join cut the
// same way loses `+connect` and sits at the menu, which looks like a join that did
// nothing. So the directory names here are as short as they can be while still
// being one per package: sixteen hex characters (staged.json holds the whole
// digest, and Lookup refuses a record naming any other) and a one-letter base.

// baseDirName is the base directory inside a stage. One letter; see above.
const baseDirName = "b"

// StageSchema versions staged.json.
const StageSchema = "aucom.join-content-stage/1.0"

// Errors a stage can report.
var (
	// ErrNotStaged is a package with no complete stage on this machine.
	ErrNotStaged = errors.New("joincontent: this package is not staged on this machine")
	// ErrStageDamaged is a stage whose files no longer verify.
	ErrStageDamaged = errors.New("joincontent: the staged files no longer match the package")
)

// Stager owns the join-content directory.
type Stager struct {
	// Root is `<asset cache>/join-content`.
	Root  string
	Store *assetsync.Store
	// Now is the clock, for tests.
	Now func() time.Time
}

// Fetch opens one file of a package for download. The joiner's AUB client, bound
// to one game id.
type Fetch func(ctx context.Context, file aub.JoinContentFile) (io.ReadCloser, error)

// Record is staged.json.
type Record struct {
	SchemaVersion string                `json:"schema_version"`
	PackageSHA256 string                `json:"package_sha256"`
	GameDir       string                `json:"game_dir"`
	MapName       string                `json:"map_name"`
	Files         []aub.JoinContentFile `json:"files"`
	StagedAt      time.Time             `json:"staged_at"`
}

// Stage is a verified staged package.
type Stage struct {
	Record Record
	// Dir is the package directory; BaseDir is what `-basedir` names; GameDirPath
	// is `<BaseDir>/<GameDir>`.
	Dir         string
	BaseDir     string
	GameDirPath string
}

// GameDirName is the game directory a package stages into. Derived from the
// digest so it is stable and says nothing about who hosted it.
func GameDirName(packageSHA256 string) string {
	return "ap-" + Short(packageSHA256)
}

func (s *Stager) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}

	return time.Now().UTC()
}

func (s *Stager) dirFor(packageSHA256 string) (string, error) {
	hexDigest := strings.TrimPrefix(strings.ToLower(packageSHA256), "sha256:")
	if len(hexDigest) != 64 || strings.Trim(hexDigest, "0123456789abcdef") != "" {
		return "", fmt.Errorf("joincontent: %q is not a package digest", packageSHA256)
	}
	if strings.TrimSpace(s.Root) == "" {
		return "", errors.New("joincontent: no join-content directory is configured")
	}

	return filepath.Join(s.Root, hexDigest[:16]), nil
}

// Dir is the directory a package is staged in, whether or not it is staged yet.
func (s *Stager) Dir(packageSHA256 string) (string, error) { return s.dirFor(packageSHA256) }

// Missing lists the files of a verified package the object store does not hold
// yet, which is what a download still has to fetch.
func (s *Stager) Missing(files []aub.JoinContentFile) []aub.JoinContentFile {
	var missing []aub.JoinContentFile
	for _, file := range files {
		if !s.Store.Has(file.SHA256) {
			missing = append(missing, file)
		}
	}

	return missing
}

// Download publishes every missing file into the object store, verified. It
// writes nothing outside the store, so an interrupted download leaves only
// complete, verified objects — which is exactly what a retry can resume from.
func (s *Stager) Download(ctx context.Context, files []aub.JoinContentFile, fetch Fetch,
	progress func(done, total int),
) error {
	missing := s.Missing(files)
	for index, file := range missing {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := fetch(ctx, file)
		if err != nil {
			return err
		}
		_, err = s.Store.Publish(io.LimitReader(body, file.Bytes+1), file.SHA256, file.Bytes)
		body.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", file.Destination, err)
		}
		if progress != nil {
			progress(index+1, len(missing))
		}
	}

	return nil
}

// Stage assembles a verified package into its directory, atomically.
//
// Every file must already be in the object store. Each is copied out and hashed
// again as it is written, so a store object damaged after it was published is
// caught here rather than loaded by an engine.
func (s *Stager) Stage(packageSHA256 string, files []aub.JoinContentFile) (Stage, error) {
	dir, err := s.dirFor(packageSHA256)
	if err != nil {
		return Stage{}, err
	}
	if existing, err := s.Lookup(packageSHA256, files); err == nil {
		return existing, nil
	}
	if err = os.MkdirAll(s.Root, 0o700); err != nil {
		return Stage{}, err
	}
	temporary, err := os.MkdirTemp(s.Root, ".staging-")
	if err != nil {
		return Stage{}, err
	}
	// Removed on every path but a successful rename.
	defer os.RemoveAll(temporary)

	gameDir := GameDirName(packageSHA256)
	record := Record{SchemaVersion: StageSchema, PackageSHA256: strings.ToLower(packageSHA256),
		GameDir: gameDir, Files: files, StagedAt: s.now()}
	for _, file := range files {
		if file.Role == RoleBSP {
			record.MapName = strings.TrimSuffix(filepath.Base(file.Destination), ".bsp")
		}
		source, err := s.Store.Object(file.SHA256)
		if err != nil {
			return Stage{}, fmt.Errorf("%s has not been downloaded: %w", file.Destination, err)
		}
		target := filepath.Join(temporary, baseDirName, gameDir, filepath.FromSlash(file.Destination))
		if err = copyVerified(source, target, file); err != nil {
			return Stage{}, err
		}
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return Stage{}, err
	}
	if err = os.WriteFile(filepath.Join(temporary, "staged.json"), raw, 0o600); err != nil {
		return Stage{}, err
	}
	// A damaged or partial previous stage is replaced, never merged into.
	if _, statErr := os.Lstat(dir); statErr == nil {
		if err = os.RemoveAll(dir); err != nil {
			return Stage{}, err
		}
	}
	if err = fsshare.Replace(temporary, dir); err != nil {
		return Stage{}, err
	}

	return s.Lookup(packageSHA256, files)
}

// Lookup returns a complete, verified stage, or ErrNotStaged / ErrStageDamaged.
//
// Verification walks the WHOLE tree under the game directory: every file listed
// must be a regular file with the right size and digest, and nothing else may be
// there — no extra file, no symbolic link, no device. An engine loads whatever is
// in the directory, so an extra file is as much a problem as a wrong one.
func (s *Stager) Lookup(packageSHA256 string, files []aub.JoinContentFile) (Stage, error) {
	dir, err := s.dirFor(packageSHA256)
	if err != nil {
		return Stage{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "staged.json"))
	if err != nil {
		return Stage{}, ErrNotStaged
	}
	var record Record
	if err = json.Unmarshal(raw, &record); err != nil || record.SchemaVersion != StageSchema ||
		!strings.EqualFold(record.PackageSHA256, packageSHA256) {
		return Stage{}, fmt.Errorf("%w: its record is unreadable", ErrStageDamaged)
	}
	if files == nil {
		files = record.Files
	}
	stage := Stage{Record: record, Dir: dir, BaseDir: filepath.Join(dir, baseDirName)}
	stage.GameDirPath = filepath.Join(stage.BaseDir, record.GameDir)
	if record.GameDir != GameDirName(packageSHA256) {
		return Stage{}, fmt.Errorf("%w: it names the wrong game directory", ErrStageDamaged)
	}

	expected := map[string]aub.JoinContentFile{}
	for _, file := range files {
		expected[filepath.FromSlash(file.Destination)] = file
	}
	found := 0
	walkErr := filepath.WalkDir(stage.GameDirPath, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == stage.GameDirPath {
			return nil
		}
		relative, _ := filepath.Rel(stage.GameDirPath, current)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", relative)
		}
		file, listed := expected[relative]
		if !listed {
			return fmt.Errorf("%s is not part of the package", relative)
		}
		if info.Size() != file.Bytes {
			return fmt.Errorf("%s is %d bytes, and the package says %d", relative, info.Size(), file.Bytes)
		}
		sum, err := hashFile(current)
		if err != nil {
			return err
		}
		if sum != file.SHA256 {
			return fmt.Errorf("%s does not hash to what the package says", relative)
		}
		found++

		return nil
	})
	if walkErr != nil {
		return Stage{}, fmt.Errorf("%w: %v", ErrStageDamaged, walkErr)
	}
	if found != len(expected) {
		return Stage{}, fmt.Errorf("%w: %d of %d files are there", ErrStageDamaged, found, len(expected))
	}

	return stage, nil
}

// Overlay links the user's base-game directories into a stage's base directory,
// so the engine can be pointed at the stage and still find the game it needs.
//
// Only the named directories are linked, each only if it exists in the user's
// game root, and each as a symbolic link (on Windows without the privilege for
// one, a directory junction — link_windows.go): the user's files are READ
// through it and never copied. A link that already points at the right place is left alone;
// one pointing anywhere else is replaced, because the binding moved.
func (st Stage) Overlay(gameRoot string, baseDirs []string) error {
	if gameRoot == "" {
		return errors.New("joincontent: no game folder is set, so there is nothing to join with")
	}
	for _, name := range baseDirs {
		if name == "" || strings.ContainsAny(name, `/\:`) || name == "." || name == ".." ||
			strings.EqualFold(name, st.Record.GameDir) {
			return fmt.Errorf("joincontent: %q is not a base-game directory name", name)
		}
		source := filepath.Join(gameRoot, name)
		info, err := os.Stat(source)
		if err != nil || !info.IsDir() {
			continue
		}
		link := filepath.Join(st.BaseDir, name)
		if current, err := os.Readlink(link); err == nil {
			if current == source {
				continue
			}
			if err = os.Remove(link); err != nil {
				return err
			}
		} else if _, statErr := os.Lstat(link); statErr == nil {
			return fmt.Errorf("joincontent: %s exists and is not a link this Companion made", link)
		}
		if err = linkDir(source, link); err != nil {
			return fmt.Errorf("joincontent: linking the game folder: %w", err)
		}
	}

	return nil
}

func copyVerified(source, target string, file aub.JoinContentFile) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, file.Bytes+1))
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		return copyErr
	case closeErr != nil:
		return closeErr
	case written != file.Bytes || hex.EncodeToString(hash.Sum(nil)) != file.SHA256:
		return fmt.Errorf("%w: the stored copy of %s is damaged", ErrStageDamaged, file.Destination)
	}

	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// LinkDir makes link a link to the directory source: a symbolic link, or on
// Windows without the privilege for one, a directory junction. It is the one
// implementation of "let an engine read the user's game folder without copying
// it", exported for the other place that needs exactly that — a Quake III
// package installed beside a base game it must not write into.
func LinkDir(source, link string) error { return linkDir(source, link) }
