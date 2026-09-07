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
	// Evidence is the archive that identified it, relative to Path.
	Evidence string `json:"evidence"`
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
}

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

// baseDirName is the directory a Quake 1 release keeps its own data in. One
// name, because the 2021 re-release does not rename it — it nests a second copy
// of the whole layout under `rerelease/`, which [Scanner.Detect] handles by
// looking inside that directory as well rather than by knowing another name.
const baseDirName = "id1"

// pakNames are the archives whose presence says a directory really is Quake's
// and not a directory that happens to be called id1.
var pakNames = []string{"pak0.pak", "pak1.pak"}

// Detect reports every directory that looks like an installed Quake 1.
//
// It never writes anything, never opens a game file, and never copies one. The
// result is a list of suggestions in a stable order; confirming one is
// somebody else's job.
func (s Scanner) Detect() []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(dir string, source Source, note string) {
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

	for _, library := range s.steamLibraries() {
		common := filepath.Join(library, "steamapps", "common")
		for _, name := range s.entriesLike(common, "quake") {
			dir := filepath.Join(common, name)
			add(dir, SourceSteam, "")
			// The 2021 re-release keeps the original game in a subdirectory of
			// its own, so the directory Steam installed is not always the one
			// an engine wants as its base directory.
			for _, sub := range s.entriesLike(dir, "rerelease") {
				add(filepath.Join(dir, sub), SourceSteam, "The re-release's own copy of the game data.")
			}
		}
	}
	for _, dir := range s.gogDirs() {
		add(dir, SourceGOG, "")
	}
	for _, dir := range s.Extra {
		add(dir, SourceNearEngine, "")
	}
	return out
}

// inspect decides whether a directory is a game root, and how its base game
// directory is spelled on this filesystem.
func (s Scanner) inspect(dir string, source Source) (Candidate, bool) {
	for _, base := range s.entriesLike(dir, baseDirName) {
		for _, pak := range pakNames {
			for _, found := range s.entriesLike(filepath.Join(dir, base), pak) {
				return Candidate{
					Path:     dir,
					Source:   source,
					BaseDir:  base,
					Evidence: base + "/" + found,
				}, true
			}
		}
	}
	return Candidate{}, false
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
