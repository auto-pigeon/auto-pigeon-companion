package assetsync_test

// What a sync guarantees: verified before publication, atomic per revision, and
// a pinned revision that nothing at the backend can change under it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

func newStore(t testing.TB) *assetsync.Store {
	t.Helper()

	store, err := assetsync.Open(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}

	return store
}

func newSyncer(t testing.TB, backend *fakeBackend, store *assetsync.Store) *assetsync.Syncer {
	t.Helper()

	syncer, err := assetsync.NewSyncer(context.Background(), backend.client(t), store)
	if err != nil {
		t.Fatal(err)
	}

	return syncer
}

func TestASyncPublishesVerifiedBytesAndRecordsTheRevisionItGot(t *testing.T) {
	backend := newBackend(t, oneMap())
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	result, err := syncer.Sync(context.Background(), "map", "map0000000000001", aub.CurrentRevision)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Fetched != 1 || result.Reused != 0 {
		t.Errorf("fetched %d, reused %d", result.Fetched, result.Reused)
	}

	// The record names the version `current` RESOLVED TO, never the word
	// `current` — which is what a build pins.
	record := result.Record
	if record.RevisionID != "rev0000000000002" || record.Revision != 2 {
		t.Errorf("record = %s / %d, want rev0000000000002 / 2", record.RevisionID, record.Revision)
	}
	if !record.Immutable {
		t.Error("a revision_rows type reported a mutable revision")
	}
	if record.Key() != "rev0000000000002" {
		t.Errorf("Key() = %q", record.Key())
	}
	if record.ManifestSHA256 == "" {
		t.Error("no manifest digest recorded")
	}
	if len(record.Files) != 1 || record.Files[0].Path != "e1m1.apmap" {
		t.Fatalf("files = %+v", record.Files)
	}

	if err = store.Verify(record); err != nil {
		t.Errorf("the freshly synced revision does not verify: %v", err)
	}
}

// A second sync of the same revision fetches nothing. The object store is keyed
// by the bytes' own digest, so what it already holds it does not ask for again.
func TestReSyncingAnUnchangedRevisionFetchesNothing(t *testing.T) {
	backend := newBackend(t, oneMap())
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	if _, err := syncer.SyncAsset(context.Background(), "map", "map0000000000001"); err != nil {
		t.Fatal(err)
	}
	first := backend.served()

	result, err := syncer.SyncAsset(context.Background(), "map", "map0000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyComplete {
		t.Error("the second sync did not report the revision as already complete")
	}
	if backend.served() != first {
		t.Errorf("the second sync fetched %d more bodies", backend.served()-first)
	}
}

// The whole point: a save at the backend does not move a revision somebody
// already chose.
func TestANewerRemoteRevisionDoesNotChangeAPinnedOne(t *testing.T) {
	backend := newBackend(t, oneMap())
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	pinned, err := syncer.SyncAsset(context.Background(), "map", "map0000000000001")
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Object(pinned.Record.Files[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(before)
	if err != nil {
		t.Fatal(err)
	}

	backend.appendRevision("map", "map0000000000001", fakeRevision{
		ID: "rev0000000000003", Number: 3, Immutable: true, Files: []fakeFile{
			{Path: "e1m1.apmap", MediaType: "application/json",
				Body: []byte(`{"schema_version":"1.1","objects":[{"id":"a"},{"id":"b"}]}`)},
		},
	})

	// `current` moves.
	moved, err := syncer.SyncAsset(context.Background(), "map", "map0000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Record.RevisionID != "rev0000000000003" {
		t.Fatalf("current is still %s", moved.Record.RevisionID)
	}

	// The pinned one does not — resolved from the cache, with no network at all.
	resolved, err := assetsync.Resolve(context.Background(), store, nil,
		"map", "map0000000000001", pinned.Record.Key())
	if err != nil {
		t.Fatalf("resolving the pin offline: %v", err)
	}
	if resolved.RevisionID != pinned.Record.RevisionID {
		t.Errorf("the pin resolved to %s", resolved.RevisionID)
	}
	after, err := os.ReadFile(before)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(body) {
		t.Error("the pinned revision's bytes changed under it")
	}
}

// A tampered body is refused, and NOTHING is published: a build cannot read it,
// because it is not there.
func TestTamperedBytesArePublishedNowhere(t *testing.T) {
	backend := newBackend(t, oneMap())
	backend.set(func(b *fakeBackend) { b.tamper = "e1m1.apmap" })
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	_, err := syncer.SyncAsset(context.Background(), "map", "map0000000000001")
	if !errors.Is(err, assetsync.ErrDigestMismatch) {
		t.Fatalf("err = %v, want a digest mismatch", err)
	}

	records, err := store.Revisions("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("a revision record was written for content that did not verify: %+v", records)
	}
	if entries := countObjects(t, store); entries != 0 {
		t.Errorf("%d objects were published", entries)
	}
}

// A short body is refused, and nothing lands.
func TestATruncatedTransferIsRefused(t *testing.T) {
	backend := newBackend(t, oneMap())
	backend.set(func(b *fakeBackend) { b.truncate = "e1m1.apmap" })
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	_, err := syncer.SyncAsset(context.Background(), "map", "map0000000000001")
	if !errors.Is(err, assetsync.ErrDigestMismatch) {
		t.Fatalf("err = %v, want a digest mismatch", err)
	}
	if entries := countObjects(t, store); entries != 0 {
		t.Errorf("%d objects were published from a truncated transfer", entries)
	}
}

// The LENGTH check is its own defence and is contested directly, because over a
// socket a server that declares more than it sends is a dropped connection and
// net/http reports it before this package sees a byte.
//
// It catches what a digest alone would also catch, and it names what actually
// happened — which is the difference between "the file is corrupt" and "the
// transfer stopped early", and those have different remedies.
func TestPublishRefusesAReaderShorterThanTheDeclaredLength(t *testing.T) {
	store := newStore(t)

	body := []byte("the whole file")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	_, err := store.Publish(bytes.NewReader(body), digest, int64(len(body))+10)
	if !errors.Is(err, assetsync.ErrDigestMismatch) {
		t.Fatalf("err = %v, want a digest mismatch", err)
	}
	if !strings.Contains(err.Error(), "were declared") {
		t.Errorf("err = %v; it should name the length that was declared", err)
	}
	if countObjects(t, store) != 0 {
		t.Error("a short transfer was published")
	}

	// And the honest case still works.
	if _, err = store.Publish(bytes.NewReader(body), digest, int64(len(body))); err != nil {
		t.Fatalf("publishing correct bytes: %v", err)
	}
	if !store.Has(digest) {
		t.Error("the object is not in the store")
	}
}

// A record is written LAST, so a revision is either complete or absent. This is
// the assertion that keeps that property: SaveRevision refuses a record naming a
// file the object store does not hold.
func TestARevisionRecordCannotNameAFileThatIsNotThere(t *testing.T) {
	store := newStore(t)

	err := store.SaveRevision(assetsync.RevisionRecord{
		AssetType: "map", AssetID: "map1", RevisionID: "rev1", Revision: 1,
		Files: []assetsync.FileRecord{{
			Path: "e1m1.apmap", Bytes: 4,
			SHA256: strings.Repeat("a", 64),
		}},
	})
	if err == nil {
		t.Fatal("a record naming an absent object was written")
	}
	if !strings.Contains(err.Error(), "object store") {
		t.Errorf("err = %v", err)
	}
}

// An interrupted sync leaves objects and no record; the next one completes
// without re-downloading what already landed.
func TestAnInterruptedSyncIsCheapToFinishAndReadsNothingHalfDone(t *testing.T) {
	twoFiles := &fakeAsset{
		Type: "prefab_package", ID: "pre0000000000001", Name: "stairs",
		Addressing: "revision_rows",
		Revisions: []fakeRevision{{
			ID: "rep0000000000001", Number: 1, Immutable: true, Files: []fakeFile{
				{Path: "prefab.apmap", MediaType: "application/json", Body: []byte(`{"a":1}`)},
				{Path: "prefab.json", MediaType: "application/json", Body: []byte(`{"b":2}`)},
			},
		}},
	}
	backend := newBackend(t, twoFiles)
	backend.set(func(b *fakeBackend) { b.tamper = "prefab.json" })
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	if _, err := syncer.SyncAsset(context.Background(), "prefab_package", "pre0000000000001"); err == nil {
		t.Fatal("the sync did not fail on the second file")
	}

	// The first file landed; there is no record, so nothing reads a half-fetched
	// revision.
	if _, err := store.Revision("prefab_package", "pre0000000000001", "rep0000000000001"); !errors.Is(err, assetsync.ErrNotCached) {
		t.Errorf("a record exists for a half-fetched revision: %v", err)
	}
	if entries := countObjects(t, store); entries != 1 {
		t.Errorf("%d objects landed before the failure, want 1", entries)
	}

	backend.set(func(b *fakeBackend) { b.tamper = "" })
	before := backend.served()
	result, err := syncer.SyncAsset(context.Background(), "prefab_package", "pre0000000000001")
	if err != nil {
		t.Fatalf("finishing the sync: %v", err)
	}
	if result.Reused != 1 || result.Fetched != 1 {
		t.Errorf("finishing fetched %d and reused %d, want 1 and 1", result.Fetched, result.Reused)
	}
	if served := backend.served() - before; served != 1 {
		t.Errorf("finishing fetched %d bodies, want 1", served)
	}
	if err = store.Verify(result.Record); err != nil {
		t.Errorf("the completed revision does not verify: %v", err)
	}
}

// The cache is readable with no client at all. A revoked session, or no network,
// takes away new fetches and nothing else.
func TestARevokedSessionLeavesTheCacheReadable(t *testing.T) {
	backend := newBackend(t, oneMap())
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	synced, err := syncer.SyncAsset(context.Background(), "map", "map0000000000001")
	if err != nil {
		t.Fatal(err)
	}

	backend.set(func(b *fakeBackend) { b.forbid = true })

	if _, err = syncer.SyncAsset(context.Background(), "map", "map0000000000001"); err == nil {
		t.Fatal("a revoked session still synced")
	}
	var apiErr *aub.APIError
	if !errors.As(err, &apiErr) || !apiErr.Unauthorized() {
		t.Errorf("err = %v, want an unauthorized API error", err)
	}

	// And what was already verified is still there, still verifiable, still
	// resolvable, with no client involved.
	resolved, err := assetsync.Resolve(context.Background(), store, nil,
		"map", "map0000000000001", synced.Record.Key())
	if err != nil {
		t.Fatalf("the cache became unreadable: %v", err)
	}
	if err = store.Verify(resolved); err != nil {
		t.Errorf("verify: %v", err)
	}
}

// `current` is a question only the server can answer, and an offline resolve
// says so rather than guessing at the newest thing in the cache.
func TestResolvingCurrentOfflineSaysWhatIsMissing(t *testing.T) {
	store := newStore(t)

	_, err := assetsync.Resolve(context.Background(), store, nil, "map", "map1", aub.CurrentRevision)
	if err == nil {
		t.Fatal("current resolved with no server")
	}
	if !strings.Contains(err.Error(), "pin a revision id") {
		t.Errorf("err = %v; it should say what to do instead", err)
	}
}

// Materializing refuses a path that would escape its directory, and keeps one
// that legitimately contains a separator.
func TestMaterializeKeepsNestedPathsAndRefusesEscapingOnes(t *testing.T) {
	nested := &fakeAsset{
		Type: "texture_source", ID: "tex0000000000001", Name: "base",
		Addressing: "revision_rows",
		Revisions: []fakeRevision{{
			ID: "trv0000000000001", Number: 1, Immutable: true, Files: []fakeFile{
				{Path: "e1u1/pow12_1.wal", MediaType: "application/octet-stream",
					Body: []byte("WAL bytes")},
			},
		}},
	}
	backend := newBackend(t, nested)
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	result, err := syncer.SyncAsset(context.Background(), "texture_source", "tex0000000000001")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	dir := t.TempDir()
	written, err := store.Materialize(result.Record, dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	want := filepath.Join(dir, "e1u1", "pow12_1.wal")
	if len(written) != 1 || written[0] != want {
		t.Errorf("wrote %v, want %s", written, want)
	}

	escaping := result.Record
	escaping.Files = []assetsync.FileRecord{{
		Path: "../escaped.wal", Bytes: result.Record.Files[0].Bytes,
		SHA256: result.Record.Files[0].SHA256,
	}}
	if _, err = store.Materialize(escaping, dir); err == nil {
		t.Fatal("a path escaping the staging directory was written")
	}
}

// A cache entry somebody edited is caught by Verify rather than by a compiler
// producing something strange.
func TestVerifyCatchesACacheThatLostOrChangedBytes(t *testing.T) {
	backend := newBackend(t, oneMap())
	store := newStore(t)
	syncer := newSyncer(t, backend, store)

	result, err := syncer.SyncAsset(context.Background(), "map", "map0000000000001")
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.Object(result.Record.Files[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("something else entirely"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err = store.Verify(result.Record); !errors.Is(err, assetsync.ErrDigestMismatch) {
		t.Fatalf("err = %v, want a digest mismatch", err)
	}
}

// Staging files are what an interrupted process leaves, and a clean removes only
// the ones old enough to be nobody's.
func TestCleanRemovesOnlyOldStagingFiles(t *testing.T) {
	store := newStore(t)
	staging := filepath.Join(store.Root(), "staging")

	fresh := filepath.Join(staging, "download-fresh")
	stale := filepath.Join(staging, "download-stale")
	for _, path := range []string{fresh, stale} {
		if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := store.CleanStaging(time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || filepath.Base(removed[0]) != "download-stale" {
		t.Errorf("removed %v", removed)
	}
	if _, err = os.Stat(fresh); err != nil {
		t.Errorf("an in-flight download was removed: %v", err)
	}
}

// A cache key that could contain a separator never becomes one.
func TestACacheKeyCannotEscapeTheCacheDirectory(t *testing.T) {
	store := newStore(t)

	for _, bad := range []string{"../escape", "a/b", ".", "..", "", strings.Repeat("x", 200)} {
		if _, err := store.Revision("map", bad, "rev1"); err == nil {
			t.Errorf("%q was accepted as an asset id", bad)
		}
		if _, err := store.Revision("map", "asset1", bad); err == nil {
			t.Errorf("%q was accepted as a revision key", bad)
		}
	}
	for _, bad := range []string{"", "zz", strings.Repeat("g", 64), "../../etc/passwd"} {
		if store.Has(bad) {
			t.Errorf("%q was accepted as a digest", bad)
		}
	}
}

// countObjects is how many blobs the store holds.
func countObjects(t testing.TB, store *assetsync.Store) int {
	t.Helper()

	count := 0
	err := filepath.WalkDir(filepath.Join(store.Root(), "objects"),
		func(_ string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				count++
			}

			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	return count
}
