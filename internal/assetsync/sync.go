package assetsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
)

// Syncer fetches revisions from one AUB into one local store.
type Syncer struct {
	client *aub.Client
	store  *Store

	// capabilities is what the deployment said about itself, read once per
	// Syncer. It is not optional: the resume decision, the digest algorithm and
	// the session contract are all in it, and a client that acted without reading
	// it would be guessing again.
	capabilities aub.Capabilities
}

// NewSyncer reads the deployment's capabilities and returns a syncer bound to
// them.
//
// The version check happens here, in aub.Capabilities, so a deployment speaking
// a different contract is refused ONCE with a clear message rather than five
// times with confusing ones.
func NewSyncer(ctx context.Context, client *aub.Client, store *Store) (*Syncer, error) {
	capabilities, err := client.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	client.AdoptCapabilities(capabilities)

	return &Syncer{client: client, store: store, capabilities: capabilities}, nil
}

// Capabilities is what the deployment declared.
func (s *Syncer) Capabilities() aub.Capabilities { return s.capabilities }

// Result is what one sync did.
type Result struct {
	Record RevisionRecord

	// Fetched and Reused are per FILE. Reused counts the files whose bytes the
	// object store already held — which is what makes re-syncing a map whose WADs
	// have not changed cost nothing, and what makes an interrupted sync cheap to
	// finish.
	Fetched int
	Reused  int

	// BytesFetched is what actually crossed the network.
	BytesFetched int64

	// AlreadyComplete reports that the store already held this exact revision and
	// nothing was fetched or written at all.
	AlreadyComplete bool
}

// Sync fetches one revision into the store and records it.
//
// `revisionID` may be empty or `current`, in which case whatever is current is
// fetched and the record says which version that turned out to be — a build then
// pins THAT, not the word `current`.
//
// The order is deliberate and is the whole of the atomicity guarantee: every file
// is verified and published first, and the record is written last. An interrupted
// run leaves objects and no record; the next one finds the objects present and
// completes without re-downloading them.
func (s *Syncer) Sync(ctx context.Context, assetType, assetID, revisionID string) (Result, error) {
	revision, err := s.client.Revision(ctx, assetType, assetID, revisionID)
	if err != nil {
		return Result{}, err
	}

	record := RevisionRecord{
		AssetType:      assetType,
		AssetID:        assetID,
		RevisionID:     revision.ID,
		Revision:       revision.Number,
		Immutable:      revision.Immutable,
		ManifestSHA256: revision.ManifestSHA256,
		ContentSHA256:  revision.ContentSHA256,
		AuthorUserID:   revision.AuthorUserID,
		CreatedAt:      revision.CreatedAt,
		TotalBytes:     revision.TotalBytes,
		Source:         s.client.BaseURL(),
		SyncedAt:       time.Now().UTC(),
	}

	// An already-complete revision is answered without touching the network
	// again beyond the metadata read above. Not skipped silently: the caller is
	// told, because "nothing happened" and "it was already here" are different
	// answers to somebody watching a sync.
	if existing, cachedErr := s.store.Revision(assetType, assetID, record.Key()); cachedErr == nil {
		if s.store.Verify(existing) == nil {
			return Result{Record: existing, Reused: len(existing.Files), AlreadyComplete: true}, nil
		}
		// A record whose bytes no longer verify is a cache that lost something.
		// The fetch below repairs it, and the record is rewritten at the end.
	}

	result := Result{}
	for _, file := range revision.Files {
		record.Files = append(record.Files, FileRecord{
			Path: file.Path, MediaType: file.MediaType, Bytes: file.Bytes, SHA256: file.SHA256,
		})
		if s.store.Has(file.SHA256) {
			result.Reused++

			continue
		}
		fetched, err := s.fetch(ctx, assetType, assetID, revision, file)
		if err != nil {
			return Result{}, err
		}
		result.Fetched++
		result.BytesFetched += fetched
	}

	if err = s.store.SaveRevision(record); err != nil {
		return Result{}, err
	}
	stored, err := s.store.Revision(assetType, assetID, record.Key())
	if err != nil {
		return Result{}, err
	}
	result.Record = stored

	return result, nil
}

// fetch downloads one file and publishes it.
//
// The revision is named EXPLICITLY — `revision.ID` when there is one, not the
// word `current` — so a save that lands between the metadata read and the
// download cannot make the two halves come from different versions. That is the
// concrete shape of "a newer remote revision must not silently change a build".
//
// There is no resume. The capability document says `range_requests: false`, so an
// interrupted transfer is restarted; resuming against a server that ignores
// `Range` produces the tail of one attempt glued to the whole of another, which
// would then fail the digest check — correctly, and after wasting the transfer.
func (s *Syncer) fetch(ctx context.Context, assetType, assetID string,
	revision aub.Revision, file aub.File) (int64, error) {
	target := revision.ID
	if target == "" {
		// A `current_only` type has no id to name. The content digest is checked
		// on the way in, so a version that moved between the two calls is caught
		// rather than silently stored under the wrong record.
		target = aub.CurrentRevision
	}

	download, err := s.client.DownloadFile(ctx, assetType, assetID, target, file.Path, "")
	if err != nil {
		return 0, err
	}
	defer download.Body.Close()

	if download.ETag != "" && download.ETag != file.SHA256 {
		return 0, fmt.Errorf("%w: the revision listed %s for %s and the download offered %s",
			ErrDigestMismatch, file.SHA256, file.Path, download.ETag)
	}

	if _, err = s.store.Publish(download.Body, file.SHA256, file.Bytes); err != nil {
		return 0, err
	}

	return file.Bytes, nil
}

// SyncAsset fetches an asset's CURRENT revision.
func (s *Syncer) SyncAsset(ctx context.Context, assetType, assetID string) (Result, error) {
	return s.Sync(ctx, assetType, assetID, aub.CurrentRevision)
}

// Resolve finds a revision to build from, preferring the local cache.
//
// This is what a queued or retried build calls, and the preference is the point:
// a build that already chose a revision reads it from the cache, so a newer
// remote revision cannot change what it compiles. Only a revision the cache does
// not hold is fetched, and only when a syncer is available — an offline
// Companion builds from what it has, and says so when it cannot.
func Resolve(ctx context.Context, store *Store, syncer *Syncer,
	assetType, assetID, key string) (RevisionRecord, error) {
	if key != "" && key != aub.CurrentRevision {
		record, err := store.Revision(assetType, assetID, key)
		if err == nil {
			if verifyErr := store.Verify(record); verifyErr == nil {
				return record, nil
			}
		}
		if syncer == nil {
			return RevisionRecord{}, fmt.Errorf(
				"%w: %s/%s at %s, and there is no connection to fetch it",
				ErrNotCached, assetType, assetID, key)
		}
		result, err := syncer.Sync(ctx, assetType, assetID, key)
		if err != nil {
			return RevisionRecord{}, err
		}

		return result.Record, nil
	}

	if syncer == nil {
		return RevisionRecord{}, errors.New(
			"assetsync: `current` names a version only the server can resolve; " +
				"pin a revision id to build offline")
	}
	result, err := syncer.SyncAsset(ctx, assetType, assetID)
	if err != nil {
		return RevisionRecord{}, err
	}

	return result.Record, nil
}
