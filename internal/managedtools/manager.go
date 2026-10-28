package managedtools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Host is what the Companion around this package provides: the executor's
// version check, the one writer of bindings, and what work is running.
// Defined here so this package depends on none of them.
type Host interface {
	// Verify asks an installation's programs what they are, through the
	// executor and its bounds, and fails unless each is the release it was
	// installed as. Nothing has been bound to it yet.
	Verify(ctx context.Context, tool Tool, record InstallRecord) error
	// Activate points the factory-managed bindings of the tool's profiles
	// at record, in one write, leaving every binding a person chose alone.
	Activate(tool Tool, record InstallRecord) error
	// Busy names the work that is using any of dirs, or that could start
	// using a tool between two of its stages (a build, a Build & Run). Empty
	// is idle.
	Busy(tool Tool, dirs []string) []string
}

// Options configure a [Manager].
type Options struct {
	// ToolsRoot holds the installations; StatePath is managed-tools.json.
	ToolsRoot string
	StatePath string
	Source    Source
	Channel   Channel
	// GOOS and GOARCH default to this process's.
	GOOS, GOARCH string
	Host         Host
	// Probes are the built-in profiles' compatibility rules per tool: a
	// release they would refuse is never installed.
	Probes map[ToolID]*profile.CompatibilityProbe
	// Disabled turns automatic setup and checks off (Settings). The status,
	// and anything already installed, stay.
	Disabled bool
	Logf     func(format string, args ...any)
	// Now, Interval, Jitter, Tick, Rand: the clock and the schedule, for
	// tests. Zero is production.
	Now      func() time.Time
	Interval time.Duration
	Jitter   time.Duration
	Tick     time.Duration
	Rand     func() float64
	Limits   Limits
	// FreeSpace overrides the disk-space probe, for tests.
	FreeSpace func(string) int64
}

// Schedule defaults.
const (
	// DefaultInterval: about once an hour while the Companion runs.
	DefaultInterval = time.Hour
	// DefaultJitter spreads installations across the hour.
	DefaultJitter = 5 * time.Minute
	// defaultTick is how often the coordinator looks at its state when it
	// has nothing to wait for; pendingTick while an update waits for work.
	defaultTick = 30 * time.Second
	pendingTick = 3 * time.Second
	// claimLease is how far a claimed check pushes the next attempt, so a
	// second Companion does not check at the same moment.
	claimLease = 2 * time.Minute
	// manualFloor is the shortest gap between two checks a person asks for.
	manualFloor = time.Minute
	// maxBackoff caps the wait after repeated failures.
	maxBackoff = time.Hour
	// dismissalWindow: a cancelled offer comes back at the next launch, or
	// after this long in a Companion that keeps running.
	dismissalWindow = 24 * time.Hour
	// maxSkipped bounds the skipped versions kept per tool.
	maxSkipped = 20
	// keepInstalled bounds the installations kept per tool: the active one,
	// the previous one for rollback, and one staged update.
	keepInstalled = 3
)

// Manager is the one coordinator of managed tools in a process.
type Manager struct {
	opts      Options
	installer installer
	session   string

	checkMu sync.Mutex // one release check at a time
	workMu  sync.Mutex // one install or activation at a time

	mu     sync.Mutex
	live   map[ToolID]*liveState
	notice []Notice
	wake   chan struct{}
}

// liveState is what is happening now, in this process. Not persisted:
// another Companion sees only the durable state.
type liveState struct {
	Phase   string
	Version string
	Done    int64
	Total   int64
	Waiting []string
}

// Notice is a one-time message the page shows once, such as "ready".
type Notice struct {
	ID   int64  `json:"id"`
	Tool ToolID `json:"tool"`
	Text string `json:"text"`
}

// New makes a coordinator. It starts nothing; [Manager.Run] does.
func New(opts Options) (*Manager, error) {
	if opts.ToolsRoot == "" || opts.StatePath == "" {
		return nil, errors.New("managedtools: a tools folder and a state file are required")
	}
	if !filepath.IsAbs(opts.ToolsRoot) {
		absolute, err := filepath.Abs(opts.ToolsRoot)
		if err != nil {
			return nil, err
		}
		opts.ToolsRoot = absolute
	}
	if opts.Source == nil {
		return nil, errors.New("managedtools: no release source")
	}
	if opts.Channel == "" {
		opts.Channel = DefaultChannel
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Jitter < 0 {
		opts.Jitter = 0
	} else if opts.Jitter == 0 {
		opts.Jitter = DefaultJitter
	}
	if opts.Tick <= 0 {
		opts.Tick = defaultTick
	}
	if opts.Rand == nil {
		opts.Rand = randomFraction
	}
	if opts.Limits == (Limits{}) {
		opts.Limits = DefaultLimits
	}
	session := make([]byte, 8)
	_, _ = rand.Read(session)
	return &Manager{
		opts:    opts,
		session: hex.EncodeToString(session),
		installer: installer{root: opts.ToolsRoot, source: opts.Source, limits: opts.Limits,
			channel: opts.Channel, freeSpace: opts.FreeSpace},
		live: map[ToolID]*liveState{},
		wake: make(chan struct{}, 1),
	}, nil
}

func randomFraction() float64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return float64(binary.LittleEndian.Uint64(b[:])>>11) / float64(1<<53)
}

// Target is the release platform name this coordinator installs for.
func (m *Manager) Target() string { return Target(m.opts.GOOS, m.opts.GOARCH) }

// Root is the tools folder.
func (m *Manager) Root() string { return m.opts.ToolsRoot }

func (m *Manager) now() time.Time { return m.opts.Now().UTC() }

// Wake asks the coordinator to look at its state now.
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run is the coordinator: it returns when ctx ends, and starts nothing that
// outlives it. One per process; several processes coordinate through the
// state file and each tool's install lock.
func (m *Manager) Run(ctx context.Context) {
	if m.opts.Disabled {
		return
	}
	for {
		m.Step(ctx)
		wait := m.opts.Tick
		if m.pending() {
			wait = pendingTick
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// Step does whatever is due once: a check, a first installation, staging an
// update somebody chose, an activation that was waiting for work to end.
// Exported so a test can drive the schedule with its own clock.
func (m *Manager) Step(ctx context.Context) {
	for _, tool := range Tools {
		if ctx.Err() != nil {
			return
		}
		if _, err := m.check(ctx, tool, false); err != nil && !errors.Is(err, errNotDue) {
			m.opts.Logf("managed tools: checking %s: %v", tool.ID, err)
		}
		if err := m.advance(ctx, tool); err != nil {
			m.opts.Logf("managed tools: %s: %v", tool.ID, err)
		}
	}
}

func (m *Manager) pending() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, live := range m.live {
		if len(live.Waiting) > 0 {
			return true
		}
	}
	return false
}

func (m *Manager) setLive(id ToolID, update func(*liveState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live := m.live[id]
	if live == nil {
		live = &liveState{}
		m.live[id] = live
	}
	update(live)
}

func (m *Manager) liveOf(id ToolID) liveState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if live := m.live[id]; live != nil {
		copied := *live
		copied.Waiting = append([]string(nil), live.Waiting...)
		return copied
	}
	return liveState{}
}

func (m *Manager) announce(id ToolID, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := int64(1)
	if len(m.notice) > 0 {
		next = m.notice[len(m.notice)-1].ID + 1
	}
	m.notice = append(m.notice, Notice{ID: next, Tool: id, Text: text})
	if len(m.notice) > 10 {
		m.notice = m.notice[len(m.notice)-10:]
	}
}

// errNotDue is a check that was not due; not a failure.
var errNotDue = errors.New("not due")

// CheckOutcome is what a check did, for the person who asked for one.
type CheckOutcome struct {
	Checked     bool      `json:"checked"`
	NotModified bool      `json:"not_modified,omitempty"`
	RetryAt     time.Time `json:"retry_at,omitempty"`
	Message     string    `json:"message"`
}

// CheckNow is Settings' "Check for updates": every tool, now, unless GitHub
// asked to wait or a check ran within the last minute.
func (m *Manager) CheckNow(ctx context.Context) []CheckOutcome {
	var out []CheckOutcome
	for _, tool := range Tools {
		outcome, err := m.check(ctx, tool, true)
		if err != nil && outcome.Message == "" {
			outcome.Message = err.Error()
		}
		out = append(out, outcome)
	}
	m.Wake()
	return out
}

// check asks GitHub about one tool when that is due (or asked for), and
// records the answer and the next time to ask.
func (m *Manager) check(ctx context.Context, tool Tool, manual bool) (CheckOutcome, error) {
	m.checkMu.Lock()
	defer m.checkMu.Unlock()
	now := m.now()
	var etag string
	var cached *Candidate
	outcome := CheckOutcome{}
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		check := &state.Tool(tool.ID).Check
		switch {
		case now.Before(check.RateLimitedUntil):
			outcome.RetryAt = check.RateLimitedUntil
			outcome.Message = fmt.Sprintf("GitHub asked the Companion to wait until %s", check.RateLimitedUntil.Local().Format("15:04"))
			return errNotDue
		case manual && !check.LastAttempt.IsZero() && now.Sub(check.LastAttempt) < manualFloor:
			outcome.RetryAt = check.LastAttempt.Add(manualFloor)
			outcome.Message = "checked less than a minute ago"
			return errNotDue
		case !manual && now.Before(check.NextAttempt):
			return errNotDue
		}
		check.LastAttempt = now
		check.NextAttempt = now.Add(claimLease)
		if check.Best != nil || len(check.Newer) > 0 || !check.LastSuccess.IsZero() {
			etag = check.ETag
		}
		cached = check.Best
		return nil
	})
	if err != nil {
		return outcome, err
	}
	m.setLive(tool.ID, func(l *liveState) { l.Phase = "checking" })
	defer m.setLive(tool.ID, func(l *liveState) {
		if l.Phase == "checking" {
			l.Phase = ""
		}
	})

	list, listErr := m.opts.Source.ListReleases(ctx, tool, etag)
	var evaluation Evaluation
	if listErr == nil && !list.NotModified {
		evaluation, listErr = m.evaluator(tool).evaluate(ctx, list.Releases)
	}
	_, err = UpdateState(m.opts.StatePath, func(state *State) error {
		ts := state.Tool(tool.ID)
		check := &ts.Check
		if listErr != nil {
			check.Failures++
			check.LastError = listErr.Error()
			next := now.Add(m.backoff(check.Failures))
			var limited *RateLimitError
			if errors.As(listErr, &limited) {
				until := limited.Until(now)
				check.RateLimitedUntil = until
				if until.After(next) {
					next = until
				}
				outcome.RetryAt = until
			}
			check.NextAttempt = next
			return nil
		}
		check.Failures, check.LastError = 0, ""
		check.LastSuccess = now
		check.RateLimitedUntil = time.Time{}
		check.NextAttempt = now.Add(m.opts.Interval + time.Duration(m.opts.Rand()*float64(m.opts.Jitter)))
		check.ETag = list.ETag
		if list.NotModified {
			check.Best = cached
		} else {
			check.Best, check.Newer = evaluation.Best, evaluation.Newer
			// A verdict that came from a manifest that could not be FETCHED
			// is about the network, not the release: the next check must
			// read the list again rather than be told "not modified" and
			// replay it.
			for _, verdict := range evaluation.Newer {
				if verdict.Reason == ReasonManifestUnreadable {
					check.ETag = ""
				}
			}
		}
		m.noteIntegrity(ts, now)
		return nil
	})
	if err != nil {
		return outcome, err
	}
	outcome.Checked = listErr == nil
	outcome.NotModified = list.NotModified
	switch {
	case listErr != nil:
		outcome.Message = listErr.Error()
	case list.NotModified:
		outcome.Message = "no new release"
	default:
		outcome.Message = "checked"
	}
	return outcome, listErr
}

// backoff is the wait after n failed checks or installs in a row: 1, 2, 4,
// … minutes, at most an hour, with a little jitter.
func (m *Manager) backoff(failures int) time.Duration {
	wait := time.Minute
	for i := 1; i < failures && wait < maxBackoff; i++ {
		wait *= 2
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	return wait + time.Duration(m.opts.Rand()*float64(wait)/10)
}

// noteIntegrity records a release this program knew with other bytes.
func (m *Manager) noteIntegrity(ts *ToolState, now time.Time) {
	best := ts.Check.Best
	if best == nil {
		return
	}
	known, ok := ts.knownBytes(best.Version)
	if ok && known != best.Identity() {
		if ts.Integrity == nil || ts.Integrity.Seen != best.Identity() {
			ts.Integrity = &IntegrityEvent{Known: known, Seen: best.Identity(), At: now}
		}
		return
	}
	if ts.Integrity != nil && ts.Integrity.Seen.Version != best.Version {
		ts.Integrity = nil
	}
}

func (m *Manager) evaluator(tool Tool) evaluator {
	return evaluator{
		tool:    tool,
		target:  m.Target(),
		channel: m.opts.Channel,
		probe:   m.opts.Probes[tool.ID],
		fetch: func(ctx context.Context, release Release, name string, limit int64) ([]byte, error) {
			var buffer bytes.Buffer
			if _, err := m.opts.Source.Download(ctx, tool, release.Tag, name, limit, &buffer); err != nil {
				return nil, err
			}
			return buffer.Bytes(), nil
		},
	}
}

// advance moves one tool toward what its state asks for.
func (m *Manager) advance(ctx context.Context, tool Tool) error {
	m.workMu.Lock()
	defer m.workMu.Unlock()
	state, err := LoadState(m.opts.StatePath)
	if err != nil {
		return err
	}
	ts := state.Tool(tool.ID)
	if err := m.reconcile(tool, ts); err != nil {
		return err
	}
	state, _ = LoadState(m.opts.StatePath)
	ts = state.Tool(tool.ID)
	best := ts.Check.Best
	now := m.now()

	if ts.Active == nil {
		if best == nil || ts.Integrity != nil {
			return nil
		}
		if ts.Failure != nil && ts.Failure.Version == best.Version && now.Before(ts.Failure.NextAttempt) {
			return nil
		}
		record, err := m.installVerified(ctx, tool, *best)
		if err != nil {
			return m.recordFailure(tool, best.Version, err)
		}
		if err := m.activate(tool, record, false); err != nil {
			return m.recordFailure(tool, best.Version, err)
		}
		m.announce(tool.ID, fmt.Sprintf("%s %s is ready", tool.Title, record.Version))
		return nil
	}

	if ts.Intent == IntentNone || ts.IntentFor == nil {
		m.setLive(tool.ID, func(l *liveState) { l.Waiting = nil })
		return m.prune(tool)
	}
	// An intent for bytes the release no longer offers, or for a version no
	// newer than what is active, is over.
	if best == nil || best.Identity() != *ts.IntentFor || !m.newer(tool, best.Version, ts.Active.Version) {
		_, err := UpdateState(m.opts.StatePath, func(state *State) error {
			current := state.Tool(tool.ID)
			current.Intent, current.IntentFor = IntentNone, nil
			return nil
		})
		return err
	}
	staged, isStaged := ts.installed(*ts.IntentFor)
	if !isStaged {
		if ts.Failure != nil && ts.Failure.Version == best.Version && now.Before(ts.Failure.NextAttempt) {
			return nil
		}
		record, err := m.installVerified(ctx, tool, *best)
		if err != nil {
			return m.recordFailure(tool, best.Version, err)
		}
		staged = record.Ref()
	}
	if ts.Intent != IntentNow {
		return nil
	}
	return m.activateWhenIdle(tool, staged, ts.Active)
}

// activateWhenIdle activates a staged update unless work uses the
// installation it replaces.
func (m *Manager) activateWhenIdle(tool Tool, staged InstallRef, active *InstallRef) error {
	if m.opts.Host != nil {
		if busy := m.opts.Host.Busy(tool, []string{active.Dir}); len(busy) > 0 {
			m.setLive(tool.ID, func(l *liveState) { l.Waiting = busy; l.Version = staged.Version })
			return nil
		}
	}
	record, err := ReadInstall(staged.Dir)
	if err != nil {
		return m.recordFailure(tool, staged.Version, err)
	}
	if err := m.activate(tool, record, true); err != nil {
		return m.recordFailure(tool, staged.Version, err)
	}
	m.setLive(tool.ID, func(l *liveState) { l.Waiting = nil })
	m.announce(tool.ID, fmt.Sprintf("%s %s is ready", tool.Title, record.Version))
	return m.prune(tool)
}

// installVerified downloads, verifies, unpacks and version-checks one
// candidate, and records it as installed. It does not activate it. A
// candidate whose programs fail the check is removed again: nothing
// unverified is left looking like an installation.
func (m *Manager) installVerified(ctx context.Context, tool Tool, candidate Candidate) (InstallRecord, error) {
	m.setLive(tool.ID, func(l *liveState) {
		l.Phase, l.Version, l.Done, l.Total = "downloading", candidate.Version, 0, candidate.Size
	})
	defer m.setLive(tool.ID, func(l *liveState) { l.Phase, l.Done, l.Total = "", 0, 0 })
	record, err := m.installer.install(ctx, tool, candidate, func(phase string, done, total int64) {
		m.setLive(tool.ID, func(l *liveState) { l.Phase, l.Done, l.Total = phase, done, total })
	})
	if err != nil {
		return InstallRecord{}, err
	}
	if m.opts.Host != nil {
		m.setLive(tool.ID, func(l *liveState) { l.Phase = "verifying" })
		if err := m.opts.Host.Verify(ctx, tool, record); err != nil {
			if !m.referenced(tool, record.Dir) {
				_ = os.RemoveAll(record.Dir)
			}
			return InstallRecord{}, fmt.Errorf("the installed programs did not pass their version check: %w", err)
		}
	}
	_, err = UpdateState(m.opts.StatePath, func(state *State) error {
		ts := state.Tool(tool.ID)
		if _, known := ts.installed(record.Ref().Identity()); !known {
			ts.Installed = append(ts.Installed, record.Ref())
		}
		if ts.Failure != nil && ts.Failure.Version == record.Version {
			ts.Failure = nil
		}
		return nil
	})
	return record, err
}

// referenced reports whether the state points at dir as active or previous.
func (m *Manager) referenced(tool Tool, dir string) bool {
	state, err := LoadState(m.opts.StatePath)
	if err != nil {
		return true
	}
	ts := state.Tool(tool.ID)
	return (ts.Active != nil && ts.Active.Dir == dir) || (ts.Previous != nil && ts.Previous.Dir == dir)
}

// activate points the managed bindings at record and records it as active.
// The bindings are written first: a state that says "active" while the
// bindings still name the old programs would be a lie the page then repeats.
func (m *Manager) activate(tool Tool, record InstallRecord, update bool) error {
	if m.opts.Host != nil {
		if err := m.opts.Host.Activate(tool, record); err != nil {
			return fmt.Errorf("binding the profiles to %s: %w", record.Version, err)
		}
	}
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		ts := state.Tool(tool.ID)
		if ts.Active != nil && ts.Active.Dir != record.Dir {
			previous := *ts.Active
			ts.Previous = &previous
		}
		ref := record.Ref()
		ts.Active = &ref
		if _, known := ts.installed(ref.Identity()); !known {
			ts.Installed = append(ts.Installed, ref)
		}
		if ts.IntentFor != nil && *ts.IntentFor == ref.Identity() {
			ts.Intent, ts.IntentFor = IntentNone, nil
		}
		ts.Dismissed, ts.Failure = nil, nil
		return nil
	})
	if err == nil {
		verb := "installed"
		if update {
			verb = "updated to"
		}
		m.opts.Logf("managed tools: %s %s %s (%s, sha256 %s)", tool.ID, verb, record.Version, record.Dir, record.ActualSHA256)
	}
	return err
}

// recordFailure keeps a retryable cause, with a backoff before the next try.
func (m *Manager) recordFailure(tool Tool, version string, cause error) error {
	now := m.now()
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		ts := state.Tool(tool.ID)
		attempts := 1
		if ts.Failure != nil && ts.Failure.Version == version {
			attempts = ts.Failure.Attempts + 1
		}
		wait := m.backoff(attempts)
		if errors.Is(cause, ErrInstallBusy) {
			wait = time.Minute
		}
		ts.Failure = &SetupFailure{Version: version, Error: cause.Error(), At: now, Attempts: attempts, NextAttempt: now.Add(wait)}
		return nil
	})
	if err != nil {
		return err
	}
	return cause
}

// reconcile puts the state right about what is on disk: an active
// installation whose folder is gone or no longer checks out is not active,
// and an installation nobody recorded is forgotten rather than trusted.
func (m *Manager) reconcile(tool Tool, ts *ToolState) error {
	broken := func(ref *InstallRef) bool {
		if ref == nil {
			return false
		}
		record, err := ReadInstall(ref.Dir)
		return err != nil || record.ActualSHA256 != ref.SHA256
	}
	activeBroken := broken(ts.Active)
	previousBroken := broken(ts.Previous)
	var missing []string
	for _, installed := range ts.Installed {
		if _, err := ReadInstall(installed.Dir); err != nil {
			missing = append(missing, installed.Dir)
		}
	}
	if !activeBroken && !previousBroken && len(missing) == 0 {
		return nil
	}
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		current := state.Tool(tool.ID)
		if activeBroken {
			m.opts.Logf("managed tools: the active %s installation %s is gone or damaged; it will be installed again", tool.ID, current.Active.Dir)
			if current.Previous != nil && !previousBroken {
				// The previous working version is used again until the new
				// one is reinstalled; the bindings move back to it.
				if record, err := ReadInstall(current.Previous.Dir); err == nil && m.opts.Host != nil && m.opts.Host.Activate(tool, record) == nil {
					previous := *current.Previous
					current.Active, current.Previous = &previous, nil
				} else {
					current.Active = nil
				}
			} else {
				current.Active = nil
			}
		}
		if previousBroken {
			current.Previous = nil
		}
		kept := current.Installed[:0]
		for _, installed := range current.Installed {
			if !contains(missing, installed.Dir) {
				kept = append(kept, installed)
			}
		}
		current.Installed = kept
		return nil
	})
	return err
}

// prune removes installations beyond the retention bound that nothing uses:
// never the active one, never the previous one, never a staged update
// somebody chose, and never one live work uses.
func (m *Manager) prune(tool Tool) error {
	state, err := LoadState(m.opts.StatePath)
	if err != nil {
		return err
	}
	ts := state.Tool(tool.ID)
	keep := map[string]bool{}
	if ts.Active != nil {
		keep[ts.Active.Dir] = true
	}
	if ts.Previous != nil {
		keep[ts.Previous.Dir] = true
	}
	if ts.IntentFor != nil {
		if staged, ok := ts.installed(*ts.IntentFor); ok {
			keep[staged.Dir] = true
		}
	}
	var removable []InstallRef
	for _, installed := range ts.Installed {
		if !keep[installed.Dir] {
			removable = append(removable, installed)
		}
	}
	if len(ts.Installed) <= keepInstalled && len(removable) == 0 {
		return nil
	}
	sort.Slice(removable, func(i, j int) bool { return removable[i].CompletedAt.Before(removable[j].CompletedAt) })
	toolDir := m.installer.toolDir(tool)
	var removed []string
	for _, installed := range removable {
		if !within(toolDir, installed.Dir) || filepath.Clean(installed.Dir) == filepath.Clean(toolDir) {
			continue
		}
		if m.opts.Host != nil && len(m.opts.Host.Busy(tool, []string{installed.Dir})) > 0 {
			continue
		}
		if err := os.RemoveAll(installed.Dir); err != nil {
			m.opts.Logf("managed tools: removing %s: %v", installed.Dir, err)
			continue
		}
		removed = append(removed, installed.Dir)
	}
	if len(removed) == 0 {
		return nil
	}
	_, err = UpdateState(m.opts.StatePath, func(state *State) error {
		current := state.Tool(tool.ID)
		kept := current.Installed[:0]
		for _, installed := range current.Installed {
			if !contains(removed, installed.Dir) {
				kept = append(kept, installed)
			}
		}
		current.Installed = kept
		return nil
	})
	return err
}

func (m *Manager) newer(tool Tool, version, than string) bool {
	a, errA := tool.Version(version)
	b, errB := tool.Version(than)
	return errA == nil && errB == nil && a.Compare(b) > 0
}

// Decision is what a person chose in the update dialog.
type Decision string

const (
	DecideNow    Decision = "now"
	DecideOnExit Decision = "on_exit"
	DecideCancel Decision = "cancel"
)

// ErrStaleOffer is a decision about bytes that are not the current offer:
// a dialog left open across a check that found something else.
var ErrStaleOffer = errors.New("managedtools: that update is no longer the one on offer")

// Decide records the person's answer to one offer.
func (m *Manager) Decide(id ToolID, decision Decision, identity BytesIdentity, dontAsk bool) error {
	if _, ok := Find(id); !ok {
		return fmt.Errorf("managedtools: %q is not a managed tool", id)
	}
	now := m.now()
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		ts := state.Tool(id)
		best := ts.Check.Best
		if best == nil || best.Identity() != identity {
			return ErrStaleOffer
		}
		switch decision {
		case DecideNow, DecideOnExit:
			ts.Intent = Intent(decision)
			copied := identity
			ts.IntentFor = &copied
			ts.Dismissed = nil
		case DecideCancel:
			ts.Dismissed = &Dismissal{BytesIdentity: identity, Session: m.session, At: now}
			if dontAsk && !ts.skipped(identity) {
				ts.Skipped = append(ts.Skipped, identity)
				if len(ts.Skipped) > maxSkipped {
					ts.Skipped = ts.Skipped[len(ts.Skipped)-maxSkipped:]
				}
			}
		default:
			return fmt.Errorf("managedtools: %q is not a decision", decision)
		}
		return nil
	})
	m.Wake()
	return err
}

// CancelPending drops a chosen update that has not been activated. Its
// staged files stay until the retention bound removes them, and the offer
// does not come back in this run.
func (m *Manager) CancelPending(id ToolID) error {
	now := m.now()
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		ts := state.Tool(id)
		if ts.IntentFor != nil {
			ts.Dismissed = &Dismissal{BytesIdentity: *ts.IntentFor, Session: m.session, At: now}
		}
		ts.Intent, ts.IntentFor = IntentNone, nil
		return nil
	})
	m.setLive(id, func(l *liveState) { l.Waiting = nil })
	m.Wake()
	return err
}

// ResetSkipped forgets "don't ask again" for one tool, or for every tool
// when id is empty.
func (m *Manager) ResetSkipped(id ToolID) error {
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		for _, tool := range Tools {
			if id == "" || tool.ID == id {
				state.Tool(tool.ID).Skipped = nil
			}
		}
		return nil
	})
	m.Wake()
	return err
}

// Retry ends a failure's backoff: the next step tries again at once.
func (m *Manager) Retry(id ToolID) error {
	now := m.now()
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		for _, tool := range Tools {
			if id != "" && tool.ID != id {
				continue
			}
			ts := state.Tool(tool.ID)
			if ts.Failure != nil {
				ts.Failure.NextAttempt = now
			}
			if ts.Check.Failures > 0 && !now.Before(ts.Check.RateLimitedUntil) {
				ts.Check.NextAttempt = now
			}
			if ts.Check.LastSuccess.IsZero() && !now.Before(ts.Check.RateLimitedUntil) {
				ts.Check.NextAttempt = now
			}
		}
		return nil
	})
	m.Wake()
	return err
}

// ApplyOnExit activates every update somebody chose to apply when the
// Companion closes, if it is staged and nothing uses what it replaces. It
// is called once, on a normal exit, after the work has ended. An update
// that is not staged yet stays chosen and is applied the next time — at the
// next start, before anything runs, or at the next close.
func (m *Manager) ApplyOnExit() []string {
	return m.applyChosen(IntentOnExit)
}

// ApplyAtStart activates, before anything else runs, an update that was
// chosen for the close and was staged but not applied: the close was a
// crash, a power cut or a kill, which is not a successful update, and the
// start is the first moment that is like a close again.
func (m *Manager) ApplyAtStart() []string {
	return m.applyChosen(IntentOnExit)
}

func (m *Manager) applyChosen(intent Intent) []string {
	m.workMu.Lock()
	defer m.workMu.Unlock()
	state, err := LoadState(m.opts.StatePath)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, tool := range Tools {
		ts := state.Tool(tool.ID)
		if ts.Intent != intent || ts.IntentFor == nil || ts.Active == nil {
			continue
		}
		staged, ok := ts.installed(*ts.IntentFor)
		if !ok {
			out = append(out, fmt.Sprintf("%s %s is not downloaded yet; it will be applied later", tool.Title, ts.IntentFor.Version))
			continue
		}
		if m.opts.Host != nil {
			if busy := m.opts.Host.Busy(tool, []string{ts.Active.Dir}); len(busy) > 0 {
				out = append(out, fmt.Sprintf("%s %s waits: %s", tool.Title, staged.Version, strings.Join(busy, "; ")))
				continue
			}
		}
		record, err := ReadInstall(staged.Dir)
		if err == nil {
			err = m.activate(tool, record, true)
		}
		if err != nil {
			_ = m.recordFailure(tool, staged.Version, err)
			out = append(out, fmt.Sprintf("%s %s was not applied: %v", tool.Title, staged.Version, err))
			continue
		}
		out = append(out, fmt.Sprintf("%s updated to %s", tool.Title, staged.Version))
	}
	return out
}

// ActiveInstall is the active installation of a tool, when it checks out.
func (m *Manager) ActiveInstall(id ToolID) (InstallRecord, bool) {
	state, err := LoadState(m.opts.StatePath)
	if err != nil {
		return InstallRecord{}, false
	}
	ts := state.Tool(id)
	if ts.Active == nil {
		return InstallRecord{}, false
	}
	record, err := ReadInstall(ts.Active.Dir)
	return record, err == nil
}

// Session is this process's id, which a dismissal is scoped to.
func (m *Manager) Session() string { return m.session }
