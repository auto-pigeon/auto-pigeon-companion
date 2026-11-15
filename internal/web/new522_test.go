package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// NEW_522 (issue #35): every pending leak request keeps its own refresh.
//
// The sender used to keep one pending request alive at a time: a second
// request's `received` marked every other unbuilt request superseded, and
// their 30-second refresh stopped. AUB keeps the relay in memory, so after an
// AUB restart the first editor's status was never put back. These tests drive
// the real sender, on a clock the test moves, against an AUB stand-in over
// real HTTP that records every POST by request id and can forget what it
// holds.

// fakeLeakAUB is AUB's leak-status relay over HTTP: it keeps the highest
// sequence per request in memory, as AUB does, and can restart (forget it all)
// or be away (answer 503).
type fakeLeakAUB struct {
	t   *testing.T
	srv *httptest.Server

	mu     sync.Mutex
	posts  []leakPost
	stored map[string]aub.LeakStatus
	down   bool
	// hold, when set, runs inside a POST before it is answered.
	hold func(requestID string, status aub.LeakStatus)
}

type leakPost struct {
	RequestID string
	Status    aub.LeakStatus
	Code      int
}

const fakeLeakToken = "token-new522"

func newFakeLeakAUB(t *testing.T) *fakeLeakAUB {
	t.Helper()
	fake := &fakeLeakAUB{t: t, stored: map[string]aub.LeakStatus{}}
	fake.srv = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.srv.Close)
	return fake
}

func (f *fakeLeakAUB) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, aub.LeakStatusPath) {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != fakeLeakToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, aub.LeakStatusPath)
	var status aub.LeakStatus
	if err := json.NewDecoder(r.Body).Decode(&status); err != nil || status.SchemaVersion != aub.LeakStatusSchema {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	down, hold := f.down, f.hold
	if down {
		f.posts = append(f.posts, leakPost{RequestID: id, Status: status, Code: http.StatusServiceUnavailable})
		f.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	f.mu.Unlock()
	if hold != nil {
		hold(id, status)
	}
	f.mu.Lock()
	f.posts = append(f.posts, leakPost{RequestID: id, Status: status, Code: http.StatusOK})
	if held, ok := f.stored[id]; !ok || status.Sequence >= held.Sequence {
		f.stored[id] = status
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// session is the Companion's session, posting through the real AUB client.
func (f *fakeLeakAUB) session() leakSession {
	client, err := aub.New(f.srv.URL, f.srv.Client())
	if err != nil {
		f.t.Fatal(err)
	}
	return leakSession{Server: f.srv.URL, UserID: "user-new522", Token: fakeLeakToken,
		Send: func(ctx context.Context, requestID string, status aub.LeakStatus) error {
			return client.PublishLeakStatusAs(ctx, fakeLeakToken, requestID, status)
		}}
}

// restart is AUB restarting: its relay is memory.
func (f *fakeLeakAUB) restart() {
	f.mu.Lock()
	f.stored = map[string]aub.LeakStatus{}
	f.mu.Unlock()
}

func (f *fakeLeakAUB) setDown(down bool) {
	f.mu.Lock()
	f.down = down
	f.mu.Unlock()
}

func (f *fakeLeakAUB) mark() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}

// since is every POST after mark, as "<id letter>=<state>[!<code>]", sorted:
// the order inside one step is the worker's, not the test's.
func (f *fakeLeakAUB) since(mark int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, post := range f.posts[mark:] {
		line := post.RequestID[:1] + "=" + post.Status.State
		if post.Code != http.StatusOK {
			line += fmt.Sprintf("!%d", post.Code)
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// holds is what AUB holds now, every request, as "<id letter>=<state>".
func (f *fakeLeakAUB) holds() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for id, status := range f.stored {
		out = append(out, id[:1]+"="+status.State)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func new522Sender(t *testing.T) (*leakSender, *fakeLeakAUB, *testClock) {
	t.Helper()
	clock, fake := newTestClock(), newFakeLeakAUB(t)
	sender := newLeakSender(clock, fake.session, t.Logf)
	sender.manual = true
	t.Cleanup(sender.close)
	return sender, fake, clock
}

// watch is the page's poll exactly as handleLeakTestRequest makes it: every
// request not held is retired from the refresh, then each held one is said
// `received` (a no-op for one already told).
func watch(sender *leakSender, held ...aub.LeakTestLink) {
	set := map[string]bool{}
	for _, link := range held {
		set[link.RequestID] = true
	}
	sender.nothingPending(set)
	for _, link := range held {
		sender.report(link, leakReceived, "", "", true)
	}
}

func expectPosts(t *testing.T, fake *fakeLeakAUB, mark int, want, what string) {
	t.Helper()
	if got := fake.since(mark); got != want {
		t.Fatalf("%s: AUB was sent %q, want %q", what, got, want)
	}
}

func expectHolds(t *testing.T, fake *fakeLeakAUB, want, what string) {
	t.Helper()
	if got := fake.holds(); got != want {
		t.Fatalf("%s: AUB holds %q, want %q", what, got, want)
	}
}

func TestEveryPendingLeakRequestKeepsItsOwnRefresh(t *testing.T) {
	sender, fake, clock := new522Sender(t)
	a, b := testLeakLink("a"), testLeakLink("b")

	// A arrives and is reviewed.
	watch(sender, a)
	sender.step()
	sender.report(a, leakReviewing, "", "", false)
	sender.step()
	expectHolds(t, fake, "a=reviewing", "A reviewed")
	sender.mu.Lock()
	aNext := sender.items[a.RequestID].next
	sender.mu.Unlock()

	// B arrives, and is reviewed too. Neither touches A's refresh deadline.
	clock.advance(5 * time.Second)
	watch(sender, a, b)
	sender.step()
	clock.advance(5 * time.Second)
	sender.report(b, leakReviewing, "", "", false)
	sender.step()
	sender.mu.Lock()
	aAfter, aSuperseded := sender.items[a.RequestID].next, sender.items[a.RequestID].superseded
	sender.mu.Unlock()
	expectHolds(t, fake, "a=reviewing b=reviewing", "B reviewed")

	// Past the normal refresh both are said again, each under its own id.
	mark := fake.mark()
	clock.advance(leakStatusRefresh)
	watch(sender, a, b)
	sender.step()
	expectPosts(t, fake, mark, "a=reviewing b=reviewing", "the first refresh with two pending")
	if !aAfter.Equal(aNext) || aSuperseded {
		t.Fatalf("receiving and reviewing B moved A's refresh: next %s -> %s, superseded %v", aNext, aAfter, aSuperseded)
	}

	// AUB restarts: both statuses are back within one refresh.
	fake.restart()
	expectHolds(t, fake, "", "AUB restarted")
	mark = fake.mark()
	clock.advance(leakStatusRefresh)
	sender.step()
	expectPosts(t, fake, mark, "a=reviewing b=reviewing", "the refresh after an AUB restart")
	expectHolds(t, fake, "a=reviewing b=reviewing", "after the AUB restart")

	// AUB is away, then back (and restarted): the backoff runs per request and
	// both are put back once it answers.
	fake.setDown(true)
	fake.restart()
	mark = fake.mark()
	clock.advance(leakStatusRefresh)
	sender.step()
	clock.advance(time.Second)
	sender.step()
	expectPosts(t, fake, mark, "a=reviewing!503 a=reviewing!503 b=reviewing!503 b=reviewing!503", "AUB away")
	fake.setDown(false)
	mark = fake.mark()
	clock.advance(2 * time.Second)
	sender.step()
	expectPosts(t, fake, mark, "a=reviewing b=reviewing", "AUB back")
	expectHolds(t, fake, "a=reviewing b=reviewing", "after the reconnect")
	if view := sender.view(a.RequestID); view == nil || view.State != leakRelayDelivered || view.Failures != 0 {
		t.Fatalf("A's relay after the reconnect: %+v", view)
	}

	// A is built. The watch no longer holds it; B is still pending.
	sender.report(a, leakBuilding, "compile of compile", "build-a", false)
	watch(sender, b)
	sender.step()
	mark = fake.mark()
	clock.advance(leakStatusRefresh)
	sender.step()
	expectPosts(t, fake, mark, "a=building b=reviewing", "A building, B pending")

	// A late acknowledgement: A's next POST is held up while A moves on and
	// the watch says B again. The answer lands on A's older status only.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fake.mu.Lock()
	fake.hold = func(id string, status aub.LeakStatus) {
		if id == a.RequestID && status.State == leakBuilding && status.Stage == "link of compile,link" {
			once.Do(func() { close(entered) })
			<-release
		}
	}
	fake.mu.Unlock()
	sender.report(a, leakBuilding, "link of compile,link", "build-a", false)
	stepped := make(chan struct{})
	go func() { sender.step(); close(stepped) }()
	<-entered
	bPosts := fake.mark()
	sender.report(a, leakReturning, "", "build-a", false)
	watch(sender, b) // a duplicate `received` for B: nothing to send
	close(release)
	<-stepped
	fake.mu.Lock()
	fake.hold = nil
	fake.mu.Unlock()
	expectPosts(t, fake, bPosts, "a=building a=returning", "the late ack")
	expectHolds(t, fake, "a=returning b=reviewing", "after the late ack")
	if view := sender.view(a.RequestID); view.Delivered != leakReturning || view.Desired != leakReturning {
		t.Fatalf("A's relay after the late ack: %+v", view)
	}
	if view := sender.view(b.RequestID); view.Delivered != leakReviewing {
		t.Fatalf("B's relay after A's late ack: %+v", view)
	}
	// The same status said again is not sent again.
	mark = fake.mark()
	sender.report(a, leakReturning, "", "build-a", false)
	sender.step()
	expectPosts(t, fake, mark, "", "a duplicate status")

	// A's terminal status goes to A, and only B is refreshed after it.
	sender.report(a, leakReturned, "", "build-a", false)
	sender.step()
	expectHolds(t, fake, "a=returned b=reviewing", "A returned")
	mark = fake.mark()
	clock.advance(leakStatusRefresh)
	sender.step()
	expectPosts(t, fake, mark, "b=reviewing", "the refresh after A returned")

	// B is dismissed: consumed from the store, told once, never refreshed.
	watch(sender)
	sender.report(b, leakDismissed, "", "", false)
	sender.step()
	expectHolds(t, fake, "a=returned b=dismissed", "B dismissed")
	mark = fake.mark()
	clock.advance(leakStatusRefresh)
	if _, scheduled := sender.step(); scheduled {
		t.Fatal("something is still scheduled with A returned and B dismissed")
	}
	expectPosts(t, fake, mark, "", "the refresh after both ended")
}

// One request expires while another is still pending: only the pending one
// keeps its refresh.
func TestAnExpiredLeakRequestStopsAloneWhileAnotherStaysPending(t *testing.T) {
	sender, fake, clock := new522Sender(t)
	c, d := testLeakLink("c"), testLeakLink("d")
	watch(sender, c, d)
	sender.step()
	mark := fake.mark()
	clock.advance(leakStatusRefresh)
	watch(sender, c, d)
	sender.step()
	expectPosts(t, fake, mark, "c=received d=received", "two received")
	// C's ten minutes ran out in the store; D is still held.
	watch(sender, d)
	mark = fake.mark()
	clock.advance(leakStatusRefresh)
	sender.step()
	expectPosts(t, fake, mark, "d=received", "C expired")
}

// A Companion restarted with two requests in its store rebuilds the sender
// from one watch: both are kept alive, not only the last one listed.
func TestRehydratedLeakRequestsAreAllRefreshed(t *testing.T) {
	sender, fake, clock := new522Sender(t)
	a, b := testLeakLink("a"), testLeakLink("b")
	watch(sender, a, b)
	sender.step()
	expectHolds(t, fake, "a=received b=received", "after the first watch")
	fake.restart()
	mark := fake.mark()
	clock.advance(leakStatusRefresh)
	sender.step()
	expectPosts(t, fake, mark, "a=received b=received", "the refresh after rehydration and an AUB restart")
}

// The refresh of unbuilt requests stays inside the store's own limit: past
// leakintent.PendingLimit, the oldest stops being refreshed, as the store
// drops it.
func TestUnbuiltLeakRefreshIsBoundedByThePendingStore(t *testing.T) {
	sender, fake, clock := new522Sender(t)
	var links []aub.LeakTestLink
	for i := 0; i <= leakintent.PendingLimit; i++ {
		link := testLeakLink("a")
		link.RequestID = fmt.Sprintf("%032x", i+1)
		links = append(links, link)
		sender.report(link, leakReceived, "", "", true)
		clock.advance(time.Millisecond)
	}
	sender.step()
	mark := fake.mark()
	clock.advance(leakStatusRefresh)
	sender.step()
	fake.mu.Lock()
	refreshed := map[string]bool{}
	for _, post := range fake.posts[mark:] {
		refreshed[post.RequestID] = true
	}
	fake.mu.Unlock()
	if len(refreshed) != leakintent.PendingLimit || refreshed[links[0].RequestID] {
		t.Fatalf("%d requests refreshed (oldest included: %v), want the newest %d", len(refreshed),
			refreshed[links[0].RequestID], leakintent.PendingLimit)
	}
}
