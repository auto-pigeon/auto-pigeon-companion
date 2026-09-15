package assetref

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
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

func TestAnAccountMapIsConvertedToAMapAndTheManifestSaysBy(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "e1m6.apmap")
	if err := os.WriteFile(apmap, []byte(`{"apmap_version":"1.3","game":"quake1","entities":[]}`), 0o600); err != nil {
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
