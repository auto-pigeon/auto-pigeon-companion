package binding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_323E: a binding store written by a build before 1.4 is read, written back
// as 1.4, and read again after a restart with every binding and every grant
// exactly as it was.
//
// And the limit that goes with it, said here because it is a limit of the
// file, not of this build: once a store is written it says 1.4, and a build
// that reads only up to 1.3 refuses the whole file. Nothing downgrades a store.
// Going back to an older Companion needs the copy of the store made BEFORE
// this build first wrote it.
//
// testdata/store-1.3.json is authored. AUCOM_TEST_BINDING_STORE names a real
// store to read as well (a COPY: this test writes only into its own temporary
// directory, never to the named file).
func TestAnOlderBindingStoreIsReadWrittenAtThisVersionAndUnchanged(t *testing.T) {
	sources := []string{"testdata/store-1.3.json"}
	if named := os.Getenv("AUCOM_TEST_BINDING_STORE"); named != "" {
		sources = append(sources, named)
	} else {
		t.Log("AUCOM_TEST_BINDING_STORE is not set; only the authored 1.3 store was read")
	}
	for _, source := range sources {
		t.Run(filepath.Base(source), func(t *testing.T) {
			original, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS == "windows" && source == sources[0] {
				// The authored store names POSIX paths, and a binding's paths
				// must be absolute on the machine that reads it: give them a
				// drive. Nothing else about the store differs.
				original = []byte(strings.NewReplacer(`"/opt/`, `"C:/opt/`, `"/home/`, `"C:/home/`).Replace(string(original)))
			}
			var raw struct {
				SchemaVersion string           `json:"schema_version"`
				Bindings      []map[string]any `json:"bindings"`
			}
			if err := json.Unmarshal(original, &raw); err != nil {
				t.Fatal(err)
			}
			if raw.SchemaVersion == SchemaVersion {
				t.Fatalf("%s is already %s; it is not an older store", source, SchemaVersion)
			}
			path := filepath.Join(t.TempDir(), "bindings.json")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}

			// Reading alone changes nothing on disk.
			before, err := LoadFile(path)
			if err != nil {
				t.Fatalf("this build does not read a %s store: %v", raw.SchemaVersion, err)
			}
			if unread, _ := os.ReadFile(path); string(unread) != string(original) {
				t.Fatal("reading the store rewrote it")
			}

			// The first write of any kind stamps the whole file.
			if err := SaveFile(path, before); err != nil {
				t.Fatal(err)
			}
			written, _ := os.ReadFile(path)
			var stamped struct {
				SchemaVersion string           `json:"schema_version"`
				Bindings      []map[string]any `json:"bindings"`
			}
			if err := json.Unmarshal(written, &stamped); err != nil {
				t.Fatal(err)
			}
			if stamped.SchemaVersion != SchemaVersion {
				t.Fatalf("the written store says %s", stamped.SchemaVersion)
			}

			// A restart: nothing in memory, the file is all there is.
			after, err := LoadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Bindings) != len(raw.Bindings) {
				t.Fatalf("%d bindings were stored, %d came back", len(raw.Bindings), len(after.Bindings))
			}
			grants := 0
			for i, was := range raw.Bindings {
				now := stamped.Bindings[i]
				if now["schema_version"] != SchemaVersion {
					t.Errorf("%v is stamped %v", now["profile_id"], now["schema_version"])
				}
				// Member for member, as JSON: paths, roots, digests, trust,
				// timestamps, the grant with its own digest and time.
				delete(was, "schema_version")
				delete(now, "schema_version")
				if !reflect.DeepEqual(was, now) {
					t.Errorf("%v changed on the way through:\n was %v\n now %v", was["profile_id"], was, now)
				}
				if was["grant"] != nil {
					grants++
				}
				if (after.Bindings[i].Grant != nil) != (was["grant"] != nil) {
					t.Errorf("%v: grant present before %v, after %v", was["profile_id"], was["grant"] != nil, after.Bindings[i].Grant != nil)
				}
				if len(after.Bindings[i].Compatibility) != 0 {
					t.Errorf("%v gained compatibility evidence nobody measured", was["profile_id"])
				}
			}
			t.Logf("%s: %s -> %s, %d bindings, %d grants, all equal", filepath.Base(source), raw.SchemaVersion, SchemaVersion, len(raw.Bindings), grants)
		})
	}
}

// The other direction, as far as one build can show it: a store from a version
// this build does not list is refused whole, with the versions it does read.
// That is exactly what a 1.3-only build says about a 1.4 store.
func TestAStoreFromAVersionThisBuildDoesNotListIsRefusedWhole(t *testing.T) {
	if !profile.LocalBindingSchemaSupported("aucom.local-binding/1.3") || !profile.LocalBindingSchemaSupported(SchemaVersion) {
		t.Fatalf("this build must read 1.3 and %s", SchemaVersion)
	}
	_, err := Load([]byte(`{"schema_version":"aucom.local-binding/9.9","bindings":[]}`))
	if err == nil || !strings.Contains(err.Error(), "this build reads") {
		t.Fatalf("a store from an unknown version: %v", err)
	}
}
