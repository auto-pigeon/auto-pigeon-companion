package job

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/compat"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Asking a program what it is, and refusing to start one that is not what its
// profile was written for (NEW_323D). The decision is [compat.Judge]; this
// file is the two things only the executor can do about it.
//
// # The probe is a job
//
// ADR-0003: one executor supervises every process this program starts, "a
// compile, a game, a version probe". So a probe is a profile action —
// [profile.ToolProfile.ActionByID] makes one per probed executable — admitted
// by the same code as any job, authorized by the same grant, run with the same
// argument array, the same empty stdin, the same bounded capture, the same
// time bound and the same process-group kill, and left behind as the same
// record. It is not queued: an answer is needed where it is asked, and a probe
// waiting behind a six-hour vis would make a refusal out of a full queue.
//
// # The gate is here
//
// Every way a compile starts — a pipeline stage, `companion job run`, the HTTP
// API, a retry — is [Service.Submit], and a queued job is checked again when a
// worker takes it. So an incompatible program is refused in one place, before
// any process is started and before a successful output can exist.
//
// # Evidence is about one file
//
// An answer is believed for the exact bytes it was taken from: the SHA-256 is
// read here, every time, before anything is started on the strength of an
// earlier answer. Replacing vis at the same path makes the recorded answer
// about a different program, and it is asked again. A page compares only size
// and modification time (it must not read megabytes on every poll), which is
// why the page's answer is never the one that authorizes a run.

func factKey(path, sha256, requirement string) string {
	return path + "\x00" + sha256 + "\x00" + requirement
}

// CheckCompatibility establishes what every program a profile holds to its
// probe is, records the answers, and returns them by executable name.
//
// It is what setting a tool up, approving it and starting the Companion do,
// and — with again set — what pressing "Check these programs" does. Without
// again, a program whose exact bytes already have an answer for the exact
// requirement is not started a second time: rebinding vis asks vis, not qbsp
// and light beside it. With again, every one is asked now, whatever is on
// record.
//
// It is refused for a profile nothing has approved, exactly as running it
// would be: a probe starts a program with arguments a document chose.
//
// A probe result approves nothing and withdraws nothing.
func (s *Service) CheckCompatibility(profileID string, again bool) (map[string]binding.ExecutableFact, error) {
	entry, err := s.catalog.Lookup(profileID)
	if err != nil {
		return nil, err
	}
	names := compat.ProbedExecutables(entry.Profile)
	if len(names) == 0 {
		return map[string]binding.ExecutableFact{}, nil
	}
	if err := s.authorize(entry, Request{ProfileID: profileID}); err != nil {
		return nil, err
	}
	tool := entry.Profile.(*profile.ToolProfile)
	facts := make(map[string]binding.ExecutableFact, len(names))
	keep := make(map[string]binding.ExecutableFact, len(names))
	for _, name := range names {
		action, _ := tool.ActionByID(profile.CompatibilityProbeActionPrefix + name)
		request := Request{ProfileID: profileID, ActionID: action.ID}
		invocation, err := s.resolve("probe", request, entry, action, false)
		if err != nil {
			continue // not bound: the checker already names a program with no path
		}
		path := invocation.Command.Executable
		fact, known := binding.ExecutableFact{}, false
		if !again {
			if identity, err := compat.Digest(path); err == nil {
				fact, known = s.knownFact(profileID, name, path, identity.SHA256, tool.CompatibilityProbe.Requirement(name))
				// The cheap half of the identity is what a page compares, so it
				// is refreshed even when the bytes did not change (a `touch`).
				fact.Size, fact.ModifiedAt = identity.Size, identity.ModifiedAt
			}
		}
		if !known || fact.State == binding.FactTimedOut || fact.State == binding.FactFailed {
			fact = s.probe(entry, name, path, request)
		}
		facts[name] = fact
		if fact.SHA256 != "" {
			keep[name] = fact
		}
	}
	if s.recordFacts != nil {
		if err := s.recordFacts(profileID, keep); err != nil {
			return facts, fmt.Errorf("job: recording what %s's programs said: %w", profileID, err)
		}
	}
	return facts, nil
}

// ProbeAt asks the programs at paths what they are, exactly as profileID's
// version check asks its bound programs — the same supervised job, the same
// bounds, the same approval rule — and records nothing on any binding. It is
// how a managed installation is verified before anything is bound to it
// (NEW_367): "installed" is not "ready" until its programs have answered.
// Names the profile does not probe are ignored.
func (s *Service) ProbeAt(profileID string, paths map[string]string) (map[string]binding.ExecutableFact, error) {
	entry, err := s.catalog.Lookup(profileID)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(entry, Request{ProfileID: profileID}); err != nil {
		return nil, err
	}
	facts := map[string]binding.ExecutableFact{}
	for _, name := range compat.ProbedExecutables(entry.Profile) {
		path, ok := paths[name]
		if !ok {
			continue
		}
		facts[name] = s.probe(entry, name, path, Request{ProfileID: profileID})
	}
	return facts, nil
}

// Compatible answers, for one action of one profile as this machine has it
// bound, what [Service.Submit] would answer about its program — without a
// request, an input or a job. It is how a build asks about EVERY mandatory
// stage before it starts the first: a pipeline whose qbsp is right and whose
// vis is not is refused whole, rather than after a compile that could only
// ever be thrown away.
//
// A program with no path recorded, or not on disk, is not this function's to
// refuse: nil, and the executor says so in its own words.
func (s *Service) Compatible(profileID, actionID string) error {
	request := Request{ProfileID: profileID, ActionID: actionID}
	entry, action, err := s.resolveAction(request)
	if err != nil {
		return err
	}
	if _, _, required := compat.Requirement(entry.Profile, action); !required {
		return nil
	}
	if err := s.authorize(entry, request); err != nil {
		return err
	}
	path := s.executables(request)[action.Executable]
	if path == "" {
		return nil
	}
	return s.ensureCompatible(entry, action, path, request)
}

// ensureCompatible refuses an action whose program is not what its profile
// requires. Nil when the profile requires nothing of it.
func (s *Service) ensureCompatible(entry CatalogEntry, action profile.Action, path string, request Request) error {
	_, requirement, required := compat.Requirement(entry.Profile, action)
	if !required {
		return nil
	}
	identity, err := compat.Digest(path)
	if err != nil {
		// Not there, or not readable. [checkExecutable] refuses that with its
		// own sentence and its own class; saying "incompatible" about a file
		// nobody could read would be a second, worse answer.
		return nil
	}
	fact, known := s.knownFact(request.ProfileID, action.Executable, path, identity.SHA256, requirement)
	// An answer that was about the asking rather than the program — it timed
	// out, it could not be started — is asked again rather than remembered.
	if !known || fact.State == binding.FactTimedOut || fact.State == binding.FactFailed {
		fact = s.probe(entry, action.Executable, path, request)
		if _, overridden := request.Executables[action.Executable]; !overridden && s.recordFacts != nil && fact.SHA256 != "" {
			if err := s.recordFacts(request.ProfileID, map[string]binding.ExecutableFact{action.Executable: fact}); err != nil {
				s.logf("job: recording what %s said: %v", path, err)
			}
		}
	}
	finding := compat.Judge(entry.Profile, action.Executable, path, &fact)
	if finding == nil {
		return nil
	}
	class := failure.ToolUnverified
	switch finding.Kind {
	case compat.Incompatible:
		class = failure.ToolIncompatible
	case compat.Unusable:
		class = failure.ToolUnavailable
	}
	return failure.As(class, finding)
}

// knownFact is the recorded answer for exactly these bytes and exactly this
// requirement, from the binding or from this process.
func (s *Service) knownFact(profileID, executable, path, sha256, requirement string) (binding.ExecutableFact, bool) {
	if local, found := s.binding(profileID); found {
		if fact, recorded := local.Compatibility[executable]; recorded &&
			fact.Path == path && fact.SHA256 == sha256 && fact.Requirement == requirement {
			return fact, true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fact, recorded := s.facts[factKey(path, sha256, requirement)]
	return fact, recorded
}

// probe asks one program what it is, as a supervised job, and classifies the
// answer. It never returns an error: every way the asking can go wrong is a
// state, because "could not ask" is itself what the caller needs to report.
func (s *Service) probe(entry CatalogEntry, executable, path string, request Request) binding.ExecutableFact {
	tool := entry.Profile.(*profile.ToolProfile)
	fact := binding.ExecutableFact{
		Path:        path,
		Requirement: tool.CompatibilityProbe.Requirement(executable),
		CheckedAt:   s.now(),
	}
	identity, err := compat.Digest(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fact.State, fact.Detail = binding.FactMissing, "there is no such file"
		return fact
	case errors.Is(err, fs.ErrPermission):
		fact.State, fact.Detail = binding.FactNotPermitted, "the file may not be read"
		return fact
	case err != nil:
		fact.State, fact.Detail = binding.FactFailed, err.Error()
		return fact
	}
	fact.Size, fact.ModifiedAt, fact.SHA256 = identity.Size, identity.ModifiedAt, identity.SHA256
	defer func() {
		s.mu.Lock()
		s.facts[factKey(path, fact.SHA256, fact.Requirement)] = fact
		s.mu.Unlock()
	}()
	if info, err := os.Stat(path); err == nil && runtime.GOOS != "windows" && info.Mode().IsRegular() && info.Mode().Perm()&0o111 == 0 {
		fact.State, fact.Detail = binding.FactNotPermitted, "the file is not marked executable"
		return fact
	}

	probeRequest := Request{
		ProfileID:   request.ProfileID,
		ActionID:    profile.CompatibilityProbeActionPrefix + executable,
		Executables: map[string]string{executable: path},
		Roots:       request.Roots,
		Label:       "Check what " + executable + " is",
	}
	j, err := s.admit(probeRequest)
	if err != nil {
		fact.State, fact.Detail = binding.FactFailed, err.Error()
		return fact
	}
	fact.JobID = j.ID
	// Run here, by the executor's own code, rather than queued. See the note
	// at the top of this file.
	s.execute(j.ID)
	finished, err := s.store.Load(j.ID)
	if err != nil || !finished.State.Terminal() {
		fact.State, fact.Detail = binding.FactFailed, "the check left no result"
		return fact
	}
	switch {
	case finished.TimedOut:
		fact.State = binding.FactTimedOut
		fact.Detail = fmt.Sprintf("it was stopped after %d seconds", tool.CompatibilityProbe.Timeout())
		return fact
	case finished.State == Cancelled || finished.State == Interrupted:
		fact.State, fact.Detail = binding.FactFailed, "the check was stopped before it finished"
		return fact
	case finished.ExitCode == nil:
		// It never ran to an exit status: the operating system would not start
		// it. Which refusal it was is the operating system's own word.
		message := strings.ToLower(finished.Error)
		switch {
		case strings.Contains(message, "exec format error"), strings.Contains(message, "not a valid win32 application"),
			strings.Contains(message, "bad cpu type"):
			fact.State, fact.Detail = binding.FactWrongArchitecture, "the operating system would not load it"
		case strings.Contains(message, "permission denied"), strings.Contains(message, "access is denied"),
			strings.Contains(message, "not executable"):
			fact.State, fact.Detail = binding.FactNotPermitted, "the operating system would not start it"
		default:
			fact.State, fact.Detail = binding.FactFailed, "it could not be started"
		}
		return fact
	}
	// It ran and exited. The status is not the answer — ericw-tools 0.18.1
	// and the Auto-Pigeon build both exit 1 after printing their usage, and
	// upstream 2.0.0-alpha11 exits 0 — so what it printed is read either way.
	stdout, _ := s.Logs(j.ID, "stdout", true)
	stderr, _ := s.Logs(j.ID, "stderr", true)
	state, named, missing, detail := compat.Classify(tool.CompatibilityProbe, executable, string(stdout), string(stderr))
	fact.State, fact.Identity, fact.Missing, fact.Detail = state, named, missing, detail
	return fact
}
