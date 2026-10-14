// Package q2run starts an engine on an installed Quake II package and waits
// for the engine's own word that it loaded THAT map, from THAT package.
//
// # Why the file the engine opened matters here
//
// A Quake II engine searches several folders for a map, and the one it finds
// first wins. Measured with Yamagi Quake II 8.70 (`Q2_009`): a loose
// `maps/<name>.bsp` in the engine's own write folder for the mod
// (`~/.local/share/YamagiQ2/<mod>/`) is loaded in place of the same name in an
// installed package — and the engine says nothing about it unless asked. Told
// `fs_debug 1`, it names every file it opens and where it found it:
//
//	FS_FOpenFile: 'maps/<name>.bsp' (found in '<folder>/<mod>/<name>.pak').
//
// So a run reads that line: the map has to have come out of the archive this
// installation put in place, whose bytes were re-hashed immediately before the
// engine started. Anything else is the engine playing somebody else's file,
// and is a refusal (`load_order_shadowed`), not a success.
//
// The same measurement found the other reason the line is wanted: Yamagi
// writes its console with stdio, which is block-buffered on a pipe, so a quiet
// engine's `Map: <name>` can sit unread for as long as the session lasts.
// `fs_debug` keeps the stream moving while the map loads.
package q2run

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginewatch"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2install"
)

// SchemaVersion versions [Result].
const SchemaVersion = "aucom.q2-run/1.0"

// DefaultWait is how long a run waits for the engine's word.
const DefaultWait = 60 * time.Second

// What a run observed about the map.
const (
	Accepted    = "accepted"
	Refused     = "refused"
	NotObserved = "not_observed"
)

// Jobs is what a run needs of the job service.
type Jobs = enginewatch.Jobs

// Request is one run.
type Request struct {
	Installation    *q2install.Installation
	EngineProfileID string
	ActionID        string
	Options         map[string]string
	Label           string
	Wait            time.Duration
	// Started is told the job id as soon as there is one.
	Started func(jobID string)
}

// Result is what a run observed.
type Result struct {
	SchemaVersion  string    `json:"schema_version"`
	InstallationID string    `json:"installation_id"`
	PackageID      string    `json:"package_id,omitempty"`
	ProfileID      string    `json:"profile_id"`
	ActionID       string    `json:"action_id"`
	JobID          string    `json:"job_id"`
	PID            int       `json:"pid,omitempty"`
	State          job.State `json:"state"`
	MapLoad        string    `json:"map_load"`
	SignalDeclared bool      `json:"signal_declared"`
	Evidence       string    `json:"evidence,omitempty"`
	RuleID         string    `json:"rule_id,omitempty"`
	Message        string    `json:"message"`
	Hint           string    `json:"hint,omitempty"`
	FailureClass   string    `json:"failure_class,omitempty"`
	EngineStopped  bool      `json:"engine_stopped,omitempty"`
	ExitCode       *int      `json:"exit_code,omitempty"`

	MapName  string `json:"map_name"`
	FSGame   string `json:"fs_game"`
	BasePath string `json:"base_path"`
	// Expected is the installed archive; LoadedFrom is where the engine said it
	// found the map; SourceVerified is true only when the two are one file.
	Expected       string `json:"expected"`
	LoadedFrom     string `json:"loaded_from,omitempty"`
	SourceVerified bool   `json:"source_verified"`
	// ArchiveSHA256 and MapSHA256 are the bytes re-hashed before the start.
	ArchiveSHA256 string `json:"archive_sha256"`
	MapSHA256     string `json:"map_sha256"`

	Executable string   `json:"executable,omitempty"`
	Args       []string `json:"args,omitempty"`
	WorkingDir string   `json:"working_dir,omitempty"`
	WaitedMS   int64    `json:"waited_ms"`
	Complete   bool     `json:"complete"`
	NotCarried []string `json:"not_carried,omitempty"`
}

// OK reports a run whose engine said it loaded the installed map.
func (r *Result) OK() bool { return r != nil && r.MapLoad == Accepted }

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
			profile.RootContent: installation.ModDir,
		},
		Options: request.Options,
		Label:   request.Label,
	}
}

// Preview resolves the command a run would start, and starts nothing.
func Preview(jobs Jobs, request Request) (*job.Job, error) {
	if _, err := resolve(jobs, request); err != nil {
		return nil, err
	}
	return jobs.Preview(jobRequest(request))
}

func resolve(jobs Jobs, request Request) (profile.Action, error) {
	if request.Installation == nil {
		return profile.Action{}, errors.New("q2run: nothing is installed to run")
	}
	entry, err := jobs.Catalog().Lookup(request.EngineProfileID)
	if err != nil {
		return profile.Action{}, failure.As(failure.ToolUnavailable, fmt.Errorf("q2run: the engine profile %s: %w", request.EngineProfileID, err))
	}
	action, found := entry.Profile.ActionByID(request.ActionID)
	if !found {
		return action, fmt.Errorf("q2run: %s has no %q action", entry.Profile.Metadata().Name, request.ActionID)
	}
	if !enginewatch.RunsAMap(action) {
		return action, fmt.Errorf("q2run: the %q action of %s loads no map; choose one that does",
			request.ActionID, entry.Profile.Metadata().Name)
	}
	return action, nil
}

// opened is Yamagi's `fs_debug` line for a file it found.
var opened = regexp.MustCompile(`FS_FOpenFile: '([^']+)' \(found in '([^']+)'\)`)

// origin remembers where the engine said it found the map.
type origin struct {
	mu   sync.Mutex
	path string
}

func (o *origin) get() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.path
}

// newWatcher is the shared watcher with Quake II's two checks: the map the
// engine opened must come out of the installed archive, and the map the client
// says it is loading must be the one asked for.
func newWatcher(rules []profile.DiagnosticRule, mapName, expected string, seen *origin) *enginewatch.Watcher {
	member := "maps/" + mapName + ".bsp"
	return enginewatch.New(rules, enginewatch.Options{
		Inspect: func(line string) *enginewatch.Verdict {
			for _, match := range opened.FindAllStringSubmatch(line, -1) {
				if !strings.EqualFold(match[1], member) {
					continue
				}
				seen.mu.Lock()
				first := seen.path == ""
				if first {
					seen.path = match[2]
				}
				seen.mu.Unlock()
				if first && !samePath(match[2], expected) {
					return &enginewatch.Verdict{Failed: true, Line: line, Rule: profile.DiagnosticRule{
						ID: "shadowed",
						Message: fmt.Sprintf("The engine opened %s from %s, not from the installed package %s.",
							member, match[2], expected),
						Hint: "Something the engine searches before this mod folder has a map of this name. Remove it, " +
							"or package the map under another name.",
						Class: failure.LoadOrderShadowed,
					}}
				}
			}
			return nil
		},
		Confirm: func(line string, rule profile.DiagnosticRule) *enginewatch.Verdict {
			after := line
			if at := strings.Index(line, "Map: "); at >= 0 {
				after = line[at+len("Map: "):]
			}
			if !strings.HasPrefix(strings.ToLower(after), strings.ToLower(mapName)) {
				return &enginewatch.Verdict{Failed: true, Line: line, Rule: profile.DiagnosticRule{
					ID: rule.ID, Message: fmt.Sprintf("The engine is loading another map, not %q.", mapName),
					Class: failure.MapNotLoaded,
				}}
			}
			return nil
		},
	})
}

// samePath compares the folder the engine printed with the installed archive,
// through links: Yamagi prints its data folder's real path.
func samePath(printed, expected string) bool {
	clean := func(path string) string {
		path = filepath.Clean(filepath.FromSlash(path))
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return real
		}
		return path
	}
	return clean(printed) == clean(expected)
}

// Launch starts the engine and waits for its word about the map. An error is
// returned only when no engine job could be started; everything after that,
// a refusal included, is in the result.
func Launch(ctx context.Context, jobs Jobs, request Request) (*Result, error) {
	action, err := resolve(jobs, request)
	if err != nil {
		return nil, err
	}
	installation := request.Installation
	// The archive and its map, again, immediately before the engine starts.
	if err := q2install.Verify(installation); err != nil {
		return nil, err
	}
	wait := request.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	submission := jobRequest(request)
	seen := &origin{}
	watcher := newWatcher(action.Diagnostics, installation.MapName, installation.Archive.Path, seen)
	submitted, err := jobs.SubmitWatched(submission, watcher)
	if err != nil {
		return nil, err
	}
	if request.Started != nil {
		request.Started(submitted.ID)
	}
	result := &Result{
		SchemaVersion: SchemaVersion, InstallationID: installation.ID, PackageID: installation.PackageID,
		ProfileID: request.EngineProfileID, ActionID: request.ActionID, JobID: submitted.ID, State: submitted.State,
		MapLoad: NotObserved, SignalDeclared: watcher.Declared(),
		MapName: installation.MapName, FSGame: installation.FSGame, BasePath: installation.BasePath,
		Expected: installation.Archive.Path, ArchiveSHA256: installation.Archive.SHA256,
		MapSHA256: installation.MapMember.SHA256, Complete: installation.Complete, NotCarried: installation.NotCarried,
	}
	if previewed, err := jobs.Preview(submission); err == nil && previewed != nil && previewed.Command != nil {
		result.Executable, result.Args, result.WorkingDir = previewed.Command.Executable, previewed.Command.Args, previewed.Command.WorkingDir
	}

	outcome := enginewatch.Await(ctx, jobs, submitted.ID, watcher, wait)
	if outcome.Job != nil {
		result.State = outcome.Job.State
	}
	result.PID = outcome.PID
	result.WaitedMS = outcome.Waited.Milliseconds()
	result.LoadedFrom = seen.get()
	verdict := outcome.Verdict
	stop := func() {
		stopped, state, code := enginewatch.Stop(jobs, result.JobID, outcome.Job)
		if stopped {
			result.EngineStopped, result.State, result.ExitCode = true, state, code
		}
	}
	switch outcome.Ending {
	case enginewatch.EndedLoaded:
		result.Evidence, result.RuleID = verdict.Line, verdict.Rule.ID
		if result.LoadedFrom == "" {
			// The client said it is loading the map, and nothing said from
			// where. That is not the installed package proven, so it is not
			// accepted; the engine is left up for a person to look at.
			result.Message = "The engine says it is loading the map, and did not say which file it opened, so " +
				"that it is the installed package was not observed. This engine profile does not ask the engine to name its files."
			break
		}
		result.MapLoad, result.SourceVerified = Accepted, true
		result.Message = firstNonEmpty(verdict.Rule.Message, "The engine loaded the map.") +
			" It opened it from the installed package."
	case enginewatch.EndedFailed:
		result.MapLoad = Refused
		result.Evidence, result.RuleID = verdict.Line, verdict.Rule.ID
		result.Message = firstNonEmpty(verdict.Rule.Message, "The engine reported an error before it loaded the map.")
		result.Hint = verdict.Rule.Hint
		result.FailureClass = firstNonEmpty(verdict.Rule.Class, failure.MapNotLoaded)
		stop()
	case enginewatch.EndedStopped:
		result.MapLoad = Refused
		result.ExitCode = outcome.Job.ExitCode
		result.FailureClass = firstNonEmpty(outcome.Job.FailureClass, failure.EngineStopped)
		result.Message = enginewatch.StoppedMessage(outcome.Job)
		result.Evidence = watcher.Last()
	case enginewatch.EndedUndeclared:
		result.Message = "The engine process started. This engine profile names no line that reports a map " +
			"has loaded, so whether it did was not observed: look at the engine, or at the job's log."
	case enginewatch.EndedCancelled:
		result.MapLoad = Refused
		result.FailureClass = failure.Cancelled
		result.Message = "The run was cancelled before the engine reported loading the map."
		stop()
	case enginewatch.EndedTimedOut:
		result.Message = fmt.Sprintf("The engine is running and did not report loading the map within %s. "+
			"It was left running: look at it, or at the job's log.", wait.Round(time.Second))
		result.Evidence = watcher.Last()
	}
	if result.MapLoad == Accepted && !result.Complete {
		result.Hint = fmt.Sprintf("The map loaded, and this package does not carry %d file(s) the map needs: those "+
			"surfaces, models or sounds are missing or default in the engine.", len(result.NotCarried))
	}
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
