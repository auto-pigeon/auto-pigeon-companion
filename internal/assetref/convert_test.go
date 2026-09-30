package assetref

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
)

// fakeConverter writes the .map the real extractor would, and records its argv.
type fakeConverter struct {
	args     []string
	verified bool
}

func (f *fakeConverter) Run(_ context.Context, subcommand string, args ...string) ([]byte, error) {
	f.args = append([]string{subcommand}, args...)
	var input, output string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--input":
			input = args[i+1]
		case "--output":
			output = args[i+1]
		}
	}
	stem := strings.TrimSuffix(filepath.Base(input), ".apmap")
	return []byte(`{}`), os.WriteFile(filepath.Join(output, stem+".map"), []byte("{ \"classname\" \"worldspawn\" }\n"), 0o600)
}

func (f *fakeConverter) Provenance() aue.Provenance {
	return aue.Provenance{Mode: aue.ModeDeveloperOverride, Verified: f.verified}
}

// The Companion reads an APMap's `game` and nothing else: which APMap versions can be converted is
// the extractor's judgement, made against the contract bundle it ships with, and this program has
// no copy of that contract to hold an opinion with. So the conversion is the same whatever the
// document declares — a legacy version, the current one, or one nobody has published — and a
// refusal, when there is one, is AUE's and arrives through runner.Run. Pinned across versions so
// that an APMap promotion never has to edit this test (AULIBS NEW_262 moved 1.3 to legacy; Q3_001
// moved 1.4, and "1.5" is here as one more version, not as "the current one").
func TestAnAccountMapIsConvertedToAMapAndTheManifestSaysBy(t *testing.T) {
	for _, version := range []string{"1.3", "1.4", "1.5", "999.999"} {
		t.Run(version, func(t *testing.T) { convertsAnAccountMap(t, version) })
	}
}

func convertsAnAccountMap(t *testing.T, version string) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "e1m6.apmap")
	document := `{"apmap_version":"` + version + `","game":"quake1","entities":[]}`
	if err := os.WriteFile(apmap, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeConverter{}
	sources := map[string]build.SourceRef{"source_map": {AssetType: "map", AssetID: "abc"}}
	resolved, sources, err := ConvertAPMapInputs(context.Background(), runner,
		map[string]string{"source_map": apmap, "wad": "/elsewhere/metal.wad"}, sources)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(resolved["source_map"]) != ".map" || resolved["wad"] != "/elsewhere/metal.wad" {
		t.Errorf("resolved = %v", resolved)
	}
	if strings.Join(runner.args[:2], " ") != "convert --apmap-to-q1map" {
		t.Errorf("argv = %v", runner.args)
	}
	ref := sources["source_map"]
	if ref.ConvertedTo != "map" || !strings.HasPrefix(ref.ConvertedSHA256, "sha256:") ||
		ref.ConvertedBy != aue.ModeDeveloperOverride || ref.ConverterVerified {
		t.Errorf("the manifest record does not say how the map was converted: %+v", ref)
	}
	if _, err := os.Stat(apmap); err != nil {
		t.Error("the APMap itself was not left in place")
	}
}

func TestAnAPMapWithNoConverterIsRefusedByName(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "dm2.apmap")
	if err := os.WriteFile(apmap, []byte(`{"game":"quake1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := ConvertAPMapInputs(context.Background(), nil, map[string]string{"source_map": apmap}, nil)
	if !errors.Is(err, ErrNoConverter) {
		t.Errorf("err = %v", err)
	}
}

func TestAnAPMapForAnUnknownGameIsRefused(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "x.apmap")
	if err := os.WriteFile(apmap, []byte(`{"game":"doom"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ConvertAPMapInputs(context.Background(), &fakeConverter{}, map[string]string{"m": apmap}, nil); err == nil ||
		!strings.Contains(err.Error(), "doom") {
		t.Errorf("err = %v", err)
	}
}

// A Quake III APMap (APMap 1.5 can hold patches and brush-primitives texture matrices) has no
// conversion direction in this build: the extractor's Quake III .map exporter lands with Q3_004.
// It is refused by its game, before the extractor is ever asked — never converted as a Quake 1 or
// Quake 2 map, which would drop exactly what makes it a Quake III map.
func TestAQuake3APMapIsRefusedBeforeAnyConversion(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "q3dm1.apmap")
	document := `{"apmap_version":"1.5","game":"quake3","map_dialect":"quake3_extended","entities":[]}`
	if err := os.WriteFile(apmap, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeConverter{}
	resolved, _, err := ConvertAPMapInputs(context.Background(), runner, map[string]string{"source_map": apmap}, nil)
	if err == nil || !strings.Contains(err.Error(), `"quake3"`) || !strings.Contains(err.Error(), "cannot turn into a .map") {
		t.Fatalf("err = %v, want the Quake III APMap refused by its game", err)
	}
	if resolved != nil {
		t.Errorf("resolved = %v alongside the refusal", resolved)
	}
	if runner.args != nil {
		t.Errorf("the extractor was asked to convert a Quake III APMap: %v", runner.args)
	}
	if entries, _ := os.ReadDir(stage); len(entries) != 1 {
		t.Errorf("the stage holds %d entries; the refusal must create nothing", len(entries))
	}
}
