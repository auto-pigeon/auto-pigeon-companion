package cli

// `companion aub` — browse and sync the assets a signed-in account may build
// with.
//
// Every one of these goes through AUB's versioned Companion API rather than
// through PocketBase's collection API, for the reason internal/aub's package
// comment gives: a program written against a collection listing is written
// against a schema, and every column added at the backend becomes a
// compatibility question here.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
)

// stagingMaxAge is how long a staging file must be untouched before a clean
// removes it.
//
// An hour rather than a minute: a staging file is an in-flight download, and a
// slow transfer on a slow link must not be deleted out from under itself by a
// clean somebody ran in another window.
const stagingMaxAge = time.Hour

func runAUB(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(env.Stderr, "error: aub requires a subcommand: "+
			"capabilities, catalog, show, revisions, sync, cached, verify, export, or clean")
		return 2
	}
	switch args[0] {
	case "capabilities":
		return runAUBCapabilities(env, args[1:])
	case "catalog":
		return runAUBCatalog(env, args[1:])
	case "show":
		return runAUBShow(env, args[1:])
	case "revisions":
		return runAUBRevisions(env, args[1:])
	case "sync":
		return runAUBSync(env, args[1:])
	case "cached":
		return runAUBCached(env, args[1:])
	case "verify":
		return runAUBVerify(env, args[1:])
	case "export":
		return runAUBExport(env, args[1:])
	case "clean":
		return runAUBClean(env, args[1:])
	default:
		fmt.Fprintf(env.Stderr, "error: unknown aub subcommand %q\n", args[0])
		return 2
	}
}

// openStore resolves the local asset cache. It needs no network and no session,
// which is what makes every offline subcommand below work with neither.
func openStore(env *Env) (*assetsync.Store, error) {
	settings, err := loadSettings(env)
	if err != nil {
		return nil, err
	}
	dir, err := settings.AssetCache()
	if err != nil {
		return nil, err
	}

	return assetsync.Open(dir)
}

// openSyncer resolves a client, a store and the deployment's capabilities.
//
// The capability read is what fails first when a session has been revoked, and
// that is deliberate: one clear "sign in again" rather than a refusal per asset.
func openSyncer(ctx context.Context, env *Env) (*assetsync.Syncer, *assetsync.Store, error) {
	settings, err := loadSettings(env)
	if err != nil {
		return nil, nil, err
	}
	if !settings.Session.Valid() {
		return nil, nil, errors.New("not signed in: run `companion auth login --email <address>`")
	}
	client, err := newClient(settings)
	if err != nil {
		return nil, nil, err
	}
	dir, err := settings.AssetCache()
	if err != nil {
		return nil, nil, err
	}
	store, err := assetsync.Open(dir)
	if err != nil {
		return nil, nil, err
	}
	syncer, err := assetsync.NewSyncer(ctx, client, store)
	if err != nil {
		return nil, nil, err
	}

	return syncer, store, nil
}

// reportAUBError turns a backend refusal into something a person can act on.
//
// A revoked session and an asset that is no longer available are the two the
// user has to be told apart, because the remedies differ: sign in again, or ask
// whoever owns it. Everything else is passed through with the reason code the
// backend gave, unedited — this program does not invent reasons.
func reportAUBError(env *Env, err error) int {
	var apiErr *aub.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Unauthorized():
			fmt.Fprintf(env.Stderr, "error: %s rejected this session. "+
				"Run `companion auth login --email <address>` and try again.\n", apiErr.Path)
			fmt.Fprintln(env.Stderr, "your locally cached assets are untouched and "+
				"`companion aub cached` still lists them")
			return 1
		case apiErr.StatusCode == 404:
			fmt.Fprintf(env.Stderr, "error: %s\n", apiErr.Message)
			fmt.Fprintln(env.Stderr, "the asset may have been deleted, or your access to it "+
				"withdrawn; AUB answers the same way for both")
			return 1
		}
	}
	if errors.Is(err, assetsync.ErrDigestMismatch) {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		fmt.Fprintln(env.Stderr, "nothing was published to the cache; "+
			"no build can read these bytes")
		return 1
	}

	return fail(env, err)
}

func runAUBCapabilities(env *Env, args []string) int {
	set := newFlagSet(env, "aub capabilities")
	asJSON := set.Bool("json", false, "print the capability document verbatim")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	ctx, stop := signalContext()
	defer stop()

	syncer, _, err := openSyncer(ctx, env)
	if err != nil {
		return reportAUBError(env, err)
	}
	capabilities := syncer.Capabilities()

	if *asJSON {
		return printJSON(env, capabilities)
	}

	fmt.Fprintf(env.Stdout, "api version:   %s\n", capabilities.APIVersion)
	fmt.Fprintf(env.Stdout, "auth:          %s\n", capabilities.Session.AuthCollection)
	if lifetime := capabilities.Session.Lifetime(); lifetime > 0 {
		fmt.Fprintf(env.Stdout, "token lasts:   %s\n", lifetime)
	} else {
		fmt.Fprintln(env.Stdout, "token lasts:   (not declared — refresh when a request is rejected)")
	}
	fmt.Fprintf(env.Stdout, "revocable:     %t\n", capabilities.Session.Revocable)
	fmt.Fprintf(env.Stdout, "resumable:     %t (range requests)\n", capabilities.Download.RangeRequests)
	fmt.Fprintf(env.Stdout, "etag:          %t\n", capabilities.Download.ETag)
	fmt.Fprintf(env.Stdout, "digests:       %s\n", capabilities.Download.DigestAlgorithm)
	fmt.Fprintln(env.Stdout, "asset types:")

	writer := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "  TYPE\tREVISIONS\tHISTORY\tSCOPES")
	for _, capability := range capabilities.Types {
		fmt.Fprintf(writer, "  %s\t%s\t%t\t%s\n", capability.AssetType,
			capability.RevisionAddressing, capability.History,
			strings.Join(capability.Scopes, ","))
	}
	writer.Flush()

	return 0
}

func runAUBCatalog(env *Env, args []string) int {
	set := newFlagSet(env, "aub catalog")
	scope := set.String("scope", aub.ScopeOwned, "authorization path: owned, member, workspace, public")
	assetType := set.String("type", "", "asset type, or comma-separated types")
	game := set.String("game", "", "filter by game")
	name := set.String("name", "", "filter by a substring of the display name")
	limit := set.Int("limit", 0, "page size (server default when zero)")
	all := set.Bool("all", false, "walk every page")
	asJSON := set.Bool("json", false, "print the entries as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	ctx, stop := signalContext()
	defer stop()

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	if !settings.Session.Valid() {
		fmt.Fprintln(env.Stderr, "error: not signed in: run `companion auth login --email <address>`")
		return 1
	}
	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}

	query := aub.CatalogQuery{
		Scope: *scope, Game: *game, Name: *name, Limit: *limit,
	}
	for _, value := range strings.Split(*assetType, ",") {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			query.Types = append(query.Types, trimmed)
		}
	}

	if *all {
		assets, listErr := client.CatalogAll(ctx, query, 0)
		if listErr != nil {
			return reportAUBError(env, listErr)
		}
		if *asJSON {
			return printJSON(env, assets)
		}

		return printAssets(env, assets, "")
	}

	page, err := client.Catalog(ctx, query)
	if err != nil {
		return reportAUBError(env, err)
	}
	if *asJSON {
		return printJSON(env, page)
	}

	return printAssets(env, page.Items, page.NextCursor)
}

func printAssets(env *Env, assets []aub.Asset, cursor string) int {
	if len(assets) == 0 {
		fmt.Fprintln(env.Stdout, "no assets")
		return 0
	}
	writer := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "TYPE\tID\tREVISION\tNAME\tACCESS")
	for _, asset := range assets {
		revision := "(none)"
		if asset.CurrentRevision != nil {
			revision = fmt.Sprintf("%d", asset.CurrentRevision.Number)
			if !asset.CurrentRevision.Immutable {
				revision += "*"
			}
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			asset.AssetType, asset.AssetID, revision, asset.DisplayName, asset.AccessPath)
	}
	writer.Flush()

	// `*` is not decoration: it marks a version that cannot be fetched again, and
	// a person choosing what to build from needs to know which ones those are.
	fmt.Fprintln(env.Stdout, "\n* the current version of this type cannot be re-fetched later")
	if cursor != "" {
		fmt.Fprintf(env.Stdout, "more: --cursor is not a flag here; use --all, "+
			"or the API's cursor %q\n", cursor)
	}

	return 0
}

func runAUBShow(env *Env, args []string) int {
	set := newFlagSet(env, "aub show")
	asJSON := set.Bool("json", false, "print the asset as JSON")
	assetType, assetID, rest, code, ok := parseAssetRef(env, set, args, "aub show")
	if !ok {
		return code
	}
	_ = rest

	ctx, stop := signalContext()
	defer stop()

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}

	detail, err := client.Asset(ctx, assetType, assetID)
	if err != nil {
		return reportAUBError(env, err)
	}
	if *asJSON {
		return printJSON(env, detail)
	}

	asset := detail.Asset
	fmt.Fprintf(env.Stdout, "type:        %s\n", asset.AssetType)
	fmt.Fprintf(env.Stdout, "id:          %s\n", asset.AssetID)
	fmt.Fprintf(env.Stdout, "name:        %s\n", asset.DisplayName)
	if asset.Game != "" {
		fmt.Fprintf(env.Stdout, "game:        %s\n", asset.Game)
	}
	fmt.Fprintf(env.Stdout, "visibility:  %s\n", asset.Visibility)
	fmt.Fprintf(env.Stdout, "access:      %s", asset.AccessPath)
	if asset.Role != "" {
		fmt.Fprintf(env.Stdout, " (%s)", asset.Role)
	}
	fmt.Fprintln(env.Stdout)
	fmt.Fprintf(env.Stdout, "revisions:   %d (%s)\n", detail.RevisionCount, asset.RevisionAddressing)
	if asset.CurrentRevision != nil {
		fmt.Fprintf(env.Stdout, "current:     %d", asset.CurrentRevision.Number)
		if asset.CurrentRevision.ID != "" {
			fmt.Fprintf(env.Stdout, " (%s)", asset.CurrentRevision.ID)
		}
		if !asset.CurrentRevision.Immutable {
			fmt.Fprint(env.Stdout, " — not re-fetchable later")
		}
		fmt.Fprintln(env.Stdout)
	} else {
		fmt.Fprintln(env.Stdout, "current:     (nothing saved yet)")
	}

	return 0
}

func runAUBRevisions(env *Env, args []string) int {
	set := newFlagSet(env, "aub revisions")
	limit := set.Int("limit", 0, "page size (server default when zero)")
	offset := set.Int("offset", 0, "where to start")
	asJSON := set.Bool("json", false, "print the page as JSON")
	assetType, assetID, _, code, ok := parseAssetRef(env, set, args, "aub revisions")
	if !ok {
		return code
	}

	ctx, stop := signalContext()
	defer stop()

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}

	page, err := client.History(ctx, assetType, assetID, *limit, *offset)
	if err != nil {
		return reportAUBError(env, err)
	}
	if *asJSON {
		return printJSON(env, page)
	}
	if len(page.Items) == 0 {
		fmt.Fprintln(env.Stdout, "no revisions")
		return 0
	}

	writer := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "REVISION\tID\tWHEN\tKIND\tDIGEST")
	for _, item := range page.Items {
		digest := item.ContentSHA256
		if digest == "" {
			digest = "(not recorded)"
		} else if len(digest) > 12 {
			digest = digest[:12]
		}
		id := item.ID
		if id == "" {
			id = "(no id — not re-fetchable)"
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\n", item.Number, id, item.CreatedAt, item.Kind, digest)
	}
	writer.Flush()
	fmt.Fprintf(env.Stdout, "\n%d of %d, retention: %s\n", len(page.Items), page.Total, page.Retention)

	return 0
}

func runAUBSync(env *Env, args []string) int {
	set := newFlagSet(env, "aub sync")
	revision := set.String("revision", aub.CurrentRevision,
		"the exact revision id to fetch, or `current`")
	asJSON := set.Bool("json", false, "print the resulting record as JSON")
	assetType, assetID, _, code, ok := parseAssetRef(env, set, args, "aub sync")
	if !ok {
		return code
	}

	ctx, stop := signalContext()
	defer stop()

	syncer, _, err := openSyncer(ctx, env)
	if err != nil {
		return reportAUBError(env, err)
	}

	result, err := syncer.Sync(ctx, assetType, assetID, *revision)
	if err != nil {
		return reportAUBError(env, err)
	}
	if *asJSON {
		return printJSON(env, result)
	}

	record := result.Record
	if result.AlreadyComplete {
		fmt.Fprintf(env.Stdout, "already cached: %s %s at revision %d\n",
			record.AssetType, record.AssetID, record.Revision)
	} else {
		fmt.Fprintf(env.Stdout, "synced %s %s at revision %d (%d fetched, %d already held, %d bytes)\n",
			record.AssetType, record.AssetID, record.Revision,
			result.Fetched, result.Reused, result.BytesFetched)
	}
	fmt.Fprintf(env.Stdout, "pin:      %s\n", record.Key())
	if !record.Immutable {
		fmt.Fprintln(env.Stdout, "note:     this asset type keeps no per-version rows, "+
			"so this pin cannot be re-fetched if it changes")
	}
	fmt.Fprintf(env.Stdout, "manifest: %s\n", record.ManifestSHA256)
	for _, file := range record.Files {
		fmt.Fprintf(env.Stdout, "  %s  %d bytes  %s\n", file.Path, file.Bytes, file.SHA256)
	}

	return 0
}

// runAUBCached lists what is on this machine. No network, no session.
func runAUBCached(env *Env, args []string) int {
	set := newFlagSet(env, "aub cached")
	asJSON := set.Bool("json", false, "print the records as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}

	store, err := openStore(env)
	if err != nil {
		return fail(env, err)
	}

	assetType, assetID := "", ""
	if len(rest) > 0 {
		assetType = rest[0]
	}
	if len(rest) > 1 {
		assetID = rest[1]
	}

	records, err := store.Revisions(assetType, assetID)
	if err != nil {
		return fail(env, err)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].AssetType != records[j].AssetType {
			return records[i].AssetType < records[j].AssetType
		}
		if records[i].AssetID != records[j].AssetID {
			return records[i].AssetID < records[j].AssetID
		}
		return records[i].Revision > records[j].Revision
	})

	if *asJSON {
		return printJSON(env, records)
	}
	if len(records) == 0 {
		fmt.Fprintf(env.Stdout, "nothing cached in %s\n", store.Root())
		return 0
	}

	writer := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "TYPE\tID\tREVISION\tPIN\tFILES\tBYTES\tSYNCED")
	for _, record := range records {
		fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%d\t%d\t%s\n",
			record.AssetType, record.AssetID, record.Revision, record.Key(),
			len(record.Files), record.TotalBytes, record.SyncedAt.Format("2006-01-02 15:04"))
	}
	writer.Flush()

	return 0
}

// runAUBVerify re-hashes a cached revision. No network, no session — what it
// checks is whether this machine still holds what it published.
func runAUBVerify(env *Env, args []string) int {
	set := newFlagSet(env, "aub verify")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}

	store, err := openStore(env)
	if err != nil {
		return fail(env, err)
	}

	assetType, assetID := "", ""
	if len(rest) > 0 {
		assetType = rest[0]
	}
	if len(rest) > 1 {
		assetID = rest[1]
	}

	records, err := store.Revisions(assetType, assetID)
	if err != nil {
		return fail(env, err)
	}
	if len(records) == 0 {
		fmt.Fprintln(env.Stdout, "nothing cached")
		return 0
	}

	failures := 0
	for _, record := range records {
		if verifyErr := store.Verify(record); verifyErr != nil {
			failures++
			fmt.Fprintf(env.Stdout, "FAIL %s %s %s: %v\n",
				record.AssetType, record.AssetID, record.Key(), verifyErr)

			continue
		}
		fmt.Fprintf(env.Stdout, "ok   %s %s %s (%d files)\n",
			record.AssetType, record.AssetID, record.Key(), len(record.Files))
	}
	if failures > 0 {
		fmt.Fprintf(env.Stderr, "%d of %d cached revisions did not verify; "+
			"re-sync them before building\n", failures, len(records))
		return 1
	}

	return 0
}

// runAUBExport writes a cached revision's files into a directory.
func runAUBExport(env *Env, args []string) int {
	set := newFlagSet(env, "aub export")
	into := set.String("into", "", "directory to write the files into")
	pin := set.String("revision", "", "the cached pin to export (default: the newest cached)")
	assetType, assetID, _, code, ok := parseAssetRef(env, set, args, "aub export")
	if !ok {
		return code
	}
	if *into == "" {
		fmt.Fprintln(env.Stderr, "error: aub export requires --into <directory>")
		return 2
	}

	store, err := openStore(env)
	if err != nil {
		return fail(env, err)
	}

	record, err := resolveCached(store, assetType, assetID, *pin)
	if err != nil {
		return fail(env, err)
	}
	if err = os.MkdirAll(*into, 0o700); err != nil {
		return fail(env, err)
	}

	written, err := store.Materialize(record, *into)
	if err != nil {
		return fail(env, err)
	}
	for _, path := range written {
		fmt.Fprintln(env.Stdout, path)
	}
	fmt.Fprintf(env.Stdout, "%d files from %s %s at revision %d (%s)\n",
		len(written), record.AssetType, record.AssetID, record.Revision, record.Key())

	return 0
}

// resolveCached picks a cached record: the named pin, or the newest revision of
// that asset.
func resolveCached(store *assetsync.Store, assetType, assetID, pin string) (assetsync.RevisionRecord, error) {
	if pin != "" {
		return store.Revision(assetType, assetID, pin)
	}
	records, err := store.Revisions(assetType, assetID)
	if err != nil {
		return assetsync.RevisionRecord{}, err
	}
	if len(records) == 0 {
		return assetsync.RevisionRecord{}, fmt.Errorf(
			"%w: %s %s — run `companion aub sync` first", assetsync.ErrNotCached, assetType, assetID)
	}
	newest := records[0]
	for _, record := range records[1:] {
		if record.Revision > newest.Revision {
			newest = record
		}
	}

	return newest, nil
}

func runAUBClean(env *Env, args []string) int {
	set := newFlagSet(env, "aub clean")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	store, err := openStore(env)
	if err != nil {
		return fail(env, err)
	}
	removed, err := store.CleanStaging(stagingMaxAge, time.Now())
	if err != nil {
		return fail(env, err)
	}
	for _, path := range removed {
		fmt.Fprintln(env.Stdout, "removed "+filepath.Base(path))
	}
	fmt.Fprintf(env.Stdout, "%d interrupted downloads removed from %s\n",
		len(removed), filepath.Join(store.Root(), "staging"))

	return 0
}

// parseAssetRef reads the `<type> <id>` pair every asset subcommand takes.
func parseAssetRef(env *Env, set *flag.FlagSet, args []string, name string,
) (string, string, []string, int, bool) {
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return "", "", nil, code, false
	}
	if len(rest) < 2 {
		fmt.Fprintf(env.Stderr, "error: %s requires an asset type and an asset id "+
			"(for example: %s map abc123def456789)\n", name, name)
		return "", "", nil, 2, false
	}

	return rest[0], rest[1], rest[2:], 0, true
}
