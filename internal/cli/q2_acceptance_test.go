package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/enginefixture"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/maturity"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/pack"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

// `AUP/AUCOM 215`'s acceptance, as one journey:
//
//	a synthetic Quake II map -> an exact AUB revision -> the experimental
//	compile -> a Quake II PAK -> a launch on the Yamagi command line
//
// Everything the Companion contributes is the real thing: the built-in
// experimental toolchain document, the built-in `auto-pigeon.q2.normal`
// pipeline, the real build runner, the real packager, the real executor and the
// built-in Yamagi engine profile. What is a fixture is the two things that
// cannot be here — ericw-tools 2.x, and a copy of Quake II — and both of them
// are fixtures that reproduce MEASURED behaviour rather than fixtures that
// simply succeed. See q2fixture_test.go.

// syntheticQ2Map is a Quake II map source with nothing of anybody else's in it:
// two rooms, one corridor, one `func_areaportal`, and two textures under a
// namespace that is ours. It is the shape that was compiled with 2.0.0-alpha7
// while this was written.
const syntheticQ2Map = `{
"classname" "worldspawn"
"message" "Auto-Pigeon Companion Q2 fixture"
{
( -16 272 0 ) ( 272 272 0 ) ( 272 -16 0 ) aucom/wall 0 0 0 1 1 0 0 0
( -16 -16 -16 ) ( 272 -16 -16 ) ( 272 272 -16 ) aucom/wall 0 0 0 1 1 0 0 0
( 272 -16 0 ) ( 272 272 0 ) ( 272 272 -16 ) aucom/wall 0 0 0 1 1 0 0 0
( -16 272 0 ) ( -16 -16 0 ) ( -16 -16 -16 ) aucom/wall 0 0 0 1 1 0 0 0
( 272 272 0 ) ( -16 272 0 ) ( -16 272 -16 ) aucom/wall 0 0 0 1 1 0 0 0
( -16 -16 0 ) ( 272 -16 0 ) ( 272 -16 -16 ) aucom/wall 0 0 0 1 1 0 0 0
}
}
{
"classname" "func_areaportal"
{
( 268 160 128 ) ( 272 160 128 ) ( 272 96 128 ) aucom/trigger 0 0 0 1 1 0 0 0
( 268 96 0 ) ( 272 96 0 ) ( 272 160 0 ) aucom/trigger 0 0 0 1 1 0 0 0
( 272 96 128 ) ( 272 160 128 ) ( 272 160 0 ) aucom/trigger 0 0 0 1 1 0 0 0
( 268 160 128 ) ( 268 96 128 ) ( 268 96 0 ) aucom/trigger 0 0 0 1 1 0 0 0
( 272 160 128 ) ( 268 160 128 ) ( 268 160 0 ) aucom/trigger 0 0 0 1 1 0 0 0
( 268 96 128 ) ( 272 96 128 ) ( 272 96 0 ) aucom/trigger 0 0 0 1 1 0 0 0
}
}
{
"classname" "info_player_start"
"origin" "128 128 32"
}
`

// gameDataRoot writes the base game data the compile needs: two textures under
// a namespace that is ours, and nothing of id Software's.
//
// This is the whole of what "never distribute commercial Q2 assets" costs a
// test. In Quake II a face's contents and surface flags are read from the
// `.wal` the map names, so the compile genuinely needs a texture root — and a
// texture root can be built out of two files nobody else owns.
func gameDataRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	textures := filepath.Join(root, "textures", "aucom")
	if err := os.MkdirAll(textures, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wall.wal", "trigger.wal"} {
		if err := os.WriteFile(filepath.Join(textures, name), synthesizedWAL(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// synthesizedWAL is a Quake II texture written here, from nothing.
//
// The format is a 100-byte header — name, size, four mip offsets, the next
// texture in the animation chain, and the flags/contents/value the compiler
// reads — followed by four mip levels of 8-bit palette indices. Writing one is
// four lines of arithmetic, and it is what lets a Quake II compile be tested
// without a copy of the game.
func synthesizedWAL(name string) []byte {
	const width, height = 8, 8
	header := make([]byte, 100)
	copy(header, strings.TrimSuffix(name, ".wal"))
	putUint32(header[32:], width)
	putUint32(header[36:], height)
	offset := uint32(100)
	body := []byte{}
	for level := 0; level < 4; level++ {
		putUint32(header[40+level*4:], offset)
		size := (width >> level) * (height >> level)
		if size < 1 {
			size = 1
		}
		body = append(body, make([]byte, size)...)
		offset += uint32(size)
	}
	return append(header, body...)
}

func putUint32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// bindTool records where this machine keeps a tool profile's programs and which
// directories it may reach.
//
// Written through the binding package rather than through a command, because
// there is no `companion tool bind`: `engine bind` is engine-only and a tool is
// bound by `acquire resolve --bind` from a document on disk, which a built-in
// document is not. The file it writes is the real one, read back by the real
// loader — see internal/web's harness, which binds a tool the same way.
func bindTool(t *testing.T, env *Env, profileID string, executables, roots map[string]string) {
	t.Helper()
	dir := filepath.Dir(env.ConfigPath)
	path := filepath.Join(dir, "bindings.json")
	set, err := binding.LoadFile(path)
	if err != nil && !strings.Contains(err.Error(), "no binding file") {
		t.Fatal(err)
	}
	entry, err := job.NewCatalog(filepath.Join(dir, "profiles")).Lookup(profileID)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := set.Find(profileID)
	local.ProfileID = profileID
	local.ProfileVersion = entry.Profile.Metadata().Version
	local.ProfileDigest = entry.Digest
	local.Trust = entry.Trust
	local.Acquisition = "user_path"
	local.Executables = executables
	local.Roots = roots
	if err := set.Put(local); err != nil {
		t.Fatal(err)
	}
	if err := binding.SaveFile(path, set); err != nil {
		t.Fatal(err)
	}
}

func TestASyntheticQuake2MapTravelsTheWholeExperimentalPath(t *testing.T) {
	// 1. The editor's export, as an exact AUB revision. The backend serves a
	//    `.map`, which is what an export produces, and the build reads it by
	//    revision id rather than by path.
	backend := fakeCompanionAUBServing(t, "level.map", "text/plain", syntheticQ2Map)
	env, _, _ := signedInEnv(t, backend.URL)

	const assetRef = "aub:map/map0000000000001@rev0000000000002#level.map"

	// 2. The experimental toolchain, pointed at the three programs. `tool_root`
	//    is deliberately not used: the three links are what the profile's
	//    `qbsp`, `vis` and `light` resolve to, exactly as three files in an
	//    unpacked ericw-tools 2.x folder would be.
	self := mustExecutable(t)
	toolDir := t.TempDir()
	exeSuffix := ""
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
	executables := map[string]string{}
	for _, name := range q2ToolNames {
		path, err := linkAsTool(self, toolDir, name, exeSuffix)
		if err != nil {
			t.Fatalf("standing in for %s: %v", name, err)
		}
		executables[name] = path
	}
	base := gameDataRoot(t)
	bindTool(t, env, builtin.EricwQ2, executables, map[string]string{
		// The three bindings `AUP/AUCOM 215` asks for separately: the programs
		// above, the base game data, and the mod. Here the last two are the
		// same directory, which is what upstream's own auto-detection does for
		// a map compiled inside `baseq2/maps`.
		profile.RootGame:    base,
		profile.RootContent: base,
	})

	// 3. The experimental compile, through the built-in Quake II pipeline.
	env, stdout, stderr := testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"build", "run", "--json",
		"--pipeline", builtin.Q2Normal,
		"--input", "source_map=" + assetRef,
		"--label", "the Quake II journey"}); code != 0 {
		t.Fatalf("build run exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var manifest struct {
		BuildID      string `json:"build_id"`
		State        string `json:"state"`
		Error        string `json:"error"`
		EngineFamily string `json:"engine_family"`
		Inputs       []struct {
			Source struct {
				RevisionID  string `json:"revision_id"`
				Refetchable bool   `json:"refetchable"`
			} `json:"source"`
		} `json:"inputs"`
		Steps []struct {
			ID          string                        `json:"id"`
			State       string                        `json:"state"`
			Outputs     []struct{ Name, Path string } `json:"outputs"`
			Diagnostics []struct {
				RuleID   string `json:"rule_id"`
				Severity string `json:"severity"`
			} `json:"diagnostics"`
		} `json:"steps"`
		Outputs []struct{ Name, Path string } `json:"outputs"`
	}
	if err := json.Unmarshal(manifestJSON(t, stdout.String()), &manifest); err != nil {
		t.Fatalf("the build manifest is not JSON: %v\n%s", err, stdout)
	}
	if manifest.State != "succeeded" {
		t.Fatalf("the build %s: %s\n%s", manifest.State, manifest.Error, stdout)
	}
	if manifest.EngineFamily != "quake2" {
		t.Errorf("the manifest records the family %q", manifest.EngineFamily)
	}
	// The exact revision, recorded against the input, never the word `current`.
	if len(manifest.Inputs) == 0 || manifest.Inputs[0].Source.RevisionID != "rev0000000000002" {
		t.Errorf("the build did not record the exact revision it read: %+v", manifest.Inputs)
	}
	if len(manifest.Steps) != 3 {
		t.Fatalf("the build ran %d steps", len(manifest.Steps))
	}
	for _, step := range manifest.Steps {
		if step.State != "succeeded" {
			t.Errorf("step %s %s", step.ID, step.State)
		}
	}

	// The three measured Quake II facts, each visible in the manifest:
	//
	//   - the compile produced the extended texinfo document;
	//   - the lighting step read it back, which it could only do because the
	//     pipeline staged it beside the BSP;
	//   - and there is no `.lit`, because Quake II has none.
	compile := manifest.Steps[0]
	produced := map[string]bool{}
	for _, out := range compile.Outputs {
		produced[out.Name] = true
	}
	if !produced["texinfo"] {
		t.Error("the compile produced no texinfo document; light reads one back")
	}
	for _, out := range manifest.Outputs {
		if out.Name == "lit" {
			t.Error("the Quake II build produced a .lit; Quake II lightmaps live inside the BSP")
		}
	}
	// And the lighting step read the texinfo document back, which it could only
	// do because the pipeline staged it beside the BSP. This is the measured
	// fact the Quake 1 pipeline has no counterpart for, and the one a run
	// without it would get wrong silently.
	if !strings.Contains(stdout.String(), "Loading extended texinfo flags") {
		t.Errorf("the lighting step did not read the extended texinfo flags:\n%s", stdout)
	}
	// 2.x writes its vis and light logs beside the BSP it was handed — inside
	// the staged input's own directory — so neither is a declared output. A
	// pipeline that claimed one would be claiming an artifact nobody can find.
	for _, out := range manifest.Outputs {
		if out.Name == "vis_log" || out.Name == "light_log" {
			t.Errorf("the pipeline declares %q; 2.x writes that log beside the staged input", out.Name)
		}
	}

	// 4. Package it as a Quake II PAK.
	out := filepath.Join(t.TempDir(), "level.pak")
	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"package", "create", "--json",
		"--target", "quake2-pak", "--build", manifest.BuildID, "--out", out,
		"--source-root", base}); code != 0 {
		t.Fatalf("package create exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("the package was not written: %v", err)
	}
	inspection, err := pack.Inspect(out, pack.FormatPAK, pack.Budget{})
	if err != nil {
		t.Fatalf("reading the package back: %v", err)
	}
	found := false
	for _, member := range inspection.Entries {
		if strings.HasSuffix(member.Path, ".bsp") {
			found = true
		}
	}
	if !found {
		t.Errorf("the Quake II PAK has no BSP in it: %+v", inspection.Entries)
	}

	// 5. Launch it on the Yamagi command line. The fixture engine records the
	//    argv; the built-in Yamagi profile decides what that argv should be, and
	//    the two are compared rather than one of them being restated here.
	// An installed Quake II is a `baseq2` directory with the game's data in it,
	// and the Companion's preflight refuses a root that is not one — an empty
	// `baseq2` is reported as "there is no game here" rather than accepted.
	//
	// So the test stands one up out of a file of its own. It is deliberately
	// not called `pak0.pak` and deliberately contains nothing of id Software's:
	// what is being tested is that the launch reaches the engine with the right
	// command line, and the Companion must never carry, copy or fabricate
	// commercial game data to test that.
	game := t.TempDir()
	if err := os.MkdirAll(filepath.Join(game, "baseq2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(game, "baseq2", "aucom-test-placeholder.txt"),
		[]byte("stands in for the tester's own copy of Quake II\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := t.TempDir()
	profiles := filepath.Join(filepath.Dir(env.ConfigPath), "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "fixture-q2.engine.json"),
		enginefixture.ProfileQ2JSON, 0o600); err != nil {
		t.Fatal(err)
	}
	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"engine", "bind", enginefixture.ProfileQ2ID,
		"--engine", self, "--game-root", game, "--content-root", content, "--approve"}); code != 0 {
		t.Fatalf("binding the Quake II fixture engine = %d: %s", code, stderr)
	}
	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"engine", "run", enginefixture.ProfileQ2ID,
		"--action", "play_map", "--map", "level", "--mod", "aucom"}); code != 0 {
		t.Fatalf("engine run exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	record, err := enginefixture.ReadRecord(filepath.Join(content, enginefixture.RecordName))
	if err != nil {
		t.Fatalf("%v", err)
	}

	yamagi, err := builtin.Find(builtin.YamagiQuake2)
	if err != nil {
		t.Fatalf("%v", err)
	}
	invocation, err := profile.Resolve(yamagi.Profile, profile.ActionPlayMap, profile.Request{
		Platform: profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Roots: map[string]string{
			profile.RootGame:        game,
			profile.RootContent:     content,
			profile.RootToolInstall: t.TempDir(),
		},
		Runtime: map[string]string{"map_name": "level", "mod_name": "aucom"},
	})
	if err != nil {
		t.Fatalf("resolving Yamagi's play_map: %v", err)
	}
	want := strings.Join(invocation.Command.Args, " ")
	if got := strings.Join(record.Argv, " "); got != want {
		t.Errorf("the launch did not use Yamagi's documented command line:\n  got  %q\n  want %q", got, want)
	}
	// And that command line is Yamagi's, not a Quake 1 profile's: `-datadir`,
	// which its own source calls the current spelling, and `+set game`, which
	// is how a Quake II mod is chosen.
	for _, fragment := range []string{"-datadir " + game, "+set game aucom", "+map level"} {
		if !strings.Contains(want, fragment) {
			t.Errorf("Yamagi's play_map does not contain %q: %s", fragment, want)
		}
	}
	if strings.Contains(want, "-basedir") {
		t.Errorf("Yamagi's play_map passes -basedir, which its own filesystem calls deprecated: %s", want)
	}
}

// manifestJSON picks the manifest out of `build run --json`'s output.
//
// A build streams the tools' own output as it goes, which is what makes a long
// compile watchable, so the manifest is the document at the end rather than the
// whole of stdout. This is a test detail and not a defect: `--json` is read by
// scripts that do the same thing.
func manifestJSON(t *testing.T, out string) []byte {
	t.Helper()
	start := strings.Index(out, `{`+"\n"+`  "schema_version"`)
	if start < 0 {
		t.Fatalf("no build manifest in the output:\n%s", out)
	}
	return []byte(out[start:])
}

// The other half of the acceptance: an unsupported Quake II route fails or
// warns explicitly, and nothing anywhere reports Quake II as plainly supported.
func TestQuake2FailsOrWarnsExplicitlyAndClaimsNoGenericSupport(t *testing.T) {
	// A leaking map, with the leak test on, is a refusal and not a BSP.
	backend := fakeCompanionAUBServing(t, "level.map", "text/plain",
		strings.Replace(syntheticQ2Map, `"message" "Auto-Pigeon Companion Q2 fixture"`,
			`"message" "aucom-leak"`, 1))
	env, _, _ := signedInEnv(t, backend.URL)

	self := mustExecutable(t)
	toolDir := t.TempDir()
	exeSuffix := ""
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
	executables := map[string]string{}
	for _, name := range q2ToolNames {
		path, linkErr := linkAsTool(self, toolDir, name, exeSuffix)
		if linkErr != nil {
			t.Fatal(linkErr)
		}
		executables[name] = path
	}
	base := gameDataRoot(t)
	bindTool(t, env, builtin.EricwQ2, executables,
		map[string]string{profile.RootGame: base, profile.RootContent: base})

	env, stdout, stderr := testEnvAt(t, env.ConfigPath)
	code := Run(env, []string{"build", "run", "--json", "--pipeline", builtin.Q2Normal,
		"--input", "source_map=aub:map/map0000000000001@rev0000000000002#level.map"})
	if code == 0 {
		t.Fatalf("a leaking Quake II map built successfully:\n%s", stdout)
	}
	if !strings.Contains(stdout.String(), "leaktest_abort") && !strings.Contains(stdout.String(), "leak") {
		t.Errorf("the failure does not say the map leaked:\n%s\n%s", stdout, stderr)
	}

	// No built-in Quake II document claims a platform is `supported` except the
	// toolchain on the one platform it was actually run on, and every one of
	// them is a work-in-progress family whatever it claims per platform.
	entries, err := builtin.Load()
	if err != nil {
		t.Fatalf("%v", err)
	}
	quake2 := 0
	for _, entry := range entries {
		family := documentFamily(entry.Profile)
		if family != "quake2" {
			continue
		}
		quake2++
		if !maturity.IsWorkInProgress(family) {
			t.Errorf("%s is for a family this build does not call work in progress", entry.File)
		}
		engine, isEngine := entry.Profile.(*profile.EngineProfile)
		if !isEngine {
			continue
		}
		for _, support := range engine.Platforms {
			if support.Status == profile.Supported {
				t.Errorf("%s claims %s is `supported`; no Quake II engine has been run here",
					entry.File, support.Platform)
			}
		}
	}
	if quake2 == 0 {
		t.Fatal("no built-in Quake II document was found, so this test checked nothing")
	}
}
