package q2install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2pack"
)

type machine struct {
	gameRoot, records string
	store             q2pack.Store
	t                 *testing.T
}

// newMachine is a Quake II game folder — `baseq2` with an authored pak0 — and
// a Companion's package store and installation records.
func newMachine(t *testing.T) *machine {
	t.Helper()
	root := t.TempDir()
	game := filepath.Join(root, "Quake II")
	pak := q2fixture.PAK([]string{"pics/colormap.pcx"}, map[string][]byte{"pics/colormap.pcx": q2fixture.PCX()})
	if _, err := q2fixture.Write(filepath.Join(game, "baseq2"), map[string][]byte{"pak0.pak": pak}); err != nil {
		t.Fatal(err)
	}
	return &machine{gameRoot: game, records: filepath.Join(root, "installs"), store: q2pack.Store{Dir: filepath.Join(root, "packages")}, t: t}
}

// pkg builds and packages one map; marker changes its bytes.
func (m *machine) pkg(name, marker string) *q2pack.Record {
	m.t.Helper()
	dir := m.t.TempDir()
	bsp := filepath.Join(dir, name+".bsp")
	if err := os.WriteFile(bsp, q2fixture.BSP(`{ "classname" "worldspawn" "message" "`+marker+`" }`), 0o644); err != nil {
		m.t.Fatal(err)
	}
	manifest := &build.Manifest{BuildID: "b-" + name + "-" + marker, EngineFamily: q2pack.FamilyQuake2, State: job.Succeeded,
		Inputs:  []build.FileRecord{{Name: "source_map", Path: filepath.Join(dir, name+".map")}},
		Roots:   []build.RootRecord{{Role: profile.RootGame, Path: filepath.Join(m.gameRoot, "baseq2")}},
		Outputs: []build.FileRecord{{Name: "bsp", Path: bsp}}}
	plan, err := q2pack.Prepare(q2pack.Request{Manifest: manifest})
	if err != nil {
		m.t.Fatal(err)
	}
	record, _, err := m.store.Create(plan, q2pack.Options{})
	if err != nil {
		m.t.Fatal(err)
	}
	return record
}

func (m *machine) request(record *q2pack.Record) Request {
	return Request{Package: record, EngineProfileID: "auto-pigeon.engine.yamagi-quake2", GameRoot: m.gameRoot, Dir: m.records}
}

func digestOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range list {
		names = append(names, entry.Name())
	}
	return names
}

func TestAPackageIsInstalledIntoAModFolderOfItsOwnAndTheBaseGameIsNotTouched(t *testing.T) {
	m := newMachine(t)
	base := digestOf(t, filepath.Join(m.gameRoot, "baseq2", "pak0.pak"))
	record := m.pkg("q2009room", "one")
	installation, err := Install(context.Background(), m.request(record))
	if err != nil {
		t.Fatal(err)
	}
	if installation.FSGame != "aup-q2009room" || installation.ModDir != filepath.Join(m.gameRoot, "aup-q2009room") {
		t.Errorf("installed as %s in %s", installation.FSGame, installation.ModDir)
	}
	if got := strings.Join(entries(t, installation.ModDir), ","); got != StampName+",q2009room.pak" {
		t.Errorf("the mod folder holds %s", got)
	}
	if got := strings.Join(entries(t, m.gameRoot), ","); got != "aup-q2009room,baseq2" {
		t.Errorf("the game folder holds %s: a staging folder was left behind", got)
	}
	if digestOf(t, filepath.Join(m.gameRoot, "baseq2", "pak0.pak")) != base || len(entries(t, filepath.Join(m.gameRoot, "baseq2"))) != 1 {
		t.Error("the base game changed")
	}
	if installation.MapMember.Path != "maps/q2009room.bsp" || installation.MapMember.SHA256 != record.Plan.Map.BSPSHA256 {
		t.Errorf("the installed map member is %+v, the build's BSP %s", installation.MapMember, record.Plan.Map.BSPSHA256)
	}
	if err := Verify(installation); err != nil {
		t.Errorf("verify: %v", err)
	}
	loaded, err := Load(m.records, installation.ID)
	if err != nil || loaded.Archive.SHA256 != record.Archive.SHA256 {
		t.Errorf("the record: %v %+v", err, loaded)
	}
}

func TestAFolderTheGameOwnsOrTheCompanionDidNotMakeIsNeverInstalledInto(t *testing.T) {
	m := newMachine(t)
	record := m.pkg("q2009room", "one")
	for _, mod := range []string{"baseq2", "BaseQ2", "xatrix", "ctf", "rogue", "id1", "../baseq2", "a/b"} {
		request := m.request(record)
		request.Mod = mod
		if _, err := Install(context.Background(), request); failure.Of(err) != failure.FSGameInvalid {
			t.Errorf("mod %q: %v", mod, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(m.gameRoot, "mymod", "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := m.request(record)
	request.Mod = "mymod"
	if _, err := Install(context.Background(), request); failure.Of(err) != failure.InstallConflict {
		t.Errorf("a folder the user made: %v", err)
	}
	request.Mod = "MyMod"
	if _, err := Install(context.Background(), request); failure.Of(err) != failure.InstallConflict {
		t.Errorf("another spelling of a folder that is there: %v", err)
	}
	if got := strings.Join(entries(t, filepath.Join(m.gameRoot, "mymod")), ","); got != "maps" {
		t.Errorf("the user's folder was touched: %s", got)
	}
	empty := t.TempDir()
	request = m.request(record)
	request.GameRoot = empty
	if _, err := Install(context.Background(), request); failure.Of(err) != failure.GameDataMissing {
		t.Errorf("a game folder with no baseq2: %v", err)
	}
}

func TestASecondInstallReplacesTheFirstWholeAndAFailedOneLeavesTheFirstRunnable(t *testing.T) {
	m := newMachine(t)
	first := m.pkg("q2009room", "one")
	installed, err := Install(context.Background(), m.request(first))
	if err != nil {
		t.Fatal(err)
	}

	// Fails at its last moment: the generation is written and verified, and
	// is never put in place.
	second := m.pkg("q2009room", "two")
	request := m.request(second)
	request.staged = func() error { return errors.New("the disk filled up") }
	if _, err := Install(context.Background(), request); err == nil {
		t.Fatal("the failing install succeeded")
	}
	if err := Verify(installed); err != nil {
		t.Fatalf("the first installation is no longer usable: %v", err)
	}
	// Cancelled at its last moment, the same.
	ctx, cancel := context.WithCancel(context.Background())
	request.staged = func() error { cancel(); return nil }
	if _, err := Install(ctx, request); failure.Of(err) != failure.Cancelled {
		t.Fatalf("the cancelled install: %v", err)
	}
	if err := Verify(installed); err != nil {
		t.Fatalf("the first installation is no longer usable after a cancel: %v", err)
	}
	if got := strings.Join(entries(t, m.gameRoot), ","); got != "aup-q2009room,baseq2" {
		t.Errorf("a failed or cancelled install left %s", got)
	}

	request.staged = nil
	replaced, err := Install(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Replaced != first.ID || replaced.Archive.SHA256 != second.Archive.SHA256 || replaced.ID != installed.ID {
		t.Errorf("the second install: %+v", replaced)
	}
	if err := Verify(installed); err == nil {
		t.Error("the first installation's record still verifies against the second's bytes")
	}
	if got := strings.Join(entries(t, m.gameRoot), ","); got != "aup-q2009room,baseq2" {
		t.Errorf("after replacing: %s", got)
	}
}

func TestADamagedInstallationIsRefusedBeforeAnEngineIsStartedOnIt(t *testing.T) {
	m := newMachine(t)
	installation, err := Install(context.Background(), m.request(m.pkg("q2009room", "one")))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(installation.Archive.Path)
	if err := os.WriteFile(installation.Archive.Path, data[:len(data)-30], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(installation); failure.Of(err) != failure.ArchiveDamaged {
		t.Errorf("a truncated archive: %v", err)
	}
	if err := Remove(m.records, installation.ID); err == nil {
		t.Error("an installation whose bytes changed was removed")
	}
	os.WriteFile(installation.Archive.Path, data, 0o644)
	os.WriteFile(filepath.Join(installation.ModDir, "autoexec.cfg"), []byte("quit\n"), 0o644)
	if err := Remove(m.records, installation.ID); failure.Of(err) != failure.InstallConflict {
		t.Errorf("a folder somebody added to was removed: %v", err)
	}
	os.Remove(filepath.Join(installation.ModDir, "autoexec.cfg"))
	if err := Remove(m.records, installation.ID); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(entries(t, m.gameRoot), ","); got != "baseq2" {
		t.Errorf("after removing: %s", got)
	}
}

func TestInstallsAtTheSameTimeNeverShareOrHalfWriteAFolder(t *testing.T) {
	m := newMachine(t)
	records := []*q2pack.Record{m.pkg("rooma", "1"), m.pkg("roomb", "1"), m.pkg("rooma", "2"), m.pkg("roomb", "2")}
	var wg sync.WaitGroup
	errs := make([]error, len(records))
	for i, record := range records {
		wg.Add(1)
		go func(i int, record *q2pack.Record) {
			defer wg.Done()
			_, errs[i] = Install(context.Background(), m.request(record))
		}(i, record)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("install %d: %v", i, err)
		}
	}
	if got := strings.Join(entries(t, m.gameRoot), ","); got != "aup-rooma,aup-roomb,baseq2" {
		t.Errorf("the game folder holds %s", got)
	}
	for _, mod := range []string{"aup-rooma", "aup-roomb"} {
		id := InstallationID(m.gameRoot, mod)
		installation, err := Load(m.records, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := Verify(installation); err != nil {
			t.Errorf("%s: the record and the folder disagree: %v", mod, err)
		}
	}
}
