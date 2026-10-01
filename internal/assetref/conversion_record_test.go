package assetref

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
)

// manifestConverter is the extractor's Quake III direction: a `.map`, and
// beside it a conversion manifest naming the digest of what it read and of
// what it wrote. `wrong` makes the manifest describe other bytes.
type manifestConverter struct{ wrong bool }

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *manifestConverter) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	var input, output string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--input":
			input = args[i+1]
		case "--output":
			output = args[i+1]
		}
	}
	source, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	stem := strings.TrimSuffix(filepath.Base(input), ".apmap")
	written := []byte("{\n\"classname\" \"worldspawn\"\n}\n")
	if err := os.WriteFile(filepath.Join(output, stem+".map"), written, 0o600); err != nil {
		return nil, err
	}
	sourceDigest := digestOf(source)
	if m.wrong {
		sourceDigest = digestOf([]byte("another map"))
	}
	manifest := fmt.Sprintf(`{"format":"auto-pigeon.q3map-conversion-manifest","format_version":1,
 "source":{"sha256":%q},"output":{"sha256":%q},
 "shaders":[{"name":"textures/a/b"},{"name":"textures/a/c"}],"models":[{"name":"models/x.md3"}],
 "warnings":[{"code":"q3map_patch_degenerate"}]}`, sourceDigest, digestOf(written))
	return []byte(`{}`), os.WriteFile(filepath.Join(output, stem+".q3map-manifest.json"), []byte(manifest), 0o600)
}

func (m *manifestConverter) Provenance() aue.Provenance {
	return aue.Provenance{Mode: aue.ModeDeveloperOverride}
}

// Q3_010: a build records WHICH document a converted `.map` came from. A local
// `.apmap` has no SourceRef, and before this its conversion left no record at
// all — only the digest of a `.map` nobody had seen.
func TestAConvertedInputRecordsTheDocumentItCameFrom(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "room.apmap")
	document := `{"apmap_version":"1.5","document_id":"room:full_map","revision":7,"game":"quake3","entities":[]}`
	if err := os.WriteFile(apmap, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, sources, conversions, err := ConvertAPMapInputsRecorded(context.Background(), &manifestConverter{},
		map[string]string{"source_map": apmap}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sources != nil {
		t.Errorf("a local input grew a source: %v", sources)
	}
	c, ok := conversions["source_map"]
	if !ok {
		t.Fatalf("no conversion was recorded: %v", conversions)
	}
	if c.From != "apmap" || c.To != "map" || c.Direction != "apmap-to-q3map" {
		t.Errorf("formats = %s -> %s by %s", c.From, c.To, c.Direction)
	}
	if c.DocumentID != "room:full_map" || c.DocumentRevision != 7 || c.APMapVersion != "1.5" || c.Game != "quake3" {
		t.Errorf("document identity = %+v", c)
	}
	if c.SourceSHA256 != "sha256:"+digestOf([]byte(document)) || c.SourceBytes != int64(len(document)) || c.SourceName != "room.apmap" {
		t.Errorf("source = %s %d %s", c.SourceName, c.SourceBytes, c.SourceSHA256)
	}
	if c.ConvertedBy != aue.ModeDeveloperOverride || c.ConverterVerified {
		t.Errorf("converter = %s verified=%t", c.ConvertedBy, c.ConverterVerified)
	}
	m := c.Manifest
	if m == nil || m.Name != "room.q3map-manifest.json" || m.Shaders != 2 || m.Models != 1 || m.Warnings != 1 ||
		m.Format != "auto-pigeon.q3map-conversion-manifest" || !strings.HasPrefix(m.SHA256, "sha256:") {
		t.Fatalf("manifest record = %+v", m)
	}
	if filepath.Dir(m.Path) != filepath.Dir(resolved["source_map"]) {
		t.Errorf("the manifest %s is not beside the .map %s", m.Path, resolved["source_map"])
	}
}

// A manifest left in the directory by another conversion describes another
// map. It is refused rather than kept as this build's record.
func TestAConversionManifestAboutOtherBytesIsRefused(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "room.apmap")
	if err := os.WriteFile(apmap, []byte(`{"game":"quake3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ConvertAPMapInputsRecorded(context.Background(), &manifestConverter{wrong: true},
		map[string]string{"source_map": apmap}, nil)
	if err == nil || failure.Of(err) != failure.ConversionRefused {
		t.Fatalf("err = %v (class %q)", err, failure.Of(err))
	}
	if !strings.Contains(err.Error(), "describes an APMap with digest") {
		t.Errorf("the refusal does not say what disagreed: %v", err)
	}
}

// A direction that writes no manifest — Quake 1, Quake II — still records the
// conversion; it just has no manifest to point at.
func TestAConversionWithNoManifestIsStillRecorded(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "e1m6.apmap")
	if err := os.WriteFile(apmap, []byte(`{"game":"quake1","document_id":"e1m6"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, conversions, err := ConvertAPMapInputsRecorded(context.Background(), &fakeConverter{},
		map[string]string{"source_map": apmap}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := conversions["source_map"]; c.Direction != "apmap-to-q1map" || c.Manifest != nil || c.DocumentID != "e1m6" {
		t.Errorf("conversion = %+v", c)
	}
}

func TestNoConverterAndAnUnknownGameAreClassed(t *testing.T) {
	stage := t.TempDir()
	apmap := filepath.Join(stage, "x.apmap")
	if err := os.WriteFile(apmap, []byte(`{"game":"doom"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ConvertAPMapInputsRecorded(context.Background(), nil, map[string]string{"m": apmap}, nil)
	if failure.Of(err) != failure.ConverterUnavailable {
		t.Errorf("no converter: class %q (%v)", failure.Of(err), err)
	}
	_, _, _, err = ConvertAPMapInputsRecorded(context.Background(), &fakeConverter{}, map[string]string{"m": apmap}, nil)
	if failure.Of(err) != failure.ConversionRefused {
		t.Errorf("unknown game: class %q (%v)", failure.Of(err), err)
	}
}
