package binding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

const fakeDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func sampleBinding(t *testing.T) LocalBinding {
	t.Helper()
	base := t.TempDir()
	return LocalBinding{
		SchemaVersion:  SchemaVersion,
		ProfileID:      "example.andrea.q1-compile",
		ProfileVersion: "0.3.0",
		ProfileDigest:  fakeDigest,
		Trust:          profile.TrustCommunity,
		Acquisition:    profile.AcquireUserPath,
		Executables:    map[string]string{"qbsp": filepath.Join(base, "tools", "ericw", "qbsp")},
		Arguments:      map[string][]string{"qbsp": {"-nopercent"}},
		Roots: map[string]string{
			profile.RootGame:    filepath.Join(base, "games", "quake"),
			profile.RootProject: filepath.Join(base, "projects", "mymap"),
		},
		ResolvedVersion:  "0.18.1",
		VersionCheckedAt: time.Now().UTC(),
		GameProfileID:    "pb7fd91k2j3aa4c",
		Grant: &profile.Grant{
			ProfileID: "example.andrea.q1-compile",
			Version:   "0.3.0",
			Digest:    fakeDigest,
			Trust:     profile.TrustCommunity,
			Granted:   []string{profile.PermRunExecutable, profile.PermRead(profile.RootWorkspace), profile.PermWrite(profile.RootWorkspace)},
			GrantedAt: time.Now().UTC(),
		},
		UpdatedAt: time.Now().UTC(),
	}
}

// The export guarantee, from the content side.
//
// The structural side is the package graph: internal/binding imports
// internal/profile, so no profile type can name a binding type, and
// `TestProfilePackageDoesNotImportBinding` in that package asserts the
// direction. This test covers the mistake the type system cannot catch — a
// person copying a value out of a binding and pasting it into a document.
func TestNothingFromABindingCanEnterAPortableProfile(t *testing.T) {
	binding := sampleBinding(t)

	leaks := map[string]string{
		"an executable path": binding.Executables["qbsp"],
		"a game root":        binding.Roots[profile.RootGame],
		"a project root":     binding.Roots[profile.RootProject],
	}
	for what, value := range leaks {
		t.Run(what, func(t *testing.T) {
			if err := profile.CheckPortable(map[string]any{"summary": value}); err == nil {
				t.Errorf("%s (%q) was accepted in a portable document", what, value)
			}
		})
	}

	// The whole record, serialized, is refused too — so even a future mistake
	// that embedded one wholesale would not produce a publishable document.
	data, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("%v", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("%v", err)
	}
	if err := profile.CheckPortable(tree); err == nil {
		t.Error("a whole local binding passed the portability check")
	}
}

// A token in a document that gets shared is a token that has been disclosed.
func TestATokenCannotEnterAPortableProfile(t *testing.T) {
	for _, secret := range []string{
		"eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InVzZXIifQ.c2lnbmF0dXJlLXZhbHVlLWhlcmU",
		"Authorization: Bearer pb_abcdef0123456789",
	} {
		if err := profile.CheckPortable(map[string]any{"description": secret}); err == nil {
			t.Errorf("a credential was accepted in a portable document: %q", secret)
		}
	}
}

// The AUB record id is kept out structurally rather than by a text rule: the
// portable Game Profile reference has no member to put it in.
func TestAnAubRecordIdHasNowhereToGoInAPortableProfile(t *testing.T) {
	document := []byte(`{
      "schema_version": "aucom.profile/1.0",
      "kind": "tool",
      "id": "example.minimal",
      "version": "1.0.0",
      "name": "Minimal",
      "summary": "A tool profile trying to pin an AUB record id.",
      "publisher": {"name": "Example"},
      "license": {"spdx": "MIT"},
      "game_profile": {"slug": "quake1", "engine_family": "quake1", "profile_id": "pb7fd91k2j3aa4c"},
      "tool_version": "1.0.0",
      "platforms": [{"platform": {"os": "linux", "arch": "amd64"}, "status": "supported"}],
      "acquisition": [{"mode": "system_path", "title": "On PATH", "commands": ["example"]}],
      "executables": [{"name": "main", "file": "example{platform.exe_suffix}"}],
      "actions": [{"id": "run", "title": "Run it", "executable": "main", "args": ["--help"]}]
    }`)
	_, err := profile.Decode(document)
	if err == nil {
		t.Fatal("a portable profile carried an AUB record id")
	}
	if !strings.Contains(err.Error(), "game_profile.profile_id") {
		t.Errorf("the refusal does not locate the member:\n%v", err)
	}
}

// A binding's rules are the inverse of a profile's: here a relative path is the
// fault, because a binding is resolved against nothing.
func TestBindingRequiresAbsolutePaths(t *testing.T) {
	b := sampleBinding(t)
	b.Executables["qbsp"] = "tools/qbsp"
	err := b.Validate()
	if err == nil {
		t.Fatal("a relative executable path was accepted")
	}
	if !strings.Contains(err.Error(), "not absolute") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestBindingRefusesToStoreAJobWorkspace(t *testing.T) {
	b := sampleBinding(t)
	b.Roots[profile.RootWorkspace] = filepath.Join(t.TempDir(), "job-1")
	err := b.Validate()
	if err == nil {
		t.Fatal("a per-job workspace was stored in a binding")
	}
	if !strings.Contains(err.Error(), "created per job") {
		t.Errorf("the error does not explain why: %v", err)
	}
}

func TestBindingRefusesAGrantForADifferentDocument(t *testing.T) {
	b := sampleBinding(t)
	b.Grant.Digest = "sha256:" + strings.Repeat("2", 64)
	err := b.Validate()
	if err == nil {
		t.Fatal("a grant against a different document was accepted")
	}
	if !strings.Contains(err.Error(), "changed after it was approved") {
		t.Errorf("the error does not explain the mismatch: %v", err)
	}
}

func TestBindingRefusesAVersionWithNoTimestamp(t *testing.T) {
	b := sampleBinding(t)
	b.VersionCheckedAt = time.Time{}
	if err := b.Validate(); err == nil {
		t.Fatal("a recorded tool version with no timestamp was accepted")
	}
}

func TestStalenessIsAboutWhenTheToolWasAsked(t *testing.T) {
	b := sampleBinding(t)
	now := time.Now().UTC()
	b.VersionCheckedAt = now.Add(-2 * time.Hour)
	if b.Stale(now, 24*time.Hour) {
		t.Error("a two-hour-old probe was called stale against a one-day limit")
	}
	if !b.Stale(now, time.Hour) {
		t.Error("a two-hour-old probe was not stale against a one-hour limit")
	}
	b.ResolvedVersion = ""
	if !b.Stale(now, 24*time.Hour) {
		t.Error("a binding that has never been probed was not stale")
	}
}

// The binding supplies the machine half of a resolution, and the workspace is a
// parameter rather than stored state.
func TestRequestCarriesTheWorkspaceWithoutStoringIt(t *testing.T) {
	b := sampleBinding(t)
	workspace := filepath.Join(t.TempDir(), "job-7")
	request := b.Request(profile.Platform{OS: "linux", Arch: "amd64"}, workspace, nil, nil, nil)

	if request.Roots[profile.RootWorkspace] != workspace {
		t.Errorf("the workspace did not reach the request: %v", request.Roots)
	}
	if _, stored := b.Roots[profile.RootWorkspace]; stored {
		t.Error("Request wrote the workspace back into the binding")
	}
	if request.Executables["qbsp"] != b.Executables["qbsp"] {
		t.Error("the resolved executable did not reach the request")
	}
	// The maps are copies: a resolution must not be able to edit stored state.
	request.Roots[profile.RootGame] = "changed"
	if b.Roots[profile.RootGame] == "changed" {
		t.Error("the request shares its root map with the binding")
	}
}

func TestAuthorizeGoesThroughTheProfilePackage(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "profile", "testdata", "community", "user-q1-toolchain.tool.json"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	p, err := profile.Decode(data)
	if err != nil {
		t.Fatalf("%v", err)
	}
	digest, err := profile.Digest(p)
	if err != nil {
		t.Fatalf("%v", err)
	}

	b := sampleBinding(t)
	b.ProfileDigest = digest
	b.Grant = nil
	if err := b.Authorize(p); err == nil {
		t.Fatal("an ungranted profile was authorized")
	}

	b.Grant = &profile.Grant{
		ProfileID: p.Metadata().ID, Version: p.Metadata().Version, Digest: digest,
		Trust: profile.TrustCommunity, Granted: profile.PermissionIDs(p), GrantedAt: time.Now().UTC(),
	}
	b.ProfileID = p.Metadata().ID
	if err := b.Validate(); err != nil {
		t.Fatalf("the binding is invalid: %v", err)
	}
	if err := b.Authorize(p); err != nil {
		t.Fatalf("a granted profile was refused: %v", err)
	}
}

func TestSetRoundTripsAndRefusesWhatItDoesNotUnderstand(t *testing.T) {
	set := NewSet()
	if err := set.Put(sampleBinding(t)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	data, err := set.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	loaded, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, found := loaded.Find("example.andrea.q1-compile"); !found {
		t.Error("the binding did not survive the round trip")
	}
	if !loaded.Remove("example.andrea.q1-compile") {
		t.Error("Remove reported nothing to remove")
	}
	if len(loaded.Bindings) != 0 {
		t.Error("Remove left the binding behind")
	}

	if _, err := Load([]byte(`{"schema_version":"aucom.local-binding/1.0","bindings":[],"extra":1}`)); err == nil {
		t.Error("a store with a member this build does not understand was accepted")
	}
	if _, err := Load([]byte(`{"schema_version":"aucom.local-binding/0.9","bindings":[]}`)); err == nil {
		t.Error("a store written by a different schema version was accepted")
	}
}

func TestPutRefusesAnInvalidBinding(t *testing.T) {
	set := NewSet()
	b := sampleBinding(t)
	b.ProfileDigest = ""
	if err := set.Put(b); err == nil {
		t.Fatal("a binding with no digest was stored")
	}
	if len(set.Bindings) != 0 {
		t.Error("the invalid binding was stored anyway")
	}
}

// The local binding format is published as a schema too — not so it can be
// shared, but so the file a user may have to read and repair is documented and
// versioned.
func TestTheLocalBindingSchemaIsPublishedAndMatchesTheStoredShape(t *testing.T) {
	raw, err := profile.SchemaFile("local-binding-1.3.schema.json")
	if err != nil {
		t.Fatalf("%v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("%v", err)
	}
	defs, _ := schema["$defs"].(map[string]any)
	binding, _ := defs["localBinding"].(map[string]any)
	properties, _ := binding["properties"].(map[string]any)
	if len(properties) == 0 {
		t.Fatal("the schema has no localBinding definition")
	}

	set := NewSet()
	if err := set.Put(sampleBinding(t)); err != nil {
		t.Fatalf("%v", err)
	}
	data, err := set.Marshal()
	if err != nil {
		t.Fatalf("%v", err)
	}
	var stored struct {
		Bindings []map[string]any `json:"bindings"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("%v", err)
	}
	for member := range stored.Bindings[0] {
		if _, described := properties[member]; !described {
			t.Errorf("the stored binding has a %q member that the published schema does not describe", member)
		}
	}
	if schema["$id"] == nil {
		t.Error("the schema has no $id")
	}
	if got := schema["properties"].(map[string]any)["schema_version"].(map[string]any)["const"]; got != SchemaVersion {
		t.Errorf("the schema pins %v, the code writes %q", got, SchemaVersion)
	}
}
