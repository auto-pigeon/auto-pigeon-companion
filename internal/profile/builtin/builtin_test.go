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

// An engine profile declares what its engine does and leaves out what it does
// not, so this checks the leaving-out rather than the declaring: QuakeSpasm is
// a client and must not offer a dedicated server, QSS is the QuakeSpasm
// derivative that does, and no profile may declare an action outside the closed
// vocabulary or give one the wrong session role.
func TestCuratedEnginesDeclareOnlyWhatTheirEnginesDo(t *testing.T) {
	engines := loadEngines(t)
	for _, id := range Q1Engines {
		if _, shipped := engines[id]; !shipped {
			t.Errorf("%s is named as a curated engine and does not ship", id)
		}
	}

	roles := map[profile.SessionRole]bool{}
	for id, engine := range engines {
		for _, action := range engine.Actions {
			if !contains(profile.EngineActions, action.ID) {
				t.Errorf("%s declares %q, which is not an engine action", id, action.ID)
			}
			roles[action.SessionRole] = true
		}
	}
	for _, role := range []profile.SessionRole{profile.SessionClient, profile.SessionListen, profile.SessionDedicated} {
		if !roles[role] {
			t.Errorf("no curated engine action declares the %q session role", role)
		}
	}

	if _, offers := engines[QuakeSpasm].ActionByID(profile.ActionHostDedicated); offers {
		t.Error("QuakeSpasm declares host_dedicated; it is a client, and a profile that offered it " +
			"would be a button that starts something else")
	}
	if _, offers := engines[QuakeSpasmSpiked].ActionByID(profile.ActionHostDedicated); !offers {
		t.Error("QuakeSpasm-Spiked does not declare host_dedicated, which is the reason it has a profile of its own")
	}
	if _, offers := engines[Q1Generic].ActionByID(profile.ActionHostDedicated); offers {
		t.Error("the generic profile declares host_dedicated; whether a build has a dedicated server " +
			"compiled in is exactly what a generic profile cannot know")
	}
}

// Hosting is asked for separately from running, and at high risk, on every
// engine that can host. A user who pressed something labelled Play is entitled
// to be asked before their machine starts accepting connections.
func TestHostingIsItsOwnHighRiskPermission(t *testing.T) {
	for id, engine := range loadEngines(t) {
		hosts := false
		for _, action := range engine.Actions {
			if action.SessionRole == profile.SessionListen || action.SessionRole == profile.SessionDedicated {
				hosts = true
			}
		}
		var hosting int
		for _, permission := range engine.Permissions() {
			if strings.HasPrefix(permission.ID, "host_") {
				hosting++
				if permission.Risk != profile.RiskHigh {
					t.Errorf("%s: hosting permission %q is %q risk", id, permission.ID, permission.Risk)
				}
			}
		}
		if hosts && hosting == 0 {
			t.Errorf("%s hosts and asks for no hosting permission", id)
		}
		if !hosts && hosting > 0 {
			t.Errorf("%s asks for a hosting permission and hosts nothing", id)
		}
	}
}

// A curated engine profile says who publishes the engine, under what licence,
// which version it was written against and when. None of that is decoration:
// it is what a user reads before pointing the Companion at a program somebody
// else wrote, and it is what a copyleft licence requires be reachable.
func TestCuratedEnginesCarrySourceLicenceAndQualification(t *testing.T) {
	for id, engine := range loadEngines(t) {
		if engine.Source == nil || engine.Source.Homepage == "" || engine.Source.Repository == "" {
			t.Errorf("%s does not say where the engine itself comes from", id)
		}
		if engine.License.SPDX == "" || engine.License.Notice == "" {
			t.Errorf("%s does not carry the engine's licence and its notice", id)
		}
		if engine.EngineVersion == "" {
			t.Errorf("%s does not say which upstream version it was written against", id)
		}
		if engine.LastQualified == "" {
			t.Errorf("%s does not say when it was last checked", id)
		}
		if !strings.Contains(engine.License.Notice, "never copies") {
			t.Errorf("%s's licence notice does not say that game data is never copied or redistributed", id)
		}
	}
}

// The claim each curated profile makes about a platform is checked for the one
// thing a reader cannot check for themselves: that anything short of
// `supported` says why.
func TestCuratedEnginesSayWhyAPlatformIsNotSupported(t *testing.T) {
	engines := loadEngines(t)
	for id, engine := range engines {
		for _, support := range engine.Platforms {
			if support.Status == profile.Supported {
				t.Errorf("%s claims %s is `supported`; no build of these engines has been run here, "+
					"so the honest status is `unverified` with a note", id, support.Platform)
			}
			if strings.TrimSpace(support.Note) == "" {
				t.Errorf("%s says %s is %q and does not say why", id, support.Platform, support.Status)
			}
		}
	}

	// The prompt's own example, kept as a test: Ironwail must not claim macOS.
	for _, arch := range []string{"amd64", "arm64"} {
		mac := profile.Platform{OS: "darwin", Arch: arch}
		status, note := engines[Ironwail].SupportFor(mac)
		if status != profile.Unsupported {
			t.Errorf("Ironwail claims %s is %q; upstream ships no macOS build", mac, status)
		}
		if !strings.Contains(note, "macOS") {
			t.Errorf("Ironwail's %s note does not mention macOS: %q", mac, note)
		}
	}
}

// Every curated engine resolves every action it declares, on every platform it
// does not call unsupported, with the runtime values that action names. A
// profile that shipped and did not resolve would fail at the point somebody
// pressed the button.
func TestEveryCuratedEngineActionResolvesOnEveryDeclaredPlatform(t *testing.T) {
	base := t.TempDir()
	runtime := map[string]string{
		"map_name":     "e1m1",
		"mod_name":     "mymod",
		"package_name": "ad_sepulcher",
		"server_host":  "quake.example.org",
		"server_port":  "26000",
	}
	for id, engine := range loadEngines(t) {
		for _, support := range engine.Platforms {
			if support.Status == profile.Unsupported {
				continue
			}
			for _, action := range engine.Actions {
				request := profile.Request{
					Platform: support.Platform,
					Roots: map[string]string{
						profile.RootGame:        filepath.Join(base, "games", "quake"),
						profile.RootContent:     filepath.Join(base, "projects", "mymap"),
						profile.RootToolInstall: filepath.Join(base, "engines"),
					},
					Runtime: runtime,
				}
				invocation, err := profile.Resolve(engine, action.ID, request)
				if err != nil {
					t.Errorf("%s/%s on %s: %v", id, action.ID, support.Platform, err)
					continue
				}
				if invocation.SessionRole == "" {
					t.Errorf("%s/%s resolved with no session role", id, action.ID)
				}
				if invocation.Command.WorkingDir != filepath.Join(base, "games", "quake") {
					t.Errorf("%s/%s runs in %q, not the game root", id, action.ID, invocation.Command.WorkingDir)
				}
			}
		}
	}
}

// The generic profile is the fallback, so it must send nothing an engine could
// fail to recognise: the four switches id Software's own Quake documented, and
// no more.
func TestTheGenericProfileSendsOnlyTheUniversalSwitches(t *testing.T) {
	engine := loadEngines(t)[Q1Generic]
	allowed := map[string]bool{"-basedir": true, "-game": true, "+map": true, "+connect": true, "+maxplayers": true}
	for _, action := range engine.Actions {
		for _, arg := range action.Args {
			if strings.HasPrefix(arg.Value, "{") {
				continue // a value, not a switch
			}
			if !allowed[arg.Value] {
				t.Errorf("the generic profile passes %q in %q; a profile for an unknown engine "+
					"may only send what every id-derived engine documents", arg.Value, action.ID)
			}
		}
	}
}

// loadEngines returns every built-in engine profile by id.
func loadEngines(t *testing.T) map[string]*profile.EngineProfile {
	t.Helper()
	entries, err := Load()
	if err != nil {
		t.Fatalf("%v", err)
	}
	out := map[string]*profile.EngineProfile{}
	for _, entry := range entries {
		if engine, isEngine := entry.Profile.(*profile.EngineProfile); isEngine {
			out[entry.Profile.Metadata().ID] = engine
		}
	}
	if len(out) == 0 {
		t.Fatal("this build ships no engine profile")
	}
	return out
}

func contains(list []string, want string) bool {
	for _, value := range list {
		if value == want {
			return true
		}
	}
	return false
}

// The curated engines' play_map resolves to the argv the profile describes,
// with runtime values substituted and nothing else.
func TestEngineActionsResolveWithRuntimeValues(t *testing.T) {
	entry, err := Find(QuakeSpasm)
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
		Runtime: map[string]string{"map_name": "e1m1", "mod_name": "mymod"},
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
	if !strings.Contains(joined, "-game mymod") {
		t.Errorf("the game directory did not reach argv: %v", invocation.Command.Args)
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
