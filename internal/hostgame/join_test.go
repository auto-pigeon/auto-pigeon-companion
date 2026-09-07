package hostgame_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/enginefixture"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/hostgame"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The four fakes are the four things a join touches. Each one records what it was
// asked, because the ORDER of the checks is part of what is under test: refusing
// after fetching nine megabytes is the same refusal arrived at more expensively.

type fakeResolver struct {
	resolution aub.HostedGameJoin
	err        error
	redeemed   []string
}

func (f *fakeResolver) ResolveJoinLink(_ context.Context, ticketID string) (aub.HostedGameJoin, error) {
	f.redeemed = append(f.redeemed, ticketID)

	return f.resolution, f.err
}

type fakeFetcher struct {
	result assetsync.Result
	err    error
	calls  int
}

func (f *fakeFetcher) Sync(context.Context, string, string, string) (assetsync.Result, error) {
	f.calls++

	return f.result, f.err
}

type fakeObjects struct{ path string }

func (f fakeObjects) Object(string) (string, error) { return f.path, nil }

type fakeCatalog struct{ entries []job.CatalogEntry }

func (f fakeCatalog) List() ([]job.CatalogEntry, error) { return f.entries, nil }

type fakeRunner struct {
	previewed []job.Request
	submitted []job.Request
}

func (f *fakeRunner) Preview(request job.Request) (*job.Job, error) {
	f.previewed = append(f.previewed, request)

	return &job.Job{
		ID:      "preview",
		Request: request,
		Command: &job.CommandPreview{
			Executable: "/games/quake/quakespasm",
			Args:       []string{"-basedir", "/games/quake", "+connect", "203.0.113.4:26000"},
			WorkingDir: "/games/quake",
			Shell:      "quakespasm -basedir /games/quake +connect 203.0.113.4:26000",
		},
	}, nil
}

func (f *fakeRunner) Submit(request job.Request) (*job.Job, error) {
	f.submitted = append(f.submitted, request)

	return &job.Job{ID: "job9", Request: request, State: job.Queued}, nil
}

const fixtureDigest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func engineEntry(t *testing.T) job.CatalogEntry {
	t.Helper()

	document, err := profile.DecodeEngine(enginefixture.ProfileJSON)
	if err != nil {
		t.Fatal(err)
	}

	return job.CatalogEntry{Profile: document, Trust: profile.TrustBuiltin, Source: "builtin"}
}

func resolution() aub.HostedGameJoin {
	return aub.HostedGameJoin{
		SchemaVersion: aub.HostedGameSchema,
		GameID:        "gme1",
		Title:         "Friday deathmatch",
		Host:          "Vera",
		Endpoint:      "203.0.113.4:26000",
		EndpointHost:  "203.0.113.4",
		EndpointPort:  26000,
		Reachability:  "verified",
		Mode:          aub.ModeListen,
		GameFamily:    "quake1",
		EngineRuntime: "aucom-fixture",
		MapID:         "map1",
		MapName:       "Sunken Chapel",
		MapRevision:   7,
		MapDigest:     fixtureDigest,
		Assets:        aub.AssetProspect{Readable: true, Path: "public"},
		Action:        "join_server",
	}
}

func syncResult(digest string) assetsync.Result {
	return assetsync.Result{
		Record: assetsync.RevisionRecord{
			AssetType:     "map",
			AssetID:       "map1",
			Revision:      7,
			ContentSHA256: digest,
			Files:         []assetsync.FileRecord{{Path: "map.apmap", SHA256: digest, Bytes: 12}},
		},
	}
}

func joinerFor(t *testing.T, resolver *fakeResolver, fetcher *fakeFetcher, catalog fakeCatalog,
	runner *fakeRunner,
) *hostgame.Joiner {
	t.Helper()

	path := filepath.Join(t.TempDir(), "map.apmap")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	return hostgame.NewJoiner(resolver, fetcher, fakeObjects{path: path}, catalog, runner)
}

// A join resolves, downloads, checks and previews — and starts nothing.
func TestAJoinPreparesEverythingAndStartsNothing(t *testing.T) {
	resolver := &fakeResolver{resolution: resolution()}
	fetcher := &fakeFetcher{result: syncResult(fixtureDigest)}
	runner := &fakeRunner{}
	joiner := joinerFor(t, resolver, fetcher, fakeCatalog{entries: []job.CatalogEntry{engineEntry(t)}}, runner)

	plan, err := joiner.Resolve(context.Background(), "autopigeon://join/tkt1")
	if err != nil {
		t.Fatal(err)
	}
	if resolver.redeemed[0] != "tkt1" {
		t.Fatalf("redeemed %q", resolver.redeemed[0])
	}
	if plan.EngineProfileID != enginefixture.ProfileID {
		t.Fatalf("engine = %q", plan.EngineProfileID)
	}
	if plan.Action != profile.ActionJoinServer {
		t.Fatalf("action = %q; AUB names it and a client must not invent one", plan.Action)
	}
	if plan.Request.Runtime["server_host"] != "203.0.113.4" ||
		plan.Request.Runtime["server_port"] != "26000" {
		t.Fatalf("runtime = %+v", plan.Request.Runtime)
	}
	if plan.Preview == nil || plan.Preview.Shell == "" {
		t.Fatal("no command preview: a user cannot approve a command they were not shown")
	}
	if len(runner.submitted) != 0 {
		t.Fatal("resolving started a job")
	}
}

// Approving is something a caller DOES at the moment of launching.
func TestNothingLaunchesWithoutApproval(t *testing.T) {
	resolver := &fakeResolver{resolution: resolution()}
	fetcher := &fakeFetcher{result: syncResult(fixtureDigest)}
	runner := &fakeRunner{}
	joiner := joinerFor(t, resolver, fetcher, fakeCatalog{entries: []job.CatalogEntry{engineEntry(t)}}, runner)

	plan, err := joiner.Resolve(context.Background(), "autopigeon://join/tkt1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = joiner.Launch(plan, false); !errors.Is(err, hostgame.ErrNotApproved) {
		t.Fatalf("err = %v, want ErrNotApproved", err)
	}
	if len(runner.submitted) != 0 {
		t.Fatal("an unapproved plan started a job")
	}
	started, err := joiner.Launch(plan, true)
	if err != nil {
		t.Fatal(err)
	}
	// What runs is what was previewed: one request, resolved once.
	if len(runner.submitted) != 1 || runner.submitted[0].ActionID != profile.ActionJoinServer {
		t.Fatalf("submitted = %+v", runner.submitted)
	}
	if started.ID == "" {
		t.Fatal("no job")
	}
}

// A map this account cannot have is refused BEFORE anything is downloaded.
func TestAJoinIsRefusedBeforeADownloadWhenTheMapIsNotThisAccountsToHave(t *testing.T) {
	unreadable := resolution()
	unreadable.Assets = aub.AssetProspect{
		Readable: false,
		Reason:   "You do not hold a role on this map and it is not public.",
	}
	resolver := &fakeResolver{resolution: unreadable}
	fetcher := &fakeFetcher{result: syncResult(fixtureDigest)}
	joiner := joinerFor(t, resolver, fetcher,
		fakeCatalog{entries: []job.CatalogEntry{engineEntry(t)}}, &fakeRunner{})

	_, err := joiner.Resolve(context.Background(), "autopigeon://join/tkt1")
	if !errors.Is(err, hostgame.ErrMapUnreadable) {
		t.Fatalf("err = %v, want ErrMapUnreadable", err)
	}
	if fetcher.calls != 0 {
		t.Fatal("a download was attempted for a map AUB had already said this account cannot have")
	}
	if !strings.Contains(err.Error(), "role on this map") {
		t.Fatalf("the refusal does not carry AUB's own reason: %v", err)
	}
}

// No engine is a refusal by name, and it happens before the download too.
func TestAMissingEngineIsRefusedByNameAndBeforeTheDownload(t *testing.T) {
	resolver := &fakeResolver{resolution: resolution()}
	fetcher := &fakeFetcher{result: syncResult(fixtureDigest)}
	joiner := joinerFor(t, resolver, fetcher, fakeCatalog{}, &fakeRunner{})

	_, err := joiner.Resolve(context.Background(), "autopigeon://join/tkt1")
	if !errors.Is(err, hostgame.ErrNoEngine) {
		t.Fatalf("err = %v, want ErrNoEngine: \"connect failed\" is a much worse message", err)
	}
	if fetcher.calls != 0 {
		t.Fatal("nine megabytes were fetched for a game this machine cannot start")
	}
	if !strings.Contains(err.Error(), "aucom-fixture") {
		t.Fatalf("the refusal does not name what the host is running: %v", err)
	}
}

// The two digests are two statements and a join is where they have to agree.
func TestABytesMismatchIsRefused(t *testing.T) {
	resolver := &fakeResolver{resolution: resolution()}
	other := strings.Repeat("ab", 32)
	fetcher := &fakeFetcher{result: syncResult(other)}
	joiner := joinerFor(t, resolver, fetcher,
		fakeCatalog{entries: []job.CatalogEntry{engineEntry(t)}}, &fakeRunner{})

	_, err := joiner.Resolve(context.Background(), "autopigeon://join/tkt1")
	if !errors.Is(err, hostgame.ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
}

// A link is a link, and a lookalike is not.
func TestOnlyTheSchemeAUBPublishedIsFollowed(t *testing.T) {
	for _, accepted := range []string{
		"autopigeon://join/tkt1",
		"  autopigeon://join/tkt1  ",
		"tkt1",
	} {
		id, err := aub.ParseJoinLink(accepted)
		if err != nil || id != "tkt1" {
			t.Fatalf("%q: id, err = %q, %v", accepted, id, err)
		}
	}
	for _, refused := range []string{
		"",
		"https://evil.example/join/tkt1",
		"autopigeon://host/tkt1",
		"aucom://join/tkt1",
		"autopigeon://join/tkt1?token=secret",
	} {
		if _, err := aub.ParseJoinLink(refused); err == nil {
			t.Fatalf("%q was accepted; following a link somebody else chose the host of is the "+
				"whole thing a scheme check prevents", refused)
		}
	}
}

// A warning AUB sent travels to the person, verbatim.
func TestAUBsWarningsReachThePerson(t *testing.T) {
	warned := resolution()
	warned.Reachability = "unverified"
	warned.Warnings = []string{"Nobody has checked that this address can be reached."}
	resolver := &fakeResolver{resolution: warned}
	fetcher := &fakeFetcher{result: syncResult(fixtureDigest)}
	joiner := joinerFor(t, resolver, fetcher,
		fakeCatalog{entries: []job.CatalogEntry{engineEntry(t)}}, &fakeRunner{})

	plan, err := joiner.Resolve(context.Background(), "autopigeon://join/tkt1")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0] != warned.Warnings[0] {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}
