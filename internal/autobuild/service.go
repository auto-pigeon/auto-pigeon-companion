package autobuild

import (
	"context"
	"errors"
	"fmt"
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
// the loop and cannot overlap the next check.
const checkTimeout = 20 * time.Second

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
// one loop, one tick at a time, whoever asked for it.
type Service struct {
	path string
	deps Deps
	mu   sync.Mutex
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
	return &Service{path: path, deps: deps}, nil
}

// Path is the state file.
func (s *Service) Path() string { return s.path }

// Run ticks until the context ends. Started once by the server: there is one
// poller per Companion, whatever number of browser tabs are open.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	for {
		if err := s.Tick(ctx); err != nil {
			s.deps.Logf("autobuild: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// State reads the current state.
func (s *Service) State() (*State, error) { return Load(s.path) }

// observation is one answer from AUB, gathered outside the file lock.
type observation struct {
	revision Revision
	name     string
	err      error
	at       time.Time
}

// Tick is one pass: notice finished builds, ask about the maps that are due,
// and start the builds that are now owed.
func (s *Service) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := Load(s.path)
	if err != nil {
		return err
	}
	if len(state.Entries) == 0 {
		// Nothing was ever switched on: no file is written and nobody is asked.
		return nil
	}
	now := s.deps.Now()

	// The questions, asked with no lock held: a slow AUB must not stop a page
	// from switching Auto-build off.
	answers := map[string]observation{}
	for _, entry := range state.Entries {
		if !entry.Enabled || entry.Halted != "" || now.Before(entry.NextCheckAt) {
			continue
		}
		asked, cancel := context.WithTimeout(ctx, checkTimeout)
		revision, name, err := s.deps.Current(asked, entry.AssetID)
		cancel()
		if err == nil && !revision.Valid() {
			err = fmt.Errorf("AUB's answer about this map named no revision (id %q, number %d)", revision.ID, revision.Number)
		}
		answers[entry.AssetID] = observation{revision: revision, name: name, err: err, at: s.deps.Now()}
	}

	_, err = Update(s.path, func(fresh *State) error {
		for i := range fresh.Entries {
			entry := &fresh.Entries[i]
			s.finish(entry)
			if answer, asked := answers[entry.AssetID]; asked && entry.Enabled {
				s.observe(entry, answer)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.startOwed()
}

// finish records a build that has stopped.
func (s *Service) finish(entry *Entry) {
	running := entry.Running
	if running == nil {
		return
	}
	if running.RunID == "" {
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

// startOwed starts the build of every enabled map that is waiting for one and
// has none running.
func (s *Service) startOwed() error {
	state, err := Load(s.path)
	if err != nil {
		return err
	}
	for _, entry := range state.Entries {
		if !entry.Enabled || entry.Running != nil || entry.Pending == nil || entry.submitted(entry.Pending.Key()) {
			continue
		}
		if err := s.start(entry.AssetID, *entry.Pending, false); err != nil && !errors.Is(err, ErrBusy) {
			s.deps.Logf("autobuild %s: %v", entry.AssetID, err)
		}
	}
	return nil
}

// start submits a build of one revision: written down first, then started.
func (s *Service) start(assetID string, revision Revision, manual bool) error {
	var snapshot Entry
	_, err := Update(s.path, func(state *State) error {
		entry, found := state.Find(assetID)
		if !found {
			return fmt.Errorf("autobuild: %s has no auto-build", assetID)
		}
		if entry.Running != nil {
			return ErrBusy
		}
		if strings.TrimSpace(entry.PipelineID) == "" {
			return errors.New("autobuild: choose a build profile first")
		}
		entry.remember(revision.Key())
		entry.Running = &Attempt{Revision: revision, Pipeline: entry.PipelineID, Manual: manual, StartedAt: s.deps.Now()}
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
		if !found || entry.Running == nil {
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
// ordinary job controls, and is still recorded when it ends.
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

func (s *Service) change(assetID string, mutate func(*Entry) error) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out Entry
	_, err := Update(s.path, func(state *State) error {
		entry, found := state.Find(assetID)
		if !found {
			return fmt.Errorf("autobuild: %s has no auto-build", assetID)
		}
		if err := mutate(entry); err != nil {
			return err
		}
		out = *entry
		return nil
	})
	return &out, err
}

// BuildNow builds the map's current revision now — the explicit action, which
// is never implied by switching Auto-build on. A pipeline given here is the
// one recorded for the map; a map never switched on gets an entry that stays
// switched off, so the build has somewhere to be recorded.
func (s *Service) BuildNow(ctx context.Context, assetID, pipelineID, displayName string) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pipelineID = strings.TrimSpace(pipelineID)
	var busy bool
	if _, err := Update(s.path, func(state *State) error {
		entry, found := state.Find(assetID)
		if !found {
			if pipelineID == "" {
				return fmt.Errorf("autobuild: choose a build profile for %s first", assetID)
			}
			entry = state.put(Entry{AssetID: assetID})
		}
		if pipelineID != "" {
			entry.PipelineID = pipelineID
		}
		if displayName != "" {
			entry.DisplayName = displayName
		}
		busy = entry.Running != nil
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
			return nil
		}
		found.Observed = &revision
		found.LastCheckAt = s.deps.Now()
		if name != "" {
			found.DisplayName = name
		}
		if found.Baseline == nil && found.Enabled {
			found.Baseline = &revision
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.start(assetID, revision, true); err != nil {
		return nil, err
	}
	return s.entry(assetID)
}

// Retry builds the revision whose build failed, once, because somebody asked.
func (s *Service) Retry(assetID string) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := Load(s.path)
	if err != nil {
		return nil, err
	}
	entry, found := state.Find(assetID)
	if !found || entry.Failed == nil {
		return nil, fmt.Errorf("autobuild: %s has no failed build to retry", assetID)
	}
	if entry.Running != nil {
		return nil, ErrBusy
	}
	if err := s.start(assetID, entry.Failed.Revision, true); err != nil {
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
