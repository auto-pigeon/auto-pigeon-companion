package hostgame

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joincontent"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joinready"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Remote is the part of the AUB client a join needs.
type Remote interface {
	joinready.Remote
	MintJoinLink(ctx context.Context, gameID string) (aub.HostedGameTicket, error)
	ResolveJoinLink(ctx context.Context, ticketID string) (aub.HostedGameJoin, error)
	DownloadJoinContentFile(ctx context.Context, gameID, destination string) (*aub.Download, error)
}

// Runner is the part of the job service a join needs.
type Runner interface {
	Preview(request job.Request) (*job.Job, error)
	Submit(request job.Request) (*job.Job, error)
	List() ([]*job.Job, error)
}

// Errors a join can produce. Each one is a different thing for the user to do.
var (
	// ErrMapUnreadable is AUB saying this account may not have the map, and so
	// may not have its compiled files either.
	ErrMapUnreadable = errors.New("hostgame: this account cannot download the map this game is playing")

	// ErrNoEngine is no engine profile implementing the runtime the host declared.
	ErrNoEngine = errors.New("hostgame: no installed engine profile can join this game")

	// ErrDigestMismatch is the files offered not being the package the game names.
	ErrDigestMismatch = errors.New("hostgame: the map files offered are not the ones this game names")

	// ErrNotReady is a join asked for before every prerequisite is done. It
	// carries the readiness report's next step in its message.
	ErrNotReady = errors.New("hostgame: this computer is not set up to join this game yet")

	// ErrGameChanged is the game moving between the setup and the fresh ticket:
	// ended, rebuilt, restarted on another revision or another address.
	ErrGameChanged = errors.New("hostgame: the game changed while you were setting up")

	// ErrNotApproved is a caller trying to launch without approving the command.
	ErrNotApproved = errors.New("hostgame: the command has not been approved")

	// ErrPlanExpired is an approval arriving for a review that is too old to
	// still describe the game.
	ErrPlanExpired = errors.New("hostgame: that review is out of date; review the join again")

	// ErrAlreadyJoining is a second launch of a join that is already running. The
	// running job is returned beside it: a double click is one game, not two.
	ErrAlreadyJoining = errors.New("hostgame: this game is already starting or running")

	// ErrAlreadyDownloading is a second download of a package in progress.
	ErrAlreadyDownloading = errors.New("hostgame: these map files are already downloading")
)

// PlanLifetime bounds how long a reviewed plan may be approved. The same two
// minutes AUB gives a join ticket: a review older than the ticket it was built
// from describes a game that may no longer be the one running.
const PlanLifetime = 2 * time.Minute

// Joiner turns a game — or a link to one — into an approved, supervised command.
type Joiner struct {
	remote Remote
	local  joinready.Local
	runner Runner
	now    func() time.Time

	mu          sync.Mutex
	downloading map[string]bool
}

// NewJoiner builds one. `local.Runner` and `local.Downloading` are filled in from
// the joiner itself, so the readiness a joiner reports is always computed by the
// same service that will run the command.
func NewJoiner(remote Remote, local joinready.Local, runner Runner) *Joiner {
	j := &Joiner{remote: remote, runner: runner, now: time.Now, downloading: map[string]bool{}}
	local.Runner = runner
	local.Downloading = j.isDownloading
	j.local = local

	return j
}

func (j *Joiner) isDownloading(packageSHA256 string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.downloading[strings.ToLower(packageSHA256)]
}

// Plan is a prepared join, waiting for approval.
//
// It exists as a value so the thing a user approves is the thing that runs: the
// request inside it was previewed by the job service, from a ticket redeemed a
// moment ago, against a game that was checked not to have changed.
type Plan struct {
	// Resolution is the redeemed ticket, verbatim.
	Resolution aub.HostedGameJoin `json:"resolution"`
	// Readiness is the report the plan was prepared from.
	Readiness joinready.Report `json:"readiness"`

	EngineProfileID string `json:"engine_profile_id"`
	Action          string `json:"action"`
	// PackageSHA256 is the join content the plan stages, when there is any.
	PackageSHA256 string `json:"package_sha256,omitempty"`

	Request job.Request         `json:"request"`
	Preview *job.CommandPreview `json:"preview,omitempty"`

	Warnings   []string  `json:"warnings,omitempty"`
	PreparedAt time.Time `json:"prepared_at"`
}

// Assess is the readiness report for one game.
func (j *Joiner) Assess(ctx context.Context, gameID string) (joinready.Report, error) {
	return joinready.Assess(ctx, j.remote, j.local, gameID)
}

// DownloadContent fetches, verifies and stages a game's join content, and
// answers with the report afterwards.
//
// It never mints or redeems a ticket: setup may take minutes, and a two-minute
// one-use capability spent at its start would be dead by its end. The package
// is reached through the lease, which AUB authorizes on every request.
func (j *Joiner) DownloadContent(ctx context.Context, gameID string, progress func(done, total int)) (
	joinready.Report, error,
) {
	report, err := j.Assess(ctx, gameID)
	if err != nil {
		return report, err
	}
	step := stepOf(report, joinready.StepJoinContent)
	switch step.State {
	case joinready.ContentStaged, joinready.ContentNotRequired, joinready.ContentUndeclared:
		return report, nil
	case joinready.ContentUnreadable:
		return report, fmt.Errorf("%w: %s", ErrMapUnreadable, step.Detail)
	case joinready.ContentDownloading:
		return report, ErrAlreadyDownloading
	case joinready.ContentRequired, joinready.ContentVerified:
	default:
		return report, fmt.Errorf("%w: %s", ErrNotReady, step.Detail)
	}
	if j.local.Stager == nil || len(report.Files) == 0 {
		return report, fmt.Errorf("%w: %s", ErrNotReady, step.Detail)
	}
	digest := strings.ToLower(report.Game.JoinContent.PackageSHA256)
	j.mu.Lock()
	if j.downloading[digest] {
		j.mu.Unlock()

		return report, ErrAlreadyDownloading
	}
	j.downloading[digest] = true
	j.mu.Unlock()
	defer func() {
		j.mu.Lock()
		delete(j.downloading, digest)
		j.mu.Unlock()
	}()

	fetch := func(ctx context.Context, file aub.JoinContentFile) (io.ReadCloser, error) {
		download, err := j.remote.DownloadJoinContentFile(ctx, gameID, file.Destination)
		if err != nil {
			return nil, err
		}
		if download.ETag != "" && !strings.EqualFold(download.ETag, file.SHA256) {
			download.Body.Close()

			return nil, fmt.Errorf("%w: %s was served as %s", ErrDigestMismatch, file.Destination,
				joincontent.Short(download.ETag))
		}

		return download.Body, nil
	}
	if err = j.local.Stager.Download(ctx, report.Files, fetch, progress); err != nil {
		return report, err
	}
	if _, err = j.local.Stager.Stage(digest, report.Files); err != nil {
		return report, err
	}

	return j.Assess(ctx, gameID)
}

// Prepare mints a FRESH ticket for a game this machine is ready to join, redeems
// it, checks the game did not change since the readiness it was prepared from,
// and asks the job service for the exact command.
//
// This is the only place a ticket is spent on a join started from the Companion,
// and it is spent immediately before the review — never at the start of a setup.
func (j *Joiner) Prepare(ctx context.Context, gameID string) (Plan, error) {
	report, err := j.Assess(ctx, gameID)
	if err != nil {
		return Plan{}, err
	}
	if err = readyOrError(report); err != nil {
		return Plan{Readiness: report}, err
	}
	ticket, err := j.remote.MintJoinLink(ctx, gameID)
	if err != nil {
		return Plan{Readiness: report}, err
	}
	resolution, err := j.remote.ResolveJoinLink(ctx, ticket.ID)
	if err != nil {
		return Plan{Readiness: report}, err
	}

	return j.plan(report, resolution)
}

// Resolve redeems a link somebody was given and prepares the join — the CLI's
// `game join <link>`. The ticket is spent here, once; the report is then read for
// the game it named.
func (j *Joiner) Resolve(ctx context.Context, link string) (Plan, error) {
	ticketID, err := aub.ParseJoinLink(link)
	if err != nil {
		return Plan{}, err
	}
	resolution, err := j.remote.ResolveJoinLink(ctx, ticketID)
	if err != nil {
		return Plan{}, err
	}
	if !resolution.Assets.Readable {
		reason := resolution.Assets.Reason
		if reason == "" {
			reason = "The host has not shared it with you."
		}

		return Plan{Resolution: resolution}, fmt.Errorf("%w: %s", ErrMapUnreadable, reason)
	}
	report, err := j.Assess(ctx, resolution.GameID)
	if err != nil {
		return Plan{Resolution: resolution}, err
	}
	if err = readyOrError(report); err != nil {
		return Plan{Resolution: resolution, Readiness: report}, err
	}

	return j.plan(report, resolution)
}

// plan revalidates a resolution against the report and previews the command.
func (j *Joiner) plan(report joinready.Report, resolution aub.HostedGameJoin) (Plan, error) {
	game := report.Game
	changed := func(what string) (Plan, error) {
		return Plan{Resolution: resolution, Readiness: report}, fmt.Errorf("%w: %s", ErrGameChanged, what)
	}
	switch {
	case resolution.GameID != game.ID:
		return changed("the link is for a different game")
	case resolution.MapRevision != game.MapRevision:
		return changed(fmt.Sprintf("the host is now playing revision %d of the map, not %d",
			resolution.MapRevision, game.MapRevision))
	case !strings.EqualFold(resolution.JoinContent.PackageSHA256, game.JoinContent.PackageSHA256) ||
		!strings.EqualFold(resolution.PackageSHA, game.PackageSHA):
		return changed("the host rebuilt the map files for this game")
	case game.Endpoint != "" && joinready.EndpointKey(resolution.Endpoint) != joinready.EndpointKey(game.Endpoint):
		return changed("the host's server moved to another address")
	}
	request, err := report.Command(resolution.Endpoint, j.local)
	if err != nil {
		return Plan{Resolution: resolution, Readiness: report}, err
	}
	preview, err := j.runner.Preview(request)
	if err != nil {
		return Plan{Resolution: resolution, Readiness: report}, err
	}
	if preview.Error != "" || preview.Command == nil {
		return Plan{Resolution: resolution, Readiness: report},
			fmt.Errorf("%w: the command could not be prepared: %s", ErrNotReady, preview.Error)
	}

	return Plan{
		Resolution:      resolution,
		Readiness:       report,
		EngineProfileID: request.ProfileID,
		Action:          profile.ActionJoinServer,
		PackageSHA256:   game.JoinContent.PackageSHA256,
		Request:         request,
		Preview:         preview.Command,
		Warnings:        append([]string(nil), resolution.Warnings...),
		PreparedAt:      j.now().UTC(),
	}, nil
}

// Launch submits an approved plan — once.
//
// `approved` is a parameter rather than a field on the Plan so that approving is
// something a caller has to DO at the moment of launching. A plan older than
// PlanLifetime is refused. A join already queued or running for the same engine
// and the same server — a double click, a second tab, a second terminal — returns
// that job with ErrAlreadyJoining instead of starting another engine.
func (j *Joiner) Launch(plan Plan, approved bool) (*job.Job, error) {
	if !approved {
		return nil, ErrNotApproved
	}
	if plan.Preview == nil {
		return nil, fmt.Errorf("hostgame: this plan was never previewed, so there is nothing to approve")
	}
	if !plan.PreparedAt.IsZero() && j.now().Sub(plan.PreparedAt) > PlanLifetime {
		return nil, ErrPlanExpired
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if existing := j.activeJoin(plan.Request); existing != nil {
		return existing, ErrAlreadyJoining
	}

	return j.runner.Submit(plan.Request)
}

func (j *Joiner) activeJoin(request job.Request) *job.Job {
	jobs, err := j.runner.List()
	if err != nil {
		return nil
	}
	for _, candidate := range jobs {
		if candidate == nil || !candidate.State.Active() || candidate.ActionID != profile.ActionJoinServer {
			continue
		}
		if candidate.ProfileID != request.ProfileID {
			continue
		}
		runtime := candidate.Request.Runtime
		if runtime[profile.RuntimeServerHost] == request.Runtime[profile.RuntimeServerHost] &&
			runtime[profile.RuntimeServerPort] == request.Runtime[profile.RuntimeServerPort] {
			return candidate
		}
	}

	return nil
}

// readyOrError maps a report that is not ready onto the error a caller acts on.
func readyOrError(report joinready.Report) error {
	if report.State == joinready.StateReadyForReview {
		return nil
	}
	step := stepOf(report, report.Next)
	switch {
	case report.State == joinready.StateSignInRequired:
		return fmt.Errorf("%w: %s", ErrNotReady, step.Detail)
	case step.ID == joinready.StepGame:
		return fmt.Errorf("%w: %s", ErrGameChanged, step.Title+". "+step.Detail)
	case step.ID == joinready.StepEngineProfile && step.State == joinready.EngineMissing:
		return fmt.Errorf("%w: the host is running %q. %s", ErrNoEngine, report.Game.EngineRuntime, step.Detail)
	case step.ID == joinready.StepJoinContent && step.State == joinready.ContentUnreadable:
		return fmt.Errorf("%w: %s", ErrMapUnreadable, step.Detail)
	}

	return fmt.Errorf("%w: %s — %s", ErrNotReady, step.Title, step.Detail)
}

func stepOf(report joinready.Report, id string) joinready.Step {
	for _, step := range report.Steps {
		if step.ID == id {
			return step
		}
	}

	return joinready.Step{ID: id}
}
