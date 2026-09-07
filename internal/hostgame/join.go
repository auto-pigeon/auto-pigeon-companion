package hostgame

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Resolver is the part of the AUB client a join needs.
type Resolver interface {
	ResolveJoinLink(ctx context.Context, ticketID string) (aub.HostedGameJoin, error)
}

// Fetcher is the part of internal/assetsync a join needs: fetch one revision of
// one asset, verified, into the local store.
//
// An interface so a join can be tested without a server AND so this package
// cannot acquire a second way of writing bytes to disk. Verification lives on the
// other side of it, once, which is what stops a digest check being something each
// call site remembers.
type Fetcher interface {
	Sync(ctx context.Context, assetType, assetID, revisionID string) (assetsync.Result, error)
}

// Objects is where a synced file's bytes are on this machine.
//
// Separate from [Fetcher] because they are two different objects in
// internal/assetsync — the syncer fetches, the store holds — and folding them
// into one interface here would make this package's dependency wider than what it
// uses.
type Objects interface {
	Object(digest string) (string, error)
}

// Runner is the part of the job service a join needs.
type Runner interface {
	Preview(request job.Request) (*job.Job, error)
	Submit(request job.Request) (*job.Job, error)
}

// Catalog is where an installed engine profile is looked up.
type Catalog interface {
	List() ([]job.CatalogEntry, error)
}

// Errors a join can produce. Each one is a different thing for the user to do,
// which is why they are values rather than one wrapped message.
var (
	// ErrMapUnreadable is AUB saying this account may not have the map. It is
	// reported BEFORE anything is downloaded, which is the whole reason the
	// resolution carries the answer.
	ErrMapUnreadable = errors.New("hostgame: this account cannot download the map this game is playing")

	// ErrNoEngine is no installed profile implementing the runtime the host
	// declared. "You do not have ironwail" is a much better message than a
	// connection that fails for reasons nobody can see.
	ErrNoEngine = errors.New("hostgame: no installed engine profile can join this game")

	// ErrDigestMismatch is the bytes not being the bytes. It cannot normally
	// happen — assetsync verifies every file against AUB's own declared digest —
	// and it is checked again here against the digest the RESOLUTION named,
	// because those are two statements by two routes and a join is where they have
	// to agree.
	ErrDigestMismatch = errors.New("hostgame: the map that was downloaded is not the one being played")

	// ErrNotApproved is a caller trying to launch without approving the command.
	ErrNotApproved = errors.New("hostgame: the command has not been approved")
)

// Joiner turns an opaque link into an approved command.
type Joiner struct {
	resolver Resolver
	fetcher  Fetcher
	objects  Objects
	catalog  Catalog
	runner   Runner
}

// NewJoiner builds one.
func NewJoiner(resolver Resolver, fetcher Fetcher, objects Objects, catalog Catalog,
	runner Runner,
) *Joiner {
	return &Joiner{resolver: resolver, fetcher: fetcher, objects: objects, catalog: catalog,
		runner: runner}
}

// Plan is a resolved, downloaded, checked join, waiting for approval.
//
// It exists as a value so that the four checks happen once, in order, and the
// thing a user approves is the thing that runs. A caller that could rebuild the
// request from the plan's parts would be able to approve one command and start
// another.
type Plan struct {
	// Join is what AUB said.
	Join aub.HostedGame
	// Resolution is the redeemed ticket, verbatim.
	Resolution aub.HostedGameJoin

	// Engine is the installed profile that will run, and Action is always
	// `join_server`.
	EngineProfileID string
	Action          string

	// MapPath is where the downloaded document is on this machine.
	MapPath string
	// MapDigest is the digest of what was downloaded, which was compared against
	// what the resolution declared.
	MapDigest string

	// Request is exactly what will be submitted, and Preview is the argv the job
	// service resolved from it. The preview is the SERVICE's, not a rendering
	// built here: a preview a user approved cannot differ from what starts.
	Request job.Request
	Preview *job.CommandPreview

	// Warnings are AUB's, carried through verbatim, plus this program's own about
	// the address. They are shown before the command, never instead of it.
	Warnings []string
}

// Resolve redeems a link and prepares everything a launch needs, without
// launching.
//
// The order is the point, and it is the order in the package comment: may this
// account have the map, are the bytes the ones being played, is there an engine,
// and only then a command to approve. Each step's failure is a different value,
// because each one is a different thing for the person to do.
func (j *Joiner) Resolve(ctx context.Context, link string) (Plan, error) {
	ticketID, err := aub.ParseJoinLink(link)
	if err != nil {
		return Plan{}, err
	}
	resolution, err := j.resolver.ResolveJoinLink(ctx, ticketID)
	if err != nil {
		return Plan{}, err
	}

	// 1. May this account have the map at all.
	if !resolution.Assets.Readable {
		reason := resolution.Assets.Reason
		if reason == "" {
			reason = "The host has not shared it with you."
		}

		return Plan{}, fmt.Errorf("%w: %s", ErrMapUnreadable, reason)
	}

	// 3. Is there an engine — asked BEFORE the download, because refusing after
	//    fetching nine megabytes is the same refusal arrived at more expensively.
	entry, err := j.engineFor(resolution)
	if err != nil {
		return Plan{}, err
	}

	// 2. Are the bytes the ones being played.
	result, err := j.fetcher.Sync(ctx, "map", resolution.MapID, "")
	if err != nil {
		return Plan{}, err
	}
	path, digest, err := j.mapFile(result)
	if err != nil {
		return Plan{}, err
	}
	// The resolution's digest and the revision's are two statements by two routes,
	// and a join is where they have to agree. assetsync has already verified the
	// bytes against the revision's; this is the OTHER one.
	if declared := normalizeDigest(resolution.MapDigest); declared != "" &&
		declared != normalizeDigest(result.Record.ContentSHA256) &&
		declared != normalizeDigest(digest) {
		return Plan{}, fmt.Errorf("%w: the host is playing %s and this deployment served %s",
			ErrDigestMismatch, short(declared), short(digest))
	}

	plan := Plan{
		Resolution:      resolution,
		EngineProfileID: entry.Profile.Metadata().ID,
		Action:          profile.ActionJoinServer,
		MapPath:         path,
		MapDigest:       digest,
		Warnings:        append([]string(nil), resolution.Warnings...),
	}
	plan.Request = job.Request{
		ProfileID: plan.EngineProfileID,
		ActionID:  profile.ActionJoinServer,
		Runtime: map[string]string{
			"server_host": resolution.EndpointHost,
			"server_port": strconv.Itoa(resolution.EndpointPort),
		},
		Label: "Join " + resolution.Title,
	}

	// 4. The command, resolved by the service that will run it.
	preview, err := j.runner.Preview(plan.Request)
	if err != nil {
		return Plan{}, err
	}
	plan.Preview = preview.Command

	return plan, nil
}

// Launch submits an approved plan.
//
// `approved` is a parameter rather than a field on the Plan so that approving is
// something a caller has to DO at the moment of launching, rather than a flag
// that could have been set when the plan was built. The refusal is by name.
func (j *Joiner) Launch(plan Plan, approved bool) (*job.Job, error) {
	if !approved {
		return nil, ErrNotApproved
	}
	if plan.Preview == nil {
		return nil, fmt.Errorf("hostgame: this plan was never previewed, so there is nothing to approve")
	}

	return j.runner.Submit(plan.Request)
}

// engineFor finds an installed engine profile that can join this game.
//
// Matched on the RUNTIME the host declared — `quakespasm`, `ironwail` — which is
// AUB's own engine vocabulary and the thing two installations can agree about. It
// is deliberately not matched on the host's `engine_profile_id`: that is a listing
// id on the host's deployment and says nothing about what this machine has
// installed, and requiring it would refuse a perfectly good local engine because
// the two people got their profiles from different places.
//
// A profile that does not declare `join_server` is not a candidate, whatever its
// runtime says: an engine which cannot connect to a server is not one this can be
// asked to.
func (j *Joiner) engineFor(resolution aub.HostedGameJoin) (job.CatalogEntry, error) {
	entries, err := j.catalog.List()
	if err != nil {
		return job.CatalogEntry{}, err
	}
	runtime := strings.ToLower(strings.TrimSpace(resolution.EngineRuntime))

	var installed []string
	var candidates []job.CatalogEntry
	for _, entry := range entries {
		engine, isEngine := entry.Profile.(*profile.EngineProfile)
		if !isEngine {
			continue
		}
		if _, offers := engine.ActionByID(profile.ActionJoinServer); !offers {
			continue
		}
		installed = append(installed, engine.Runtime)
		if strings.EqualFold(strings.TrimSpace(engine.Runtime), runtime) {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		sort.Strings(installed)

		return job.CatalogEntry{}, fmt.Errorf("%w: the host is running %q and this machine has %s",
			ErrNoEngine, resolution.EngineRuntime, describeInstalled(installed))
	}
	// Sorted by id so a machine with two profiles for one runtime picks the same
	// one every time. A join that chose differently between two runs would make
	// "it worked yesterday" unanswerable.
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].Profile.Metadata().ID < candidates[b].Profile.Metadata().ID
	})

	return candidates[0], nil
}

func describeInstalled(installed []string) string {
	if len(installed) == 0 {
		return "no engine profile that can join a server"
	}

	return strings.Join(unique(installed), ", ")
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}

	return out
}

// mapFile picks the document out of a synced revision.
//
// A map revision is one file, which is what makes this unambiguous; a revision
// that carried several would be a shape this program has never been served and
// guessing which one is the map would be exactly the wrong thing to do about it.
func (j *Joiner) mapFile(result assetsync.Result) (string, string, error) {
	files := result.Record.Files
	if len(files) != 1 {
		return "", "", fmt.Errorf(
			"hostgame: that map revision holds %d files and a map revision is one document", len(files))
	}
	path, err := j.objects.Object(files[0].SHA256)
	if err != nil {
		return "", "", err
	}

	return path, files[0].SHA256, nil
}

func normalizeDigest(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
}

func short(digest string) string {
	digest = strings.TrimPrefix(strings.ToLower(digest), "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}

	return digest
}
