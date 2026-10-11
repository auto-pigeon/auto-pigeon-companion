package q3install

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// fileCache is a content-addressed cache: digest → the file that holds it.
type fileCache map[string]string

func (c fileCache) Has(digest string) bool {
	_, err := os.Stat(c[digest])
	return c[digest] != "" && err == nil
}

func (c fileCache) Object(digest string) (string, error) {
	if c.Has(digest) {
		return c[digest], nil
	}
	return "", errors.New("not cached")
}

func (c fileCache) Publish(io.Reader, string, int64) (string, error) {
	return "", errors.New("an install fetches nothing")
}

// bind makes the world's package say the saved map is bound to an archive
// called `name` under the build's `game` folder, puts that archive in a cache,
// and returns the cache and the archive's digest.
func (w *world) bind(cache fileCache, game, name string, members map[string]string) string {
	w.t.Helper()
	data := zipBytes(w.t, members)
	object := filepath.Join(filepath.Dir(w.installs), "cache", name+".object")
	write(w.t, object, data)
	digest, size, err := hashFile(object)
	if err != nil {
		w.t.Fatalf("%v", err)
	}
	cache[digest] = object
	w.record.Plan.Sources = append(w.record.Plan.Sources, q3pack.Source{
		ID: "archive:" + digest, Kind: "archive", Name: name, Game: game, SHA256: digest, Size: size,
		Origin: q3vfs.OriginCache, RuntimeFiles: len(members),
	})
	return digest
}

func (w *world) bound(kind Kind, cache fileCache) Request {
	request := w.request(kind)
	request.Cache = cache
	return request
}

// dotNames are the entries of an installs directory that are not installations:
// a generation being built, or one that was replaced.
func dotNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			out = append(out, entry.Name())
		}
	}
	return out
}

// The defect `Q3_017A` closes. A managed install of a map whose build read a
// bound package puts that package in the managed base directory — under the
// folder and the name the binding gives, with the bytes the build verified —
// and writes nothing into the game folder or the cache to do it.
func TestAManagedInstallStagesTheBoundPackageIntoACleanOverlay(t *testing.T) {
	w := newWorld(t, "baseq3")
	w.record.Complete = false
	w.record.Plan.NotCarried = []string{"textures/bound/wall.tga", "scripts/bound.shader", "textures/nowhere/gone.tga"}
	cache := fileCache{}
	digest := w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{
		"textures/bound/wall.tga": "bound", "scripts/bound.shader": "textures/bound/wall { }",
	})
	before := snapshot(t, w.gameRoot)
	cached := snapshot(t, filepath.Dir(cache[digest]))

	preview, err := Preview(w.bound(Managed, cache))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(preview.Packages) != 1 || preview.Packages[0].State != PackageStaged {
		t.Fatalf("a preview does not say the package would be staged: %+v", preview.Packages)
	}
	if _, err := os.Stat(w.installs); !os.IsNotExist(err) {
		t.Error("a preview wrote something")
	}

	installation, err := Install(context.Background(), w.bound(Managed, cache))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if installation.SchemaVersion != SchemaVersion || len(installation.Packages) != 1 {
		t.Fatalf("%+v", installation)
	}
	pkg := installation.Packages[0]
	want := filepath.Join(installation.BasePath, "baseq3", "zz_bound.pk3")
	if pkg.State != PackageStaged || pkg.Path != want || pkg.SHA256 != digest || pkg.Game != "baseq3" ||
		pkg.Origin != q3vfs.OriginCache || pkg.RuntimeFiles != 2 || (pkg.Method != "hardlink" && pkg.Method != "copy") {
		t.Errorf("the staged package: %+v", pkg)
	}
	if info, err := os.Lstat(want); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the bound package is not a file in the managed base: %v %v", info, err)
	}
	if found, size, _ := hashFile(want); found != digest || size != pkg.Size {
		t.Errorf("the staged file is %s, %d bytes", found, size)
	}
	// The engine's directory holds the game through links, the map and the package.
	if _, err := os.Stat(filepath.Join(installation.BasePath, "baseq3", "pak0.pk3")); err != nil {
		t.Errorf("the game is not readable beside the package: %v", err)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Fatalf("staging a bound package changed the game folder:\n%s", after)
	}
	if installation.SuppliedByPackages != 2 {
		t.Errorf("%d of the 3 files the archive does not carry are said to be in the package", installation.SuppliedByPackages)
	}
	if err := Verify(installation); err != nil {
		t.Errorf("%v", err)
	}
	// Recorded, and read back.
	loaded, err := Load(w.installs, installation.ID)
	if err != nil || len(loaded.Packages) != 1 || loaded.Packages[0].Path != want {
		t.Fatalf("%v %+v", err, loaded)
	}
	// The same install again is the same installation, not a second one.
	again, err := Install(context.Background(), w.bound(Managed, cache))
	if err != nil || again.ID != installation.ID || !again.InstalledAt.Equal(installation.InstalledAt) {
		t.Errorf("%v %+v", err, again)
	}
	if left := dotNames(t, w.installs); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}

	// Taking it away removes this program's directory — and neither the game
	// nor the cached package the staged file stood for.
	if err := Remove(w.installs, installation.ID); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := os.Lstat(installation.Directory); !os.IsNotExist(err) {
		t.Errorf("the managed directory is still there: %v", err)
	}
	if after := snapshot(t, filepath.Dir(cache[digest])); after != cached {
		t.Errorf("removing the install changed the cache:\n%s", after)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Errorf("removing the install changed the game folder")
	}
}

// The name is part of what was bound: it decides what the engine reads first.
// Each staged package is placed by the engine's own rule — later name first,
// without regard to case, loose before all — and every path it loses or hides
// is named.
func TestAStagedPackageTakesTheEnginesPrecedenceAndConflictsAreNamed(t *testing.T) {
	w := newWorld(t, "baseq3")
	write(t, filepath.Join(w.gameRoot, "baseq3", "textures", "base", "loose.tga"), []byte("loose"))
	cache := fileCache{}
	w.bind(cache, "baseq3", "zz_late.pk3", map[string]string{
		"textures/base/wall.tga": "late", "textures/mine/floor.tga": "late floor", "textures/base/LOOSE.tga": "late loose",
	})
	w.bind(cache, "baseq3", "AA_early.pk3", map[string]string{
		"textures/base/wall.tga": "early", "default.cfg": "// early",
	})
	installation, err := Install(context.Background(), w.bound(Managed, cache))
	if err != nil {
		t.Fatalf("%v", err)
	}
	late, early := installation.Packages[0], installation.Packages[1]

	// `zz_late` is searched before everything but loose files…
	if strings.Join(late.Searched, ",") != "loose files in baseq3" {
		t.Errorf("searched before zz_late.pk3: %v", late.Searched)
	}
	// …so it loses only to the loose file, and hides the game's, the map's and
	// the other package's.
	if len(late.Shadowed) != 1 || late.Shadowed[0].Path != "textures/base/LOOSE.tga" ||
		late.Shadowed[0].By != "the loose file textures/base/loose.tga" || late.ShadowedCount != 1 {
		t.Errorf("zz_late.pk3 loses: %+v", late.Shadowed)
	}
	hides := map[string]bool{}
	for _, shadow := range late.Overrides {
		hides[shadow.Path+" in "+shadow.By] = true
	}
	for _, want := range []string{
		"textures/base/wall.tga in pak0.pk3", "textures/base/wall.tga in AA_early.pk3",
		"textures/base/wall.tga in " + testArchive, "textures/mine/floor.tga in " + testArchive,
	} {
		if !hides[want] {
			t.Errorf("zz_late.pk3 is not said to hide %s: %+v", want, late.Overrides)
		}
	}
	if late.OverridesCount != len(late.Overrides) {
		t.Errorf("counted %d, listed %d", late.OverridesCount, len(late.Overrides))
	}
	// `AA_early` sorts before `auto-pigeon-…` without regard to case: everything
	// is searched before it, and both its files are taken from elsewhere.
	if joined := strings.Join(early.Searched, ","); joined != "loose files in baseq3,zz_late.pk3,pak0.pk3,"+testArchive {
		t.Errorf("searched before AA_early.pk3: %s", joined)
	}
	lost := map[string]string{}
	for _, shadow := range early.Shadowed {
		lost[shadow.Path] = shadow.By
	}
	if lost["textures/base/wall.tga"] != "zz_late.pk3" || lost["default.cfg"] != "pak0.pk3" || len(early.Overrides) != 0 {
		t.Errorf("AA_early.pk3: loses %+v, hides %+v", early.Shadowed, early.Overrides)
	}
	// The map's own archive: the staged package named after it is searched
	// before it and wins the paths they share, by name.
	if joined := strings.Join(installation.LoadOrder.Searched, ","); joined != "loose files in baseq3,zz_late.pk3,pak0.pk3" {
		t.Errorf("searched before the map's archive: %s", joined)
	}
	won := map[string]string{}
	for _, shadow := range installation.LoadOrder.Shadowed {
		won[shadow.Path] = shadow.By
	}
	if won["textures/base/wall.tga"] != "zz_late.pk3" || won["textures/mine/floor.tga"] != "zz_late.pk3" {
		t.Errorf("the map's archive loses: %+v", installation.LoadOrder.Shadowed)
	}
	if joined := strings.Join(installation.LoadOrder.Limits, "\n"); !strings.Contains(joined, "Q3Map2") {
		t.Errorf("the limits do not say whose order this is: %s", joined)
	}
}

// A bound package that would supply the MAP is the refusal it always was: the
// engine would load another map and say nothing.
func TestABoundPackageThatSuppliesTheMapIsRefused(t *testing.T) {
	w := newWorld(t, "baseq3")
	cache := fileCache{}
	w.bind(cache, "baseq3", "zz_other.pk3", map[string]string{"MAPS/" + strings.ToUpper(testMap) + ".bsp": "another map"})
	_, err := Install(context.Background(), w.bound(Managed, cache))
	if failure.Of(err) != failure.LoadOrderShadowed || !strings.Contains(err.Error(), "zz_other.pk3") {
		t.Fatalf("%v", err)
	}
	if entries, _ := os.ReadDir(w.installs); len(entries) != 0 {
		t.Errorf("a refused install left %d entries", len(entries))
	}
}

// A package bound under the base game, for a map that goes into a mod: the
// base directory becomes a real directory of links so the package can sit in
// it, and the user's base game is still not written.
func TestAPackageBoundUnderTheBaseGameIsStagedThereWhenTheMapGoesIntoAMod(t *testing.T) {
	w := newWorld(t, "apmod")
	w.record.Plan.FSGame = "apmod"
	cache := fileCache{}
	w.bind(cache, "baseq3", "zz_base.pk3", map[string]string{"textures/x.tga": "x"})
	w.bind(cache, "apmod", "zz_mod.pk3", map[string]string{"textures/y.tga": "y"})
	before := snapshot(t, w.gameRoot)
	installation, err := Install(context.Background(), w.bound(Managed, cache))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Fatal("the game folder changed")
	}
	base := filepath.Join(installation.BasePath, "baseq3")
	if info, err := os.Lstat(base); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the base game is not a real directory in the managed base: %v %v", info, err)
	}
	for _, name := range []string{"zz_base.pk3", "pak0.pk3", "vm/qagame.qvm"} {
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(name))); err != nil {
			t.Errorf("baseq3/%s: %v", name, err)
		}
	}
	for _, name := range []string{"zz_mod.pk3", testArchive} {
		if info, err := os.Lstat(filepath.Join(installation.BasePath, "apmod", name)); err != nil || !info.Mode().IsRegular() {
			t.Errorf("apmod/%s: %v", name, err)
		}
	}
	if installation.Packages[0].Game != "baseq3" || installation.Packages[1].Game != "apmod" {
		t.Errorf("%+v", installation.Packages)
	}
	if strings.Join(installation.Packages[0].Searched, ",") != "loose files in baseq3" {
		t.Errorf("searched before zz_base.pk3: %v", installation.Packages[0].Searched)
	}
	// Another game beside them is still one link.
	assertLinked(t, filepath.Join(installation.BasePath, "missionpack"), filepath.Join(w.gameRoot, "missionpack"), symlinksAllowed(t))
}

// The file in the cache is checked against the digest AND the size the build
// recorded, at the moment it is installed. Bytes that are not the bound
// package's are never given to the engine, and nothing is left behind.
func TestABoundPackageThatIsNotTheBytesTheBuildReadIsRefused(t *testing.T) {
	// The same size, other bytes: only hashing tells.
	w := newWorld(t, "baseq3")
	cache := fileCache{}
	digest := w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "aaaa"})
	write(t, cache[digest], zipBytes(t, map[string]string{"textures/a.tga": "bbbb"}))
	before := snapshot(t, w.gameRoot)
	_, err := Install(context.Background(), w.bound(Managed, cache))
	if failure.Of(err) != failure.ArchiveDamaged || !strings.Contains(err.Error(), "zz_bound.pk3") || !strings.Contains(err.Error(), digest) {
		t.Fatalf("other bytes of the same size: %v", err)
	}
	if entries, _ := os.ReadDir(w.installs); len(entries) != 0 {
		t.Errorf("a refused install left %d entries behind", len(entries))
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Error("a refused install changed the game folder")
	}

	// Another size: said by a preview already, before anything is written.
	w = newWorld(t, "baseq3")
	cache = fileCache{}
	digest = w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "aaaa"})
	write(t, cache[digest], zipBytes(t, map[string]string{"textures/a.tga": "a longer file"}))
	if _, err := Preview(w.bound(Managed, cache)); failure.Of(err) != failure.ArchiveDamaged {
		t.Errorf("a preview of a package of another size: %v", err)
	}
	if _, err := Install(context.Background(), w.bound(Managed, cache)); failure.Of(err) != failure.ArchiveDamaged {
		t.Errorf("an install of a package of another size: %v", err)
	}

	// A record that names no digest names no package.
	w = newWorld(t, "baseq3")
	cache = fileCache{}
	w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "a"})
	w.record.Plan.Sources[0].SHA256 = "not-a-digest"
	if _, err := Preview(w.bound(Managed, cache)); failure.Of(err) != failure.ContentRefused {
		t.Errorf("a source with no digest: %v", err)
	}
	// And a name that is a path is never joined onto a directory.
	for _, name := range []string{"../zz.pk3", "a/b.pk3", ".hidden.pk3", "zz;rm -rf.pk3", "zz.zip"} {
		w = newWorld(t, "baseq3")
		cache = fileCache{}
		w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "a"})
		w.record.Plan.Sources[0].Name = name
		if _, err := Install(context.Background(), w.bound(Managed, cache)); failure.Of(err) != failure.ContentRefused {
			t.Errorf("the archive name %q: %v", name, err)
		}
		if entries, _ := os.ReadDir(w.installs); len(entries) != 0 {
			t.Errorf("the archive name %q left %d entries", name, len(entries))
		}
	}
}

// A bound package that is not on this machine is said by name BEFORE anything
// claims an engine could be started: by a preview, by an install, and — for one
// that went missing afterwards — by the check a run makes.
func TestAnAbsentBoundPackageIsNamedBeforeAnythingIsInstalled(t *testing.T) {
	w := newWorld(t, "baseq3")
	cache := fileCache{}
	first := w.bind(cache, "baseq3", "zz_first.pk3", map[string]string{"textures/a.tga": "a"})
	second := w.bind(cache, "baseq3", "zz_second.pk3", map[string]string{"textures/b.tga": "b"})
	delete(cache, first)
	delete(cache, second)
	before := snapshot(t, w.gameRoot)
	for name, attempt := range map[string]func() error{
		"preview":                func() error { _, err := Preview(w.bound(Managed, cache)); return err },
		"install":                func() error { _, err := Install(context.Background(), w.bound(Managed, cache)); return err },
		"install with no cache":  func() error { _, err := Install(context.Background(), w.request(Managed)); return err },
		"preview with no cache ": func() error { _, err := Preview(w.request(Managed)); return err },
	} {
		err := attempt()
		if failure.Of(err) != failure.GameDataMissing {
			t.Fatalf("%s: %v", name, err)
		}
		for _, want := range []string{"zz_first.pk3", first, "zz_second.pk3", second, "Build the map again"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s does not say %q: %v", name, want, err)
			}
		}
		if _, statErr := os.Stat(filepath.Join(w.installs, plannedID(t, w))); !os.IsNotExist(statErr) {
			t.Errorf("%s left an installation", name)
		}
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Error("a refused install changed the game folder")
	}

	// One that is there is staged; taken away afterwards, the run's own check
	// refuses before an engine is started.
	w = newWorld(t, "baseq3")
	cache = fileCache{}
	w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "a"})
	installation, err := Install(context.Background(), w.bound(Managed, cache))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.Remove(installation.Packages[0].Path); err != nil {
		t.Fatalf("%v", err)
	}
	if err := Verify(installation); failure.Of(err) != failure.GameDataMissing || !strings.Contains(err.Error(), "zz_bound.pk3") {
		t.Errorf("a staged package that is gone: %v", err)
	}
	write(t, installation.Packages[0].Path, []byte("something else"))
	if err := Verify(installation); failure.Of(err) != failure.ArchiveDamaged {
		t.Errorf("a staged package that was replaced: %v", err)
	}
	// Installing again puts it right, in place.
	repaired, err := Install(context.Background(), w.bound(Managed, cache))
	if err != nil || repaired.ID != installation.ID || Verify(repaired) != nil {
		t.Fatalf("%v %+v", err, repaired)
	}
	if left := dotNames(t, w.installs); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func plannedID(t *testing.T, w *world) string {
	t.Helper()
	digest := hexDigest(w.record.Archive.SHA256)
	root, err := filepath.Abs(w.gameRoot)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return installID(Managed, root, w.record.Plan.GameDir, w.record.Archive.File, digest)
}

// An install that is cancelled, or fails, while an installation of the same
// package is already there leaves THAT one as it was: listed, verified, and
// what an engine can still be started on. A retry then replaces it.
func TestACancelledOrFailedReinstallLeavesThePreviousInstallationUsable(t *testing.T) {
	w := newWorld(t, "baseq3")
	// The installation an earlier build of this program made: the map, and
	// nothing about bound packages.
	previous, err := Install(context.Background(), w.request(Managed))
	if err != nil {
		t.Fatalf("%v", err)
	}
	earlier := snapshot(t, previous.Directory)
	cache := fileCache{}
	digest := w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "aaaa"})
	good, err := os.ReadFile(cache[digest])
	if err != nil {
		t.Fatalf("%v", err)
	}
	intact := func(why string) {
		t.Helper()
		loaded, err := Load(w.installs, previous.ID)
		if err != nil {
			t.Fatalf("%s: the previous installation is not readable: %v", why, err)
		}
		if err := Verify(loaded); err != nil {
			t.Errorf("%s: the previous installation does not verify: %v", why, err)
		}
		if now := snapshot(t, previous.Directory); now != earlier {
			t.Errorf("%s: the previous installation changed:\n%s\n---\n%s", why, earlier, now)
		}
		if list, _ := List(w.installs); len(list) != 1 || list[0].ID != previous.ID {
			t.Errorf("%s: installations listed: %+v", why, list)
		}
		if left := dotNames(t, w.installs); len(left) != 0 {
			t.Errorf("%s: left behind: %v", why, left)
		}
	}

	// Cancelled when the new generation is all but built.
	ctx, cancel := context.WithCancel(context.Background())
	request := w.bound(Managed, cache)
	request.afterCopy = cancel
	if _, err := Install(ctx, request); failure.Of(err) != failure.Cancelled {
		t.Fatalf("%v", err)
	}
	cancel()
	intact("cancelled")

	// Cancelled before it starts.
	if _, err := Install(ctx, w.bound(Managed, cache)); failure.Of(err) != failure.Cancelled {
		t.Fatalf("%v", err)
	}
	intact("cancelled before it started")

	// Failed: the cached package is not the bound one.
	write(t, cache[digest], zipBytes(t, map[string]string{"textures/a.tga": "bbbb"}))
	if _, err := Install(context.Background(), w.bound(Managed, cache)); failure.Of(err) != failure.ArchiveDamaged {
		t.Fatalf("%v", err)
	}
	intact("failed")

	// Retried with the right bytes: the same installation, now with the package.
	write(t, cache[digest], good)
	installation, err := Install(context.Background(), w.bound(Managed, cache))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if installation.ID != previous.ID || len(installation.Packages) != 1 || installation.Packages[0].State != PackageStaged {
		t.Fatalf("%+v", installation)
	}
	if err := Verify(installation); err != nil {
		t.Errorf("%v", err)
	}
	if list, _ := List(w.installs); len(list) != 1 {
		t.Errorf("installations listed: %d", len(list))
	}
	if entries, _ := os.ReadDir(w.installs); len(entries) != 1 {
		t.Errorf("the installs directory holds %d entries", len(entries))
	}
}

// A record written before bound packages were staged is still an installation:
// it is read, verified and removed.
func TestAnInstallationRecordedBeforePackagesWereStagedIsStillRead(t *testing.T) {
	w := newWorld(t, "baseq3")
	installation, err := Install(context.Background(), w.request(Managed))
	if err != nil {
		t.Fatalf("%v", err)
	}
	record := filepath.Join(installation.Directory, RecordFileName)
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("%v", err)
	}
	write(t, record, []byte(strings.Replace(string(data), SchemaVersion, schemaVersion10, 1)))
	loaded, err := Load(w.installs, installation.ID)
	if err != nil || loaded.SchemaVersion != schemaVersion10 {
		t.Fatalf("%v %+v", err, loaded)
	}
	if err := Verify(loaded); err != nil {
		t.Errorf("%v", err)
	}
	write(t, record, []byte(strings.Replace(string(data), SchemaVersion, "aucom.q3-install/9.0", 1)))
	if _, err := Load(w.installs, installation.ID); err == nil {
		t.Error("a record in a schema this build does not know was read")
	}
	write(t, record, data)
	if err := Remove(w.installs, installation.ID); err != nil {
		t.Errorf("%v", err)
	}
}

// What the user's own game folder already holds under the bound name. The same
// bytes are the package, and nothing is added; other bytes are two different
// archives wanting one name, and the engine is not given the wrong one.
func TestAFileOfTheBoundNameInTheGameFolder(t *testing.T) {
	w := newWorld(t, "baseq3")
	cache := fileCache{}
	digest := w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "a"})
	data, err := os.ReadFile(cache[digest])
	if err != nil {
		t.Fatalf("%v", err)
	}
	// The same bytes, under the name in other capitals.
	theirs := filepath.Join(w.gameRoot, "baseq3", "ZZ_Bound.pk3")
	write(t, theirs, data)
	installation, err := Install(context.Background(), w.request(Managed)) // no cache is needed for it
	if err != nil {
		t.Fatalf("%v", err)
	}
	pkg := installation.Packages[0]
	if pkg.State != PackageInGameFolder || pkg.Path != filepath.Join(installation.BasePath, "baseq3", "ZZ_Bound.pk3") {
		t.Errorf("%+v", pkg)
	}
	assertLinked(t, pkg.Path, theirs, symlinksAllowed(t))
	if _, err := os.Lstat(filepath.Join(installation.BasePath, "baseq3", "zz_bound.pk3")); !os.IsNotExist(err) && symlinksAllowed(t) {
		t.Error("the package was staged beside the user's own copy of it")
	}
	if err := Verify(installation); err != nil {
		t.Errorf("%v", err)
	}

	// Other bytes.
	w = newWorld(t, "baseq3")
	cache = fileCache{}
	w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "a"})
	write(t, filepath.Join(w.gameRoot, "baseq3", "zz_bound.pk3"), zipBytes(t, map[string]string{"textures/a.tga": "another"}))
	before := snapshot(t, w.gameRoot)
	_, err = Install(context.Background(), w.bound(Managed, cache))
	if failure.Of(err) != failure.InstallConflict || !strings.Contains(err.Error(), "zz_bound.pk3") {
		t.Fatalf("%v", err)
	}
	if after := snapshot(t, w.gameRoot); after != before {
		t.Error("the user's file was touched")
	}
	// A bound package with the map's own archive name.
	w = newWorld(t, "baseq3")
	cache = fileCache{}
	w.bind(cache, "baseq3", strings.ToUpper(testArchive[:len(testArchive)-4])+".pk3", map[string]string{"textures/a.tga": "a"})
	if _, err := Install(context.Background(), w.bound(Managed, cache)); failure.Of(err) != failure.InstallConflict {
		t.Errorf("a bound package named like the map's archive: %v", err)
	}
	// Two different packages bound under one name.
	w = newWorld(t, "baseq3")
	cache = fileCache{}
	w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "a"})
	w.bind(cache, "baseq3", "ZZ_BOUND.pk3", map[string]string{"textures/a.tga": "b"})
	if _, err := Install(context.Background(), w.bound(Managed, cache)); failure.Of(err) != failure.ContentRefused {
		t.Errorf("two packages bound under one name: %v", err)
	}
}

// A game-folder install keeps its contract: ONE file. It does not copy a bound
// package into the user's game, and it says — by name, with the digest and
// where it would have to be — that the engine will not find it.
func TestAGameFolderInstallDoesNotCopyABoundPackageAndSaysSo(t *testing.T) {
	w := newWorld(t, "baseq3")
	cache := fileCache{}
	digest := w.bind(cache, "baseq3", "zz_bound.pk3", map[string]string{"textures/a.tga": "a"})
	before := snapshotEntries(t, w.gameRoot)
	installation, err := Install(context.Background(), w.bound(GameFolder, cache))
	if err != nil {
		t.Fatalf("%v", err)
	}
	after := snapshotEntries(t, w.gameRoot)
	var added []string
	for name := range after {
		if _, was := before[name]; !was {
			added = append(added, name)
		}
	}
	if len(added) != 1 || !strings.HasSuffix(added[0], testArchive) {
		t.Fatalf("a game-folder install added %v", added)
	}
	pkg := installation.Packages[0]
	where := filepath.Join(w.gameRoot, "baseq3", "zz_bound.pk3")
	if pkg.State != PackageNotStaged || pkg.Path != where || !strings.Contains(pkg.Reason, digest) || !strings.Contains(pkg.Reason, "managed") {
		t.Errorf("%+v", pkg)
	}
	if got := installation.NotStaged(); len(got) != 1 || got[0] != "baseq3/zz_bound.pk3" {
		t.Errorf("not staged: %v", got)
	}
	// It is not a refusal to run, and not a claim either.
	if err := Verify(installation); err != nil {
		t.Errorf("%v", err)
	}
	// Once the user has put that archive there, the same install says so.
	data, err := os.ReadFile(cache[digest])
	if err != nil {
		t.Fatalf("%v", err)
	}
	write(t, where, data)
	again, err := Install(context.Background(), w.bound(GameFolder, cache))
	if err != nil || again.ID != installation.ID {
		t.Fatalf("%v", err)
	}
	if again.Packages[0].State != PackageInGameFolder || len(again.NotStaged()) != 0 {
		t.Errorf("%+v", again.Packages)
	}
	if loaded, err := Load(w.installs, again.ID); err != nil || loaded.Packages[0].State != PackageInGameFolder {
		t.Errorf("the record still says it is not there: %v", err)
	}
	// Removing the install removes the map's archive, and not the user's file.
	if err := Remove(w.installs, again.ID); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(where); err != nil {
		t.Error("the user's copy of the bound package went with the install")
	}
}

// A generation that was replaced, or half built by a process that died, is
// this program's own and is taken away by the next install; one that is being
// built right now is somebody else's and is left.
func TestObsoleteGenerationsAreSweptAndALiveOneIsNot(t *testing.T) {
	w := newWorld(t, "baseq3")
	retired := filepath.Join(w.installs, retiredPrefix+"old")
	stale := filepath.Join(w.installs, buildingPrefix+"dead")
	live := filepath.Join(w.installs, buildingPrefix+"live")
	for _, dir := range []string{retired, stale, live} {
		write(t, filepath.Join(dir, "base", "baseq3", "x.pk3"), []byte("x"))
	}
	installation, err := Install(context.Background(), w.request(Managed))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Error("a retired generation was left")
	}
	for _, dir := range []string{stale, live} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s, which is younger than %s, was removed", filepath.Base(dir), staleBuilding)
		}
	}
	if list, _ := List(w.installs); len(list) != 1 || list[0].ID != installation.ID {
		t.Errorf("a generation is listed as an installation: %+v", list)
	}
	// Past the bound, the dead one goes.
	w2 := w.request(Managed)
	w2.Now = func() (now time.Time) { return time.Now().Add(2 * staleBuilding) }
	if err := Remove(w.installs, installation.ID); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := Install(context.Background(), w2); err != nil {
		t.Fatalf("%v", err)
	}
	if left := dotNames(t, w.installs); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}
