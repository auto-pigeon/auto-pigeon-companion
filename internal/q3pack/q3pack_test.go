package q3pack_test

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// The fixture is `internal/q3deps/testdata/q3011`: an original map compiled by
// the real Q3Map2, and the content it was compiled against. Its README says
// what each file is there to prove.
const (
	fixtureDir     = "../q3deps/testdata/q3011"
	fixtureArchive = "zz_apq3011_assets.pk3"
)

// built is a finished Quake III build, staged the way `build.Runner` stages
// one: the content as a PK3 in `<content>/baseq3`, a base game folder beside
// it, and a manifest that records both.
type built struct {
	manifest *build.Manifest
	content  string
	game     string
	archive  string // the content archive's digest, in hex
}

type fixtureOptions struct {
	loose    bool                // the content as loose files instead of one archive
	baseGame map[string]string   // files to put in the BASE GAME instead of the content
	remove   []string            // files to leave out of the content
	extra    map[string][]byte   // files to add to the content
	mutate   func(dir string)    // any other change to the content tree
	fsGame   string              // stage as a mod
	zipNames map[string]string   // content path -> the name it gets inside the archive
	archives map[string][]string // extra archives: name -> content paths
}

func makeBuild(t *testing.T, options fixtureOptions) built {
	t.Helper()
	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	copyTree(t, filepath.Join(fixtureDir, "content"), tree)
	for _, name := range options.remove {
		if err := os.Remove(filepath.Join(tree, filepath.FromSlash(name))); err != nil {
			t.Fatalf("%v", err)
		}
	}
	for name, data := range options.extra {
		writeFile(t, filepath.Join(tree, filepath.FromSlash(name)), data)
	}
	if options.mutate != nil {
		options.mutate(tree)
	}

	gameDir := "baseq3"
	if options.fsGame != "" {
		gameDir = options.fsGame
	}
	game := filepath.Join(root, "game")
	content := filepath.Join(root, "content")
	mustMkdir(t, filepath.Join(game, "baseq3"))
	mustMkdir(t, filepath.Join(content, gameDir))
	for name, from := range options.baseGame {
		data, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(from)))
		if err != nil {
			t.Fatalf("%v", err)
		}
		writeFile(t, filepath.Join(game, "baseq3", filepath.FromSlash(name)), data)
		if err := os.Remove(filepath.Join(tree, filepath.FromSlash(from))); err != nil {
			t.Fatalf("%v", err)
		}
	}

	archiveDigest := ""
	if options.loose {
		copyTree(t, tree, filepath.Join(content, gameDir))
	} else {
		separate := map[string]bool{}
		for name, members := range options.archives {
			zipTree(t, tree, filepath.Join(content, gameDir, name), members, nil)
			for _, member := range members {
				separate[member] = true
			}
		}
		var members []string
		for _, name := range listTree(t, tree) {
			if !separate[name] {
				members = append(members, name)
			}
		}
		path := filepath.Join(content, gameDir, fixtureArchive)
		zipTree(t, tree, path, members, options.zipNames)
		archiveDigest = digestOf(t, path)
	}

	buildDir := filepath.Join(root, "build")
	stage, err := q3vfs.Build(q3vfs.Request{
		FSGame: options.fsGame,
		Roots:  map[string]string{profile.RootGame: game, profile.RootContent: content},
		Dir:    filepath.Join(buildDir, "vfs"),
	})
	if err != nil {
		t.Fatalf("staging the fixture: %v", err)
	}
	source := filepath.Join(buildDir, "input", "q3011_room.map")
	bsp := filepath.Join(buildDir, "output", "bsp", "q3011_room.bsp")
	copyFile(t, filepath.Join(fixtureDir, "maps", "q3011_room.map"), source)
	copyFile(t, filepath.Join(fixtureDir, "maps", "q3011_room.bsp"), bsp)
	manifest := &build.Manifest{
		SchemaVersion: "aucom.build-manifest/1.3",
		BuildID:       "b-q3011-fixture",
		EngineFamily:  q3pack.FamilyQuake3,
		State:         job.Succeeded,
		Pipeline:      build.DocumentRef{ID: "auto-pigeon.q3.fast-preview", Version: "1.0.0"},
		GameData:      stage,
		Inputs: []build.FileRecord{{
			Name: "source_map", Role: "q3.map.source", Path: source, SHA256: "sha256:" + digestOf(t, source),
		}},
		Outputs: []build.FileRecord{{
			Name: "bsp", Role: "q3.bsp.lit", Path: bsp, SHA256: "sha256:" + digestOf(t, bsp),
		}},
		Directory: buildDir,
	}
	return built{manifest: manifest, content: content, game: game, archive: archiveDigest}
}

func prepare(t *testing.T, fixture built, grants []q3pack.Grant, include ...string) *q3pack.Prepared {
	t.Helper()
	prepared, err := q3pack.Prepare(q3pack.Request{
		Manifest: fixture.manifest, Grants: grants, Include: include, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	return prepared
}

func ownArchive(fixture built) []q3pack.Grant {
	return []q3pack.Grant{{Archive: fixture.archive, Basis: q3pack.BasisOwnWork}}
}

func memberPaths(plan *q3pack.Plan) []string {
	out := make([]string, 0, len(plan.Members))
	for _, member := range plan.Members {
		out = append(out, member.Path)
	}
	return out
}

func dependency(t *testing.T, plan *q3pack.Plan, name string) q3pack.Dependency {
	t.Helper()
	for _, dependency := range plan.Dependencies {
		if dependency.Name == name {
			return dependency
		}
	}
	t.Fatalf("the plan has no dependency %s", name)
	return q3pack.Dependency{}
}

func problemCodes(plan *q3pack.Plan) map[string]int {
	codes := map[string]int{}
	for _, problem := range plan.Problems {
		codes[problem.Code]++
	}
	return codes
}

// The whole point of the package, on the fixture built to show it: the archive
// carries what an ENGINE needs and nothing the compiler alone read.
func TestAPackageCarriesWhatAnEngineNeedsAndNothingOnlyTheCompilerRead(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	plan := prepare(t, fixture, ownArchive(fixture)).Plan

	want := []string{
		"maps/q3011_room.bsp",
		"models/apq3011/beacon.md3", // a func_static's model2: the engine opens it
		"models/apq3011/beacon.tga",
		"models/apq3011/crate.tga", // the BAKED model's skin: named by the BSP, not by the map
		"scripts/apq3011.shader",
		"sound/apq3011/hum.wav",
		"textures/apq3011/ceiling.tga",
		"textures/apq3011/floor.tga",
		"textures/apq3011/glow_stage.tga",
		"textures/apq3011/wall.tga",
	}
	if got := memberPaths(plan); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the archive would hold\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if len(plan.Problems) != 0 {
		t.Errorf("a fully granted package is held: %+v", plan.Problems)
	}

	// The baked model: the compiler needed the .md3 and an engine does not.
	crate := dependency(t, plan, "models/apq3011/crate.md3")
	if !crate.RequiredAtCompile || crate.RequiredAtRuntime || crate.Disposition != q3pack.CompileOnly {
		t.Errorf("the baked model: %+v", crate)
	}
	// Its skin: the map source never names it, the BSP does.
	skin := dependency(t, plan, "models/apq3011/crate")
	if skin.RequiredAtCompile || !skin.RequiredAtRuntime || skin.Disposition != q3pack.UserAuthored {
		t.Errorf("the baked model's skin: %+v", skin)
	}
	// The model2: an engine needs it and the compiler never opened it.
	beacon := dependency(t, plan, "models/apq3011/beacon.md3")
	if beacon.RequiredAtCompile || !beacon.RequiredAtRuntime {
		t.Errorf("the model2 model: %+v", beacon)
	}
	// A shader: both programs read the script, each reads its own image.
	glow := dependency(t, plan, "textures/apq3011/glow")
	if !glow.RequiredAtCompile || !glow.RequiredAtRuntime {
		t.Errorf("the shader: %+v", glow)
	}
	for _, file := range glow.Files {
		switch file.Path {
		case "textures/apq3011/glow_editor.tga":
			if !file.Compile || file.Runtime || file.Member != "" {
				t.Errorf("the editor image: %+v", file)
			}
		case "textures/apq3011/glow_stage.tga":
			if file.Compile || !file.Runtime || file.Member == "" {
				t.Errorf("the stage image: %+v", file)
			}
		case "scripts/apq3011.shader":
			if !file.Compile || !file.Runtime {
				t.Errorf("the shader script: %+v", file)
			}
		}
	}
	if plan.ArchiveName != "auto-pigeon-q3011_room-local-"+strings.TrimPrefix(fixture.manifest.Inputs[0].SHA256, "sha256:")[:12]+".pk3" {
		t.Errorf("the archive is named %s", plan.ArchiveName)
	}
	if plan.GameDir != "baseq3" {
		t.Errorf("the archive is meant for %q", plan.GameDir)
	}
}

// A saved map's archive is named by the account's id and revision.
func TestASavedMapsArchiveIsNamedByItsIdAndRevision(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	fixture.manifest.Inputs[0].Source = &build.SourceRef{
		AssetType: "map", AssetID: "nonv3qd3tz8r96s", RevisionID: "8yaqnmowk5b6j3d", Revision: 2,
	}
	plan := prepare(t, fixture, ownArchive(fixture)).Plan
	if plan.ArchiveName != "auto-pigeon-nonv3qd3tz8r96s-2.pk3" {
		t.Errorf("the archive is named %s", plan.ArchiveName)
	}
	if err := q3vfs.CheckArchiveName(plan.ArchiveName); err != nil {
		t.Errorf("the archive's own name is not one a build would stage: %v", err)
	}
}

// With no grant nothing but the map is packaged, every file an engine needs is
// named as unresolved, and nothing is written.
func TestWithoutAGrantOnlyTheMapIsPackagedAndTheArchiveIsRefused(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	prepared := prepare(t, fixture, nil)
	plan := prepared.Plan
	if got := memberPaths(plan); len(got) != 1 || got[0] != "maps/q3011_room.bsp" {
		t.Errorf("without a grant the archive would hold %v", got)
	}
	if codes := problemCodes(plan); codes[q3pack.ProblemUnresolved] != 9 || len(codes) != 1 {
		t.Errorf("problems: %v", codes)
	}
	if len(plan.Sources) != 1 || plan.Sources[0].SHA256 != fixture.archive || plan.Sources[0].RuntimeFiles != 9 {
		t.Errorf("sources: %+v", plan.Sources)
	}
	dir := filepath.Join(t.TempDir(), "out")
	if _, err := q3pack.Create(prepared, q3pack.Options{Dir: dir}); !errors.Is(err, q3pack.ErrBlocked) {
		t.Fatalf("an ungranted package was not refused: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused package left %d file(s) behind", len(entries))
	}
}

// Denied rights: the answer is "no", it is recorded as that, and the file is
// not packaged. The archive is written only when somebody accepts, in writing,
// what it will lack — and it then says so about itself.
func TestDeniedRightsAreBlockedAndAcceptedOnlyInWriting(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	prepared := prepare(t, fixture, []q3pack.Grant{{Archive: fixture.archive, Basis: q3pack.BasisNotRedistributable}})
	plan := prepared.Plan
	if codes := problemCodes(plan); codes[q3pack.ProblemBlocked] != 9 {
		t.Errorf("problems: %v", codes)
	}
	if skin := dependency(t, plan, "textures/apq3011/wall"); skin.Disposition != q3pack.Blocked {
		t.Errorf("a denied texture is %s", skin.Disposition)
	}
	if _, err := q3pack.Create(prepared, q3pack.Options{Dir: t.TempDir()}); !errors.Is(err, q3pack.ErrBlocked) {
		t.Fatalf("a package with denied assets was written: %v", err)
	}
	record, err := q3pack.Create(prepared, q3pack.Options{
		Dir: t.TempDir(), AcceptReason: "the texture pack is third-party; players install it themselves",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if record.Complete || record.Acceptance == nil || len(record.Acceptance.NotCarried) != 9 ||
		!strings.Contains(record.Acceptance.Reason, "third-party") {
		t.Errorf("the record does not say what the archive lacks: %+v", record.Acceptance)
	}
	if names := archiveNames(t, record.ArchivePath); len(names) != 1 || names[0] != "maps/q3011_room.bsp" {
		t.Errorf("a package with denied assets holds %v", names)
	}
}

// A licence is named or the grant is refused, and a named licence travels with
// each member it let in.
func TestALicensedGrantNamesItsLicence(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	_, err := q3pack.Prepare(q3pack.Request{
		Manifest: fixture.manifest,
		Grants:   []q3pack.Grant{{Archive: fixture.archive, Basis: q3pack.BasisLicensed}},
	})
	if err == nil || !strings.Contains(err.Error(), "names no licence") {
		t.Fatalf("a licensed grant with no licence: %v", err)
	}
	plan := prepare(t, fixture, []q3pack.Grant{{
		Archive: "sha256:" + strings.ToUpper(fixture.archive), Basis: q3pack.BasisLicensed, Licence: "CC0-1.0",
	}}, "LICENSE-apq3011.txt").Plan
	found := false
	for _, member := range plan.Members {
		if member.Path == "maps/q3011_room.bsp" {
			continue
		}
		if member.Disposition != q3pack.Licensed || member.Licence != "CC0-1.0" {
			t.Errorf("%s: %s under %q", member.Path, member.Disposition, member.Licence)
		}
		if member.Path == "LICENSE-apq3011.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("the licence text that was asked for is not in the archive: %v", memberPaths(plan))
	}
}

// A grant is for something this build read. One that names nothing is refused
// rather than quietly applied to nothing.
func TestAGrantThatNamesNothingIsRefused(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	for name, grants := range map[string][]q3pack.Grant{
		"another archive": {{Archive: strings.Repeat("ab", 32), Basis: q3pack.BasisOwnWork}},
		"an unused path":  {{Path: "textures/nosuch/file.tga", Basis: q3pack.BasisOwnWork}},
		"two targets":     {{Archive: fixture.archive, Loose: true, Basis: q3pack.BasisOwnWork}},
		"no basis":        {{Archive: fixture.archive}},
		"twice":           {{Archive: fixture.archive, Basis: q3pack.BasisOwnWork}, {Archive: fixture.archive, Basis: q3pack.BasisLicensed, Licence: "MIT"}},
	} {
		if _, err := q3pack.Prepare(q3pack.Request{Manifest: fixture.manifest, Grants: grants}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A grant for one file outranks the grant for the archive it is in.
func TestAGrantForOneFileOutranksItsArchives(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	plan := prepare(t, fixture, []q3pack.Grant{
		{Archive: fixture.archive, Basis: q3pack.BasisOwnWork},
		{Path: "sound/apq3011/hum.wav", Basis: q3pack.BasisNotRedistributable},
	}).Plan
	for _, path := range memberPaths(plan) {
		if path == "sound/apq3011/hum.wav" {
			t.Error("a file denied by its own grant was packaged under its archive's")
		}
	}
	if codes := problemCodes(plan); codes[q3pack.ProblemBlocked] != 1 {
		t.Errorf("problems: %v", codes)
	}
}

// A BSP that is present while a model it needs is not: the compiler exited 0
// for this, and the package is what refuses.
func TestAMissingRuntimeModelStopsThePackage(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{remove: []string{"models/apq3011/beacon.md3"}})
	prepared := prepare(t, fixture, ownArchive(fixture))
	plan := prepared.Plan
	beacon := dependency(t, plan, "models/apq3011/beacon.md3")
	if beacon.Disposition != q3pack.Missing {
		t.Errorf("a model nothing has is %s", beacon.Disposition)
	}
	if codes := problemCodes(plan); codes[q3pack.ProblemMissing] != 1 {
		t.Errorf("problems: %v", codes)
	}
	if _, err := q3pack.Create(prepared, q3pack.Options{Dir: t.TempDir()}); !errors.Is(err, q3pack.ErrBlocked) {
		t.Fatalf("a package missing a model was written: %v", err)
	}
}

// A missing stage image is the case the compiler is SILENT about (measured),
// so this is the only place it is caught.
func TestAMissingStageImageStopsThePackage(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{remove: []string{"textures/apq3011/glow_stage.tga"}})
	plan := prepare(t, fixture, ownArchive(fixture)).Plan
	if glow := dependency(t, plan, "textures/apq3011/glow"); glow.Disposition != q3pack.Missing {
		t.Errorf("a shader whose stage image is missing is %s", glow.Disposition)
	}
	// And the reverse: a missing EDITOR image does not hold a package, because
	// no engine reads it.
	fixture = makeBuild(t, fixtureOptions{remove: []string{"textures/apq3011/glow_editor.tga"}})
	plan = prepare(t, fixture, ownArchive(fixture)).Plan
	if len(plan.Problems) != 0 {
		t.Errorf("a missing editor image holds the package: %+v", plan.Problems)
	}
}

// What the installed game supplies is never packaged, and is not a problem.
func TestTheBaseGamesFilesAreNeverPackaged(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{baseGame: map[string]string{
		"textures/apq3011/wall.tga": "textures/apq3011/wall.tga",
	}})
	plan := prepare(t, fixture, ownArchive(fixture)).Plan
	for _, path := range memberPaths(plan) {
		if path == "textures/apq3011/wall.tga" {
			t.Error("a file the base game supplies was packaged")
		}
	}
	if wall := dependency(t, plan, "textures/apq3011/wall"); wall.Disposition != q3pack.BaseGame {
		t.Errorf("a base game texture is %s", wall.Disposition)
	}
	if len(plan.Problems) != 0 {
		t.Errorf("a base game dependency holds the package: %+v", plan.Problems)
	}
}

// Two files for one path: refused, and not something a reason can accept.
func TestADuplicatePathIsRefusedAndCannotBeAccepted(t *testing.T) {
	bsp, err := os.ReadFile(filepath.Join(fixtureDir, "maps", "q3011_room.bsp"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	fixture := makeBuild(t, fixtureOptions{extra: map[string][]byte{"maps/Q3011_ROOM.bsp": bsp[:4096]}})
	prepared := prepare(t, fixture, ownArchive(fixture), "maps/q3011_room.bsp")
	if codes := problemCodes(prepared.Plan); codes[q3pack.ProblemDuplicate] != 1 {
		t.Fatalf("problems: %+v", prepared.Plan.Problems)
	}
	_, err = q3pack.Create(prepared, q3pack.Options{Dir: t.TempDir(), AcceptReason: "I accept everything"})
	if !errors.Is(err, q3pack.ErrBlocked) || !strings.Contains(err.Error(), "packaged twice") {
		t.Fatalf("a duplicate path was accepted: %v", err)
	}
}

// An archive with a member that climbs out of it is not read at all: nothing
// from it is packaged, and the reason names the archive.
func TestAnArchiveThatClimbsOutOfItselfIsNotRead(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{zipNames: map[string]string{
		"LICENSE-apq3011.txt": "../../LICENSE-apq3011.txt",
	}})
	plan := prepare(t, fixture, ownArchive(fixture)).Plan
	if got := memberPaths(plan); len(got) != 1 {
		t.Errorf("files were packaged out of an unsafe archive: %v", got)
	}
	wall := dependency(t, plan, "textures/apq3011/wall")
	if wall.Disposition != q3pack.Blocked || !strings.Contains(wall.Files[0].Reason, "could not be read safely") {
		t.Errorf("a file from an unsafe archive: %+v", wall)
	}
}

// A released commercial file is recognised by its content, and the grant for
// the archive around it does not cover it.
func TestAKnownCommercialFileNeedsAGrantOfItsOwn(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	corpus := &pack.AssetCorpus{
		SchemaVersion: pack.AssetCorpusSchemaVersion, Source: "this test",
		Assets: []pack.KnownAsset{{
			SHA256:  "sha256:" + digestOf(t, filepath.Join(fixtureDir, "content/textures/apq3011/wall.tga")),
			Release: "a released game's wall texture",
		}},
	}
	request := q3pack.Request{Manifest: fixture.manifest, Grants: ownArchive(fixture), KnownAssets: corpus, WorkDir: t.TempDir()}
	prepared, err := q3pack.Prepare(request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer prepared.Close()
	wall := dependency(t, prepared.Plan, "textures/apq3011/wall")
	if wall.Disposition != q3pack.Blocked || !strings.Contains(wall.Files[0].Reason, "byte-for-byte") {
		t.Errorf("a known asset under an archive grant: %+v", wall)
	}
	for _, path := range memberPaths(prepared.Plan) {
		if path == "textures/apq3011/wall.tga" {
			t.Error("a known commercial file was packaged under its archive's grant")
		}
	}

	request.Grants = append(request.Grants, q3pack.Grant{
		Path: "textures/apq3011/wall.tga", Basis: q3pack.BasisLicensed, Licence: "written permission from the publisher",
	})
	again, err := q3pack.Prepare(request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer again.Close()
	if wall := dependency(t, again.Plan, "textures/apq3011/wall"); wall.Disposition != q3pack.Licensed {
		t.Errorf("a known asset with its own grant is %s", wall.Disposition)
	}
}

// Loose files are a source like an archive is, and keep the capitalisation
// they have on disk.
func TestLooseFilesAreGrantedAsOneSourceAndKeepTheirCase(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{loose: true, mutate: func(dir string) {
		if err := os.Rename(filepath.Join(dir, "textures/apq3011/wall.tga"), filepath.Join(dir, "textures/apq3011/Wall.TGA")); err != nil {
			panic(err)
		}
	}})
	plan := prepare(t, fixture, nil).Plan
	if codes := problemCodes(plan); codes[q3pack.ProblemUnresolved] != 9 {
		t.Errorf("ungranted loose files: %v", codes)
	}
	plan = prepare(t, fixture, []q3pack.Grant{{Loose: true, Basis: q3pack.BasisOwnWork}}).Plan
	found := false
	for _, path := range memberPaths(plan) {
		if path == "textures/apq3011/Wall.TGA" {
			found = true
		}
		if path == "textures/apq3011/wall.tga" {
			t.Error("a file's capitalisation was changed on its way into the archive")
		}
	}
	if !found || len(plan.Problems) != 0 {
		t.Errorf("members %v, problems %+v", memberPaths(plan), plan.Problems)
	}
}

// A mod build's archive is meant for the mod.
func TestAModBuildsArchiveIsMeantForTheMod(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{fsGame: "apmod"})
	plan := prepare(t, fixture, ownArchive(fixture)).Plan
	if plan.GameDir != "apmod" || plan.FSGame != "apmod" || len(plan.Problems) != 0 {
		t.Errorf("game dir %q, fs_game %q, problems %+v", plan.GameDir, plan.FSGame, plan.Problems)
	}
}

// The same build, packaged twice, is the same bytes.
func TestTheSamePackageTwiceIsTheSameBytes(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	var digests []string
	for i := 0; i < 2; i++ {
		prepared := prepare(t, fixture, ownArchive(fixture))
		// A different clock each time: nothing about WHEN may reach the archive.
		at := time.Date(2026, 10, 1+i, 12, i, 0, 0, time.UTC)
		record, err := q3pack.Create(prepared, q3pack.Options{Dir: t.TempDir(), Now: func() time.Time { return at }})
		if err != nil {
			t.Fatalf("%v", err)
		}
		digests = append(digests, record.Archive.SHA256)
		if digestOf(t, record.ArchivePath) != strings.TrimPrefix(record.Archive.SHA256, "sha256:") {
			t.Error("the record's digest is not the archive's")
		}
	}
	if digests[0] != digests[1] {
		t.Errorf("two packages of one build differ: %s and %s", digests[0], digests[1])
	}
}

// What is written is what the plan listed, in order, and nothing of the tool.
func TestTheArchiveHoldsExactlyThePlan(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	prepared := prepare(t, fixture, ownArchive(fixture))
	dir := t.TempDir()
	record, err := q3pack.Create(prepared, q3pack.Options{Dir: dir, Companion: "test"})
	if err != nil {
		t.Fatalf("%v", err)
	}
	names := archiveNames(t, record.ArchivePath)
	if strings.Join(names, "\n") != strings.Join(memberPaths(prepared.Plan), "\n") {
		t.Errorf("the archive holds %v and the plan listed %v", names, memberPaths(prepared.Plan))
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("the archive's members are not in order: %v", names)
	}
	verification, err := pack.Verify(record.ArchivePath, pack.FormatPK3, pack.Budget{})
	if err != nil || !verification.OK() {
		t.Errorf("the archive does not verify: %v %+v", err, verification)
	}
	// Refused over an archive that is already there.
	again := prepare(t, fixture, ownArchive(fixture))
	if _, err := q3pack.Create(again, q3pack.Options{Dir: dir}); err == nil || !errors.Is(err, pack.ErrWouldOverwrite) {
		t.Errorf("an existing archive was written over: %v", err)
	}
	// And the record is checked against the archive when it is read back.
	loaded, err := q3pack.LoadRecord(dir)
	if err != nil || loaded.ID != record.ID || !loaded.Complete {
		t.Fatalf("%v %+v", err, loaded)
	}
	if err := os.WriteFile(record.ArchivePath, []byte("not the archive"), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := q3pack.LoadRecord(dir); err == nil || !strings.Contains(err.Error(), "not the archive its record describes") {
		t.Errorf("a replaced archive was read back as the package: %v", err)
	}
}

// The store names a package by its digest, so the same package created twice
// is one package and not an overwrite.
func TestTheStoreKeepsOnePackagePerDigest(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	store := q3pack.Store{Dir: filepath.Join(t.TempDir(), "packages")}
	first, existed, err := store.Create(prepare(t, fixture, ownArchive(fixture)), q3pack.Options{})
	if err != nil || existed {
		t.Fatalf("%v existed=%v", err, existed)
	}
	second, existed, err := store.Create(prepare(t, fixture, ownArchive(fixture)), q3pack.Options{})
	if err != nil || !existed || second.ID != first.ID {
		t.Fatalf("%v existed=%v %s %s", err, existed, first.ID, second.ID)
	}
	records, err := store.List()
	if err != nil || len(records) != 1 {
		t.Fatalf("%v %d", err, len(records))
	}
	if _, err := store.Get("../" + first.ID); err == nil {
		t.Error("a package id that is a path was read")
	}
	// A refused package leaves nothing in the store.
	if _, _, err := store.Create(prepare(t, fixture, nil), q3pack.Options{}); !errors.Is(err, q3pack.ErrBlocked) {
		t.Fatalf("%v", err)
	}
	entries, _ := os.ReadDir(store.Dir)
	if len(entries) != 1 {
		t.Errorf("the store holds %d entries after a refusal", len(entries))
	}
}

// Quake 1 and Quake II are packaged by `companion package create`, not here.
func TestABuildOfAnotherGameIsRefused(t *testing.T) {
	fixture := makeBuild(t, fixtureOptions{})
	fixture.manifest.EngineFamily = "quake1"
	_, err := q3pack.Prepare(q3pack.Request{Manifest: fixture.manifest})
	if !errors.Is(err, q3pack.ErrNotQuake3) {
		t.Fatalf("%v", err)
	}
	fixture.manifest.EngineFamily = q3pack.FamilyQuake3
	fixture.manifest.State = job.Failed
	if _, err := q3pack.Prepare(q3pack.Request{Manifest: fixture.manifest}); err == nil {
		t.Fatal("a failed build was packaged")
	}
}

// --- helpers ---------------------------------------------------------------

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("%v", err)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("%v", err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("%v", err)
	}
	writeFile(t, to, data)
}

func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	sort.Strings(out)
	return out
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	for _, name := range listTree(t, from) {
		copyFile(t, filepath.Join(from, filepath.FromSlash(name)), filepath.Join(to, filepath.FromSlash(name)))
	}
}

func zipTree(t *testing.T, tree, archive string, members []string, rename map[string]string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(archive))
	file, err := os.Create(archive)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	for _, name := range members {
		stored := name
		if renamed, ok := rename[name]; ok {
			stored = renamed
		}
		part, err := writer.CreateHeader(&zip.FileHeader{Name: stored, Method: zip.Store})
		if err != nil {
			t.Fatalf("%v", err)
		}
		data, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("%v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("%v", err)
	}
}

func digestOf(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatalf("%v", err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func archiveNames(t *testing.T, path string) []string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer reader.Close()
	var names []string
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}
