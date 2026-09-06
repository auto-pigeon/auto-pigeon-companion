package profile

import (
	"path/filepath"
	"strings"
	"testing"
)

func testRequest(t *testing.T) Request {
	t.Helper()
	base := t.TempDir()
	return Request{
		Platform: Platform{OS: "linux", Arch: "amd64"},
		Roots: map[string]string{
			RootWorkspace:   filepath.Join(base, "workspace"),
			RootToolInstall: filepath.Join(base, "tools", "q1"),
			RootGame:        filepath.Join(base, "games", "quake"),
			RootContent:     filepath.Join(base, "projects", "mymap"),
		},
		Inputs: map[string]string{"source_map": filepath.Join(base, "workspace", "input", "level.map")},
	}
}

func TestResolveProducesTheCommandThePreviewShows(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)

	invocation, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"-threads", "4", request.Inputs["source_map"],
		filepath.Join(request.Roots[RootWorkspace], "compile", "level.bsp")}
	if len(invocation.Command.Args) != len(want) {
		t.Fatalf("argv is %v, want %v", invocation.Command.Args, want)
	}
	for i := range want {
		if invocation.Command.Args[i] != want[i] {
			t.Errorf("argument %d is %q, want %q", i, invocation.Command.Args[i], want[i])
		}
	}
	if got, want := invocation.Command.Executable, filepath.Join(request.Roots[RootToolInstall], "qbsp"); got != want {
		t.Errorf("executable is %q, want %q", got, want)
	}
	if got, want := invocation.Command.WorkingDir, filepath.Clean(request.Roots[RootWorkspace]); got != want {
		t.Errorf("working directory is %q, want %q", got, want)
	}
	if len(invocation.WriteRoots) != 1 {
		t.Errorf("write roots are %v; the action declares exactly one", invocation.WriteRoots)
	}
	if invocation.Network.Required {
		t.Error("the invocation claims the action needs the network; the document does not say so")
	}
	// The preview and the executed command are the same value, which is the
	// only way a preview is worth showing.
	if !strings.Contains(invocation.Command.String(), "-threads") {
		t.Errorf("the rendered preview does not contain the arguments: %s", invocation.Command)
	}
}

// A conditional argument appears when its condition holds and not otherwise,
// and nothing else in the command moves.
func TestConditionalArgumentsAreIncludedOnlyWhenTheirConditionHolds(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)

	off, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	request.Options = map[string]string{"quiet": "true"}
	on, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(on.Command.Args) != len(off.Command.Args)+1 {
		t.Fatalf("turning the option on changed the argument count from %d to %d", len(off.Command.Args), len(on.Command.Args))
	}
	found := false
	for _, a := range on.Command.Args {
		if a == "-nopercent" {
			found = true
		}
	}
	if !found {
		t.Errorf("the conditional argument was not included: %v", on.Command.Args)
	}
	for _, a := range off.Command.Args {
		if a == "-nopercent" {
			t.Errorf("the conditional argument was included with the option off: %v", off.Command.Args)
		}
	}
}

// An option's value reaches argv as one literal element. It is not split on
// spaces, it is not re-parsed, and nothing in it is interpreted.
func TestOptionValuesReachArgvAsSingleLiteralElements(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)
	request.Options = map[string]string{"basename": "level-1.2_final"}

	invocation, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	last := invocation.Command.Args[len(invocation.Command.Args)-1]
	if filepath.Base(last) != "level-1.2_final.bsp" {
		t.Errorf("the option did not reach the output path literally: %q", last)
	}
}

// Paths with spaces, Unicode and characters a shell would treat as syntax are
// passed through untouched, because there is no shell.
func TestPathsWithSpacesAndMetacharactersArePassedLiterally(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)
	awkward := filepath.Join(t.TempDir(), "my maps", "проба; rm -rf $HOME", "level.map")
	request.Inputs = map[string]string{"source_map": awkward}

	invocation, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	found := false
	for _, a := range invocation.Command.Args {
		if a == awkward {
			found = true
		}
	}
	if !found {
		t.Errorf("the input path was altered on its way to argv: %v", invocation.Command.Args)
	}
}

func TestUnresolvedPlaceholdersAreRefusedRatherThanEmptied(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)
	request.Inputs = nil

	_, err := Resolve(p, "compile", request)
	if err == nil {
		t.Fatal("an action resolved with a required input missing")
	}
	if !strings.Contains(err.Error(), "source_map") {
		t.Errorf("the error does not name the missing input: %v", err)
	}
}

func TestMissingRootIsRefusedByName(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)
	delete(request.Roots, RootToolInstall)

	_, err := Resolve(p, "compile", request)
	if err == nil {
		t.Fatal("an action resolved with no tool installation")
	}
	if !strings.Contains(err.Error(), RootToolInstall) {
		t.Errorf("the error does not name the missing root: %v", err)
	}
}

func TestIllegalOptionValuesAreRefusedAtResolution(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)
	request.Options = map[string]string{"threads": "999"}

	_, err := Resolve(p, "compile", request)
	if err == nil {
		t.Fatal("an out-of-range option was accepted")
	}
	if !strings.Contains(err.Error(), "maximum") {
		t.Errorf("the error does not explain the range: %v", err)
	}

	request.Options = map[string]string{"nonsense": "1"}
	if _, err := Resolve(p, "compile", request); err == nil {
		t.Fatal("an undeclared option was accepted")
	}
}

func TestUnsupportedPlatformIsRefusedWithTheProfilesOwnClaim(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)
	request.Platform = Platform{OS: "windows", Arch: "amd64"}

	_, err := Resolve(p, "compile", request)
	if err == nil {
		t.Fatal("an undeclared platform was accepted")
	}
	if !strings.Contains(err.Error(), "makes no claim") {
		t.Errorf("the error does not explain that the profile is silent about this platform: %v", err)
	}
}

func TestAnActionInheritsNothingUnlessItSaysSo(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)
	request.HostEnv = map[string]string{"AUB_TOKEN": "secret", "HOME": "/home/someone"}

	invocation, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(invocation.Command.Env) != 0 {
		t.Errorf("the process would inherit %v; the document asked for nothing", invocation.Command.Env)
	}
}

func TestCommandDigestIsAboutWhatRunsAndNotWhoWroteIt(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	request := testRequest(t)

	first, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	second, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if first.Command.Digest() != second.Command.Digest() {
		t.Error("resolving the same request twice produced different command digests")
	}

	request.Options = map[string]string{"threads": "8"}
	changed, err := Resolve(p, "compile", request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if changed.Command.Digest() == first.Command.Digest() {
		t.Error("changing an argument did not change the command digest")
	}
}

// A pipeline names capabilities; the resolver answers which installed action
// implements each one. Nothing in the pipeline document decides that.
func TestPipelineResolvesThroughCapabilitiesAndChecksTheWiring(t *testing.T) {
	tool, ok := decodeFixture(t, "community/user-q1-toolchain.tool.json").(*ToolProfile)
	if !ok {
		t.Fatal("the fixture is not a tool profile")
	}
	pipeline := &PipelineProfile{
		Meta: Meta{
			SchemaVersion: SchemaVersion, Kind: KindPipeline,
			ID: "example.one-step", Version: "1.0.0", Name: "One step",
			Summary:   "Compile and stop.",
			Publisher: Publisher{Name: "Example"}, License: License{SPDX: "MIT"},
		},
		Inputs: []InputSpec{{Name: "source_map", Role: "q1.map.source", Required: true}},
		Steps: []PipelineStep{{
			ID: "compile", Title: "Compile", Capability: "q1.bsp.compile",
			Inputs:  []PipelineWire{{Name: "source_map", From: "pipeline.source_map"}},
			Options: map[string]string{"threads": "8"},
		}},
		Outputs: []PipelineOutput{{Name: "bsp", Role: "q1.bsp", From: "compile.bsp"}},
	}
	if err := pipeline.Validate(); err != nil {
		t.Fatalf("the pipeline is invalid: %v", err)
	}

	resolver := fixedResolver{profile: tool}
	steps, err := pipeline.Resolve(resolver)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(steps) != 1 || steps[0].Action.ID != "compile" {
		t.Fatalf("unexpected resolution: %+v", steps)
	}

	// A pipeline may not set an option the resolved action does not declare,
	// and may not wire an artifact of the wrong kind.
	pipeline.Steps[0].Options = map[string]string{"unheard_of": "1"}
	if _, err := pipeline.Resolve(resolver); err == nil {
		t.Error("an option the action does not declare was accepted")
	}
	pipeline.Steps[0].Options = nil
	pipeline.Inputs[0].Role = "q1.bsp"
	if _, err := pipeline.Resolve(resolver); err == nil {
		t.Error("an artifact of the wrong role was wired into a step")
	}
}

// A pipeline needing a capability nothing provides fails before anything runs,
// which is the point of resolving separately from executing.
func TestPipelineWithNoProviderFailsBeforeAnythingRuns(t *testing.T) {
	pipeline := decodeFixture(t, "../builtin/sample-q1-normal.pipeline.json").(*PipelineProfile)
	if _, err := pipeline.Resolve(fixedResolver{}); err == nil {
		t.Fatal("a pipeline resolved with nothing installed")
	} else if !strings.Contains(err.Error(), "q1.bsp.compile") {
		t.Errorf("the error does not name the missing capability: %v", err)
	}
}

type fixedResolver struct{ profile *ToolProfile }

func (r fixedResolver) Provider(capability string) (*ToolProfile, Action, bool) {
	if r.profile == nil {
		return nil, Action{}, false
	}
	for _, a := range r.profile.Actions {
		if a.Capability == capability {
			return r.profile, a, true
		}
	}
	return nil, Action{}, false
}
