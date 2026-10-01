// Package q3vfs stages the game data a Quake III build may read into a
// directory of the build's own, and records what it staged.
//
// # Why a build does not hand Q3Map2 the folders it was given (Q3_010)
//
// Q3Map2 is told where content is with `-fs_basepath <dir>`, twice — the base
// game data and the user's own content — and until `Q3_010` each `<dir>` was the
// host directory itself. Three measured behaviours of Q3Map2 2.5.17n make that
// more than untidy:
//
//   - `-fs_game <name>` is joined onto every base path with no check at all:
//     `-fs_game ..` printed `VFS Init: <game_root>/../`, so the compiler read
//     the PARENT of each folder the user had approved — and of the job's own
//     workspace. A name is not a path, and nothing enforced that.
//   - `-fs_game nosuchmod` initialises three directories that do not exist and
//     says nothing. The build "succeeds" having read none of the mod.
//   - a PK3 that is truncated is skipped in silence and the build exits 0; every
//     image it carried then becomes `Couldn't find image for shader`, which
//     reads as a missing texture and is really a damaged archive.
//
// So a build stages. For each approved root, only the game directories this
// build names — the base game's and the mod's — are reproduced under
// `<build>/vfs/<role>/`, holding the PK3s that parse as archives and the
// regular loose files, each a link to the file it stands for. Q3Map2 is then
// pointed at the staged directories and at nothing else: a sibling mod, a
// parent directory and a link out of the approved folder are not there to be
// read. What was staged — every archive with its digest, the loose file count,
// what was left out and why — is a member of the build manifest, so "which
// PK3s did this BSP come from" has an answer afterwards.
//
// # What it does not do
//
// It does not unpack an archive, does not decide which shader wins, and does
// not supply game data: `pak0.pk3` is id's, and a root without it is recorded
// as exactly that. It does not make the build hermetic either — the staged
// files are links to the user's own, read in place — it makes the set of files
// the compiler can NAME equal to the set the user approved.
package q3vfs

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
)

// BaseGame is the directory Q3Map2's `-game quake3` reads under each base path.
const BaseGame = "baseq3"

// Bounds. A root over either is refused rather than staged in part: half of a
// content folder is a build whose missing textures nobody can explain.
const (
	MaxLooseFiles     = 100000
	MaxArchives       = 256
	MaxArchiveEntries = 200000
	MaxSkippedListed  = 50
	maxNameLength     = 64
)

// Method is how a staged file stands for the user's own.
type Method string

const (
	MethodSymlink  Method = "symlink"
	MethodHardlink Method = "hardlink"
	MethodCopy     Method = "copy"
)

// Archive is one PK3 that was staged.
type Archive struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// SHA256 is the archive's own digest: which `pak0.pk3` this was.
	SHA256 string `json:"sha256"`
	// Entries is how many members its central directory declares. The members
	// were located, not decompressed: what was checked is that the archive
	// holds together, not that every texture in it decodes.
	Entries int `json:"entries"`
}

// Skipped is one thing in an approved game directory that was not staged.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Game is one game directory of one root, as staged.
type Game struct {
	// Name is the directory's name: the base game's, or the mod's.
	Name string `json:"name"`
	// Present is false when the root has no such directory. A fact, recorded,
	// and only sometimes a failure: a content folder with no `baseq3` is
	// ordinary for a mod.
	Present    bool      `json:"present"`
	Archives   []Archive `json:"archives,omitempty"`
	LooseFiles int       `json:"loose_files"`
	LooseBytes int64     `json:"loose_bytes"`
	// Skipped is what was left out, the first [MaxSkippedListed] of it by
	// name; SkippedMore counts the rest.
	Skipped     []Skipped `json:"skipped,omitempty"`
	SkippedMore int       `json:"skipped_more,omitempty"`
}

// skip records something that was not staged, within the listing bound.
func (g *Game) skip(path, reason string) {
	if len(g.Skipped) >= MaxSkippedListed {
		g.SkippedMore++
		return
	}
	g.Skipped = append(g.Skipped, Skipped{Path: path, Reason: reason})
}

// Root is one approved root, as staged.
type Root struct {
	Role string `json:"role"`
	// Source is the folder the user approved, on this machine. Diagnostic.
	Source string `json:"source"`
	// Path is what the compiler is given instead.
	Path  string `json:"path"`
	Games []Game `json:"games"`
}

// Finding is something a person should know about the staged data that did not
// stop the build.
type Finding struct {
	Class   string `json:"class"`
	Role    string `json:"role,omitempty"`
	Message string `json:"message"`
}

// Stage is everything that was staged for one build.
type Stage struct {
	BaseGame string `json:"base_game"`
	// FSGame is the mod directory name, empty for a plain base-game map.
	FSGame string `json:"fs_game,omitempty"`
	// Method is how staged files stand for the user's. One value for the whole
	// stage: the weakest that had to be used.
	Method   Method    `json:"method"`
	Roots    []Root    `json:"roots"`
	Findings []Finding `json:"findings,omitempty"`
}

// Paths is role → staged directory, which is what replaces the request's roots.
func (s *Stage) Paths() map[string]string {
	out := make(map[string]string, len(s.Roots))
	for _, root := range s.Roots {
		out[root.Role] = root.Path
	}
	return out
}

// Request is one staging.
type Request struct {
	// FSGame is the mod directory name, or empty.
	FSGame string
	// Roots is role → the folder the user approved for it.
	Roots map[string]string
	// Dir is where to stage; it is created, and must not exist with content.
	Dir string
}

// CheckFSGame says whether a mod directory name is a name.
//
// The profile's own `text` option already confines it to letters, digits, `.`,
// `_` and `-`; this is the rule for the thing it MEANS. A name that is only
// dots is a directory reference, one that starts with a dot is hidden from the
// listing a user would check it against, and a path separator of either kind is
// a path.
func CheckFSGame(name string) error {
	refuse := func(format string, args ...any) error {
		return failure.As(failure.FSGameInvalid, fmt.Errorf("the mod directory name "+format, args...))
	}
	switch {
	case name == "":
		return nil
	case len(name) > maxNameLength:
		return refuse("is %d bytes long; a game directory name is at most %d", len(name), maxNameLength)
	case strings.HasPrefix(name, "."):
		return refuse("%q starts with a dot; it has to be the NAME of a directory inside the game data folder, "+
			"and `.` and `..` are other directories", name)
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-') {
			return refuse("%q contains %q; a name may contain letters, digits, `.`, `_` and `-` only, "+
				"and it is never a path", name, string(ch))
		}
	}
	return nil
}

// Build stages the request's roots.
//
// It refuses — with a class from internal/failure, and having staged nothing a
// build would go on to read — a mod name that is not a name, a mod no approved
// root has, a root with no game directory at all, an archive that does not
// hold together, and a link that leaves the folder it was found in.
func Build(request Request) (*Stage, error) {
	if err := CheckFSGame(request.FSGame); err != nil {
		return nil, err
	}
	fsGame := request.FSGame
	if strings.EqualFold(fsGame, BaseGame) {
		// `-fs_game baseq3` names the base game twice. It is the base game.
		fsGame = ""
	}
	stage := &Stage{BaseGame: BaseGame, FSGame: fsGame, Method: MethodSymlink}
	games := []string{BaseGame}
	if fsGame != "" {
		games = append(games, fsGame)
	}

	roles := make([]string, 0, len(request.Roots))
	for role := range request.Roots {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	// Every approved folder, resolved, before anything is linked: a link is
	// allowed to land in ANY of them, because a user who keeps the base game
	// in one approved folder and links it from another approved both.
	approved := make([]string, 0, len(roles))
	sources := make(map[string]string, len(roles))
	for _, role := range roles {
		resolved, err := filepath.EvalSymlinks(request.Roots[role])
		if err != nil {
			return nil, failure.As(failure.GameDataMissing, fmt.Errorf("the %q folder: %w", role, err))
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			return nil, fmt.Errorf("the %q folder: %w", role, err)
		}
		sources[role] = absolute
		approved = append(approved, absolute)
	}

	if err := os.MkdirAll(request.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("q3vfs: creating %s: %w", request.Dir, err)
	}
	st := &stager{stage: stage, approved: approved}
	modFound := false
	for _, role := range roles {
		root := Root{Role: role, Source: sources[role], Path: filepath.Join(request.Dir, role)}
		if err := os.MkdirAll(root.Path, 0o700); err != nil {
			return nil, fmt.Errorf("q3vfs: creating %s: %w", root.Path, err)
		}
		anyPresent := false
		for _, name := range games {
			game, err := st.game(role, filepath.Join(root.Source, name), filepath.Join(root.Path, name), name)
			if err != nil {
				return nil, err
			}
			root.Games = append(root.Games, game)
			if game.Present {
				anyPresent = true
				if name == fsGame {
					modFound = true
				}
			}
		}
		if !anyPresent {
			return nil, failure.As(failure.GameDataMissing, fmt.Errorf(
				"the %q folder %s has no %s directory: Q3Map2 reads `<folder>/%s/…`, so choose the folder that "+
					"CONTAINS %s, not %s itself and not a folder beside it",
				role, root.Source, describeGames(games), BaseGame, BaseGame, BaseGame))
		}
		stage.Roots = append(stage.Roots, root)
	}
	if fsGame != "" && !modFound {
		return nil, failure.As(failure.FSGameNotFound, fmt.Errorf(
			"no approved folder has a %q directory, so `-fs_game %s` would read nothing: Q3Map2 does not complain "+
				"about a mod directory that is not there (measured), it compiles without it", fsGame, fsGame))
	}
	stage.findings()
	return stage, nil
}

func describeGames(games []string) string {
	quoted := make([]string, len(games))
	for i, game := range games {
		quoted[i] = "`" + game + "`"
	}
	return strings.Join(quoted, " or ")
}

// findings records what a person should know and the compiler will not say.
func (s *Stage) findings() {
	for _, root := range s.Roots {
		if root.Role != "game_root" {
			continue
		}
		for _, game := range root.Games {
			if game.Name != s.BaseGame || !game.Present {
				continue
			}
			if len(game.Archives) == 0 && game.LooseFiles == 0 {
				s.Findings = append(s.Findings, Finding{
					Class: failure.GameDataMissing, Role: root.Role,
					Message: fmt.Sprintf("the base game directory %s is empty: there is no pak0.pk3 and no loose file, "+
						"so every base-game shader the map names will be reported missing by the compiler. "+
						"The base game data is yours to supply; the Companion ships none.", game.Name),
				})
			}
		}
	}
}

type stager struct {
	stage    *Stage
	approved []string
	loose    int
}

// game stages one game directory of one root.
func (st *stager) game(role, source, destination, name string) (Game, error) {
	game := Game{Name: name}
	info, err := os.Stat(source)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return game, nil
	case err != nil:
		return game, fmt.Errorf("the %q folder: %w", role, err)
	case !info.IsDir():
		return game, nil
	}
	if err := st.inside(role, source); err != nil {
		return game, err
	}
	game.Present = true
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return game, fmt.Errorf("q3vfs: creating %s: %w", destination, err)
	}

	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("the %q folder: %w", role, walkErr)
		}
		if path == source {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		slashed := filepath.ToSlash(relative)
		if strings.HasPrefix(entry.Name(), ".") {
			// `.git`, `.DS_Store`, an editor's swap file. No engine reads a
			// hidden name out of a game directory, and a project folder is
			// exactly where a version-control directory of 10,000 files sits.
			game.skip(slashed, "a hidden name")
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, err := os.Stat(path) // follows a link: what it IS is what matters
		if err != nil {
			if entry.Type()&fs.ModeSymlink != 0 {
				game.skip(slashed, "a link to something that is not there")
				return nil
			}
			return fmt.Errorf("the %q folder: %w", role, err)
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if err := st.inside(role, path); err != nil {
				return err
			}
			if info.IsDir() {
				// WalkDir does not descend a linked directory, and following
				// one by hand is how a walk meets a cycle. Said, not staged.
				game.skip(slashed, "a link to a directory; link or copy its files instead")
				return nil
			}
		}
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o700)
		case !info.Mode().IsRegular():
			game.skip(slashed, "not a regular file")
			return nil
		}

		isArchive := !strings.Contains(slashed, "/") && strings.EqualFold(filepath.Ext(path), ".pk3")
		if isArchive {
			if len(game.Archives) >= MaxArchives {
				return failure.As(failure.ContentRefused, fmt.Errorf(
					"the %q folder's %s holds more than %d PK3 archives", role, name, MaxArchives))
			}
			archive, err := inspectArchive(path, info.Size())
			if err != nil {
				return failure.As(failure.ArchiveDamaged, fmt.Errorf(
					"the archive %s/%s in the %q folder is damaged or incomplete: %w. "+
						"Q3Map2 skips an archive it cannot read without a word (measured), so the build stops here "+
						"instead of compiling without it", name, slashed, role, err))
			}
			archive.Name = slashed
			game.Archives = append(game.Archives, archive)
		} else {
			st.loose++
			if st.loose > MaxLooseFiles {
				return failure.As(failure.ContentRefused, fmt.Errorf(
					"the approved folders hold more than %d loose files; that is more than a build stages", MaxLooseFiles))
			}
			game.LooseFiles++
			game.LooseBytes += info.Size()
		}
		return st.link(path, target)
	})
	if err != nil {
		return game, err
	}
	sort.Slice(game.Archives, func(i, j int) bool { return game.Archives[i].Name < game.Archives[j].Name })
	return game, nil
}

// inside refuses a path that resolves outside every approved folder.
func (st *stager) inside(role, path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("the %q folder: %w", role, err)
	}
	for _, root := range st.approved {
		if resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return nil
		}
	}
	return failure.As(failure.ContentRefused, fmt.Errorf(
		"%s in the %q folder is a link to %s, which is outside every folder this build was given. "+
			"A build reads what you approved and nothing else: bind the folder that really holds it",
		path, role, resolved))
}

// link makes target stand for source: a symbolic link, else a hard link, else a
// copy. The fallbacks are for a filesystem or an account that cannot make the
// first (Windows without the privilege), and the stage records the weakest one
// used, because a copy of `pak0.pk3` per build is a cost somebody should see.
func (st *stager) link(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("q3vfs: creating %s: %w", filepath.Dir(target), err)
	}
	if err := os.Symlink(source, target); err == nil {
		return nil
	}
	if err := os.Link(source, target); err == nil {
		st.weaken(MethodHardlink)
		return nil
	}
	st.weaken(MethodCopy)
	return copyFile(source, target)
}

func (st *stager) weaken(to Method) {
	if st.stage.Method == MethodCopy {
		return
	}
	st.stage.Method = to
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("q3vfs: reading %s: %w", source, err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("q3vfs: writing %s: %w", target, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("q3vfs: copying %s: %w", source, err)
	}
	return out.Close()
}

// inspectArchive reads a PK3's central directory, locates every member, and
// digests the archive.
//
// Nothing is decompressed. What is established is that the archive holds
// together: it has a central directory, the directory is of a sane size, and
// each member's data lies inside the file. That is exactly the damage a
// truncated or half-downloaded PK3 has, and it is the damage Q3Map2 turns into
// a silent skip.
func inspectArchive(path string, size int64) (Archive, error) {
	file, err := os.Open(path)
	if err != nil {
		return Archive{}, err
	}
	defer file.Close()
	reader, err := zip.NewReader(file, size)
	if err != nil {
		return Archive{}, fmt.Errorf("it is not a readable ZIP (%v)", err)
	}
	if len(reader.File) > MaxArchiveEntries {
		return Archive{}, fmt.Errorf("it declares %d members, over the %d a build stages", len(reader.File), MaxArchiveEntries)
	}
	entries := 0
	for _, member := range reader.File {
		if strings.HasSuffix(member.Name, "/") {
			continue
		}
		entries++
		offset, err := member.DataOffset()
		if err != nil {
			return Archive{}, fmt.Errorf("the member %q cannot be located (%v)", clip(member.Name), err)
		}
		if end := offset + int64(member.CompressedSize64); offset < 0 || end > size {
			return Archive{}, fmt.Errorf("the member %q claims bytes %d..%d of a %d-byte archive",
				clip(member.Name), offset, end, size)
		}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Archive{}, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return Archive{}, err
	}
	return Archive{Size: size, SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Entries: entries}, nil
}

func clip(name string) string {
	if len(name) > 80 {
		return name[:80] + "…"
	}
	return name
}
