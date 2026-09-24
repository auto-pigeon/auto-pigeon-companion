package cli

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
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

func TestABrowserThatCannotOpenPrintsTheAddressOnceAndWaits(t *testing.T) {
	s := startServe(t, []string{"serve", "--interactive", "--open", "--startup-window", "3s"},
		os.ErrNotExist)
	out := s.stdout.String()
	address := "http://" + s.address + "/"
	if !strings.Contains(out, "could not open your browser") || strings.Count(out, address) != 1 {
		t.Fatalf("stdout %q: want the failure said and %s printed once", out, address)
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
