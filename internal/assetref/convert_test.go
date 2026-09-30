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

// A Quake III APMap is converted by the extractor's own Quake III direction (Q3_004) — never by the
// Quake 1 or Quake 2 one, which would drop exactly what makes it a Quake III map — and the manifest
// records the conversion like any other.
func TestAQuake3APMapIsConvertedByTheQuake3Direction(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "q3dm1.apmap")
	document := `{"apmap_version":"1.5","game":"quake3","map_dialect":"quake3_extended","entities":[]}`
	if err := os.WriteFile(apmap, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeConverter{}
	sources := map[string]build.SourceRef{"source_map": {AssetType: "map", AssetID: "q3"}}
	resolved, sources, err := ConvertAPMapInputs(context.Background(), runner, map[string]string{"source_map": apmap}, sources)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(runner.args[:2], " ") != "convert --apmap-to-q3map" {
		t.Fatalf("argv = %v, want the Quake III direction", runner.args)
	}
	if filepath.Base(resolved["source_map"]) != "q3dm1.map" || sources["source_map"].ConvertedTo != "map" {
		t.Errorf("resolved = %v, ref = %+v", resolved, sources["source_map"])
	}
}

// An extractor that refuses (an older build without the direction, or a document the Quake III
// writer will not write) fails the conversion with its own words, and nothing is resolved.
func TestAQuake3ConversionRefusedByTheExtractorIsReported(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "q3dm1.apmap")
	if err := os.WriteFile(apmap, []byte(`{"game":"quake3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("apmap-to-q3map refused [q3map_shader_unsafe]: fac_1 (entities[0].content[2].faces[0]): shader \"../evil\" is not a safe Quake III shader path")
	resolved, _, err := ConvertAPMapInputs(context.Background(), &refusingConverter{err: refusal}, map[string]string{"source_map": apmap}, nil)
	if err == nil || !strings.Contains(err.Error(), "q3map_shader_unsafe") || resolved != nil {
		t.Fatalf("err = %v, resolved = %v", err, resolved)
	}
}

type refusingConverter struct{ err error }

func (r *refusingConverter) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, r.err
}

func (r *refusingConverter) Provenance() aue.Provenance {
	return aue.Provenance{Mode: aue.ModeDeveloperOverride}
}
