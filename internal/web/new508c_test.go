package web

import (
	"context"
	"net/http"
	"testing"
)

// Issue #38. "Download revision" said a map was ready to build and stopped at
// the map: a build's second step fetches the texture bundle of that exact
// revision, so with no connection the build failed at "Downloading the
// textures". The download now fetches and verifies the bundle into the one
// cache a build reads, and says so; when it cannot, the map is still
// downloaded and the answer does not call it ready.
func TestADownloadedRevisionCarriesItsTextures(t *testing.T) {
	sync := func(m *machine) (int, map[string]any) {
		return m.call(http.MethodPost, "/api/v1/library/sync", map[string]string{
			"asset_type": m.backend.asset.assetType, "asset_id": m.backend.asset.assetID,
			"revision": m.backend.asset.revisionID,
		})
	}
	cached := func(m *machine) string {
		_, body := m.call(http.MethodGet, "/api/v1/library/cached", nil)
		items, _ := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("the cached list has %d item(s): %v", len(items), body)
		}
		state, _ := items[0].(map[string]any)["textures"].(string)
		return state
	}

	t.Run("map and textures, then a build's bundle with the server gone", func(t *testing.T) {
		m := newMachine(t)
		m.signIn()
		status, body := sync(m)
		textures, _ := body["textures"].(map[string]any)
		if status != http.StatusOK || textures == nil || textures["state"] != "cached" {
			t.Fatalf("the download answered %d with textures %v", status, body["textures"])
		}
		m.backend.mu.Lock()
		fetched := m.backend.textureExports
		m.backend.mu.Unlock()
		if fetched != 1 {
			t.Fatalf("the texture export was asked for %d time(s) by one download", fetched)
		}
		if state := cached(m); state != "cached" {
			t.Errorf("On this computer says the textures are %q", state)
		}

		// The server goes away. What a build asks for is answered from the cache.
		m.backend.server.Close()
		entry, err := m.server.verifiedTextureBundle(context.Background(), m.backend.asset.assetID, m.backend.asset.revision)
		if err != nil {
			t.Fatalf("with the server gone, a build's texture bundle: %v", err)
		}
		if entry.Receipt.MapID != m.backend.asset.assetID || entry.Receipt.Revision != m.backend.asset.revision {
			t.Errorf("the cached bundle is for %s revision %d", entry.Receipt.MapID, entry.Receipt.Revision)
		}
		// And downloading it again is a reuse, not a second fetch.
	})

	t.Run("the textures cannot be fetched: the map is downloaded, and not called ready", func(t *testing.T) {
		m := newMachine(t)
		m.signIn()
		m.backend.mu.Lock()
		m.backend.textureRefusal = http.StatusServiceUnavailable
		m.backend.mu.Unlock()
		status, body := sync(m)
		textures, _ := body["textures"].(map[string]any)
		if status != http.StatusOK || body["key"] != m.backend.asset.revisionID {
			t.Fatalf("a download whose textures failed answered %d %v", status, body)
		}
		if textures == nil || textures["state"] != "missing" || textures["error"] == "" {
			t.Fatalf("the failed texture download is reported as %v", body["textures"])
		}
		if state := cached(m); state != "missing" {
			t.Errorf("On this computer says the textures are %q", state)
		}

		// Connected again, the same download completes it.
		m.backend.mu.Lock()
		m.backend.textureRefusal = 0
		m.backend.mu.Unlock()
		if _, body = sync(m); body["textures"].(map[string]any)["state"] != "cached" {
			t.Fatalf("downloading again did not fetch the textures: %v", body["textures"])
		}
		if state := cached(m); state != "cached" {
			t.Errorf("after the second download On this computer says %q", state)
		}
	})

	t.Run("on this computer, and its own textures incomplete: not called ready", func(t *testing.T) {
		m := newMachine(t)
		m.signIn()
		m.backend.textures = fixtureTextureBundle(m.backend.asset.assetID, m.backend.asset.revision, false)
		status, body := sync(m)
		textures, _ := body["textures"].(map[string]any)
		// The fixture lacks only a WAD the deployment does not send: since
		// NEW_505 that is `needs_own_files` — still not "ready", and buildable
		// with the person's own copy (#9) — not `incomplete`.
		if status != http.StatusOK || textures == nil || textures["state"] != "needs_own_files" || textures["compiler_ready"] != false {
			t.Fatalf("a revision whose textures are incomplete answered %d %v", status, body["textures"])
		}
		if refusals, _ := textures["refusals"].([]any); len(refusals) == 0 {
			t.Errorf("the answer does not say what is incomplete: %v", textures)
		}
		if state := cached(m); state != "needs_own_files" {
			t.Errorf("On this computer says the textures are %q", state)
		}
	})
}
