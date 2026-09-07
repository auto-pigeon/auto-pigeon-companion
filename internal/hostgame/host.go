package hostgame

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
)

// Backend is the part of the AUB client this package needs.
//
// An interface rather than *aub.Client so the lifecycle can be exercised against
// a fake that returns a refusal on the third beat, which is the case that
// matters and the one a live server will not produce on demand. *aub.Client
// satisfies it.
type Backend interface {
	PreviewHostedGame(ctx context.Context, registration aub.HostedGameRegistration) (
		aub.HostedGamePreview, error)
	RegisterHostedGame(ctx context.Context, registration aub.HostedGameRegistration) (
		aub.HostedGameResult, error)
	HeartbeatHostedGame(ctx context.Context, gameID string, body aub.HeartbeatBody) (
		aub.HostedGameResult, error)
	StopHostedGame(ctx context.Context, gameID, reason string) (aub.HostedGameResult, error)
}

// Jobs is the part of the job service this package needs: the state of one
// supervised process.
type Jobs interface {
	Get(id string) (*job.Job, error)
}

// Errors this package returns.
var (
	// ErrNotConfirmed is a registration whose preview nobody read. It is a value
	// rather than a message so the two surfaces that can produce it — the CLI and
	// the local API — refuse identically.
	ErrNotConfirmed = errors.New(
		"hostgame: a game is listed after its host has seen what the listing will say")

	// ErrNotHosting is a job that is not a server. Advertising a `play_map` as a
	// game people can join would put an address in a listing that nothing is
	// listening on.
	ErrNotHosting = errors.New("hostgame: that job is not hosting anything")

	// ErrAlreadyAdvertising is a second Start for a lease this Advertiser is
	// already beating for.
	ErrAlreadyAdvertising = errors.New("hostgame: this game is already being advertised")
)

// Advertiser keeps one machine's advertisements alive.
//
// One per Companion process. It holds a beat loop per advertised game and
// nothing else: the lease's content is AUB's, the process's state is the job
// service's, and this is the thing that carries one to the other.
type Advertiser struct {
	backend Backend
	jobs    Jobs

	// now and sleep are the clock, so the loop is testable without waiting. A
	// heartbeat test that actually slept thirty seconds is a test nobody runs.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu     sync.Mutex
	active map[string]*advertisement
}

// New builds an Advertiser.
func New(backend Backend, jobs Jobs) *Advertiser {
	return &Advertiser{
		backend: backend,
		jobs:    jobs,
		now:     func() time.Time { return time.Now().UTC() },
		after:   time.After,
		active:  map[string]*advertisement{},
	}
}

// advertisement is one live lease and the loop keeping it alive.
type advertisement struct {
	gameID string
	jobID  string
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	game    aub.HostedGame
	stopped bool
	reason  string
	failure error
}

// Preview asks AUB what a registration would publish, without writing anything.
//
// It is the whole of the review step, and it is a round trip to AUB rather than a
// rendering built here on purpose: the fields a person approves have to be the
// fields that will be published, and a second implementation of that computation
// in this program would be a second answer waiting to disagree with the first.
func (a *Advertiser) Preview(ctx context.Context, registration aub.HostedGameRegistration) (
	aub.HostedGamePreview, error,
) {
	return a.backend.PreviewHostedGame(ctx, registration)
}

// StartOptions is what an advertisement needs beyond the registration.
type StartOptions struct {
	// JobID is the supervised process this lease describes. Required: a lease
	// with no process behind it is an advertisement nothing can end.
	JobID string
	// PollInterval overrides how often the job's state is re-read. Zero means the
	// heartbeat cadence AUB published, which is the right answer: there is nothing
	// to be gained by noticing a crash sooner than the next beat would report it.
	PollInterval time.Duration
	// Occupancy, when set, is asked for a fresh count before each beat. A host
	// whose engine cannot report one leaves it nil, and the beat then says nothing
	// about players rather than sending zeros.
	Occupancy func() (current, max int, ok bool)
}

// Start registers a game and begins beating for it.
//
// The registration is REFUSED here when it is unconfirmed, before any request is
// made. AUB refuses it too and its refusal is the authority; this one exists so
// that a caller who skipped the review gets a message naming the review rather
// than an HTTP status naming a field.
func (a *Advertiser) Start(ctx context.Context, registration aub.HostedGameRegistration,
	options StartOptions,
) (aub.HostedGame, error) {
	if !registration.ConfirmExposure {
		return aub.HostedGame{}, ErrNotConfirmed
	}
	if options.JobID == "" {
		return aub.HostedGame{}, fmt.Errorf(
			"hostgame: an advertisement names the job serving it, so that stopping the server ends the listing")
	}
	running, err := a.jobs.Get(options.JobID)
	if err != nil {
		return aub.HostedGame{}, err
	}
	if !running.Hosting() {
		return aub.HostedGame{}, ErrNotHosting
	}

	result, err := a.backend.RegisterHostedGame(ctx, registration)
	if err != nil {
		return aub.HostedGame{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.active[result.Game.ID]; exists {
		// A reclaim of a lease this process is already beating for. The lease is
		// updated on the server and the loop that owns it goes on running; starting
		// a second loop for one lease would double the beat rate and, worse, make
		// two goroutines race to report the ending.
		return result.Game, ErrAlreadyAdvertising
	}

	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &advertisement{
		gameID: result.Game.ID,
		jobID:  options.JobID,
		cancel: cancel,
		done:   make(chan struct{}),
		game:   result.Game,
	}
	a.active[result.Game.ID] = entry
	go a.beat(loopCtx, entry, result.Game.HeartbeatInterval(), options)

	return result.Game, nil
}

// beat is the loop. One per advertised game.
//
// It ends the lease itself when the job ends, with the reason the job actually
// ended for. That is the whole of *stop, crash and sign-out converge*: the three
// are different job states here and three different words at AUB, and none of
// them is `heartbeat_missed` — that one is what AUB concludes when this loop was
// not given the chance to say anything, and a client that could send it would be
// able to write a history that did not happen.
func (a *Advertiser) beat(ctx context.Context, entry *advertisement, interval time.Duration,
	options StartOptions,
) {
	defer close(entry.done)
	defer func() {
		a.mu.Lock()
		delete(a.active, entry.gameID)
		a.mu.Unlock()
	}()

	poll := options.PollInterval
	if poll <= 0 {
		poll = interval
	}

	for {
		// The job's own state decides whether there is anything left to beat for,
		// and it is checked BEFORE the beat rather than after: a process that died
		// while this loop was asleep must not have its lease renewed on the way to
		// noticing.
		if reason, ended := a.jobEnded(entry); ended {
			a.end(ctx, entry, reason)

			return
		}

		select {
		case <-ctx.Done():
			// The Companion is shutting down or the caller asked to stop. Either way
			// somebody is signing off, and saying so is better than going silent and
			// leaving AUB to conclude it two minutes later.
			a.end(context.WithoutCancel(ctx), entry, aub.ReasonOwnerSignedOut)

			return
		case <-a.after(poll):
		}

		if reason, ended := a.jobEnded(entry); ended {
			a.end(ctx, entry, reason)

			return
		}
		if err := a.sendBeat(ctx, entry, options); err != nil {
			// A refused beat is the end of this advertisement, and it is not an
			// error to escalate: AUB refuses a beat for a lease that has lapsed, and
			// a lapsed lease is precisely the state where continuing to beat would
			// achieve nothing. The reason is recorded so a caller can say what
			// happened.
			entry.mu.Lock()
			entry.failure = err
			entry.mu.Unlock()

			return
		}
	}
}

// jobEnded reads the supervised job and translates a terminal state into AUB's
// vocabulary.
func (a *Advertiser) jobEnded(entry *advertisement) (string, bool) {
	running, err := a.jobs.Get(entry.jobID)
	if err != nil {
		// The job record is gone. Something removed it under this loop, and the
		// honest thing to report is that the process this lease described is no
		// longer one this program can see.
		return aub.ReasonHostCrashed, true
	}
	switch running.State {
	case job.Cancelled, job.Cancelling:
		return aub.ReasonHostStopped, true
	case job.Succeeded:
		// A server that exited zero was told to stop by somebody: an engine's own
		// `quit`, a clean shutdown. It is a stop, not a crash.
		return aub.ReasonHostStopped, true
	case job.Failed:
		return aub.ReasonHostCrashed, true
	case job.Interrupted:
		// The Companion was killed while the process was running, and the process
		// went with it. Nobody chose this, which is what distinguishes it from a
		// stop, and it is a crash from the point of view of everybody who was
		// connected.
		return aub.ReasonHostCrashed, true
	}

	return "", false
}

func (a *Advertiser) sendBeat(ctx context.Context, entry *advertisement, options StartOptions) error {
	body := aub.HeartbeatBody{}
	if options.Occupancy != nil {
		if current, max, ok := options.Occupancy(); ok {
			observable := true
			body.PlayersCurrent = &current
			body.PlayersMax = &max
			body.PlayersObservable = &observable
		}
	}
	result, err := a.backend.HeartbeatHostedGame(ctx, entry.gameID, body)
	if err != nil {
		return err
	}
	entry.mu.Lock()
	entry.game = result.Game
	entry.mu.Unlock()

	return nil
}

func (a *Advertiser) end(ctx context.Context, entry *advertisement, reason string) {
	entry.mu.Lock()
	already := entry.stopped
	entry.stopped = true
	entry.reason = reason
	entry.mu.Unlock()
	if already {
		return
	}
	result, err := a.backend.StopHostedGame(ctx, entry.gameID, reason)
	if err != nil {
		entry.mu.Lock()
		entry.failure = err
		entry.mu.Unlock()

		return
	}
	entry.mu.Lock()
	entry.game = result.Game
	entry.mu.Unlock()
}

// Stop ends an advertisement this process owns, with a reason.
//
// Idempotent: a second Stop is the same answer, because a Companion that crashed
// mid-shutdown and retries must not be told it is wrong — the rule AUB's own stop
// route follows, kept on this side too so the two agree.
func (a *Advertiser) Stop(ctx context.Context, gameID, reason string) error {
	if reason == "" {
		reason = aub.ReasonHostStopped
	}
	a.mu.Lock()
	entry, live := a.active[gameID]
	a.mu.Unlock()
	if !live {
		_, err := a.backend.StopHostedGame(ctx, gameID, reason)

		return err
	}
	a.end(ctx, entry, reason)
	entry.cancel()
	<-entry.done

	return nil
}

// Close ends every advertisement this process is keeping alive.
//
// It is what makes signing out or quitting converge rather than leaving somebody
// else's listing showing a game that stopped. What it cannot cover is a kill —
// and that is exactly the case AUB's own expiry is for, which is why this program
// never needs to send `heartbeat_missed` and must not be able to.
func (a *Advertiser) Close(ctx context.Context, reason string) {
	if reason == "" {
		reason = aub.ReasonOwnerSignedOut
	}
	a.mu.Lock()
	ids := make([]string, 0, len(a.active))
	for id := range a.active {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	for _, id := range ids {
		_ = a.Stop(ctx, id, reason)
	}
}

// Advertised is one live advertisement, as a caller reads it.
type Advertised struct {
	Game    aub.HostedGame
	JobID   string
	Stopped bool
	Reason  string
	Failure error
}

// Active is what this process is currently advertising.
func (a *Advertiser) Active() []Advertised {
	a.mu.Lock()
	entries := make([]*advertisement, 0, len(a.active))
	for _, entry := range a.active {
		entries = append(entries, entry)
	}
	a.mu.Unlock()

	out := make([]Advertised, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		out = append(out, Advertised{
			Game:    entry.game,
			JobID:   entry.jobID,
			Stopped: entry.stopped,
			Reason:  entry.reason,
			Failure: entry.failure,
		})
		entry.mu.Unlock()
	}

	return out
}

// Wait blocks until an advertisement's loop has ended. For tests and for a
// caller shutting down in order.
func (a *Advertiser) Wait(gameID string) {
	a.mu.Lock()
	entry, live := a.active[gameID]
	a.mu.Unlock()
	if live {
		<-entry.done
	}
}
