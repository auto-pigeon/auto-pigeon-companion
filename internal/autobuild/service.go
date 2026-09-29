package autobuild

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// CheckInterval is how often a map is asked about while all is well.
const CheckInterval = 30 * time.Second

// MaxBackoff bounds the wait after AUB failed to answer several times in a row.
const MaxBackoff = 10 * time.Minute

// TickInterval is how often the loop wakes: to notice that a build finished
// (a local read) and to ask about any map whose check is due. AUB is asked only
// when a map's NextCheckAt has passed, never on every tick.
const TickInterval = 5 * time.Second

// checkTimeout bounds one question to AUB, so a hung connection cannot hold
// a check slot for long and cannot overlap the next check of that map.
const checkTimeout = 20 * time.Second

// MaxConcurrentChecks bounds the questions the poller has in flight at once.
// One slow map holds one of them for at most checkTimeout and then backs off,
// so the others keep close to their thirty seconds.
const MaxConcurrentChecks = 4

// submitGrace is how long a submission another process wrote down may go
// without the run it started being recorded before it is judged interrupted.
// Starting a run returns its id at once; this is far longer than that takes.
const submitGrace = 2 * time.Minute

// The ways a check can fail that are not "try again shortly".
var (
	// ErrAccess: AUB refused this session, or there is none. Checked again,
	// on the backoff, so signing in again resumes it.
	ErrAccess = errors.New("autobuild: AUB refused this session")
	// ErrMissing: the map is not there, or no longer this person's to read.
	// Checking stops until Auto-build is switched on again.
	ErrMissing = errors.New("autobuild: the map is not on AUB")
	// ErrBusy: a build of this map is already running.
	ErrBusy = errors.New("autobuild: a build of this map is already running; wait for it, or stop it in Activity")
)

// Terminal run states, as the Build & Run coordinator records them.
const (
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// Deps are what an auto-build drives. Every one exists elsewhere.
type Deps struct {
	// Current asks AUB, with the person's own session, which revision of a map
	// is current. It returns the map's name as well, for the page.
	Current func(ctx context.Context, assetID string) (Revision, string, error)
	// Start submits one ordinary build of that revision with that pipeline and
	// returns the run's id at once; the build goes on without it.
	Start func(entry Entry, revision Revision) (string, error)
	// RunState reports a run's state and, once it failed, why. An error means
	// the run is not there to ask about.
	RunState func(runID string) (state, why string, err error)
	Now      func() time.Time
	Logf     func(format string, args ...any)
}

// Service is the auto-build of every map on this machine. One per process:
// one poller, whoever asked for it.
//
// # Two kinds of lock, and why neither is held while AUB is asked (NEW_265A)
//
// The poller's own decisions — which builds have finished, which maps are due,
// which builds are owed — are made one pass at a time under pass. A person's
// action (on, off, another build profile, Build now, Retry) never takes pass:
// it is a short read-change-write of the state file under the file's
// cross-process lock, and that lock is held only for the read, the change and
// the write. Questions to AUB and the start of a build happen with no lock
// held, so a server that takes twenty seconds to answer cannot keep a person
// from switching Auto-build off.
//
// What makes that safe is that an answer carries the Generation of the entry
// it was asked for, and is applied only if the entry is still switched on at
// that generation; and that an automatic build is started only after its map
// is found, inside the lock, still switched on and still owed that build.
type Service struct {
	path     string
	deps     Deps
	instance string

	// pass serializes the poller's passes and the answers they apply.
	pass sync.Mutex
	// slots bounds the questions in flight (MaxConcurrentChecks).
	slots chan struct{}
	// wake asks the loop for a pass now rather than at the next tick.
	wake chan struct{}
	// inflight counts every check goroutine, so Run can wait for them.
	inflight sync.WaitGroup

	// mu guards the two maps below and is held only to read or change them.
	mu sync.Mutex
	// checking is the question in flight for each map: at most one.
	checking map[string]Checking
	// starting counts the builds this instance has written down and not yet
	// recorded a run for, per map.
	starting map[string]int
}

// Checking is a question to AUB that has not been answered yet.
type Checking struct {
	Since      time.Time
	Generation uint64
}

// New builds a service over a state file.
func New(path string, deps Deps) (*Service, error) {
	if path == "" || deps.Current == nil || deps.Start == nil || deps.RunState == nil {
		return nil, errors.New("autobuild: a service needs a state file and every dependency")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.Logf == nil {
		deps.Logf = func(string, ...any) {}
	}
	nonce := make([]byte, 6)
	_, _ = rand.Read(nonce)
	return &Service{
		path: path, deps: deps,
		instance: fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(nonce)),
		slots:    make(chan struct{}, MaxConcurrentChecks),
		wake:     make(chan struct{}, 1),
		checking: map[string]Checking{},
		starting: map[string]int{},
	}, nil
}

// Path is the state file.
func (s *Service) Path() string { return s.path }

// Run ticks until the context ends. Started once by the server: there is one
// poller per Companion, whatever number of browser tabs are open. Its passes
// do not wait for AUB: a question is asked in the background and applied when
// its answer arrives, so the next pass still notices finished builds and asks
// about the other maps. When the context ends, Run waits for the questions in
// flight, which end with it.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	defer s.inflight.Wait()
	for {
		if _, err := s.tick(ctx); err != nil {
			s.deps.Logf("autobuild: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

// Nudge asks the running loop for a pass now — after a map was switched on,
// so its baseline is taken in a moment rather than at the next tick. It never
// waits, and it asks nothing itself.
func (s *Service) Nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// State reads the current state.
func (s *Service) State() (*State, error) { return Load(s.path) }

// CheckingNow reports the question in flight for a map, if there is one.
func (s *Service) CheckingNow(assetID string) (Checking, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	check, found := s.checking[assetID]
	return check, found
}

// observation is one answer from AUB, gathered outside the file lock.
type observation struct {
	revision Revision
	name     string
	err      error
	at       time.Time
}

// due is a map whose check is owed, as it was when the pass looked.
type due struct {
	assetID    string
	generation uint64
	next       time.Time
}

// Tick is one pass that waits for the questions it asked: notice finished
// builds, ask about the maps that are due, apply each answer as it arrives and
// start the builds that are now owed. Run does the same without waiting.
func (s *Service) Tick(ctx context.Context) error {
	wait, err := s.tick(ctx)
	wait()
	if err != nil {
		return err
	}
	s.pass.Lock()
	defer s.pass.Unlock()
	return s.startOwed("")
}

// tick is one pass. It returns at once, with a function that waits for the
// questions it started.
func (s *Service) tick(ctx context.Context) (func(), error) {
	s.pass.Lock()
	defer s.pass.Unlock()

	var group sync.WaitGroup
	state, err := Load(s.path)
	if err != nil {
		return group.Wait, err
	}
	if len(state.Entries) == 0 {
		// Nothing was ever switched on: no file is written and nobody is asked.
		return group.Wait, nil
	}
	now := s.deps.Now()
	var owed []due
	if _, err := Update(s.path, func(fresh *State) error {
		owed = owed[:0]
		for i := range fresh.Entries {
			entry := &fresh.Entries[i]
			s.finish(entry)
			if entry.Enabled && entry.Halted == "" && !now.Before(entry.NextCheckAt) {
				owed = append(owed, due{entry.AssetID, entry.Generation, entry.NextCheckAt})
			}
		}
		return nil
	}); err != nil {
		return group.Wait, err
	}
	// The map that has waited longest is asked first, so a slow map holding a
	// slot delays the others by one pass at most, never indefinitely.
	sort.SliceStable(owed, func(i, j int) bool { return owed[i].next.Before(owed[j].next) })
	for _, check := range owed {
		if ctx.Err() != nil {
			break
		}
		if !s.claim(check) {
			continue
		}
		group.Add(1)
		s.inflight.Add(1)
		go func(check due) {
			defer s.inflight.Done()
			defer group.Done()
			s.check(ctx, check)
		}(check)
	}
	if err := s.startOwed(""); err != nil {
		return group.Wait, err
	}
	return group.Wait, nil
}

// claim reserves a slot and marks the map as being asked about. It refuses
// when the map already has a question in flight or every slot is taken; the
// map stays due and is asked at a later pass.
func (s *Service) claim(check due) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.checking[check.assetID]; busy {
		return false
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return false
	}
	s.checking[check.assetID] = Checking{Since: s.deps.Now(), Generation: check.generation}
	return true
}

// check asks AUB about one map, with no lock held, then applies the answer if
// the map's auto-build is still the one it was asked for.
func (s *Service) check(ctx context.Context, check due) {
	defer func() {
		s.mu.Lock()
		delete(s.checking, check.assetID)
		s.mu.Unlock()
	}()
	asked, cancel := context.WithTimeout(ctx, checkTimeout)
	revision, name, err := s.deps.Current(asked, check.assetID)
	cancel()
	<-s.slots
	if ctx.Err() != nil {
		// The Companion is stopping: that is not AUB failing to answer, and
		// nothing is recorded for it.
		return
	}
	if err == nil && !revision.Valid() {
		err = fmt.Errorf("AUB's answer about this map named no revision (id %q, number %d)", revision.ID, revision.Number)
	}
	answer := observation{revision: revision, name: name, err: err, at: s.deps.Now()}

	s.pass.Lock()
	defer s.pass.Unlock()
	applied := false
	if _, err := Update(s.path, func(fresh *State) error {
		entry, found := fresh.Find(check.assetID)
		if !found || !entry.Enabled || entry.Halted != "" || entry.Generation != check.generation {
			return errUnchanged
		}
		s.observe(entry, answer)
		applied = true
		return nil
	}); err != nil {
		s.deps.Logf("autobuild %s: recording a check: %v", check.assetID, err)
		return
	}
	if !applied {
		s.deps.Logf("autobuild %s: an answer from AUB arrived after its auto-build was changed; it was not used", check.assetID)
		return
	}
	if err := s.startOwed(check.assetID); err != nil {
		s.deps.Logf("autobuild %s: %v", check.assetID, err)
	}
}

// finish records a build that has stopped.
func (s *Service) finish(entry *Entry) {
	running := entry.Running
	if running == nil {
		return
	}
	if running.RunID == "" {
		if s.submitting(entry.AssetID, running) {
			// Written down and being started right now: not a crash.
			return
		}
		// Written down as submitted, and no run was recorded for it: the
		// Companion stopped between the two. It is not started again on its
		// own — nobody knows whether it began.
		running.State, running.Error = RunFailed, "the Companion stopped before this build had started; press Retry to build it"
		running.FinishedAt = s.deps.Now()
		entry.Failed, entry.Running = running, nil
		return
	}
	state, why, err := s.deps.RunState(running.RunID)
	if err != nil {
		running.State, running.Error = RunFailed, "the record of this build is gone: "+err.Error()
		running.FinishedAt = s.deps.Now()
		entry.Failed, entry.Running = running, nil
		return
	}
	switch state {
	case RunSucceeded:
		running.State, running.FinishedAt = state, s.deps.Now()
		entry.LastBuilt, entry.Running = running, nil
		if entry.Failed != nil && entry.Failed.Revision.Number <= running.Revision.Number {
			entry.Failed = nil
		}
		s.deps.Logf("autobuild %s: revision %d built (run %s)", entry.AssetID, running.Revision.Number, running.RunID)
	case RunFailed, RunCancelled:
		running.State, running.Error, running.FinishedAt = state, why, s.deps.Now()
		entry.Failed, entry.Running = running, nil
		s.deps.Logf("autobuild %s: revision %d %s (run %s)", entry.AssetID, running.Revision.Number, state, running.RunID)
	}
}

// submitting reports whether a submission with no run yet is still being
// started, rather than one a crash interrupted. This instance knows its own;
// another process's is given submitGrace to record its run. An attempt that
// names no submitter was written before submitters were recorded, and is
// judged as it always was.
func (s *Service) submitting(assetID string, attempt *Attempt) bool {
	switch attempt.Submitter {
	case "":
		return false
	case s.instance:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.starting[assetID] > 0
	}
	return s.deps.Now().Sub(attempt.StartedAt) < submitGrace
}

// observe applies one answer from AUB.
func (s *Service) observe(entry *Entry, answer observation) {
	entry.LastCheckAt = answer.at
	if answer.name != "" {
		entry.DisplayName = answer.name
	}
	if answer.err != nil {
		entry.Failures++
		switch {
		case errors.Is(answer.err, ErrMissing):
			entry.Halted = HaltMissing
			entry.CheckError = "This map is not on the Auto-Pigeon server any more, or is no longer shared with you. Auto-build stopped checking it."
		case errors.Is(answer.err, ErrAccess):
			entry.CheckError = "The Auto-Pigeon server refused this session. Sign in again; Auto-build keeps trying, less often."
		default:
			entry.CheckError = "The Auto-Pigeon server could not be asked: " + answer.err.Error()
		}
		if errors.Is(answer.err, ErrAccess) {
			entry.Halted = ""
		}
		entry.NextCheckAt = answer.at.Add(backoff(entry.Failures))
		return
	}
	entry.Failures, entry.CheckError = 0, ""
	entry.NextCheckAt = answer.at.Add(CheckInterval)
	revision := answer.revision
	if entry.Baseline != nil && entry.Observed != nil && revision.Number < entry.Observed.Number {
		// Somebody else — Build now — has already seen a newer revision while
		// this question was in flight. The older answer changes nothing.
		return
	}
	entry.Observed = &revision
	if entry.Baseline == nil {
		// The revision current when Auto-build was switched on is not built:
		// switching it on means "from now on".
		entry.Baseline = &revision
		return
	}
	switch {
	case revision.Key() == entry.Baseline.Key(),
		entry.submitted(revision.Key()),
		entry.Pending != nil && entry.Pending.Key() == revision.Key(),
		revision.Number <= entry.Baseline.Number,
		entry.Pending != nil && revision.Number < entry.Pending.Number:
		return
	}
	// Newer than anything waiting: it replaces it. Edits saved while a build
	// runs collapse into the newest one.
	entry.Pending = &revision
}

// backoff is the wait after n failed checks in a row: doubling from a minute,
// never more than MaxBackoff.
func backoff(failures int) time.Duration {
	wait := CheckInterval
	for i := 0; i < failures && wait < MaxBackoff; i++ {
		wait *= 2
	}
	if wait > MaxBackoff {
		wait = MaxBackoff
	}
	return wait
}

// startOwed starts the build of every enabled map — or of one, when assetID
// is given — that is waiting for one and has none running. Whether a build is
// owed is decided again inside the lock, by owedBuild: a map switched off
// since it was read is not built.
func (s *Service) startOwed(assetID string) error {
	state, err := Load(s.path)
	if err != nil {
		return err
	}
	for _, entry := range state.Entries {
		if assetID != "" && entry.AssetID != assetID {
			continue
		}
		if _, owed := owedBuild(&entry); !owed {
			continue
		}
		if err := s.submit(entry.AssetID, false, owedBuild); err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, errNotOwed) {
			s.deps.Logf("autobuild %s: %v", entry.AssetID, err)
		}
	}
	return nil
}

// errNotOwed: by the time the lock was held, the build was no longer owed.
var errNotOwed = errors.New("autobuild: no build is owed")

// owedBuild is the revision an enabled map is waiting to have built, if any.
func owedBuild(entry *Entry) (Revision, bool) {
	if !entry.Enabled || entry.Running != nil || entry.Pending == nil || entry.submitted(entry.Pending.Key()) {
		return Revision{}, false
	}
	return *entry.Pending, true
}

// submit submits a build of one revision: written down first, then started.
// pick names the revision from the entry as it is inside the lock.
func (s *Service) submit(assetID string, manual bool, pick func(*Entry) (Revision, bool)) error {
	var snapshot Entry
	var revision Revision
	s.mu.Lock()
	s.starting[assetID]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.starting[assetID]--; s.starting[assetID] <= 0 {
			delete(s.starting, assetID)
		}
		s.mu.Unlock()
	}()
	_, err := Update(s.path, func(state *State) error {
		entry, found := state.Find(assetID)
		if !found {
			return fmt.Errorf("autobuild: %s has no auto-build", assetID)
		}
		if entry.Running != nil {
			return ErrBusy
		}
		picked, ok := pick(entry)
		if !ok {
			return errNotOwed
		}
		if strings.TrimSpace(entry.PipelineID) == "" {
			return errors.New("autobuild: choose a build profile first")
		}
		revision = picked
		entry.remember(revision.Key())
		entry.Running = &Attempt{
			Revision: revision, Pipeline: entry.PipelineID, Manual: manual,
			StartedAt: s.deps.Now(), Submitter: s.instance,
		}
		if entry.Pending != nil && entry.Pending.Number <= revision.Number {
			entry.Pending = nil
		}
		snapshot = *entry
		return nil
	})
	if err != nil {
		return err
	}
	runID, startErr := s.deps.Start(snapshot, revision)
	_, err = Update(s.path, func(state *State) error {
		entry, found := state.Find(assetID)
		if !found || entry.Running == nil || entry.Running.RunID != "" || entry.Running.Revision.Key() != revision.Key() {
			return nil
		}
		if startErr != nil {
			entry.Running.State, entry.Running.Error = RunFailed, startErr.Error()
			entry.Running.FinishedAt = s.deps.Now()
			entry.Failed, entry.Running = entry.Running, nil
			return nil
		}
		entry.Running.RunID = runID
		return nil
	})
	if startErr != nil {
		return startErr
	}
	s.deps.Logf("autobuild %s: building revision %d (run %s)", assetID, revision.Number, runID)
	return err
}

// Enable switches Auto-build on for a map with a pipeline. The revision current
// at the next check becomes the baseline; nothing is built for it.
func (s *Service) Enable(assetID, displayName, pipelineID string) (*Entry, error) {
	assetID, pipelineID = strings.TrimSpace(assetID), strings.TrimSpace(pipelineID)
	if assetID == "" || pipelineID == "" {
		return nil, errors.New("autobuild: switching it on needs a map and a build profile")
	}
	var out Entry
	_, err := Update(s.path, func(state *State) error {
		entry, found := state.Find(assetID)
		next := Entry{AssetID: assetID}
		if found {
			next = *entry
		}
		if displayName != "" {
			next.DisplayName = displayName
		}
		next.PipelineID = pipelineID
		next.Enabled = true
		next.Generation++
		next.EnabledAt = s.deps.Now()
		next.Baseline, next.Pending = nil, nil
		next.Halted, next.CheckError, next.Failures = "", "", 0
		next.NextCheckAt = time.Time{}
		out = *state.put(next)
		return nil
	})
	return &out, err
}

// Disable switches it off. A build already running is left to run, under the
// ordinary job controls, and is still recorded when it ends. A question to AUB
// in flight is not waited for: its answer is not used.
func (s *Service) Disable(assetID string) (*Entry, error) {
	return s.change(assetID, func(entry *Entry) error {
		entry.Enabled, entry.Pending = false, nil
		entry.NextCheckAt = time.Time{}
		return nil
	})
}

// SetPipeline changes the build profile the next build uses.
func (s *Service) SetPipeline(assetID, pipelineID string) (*Entry, error) {
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return nil, errors.New("autobuild: choose a build profile")
	}
	return s.change(assetID, func(entry *Entry) error {
		entry.PipelineID = pipelineID
		return nil
	})
}

// change is one of a person's changes to a map's auto-build: it moves the
// entry to its next generation, so an answer asked for before it is not used.
func (s *Service) change(assetID string, mutate func(*Entry) error) (*Entry, error) {
	var out Entry
	_, err := Update(s.path, func(state *State) error {
		entry, found := state.Find(assetID)
		if !found {
			return fmt.Errorf("autobuild: %s has no auto-build", assetID)
		}
		if err := mutate(entry); err != nil {
			return err
		}
		entry.Generation++
		out = *entry
		return nil
	})
	return &out, err
}

// BuildNow builds the map's current revision now — the explicit action, which
// is never implied by switching Auto-build on. A pipeline given here is the
// one recorded for the map; a map never switched on gets an entry that stays
// switched off, so the build has somewhere to be recorded. AUB is asked with
// no lock held, like the poller's questions.
func (s *Service) BuildNow(ctx context.Context, assetID, pipelineID, displayName string) (*Entry, error) {
	pipelineID = strings.TrimSpace(pipelineID)
	var busy bool
	var generation uint64
	if _, err := Update(s.path, func(state *State) error {
		entry, found := state.Find(assetID)
		if !found {
			if pipelineID == "" {
				return fmt.Errorf("autobuild: choose a build profile for %s first", assetID)
			}
			entry = state.put(Entry{AssetID: assetID})
		}
		if pipelineID != "" && pipelineID != entry.PipelineID {
			entry.PipelineID = pipelineID
			entry.Generation++
		}
		if displayName != "" {
			entry.DisplayName = displayName
		}
		busy, generation = entry.Running != nil, entry.Generation
		return nil
	}); err != nil {
		return nil, err
	}
	if busy {
		return nil, ErrBusy
	}
	asked, cancel := context.WithTimeout(ctx, checkTimeout)
	revision, name, err := s.deps.Current(asked, assetID)
	cancel()
	if err != nil {
		return nil, err
	}
	if !revision.Valid() {
		return nil, errors.New("autobuild: AUB's answer about this map named no revision")
	}
	if _, err := Update(s.path, func(fresh *State) error {
		found, ok := fresh.Find(assetID)
		if !ok {
			return errUnchanged
		}
		if found.Observed == nil || found.Observed.Number <= revision.Number {
			found.Observed = &revision
		}
		found.LastCheckAt = s.deps.Now()
		if name != "" {
			found.DisplayName = name
		}
		if found.Baseline == nil && found.Enabled && found.Generation == generation {
			found.Baseline = &revision
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.submit(assetID, true, func(*Entry) (Revision, bool) { return revision, true }); err != nil {
		return nil, err
	}
	return s.entry(assetID)
}

// Retry builds the revision whose build failed, once, because somebody asked.
func (s *Service) Retry(assetID string) (*Entry, error) {
	err := s.submit(assetID, true, func(entry *Entry) (Revision, bool) {
		if entry.Failed == nil {
			return Revision{}, false
		}
		return entry.Failed.Revision, true
	})
	if errors.Is(err, errNotOwed) {
		return nil, fmt.Errorf("autobuild: %s has no failed build to retry", assetID)
	}
	if err != nil {
		return nil, err
	}
	return s.entry(assetID)
}

func (s *Service) entry(assetID string) (*Entry, error) {
	state, err := Load(s.path)
	if err != nil {
		return nil, err
	}
	entry, found := state.Find(assetID)
	if !found {
		return nil, fmt.Errorf("autobuild: %s has no auto-build", assetID)
	}
	out := *entry
	return &out, nil
}
