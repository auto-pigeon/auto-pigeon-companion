package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/noconsole"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/tooluse"
)

// The Companion's side of its own update (NEW_368): the admission fence an
// "Update now" closes before the process stops to be replaced, the proving
// start of a staged bundle, and what is running that an update waits for.

// codeUpdating is the code of a request refused while the Companion stops to
// be replaced: retryable, and nothing of it was started or consumed.
const codeUpdating = "companion_updating"

// errUpdating is work refused at the fence.
var errUpdating = errors.New("Auto-Pigeon Companion is restarting to finish an update; nothing was started — try again in a moment")

// admission counts the requests that may start work, and closes to new ones
// for an update. A request is admitted for its whole handler, so once the
// fence has waited for the admitted ones, everything they started is in
// ActiveWork and nothing more can be.
type admission struct {
	mu       sync.Mutex
	closed   bool
	inflight int
	idle     *sync.Cond
}

func (a *admission) enter() (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, false
	}
	a.inflight++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			a.inflight--
			if a.idle != nil {
				a.idle.Broadcast()
			}
			a.mu.Unlock()
		})
	}, true
}

// close refuses new work and waits, at most wait, for the admitted requests
// to return. It reports whether they all did; when they did not, admission
// is open again.
func (a *admission) close(wait time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.idle == nil {
		a.idle = sync.NewCond(&a.mu)
	}
	a.closed = true
	deadline := time.Now().Add(wait)
	for a.inflight > 0 {
		if time.Now().After(deadline) {
			a.closed = false
			return false
		}
		// Woken by every exit; a timer wakes the wait at the deadline.
		timer := time.AfterFunc(time.Until(deadline), func() {
			a.mu.Lock()
			a.idle.Broadcast()
			a.mu.Unlock()
		})
		a.idle.Wait()
		timer.Stop()
	}
	return true
}

func (a *admission) reopen() {
	a.mu.Lock()
	a.closed = false
	a.mu.Unlock()
}

func (a *admission) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

// startsNoWork are the mutating routes a page still needs while the
// Companion stops for an update: its lease, Quit, and the update's own
// cancel. Everything else that is not a GET is held at the fence.
var startsNoWork = []string{"/api/lifecycle/", "/api/v1/tools/"}

// admit wraps a route: a mutating request is admitted, or refused with a
// retryable "updating" while the fence is closed.
func (s *Server) admit(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			handler(w, r)
			return
		}
		for _, prefix := range startsNoWork {
			if strings.HasPrefix(r.URL.Path, prefix) {
				handler(w, r)
				return
			}
		}
		release, ok := s.admission.enter()
		if !ok {
			w.Header().Set("Retry-After", "15")
			writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: errUpdating.Error(), Code: codeUpdating})
			return
		}
		defer release()
		handler(w, r)
	}
}

// selfHost is [managedtools.SelfHost] for this server.
type selfHost struct{ s *Server }

// SelfUpdateHost is this server's host for the Companion's own update.
func (s *Server) SelfUpdateHost() managedtools.SelfHost { return selfHost{s: s} }

// proveTimeout bounds a proving start.
const proveTimeout = 60 * time.Second

// ProveResult is what `companion selfupdate prove` prints.
type ProveResult struct {
	Version           string `json:"version"`
	Commit            string `json:"commit,omitempty"`
	ConverterVersion  string `json:"converter_version"`
	ConverterVerified bool   `json:"converter_verified"`
	// A migration bridge alone also answers what the updater it is replacing asks: that the extractor
	// its manifest lists is there, with those bytes. Nothing here has run it.
	ExtractorVersion  string `json:"extractor_version,omitempty"`
	ExtractorVerified bool   `json:"extractor_verified,omitempty"`
	ConfigRead        bool   `json:"config_read"`
	Served            bool   `json:"served"`
	Error             string `json:"error,omitempty"`
}

// Prove runs a Companion's proving start: the program itself, with a fixed
// argument list, bounded, against this configuration, changing nothing.
func Prove(ctx context.Context, executable, version string) (ProveResult, error) {
	ctx, cancel := context.WithTimeout(ctx, proveTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "selfupdate", "prove", "--expect-version", version)
	// The candidate's OWN converter, beside it: a developer override in this
	// environment must not stand in for the one its bundle manifest lists.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, converter.EnvBinaryOverride+"=") {
			command.Env = append(command.Env, entry)
		}
	}
	// Background work: no console window on Windows (NEW_368).
	noconsole.Apply(command)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	var result ProveResult
	decodeErr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result)
	switch {
	case ctx.Err() != nil:
		return result, fmt.Errorf("its proving start did not finish within %s", proveTimeout)
	case decodeErr != nil:
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		return result, fmt.Errorf("its proving start printed no result (%v): %s", runErr, tail)
	case result.Error != "":
		return result, errors.New(result.Error)
	case runErr != nil:
		return result, runErr
	case result.Version != version:
		return result, fmt.Errorf("it says it is %s, not %s", result.Version, version)
	case !result.ConverterVerified || !result.ConfigRead || !result.Served:
		return result, fmt.Errorf("its proving start was incomplete: %+v", result)
	}
	return result, nil
}

func (h selfHost) Prove(ctx context.Context, record managedtools.InstallRecord) error {
	program := record.Programs["companion"]
	if program == "" {
		return errors.New("the staged bundle has no Companion")
	}
	result, err := Prove(ctx, program, record.Version)
	if err == nil {
		h.s.logf("self update: staged %s proved: converter %s verified, configuration read, served on loopback", result.Version, result.ConverterVersion)
	}
	return err
}

func (h selfHost) SelfBusy() []string {
	var out []string
	for _, work := range h.s.ActiveWork() {
		out = append(out, work.Label)
	}
	return out
}

// RestartForUpdate is the activation boundary of "Update now": close
// admission, wait for the requests already admitted, look again at what is
// running — the fence is what makes this second look the last one — and
// stop the process for the update. Work that started meanwhile reopens
// admission and is what the update waits for.
func (h selfHost) RestartForUpdate(record managedtools.InstallRecord) ([]string, error) {
	s := h.s
	if !s.admission.close(30 * time.Second) {
		return []string{"a request that was starting work"}, nil
	}
	if busy := h.SelfBusy(); len(busy) > 0 || s.toolGate.running() > 0 {
		s.admission.reopen()
		if len(busy) == 0 {
			busy = []string{"a build"}
		}
		return busy, nil
	}
	if s.lifecycle == nil {
		s.admission.reopen()
		return nil, errors.New("this Companion has no lifecycle to stop")
	}
	// Other Companion processes sharing the tools folder — a `companion build
	// run` in a terminal — run from this bundle too: the bundle's switch gate
	// keeps new ones out and their running builds are waited for, like this
	// process's own (01_NEW_368 §2).
	if registry, err := s.toolRegistry(); err == nil {
		gate, err := registry.Switch(BundleTool, "the Companion replacing itself with "+record.Version)
		if err != nil {
			s.admission.reopen()
			return []string{err.Error()}, nil
		}
		var elsewhere []string
		for _, use := range gate.Users() {
			if !use.Here {
				elsewhere = append(elsewhere, use.Label)
			}
		}
		if len(elsewhere) > 0 {
			gate.Release()
			s.admission.reopen()
			return elsewhere, nil
		}
		s.selfGate.Store(gate)
	}
	s.selfRestart.Store(&record)
	s.logf("self update: admission closed, nothing running; stopping to put %s in place", record.Version)
	s.lifecycle.Stop(ExitUpdate)
	return nil, nil
}

// SelfRestartRecord is the staged bundle an "Update now" stopped this
// process for, once it has.
func (s *Server) SelfRestartRecord() (managedtools.InstallRecord, bool) {
	record := s.selfRestart.Load()
	if record == nil {
		return managedtools.InstallRecord{}, false
	}
	return *record, true
}

// SelfGate hands over the bundle's switch gate "Update now" took, once.
func (s *Server) SelfGate() *tooluse.Gate { return s.selfGate.Swap(nil) }

// ToolRegistry is the tools folder's cross-process coordination.
func (s *Server) ToolRegistry() (*tooluse.Registry, error) { return s.toolRegistry() }

// SelfAtExit is the staged bundle to put in place as this process ends.
func (s *Server) SelfAtExit(restart bool) (managedtools.InstallRecord, bool) {
	if s.tools == nil {
		return managedtools.InstallRecord{}, false
	}
	return s.tools.SelfAtExit(restart)
}

// RecordSelfResult passes a replacement's outcome to the coordinator.
func (s *Server) RecordSelfResult(result managedtools.SelfResult) {
	if s.tools == nil {
		return
	}
	if err := s.tools.RecordSelfResult(result); err != nil {
		s.logf("self update: recording the result: %v", err)
	}
}
