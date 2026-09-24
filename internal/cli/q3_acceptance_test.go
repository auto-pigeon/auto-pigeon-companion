package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// `AUP/AUCOM 216`'s acceptance, as one journey:
//
//	a synthetic Quake III map -> the experimental compile -> a dependency
//	review -> a PK3 -> a launch on ioquake3's documented command line
//
// Everything the Companion contributes is the real thing: the built-in Q3Map2
// document, the built-in `auto-pigeon.q3.normal` pipeline, the real build
// runner, the real packager, the real dependency scan, the real executor and
// the built-in ioquake3 profile. What is a fixture is the two things that
// cannot be here — Q3Map2 itself, and a copy of Quake III — and the first of
// those reproduces MEASURED behaviour rather than simply succeeding. See
// q3fixture_test.go.

// syntheticQ3Map is a Quake III map source with nothing of anybody else's in
// it: two rooms, a doorway, a patch, and shader names under a namespace that is
// ours.
//
// The face names carry no `textures/` prefix, because that is how a Quake III
// map is written and Q3Map2 adds it — measured, and the reason the scan
// normalizes the same way.
const syntheticQ3Map = `// Auto-Pigeon Companion Q3 acceptance fixture
{
"classname" "worldspawn"
"message" "Auto-Pigeon Companion Q3 fixture"
{
( -16 528 0 ) ( 528 528 0 ) ( 528 -16 0 ) aucom/floor 0 0 0 0.500000 0.500000 0 0 0
( -16 -16 -16 ) ( 528 -16 -16 ) ( 528 528 -16 ) aucom/floor 0 0 0 0.500000 0.500000 0 0 0
( 528 -16 0 ) ( 528 528 0 ) ( 528 528 -16 ) aucom/floor 0 0 0 0.500000 0.500000 0 0 0
( -16 528 0 ) ( -16 -16 0 ) ( -16 -16 -16 ) aucom/floor 0 0 0 0.500000 0.500000 0 0 0
( 528 528 0 ) ( -16 528 0 ) ( -16 528 -16 ) aucom/floor 0 0 0 0.500000 0.500000 0 0 0
( -16 -16 0 ) ( 528 -16 0 ) ( 528 -16 -16 ) aucom/floor 0 0 0 0.500000 0.500000 0 0 0
}
{
( -16 528 272 ) ( 528 528 272 ) ( 528 -16 272 ) aucom/wall 0 0 0 0.500000 0.500000 0 0 0
( -16 -16 256 ) ( 528 -16 256 ) ( 528 528 256 ) aucom/wall 0 0 0 0.500000 0.500000 0 0 0
( 528 -16 272 ) ( 528 528 272 ) ( 528 528 256 ) aucom/wall 0 0 0 0.500000 0.500000 0 0 0
( -16 528 272 ) ( -16 -16 272 ) ( -16 -16 256 ) aucom/wall 0 0 0 0.500000 0.500000 0 0 0
( 528 528 272 ) ( -16 528 272 ) ( -16 528 256 ) aucom/wall 0 0 0 0.500000 0.500000 0 0 0
( -16 -16 272 ) ( 528 -16 272 ) ( 528 -16 256 ) aucom/wall 0 0 0 0.500000 0.500000 0 0 0
}
{
patchDef2
{
aucom/wall
( 3 3 0 0 0 )
(
( ( 64 64 8 0 0 ) ( 64 192 8 0 1 ) ( 64 320 8 0 2 ) )
( ( 192 64 40 1 0 ) ( 192 192 40 1 1 ) ( 192 320 40 1 2 ) )
( ( 320 64 8 2 0 ) ( 320 192 8 2 1 ) ( 320 320 8 2 2 ) )
)
}
}
}
{
"classname" "info_player_deathmatch"
"origin" "256 256 32"
}
`

// q3Content writes the content the compile reads and the package carries: two
// textures under a namespace that is ours, one shader script, and nothing of id
// Software's.
//
// The layout is a game directory — `<root>/baseq3/…` — because that is what
// `-fs_basepath` expects, and the directory the package is built out of is the
// `baseq3` inside it, because that is what a PK3's own root is.
func q3Content(t *testing.T) (root, vfs string) {
	t.Helper()
	root = t.TempDir()
	vfs = filepath.Join(root, "baseq3")
	textures := filepath.Join(vfs, "textures", "aucom")
	scripts := filepath.Join(vfs, "scripts")
	for _, dir := range []string{textures, scripts} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"wall.tga", "floor.tga"} {
		if err := os.WriteFile(filepath.Join(textures, name),
			[]byte("stands in for the author's own texture; no id Software content here\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(scripts, "aucom.shader"), []byte(`textures/aucom/wall
{
	qer_editorimage textures/aucom/wall.tga
	{
		map textures/aucom/wall.tga
	}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, vfs
}

// bindQ3Map2 points the built-in Q3Map2 document at the fixture and at the two
// content roots it needs.
func bindQ3Map2(t *testing.T, env *Env, gameRoot, contentRoot string) string {
	t.Helper()
	self := mustExecutable(t)
	exeSuffix := ""
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
	path, err := linkAsTool(self, t.TempDir(), q3ToolName, exeSuffix)
	if err != nil {
		t.Fatalf("standing in for q3map2: %v", err)
	}
	bindTool(t, env, builtin.Q3Map2, map[string]string{q3ToolName: path}, map[string]string{
		profile.RootGame:    gameRoot,
		profile.RootContent: contentRoot,
	})
	return self
}

func TestASyntheticQuake3MapTravelsTheWholeExperimentalPath(t *testing.T) {
	env, _, _ := testEnv(t)
	contentRoot, vfs := q3Content(t)
	self := bindQ3Map2(t, env, contentRoot, contentRoot)

	source := filepath.Join(t.TempDir(), "aucomdm1.map")
	if err := os.WriteFile(source, []byte(syntheticQ3Map), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. The experimental compile, through the built-in Quake III pipeline.
	env, stdout, stderr := testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"build", "run", "--json",
		"--pipeline", builtin.Q3Normal,
		"--input", "source_map=" + source,
		"--label", "the Quake III journey"}); code != 0 {
		t.Fatalf("build run exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var manifest struct {
		BuildID      string `json:"build_id"`
		State        string `json:"state"`
		Error        string `json:"error"`
		EngineFamily string `json:"engine_family"`
		Steps        []struct {
			ID      string                        `json:"id"`
			State   string                        `json:"state"`
			Outputs []struct{ Name, Path string } `json:"outputs"`
		} `json:"steps"`
		Outputs []struct{ Name, Path string } `json:"outputs"`
	}
	if err := json.Unmarshal(manifestJSON(t, stdout.String()), &manifest); err != nil {
		t.Fatalf("the build manifest is not JSON: %v\n%s", err, stdout)
	}
	if manifest.State != "succeeded" {
		t.Fatalf("the build %s: %s\n%s", manifest.State, manifest.Error, stdout)
	}
	if manifest.EngineFamily != "quake3" {
		t.Errorf("the manifest records the family %q", manifest.EngineFamily)
	}
	if len(manifest.Steps) != 3 {
		t.Fatalf("the build ran %d steps", len(manifest.Steps))
	}
	for _, step := range manifest.Steps {
		if step.State != "succeeded" {
			t.Errorf("step %s %s", step.ID, step.State)
		}
	}

	// The measured Quake III facts, each visible in what the build did:
	//
	//   - the compile produced the surface file as well as the portal file,
	//     both beside the input it was handed;
	//   - the lighting step opened BOTH the surface file and the map source,
	//     which it could only do because the pipeline staged them beside the
	//     BSP;
	//   - and there is no separate lighting file, because Quake III has none.
	produced := map[string]bool{}
	for _, out := range manifest.Steps[0].Outputs {
		produced[out.Name] = true
	}
	for _, wanted := range []string{"bsp", "prt", "srf"} {
		if !produced[wanted] {
			t.Errorf("the compile produced no %q; Q3Map2 writes it beside the input", wanted)
		}
	}
	out := stdout.String()
	if !strings.Contains(out, ".srf") || !strings.Contains(out, "entering") {
		t.Errorf("the lighting step did not open the surface file:\n%s", out)
	}
	if !strings.Contains(out, ".map\n") && !strings.Contains(out, ".map ") {
		t.Errorf("the lighting step did not open the map source:\n%s", out)
	}
	for _, output := range manifest.Outputs {
		if output.Name == "lit" {
			t.Error("the Quake III build produced a .lit; Quake III lightmaps live inside the BSP")
		}
	}

	// 2. The dependency review, before anything is packaged. With only the
	//    build's outputs selected, every texture the map uses is the author's
	//    own and outside the archive — which is the silently incomplete PK3
	//    this refuses to write.
	archive := filepath.Join(t.TempDir(), "aucomdm1.pk3")
	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	code := Run(env, []string{"package", "create",
		"--target", "quake3-pk3", "--build", manifest.BuildID, "--out", archive,
		"--map", source, "--source-root", vfs})
	if code == 0 {
		t.Fatalf("a package missing every texture was written:\n%s", stdout)
	}
	held := stderr.String()
	for _, wanted := range []string{"textures/aucom/wall", "scripts/aucom.shader", "--accept-missing"} {
		if !strings.Contains(held, wanted) {
			t.Errorf("the review does not mention %q:\n%s", wanted, held)
		}
	}
	if _, err := os.Stat(archive); err == nil {
		t.Error("the archive was written despite the review")
	}

	// 3. With the content selected too, the review passes and the PK3 is
	//    written — the same command, with the missing files added rather than
	//    with the check turned off.
	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"package", "create", "--json",
		"--target", "quake3-pk3", "--build", manifest.BuildID, "--out", archive,
		"--map", source, "--from", vfs, "--source-root", vfs}); code != 0 {
		t.Fatalf("package create exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	inspection, err := pack.Inspect(archive, pack.FormatPK3, pack.Budget{})
	if err != nil {
		t.Fatalf("reading the package back: %v", err)
	}
	members := map[string]bool{}
	for _, member := range inspection.Entries {
		members[member.Path] = true
	}
	for _, wanted := range []string{
		"maps/aucomdm1.bsp", "textures/aucom/wall.tga", "textures/aucom/floor.tga", "scripts/aucom.shader",
	} {
		if !members[wanted] {
			t.Errorf("the PK3 has no %s: %v", wanted, inspection.Entries)
		}
	}

	// 4. Launch it on ioquake3's documented command line. The fixture records
	//    the argv; the built-in ioquake3 profile decides what that argv should
	//    be, and the two are compared rather than one of them being restated
	//    here.
	//
	//    The game root is stood up out of a file of its own. It is deliberately
	//    not called `pak0.pk3` and contains nothing of id Software's: what is
	//    being tested is that the launch reaches the engine with the right
	//    command line, and the Companion must never carry, copy or fabricate
	//    commercial game data to test that.
	game := t.TempDir()
	if err := os.MkdirAll(filepath.Join(game, "baseq3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(game, "baseq3", "aucom-test-placeholder.txt"),
		[]byte("stands in for the tester's own copy of Quake III\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := t.TempDir()
	profiles := filepath.Join(filepath.Dir(env.ConfigPath), "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "fixture-q3.engine.json"),
		enginefixture.ProfileQ3JSON, 0o600); err != nil {
		t.Fatal(err)
	}
	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"engine", "bind", enginefixture.ProfileQ3ID,
		"--engine", self, "--game-root", game, "--content-root", content, "--approve"}); code != 0 {
		t.Fatalf("binding the Quake III fixture engine = %d: %s", code, stderr)
	}
	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"engine", "run", enginefixture.ProfileQ3ID,
		"--action", "play_map", "--map", "aucomdm1", "--mod", "aucom"}); code != 0 {
		t.Fatalf("engine run exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	record, err := enginefixture.ReadRecord(filepath.Join(content, enginefixture.RecordName))
	if err != nil {
		t.Fatalf("%v", err)
	}

	ioq3, err := builtin.Find(builtin.IoQuake3)
	if err != nil {
		t.Fatalf("%v", err)
	}
	invocation, err := profile.Resolve(ioq3.Profile, profile.ActionPlayMap, profile.Request{
		Platform: profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Roots: map[string]string{
			profile.RootGame:        game,
			profile.RootContent:     content,
			profile.RootToolInstall: t.TempDir(),
		},
		Runtime: map[string]string{"map_name": "aucomdm1", "mod_name": "aucom"},
	})
	if err != nil {
		t.Fatalf("resolving ioquake3's play_map: %v", err)
	}
	want := strings.Join(invocation.Command.Args, " ")
	if got := strings.Join(record.Argv, " "); got != want {
		t.Errorf("the launch did not use ioquake3's documented command line:\n  got  %q\n  want %q", got, want)
	}
	// And that command line is Quake III's vocabulary, not Quake II's: cvars
	// set with `+set`, a game directory named rather than pointed at.
	for _, fragment := range []string{"+set fs_basepath " + game, "+set fs_game aucom", "+map aucomdm1"} {
		if !strings.Contains(want, fragment) {
			t.Errorf("ioquake3's play_map does not contain %q: %s", fragment, want)
		}
	}
	for _, absent := range []string{"-basedir", "-datadir", "+set game "} {
		if strings.Contains(want, absent) {
			t.Errorf("ioquake3's play_map passes %q, which is not Quake III's vocabulary: %s", absent, want)
		}
	}
}

// `AUP/AUCOM 216` asks for play, join and host to be *validated with fixtures*,
// and play is validated by the journey above. These are the other two, and the
// reason they get their own test is that they are the actions whose command
// lines nobody here can check against a running engine: the fixture proves the
// Companion builds the argv the built-in profile describes, and the built-in
// profile is what carries ioquake3's own documentation.
func TestQuake3JoinAndHostReachTheEngineWithIoquake3sDocumentedCommandLine(t *testing.T) {
	env, _, _ := testEnv(t)
	self := mustExecutable(t)
	game := t.TempDir()
	if err := os.MkdirAll(filepath.Join(game, "baseq3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(game, "baseq3", "aucom-test-placeholder.txt"),
		[]byte("stands in for the tester's own copy of Quake III\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(filepath.Dir(env.ConfigPath), "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "fixture-q3.engine.json"),
		enginefixture.ProfileQ3JSON, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		action  string
		args    []string
		runtime map[string]string
		wants   []string
	}{
		{
			action:  profile.ActionJoinServer,
			args:    []string{"--server", "198.51.100.7", "--port", "27960"},
			runtime: map[string]string{"server_host": "198.51.100.7", "server_port": "27960"},
			// No `fs_game`: a Quake III server tells the client which game
			// directory it is running, and a client that insisted would be a
			// client that could not join a mod it had not been told about.
			wants: []string{"+connect 198.51.100.7:27960"},
		},
		{
			action:  profile.ActionHostDedicated,
			args:    []string{"--map", "aucomdm1", "--mod", "aucom"},
			runtime: map[string]string{"map_name": "aucomdm1", "mod_name": "aucom"},
			// Upstream's own two values for `dedicated`, and the two cvars that
			// decide whether somebody without your PK3 can play on it.
			wants: []string{"+set dedicated 1", "+set sv_pure 1", "+set sv_allowDownload 1", "+map aucomdm1"},
		},
	} {
		t.Run(c.action, func(t *testing.T) {
			content := t.TempDir()
			env, _, stderr := testEnvAt(t, env.ConfigPath)
			if code := Run(env, []string{"engine", "bind", enginefixture.ProfileQ3ID,
				"--engine", self, "--game-root", game, "--content-root", content, "--approve"}); code != 0 {
				t.Fatalf("binding the fixture engine = %d: %s", code, stderr)
			}
			env, stdout, stderr := testEnvAt(t, env.ConfigPath)
			run := append([]string{"engine", "run", enginefixture.ProfileQ3ID, "--action", c.action}, c.args...)
			if code := Run(env, run); code != 0 {
				t.Fatalf("engine run %s = %d\nstdout: %s\nstderr: %s", c.action, code, stdout, stderr)
			}
			record, err := enginefixture.ReadRecord(filepath.Join(content, enginefixture.RecordName))
			if err != nil {
				t.Fatalf("%v", err)
			}
			ioq3, err := builtin.Find(builtin.IoQuake3)
			if err != nil {
				t.Fatalf("%v", err)
			}
			invocation, err := profile.Resolve(ioq3.Profile, c.action, profile.Request{
				Platform: profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
				Roots: map[string]string{
					profile.RootGame:        game,
					profile.RootContent:     content,
					profile.RootToolInstall: t.TempDir(),
				},
				Runtime: c.runtime,
			})
			if err != nil {
				t.Fatalf("resolving ioquake3's %s: %v", c.action, err)
			}
			want := strings.Join(invocation.Command.Args, " ")
			if got := strings.Join(record.Argv, " "); got != want {
				t.Errorf("%s did not use ioquake3's documented command line:\n  got  %q\n  want %q",
					c.action, got, want)
			}
			for _, fragment := range c.wants {
				if !strings.Contains(want, fragment) {
					t.Errorf("ioquake3's %s does not contain %q: %s", c.action, fragment, want)
				}
			}
		})
	}
}

// The other half of the acceptance: an unsupported or incomplete Quake III
// route fails or warns explicitly, and nothing anywhere reports Quake III as
// plainly supported.
func TestQuake3FailsOrWarnsExplicitlyAndClaimsNoGenericSupport(t *testing.T) {
	// A leaking map is the case that would slip through an exit-status check.
	// Measured: with `-leaktest` Q3Map2 writes the line file, writes no BSP and
	// EXITS 0. The build fails because a required output is not there.
	t.Run("a leak is a failed build even though the compiler exits zero", func(t *testing.T) {
		env, _, _ := testEnv(t)
		contentRoot, _ := q3Content(t)
		bindQ3Map2(t, env, contentRoot, contentRoot)
		source := filepath.Join(t.TempDir(), "leaky.map")
		if err := os.WriteFile(source, []byte(syntheticQ3Map+"\n// aucom_leak_me\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		env, stdout, _ := testEnvAt(t, env.ConfigPath)
		if code := Run(env, []string{"build", "run", "--json",
			"--pipeline", builtin.Q3FastPreview, "--input", "source_map=" + source}); code == 0 {
			t.Fatalf("a leaked Quake III compile reported success:\n%s", stdout)
		}
		out := stdout.String()
		if !strings.Contains(out, "MAP LEAKED") {
			t.Errorf("the leak was not reported in words:\n%s", out)
		}
	})

	// A missing texture is a warning from the compiler and a review from the
	// packager, and the difference between those two is the whole point.
	t.Run("a missing texture compiles and does not package", func(t *testing.T) {
		env, _, _ := testEnv(t)
		contentRoot, vfs := q3Content(t)
		bindQ3Map2(t, env, contentRoot, contentRoot)
		source := filepath.Join(t.TempDir(), "missing.map")
		body := strings.ReplaceAll(syntheticQ3Map, "aucom/floor", "aucom/nosuchtexture")
		if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		env, stdout, stderr := testEnvAt(t, env.ConfigPath)
		if code := Run(env, []string{"build", "run", "--json",
			"--pipeline", builtin.Q3FastPreview, "--input", "source_map=" + source}); code != 0 {
			t.Fatalf("the compile refused a missing texture; measured, it warns: %s\n%s", stderr, stdout)
		}
		if !strings.Contains(stdout.String(), "Couldn't find image for shader textures/aucom/nosuchtexture") {
			t.Errorf("the compiler's warning did not reach the job's output:\n%s", stdout)
		}

		var manifest struct {
			BuildID string `json:"build_id"`
		}
		if err := json.Unmarshal(manifestJSON(t, stdout.String()), &manifest); err != nil {
			t.Fatalf("%v", err)
		}
		archive := filepath.Join(t.TempDir(), "missing.pk3")
		env, stdout, stderr = testEnvAt(t, env.ConfigPath)
		if code := Run(env, []string{"package", "create",
			"--target", "quake3-pk3", "--build", manifest.BuildID, "--out", archive,
			"--map", source, "--from", vfs, "--source-root", vfs}); code == 0 {
			t.Fatalf("a package missing a texture the map uses was written:\n%s", stdout)
		}
		if !strings.Contains(stderr.String(), "textures/aucom/nosuchtexture") {
			t.Errorf("the review does not name the missing texture:\n%s", stderr)
		}

		// And it can be packaged deliberately, with a reason recorded — which
		// is what makes the refusal a review rather than a wall.
		env, stdout, stderr = testEnvAt(t, env.ConfigPath)
		if code := Run(env, []string{"package", "create",
			"--target", "quake3-pk3", "--build", manifest.BuildID, "--out", archive,
			"--map", source, "--from", vfs, "--source-root", vfs,
			"--accept-missing", "--reason", "the texture ships in the server's own pk3"}); code != 0 {
			t.Fatalf("accepting the review did not package it: %d\n%s\n%s", code, stdout, stderr)
		}
		if !strings.Contains(stderr.String(), "the texture ships in the server's own pk3") {
			t.Errorf("the recorded reason was not printed back:\n%s", stderr)
		}
	})

	// Nothing lists Quake III as finished, anywhere a person chooses one.
	t.Run("no surface reports Quake III as plainly supported", func(t *testing.T) {
		env, stdout, stderr := testEnv(t)
		if code := Run(env, []string{"build", "pipelines"}); code != 0 {
			t.Fatalf("exit code = %d (%s)", code, stderr)
		}
		out := stdout.String()
		for _, wanted := range []string{
			builtin.Q3FastPreview, builtin.Q3Normal, "Work in progress", maturity.Quake3Message,
			"companion feedback compatibility",
		} {
			if !strings.Contains(out, wanted) {
				t.Errorf("the pipeline listing is missing %q:\n%s", wanted, out)
			}
		}
		if n := strings.Count(out, maturity.Quake3Message); n != 1 {
			t.Errorf("the Quake III sentence appears %d times; a warning printed per row stops being read", n)
		}
	})
}
