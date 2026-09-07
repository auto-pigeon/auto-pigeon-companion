package build

// Where an input came from, recorded on the build that read it.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestABuildRecordsTheExactRevisionItsInputCameFrom(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	source := h.sourceMap("e1m1.map", "{ }")

	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": source},
		Sources: map[string]SourceRef{"source_map": {
			Backend:        "http://localhost:9190",
			AssetType:      "map",
			AssetID:        "map0000000000001",
			DisplayName:    "e1m1",
			RevisionID:     "rev0000000000002",
			Revision:       2,
			Refetchable:    true,
			ManifestSHA256: "0a1b2c",
			ContentSHA256:  "3d4e5f",
			FetchedAt:      "2026-09-07T10:00:00Z",
		}},
	})

	input := inputNamed(t, manifest, "source_map")
	if input.Source == nil {
		t.Fatal("the input records no source")
	}
	if input.Source.RevisionID != "rev0000000000002" || input.Source.Revision != 2 {
		t.Errorf("source = %+v", input.Source)
	}
	if !input.Source.Refetchable {
		t.Error("an immutable revision was recorded as not re-fetchable")
	}
	// The digest is still there: the source says WHERE, the digest says WHAT, and
	// neither replaces the other.
	if input.SHA256 == "" {
		t.Error("the input lost its digest")
	}

	// And it survives a round trip through the file a build leaves behind.
	path := filepath.Join(manifest.Directory, ManifestFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no manifest on disk: %v", err)
	}
	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("re-reading the manifest: %v", err)
	}
	reloaded := inputNamed(t, loaded, "source_map")
	if reloaded.Source == nil || reloaded.Source.AssetID != "map0000000000001" {
		t.Errorf("the source did not survive the round trip: %+v", reloaded.Source)
	}
}

// An input the user pointed at on their own disk records NO source, rather than
// an empty one. Absence is the honest answer: it has no identity to record.
func TestALocalInputRecordsNoSourceAtAll(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	source := h.sourceMap("e1m1.map", "{ }")

	manifest := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": source},
	})

	input := inputNamed(t, manifest, "source_map")
	if input.Source != nil {
		t.Errorf("a local file recorded a source: %+v", input.Source)
	}

	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); contains(got, `"source"`) {
		t.Errorf("an absent source was serialized anyway: %s", got)
	}
}

// The reproducible key is over the RECIPE. The same bytes from a different
// backend are the same build, so the key must not move when only the provenance
// does — a key that included the origin would say "not reproducible" about a
// build that reproduced exactly.
func TestTheReproducibleKeyIgnoresWhereAnInputCameFrom(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	source := h.sourceMap("e1m1.map", "{ }")

	plain := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": source},
	})
	fromAUB := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": source},
		Sources: map[string]SourceRef{"source_map": {
			Backend: "http://localhost:9190", AssetType: "map",
			AssetID: "map1", RevisionID: "rev1", Revision: 1, Refetchable: true,
		}},
	})

	if plain.ReproducibleKey == "" {
		t.Fatal("no reproducible key")
	}
	if plain.ReproducibleKey != fromAUB.ReproducibleKey {
		t.Errorf("the key moved because the provenance did: %s vs %s",
			plain.ReproducibleKey, fromAUB.ReproducibleKey)
	}
}

// A version that only added optional fields is one an older manifest is still a
// valid instance of, so 1.0 stays readable. Anything else is refused rather than
// half-read.
func TestTheManifestReaderAcceptsBothPublishedVersionsAndNothingElse(t *testing.T) {
	dir := t.TempDir()

	// The version contains a `/`, so it is not a filename. Numbered instead.
	written := 0
	write := func(version string) string {
		written++
		path := filepath.Join(dir, fmt.Sprintf("manifest-%d.json", written))
		body, err := json.Marshal(Manifest{
			SchemaVersion: version, BuildID: "b1", Platform: "linux/amd64",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}

		return path
	}

	for _, version := range []string{"aucom.build-manifest/1.0", "aucom.build-manifest/1.1"} {
		if _, err := LoadManifest(write(version)); err != nil {
			t.Errorf("%s: %v", version, err)
		}
	}
	for _, version := range []string{"aucom.build-manifest/0.9", "aucom.build-manifest/2.0", ""} {
		if _, err := LoadManifest(write(version)); err == nil {
			t.Errorf("%q was accepted", version)
		}
	}
}

func inputNamed(t *testing.T, manifest *Manifest, name string) FileRecord {
	t.Helper()

	for _, input := range manifest.Inputs {
		if input.Name == name {
			return input
		}
	}
	t.Fatalf("no input named %q in %+v", name, manifest.Inputs)

	return FileRecord{}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}

	return -1
}
