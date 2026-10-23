package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/compat"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_323D: what a page is told about a program's compatibility is read from
// the recorded answer, believed only for the file that is there now, and is
// reported BESIDE the problems that were already reported — it replaces none
// of them and is replaced by none.

const ericwQ1 = "auto-pigeon.ericw-tools.q1"

// compatBinding binds the built-in 2.0 profile to one real file per program,
// with a recorded answer of the given state for vis.
func compatBinding(t *testing.T, document profile.Profile, digest string, vis binding.FactState) (binding.LocalBinding, string) {
	t.Helper()
	dir := t.TempDir()
	local := binding.LocalBinding{
		SchemaVersion: binding.SchemaVersion, ProfileID: ericwQ1, ProfileDigest: digest, Trust: profile.TrustBuiltin,
		Executables: map[string]string{}, Compatibility: map[string]binding.ExecutableFact{}, UpdatedAt: time.Now().UTC(),
	}
	tool := document.(*profile.ToolProfile)
	for _, name := range []string{"qbsp", "vis", "light", "bspinfo"} {
		path := filepath.Join(dir, name)
		writeFile(t, path, "#!/bin/false "+name+"\n")
		local.Executables[name] = path
		if _, probed := tool.CompatibilityProbe.For(name); !probed {
			continue
		}
		identity, err := compat.Digest(path)
		if err != nil {
			t.Fatal(err)
		}
		state := binding.FactCompatible
		if name == "vis" {
			state = vis
		}
		local.Compatibility[name] = binding.ExecutableFact{
			Path: path, Size: identity.Size, ModifiedAt: identity.ModifiedAt, SHA256: identity.SHA256,
			Requirement: tool.CompatibilityProbe.Requirement(name), State: state, Identity: "v0.18.1",
			Detail: "it calls itself v0.18.1; this profile accepts 2.…", CheckedAt: time.Now().UTC(),
		}
	}
	return local, dir
}

func faultsOf(problems engine.Problems) []engine.Fault {
	out := make([]engine.Fault, 0, len(problems))
	for _, problem := range problems {
		out = append(out, problem.Fault)
	}
	return out
}

func sameFaults(got engine.Problems, want ...engine.Fault) bool {
	faults := faultsOf(got)
	if len(faults) != len(want) {
		return false
	}
	for i := range want {
		if faults[i] != want[i] {
			return false
		}
	}
	return true
}

func TestARecordedIncompatibilityIsReportedPerAction(t *testing.T) {
	document, digest := builtinEngine(t, ericwQ1)
	local, _ := compatBinding(t, document, digest, binding.FactIncompatible)
	for action, want := range map[string][]engine.Fault{
		"compile": nil, "light": nil, "inspect": nil,
		"vis": {engine.FaultToolIncompatible},
	} {
		if got := linux().Check(document, profile.TrustBuiltin, digest, local, action); !sameFaults(got, want...) {
			t.Errorf("%s: %v, want %v", action, faultsOf(got), want)
		}
	}
	// Every other state is its own fault kind, never "incompatible".
	for state, want := range map[binding.FactState]engine.Fault{
		binding.FactUnknownOutput: engine.FaultToolUnverified, binding.FactTimedOut: engine.FaultToolUnverified,
		binding.FactNotPermitted: engine.FaultToolUnusable, binding.FactWrongArchitecture: engine.FaultToolUnusable,
		binding.FactFailed: engine.FaultToolUnusable,
	} {
		local, _ := compatBinding(t, document, digest, state)
		if got := linux().Check(document, profile.TrustBuiltin, digest, local, "vis"); !sameFaults(got, want) {
			t.Errorf("%s: %v, want %s", state, faultsOf(got), want)
		}
	}
}

func TestAnAnswerAboutAnotherFileIsNotShownAsThisOnes(t *testing.T) {
	document, digest := builtinEngine(t, ericwQ1)
	local, dir := compatBinding(t, document, digest, binding.FactCompatible)
	if got := linux().Check(document, profile.TrustBuiltin, digest, local, "vis"); len(got) != 0 {
		t.Fatalf("a compatible vis: %v", got)
	}
	// Replaced where it stood.
	path := filepath.Join(dir, "vis")
	writeFile(t, path, "#!/bin/false another program entirely\n")
	later := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if got := linux().Check(document, profile.TrustBuiltin, digest, local, "vis"); !sameFaults(got, engine.FaultToolUnverified) {
		t.Errorf("a replaced vis: %v", faultsOf(got))
	}
	// Bound somewhere else: the old path's answer does not follow it.
	moved := filepath.Join(dir, "vis-elsewhere")
	writeFile(t, moved, "#!/bin/false vis\n")
	local.Executables["vis"] = moved
	if got := linux().Check(document, profile.TrustBuiltin, digest, local, "vis"); !sameFaults(got, engine.FaultToolUnverified) {
		t.Errorf("a rebound vis: %v", faultsOf(got))
	}
	// Not there at all: the problem it always was, and only that.
	local.Executables["vis"] = filepath.Join(dir, "gone")
	if got := linux().Check(document, profile.TrustBuiltin, digest, local, "vis"); !sameFaults(got, engine.FaultMissingEngine) {
		t.Errorf("a missing vis: %v", faultsOf(got))
	}
}

// A withdrawn approval stays the first thing said, and a compatible answer on
// record does not stand in for it.
func TestCompatibilityNeitherHidesNorReplacesAnApproval(t *testing.T) {
	document, digest := builtinEngine(t, ericwQ1)
	// The same document as somebody else's: it needs a grant, and has none.
	local, _ := compatBinding(t, document, digest, binding.FactCompatible)
	local.Trust = profile.TrustCommunity
	if got := linux().Check(document, profile.TrustCommunity, digest, local, "vis"); !sameFaults(got, engine.FaultNotAuthorized) {
		t.Errorf("compatible programs, no approval: %v", faultsOf(got))
	}
	// With a proven incompatibility on record both are said, approval first.
	local, _ = compatBinding(t, document, digest, binding.FactIncompatible)
	if got := linux().Check(document, profile.TrustCommunity, digest, local, "vis"); !sameFaults(got, engine.FaultNotAuthorized, engine.FaultToolIncompatible) {
		t.Errorf("incompatible programs, no approval: %v", faultsOf(got))
	}
	// With nothing on record, "not asked yet" is left unsaid: a program
	// nothing has approved cannot be asked, and approving it is the next step.
	local.Compatibility = nil
	if got := linux().Check(document, profile.TrustCommunity, digest, local, "vis"); !sameFaults(got, engine.FaultNotAuthorized) {
		t.Errorf("unasked programs, no approval: %v", faultsOf(got))
	}
	// Approved (built in) and never asked: that is a problem, and it is named.
	if got := linux().Check(document, profile.TrustBuiltin, digest, local, "vis"); !sameFaults(got, engine.FaultToolUnverified) {
		t.Errorf("unasked programs, approved: %v", faultsOf(got))
	}
}

// TestAToolNobodyPointedAtIsNotChosenAgain (NEW_345): a program whose path was
// never recorded was fixed with "Choose it again" — wrong on every fresh
// install, where nobody chose anything. A path that was recorded and is gone
// is still chosen again.
func TestAToolNobodyPointedAtIsNotChosenAgain(t *testing.T) {
	document, digest := builtinEngine(t, ericwQ1)
	never := binding.LocalBinding{SchemaVersion: binding.SchemaVersion, ProfileID: ericwQ1, ProfileDigest: digest,
		Trust: profile.TrustBuiltin, Executables: map[string]string{}}
	got := linux().Check(document, profile.TrustBuiltin, digest, never, "vis")
	if !sameFaults(got, engine.FaultMissingEngine) || !strings.HasPrefix(got[0].Fix, "Choose where it is under") {
		t.Errorf("never chosen: %v", got)
	}
	local, dir := compatBinding(t, document, digest, binding.FactCompatible)
	local.Executables["vis"] = filepath.Join(dir, "gone")
	got = linux().Check(document, profile.TrustBuiltin, digest, local, "vis")
	if !sameFaults(got, engine.FaultMissingEngine) || !strings.HasPrefix(got[0].Fix, "Choose it again under") {
		t.Errorf("chosen and gone: %v", got)
	}
}
