// Package q3run starts an engine on an installed Quake III package and waits
// for the engine's own word about the map.
//
// # Why "it started" is not the answer
//
// Measured on ioquake3 1.36 (`Q3_011`): told to load a map it cannot find, the
// engine prints `Can't find map maps/<name>.bsp`, keeps running, and exits 0
// when it is eventually asked to quit. Told to load one with no game data, the
// client loads the world, prints `ERROR: DEFAULT_MODEL (sarge) failed to
// register`, and keeps running too. In both cases there is a live process, a
// pid and — later — a success status, and nobody is playing the map.
//
// So a run here is not finished when the process exists. The engine job is
// started through the one job service, exactly as any other launch is, and its
// output is read as it arrives against the engine profile's own diagnostic
// rules: a rule the document marks `signal: map_loaded` is the engine saying
// the map is loaded, and an error rule seen before it is the engine saying it
// is not. The result says which of the three happened — accepted, refused, or
// not observed — with the engine's line, the pid, and the command.
//
// "Not observed" is a real answer and never a polite word for success. A
// document that names no `map_loaded` line gets it immediately; an engine that
// says nothing within the wait gets it then, and is left running for the person
// to look at.
package q3run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3install"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// SchemaVersion versions [Result].
const SchemaVersion = "aucom.q3-run/1.0"

// DefaultWait is how long a run waits for the engine's word.
const DefaultWait = 60 * time.Second

// What a run observed about the map.
const (
	// Accepted: the engine printed its own "map loaded" line.
	Accepted = "accepted"
	// Refused: the engine said it could not, or stopped first.
	Refused = "refused"
	// NotObserved: neither was seen. Not a success.
	NotObserved = "not_observed"
)

// Jobs is what a run needs of the job service.
type Jobs interface {
	SubmitWatched(request job.Request, mirror io.Writer) (*job.Job, error)
	Preview(request job.Request) (*job.Job, error)
	Get(id string) (*job.Job, error)
	Cancel(id string) (*job.Job, error)
	Catalog() job.Catalog
}

// Request is one run.
type Request struct {
	Installation *q3install.Installation
	// EngineProfileID and ActionID name the engine action to run.
	EngineProfileID string
	ActionID        string
	// Options are the action's own options, as the person chose them.
	Options map[string]string
	Label   string
	// Wait bounds the wait for the engine's word. Zero means [DefaultWait].
	Wait time.Duration
	// Started, when set, is told the job id as soon as there is one — before
	// the wait — so a caller can show the job, or cancel it.
	Started func(jobID string)
}

// Result is what a run observed.
type Result struct {
	SchemaVersion  string `json:"schema_version"`
	InstallationID string `json:"installation_id"`
	PackageID      string `json:"package_id,omitempty"`
	ProfileID      string `json:"profile_id"`
	ActionID       string `json:"action_id"`
	JobID          string `json:"job_id"`
	// PID is the engine process, as the operating system knows it. Zero when
	// no process was started.
	PID int `json:"pid,omitempty"`
	// State is the engine job's state when the run stopped waiting.
	State job.State `json:"state"`
	// MapLoad is `accepted`, `refused` or `not_observed`.
	MapLoad string `json:"map_load"`
	// SignalDeclared is false when the engine profile names no `map_loaded`
	// line, so `not_observed` is the most this run could ever have said.
	SignalDeclared bool `json:"signal_declared"`
	// Evidence is the engine's own line: the one that said the map loaded, or
	// the one that said why it did not.
	Evidence string `json:"evidence,omitempty"`
	RuleID   string `json:"rule_id,omitempty"`
	// Message and Hint are the sentence a person reads and what to do about it.
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	// FailureClass says what kind of failure a refusal is.
	FailureClass string `json:"failure_class,omitempty"`
	// EngineStopped is true when this run stopped the engine because it had not
	// loaded the map: an engine idling on an error is not left running.
	EngineStopped bool `json:"engine_stopped,omitempty"`
	ExitCode      *int `json:"exit_code,omitempty"`

	MapName  string `json:"map_name"`
	FSGame   string `json:"fs_game"`
	BasePath string `json:"base_path"`
	// Executable, Args and WorkingDir are the command as the operating system
	// received it, resolved through the resolver the executor uses.
	Executable string   `json:"executable,omitempty"`
	Args       []string `json:"args,omitempty"`
	WorkingDir string   `json:"working_dir,omitempty"`
	WaitedMS   int64    `json:"waited_ms"`
	// Complete is the package's own: false when it lacks something an engine
	// needs. A map can load and still be drawn with missing textures.
	Complete   bool     `json:"complete"`
	NotCarried []string `json:"not_carried,omitempty"`
}

// OK reports a run whose engine said it loaded the map.
func (r *Result) OK() bool { return r != nil && r.MapLoad == Accepted }

// jobRequest is the engine job for an installation.
//
// The game root is the installation's base path — the managed directory, or
// the user's own game folder — and it overrides the binding's for this one job,
// which is what `job.Request.Roots` is for. The content root is given the same
// directory: these profiles declare it for what the user built, and what the
// user built is in the package, under the base path.
func jobRequest(request Request) job.Request {
	installation := request.Installation
	return job.Request{
		ProfileID: request.EngineProfileID,
		ActionID:  request.ActionID,
		Runtime: map[string]string{
			profile.RuntimeMapName: installation.MapName,
			profile.RuntimeModName: installation.FSGame,
		},
		Roots: map[string]string{
			profile.RootGame:    installation.BasePath,
			profile.RootContent: installation.BasePath,
		},
		Options: request.Options,
		Label:   request.Label,
	}
}

// BaseGameOption is the option an engine action declares to be told the name
// of a base directory that is not Quake III's own.
const BaseGameOption = "base_game"

// withBaseGame tells the engine which base directory the installation is in,
// when that is not Quake III's own and the action can be told.
//
// Measured on ioquake3 1.36 (`Q3_011`): an engine pointed at a game folder
// whose base directory is called `baseq3` insists on `pak0.pk3`, and one told
// `com_basegame <another name>` starts without id data. The installation
// already knows which directory it went into, so the run says so rather than
// leaving a person to pass the same name twice — and refuses when they pass a
// different one, because an engine reading one base directory while the map
// sits in another loads nothing.
func withBaseGame(request Request, action profile.Action) (Request, error) {
	installation := request.Installation
	declared := false
	for _, option := range action.Options {
		if option.Name == BaseGameOption {
			declared = true
		}
	}
	given := request.Options[BaseGameOption]
	standard := strings.EqualFold(installation.BaseGame, q3vfs.BaseGame)
	switch {
	case given != "" && !strings.EqualFold(given, installation.BaseGame):
		return request, fmt.Errorf("q3run: the option %s is %q, and the package is installed for the base "+
			"directory %q. An engine reading one while the map is in the other loads nothing",
			BaseGameOption, given, installation.BaseGame)
	case standard || given != "" || !declared:
		return request, nil
	}
	options := map[string]string{BaseGameOption: installation.BaseGame}
	for name, value := range request.Options {
		options[name] = value
	}
	request.Options = options
	return request, nil
}

// Preview resolves the command a run would start, and starts nothing.
func Preview(jobs Jobs, request Request) (*job.Job, error) {
	request, _, err := resolve(jobs, request)
	if err != nil {
		return nil, err
	}
	return jobs.Preview(jobRequest(request))
}

// resolve finds the action a run names and completes the request for it.
func resolve(jobs Jobs, request Request) (Request, profile.Action, error) {
	if request.Installation == nil {
		return request, profile.Action{}, errors.New("q3run: nothing is installed to run")
	}
	entry, err := jobs.Catalog().Lookup(request.EngineProfileID)
	if err != nil {
		return request, profile.Action{}, failure.As(failure.ToolUnavailable,
			fmt.Errorf("q3run: the engine profile %s: %w", request.EngineProfileID, err))
	}
	action, found := entry.Profile.ActionByID(request.ActionID)
	if !found {
		return request, action, fmt.Errorf("q3run: %s has no %q action", entry.Profile.Metadata().Name, request.ActionID)
	}
	if !runsAMap(action) {
		return request, action, fmt.Errorf("q3run: the %q action of %s loads no map; choose one that does",
			request.ActionID, entry.Profile.Metadata().Name)
	}
	request, err = withBaseGame(request, action)
	return request, action, err
}

// Launch starts the engine and waits for its word about the map.
//
// An error is returned only when no engine job could be started. Everything
// after that is in the result, including a refusal: the job exists, has a log,
// and is what a person opens to read why.
func Launch(ctx context.Context, jobs Jobs, request Request) (*Result, error) {
	request, action, err := resolve(jobs, request)
	if err != nil {
		return nil, err
	}
	installation := request.Installation
	// The archive, again, immediately before the engine is started on it.
	if err := q3install.Verify(installation); err != nil {
		return nil, err
	}
	wait := request.Wait
	if wait <= 0 {
		wait = DefaultWait
	}

	submission := jobRequest(request)
	watcher := newWatcher(action.Diagnostics, installation.MapName)
	submitted, err := jobs.SubmitWatched(submission, watcher)
	if err != nil {
		return nil, err
	}
	if request.Started != nil {
		request.Started(submitted.ID)
	}
	result := &Result{
		SchemaVersion:  SchemaVersion,
		InstallationID: installation.ID,
		PackageID:      installation.PackageID,
		ProfileID:      request.EngineProfileID,
		ActionID:       request.ActionID,
		JobID:          submitted.ID,
		State:          submitted.State,
		MapLoad:        NotObserved,
		SignalDeclared: watcher.declared,
		MapName:        installation.MapName,
		FSGame:         installation.FSGame,
		BasePath:       installation.BasePath,
		Complete:       installation.Complete,
		NotCarried:     installation.NotCarried,
	}
	if previewed, err := jobs.Preview(submission); err == nil && previewed != nil && previewed.Command != nil {
		result.Executable = previewed.Command.Executable
		result.Args = previewed.Command.Args
		result.WorkingDir = previewed.Command.WorkingDir
	}

	started := time.Now()
	deadline := started.Add(wait)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var current *job.Job
	for {
		if loaded, err := jobs.Get(submitted.ID); err == nil {
			current = loaded
			result.State = current.State
			if current.Process.PID != 0 {
				result.PID = current.Process.PID
			}
		}
		verdict := watcher.verdict()
		switch {
		case verdict.loaded:
			result.MapLoad = Accepted
			result.Evidence, result.RuleID = verdict.line, verdict.rule.ID
			result.Message = firstNonEmpty(verdict.rule.Message, "The engine loaded the map.")
			result.WaitedMS = time.Since(started).Milliseconds()
			return finish(result), nil
		case verdict.failed:
			result.MapLoad = Refused
			result.Evidence, result.RuleID = verdict.line, verdict.rule.ID
			result.Message = firstNonEmpty(verdict.rule.Message, "The engine reported an error before it loaded the map.")
			result.Hint = verdict.rule.Hint
			result.FailureClass = firstNonEmpty(verdict.rule.Class, failure.MapNotLoaded)
			result.WaitedMS = time.Since(started).Milliseconds()
			stop(jobs, result, current)
			return finish(result), nil
		case current != nil && current.State.Terminal():
			// It ended, and never said the map loaded.
			result.MapLoad = Refused
			result.ExitCode = current.ExitCode
			result.FailureClass = firstNonEmpty(current.FailureClass, failure.EngineStopped)
			result.Message = stoppedMessage(current)
			result.Evidence = watcher.last()
			result.WaitedMS = time.Since(started).Milliseconds()
			return finish(result), nil
		case !watcher.declared && result.PID != 0:
			// The document names no line to wait for. The process exists, and
			// that is all this run can say.
			result.Message = "The engine process started. This engine profile names no line that reports a map " +
				"has loaded, so whether it did was not observed: look at the engine, or at the job's log."
			result.WaitedMS = time.Since(started).Milliseconds()
			return finish(result), nil
		}
		select {
		case <-ctx.Done():
			result.Message = "The run was cancelled before the engine reported loading the map."
			result.FailureClass = failure.Cancelled
			result.MapLoad = Refused
			result.WaitedMS = time.Since(started).Milliseconds()
			stop(jobs, result, current)
			return finish(result), nil
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			result.Message = fmt.Sprintf("The engine is running and did not report loading the map within %s. "+
				"It was left running: look at it, or at the job's log.", wait.Round(time.Second))
			result.Evidence = watcher.last()
			result.WaitedMS = time.Since(started).Milliseconds()
			return finish(result), nil
		}
	}
}

// finish adds what is true of every result.
func finish(result *Result) *Result {
	if result.MapLoad == Accepted && !result.Complete {
		result.Hint = fmt.Sprintf("The map loaded, and this package does not carry %d file(s) the map needs "+
			"(%s): those surfaces, models or sounds are missing or default in the engine.",
			len(result.NotCarried), abbreviate(result.NotCarried, 3))
	}
	return result
}

// stop ends an engine that did not load the map, when it is still running.
func stop(jobs Jobs, result *Result, current *job.Job) {
	if current != nil && current.State.Terminal() {
		return
	}
	cancelled, err := jobs.Cancel(result.JobID)
	if err != nil || cancelled == nil {
		return
	}
	result.EngineStopped = true
	result.State = cancelled.State
	// Until the process tree is gone, briefly: "stopped" is said of a process
	// that has stopped, and a caller that exits on this result must not take a
	// half-stopped engine's supervisor away with it.
	result.State, result.ExitCode = Settle(jobs, result.JobID, cancelled)
}

// Settle waits, briefly, for a job that was told to stop to reach its final
// state, and returns that state and its exit status.
func Settle(jobs Jobs, id string, last *job.Job) (job.State, *int) {
	deadline := time.Now().Add(stopWait)
	for last != nil && !last.State.Terminal() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if loaded, err := jobs.Get(id); err == nil {
			last = loaded
		}
	}
	if last == nil {
		return "", nil
	}
	return last.State, last.ExitCode
}

// stopWait bounds the wait for a stopped engine to be gone.
const stopWait = 10 * time.Second

// stoppedMessage is the sentence for an engine that ended without loading.
func stoppedMessage(finished *job.Job) string {
	if finished.Error != "" {
		return "The engine stopped before it loaded the map: " + finished.Error
	}
	if finished.ExitCode != nil {
		return fmt.Sprintf("The engine exited with status %d before it reported loading the map.", *finished.ExitCode)
	}
	return "The engine stopped before it reported loading the map."
}

// runsAMap reports an action that is told a map to load.
func runsAMap(action profile.Action) bool {
	for _, arg := range action.Args {
		if strings.Contains(arg.Value, "{runtime."+profile.RuntimeMapName+"}") {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func abbreviate(values []string, limit int) string {
	if len(values) <= limit {
		return strings.Join(values, ", ")
	}
	return strings.Join(values[:limit], ", ") + fmt.Sprintf(" and %d more", len(values)-limit)
}

// --- the watcher -----------------------------------------------------------

// maxLineBytes bounds one line of engine output held while it is being read. A
// program that never prints a newline must not grow a buffer without limit.
const maxLineBytes = 64 << 10

// watcher reads an engine's output as it arrives and remembers the first line
// that settles the question.
type watcher struct {
	rules    []profile.DiagnosticRule
	mapName  string
	declared bool

	mu      sync.Mutex
	pending []byte
	result  verdict
	recent  string
}

type verdict struct {
	loaded bool
	failed bool
	line   string
	rule   profile.DiagnosticRule
}

func newWatcher(rules []profile.DiagnosticRule, mapName string) *watcher {
	w := &watcher{mapName: mapName}
	for _, rule := range rules {
		// The mirror carries both streams as one, so a rule's own stream is
		// not something this reader can hold it to.
		rule.Stream = ""
		w.rules = append(w.rules, rule)
		if rule.Signal == profile.SignalMapLoaded {
			w.declared = true
		}
	}
	return w
}

// Write is the job service's mirror: the engine's output, both streams, as it
// is produced.
func (w *watcher) Write(chunk []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, chunk...)
	for {
		end := -1
		for i, b := range w.pending {
			if b == '\n' {
				end = i
				break
			}
		}
		if end < 0 {
			if len(w.pending) > maxLineBytes {
				w.line(string(w.pending[:maxLineBytes]))
				w.pending = w.pending[:0]
			}
			return len(chunk), nil
		}
		w.line(strings.TrimRight(string(w.pending[:end]), "\r"))
		w.pending = w.pending[end+1:]
	}
}

// line applies the rules to one line. The first settling line wins and later
// ones are not consulted: an engine that loaded the map and then printed an
// error has loaded the map, and one that errored and then loaded something has
// not loaded what it was asked for first.
func (w *watcher) line(text string) {
	text = clean(text)
	if strings.TrimSpace(text) != "" {
		w.recent = text
	}
	if w.result.loaded || w.result.failed {
		return
	}
	for _, rule := range w.rules {
		if !rule.Matches("", text) {
			continue
		}
		switch {
		case rule.Signal == profile.SignalMapLoaded:
			// An id Tech 3 server names the map on this line, as
			// `\mapname\<name>\`. When the line says which map, it has to be
			// the one that was asked for: an engine that fell back to another
			// map has not loaded this one.
			if named, says := namedMap(text); says && !strings.EqualFold(named, w.mapName) {
				w.result = verdict{failed: true, line: text, rule: profile.DiagnosticRule{
					ID:      rule.ID,
					Message: fmt.Sprintf("The engine loaded %q, not %q.", named, w.mapName),
					Class:   failure.MapNotLoaded,
				}}
				return
			}
			w.result = verdict{loaded: true, line: text, rule: rule}
			return
		case rule.Severity == profile.SeverityError:
			w.result = verdict{failed: true, line: text, rule: rule}
			return
		}
	}
}

func (w *watcher) verdict() verdict {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *watcher) last() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.recent
}

// namedMap reads `\mapname\<name>\` out of a server info line.
func namedMap(line string) (string, bool) {
	const key = `\mapname\`
	at := strings.Index(line, key)
	if at < 0 {
		return "", false
	}
	rest := line[at+len(key):]
	if end := strings.IndexByte(rest, '\\'); end >= 0 {
		rest = rest[:end]
	}
	return rest, rest != ""
}

// clean makes a line of engine output safe to show: control characters out,
// length bounded. The raw bytes are in the job's log.
func clean(line string) string {
	var b strings.Builder
	for _, r := range line {
		if r == '\t' {
			b.WriteRune(' ')
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 600 {
			b.WriteString("…")
			break
		}
	}
	return b.String()
}
