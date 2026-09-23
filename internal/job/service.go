package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The service: one queue, one set of workers, one place a job's state changes.
//
// Everything that runs a program in this repository goes through Submit. There
// is no second path for a built-in profile, none for the CLI, none for the GUI,
// and none for "just this once". That is what makes the guarantees in this
// package worth stating: a rule the executor enforces is a rule that holds,
// rather than a rule that holds on the path somebody remembered to route
// through it.

// defaultQueueDepth bounds how many jobs may be waiting. A local build tool is
// not a job server; a queue deeper than this is a caller in a loop, and telling
// them so immediately is better than accepting work nobody is waiting for.
const defaultQueueDepth = 256

// BindingLookup returns the local binding for a profile: where its executables
// are on this machine, which roots it may reach, and what the user granted.
//
// A function rather than a store, because the job service has no business
// knowing what file bindings live in. The CLI reads one path, the server
// another, and a test supplies a value directly.
type BindingLookup func(profileID string) (binding.LocalBinding, bool)

// Options configures a Service.
type Options struct {
	Store   *Store
	Catalog Catalog
	// Bindings resolves a profile's local installation. Nil means no bindings
	// are recorded, which is the state of a fresh machine: a job then depends
	// entirely on what its request supplies.
	Bindings BindingLookup
	// Concurrency is how many jobs run at once. Zero picks a default from the
	// machine.
	Concurrency int
	// QueueDepth bounds the waiting jobs. Zero means defaultQueueDepth.
	QueueDepth int
	// MaxTimeout caps what an action may ask for. Zero means no cap beyond the
	// action's own.
	MaxTimeout time.Duration
	// Secrets supplies literal strings that must never reach a log or a record
	// — the AUB session token, when there is one. A function rather than a
	// value so the service never holds a credential of its own, and so a token
	// that changes mid-session is redacted with its current value.
	Secrets func() []string
	// LookupEnv reads the Companion's own environment, for the names an action
	// declared as inherited. Nil means os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// KeepWorkspace keeps a successful job's scratch directory. Failed and
	// interrupted jobs always keep theirs.
	KeepWorkspace bool
	// Logf receives one line per lifecycle event. Nil discards them.
	Logf func(format string, args ...any)
	// OnFinished is told about every job that reaches a terminal state, with a
	// copy of the record and the correlation id its submitter carried ("" when
	// none). It is how a failure is reported as an incident without this
	// package knowing incidents exist. Called synchronously from the worker,
	// so it must not block; nil means nobody is listening.
	OnFinished func(j *Job, correlationID string)
}

// Service is the job runtime.
type Service struct {
	store       *Store
	catalog     Catalog
	bindings    BindingLookup
	concurrency int
	maxTimeout  time.Duration
	secrets     func() []string
	lookupEnv   func(string) (string, bool)
	now         func() time.Time
	keep        bool
	logf        func(string, ...any)
	onFinished  func(*Job, string)

	queue chan string

	mu sync.Mutex
	// mirrors carries a live output writer for a job submitted by something
	// that is watching it. Not persisted, and dropped when the job ends.
	mirrors map[string]io.Writer
	// owned is the ids this process is responsible for right now. The
	// heartbeat walks this rather than the store: a store with a thousand
	// finished jobs would otherwise be re-read from disk every two seconds to
	// find the two that are running.
	owned map[string]bool
	// correlations holds the correlation id a submission carried, by job id,
	// until the job finishes. Memory only: see Request.CorrelationID.
	correlations map[string]string
	closed       bool

	workers sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
}

// NewService builds a service. It does not start workers; see [Service.Start].
func NewService(options Options) (*Service, error) {
	if options.Store == nil {
		return nil, errors.New("job: the service needs a store")
	}
	if options.Catalog == nil {
		return nil, errors.New("job: the service needs a profile catalog")
	}
	concurrency := options.Concurrency
	if concurrency <= 0 {
		// Map compilers are already threaded, so running many at once mostly
		// makes each slower. Two is enough for the case this is really for:
		// a build in progress while the user starts another.
		concurrency = 2
		if runtime.NumCPU() >= 8 {
			concurrency = 4
		}
	}
	depth := options.QueueDepth
	if depth <= 0 {
		depth = defaultQueueDepth
	}
	service := &Service{
		store:        options.Store,
		catalog:      options.Catalog,
		bindings:     options.Bindings,
		concurrency:  concurrency,
		maxTimeout:   options.MaxTimeout,
		secrets:      options.Secrets,
		lookupEnv:    options.LookupEnv,
		now:          options.Now,
		keep:         options.KeepWorkspace,
		logf:         options.Logf,
		onFinished:   options.OnFinished,
		queue:        make(chan string, depth),
		mirrors:      map[string]io.Writer{},
		owned:        map[string]bool{},
		correlations: map[string]string{},
	}
	if service.lookupEnv == nil {
		service.lookupEnv = os.LookupEnv
	}
	if service.now == nil {
		service.now = func() time.Time { return time.Now().UTC() }
	}
	if service.logf == nil {
		service.logf = func(string, ...any) {}
	}
	return service, nil
}

// Store is the service's job store, for readers that only need to look.
func (s *Service) Store() *Store { return s.store }

// Catalog is the service's profile catalog.
func (s *Service) Catalog() Catalog { return s.catalog }

// Start recovers abandoned jobs and launches the workers.
//
// Recovery first, and always before the first job is accepted: a store still
// claiming that something is running is a store whose next reader would believe
// it. See [Store.Recover].
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("job: the service is already started")
	}
	s.started = true
	s.mu.Unlock()

	recovered, err := s.store.Recover(s.now())
	if err != nil {
		return err
	}
	for _, id := range recovered {
		s.logf("job %s was interrupted by a previous shutdown; retry it explicitly to run it again", id)
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	for i := 0; i < s.concurrency; i++ {
		s.workers.Add(1)
		go s.worker()
	}
	s.workers.Add(1)
	go s.heartbeat()
	return nil
}

// Close stops accepting work, takes down anything running, and waits.
//
// A job stopped this way is [Interrupted], not [Failed]: the reason it stopped
// was this program shutting down, and recording it as the tool's failure would
// be blaming the wrong one.
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.queue)
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
	return nil
}

// heartbeat keeps this process's claim on the jobs it owns fresh, so another
// process's recovery pass leaves them alone. See [Store.Recover].
func (s *Service) heartbeat() {
	defer s.workers.Done()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			ids := make([]string, 0, len(s.owned))
			for id := range s.owned {
				ids = append(ids, id)
			}
			s.mu.Unlock()
			for _, id := range ids {
				_ = s.store.Heartbeat(id, s.now())
			}
			// Recovery is not only a start-up act. A Companion restarted within
			// heartbeatStale of a crash found the crashed one's claim still
			// fresh and left the job `running` until the next restart, however
			// long that was (NEW_244D). Jobs this process supervises heartbeat
			// every tick above and are never stale here.
			if tick++; tick%recoverEveryTicks == 0 {
				if recovered, err := s.store.Recover(s.now(), ids...); err == nil {
					for _, id := range recovered {
						s.logf("job %s: its Companion stopped while it ran; it is marked interrupted and is not run again", id)
					}
				}
			}
		}
	}
}

func (s *Service) worker() {
	defer s.workers.Done()
	for id := range s.queue {
		s.execute(id)
	}
}

// Submit accepts a job: validates the request against the profile, writes the
// record, and queues it.
//
// Everything that can be refused before a job exists is refused here — an
// unknown profile, an unknown action, an ungranted permission, options the
// action does not declare — so that a rejected request produces one clear error
// instead of a failed job the user has to open to find out why.
func (s *Service) Submit(request Request) (*Job, error) {
	s.mu.Lock()
	closed, started := s.closed, s.started
	s.mu.Unlock()
	if closed {
		return nil, errors.New("job: the Companion is shutting down and is not accepting jobs")
	}
	if !started {
		// Without workers a submission would be recorded, queued, and never
		// looked at again — a job that sits in `queued` forever, which is a
		// worse answer than a refusal. Reading the store needs no workers, so
		// list, show, logs and cancel work on a service nobody started.
		return nil, errors.New("job: this job service was opened for reading only and cannot run anything")
	}

	entry, action, err := s.resolveAction(request)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(entry, request); err != nil {
		return nil, err
	}

	now := s.now()
	id, err := NewID(now)
	if err != nil {
		return nil, err
	}
	// Resolved once here so a request that cannot become a command is refused
	// rather than queued. The result is thrown away: the job resolves again
	// when it runs, against its own staged inputs.
	if _, err := s.resolve(id, request, entry, action, false); err != nil {
		return nil, err
	}

	meta := entry.Profile.Metadata()
	j := &Job{
		SchemaVersion:  SchemaVersion,
		ID:             id,
		State:          Queued,
		Request:        request.clone(),
		ProfileID:      meta.ID,
		ProfileVersion: meta.Version,
		ProfileDigest:  entry.Digest,
		ProfileName:    meta.Name,
		ActionID:       action.ID,
		ActionTitle:    action.Title,
		Trust:          entry.Trust,
		SessionRole:    action.SessionRole,
		Installs:       s.installs(request.ProfileID),
		TimeoutSeconds: action.TimeoutSeconds,
		CreatedAt:      now,
		Owner:          Owner{PID: os.Getpid()},
		History:        []Event{{State: Queued, At: now}},
	}
	if err := s.store.Save(j); err != nil {
		return nil, err
	}
	if err := s.store.Heartbeat(id, now); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.owned[id] = true
	if request.CorrelationID != "" {
		s.correlations[id] = request.CorrelationID
	}
	s.mu.Unlock()

	select {
	case s.queue <- id:
	default:
		s.finish(j, Failed, "the job queue is full; wait for the running jobs to finish", nil)
		return nil, fmt.Errorf("job: the queue is full (%d waiting)", cap(s.queue))
	}
	s.logf("job %s queued: %s", id, j.summarise())
	return j.Clone(), nil
}

// SubmitWatched is Submit with a writer that receives the program's output as
// it is produced.
//
// The writer is not persisted and is dropped when the job ends. It is how
// `companion job run --wait` shows a build happening rather than printing it
// afterwards, without a second execution path that streams.
func (s *Service) SubmitWatched(request Request, mirror io.Writer) (*Job, error) {
	j, err := s.Submit(request)
	if err != nil {
		return nil, err
	}
	if mirror != nil {
		s.mu.Lock()
		s.mirrors[j.ID] = mirror
		s.mu.Unlock()
	}
	return j, nil
}

// Preview resolves a request into the exact command it would run, and starts
// nothing.
//
// The workspace path it resolves against is the one a job with the returned id
// would get. That is what makes the preview honest: it is the same [Resolve]
// call, against the same layout, producing the same [profile.Command.Digest] as
// the run. Nothing is written to disk.
func (s *Service) Preview(request Request) (*Job, error) {
	entry, action, err := s.resolveAction(request)
	if err != nil {
		return nil, err
	}
	authErr := s.authorize(entry, request)

	now := s.now()
	id, err := NewID(now)
	if err != nil {
		return nil, err
	}
	invocation, err := s.resolve(id, request, entry, action, false)
	if err != nil {
		return nil, err
	}
	meta := entry.Profile.Metadata()
	j := &Job{
		SchemaVersion:  SchemaVersion,
		ID:             id,
		State:          Queued,
		Request:        request.clone(),
		ProfileID:      meta.ID,
		ProfileVersion: meta.Version,
		ProfileDigest:  entry.Digest,
		ProfileName:    meta.Name,
		ActionID:       action.ID,
		ActionTitle:    action.Title,
		Trust:          entry.Trust,
		SessionRole:    invocation.SessionRole,
		Installs:       s.installs(request.ProfileID),
		TimeoutSeconds: invocation.TimeoutSeconds,
		Workspace:      s.store.layout(id).Workspace,
		CreatedAt:      now,
		Command:        preview(invocation, s.store.layout(id), action, s.lookupEnv, s.redactor()),
	}
	if authErr != nil {
		// Shown rather than withheld. Reviewing what a profile would run is
		// exactly what somebody does *before* granting it anything, so a
		// preview that refused until the grant existed would have the order
		// backwards.
		j.Error = authErr.Error()
	}
	return j, nil
}

// Get returns one job.
func (s *Service) Get(id string) (*Job, error) {
	j, err := s.store.Load(id)
	if err != nil {
		return nil, err
	}
	return j.Clone(), nil
}

// List returns every job, newest first.
func (s *Service) List() ([]*Job, error) {
	jobs, err := s.store.List()
	if err != nil {
		return nil, err
	}
	out := make([]*Job, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.Clone())
	}
	return out, nil
}

// Cancel asks a job to stop.
//
// It works whichever process is supervising the job: the request is a marker in
// the job's own directory, which the owning executor notices within a second.
// A job that has not started yet is stopped here and now, because there is no
// process to wait for.
func (s *Service) Cancel(id string) (*Job, error) {
	j, err := s.store.Load(id)
	if err != nil {
		return nil, err
	}
	if j.State.Terminal() {
		return nil, &TransitionError{JobID: id, From: j.State, To: Cancelling}
	}
	if err := s.store.RequestCancel(id); err != nil {
		return nil, err
	}
	if j.State == Queued {
		// Nothing has been resolved and nothing started. The worker will find
		// the marker before it does either, but recording it now means the
		// answer to "did that stop" does not depend on a queue this caller
		// cannot see.
		s.transition(j, Cancelling, "a stop was asked for before the job started")
		if err := s.store.Save(j); err != nil {
			return nil, err
		}
	}
	s.logf("job %s: stop requested", id)
	return j.Clone(), nil
}

// Retry runs a finished job's request again, as a new job.
//
// A new job, always. Re-running in place would destroy the record of what
// happened the first time, and for an [Interrupted] job — one whose outcome
// nobody knows — it would be the Companion deciding on its own that running the
// command a second time is safe. It is not this program's decision to make.
func (s *Service) Retry(id string) (*Job, error) {
	previous, err := s.store.Load(id)
	if err != nil {
		return nil, err
	}
	if !previous.Retryable() {
		return nil, fmt.Errorf("job: %s is %s; wait for it to finish, or cancel it, before running it again", id, previous.State)
	}
	request := previous.Request.clone()
	request.RetryOf = previous.ID
	return s.Submit(request)
}

// Logs returns one stream of a job's output.
//
// raw is the bytes the program wrote. Anything else is the user view: valid
// UTF-8, no terminal control sequences, credentials redacted. See redact.go for
// why both exist.
func (s *Service) Logs(id, stream string, raw bool) ([]byte, error) {
	name := stdoutLogName
	switch stream {
	case "", "stdout":
	case "stderr":
		name = stderrLogName
	default:
		return nil, fmt.Errorf("job: %q is not a stream; use stdout or stderr", stream)
	}
	data, err := s.store.ReadLog(id, name)
	if err != nil {
		return nil, err
	}
	if raw {
		return data, nil
	}
	return []byte(s.redactor().UserView(data)), nil
}

// Wait blocks until a job reaches a terminal state.
//
// It polls the record rather than waiting on an in-memory signal, so it works
// for a job another process is supervising — which is what `companion job run
// --wait` needs when the GUI server is the one running it.
func (s *Service) Wait(ctx context.Context, id string) (*Job, error) {
	for {
		j, err := s.store.Load(id)
		if err != nil {
			return nil, err
		}
		if j.State.Terminal() {
			return j.Clone(), nil
		}
		select {
		case <-ctx.Done():
			return j.Clone(), ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// redactor builds the current redactor from the caller's secret source.
func (s *Service) redactor() *Redactor {
	if s.secrets == nil {
		return NewRedactor()
	}
	return NewRedactor(s.secrets()...)
}

// resolveAction finds the profile and the action a request names.
func (s *Service) resolveAction(request Request) (CatalogEntry, profile.Action, error) {
	if strings.TrimSpace(request.ProfileID) == "" {
		return CatalogEntry{}, profile.Action{}, errors.New("job: the request names no profile")
	}
	if strings.TrimSpace(request.ActionID) == "" {
		return CatalogEntry{}, profile.Action{}, errors.New("job: the request names no action")
	}
	entry, err := s.catalog.Lookup(request.ProfileID)
	if err != nil {
		return CatalogEntry{}, profile.Action{}, err
	}
	// Validated on every submission, not only on import. A document on disk can
	// change after it was approved, and a profile that no longer validates is
	// one whose permissions summary is no longer the one the user read.
	if err := entry.Profile.Validate(); err != nil {
		return CatalogEntry{}, profile.Action{}, fmt.Errorf("job: %s is no longer a valid profile: %w", request.ProfileID, err)
	}
	action, found := entry.Profile.ActionByID(request.ActionID)
	if !found {
		ids := make([]string, 0)
		for _, candidate := range entry.Profile.ActionList() {
			ids = append(ids, candidate.ID)
		}
		sort.Strings(ids)
		return CatalogEntry{}, profile.Action{}, fmt.Errorf("job: %s has no action %q (it has: %s)",
			request.ProfileID, request.ActionID, strings.Join(ids, ", "))
	}
	return entry, action, nil
}

// authorize is the gate. A built-in profile passes because installing the build
// was the decision; anything else needs a grant recorded against the digest of
// the document as it is now.
func (s *Service) authorize(entry CatalogEntry, request Request) error {
	var grant *profile.Grant
	if local, found := s.binding(request.ProfileID); found {
		grant = local.Grant
	}
	return profile.Authorize(entry.Profile, entry.Trust, entry.Digest, grant)
}

// installs is which downloaded packages a job depends on, copied onto the
// record when it is created.
//
// Copied rather than looked up later, for the same reason the profile digest
// is: the question the record answers is "what did this job run", and a binding
// can be re-pointed at a different version tomorrow. It is also what keeps a
// retained job's toolchain out of the cache collector's reach — a record that
// named nothing would let the evidence for a build outlive the compiler that
// produced it.
func (s *Service) installs(profileID string) []string {
	local, found := s.binding(profileID)
	if !found || len(local.Installs) == 0 {
		return nil
	}
	digests := make([]string, 0, len(local.Installs))
	for _, install := range local.Installs {
		digests = append(digests, install.Digest)
	}
	return digests
}

func (s *Service) binding(profileID string) (binding.LocalBinding, bool) {
	if s.bindings == nil {
		return binding.LocalBinding{}, false
	}
	return s.bindings(profileID)
}

// roots merges the machine's roots for a profile: the binding's, then the
// request's overrides, then the job's own workspace.
//
// The workspace is last and cannot be overridden. It is created and destroyed
// by this package, and a request that could point it somewhere else could point
// it at the user's home directory.
func (s *Service) roots(id string, request Request) map[string]string {
	out := map[string]string{}
	if local, found := s.binding(request.ProfileID); found {
		for role, path := range local.Roots {
			out[role] = path
		}
	}
	for role, path := range request.Roots {
		if role == profile.RootWorkspace {
			continue
		}
		out[role] = path
	}
	out[profile.RootWorkspace] = s.store.layout(id).Workspace
	return out
}

// executables merges the binding's resolved executable paths with the request's
// overrides.
func (s *Service) executables(request Request) map[string]string {
	out := map[string]string{}
	if local, found := s.binding(request.ProfileID); found {
		for name, path := range local.Executables {
			out[name] = path
		}
	}
	for name, path := range request.Executables {
		out[name] = path
	}
	return out
}

// inputRoots is the set a staged input's source must come from.
//
// The job's declared roots, minus the workspace, which does not exist yet at
// staging time and holds nothing of the user's. Empty means unrestricted — see
// [plannedInput] for why that is the right answer on a machine with no
// bindings.
func (s *Service) inputRoots(id string, request Request) []string {
	roots := s.roots(id, request)
	out := make([]string, 0, len(roots))
	for _, role := range sortedKeys(roots) {
		if role == profile.RootWorkspace {
			continue
		}
		out = append(out, roots[role])
	}
	return out
}

// resolve turns a request into an exact invocation.
//
// stage decides whether the user's input files are actually copied. False is
// the preview and the submit-time check: the same paths are computed and the
// same errors are raised, without touching anything.
func (s *Service) resolve(id string, request Request, entry CatalogEntry, action profile.Action, stage bool) (profile.Invocation, error) {
	l := s.store.layout(id)
	allowed := s.inputRoots(id, request)
	groups := make(map[string]string, len(action.Inputs))
	for _, declared := range action.Inputs {
		groups[declared.Name] = declared.StageGroup()
	}
	inputs := make(map[string]string, len(request.Inputs))
	for _, name := range sortedKeys(request.Inputs) {
		resolved, destination, err := plannedInput(l, name, groups[name], request.Inputs[name], allowed)
		if err != nil {
			return profile.Invocation{}, err
		}
		if stage {
			if err := stageInput(name, resolved, destination); err != nil {
				return profile.Invocation{}, err
			}
		}
		inputs[name] = destination
	}

	hostEnv := map[string]string{}
	if action.Environment != nil {
		for _, name := range action.Environment.Inherit {
			if value, present := s.lookupEnv(name); present {
				hostEnv[name] = value
			}
		}
	}

	invocation, err := profile.Resolve(entry.Profile, action.ID, profile.Request{
		Platform:    profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Roots:       s.roots(id, request),
		Executables: s.executables(request),
		Inputs:      inputs,
		Options:     request.Options,
		Runtime:     request.Runtime,
		HostEnv:     hostEnv,
	})
	if err != nil {
		return profile.Invocation{}, err
	}
	invocation.ProfileDigest = entry.Digest
	return invocation, nil
}

// execute is one job's whole life inside a worker.
func (s *Service) execute(id string) {
	j, err := s.store.Load(id)
	if err != nil {
		s.logf("job %s: %v", id, err)
		return
	}
	defer func() {
		s.mu.Lock()
		delete(s.mirrors, id)
		delete(s.owned, id)
		s.mu.Unlock()
	}()

	if s.store.CancelRequested(id) {
		s.finish(j, Cancelled, "stopped before it started", nil)
		return
	}
	// A job still in the queue when the Companion started shutting down. It is
	// marked interrupted without being started: beginning a process here would
	// mean spawning something only to kill it a moment later, and telling the
	// user it "ran".
	if err := s.ctxOrBackground().Err(); err != nil {
		s.finish(j, Interrupted, "the Companion shut down before this job started", nil)
		return
	}
	if err := s.step(j, Resolving, ""); err != nil {
		s.logf("job %s: %v", id, err)
		return
	}

	l := s.store.layout(id)
	if err := l.create(); err != nil {
		s.finish(j, Failed, err.Error(), nil)
		return
	}
	j.Workspace = l.Workspace
	j.ArtifactDir = l.Artifacts

	entry, action, err := s.resolveAction(j.Request)
	if err != nil {
		s.finish(j, Failed, err.Error(), nil)
		return
	}
	if err := s.authorize(entry, j.Request); err != nil {
		s.finish(j, Failed, err.Error(), nil)
		return
	}
	// The digest is re-read here, not carried from submission: a document on
	// disk can change between the two, and running one the user approved the
	// other version of is exactly what the digest exists to prevent.
	if j.ProfileDigest != "" && entry.Digest != j.ProfileDigest {
		s.finish(j, Failed, fmt.Sprintf("job: %s changed on disk after this job was queued (%s, now %s)",
			j.ProfileID, j.ProfileDigest, entry.Digest), nil)
		return
	}

	invocation, err := s.resolve(id, j.Request, entry, action, true)
	if err != nil {
		s.finish(j, Failed, err.Error(), nil)
		return
	}

	s.mu.Lock()
	mirror := s.mirrors[id]
	s.mu.Unlock()

	run := &execution{
		id:         id,
		layout:     l,
		invocation: invocation,
		action:     action,
		store:      s.store,
		redactor:   s.redactor(),
		mirror:     mirror,
		lookupEnv:  s.lookupEnv,
		maxTimeout: s.maxTimeout,
		onStarted: func(pid int, command *CommandPreview) error {
			j.Command = command
			j.Owner = Owner{PID: os.Getpid()}
			j.Process = ProcessIdentity{PID: pid, StartTicks: processStartTicks(pid)}
			j.StartedAt = s.now()
			j.TimeoutSeconds = invocation.TimeoutSeconds
			return s.step(j, Running, fmt.Sprintf("pid %d", pid))
		},
	}
	result := run.run(s.ctxOrBackground())

	if j.Command == nil {
		j.Command = result.command
	}
	j.Stdout = result.stdout
	j.Stderr = result.stderr
	j.Diagnostics = result.diagnostics
	j.ExitCode = result.exitCode
	j.TimedOut = result.reason == stopTimedOut

	// Outputs are collected whatever the outcome. A failed compile still wrote
	// a point file, and that is the thing a user needs to find the leak.
	roles := map[string]string{}
	optional := map[string]bool{}
	for _, output := range action.Outputs {
		roles[output.Name] = output.Role
		optional[output.Name] = output.Optional
	}
	artifacts, collectErr := collect(l, invocation.Outputs, roles, optional)
	j.Artifacts = artifacts

	switch {
	case result.reason == stopShutdown:
		s.finish(j, Interrupted, "the Companion shut down while this job was running", nil)
		return
	case result.reason == stopCancelled || s.store.CancelRequested(id):
		// A started job reaches Cancelled through Cancelling, as the state
		// machine says. Going straight there was a transition finish() had to
		// force, and it logged the violation on every Stop.
		if j.State == Running || j.State == Resolving {
			if err := s.step(j, Cancelling, "a stop was asked for while it was running"); err != nil {
				s.logf("job %s: %v", id, err)
			}
		}
		s.finish(j, Cancelled, "stopped because you asked", nil)
		return
	case result.err != nil && result.exitCode != nil && cleanStopProof(action, result.diagnostics) != "":
		// The program printed the line its profile says only a normal quit
		// prints, then exited non-zero: a quit that crashed on the way out.
		// Recorded as a stop, with the exit status and the line kept as the
		// evidence, so a hosted game ends `host_stopped` and not `host_crashed`.
		s.finish(j, Succeeded, fmt.Sprintf("clean stop: exited with status %d after %q (%s)",
			*result.exitCode, cleanStopProof(action, result.diagnostics), result.err.Error()), nil)
		return
	case result.err != nil:
		s.finish(j, Failed, result.err.Error(), nil)
		return
	case collectErr != nil:
		s.finish(j, Failed, collectErr.Error(), nil)
		return
	}
	s.finish(j, Succeeded, "", func() {
		if !s.keep {
			// The workspace goes; the artifacts and the logs stay. Nothing here
			// can reach a file the user owns: inputs were copied in, outputs
			// were copied out.
			if err := l.remove(); err != nil {
				s.logf("job %s: %v", id, err)
			}
		}
	})
}

func (s *Service) ctxOrBackground() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// step moves a job to a new state and persists it.
func (s *Service) step(j *Job, to State, note string) error {
	if err := Transition(j.ID, j.State, to); err != nil {
		return err
	}
	s.transition(j, to, note)
	return s.store.Save(j)
}

func (s *Service) transition(j *Job, to State, note string) {
	j.State = to
	j.History = append(j.History, Event{State: to, At: s.now(), Note: note})
}

// cleanStopProof returns the raw line of the first diagnostic whose rule the
// action marks `clean_stop`, or "" when the program printed none. Only the
// program's own words decide it; the exit status alone never does.
func cleanStopProof(action profile.Action, diagnostics []Diagnostic) string {
	for _, rule := range action.Diagnostics {
		if !rule.CleanStop {
			continue
		}
		for _, d := range diagnostics {
			if d.RuleID == rule.ID {
				return d.Raw
			}
		}
	}

	return ""
}

// finish records a terminal state. It is the only way a job stops.
func (s *Service) finish(j *Job, state State, message string, after func()) {
	if j.State.Terminal() {
		return
	}
	if err := Transition(j.ID, j.State, state); err != nil {
		// A state machine violation is a bug in this file, not something a user
		// can act on. It is recorded on the job rather than swallowed, so the
		// evidence survives.
		s.logf("job %s: %v", j.ID, err)
		j.History = append(j.History, Event{State: state, At: s.now(), Note: "forced: " + err.Error()})
		j.State = state
	} else {
		s.transition(j, state, message)
	}
	if state != Succeeded && message != "" {
		j.Error = s.redactor().Redact(message)
	}
	j.FinishedAt = s.now()
	j.Owner = Owner{}
	if err := s.store.Save(j); err != nil {
		s.logf("job %s: %v", j.ID, err)
	}
	_ = s.store.ClearCancel(j.ID)
	if after != nil {
		after()
	}
	s.logf("job %s %s", j.ID, state)
	s.mu.Lock()
	correlation := s.correlations[j.ID]
	delete(s.correlations, j.ID)
	s.mu.Unlock()
	if s.onFinished != nil {
		s.onFinished(j.Clone(), correlation)
	}
}
