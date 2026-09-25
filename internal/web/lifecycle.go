package web

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// The Companion's process lifecycle: who decides that it stops.
//
// # Two modes
//
// A person who starts the Companion from a desktop icon or a download opens one
// program and expects closing it to close it. That is INTERACTIVE mode, the
// no-subcommand launch: the process lives while a page it served is open, and
// stops once the last one has been gone for [DefaultCloseGrace].
//
// `companion serve` is SERVER mode, and it is what every script, harness and
// operator already starts: no browser is attached, none is expected, and it
// stops only when it is interrupted or somebody chooses Quit in the page.
// `--stay-running` asks for server mode by name.
//
// # Why a lease, and not `beforeunload`
//
// An unload event cannot tell a reload from a close, and it never fires for a
// renderer that crashed or a machine that slept. So nothing here listens for
// one. Each open page holds a LEASE instead — a WebSocket authenticated with
// this run's API token (lease.go) — and the process counts leases. A reload
// drops one and opens the next a second later; a crash drops one and the tab's
// "Reload" opens the next; a close drops the last one and nothing replaces it.
// Only the last of those outlasts the grace period, and that is the whole
// decision.
//
// The token is the one this server already mints per run and already puts in
// the page (auth.go). A launch-scoped cookie exchanged from a one-time URL value
// would be a second local authentication system with an ambient credential in
// it, which is precisely what auth.go was written to avoid. A page from an
// earlier run carries an earlier token, its lease is refused, and it shows the
// "restarted since this page was opened" banner the page already has.
//
// # Work outlives the page
//
// A compile, a Build & Run, a join download or a running game is not stopped by
// closing a tab. While any is active the process stays, says once in the
// terminal what it is waiting for, and stops when the last one ends — unless a
// page came back in the meantime, in which case nothing has closed.

// DefaultCloseGrace is how long an interactive Companion waits after its last
// page went away. Long enough for a reload, a crashed tab's own Reload button
// and a browser restoring its session; short enough that closing the window
// visibly closes the program.
const DefaultCloseGrace = 15 * time.Second

// DefaultStartupWindow is how long an interactive Companion waits for its first
// page. A cold browser start takes seconds; somebody copying a printed URL
// because no browser opened takes longer. After this, a Companion nobody is
// using stops rather than sitting in the background for ever.
const DefaultStartupWindow = 3 * time.Minute

// lifecyclePoll is how often the decision is re-taken. The decision reads
// memory only (see [Server.activeWork]), so a second is cheap and bounds how
// late a stop can be by one second.
const lifecyclePoll = time.Second

// ExitCause is why a Companion stopped, as the one causal line says it.
type ExitCause string

const (
	// ExitUIClosed: interactive, the last page closed, nothing was running.
	ExitUIClosed ExitCause = "ui_closed"
	// ExitQuit: somebody chose Quit Auto-Pigeon Companion in a page.
	ExitQuit ExitCause = "quit"
	// ExitInterrupted: Ctrl+C, SIGTERM, or a Windows console close.
	ExitInterrupted ExitCause = "interrupted"
	// ExitStartupTimeout: interactive, and no page ever connected.
	ExitStartupTimeout ExitCause = "startup_timeout"
	// ExitWorkFinished: interactive, the page had closed, and the work that
	// kept the process alive has ended.
	ExitWorkFinished ExitCause = "work_finished"
)

// ActiveWork is one thing that keeps a Companion running after its page closed,
// as the page's Quit dialog and the terminal notice name it.
type ActiveWork struct {
	// Kind is job, build, build_and_run, hosted_game, game or join_download.
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state,omitempty"`
}

// LifecycleOptions configures a [Lifecycle].
type LifecycleOptions struct {
	// Interactive is application mode. False is server mode, which never stops
	// on its own.
	Interactive bool
	// CloseGrace and StartupWindow default to [DefaultCloseGrace] and
	// [DefaultStartupWindow].
	CloseGrace    time.Duration
	StartupWindow time.Duration
	// Notify receives what keeps the process alive after its page closed, as
	// one sentence fragment, once each time such a wait begins. The caller words the
	// terminal line, because only the caller knows the address to name in it.
	// Nil discards it.
	Notify func(summary string)
	// Logf receives the lifecycle's own account of itself — each page lease
	// opened and closed with the count left, the grace period starting, the
	// stop decided — for the detail log. It is what a person reads when the
	// process did not stop and they need to know which of those did not
	// happen. Nil discards it.
	Logf func(format string, args ...any)
	// Now and Poll are the clock, for tests.
	Now  func() time.Time
	Poll time.Duration
}

// Lifecycle decides when this process stops. One per server process.
type Lifecycle struct {
	interactive   bool
	grace         time.Duration
	startupWindow time.Duration
	notify        func(string)
	logf          func(format string, args ...any)
	now           func() time.Time
	poll          time.Duration

	mu            sync.Mutex
	started       time.Time
	leases        int
	everConnected bool
	lastGone      time.Time
	// waiting is the work named when the process started waiting for it with
	// no page open; the notice is printed when that wait begins, not once a
	// second and not at every stage of a build.
	waiting string
	cause   ExitCause
	stopAt  time.Time
	done    chan struct{}
	// closers are the open leases, told to close with the cause when the
	// process stops so a page can say why it went.
	closers map[int]func(ExitCause)
	nextID  int
}

// NewLifecycle builds one. Its clock starts now: the startup window is measured
// from here.
func NewLifecycle(options LifecycleOptions) *Lifecycle {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	grace := options.CloseGrace
	if grace <= 0 {
		grace = DefaultCloseGrace
	}
	window := options.StartupWindow
	if window <= 0 {
		window = DefaultStartupWindow
	}
	poll := options.Poll
	if poll <= 0 {
		poll = lifecyclePoll
	}
	notify := options.Notify
	if notify == nil {
		notify = func(string) {}
	}
	logf := options.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Lifecycle{
		interactive:   options.Interactive,
		grace:         grace,
		startupWindow: window,
		notify:        notify,
		logf:          logf,
		now:           now,
		poll:          poll,
		started:       now(),
		done:          make(chan struct{}),
		closers:       map[int]func(ExitCause){},
	}
}

// Interactive reports application mode.
func (l *Lifecycle) Interactive() bool { return l != nil && l.interactive }

// CloseGrace is the grace period after the last page closed.
func (l *Lifecycle) CloseGrace() time.Duration { return l.grace }

// StartupWindow is how long an interactive process waits for its first page.
func (l *Lifecycle) StartupWindow() time.Duration { return l.startupWindow }

// Done is closed when the process has decided to stop.
func (l *Lifecycle) Done() <-chan struct{} { return l.done }

// Cause is why the process stopped, or "" while it has not.
func (l *Lifecycle) Cause() ExitCause {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cause
}

// StoppedAt is when the stop was decided, or zero while it has not been.
func (l *Lifecycle) StoppedAt() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopAt
}

// EverConnected reports whether any page has held a lease in this run.
func (l *Lifecycle) EverConnected() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.everConnected
}

// Leases is how many pages hold a lease right now.
func (l *Lifecycle) Leases() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.leases
}

// acquire records one open page, described by page for the log. The returned
// release is idempotent and takes the reason the page went, as the log says
// it. close is how the lifecycle tells that page the process is stopping.
func (l *Lifecycle) acquire(page string, close func(ExitCause)) (release func(reason string), stopping bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cause != "" {
		l.logf("lifecycle: a page opened a lease while stopping (%s); told it the process is stopping", page)
		return func(string) {}, true
	}
	l.leases++
	l.everConnected = true
	if l.waiting != "" {
		l.logf("lifecycle: a page opened again while waiting for %s; not stopping", l.waiting)
	}
	l.waiting = ""
	id := l.nextID
	l.nextID++
	l.closers[id] = close
	opened := l.now()
	l.logf("lifecycle: page lease %d opened (%s); %d page(s) open", id, page, l.leases)
	var once sync.Once
	return func(reason string) {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			delete(l.closers, id)
			l.leases--
			now := l.now()
			l.logf("lifecycle: page lease %d closed after %s: %s; %d page(s) open",
				id, now.Sub(opened).Round(time.Millisecond), reason, l.leases)
			if l.leases == 0 {
				l.lastGone = now
				switch {
				case l.cause != "":
				case l.interactive:
					l.logf("lifecycle: no page is open; stopping in %s unless a page opens or work is running", l.grace)
				default:
					l.logf("lifecycle: no page is open; server mode keeps running")
				}
			}
		})
	}, false
}

// Stop ends the process with a cause. The first cause wins: an interrupt that
// arrives while a Quit is shutting down does not rename the Quit.
func (l *Lifecycle) Stop(cause ExitCause) {
	l.mu.Lock()
	if l.cause != "" {
		l.mu.Unlock()
		return
	}
	l.cause = cause
	l.stopAt = l.now()
	l.logf("lifecycle: stop decided, cause %s; %d page(s) open", cause, l.leases)
	closers := make([]func(ExitCause), 0, len(l.closers))
	for _, close := range l.closers {
		closers = append(closers, close)
	}
	close(l.done)
	l.mu.Unlock()
	for _, close := range closers {
		close(cause)
	}
}

// Run takes the decision every poll until the process stops or ctx ends. busy
// is what is still running; it is read only once no page is open. A ctx that
// ends is an interrupt.
func (l *Lifecycle) Run(ctx context.Context, busy func() []ActiveWork) {
	ticker := time.NewTicker(l.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			l.Stop(ExitInterrupted)
			return
		case <-l.done:
			return
		case <-ticker.C:
			if cause, ok := l.decide(busy); ok {
				l.Stop(cause)
				return
			}
		}
	}
}

// decide is one decision. It never stops a server-mode process, never stops
// while a page is open, and never stops while work is running.
func (l *Lifecycle) decide(busy func() []ActiveWork) (ExitCause, bool) {
	if !l.interactive {
		return "", false
	}
	l.mu.Lock()
	if l.cause != "" || l.leases > 0 {
		l.mu.Unlock()
		return "", false
	}
	now := l.now()
	absentSince, window := l.started, l.startupWindow
	if l.everConnected {
		absentSince, window = l.lastGone, l.grace
	}
	wasWaiting := l.waiting != ""
	l.mu.Unlock()

	if now.Sub(absentSince) < window {
		return "", false
	}
	var work []ActiveWork
	if busy != nil {
		work = busy()
	}
	if len(work) > 0 {
		summary := describeWork(work)
		l.mu.Lock()
		first := l.waiting == ""
		l.waiting = summary
		l.mu.Unlock()
		// Once per wait, naming what is running then: a build moving from
		// one stage to the next is the same wait, and a line per stage was
		// noise in the terminal it was meant to keep quiet.
		if first {
			l.notify(summary)
		}
		return "", false
	}
	switch {
	case wasWaiting:
		return ExitWorkFinished, true
	case l.everConnected:
		return ExitUIClosed, true
	default:
		return ExitStartupTimeout, true
	}
}

// describeWork is the work as one sentence fragment, in a stable order.
func describeWork(work []ActiveWork) string {
	labels := make([]string, 0, len(work))
	for _, item := range work {
		label := item.Label
		if item.State != "" {
			label += " (" + item.State + ")"
		}
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return strings.Join(labels, "; ")
}

// ExitLine is the one causal line printed when the process stops.
func ExitLine(cause ExitCause, startupWindow time.Duration) string {
	switch cause {
	case ExitUIClosed:
		return "Auto-Pigeon Companion stopped: its last browser window was closed."
	case ExitQuit:
		return "Auto-Pigeon Companion stopped: Quit was chosen in the page."
	case ExitInterrupted:
		return "Auto-Pigeon Companion stopped: interrupted."
	case ExitStartupTimeout:
		return fmt.Sprintf("Auto-Pigeon Companion stopped: no browser opened its page within %s. "+
			"Start it again, or run `companion --stay-running` to keep it running without a page.", startupWindow)
	case ExitWorkFinished:
		return "Auto-Pigeon Companion stopped: the work it kept running for has finished, and no page is open."
	}
	return "Auto-Pigeon Companion stopped."
}
