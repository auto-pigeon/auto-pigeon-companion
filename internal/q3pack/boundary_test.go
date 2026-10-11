package q3pack_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3install"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// fileCache is a content-addressed cache that holds what it was given.
type fileCache map[string]string

func (c fileCache) Has(digest string) bool { return c[digest] != "" }

func (c fileCache) Object(digest string) (string, error) {
	if path := c[digest]; path != "" {
		return path, nil
	}
	return "", errors.New("not cached")
}

func (c fileCache) Publish(io.Reader, string, int64) (string, error) {
	return "", errors.New("this cache fetches nothing")
}

// Local engine use is not redistribution (Q3_017A). A package the saved map is
// bound to is staged for the engine by a managed install — and that puts it
// nowhere near the archive somebody gives away: with no grant the map's archive
// holds the map and nothing from the bound package, before the install and
// after it, and the bound archive itself is never a member.
func TestABoundPackageStagedForTheEngineStaysOutOfTheMapsArchive(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{bound: true})
	stage := fixture.manifest.GameData
	if len(stage.Packages) != 1 || !stage.Packages[0].Staged || stage.Packages[0].Origin != q3vfs.OriginCache {
		t.Fatalf("the fixture's bound package: %+v", stage.Packages)
	}

	prepared := prepare(t, fixture, nil)
	plan := prepared.Plan
	if got := memberPaths(plan); len(got) != 1 || got[0] != "maps/q3011_room.bsp" {
		t.Fatalf("with no grant the archive would hold %v", got)
	}
	if len(plan.Sources) != 1 || plan.Sources[0].Origin != q3vfs.OriginCache || plan.Sources[0].Grant != nil {
		t.Fatalf("the bound package is not a source with no grant: %+v", plan.Sources)
	}
	// Bound is not granted: writing it takes an acceptance, in writing.
	if _, err := q3pack.Create(prepared, q3pack.Options{Dir: filepath.Join(t.TempDir(), "refused")}); !errors.Is(err, q3pack.ErrBlocked) {
		t.Fatalf("a package with a bound, ungranted source was written: %v", err)
	}
	store := q3pack.Store{Dir: filepath.Join(t.TempDir(), "packages")}
	record, _, err := store.Create(prepared, q3pack.Options{AcceptReason: "the bound package is not mine to ship"})
	if err != nil {
		t.Fatalf("%v", err)
	}
	exported := func(path string) {
		t.Helper()
		names := archiveNames(t, path)
		if len(names) != 1 || names[0] != "maps/q3011_room.bsp" {
			t.Errorf("%s holds %v", path, names)
		}
		for _, name := range names {
			if strings.HasSuffix(strings.ToLower(name), ".pk3") {
				t.Errorf("%s carries the archive %s", path, name)
			}
		}
	}
	exported(record.ArchivePath)
	before, err := os.ReadFile(record.ArchivePath)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The managed install stages the bound package for the engine…
	installs := filepath.Join(t.TempDir(), "installs")
	installation, err := q3install.Install(context.Background(), q3install.Request{
		Package: record, Kind: q3install.Managed, GameRoot: fixture.game, Dir: installs, Cache: fileCache(fixture.cache),
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(installation.Packages) != 1 || installation.Packages[0].State != q3install.PackageStaged {
		t.Fatalf("the bound package was not staged: %+v", installation.Packages)
	}
	stagedPackage := filepath.Join(installation.BasePath, "baseq3", fixtureArchive)
	if digestOf(t, stagedPackage) != fixture.archive {
		t.Errorf("the staged package is not the bound one")
	}
	// …and nothing it staged is in what is given away: not in the package's
	// own directory, not in the archive, and not in the archive installed.
	exported(installation.Archive.Path)
	after, err := os.ReadFile(record.ArchivePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("installing changed the package's archive: %v", err)
	}
	entries, err := os.ReadDir(record.Directory)
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), fixtureArchive) {
			t.Errorf("the bound package is in the package's own directory")
		}
	}
	// A package made from the same build AFTER the install is the same review:
	// the overlay is not something a package is planned from.
	again := prepare(t, fixture, nil)
	if got := memberPaths(again.Plan); len(got) != 1 || got[0] != "maps/q3011_room.bsp" {
		t.Errorf("after an install the archive would hold %v", got)
	}
	if len(again.Plan.NotCarried) != len(plan.NotCarried) || len(plan.NotCarried) == 0 {
		t.Errorf("not carried: %d before the install, %d after", len(plan.NotCarried), len(again.Plan.NotCarried))
	}
	// What the package does not carry and the staged package has is counted,
	// so "incomplete" is not read as "the engine will lack these".
	if installation.Complete || installation.SuppliedByPackages != len(plan.NotCarried) {
		t.Errorf("complete %v, %d of %d supplied by the staged package",
			installation.Complete, installation.SuppliedByPackages, len(plan.NotCarried))
	}
}
