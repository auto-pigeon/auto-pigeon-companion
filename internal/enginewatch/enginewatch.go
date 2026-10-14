// Package enginewatch reads an engine's output as it arrives and waits for the
// engine's own word about the map it was told to load.
//
// It is the one implementation of "a live process is not a loaded map" that the
// Quake III and Quake II runs share (`internal/q3run`, `internal/q2run`). The
// rules are the engine profile's: a rule marked `signal: map_loaded` is the
// engine saying the map loaded, an error rule seen first is the engine saying
// it did not, and silence is "not observed" — never success. What each family
// adds is a check of its own on the lines it can read more out of: a Quake III
// server names the map it started, and a Quake II engine asked to can name the
// file it opened.
package enginewatch

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// maxLineBytes bounds one line of engine output held while it is being read.
const maxLineBytes = 64 << 10

// Verdict is the line that settled the question, and how.
type Verdict struct {
	Loaded bool
	Failed bool
	Line   string
	Rule   profile.DiagnosticRule
}

// Settled reports a verdict either way.
func (v Verdict) Settled() bool { return v.Loaded || v.Failed }

// Options are what a family adds to the profile's rules.
type Options struct {
	// Inspect sees every line before the rules do. A verdict it returns
	// settles the run.
	Inspect func(line string) *Verdict
	// Confirm is asked when a `map_loaded` rule matched. A failed verdict it
	// returns overrules the match: the engine loaded something, and not what it
	// was asked to.
	Confirm func(line string, rule profile.DiagnosticRule) *Verdict
}

// Watcher is a job's output mirror that remembers the first settling line.
type Watcher struct {
	rules    []profile.DiagnosticRule
	declared bool
	options  Options

	mu      sync.Mutex
	pending []byte
	result  Verdict
	recent  string
}

// New watches for the rules of an engine action.
func New(rules []profile.DiagnosticRule, options Options) *Watcher {
	w := &Watcher{options: options}
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

// Declared reports whether the action names a line that means "loaded".
func (w *Watcher) Declared() bool { return w.declared }

// Write is the job service's mirror: both streams, as they are produced.
func (w *Watcher) Write(chunk []byte) (int, error) {
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

// line applies the rules to one line. The first settling line wins: an engine
// that loaded the map and then printed an error has loaded the map, and one
// that errored and then loaded something has not loaded what it was asked for
// first.
func (w *Watcher) line(text string) {
	text = Clean(text)
	if strings.TrimSpace(text) != "" {
		w.recent = text
	}
	if w.result.Settled() {
		return
	}
	if w.options.Inspect != nil {
		if verdict := w.options.Inspect(text); verdict != nil && verdict.Settled() {
			w.result = *verdict
			return
		}
	}
	for _, rule := range w.rules {
		if !rule.Matches("", text) {
			continue
		}
		switch {
		case rule.Signal == profile.SignalMapLoaded:
			if w.options.Confirm != nil {
				if verdict := w.options.Confirm(text, rule); verdict != nil && verdict.Failed {
					w.result = *verdict
					return
				}
			}
			w.result = Verdict{Loaded: true, Line: text, Rule: rule}
			return
		case rule.Severity == profile.SeverityError:
			w.result = Verdict{Failed: true, Line: text, Rule: rule}
			return
		}
	}
}

// Verdict is what has been settled so far.
func (w *Watcher) Verdict() Verdict {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

// Last is the newest non-empty line.
func (w *Watcher) Last() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.recent
}

// Clean makes a line of engine output safe to show: control characters out,
// length bounded. The raw bytes are in the job's log.
func Clean(line string) string {
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

// Jobs is what waiting needs of the job service.
type Jobs interface {
	SubmitWatched(request job.Request, mirror io.Writer) (*job.Job, error)
	Preview(request job.Request) (*job.Job, error)
	Get(id string) (*job.Job, error)
	Cancel(id string) (*job.Job, error)
	Catalog() job.Catalog
}

// Ending says why a wait ended.
type Ending string

const (
	// EndedLoaded: a `map_loaded` line was seen.
	EndedLoaded Ending = "loaded"
	// EndedFailed: an error line, or a family check, said it did not load.
	EndedFailed Ending = "failed"
	// EndedStopped: the engine ended without saying it loaded.
	EndedStopped Ending = "stopped"
	// EndedUndeclared: the profile names no line to wait for; the process exists.
	EndedUndeclared Ending = "undeclared"
	// EndedCancelled: the caller stopped waiting.
	EndedCancelled Ending = "cancelled"
	// EndedTimedOut: the wait ran out with the engine still up.
	EndedTimedOut Ending = "timed_out"
)

// Outcome is how a wait ended.
type Outcome struct {
	Ending  Ending
	Verdict Verdict
	// Job is the engine job as last read; PID the process once there was one.
	Job    *job.Job
	PID    int
	Waited time.Duration
}

// Await waits for the watcher to settle, the engine to end, the wait to run
// out or the context to be cancelled.
func Await(ctx context.Context, jobs Jobs, jobID string, watcher *Watcher, wait time.Duration) Outcome {
	started := time.Now()
	deadline := started.Add(wait)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	outcome := Outcome{}
	for {
		if loaded, err := jobs.Get(jobID); err == nil {
			outcome.Job = loaded
			if loaded.Process.PID != 0 {
				outcome.PID = loaded.Process.PID
			}
		}
		verdict := watcher.Verdict()
		outcome.Verdict = verdict
		outcome.Waited = time.Since(started)
		switch {
		case verdict.Loaded:
			outcome.Ending = EndedLoaded
			return outcome
		case verdict.Failed:
			outcome.Ending = EndedFailed
			return outcome
		case outcome.Job != nil && outcome.Job.State.Terminal():
			outcome.Ending = EndedStopped
			return outcome
		case !watcher.Declared() && outcome.PID != 0:
			outcome.Ending = EndedUndeclared
			return outcome
		}
		select {
		case <-ctx.Done():
			outcome.Ending = EndedCancelled
			outcome.Waited = time.Since(started)
			return outcome
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			outcome.Ending = EndedTimedOut
			outcome.Waited = time.Since(started)
			return outcome
		}
	}
}

// Stop ends an engine that did not load the map, when it is still running, and
// waits briefly for it to be gone. It reports whether it stopped one, and the
// job's final state and exit status.
func Stop(jobs Jobs, jobID string, current *job.Job) (bool, job.State, *int) {
	if current != nil && current.State.Terminal() {
		return false, current.State, current.ExitCode
	}
	cancelled, err := jobs.Cancel(jobID)
	if err != nil || cancelled == nil {
		if current != nil {
			return false, current.State, current.ExitCode
		}
		return false, "", nil
	}
	state, code := Settle(jobs, jobID, cancelled)
	return true, state, code
}

// stopWait bounds the wait for a stopped engine to be gone.
const stopWait = 10 * time.Second

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

// StoppedMessage is the sentence for an engine that ended without loading.
func StoppedMessage(finished *job.Job) string {
	if finished.Error != "" {
		return "The engine stopped before it loaded the map: " + finished.Error
	}
	if finished.ExitCode != nil {
		return fmt.Sprintf("The engine exited with status %d before it reported loading the map.", *finished.ExitCode)
	}
	return "The engine stopped before it reported loading the map."
}

// RunsAMap reports an action that is told a map to load.
func RunsAMap(action profile.Action) bool {
	for _, arg := range action.Args {
		if strings.Contains(arg.Value, "{runtime."+profile.RuntimeMapName+"}") {
			return true
		}
	}
	return false
}
