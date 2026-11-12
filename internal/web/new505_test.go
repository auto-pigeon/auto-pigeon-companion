package web

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// Issue #9 (NEW_505): "Use in a build" → Build of a saved account revision
// sent the map and nothing for its textures, so a Quake 1 compile opened no
// WAD and the review said everything was in place. A Build-page build of a
// saved revision now compiles with that revision's verified texture bundle —
// the one cache Build & Run reads, keyed by map AND revision — and a folder
// the person chose stays the fallback.

// buildRun is one Build-page build, read back the way the page reads it.
type pageBuild struct {
	status   int
	body     map[string]any
	manifest map[string]any
	log      string
}

// buildSaved sends the build the Build page sends for a downloaded revision:
// no leak request, the map as an `aub:` input, a Textures folder when given.
func (m *machine) buildSaved(mapID string, folder string) pageBuild {
	m.t.Helper()
	request := map[string]any{
		"pipeline": "aucom.fixture.q1-leak",
		"inputs":   map[string]string{"source_map": "aub:map/" + mapID + "@" + m.backend.asset.revisionID + "#fixture.apmap"},
	}
	if folder != "" {
		request["roots"] = map[string]string{"content_root": folder}
	}
	run := pageBuild{}
	run.status, run.body = m.call(http.MethodPost, "/api/v1/build/runs", request)
	if run.status != http.StatusAccepted {
		return run
	}
	id, _ := run.body["build"].(string)
	run.manifest = m.waitForBuild(id)
	_, got := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	run.log, _ = got["log"].(string)
	return run
}

// buildTextureDirs is every job-owned Build-page texture copy left behind.
func (m *machine) buildTextureDirs() []string {
	entries, _ := os.ReadDir(m.builds)
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), buildTexturesPrefix) {
			out = append(out, entry.Name())
		}
	}
	return out
}

// notCarriedBundle is an export of WADs in order, carrying those in carried
// and naming the rest as installed WADs the deployment did not send.
func notCarriedBundle(mapID string, revision int, order []string, carried map[string][]byte) []byte {
	manifest := texturebundle.Manifest{SchemaVersion: texturebundle.Schema, MapID: mapID, MapName: "NEW_505 room",
		Revision: revision, Game: "quake1", ExportedAt: "2026-11-12T00:00:00Z", WADsDeclared: order,
		Requirements: []texturebundle.Requirement{}, Files: []texturebundle.File{}, CompilerReady: true,
		CompilerRefusals: []string{}}
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	add := func(name string, body []byte) {
		out, err := writer.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := out.Write(body); err != nil {
			panic(err)
		}
	}
	for index, name := range order {
		requirement := texturebundle.Requirement{Order: index, Name: name, Game: "quake1", Kind: "wad",
			Status: "resolved", Files: []texturebundle.File{}}
		if body, ok := carried[name]; ok {
			sum := sha256.Sum256(body)
			file := texturebundle.File{Path: name, Source: name, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(body))}
			requirement.Origin, requirement.Included, requirement.Files = "user", true, []texturebundle.File{file}
			manifest.Files = append(manifest.Files, file)
			add(name, body)
		} else {
			requirement.Origin, requirement.Note = "installed", "no redistribution permission on record"
			manifest.CompilerReady = false
			manifest.CompilerRefusals = append(manifest.CompilerRefusals, "wad_bytes_not_carried: "+name)
		}
		manifest.Requirements = append(manifest.Requirements, requirement)
	}
	add(texturebundle.LicensesName, []byte("# Attribution\n"))
	encoded, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	add(texturebundle.ManifestName, encoded)
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func TestABuildOfASavedRevisionCompilesWithItsVerifiedTextures(t *testing.T) {
	floor := fixturewad.WAD("n505_floor", 21)
	t.Run("the account's package, with no folder, then the exact revision offline", func(t *testing.T) {
		m, _ := newLeakMachine(t)
		m.saveRoom("new505a", 1, q1Room("new505.wad", false, "n505_floor"),
			leakBundle("new505a", 1, []string{"new505.wad"}, map[string][]byte{"new505.wad": floor}))
		run := m.buildSaved("new505a", "")
		if run.status != http.StatusAccepted || run.manifest["state"] != "succeeded" {
			t.Fatalf("a build of a saved revision with no folder: %d %v %v", run.status, run.body, run.manifest["state"])
		}
		opened := "Opened WAD: new505.wad sha256=" + sha256Hex(floor)
		for _, want := range []string{opened, "Textures: the texture bundle of saved revision 1"} {
			if !strings.Contains(run.log, want) {
				t.Errorf("the build log has no %q:\n%s", want, run.log)
			}
		}
		if strings.Contains(run.log, "unable to find texture n505") || strings.Contains(run.log, "not found") {
			t.Errorf("the compiler did not find the revision's textures:\n%s", run.log)
		}
		bundle := rootBundle(t, run.manifest)
		if bundle["map_id"] != "new505a" || bundle["revision"] != float64(1) {
			t.Errorf("the recorded bundle is %v", bundle)
		}
		args := strings.Join(compileArgs(run.manifest), " ")
		if !strings.Contains(args, "-wadpath "+filepath.Join(m.builds, buildTexturesPrefix)) {
			t.Errorf("the compiler did not read a copy this build owns: %s", args)
		}
		if left := m.buildTextureDirs(); len(left) != 0 {
			t.Errorf("the build's texture copy was left behind: %v", left)
		}

		// No connection: the exact revision is on this computer, map and
		// textures, and builds from there.
		m.backend.server.Close()
		offline := m.buildSaved("new505a", "")
		if offline.status != http.StatusAccepted || offline.manifest["state"] != "succeeded" ||
			!strings.Contains(offline.log, opened) {
			t.Fatalf("offline, the cached revision: %d %v\n%s", offline.status, offline.body, offline.log)
		}
		if digest := rootBundle(t, offline.manifest)["digest"]; digest != bundle["digest"] {
			t.Errorf("offline bundle %v, online %v", digest, bundle["digest"])
		}
	})

	t.Run("the package cannot be read: refused, and a chosen folder is the fallback", func(t *testing.T) {
		m, _ := newLeakMachine(t)
		m.saveRoom("new505b", 1, q1Room("new505.wad", false, "n505_floor"),
			leakBundle("new505b", 1, []string{"new505.wad"}, map[string][]byte{"new505.wad": floor}))
		m.backend.mu.Lock()
		m.backend.textureRefusal = http.StatusForbidden
		m.backend.mu.Unlock()
		jobs := len(m.jobIDs())
		refused := m.buildSaved("new505b", "")
		if refused.status < 400 || !strings.Contains(fmt.Sprint(refused.body["error"]), "needs its textures") {
			t.Fatalf("a build whose textures were refused: %d %v", refused.status, refused.body)
		}
		if len(m.jobIDs()) != jobs {
			t.Error("a refused build left a job behind")
		}

		folder := filepath.Join(m.dir, "my-wads")
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "new505.wad"), floor, 0o600); err != nil {
			t.Fatal(err)
		}
		fallback := m.buildSaved("new505b", folder)
		if fallback.status != http.StatusAccepted || fallback.manifest["state"] != "succeeded" {
			t.Fatalf("the folder fallback: %d %v", fallback.status, fallback.body)
		}
		if !strings.Contains(fallback.log, "the folder chosen on this page is used instead") ||
			!strings.Contains(strings.Join(compileArgs(fallback.manifest), " "), "-wadpath "+folder) {
			t.Errorf("the fallback did not use the folder, or did not say why:\n%s", fallback.log)
		}
	})

	t.Run("a cached bundle of another revision is never used", func(t *testing.T) {
		m, _ := newLeakMachine(t)
		m.saveRoom("new505c", 1, q1Room("new505.wad", false, "n505_floor"),
			leakBundle("new505c", 1, []string{"new505.wad"}, map[string][]byte{"new505.wad": floor}))
		if run := m.buildSaved("new505c", ""); run.manifest["state"] != "succeeded" {
			t.Fatalf("revision 1: %v %v", run.status, run.body)
		}
		// Revision 2 is saved; its textures cannot be fetched. Revision 1's
		// bundle is in the cache and must not stand in for it.
		m.saveRoom("new505c", 2, q1Room("new505.wad", false, "n505_floor"),
			leakBundle("new505c", 2, []string{"new505.wad"}, map[string][]byte{"new505.wad": floor}))
		m.backend.mu.Lock()
		m.backend.textureRefusal = http.StatusServiceUnavailable
		m.backend.mu.Unlock()
		run := m.buildSaved("new505c", "")
		if run.status < 400 || !strings.Contains(fmt.Sprint(run.body["error"]), "Saved revision 2 needs its textures") {
			t.Fatalf("revision 2 with only revision 1's bundle cached: %d %v", run.status, run.body)
		}
	})

	t.Run("an incomplete manifest is explained and nothing is built", func(t *testing.T) {
		m, _ := newLeakMachine(t)
		m.saveRoom("new505d", 1, q1Room("new505.wad;gone.wad", false, "n505_floor"),
			leakBundle("new505d", 1, []string{"new505.wad", "gone.wad"}, map[string][]byte{"new505.wad": floor}))
		jobs := len(m.jobIDs())
		run := m.buildSaved("new505d", "")
		message := fmt.Sprint(run.body["error"])
		if run.status != http.StatusConflict || !strings.Contains(message, "are incomplete") ||
			!strings.Contains(message, "gone.wad") || !strings.Contains(message, "texture_source_missing") {
			t.Fatalf("an incomplete bundle: %d %v", run.status, run.body)
		}
		if len(m.jobIDs()) != jobs || len(m.buildTextureDirs()) != 0 {
			t.Error("an incomplete bundle started a build or left a copy")
		}
	})

	t.Run("a WAD the deployment did not send is the person's own file, by exact name", func(t *testing.T) {
		m, _ := newLeakMachine(t)
		base := fixturewad.WAD("n505_base", 22)
		m.saveRoom("new505e", 1, q1Room("new505.wad;gfx.wad", false, "n505_floor", "n505_base"),
			notCarriedBundle("new505e", 1, []string{"new505.wad", "gfx.wad"}, map[string][]byte{"new505.wad": floor}))
		asked := m.buildSaved("new505e", "")
		if asked.status != http.StatusConflict || !strings.Contains(fmt.Sprint(asked.body["error"]), "gfx.wad") ||
			!strings.Contains(fmt.Sprint(asked.body["error"]), "choose the folder that holds your copy under Textures") {
			t.Fatalf("a bundle missing only the person's own WAD, no folder: %d %v", asked.status, asked.body)
		}

		folder := filepath.Join(m.dir, "own")
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
		// A file of the same name as the account's WAD, with other pixels:
		// it must not replace the verified one.
		if err := os.WriteFile(filepath.Join(folder, "new505.wad"), fixturewad.WAD("n505_floor", 99), 0o600); err != nil {
			t.Fatal(err)
		}
		empty := m.buildSaved("new505e", folder)
		if empty.status != http.StatusConflict || !strings.Contains(fmt.Sprint(empty.body["error"]), "has no gfx.wad") {
			t.Fatalf("a folder without the WAD: %d %v", empty.status, empty.body)
		}
		if err := os.WriteFile(filepath.Join(folder, "gfx.wad"), base, 0o600); err != nil {
			t.Fatal(err)
		}
		run := m.buildSaved("new505e", folder)
		if run.status != http.StatusAccepted || run.manifest["state"] != "succeeded" {
			t.Fatalf("with the person's own copy: %d %v", run.status, run.body)
		}
		for _, want := range []string{"Opened WAD: new505.wad sha256=" + sha256Hex(floor),
			"Opened WAD: gfx.wad sha256=" + sha256Hex(base), "completed with your own copy of gfx.wad"} {
			if !strings.Contains(run.log, want) {
				t.Errorf("the build log has no %q:\n%s", want, run.log)
			}
		}
		roots, _ := run.manifest["roots"].([]any)
		root, _ := roots[0].(map[string]any)
		source, _ := root["source"].(map[string]any)
		own, _ := source["own_files"].([]any)
		if source["kind"] != string(build.RootFromTextureBundle) || len(own) != 1 ||
			own[0].(map[string]any)["sha256"] != sha256Hex(base) {
			t.Errorf("the own copy is not recorded beside the bundle: %v", source)
		}
	})

	t.Run("the review names the bundle and copies nothing", func(t *testing.T) {
		m, _ := newLeakMachine(t)
		m.saveRoom("new505f", 1, q1Room("new505.wad", false, "n505_floor"),
			leakBundle("new505f", 1, []string{"new505.wad"}, map[string][]byte{"new505.wad": floor}))
		status, preview := m.call(http.MethodPost, "/api/v1/build/preview", map[string]any{
			"pipeline": "aucom.fixture.q1-leak",
			"inputs":   map[string]string{"source_map": "aub:map/new505f@" + m.backend.asset.revisionID + "#fixture.apmap"},
		})
		if status != http.StatusOK {
			t.Fatalf("preview: %d %v", status, preview)
		}
		cache, _ := m.server.textureCache()
		entry, found := cache.Lookup(texturebundle.Expect{MapID: "new505f", Revision: 1})
		if !found || rootBundle(t, preview)["map_id"] != "new505f" ||
			!strings.Contains(strings.Join(compileArgs(preview), " "), "-wadpath "+entry.ContentRoot) {
			t.Errorf("the review does not name the revision's bundle: %v", compileArgs(preview))
		}
		if len(m.buildTextureDirs()) != 0 {
			t.Error("a review copied textures")
		}
	})
}

// Issue #6 (NEW_505): a map card named only `wads_declared`, so a revision
// bound to a texture package — a Quake II namespace, a Quake III package —
// read "none declared". The summary names every source from the most
// authoritative document there is, and tells unavailable from empty.
func TestATextureSummaryNamesWhatARevisionIsBoundTo(t *testing.T) {
	requirements := func(game string, wads []string, items ...map[string]any) aub.MapTextures {
		raw, _ := json.Marshal(map[string]any{"map_id": "m", "revision": 3, "game": game, "wads_declared": wads,
			"requirements": items})
		var view aub.MapTextures
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	for _, tc := range []struct {
		name   string
		view   aub.MapTextures
		state  string
		chips  []string
		states []string
	}{
		{"a Quake II package and no declared WADs", requirements("quake2", nil,
			map[string]any{"name": "aup5050", "kind": "folder", "origin": "user", "status": "resolved", "resolved": true,
				"binding": map[string]any{"status": "bound", "revision": 2}},
			map[string]any{"name": "e1u1", "kind": "folder", "origin": "installed", "status": "resolved", "resolved": true}),
			texturesAvailable, []string{"aup5050", "e1u1"}, []string{packageBound, packageFromGame}},
		{"a bound package whose revision is gone", requirements("quake2", nil,
			map[string]any{"name": "aup5050", "origin": "user", "status": "missing",
				"binding": map[string]any{"status": "bound_revision_unavailable", "revision": 2}}),
			texturesUnavailable, []string{"aup5050"}, []string{packageUnavailable}},
		{"somebody else's private package", requirements("quake1", []string{"theirs.wad"},
			map[string]any{"name": "theirs.wad", "origin": "user", "status": "resolved", "resolved": true,
				"visibility": "private", "mine": false}),
			texturesUnavailable, []string{"theirs.wad"}, []string{packageUnavailable}},
		{"an installed WAD the deployment does not send", requirements("quake1", []string{"mine.wad", "gfx.wad"},
			map[string]any{"name": "mine.wad", "origin": "user", "status": "resolved", "resolved": true, "mine": true},
			map[string]any{"name": "gfx.wad", "origin": "installed", "status": "resolved", "resolved": true,
				"redistribution": map[string]any{"decision": "withheld", "reason": "undeclared"}}),
			texturesNeedsOwn, []string{"mine.wad", "gfx.wad"}, []string{packageAvailable, packageOwnCopy}},
		{"a declared WAD nothing in the account supplies", requirements("quake1", []string{"gone.wad"},
			map[string]any{"name": "gone.wad", "status": "missing"}),
			texturesNeedsOwn, []string{"gone.wad"}, []string{packageOwnCopy}},
		{"a Quake II namespace nothing resolves", requirements("quake2", nil,
			map[string]any{"name": "gone", "status": "missing"}),
			texturesUnavailable, []string{"gone"}, []string{packageMissing}},
		{"genuinely nothing", requirements("quake1", nil), texturesNoneDeclared, nil, nil},
		{"Quake III, which the account holds no texture source for", requirements("quake3", nil,
			map[string]any{"name": "x", "status": "unsupported_game"}), texturesGameFolders, nil, nil},
		{"an older account: the declaration alone", requirements("", []string{"a.wad"}),
			texturesAvailable, []string{"a.wad"}, []string{packageAvailable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := summaryFromRequirements(tc.view, nil)
			if summary.State != tc.state || len(summary.Packages) != len(tc.chips) {
				t.Fatalf("state %q with %v; want %q with %v", summary.State, summary.Packages, tc.state, tc.chips)
			}
			for i, item := range summary.Packages {
				if item.Name != tc.chips[i] || item.State != tc.states[i] {
					t.Errorf("source %d is %s/%s; want %s/%s", i, item.Name, item.State, tc.chips[i], tc.states[i])
				}
			}
		})
	}

	floor := fixturewad.WAD("n505_floor", 31)
	manifest := func(bundle []byte) texturebundle.Entry {
		cache, err := texturebundle.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		entry, err := cache.Publish(context.Background(), bundle, texturebundle.Expect{MapID: "m", Revision: 3})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	verified := summaryFromManifest(manifest(leakBundle("m", 3, []string{"a.wad"}, map[string][]byte{"a.wad": floor})))
	if verified.State != texturesVerified || verified.Packages[0].State != packageVerified || verified.From != "verified_cache" {
		t.Errorf("a complete cached manifest: %+v", verified)
	}
	own := summaryFromManifest(manifest(notCarriedBundle("m", 3, []string{"a.wad", "gfx.wad"}, map[string][]byte{"a.wad": floor})))
	if own.State != texturesNeedsOwn || own.Packages[1].State != packageOwnCopy {
		t.Errorf("a cached manifest without the person's own WAD: %+v", own)
	}
	incomplete := summaryFromManifest(manifest(leakBundle("m", 3, []string{"a.wad", "gone.wad"}, map[string][]byte{"a.wad": floor})))
	if incomplete.State != texturesUnavailable || len(incomplete.Refusals) == 0 {
		t.Errorf("an incomplete cached manifest: %+v", incomplete)
	}
}

// The card routes: My Maps reads the account's resolution, or the verified
// manifest once the revision is here; On this computer reads only the cache.
func TestTheMapCardsSayWhatTheTexturesAre(t *testing.T) {
	m, _ := newLeakMachine(t)
	floor := fixturewad.WAD("n505_floor", 41)
	m.saveRoom("new505g", 4, q1Room("new505.wad", false, "n505_floor"),
		leakBundle("new505g", 4, []string{"new505.wad"}, map[string][]byte{"new505.wad": floor}))
	m.backend.mu.Lock()
	m.backend.textureRequirements = map[string]any{"map_id": "new505g", "revision": 4, "game": "quake2",
		"wads_declared": []string{}, "texture_count": 2, "requirements": []map[string]any{
			{"name": "aup5050", "kind": "folder", "origin": "user", "status": "resolved", "resolved": true,
				"binding": map[string]any{"status": "bound", "revision": 7}}}}
	m.backend.mu.Unlock()
	status, body := m.call(http.MethodGet, "/api/v1/library/maps/new505g/textures", nil)
	summary, _ := body["summary"].(map[string]any)
	packages, _ := summary["packages"].([]any)
	if status != http.StatusOK || summary["state"] != texturesAvailable || len(packages) != 1 ||
		packages[0].(map[string]any)["name"] != "aup5050" || packages[0].(map[string]any)["revision"] != float64(7) {
		t.Fatalf("a package-bound revision with no declared WADs: %d %v", status, body)
	}

	// Downloaded: the card of this computer reads the verified manifest of
	// that revision, and so does My Maps for the same revision.
	if status, body := m.call(http.MethodPost, "/api/v1/library/sync", map[string]string{
		"asset_type": "map", "asset_id": "new505g", "revision": m.backend.asset.revisionID}); status != http.StatusOK {
		t.Fatalf("download: %d %v", status, body)
	}
	_, body = m.call(http.MethodGet, "/api/v1/library/maps/new505g/textures", nil)
	if summary, _ := body["summary"].(map[string]any); summary["from"] != "verified_cache" || summary["state"] != texturesVerified {
		t.Errorf("My Maps, revision on this computer: %v", body["summary"])
	}
	m.backend.server.Close()
	_, cached := m.call(http.MethodGet, "/api/v1/library/cached", nil)
	items, _ := cached["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("cached: %v", cached)
	}
	item := items[0].(map[string]any)
	offline, _ := item["texture_summary"].(map[string]any)
	if item["textures"] != "cached" || offline["state"] != texturesVerified || offline["revision"] != float64(4) {
		t.Errorf("On this computer, offline: textures %v, summary %v", item["textures"], offline)
	}
}

// Issue #9 for Quake II: a Build-page build of a saved revision bound to an
// account texture package compiles with that package's verified files laid
// over the content folder bound in Profiles, as Build & Run does; a binding
// whose revision is gone refuses the build.
func TestABuildOfASavedQuake2RevisionReadsItsBoundPackage(t *testing.T) {
	m := newMachine(t)
	m.prepareQ2Play(t)
	request := map[string]any{
		"pipeline": "aucom.fixture.q2-pipeline",
		"inputs": map[string]string{"source_map": "aub:map/" + m.backend.asset.assetID + "@" +
			m.backend.asset.revisionID + "#" + m.backend.asset.fileName},
	}
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", request)
	if status != http.StatusAccepted {
		t.Fatalf("a Quake II build of a saved revision: %d %v", status, body)
	}
	id := body["build"].(string)
	manifest := m.waitForBuild(id)
	_, got := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	log, _ := got["log"].(string)
	if manifest["state"] != "succeeded" || !strings.Contains(log, "verified account texture file(s) of "+q2AccountNamespace+" revision 2") {
		t.Fatalf("state %v; log:\n%s", manifest["state"], log)
	}
	roots, _ := manifest["roots"].([]any)
	found := false
	for _, raw := range roots {
		root, _ := raw.(map[string]any)
		if root["role"] == "content_root" {
			source, _ := root["source"].(map[string]any)
			bundle, _ := source["bundle"].(map[string]any)
			found = bundle["map_id"] == m.backend.asset.assetID
		}
	}
	if !found {
		t.Errorf("the build did not record the account package as its content root: %v", roots)
	}

	// A new revision, so the cache does not answer for it.
	m.backend.setAsset(func(asset *fixtureAsset) {
		asset.revision, asset.revisionID = asset.revision+1, asset.revisionID+"-next"
	})
	m.backend.mu.Lock()
	m.backend.textures = fixtureQ2TextureBundle(m.backend.asset.assetID, m.backend.asset.revision, "bound_revision_unavailable")
	m.backend.mu.Unlock()
	request["inputs"] = map[string]string{"source_map": "aub:map/" + m.backend.asset.assetID + "@" +
		m.backend.asset.revisionID + "#" + m.backend.asset.fileName}
	status, body = m.call(http.MethodPost, "/api/v1/build/runs", request)
	if status != http.StatusConflict || !strings.Contains(fmt.Sprint(body["error"]), "attach an available revision") {
		t.Errorf("a revision whose bound package revision is gone: %d %v", status, body)
	}
}
