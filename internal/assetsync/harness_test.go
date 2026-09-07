package assetsync_test

// A fake AUB speaking the Companion API.
//
// It is a fake rather than a mock: it serves the real routes, the real headers
// and the real conditional behaviour, so a test that passes here is testing what
// the client actually does with what AUB actually sends. What it does NOT do is
// authorize — that is AUB's, and it is tested there against the real five
// collections.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
)

type fakeFile struct {
	Path      string
	MediaType string
	Body      []byte
}

func (f fakeFile) digest() string {
	sum := sha256.Sum256(f.Body)

	return hex.EncodeToString(sum[:])
}

type fakeRevision struct {
	ID        string
	Number    int
	Immutable bool
	Files     []fakeFile
}

func (r fakeRevision) manifest() string {
	var builder strings.Builder
	builder.WriteString(aub.CompanionAPIVersion)
	builder.WriteByte('\n')
	for _, file := range r.Files {
		fmt.Fprintf(&builder, "%s\x00%s\x00%d\x00%s\n",
			file.Path, file.MediaType, len(file.Body), file.digest())
	}
	sum := sha256.Sum256([]byte(builder.String()))

	return hex.EncodeToString(sum[:])
}

type fakeAsset struct {
	Type       string
	ID         string
	Name       string
	Addressing string
	Revisions  []fakeRevision
}

func (a fakeAsset) current() (fakeRevision, bool) {
	if len(a.Revisions) == 0 {
		return fakeRevision{}, false
	}

	return a.Revisions[len(a.Revisions)-1], true
}

type fakeBackend struct {
	mu     sync.Mutex
	assets map[string]*fakeAsset

	// downloads counts served file bodies, so a test can assert that a re-sync
	// fetched nothing.
	downloads int

	// truncate, when set, makes the named path send fewer bytes than it declares
	// — an interrupted transfer that a length check must catch.
	truncate string

	// tamper, when set, makes the named path send different bytes under the same
	// declared digest.
	tamper string

	// forbid, when set, makes every route answer 401 — a revoked session.
	forbid bool

	server *httptest.Server
}

func newBackend(t testing.TB, assets ...*fakeAsset) *fakeBackend {
	t.Helper()

	backend := &fakeBackend{assets: map[string]*fakeAsset{}}
	for _, asset := range assets {
		backend.assets[asset.Type+"/"+asset.ID] = asset
	}
	backend.server = httptest.NewServer(http.HandlerFunc(backend.serve))
	t.Cleanup(backend.server.Close)

	return backend
}

func (b *fakeBackend) url() string { return b.server.URL }

func (b *fakeBackend) client(t testing.TB) *aub.Client {
	t.Helper()

	client, err := aub.New(b.url(), b.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("test-token")

	return client
}

func (b *fakeBackend) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.forbid {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "Missing auth."})

		return
	}

	path := strings.TrimPrefix(r.URL.Path, aub.CompanionPrefix)
	switch {
	case path == "/capabilities":
		json.NewEncoder(w).Encode(map[string]any{
			"api_version": aub.CompanionAPIVersion,
			"session": map[string]any{
				"auth_collection": "users", "token_lifetime_seconds": 3600,
				"login_path":   "/api/collections/users/auth-with-password",
				"refresh_path": "/api/collections/users/auth-refresh", "revocable": false,
			},
			"download": map[string]any{
				"range_requests": false, "etag": true, "conditional_requests": true,
				"signed_urls": false, "digest_algorithm": "sha256",
			},
			"asset_types": []map[string]any{{
				"asset_type": "map", "revision_addressing": "revision_rows",
				"history": true, "scopes": []string{"owned"}, "has_visibility": true,
			}},
			"page": map[string]any{
				"catalog_default_limit": 50, "catalog_max_limit": 200,
				"history_default_limit": 50, "history_max_limit": 200,
			},
			"server_time": time.Now().UTC().Format(time.RFC3339),
		})

	case strings.HasPrefix(path, "/assets/"):
		b.serveAsset(w, r, strings.TrimPrefix(path, "/assets/"))

	default:
		http.NotFound(w, r)
	}
}

func (b *fakeBackend) serveAsset(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.SplitN(rest, "/", 5)
	if len(parts) < 2 {
		http.NotFound(w, r)

		return
	}
	asset, ok := b.assets[parts[0]+"/"+parts[1]]
	if !ok {
		notFound(w, "companion_asset_not_found")

		return
	}

	// /assets/{type}/{id}
	if len(parts) == 2 {
		json.NewEncoder(w).Encode(map[string]any{
			"api_version":    aub.CompanionAPIVersion,
			"asset":          b.assetView(asset),
			"revision_count": len(asset.Revisions),
		})

		return
	}
	if parts[2] != "revisions" {
		http.NotFound(w, r)

		return
	}

	// /assets/{type}/{id}/revisions
	if len(parts) == 3 {
		items := []map[string]any{}
		for index := len(asset.Revisions) - 1; index >= 0; index-- {
			items = append(items, b.summary(asset.Revisions[index]))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"api_version": aub.CompanionAPIVersion, "asset_type": asset.Type,
			"asset_id": asset.ID, "revision_addressing": asset.Addressing,
			"items": items, "total": len(items), "offset": 0, "has_more": false,
			"retention": "retain_all",
		})

		return
	}

	revision, found := b.resolve(asset, parts[3])
	if !found {
		notFound(w, "companion_revision_not_found")

		return
	}

	// /assets/{type}/{id}/revisions/{rev}
	if len(parts) == 4 {
		detail := b.summary(revision)
		detail["api_version"] = aub.CompanionAPIVersion
		detail["asset_type"] = asset.Type
		detail["asset_id"] = asset.ID
		detail["manifest_sha256"] = revision.manifest()
		files := []map[string]any{}
		total := 0
		for _, file := range revision.Files {
			files = append(files, map[string]any{
				"path": file.Path, "media_type": file.MediaType,
				"bytes": len(file.Body), "sha256": file.digest(),
			})
			total += len(file.Body)
		}
		detail["files"] = files
		detail["total_bytes"] = total
		json.NewEncoder(w).Encode(detail)

		return
	}

	// /assets/{type}/{id}/revisions/{rev}/files/{path}
	if len(parts) != 5 || !strings.HasPrefix(parts[4], "files/") {
		http.NotFound(w, r)

		return
	}
	wanted := strings.TrimPrefix(parts[4], "files/")
	for _, file := range revision.Files {
		if file.Path != wanted {
			continue
		}
		body := file.Body
		declared := file.digest()
		if b.tamper == file.Path {
			body = append([]byte("tampered"), body...)
		}
		if b.truncate == file.Path && len(body) > 2 {
			body = body[:len(body)-2]
		}

		w.Header().Set("ETag", `"`+declared+`"`)
		w.Header().Set("Accept-Ranges", "none")
		w.Header().Set("Content-Type", file.MediaType)
		w.Header().Set("X-Companion-Revision-Id", revision.ID)
		w.Header().Set("X-Companion-Revision", strconv.Itoa(revision.Number))
		w.Header().Set("X-Companion-Revision-Immutable", strconv.FormatBool(revision.Immutable))
		w.Header().Set("X-Companion-Manifest-Sha256", revision.manifest())
		if match := r.Header.Get("If-None-Match"); match == `"`+declared+`"` {
			w.WriteHeader(http.StatusNotModified)

			return
		}
		// The length declared is the length SENT. A server that declares more
		// than it sends is a dropped connection, which net/http surfaces as a
		// transport error before this package sees a byte — that path is
		// exercised directly against Store.Publish, where the length check can
		// be contested without a socket in the way.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		b.downloads++
		w.Write(body)

		return
	}
	notFound(w, "companion_file_not_found")
}

func (b *fakeBackend) resolve(asset *fakeAsset, id string) (fakeRevision, bool) {
	if id == aub.CurrentRevision {
		return asset.current()
	}
	for _, revision := range asset.Revisions {
		if revision.ID == id {
			return revision, true
		}
	}

	return fakeRevision{}, false
}

func (b *fakeBackend) summary(revision fakeRevision) map[string]any {
	return map[string]any{
		"revision_id": revision.ID,
		"revision":    revision.Number,
		"immutable":   revision.Immutable,
		"created_at":  "2026-09-07T10:00:00Z",
		"kind":        "upload",
	}
}

func (b *fakeBackend) assetView(asset *fakeAsset) map[string]any {
	view := map[string]any{
		"asset_type": asset.Type, "asset_id": asset.ID, "display_name": asset.Name,
		"visibility": "private", "access_path": "owned",
		"revision_addressing": asset.Addressing,
	}
	if current, ok := asset.current(); ok {
		view["current_revision"] = b.summary(current)
	}

	return view
}

func notFound(w http.ResponseWriter, code string) {
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]any{
		"status": 404, "message": "not found",
		"data": map[string]any{"reason": map[string]any{"code": code}},
	})
}

// appendRevision adds a newer revision to an asset, which is what a save at the
// backend looks like from here.
func (b *fakeBackend) appendRevision(assetType, assetID string, revision fakeRevision) {
	b.mu.Lock()
	defer b.mu.Unlock()

	asset := b.assets[assetType+"/"+assetID]
	asset.Revisions = append(asset.Revisions, revision)
}

func (b *fakeBackend) served() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.downloads
}

func (b *fakeBackend) set(mutate func(*fakeBackend)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	mutate(b)
}

// oneMap is the ordinary fixture: a map with two revisions.
func oneMap() *fakeAsset {
	return &fakeAsset{
		Type: "map", ID: "map0000000000001", Name: "e1m1", Addressing: "revision_rows",
		Revisions: []fakeRevision{
			{ID: "rev0000000000001", Number: 1, Immutable: true, Files: []fakeFile{
				{Path: "e1m1.apmap", MediaType: "application/json",
					Body: []byte(`{"schema_version":"1.1","objects":[]}`)},
			}},
			{ID: "rev0000000000002", Number: 2, Immutable: true, Files: []fakeFile{
				{Path: "e1m1.apmap", MediaType: "application/json",
					Body: []byte(`{"schema_version":"1.1","objects":[{"id":"a"}]}`)},
			}},
		},
	}
}
