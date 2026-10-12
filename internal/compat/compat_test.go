package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// The first lines each program really prints when started with nothing,
// measured on linux/amd64 on 2026-10-12 (NEW_323D): EricW 0.18.1, upstream
// 2.0.0-alpha11 and the Auto-Pigeon build 1.4505. Only the lines the probe
// reads are kept — the banner, and for the 2.x programs the usage line that
// lists -logfile.
const (
	legacyVis  = "---- vis / ericw-tools v0.18.1 ----\nusage: vis [-threads #] [-level 0-4] [-fast] [-v|-vv] [-credits] bspfile\n"
	legacyQbsp = "---- qbsp / ericw-tools v0.18.1 ----\n\nqbsp performs geometric level processing of Quake .MAP files to create\nQuake .BSP files.\n"
	modernVis  = "---- vis / ericw-tools 2.0.0-alpha11 ----\nvis calculates the visibility (and hearability) sets for \n.BSP files.\n\n  -logfile \"path\"               File to output logging data to. If unchanged, it is set by the tool.\n"
	modernQbsp = "---- qbsp / ericw-tools 2.0.0-alpha11 ----\nqbsp performs geometric level processing of Quake .MAP files to create\nQuake .BSP files.\n"
	// light prints a section header before its banner; the identity is still
	// the token after the marker, on whichever line carries it.
	forkLight = "---- ClearLightmapSurfaces ----\n---- light / ericw-tools 2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505 ----\nlight compiles lightmap data for BSPs\n\n  -logfile \"path\"               File to output logging data to. If unchanged, it is set by the tool.\n"
	forkVis   = "---- vis / ericw-tools 2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505 ----\n  -logfile \"path\"               File to output logging data to.\n"
)

func tool(t *testing.T, id string) *profile.ToolProfile {
	t.Helper()
	entry, err := builtin.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	document, ok := entry.Profile.(*profile.ToolProfile)
	if !ok || document.CompatibilityProbe == nil {
		t.Fatalf("%s declares no compatibility probe", id)
	}
	return document
}

const (
	upstreamID = "auto-pigeon.ericw-tools.q1"
	forkID     = "auto-pigeon.ericw-tools.auto-pigeon-build.q1"
)

func TestAProgramIsJudgedByItsOwnWords(t *testing.T) {
	upstream, fork := tool(t, upstreamID).CompatibilityProbe, tool(t, forkID).CompatibilityProbe
	cases := []struct {
		name       string
		probe      *profile.CompatibilityProbe
		executable string
		stdout     string
		state      binding.FactState
		identity   string
		missing    string
	}{
		{"0.18.1 vis under the 2.0 profile", upstream, "vis", legacyVis, binding.FactIncompatible, "v0.18.1", ""},
		{"0.18.1 qbsp under the 2.0 profile", upstream, "qbsp", legacyQbsp, binding.FactIncompatible, "v0.18.1", ""},
		{"alpha11 vis under the 2.0 profile", upstream, "vis", modernVis, binding.FactCompatible, "2.0.0-alpha11", ""},
		{"alpha11 qbsp under the 2.0 profile", upstream, "qbsp", modernQbsp, binding.FactCompatible, "2.0.0-alpha11", ""},
		// The fork's programs satisfy the upstream document's contract (they
		// are 2.x and list -logfile); the upstream ones do not satisfy the
		// fork's, which is written for the Auto-Pigeon build by name.
		{"fork vis under the 2.0 profile", upstream, "vis", forkVis, binding.FactCompatible, "2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505", ""},
		{"fork light under the fork profile", fork, "light", forkLight, binding.FactCompatible, "2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505", ""},
		{"alpha11 vis under the fork profile", fork, "vis", modernVis, binding.FactIncompatible, "2.0.0-alpha11", ""},
		// A 2.x banner over a usage that does not list the option the action
		// passes: named, accepted, and still proven wrong by its own usage.
		{"a 2.x vis without -logfile", upstream, "vis", "---- vis / ericw-tools 2.0.0-alpha1 ----\nusage: vis [-fast] bspfile\n", binding.FactIncompatible, "2.0.0-alpha1", "-logfile"},
		// Nothing learned is not "incompatible".
		{"a program that prints nothing", upstream, "vis", "", binding.FactUnknownOutput, "", ""},
		{"a program that is something else", upstream, "vis", "GNU bash, version 5.2\n", binding.FactUnknownOutput, "", ""},
	}
	for _, c := range cases {
		state, identity, missing, detail := Classify(c.probe, c.executable, c.stdout, "")
		if state != c.state || identity != c.identity || strings.Join(missing, ",") != c.missing {
			t.Errorf("%s: %s %q missing %v (%s); want %s %q missing %q", c.name, state, identity, missing, detail, c.state, c.identity, c.missing)
		}
		if state != binding.FactCompatible && detail == "" {
			t.Errorf("%s: %s with no sentence saying why", c.name, state)
		}
	}
}

// 1.4505 and 2.0.0-alpha11-19-g… are different identities, not an older and a
// newer one: nothing here parses or orders a version.
func TestAnIdentityIsMatchedLiterallyAndNeverOrdered(t *testing.T) {
	rule := profile.IdentityRule{Prefix: "2.", Contains: "+auto-pigeon."}
	for identity, want := range map[string]bool{
		"2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505": true,
		"2.0.0-alpha11-16-gd4e4a75f+auto-pigeon.1.4502": true,
		"2.0.0-alpha11":        false,
		"1.4505":               false,
		"v0.18.1":              false,
		"12.0.0+auto-pigeon.9": false,
	} {
		if got := rule.Matches(identity); got != want {
			t.Errorf("%q: %v, want %v", identity, got, want)
		}
	}
}

func TestOnlyTheProgramsADocumentNamesAreHeldToIt(t *testing.T) {
	document := tool(t, upstreamID)
	for id, want := range map[string]bool{"compile": true, "vis": true, "light": true, "inspect": false, "check": false} {
		action, found := document.ActionByID(id)
		if !found {
			t.Fatalf("no action %s", id)
		}
		if _, _, required := Requirement(document, action); required != want {
			t.Errorf("%s (%s): required %v, want %v", id, action.Executable, required, want)
		}
	}
	// The probe of a program is not itself refused for the program not having
	// been probed.
	probe, found := document.ActionByID(profile.CompatibilityProbeActionPrefix + "vis")
	if !found || probe.Executable != "vis" || len(probe.Inputs)+len(probe.Outputs) != 0 {
		t.Fatalf("the vis probe action = %+v, %v", probe, found)
	}
	if _, _, required := Requirement(document, probe); required {
		t.Error("a probe requires a probe of itself")
	}
	if _, found := document.ActionByID(profile.CompatibilityProbeActionPrefix + "bspinfo"); found {
		t.Error("bspinfo is not held to the probe and has a probe action")
	}
	for _, action := range document.ActionList() {
		if strings.HasPrefix(action.ID, profile.CompatibilityProbeActionPrefix) {
			t.Errorf("%s is listed as something a user runs", action.ID)
		}
	}
	// A program the probe names and no action runs is not asked: removing the
	// light action from a copy of this document leaves two programs to ask.
	trimmed := *document
	trimmed.Actions = nil
	for _, action := range document.Actions {
		if action.Executable != "light" {
			trimmed.Actions = append(trimmed.Actions, action)
		}
	}
	if got := strings.Join(ProbedExecutables(&trimmed), ","); got != "qbsp,vis" {
		t.Errorf("with no action running light, the programs asked are %q", got)
	}
	// A document that declares nothing requires nothing: a community profile
	// written for 0.18.1 keeps working exactly as it did.
	entry, err := builtin.Find("auto-pigeon.q3map2")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range entry.Profile.ActionList() {
		if _, _, required := Requirement(entry.Profile, action); required {
			t.Errorf("q3map2's %s is held to a probe it does not declare", action.ID)
		}
	}
}

// What the document requires is part of what an answer is about: changing an
// accepted identity or a required option changes the digest, per executable.
func TestARequirementIsAboutOneExecutableAndChangesWithTheDocument(t *testing.T) {
	probe := *tool(t, upstreamID).CompatibilityProbe
	vis, light, qbsp := probe.Requirement("vis"), probe.Requirement("light"), probe.Requirement("qbsp")
	if vis == "" || vis == qbsp || light == qbsp || probe.Requirement("bspinfo") != "" {
		t.Fatalf("requirements: vis %s light %s qbsp %s bspinfo %q", vis, light, qbsp, probe.Requirement("bspinfo"))
	}
	changed := probe
	changed.Accept = []profile.IdentityRule{{Prefix: "2.0.0-alpha12"}}
	if changed.Requirement("vis") == vis {
		t.Error("a different accepted identity left the requirement unchanged")
	}
	changed = probe
	changed.Executables = []profile.ProbedExecutable{{Name: "qbsp"}, {Name: "vis", Mentions: []string{"-logfile", "-nostate"}}, {Name: "light", Mentions: []string{"-logfile"}}}
	if changed.Requirement("vis") == vis || changed.Requirement("light") != light {
		t.Error("a different required option for vis must change vis's requirement and nobody else's")
	}
}

func TestJudgeNamesEachStateDifferently(t *testing.T) {
	document := tool(t, upstreamID)
	path := "/opt/tools/vis"
	if finding := Judge(document, "vis", path, nil); finding == nil || finding.Kind != Unverified || !strings.Contains(finding.Fix, "toolchain check "+upstreamID) {
		t.Errorf("no evidence = %+v", finding)
	}
	kinds := map[binding.FactState]Kind{
		binding.FactIncompatible: Incompatible, binding.FactUnknownOutput: Unverified, binding.FactTimedOut: Unverified,
		binding.FactNotPermitted: Unusable, binding.FactWrongArchitecture: Unusable, binding.FactMissing: Unusable, binding.FactFailed: Unusable,
	}
	seen := map[string]binding.FactState{}
	for state, want := range kinds {
		finding := Judge(document, "vis", path, &binding.ExecutableFact{State: state, Identity: "v0.18.1", Detail: "why"})
		if finding == nil || finding.Kind != want || finding.State != state || !strings.Contains(finding.Summary, path) || finding.Fix == "" {
			t.Errorf("%s = %+v, want kind %s", state, finding, want)
			continue
		}
		if other, dup := seen[finding.Summary]; dup {
			t.Errorf("%s and %s are told to the user in the same sentence", state, other)
		}
		seen[finding.Summary] = state
	}
	if finding := Judge(document, "vis", path, &binding.ExecutableFact{State: binding.FactCompatible}); finding != nil {
		t.Errorf("a compatible program = %+v", finding)
	}
}

func TestAFactDescribesOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vis")
	if err := os.WriteFile(path, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	fact := binding.ExecutableFact{Path: path, Size: first.Size, ModifiedAt: first.ModifiedAt, SHA256: first.SHA256, Requirement: "r"}
	if cheap, _ := Stat(path); !fact.Describes(path, cheap.Size, cheap.ModifiedAt, "r") {
		t.Fatal("a fact does not describe the file it was taken from")
	}
	if cheap, _ := Stat(path); fact.Describes(path, cheap.Size, cheap.ModifiedAt, "another requirement") {
		t.Error("a fact for one requirement answered another")
	}
	// Replaced where it stood, same length, a moment later.
	if err := os.WriteFile(path, []byte("two"), 0o755); err != nil {
		t.Fatal(err)
	}
	later := first.ModifiedAt.Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	second, err := Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	if second.SHA256 == first.SHA256 {
		t.Fatal("different bytes, same digest")
	}
	if fact.Describes(path, second.Size, second.ModifiedAt, "r") {
		t.Error("a fact about the old file described the new one")
	}
}
