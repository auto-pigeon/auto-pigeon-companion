package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Where an installed game was found.
//
// It is recorded because "the Companion found this under Steam" and "I typed
// this in" are different facts, and only the second one is a decision. The
// first is a suggestion that still has to be confirmed.
type Source string

const (
	// SourceSteam is a Steam library.
	SourceSteam Source = "steam"
	// SourceGOG is a GOG installation.
	SourceGOG Source = "gog"
	// SourceNearEngine is a directory the caller named — normally the one the
	// engine executable sits in, which is where a hand-installed Quake usually
	// is.
	SourceNearEngine Source = "near_engine"
)

// Candidate is one directory that looks like an installed game.
//
// It is not a binding and cannot become one on its own. Path is what a user
// would have to confirm; Evidence is the file that made the Companion think so,
// so that a person can check the suggestion instead of trusting it.
type Candidate struct {
	// Path is the game root: the directory that contains the base game
	// directory, not the base game directory itself.
	Path string `json:"path"`
	// Source is where the guess came from.
	Source Source `json:"source"`
	// BaseDir is the base game directory as it is actually spelled on disk.
	// Quake's own releases are inconsistent about case, and an engine given
	// the wrong spelling on a case-sensitive filesystem finds nothing.
	BaseDir string `json:"base_dir"`
	// Evidence is the archive that identified it, relative to Path, with a
	// forward slash whatever the platform.
	Evidence string `json:"evidence"`
	// EvidencePath is the same archive spelled the way this platform spells a
	// relative path (`id1\pak0.pak` on Windows), for a sentence a person reads.
	EvidencePath string `json:"evidence_path"`
	// Where says how a near-engine candidate relates to the executable it was
	// found from: [WhereBesideEngine] or [WhereAboveEngine]. Empty otherwise.
	Where string `json:"where,omitempty"`
	// Note is anything the user should know before confirming.
	Note string `json:"note,omitempty"`
}

// Scanner looks for installed games.
//
// Every dependency on the outside world is a field, because a discovery pass
// that read the developer's real home directory would be a test that passed on
// one machine.
type Scanner struct {
	// GOOS is the platform whose conventions to use. Empty means the running
	// one.
	GOOS string
	// Lookenv reads an environment variable. Nil means os.LookupEnv.
	Lookenv func(string) (string, bool)
	// ReadDir lists a directory. Nil means os.ReadDir.
	ReadDir func(string) ([]os.DirEntry, error)
	// ReadFile reads a small file — only Steam's library index. Nil means
	// os.ReadFile.
	ReadFile func(string) ([]byte, error)
	// Extra are directories the caller wants considered as well, reported as
	// [SourceNearEngine]. The engine's own directory belongs here.
	Extra []string
	// Layout is the game whose data to look for. The zero value is Quake 1,
	// which is what every scanner looked for before a profile could say
	// otherwise — and only Quake 1 is looked for in Steam and GOG libraries.
	Layout Layout
}

// Layout is what makes a directory one game's data folder: the folder's name,
// as an engine profile's [profile.EngineProfile.BaseFolder] gives it, and the
// archives whose presence proves it holds that game rather than merely being
// called that. An empty folder, or one holding another game's data, is not a
// match.
type Layout struct {
	// BaseDir is the data folder: `id1`, `baseq2`, `baseq3`.
	BaseDir string `json:"base_dir"`
	// Archives are the files any one of which is evidence, matched without
	// regard to case.
	Archives []string `json:"archives"`
}

// The layouts of the supported games.
var (
	Quake1Layout = Layout{BaseDir: "id1", Archives: []string{"pak0.pak", "pak1.pak"}}
	Quake2Layout = Layout{BaseDir: "baseq2", Archives: []string{"pak0.pak"}}
	Quake3Layout = Layout{BaseDir: "baseq3", Archives: []string{"pak0.pk3"}}
)

// LayoutForBaseFolder is the layout whose data folder is base, as an engine
// profile names it. The profile decides, not the executable: an engine such as
// FTEQW plays more than one game, and each of its profiles names its own.
func LayoutForBaseFolder(base string) (Layout, bool) {
	for _, layout := range []Layout{Quake1Layout, Quake2Layout, Quake3Layout} {
		if strings.EqualFold(layout.BaseDir, base) {
			return layout, true
		}
	}
	return Layout{}, false
}

func (s Scanner) layout() Layout {
	if s.Layout.BaseDir == "" {
		return Quake1Layout
	}
	return s.Layout
}

// quake1 says whether this scan is for Quake 1, the one game whose store
// installs (Steam, GOG) and re-release layout the scanner knows.
func (s Scanner) quake1() bool { return strings.EqualFold(s.layout().BaseDir, Quake1Layout.BaseDir) }

func (s Scanner) goos() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

func (s Scanner) lookenv(name string) (string, bool) {
	if s.Lookenv != nil {
		return s.Lookenv(name)
	}
	return os.LookupEnv(name)
}

func (s Scanner) readDir(path string) ([]os.DirEntry, error) {
	if s.ReadDir != nil {
		return s.ReadDir(path)
	}
	return os.ReadDir(path)
}

func (s Scanner) readFile(path string) ([]byte, error) {
	if s.ReadFile != nil {
		return s.ReadFile(path)
	}
	return os.ReadFile(path)
}

// rereleaseDirName is where the 2021 re-release keeps its own copy of that
// layout, nested inside the directory the installer created.
const rereleaseDirName = "rerelease"

// Detect reports every directory that looks like an installed Quake 1.
//
// It never writes anything, never opens a game file, and never copies one. The
// result is a list of suggestions in a stable order; confirming one is
// somebody else's job.
func (s Scanner) Detect() []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	one := func(dir string, source Source, note string) {
		dir = filepath.Clean(dir)
		if dir == "" || dir == "." || seen[dir] {
			return
		}
		seen[dir] = true
		if candidate, ok := s.inspect(dir, source); ok {
			candidate.Note = note
			out = append(out, candidate)
		}
	}
	// add looks at a directory and at the re-release layout nested inside it.
	//
	// The 2021 re-release does not rename `id1`; it puts a second copy of the
	// whole layout under `rerelease/`, so the directory an installer created is
	// not always the one an engine wants as its base directory. That is true
	// wherever the install came from, which is why this is here and not in the
	// Steam branch.
	add := func(dir string, source Source, note string) {
		one(dir, source, note)
		if !s.quake1() {
			return
		}
		for _, sub := range s.entriesLike(dir, rereleaseDirName) {
			one(filepath.Join(dir, sub), source, "The re-release's own copy of the game data.")
		}
	}

	// The store layouts below are Quake 1's; another game is looked for only
	// where the caller points.
	if s.quake1() {
		for _, library := range s.steamLibraries() {
			common := filepath.Join(library, "steamapps", "common")
			for _, name := range s.entriesLike(common, "quake") {
				add(filepath.Join(common, name), SourceSteam, "")
			}
		}
		for _, dir := range s.gogDirs() {
			add(dir, SourceGOG, "")
		}
	}
	for _, dir := range s.Extra {
		add(dir, SourceNearEngine, "")
	}
	return out
}

// inspect decides whether a directory is a game root, and how its base game
// directory is spelled on this filesystem.
func (s Scanner) inspect(dir string, source Source) (Candidate, bool) {
	layout := s.layout()
	for _, base := range s.entriesLike(dir, layout.BaseDir) {
		for _, archive := range layout.Archives {
			for _, found := range s.filesLike(filepath.Join(dir, base), archive) {
				return Candidate{
					Path:         dir,
					Source:       source,
					BaseDir:      base,
					Evidence:     base + "/" + found,
					EvidencePath: filepath.Join(base, found),
				}, true
			}
		}
	}
	return Candidate{}, false
}

// filesLike is entriesLike for evidence: only a real file counts — never a
// directory that happens to carry an archive's name, and never an empty file.
func (s Scanner) filesLike(dir, want string) []string {
	entries, err := s.readDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if !strings.EqualFold(entry.Name(), want) || entry.IsDir() {
			continue
		}
		if info, err := entry.Info(); err == nil && (!info.Mode().IsRegular() || info.Size() == 0) {
			continue
		}
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}

// How a near-engine candidate relates to the executable it was found from.
const (
	// WhereBesideEngine: the data folder is in the executable's own directory.
	WhereBesideEngine = "beside_engine"
	// WhereAboveEngine: the executable is in a program subdirectory (`bin`)
	// and the data folder is beside that subdirectory.
	WhereAboveEngine = "above_engine"
)

// programDirNames are the subdirectories engines conventionally keep their
// executables in, below the game root.
var programDirNames = []string{"bin", "bin32", "bin64", "x86", "x64", "x86_64", "amd64", "win32", "win64", "linux32", "linux64"}

func isProgramDir(name string) bool {
	for _, known := range programDirNames {
		if strings.EqualFold(name, known) {
			return true
		}
	}
	return false
}

// NearExecutable proposes game roots for the engine at executable, looking
// only where a hand-installed game conventionally is: the executable's own
// directory, and — when the executable sits in a program subdirectory such as
// `bin` — the directory above it (at most two such levels, `bin/x64`).
//
// Like [Scanner.Detect] it only reads directory listings: it never starts the
// engine, opens a game file or writes anything. Every candidate is a
// suggestion a person still has to confirm.
func (s Scanner) NearExecutable(executable string) []Candidate {
	dir := filepath.Dir(filepath.Clean(executable))
	dirs := []string{dir}
	for current := dir; len(dirs) < 3 && isProgramDir(filepath.Base(current)); {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		dirs = append(dirs, parent)
		current = parent
	}
	var out []Candidate
	seen := map[string]bool{}
	for index, root := range dirs {
		where := WhereBesideEngine
		if index > 0 {
			where = WhereAboveEngine
		}
		roots := []string{root}
		if s.quake1() {
			for _, sub := range s.entriesLike(root, rereleaseDirName) {
				roots = append(roots, filepath.Join(root, sub))
			}
		}
		for _, candidateDir := range roots {
			if seen[candidateDir] {
				continue
			}
			seen[candidateDir] = true
			if candidate, ok := s.inspect(candidateDir, SourceNearEngine); ok {
				candidate.Where = where
				if candidateDir != root {
					candidate.Note = "The re-release's own copy of the game data."
				}
				out = append(out, candidate)
			}
		}
	}
	return out
}

// entriesLike returns the entries of a directory whose names equal want,
// ignoring case.
//
// Case-insensitively, because Quake's own releases are not consistent about it
// — `Id1/PAK0.PAK` and `id1/pak0.pak` are both out there — and on a
// case-sensitive filesystem an engine handed the wrong spelling finds no game
// at all. Matching what is actually on disk and reporting that spelling is the
// only way to hand the engine something that works.
func (s Scanner) entriesLike(dir, want string) []string {
	entries, err := s.readDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), want) {
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out
}

// steamLibraries lists the Steam library roots: the default one for this
// platform, plus every extra library Steam has recorded.
func (s Scanner) steamLibraries() []string {
	var roots []string
	add := func(path string) {
		if path != "" {
			roots = append(roots, filepath.Clean(path))
		}
	}
	switch s.goos() {
	case "windows":
		for _, variable := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
			if base, ok := s.lookenv(variable); ok {
				add(filepath.Join(base, "Steam"))
			}
		}
	case "darwin":
		if home, ok := s.lookenv("HOME"); ok {
			add(filepath.Join(home, "Library", "Application Support", "Steam"))
		}
	default:
		if home, ok := s.lookenv("HOME"); ok {
			add(filepath.Join(home, ".steam", "steam"))
			add(filepath.Join(home, ".local", "share", "Steam"))
			// The Flatpak build keeps its own home, and a user who installed
			// Steam that way has no library anywhere else.
			add(filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"))
		}
		if data, ok := s.lookenv("XDG_DATA_HOME"); ok {
			add(filepath.Join(data, "Steam"))
		}
	}

	// Steam records every additional library it knows about. Reading that
	// index is the difference between finding a game on the second drive and
	// telling a user with two drives that they do not own Quake.
	var extra []string
	for _, root := range roots {
		extra = append(extra, s.libraryFolders(filepath.Join(root, "steamapps", "libraryfolders.vdf"))...)
	}
	return dedupe(append(roots, extra...))
}

// maxLibraryIndex bounds the file read below. Steam's index is a few kilobytes;
// anything of this size is not Steam's index.
const maxLibraryIndex = 1 << 20

// libraryFolders pulls the library paths out of Steam's index.
//
// A deliberately small reader for a format this program has no business
// implementing. Valve's VDF is a nested key/value text format, and the only
// thing wanted from it is the value of each `"path"` key; a full parser would
// be a hundred lines and a dependency on a format nobody documents. Anything
// that is not a plain absolute path is ignored rather than guessed at.
func (s Scanner) libraryFolders(index string) []string {
	raw, err := s.readFile(index)
	if err != nil || len(raw) > maxLibraryIndex {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := splitQuoted(line)
		if len(fields) != 2 || fields[0] != "path" {
			continue
		}
		// Windows spells its paths with escaped backslashes inside the index.
		path := strings.ReplaceAll(fields[1], `\\`, `\`)
		if filepath.IsAbs(path) {
			out = append(out, filepath.Clean(path))
		}
	}
	return out
}

// splitQuoted returns the double-quoted tokens on one line.
func splitQuoted(line string) []string {
	var out []string
	rest := line
	for {
		_, after, found := strings.Cut(rest, `"`)
		if !found {
			return out
		}
		token, tail, closed := strings.Cut(after, `"`)
		if !closed {
			return out
		}
		out = append(out, token)
		rest = tail
	}
}

// gogDirs lists where GOG installs things.
func (s Scanner) gogDirs() []string {
	var out []string
	switch s.goos() {
	case "windows":
		for _, variable := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
			if base, ok := s.lookenv(variable); ok {
				out = append(out, filepath.Join(base, "GOG Galaxy", "Games", "Quake"))
				out = append(out, filepath.Join(base, "GOG.com", "Quake"))
			}
		}
	case "darwin":
		if home, ok := s.lookenv("HOME"); ok {
			out = append(out, filepath.Join(home, "Applications", "Quake"))
		}
	default:
		if home, ok := s.lookenv("HOME"); ok {
			// The Linux installer's default, and where most people leave it.
			out = append(out, filepath.Join(home, "GOG Games", "Quake"))
			out = append(out, filepath.Join(home, "Games", "quake"))
		}
	}
	return out
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
