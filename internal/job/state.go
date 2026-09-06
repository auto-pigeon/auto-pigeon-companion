package job

import (
	"fmt"
	"sort"
	"strings"
)

// The lifecycle, as a table rather than as scattered assignments.
//
// Eight states and one transition function. Every change of state in this
// package goes through [Transition], so "can a cancelled job start running" is
// answered in one place by a table a test can read, rather than by whichever
// assignment happened to run last. A supervisor whose states drift is a
// supervisor that reports a finished job as running, and there is no way to
// tell from the outside which of the two is wrong.

// State is where a job is in its life.
type State string

const (
	// Queued: accepted and persisted, nothing resolved and nothing started.
	Queued State = "queued"
	// Resolving: the workspace is being built, inputs staged, and the profile
	// action turned into an exact command. No process exists yet.
	Resolving State = "resolving"
	// Running: a process exists.
	Running State = "running"
	// Cancelling: a stop was asked for and the process tree is being taken
	// down. Distinct from Cancelled because a kill is not instantaneous and a
	// user who pressed stop is entitled to see that it was heard.
	Cancelling State = "cancelling"

	// Succeeded: the process exited with one of the action's success codes and
	// every required output was collected.
	Succeeded State = "succeeded"
	// Failed: anything that went wrong on the job's own account — a nonzero
	// exit, a missing executable, a timeout, a missing required output.
	Failed State = "failed"
	// Cancelled: stopped because somebody asked.
	Cancelled State = "cancelled"
	// Interrupted: the Companion stopped while this job was unfinished, so
	// nothing here knows how it ended. Never retried automatically — see
	// [Store.Recover].
	Interrupted State = "interrupted"
)

// States is every state, in lifecycle order. Exported so a test asserts against
// the package's own list rather than a copy that can fall behind.
var States = []State{Queued, Resolving, Running, Cancelling, Succeeded, Failed, Cancelled, Interrupted}

// transitions is the whole state machine. A state's entry is every state it may
// move to; a state with an empty entry is terminal.
var transitions = map[State][]State{
	Queued:     {Resolving, Cancelling, Cancelled, Failed, Interrupted},
	Resolving:  {Running, Cancelling, Failed, Interrupted},
	Running:    {Cancelling, Succeeded, Failed, Interrupted},
	Cancelling: {Cancelled, Failed, Succeeded, Interrupted},

	Succeeded:   {},
	Failed:      {},
	Cancelled:   {},
	Interrupted: {},
}

// Valid reports whether s is a state this build knows.
func (s State) Valid() bool {
	_, known := transitions[s]
	return known
}

// Terminal reports whether a job in this state will never change again.
func (s State) Terminal() bool {
	next, known := transitions[s]
	return known && len(next) == 0
}

// Active reports whether a job in this state is the executor's responsibility
// right now. It is the question [Store.Recover] asks of every stored job at
// startup.
func (s State) Active() bool { return s.Valid() && !s.Terminal() }

// Describe is the one-line explanation a user is shown.
func (s State) Describe() string {
	switch s {
	case Queued:
		return "waiting for a free slot; nothing has been resolved or started"
	case Resolving:
		return "building the workspace and working out the exact command"
	case Running:
		return "the program is running"
	case Cancelling:
		return "stopping the program and everything it started"
	case Succeeded:
		return "finished, and every required output was produced"
	case Failed:
		return "did not finish successfully"
	case Cancelled:
		return "stopped because you asked"
	case Interrupted:
		return "the Companion stopped while this was unfinished, so how it ended is not known"
	}
	return "unknown state"
}

// TransitionError reports a refused state change, naming what was allowed.
//
// It says what the job may do next rather than only that it may not do this,
// because the caller reading it is usually a request handler that has to
// explain the refusal to somebody.
type TransitionError struct {
	JobID   string
	From    State
	To      State
	Allowed []State
}

func (e *TransitionError) Error() string {
	if !e.From.Valid() {
		return fmt.Sprintf("job %s: %q is not a state this build knows", e.JobID, e.From)
	}
	if !e.To.Valid() {
		return fmt.Sprintf("job %s: %q is not a state this build knows", e.JobID, e.To)
	}
	if len(e.Allowed) == 0 {
		return fmt.Sprintf("job %s has already %s and cannot move to %s", e.JobID, e.From, e.To)
	}
	names := make([]string, 0, len(e.Allowed))
	for _, s := range e.Allowed {
		names = append(names, string(s))
	}
	sort.Strings(names)
	return fmt.Sprintf("job %s is %s and cannot move to %s (it can move to: %s)",
		e.JobID, e.From, e.To, strings.Join(names, ", "))
}

// Transition reports whether a job may move from one state to another.
//
// A move to the state a job is already in is refused rather than ignored. The
// two ways that happens are a double-cancel and a race between two goroutines
// finishing the same job, and both are worth failing loudly instead of
// absorbing.
func Transition(id string, from, to State) error {
	allowed, known := transitions[from]
	if !known || !to.Valid() {
		return &TransitionError{JobID: id, From: from, To: to}
	}
	for _, candidate := range allowed {
		if candidate == to {
			return nil
		}
	}
	return &TransitionError{JobID: id, From: from, To: to, Allowed: allowed}
}
