package q3install

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
)

const (
	testArchive = "auto-pigeon-testmap-3.pk3"
	testMap     = "testmap"
)

// world is a user's game folder, a package and somewhere to record installs.
type world struct {
	t        *testing.T
	gameRoot string
	installs string
	record   *q3pack.Record
}

func zipBytes(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		part, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, err := part.Write([]byte(members[name])); err != nil {
			t.Fatalf("%v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("%v", err)
	}
	return buffer.Bytes()
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
}

// newWorld makes a game folder that looks like somebody's: a base game with an
// archive, a configuration file and a directory, and a second game beside it.
func newWorld(t *testing.T, gameDir string) *world {
	t.Helper()
	root := t.TempDir()
	w := &world{t: t, gameRoot: filepath.Join(root, "quake3"), installs: filepath.Join(root, "installs")}
	write(t, filepath.Join(w.gameRoot, "baseq3", "pak0.pk3"), zipBytes(t, map[string]string{
		"textures/base/wall.tga": "the game's own", "default.cfg": "// the game's",
	}))
	write(t, filepath.Join(w.gameRoot, "baseq3", "q3config.cfg"), []byte("seta name \"somebody\"\n"))
	write(t, filepath.Join(w.gameRoot, "baseq3", "vm", "qagame.qvm"), []byte("vm"))
	write(t, filepath.Join(w.gameRoot, "missionpack", "pak0.pk3"), zipBytes(t, map[string]string{"x": "y"}))

	members := map[string]string{
		"maps/" + testMap + ".bsp": "IBSP the map",
		"textures/mine/floor.tga":  "mine",
		"textures/base/wall.tga":   "my replacement",
	}
	packageDir := filepath.Join(root, "package")
	archive := filepath.Join(packageDir, testArchive)
	write(t, archive, zipBytes(t, members))
	digest, size, err := hashFile(archive)
	if err != nil {
		t.Fatalf("%v", err)
	}
	plan := &q3pack.Plan{
		BuildID: "b-test", Map: q3pack.MapIdentity{Name: testMap},
		ArchiveName: testArchive, BaseGame: "baseq3", GameDir: gameDir,
	}
	for name := range members {
		plan.Members = append(plan.Members, q3pack.Member{Path: name})
	}
	w.record = &q3pack.Record{
		ID: "pkg-test", Plan: plan, Complete: true,
		Archive:     pack.ArchiveRef{File: testArchive, Size: size, SHA256: "sha256:" + digest},
		ArchivePath: archive, Directory: packageDir,
	}
	return w
}

func (w *world) request(kind Kind) Request {
	return Request{Package: w.record, Kind: kind, GameRoot: w.gameRoot, Dir: w.installs}
}

// snapshot is every path under a directory with its content, links not
// followed — what "the game folder was not touched" is checked against.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(dir, path)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, relative+" -> "+target)
		case info.IsDir():
			lines = append(lines, relative+"/")
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest, _, _ := hashFile(path)
			lines = append(lines, relative+" "+digest+" "+info.Mode().String()+" "+string(rune('0'+len(data)%10)))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	return strings.Join(lines, "\n")
}

// The default install never writes into the user's game folder: the engine is
// given a directory the Companion owns, and reads the game through links.
func TestAManagedInstallNeverWritesIntoTheGameFolder(t *testing.T) {
	w := newWorld(t, "baseq3")
	before := snapshot(t, w.gameRoot)
	installation, err := Install(context.Background(), w.request(Managed))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Fatalf("a managed install changed the game folder:\n%s\n---\n%s", before, after)
	}
	if installation.BasePath == w.gameRoot || !strings.HasPrefix(installation.BasePath, w.installs) {
		t.Errorf("the engine would be pointed at %s", installation.BasePath)
	}
	if installation.FSGame != "baseq3" {
		t.Errorf("fs_game is %q", installation.FSGame)
	}
	// The archive is a real file there, and the game's own files are links.
	game := filepath.Join(installation.BasePath, "baseq3")
	if info, err := os.Lstat(filepath.Join(game, testArchive)); err != nil || !info.Mode().IsRegular() {
		t.Errorf("the archive: %v %v", info, err)
	}
	for _, name := range []string{"pak0.pk3", "q3config.cfg", "vm"} {
		info, err := os.Lstat(filepath.Join(game, name))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a link in the managed directory: %v %v", name, info, err)
		}
	}
	// And they read through: the engine finds the game.
	if data, err := os.ReadFile(filepath.Join(game, "vm", "qagame.qvm")); err != nil || string(data) != "vm" {
		t.Errorf("the game is not readable through the managed directory: %q %v", data, err)
	}
	if info, err := os.Lstat(filepath.Join(installation.BasePath, "missionpack")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the other game directory is not one link: %v %v", info, err)
	}

	// Installing it again is the same installation.
	again, err := Install(context.Background(), w.request(Managed))
	if err != nil || again.ID != installation.ID {
		t.Fatalf("%v %v", err, again)
	}
	if err := Verify(installation); err != nil {
		t.Errorf("%v", err)
	}

	// Taking it away removes the Companion's directory and nothing of the game.
	if err := Remove(w.installs, installation.ID); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := os.Lstat(installation.Directory); !os.IsNotExist(err) {
		t.Errorf("the managed directory is still there: %v", err)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Fatalf("removing a managed install changed the game folder:\n%s", after)
	}
	if _, err := Load(w.installs, installation.ID); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("%v", err)
	}
}

// A mod the user does not have is a real directory in the managed base, and
// the base game is one link beside it.
func TestAManagedModInstallLinksTheBaseGameWhole(t *testing.T) {
	w := newWorld(t, "apmod")
	before := snapshot(t, w.gameRoot)
	installation, err := Install(context.Background(), w.request(Managed))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if installation.FSGame != "apmod" || installation.CreatedGameDir {
		t.Errorf("%+v", installation)
	}
	if info, err := os.Lstat(filepath.Join(installation.BasePath, "baseq3")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the base game is not one link: %v %v", info, err)
	}
	if info, err := os.Lstat(filepath.Join(installation.BasePath, "apmod", testArchive)); err != nil || !info.Mode().IsRegular() {
		t.Errorf("the archive: %v %v", info, err)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Fatal("a managed mod install changed the game folder")
	}
}

// The explicit target writes exactly one file, and removal takes exactly that
// file — while it is still that file.
func TestAGameFolderInstallIsOneFileAndIsRemovedOnlyWhileItIsThatFile(t *testing.T) {
	w := newWorld(t, "baseq3")
	before := snapshot(t, w.gameRoot)
	installation, err := Install(context.Background(), w.request(GameFolder))
	if err != nil {
		t.Fatalf("%v", err)
	}
	target := filepath.Join(w.gameRoot, "baseq3", testArchive)
	if installation.Archive.Path != target || installation.BasePath != w.gameRoot {
		t.Errorf("%+v", installation)
	}
	after := snapshot(t, w.gameRoot)
	added := strings.TrimSpace(strings.ReplaceAll(after, before, ""))
	if !strings.Contains(after, "baseq3/"+testArchive) || strings.Count(after, "\n") != strings.Count(before, "\n")+1 {
		t.Fatalf("a game folder install wrote more than one file:\n%s", added)
	}
	// Idempotent, through this program's own receipt.
	if again, err := Install(context.Background(), w.request(GameFolder)); err != nil || again.ID != installation.ID {
		t.Fatalf("%v", err)
	}
	if err := Remove(w.installs, installation.ID); err != nil {
		t.Fatalf("%v", err)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Fatalf("removal did not restore the game folder:\n%s", after)
	}

	// Installed, then replaced by somebody: left alone, and said.
	installation, err = Install(context.Background(), w.request(GameFolder))
	if err != nil {
		t.Fatalf("%v", err)
	}
	write(t, target, []byte("somebody's own archive now"))
	if err := Verify(installation); err == nil {
		t.Error("a replaced archive verified")
	}
	if err := Remove(w.installs, installation.ID); err == nil || !strings.Contains(err.Error(), "left alone") {
		t.Fatalf("a file that changed since it was installed: %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "somebody's own archive now" {
		t.Error("somebody's file was removed")
	}
}

// A file that is already there is never written over.
func TestAnExistingArchiveOfThatNameIsNotWrittenOver(t *testing.T) {
	for _, kind := range []Kind{Managed, GameFolder} {
		w := newWorld(t, "baseq3")
		theirs := filepath.Join(w.gameRoot, "baseq3", testArchive)
		write(t, theirs, []byte("theirs"))
		before := snapshot(t, w.gameRoot)
		_, err := Install(context.Background(), w.request(kind))
		if failure.Of(err) != failure.InstallConflict {
			t.Fatalf("%s: %v", kind, err)
		}
		if after := snapshot(t, w.gameRoot); after != before {
			t.Errorf("%s: a refused install changed the game folder", kind)
		}
		if entries, _ := os.ReadDir(w.installs); len(entries) != 0 {
			t.Errorf("%s: a refused install left a record", kind)
		}
	}
}

// A mod directory this install made is removed with it, unless somebody has
// put something in it since.
func TestAModDirectoryMadeByTheInstallGoesWithItOnlyWhenEmpty(t *testing.T) {
	w := newWorld(t, "apmod")
	before := snapshot(t, w.gameRoot)
	installation, err := Install(context.Background(), w.request(GameFolder))
	if err != nil || !installation.CreatedGameDir {
		t.Fatalf("%v %+v", err, installation)
	}
	if err := Remove(w.installs, installation.ID); err != nil {
		t.Fatalf("%v", err)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Fatalf("the mod directory was not removed:\n%s", after)
	}

	installation, err = Install(context.Background(), w.request(GameFolder))
	if err != nil {
		t.Fatalf("%v", err)
	}
	mine := filepath.Join(w.gameRoot, "apmod", "autoexec.cfg")
	write(t, mine, []byte("bind x say hi\n"))
	if err := Remove(w.installs, installation.ID); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Error("a file somebody added to the mod directory was removed with the install")
	}
}

// Load order, on the rules measured on ioquake3: a loose file beats every
// archive, the later archive name wins without regard to case.
func TestAMapThatSomethingElseWouldSupplyIsNotInstalled(t *testing.T) {
	bsp := "maps/" + testMap + ".bsp"
	cases := map[string]struct {
		file    string
		content []byte
		refused bool
	}{
		"a later archive":           {"baseq3/zz_other.pk3", nil, true},
		"a later archive, in caps":  {"baseq3/ZZ_OTHER.PK3", nil, true},
		"pak9, which sorts after a": {"baseq3/pak9.pk3", nil, true},
		"an earlier archive":        {"baseq3/aaa_other.pk3", nil, false},
		"a loose file":              {"baseq3/maps/" + testMap + ".bsp", []byte("loose"), true},
		"a loose file, in caps":     {"baseq3/MAPS/" + strings.ToUpper(testMap) + ".BSP", []byte("loose"), true},
	}
	for name, c := range cases {
		for _, kind := range []Kind{Managed, GameFolder} {
			w := newWorld(t, "baseq3")
			content := c.content
			if content == nil {
				content = zipBytes(t, map[string]string{"MAPS/" + strings.ToUpper(testMap) + ".bsp": "another map"})
			}
			write(t, filepath.Join(w.gameRoot, filepath.FromSlash(c.file)), content)
			before := snapshot(t, w.gameRoot)
			installation, err := Install(context.Background(), w.request(kind))
			if c.refused {
				if failure.Of(err) != failure.LoadOrderShadowed || !strings.Contains(err.Error(), bsp) {
					t.Errorf("%s (%s): %v", name, kind, err)
				}
				if after := snapshot(t, w.gameRoot); after != before {
					t.Errorf("%s (%s): a refused install changed the game folder", name, kind)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s (%s): %v", name, kind, err)
				continue
			}
			if len(installation.LoadOrder.Overrides) == 0 || installation.LoadOrder.Overrides[0].Path != bsp {
				t.Errorf("%s (%s): the map hides another archive's and that is not said: %+v", name, kind, installation.LoadOrder)
			}
		}
	}
}

// Anything else the package would lose to is reported by name, and the install
// goes ahead: `pak0.pk3` sorts after `auto-pigeon-…`, so the game's own file of
// the same path is the one the engine draws.
func TestAMemberTheBaseGameAlsoSuppliesIsReported(t *testing.T) {
	w := newWorld(t, "baseq3")
	installation, err := Install(context.Background(), w.request(Managed))
	if err != nil {
		t.Fatalf("%v", err)
	}
	shadowed := installation.LoadOrder.Shadowed
	if len(shadowed) != 1 || shadowed[0].Path != "textures/base/wall.tga" || shadowed[0].By != "pak0.pk3" {
		t.Errorf("shadowed: %+v", shadowed)
	}
	if joined := strings.Join(installation.LoadOrder.Searched, ","); !strings.Contains(joined, "pak0.pk3") {
		t.Errorf("searched before: %v", installation.LoadOrder.Searched)
	}
	// In a mod directory nothing of the base game is searched first.
	w = newWorld(t, "apmod")
	installation, err = Install(context.Background(), w.request(Managed))
	if err != nil || len(installation.LoadOrder.Shadowed) != 0 {
		t.Errorf("%v %+v", err, installation)
	}
}

// A cancelled install leaves nothing: no archive, no temporary file, no
// directory, no record.
func TestACancelledInstallLeavesNothing(t *testing.T) {
	for _, kind := range []Kind{Managed, GameFolder} {
		for _, game := range []string{"baseq3", "apmod"} {
			w := newWorld(t, game)
			before := snapshot(t, w.gameRoot)
			ctx, cancel := context.WithCancel(context.Background())
			request := w.request(kind)
			request.afterCopy = cancel
			_, err := Install(ctx, request)
			if failure.Of(err) != failure.Cancelled {
				t.Fatalf("%s/%s: %v", kind, game, err)
			}
			if after := snapshot(t, w.gameRoot); after != before {
				t.Errorf("%s/%s: a cancelled install left something in the game folder:\n%s", kind, game, after)
			}
			if entries, _ := os.ReadDir(w.installs); len(entries) != 0 {
				t.Errorf("%s/%s: a cancelled install left %d entries behind", kind, game, len(entries))
			}
			cancel()
		}
	}
}

// What is refused before anything is touched.
func TestWhatAnInstallRefuses(t *testing.T) {
	w := newWorld(t, "baseq3")
	request := w.request(Managed)
	request.GameRoot = filepath.Join(w.gameRoot, "nowhere")
	if _, err := Install(context.Background(), request); failure.Of(err) != failure.GameDataMissing {
		t.Errorf("a game folder that is not there: %v", err)
	}
	request = w.request(GameFolder)
	request.GameRoot = filepath.Join(w.gameRoot, "baseq3") // the base game itself, not the folder that contains it
	if _, err := Install(context.Background(), request); failure.Of(err) != failure.GameDataMissing {
		t.Errorf("the base game directory given as the game folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.gameRoot, "baseq3", "baseq3")); err == nil {
		t.Error("a base game directory was created")
	}
	for _, name := range []string{"..", "a/b", ".hidden"} {
		request = w.request(Managed)
		request.Game = name
		if _, err := Install(context.Background(), request); failure.Of(err) != failure.FSGameInvalid {
			t.Errorf("the game directory %q: %v", name, err)
		}
	}
	request = w.request("somewhere")
	if _, err := Install(context.Background(), request); err == nil {
		t.Error("an unknown target was accepted")
	}
	if _, err := Load(w.installs, "../x"); err == nil {
		t.Error("an installation id that is a path was read")
	}
}

// A preview refuses what an install refuses and writes nothing.
func TestAPreviewWritesNothing(t *testing.T) {
	w := newWorld(t, "baseq3")
	before := snapshot(t, w.gameRoot)
	preview, err := Preview(w.request(GameFolder))
	if err != nil || preview.Archive.Path != filepath.Join(w.gameRoot, "baseq3", testArchive) {
		t.Fatalf("%v %+v", err, preview)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Error("a preview changed the game folder")
	}
	if _, err := os.Stat(w.installs); !os.IsNotExist(err) {
		t.Error("a preview recorded something")
	}
}
