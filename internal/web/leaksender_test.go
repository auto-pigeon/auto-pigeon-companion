package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// --- a clock and an account server a test controls --------------------------

type testClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*testTimer
}

type testTimer struct {
	at   time.Time
	fire chan time.Time
	done bool
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) After(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &testTimer{at: c.now.Add(d), fire: make(chan time.Time, 1)}
	c.timers = append(c.timers, timer)
	return timer.fire, func() {
		c.mu.Lock()
		timer.done = true
		c.mu.Unlock()
	}
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, timer := range c.timers {
		if !timer.done && !timer.at.After(c.now) {
			timer.done = true
			timer.fire <- c.now
		}
	}
}

// relayServer stands in for AUB's relay: it records every POST, answers what
// the test told it to, and can forget what it holds.
type relayServer struct {
	mu       sync.Mutex
	attempts []string // every POST, as "state/stage"
	accepted []aub.LeakStatus
	stored   map[string]aub.LeakStatus
	fail     func(attempt int, status aub.LeakStatus) error
	// account is who is signed in; an empty UserID is nobody.
	account leakSession
	// before runs inside a POST, before it answers.
	before func(ctx context.Context, status aub.LeakStatus)
}

func newRelayServer() *relayServer {
	return &relayServer{stored: map[string]aub.LeakStatus{},
		account: leakSession{Server: "https://aub.test", UserID: "user-a", Token: "token-a1"}}
}

func (r *relayServer) session() leakSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.UserID == "" {
		return leakSession{}
	}
	session := r.account
	session.Send = r.send
	return session
}

func (r *relayServer) signIn(userID, token string) {
	r.mu.Lock()
	r.account.UserID, r.account.Token = userID, token
	r.mu.Unlock()
}

func (r *relayServer) send(ctx context.Context, requestID string, status aub.LeakStatus) error {
	r.mu.Lock()
	r.attempts = append(r.attempts, strings.TrimSuffix(status.State+"/"+status.Stage, "/"))
	attempt, fail, before := len(r.attempts), r.fail, r.before
	r.mu.Unlock()
	if before != nil {
		before(ctx, status)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if fail != nil {
		if err := fail(attempt, status); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// AUB's own rule: a lower sequence than the stored one is not stored.
	if held, ok := r.stored[requestID]; !ok || status.Sequence >= held.Sequence {
		r.stored[requestID] = status
	}
	r.accepted = append(r.accepted, status)
	return nil
}

func (r *relayServer) failing(err error) {
	r.mu.Lock()
	if err == nil {
		r.fail = nil
	} else {
		r.fail = func(int, aub.LeakStatus) error { return err }
	}
	r.mu.Unlock()
}

func (r *relayServer) posts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.attempts)
}

func (r *relayServer) acceptedStates() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var states []string
	for _, status := range r.accepted {
		states = append(states, strings.TrimSuffix(status.State+"/"+status.Stage, "/"))
	}
	return strings.Join(states, " ")
}

func (r *relayServer) holds(requestID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := r.stored[requestID]
	return strings.TrimSuffix(status.State+"/"+status.Stage, "/")
}

var (
	errRelayAway   = &aub.APIError{StatusCode: http.StatusServiceUnavailable, Path: aub.LeakStatusPath}
	errRelayDenied = &aub.APIError{StatusCode: http.StatusUnauthorized, Path: aub.LeakStatusPath}
	errRelayBad    = &aub.APIError{StatusCode: http.StatusBadRequest, Path: aub.LeakStatusPath}
)

func testLeakLink(letter string) aub.LeakTestLink {
	return aub.LeakTestLink{AssetID: "saved-map", Revision: 4, ContentSHA256: strings.Repeat("9", 64),
		RequestID: strings.Repeat(letter, 32)}
}

// steppedSender is a sender whose worker the test is: nothing is sent until
// the test calls step, at the time its own clock says.
func steppedSender(t *testing.T) (*leakSender, *relayServer, *testClock) {
	t.Helper()
	clock, relay := newTestClock(), newRelayServer()
	sender := newLeakSender(clock, relay.session, t.Logf)
	sender.manual = true
	t.Cleanup(sender.close)
	return sender, relay, clock
}

// --- B: bounded backoff -----------------------------------------------------

func TestLeakStatusRetriesBackOffAndNeverSpin(t *testing.T) {
	sender, relay, clock := steppedSender(t)
	relay.failing(errRelayAway)
	link := testLeakLink("a")
	sender.report(link, leakReceived, "", "", true)

	for attempt, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second} {
		wait, scheduled := sender.step()
		if !scheduled || wait != want {
			t.Fatalf("after failure %d the next attempt is in %s (scheduled %v), want %s", attempt+1, wait, scheduled, want)
		}
		if got := relay.posts(); got != attempt+1 {
			t.Fatalf("after failure %d there were %d POST(s)", attempt+1, got)
		}
		// Asking again before the wait is over sends nothing: the page's
		// two-second watch is exactly this.
		clock.advance(want - time.Millisecond)
		sender.report(link, leakReceived, "", "", true)
		if again, _ := sender.step(); again != time.Millisecond || relay.posts() != attempt+1 {
			t.Fatalf("an attempt was made %s early (POSTs %d)", again, relay.posts())
		}
		clock.advance(time.Millisecond)
	}
	if view := sender.view(link.RequestID); view == nil || view.State != leakRelayRetrying || view.Failures != 8 || view.Delivered != "" {
		t.Fatalf("the page is told %+v", view)
	}
	relay.failing(nil)
	if _, scheduled := sender.step(); !scheduled || relay.acceptedStates() != "received" {
		t.Fatalf("after recovery AUB accepted %q", relay.acceptedStates())
	}
	if view := sender.view(link.RequestID); view.State != leakRelayDelivered || view.Failures != 0 || view.Delivered != leakReceived {
		t.Fatalf("after recovery the page is told %+v", view)
	}
}

// --- C: progress advances while the server is away --------------------------

func TestLeakStatusRecoverySendsWhereTheWorkIsNotWhereItWas(t *testing.T) {
	sender, relay, clock := steppedSender(t)
	relay.failing(errRelayAway)
	link := testLeakLink("b")
	sender.report(link, leakReceived, "", "", true)
	sender.step()
	sender.report(link, leakReviewing, "", "", false)
	clock.advance(time.Second)
	sender.step()
	sender.report(link, leakBuilding, "convert", "build-1", false)
	sender.report(link, leakBuilding, "qbsp", "build-1", false)
	// The watch keeps saying "received" all the while.
	sender.report(link, leakReceived, "", "", true)

	relay.failing(nil)
	clock.advance(2 * time.Second)
	sender.step()
	if got := relay.acceptedStates(); got != "building/qbsp" {
		t.Fatalf("recovery delivered %q, want only the current state", got)
	}
	if got := relay.holds(link.RequestID); got != "building/qbsp" {
		t.Fatalf("AUB holds %q", got)
	}
	// Nothing older is owed, and nothing older is sent later.
	sender.report(link, leakReceived, "", "", true)
	clock.advance(time.Second)
	sender.step()
	if got := relay.acceptedStates(); got != "building/qbsp" {
		t.Fatalf("an obsolete status followed: %q", got)
	}
}

// --- D: producers race, the wake-up channel overflows -----------------------

func TestConcurrentLeakStatusesEndOnTheLastOneSaid(t *testing.T) {
	relay := newRelayServer()
	sender := newLeakSender(nil, relay.session, t.Logf)
	t.Cleanup(sender.close)
	link := testLeakLink("c")

	// The first POST is held inside the server while everything else is said.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	relay.before = func(ctx context.Context, status aub.LeakStatus) {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}
	sender.report(link, leakReceived, "", "", true)
	<-entered

	// Many producers at once; the wake-up channel holds one, so nearly all
	// of these find it full. The last one said is `building/vis`.
	var producers sync.WaitGroup
	for i := range 64 {
		producers.Add(1)
		go func() {
			defer producers.Done()
			sender.report(link, leakReceived, "", "", true)
			sender.report(link, leakBuilding, fmt.Sprintf("step-%d", i), "build-1", false)
		}()
	}
	producers.Wait()
	sender.report(link, leakBuilding, "vis", "build-1", false)
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for relay.holds(link.RequestID) != "building/vis" {
		if time.Now().After(deadline) {
			t.Fatalf("AUB holds %q; accepted %q", relay.holds(link.RequestID), relay.acceptedStates())
		}
		time.Sleep(5 * time.Millisecond)
	}
	relay.mu.Lock()
	accepted := append([]aub.LeakStatus(nil), relay.accepted...)
	relay.mu.Unlock()
	// 65 things were said after `received`; they were not all sent, and what
	// was sent arrived in the order it was said.
	if len(accepted) > 3 {
		t.Errorf("%d statuses were posted for one coalesced burst: %q", len(accepted), relay.acceptedStates())
	}
	for i := 1; i < len(accepted); i++ {
		if accepted[i].Sequence <= accepted[i-1].Sequence {
			t.Errorf("status %d (%s, #%d) was sent after #%d", i, accepted[i].State, accepted[i].Sequence, accepted[i-1].Sequence)
		}
	}
	if view := sender.view(link.RequestID); view.State != leakRelayDelivered || view.Delivered != leakBuilding {
		t.Errorf("the page is told %+v", view)
	}
}

// A POST that was held up somewhere and lands after a newer one must not put
// the editor back: the sequence it carries is lower, and AUB's rule drops it.
func TestALateOlderLeakStatusCarriesALowerSequence(t *testing.T) {
	sender, relay, clock := steppedSender(t)
	link := testLeakLink("d")
	sender.report(link, leakReceived, "", "", true)
	sender.step()
	clock.advance(time.Millisecond)
	sender.report(link, leakReviewing, "", "", false)
	sender.step()
	relay.mu.Lock()
	received, reviewing := relay.accepted[0], relay.accepted[1]
	relay.mu.Unlock()
	if received.Sequence <= 0 || reviewing.Sequence <= received.Sequence {
		t.Fatalf("sequences: received #%d, reviewing #%d", received.Sequence, reviewing.Sequence)
	}
	// The held-up `received` arrives now.
	_ = relay.send(context.Background(), link.RequestID, received)
	if got := relay.holds(link.RequestID); got != "reviewing" {
		t.Fatalf("AUB holds %q after the late POST", got)
	}
	// Two statuses in the same millisecond are still ordered.
	sender.report(link, leakBuilding, "a", "b", false)
	sender.report(link, leakBuilding, "b", "b", false)
	sender.step()
	relay.mu.Lock()
	last := relay.accepted[len(relay.accepted)-1]
	relay.mu.Unlock()
	if last.Stage != "b" || last.Sequence < reviewing.Sequence+2 {
		t.Fatalf("the last status is %q #%d after #%d", last.Stage, last.Sequence, reviewing.Sequence)
	}
}

// --- E: AUB forgets, the refresh puts it back -------------------------------

func TestAnActiveLeakStatusIsRefreshedAfterAUBLosesIt(t *testing.T) {
	sender, relay, clock := steppedSender(t)
	link := testLeakLink("e")
	sender.report(link, leakReceived, "", "", true)
	sender.step()
	sender.report(link, leakReviewing, "", "", false)
	wait, scheduled := sender.step()
	if !scheduled || wait != leakStatusRefresh || relay.holds(link.RequestID) != "reviewing" {
		t.Fatalf("after `reviewing`: next in %s, AUB holds %q", wait, relay.holds(link.RequestID))
	}
	// AUB restarts: its relay is memory.
	relay.mu.Lock()
	relay.stored = map[string]aub.LeakStatus{}
	relay.mu.Unlock()
	posts := relay.posts()

	// The watch saying "received" does not restore anything, and must not.
	clock.advance(leakStatusRefresh - time.Second)
	sender.report(link, leakReceived, "", "", true)
	sender.step()
	if relay.posts() != posts {
		t.Fatal("a status was sent before its refresh was due")
	}
	clock.advance(time.Second)
	sender.step()
	if got := relay.holds(link.RequestID); got != "reviewing" {
		t.Fatalf("after the refresh AUB holds %q, want reviewing", got)
	}
	if got := relay.acceptedStates(); got != "received reviewing reviewing" {
		t.Fatalf("AUB was told %q", got)
	}

	// A finished request is not kept alive, and neither is one a newer click
	// replaced before anyone built it.
	sender.report(link, leakReturned, "", "build-1", false)
	if _, scheduled := sender.step(); scheduled {
		t.Fatal("a returned request is still scheduled")
	}
	older, newer := testLeakLink("1"), testLeakLink("2")
	sender.report(older, leakReceived, "", "", true)
	sender.step()
	sender.report(newer, leakReceived, "", "", true)
	sender.step()
	posts = relay.posts()
	clock.advance(leakStatusRefresh)
	sender.step()
	if got := relay.posts() - posts; got != 1 {
		t.Fatalf("%d refreshes for one live request and one replaced", got)
	}
}

// --- F: signed out, another account, a refused session ----------------------

func TestLeakStatusesWaitForTheSessionTheyBelongTo(t *testing.T) {
	sender, relay, clock := steppedSender(t)
	relay.signIn("", "")
	link := testLeakLink("f")
	sender.report(link, leakReceived, "", "", true)
	for range 20 {
		sender.report(link, leakReceived, "", "", true) // the watch
		sender.step()
		clock.advance(2 * time.Second)
	}
	if relay.posts() != 0 {
		t.Fatalf("%d POST(s) while signed out", relay.posts())
	}
	if view := sender.view(link.RequestID); view.State != leakRelaySignIn {
		t.Fatalf("signed out, the page is told %+v", view)
	}
	// Signing in resolves the same request: nothing was forgotten.
	relay.signIn("user-a", "token-a1")
	sender.sessionChanged()
	sender.step()
	if got := relay.acceptedStates(); got != "received" {
		t.Fatalf("after signing in AUB was told %q", got)
	}

	// Another account signs in on this machine. The request is the first
	// account's and nothing about it is posted as anybody else.
	relay.signIn("user-b", "token-b1")
	sender.report(link, leakReviewing, "", "", false)
	for range 10 {
		sender.step()
		clock.advance(leakSessionRecheck)
	}
	if relay.posts() != 1 {
		t.Fatalf("a status was posted under another account (%d POSTs)", relay.posts())
	}
	if view := sender.view(link.RequestID); view.State != leakRelayAccount {
		t.Fatalf("under another account the page is told %+v", view)
	}
	relay.signIn("user-a", "token-a2")
	sender.sessionChanged()
	sender.step()
	if got := relay.acceptedStates(); got != "received reviewing" {
		t.Fatalf("back on the right account AUB was told %q", got)
	}

	// AUB refuses the session. One POST found that out; no more follow until
	// the session carries another token or a person asks again.
	relay.failing(errRelayDenied)
	sender.report(link, leakBuilding, "qbsp", "build-1", false)
	sender.step()
	refused := relay.posts()
	for range 20 {
		sender.report(link, leakReceived, "", "", true)
		sender.step()
		clock.advance(leakSessionRecheck)
	}
	if relay.posts() != refused {
		t.Fatalf("%d more POST(s) with a session AUB had refused", relay.posts()-refused)
	}
	sender.retryNow(link.RequestID) // a person pressing Retry
	sender.step()
	if relay.posts() != refused+1 {
		t.Fatalf("an explicit retry made %d POST(s)", relay.posts()-refused)
	}
	relay.failing(nil)
	clock.advance(leakSessionRecheck)
	sender.step()
	if relay.posts() != refused+1 {
		t.Fatal("the refused token was tried again without a new sign-in")
	}
	relay.signIn("user-a", "token-a3")
	sender.sessionChanged()
	sender.step()
	if got := relay.holds(link.RequestID); got != "building/qbsp" {
		t.Fatalf("after a new sign-in AUB holds %q", got)
	}
}

// --- G: a request AUB rejects, and one that is too old -----------------------

func TestARejectedOrExpiredLeakRequestStops(t *testing.T) {
	sender, relay, clock := steppedSender(t)
	relay.failing(errRelayBad)
	link := testLeakLink("0")
	sender.report(link, leakReceived, "", "", true)
	if _, scheduled := sender.step(); scheduled || relay.posts() != 1 {
		t.Fatalf("after a rejection: scheduled %v, %d POST(s)", scheduled, relay.posts())
	}
	relay.failing(nil)
	for range 10 {
		sender.report(link, leakReceived, "", "", true)
		sender.report(link, leakReviewing, "", "", false)
		sender.retryNow(link.RequestID)
		clock.advance(time.Minute)
		sender.step()
	}
	if relay.posts() != 1 {
		t.Fatalf("a rejected request was posted %d times", relay.posts())
	}
	if view := sender.view(link.RequestID); view.State != leakRelayStopped || view.Error == "" {
		t.Fatalf("the page is told %+v", view)
	}

	// An id is bound to the revision it first named.
	moved := link
	moved.Revision++
	sender.report(moved, leakBuilding, "", "", false)
	if view := sender.view(link.RequestID); view.Desired == leakBuilding {
		t.Fatal("the same id was accepted for another revision")
	}

	// A request nobody could deliver is dropped at the end of its lifetime,
	// not retried for as long as the program runs.
	relay.failing(errRelayAway)
	old := testLeakLink("3")
	sender.report(old, leakReceived, "", "", true)
	sender.step()
	clock.advance(leakTrackedLifetime)
	posts := relay.posts()
	if _, scheduled := sender.step(); scheduled || relay.posts() != posts || sender.view(old.RequestID) != nil {
		t.Fatalf("an expired request: scheduled %v, POSTs +%d, view %+v", scheduled, relay.posts()-posts, sender.view(old.RequestID))
	}

	// The table is bounded.
	relay.failing(nil)
	for i := range leakTrackedRequests + 40 {
		sender.report(aub.LeakTestLink{AssetID: "m", Revision: 1, ContentSHA256: strings.Repeat("1", 64),
			RequestID: fmt.Sprintf("%032x", i+1)}, leakDismissed, "", "", false)
	}
	sender.mu.Lock()
	tracked := len(sender.items)
	sender.mu.Unlock()
	if tracked > leakTrackedRequests {
		t.Fatalf("%d requests are tracked, the bound is %d", tracked, leakTrackedRequests)
	}
}

// --- H: stopping in the middle of a POST ------------------------------------

func TestClosingTheLeakSenderEndsABlockedPost(t *testing.T) {
	relay := newRelayServer()
	sender := newLeakSender(nil, relay.session, t.Logf)
	entered := make(chan struct{}, 1)
	relay.before = func(ctx context.Context, _ aub.LeakStatus) {
		entered <- struct{}{}
		<-ctx.Done() // a server that never answers
	}
	link := testLeakLink("4")
	sender.report(link, leakReceived, "", "", true)
	<-entered

	closed := make(chan struct{})
	go func() { sender.close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close waited on a POST that would never return")
	}
	select {
	case <-sender.done:
	default:
		t.Fatal("the worker is still running after close")
	}
	posts := relay.posts()
	sender.report(link, leakReviewing, "", "", false)
	sender.retryNow(link.RequestID)
	sender.sessionChanged()
	sender.close() // twice is fine
	time.Sleep(50 * time.Millisecond)
	if relay.posts() != posts || len(relay.accepted) != 0 {
		t.Fatalf("a closed sender posted: %d more, accepted %q", relay.posts()-posts, relay.acceptedStates())
	}
	// A sender nobody ever used closes without a worker to wait for.
	newLeakSender(nil, relay.session, nil).close()
}

// A build, a GET and a review never wait on the relay: with the account
// server holding every POST open, the page's own routes still answer at once,
// and Close ends the held POST.
func TestTheLeakRoutesDoNotWaitOnTheRelay(t *testing.T) {
	m := newMachine(t)
	m.backend.asset.fileName = "fixture.apmap"
	m.signIn()
	relay := newRelayServer()
	held := make(chan struct{}, 8)
	relay.before = func(ctx context.Context, _ aub.LeakStatus) {
		held <- struct{}{}
		<-ctx.Done()
	}
	m.server.leaks.close()
	m.server.leaks = newLeakSender(nil, relay.session, t.Logf)
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	link := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: strings.Repeat("5", 32)}
	if err := leakintent.Receive(leakintent.Path(dir), link, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); status != http.StatusOK || body["request_id"] != link.RequestID {
		t.Fatalf("the watch: %d %v", status, body)
	}
	<-held
	for range 5 {
		if status, _ := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); status != http.StatusOK {
			t.Fatalf("the watch: %d", status)
		}
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/leak-test/reviewing", map[string]any{"request_id": link.RequestID}); status != http.StatusOK {
		t.Fatalf("reviewing: %d", status)
	}
	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending", nil); status != http.StatusOK || body["request_id"] != link.RequestID {
		t.Fatalf("pending: %d %v", status, body)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("the page's routes took %s with the relay held", took)
	}
	started = time.Now()
	m.server.Close()
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("Close took %s with a POST held", took)
	}
}

// --- I: a stage is cut by characters ----------------------------------------

func TestLeakStagesAreTruncatedByCharacter(t *testing.T) {
	long := strings.Repeat("la revisione è cambiata — 改訂が変わりました ", 12)
	cut := truncateLeakStage(long)
	if !utf8.ValidString(cut) || utf8.RuneCountInString(cut) != leakStageLimit || !strings.HasPrefix(long, cut) {
		t.Fatalf("%d characters, valid %v: %q", utf8.RuneCountInString(cut), utf8.ValidString(cut), cut)
	}
	if len(cut) <= leakStageLimit {
		t.Fatal("the fixture has no multi-byte characters in its first 120")
	}
	if short := "perché no"; truncateLeakStage(short) != short {
		t.Fatal("a short stage was changed")
	}
	if got := truncateLeakStage("bad \xff byte"); !utf8.ValidString(got) {
		t.Fatalf("invalid bytes were passed on: %q", got)
	}

	// And through the server: an error with non-ASCII text arrives whole
	// characters, inside AUB's limit.
	m := newMachine(t)
	m.signIn()
	link := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: strings.Repeat("6", 32)}
	m.server.reportLeakStatus(link, leakReturnFailed, long, "build-1")
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.backend.mu.Lock()
		bodies := append([]map[string]any(nil), m.backend.leakStatusBodies...)
		m.backend.mu.Unlock()
		if len(bodies) > 0 {
			stage, _ := bodies[0]["stage"].(string)
			if stage != cut {
				t.Fatalf("AUB received the stage %q", stage)
			}
			if sequence, _ := bodies[0]["sequence"].(float64); sequence <= 0 {
				t.Fatalf("the status carried no sequence: %v", bodies[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the status never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A transient failure is anything that is not AUB saying no.
func TestWhichLeakFailuresAreRetried(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{errors.New("dial tcp: connection refused"), leakRelayRetrying},
		{context.DeadlineExceeded, leakRelayRetrying},
		{&aub.APIError{StatusCode: 500}, leakRelayRetrying},
		{&aub.APIError{StatusCode: 502}, leakRelayRetrying},
		{&aub.APIError{StatusCode: 429}, leakRelayRetrying},
		{&aub.APIError{StatusCode: 401}, leakRelaySignIn},
		{&aub.APIError{StatusCode: 404}, leakRelayStopped},
		{&aub.APIError{StatusCode: 409}, leakRelayStopped},
		{&aub.APIError{StatusCode: 400}, leakRelayStopped},
	} {
		sender, relay, _ := steppedSender(t)
		relay.failing(test.err)
		link := testLeakLink("7")
		sender.report(link, leakReceived, "", "", true)
		sender.step()
		if view := sender.view(link.RequestID); view.State != test.want {
			t.Errorf("%v: the request is %q, want %q", test.err, view.State, test.want)
		}
		if view := sender.view(link.RequestID); strings.Contains(view.Error, "token") || strings.Contains(view.Error, "http://") {
			t.Errorf("%v: the reported failure names too much: %q", test.err, view.Error)
		}
	}
}

// --- L: the page, when the pending request is replaced under it -------------

func TestThePageNeverShowsOneLeakRequestAsAnother(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command("node", "testdata/leakrequest.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("leakrequest.check.mjs: %v\n%s", err, out)
	}
}

// The server's half of it: a page that names the request it shows is told
// when another one replaced it, and that newer request is NOT resolved for it.
func TestPendingAnswersReplacedToAPageNamingAnOlderRequest(t *testing.T) {
	m := newMachine(t)
	m.backend.asset.fileName = "fixture.apmap"
	m.signIn()
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	older := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: strings.Repeat("a", 32)}
	newer := older
	newer.RequestID = strings.Repeat("b", 32)
	if err := leakintent.Receive(leakintent.Path(dir), older, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+older.RequestID, nil)
	if status != http.StatusOK || body["request_id"] != older.RequestID || body["source_ref"] == nil || body["replaced"] != nil {
		t.Fatalf("the named pending request: %d %v", status, body)
	}
	if err := leakintent.Receive(leakintent.Path(dir), newer, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	served := m.backend.count()
	status, body = m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+older.RequestID, nil)
	if status != http.StatusOK || body["replaced"] != true || body["request_id"] != newer.RequestID || body["source_ref"] != nil {
		t.Fatalf("asking about a replaced request: %d %v", status, body)
	}
	if got := m.backend.count(); got != served {
		t.Errorf("a replaced request made %d call(s) to the account server", got-served)
	}
	// The form without a name still answers about whatever is pending.
	if status, body = m.call(http.MethodGet, "/api/v1/leak-test/pending", nil); status != http.StatusOK || body["request_id"] != newer.RequestID || body["source_ref"] == nil {
		t.Fatalf("the unnamed form: %d %v", status, body)
	}
	// And a build for the older request is refused: it is no longer pending.
	if m.server.matchesPendingLeakRequest(older.RequestID, build.SourceRef{AssetType: aub.AssetTypeMap, AssetID: older.AssetID,
		RevisionID: "r", Revision: older.Revision, ContentSHA256: older.ContentSHA256, Refetchable: true}) {
		t.Fatal("a replaced request still matches a build")
	}
}

// --- a session that ran out is "sign in", not an error to retry -------------

// Found live (`NEW_307W1`): the Companion held a token that had expired a week
// before. The notice printed AUB's raw 401 with a Retry; the editor was told
// nothing. Both kinds are covered: a token that says it expired, which is not
// worth sending at all, and one AUB refuses although it looks fine.
func TestAnExpiredSessionAsksForSignInAndTheSameRequestThenResolves(t *testing.T) {
	m := newMachine(t)
	m.backend.asset.fileName = "fixture.apmap"
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	link := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: strings.Repeat("8", 32)}
	if err := leakintent.Receive(leakintent.Path(dir), link, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(-time.Hour).Unix())))
	for name, token := range map[string]string{"a token past its own expiry": "h." + claims + ".s", "a token AUB refuses": "opaque-refused"} {
		m.server.aubClient().SetToken(token)
		m.backend.mu.Lock()
		m.backend.rejectToken = token
		m.backend.mu.Unlock()
		served := m.backend.count()
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
		if status != http.StatusOK || body["sign_in_required"] != true || body["request_id"] != link.RequestID || body["source_ref"] != nil {
			t.Fatalf("%s: %d %v", name, status, body)
		}
		if strings.Contains(name, "expiry") && m.backend.count() != served {
			t.Errorf("%s was sent to the account server", name)
		}
		// The watch runs all the while, and says nothing to AUB.
		for range 3 {
			m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
		}
	}
	time.Sleep(150 * time.Millisecond)
	m.backend.mu.Lock()
	posts := m.backend.leakStatusPosts
	m.backend.mu.Unlock()
	if posts != 0 {
		t.Fatalf("%d status POST(s) reached AUB with a session it refuses", posts)
	}
	if _, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); body["relay"].(map[string]any)["state"] != leakRelaySignIn {
		t.Fatalf("the page is told %v", body["relay"])
	}

	// Signing in is all it takes: the SAME request resolves and is acknowledged.
	m.signIn()
	status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
	if status != http.StatusOK || body["sign_in_required"] != nil || body["request_id"] != link.RequestID || body["source_ref"] == nil {
		t.Fatalf("after signing in: %d %v", status, body)
	}
	if got := m.leakStatusesSoon(t, 1); len(got) != 1 || got[0] != link.RequestID+" received" {
		t.Fatalf("after signing in the editor was told %v", got)
	}
}
