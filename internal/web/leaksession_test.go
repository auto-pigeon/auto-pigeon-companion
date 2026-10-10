package web

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// A leak status and the session it is sent under (NEW_307W1, NEW_323B).
//
// Found live (`NEW_307W1`): the Companion held a token that had expired a week
// before. The notice printed AUB's raw 401 with a Retry; the editor was told
// nothing. Two kinds of session are no use: a token that says it expired, which
// is not worth sending at all, and one AUB refuses although it looks fine.
//
// Release run 38072497788 then failed on Windows in the test that covered
// both. It walked the two kinds in Go's map order with the real worker running
// beside it, and counted "what reached AUB" per kind: a POST the worker had
// dispatched under the first kind's token arrived after the fixture had moved
// on to the second, and was charged to it. That attempt was an OLDER one, sent
// under the session it was admitted under, and not a new request under an
// expired token.
//
// So these are stepped: the test is the worker, both orders are written out,
// and every POST that reaches AUB is accounted to the session it carried.
// Reading the sender for that found what the count had been standing in front
// of — see TestALeakStatusIsSentAsTheSessionItWasAdmittedUnder.

const refusedSession = "opaque-refused"

func expiredSession() string {
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(-time.Hour).Unix())))
	return "h." + claims + ".s"
}

// sessionName says which session a token is without printing it.
func sessionName(token, signedIn string) string {
	switch {
	case token == "":
		return "no session"
	case token == refusedSession:
		return "the refused session"
	case aub.TokenExpired(token, time.Now()):
		return "the expired session"
	case token == signedIn:
		return "the signed-in session"
	}
	return "an unknown session"
}

func sessionNames(tokens []string, signedIn string) []string {
	names := make([]string, 0, len(tokens))
	for _, token := range tokens {
		names = append(names, sessionName(token, signedIn))
	}
	return names
}

// steppedLeaks replaces the machine's leak sender with one the test steps. It
// is the server's own session and the server's own client underneath: only
// the worker goroutine and the clock are the test's.
func (m *machine) steppedLeaks() (*leakSender, *testClock) {
	m.t.Helper()
	m.server.leaks.close()
	clock := newTestClock()
	sender := newLeakSender(clock, m.server.leakSession, m.t.Logf)
	sender.manual = true
	m.server.leaks = sender
	m.t.Cleanup(sender.close)
	return sender, clock
}

// pendingLeakLink puts one leak request on this machine, as a link does.
func (m *machine) pendingLeakLink(letter string) aub.LeakTestLink {
	m.t.Helper()
	m.backend.savedAPMap("quake1")
	dir, err := m.server.configDir()
	if err != nil {
		m.t.Fatal(err)
	}
	link := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: strings.Repeat(letter, 32)}
	if err := leakintent.Receive(leakintent.Path(dir), link, time.Now().UTC()); err != nil {
		m.t.Fatal(err)
	}
	return link
}

// watch is the open page asking whether a link arrived, with the worker given
// its turn after each ask.
func (m *machine) watch(sender *leakSender, times int) map[string]any {
	m.t.Helper()
	var body map[string]any
	for range times {
		_, body = m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
		sender.step()
	}
	return body
}

func (m *machine) acceptedStatuses() []string {
	m.backend.mu.Lock()
	defer m.backend.mu.Unlock()
	return append([]string(nil), m.backend.leakStatuses...)
}

func (m *machine) signedInToken() string {
	return m.server.aubClient().Token()
}

func TestASessionThatIsNoUseStartsNoStatusRequestInEitherOrder(t *testing.T) {
	type kind struct {
		name    string
		token   func() string
		expired bool
	}
	refused := kind{"a session AUB refuses", func() string { return refusedSession }, false}
	expired := kind{"a session past its own expiry", expiredSession, true}
	for _, order := range [][]kind{{refused, expired}, {expired, refused}} {
		t.Run(order[0].name+", then "+order[1].name, func(t *testing.T) {
			m := newMachine(t)
			sender, clock := m.steppedLeaks()
			link := m.pendingLeakLink("8")
			m.backend.refuse(refusedSession)

			for _, phase := range order {
				token := phase.token()
				m.server.aubClient().SetToken(token)
				served, arrived := m.backend.count(), len(m.backend.statusArrivals(0))

				status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
				if status != http.StatusOK || body["sign_in_required"] != true || body["request_id"] != link.RequestID || body["source_ref"] != nil {
					t.Fatalf("%s: %d %v", phase.name, status, body)
				}
				watched := m.watch(sender, 3)
				posts := m.backend.statusArrivals(arrived)
				if phase.expired {
					// Not worth sending at all: not the review's question, and
					// not a status.
					if got := m.backend.count() - served; got != 0 || len(posts) != 0 {
						t.Fatalf("%s made %d request(s) to the account server, %d of them a status (%v)",
							phase.name, got, len(posts), sessionNames(posts, ""))
					}
				} else if len(posts) != 1 || posts[0] != token {
					// AUB is the authority on a token that looks fine, so it is
					// asked — once. Its refusal is the end of that.
					t.Fatalf("%s: %d status POST(s) arrived, carrying %v; want one, under that session",
						phase.name, len(posts), sessionNames(posts, ""))
				}
				if relay, _ := watched["relay"].(map[string]any); relay["state"] != leakRelaySignIn {
					t.Fatalf("%s: the page is told %v", phase.name, watched["relay"])
				}

				// The watch runs on, the recheck comes round, and nothing more
				// is started under a session that is no use.
				arrived = len(m.backend.statusArrivals(0))
				m.watch(sender, 3)
				clock.advance(leakSessionRecheck)
				sender.step()
				m.watch(sender, 2)
				if more := m.backend.statusArrivals(arrived); len(more) != 0 {
					t.Fatalf("%s: %d more status POST(s) were started after it was known to be no use (%v)",
						phase.name, len(more), sessionNames(more, ""))
				}
			}
			if accepted := m.acceptedStatuses(); len(accepted) != 0 {
				t.Fatalf("AUB accepted %v from sessions it refuses", accepted)
			}

			// Signing in is all it takes: the SAME request resolves, and the
			// editor is told once that it arrived.
			m.signIn()
			sender.step()
			status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
			if status != http.StatusOK || body["sign_in_required"] != nil || body["request_id"] != link.RequestID || body["source_ref"] == nil {
				t.Fatalf("after signing in: %d %v", status, body)
			}
			m.watch(sender, 3)
			received := 0
			for _, line := range m.acceptedStatuses() {
				if !strings.HasPrefix(line, link.RequestID+" ") {
					t.Fatalf("the editor was told about another request: %q", line)
				}
				if line == link.RequestID+" received" {
					received++
				}
			}
			if received != 1 {
				t.Fatalf("after signing in the editor was told `received` %d time(s): %v", received, m.acceptedStatuses())
			}
			for _, token := range m.backend.statusArrivals(0) {
				if aub.TokenExpired(token, time.Now()) {
					t.Fatal("a status POST carried the expired session")
				}
			}
		})
	}
}

// An attempt is admitted against one reading of the session, and the POST is
// built a moment later. The POST used to read the client's token again when it
// was built — so a session that changed in that moment (a sign-out, another
// account, `companion auth login` in a terminal rewriting the config file) put
// a token on the wire that nothing had admitted, and the answer was then
// recorded against the token that HAD been: an expired token could start a
// request, and a refusal of one session was remembered as a refusal of
// another (NEW_323B).
func TestALeakStatusIsSentAsTheSessionItWasAdmittedUnder(t *testing.T) {
	for _, becomes := range []struct {
		name  string
		token func() string
	}{
		{"the expired session", expiredSession},
		{"the refused session", func() string { return refusedSession }},
		{"no session", func() string { return "" }},
	} {
		t.Run("the session becomes "+becomes.name+" between admission and dispatch", func(t *testing.T) {
			m := newMachine(t)
			sender, _ := m.steppedLeaks()
			link := m.pendingLeakLink("7")
			m.backend.refuse(refusedSession)
			m.signIn()
			admitted := m.signedInToken()

			// The barrier: the session the attempt is admitted under is read,
			// and THEN the session changes — before anything is dispatched.
			swapped := false
			sender.session = func() leakSession {
				session := m.server.leakSession()
				if !swapped && session.Send != nil {
					swapped = true
					m.server.aubClient().SetToken(becomes.token())
				}
				return session
			}
			sender.report(link, leakReceived, "", "", true)
			sender.step()

			posts := m.backend.statusArrivals(0)
			if len(posts) != 1 || posts[0] != admitted {
				t.Fatalf("the attempt admitted under the signed-in session arrived as %v", sessionNames(posts, admitted))
			}
			if accepted := m.acceptedStatuses(); len(accepted) != 1 || accepted[0] != link.RequestID+" received" {
				t.Fatalf("AUB accepted %v", accepted)
			}
			// It was delivered, by the account it was admitted under. What the
			// session is NOW decides the next attempt, not this one.
			if view := sender.view(link.RequestID); view.State != leakRelayDelivered || view.Delivered != leakReceived {
				t.Fatalf("the page is told %+v", view)
			}
		})
	}
}

// heldStatusPost holds the next status POST at AUB, after it arrived and
// before it is answered. It returns the channel that says it arrived, and the
// function that lets it be answered.
func (m *machine) heldStatusPost() (arrived <-chan string, release func()) {
	reached, proceed := make(chan string, 1), make(chan struct{})
	held := false
	m.backend.mu.Lock()
	m.backend.leakStatusArrival = func(token string) {
		m.backend.mu.Lock()
		first := !held
		held = true
		m.backend.mu.Unlock()
		if first {
			reached <- token
			<-proceed
		}
	}
	m.backend.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(proceed) }) }
	m.t.Cleanup(release)
	return reached, release
}

// THE RULE for a POST that is already in the air when the session changes: it
// belongs to the session it was admitted under. It is not cancelled and it is
// not re-attributed. Its answer is a fact about that session — and a refusal
// of a session nobody holds any more is not a reason to make the request wait
// for a sign-in that has already happened.
func TestARefusalOfTheOldSessionDoesNotHoldTheRequestAfterASignIn(t *testing.T) {
	m := newMachine(t)
	sender, _ := m.steppedLeaks()
	link := m.pendingLeakLink("6")
	m.backend.refuse(refusedSession)
	m.server.aubClient().SetToken(refusedSession)
	arrived, release := m.heldStatusPost()

	sender.report(link, leakReceived, "", "", true)
	stepped := make(chan struct{})
	go func() { sender.step(); close(stepped) }()
	if token := <-arrived; token != refusedSession {
		t.Fatalf("the held POST carries %s", sessionName(token, ""))
	}
	// The person signs in while that POST is unanswered.
	m.signIn()
	signedIn := m.signedInToken()
	release()
	<-stepped

	// The 401 was about the session that is gone. The request is due now,
	// under the one that is here, with no clock moved.
	sender.step()
	posts := m.backend.statusArrivals(0)
	if len(posts) != 2 || posts[0] != refusedSession || posts[1] != signedIn {
		t.Fatalf("status POSTs arrived as %v; want the old session's, then one under the new", sessionNames(posts, signedIn))
	}
	if accepted := m.acceptedStatuses(); len(accepted) != 1 || accepted[0] != link.RequestID+" received" {
		t.Fatalf("AUB accepted %v; want the same request acknowledged once", accepted)
	}
	if view := sender.view(link.RequestID); view.State != leakRelayDelivered {
		t.Fatalf("the page is told %+v", view)
	}
	// And once is once: the watch does not make it twice.
	m.watch(sender, 3)
	if posts := m.backend.statusArrivals(0); len(posts) != 2 {
		t.Fatalf("%d status POSTs after the watch ran on: %v", len(posts), sessionNames(posts, signedIn))
	}
}

// The other way round: accepted late, after the person signed out. AUB took it
// from the account that sent it, so it is delivered — and nothing new is
// started for a machine nobody is signed in on.
func TestAnAnswerToTheOldSessionAfterSigningOutStartsNothingNew(t *testing.T) {
	m := newMachine(t)
	sender, clock := m.steppedLeaks()
	link := m.pendingLeakLink("5")
	m.signIn()
	signedIn := m.signedInToken()
	arrived, release := m.heldStatusPost()

	sender.report(link, leakReceived, "", "", true)
	stepped := make(chan struct{})
	go func() { sender.step(); close(stepped) }()
	if token := <-arrived; token != signedIn {
		t.Fatalf("the held POST carries %s", sessionName(token, signedIn))
	}
	if status, body := m.call(http.MethodPost, "/api/auth/logout", nil); status != http.StatusOK {
		t.Fatalf("signing out = %d: %v", status, body["error"])
	}
	release()
	<-stepped

	if accepted := m.acceptedStatuses(); len(accepted) != 1 || accepted[0] != link.RequestID+" received" {
		t.Fatalf("AUB accepted %v", accepted)
	}
	if view := sender.view(link.RequestID); view.Delivered != leakReceived {
		t.Fatalf("the page is told %+v", view)
	}
	// The refresh comes due with nobody signed in: it is held, not sent.
	clock.advance(leakStatusRefresh)
	sender.step()
	m.watch(sender, 2)
	if posts := m.backend.statusArrivals(0); len(posts) != 1 {
		t.Fatalf("signed out, %d status POSTs arrived: %v", len(posts), sessionNames(posts, signedIn))
	}
	if view := sender.view(link.RequestID); view.State != leakRelaySignIn {
		t.Fatalf("signed out, the page is told %+v", view)
	}
}

// The same two kinds with the REAL worker running beside the test, which is
// the shape that failed on Windows — written so that what it failed on is the
// thing under test. In the order that failed, the refused session's POST is
// held in the air until the test has moved on to the expired session, and is
// then accounted to the session it carried.
func TestAnExpiredSessionAsksForSignInAndTheSameRequestThenResolves(t *testing.T) {
	for _, order := range [][]string{{"refused", "expired"}, {"expired", "refused"}} {
		t.Run(order[0]+" then "+order[1], func(t *testing.T) {
			m := newMachine(t)
			link := m.pendingLeakLink("8")
			m.backend.refuse(refusedSession)
			expired := expiredSession()
			var arrived <-chan string
			release := func() {}
			if order[0] == "refused" {
				arrived, release = m.heldStatusPost()
			}

			for _, kind := range order {
				token := refusedSession
				if kind == "expired" {
					token = expired
				}
				m.server.aubClient().SetToken(token)
				served := m.backend.count()
				status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil)
				if status != http.StatusOK || body["sign_in_required"] != true || body["request_id"] != link.RequestID || body["source_ref"] != nil {
					t.Fatalf("the %s session: %d %v", kind, status, body)
				}
				// The watch runs all the while.
				for range 3 {
					m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
				}
				switch {
				case kind == "refused" && arrived != nil:
					// The worker's one POST under this session is now in the
					// air, unanswered, and the test moves on without it.
					if got := <-arrived; got != refusedSession {
						t.Fatalf("the POST in the air carries %s", sessionName(got, ""))
					}
				case kind == "expired" && arrived != nil:
					// Whatever reached AUB while the expired session was the
					// session, it was not a new request: the only POST there is
					// is the older one, still held.
					if got := m.backend.count() - served; got != 0 {
						t.Fatalf("the expired session made %d request(s) to the account server", got)
					}
					release()
				}
			}

			// The worker settles: one POST, under the refused session, refused.
			deadline := time.Now().Add(10 * time.Second)
			for {
				view := m.server.leaks.view(link.RequestID)
				posts := m.backend.statusArrivals(0)
				if view != nil && view.State == leakRelaySignIn && len(posts) >= 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the sender did not settle: %+v after %v", view, sessionNames(posts, ""))
				}
				time.Sleep(5 * time.Millisecond)
			}
			for range 3 {
				m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
			}
			posts := m.backend.statusArrivals(0)
			if len(posts) != 1 || posts[0] != refusedSession {
				t.Fatalf("status POSTs arrived as %v; want one, under the refused session", sessionNames(posts, ""))
			}
			if accepted := m.acceptedStatuses(); len(accepted) != 0 {
				t.Fatalf("AUB accepted %v from a session it refuses", accepted)
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
		})
	}
}
