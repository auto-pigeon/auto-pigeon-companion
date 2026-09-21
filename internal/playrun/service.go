package playrun

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
)

// Deps are the services a run drives. Every one of them already exists
// elsewhere in this program, and none of them is reimplemented here.
//
// They arrive as functions rather than as concrete types for two reasons. The
// first is the boundary: this package must not learn how to talk to AUB, how to
// verify an extractor or how to start a process, because each of those has a
// package whose whole job that is. The second is that a coordinator is exactly
// the thing worth testing without a network, a compiler or a game installed.
type Deps struct {
	// FetchMap materializes the exact map revision named in the request and
	// returns the local file plus the source record.
	FetchMap func(ctx context.Context, request Request) (MapResult, error)

	// FetchBundle returns a verified texture bundle for that same revision.
	// It is responsible for the cache: a complete verified entry must be
	// reusable with no session and no network.
	FetchBundle func(ctx context.Context, request Request) (BundleResult, error)

	// Convert turns an APMap file into a Quake `.map` by running the extractor
	// as a separate process. It returns the original path unchanged, and a nil
	// extractor, when the file is already a `.map`.
	Convert func(ctx context.Context, request Request, mapFile string) (ConvertResult, error)

	// Build runs the pipeline. `announce` is called as the manifest changes,
	// which is how a compiling run reports the step and job a Cancel would
	// have to stop.
	Build func(ctx context.Context, request build.Request, announce func(*build.Manifest)) (*build.Manifest, error)

	// MapInputName says which declared input of a pipeline the map source is.
	// Asked of the pipeline document rather than assumed, so this package works
	// for a pipeline it has never seen.
	MapInputName func(pipelineID string) (string, error)

	// PlanInstall reads a finished build and the verified bundle and says what
	// goes into the mod directory. Separate from Install so "what would be
	// written" is answerable without writing it.
	PlanInstall func(ctx context.Context, record *Record) (InstallPlan, error)

	// Install stages the level, its WADs and the build manifest into the owned
	// mod directory, atomically, replacing only what the Companion staged
	// before.
	Install func(ctx context.Context, request Request, plan InstallPlan) (InstallResult, error)

	// VerifyInstalled re-checks the staged files immediately before launch.
	// Separate from Install so the check is a step with its own failure rather
	// than a claim Install makes about itself.
	VerifyInstalled func(ctx context.Context, request Request, files []StagedFile) error

	// Launch starts the engine and returns as soon as it has started. A
	// long-running engine being alive is a successful launch.
	Launch func(ctx context.Context, request Request) (LaunchRecord, error)

	// Unstage removes what a cancelled or failed install left, so a run never
	// leaves a half-installed mod. It is the same ownership-respecting removal
	// `companion engine unstage` performs.
	Unstage func(request Request) error

	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Logf receives one line per transition. Nil discards them.
	Logf func(format string, args ...any)

	// Live is the registry of runs executing in THIS process. A caller that
	// builds a Service per request must pass the same one every time, or a
	// cancel arriving on one Service cannot reach the run another is executing
	// (246I1.1: the cancel then recorded `cancelled` while the compile went on
	// to install and launch). Nil gives the Service a registry of its own.
	Live *Live
}

// MapResult is what [Deps.FetchMap] produced.
type MapResult struct {
	Path   string
	Source *build.SourceRef
}

// BundleResult is what [Deps.FetchBundle] produced.
type BundleResult struct {
	// Ref is the bundle's portable identity, recorded in the run and carried
	// into the build manifest.
	Ref *build.BundleRef
	// ContentRoot is the verified directory of original files on this machine,
	// and is what the build is given for the `content_root` role.
	ContentRoot string
}

// ConvertResult is what [Deps.Convert] produced.
type ConvertResult struct {
	// Path is the `.map` the build will compile. It is the input unchanged
	// when no conversion was needed.
	Path string
	// Extractor identifies the program that ran, or is nil when none did.
	Extractor *ExtractorRef
}

// InstallPlan is what a finished build offers the installer.
type InstallPlan struct {
	BSP string
	Lit string
	// WADs are the bundle's carried files: where each is on this machine, and
	// the relative path it takes inside `<mod>/wads`.
	WADs []InstallFile
	// BuildManifest is the build's own manifest.json.
	BuildManifest string
}

// InstallFile is one file to stage.
type InstallFile struct {
	// Path is relative and POSIX-spelled, inside the `wads` directory.
	Path string
	// Source is the file to copy.
	Source string
}

// InstallResult is what [Deps.Install] wrote.
type InstallResult struct {
	Dir   string
	Files []StagedFile
}

// ErrNotCompilerReady stops a run before the extractor or a compiler starts.
//
// Its own error because it is not a failure of this program: AUB has said which
// texture sources it cannot redistribute, and the answer is a list of named
// refusals rather than a retry. Nothing falls back to the installed `id1` WADs
// or to a similarly named local file.
var ErrNotCompilerReady = errors.New("playrun: this map's texture bundle is not compiler-ready")

// Live is the set of runs this process is executing, and how to stop each.
type Live struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewLive returns an empty registry. One per process, shared by every Service.
func NewLive() *Live { return &Live{running: map[string]context.CancelFunc{}} }

func (l *Live) add(id string, cancel context.CancelFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running[id] = cancel
}

func (l *Live) remove(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.running, id)
}

func (l *Live) lookup(id string) (context.CancelFunc, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cancel, ok := l.running[id]

	return cancel, ok
}

// Service runs and records Build & Run sequences.
type Service struct {
	store *Store
	deps  Deps
	live  *Live
}

// NewService builds a coordinator.
func NewService(store *Store, deps Deps) (*Service, error) {
	switch {
	case store == nil:
		return nil, errors.New("playrun: a service needs a store")
	case deps.FetchMap == nil, deps.FetchBundle == nil, deps.Convert == nil,
		deps.Build == nil, deps.Install == nil, deps.Launch == nil,
		deps.MapInputName == nil, deps.PlanInstall == nil:
		return nil, errors.New("playrun: a service needs every stage's implementation")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.Logf == nil {
		deps.Logf = func(string, ...any) {}
	}
	if deps.VerifyInstalled == nil {
		deps.VerifyInstalled = func(context.Context, Request, []StagedFile) error { return nil }
	}
	if deps.Unstage == nil {
		deps.Unstage = func(Request) error { return nil }
	}

	live := deps.Live
	if live == nil {
		live = NewLive()
	}

	return &Service{store: store, deps: deps, live: live}, nil
}

// Store is where records live.
func (s *Service) Store() *Store { return s.store }

// Start validates a request, writes the record and begins the sequence.
//
// It returns as soon as the run has an identity. Everything after that is read
// back from the record, which is what makes a reload recover the same run
// rather than an empty page.
func (s *Service) Start(request Request) (*Record, error) {
	return s.start(request, "")
}

// start is Start, with the attempt this one repeats. Set BEFORE the goroutine
// exists: a caller that wrote it afterwards would be writing to a record
// another goroutine already owns, and the two saves would race for the file.
func (s *Service) start(request Request, retryOf string) (*Record, error) {
	if err := request.Normalize(); err != nil {
		return nil, err
	}
	now := s.deps.Now()
	id, err := NewID(now)
	if err != nil {
		return nil, err
	}
	record := &Record{
		SchemaVersion: SchemaVersion,
		ID:            id,
		Request:       request,
		State:         Queued,
		CreatedAt:     now,
		RetryOf:       retryOf,
	}
	if err = s.store.Save(record); err != nil {
		return nil, err
	}

	// The copy the caller gets, taken BEFORE the goroutine exists. From the
	// moment it does, that goroutine owns the record, and a caller holding the
	// same pointer would be reading fields it is writing. Everything a caller
	// wants after this is in the store, which is the whole point of the record
	// being durable.
	snapshot := *record

	// Not the request's context: the sequence must survive the response.
	ctx, cancel := context.WithCancel(context.Background())
	s.live.add(id, cancel)

	go func() {
		defer cancel()
		defer s.live.remove(id)
		s.execute(ctx, record)
	}()

	return &snapshot, nil
}

// Get reads one run.
func (s *Service) Get(id string) (*Record, error) { return s.store.Load(id) }

// List reads every run, newest first.
func (s *Service) List() ([]*Record, error) { return s.store.List() }

// Cancel stops a run.
//
// The context cancellation reaches the active child job through the build
// runner and the job service — the same process-tree handling `companion job
// cancel` uses, including the Windows process group — and the sequence itself
// unstages anything it had installed. There is no state in which cancelling
// leaves half a mod behind.
func (s *Service) Cancel(id string) error {
	record, err := s.store.Load(id)
	if err != nil {
		return err
	}
	if record.State.Terminal() {
		return fmt.Errorf("playrun: run %s already %s", id, record.State)
	}
	if cancel, live := s.live.lookup(id); live {
		cancel()

		return nil
	}
	// Nothing in this process is running it. That is a run a previous process
	// left behind, and recording it as cancelled is the truthful answer: no
	// child of this program is going to finish it.
	record.State = Cancelled
	record.FinishedAt = s.deps.Now()
	s.closeStage(record, "the Companion that started this run is no longer running")

	return s.store.Save(record)
}

// Retry starts a NEW run from a finished one.
//
// A new record, linked to the previous one. Verified cached objects — the map
// revision, the texture bundle — are reused because they are content-addressed
// and were verified; what is not reused is the RECORD, because "it failed, then
// it worked" is two attempts and a record that overwrote itself could not say
// that.
func (s *Service) Retry(id string) (*Record, error) {
	previous, err := s.store.Load(id)
	if err != nil {
		return nil, err
	}
	if !previous.Retryable() {
		return nil, fmt.Errorf("playrun: run %s is %s, and only a failed or cancelled run is retried",
			id, previous.State)
	}
	return s.start(previous.Request, previous.ID)
}

// Recover marks a run that a stopped Companion left active.
//
// Called at start-up. A record saying `compiling` with nothing compiling is the
// one state a durable record can be wrong in, and leaving it there would make
// the Activity list claim forever that a build is running.
func (s *Service) Recover() (int, error) {
	records, err := s.store.List()
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, record := range records {
		if record.State.Terminal() {
			continue
		}
		record.State = Failed
		record.FailedAt = currentStage(record)
		record.Error = "the Companion stopped while this run was " + string(record.FailedAt)
		record.Remedy = "Start it again — the map and textures it had already downloaded are still verified in the cache."
		record.FinishedAt = s.deps.Now()
		s.closeStage(record, record.Error)
		if saveErr := s.store.Save(record); saveErr == nil {
			recovered++
		}
	}

	return recovered, nil
}

func currentStage(record *Record) State {
	if len(record.Stages) == 0 {
		return record.State
	}

	return record.Stages[len(record.Stages)-1].State
}
