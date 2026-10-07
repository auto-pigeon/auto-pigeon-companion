package cli

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/web"
)

// These run the real `serve` in-process, hold leases over real WebSockets the
// way a page does, and watch the process decide to stop. They need no signal,
// so they run on every platform — including the native Windows runner, where
// the older SIGTERM-driven serve test is skipped.

// syncBuffer is a bytes.Buffer several goroutines may write: the lifecycle's
// notice, the shutdown line and the test's reads.
type syncBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes []string      // every Write, as written: a notice split across two is visible
	wrote  chan struct{} // closed and replaced on every Write, so a reader can wait for one
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, string(p))
	if b.wrote != nil {
		close(b.wrote)
	}
	b.wrote = make(chan struct{})
	return b.buf.Write(p)
}

// waitFor returns true once the buffer contains want, woken by each Write —
// never by a fixed sleep — or false when the deadline passes first.
func (b *syncBuffer) waitFor(want string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		b.mu.Lock()
		if strings.Contains(b.buf.String(), want) {
			b.mu.Unlock()
			return true
		}
		if b.wrote == nil {
			b.wrote = make(chan struct{})
		}
		wrote := b.wrote
		b.mu.Unlock()
		select {
		case <-wrote:
		case <-deadline:
			return false
		}
	}
}

func (b *syncBuffer) firstWrite() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.writes) == 0 {
		return ""
	}
	return b.writes[0]
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type servedCompanion struct {
	t       *testing.T
	env     *Env
	stdout  *syncBuffer
	stderr  *syncBuffer
	opened  chan string
	done    chan int
	address string // host:port
	token   string
}

// startServe runs `companion <args>` and waits until it has asked for the
// browser — the moment its address and token exist.
func startServe(t *testing.T, args []string, openErr error) *servedCompanion {
	t.Helper()
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	env := &Env{
		Stdout:     stdout,
		Stderr:     stderr,
		Version:    "1.500",
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Lookenv:    func(string) (string, bool) { return "", false },
		// --interactive registers the link handler at first use: never this
		// machine's own.
		URIRegistrar: isolatedRegistrar(t),
	}
	s := &servedCompanion{t: t, env: env, stdout: stdout, stderr: stderr,
		opened: make(chan string, 1), done: make(chan int, 1)}
	env.OpenBrowser = func(page string) error {
		s.opened <- page
		return openErr
	}
	go func() { s.done <- Run(env, args) }()
	select {
	case page := <-s.opened:
		parsed, err := url.Parse(page)
		if err != nil {
			t.Fatal(err)
		}
		s.address = parsed.Host
	case code := <-s.done:
		t.Fatalf("exited %d before opening a browser; stdout %q stderr %q", code, stdout.String(), stderr.String())
	case <-time.After(15 * time.Second):
		t.Fatalf("the browser was never opened; stderr %q", stderr.String())
	}
	token, err := web.ReadToken(web.TokenPath(filepath.Dir(env.ConfigPath)))
	if err != nil {
		t.Fatal(err)
	}
	s.token = token
	// Startup is announced when its notice is on the terminal. Opening the
	// browser happens BEFORE that notice is written, so the opener firing is not
	// the boundary; the notice's own last line is, and it arrives in one write.
	if slices.Contains(args, "--interactive") && !stdout.waitFor(startupNoticeEnd, 15*time.Second) {
		t.Fatalf("startup was never announced; stdout %q", stdout.String())
	}
	t.Cleanup(func() {
		select {
		case <-s.done:
		default:
			// Whatever the test did, never leave a server running behind it.
			s.quit(true)
			s.wait(20 * time.Second)
		}
	})
	return s
}

// lease opens one page's lease and reads its hello.
func (s *servedCompanion) lease() net.Conn {
	s.t.Helper()
	conn, err := net.Dial("tcp", s.address)
	if err != nil {
		s.t.Fatal(err)
	}
	var key [16]byte
	rand.Read(key[:])
	request := "GET /api/lifecycle/lease HTTP/1.1\r\nHost: " + s.address + "\r\n" +
		"Origin: http://" + s.address + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key[:]) + "\r\n" +
		"Sec-WebSocket-Protocol: aucom.lease.v1, aucom.token." + s.token + "\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		s.t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		s.t.Fatalf("lease refused: %v %v", err, response)
	}
	// The hello frame: a small text frame.
	var head [2]byte
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(reader, head[:]); err != nil {
		s.t.Fatal(err)
	}
	payload := make([]byte, head[1]&0x7F)
	io.ReadFull(reader, payload)
	if !strings.Contains(string(payload), `"hello"`) {
		s.t.Fatalf("first frame %q is not the hello", payload)
	}
	conn.SetReadDeadline(time.Time{})
	return conn
}

func (s *servedCompanion) api(method, path, body string) (int, map[string]any) {
	s.t.Helper()
	request, err := http.NewRequest(method, "http://"+s.address+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	request.Header.Set("X-AUCOM-Token", s.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil
	}
	defer response.Body.Close()
	decoded := map[string]any{}
	json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

func (s *servedCompanion) leases() int {
	s.t.Helper()
	status, body := s.api(http.MethodGet, "/api/lifecycle", "")
	if status != http.StatusOK {
		s.t.Fatalf("GET /api/lifecycle: %d", status)
	}
	return int(body["leases"].(float64))
}

func (s *servedCompanion) quit(cancel bool) (int, map[string]any) {
	body := `{"cancel_active":false}`
	if cancel {
		body = `{"cancel_active":true}`
	}
	return s.api(http.MethodPost, "/api/lifecycle/quit", body)
}

func (s *servedCompanion) wait(limit time.Duration) (int, bool) {
	select {
	case code := <-s.done:
		return code, true
	case <-time.After(limit):
		return 0, false
	}
}

func (s *servedCompanion) stillRunning(after time.Duration) bool {
	select {
	case code := <-s.done:
		s.done <- code
		return false
	case <-time.After(after):
		return true
	}
}

// assertGone checks nothing is left: the listener is closed and the token and
// address files are removed.
func (s *servedCompanion) assertGone() {
	s.t.Helper()
	if conn, err := net.DialTimeout("tcp", s.address, time.Second); err == nil {
		conn.Close()
		s.t.Errorf("something still listens on %s after the Companion stopped", s.address)
	}
	dir := filepath.Dir(s.env.ConfigPath)
	for _, name := range []string{web.TokenFileName, web.URLFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			s.t.Errorf("%s survived the shutdown", name)
		}
	}
}

func TestClosingTheOnlyPageStopsAnInteractiveCompanion(t *testing.T) {
	s := startServe(t, []string{"serve", "--interactive", "--open", "--close-grace", "400ms"}, nil)
	page := s.lease()
	if n := s.leases(); n != 1 {
		t.Fatalf("leases = %d", n)
	}
	closed := time.Now()
	page.Close()
	code, stopped := s.wait(15 * time.Second)
	if !stopped {
		t.Fatalf("still running 15s after its only page closed; stdout %q", s.stdout.String())
	}
	if code != 0 {
		t.Errorf("exit code %d", code)
	}
	if waited := time.Since(closed); waited < 400*time.Millisecond {
		t.Errorf("stopped %s after the close: inside the grace period", waited)
	}
	want := "Auto-Pigeon Companion 1.500 is open in your browser.\n" +
		"Close its last window to stop it, or press Ctrl+C.\n" +
		"Auto-Pigeon Companion stopped: its last browser window was closed.\n"
	if got := s.stdout.String(); got != want {
		t.Errorf("the terminal said\n%q\nwant\n%q", got, want)
	}
	if strings.TrimSpace(s.stderr.String()) != "" {
		t.Errorf("stderr is not quiet in interactive mode: %q", s.stderr.String())
	}
	logged, err := os.ReadFile(filepath.Join(filepath.Dir(s.env.ConfigPath), DetailLogName))
	if err != nil || !strings.Contains(string(logged), "listening on http://") || !strings.Contains(string(logged), "cause ui_closed") {
		t.Errorf("the detail log does not hold the server's lines: %v %q", err, logged)
	}
	// NEW_254: the lines that answer "why did it not stop" — which process and
	// mode, which opener was asked, each lease and why it ended, the grace, the
	// decision, and how long each shutdown phase took.
	for _, want := range []string{
		"lifecycle: interactive, pid ", "close grace 400ms, startup window 3m0s",
		"browser: asked the system to open http://", " with ",
		"lifecycle: page lease 0 opened (port ", "; 1 page(s) open",
		"lifecycle: page lease 0 closed after ", "; 0 page(s) open",
		"lifecycle: no page is open; stopping in 400ms unless a page opens or work is running",
		"lifecycle: stop decided, cause ui_closed",
		"shutdown: builds, Build & Run and hosted listings ended ",
		"shutdown: jobs closed ", "printing the exit line and exiting",
	} {
		if !strings.Contains(string(logged), want) {
			t.Errorf("the detail log does not say %q:\n%s", want, logged)
		}
	}
	if strings.Contains(string(logged), s.token) {
		t.Error("the detail log carries the API token")
	}
	s.assertGone()
}

func TestAReloadDoesNotStopItAndTwoTabsNeedTheLastToClose(t *testing.T) {
	s := startServe(t, []string{"serve", "--interactive", "--open", "--close-grace", "1500ms"}, nil)

	// A reload: the old page's lease goes and the new one's arrives.
	first := s.lease()
	first.Close()
	time.Sleep(200 * time.Millisecond)
	reloaded := s.lease()
	if !s.stillRunning(2500 * time.Millisecond) {
		t.Fatalf("a reload stopped the Companion; stdout %q", s.stdout.String())
	}

	// Two tabs: closing one does nothing.
	second := s.lease()
	reloaded.Close()
	if !s.stillRunning(2500 * time.Millisecond) {
		t.Fatal("closing one of two tabs stopped the Companion")
	}
	if n := s.leases(); n != 1 {
		t.Fatalf("leases = %d after closing one of two", n)
	}
	second.Close()
	if _, stopped := s.wait(15 * time.Second); !stopped {
		t.Fatal("closing the last tab did not stop the Companion")
	}
	s.assertGone()
}

func TestStayRunningSurvivesEveryPageClosingAndQuitStopsIt(t *testing.T) {
	s := startServe(t, []string{"--stay-running"}, nil)
	page := s.lease()
	page.Close()
	// Server mode has no grace to wait out; well beyond the default one would
	// be a slow test, so this checks it has not stopped and that the lease
	// count went to zero.
	if !s.stillRunning(1500 * time.Millisecond) {
		t.Fatal("--stay-running stopped when its page closed")
	}
	if n := s.leases(); n != 0 {
		t.Fatalf("leases = %d", n)
	}
	if status, body := s.quit(false); status != http.StatusAccepted {
		t.Fatalf("quit: %d %v", status, body)
	}
	code, stopped := s.wait(15 * time.Second)
	if !stopped || code != 0 {
		t.Fatalf("Quit did not stop a --stay-running Companion (stopped %v, code %d)", stopped, code)
	}
	if !strings.HasSuffix(s.stdout.String(), "stopped: Quit was chosen in the page\n") {
		t.Errorf("stdout %q", s.stdout.String())
	}
	s.assertGone()
}

func TestQuitWithNothingRunningStopsAnInteractiveCompanionAtOnce(t *testing.T) {
	s := startServe(t, []string{"serve", "--interactive", "--open"}, nil)
	page := s.lease()
	defer page.Close()
	asked := time.Now()
	if status, body := s.quit(false); status != http.StatusAccepted {
		t.Fatalf("quit: %d %v", status, body)
	}
	if _, stopped := s.wait(15 * time.Second); !stopped {
		t.Fatal("Quit did not stop it")
	}
	// Not the 15 s close grace: Quit is a decision, not an absence.
	if waited := time.Since(asked); waited > 5*time.Second {
		t.Errorf("Quit took %s", waited)
	}
	if !strings.HasSuffix(s.stdout.String(), "Auto-Pigeon Companion stopped: Quit was chosen in the page.\n") {
		t.Errorf("stdout %q", s.stdout.String())
	}
	s.assertGone()
}

func TestNoPageWithinTheStartupWindowStopsWithAReadableLine(t *testing.T) {
	s := startServe(t, []string{"serve", "--interactive", "--open", "--startup-window", "500ms"}, nil)
	if _, stopped := s.wait(15 * time.Second); !stopped {
		t.Fatal("still running with no page after its startup window")
	}
	out := s.stdout.String()
	if !strings.Contains(out, "no browser opened its page within 500ms") || !strings.Contains(out, "Details: ") {
		t.Errorf("stdout %q", out)
	}
	s.assertGone()
}

const startupNoticeEnd = "Close its last window to stop it, or press Ctrl+C.\n"

func TestABrowserThatCannotOpenPrintsTheAddressOnceAndWaits(t *testing.T) {
	s := startServe(t, []string{"serve", "--interactive", "--open", "--startup-window", "3s"},
		os.ErrNotExist)
	out := s.stdout.String()
	address := "http://" + s.address + "/"
	if !strings.Contains(out, "could not open your browser") || strings.Count(out, address) != 1 {
		t.Fatalf("stdout %q: want the failure said and %s printed once", out, address)
	}
	// One write: the failure, the one address, the window and how to stop it.
	first := s.stdout.firstWrite()
	for _, want := range []string{"Auto-Pigeon Companion 1.500 could not open your browser.\n", address, "within 3s", startupNoticeEnd} {
		if !strings.Contains(first, want) {
			t.Fatalf("the first write %q lacks %q: the notice was split", first, want)
		}
	}
	// Somebody pastes the address: the lease makes it a normal session.
	page := s.lease()
	if !s.stillRunning(3500 * time.Millisecond) {
		t.Fatal("stopped at the end of the startup window although a page had connected")
	}
	page.Close()
	if _, stopped := s.wait(30 * time.Second); !stopped {
		t.Fatal("did not stop after its page closed")
	}
}

func TestTheTwoModesAreExclusive(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"serve", "--interactive", "--stay-running"}); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "choose one") {
		t.Errorf("stderr %q", stderr.String())
	}
}

func TestTheNoSubcommandLaunchIsInteractiveAndStayRunningIsServerMode(t *testing.T) {
	cases := map[string][]string{
		"":                       {"--open", "--interactive"},
		"--debug":                {"--open", "--debug", "--interactive"},
		"--stay-running":         {"--open", "--stay-running"},
		"--stay-running --debug": {"--open", "--debug", "--stay-running"},
		"--debug --stay-running": {"--open", "--debug", "--stay-running"},
	}
	for given, want := range cases {
		args := strings.Fields(given)
		got, ok := guiArguments(args)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("companion %s → serve %v (%v), want %v", given, got, ok, want)
		}
	}
	for _, notGUI := range [][]string{{"serve"}, {"--port", "1"}, {"--help"}, {"--debug", "version"}} {
		if _, ok := guiArguments(notGUI); ok {
			t.Errorf("%v was taken as the GUI launch", notGUI)
		}
	}
}

func TestTheStartupNoticeIsCompleteAndNamesTheAddressOnce(t *testing.T) {
	const url = "http://127.0.0.1:8789/"
	cases := []struct {
		name                string
		opened, triedToOpen bool
		want                []string
		address             int
	}{
		{"opened", true, true, []string{"is open in your browser.\n", startupNoticeEnd}, 0},
		{"could not open", false, true, []string{"could not open your browser.\n", "Open " + url + " within 2m0s. ", startupNoticeEnd}, 1},
		{"not asked to open", false, false, []string{"is running at " + url + "\n", "Open it within 2m0s. ", startupNoticeEnd}, 1},
	}
	for _, c := range cases {
		got := startupNotice("1.500", url, c.opened, c.triedToOpen, 2*time.Minute)
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q lacks %q", c.name, got, want)
			}
		}
		if n := strings.Count(got, url); n != c.address {
			t.Errorf("%s: the address appears %d times in %q, want %d", c.name, n, got, c.address)
		}
		if !strings.HasSuffix(got, startupNoticeEnd) {
			t.Errorf("%s: %q does not end with the exit guidance", c.name, got)
		}
	}
}

// A second launch while the Companion is open shows the running one and
// starts nothing: it does not truncate the first one's log, replace its
// token, or listen beside it (NEW_310, found on Windows — a double-click while
// a Companion was already open started a second on a fallback port, and the
// shared log came out with a hole of NULs).
func TestASecondLaunchShowsTheRunningCompanionInsteadOfStartingAnother(t *testing.T) {
	first := startServe(t, []string{"serve", "--interactive", "--open"}, nil)
	first.lease()
	dir := filepath.Dir(first.env.ConfigPath)
	logPath := filepath.Join(dir, DetailLogName)
	before, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(before), "listening on http://") {
		t.Fatalf("the first Companion's log: %v %q", err, before)
	}

	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	opened := make(chan string, 1)
	second := &Env{Stdout: stdout, Stderr: stderr, Version: "1.501", ConfigPath: first.env.ConfigPath,
		Lookenv: func(string) (string, bool) { return "", false }, URIRegistrar: isolatedRegistrar(t),
		OpenBrowser: func(page string) error { opened <- page; return nil }}
	done := make(chan int, 1)
	go func() { done <- Run(second, []string{"serve", "--interactive", "--open", "--open-area=build"}) }()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("the second launch exited %d; stderr %q", code, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the second launch is still running: it started a second Companion")
	}
	want := "http://" + first.address + "/#build"
	select {
	case page := <-opened:
		if page != want {
			t.Errorf("the second launch opened %q, want the running one's %q", page, want)
		}
	default:
		t.Error("the second launch opened no page")
	}
	if !strings.Contains(stdout.String(), "Auto-Pigeon Companion 1.500 is already open at "+want) ||
		!strings.Contains(stdout.String(), "This launch (1.501) started nothing") {
		t.Errorf("the second launch did not say which build answers: %q", stdout.String())
	}
	if token, err := web.ReadToken(web.TokenPath(dir)); err != nil || token != first.token {
		t.Errorf("the running Companion's token was replaced: %v", err)
	}
	after, _ := os.ReadFile(logPath)
	if !strings.HasPrefix(string(after), string(before)) || strings.Contains(string(after), "1.501") {
		t.Errorf("the running Companion's log was truncated or written by the second launch:\n%q", after)
	}
	if status, _ := first.api(http.MethodGet, "/api/status", ""); status != http.StatusOK {
		t.Errorf("the running Companion stopped answering: %d", status)
	}
}

// Launches a moment apart, from cold, end with exactly one Companion for the
// configuration (NEW_310A): the check for a running one and the start are one
// critical section under the per-configuration start lock, so the launches
// that lose the race find the winner rather than both starting. One server,
// one log with one start in it and no hole, one token, and every launcher is
// shown that one page.
func TestLaunchesAMomentApartStartOneCompanion(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")
	const launches = 4
	type launch struct {
		stdout, stderr *syncBuffer
		opened         chan string
		done           chan int
	}
	all := make([]*launch, launches)
	start := make(chan struct{})
	for i := range all {
		l := &launch{stdout: &syncBuffer{}, stderr: &syncBuffer{}, opened: make(chan string, 1), done: make(chan int, 1)}
		all[i] = l
		env := &Env{Stdout: l.stdout, Stderr: l.stderr, Version: fmt.Sprintf("1.50%d", i), ConfigPath: config,
			Lookenv: func(string) (string, bool) { return "", false }, URIRegistrar: isolatedRegistrar(t),
			OpenBrowser: func(page string) error { l.opened <- page; return nil }}
		go func() {
			<-start
			l.done <- Run(env, []string{"serve", "--interactive", "--open"})
		}()
	}
	close(start)

	// Every launcher opens a page; all but one then exit.
	pages := map[string]bool{}
	for i, l := range all {
		select {
		case page := <-l.opened:
			pages[page] = true
		case <-time.After(30 * time.Second):
			t.Fatalf("launch %d opened no page; stdout %q stderr %q", i, l.stdout.String(), l.stderr.String())
		}
	}
	if len(pages) != 1 {
		t.Fatalf("the launches were shown %d different Companions: %v", len(pages), pages)
	}
	running := -1
	deadline := time.After(30 * time.Second)
	for exited := 0; exited < launches-1; {
		select {
		case <-deadline:
			t.Fatalf("%d of %d launches are still running: more than one Companion started", launches-exited, launches)
		default:
		}
		exited = 0
		running = -1
		for i, l := range all {
			select {
			case code := <-l.done:
				l.done <- code
				exited++
				if code != 0 {
					t.Fatalf("launch %d exited %d; stderr %q", i, code, l.stderr.String())
				}
			default:
				running = i
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if running < 0 {
		t.Fatal("no launch is running a Companion")
	}

	dir := filepath.Dir(config)
	logged, err := os.ReadFile(filepath.Join(dir, DetailLogName))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(logged), "listening on http://"); n != 1 {
		t.Errorf("the log records %d starts, want 1:\n%s", n, logged)
	}
	if strings.ContainsRune(string(logged), 0) {
		t.Errorf("the log has a hole of NULs: two writers truncated it")
	}
	token, err := web.ReadToken(web.TokenPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	address, err := web.ReadURL(web.URLPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, strings.TrimRight(address, "/")+"/api/lifecycle/quit",
		strings.NewReader(`{"cancel_active":false}`))
	request.Header.Set("X-AUCOM-Token", token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("the one Companion does not answer with the published token: %v", err)
	}
	response.Body.Close()
	select {
	case <-all[running].done:
	case <-time.After(20 * time.Second):
		t.Error("the one Companion did not stop on Quit")
	}
}
