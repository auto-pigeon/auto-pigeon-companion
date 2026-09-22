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
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/enginefixture"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/pathpick"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/texturebundle"
)

// A whole fixture machine: a backend with one map on it, a toolchain, an
// engine, and the real server wired to all three.
//
// It is here rather than in a shared package for the reason the other per-
// package fixtures in this repository are: one package needs it. What it must
// not be is a second implementation of anything real — the executor, the build
// runner, the catalog, the asset cache and the HTTP surface are all the shipped
// ones. The only fakes are the backend on the far side of the network and the
// two programs, and both of those are fakes because the real ones are a
// commercial game and a compiler nobody's CI has.

// buildHelperFlag makes this test binary behave as the fixture compiler.
//
// An argument rather than an environment variable, because the executor
// deliberately does not pass its own environment to a job — a fixture that
// depended on one would be testing around the thing it is meant to exercise.
const buildHelperFlag = "-aucom-web-build-helper"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case buildHelperFlag:
			os.Exit(buildHelperMain(os.Args[2:]))
		case enginefixture.Flag:
			os.Exit(enginefixture.Main(os.Args[2:]))
		}
	}
	os.Exit(m.Run())
}

// buildHelperMain is the fixture compiler: it reads a `.map` and writes a
// `.bsp` beside where it was told to, the way a real one does.
func buildHelperMain(args []string) int {
	failing := false
	if len(args) > 0 && args[0] == "--fail" {
		failing, args = true, args[1:]
	}
	// `-wadpath <directory>`: the flag and the directory as two argv elements,
	// which is how the real `qbsp` takes it. The fixture LISTS the directory,
	// so a test can prove the compiler was handed the verified bundle rather
	// than merely that an argument appeared in a preview.
	wadpath := ""
	if len(args) >= 2 && args[0] == "-wadpath" {
		wadpath, args = args[1], args[2:]
	}
	if wadpath != "" {
		names, err := os.ReadDir(wadpath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "helper: -wadpath", err)

			return 1
		}
		for _, name := range names {
			fmt.Println("wadpath holds", name.Name())
		}
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "helper: compile takes a source and a destination")
		return 2
	}
	source, destination := args[0], args[1]
	if failing {
		// The shape a real compiler's failure has: a message a person can read,
		// on stderr, and a non-zero status.
		fmt.Fprintln(os.Stderr, "*** ERROR: could not open the texture wad")
		return 3
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		return 1
	}
	fmt.Println("reading", filepath.Base(source))
	if err := os.WriteFile(destination, append([]byte("BSP:"), contents...), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		return 1
	}
	fmt.Println("wrote", filepath.Base(destination))
	return 0
}

// --- the fake backend -------------------------------------------------------

// fixtureMap is the one asset the fixture backend serves.
type fixtureAsset struct {
	assetType  string
	assetID    string
	name       string
	revisionID string
	revision   int
	fileName   string
	body       []byte
}

func (a fixtureAsset) digest() string {
	sum := sha256.Sum256(a.body)
	return hex.EncodeToString(sum[:])
}

// manifestDigest is the server's digest over the ordered file list, which is
// what a revision record carries as its identity.
func (a fixtureAsset) manifestDigest() string {
	sum := sha256.Sum256([]byte(a.fileName + "\n" + a.digest() + "\n"))
	return hex.EncodeToString(sum[:])
}

type fixtureBackend struct {
	server *httptest.Server
	asset  fixtureAsset
	token  string
	email  string

	// served counts requests, so a test can assert that a restart read from
	// disk rather than going back to the network.
	mu     sync.Mutex
	served int

	// games, when set, answers the hosted-game, game-profile and profile
	// catalogue routes (244F). See games_test.go.
	games http.Handler

	// textures, when set, is the bundle the texture-export route serves
	// instead of the default two-WAD one.
	textures []byte

	// notices, when set, answers AUB's operational-notice route (241).
	// Unset, the route is a 404, as on a deployment that predates it.
	notices http.Handler
}

func (b *fixtureBackend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.served
}

func newFixtureBackend(t *testing.T) *fixtureBackend {
	t.Helper()
	backend := &fixtureBackend{
		token: "fixture-session-token",
		email: "mapper@example.test",
		asset: fixtureAsset{
			assetType:  "map",
			assetID:    "map-e1m1",
			name:       "First Coast",
			revisionID: "rev-000004",
			revision:   4,
			fileName:   "e1m1.map",
			body:       []byte("{ \"classname\" \"worldspawn\" }\n"),
		},
	}
	backend.server = httptest.NewServer(http.HandlerFunc(backend.serve))
	t.Cleanup(backend.server.Close)
	return backend
}

func (b *fixtureBackend) url() string { return b.server.URL }

func (b *fixtureBackend) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.served++
	b.mu.Unlock()
	write := func(body any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	path := r.URL.Path

	// The login route, which is PocketBase's shape rather than the Companion
	// API's — the client authenticates before it knows anything else.
	if strings.HasSuffix(path, "/auth-with-password") {
		write(map[string]any{
			"token":  b.token,
			"record": map[string]any{"id": "user1", "email": b.email},
		})
		return
	}

	// The map texture export (`AUCOM/AUE/AUT 246I1`). Outside the Companion
	// prefix, because AUB serves it under the map routes, and pinned to the
	// exact revision the caller asked for: a mismatch is the refusal the real
	// deployment makes, not a bundle for a different revision.
	if strings.HasPrefix(path, "/api/maps/") && strings.HasSuffix(path, "/texture-export") {
		b.serveTextureExport(w, r)
		return
	}

	if b.notices != nil && path == aub.NoticesPath {
		b.notices.ServeHTTP(w, r)
		return
	}
	if b.games != nil && (strings.HasPrefix(path, aub.HostedGamePrefix) ||
		strings.HasPrefix(path, "/api/game-profiles") || strings.HasPrefix(path, aub.ProfileCatalogPrefix)) {
		b.games.ServeHTTP(w, r)
		return
	}
	if !strings.HasPrefix(path, aub.CompanionPrefix) {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") == "" {
		w.WriteHeader(http.StatusUnauthorized)
		write(map[string]any{"code": "unauthorized", "message": "no session"})
		return
	}
	rest := strings.TrimPrefix(path, aub.CompanionPrefix)
	asset := b.asset

	switch {
	case rest == "/capabilities":
		write(map[string]any{
			"api_version": aub.CompanionAPIVersion,
			"session": map[string]any{
				"auth_collection": "users", "token_lifetime_seconds": 3600,
				"login_path": "/api/collections/users/auth-with-password",
			},
			"download": map[string]any{"digest_algorithm": "sha256"},
			"asset_types": []map[string]any{
				{"asset_type": asset.assetType, "revision_addressing": "revision_id", "history": true},
			},
			"page":        map[string]any{"catalog_default_limit": 25, "catalog_max_limit": 100},
			"server_time": "2026-09-07T00:00:00Z",
		})
	case rest == "/catalog":
		// Two pages, so the page's "Show more" is exercised: the account has
		// more than one page of assets, as the operator's did (NEW_244D).
		if r.URL.Query().Get("cursor") == "page-2" {
			older := b.assetView()
			older["asset_id"], older["display_name"] = "older0000000001", "Older Coast"
			write(map[string]any{
				"api_version": aub.CompanionAPIVersion, "scope": "owned",
				"items": []map[string]any{older}, "has_more": false,
			})
			return
		}
		write(map[string]any{
			"api_version": aub.CompanionAPIVersion,
			"scope":       "owned",
			"items":       []map[string]any{b.assetView()},
			"has_more":    true,
			"next_cursor": "page-2",
		})
	case rest == b.assetPath():
		write(map[string]any{
			"api_version":    aub.CompanionAPIVersion,
			"asset":          b.assetView(),
			"revision_count": 1,
		})
	case rest == b.assetPath()+"/revisions":
		write(map[string]any{
			"api_version": aub.CompanionAPIVersion,
			"asset_type":  asset.assetType, "asset_id": asset.assetID,
			"revision_addressing": "revision_id",
			"items":               []map[string]any{b.revisionSummary()},
			"total":               1, "offset": 0, "has_more": false,
		})
	case rest == b.assetPath()+"/revisions/"+asset.revisionID,
		rest == b.assetPath()+"/revisions/"+aub.CurrentRevision:
		body := b.revisionSummary()
		body["api_version"] = aub.CompanionAPIVersion
		body["asset_type"] = asset.assetType
		body["asset_id"] = asset.assetID
		body["manifest_sha256"] = asset.manifestDigest()
		body["total_bytes"] = len(asset.body)
		body["files"] = []map[string]any{{
			"path": asset.fileName, "media_type": "text/plain",
			"bytes": len(asset.body), "sha256": asset.digest(),
		}}
		write(body)
	case strings.HasPrefix(rest, b.assetPath()+"/revisions/") && strings.Contains(rest, "/files/"):
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(asset.body)))
		_, _ = w.Write(asset.body)
	default:
		w.WriteHeader(http.StatusNotFound)
		write(map[string]any{"code": "not_found", "message": "no such route: " + rest})
	}
}

// textures, when set, replaces the default two-WAD bundle. A test that wants a
// refusal or a wrong revision sets it.
func (b *fixtureBackend) serveTextureExport(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "unauthorized", "message": "no session"})

		return
	}
	mapID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/maps/"), "/texture-export")
	if mapID != b.asset.assetID {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"reason": map[string]any{"code": "map_not_found"}},
		})

		return
	}
	if raw := r.URL.Query().Get("revision"); raw != "" && raw != fmt.Sprint(b.asset.revision) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": "This map's texture export is built from its current saved document.",
			"data":    map[string]any{"reason": map[string]any{"code": "revision_not_exportable"}},
		})

		return
	}
	bundle := b.textures
	if bundle == nil {
		bundle = fixtureTextureBundle(b.asset.assetID, b.asset.revision, true)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="fixture-textures.zip"`)
	_, _ = w.Write(bundle)
}

// fixtureTextureBundle builds a bundle the way AUB builds one: two declared
// WADs in the map's own order, each carried with its declared digest, plus the
// manifest and the licences document.
func fixtureTextureBundle(mapID string, revision int, ready bool) []byte {
	first := []byte("WAD2" + "first-wall-texture")
	second := []byte("WAD2" + "second-wall-texture")
	digest := func(body []byte) string {
		sum := sha256.Sum256(body)

		return hex.EncodeToString(sum[:])
	}
	files := []map[string]any{
		{"path": "first.wad", "source": "first.wad", "sha256": digest(first), "bytes": len(first)},
		{"path": "second.wad", "source": "second.wad", "sha256": digest(second), "bytes": len(second)},
	}
	manifest := map[string]any{
		"schema_version": texturebundle.Schema,
		"map_id":         mapID, "map_name": "First Coast", "revision": revision, "game": "quake1",
		"exported_at":   "2026-09-21T00:00:00Z",
		"wads_declared": []string{"first.wad", "second.wad"},
		"requirements": []map[string]any{
			{"order": 0, "name": "first.wad", "game": "quake1", "kind": "wad", "status": "resolved",
				"included": true, "files": files[0:1]},
			{"order": 1, "name": "second.wad", "game": "quake1", "kind": "wad", "status": "resolved",
				"included": true, "files": files[1:2]},
		},
		"files":             files,
		"compiler_ready":    ready,
		"compiler_refusals": []string{},
	}
	if !ready {
		manifest["compiler_refusals"] = []string{"wad_bytes_not_carried: quake101.wad"}
		manifest["unresolved"] = []string{"texture_source_installed: quake101.wad"}
	}

	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	add := func(name string, body []byte) {
		out, err := writer.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err = out.Write(body); err != nil {
			panic(err)
		}
	}
	add("first.wad", first)
	add("second.wad", second)
	add(texturebundle.LicensesName, []byte("# Attribution\n"))
	encoded, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	add(texturebundle.ManifestName, encoded)
	if err = writer.Close(); err != nil {
		panic(err)
	}

	return buffer.Bytes()
}

func (b *fixtureBackend) assetPath() string {
	return "/assets/" + b.asset.assetType + "/" + b.asset.assetID
}

func (b *fixtureBackend) revisionSummary() map[string]any {
	return map[string]any{
		"revision_id": b.asset.revisionID, "revision": b.asset.revision,
		"immutable": true, "content_sha256": b.asset.digest(),
		"author_user_id": "user1", "created_at": "2026-09-01T10:00:00Z",
		"kind": "full", "base_revision": 0,
	}
}

func (b *fixtureBackend) assetView() map[string]any {
	return map[string]any{
		"asset_type": b.asset.assetType, "asset_id": b.asset.assetID,
		"display_name": b.asset.name, "game": "quake1", "visibility": "private",
		"owner_user_id": "user1", "access_path": "owned", "role": "owner",
		"created_at": "2026-09-01T10:00:00Z", "revision_addressing": "revision_id",
		"current_revision": b.revisionSummary(),
	}
}

// --- the fixture machine ----------------------------------------------------

type machine struct {
	t        *testing.T
	server   *Server
	backend  *fixtureBackend
	dir      string
	profiles string
	bindings string
	builds   string
	assets   string
	gameRoot string
	content  string
	settings config.Config
}

// newMachine is a clean machine with a backend, a toolchain and an engine
// available, and nothing set up: no session, no binding, no grant.
func newMachine(t *testing.T) *machine {
	t.Helper()
	dir := t.TempDir()
	m := &machine{
		t:        t,
		backend:  newFixtureBackend(t),
		dir:      dir,
		profiles: filepath.Join(dir, "profiles"),
		bindings: filepath.Join(dir, "bindings.json"),
		builds:   filepath.Join(dir, "builds"),
		assets:   filepath.Join(dir, "assets"),
		gameRoot: filepath.Join(dir, "quake"),
		content:  filepath.Join(dir, "project"),
	}
	for _, sub := range []string{m.profiles, m.builds, m.assets, m.content} {
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A game root that looks like an installed Quake, because the engine
	// profile's preflight checks for one.
	writeFixtureFile(t, filepath.Join(m.gameRoot, "id1", "pak0.pak"), "PACK")

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	m.writeProfile("aucom.fixture.q1-engine.json", enginefixture.ProfileJSON)
	m.writeProfile("aucom.fixture.toolchain.json", fixtureToolJSON(self))
	m.writeProfile("aucom.fixture.pipeline.json", fixturePipelineJSON())

	settings := config.Default()
	settings.AUBBaseURL = m.backend.url()
	settings.JobsDir = filepath.Join(dir, "jobs")
	settings.ProfilesDir = m.profiles
	settings.AssetCacheDir = m.assets
	settings.ToolCacheDir = filepath.Join(dir, "tools")
	m.settings = settings

	store, err := job.OpenStore(settings.JobsDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := job.NewService(job.Options{
		Store:   store,
		Catalog: job.NewCatalog(m.profiles),
		// binding.Lookup re-reads the file on every call, so a setup recorded
		// through the API is visible to the executor at once — which is the
		// whole of what "save the setup, then press Start" has to mean.
		Bindings: binding.Lookup(m.bindings),
		Logf:     func(format string, args ...any) { t.Logf("jobs: "+format, args...) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close(); cancel() })

	client, err := aub.New(settings.AUBBaseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Options{
		Version: "test",
		Config:  settings,
		Client:  client,
		Jobs:    service,
		Paths: Paths{
			Profiles:   m.profiles,
			Bindings:   m.bindings,
			Builds:     m.builds,
			AssetCache: m.assets,
			ConfigDir:  filepath.Join(dir, "config"),
		},
		// A picker with no helpers installed, so no test ever opens a window on
		// anybody's desktop and the page falls back to its text fields — which
		// is also the machine a headless CI runner actually is.
		Picker: &pathpick.Picker{
			GOOS: runtime.GOOS,
			Look: func(string) (string, error) { return "", os.ErrNotExist },
		},
		UpdateConfig: func(mutate func(*config.Config) error) (config.Config, error) {
			current := m.settings
			if err := mutate(&current); err != nil {
				return config.Config{}, err
			}
			m.settings = current
			return current, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	m.server = server
	return m
}

func (m *machine) writeProfile(name string, document []byte) {
	m.t.Helper()
	if err := os.WriteFile(filepath.Join(m.profiles, name), document, 0o600); err != nil {
		m.t.Fatal(err)
	}
}

func writeFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func encodeFixture(document map[string]any) []byte {
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		panic(err)
	}
	return body
}

// fixtureToolJSON is a one-capability toolchain over the helper above.
func fixtureToolJSON(self string) []byte {
	return encodeFixture(map[string]any{
		"schema_version": "aucom.profile/1.1",
		"kind":           "tool",
		"id":             "aucom.fixture.toolchain",
		"version":        "1.0.0",
		"name":           "Fixture toolchain",
		"summary":        "A stand-in compiler, so the journey exercises the real executor.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"tool_version":   "0.0.0-fixture",
		"platforms": []map[string]any{
			{"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "status": "supported"},
		},
		"acquisition": []map[string]any{
			{"mode": "user_path", "title": "Point at a copy you already have", "hint": "choose the test binary"},
		},
		// The roles are the REAL Quake 1 ones, not invented `fixture.*` ones.
		//
		// `AUCOM/AUT 246I` made the Build page classify an input from its
		// declared role — `q1.map.source` is a map, `q1.wad` is a texture
		// collection — and left this fixture declaring `fixture.map`, which
		// classifies as an ordinary file. The page then offered no "a map from
		// My Maps" option for it, and the browser journey's attempt to choose
		// one silently did nothing: it timed out at the command preview with no
		// input supplied. The fixture was the thing that was wrong.
		"capabilities": []map[string]any{
			{"id": "fixture.compile", "title": "Compile", "consumes": []string{"q1.map.source"},
				"produces": []string{"q1.bsp"}},
		},
		"executables": []map[string]any{
			{"name": "tool", "title": "The fixture program", "file": "tool{platform.exe_suffix}"},
		},
		"actions": []map[string]any{
			{
				"id": "compile", "title": "Compile", "capability": "fixture.compile",
				"executable": "tool",
				"args": []any{
					buildHelperFlag,
					// A conditional argument, so one fixture can be both the
					// compiler that works and the compiler that fails — and so
					// the command preview shows which arguments a build chose.
					map[string]any{"value": "--fail", "when": map[string]any{"option": "fail"}},
					// `-wadpath <directory>`, passed only when a texture folder
					// is set and as two argv elements, exactly as the real
					// `qbsp` takes it (`AUCOM/AUE/AUT 246I1`).
					map[string]any{"value": "-wadpath", "when": map[string]any{"root": "content_root"}},
					map[string]any{"value": "{root.content_root}", "when": map[string]any{"root": "content_root"}},
					"{input.source_map}", "{output.bsp}",
				},
				"working_dir": map[string]any{"root": "workspace"},
				"inputs": []map[string]any{
					{"name": "source_map", "title": "Source", "role": "q1.map.source",
						"required": true, "extensions": []string{".map"}},
				},
				"outputs": []map[string]any{
					{"name": "bsp", "title": "BSP", "role": "q1.bsp", "path": "{option.basename}.bsp"},
				},
				"options": []map[string]any{
					{"name": "basename", "title": "Name", "type": "text", "default": "level", "max_length": 64},
					{"name": "fail", "title": "Fail on purpose", "type": "bool", "default": "false"},
				},
				"diagnostics": []map[string]any{
					{"id": "wad", "match": "*** ERROR", "severity": "error",
						"message": "The compiler could not read something it needed."},
				},
				"roots": []map[string]any{
					{"role": "workspace", "access": "read_write", "purpose": "compile"},
					{"role": "content_root", "access": "read", "optional": true,
						"purpose": "find the texture WADs the map names"},
				},
				"timeout_seconds": 120,
			},
		},
	})
}

func fixturePipelineJSON() []byte {
	return encodeFixture(map[string]any{
		"schema_version": "aucom.profile/1.1",
		"kind":           "pipeline",
		"id":             "aucom.fixture.pipeline",
		"version":        "1.0.0",
		"name":           "Fixture build",
		"summary":        "One stage: compile a map source into a BSP.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		// The family, so internal/profile classifies `q1.map.source` as a map
		// and `q1.wad` as a texture collection. Without it the Build page
		// offers an ordinary file picker for a map, which is what 246I fixed in
		// the classifier and left unfixed in this fixture.
		"game_profile": map[string]any{"slug": "quake1", "engine_family": "quake1"},
		"inputs": []map[string]any{
			{"name": "source_map", "title": "Map source", "role": "q1.map.source",
				"required": true, "extensions": []string{".map"}},
		},
		"steps": []map[string]any{
			{"id": "compile", "title": "Compile", "capability": "fixture.compile",
				"inputs": []map[string]any{{"name": "source_map", "from": "pipeline.source_map"}}},
		},
		"outputs": []map[string]any{
			{"name": "bsp", "title": "The compiled map", "role": "q1.bsp", "from": "compile.bsp"},
		},
	})
}

// --- helpers the journey tests use ------------------------------------------

func mustExecutable(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	return self
}

// writeSourceMap is a map source on this machine, for the half of the journey
// that builds from a local file rather than from the backend.
func (m *machine) writeSourceMap() string {
	m.t.Helper()
	path := filepath.Join(m.content, "local.map")
	writeFixtureFile(m.t, path, "{ \"classname\" \"worldspawn\" }\n")
	return path
}

// bindExecutable records where one of a profile's programs is, the way the Run
// area's setup form does.
func (m *machine) bindExecutable(profileID, name, path string) {
	m.t.Helper()
	set, err := binding.LoadFile(m.bindings)
	if err != nil && !errorsIsNoFile(err) {
		m.t.Fatal(err)
	}
	catalog := job.NewCatalog(m.profiles)
	entry, err := catalog.Lookup(profileID)
	if err != nil {
		m.t.Fatal(err)
	}
	local, _ := set.Find(profileID)
	local.ProfileID = profileID
	local.ProfileVersion = entry.Profile.Metadata().Version
	local.ProfileDigest = entry.Digest
	local.Trust = entry.Trust
	local.Acquisition = "user_path"
	if local.Executables == nil {
		local.Executables = map[string]string{}
	}
	local.Executables[name] = path
	if local.Grant == nil {
		m.t.Fatalf("%s has no grant: bind the executable after approving it, as the page does", profileID)
	}
	if err := set.Put(local); err != nil {
		m.t.Fatal(err)
	}
	if err := binding.SaveFile(m.bindings, set); err != nil {
		m.t.Fatal(err)
	}
}

func errorsIsNoFile(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no binding file")
}

// waitForBuild polls the build until it stops being live, the way the page does.
func (m *machine) waitForBuild(id string) map[string]any {
	m.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		status, body := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
		if status != http.StatusOK {
			m.t.Fatalf("reading build %s = %d: %v", id, status, body["error"])
		}
		manifest, _ := body["manifest"].(map[string]any)
		if body["live"] != true {
			return manifest
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("build %s did not finish: %v", id, manifest["state"])
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (m *machine) waitForJob(id string) map[string]any {
	m.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		status, body := m.call(http.MethodGet, "/api/v1/jobs/"+id, nil)
		if status != http.StatusOK {
			m.t.Fatalf("reading job %s = %d: %v", id, status, body["error"])
		}
		state, _ := body["state"].(string)
		switch state {
		case "succeeded", "failed", "cancelled", "interrupted":
			return body
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("job %s stayed %s", id, state)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// findItem picks one row out of an `items` list by a field, and fails with the
// ids it did see — a test that only says "not found" makes somebody re-run it
// with a print statement.
func findItem(t *testing.T, body map[string]any, field, want string) map[string]any {
	t.Helper()
	items, _ := body["items"].([]any)
	seen := make([]string, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		value, _ := item[field].(string)
		seen = append(seen, value)
		if value == want {
			return item
		}
	}
	t.Fatalf("no item has %s=%q; the list has %v", field, want, seen)
	return nil
}

// restart is a second Server over the same directories and the same saved
// configuration — which is what a restart of the Companion is.
//
// The executor is the same one: a second job service over one store would be
// two processes claiming the same jobs, which is not what a restart is and is
// not something this test should invent.
func (m *machine) restart() *machine {
	m.t.Helper()
	client, err := aub.New(m.settings.AUBBaseURL, nil)
	if err != nil {
		m.t.Fatal(err)
	}
	client.SetToken(m.settings.Session.Token)
	server, err := NewServer(Options{
		Version: "test",
		Config:  m.settings,
		Client:  client,
		Jobs:    m.server.jobs,
		Paths: Paths{
			Profiles:   m.profiles,
			Bindings:   m.bindings,
			Builds:     m.builds,
			AssetCache: m.assets,
		},
		Picker: &pathpick.Picker{
			GOOS: runtime.GOOS,
			Look: func(string) (string, error) { return "", os.ErrNotExist },
		},
		UpdateConfig: func(mutate func(*config.Config) error) (config.Config, error) {
			var current config.Config
			return current, mutate(&current)
		},
	})
	if err != nil {
		m.t.Fatal(err)
	}
	m.t.Cleanup(server.Close)

	next := *m
	next.server = server
	return &next
}

// jobIDs is every job the store holds, in the order the API reports them.
func (m *machine) jobIDs() []string {
	m.t.Helper()
	status, body := m.call(http.MethodGet, "/api/v1/jobs", nil)
	if status != http.StatusOK {
		m.t.Fatalf("listing jobs = %d", status)
	}
	items, _ := body["items"].([]any)
	ids := make([]string, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		id, _ := item["id"].(string)
		ids = append(ids, id)
	}
	return ids
}
