//go:build !windows

package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_323D: a program is asked what it is before it is started as a compiler,
// and one that is not what its profile was written for is never started as
// one. Every case below asserts that INDEPENDENTLY of what the service says:
// each fixture program appends its own argument list to a file, so "vis was
// never invoked with a map" is read from what vis itself recorded.
//
// The fixtures are shell scripts rather than the test binary because a probe
// passes NO arguments — that is the invocation ericw-tools answers with its
// banner — and the test binary selects its behaviour by argument.

// What the three real programs print when started with nothing (measured
// 2026-10-12; compat_test.go in internal/compat keeps the same lines).
const (
	bannerLegacy  = "---- %s / ericw-tools v0.18.1 ----\nusage: %s [-threads #] [-level 0-4] [-fast] bspfile"
	bannerModern  = "---- %s / ericw-tools 2.0.0-alpha11 ----\n  -logfile \"path\"               File to output logging data to."
	bannerFork    = "---- %s / ericw-tools 2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505 ----\n  -logfile \"path\"               File to output logging data to."
	bannerUnknown = "%s: hello%.0s"
)

type compatRig struct {
	*harness
	t     *testing.T
	tools string
	log   string
	id    string
	mu    sync.Mutex
	saved map[string]binding.ExecutableFact
}

// fixtureProgram writes a program that records how it was started and then
// behaves like one of the real ones: a banner and `exit` when given nothing,
// "compiled" when given a map.
func (r *compatRig) fixtureProgram(name, banner string, probeExit int, extra string) string {
	r.t.Helper()
	path := filepath.Join(r.tools, name)
	script := fmt.Sprintf("#!/bin/sh\necho \"%s|$*\" >> '%s'\n%s\nif [ $# -eq 0 ]; then\ncat <<'BANNER'\n%s\nBANNER\nexit %d\nfi\necho compiled\n",
		name, r.log, extra, fmt.Sprintf(banner, name, name), probeExit)
	// Written beside and renamed over, as an installer replaces a program: a
	// shell still reading the old file keeps the old file.
	if err := os.WriteFile(path+".new", []byte(script), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		r.t.Fatal(err)
	}
	return path
}

// invocations is what the fixture programs recorded: "<program>|<its args>".
func (r *compatRig) invocations() []string {
	r.t.Helper()
	data, err := os.ReadFile(r.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (r *compatRig) probes(program string) int {
	count := 0
	for _, line := range r.invocations() {
		if line == program+"|" {
			count++
		}
	}
	return count
}

// compiles is how often a program was started with a map — the thing that
// must not happen to an incompatible one.
func (r *compatRig) compiles(program string) int {
	count := 0
	for _, line := range r.invocations() {
		if strings.HasPrefix(line, program+"|") && line != program+"|" {
			count++
		}
	}
	return count
}

// newCompatRig installs one tool profile with qbsp, vis, light and bspinfo,
// approved, bound to fixture programs. accept and timeout are the document's
// own requirement; withProbe false is a document that requires nothing.
func newCompatRig(t *testing.T, withProbe bool, accept map[string]any, timeout int) *compatRig {
	t.Helper()
	const id = "local.tool.compat"
	action := func(id, executable string) map[string]any {
		return map[string]any{"id": id, "title": id, "executable": executable, "args": []any{"-logfile", "x.log", "level.bsp"}}
	}
	document := map[string]any{
		"schema_version": "aucom.profile/1.1",
		"kind":           "tool",
		"id":             id,
		"version":        "1.0.0",
		"name":           "Compatibility test tools",
		"summary":        "Four fixture programs, three of them held to a version.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"tool_version":   "2.0.0-alpha11",
		"platforms": []map[string]any{
			{"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "status": "supported"},
		},
		"acquisition": []map[string]any{
			{"mode": "user_path", "title": "Point at a copy you already have", "hint": "choose the folder"},
		},
		"executables": []map[string]any{
			{"name": "qbsp", "file": "qbsp"}, {"name": "vis", "file": "vis"},
			{"name": "light", "file": "light"}, {"name": "bspinfo", "file": "bspinfo"},
		},
		"actions": []map[string]any{
			action("compile", "qbsp"), action("vis", "vis"), action("light", "light"), action("inspect", "bspinfo"),
		},
	}
	if withProbe {
		document["compatibility_probe"] = map[string]any{
			"parse":           map[string]any{"kind": "after_marker", "marker": "ericw-tools ", "stream": "stdout"},
			"timeout_seconds": timeout,
			"accept":          []any{accept},
			"executables": []any{
				map[string]any{"name": "qbsp"},
				map[string]any{"name": "vis", "mentions": []string{"-logfile"}},
				map[string]any{"name": "light", "mentions": []string{"-logfile"}},
			},
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	rig := &compatRig{t: t, id: id, saved: map[string]binding.ExecutableFact{}}
	rig.harness = newHarness(t, encoded, func(options *Options) {
		options.RecordFacts = func(profileID string, facts map[string]binding.ExecutableFact) error {
			rig.mu.Lock()
			defer rig.mu.Unlock()
			local := rig.grant.bindings[profileID]
			if local.Compatibility == nil {
				local.Compatibility = map[string]binding.ExecutableFact{}
			}
			for name, fact := range facts {
				local.Compatibility[name] = fact
				rig.saved[name] = fact
			}
			rig.grant.bindings[profileID] = local
			return nil
		}
	})
	rig.tools = filepath.Join(rig.dir, "tools")
	rig.log = filepath.Join(rig.dir, "invocations.log")
	if err := os.MkdirAll(rig.tools, 0o700); err != nil {
		t.Fatal(err)
	}
	return rig
}

// bind records where the programs are, as the bind handler does.
func (r *compatRig) bind(paths map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	local := r.grant.bindings[r.id]
	local.Executables = paths
	r.grant.bindings[r.id] = local
}

func (r *compatRig) all(banner string, exit int) {
	paths := map[string]string{}
	for _, name := range []string{"qbsp", "vis", "light", "bspinfo"} {
		paths[name] = r.fixtureProgram(name, banner, exit, "")
	}
	r.bind(paths)
}

func (r *compatRig) request(action string) Request {
	return Request{ProfileID: r.id, ActionID: action}
}

// refused submits an action that must not start, and returns the refusal.
func (r *compatRig) refused(action, class string) error {
	r.t.Helper()
	submitted, err := r.service.Submit(r.request(action))
	if err == nil {
		finished := r.waitFor(submitted.ID)
		r.t.Fatalf("%s was admitted and ended %s (%s); it should have been refused as %s", action, finished.State, finished.Error, class)
	}
	if got := failure.Of(err); got != class {
		r.t.Fatalf("%s was refused as %q (%v); want %q", action, got, err, class)
	}
	return err
}

func (r *compatRig) succeeds(action string) *Job {
	r.t.Helper()
	finished := r.runToEnd(r.request(action))
	if finished.State != Succeeded {
		r.t.Fatalf("%s ended %s: %s", action, finished.State, finished.Error)
	}
	return finished
}

var acceptModern = map[string]any{"prefix": "2."}

func TestAModernProfileBoundToLegacyProgramsStartsNoCompiler(t *testing.T) {
	rig := newCompatRig(t, true, acceptModern, 10)
	rig.all(bannerLegacy, 1)
	before, _ := rig.store.List()

	for _, action := range []string{"compile", "vis", "light"} {
		err := rig.refused(action, failure.ToolIncompatible)
		if !strings.Contains(err.Error(), "v0.18.1") || !strings.Contains(err.Error(), "Nothing else was chosen for you") {
			t.Errorf("%s: the refusal does not say what the program is, or that nothing was substituted: %v", action, err)
		}
	}
	for _, program := range []string{"qbsp", "vis", "light"} {
		if n := rig.compiles(program); n != 0 {
			t.Errorf("%s was started with a map %d time(s): %v", program, n, rig.invocations())
		}
		// Asked once; the second question about the same bytes is answered
		// from what was recorded.
		if n := rig.probes(program); n != 1 {
			t.Errorf("%s was asked what it is %d time(s), want 1", program, n)
		}
	}
	if err := rig.refused("vis", failure.ToolIncompatible); err == nil || rig.probes("vis") != 1 {
		t.Errorf("a second refusal asked again: %d probes", rig.probes("vis"))
	}
	// The only records are the probes: no build that was never going to work.
	after, _ := rig.store.List()
	for _, j := range after[len(before):] {
		if _, probe := profile.CompatibilityProbeExecutable(j.ActionID); !probe {
			t.Errorf("a refused action left the job %s (%s)", j.ID, j.ActionID)
		}
		if j.State != Succeeded {
			t.Errorf("the probe %s ended %s: a program that answers has answered, whatever its status", j.ActionID, j.State)
		}
	}
	// An inspection program nothing is required of still runs.
	rig.succeeds("inspect")
	if rig.probes("bspinfo") != 0 {
		t.Error("bspinfo was probed, and the document requires nothing of it")
	}
	// And the answers were kept, as evidence about those exact files.
	fact := rig.saved["vis"]
	if fact.State != binding.FactIncompatible || fact.Identity != "v0.18.1" || fact.SHA256 == "" || fact.JobID == "" {
		t.Errorf("the recorded answer for vis = %+v", fact)
	}
}

func TestSupportedProgramsAreAskedOnceAndThenRun(t *testing.T) {
	for name, c := range map[string]struct {
		banner string
		exit   int
		accept map[string]any
	}{
		// Upstream 2.0.0-alpha11 exits 0 after its usage; the Auto-Pigeon
		// build exits 1. Neither status is the answer.
		"upstream alpha11":    {bannerModern, 0, acceptModern},
		"the verified fork":   {bannerFork, 1, map[string]any{"prefix": "2.", "contains": "+auto-pigeon."}},
		"fork under upstream": {bannerFork, 1, acceptModern},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newCompatRig(t, true, c.accept, 10)
			rig.all(c.banner, c.exit)
			for _, action := range []string{"compile", "vis", "light", "vis"} {
				finished := rig.succeeds(action)
				if !strings.Contains(rig.mustLog(finished.ID, "stdout"), "compiled") {
					t.Errorf("%s did not run the program", action)
				}
			}
			if rig.probes("vis") != 1 || rig.compiles("vis") != 2 || rig.probes("qbsp") != 1 || rig.compiles("qbsp") != 1 {
				t.Errorf("invocations: %v", rig.invocations())
			}
			if want := "vis|-logfile x.log level.bsp"; !strings.Contains(strings.Join(rig.invocations(), "\n"), want) {
				t.Errorf("vis was not started with its declared arguments (%s): %v", want, rig.invocations())
			}
		})
	}
}

func TestTheForksOwnProfileRefusesUpstreamPrograms(t *testing.T) {
	rig := newCompatRig(t, true, map[string]any{"prefix": "2.", "contains": "+auto-pigeon."}, 10)
	rig.all(bannerModern, 0)
	rig.refused("compile", failure.ToolIncompatible)
	if rig.compiles("qbsp") != 0 {
		t.Errorf("qbsp ran: %v", rig.invocations())
	}
}

// A working qbsp cannot make an old vis ready, and an old vis does not take a
// working qbsp away.
func TestAModernQbspBesideLegacyVisAndLight(t *testing.T) {
	rig := newCompatRig(t, true, acceptModern, 10)
	rig.bind(map[string]string{
		"qbsp":    rig.fixtureProgram("qbsp", bannerModern, 0, ""),
		"vis":     rig.fixtureProgram("vis", bannerLegacy, 1, ""),
		"light":   rig.fixtureProgram("light", bannerLegacy, 1, ""),
		"bspinfo": rig.fixtureProgram("bspinfo", bannerLegacy, 1, ""),
	})
	rig.succeeds("compile")
	rig.refused("vis", failure.ToolIncompatible)
	rig.refused("light", failure.ToolIncompatible)
	if rig.compiles("qbsp") != 1 || rig.compiles("vis") != 0 || rig.compiles("light") != 0 {
		t.Errorf("invocations: %v", rig.invocations())
	}
}

// Evidence is about bytes, not about a path.
func TestReplacingAProgramWhereItStandsIsNoticedBeforeItRuns(t *testing.T) {
	rig := newCompatRig(t, true, acceptModern, 10)
	rig.all(bannerModern, 0)
	rig.succeeds("vis")

	rig.fixtureProgram("vis", bannerLegacy, 1, "") // same path, another program
	rig.refused("vis", failure.ToolIncompatible)
	if rig.probes("vis") != 2 || rig.compiles("vis") != 1 {
		t.Fatalf("after the replacement: %v", rig.invocations())
	}
	if rig.saved["vis"].Identity != "v0.18.1" {
		t.Errorf("the recorded answer still describes the old program: %+v", rig.saved["vis"])
	}

	// And back: the same bytes at the same path as the first time, which this
	// process already asked — an answer about bytes is still about them.
	rig.fixtureProgram("vis", bannerModern, 0, "")
	rig.succeeds("vis")
	if rig.probes("vis") != 2 || rig.compiles("vis") != 2 {
		t.Errorf("after restoring it: %v", rig.invocations())
	}
	// Same bytes at another path are another binding: asked, not assumed.
	moved := filepath.Join(rig.tools, "elsewhere-vis")
	data, _ := os.ReadFile(filepath.Join(rig.tools, "vis"))
	if err := os.WriteFile(moved, data, 0o755); err != nil {
		t.Fatal(err)
	}
	request := rig.request("vis")
	request.Executables = map[string]string{"vis": moved}
	if finished := rig.runToEnd(request); finished.State != Succeeded {
		t.Fatalf("the moved copy ended %s: %s", finished.State, finished.Error)
	}
	if rig.probes("vis") != 3 {
		t.Errorf("a program at a path nothing recorded was not asked: %v", rig.invocations())
	}
	if rig.saved["vis"].Path == moved {
		t.Error("an answer about a path one job overrode was recorded as the binding's")
	}
}

// What the document requires is part of what an answer is about.
func TestAnAnswerToAnotherRequirementIsNotBelieved(t *testing.T) {
	rig := newCompatRig(t, true, acceptModern, 10)
	rig.all(bannerLegacy, 1)
	// A forged or outdated record that calls the legacy vis compatible, for
	// some other requirement.
	rig.refused("vis", failure.ToolIncompatible)
	rig.mu.Lock()
	local := rig.grant.bindings[rig.id]
	fact := local.Compatibility["vis"]
	fact.State, fact.Requirement = binding.FactCompatible, "sha256:"+strings.Repeat("0", 64)
	local.Compatibility["vis"] = fact
	rig.grant.bindings[rig.id] = local
	rig.mu.Unlock()
	// A fresh service has no memory of its own, only the record.
	fresh := newCompatRigSharing(t, rig)
	if _, err := fresh.Submit(rig.request("vis")); failure.Of(err) != failure.ToolIncompatible {
		t.Fatalf("a record for another requirement was believed: %v", err)
	}
	if rig.compiles("vis") != 0 {
		t.Errorf("vis ran: %v", rig.invocations())
	}
}

// newCompatRigSharing is a second service over the same store, catalog and
// bindings: another process, or this one after a restart.
func newCompatRigSharing(t *testing.T, rig *compatRig) *Service {
	t.Helper()
	service, err := NewService(Options{Store: rig.store, Catalog: NewCatalog(rig.profiles), Bindings: rig.grant.lookup})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func TestEveryWayAskingCanGoWrongIsItsOwnAnswer(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(r *compatRig) string
		state   binding.FactState
		class   string
	}{
		{"prints something that names nothing", func(r *compatRig) string {
			return r.fixtureProgram("vis", bannerUnknown, 0, "")
		}, binding.FactUnknownOutput, failure.ToolUnverified},
		{"does not answer in time", func(r *compatRig) string {
			return r.fixtureProgram("vis", bannerModern, 0, "if [ $# -eq 0 ]; then sleep 30; fi")
		}, binding.FactTimedOut, failure.ToolUnverified},
		{"may not be executed", func(r *compatRig) string {
			path := r.fixtureProgram("vis", bannerModern, 0, "")
			if err := os.Chmod(path, 0o644); err != nil {
				r.t.Fatal(err)
			}
			return path
		}, binding.FactNotPermitted, failure.ToolUnavailable},
		{"is not a program for this machine", func(r *compatRig) string {
			path := filepath.Join(r.tools, "vis")
			// An ELF magic number over nothing a loader can use.
			if err := os.WriteFile(path, append([]byte("\x7fELF\x02\x01\x01"), make([]byte, 64)...), 0o755); err != nil {
				r.t.Fatal(err)
			}
			return path
		}, binding.FactWrongArchitecture, failure.ToolUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newCompatRig(t, true, acceptModern, 1)
			rig.all(bannerModern, 0)
			c.prepare(rig)
			err := rig.refused("vis", c.class)
			if failure.Of(err) == failure.ToolIncompatible {
				t.Errorf("%s was called an incompatibility: %v", c.name, err)
			}
			if rig.compiles("vis") != 0 {
				t.Errorf("vis ran: %v", rig.invocations())
			}
			facts, checkErr := rig.service.CheckCompatibility(rig.id, true)
			if checkErr != nil {
				t.Fatal(checkErr)
			}
			if facts["vis"].State != c.state {
				t.Errorf("recorded state %q (%s), want %q", facts["vis"].State, facts["vis"].Detail, c.state)
			}
			if facts["qbsp"].State != binding.FactCompatible {
				t.Errorf("qbsp beside it = %+v", facts["qbsp"])
			}
		})
	}
	t.Run("is not there", func(t *testing.T) {
		rig := newCompatRig(t, true, acceptModern, 1)
		rig.all(bannerModern, 0)
		if err := os.Remove(filepath.Join(rig.tools, "vis")); err != nil {
			t.Fatal(err)
		}
		// A missing program keeps the refusal it always had.
		submitted, err := rig.service.Submit(rig.request("vis"))
		if err != nil {
			t.Fatalf("a missing program is the executor's own failure, not a compatibility refusal: %v", err)
		}
		if finished := rig.waitFor(submitted.ID); finished.State != Failed || finished.FailureClass != failure.ToolUnavailable {
			t.Errorf("a missing vis ended %s / %s", finished.State, finished.FailureClass)
		}
		facts, _ := rig.service.CheckCompatibility(rig.id, true)
		if facts["vis"].State != binding.FactMissing {
			t.Errorf("recorded state %q, want missing", facts["vis"].State)
		}
		if _, kept := rig.saved["vis"]; kept {
			t.Error("an answer about a file that is not there was recorded as evidence")
		}
	})
}

// A document that requires nothing is run exactly as before: the legitimate
// legacy contract a community profile declares.
func TestAProfileThatRequiresNothingRunsWhatItIsGiven(t *testing.T) {
	rig := newCompatRig(t, false, nil, 0)
	rig.all(bannerLegacy, 1)
	for _, action := range []string{"compile", "vis", "light"} {
		rig.succeeds(action)
	}
	for _, program := range []string{"qbsp", "vis", "light"} {
		if rig.probes(program) != 0 || rig.compiles(program) != 1 {
			t.Errorf("%s: %v", program, rig.invocations())
		}
	}
	facts, err := rig.service.CheckCompatibility(rig.id, true)
	if err != nil || len(facts) != 0 {
		t.Errorf("checking a profile that requires nothing = %v, %v", facts, err)
	}
}

// A probe is not an approval and is not allowed before one.
func TestAskingNeverApprovesAndIsRefusedWithoutApproval(t *testing.T) {
	rig := newCompatRig(t, true, acceptModern, 10)
	rig.all(bannerModern, 0)

	rig.mu.Lock()
	local := rig.grant.bindings[rig.id]
	withdrawn := local.Grant
	local.Grant = nil
	rig.grant.bindings[rig.id] = local
	rig.mu.Unlock()

	var notGranted *profile.NotGrantedError
	if _, err := rig.service.CheckCompatibility(rig.id, true); !errors.As(err, &notGranted) {
		t.Fatalf("a profile nothing approved was probed: %v", err)
	}
	if _, err := rig.service.Submit(rig.request("vis")); !errors.As(err, &notGranted) {
		t.Fatalf("a withdrawn approval did not refuse: %v", err)
	}
	if len(rig.invocations()) != 0 {
		t.Fatalf("something was started for a profile nothing approved: %v", rig.invocations())
	}

	// Approved again: asked, compatible, and the grant is the one given — the
	// passing probe wrote nothing to it.
	rig.mu.Lock()
	local = rig.grant.bindings[rig.id]
	local.Grant = withdrawn
	rig.grant.bindings[rig.id] = local
	rig.mu.Unlock()
	if _, err := rig.service.CheckCompatibility(rig.id, true); err != nil {
		t.Fatal(err)
	}
	rig.mu.Lock()
	local = rig.grant.bindings[rig.id]
	rig.mu.Unlock()
	if local.Grant != withdrawn || local.Compatibility["vis"].State != binding.FactCompatible {
		t.Errorf("after probing: grant %p (was %p), vis %+v", local.Grant, withdrawn, local.Compatibility["vis"])
	}
	// And withdrawn again, with compatible answers on record: still refused.
	rig.mu.Lock()
	local.Grant = nil
	rig.grant.bindings[rig.id] = local
	rig.mu.Unlock()
	before := len(rig.invocations())
	if _, err := rig.service.Submit(rig.request("vis")); !errors.As(err, &notGranted) {
		t.Fatalf("compatible evidence stood in for an approval: %v", err)
	}
	if len(rig.invocations()) != before {
		t.Errorf("a refused action started something: %v", rig.invocations()[before:])
	}
}

// A job that was admitted and then waited is checked again when a worker
// takes it: the file may not be the file that was there.
func TestAQueuedJobIsCheckedAgainWhenItStarts(t *testing.T) {
	rig := newCompatRig(t, true, acceptModern, 10)
	rig.all(bannerModern, 0)
	admitted, err := rig.service.admit(rig.request("vis"))
	if err != nil {
		t.Fatal(err)
	}
	rig.fixtureProgram("vis", bannerLegacy, 1, "")
	rig.service.execute(admitted.ID)
	finished := rig.waitFor(admitted.ID)
	if finished.State != Failed || finished.FailureClass != failure.ToolIncompatible {
		t.Fatalf("the job ended %s / %s: %s", finished.State, finished.FailureClass, finished.Error)
	}
	if rig.compiles("vis") != 0 {
		t.Errorf("the replaced vis was started with a map: %v", rig.invocations())
	}
}
