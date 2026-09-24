package q3deps_test

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3deps"
)

const (
	fixtureMap     = "testdata/maps/aucom-fixture.map"
	fixtureContent = "testdata/content"
	compilerReport = "testdata/q3map2-2.5.17n-report.txt"
)

// The property this package exists for, checked against the program it exists
// to agree with.
//
// `testdata/q3map2-2.5.17n-report.txt` is not a hand-written expectation: it is
// what Q3Map2 2.5.17n actually printed when it compiled `testdata/maps/
// aucom-fixture.map` against `testdata/content`, kept so that the agreement can
// be re-checked on a machine that has no compiler. The fixture is built to make
// the two interesting cases both appear: a name nothing defines, and a shader
// that IS defined and whose image is missing — which a scan that only looked
// for files of the shader's own name would call satisfied.
//
// Note what the compiler did with all of it: exit 0. Two warnings, one error
// about a model it could not open, and a BSP written anyway. That is why this
// review has to exist somewhere else.
func TestTheScanAgreesWithTheCompilerAboutWhatIsMissing(t *testing.T) {
	report, err := q3deps.Discover([]string{fixtureMap}, q3deps.Scan{
		ContentRoots: []string{fixtureContent},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	scanned := map[string]bool{}
	for _, resolution := range report.Resolutions {
		if resolution.Kind == q3deps.KindShader && resolution.Status == q3deps.StatusMissing {
			scanned[resolution.Name] = true
		}
	}
	compiler := shadersTheCompilerCouldNotFind(t)
	if len(compiler) == 0 {
		t.Fatal("the recorded compiler report names no missing shader; the fixture has stopped testing anything")
	}
	for _, name := range compiler {
		if !scanned[name] {
			t.Errorf("Q3Map2 could not find an image for %q and this scan did not report it missing", name)
		}
		delete(scanned, name)
	}
	for name := range scanned {
		t.Errorf("this scan reports %q missing and Q3Map2 compiled it without complaint", name)
	}
}

// shadersTheCompilerCouldNotFind reads the recorded run.
func shadersTheCompilerCouldNotFind(t *testing.T) []string {
	t.Helper()
	file, err := os.Open(compilerReport)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer file.Close()
	const marker = "WARNING: Couldn't find image for shader "
	var names []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if _, name, found := strings.Cut(scanner.Text(), marker); found {
			names = append(names, strings.TrimSpace(name))
		}
	}
	sort.Strings(names)
	return names
}

// Every reference the fixture map makes, and the answer each one gets when the
// content is on disk and nothing is packaged yet.
func TestTheFixtureResolvesEveryReference(t *testing.T) {
	report, err := q3deps.Discover([]string{fixtureMap}, q3deps.Scan{
		ContentRoots: []string{fixtureContent},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	want := map[string]q3deps.Status{
		// Defined by the fixture's shader script, image present.
		"textures/aucom/wall": q3deps.StatusUnpackaged,
		// No shader at all: a plain image, found by extension search.
		"textures/aucom/floor": q3deps.StatusUnpackaged,
		// Nothing defines it and no image has its name.
		"textures/aucom/missing": q3deps.StatusMissing,
		// Defined, and its stage names an image that is not there.
		"textures/aucom/brokenshader": q3deps.StatusMissing,
		// An entity's own files.
		"models/aucom/marker.md3": q3deps.StatusMissing,
		"music/aucom/theme.wav":   q3deps.StatusMissing,
	}
	got := map[string]q3deps.Status{}
	for _, resolution := range report.Resolutions {
		got[resolution.Name] = resolution.Status
	}
	for name, status := range want {
		if got[name] != status {
			t.Errorf("%s resolved to %q, want %q", name, got[name], status)
		}
		delete(got, name)
	}
	for name, status := range got {
		t.Errorf("the scan found %s (%s), which the fixture does not reference", name, status)
	}
}

// The patch is why the parser reads more than brush faces. `patchDef2` names
// its shader the same way a face does — measured: writing it with the
// `textures/` prefix produces `textures/textures/…` — so a scan that skipped
// patches would miss every curved surface's texture.
func TestAPatchIsADependencyToo(t *testing.T) {
	references, err := q3deps.ParseMap(fixtureMap)
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, reference := range references {
		if reference.Name == "textures/aucom/wall" && strings.Contains(reference.From, "patch") {
			return
		}
	}
	// The wall is used on faces and on the patch, and they merge into one
	// reference, so the `From` of the first one seen wins. Check the count
	// instead: a scan that ignored the patch would see fewer uses.
	for _, reference := range references {
		if reference.Name == "textures/aucom/wall" && reference.Count > 1 {
			return
		}
	}
	t.Error("the patch's shader is not among the references")
}

// The doubled prefix, which is an authoring mistake that reads like a missing
// file. Measured on Q3Map2 2.5.17n: `textures/aucom/wall` written on a face
// produces `Couldn't find image for shader textures/textures/aucom/wall`.
func TestAFaceWrittenWithTheTexturesPrefixIsNamedAsSuch(t *testing.T) {
	source := filepath.Join(t.TempDir(), "prefixed.map")
	if err := os.WriteFile(source, []byte(`{
"classname" "worldspawn"
{
( -16 528 0 ) ( 528 528 0 ) ( 528 -16 0 ) textures/aucom/wall 0 0 0 0.5 0.5 0 0 0
( -16 -16 -16 ) ( 528 -16 -16 ) ( 528 528 -16 ) textures/aucom/wall 0 0 0 0.5 0.5 0 0 0
( 528 -16 0 ) ( 528 528 0 ) ( 528 528 -16 ) textures/aucom/wall 0 0 0 0.5 0.5 0 0 0
}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	references, err := q3deps.ParseMap(source)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(references) != 1 {
		t.Fatalf("%d references, want one", len(references))
	}
	if references[0].Name != "textures/textures/aucom/wall" {
		t.Errorf("the name resolved to %q; Q3Map2 prepends `textures/` whatever the face said",
			references[0].Name)
	}
	if references[0].Note == "" {
		t.Error("the doubled prefix is not named, so the report would say `missing` and leave the " +
			"reader looking for a file that was never the problem")
	}
}

// A dependency the package carries is packaged, and one it does not is held —
// which is the difference between a review and a formality.
func TestWhatTheArchiveCarriesIsPackagedAndTheRestIsHeld(t *testing.T) {
	members := map[string]string{
		"scripts/aucom.shader":    filepath.Join(fixtureContent, "scripts", "aucom.shader"),
		"textures/aucom/wall.tga": filepath.Join(fixtureContent, "textures", "aucom", "wall.tga"),
		"maps/aucom-fixture.bsp":  fixtureMap,
	}
	report, err := q3deps.Discover([]string{fixtureMap}, q3deps.Scan{
		Members:      members,
		ContentRoots: []string{fixtureContent},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	byName := map[string]q3deps.Resolution{}
	for _, resolution := range report.Resolutions {
		byName[resolution.Name] = resolution
	}
	if got := byName["textures/aucom/wall"].Status; got != q3deps.StatusPackaged {
		t.Errorf("the wall is %q; its script and its image are both in the archive", got)
	}
	if byName["textures/aucom/wall"].NeedsReview() {
		t.Error("a fully packaged dependency is held for review")
	}
	if got := byName["textures/aucom/floor"].Status; got != q3deps.StatusUnpackaged {
		t.Errorf("the floor is %q; it is the user's own file and the archive does not carry it", got)
	}
	if !byName["textures/aucom/floor"].NeedsReview() {
		t.Error("a dependency the package leaves out is not held for review, which is the silently " +
			"incomplete PK3 this package exists to prevent")
	}
	if report.Blocked() == nil {
		t.Error("a report with unaccounted references does not block")
	}
}

// A scan of a package that carries everything blocks nothing. Without this the
// gate could be "always refuse", which is not a review either.
func TestACompletePackageIsNotHeld(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	source := write("maps/complete.map", `{
"classname" "worldspawn"
{
( -16 528 0 ) ( 528 528 0 ) ( 528 -16 0 ) aucom/only 0 0 0 0.5 0.5 0 0 0
( -16 -16 -16 ) ( 528 -16 -16 ) ( 528 528 -16 ) aucom/only 0 0 0 0.5 0.5 0 0 0
( 528 -16 0 ) ( 528 528 0 ) ( 528 528 -16 ) aucom/only 0 0 0 0.5 0.5 0 0 0
}
}
`)
	image := write("textures/aucom/only.tga", "not really a tga, and the scan does not read it")
	report, err := q3deps.Discover([]string{source}, q3deps.Scan{
		Members: map[string]string{"textures/aucom/only.tga": image},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := report.Blocked(); err != nil {
		t.Errorf("a package that carries everything is blocked: %v", err)
	}
	if len(report.Reviewable()) != 0 {
		t.Errorf("%d references held for review", len(report.Reviewable()))
	}
}

// The report says what it did not look at. A review that quietly bounded itself
// is the thing this package is trying not to be.
func TestTheReportStatesItsOwnLimits(t *testing.T) {
	report, err := q3deps.Discover([]string{fixtureMap}, q3deps.Scan{
		ContentRoots: []string{fixtureContent},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(report.Limits) == 0 {
		t.Fatal("the report states no limits")
	}
	joined := strings.Join(report.Limits, "\n")
	for _, expected := range []string{"model", "base-game", "gamecode"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("the limits do not mention %q:\n%s", expected, joined)
		}
	}
	// And the model itself carries the sentence, wherever it resolved.
	for _, resolution := range report.Resolutions {
		if resolution.Kind != q3deps.KindModel {
			continue
		}
		if resolution.Review == "" {
			t.Error("a model reference is not held for review at all")
		}
	}
}
