package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

func TestEveryBuiltinDocumentIsValidAndDigestible(t *testing.T) {
	entries, err := Load()
	if err != nil {
		t.Fatalf("the profiles this build ships do not load: %v", err)
	}
	if len(entries) < 3 {
		t.Fatalf("expected at least one document of each kind, got %d", len(entries))
	}
	kinds := map[profile.Kind]bool{}
	for _, e := range entries {
		meta := e.Profile.Metadata()
		kinds[meta.Kind] = true
		if e.Trust() != profile.TrustBuiltin {
			t.Errorf("%s is not built in", e.File)
		}
		if !strings.HasPrefix(e.Digest, "sha256:") {
			t.Errorf("%s has no digest", e.File)
		}
		if meta.SchemaVersion != profile.SchemaVersion {
			t.Errorf("%s declares %q, not the current schema version", e.File, meta.SchemaVersion)
		}
		if !strings.HasPrefix(meta.ID, "auto-pigeon.") {
			t.Errorf("%s has the id %q; a document that ships inside this binary is published by "+
				"this project and its id says so", e.File, meta.ID)
		}
		// A document that is still a sample says so in its id. The rule used to
		// be that *every* built-in was one; it stopped being true when the
		// qualified EricW profile arrived, and the half that still matters is
		// that an unqualified document may not present itself as a curated one.
		if tool, isTool := e.Profile.(*profile.ToolProfile); isTool && !strings.Contains(meta.ID, ".sample.") {
			if strings.Contains(tool.ToolVersion, "sample") {
				t.Errorf("%s claims the tool version %q while not calling itself a sample",
					e.File, tool.ToolVersion)
			}
		}
	}
	for _, kind := range profile.Kinds {
		if !kinds[kind] {
			t.Errorf("no built-in %s profile ships, so that kind has no worked example", kind)
		}
	}
}

func TestBuiltinDigestsAreStableAcrossLoads(t *testing.T) {
	first, err := Load()
	if err != nil {
		t.Fatalf("%v", err)
	}
	second, err := Load()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for i := range first {
		if first[i].Digest != second[i].Digest {
			t.Errorf("%s digests differently on two loads: %s vs %s", first[i].File, first[i].Digest, second[i].Digest)
		}
	}
}

func TestFindReportsAMissingIdByName(t *testing.T) {
	if _, err := Find(EricwQ1); err != nil {
		t.Errorf("a built-in profile could not be found: %v", err)
	}
	_, err := Find("not.installed")
	if err == nil {
		t.Fatal("a missing id was found")
	}
	if !strings.Contains(err.Error(), "not.installed") {
		t.Errorf("the error does not name what was looked for: %v", err)
	}
}

// This is the test the extension model exists to pass.
//
// A profile that ships inside the binary and a profile a user typed by hand are
// resolved by the same function, with no branch on trust anywhere between them,
// and produce the same command. The two documents are deliberately spelled
// differently — different id, different publisher, different member order,
// arguments written in both the string and the object form — so that what is
// being compared is what runs and not how it was written.
//
// If a privileged path for built-in profiles ever appears, this is what fails.
func TestBuiltinAndUserAuthoredResolveToTheSameCommand(t *testing.T) {
	sample, err := Find(EricwQ1)
	if err != nil {
		t.Fatalf("%v", err)
	}
	authoredData, err := os.ReadFile(filepath.Join("..", "testdata", "community", "user-ericw-q1.tool.json"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	authored, err := profile.Decode(authoredData)
	if err != nil {
		t.Fatalf("the user-authored fixture is invalid:\n%v", err)
	}

	// They are different documents.
	builtinDigest, err := profile.Digest(sample.Profile)
	if err != nil {
		t.Fatalf("%v", err)
	}
	authoredDigest, err := profile.Digest(authored)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if builtinDigest == authoredDigest {
		t.Fatal("the two fixtures are the same document; the test would prove nothing")
	}
	if sample.Trust() == profile.TrustCommunity {
		t.Fatal("the built-in sample is not built in")
	}

	base := t.TempDir()
	request := profile.Request{
		Platform: profile.Platform{OS: "linux", Arch: "amd64"},
		Roots: map[string]string{
			profile.RootWorkspace:   filepath.Join(base, "workspace"),
			profile.RootToolInstall: filepath.Join(base, "tools"),
		},
		Inputs:  map[string]string{"source_map": filepath.Join(base, "workspace", "input", "source_map", "level.map")},
		Options: map[string]string{"basename": "start", "verbosity": "verbose"},
	}

	fromBuiltin, err := profile.Resolve(sample.Profile, "compile", request)
	if err != nil {
		t.Fatalf("resolving the built-in profile: %v", err)
	}
	fromAuthored, err := profile.Resolve(authored, "compile", request)
	if err != nil {
		t.Fatalf("resolving the user-authored profile: %v", err)
	}

	if fromBuiltin.Command.Digest() != fromAuthored.Command.Digest() {
		t.Errorf("the same command resolved differently\n  built in:      %s\n  user authored: %s",
			fromBuiltin.Command, fromAuthored.Command)
	}
	if fromBuiltin.ProfileID == fromAuthored.ProfileID {
		t.Error("the two invocations report the same profile id; they should differ in identity and agree in effect")
	}
	// And the shared effect is the real thing, not an empty command.
	if len(fromBuiltin.Command.Args) != 4 {
		t.Errorf("unexpected argv: %v", fromBuiltin.Command.Args)
	}
	if want := filepath.Join(request.Roots[profile.RootWorkspace], "start.bsp"); fromBuiltin.Outputs["bsp"] != want {
		t.Errorf("output path is %q, want %q", fromBuiltin.Outputs["bsp"], want)
	}
}

// Two built-in tool profiles that both provide `q1.bsp.compile` would make
// "which tool runs this step" a question a pipeline could not answer, and the
// answer would depend on iteration order. It is why the unqualified Q1 sample
// was retired rather than kept beside the qualified profile.
func TestNoTwoBuiltinToolsProvideTheSameCapability(t *testing.T) {
	entries, err := Load()
	if err != nil {
		t.Fatalf("%v", err)
	}
	provider := map[string]string{}
	for _, e := range entries {
		tool, isTool := e.Profile.(*profile.ToolProfile)
		if !isTool {
			continue
		}
		for _, a := range tool.Actions {
			if a.Capability == "" {
				continue
			}
			if first, clash := provider[a.Capability]; clash {
				t.Errorf("%s and %s both provide %q", first, e.File, a.Capability)
			}
			provider[a.Capability] = e.File
		}
	}
}

// Every built-in pipeline resolves against the built-in toolchain, on the
// machine this test runs on, before anybody has installed anything. A pipeline
// that shipped and did not resolve would fail at the point a user pressed the
// button.
func TestEveryBuiltinPipelineResolvesAgainstTheBuiltinToolchain(t *testing.T) {
	toolEntry, err := Find(EricwQ1)
	if err != nil {
		t.Fatalf("%v", err)
	}
	tool, ok := toolEntry.Profile.(*profile.ToolProfile)
	if !ok {
		t.Fatal("the EricW profile is not a tool profile")
	}
	entries, err := Load()
	if err != nil {
		t.Fatalf("%v", err)
	}
	seen := 0
	for _, e := range entries {
		pipeline, isPipeline := e.Profile.(*profile.PipelineProfile)
		if !isPipeline {
			continue
		}
		seen++
		steps, err := pipeline.Resolve(installed{tool})
		if err != nil {
			t.Errorf("%s does not resolve against the built-in toolchain:\n%v", e.File, err)
			continue
		}
		if len(steps) != 3 {
			t.Errorf("%s resolved to %d steps, want three", e.File, len(steps))
			continue
		}
		for i, want := range []string{"compile", "vis", "light"} {
			if steps[i].Action.ID != want {
				t.Errorf("%s step %d resolved to %q, want %q", e.File, i, steps[i].Action.ID, want)
			}
		}
	}
	if seen != 3 {
		t.Errorf("this build ships %d pipelines; fast_preview, normal and final are the three", seen)
	}
}

// The three pipelines are the same three steps with different options. What
// distinguishes them has to actually reach the command, or they are three names
// for one build.
func TestTheThreePipelinesDifferInWhatTheyRun(t *testing.T) {
	want := map[string]struct{ visFast, sampling, lit string }{
		"auto-pigeon.q1.fast-preview": {"true", "none", "false"},
		"auto-pigeon.q1.normal":       {"false", "none", "true"},
		"auto-pigeon.q1.final":        {"false", "extra4", "true"},
	}
	for id, expected := range want {
		entry, err := Find(id)
		if err != nil {
			t.Fatalf("%v", err)
		}
		pipeline := entry.Profile.(*profile.PipelineProfile)
		options := map[string]map[string]string{}
		for _, step := range pipeline.Steps {
			options[step.ID] = step.Options
		}
		if got := options["vis"]["fast"]; got != expected.visFast {
			t.Errorf("%s: vis fast is %q, want %q", id, got, expected.visFast)
		}
		if got := options["light"]["sampling"]; got != expected.sampling {
			t.Errorf("%s: light sampling is %q, want %q", id, got, expected.sampling)
		}
		if got := options["light"]["lit"]; got != expected.lit {
			t.Errorf("%s: light lit is %q, want %q", id, got, expected.lit)
		}
	}
}

// The engine sample exercises all five actions and all three session roles, so
// that "an engine that does not support one omits it" is a rule with a worked
// example rather than only a paragraph.
func TestTheEngineSampleCoversEveryActionAndSessionRole(t *testing.T) {
	entry, err := Find("auto-pigeon.sample.q1-engine")
	if err != nil {
		t.Fatalf("%v", err)
	}
	engine, ok := entry.Profile.(*profile.EngineProfile)
	if !ok {
		t.Fatalf("the sample is not an engine profile")
	}
	for _, id := range profile.EngineActions {
		if _, found := engine.ActionByID(id); !found {
			t.Errorf("the sample engine has no %q action", id)
		}
	}
	roles := map[profile.SessionRole]bool{}
	for _, a := range engine.Actions {
		roles[a.SessionRole] = true
	}
	for _, role := range []profile.SessionRole{profile.SessionClient, profile.SessionListen, profile.SessionDedicated} {
		if !roles[role] {
			t.Errorf("no action declares the %q session role", role)
		}
	}

	permissions := entry.Profile.Permissions()
	var hosting int
	for _, p := range permissions {
		if strings.HasPrefix(p.ID, "host_") {
			hosting++
			if p.Risk != profile.RiskHigh {
				t.Errorf("hosting permission %q is %q risk", p.ID, p.Risk)
			}
		}
	}
	if hosting != 2 {
		t.Errorf("expected separate listen and dedicated hosting permissions, got %d", hosting)
	}
}

// The engine sample's play_map resolves to the argv the profile describes,
// with runtime values substituted and nothing else.
func TestEngineActionsResolveWithRuntimeValues(t *testing.T) {
	entry, err := Find("auto-pigeon.sample.q1-engine")
	if err != nil {
		t.Fatalf("%v", err)
	}
	base := t.TempDir()
	request := profile.Request{
		Platform: profile.Platform{OS: "linux", Arch: "amd64"},
		Roots: map[string]string{
			profile.RootGame:        filepath.Join(base, "games", "quake"),
			profile.RootContent:     filepath.Join(base, "projects", "mymap"),
			profile.RootToolInstall: filepath.Join(base, "engines"),
		},
		Runtime: map[string]string{"map_name": "e1m1"},
	}
	invocation, err := profile.Resolve(entry.Profile, profile.ActionPlayMap, request)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if invocation.SessionRole != profile.SessionClient {
		t.Errorf("play_map resolved as %q", invocation.SessionRole)
	}
	joined := strings.Join(invocation.Command.Args, " ")
	if !strings.Contains(joined, "+map e1m1") {
		t.Errorf("the map name did not reach argv: %v", invocation.Command.Args)
	}
	if !strings.Contains(joined, request.Roots[profile.RootGame]) {
		t.Errorf("the game root did not reach argv: %v", invocation.Command.Args)
	}

	// A runtime value the action does not use is not required; one it does use
	// is.
	delete(request.Runtime, "map_name")
	if _, err := profile.Resolve(entry.Profile, profile.ActionPlayMap, request); err == nil {
		t.Error("play_map resolved with no map name")
	}
}

type installed struct{ tool *profile.ToolProfile }

func (i installed) Provider(capability string) (*profile.ToolProfile, profile.Action, bool) {
	for _, a := range i.tool.Actions {
		if a.Capability == capability {
			return i.tool, a, true
		}
	}
	return nil, profile.Action{}, false
}
