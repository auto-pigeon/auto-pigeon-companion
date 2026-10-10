package pack_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_313A. A deployment's declaration that an installed WAD's exact bytes may
// be redistributed authorizes ONE thing: staging that WAD so a compiler can
// read it. It is not an authorization to package anything — not that WAD, and
// certainly not a pak file, a map or a model. The packaging policy's
// known-asset-digest rule has no input a texture bundle could reach, and this
// test holds it there.

func hexDigest(body []byte) string {
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

// declaredBundle is a 1.2 export carrying one installed WAD under a
// declaration, with its notice. The WAD is fixturewad's; it is nobody's
// texture.
func declaredBundle(t *testing.T, wad []byte) []byte {
	t.Helper()
	notice := []byte("Fixture textures by the Auto-Pigeon test suite.\n")
	member := map[string]any{"path": "fixture.wad", "source": "fixture.wad", "sha256": hexDigest(wad), "bytes": len(wad)}
	manifest := map[string]any{
		"schema_version": texturebundle.Schema,
		"map_id":         "map0000000001", "revision": 7, "game": "quake1",
		"wads_declared": []string{"fixture.wad"},
		"requirements": []map[string]any{{
			"order": 0, "name": "fixture.wad", "game": "quake1", "kind": "wad",
			"origin": "installed", "status": "resolved", "included": true,
			"files": []map[string]any{member},
			"redistribution": map[string]any{
				"decision": "included", "reason": "declared",
				"sha256": hexDigest(wad), "bytes": len(wad),
				"source": "Fixture texture set", "credit": "The Auto-Pigeon test suite",
				"terms": "Free to redistribute with this notice.", "notice_paths": []string{"NOTICES/CREDITS.txt"},
			},
		}},
		"files": []map[string]any{member},
		"notices": []map[string]any{{
			"path": "NOTICES/CREDITS.txt", "sha256": hexDigest(notice), "bytes": len(notice),
			"sources": []string{"fixture.wad"},
		}},
		"compiler_ready": true, "compiler_refusals": []string{},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	for name, body := range map[string][]byte{
		"fixture.wad": wad, "NOTICES/CREDITS.txt": notice, texturebundle.ManifestName: encoded,
	} {
		out, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, err = out.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}

	return buffer.Bytes()
}

func TestADeclaredInstalledWADDoesNotChangeWhatMayBePackaged(t *testing.T) {
	wad := fixturewad.WAD("fx_declared", 7)
	corpus := &pack.AssetCorpus{
		SchemaVersion: pack.AssetCorpusSchemaVersion, Source: "a test",
		Assets: []pack.KnownAsset{{SHA256: hexDigest(wad), Release: "a released file, for this test"}},
	}
	policy := pack.Policy{KnownAssets: corpus}
	candidates := []pack.Candidate{
		// The declared WAD itself, offered for packaging.
		{Path: "fixture.wad", Source: "/somewhere/fixture.wad", Size: int64(len(wad)), SHA256: hexDigest(wad)},
		// The same bytes renamed as game data, which a WAD declaration is
		// nothing to do with.
		{Path: "pak0.pak", Source: "/somewhere/id1/pak0.pak", Size: int64(len(wad)), SHA256: hexDigest(wad)},
		{Path: "maps/start.bsp", Source: "/somewhere/maps/start.bsp", Size: int64(len(wad)), SHA256: hexDigest(wad)},
		{Path: "progs/player.mdl", Source: "/somewhere/progs/player.mdl", Size: int64(len(wad)), SHA256: hexDigest(wad)},
	}
	before := make([]pack.Decision, 0, len(candidates))
	for _, candidate := range candidates {
		before = append(before, policy.Decide(candidate))
	}

	// The bundle is verified and published on this machine: the WAD is staged
	// for a compiler, under the deployment's declaration.
	cache, err := texturebundle.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := cache.Publish(context.Background(), declaredBundle(t, wad),
		texturebundle.Expect{MapID: "map0000000001", Revision: 7})
	if err != nil {
		t.Fatalf("publishing the declared bundle: %v", err)
	}
	if !entry.CompilerReady() || entry.Manifest.OrderedWADs()[0].Redistribution == nil {
		t.Fatalf("the fixture is not a declared, carried installed WAD: %+v", entry.Manifest.OrderedWADs())
	}

	for index, candidate := range candidates {
		decision := policy.Decide(candidate)
		if decision.Verdict != pack.Refuse || decision.Rule != "known-asset-digest" ||
			decision.Provenance != pack.ProvenanceKnownAsset {
			t.Errorf("%s: %s by %s (%s); a WAD declaration must not unlock packaging",
				candidate.Path, decision.Verdict, decision.Rule, decision.Provenance)
		}
		if !reflect.DeepEqual(before[index], decision) {
			t.Errorf("%s: the decision changed once a bundle declared those bytes:\n before %+v\n after  %+v",
				candidate.Path, before[index], decision)
		}
	}
	// Staging the bundle's verified copy of the file is no better evidence:
	// it is refused from inside the cache as it is from anywhere else.
	staged := filepath.Join(entry.ContentRoot, "fixture.wad")
	if decision := policy.Decide(pack.Candidate{
		Path: "fixture.wad", Source: staged, Size: int64(len(wad)), SHA256: hexDigest(wad),
	}); decision.Verdict != pack.Refuse || decision.Rule != "known-asset-digest" {
		t.Errorf("the bundle's staged copy: %s by %s", decision.Verdict, decision.Rule)
	}
}

// The packaging policy cannot read a texture bundle at all: it does not import
// the package that holds one. A declaration has no way in.
func TestThePackagingPolicyDoesNotReadTextureBundles(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files (%v)", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			if strings.Contains(imported.Path.Value, "/internal/texturebundle") ||
				strings.Contains(imported.Path.Value, "/internal/playrun") {
				t.Errorf("%s imports %s: packaging must not take evidence from a texture bundle",
					file, imported.Path.Value)
			}
		}
	}
}
