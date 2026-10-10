package web

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// The sender of leak statuses (`NEW_307W1`).
//
// What it replaced remembered a status as said BEFORE posting it and ignored
// the answer: one refused `received` was never sent again, the page's watch
// could not repeat it, and the editor waited on a request this program was
// already holding. Here a status is said when AUB ACCEPTS it and not before.
//
// For each request it keeps the newest thing to say (the DESIRED status, with a
// generation that only grows) and the last thing AUB accepted. One worker sends
// whatever differs. An update that arrives while an older one is still failing
// replaces it — the editor is told where the work IS, never walked through
// where it was — and a full wake-up channel loses nothing, because the state to
// send lives in the table and not in the channel.
//
// It stays best effort: nothing waits on it, and the result bundle is still the
// only thing the editor imports.

const (
	// leakTrackedRequests bounds the table. It is a table of requests a person
	// clicked for, so this is far above anything a session produces.
	leakTrackedRequests = 256
	// leakTrackedLifetime is how long a request is worth telling the editor
	// about: AUB's own relay forgets a request after the same thirty minutes.
	leakTrackedLifetime = 30 * time.Minute
	// leakStatusTimeout bounds one POST.
	leakStatusTimeout = 8 * time.Second
	// leakStatusRefresh re-says the CURRENT status of a request that is still
	// being worked on. AUB keeps the relay in memory, so a restart there
	// forgets it; this puts it back without changing anything.
	leakStatusRefresh = 30 * time.Second
	// leakSessionRecheck is how often a request that cannot be sent under the
	// present session looks at the session again. It reads memory and the
	// config file; it sends nothing.
	leakSessionRecheck = 30 * time.Second
)

// leakStatusBackoff is the wait after each consecutive transient failure; the
// last one repeats.
var leakStatusBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second,
	8 * time.Second, 16 * time.Second, 30 * time.Second}

// Why a request is not being sent right now, as the page can be told.
const (
	leakRelayDelivered = "delivered"
	leakRelayPending   = "pending"
	leakRelayRetrying  = "retrying"
	leakRelaySignIn    = "sign_in_required"
	leakRelayAccount   = "other_account"
	leakRelayStopped   = "stopped"
)

// leakClock is the scheduler's view of time, so a test can move it.
type leakClock interface {
	Now() time.Time
	// After is a timer: the channel fires once after d, and stop releases it.
	After(d time.Duration) (fire <-chan time.Time, stop func())
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }
func (wallClock) After(d time.Duration) (<-chan time.Time, func()) {
	timer := time.NewTimer(d)
	return timer.C, func() { timer.Stop() }
}

// leakSession is who this program is signed in as at the moment of asking, and
// the way to post as them. Send is nil when nobody is signed in.
//
// It is ONE reading. Send posts as the session this value describes — Token —
// and never as whatever the sign-in has become since, so the session an
// attempt was admitted under, the one on the wire and the one its answer is
// recorded against are the same session.
//
// What that means for a POST already in the air when the session changes: it
// is the old session's, and stays so. It is not cancelled (only close cancels
// one) and it is not re-attributed. Accepted, the status is delivered and the
// request is the account's that sent it; refused, that token is remembered as
// refused and the request is looked at again under the session there is now.
type leakSession struct {
	Server string
	UserID string
	// Token is compared, to notice a new sign-in. It is never logged.
	Token string
	Send  func(ctx context.Context, requestID string, status aub.LeakStatus) error
}

type leakTracked struct {
	link aub.LeakTestLink
	// The account and server this request's statuses are for: the first one
	// AUB accepted a status from. Another account never inherits them.
	bound          bool
	server, userID string

	desired    aub.LeakStatus
	generation uint64
	acked      aub.LeakStatus
	ackedGen   uint64

	inFlight bool
	failures int
	// next is the earliest moment another attempt is allowed.
	next    time.Time
	expires time.Time
	// waiting is why nothing is being sent, when nothing is.
	waiting string
	// rejectedToken is the token AUB refused. Nothing is sent until the
	// session carries another one, or a person asks again.
	rejectedToken string
	// superseded: a newer request replaced this one before it was built, so
	// nobody is waiting on it and it is not refreshed.
	superseded bool
	lastError  string
}

// leakRelayView is what the page may be shown about one request's delivery.
type leakRelayView struct {
	State     string `json:"state"`
	Desired   string `json:"desired"`
	Delivered string `json:"delivered,omitempty"`
	Failures  int    `json:"failures,omitempty"`
	Error     string `json:"error,omitempty"`
}

type leakSender struct {
	clock   leakClock
	session func() leakSession
	logf    func(format string, args ...any)

	mu      sync.Mutex
	items   map[string]*leakTracked
	closed  bool
	started bool
	// manual leaves the worker unstarted, so a test drives step itself.
	manual bool
	wake   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func newLeakSender(clock leakClock, session func() leakSession, logf func(string, ...any)) *leakSender {
	if clock == nil {
		clock = wallClock{}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &leakSender{clock: clock, session: session, logf: logf, items: map[string]*leakTracked{},
		wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// leakStateActive reports whether somebody is still waiting on a request in
// this state, so its status is worth keeping alive on AUB.
func leakStateActive(state string) bool {
	switch state {
	case leakReceived, leakReviewing, leakBuilding, leakReturning, leakReturnFailed:
		return true
	}
	return false
}

func sameLeakStatus(a, b aub.LeakStatus) bool {
	return a.State == b.State && a.Stage == b.Stage && a.BuildID == b.BuildID
}

// report records what should be said about a request and returns at once.
// `opening` is a status that only ever starts a request's story ("received"):
// it never replaces anything already said or waiting to be.
func (q *leakSender) report(link aub.LeakTestLink, state, stage, buildID string, opening bool) {
	if q == nil || link.RequestID == "" {
		return
	}
	now := q.clock.Now()
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	item := q.items[link.RequestID]
	switch {
	case item != nil && (item.link.AssetID != link.AssetID || item.link.Revision != link.Revision ||
		item.link.ContentSHA256 != link.ContentSHA256):
		// The id is bound to the revision it first named, here as on AUB.
		q.mu.Unlock()
		return
	case item == nil:
		q.evict(now)
		item = &leakTracked{link: link, expires: now.Add(leakTrackedLifetime), waiting: leakRelayPending}
		q.items[link.RequestID] = item
		if opening {
			// One request is pending at a time: an older one that never
			// reached a build was replaced by this click.
			for id, other := range q.items {
				if id != link.RequestID && (other.desired.State == leakReceived || other.desired.State == leakReviewing) {
					other.superseded = true
				}
			}
		}
	case opening || sameLeakStatus(item.desired, aub.LeakStatus{State: state, Stage: stage, BuildID: buildID}):
		// Nothing new to say. A request that is waiting for a sign-in is
		// looked at again, because this call is the page noticing one.
		wake := item.waiting == leakRelaySignIn || item.waiting == leakRelayAccount
		if wake {
			item.next = now
		}
		q.mu.Unlock()
		if wake {
			q.kick()
		}
		return
	}
	item.generation++
	// The wire sequence orders updates on AUB across a restart of this
	// program, so it is wall-clock milliseconds, made strictly increasing.
	sequence := now.UnixMilli()
	if sequence <= item.desired.Sequence {
		sequence = item.desired.Sequence + 1
	}
	item.desired = aub.LeakStatus{MapID: link.AssetID, Revision: link.Revision, ContentSHA256: link.ContentSHA256,
		State: state, Stage: stage, BuildID: buildID, Sequence: sequence}
	if item.waiting != leakRelayStopped && item.failures == 0 {
		// A new thing to say does not shorten a backoff that is running: the
		// server is away for the newer status as much as for the older one.
		item.next = now
	}
	q.mu.Unlock()
	q.kick()
}

// evict makes room for one more request: the expired go first, then whichever
// has nothing left to say, then the oldest. Called with the lock held.
func (q *leakSender) evict(now time.Time) {
	for id, item := range q.items {
		if !now.Before(item.expires) {
			delete(q.items, id)
		}
	}
	for len(q.items) >= leakTrackedRequests {
		var victim string
		var best time.Time
		settled := false
		for id, item := range q.items {
			done := item.ackedGen == item.generation && !leakStateActive(item.acked.State)
			if victim == "" || (done && !settled) || (done == settled && item.expires.Before(best)) {
				victim, best, settled = id, item.expires, done
			}
		}
		delete(q.items, victim)
	}
}

// kick wakes the worker, starting it the first time. A wake-up that does not
// fit is not lost work: the worker reads the table, not the channel.
func (q *leakSender) kick() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	if !q.started && !q.manual {
		q.started = true
		go q.run()
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *leakSender) run() {
	defer close(q.done)
	for {
		wait, scheduled := q.step()
		if q.ctx.Err() != nil {
			return
		}
		if !scheduled {
			select {
			case <-q.ctx.Done():
				return
			case <-q.wake:
			}
			continue
		}
		if wait <= 0 {
			continue
		}
		fire, stop := q.clock.After(wait)
		select {
		case <-q.ctx.Done():
			stop()
			return
		case <-q.wake:
		case <-fire:
		}
		stop()
	}
}

// due is when this request next wants the worker, and whether it ever will.
// Called with the lock held.
func (item *leakTracked) due() (time.Time, bool) {
	if item.waiting == leakRelayStopped {
		return time.Time{}, false
	}
	if item.generation != item.ackedGen {
		return item.next, true
	}
	if leakStateActive(item.acked.State) && !item.superseded {
		return item.next, true
	}
	return time.Time{}, false
}

// step attempts every request that is due, one at a time, and answers how
// long until the next one is. `scheduled` is false when nothing is waiting.
func (q *leakSender) step() (wait time.Duration, scheduled bool) {
	for {
		now := q.clock.Now()
		q.mu.Lock()
		var id string
		var item *leakTracked
		var earliest time.Time
		for key, candidate := range q.items {
			if !now.Before(candidate.expires) {
				delete(q.items, key)
				continue
			}
			at, wanted := candidate.due()
			if wanted && (item == nil || at.Before(earliest)) {
				earliest, id, item = at, key, candidate
			}
		}
		q.mu.Unlock()
		if item == nil {
			return 0, false
		}
		if earliest.After(now) {
			return earliest.Sub(now), true
		}
		if !q.attempt(id, item) {
			return 0, false
		}
	}
}

// attempt sends one request's desired status, or works out why it may not be
// sent. It reports false when the sender is stopping.
func (q *leakSender) attempt(id string, item *leakTracked) bool {
	session := q.session()
	now := q.clock.Now()
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	hold := func(why string) bool {
		if item.waiting != why {
			q.logf("leak status: request %s is held (%s); nothing is sent until that changes", shortLeakID(id), why)
		}
		item.waiting, item.next = why, now.Add(leakSessionRecheck)
		q.mu.Unlock()
		return true
	}
	if session.Send == nil {
		return hold(leakRelaySignIn)
	}
	if item.bound && (item.server != session.Server || item.userID != session.UserID) {
		return hold(leakRelayAccount)
	}
	if item.rejectedToken != "" && item.rejectedToken == session.Token {
		return hold(leakRelaySignIn)
	}
	payload, generation := item.desired, item.generation
	if item.ackedGen != generation && item.ackedGen != 0 && sameLeakStatus(payload, item.acked) && item.waiting != leakRelayRetrying {
		// The work went somewhere and came back while a POST was in the air
		// (return_failed, returning, return_failed): AUB already says this.
		item.acked, item.ackedGen, item.waiting = payload, generation, leakRelayDelivered
		item.next = now.Add(leakStatusRefresh)
		q.mu.Unlock()
		return true
	}
	item.inFlight = true
	q.mu.Unlock()

	ctx, cancel := context.WithTimeout(q.ctx, leakStatusTimeout)
	err := session.Send(ctx, id, payload)
	cancel()

	// Whose answer this is. The POST belonged to `session`, the one it was
	// admitted under, and it was sent as that session whatever happened to the
	// sign-in meanwhile. A refusal is a fact about THAT session; whether it
	// holds the request depends on whether anybody still has it.
	var apiErr *aub.APIError
	refused := errors.As(err, &apiErr) && apiErr.Unauthorized()
	sessionMoved := false
	if refused && q.ctx.Err() == nil {
		current := q.session()
		sessionMoved = current.Token != session.Token || current.Server != session.Server
	}

	now = q.clock.Now()
	q.mu.Lock()
	defer q.mu.Unlock()
	item.inFlight = false
	if q.ctx.Err() != nil {
		return false
	}
	switch {
	case err == nil:
		if item.failures > 0 || item.waiting == leakRelaySignIn || item.waiting == leakRelayAccount {
			q.logf("leak status: request %s delivered `%s` after %d failed attempt(s)", shortLeakID(id), payload.State, item.failures)
		}
		// The request now belongs to the account AUB accepted it from. Not
		// before: a session AUB refused never owned anything.
		item.bound, item.server, item.userID = true, session.Server, session.UserID
		item.acked, item.ackedGen = payload, generation
		item.failures, item.rejectedToken, item.lastError, item.waiting = 0, "", "", leakRelayDelivered
		if item.generation != generation {
			item.waiting, item.next = leakRelayPending, now
		} else {
			item.next = now.Add(leakStatusRefresh)
		}
	case refused && sessionMoved:
		// AUB refused a session that has been replaced since the POST left:
		// someone signed in, or out, while it was unanswered. That token is
		// remembered as refused, and the request is due at once under whatever
		// the session is now — which the next attempt reads for itself, and
		// holds for a sign-in only if there is still nobody to send it as.
		// Parking it here made a person who had just signed in wait out a
		// recheck for a refusal that was no longer about them.
		q.logf("leak status: request %s was refused (HTTP %d) under a session that has since changed; it is looked at again now", shortLeakID(id), apiErr.StatusCode)
		item.rejectedToken, item.waiting, item.lastError = session.Token, leakRelayPending, ""
		item.next = now
	case refused:
		// AUB refused the session. Asking again with the same token would be
		// the same refusal every few seconds.
		q.logf("leak status: request %s was refused (HTTP %d); nothing more is sent until someone signs in", shortLeakID(id), apiErr.StatusCode)
		item.rejectedToken, item.waiting, item.lastError = session.Token, leakRelaySignIn, "the account server refused this session"
		item.next = now.Add(leakSessionRecheck)
	case errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 &&
		apiErr.StatusCode != 408 && apiErr.StatusCode != 425 && apiErr.StatusCode != 429:
		// AUB understood and said no: a malformed status, an id bound to other
		// bytes, a map this account cannot see. A retry would be told the same.
		q.logf("leak status: request %s was rejected (HTTP %d); it is not sent again", shortLeakID(id), apiErr.StatusCode)
		item.waiting, item.lastError = leakRelayStopped, apiErr.Error()
	default:
		step := item.failures
		if step >= len(leakStatusBackoff) {
			step = len(leakStatusBackoff) - 1
		}
		if item.failures == 0 {
			// One line when it starts failing, one when it recovers.
			q.logf("leak status: request %s could not be delivered (%s); retrying", shortLeakID(id), leakFailureClass(err))
		}
		item.failures++
		item.waiting, item.lastError = leakRelayRetrying, leakFailureClass(err)
		item.next = now.Add(leakStatusBackoff[step])
	}
	return true
}

// leakFailureClass names a failure without repeating a server's body or a URL.
func leakFailureClass(err error) string {
	var apiErr *aub.APIError
	switch {
	case errors.As(err, &apiErr):
		return "the account server answered HTTP " + strconv.Itoa(apiErr.StatusCode)
	case errors.Is(err, context.DeadlineExceeded):
		return "the account server did not answer in time"
	default:
		return "the account server could not be reached"
	}
}

func shortLeakID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

// view is one request's delivery, for the page.
func (q *leakSender) view(requestID string) *leakRelayView {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item := q.items[requestID]
	if item == nil {
		return nil
	}
	return &leakRelayView{State: item.waiting, Desired: item.desired.State, Delivered: item.acked.State,
		Failures: item.failures, Error: item.lastError}
}

// sessionChanged is called when somebody signs in or out here, so a held
// request is looked at now instead of at its next recheck.
func (q *leakSender) sessionChanged() {
	if q == nil {
		return
	}
	now := q.clock.Now()
	q.mu.Lock()
	held := false
	for _, item := range q.items {
		if item.waiting == leakRelaySignIn || item.waiting == leakRelayAccount {
			item.next, held = now, true
		}
	}
	q.mu.Unlock()
	if held {
		q.kick()
	}
}

// nothingPending is the page's watch saying which request, if any, this
// machine still holds for review. Every OTHER request that never reached a
// build is over — its ten-minute intent expired, or a newer click replaced it
// — so its "received" or "reviewing" stops being refreshed. Found live
// (`NEW_307W1`): a request nobody built was still "in review" on AUB twenty
// minutes after this machine had forgotten it. A build is not touched: it
// consumed its request and reports for itself.
func (q *leakSender) nothingPending(except string) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, item := range q.items {
		if id != except && (item.desired.State == leakReceived || item.desired.State == leakReviewing) {
			item.superseded = true
		}
	}
}

// retryNow is a person asking again for one request: whatever was holding its
// status back, short of AUB having rejected the request itself, is forgotten.
func (q *leakSender) retryNow(requestID string) {
	if q == nil {
		return
	}
	now := q.clock.Now()
	q.mu.Lock()
	item := q.items[requestID]
	if item == nil || item.waiting == leakRelayStopped {
		q.mu.Unlock()
		return
	}
	item.rejectedToken, item.failures, item.next = "", 0, now
	q.mu.Unlock()
	q.kick()
}

// close stops the worker and whatever POST it is in. Nothing is sent after it
// returns, and a later report is ignored.
func (q *leakSender) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	started, already := q.started, q.closed
	q.closed = true
	q.mu.Unlock()
	q.cancel()
	if started && !already {
		<-q.done
	}
}
