package web

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/hostgame"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// --- the decision, on a clock the test turns -------------------------------

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newClockedLifecycle(interactive bool, notify func(string)) (*Lifecycle, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	return NewLifecycle(LifecycleOptions{
		Interactive:   interactive,
		CloseGrace:    15 * time.Second,
		StartupWindow: 3 * time.Minute,
		Now:           clock.Now,
		Notify:        notify,
	}), clock
}

func noWork() []ActiveWork { return nil }

func TestClosingTheOnlyPageStopsAfterTheGraceAndNotBefore(t *testing.T) {
	l, clock := newClockedLifecycle(true, nil)
	release, _ := l.acquire(func(ExitCause) {})
	clock.Advance(time.Hour)
	if _, stop := l.decide(noWork); stop {
		t.Fatal("stopped while a page was open")
	}
	release()
	clock.Advance(14 * time.Second)
	if _, stop := l.decide(noWork); stop {
		t.Fatal("stopped inside the grace period")
	}
	clock.Advance(2 * time.Second)
	if cause, stop := l.decide(noWork); !stop || cause != ExitUIClosed {
		t.Fatalf("after the grace: stop=%v cause=%q, want ui_closed", stop, cause)
	}
}

func TestAReloadInsideTheGraceDoesNotStop(t *testing.T) {
	l, clock := newClockedLifecycle(true, nil)
	release, _ := l.acquire(func(ExitCause) {})
	release()
	clock.Advance(2 * time.Second) // the new page's lease arrives
	second, _ := l.acquire(func(ExitCause) {})
	clock.Advance(time.Minute)
	if _, stop := l.decide(noWork); stop {
		t.Fatal("a reload stopped the Companion")
	}
	second()
	clock.Advance(16 * time.Second)
	if cause, stop := l.decide(noWork); !stop || cause != ExitUIClosed {
		t.Fatalf("closing the reloaded page: stop=%v cause=%q", stop, cause)
	}
}

func TestTwoPagesClosingOneDoesNothingClosingTheLastStops(t *testing.T) {
	l, clock := newClockedLifecycle(true, nil)
	first, _ := l.acquire(func(ExitCause) {})
	second, _ := l.acquire(func(ExitCause) {})
	first()
	first() // idempotent: a double release must not count as the second page
	clock.Advance(time.Minute)
	if _, stop := l.decide(noWork); stop {
		t.Fatal("closing one of two pages stopped the Companion")
	}
	if l.Leases() != 1 {
		t.Fatalf("leases = %d, want 1", l.Leases())
	}
	second()
	clock.Advance(16 * time.Second)
	if cause, stop := l.decide(noWork); !stop || cause != ExitUIClosed {
		t.Fatalf("closing the last page: stop=%v cause=%q", stop, cause)
	}
}

func TestServerModeNeverStopsOnItsOwn(t *testing.T) {
	l, clock := newClockedLifecycle(false, nil)
	release, _ := l.acquire(func(ExitCause) {})
	release()
	clock.Advance(24 * time.Hour)
	if _, stop := l.decide(noWork); stop {
		t.Fatal("server mode stopped because no page was open")
	}
}

func TestNoPageWithinTheStartupWindowStops(t *testing.T) {
	l, clock := newClockedLifecycle(true, nil)
	clock.Advance(2 * time.Minute)
	if _, stop := l.decide(noWork); stop {
		t.Fatal("stopped inside the startup window")
	}
	clock.Advance(time.Minute + time.Second)
	if cause, stop := l.decide(noWork); !stop || cause != ExitStartupTimeout {
		t.Fatalf("stop=%v cause=%q, want startup_timeout", stop, cause)
	}
}

func TestRunningWorkKeepsItAliveSaysSoOnceAndStopsWhenItEnds(t *testing.T) {
	var notices []string
	l, clock := newClockedLifecycle(true, func(summary string) { notices = append(notices, summary) })
	release, _ := l.acquire(func(ExitCause) {})
	release()
	clock.Advance(20 * time.Second)

	work := []ActiveWork{{Kind: "job", ID: "j1", Label: "Compile — qbsp", State: "running"}}
	busy := func() []ActiveWork { return work }
	for i := 0; i < 5; i++ {
		if _, stop := l.decide(busy); stop {
			t.Fatal("stopped while a compile was running")
		}
		clock.Advance(time.Second)
	}
	// The build moves to its next stage: the same wait, and no second line.
	work = []ActiveWork{{Kind: "job", ID: "j2", Label: "Compute lighting — light", State: "running"}}
	if _, stop := l.decide(busy); stop {
		t.Fatal("stopped between two stages")
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "Compile — qbsp (running)") {
		t.Fatalf("notices = %q, want one naming the compile", notices)
	}

	work = nil
	if cause, stop := l.decide(busy); !stop || cause != ExitWorkFinished {
		t.Fatalf("after the compile: stop=%v cause=%q, want work_finished", stop, cause)
	}
}

func TestAPageThatComesBackWhileWorkRunsCancelsTheWait(t *testing.T) {
	l, clock := newClockedLifecycle(true, nil)
	release, _ := l.acquire(func(ExitCause) {})
	release()
	clock.Advance(20 * time.Second)
	busy := []ActiveWork{{Kind: "build", ID: "b1", Label: "Build e1m1"}}
	l.decide(func() []ActiveWork { return busy })
	again, _ := l.acquire(func(ExitCause) {})
	busy = nil
	if _, stop := l.decide(func() []ActiveWork { return busy }); stop {
		t.Fatal("stopped with a page open")
	}
	again()
	clock.Advance(16 * time.Second)
	if cause, _ := l.decide(noWork); cause != ExitUIClosed {
		t.Fatalf("cause = %q: a page came back, so this is a close, not a work-finished", cause)
	}
}

func TestTheFirstCauseWinsAndOpenPagesAreTold(t *testing.T) {
	l, _ := newClockedLifecycle(true, nil)
	var told []ExitCause
	release, _ := l.acquire(func(cause ExitCause) { told = append(told, cause) })
	defer release()
	l.Stop(ExitQuit)
	l.Stop(ExitInterrupted)
	if l.Cause() != ExitQuit {
		t.Fatalf("cause = %q, want quit", l.Cause())
	}
	if len(told) != 1 || told[0] != ExitQuit {
		t.Fatalf("pages were told %q", told)
	}
	select {
	case <-l.Done():
	default:
		t.Fatal("Done is not closed")
	}
	if _, stopping := l.acquire(func(ExitCause) {}); !stopping {
		t.Fatal("a lease was granted after the process decided to stop")
	}
}

func TestRunStopsOnAnInterrupt(t *testing.T) {
	l := NewLifecycle(LifecycleOptions{Interactive: false, Poll: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { l.Run(ctx, noWork); close(finished) }()
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return on cancel")
	}
	if l.Cause() != ExitInterrupted {
		t.Fatalf("cause = %q", l.Cause())
	}
}

func TestEveryCauseHasItsOwnLine(t *testing.T) {
	seen := map[string]ExitCause{}
	for _, cause := range []ExitCause{ExitUIClosed, ExitQuit, ExitInterrupted, ExitStartupTimeout, ExitWorkFinished} {
		line := ExitLine(cause, 3*time.Minute)
		if !strings.HasPrefix(line, "Auto-Pigeon Companion stopped: ") {
			t.Errorf("%s: %q", cause, line)
		}
		if other, dup := seen[line]; dup {
			t.Errorf("%s and %s print the same line", cause, other)
		}
		seen[line] = cause
	}
}

// --- the lease, over a real socket -------------------------------------------

// leaseClient is the smallest WebSocket client that can hold a lease: the
// handshake a browser sends, masked frames, and a reader for the server's.
type leaseClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func dialLease(t *testing.T, address, host, origin, token string) (*leaseClient, *http.Response) {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	var key [16]byte
	rand.Read(key[:])
	protocols := leaseProtocol
	if token != "" {
		protocols += ", " + leaseTokenPrefix + token
	}
	request := "GET /api/lifecycle/lease HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key[:]) + "\r\n" +
		"Sec-WebSocket-Protocol: " + protocols + "\r\n"
	if origin != "" {
		request += "Origin: " + origin + "\r\nSec-Fetch-Site: same-origin\r\nSec-Fetch-Mode: websocket\r\n"
	}
	request += "\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, response
	}
	return &leaseClient{conn: conn, reader: reader}, response
}

func (c *leaseClient) next(t *testing.T) (byte, []byte) {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var head [2]byte
	if _, err := io.ReadFull(c.reader, head[:]); err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	if head[1]&0x80 != 0 {
		t.Fatal("the server masked a frame")
	}
	length := int(head[1] & 0x7F)
	if length == 126 {
		var extended [2]byte
		io.ReadFull(c.reader, extended[:])
		length = int(binary.BigEndian.Uint16(extended[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		t.Fatal(err)
	}
	return head[0] & 0x0F, payload
}

func (c *leaseClient) message(t *testing.T) leaseMessage {
	t.Helper()
	opcode, payload := c.next(t)
	if opcode != opText {
		t.Fatalf("opcode %#x, want a text frame", opcode)
	}
	var message leaseMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("%q: %v", payload, err)
	}
	return message
}

func (c *leaseClient) send(t *testing.T, opcode byte, payload []byte) {
	t.Helper()
	mask := [4]byte{1, 2, 3, 4}
	out := []byte{0x80 | opcode, 0x80 | byte(len(payload))}
	out = append(out, mask[:]...)
	for i, b := range payload {
		out = append(out, b^mask[i%4])
	}
	if _, err := c.conn.Write(out); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func leaseServer(t *testing.T, lifecycle *Lifecycle) (*Server, *httptest.Server) {
	t.Helper()
	server, err := NewServer(Options{Version: "test", Jobs: newTestJobs(t), Lifecycle: lifecycle})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: server}}
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

func TestALeaseIsHeldForAsLongAsItsSocketIsOpen(t *testing.T) {
	lifecycle := NewLifecycle(LifecycleOptions{Interactive: true, CloseGrace: 7 * time.Second})
	server, httpServer := leaseServer(t, lifecycle)
	address := httpServer.Listener.Addr().String()
	origin := "http://" + address

	client, response := dialLease(t, address, address, origin, server.Token().Value())
	if client == nil {
		t.Fatalf("handshake refused: %d", response.StatusCode)
	}
	if got := response.Header.Get("Sec-WebSocket-Protocol"); got != leaseProtocol {
		t.Errorf("selected subprotocol %q: the token must never be echoed", got)
	}
	hello := client.message(t)
	if hello.Type != "hello" || hello.Mode != "interactive" || hello.GraceSeconds != 7 {
		t.Fatalf("hello = %+v", hello)
	}
	waitFor(t, "the lease", func() bool { return lifecycle.Leases() == 1 })

	client.send(t, opText, []byte(`{"type":"beat"}`))
	client.send(t, opPing, []byte("p"))
	if opcode, payload := client.next(t); opcode != opPong || string(payload) != "p" {
		t.Fatalf("ping answered with %#x %q", opcode, payload)
	}

	// A crashed renderer: no close frame, the socket just goes.
	client.conn.Close()
	waitFor(t, "the lease to end", func() bool { return lifecycle.Leases() == 0 })
	if !lifecycle.EverConnected() {
		t.Error("EverConnected is false after a page connected")
	}
}

func TestALeaseNeedsThisRunsTokenAndThisOrigin(t *testing.T) {
	lifecycle := NewLifecycle(LifecycleOptions{Interactive: true})
	server, httpServer := leaseServer(t, lifecycle)
	address := httpServer.Listener.Addr().String()

	for name, attempt := range map[string]struct {
		host, origin, token string
		want                int
	}{
		"no token":            {address, "http://" + address, "", http.StatusUnauthorized},
		"an earlier run's":    {address, "http://" + address, "not-this-runs-token", http.StatusUnauthorized},
		"another site":        {address, "https://evil.example", server.Token().Value(), http.StatusForbidden},
		"a rebound host name": {"rebound.example:80", "http://rebound.example", server.Token().Value(), http.StatusForbidden},
	} {
		client, response := dialLease(t, address, attempt.host, attempt.origin, attempt.token)
		if client != nil {
			t.Errorf("%s: a lease was granted", name)
			client.conn.Close()
			continue
		}
		if response.StatusCode != attempt.want {
			t.Errorf("%s: status %d, want %d", name, response.StatusCode, attempt.want)
		}
	}
	if lifecycle.EverConnected() {
		t.Error("a refused handshake counted as a page")
	}
}

func TestTheLeaseTokenIsReadOnlyFromAnUpgrade(t *testing.T) {
	server, _ := newTestServer(t, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.Host = testHost
	r.Header.Set("Sec-WebSocket-Protocol", leaseTokenPrefix+server.Token().Value())
	if response, _ := send(t, server, r); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d: a subprotocol header authenticated an ordinary request", response.StatusCode)
	}
}

func TestQuitWithNothingRunningStopsAndTellsEveryPage(t *testing.T) {
	lifecycle := NewLifecycle(LifecycleOptions{Interactive: true})
	server, httpServer := leaseServer(t, lifecycle)
	address := httpServer.Listener.Addr().String()
	client, _ := dialLease(t, address, address, "http://"+address, server.Token().Value())
	if client == nil {
		t.Fatal("no lease")
	}
	client.message(t) // hello

	response, body := do(t, server, http.MethodPost, "/api/lifecycle/quit", `{"cancel_active":false}`)
	if response.StatusCode != http.StatusAccepted || body["stopping"] != true {
		t.Fatalf("quit: %d %v", response.StatusCode, body)
	}
	if lifecycle.Cause() != ExitQuit {
		t.Fatalf("cause = %q", lifecycle.Cause())
	}
	stopping := client.message(t)
	if stopping.Type != "stopping" || stopping.Cause != ExitQuit {
		t.Fatalf("the page was told %+v", stopping)
	}
	opcode, payload := client.next(t)
	if opcode != opClose || binary.BigEndian.Uint16(payload) != 1001 {
		t.Fatalf("close frame %#x %v, want 1001 going away", opcode, payload)
	}
}

func TestQuitWithWorkRunningAsksFirstAndCancelsOnlyWhenTold(t *testing.T) {
	lifecycle := NewLifecycle(LifecycleOptions{Interactive: true})
	server, _ := leaseServer(t, lifecycle)
	cancelled := make(chan struct{})
	run := &buildRun{ID: "20260923T120000Z-e1m1", Label: "e1m1", log: newTailBuffer(1024)}
	run.cancel = func() {
		run.finish(nil, fmt.Errorf("cancelled"))
		close(cancelled)
	}
	server.builds.put(run)

	response, body := do(t, server, http.MethodPost, "/api/lifecycle/quit", `{"cancel_active":false}`)
	if response.StatusCode != http.StatusConflict || body["code"] != codeWorkActive {
		t.Fatalf("quit with a build running: %d %v", response.StatusCode, body)
	}
	active, _ := body["active"].([]any)
	if len(active) != 1 || !strings.Contains(fmt.Sprint(active[0]), "Build e1m1") {
		t.Fatalf("active = %v", body["active"])
	}
	if lifecycle.Cause() != "" {
		t.Fatal("a refused quit stopped the process: keep running means keep running")
	}

	response, body = do(t, server, http.MethodPost, "/api/lifecycle/quit", `{"cancel_active":true}`)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("quit with cancel: %d %v", response.StatusCode, body)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the build was not cancelled")
	}
	select {
	case <-lifecycle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the process did not stop once the cancelled work had ended")
	}
	if lifecycle.Cause() != ExitQuit {
		t.Fatalf("cause = %q", lifecycle.Cause())
	}
}

func TestTheLifecycleRouteSaysTheModeAndWhatIsRunning(t *testing.T) {
	server, _ := newTestServer(t, nil)
	response, body := do(t, server, http.MethodGet, "/api/lifecycle", "")
	if response.StatusCode != http.StatusOK || body["mode"] != "server" {
		t.Fatalf("%d %v", response.StatusCode, body)
	}
	if active, ok := body["active"].([]any); ok && len(active) != 0 {
		t.Fatalf("active = %v on an idle server", active)
	}
}

// --- hosted games end with the process ---------------------------------------

type stopRecorder struct {
	mu      sync.Mutex
	stopped map[string]string
	beats   int
}

func (r *stopRecorder) PreviewHostedGame(context.Context, aub.HostedGameRegistration) (aub.HostedGamePreview, error) {
	return aub.HostedGamePreview{}, nil
}

func (r *stopRecorder) RegisterHostedGame(_ context.Context, registration aub.HostedGameRegistration) (aub.HostedGameResult, error) {
	return aub.HostedGameResult{Game: aub.HostedGame{ID: "game-1", Title: registration.Title, HeartbeatSecs: 3600}}, nil
}

func (r *stopRecorder) HeartbeatHostedGame(context.Context, string, aub.HeartbeatBody) (aub.HostedGameResult, error) {
	r.mu.Lock()
	r.beats++
	r.mu.Unlock()
	return aub.HostedGameResult{}, nil
}

func (r *stopRecorder) StopHostedGame(_ context.Context, gameID, reason string) (aub.HostedGameResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped[gameID] = reason
	return aub.HostedGameResult{}, nil
}

type runningHost struct{}

func (runningHost) Get(id string) (*job.Job, error) {
	return &job.Job{ID: id, State: job.Running, SessionRole: "dedicated_server"}, nil
}

func TestClosingTheServerEndsHostedListingsAtOnce(t *testing.T) {
	server, _ := newTestServer(t, nil)
	backend := &stopRecorder{stopped: map[string]string{}}
	// The next beat is an hour away and the job never ends: if the listing
	// ends, it is Close that ended it and not the loop noticing on its own.
	advertiser := hostgame.New(backend, runningHost{})
	if _, err := advertiser.Start(context.Background(), aub.HostedGameRegistration{
		Title: "e1m1", ConfirmExposure: true,
	}, hostgame.StartOptions{JobID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	server.hosting.mu.Lock()
	server.hosting.advertiser = advertiser
	server.hosting.mu.Unlock()

	started := time.Now()
	server.Close()
	elapsed := time.Since(started)

	backend.mu.Lock()
	reason := backend.stopped["game-1"]
	backend.mu.Unlock()
	if reason != aub.ReasonHostStopped {
		t.Fatalf("the listing ended with %q, want %q", reason, aub.ReasonHostStopped)
	}
	if elapsed > 2*time.Second {
		t.Errorf("ending the listing took %s; it must not wait for a beat", elapsed)
	}
	if len(advertiser.Active()) != 0 {
		t.Error("the advertisement is still active after Close")
	}
}
