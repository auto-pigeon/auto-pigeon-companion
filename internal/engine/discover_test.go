package engine_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
)

// A fake filesystem, because a discovery pass that read the developer's real
// home directory would be a test that passed on one machine and told nobody
// anything about the other five.
type fakeFS struct {
	// dirs maps a directory to the names in it.
	dirs  map[string][]string
	files map[string]string
}

// fsRoot is the top of the fake tree, spelled the way the running platform
// spells an absolute path — the discovery code joins and cleans real paths, and
// a POSIX literal would simply not match on Windows.
func fsRoot() string {
	if runtime.GOOS == "windows" {
		return `C:\`
	}
	return "/"
}

// at builds a path in the fake tree from segments.
func at(parts ...string) string {
	return filepath.Join(append([]string{fsRoot()}, parts...)...)
}

// newFakeFS cleans every key, so the map is keyed the way the code under test
// will ask for it.
func newFakeFS(dirs map[string][]string, files map[string]string) fakeFS {
	cleaned := fakeFS{dirs: map[string][]string{}, files: map[string]string{}}
	for path, names := range dirs {
		cleaned.dirs[filepath.Clean(path)] = names
	}
	for path, body := range files {
		cleaned.files[filepath.Clean(path)] = body
	}
	return cleaned
}

func (f fakeFS) readDir(path string) ([]os.DirEntry, error) {
	names, ok := f.dirs[filepath.Clean(path)]
	if !ok {
		return nil, os.ErrNotExist
	}
	entries := make([]os.DirEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, fakeEntry(name))
	}
	return entries, nil
}

func (f fakeFS) readFile(path string) ([]byte, error) {
	body, ok := f.files[filepath.Clean(path)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(body), nil
}

type fakeEntry string

func (e fakeEntry) Name() string               { return string(e) }
func (e fakeEntry) IsDir() bool                { return !strings.Contains(string(e), ".") }
func (e fakeEntry) Type() os.FileMode          { return 0 }
func (e fakeEntry) Info() (os.FileInfo, error) { return nil, os.ErrNotExist }

func TestDetectFindsSteamAndGOGAndSaysWhatMadeItThink(t *testing.T) {
	home := "/home/mapper"
	fs := fakeFS{dirs: map[string][]string{
		home + "/.steam/steam/steamapps/common":                     {"Quake", "Half-Life"},
		home + "/.steam/steam/steamapps/common/Quake":               {"Id1", "rerelease", "quakespasm"},
		home + "/.steam/steam/steamapps/common/Quake/Id1":           {"PAK0.PAK", "PAK1.PAK"},
		home + "/.steam/steam/steamapps/common/Quake/rerelease":     {"id1"},
		home + "/.steam/steam/steamapps/common/Quake/rerelease/id1": {"pak0.pak"},
		home + "/GOG Games/Quake":                                   {"id1", "Quake.exe"},
		home + "/GOG Games/Quake/id1":                               {"pak0.pak"},
	}}
	scanner := engine.Scanner{
		GOOS:     "linux",
		Lookenv:  func(name string) (string, bool) { return map[string]string{"HOME": home}[name], name == "HOME" },
		ReadDir:  fs.readDir,
		ReadFile: fs.readFile,
	}

	found := scanner.Detect()
	if len(found) != 3 {
		t.Fatalf("found %d candidates, want 3: %+v", len(found), found)
	}

	// The base directory is reported with the spelling that is actually on
	// disk. On a case-sensitive filesystem `Id1` and `id1` are two different
	// directories, and an engine handed the wrong one finds no game.
	if found[0].BaseDir != "Id1" {
		t.Errorf("the Steam candidate reports the base directory as %q, want the spelling on disk, %q", found[0].BaseDir, "Id1")
	}
	if found[0].Source != engine.SourceSteam || found[0].Evidence != "Id1/PAK0.PAK" {
		t.Errorf("the Steam candidate is %+v", found[0])
	}
	if found[1].Note == "" {
		t.Errorf("the re-release's own copy is reported without saying what it is: %+v", found[1])
	}
	if found[2].Source != engine.SourceGOG {
		t.Errorf("the GOG candidate is %+v", found[2])
	}
}

// A user with two drives has their games on the second one, and Steam is the
// only thing that knows where it is.
func TestDetectReadsSteamsOwnLibraryIndex(t *testing.T) {
	home := at("home", "mapper")
	second := at("mnt", "games", "SteamLibrary")
	quake := filepath.Join(second, "steamapps", "common", "Quake")
	fs := newFakeFS(
		map[string][]string{
			filepath.Join(second, "steamapps", "common"): {"Quake"},
			quake:                       {"id1"},
			filepath.Join(quake, "id1"): {"pak0.pak"},
		},
		map[string]string{
			filepath.Join(home, ".steam", "steam", "steamapps", "libraryfolders.vdf"): `
"libraryfolders"
{
	"0"
	{
		"path"		"` + home + `"
	}
	"1"
	{
		"path"		"` + second + `"
	}
}
`,
		},
	)
	scanner := engine.Scanner{
		GOOS:     "linux",
		Lookenv:  func(name string) (string, bool) { return map[string]string{"HOME": home}[name], name == "HOME" },
		ReadDir:  fs.readDir,
		ReadFile: fs.readFile,
	}
	found := scanner.Detect()
	if len(found) != 1 || found[0].Path != quake {
		t.Fatalf("the second library was not searched: %+v", found)
	}
}

// A directory that is called id1 and has no game in it is not a game.
func TestDetectIgnoresADirectoryWithNoArchiveInIt(t *testing.T) {
	home := at("home", "mapper")
	steam := filepath.Join(home, ".steam", "steam", "steamapps", "common")
	fs := newFakeFS(map[string][]string{
		steam:                                {"Quake"},
		filepath.Join(steam, "Quake"):        {"id1"},
		filepath.Join(steam, "Quake", "id1"): {"readme.txt"},
	}, nil)
	scanner := engine.Scanner{
		GOOS:     "linux",
		Lookenv:  func(name string) (string, bool) { return map[string]string{"HOME": home}[name], name == "HOME" },
		ReadDir:  fs.readDir,
		ReadFile: fs.readFile,
	}
	if found := scanner.Detect(); len(found) != 0 {
		t.Fatalf("an empty id1 was reported as an installed game: %+v", found)
	}
}

// Where the user says to look is looked at too, which is the whole of the
// "manual location" case: somebody who installed Quake by unzipping it into a
// folder of their own gets the same treatment as somebody who used Steam.
func TestDetectConsidersDirectoriesTheCallerNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "id1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id1", "pak0.pak"), []byte("PACK"), 0o600); err != nil {
		t.Fatal(err)
	}
	scanner := engine.Scanner{
		GOOS:    "linux",
		Lookenv: func(string) (string, bool) { return "", false },
		Extra:   []string{dir},
	}
	found := scanner.Detect()
	if len(found) != 1 || found[0].Source != engine.SourceNearEngine || found[0].Path != dir {
		t.Fatalf("a directory named by the caller was not considered: %+v", found)
	}
}

// Discovery is inert. It is not a matter of policy in the CLI: nothing in the
// package can write, because nothing in it has anything to write with.
func TestDetectWritesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "id1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id1", "pak0.pak"), []byte("PACK"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, dir)
	engine.Scanner{GOOS: "linux", Lookenv: func(string) (string, bool) { return "", false }, Extra: []string{dir}}.Detect()
	if after := treeOf(t, dir); after != before {
		t.Errorf("detection changed the game directory:\n  before %s\n  after  %s", before, after)
	}
}

func treeOf(t *testing.T, dir string) string {
	t.Helper()
	var names []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		names = append(names, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(names, " ")
}

// The 2021 re-release nests a second copy of the whole layout under
// `rerelease/`. That is true wherever the install came from, so it has to be
// looked for wherever a candidate comes from — including a directory the user
// named, which is the case a Steam-only implementation misses.
func TestDetectFindsTheReReleaseLayoutUnderADirectoryTheCallerNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rerelease", "id1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rerelease", "id1", "pak0.pak"), []byte("PACK"), 0o600); err != nil {
		t.Fatal(err)
	}
	found := engine.Scanner{
		GOOS:    "linux",
		Lookenv: func(string) (string, bool) { return "", false },
		Extra:   []string{dir},
	}.Detect()
	if len(found) != 1 {
		t.Fatalf("found %d candidates, want the nested re-release: %+v", len(found), found)
	}
	if found[0].Path != filepath.Join(dir, "rerelease") {
		t.Errorf("the candidate is %q, want the directory that actually holds id1", found[0].Path)
	}
	if found[0].Note == "" {
		t.Error("the re-release candidate does not say what it is")
	}
}
