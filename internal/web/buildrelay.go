package web

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
)

// What the editor is told about its normal build request, through AUB.
//
// The editor tab that pressed Build cannot ask this program anything — a page
// may not call the reader's loopback — so this program posts the request's
// state to the account's own server and the editor reads it there. It is
// leaksender.go's discipline for a different request, deliberately smaller:
//
//   - A status is said when AUB ACCEPTS it, not before. Each request keeps the
//     newest thing to say and the last thing accepted; one worker sends what
//     differs, with the leak relay's backoff. A newer status replaces one
//     still failing: the editor is told where the work is.
//   - The sequence only grows (wall-clock milliseconds, made strictly
//     increasing, and above anything AUB already holds), so a late POST cannot
//     put the editor back a step.
//   - A terminal status — completed, failed, cancelled — is final, here as on
//     AUB: nothing said after it is sent.
//   - Best effort. Nothing waits on it and a failure blocks nothing local: a
//     build the person started runs whether or not the editor hears of it.
//
// It also remembers, per request, what this program has learned while it is
// alive: what AUB said the request is about (resolved), and the run a start
// made of it. Neither is written to disk: the intent file is the request's
// only durable trace, and a restart resolves it again.

const (
	// buildTrackedRequests bounds the table: requests a person clicked for.
	buildTrackedRequests = 256
	// buildTrackedLifetime is how long a request is remembered after its last
	// status: long enough for a long build after a half-hour wait.
	buildTrackedLifetime = 3 * time.Hour
	// buildStatusTimeout bounds one POST.
	buildStatusTimeout = 8 * time.Second
	// buildSessionRecheck is how often a request held for a sign-in looks at
	// the session again. It reads memory; it sends nothing.
	buildSessionRecheck = 30 * time.Second
)

// buildRunPoll is how often a started request's run is read back from its
// record. A variable so a test can shorten it.
var buildRunPoll = 500 * time.Millisecond

// The stages a refusal or an ending is said with. Tokens, not sentences: the
// editor has its own words for each.
const (
	buildStageIntentMismatch   = "intent_mismatch"
	buildStageRevisionMismatch = "revision_mismatch"
	buildStageNoSource         = "no_apmap_source"
	buildStageProfileMismatch  = "profile_mismatch"
	buildStageUnsupported      = "unsupported_profile"
	buildStageDismissed        = "dismissed"
	buildStageInterrupted      = "interrupted"
)

// buildRequestFacts is what AUB said a request is about, checked against the
// Companion's own read of the revision and of its bytes.
type buildRequestFacts struct {
	Link          aub.BuildRequestLink
	AssetID       string
	Revision      int
	RevisionID    string
	ContentSHA256 string
	SourceFile    string
	Name          string
	Game          string
	ExpiresAt     time.Time
}

// buildSession is who this program is signed in as at the moment of asking,
// read ONCE, and the way to post as exactly that session (see leakSession).
type buildSession struct {
	Server string
	UserID string
	Token  string
	Send   func(ctx context.Context, requestID string, status aub.BuildRequestStatus) (aub.BuildRequestStatusAnswer, error)
}

type buildTracked struct {
	link     aub.BuildRequestLink
	resolved *buildRequestFacts
	// runID is the run a start made of this request. Set once.
	runID string
	// received: `received` has been asked for, once.
	received bool

	// The account a status was first accepted from. Another never inherits it.
	bound          bool
	server, userID string

	desired    aub.BuildRequestStatus
	generation uint64
	acked      aub.BuildRequestStatus
	ackedGen   uint64
	// floor is the highest sequence AUB is known to hold.
	floor int64
	// final: a terminal status is desired, and nothing replaces it.
	final bool
	// over: AUB holds nothing more for this request — a terminal status was
	// accepted, or AUB answered that it had moved past what was sent.
	over bool

	failures      int
	next          time.Time
	expires       time.Time
	waiting       string
	rejectedToken string
	lastError     string
}

type buildRelay struct {
	clock   leakClock
	session func() buildSession
	logf    func(format string, args ...any)

	// starting makes "is this request still pending, and which run did it
	// start" and the start itself one step: two presses for one request start
	// at most one run.
	starting sync.Mutex

	mu      sync.Mutex
	items   map[string]*buildTracked
	closed  bool
	started bool
	// manual leaves the worker unstarted, so a test drives step itself.
	manual bool
	wake   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func newBuildRelay(clock leakClock, session func() buildSession, logf func(string, ...any)) *buildRelay {
	if clock == nil {
		clock = wallClock{}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &buildRelay{clock: clock, session: session, logf: logf, items: map[string]*buildTracked{},
		wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// buildRelaySession is the session statuses are posted as right now.
func (s *Server) buildRelaySession() buildSession {
	s.adoptSessionFromDisk()
	s.mu.RLock()
	client, session := s.client, s.settings.Session
	s.mu.RUnlock()
	if client == nil {
		return buildSession{}
	}
	token := client.Token()
	if token == "" || aub.TokenExpired(token, time.Now()) {
		return buildSession{}
	}
	return buildSession{Server: client.BaseURL(), UserID: session.UserID, Token: token,
		Send: func(ctx context.Context, requestID string, status aub.BuildRequestStatus) (aub.BuildRequestStatusAnswer, error) {
			return client.PublishBuildRequestStatusAs(ctx, token, requestID, status)
		}}
}

// item is a request's entry, made when it is first seen. Lock held.
func (q *buildRelay) item(link aub.BuildRequestLink, now time.Time) *buildTracked {
	item := q.items[link.RequestID]
	if item != nil && item.link != link {
		// An id is bound to the intent it first named.
		return nil
	}
	if item == nil {
		q.evict(now)
		item = &buildTracked{link: link, waiting: leakRelayPending}
		q.items[link.RequestID] = item
	}
	item.expires = now.Add(buildTrackedLifetime)
	return item
}

// evict makes room for one more request: the expired first, then the oldest.
// Lock held.
func (q *buildRelay) evict(now time.Time) {
	for id, item := range q.items {
		if !now.Before(item.expires) {
			delete(q.items, id)
		}
	}
	if len(q.items) < buildTrackedRequests {
		return
	}
	ids := make([]string, 0, len(q.items))
	for id := range q.items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return q.items[ids[i]].expires.Before(q.items[ids[j]].expires) })
	for _, id := range ids[:len(q.items)-buildTrackedRequests+1] {
		delete(q.items, id)
	}
}

// resolvedAs records what a request was resolved to, the sequence AUB holds
// for it and the state AUB holds: a request AUB already has past `pending` —
// this program said `received` before a restart — is not put back a step.
func (q *buildRelay) resolvedAs(facts buildRequestFacts, sequence int64, state string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if item := q.item(facts.Link, q.clock.Now()); item != nil {
		copy := facts
		item.resolved = &copy
		if sequence > item.floor {
			item.floor = sequence
		}
		if state != aub.BuildRequestPending {
			item.received = true
		}
	}
}

// resolved is what a request was last resolved to, or nil.
func (q *buildRelay) resolved(requestID string) *buildRequestFacts {
	q.mu.Lock()
	defer q.mu.Unlock()
	if item := q.items[requestID]; item != nil && item.resolved != nil {
		copy := *item.resolved
		return &copy
	}
	return nil
}

// startedRun is the run a start made of this request, or "".
func (q *buildRelay) startedRun(requestID string) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if item := q.items[requestID]; item != nil {
		return item.runID
	}
	return ""
}

func (q *buildRelay) startedAs(link aub.BuildRequestLink, runID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if item := q.item(link, q.clock.Now()); item != nil {
		item.runID = runID
	}
}

// received says `received` for a request, the first time only.
func (q *buildRelay) receivedOnce(link aub.BuildRequestLink) {
	q.mu.Lock()
	item := q.item(link, q.clock.Now())
	first := item != nil && !item.received
	if first {
		item.received = true
	}
	q.mu.Unlock()
	if first {
		q.report(link, aub.BuildRequestReceived, "", "", "")
	}
}

func sameBuildStatus(a, b aub.BuildRequestStatus) bool {
	return a.State == b.State && a.Stage == b.Stage && a.BuildID == b.BuildID && a.RunID == b.RunID
}

// report records what should be said about a request and returns at once.
func (q *buildRelay) report(link aub.BuildRequestLink, state, stage, buildID, runID string) {
	if q == nil {
		return
	}
	stage = truncateBuildField(stage, aub.BuildRequestStageLimit)
	buildID, runID = truncateBuildField(buildID, aub.BuildRequestIDLimit), truncateBuildField(runID, aub.BuildRequestIDLimit)
	now := q.clock.Now()
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	item := q.item(link, now)
	wanted := aub.BuildRequestStatus{State: state, Stage: stage, BuildID: buildID, RunID: runID}
	switch {
	case item == nil || item.final || item.over:
		// Ended, here or on AUB: nothing said now would be stored.
		q.mu.Unlock()
		return
	case sameBuildStatus(item.desired, wanted):
		// Nothing new to say. A request held for a sign-in is looked at again,
		// because this call is somebody noticing it.
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
	sequence := now.UnixMilli()
	for _, held := range []int64{item.desired.Sequence, item.acked.Sequence, item.floor} {
		if sequence <= held {
			sequence = held + 1
		}
	}
	wanted.Sequence = sequence
	item.desired = wanted
	item.final = aub.BuildRequestTerminal(state)
	if item.waiting != leakRelayStopped && item.failures == 0 {
		item.next = now
	}
	q.mu.Unlock()
	q.kick()
}

// truncateBuildField keeps a field inside AUB's limit, which counts characters.
func truncateBuildField(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func (q *buildRelay) kick() {
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

func (q *buildRelay) run() {
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
func (item *buildTracked) due() (time.Time, bool) {
	if item.waiting == leakRelayStopped || item.over || item.generation == item.ackedGen {
		return time.Time{}, false
	}
	return item.next, true
}

// step attempts every request that is due, one at a time.
func (q *buildRelay) step() (wait time.Duration, scheduled bool) {
	for {
		now := q.clock.Now()
		q.mu.Lock()
		var id string
		var item *buildTracked
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
// sent. It reports false when the relay is stopping.
func (q *buildRelay) attempt(id string, item *buildTracked) bool {
	session := q.session()
	now := q.clock.Now()
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	hold := func(why string) bool {
		if item.waiting != why {
			q.logf("build request status: request %s is held (%s); nothing is sent until that changes", shortLeakID(id), why)
		}
		item.waiting, item.next = why, now.Add(buildSessionRecheck)
		q.mu.Unlock()
		return true
	}
	switch {
	case session.Send == nil:
		return hold(leakRelaySignIn)
	case item.bound && (item.server != session.Server || item.userID != session.UserID):
		return hold(leakRelayAccount)
	case item.rejectedToken != "" && item.rejectedToken == session.Token:
		return hold(leakRelaySignIn)
	}
	payload, generation := item.desired, item.generation
	q.mu.Unlock()

	ctx, cancel := context.WithTimeout(q.ctx, buildStatusTimeout)
	answer, err := session.Send(ctx, id, payload)
	cancel()

	now = q.clock.Now()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ctx.Err() != nil {
		return false
	}
	var apiErr *aub.APIError
	switch {
	case err == nil:
		if item.failures > 0 {
			q.logf("build request status: request %s delivered `%s` after %d failed attempt(s)", shortLeakID(id), payload.State, item.failures)
		}
		item.bound, item.server, item.userID = true, session.Server, session.UserID
		item.acked, item.ackedGen = payload, generation
		item.failures, item.rejectedToken, item.lastError, item.waiting = 0, "", "", leakRelayDelivered
		if aub.BuildRequestTerminal(payload.State) || answer.Superseded && aub.BuildRequestTerminal(answer.State) {
			// AUB holds an ending: nothing more this program says would be
			// stored. (Superseded by a later sequence alone is not an ending:
			// the next status is sent as usual.)
			item.over = true
			if answer.Superseded {
				item.acked.State = answer.State
			}
		} else if item.generation != generation {
			item.waiting, item.next = leakRelayPending, now
		}
	case errors.As(err, &apiErr) && apiErr.Unauthorized():
		q.logf("build request status: request %s was refused (HTTP %d); nothing more is sent until someone signs in", shortLeakID(id), apiErr.StatusCode)
		item.rejectedToken, item.waiting, item.lastError = session.Token, leakRelaySignIn, "the account server refused this session"
		item.next = now.Add(buildSessionRecheck)
	case errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 &&
		apiErr.StatusCode != 408 && apiErr.StatusCode != 425 && apiErr.StatusCode != 429:
		// AUB understood and said no — a 404 is "not this account's, or gone".
		// A retry would be told the same.
		q.logf("build request status: request %s was rejected (HTTP %d); it is not sent again", shortLeakID(id), apiErr.StatusCode)
		item.waiting, item.lastError = leakRelayStopped, apiErr.Error()
	default:
		step := item.failures
		if step >= len(leakStatusBackoff) {
			step = len(leakStatusBackoff) - 1
		}
		if item.failures == 0 {
			q.logf("build request status: request %s could not be delivered (%s); retrying", shortLeakID(id), leakFailureClass(err))
		}
		item.failures++
		item.waiting, item.lastError = leakRelayRetrying, leakFailureClass(err)
		item.next = now.Add(leakStatusBackoff[step])
	}
	return true
}

// view is one request's delivery, for the page — leakRelayView's shape.
func (q *buildRelay) view(requestID string) *leakRelayView {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item := q.items[requestID]
	if item == nil || item.generation == 0 {
		return nil
	}
	return &leakRelayView{State: item.waiting, Desired: item.desired.State, Delivered: item.acked.State,
		Failures: item.failures, Error: item.lastError}
}

// sessionChanged: somebody signed in or out here, so a held request is looked
// at now instead of at its next recheck.
func (q *buildRelay) sessionChanged() {
	if q == nil {
		return
	}
	now := q.clock.Now()
	q.mu.Lock()
	held := false
	for _, item := range q.items {
		if item.waiting == leakRelaySignIn || item.waiting == leakRelayAccount {
			item.rejectedToken, item.next, held = "", now, true
		}
	}
	q.mu.Unlock()
	if held {
		q.kick()
	}
}

// close stops the worker and whatever POST it is in. Nothing is sent after it
// returns, and a later report is ignored.
func (q *buildRelay) close() {
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

// --- following a started request's run --------------------------------------

// watchBuildRequestRun says what the run a request started is doing, until it
// ends: `building` with the run's stage as it moves, then `completed`,
// `failed` or `cancelled`, once. Its life is the run's: it stops when the run
// ends, when this process no longer runs it, or when the Companion stops.
func (s *Server) watchBuildRequestRun(link aub.BuildRequestLink, runID string) {
	relay := s.buildReqs
	said, unreadable := "", 0
	for {
		select {
		case <-relay.ctx.Done():
			return
		case <-time.After(buildRunPoll):
		}
		record, err := s.readPlayRun(runID)
		if err != nil {
			if unreadable++; unreadable >= 20 {
				relay.report(link, aub.BuildRequestFailed, "run_unreadable", "", runID)
				return
			}
			continue
		}
		unreadable = 0
		if record.State.Terminal() {
			state, stage := buildRequestEnding(record)
			relay.report(link, state, stage, record.BuildID, runID)
			return
		}
		if key := string(record.State) + "|" + record.BuildID; key != said {
			said = key
			relay.report(link, aub.BuildRequestBuilding, string(record.State), record.BuildID, runID)
		}
		if !s.playRunLive(runID) {
			// Not running here and not ended: read once more, since it may
			// have ended between the two reads, else it was interrupted.
			if again, err := s.readPlayRun(runID); err == nil && again.State.Terminal() {
				state, stage := buildRequestEnding(again)
				relay.report(link, state, stage, again.BuildID, runID)
			} else {
				relay.report(link, aub.BuildRequestFailed, buildStageInterrupted, record.BuildID, runID)
			}
			return
		}
	}
}

func (s *Server) readPlayRun(runID string) (*playrun.Record, error) {
	service, err := s.playService()
	if err != nil {
		return nil, err
	}
	return service.Get(runID)
}

func (s *Server) playRunLive(runID string) bool {
	for _, run := range s.playLive.Running() {
		if run.ID == runID {
			return true
		}
	}
	return false
}

// buildRequestEnding is the terminal status a finished run is reported as.
func buildRequestEnding(record *playrun.Record) (state, stage string) {
	switch record.State {
	case playrun.Succeeded:
		if record.Request.BuildOnly {
			return aub.BuildRequestCompleted, "built"
		}
		return aub.BuildRequestCompleted, "engine_started"
	case playrun.Cancelled:
		return aub.BuildRequestCancelled, string(record.FailedAt)
	default:
		stage = string(record.FailedAt)
		if record.FailureCode != "" {
			stage += ":" + record.FailureCode
		}
		return aub.BuildRequestFailed, stage
	}
}
