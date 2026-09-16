package hostgame_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/enginefixture"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/hostgame"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joincontent"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joinready"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The fakes are the things a join touches, and each records what it was asked:
// WHEN a ticket is spent and WHETHER bytes were fetched are part of what is under
// test, not incidental.

type fakeRemote struct {
	mu         sync.Mutex
	detail     aub.HostedGameDetail
	detailErr  error
	pkg        aub.JoinPackage
	bodies     map[string][]byte
	resolution aub.HostedGameJoin
	resolveErr error

	minted     int
	redeemed   []string
	downloaded int
}

func (f *fakeRemote) HostedGameByID(context.Context, string) (aub.HostedGameDetail, error) {
	return f.detail, f.detailErr
}

func (f *fakeRemote) GameProfileBySlug(context.Context, string) (aub.GameProfileSummary, error) {
	return aub.GameProfileSummary{Slug: "quake1", Name: "Quake", EngineFamily: "quake1"}, nil
}

func (f *fakeRemote) GameJoinContent(context.Context, string) (aub.GameJoinContent, error) {
	return aub.GameJoinContent{SchemaVersion: aub.HostedGameSchema, Package: f.pkg}, nil
}

func (f *fakeRemote) MintJoinLink(_ context.Context, gameID string) (aub.HostedGameTicket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.minted++

	return aub.HostedGameTicket{ID: "fresh-ticket", GameID: gameID}, nil
}

func (f *fakeRemote) ResolveJoinLink(_ context.Context, ticketID string) (aub.HostedGameJoin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redeemed = append(f.redeemed, ticketID)

	return f.resolution, f.resolveErr
}

func (f *fakeRemote) DownloadJoinContentFile(_ context.Context, _, destination string) (*aub.Download, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloaded++
	body, ok := f.bodies[destination]
	if !ok {
		return nil, &aub.APIError{StatusCode: http.StatusNotFound}
	}

	return &aub.Download{Body: io.NopCloser(bytes.NewReader(body))}, nil
}

type fakeRunner struct {
	mu        sync.Mutex
	previewed []job.Request
	submitted []*job.Job
}

func (f *fakeRunner) Preview(request job.Request) (*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previewed = append(f.previewed, request)

	return &job.Job{ID: "preview", Request: request, Command: &job.CommandPreview{
		Executable: "/games/quake/engine", Args: []string{"+connect", request.Runtime["server_host"]},
		WorkingDir: "/games/quake", Shell: "engine +connect " + request.Runtime["server_host"],
	}}, nil
}

func (f *fakeRunner) Submit(request job.Request) (*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	started := &job.Job{ID: "job9", Request: request, State: job.Queued, ProfileID: request.ProfileID,
		ActionID: request.ActionID}
	f.submitted = append(f.submitted, started)

	return started, nil
}

func (f *fakeRunner) List() ([]*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]*job.Job(nil), f.submitted...), nil
}

type catalog struct{ entries []job.CatalogEntry }

func (c catalog) List() ([]job.CatalogEntry, error) { return c.entries, nil }
func (c catalog) Lookup(id string) (job.CatalogEntry, error) {
	for _, entry := range c.entries {
		if entry.Profile.Metadata().ID == id {
			return entry, nil
		}
	}

	return job.CatalogEntry{}, job.ErrNoProfile
}

// world is one machine that has everything a join needs.
type world struct {
	remote  *fakeRemote
	runner  *fakeRunner
	local   joinready.Local
	owned   string
	bindSet *binding.Set
	stager  *joincontent.Stager
}

func newWorld(t *testing.T) *world {
	t.Helper()

	document, err := profile.DecodeEngine(enginefixture.ProfileJSON)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := profile.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	entry := job.CatalogEntry{Profile: document, Trust: profile.TrustBuiltin, Digest: digest, Source: "builtin"}
	owned := t.TempDir()
	os.MkdirAll(filepath.Join(owned, "id1"), 0o755)
	os.WriteFile(filepath.Join(owned, "id1", "pak0.pak"), []byte("PACK"), 0o644)
	program := filepath.Join(t.TempDir(), "engine")
	os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755)
	set := binding.NewSet()
	err = set.Put(binding.LocalBinding{SchemaVersion: binding.SchemaVersion, ProfileID: document.ID,
		ProfileVersion: document.Version, ProfileDigest: digest, Trust: profile.TrustBuiltin,
		Acquisition: profile.AcquireUserPath,
		Executables: map[string]string{"engine": program},
		Roots:       map[string]string{"game_root": owned, "content_root": t.TempDir()},
		UpdatedAt:   time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	cache := t.TempDir()
	store, err := assetsync.Open(cache)
	if err != nil {
		t.Fatal(err)
	}
	stager := &joincontent.Stager{Root: filepath.Join(cache, "join-content"), Store: store}

	w := &world{remote: &fakeRemote{bodies: map[string][]byte{}}, runner: &fakeRunner{}, owned: owned,
		bindSet: set, stager: stager}
	w.local = joinready.Local{
		Catalog:  catalog{entries: []job.CatalogEntry{entry}},
		Bindings: func() (*binding.Set, error) { return w.bindSet, nil },
		Checker:  engine.Checker{Platform: profile.Platform{OS: "linux", Arch: "amd64"}},
		Stager:   stager,
	}
	w.remote.detail = aub.HostedGameDetail{SchemaVersion: aub.HostedGameSchema, Game: aub.HostedGame{
		ID: "gme1", Title: "Friday deathmatch", HostNickname: "Vera", State: "live", Joinable: true,
		MapID: "map1", MapName: "Sunken Chapel", MapRevision: 7, GameFamily: "quake1", GameSlug: "quake1",
		EngineRuntime: "aucom-fixture", Endpoint: "203.0.113.4:26000", Reachability: "verified",
		PlayersSource: "host", JoinContent: aub.JoinContent{State: aub.JoinContentNotRequired,
			ContentIdentity: "quake1:id1/maps/dm3.bsp", Source: "host"},
	}}
	w.remote.resolution = aub.HostedGameJoin{SchemaVersion: aub.HostedGameSchema, GameID: "gme1",
		Title: "Friday deathmatch", Host: "Vera", Endpoint: "203.0.113.4:26000", EndpointHost: "203.0.113.4",
		EndpointPort: 26000, EndpointKey: "203.0.113.4:26000", Reachability: "verified", GameFamily: "quake1",
		EngineRuntime: "aucom-fixture", MapID: "map1", MapRevision: 7,
		Assets:      aub.AssetProspect{Readable: true, Path: "public"},
		JoinContent: aub.JoinContent{State: aub.JoinContentNotRequired}, Action: "join_server"}

	return w
}

// withPackage makes the game require join content, served correctly.
func (w *world) withPackage(t *testing.T) aub.JoinPackage {
	t.Helper()

	bodies := map[string][]byte{"maps/chapel.bsp": []byte("BSP29" + strings.Repeat("c", 3000))}
	pkg := aub.JoinPackage{SchemaVersion: aub.JoinContentSchema, MapID: "map1", MapRevision: 7, GameFamily: "quake1"}
	for destination, body := range bodies {
		sum := sha256.Sum256(body)
		pkg.Files = append(pkg.Files, aub.JoinContentFile{Role: "bsp", Destination: destination,
			Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	}
	pkg.PackageSHA256 = pkg.Manifest().Digest()
	pkg.TotalBytes, pkg.FileCount = 3005, 1
	readable := true
	w.remote.pkg, w.remote.bodies = pkg, bodies
	w.remote.detail.Game.PackageSHA = pkg.PackageSHA256
	w.remote.detail.Game.JoinContent = aub.JoinContent{State: aub.JoinContentRequired, PackageSHA256: pkg.PackageSHA256,
		TotalBytes: pkg.TotalBytes, FileCount: 1, Source: "host", Readable: &readable}
	w.remote.resolution.PackageSHA = pkg.PackageSHA256
	w.remote.resolution.JoinContent = w.remote.detail.Game.JoinContent

	return pkg
}

func (w *world) joiner() *hostgame.Joiner { return hostgame.NewJoiner(w.remote, w.local, w.runner) }

// A join resolves, checks and previews — and starts nothing.
func TestAJoinPreparesEverythingAndStartsNothing(t *testing.T) {
	w := newWorld(t)
	plan, err := w.joiner().Prepare(context.Background(), "gme1")
	if err != nil {
		t.Fatal(err)
	}
	if w.remote.minted != 1 || len(w.remote.redeemed) != 1 || w.remote.redeemed[0] != "fresh-ticket" {
		t.Fatalf("minted %d, redeemed %v: a review spends exactly one fresh ticket", w.remote.minted, w.remote.redeemed)
	}
	if plan.EngineProfileID != enginefixture.ProfileID || plan.Action != profile.ActionJoinServer {
		t.Fatalf("engine = %q, action = %q", plan.EngineProfileID, plan.Action)
	}
	if plan.Request.Runtime["server_host"] != "203.0.113.4" || plan.Request.Runtime["server_port"] != "26000" {
		t.Fatalf("runtime = %+v", plan.Request.Runtime)
	}
	if plan.Preview == nil || plan.Preview.Shell == "" {
		t.Fatal("no command preview: a user cannot approve a command they were not shown")
	}
	if len(w.runner.submitted) != 0 {
		t.Fatal("preparing started a job")
	}
}

// Setup can take minutes; a two-minute one-use ticket is not spent on it.
func TestSetupNeverSpendsATicket(t *testing.T) {
	w := newWorld(t)
	w.withPackage(t)
	joiner := w.joiner()
	report, err := joiner.Assess(context.Background(), "gme1")
	if err != nil {
		t.Fatal(err)
	}
	if report.State != joinready.StateSetupRequired || report.Next != joinready.StepJoinContent {
		t.Fatalf("report = %s next %s", report.State, report.Next)
	}
	if report, err = joiner.DownloadContent(context.Background(), "gme1", nil); err != nil {
		t.Fatal(err)
	}
	if report.State != joinready.StateReadyForReview {
		t.Fatalf("after download: %s %+v", report.State, report.Steps)
	}
	if w.remote.minted != 0 || len(w.remote.redeemed) != 0 {
		t.Fatalf("setup minted %d and redeemed %v", w.remote.minted, w.remote.redeemed)
	}
}

// Approving is something a caller DOES, and a second approval is the same game.
func TestNothingLaunchesWithoutApproval(t *testing.T) {
	w := newWorld(t)
	joiner := w.joiner()
	plan, err := joiner.Prepare(context.Background(), "gme1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = joiner.Launch(plan, false); !errors.Is(err, hostgame.ErrNotApproved) {
		t.Fatalf("err = %v, want ErrNotApproved", err)
	}
	if len(w.runner.submitted) != 0 {
		t.Fatal("an unapproved plan started a job")
	}
	started, err := joiner.Launch(plan, true)
	if err != nil || started.ID == "" {
		t.Fatalf("launch: %v", err)
	}
	again, err := joiner.Launch(plan, true)
	if !errors.Is(err, hostgame.ErrAlreadyJoining) || again.ID != started.ID {
		t.Fatalf("a double click: %v, job %v", err, again)
	}
	if len(w.runner.submitted) != 1 || w.runner.submitted[0].Request.ActionID != profile.ActionJoinServer {
		t.Fatalf("submitted %d jobs", len(w.runner.submitted))
	}
}

func TestTwoTabsApprovingAtOnceStartOneGame(t *testing.T) {
	w := newWorld(t)
	joiner := w.joiner()
	plan, err := joiner.Prepare(context.Background(), "gme1")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			joiner.Launch(plan, true)
		}()
	}
	group.Wait()
	if len(w.runner.submitted) != 1 {
		t.Fatalf("%d engines started for one join", len(w.runner.submitted))
	}
}

func TestAnOldReviewCannotBeApproved(t *testing.T) {
	w := newWorld(t)
	joiner := w.joiner()
	plan, err := joiner.Prepare(context.Background(), "gme1")
	if err != nil {
		t.Fatal(err)
	}
	plan.PreparedAt = plan.PreparedAt.Add(-hostgame.PlanLifetime - time.Second)
	if _, err = joiner.Launch(plan, true); !errors.Is(err, hostgame.ErrPlanExpired) {
		t.Fatalf("err = %v", err)
	}
}

// A map this account cannot have is refused BEFORE anything is downloaded.
func TestAJoinIsRefusedBeforeADownloadWhenTheMapIsNotThisAccountsToHave(t *testing.T) {
	w := newWorld(t)
	w.withPackage(t)
	unreadable := false
	w.remote.detail.Game.JoinContent.Readable = &unreadable
	w.remote.resolution.Assets = aub.AssetProspect{Readable: false,
		Reason: "You do not hold a role on this map and it is not public."}

	joiner := w.joiner()
	if _, err := joiner.DownloadContent(context.Background(), "gme1", nil); !errors.Is(err, hostgame.ErrMapUnreadable) {
		t.Fatalf("download: %v", err)
	}
	_, err := joiner.Resolve(context.Background(), "autopigeon://join/tkt1")
	if !errors.Is(err, hostgame.ErrMapUnreadable) || !strings.Contains(err.Error(), "role on this map") {
		t.Fatalf("resolve: %v", err)
	}
	if w.remote.downloaded != 0 {
		t.Fatal("bytes were fetched for a map AUB had already said this account cannot have")
	}
}

// No engine is a refusal by name, before the download and before a ticket.
func TestAMissingEngineIsRefusedByNameAndBeforeTheDownload(t *testing.T) {
	w := newWorld(t)
	w.withPackage(t)
	w.remote.detail.Game.EngineRuntime = "ironwail"
	w.remote.resolution.EngineRuntime = "ironwail"

	joiner := w.joiner()
	_, err := joiner.Prepare(context.Background(), "gme1")
	if !errors.Is(err, hostgame.ErrNoEngine) || !strings.Contains(err.Error(), "ironwail") {
		t.Fatalf("err = %v, want ErrNoEngine naming the runtime", err)
	}
	if w.remote.minted != 0 || w.remote.downloaded != 0 {
		t.Fatalf("minted %d, downloaded %d for a game this machine cannot start", w.remote.minted, w.remote.downloaded)
	}
}

// Bytes that are not the package are never staged.
func TestABytesMismatchIsRefused(t *testing.T) {
	w := newWorld(t)
	pkg := w.withPackage(t)
	w.remote.bodies["maps/chapel.bsp"] = append([]byte("EVIL!"), w.remote.bodies["maps/chapel.bsp"][5:]...)

	joiner := w.joiner()
	if _, err := joiner.DownloadContent(context.Background(), "gme1", nil); !errors.Is(err, assetsync.ErrDigestMismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, err := w.stager.Lookup(pkg.PackageSHA256, pkg.Files); err == nil {
		t.Fatal("tampered bytes were staged")
	}
	report, _ := joiner.Assess(context.Background(), "gme1")
	if report.State == joinready.StateReadyForReview {
		t.Fatal("a failed download reads as ready")
	}
}

// What the engine is pointed at is the managed stage, and the owned game is
// only ever linked, never written.
func TestAStagedPackageIsWhatTheEngineLoads(t *testing.T) {
	w := newWorld(t)
	pkg := w.withPackage(t)
	joiner := w.joiner()
	if _, err := joiner.DownloadContent(context.Background(), "gme1", nil); err != nil {
		t.Fatal(err)
	}
	plan, err := joiner.Prepare(context.Background(), "gme1")
	if err != nil {
		t.Fatal(err)
	}
	stage, err := w.stager.Lookup(pkg.PackageSHA256, pkg.Files)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Request.Roots["game_root"] != stage.BaseDir || plan.Request.Roots["content_root"] != stage.GameDirPath ||
		plan.Request.Runtime["mod_name"] != joincontent.GameDirName(pkg.PackageSHA256) {
		t.Fatalf("request = %+v", plan.Request)
	}
	if target, err := os.Readlink(filepath.Join(stage.BaseDir, "id1")); err != nil || target != filepath.Join(w.owned, "id1") {
		t.Fatalf("id1 link = %q, %v", target, err)
	}
	entries, _ := os.ReadDir(w.owned)
	if len(entries) != 1 {
		t.Fatalf("the owned game folder gained entries: %v", entries)
	}
}

// The game moving during setup is caught at the fresh ticket, by name.
func TestAGameThatChangedDuringSetupIsRefusedAfterTheFreshTicket(t *testing.T) {
	for name, change := range map[string]func(w *world){
		"revision": func(w *world) { w.remote.resolution.MapRevision = 8 },
		"address": func(w *world) {
			w.remote.resolution.Endpoint = "203.0.113.9:26000"
			w.remote.resolution.EndpointHost = "203.0.113.9"
		},
		"package": func(w *world) {
			w.remote.resolution.JoinContent.PackageSHA256 = "sha256:" + strings.Repeat("0", 64)
			w.remote.resolution.PackageSHA = w.remote.resolution.JoinContent.PackageSHA256
		},
	} {
		w := newWorld(t)
		w.withPackage(t)
		joiner := w.joiner()
		if _, err := joiner.DownloadContent(context.Background(), "gme1", nil); err != nil {
			t.Fatal(err)
		}
		change(w)
		plan, err := joiner.Prepare(context.Background(), "gme1")
		if !errors.Is(err, hostgame.ErrGameChanged) {
			t.Errorf("%s: err = %v", name, err)
		}
		if plan.Preview != nil {
			t.Errorf("%s: a changed game produced a command to approve", name)
		}
	}

	w := newWorld(t)
	w.remote.detail.Game.State, w.remote.detail.Game.Joinable, w.remote.detail.Game.Reason = "offline", false, "host_stopped"
	if _, err := w.joiner().Prepare(context.Background(), "gme1"); !errors.Is(err, hostgame.ErrGameChanged) ||
		w.remote.minted != 0 {
		t.Errorf("an ended game: %v, minted %d", err, w.remote.minted)
	}
}

// A link is a link, and a lookalike is not.
func TestOnlyTheSchemeAUBPublishedIsFollowed(t *testing.T) {
	for _, accepted := range []string{"autopigeon://join/tkt1", "  autopigeon://join/tkt1  ", "tkt1"} {
		id, err := aub.ParseJoinLink(accepted)
		if err != nil || id != "tkt1" {
			t.Fatalf("%q: id, err = %q, %v", accepted, id, err)
		}
	}
	for _, refused := range []string{
		"", "https://evil.example/join/tkt1", "autopigeon://host/tkt1", "aucom://join/tkt1",
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
	w := newWorld(t)
	w.remote.resolution.Reachability = "unverified"
	w.remote.resolution.Warnings = []string{"Nobody has checked that this address can be reached."}
	plan, err := w.joiner().Resolve(context.Background(), "autopigeon://join/tkt1")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0] != w.remote.resolution.Warnings[0] {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}
