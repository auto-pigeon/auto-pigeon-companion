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
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginewatch"
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
	// SuppliedByPackages is how many of NotCarried a bound package of the
	// installation has, so the engine finds them on this machine.
	SuppliedByPackages int `json:"supplied_by_packages,omitempty"`
	// PackagesNotStaged names the packages the saved map is bound to that this
	// installation does not give the engine: a game-folder install writes one
	// file. A managed install has none — it refuses instead.
	PackagesNotStaged []string `json:"packages_not_staged,omitempty"`
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
	// The archive and every bound package staged with it, again, immediately
	// before the engine is started on them.
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
		SignalDeclared: watcher.Declared(),
		MapName:        installation.MapName,
		FSGame:         installation.FSGame,
		BasePath:       installation.BasePath,
		Complete:       installation.Complete,
		NotCarried:     installation.NotCarried,

		SuppliedByPackages: installation.SuppliedByPackages,
		PackagesNotStaged:  installation.NotStaged(),
	}
	if previewed, err := jobs.Preview(submission); err == nil && previewed != nil && previewed.Command != nil {
		result.Executable = previewed.Command.Executable
		result.Args = previewed.Command.Args
		result.WorkingDir = previewed.Command.WorkingDir
	}

	outcome := enginewatch.Await(ctx, jobs, submitted.ID, watcher, wait)
	if outcome.Job != nil {
		result.State = outcome.Job.State
	}
	result.PID = outcome.PID
	result.WaitedMS = outcome.Waited.Milliseconds()
	verdict := outcome.Verdict
	switch outcome.Ending {
	case enginewatch.EndedLoaded:
		result.MapLoad = Accepted
		result.Evidence, result.RuleID = verdict.Line, verdict.Rule.ID
		result.Message = firstNonEmpty(verdict.Rule.Message, "The engine loaded the map.")
	case enginewatch.EndedFailed:
		result.MapLoad = Refused
		result.Evidence, result.RuleID = verdict.Line, verdict.Rule.ID
		result.Message = firstNonEmpty(verdict.Rule.Message, "The engine reported an error before it loaded the map.")
		result.Hint = verdict.Rule.Hint
		result.FailureClass = firstNonEmpty(verdict.Rule.Class, failure.MapNotLoaded)
		stop(jobs, result, outcome.Job)
	case enginewatch.EndedStopped:
		// It ended, and never said the map loaded.
		result.MapLoad = Refused
		result.ExitCode = outcome.Job.ExitCode
		result.FailureClass = firstNonEmpty(outcome.Job.FailureClass, failure.EngineStopped)
		result.Message = enginewatch.StoppedMessage(outcome.Job)
		result.Evidence = watcher.Last()
	case enginewatch.EndedUndeclared:
		// The document names no line to wait for. The process exists, and
		// that is all this run can say.
		result.Message = "The engine process started. This engine profile names no line that reports a map " +
			"has loaded, so whether it did was not observed: look at the engine, or at the job's log."
	case enginewatch.EndedCancelled:
		result.Message = "The run was cancelled before the engine reported loading the map."
		result.FailureClass = failure.Cancelled
		result.MapLoad = Refused
		stop(jobs, result, outcome.Job)
	case enginewatch.EndedTimedOut:
		result.Message = fmt.Sprintf("The engine is running and did not report loading the map within %s. "+
			"It was left running: look at it, or at the job's log.", wait.Round(time.Second))
		result.Evidence = watcher.Last()
	}
	return finish(result), nil
}

// finish adds what is true of every result.
func finish(result *Result) *Result {
	if supplied := result.SuppliedByPackages; result.MapLoad == Accepted && !result.Complete && supplied > 0 {
		// Not carried is not the same as not there: the bound packages this
		// installation gives the engine have some of them, and saying all of
		// them are missing would send somebody looking for files that are drawn.
		rest := len(result.NotCarried) - supplied
		result.Hint = fmt.Sprintf("The map loaded. This package does not carry %d file(s) the map needs; %d of them "+
			"are in the bound package(s) this installation gives the engine, on this machine", len(result.NotCarried), supplied)
		if rest > 0 {
			result.Hint += fmt.Sprintf(", and the other %d are missing or default in the engine.", rest)
		} else {
			result.Hint += ". Somebody you give the package to does not have them."
		}
		return result
	}
	if result.MapLoad == Accepted && !result.Complete {
		result.Hint = fmt.Sprintf("The map loaded, and this package does not carry %d file(s) the map needs "+
			"(%s): those surfaces, models or sounds are missing or default in the engine.",
			len(result.NotCarried), abbreviate(result.NotCarried, 3))
	}
	return result
}

// stop ends an engine that did not load the map, when it is still running.
// Until the process tree is gone, briefly: "stopped" is said of a process that
// has stopped, and a caller that exits on this result must not take a
// half-stopped engine's supervisor away with it.
func stop(jobs Jobs, result *Result, current *job.Job) {
	stopped, state, code := enginewatch.Stop(jobs, result.JobID, current)
	if !stopped {
		return
	}
	result.EngineStopped = true
	result.State, result.ExitCode = state, code
}

// Settle waits, briefly, for a job that was told to stop to reach its final
// state, and returns that state and its exit status.
func Settle(jobs Jobs, id string, last *job.Job) (job.State, *int) {
	return enginewatch.Settle(jobs, id, last)
}

// runsAMap reports an action that is told a map to load.
func runsAMap(action profile.Action) bool { return enginewatch.RunsAMap(action) }

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

// newWatcher is `internal/enginewatch`'s watcher with Quake III's own check: an
// id Tech 3 server names the map on its `map_loaded` line, as
// `\mapname\<name>\`, and when the line says which map it has to be the one
// that was asked for — an engine that fell back to another map has not loaded
// this one.
func newWatcher(rules []profile.DiagnosticRule, mapName string) *enginewatch.Watcher {
	return enginewatch.New(rules, enginewatch.Options{
		Confirm: func(line string, rule profile.DiagnosticRule) *enginewatch.Verdict {
			if named, says := namedMap(line); says && !strings.EqualFold(named, mapName) {
				return &enginewatch.Verdict{Failed: true, Line: line, Rule: profile.DiagnosticRule{
					ID:      rule.ID,
					Message: fmt.Sprintf("The engine loaded %q, not %q.", named, mapName),
					Class:   failure.MapNotLoaded,
				}}
			}
			return nil
		},
	})
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
