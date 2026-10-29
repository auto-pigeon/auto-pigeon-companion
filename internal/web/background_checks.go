package web

import (
	"net/http"
	"sort"
	"sync"
	"time"
)

// Background checks the page shows in one place (NEW_368): the start-up check
// of the programs every bound profile runs, a person's "Check these
// programs", and the first check of the converter. They are this server's,
// so every tab reads the same rows from GET /api/v1/tools and none of them
// starts a check of its own; a check already running is not started twice.

// Activity kinds, as the page words them.
const (
	checkingPrograms = "checking_programs"
	checkConverter   = "checking_converter"
)

type checkRow struct {
	Kind string `json:"kind"`
	// Detail is what is being checked right now, when there is a name for it.
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since"`
}

type checkFailure struct {
	Kind  string    `json:"kind"`
	Error string    `json:"error"`
	At    time.Time `json:"at"`
	// Retry is whether POST /api/v1/checks/retry runs it again.
	Retry bool `json:"retry"`
}

type checkBoard struct {
	mu       sync.Mutex
	running  map[string]checkRow
	failures map[string]checkFailure
}

// begin records that the check named key started. False when it is already
// running: the caller starts nothing.
func (a *checkBoard) begin(key, kind, detail string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running == nil {
		a.running = map[string]checkRow{}
	}
	if _, busy := a.running[key]; busy {
		return false
	}
	a.running[key] = checkRow{Kind: kind, Detail: detail, Since: time.Now().UTC()}
	return true
}

func (a *checkBoard) detail(key, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if row, ok := a.running[key]; ok {
		row.Detail = detail
		a.running[key] = row
	}
}

// end records how the check finished: a failure replaces the last one of
// the same check, a success clears it.
func (a *checkBoard) end(key string, err error, retry bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	row := a.running[key]
	delete(a.running, key)
	if a.failures == nil {
		a.failures = map[string]checkFailure{}
	}
	if err == nil {
		delete(a.failures, key)
		return
	}
	a.failures[key] = checkFailure{Kind: row.Kind, Error: err.Error(), At: time.Now().UTC(), Retry: retry}
}

func (a *checkBoard) snapshot() ([]checkRow, []checkFailure) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var rows []checkRow
	for _, row := range a.running {
		rows = append(rows, row)
	}
	var failures []checkFailure
	for _, failure := range a.failures {
		failures = append(failures, failure)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Since.Before(rows[j].Since) })
	sort.Slice(failures, func(i, j int) bool { return failures[i].At.Before(failures[j].At) })
	return rows, failures
}

// converterState is what a lazily resolved converter can say about its first
// check without starting one.
type converterState interface {
	Resolving() bool
	ResolveErr() error
}

// checks is the board plus the converter's own state.
func (s *Server) checks() ([]checkRow, []checkFailure) {
	rows, failures := s.board.snapshot()
	if converter, ok := s.runner.(converterState); ok {
		if converter.Resolving() {
			rows = append(rows, checkRow{Kind: checkConverter})
		} else if err := converter.ResolveErr(); err != nil {
			failures = append(failures, checkFailure{Kind: checkConverter, Error: err.Error()})
		}
	}
	return rows, failures
}

// handleChecksRetry runs the program check again, in the background, and
// answers with the status the page polls. Nothing else is retried here: a
// converter that failed its check is a property of this installation.
func (s *Server) handleChecksRetry(w http.ResponseWriter, r *http.Request) {
	s.startRefresh()
	writeJSON(w, http.StatusOK, s.toolsStatus())
}
