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
	// One toolchain per game family, and a pipeline resolves against its own.
	// Resolving every pipeline against every tool would hide the property this
	// build depends on: the Q1 toolchain provides `q1.*`, the Q2 one `q2.*` and
	// Q3Map2 `q3.*`, so a Quake 1 pipeline can never be satisfied by the
	// experimental Quake II compiler, a Quake II pipeline can never silently
	// fall back to the stable Quake 1 one, and neither can reach Q3Map2.
	toolchains := map[string]*profile.ToolProfile{}
	for family, id := range map[string]string{"quake1": EricwQ1, "quake2": EricwQ2, "quake3": Q3Map2} {
		entry, err := Find(id)
		if err != nil {
			t.Fatalf("%v", err)
		}
		tool, ok := entry.Profile.(*profile.ToolProfile)
		if !ok {
			t.Fatalf("%s is not a tool profile", id)
		}
		toolchains[family] = tool
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
		family := pipeline.GameProfile.EngineFamily
		tool, known := toolchains[family]
		if !known {
			t.Errorf("%s is for the family %q, which this build ships no toolchain for", e.File, family)
			continue
		}
		steps, err := pipeline.Resolve(installed{tool})
		if err != nil {
			t.Errorf("%s does not resolve against the %s toolchain:\n%v", e.File, family, err)
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
	if want := len(Q1Pipelines) + len(Q2Pipelines) + len(Q3Pipelines); seen != want {
		t.Errorf("this build ships %d pipelines; %d Quake 1, %d Quake II and %d Quake III are what it declares",
			seen, len(Q1Pipelines), len(Q2Pipelines), len(Q3Pipelines))
	}
}

// The other half of the same property, stated as its own failure: a pipeline
// offered nothing but another game's toolchain must refuse rather than resolve.
// A shared capability id — `bsp.compile` for all three — would have made this
// pass by accident and produced a Quake 1 BSP for a Quake III project.
//
// Six pairs rather than two, because the third toolchain is where a shared id
// would have been most tempting: Q3Map2 is one program with three stage
// switches, and "it is all the same compiler anyway" is exactly the reasoning
// this test exists to fail.
func TestAPipelineCannotResolveAgainstAnotherGamesToolchain(t *testing.T) {
	families := []struct {
		name      string
		toolchain string
		pipelines []string
	}{
		{"Quake 1", EricwQ1, Q1Pipelines},
		{"Quake II", EricwQ2, Q2Pipelines},
		{"Quake III", Q3Map2, Q3Pipelines},
	}
	for _, tool := range families {
		entry, err := Find(tool.toolchain)
		if err != nil {
			t.Fatalf("%v", err)
		}
		document := entry.Profile.(*profile.ToolProfile)
		for _, other := range families {
			if other.toolchain == tool.toolchain {
				continue
			}
			for _, id := range other.pipelines {
				pipelineEntry, err := Find(id)
				if err != nil {
					t.Fatalf("%v", err)
				}
				if _, err := pipelineEntry.Profile.(*profile.PipelineProfile).Resolve(installed{document}); err == nil {
					t.Errorf("the %s pipeline %s resolved against the %s toolchain", other.name, id, tool.name)
				}
			}
		}
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

// The two Quake II pipelines differ in the same way, and in one way the Quake 1
// ones cannot: neither may set `lit`. `light` accepts the switch on a Quake II
// BSP, prints a line and writes nothing, because Quake II lightmaps are already
// coloured — so an option that looks like it does something is exactly the kind
// of thing that reaches a user as "the coloured lighting file is missing".
func TestTheTwoQuake2PipelinesDifferInWhatTheyRun(t *testing.T) {
	want := map[string]struct{ visFast, sampling, threads string }{
		"auto-pigeon.q2.fast-preview": {"true", "none", "1"},
		"auto-pigeon.q2.normal":       {"false", "none", "4"},
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
		if got := options["light"]["threads"]; got != expected.threads {
			t.Errorf("%s: light threads is %q, want %q", id, got, expected.threads)
		}
		if _, set := options["light"]["lit"]; set {
			t.Errorf("%s sets `lit` on the lighting step; Quake II has no .lit file", id)
		}
		// The texinfo document qbsp writes must be wired to light. Without it
		// the run succeeds with the wrong surface flags, which is the failure
		// nobody notices.
		wired := false
		for _, step := range pipeline.Steps {
			if step.ID != "light" {
				continue
			}
			for _, in := range step.Inputs {
				if in.Name == "texinfo" && in.From == "compile.texinfo" {
					wired = true
				}
			}
		}
		if !wired {
			t.Errorf("%s does not wire compile.texinfo into the lighting step", id)
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

	for _, id := range Q2Engines {
		if _, shipped := engines[id]; !shipped {
			t.Errorf("%s is named as a curated Quake II engine and does not ship", id)
		}
	}
	// FTEQW's Quake II profile declares one action, and the reason is upstream's
	// own: Quake II's game logic is server-side and FTEQW does not ship it, so
	// every action that starts a local server would be a button that fails on a
	// machine that looks correctly configured. A client does not need it.
	fteqwQ2 := engines[FTEQWQ2]
	if len(fteqwQ2.Actions) != 1 {
		t.Errorf("FTEQW's Quake II profile declares %d actions; it declares join_server and nothing else",
			len(fteqwQ2.Actions))
	}
	if _, offers := fteqwQ2.ActionByID(profile.ActionJoinServer); !offers {
		t.Error("FTEQW's Quake II profile does not declare join_server, which is the one action it has")
	}
	for _, starts := range []string{profile.ActionPlayMap, profile.ActionPlayPackage, profile.ActionHostListen, profile.ActionHostDedicated} {
		if _, offers := fteqwQ2.ActionByID(starts); offers {
			t.Errorf("FTEQW's Quake II profile declares %q, which starts a local Quake II server and "+
				"therefore needs gamecode FTEQW does not ship and Auto-Pigeon must not distribute", starts)
		}
	}
	if _, offers := engines[Q2Generic].ActionByID(profile.ActionHostDedicated); offers {
		t.Error("the generic Quake II profile declares host_dedicated; whether a build has a dedicated " +
			"server compiled in is exactly what a generic profile cannot know")
	}
	if _, offers := engines[YamagiQuake2].ActionByID(profile.ActionHostDedicated); !offers {
		t.Error("Yamagi does not declare host_dedicated; it ships q2ded, which is why it has two executables")
	}

	for _, id := range Q3Engines {
		if _, shipped := engines[id]; !shipped {
			t.Errorf("%s is named as a curated Quake III engine and does not ship", id)
		}
	}
	// ioquake3 declares all five, and that is the difference from FTEQW's
	// Quake II case rather than an inconsistency with it: upstream's own
	// download carries `baseq3/vm/qagame.qvm` beside the engine, so a local
	// server needs nothing this profile cannot see.
	ioq3 := engines[IoQuake3]
	for _, wanted := range profile.EngineActions {
		if _, offers := ioq3.ActionByID(wanted); !offers {
			t.Errorf("ioquake3 does not declare %q; it ships its own game logic and its own dedicated "+
				"server, so there is nothing it cannot do", wanted)
		}
	}
	if _, offers := engines[Q3Generic].ActionByID(profile.ActionHostDedicated); offers {
		t.Error("the generic Quake III profile declares host_dedicated; the name of a dedicated binary " +
			"is each project's own invention and a generic profile cannot know it")
	}
}

// The generic Quake III profile is the fallback for an engine nobody here has
// seen, so it may send only id Software's own Quake III vocabulary. ioquake3's
// server cvars are the tempting addition and the wrong one: a fallback that
// sent `sv_pure` would be a fallback that worked on ioquake3.
func TestTheGenericQuake3ProfileSendsOnlyTheVanillaVocabulary(t *testing.T) {
	engine := loadEngines(t)[Q3Generic]
	allowed := map[string]bool{
		"+set": true, "fs_basepath": true, "fs_game": true, "sv_maxclients": true,
		"+map": true, "+connect": true,
	}
	for _, action := range engine.Actions {
		for _, arg := range action.Args {
			if strings.HasPrefix(arg.Value, "{") {
				continue // a value, not a switch
			}
			if !allowed[arg.Value] {
				t.Errorf("the generic Quake III profile passes %q in %q; a profile for an unknown engine "+
					"may only send what id Software's own Quake III documented", arg.Value, action.ID)
			}
		}
	}
}

// Q3Map2 is the only toolchain here with no managed download, and that is a
// decision rather than an omission: upstream publishes it inside a bundle of
// the NetRadiant editor, in a container this program does not unpack, and
// `AUP/AUCOM 216` says not to install an editor to get at a compiler. A later
// change that adds a `managed_download` has to add a catalogue entry for a
// 40 MB editor first, and this is where it is asked to think about that.
func TestQ3Map2DeclaresNoManagedDownloadAndSaysWhy(t *testing.T) {
	entry, err := Find(Q3Map2)
	if err != nil {
		t.Fatalf("%v", err)
	}
	tool := entry.Profile.(*profile.ToolProfile)
	if len(tool.Acquisition) == 0 {
		t.Fatal("Q3Map2 declares no way of being acquired at all")
	}
	for _, option := range tool.Acquisition {
		if option.Mode == profile.AcquireManagedDownload {
			t.Errorf("Q3Map2 declares a managed download of %q; there is no artifact to pin that is not "+
				"a map editor", option.CatalogPackage)
		}
	}
	first := tool.Acquisition[0]
	if first.Mode != profile.AcquireUserPath {
		t.Errorf("Q3Map2's first acquisition route is %q; the one that works is user_path", first.Mode)
	}
	if !strings.Contains(first.Note, "editor") {
		t.Error("Q3Map2's first acquisition route does not say why there is no download; " +
			"`unavailable` with no reason is the thing a user cannot act on")
	}
}

// The two Quake III pipelines differ in what they run, and both wire the two
// companion files the lighting stage refuses to start without. Measured: with
// no `<stem>.srf` or no `<stem>.map` beside the BSP, Q3Map2's light stage exits
// 1 saying a script file was not found — and the file it names is one nobody
// thought they had asked for.
func TestTheTwoQuake3PipelinesDifferAndWireTheLightingCompanions(t *testing.T) {
	want := map[string]struct{ visFast, lightFast, threads string }{
		"auto-pigeon.q3.fast-preview": {"true", "true", "1"},
		"auto-pigeon.q3.normal":       {"false", "false", "4"},
	}
	for id, expected := range want {
		entry, err := Find(id)
		if err != nil {
			t.Fatalf("%v", err)
		}
		pipeline := entry.Profile.(*profile.PipelineProfile)
		options := map[string]map[string]string{}
		inputs := map[string]map[string]string{}
		for _, step := range pipeline.Steps {
			options[step.ID] = step.Options
			wired := map[string]string{}
			for _, in := range step.Inputs {
				wired[in.Name] = in.From
			}
			inputs[step.ID] = wired
		}
		if got := options["vis"]["fast"]; got != expected.visFast {
			t.Errorf("%s: vis fast is %q, want %q", id, got, expected.visFast)
		}
		if got := options["light"]["fast"]; got != expected.lightFast {
			t.Errorf("%s: light fast is %q, want %q", id, got, expected.lightFast)
		}
		if got := options["light"]["threads"]; got != expected.threads {
			t.Errorf("%s: light threads is %q, want %q", id, got, expected.threads)
		}
		if got := inputs["light"]["srf"]; got != "compile.srf" {
			t.Errorf("%s wires the lighting step's surface file from %q, want compile.srf", id, got)
		}
		if got := inputs["light"]["source_map"]; got != "pipeline.source_map" {
			t.Errorf("%s wires the lighting step's map source from %q, want pipeline.source_map", id, got)
		}
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

// One engine takes one command line, and a platform does not change it. What a
// platform does change is the file name of the program, and that is the whole
// of the difference: `.exe` on Windows and nothing anywhere else.
func TestAnEnginesArgvIsTheSameOnEveryPlatformAndOnlyTheFileNameDiffers(t *testing.T) {
	base := t.TempDir()
	tools := filepath.Join(base, "engines")
	runtimeValues := map[string]string{
		"map_name": "e1m1", "mod_name": "mymod", "package_name": "ad_sepulcher",
		"server_host": "quake.example.org", "server_port": "26000",
	}
	for id, engine := range loadEngines(t) {
		for _, action := range engine.Actions {
			var first []string
			var firstPlatform profile.Platform
			for _, support := range engine.Platforms {
				if support.Status == profile.Unsupported {
					continue
				}
				invocation, err := profile.Resolve(engine, action.ID, profile.Request{
					Platform: support.Platform,
					Roots: map[string]string{
						profile.RootGame:        filepath.Join(base, "quake"),
						profile.RootContent:     filepath.Join(base, "project"),
						profile.RootToolInstall: tools,
					},
					Runtime: runtimeValues,
				})
				if err != nil {
					t.Errorf("%s/%s on %s: %v", id, action.ID, support.Platform, err)
					continue
				}
				if first == nil {
					first, firstPlatform = invocation.Command.Args, support.Platform
				} else if strings.Join(invocation.Command.Args, "\x00") != strings.Join(first, "\x00") {
					t.Errorf("%s/%s differs between %s and %s:\n  %v\n  %v",
						id, action.ID, firstPlatform, support.Platform, first, invocation.Command.Args)
				}
				wantSuffix := ""
				if support.Platform.OS == "windows" {
					wantSuffix = ".exe"
				}
				if !strings.HasSuffix(invocation.Command.Executable, wantSuffix) {
					t.Errorf("%s on %s resolves the program to %q, which does not end in %q",
						id, support.Platform, invocation.Command.Executable, wantSuffix)
				}
				if !strings.HasPrefix(invocation.Command.Executable, tools) {
					t.Errorf("%s on %s resolves the program to %q, outside the engine's own folder",
						id, support.Platform, invocation.Command.Executable)
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
			if strings.HasPrefix(arg.Value, "{") || arg.Value == "." {
				continue // a value, not a switch ("." is the working directory, the game root)
			}
			if !allowed[arg.Value] {
				t.Errorf("the generic profile passes %q in %q; a profile for an unknown engine "+
					"may only send what every id-derived engine documents", arg.Value, action.ID)
			}
		}
	}
}

// The generic Quake II profile is the same idea for a different game, and the
// vocabulary is genuinely different: Quake II's paths and player limit are
// cvars set with `+set`, and `-datadir` — which Yamagi's own profile uses — is
// Yamagi's invention. A generic profile that sent it would be a fallback that
// only worked on the engine it was a fallback for.
func TestTheGenericQuake2ProfileSendsOnlyTheVanillaVocabulary(t *testing.T) {
	engine := loadEngines(t)[Q2Generic]
	allowed := map[string]bool{"+set": true, "basedir": true, "game": true, "maxclients": true, "+map": true, "+connect": true}
	for _, action := range engine.Actions {
		for _, arg := range action.Args {
			if strings.HasPrefix(arg.Value, "{") {
				continue // a value, not a switch
			}
			if !allowed[arg.Value] {
				t.Errorf("the generic Quake II profile passes %q in %q; a profile for an unknown engine "+
					"may only send what id Software's own Quake II documented", arg.Value, action.ID)
			}
		}
	}
	// And the Yamagi profile must not be written in the vanilla spelling, which
	// its own source calls deprecated.
	for _, action := range loadEngines(t)[YamagiQuake2].Actions {
		for _, arg := range action.Args {
			if arg.Value == "basedir" || arg.Value == "-basedir" {
				t.Errorf("the Yamagi profile passes %q in %q; Yamagi's filesystem prints "+
					"`+set basedir is deprecated, use -datadir instead`", arg.Value, action.ID)
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

// A Quake 1 join names its base directory as ".", the working directory the job
// already sets to the game root, and puts no folder path on the command line at
// all. vkQuake 1.36.0 keeps only the first 255 characters of its whole command
// line, its own executable included — and an AppImage run extracted beside a job
// has an executable path of about 140 characters. 244F's native run measured the
// cost of an absolute stage path: `+connect` was cut off and the client played
// its demo loop instead of joining.
func TestAQuake1JoinPutsNoFolderOnTheCommandLine(t *testing.T) {
	base := t.TempDir()
	game := filepath.Join(base, "a-deliberately-long-directory-name-standing-in-for-a-join-content-stage", "b")
	content := filepath.Join(game, "ap-0123456789ab")
	engines := loadEngines(t)
	for _, id := range Q1Engines {
		engine := engines[id]
		for _, support := range engine.Platforms {
			if support.Status == profile.Unsupported {
				continue
			}
			invocation, err := profile.Resolve(engine, profile.ActionJoinServer, profile.Request{
				Platform: support.Platform,
				Roots: map[string]string{
					profile.RootGame: game, profile.RootContent: content,
					profile.RootToolInstall: filepath.Join(base, "engines"),
				},
				Runtime: map[string]string{"mod_name": "ap-0123456789ab", "server_host": "203.0.113.4", "server_port": "26000"},
			})
			if err != nil {
				t.Errorf("%s on %s: %v", id, support.Platform, err)
				continue
			}
			if invocation.Command.WorkingDir != game {
				t.Errorf("%s on %s runs in %q, not the game root", id, support.Platform, invocation.Command.WorkingDir)
			}
			line := strings.Join(invocation.Command.Args, " ")
			if strings.Contains(line, base) || !strings.Contains(line, "-basedir . ") {
				t.Errorf("%s on %s joins with %q; the base directory must be the working directory", id, support.Platform, line)
			}
		}
	}
}
