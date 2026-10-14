package q2pack

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
)

// roomEntities is an authored gameplay room's entity lump, as a compile of it
// leaves it: a custom sky, a looping custom sound, an item drawn with a custom
// model, and the classname-chosen things the game supplies itself.
const roomEntities = `{
"classname" "worldspawn"
"sky" "q2009sky"
"sounds" "2"
}
{
"classname" "info_player_start"
"origin" "0 0 24"
}
{
"classname" "target_speaker"
"noise" "q2009/hum"
"spawnflags" "1"
"origin" "64 0 64"
}
{
"classname" "item_health"
"model" "models/q2009/thing/tris.md2"
"origin" "96 0 24"
}
{
"classname" "func_door"
"model" "*1"
}
`

type world struct {
	base, content, buildDir string
	manifest                *build.Manifest
}

// newWorld is a finished Quake II build of the room: the base game is a PAK of
// authored files under retail-looking names, and the content folder holds the
// map's own files and a notice.
func newWorld(t *testing.T, textures ...string) world {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "game", "baseq2")
	names := []string{"textures/e1u1/floor1_1.wal", "pics/colormap.pcx", "maps/base1.bsp"}
	pak := q2fixture.PAK(names, map[string][]byte{
		"textures/e1u1/floor1_1.wal": q2fixture.WAL("e1u1/floor1_1"),
		"pics/colormap.pcx":          q2fixture.PCX(),
		"maps/base1.bsp":             q2fixture.BSP(`{ "classname" "worldspawn" }`),
	})
	files := map[string][]byte{"pak0.pak": pak}
	if _, err := q2fixture.Write(base, files); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(root, "content")
	contentFiles := map[string][]byte{
		"textures/q2009/marker.wal":   q2fixture.WAL("q2009/marker"),
		"sound/q2009/hum.wav":         q2fixture.WAV(),
		"models/q2009/thing/tris.md2": q2fixture.MD2("models/q2009/thing/skin.pcx"),
		"models/q2009/thing/skin.pcx": q2fixture.PCX(),
		"LICENSE.txt":                 []byte("authored for the Q2_009 tests\n"),
		"maps/q2009room.map":          []byte("// the source; never a member\n"),
		"maps/q2009room.texinfo.json": []byte("{}\n"),
	}
	for _, side := range []string{"rt", "bk", "lf", "ft", "up", "dn"} {
		contentFiles["env/q2009sky"+side+".pcx"] = q2fixture.PCX()
	}
	// One face also as .tga: a GL renderer asks for that one, so both travel.
	contentFiles["env/q2009skyrt.tga"] = []byte("TGA stand-in")
	if _, err := q2fixture.Write(content, contentFiles); err != nil {
		t.Fatal(err)
	}
	if len(textures) == 0 {
		textures = []string{"e1u1/floor1_1", "q2009/marker"}
	}
	buildDir := filepath.Join(root, "build")
	bsp := filepath.Join(buildDir, "q2009room.bsp")
	if _, err := q2fixture.Write(buildDir, map[string][]byte{"q2009room.bsp": q2fixture.BSP(roomEntities, textures...)}); err != nil {
		t.Fatal(err)
	}
	digest, size, _ := hashFile(bsp)
	manifest := &build.Manifest{
		BuildID: "20261014T190000Z-q2009test", EngineFamily: FamilyQuake2, State: job.Succeeded,
		Inputs:  []build.FileRecord{{Name: "source_map", Path: filepath.Join(content, "maps", "q2009room.map"), SHA256: "sha256:abc"}},
		Roots:   []build.RootRecord{{Role: profile.RootGame, Path: base}, {Role: profile.RootContent, Path: content}},
		Outputs: []build.FileRecord{{Name: "bsp", Path: bsp, SHA256: digest, Size: size}},
	}
	return world{base: base, content: content, buildDir: buildDir, manifest: manifest}
}

func memberPaths(plan *Plan) []string {
	var out []string
	for _, member := range plan.Members {
		out = append(out, member.Path)
	}
	return out
}

func TestAPackageCarriesTheMapAndTheGrantedFilesItNamesAndNeverTheBaseGame(t *testing.T) {
	w := newWorld(t)
	plan, err := Prepare(Request{Manifest: w.manifest, Grants: []Grant{{Basis: BasisOwnWork, Loose: true}}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docs/q2009room/license.txt",
		"env/q2009skybk.pcx", "env/q2009skydn.pcx", "env/q2009skyft.pcx", "env/q2009skylf.pcx", "env/q2009skyrt.pcx", "env/q2009skyrt.tga", "env/q2009skyup.pcx",
		"maps/q2009room.bsp",
		"models/q2009/thing/skin.pcx", "models/q2009/thing/tris.md2",
		"sound/q2009/hum.wav",
		"textures/q2009/marker.wal",
	}
	if got := memberPaths(plan); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("members, in archive order:\n got  %v\n want %v", got, want)
	}
	if len(plan.Problems) != 0 || len(plan.NotCarried) != 0 {
		t.Errorf("a complete map has problems %v / not carried %v", plan.Problems, plan.NotCarried)
	}
	for _, dependency := range plan.Dependencies {
		if dependency.Name == "e1u1/floor1_1" && dependency.Disposition != BaseGame {
			t.Errorf("the base game's texture became %s", dependency.Disposition)
		}
	}
	if plan.ArchiveName != "q2009room.pak" || plan.GameDir != "aup-q2009room" {
		t.Errorf("archive %q in %q", plan.ArchiveName, plan.GameDir)
	}
	if plan.Map.BSPSHA256 == "" || plan.Map.SourceName != "q2009room.map" {
		t.Errorf("the identity chain is incomplete: %+v", plan.Map)
	}
}

func TestNothingOfTheUsersIsPackagedWithoutAGrant(t *testing.T) {
	w := newWorld(t)
	plan, err := Prepare(Request{Manifest: w.manifest})
	if err != nil {
		t.Fatal(err)
	}
	if got := memberPaths(plan); len(got) != 1 || got[0] != "maps/q2009room.bsp" {
		t.Errorf("without a grant the archive holds %v", got)
	}
	if err := plan.Blocked(false); failure.Of(err) != failure.PackageHeld {
		t.Fatalf("held = %v", err)
	}
	if err := plan.Blocked(true); err != nil {
		t.Errorf("a reason accepts what is not carried, and did not: %v", err)
	}
	if len(plan.NotCarried) != 10 { // six sky faces, the sound, the model, its skin, the texture
		t.Errorf("not carried: %v", plan.NotCarried)
	}

	// Denied is denied: it stays out, and one path's own grant outranks the
	// folder's.
	plan, err = Prepare(Request{Manifest: w.manifest, Grants: []Grant{
		{Basis: BasisNotRedistributable, Loose: true},
		{Basis: BasisLicensed, Licence: "CC0-1.0", Path: "textures/q2009/marker.wal"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := memberPaths(plan); strings.Join(got, ",") != "docs/q2009room/license.txt,maps/q2009room.bsp,textures/q2009/marker.wal" {
		t.Errorf("members %v", got)
	}
	for _, member := range plan.Members {
		if member.Path == "textures/q2009/marker.wal" && (member.Disposition != Licensed || member.Licence != "CC0-1.0") {
			t.Errorf("the licensed texture is %+v", member)
		}
	}
}

func TestAMissingTextureIsNamedBeforeAnArchiveIsWritten(t *testing.T) {
	w := newWorld(t, "e1u1/floor1_1", "q2009/marker", "q2009/nowhere")
	plan, err := Prepare(Request{Manifest: w.manifest, Grants: []Grant{{Basis: BasisOwnWork, Loose: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.NotCarried) != 1 || plan.NotCarried[0] != "textures/q2009/nowhere.wal" {
		t.Fatalf("not carried %v", plan.NotCarried)
	}
	_, err = Create(plan, Options{Dir: t.TempDir()})
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("an incomplete package was written: %v", err)
	}
	record, err := Create(plan, Options{Dir: t.TempDir(), AcceptReason: "the marker texture is drawn by the base game's default"})
	if err != nil {
		t.Fatal(err)
	}
	if record.Complete || record.Acceptance == nil || record.Acceptance.NotCarried[0] != "textures/q2009/nowhere.wal" {
		t.Errorf("an accepted incomplete package is recorded as %+v / %+v", record.Complete, record.Acceptance)
	}
}

func TestANameAnEngineCouldSatisfyFromSomewhereElseIsRefused(t *testing.T) {
	w := newWorld(t)
	plan, err := Prepare(Request{Manifest: w.manifest, MapName: "base1", Grants: []Grant{{Basis: BasisOwnWork, Loose: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Blocked(true); err == nil || !strings.Contains(err.Error(), "the base game already has maps/base1.bsp") {
		t.Fatalf("a base-game map name was accepted: %v", err)
	}
	// The downloaded original beside the source is the same trap.
	if _, err := q2fixture.Write(w.content, map[string][]byte{"maps/corpusmap.bsp": q2fixture.BSP("")}); err != nil {
		t.Fatal(err)
	}
	plan, err = Prepare(Request{Manifest: w.manifest, MapName: "corpusmap", Grants: []Grant{{Basis: BasisOwnWork, Loose: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Blocked(true); err == nil || !strings.Contains(err.Error(), "your content folder already has maps/corpusmap.bsp") {
		t.Fatalf("the precompiled original's name was accepted: %v", err)
	}
	for _, bad := range []string{"Room", "../room", strings.Repeat("r", maxMapName+1)} {
		if _, err := Prepare(Request{Manifest: w.manifest, MapName: bad}); failure.Of(err) != failure.InputInvalid {
			t.Errorf("map name %q: %v", bad, err)
		}
	}
}

func TestAGrantIsRefusedWhenItSaysNothingUsable(t *testing.T) {
	w := newWorld(t)
	for _, grants := range [][]Grant{
		{{Basis: BasisLicensed, Loose: true}},
		{{Basis: "probably_fine", Loose: true}},
		{{Basis: BasisOwnWork, Path: "../../etc/passwd"}},
		{{Basis: BasisOwnWork}},
		{{Basis: BasisOwnWork, Loose: true}, {Basis: BasisNotRedistributable, Loose: true}},
	} {
		if _, err := Prepare(Request{Manifest: w.manifest, Grants: grants}); failure.Of(err) != failure.InputInvalid {
			t.Errorf("grants %+v: %v", grants, err)
		}
	}
}

func TestTheWrittenArchiveIsReadBackAndCarriesNoLocalPath(t *testing.T) {
	w := newWorld(t)
	plan, err := Prepare(Request{Manifest: w.manifest, Grants: []Grant{{Basis: BasisOwnWork, Loose: true}}})
	if err != nil {
		t.Fatal(err)
	}
	fixed := func() time.Time { return time.Date(2026, 10, 14, 19, 0, 0, 0, time.UTC) }
	first, existed, err := Store{Dir: t.TempDir()}.Create(plan, Options{Now: fixed, Manifest: w.manifest})
	if err != nil || existed {
		t.Fatalf("create: %v %v", err, existed)
	}
	if len(first.Entries) != len(plan.Members) {
		t.Fatalf("read back %d of %d", len(first.Entries), len(plan.Members))
	}
	inspection, err := pack.Inspect(first.ArchivePath, pack.FormatPAK, pack.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range inspection.Entries {
		if strings.HasSuffix(entry.Path, ".map") || strings.HasSuffix(entry.Path, ".texinfo.json") {
			t.Errorf("the archive holds %s", entry.Path)
		}
	}
	archive, _ := os.ReadFile(first.ArchivePath)
	sidecar, _ := os.ReadFile(first.ManifestPath)
	for name, data := range map[string][]byte{"archive": archive, "sidecar": sidecar} {
		for _, private := range []string{w.content, w.base, w.buildDir, os.TempDir()} {
			if bytes.Contains(data, []byte(private)) {
				t.Errorf("the %s names the local path %s", name, private)
			}
		}
	}
	// The same plan in another store is the same bytes: the order and the
	// format are internal/pack's, and they depend on nothing local.
	second, _, err := Store{Dir: t.TempDir()}.Create(plan, Options{Now: fixed, Manifest: w.manifest})
	if err != nil {
		t.Fatal(err)
	}
	if first.Archive.SHA256 != second.Archive.SHA256 || first.ID != second.ID {
		t.Errorf("two writes of one plan differ: %s %s", first.Archive.SHA256, second.Archive.SHA256)
	}
	// And a store keeps one copy of one package.
	store := Store{Dir: filepath.Dir(first.Directory)}
	again, existed, err := store.Create(plan, Options{Now: fixed})
	if err != nil || !existed || again.ID != first.ID {
		t.Errorf("writing the same package twice: %v %v %v", err, existed, again)
	}
}

func TestAPackageWhoseArchiveChangedIsNotThePackage(t *testing.T) {
	w := newWorld(t)
	plan, err := Prepare(Request{Manifest: w.manifest, Grants: []Grant{{Basis: BasisOwnWork, Loose: true}}})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Dir: t.TempDir()}
	record, _, err := store.Create(plan, Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(record.ArchivePath)
	if err := os.WriteFile(record.ArchivePath, data[:len(data)-64], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(record.ID); failure.Of(err) != failure.ArchiveDamaged {
		t.Fatalf("a truncated archive loaded: %v", err)
	}
	if list, _ := store.List(); len(list) != 0 {
		t.Errorf("a damaged package is listed: %v", list)
	}
	// Creating it again moves the damaged copy aside and writes it fresh.
	again, existed, err := store.Create(plan, Options{})
	if err != nil || existed || again.ID != record.ID {
		t.Fatalf("creating a damaged package again: %v %v", err, existed)
	}
	if _, err := store.Get(record.ID); err != nil {
		t.Errorf("the fresh package: %v", err)
	}
	if entries, _ := filepath.Glob(filepath.Join(store.Dir, ".damaged-"+record.ID+"-*")); len(entries) != 1 {
		t.Errorf("the damaged copy was not kept aside: %v", entries)
	}
}

func TestOnlyAFinishedQuake2BuildIsPackaged(t *testing.T) {
	w := newWorld(t)
	other := *w.manifest
	other.EngineFamily = "quake3"
	if _, err := Prepare(Request{Manifest: &other}); !errors.Is(err, ErrNotQuake2) {
		t.Errorf("a Quake III build: %v", err)
	}
	failed := *w.manifest
	failed.State = job.Failed
	if _, err := Prepare(Request{Manifest: &failed}); !errors.Is(err, build.ErrNotPlayable) {
		t.Errorf("a failed build: %v", err)
	}
	q1 := *w.manifest
	q1BSP := filepath.Join(t.TempDir(), "q1.bsp")
	os.WriteFile(q1BSP, append([]byte{29, 0, 0, 0}, make([]byte, 200)...), 0o644)
	q1.Outputs = []build.FileRecord{{Name: "bsp", Path: q1BSP}}
	if _, err := Prepare(Request{Manifest: &q1}); failure.Of(err) != failure.OutputInvalid {
		t.Errorf("a BSP29 output: %v", err)
	}
}

// A build started from the Build page names no folders: they are the compiling
// tool's own setup, recorded with the tool (Q2_009, found live — the panel
// said the build "records no base game folder").
func TestTheFoldersOfABuildFromTheBuildPageAreTheCompilingTools(t *testing.T) {
	w := newWorld(t)
	manifest := *w.manifest
	manifest.Roots = nil
	manifest.Steps = []build.Step{{ID: "compile", Capability: "q2.bsp.compile", Profile: build.DocumentRef{ID: "local.tool.q2"}}}
	manifest.Tools = []build.ToolRecord{
		{Profile: build.DocumentRef{ID: "another.tool"}, Roots: map[string]string{profile.RootGame: t.TempDir()}},
		{Profile: build.DocumentRef{ID: "local.tool.q2"}, Roots: map[string]string{profile.RootGame: w.base, profile.RootContent: w.content}},
	}
	plan, err := Prepare(Request{Manifest: &manifest, Grants: []Grant{{Basis: BasisOwnWork, Loose: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Problems) != 0 || len(plan.Members) != 13 {
		t.Fatalf("problems %v, %d members", plan.Problems, len(plan.Members))
	}
}
