package cli

// `companion aub` through the real command surface, against a fake AUB.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// fakeCompanionAUB serves the four Companion routes these commands use, for one
// map with two revisions.
func fakeCompanionAUB(t *testing.T) *httptest.Server {
	t.Helper()

	const document = `{"schema_version":"1.1","objects":[]}`
	// sha256 of `document`, computed by the server itself so the fixture cannot
	// drift from the bytes it serves.
	digest := sha256Hex(document)

	summary := func(id string, number int) map[string]any {
		return map[string]any{
			"revision_id": id, "revision": number, "immutable": true,
			"content_sha256": digest, "created_at": "2026-09-07T10:00:00Z", "kind": "upload",
		}
	}
	files := []map[string]any{{
		"path": "e1m1.apmap", "media_type": "application/json",
		"bytes": len(document), "sha256": digest,
	}}

	mux := http.NewServeMux()
	mux.HandleFunc(aub.CompanionPrefix+"/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"api_version": aub.CompanionAPIVersion,
			"session": map[string]any{
				"auth_collection": "users", "token_lifetime_seconds": 7200,
				"login_path":   "/api/collections/users/auth-with-password",
				"refresh_path": "/api/collections/users/auth-refresh", "revocable": false,
			},
			"download": map[string]any{
				"range_requests": false, "etag": true, "conditional_requests": true,
				"signed_urls": false, "digest_algorithm": "sha256",
			},
			"asset_types": []map[string]any{{
				"asset_type": "map", "revision_addressing": "revision_rows",
				"history": true, "scopes": []string{"owned", "public"}, "has_visibility": true,
			}},
			"page": map[string]any{
				"catalog_default_limit": 50, "catalog_max_limit": 200,
				"history_default_limit": 50, "history_max_limit": 200,
			},
			"server_time": "2026-09-07T10:00:00Z",
		})
	})
	mux.HandleFunc(aub.CompanionPrefix+"/catalog", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"api_version": aub.CompanionAPIVersion,
			"scope":       r.URL.Query().Get("scope"),
			"items": []map[string]any{{
				"asset_type": "map", "asset_id": "map0000000000001",
				"display_name": "e1m1", "visibility": "private", "access_path": "owned",
				"revision_addressing": "revision_rows", "current_revision": summary("rev0000000000002", 2),
			}},
			"has_more": false,
		})
	})
	mux.HandleFunc(aub.CompanionPrefix+"/assets/map/map0000000000001",
		func(w http.ResponseWriter, _ *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"api_version": aub.CompanionAPIVersion,
				"asset": map[string]any{
					"asset_type": "map", "asset_id": "map0000000000001",
					"display_name": "e1m1", "visibility": "private", "access_path": "owned",
					"revision_addressing": "revision_rows",
					"current_revision":    summary("rev0000000000002", 2),
				},
				"revision_count": 2,
			})
		})
	mux.HandleFunc(aub.CompanionPrefix+"/assets/map/map0000000000001/revisions",
		func(w http.ResponseWriter, _ *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"api_version": aub.CompanionAPIVersion, "asset_type": "map",
				"asset_id": "map0000000000001", "revision_addressing": "revision_rows",
				"items":     []map[string]any{summary("rev0000000000002", 2), summary("rev0000000000001", 1)},
				"total":     2,
				"retention": "retain_all",
			})
		})
	for _, revision := range []struct {
		id     string
		number int
	}{{"rev0000000000001", 1}, {"rev0000000000002", 2}, {aub.CurrentRevision, 2}} {
		id, number := revision.id, revision.number
		stored := id
		if stored == aub.CurrentRevision {
			stored = "rev0000000000002"
		}
		base := aub.CompanionPrefix + "/assets/map/map0000000000001/revisions/" + id
		mux.HandleFunc(base, func(w http.ResponseWriter, _ *http.Request) {
			detail := summary(stored, number)
			detail["api_version"] = aub.CompanionAPIVersion
			detail["asset_type"] = "map"
			detail["asset_id"] = "map0000000000001"
			detail["manifest_sha256"] = "manifest-" + stored
			detail["files"] = files
			detail["total_bytes"] = len(document)
			json.NewEncoder(w).Encode(detail)
		})
		mux.HandleFunc(base+"/files/e1m1.apmap", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("ETag", `"`+digest+`"`)
			w.Header().Set("Accept-Ranges", "none")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Companion-Revision-Id", stored)
			w.Header().Set("X-Companion-Revision", fmt.Sprint(number))
			w.Header().Set("X-Companion-Revision-Immutable", "true")
			w.Header().Set("Content-Length", fmt.Sprint(len(document)))
			w.Write([]byte(document))
		})
	}

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server
}

// signedInEnv is a testEnv with a configured AUB, a session, and an asset cache
// of its own.
func signedInEnv(t *testing.T, baseURL string) (*Env, *strings.Builder, *strings.Builder) {
	t.Helper()

	env, stdout, stderr := testEnv(t)
	cache := filepath.Join(t.TempDir(), "assets")
	env.Lookenv = func(name string) (string, bool) {
		if name == config.EnvAssetCacheDir {
			return cache, true
		}

		return "", false
	}
	t.Setenv(config.EnvAssetCacheDir, cache)

	settings := config.Default()
	settings.AUBBaseURL = baseURL
	settings.Session = config.Session{Token: "test-token", Email: "a@example", UserID: "u1"}
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatal(err)
	}

	out := &strings.Builder{}
	errOut := &strings.Builder{}
	env.Stdout = out
	env.Stderr = errOut
	_ = stdout
	_ = stderr

	return env, out, errOut
}

func TestAUBCapabilitiesPrintsWhatTheDeploymentDeclared(t *testing.T) {
	server := fakeCompanionAUB(t)
	env, stdout, stderr := signedInEnv(t, server.URL)

	if code := Run(env, []string{"aub", "capabilities"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{aub.CompanionAPIVersion, "2h0m0s", "revocable:     false",
		"resumable:     false", "revision_rows"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

func TestAUBSyncPinsTheRevisionCurrentResolvedTo(t *testing.T) {
	server := fakeCompanionAUB(t)
	env, stdout, stderr := signedInEnv(t, server.URL)

	if code := Run(env, []string{"aub", "sync", "map", "map0000000000001"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "revision 2") {
		t.Errorf("output does not name the revision it got:\n%s", out)
	}
	// The pin is the revision id, never the word `current`.
	if !strings.Contains(out, "pin:      rev0000000000002") {
		t.Errorf("the pin is not the resolved revision id:\n%s", out)
	}

	// And it is on disk, listed by a command that needs no network.
	stdout.Reset()
	if code := Run(env, []string{"aub", "cached"}); code != 0 {
		t.Fatalf("cached: exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "rev0000000000002") {
		t.Errorf("the cached listing does not show it:\n%s", stdout.String())
	}

	stdout.Reset()
	if code := Run(env, []string{"aub", "verify"}); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ok   map") {
		t.Errorf("verify did not report the revision:\n%s", stdout.String())
	}
}

func TestAUBExportWritesTheCachedRevisionSomewhere(t *testing.T) {
	server := fakeCompanionAUB(t)
	env, stdout, stderr := signedInEnv(t, server.URL)

	if code := Run(env, []string{"aub", "sync", "map", "map0000000000001",
		"--revision", "rev0000000000001"}); code != 0 {
		t.Fatalf("sync: exit %d: %s", code, stderr.String())
	}

	into := filepath.Join(t.TempDir(), "out")
	stdout.Reset()
	if code := Run(env, []string{"aub", "export", "map", "map0000000000001",
		"--into", into, "--revision", "rev0000000000001"}); code != 0 {
		t.Fatalf("export: exit %d: %s", code, stderr.String())
	}
	body, err := os.ReadFile(filepath.Join(into, "e1m1.apmap"))
	if err != nil {
		t.Fatalf("the exported file is not there: %v", err)
	}
	if !strings.Contains(string(body), "schema_version") {
		t.Errorf("exported %q", body)
	}
}

// Signing out removes the credential and stops privileged reads; it does not
// remove the work already on this machine.
func TestSigningOutKeepsTheCachedAssetsAndStopsNewReads(t *testing.T) {
	server := fakeCompanionAUB(t)
	env, stdout, stderr := signedInEnv(t, server.URL)

	if code := Run(env, []string{"aub", "sync", "map", "map0000000000001"}); code != 0 {
		t.Fatalf("sync: exit %d: %s", code, stderr.String())
	}

	stdout.Reset()
	if code := Run(env, []string{"auth", "logout"}); code != 0 {
		t.Fatalf("logout: exit %d: %s", code, stderr.String())
	}

	settings, err := config.LoadFrom(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Session.Token != "" {
		t.Error("the token is still in the config file after signing out")
	}

	// A privileged read is refused...
	stdout.Reset()
	stderr.Reset()
	if code := Run(env, []string{"aub", "sync", "map", "map0000000000001"}); code == 0 {
		t.Error("a sync succeeded after signing out")
	}
	if !strings.Contains(stderr.String(), "not signed in") {
		t.Errorf("the refusal does not say what to do:\n%s", stderr.String())
	}

	// ...and the local work is untouched.
	stdout.Reset()
	if code := Run(env, []string{"aub", "cached"}); code != 0 {
		t.Fatalf("cached: exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "map0000000000001") {
		t.Errorf("signing out removed the cached asset:\n%s", stdout.String())
	}
	stdout.Reset()
	if code := Run(env, []string{"aub", "verify"}); code != 0 {
		t.Errorf("the cached asset no longer verifies after signing out: %s", stderr.String())
	}
}

func TestAnAssetReferenceIsParsedIntoItsParts(t *testing.T) {
	for _, want := range []struct {
		value string
		ref   AssetRef
	}{
		{"aub:map/abc123", AssetRef{AssetType: "map", AssetID: "abc123", Revision: aub.CurrentRevision}},
		{"aub:map/abc123@rev9", AssetRef{AssetType: "map", AssetID: "abc123", Revision: "rev9"}},
		{"aub:prefab_package/p1@r1#prefab.apmap", AssetRef{
			AssetType: "prefab_package", AssetID: "p1", Revision: "r1", File: "prefab.apmap"}},
		{"aub:texture_source/t1#e1u1/pow12_1.wal", AssetRef{
			AssetType: "texture_source", AssetID: "t1",
			Revision: aub.CurrentRevision, File: "e1u1/pow12_1.wal"}},
	} {
		got, err := ParseAssetRef(want.value)
		if err != nil {
			t.Errorf("%s: %v", want.value, err)

			continue
		}
		if got != want.ref {
			t.Errorf("%s parsed to %+v, want %+v", want.value, got, want.ref)
		}
	}

	for _, bad := range []string{"", "map/abc", "aub:", "aub:map", "aub:/abc", "aub:map/"} {
		if _, err := ParseAssetRef(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))

	return hex.EncodeToString(sum[:])
}

// The session token lives in one 0600 file and reaches nothing else.
//
// The asset cache is the new place it could have leaked into — a revision record
// carries where it came FROM, and a base URL with credentials in it, or a header
// copied into a record "for debugging", is exactly how that happens.
func TestNothingInTheAssetCacheCarriesTheCredential(t *testing.T) {
	server := fakeCompanionAUB(t)
	env, _, stderr := signedInEnv(t, server.URL)

	if code := Run(env, []string{"aub", "sync", "map", "map0000000000001"}); code != 0 {
		t.Fatalf("sync: exit %d: %s", code, stderr.String())
	}

	settings, err := config.LoadFrom(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := settings.AssetCache()
	if err != nil {
		t.Fatal(err)
	}

	found := 0
	err = filepath.WalkDir(cache, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		found++
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(body), "test-token") {
			t.Errorf("%s contains the session token", path)
		}
		if strings.Contains(string(body), "Authorization") {
			t.Errorf("%s contains an Authorization header", path)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("nothing was written to the cache, so this proves nothing")
	}
}

// And the one file that DOES hold it is 0600.
func TestTheConfigFileHoldingTheTokenIsNotReadableByOthers(t *testing.T) {
	server := fakeCompanionAUB(t)
	env, _, _ := signedInEnv(t, server.URL)

	info, err := os.Stat(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("config.json is %04o; it holds a session token", perm)
	}
}
