package web

import (
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
	"github.com/andrea-dintino/auto-pigeon-companion/internal/launch"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/pathpick"
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
		write(map[string]any{
			"api_version": aub.CompanionAPIVersion,
			"scope":       "owned",
			"items":       []map[string]any{b.assetView()},
			"has_more":    false,
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
		Catalog: job.Chain{job.NewCatalog(m.profiles), launch.NewCatalog(launch.ExampleProvider())},
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
		Version:  "test",
		Config:   settings,
		Client:   client,
		Jobs:     service,
		Provider: launch.ExampleProvider(),
		Paths: Paths{
			Profiles:   m.profiles,
			Bindings:   m.bindings,
			Builds:     m.builds,
			AssetCache: m.assets,
		},
		// A picker with no helpers installed, so no test ever opens a window on
		// anybody's desktop and the page falls back to its text fields — which
		// is also the machine a headless CI runner actually is.
		Picker: &pathpick.Picker{
			GOOS: runtime.GOOS,
			Look: func(string) (string, error) { return "", os.ErrNotExist },
		},
		SaveConfig: func(updated config.Config) error {
			m.settings = updated
			return nil
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
		"capabilities": []map[string]any{
			{"id": "fixture.compile", "title": "Compile", "consumes": []string{"fixture.map"},
				"produces": []string{"fixture.bsp"}},
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
					"{input.source_map}", "{output.bsp}",
				},
				"working_dir": map[string]any{"root": "workspace"},
				"inputs": []map[string]any{
					{"name": "source_map", "title": "Source", "role": "fixture.map",
						"required": true, "extensions": []string{".map"}},
				},
				"outputs": []map[string]any{
					{"name": "bsp", "title": "BSP", "role": "fixture.bsp", "path": "{option.basename}.bsp"},
				},
				"options": []map[string]any{
					{"name": "basename", "title": "Name", "type": "text", "default": "level", "max_length": 64},
					{"name": "fail", "title": "Fail on purpose", "type": "bool", "default": "false"},
				},
				"diagnostics": []map[string]any{
					{"id": "wad", "match": "*** ERROR", "severity": "error",
						"message": "The compiler could not read something it needed."},
				},
				"roots":           []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "compile"}},
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
		"inputs": []map[string]any{
			{"name": "source_map", "title": "Map source", "role": "fixture.map",
				"required": true, "extensions": []string{".map"}},
		},
		"steps": []map[string]any{
			{"id": "compile", "title": "Compile", "capability": "fixture.compile",
				"inputs": []map[string]any{{"name": "source_map", "from": "pipeline.source_map"}}},
		},
		"outputs": []map[string]any{
			{"name": "bsp", "title": "The compiled map", "role": "fixture.bsp", "from": "compile.bsp"},
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
		Version:  "test",
		Config:   m.settings,
		Client:   client,
		Jobs:     m.server.jobs,
		Provider: launch.ExampleProvider(),
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
		SaveConfig: func(config.Config) error { return nil },
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
