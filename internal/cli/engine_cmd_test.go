package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/enginefixture"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

// testEnvAt is testEnv against a config directory that already exists, so a
// test can run several commands against one machine's state.
func testEnvAt(t *testing.T, configPath string) (*Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return &Env{
		Stdout:     &stdout,
		Stderr:     &stderr,
		Version:    "test-version",
		ConfigPath: configPath,
		Lookenv:    func(string) (string, bool) { return "", false },
	}, &stdout, &stderr
}

// gameDir builds something that looks like an installed Quake.
func gameDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "id1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id1", "pak0.pak"), []byte("PACK"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func programFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/false\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEngineListShowsEveryCuratedEngineAndWhatItClaimsHere(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"engine", "list"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	out := stdout.String()
	for _, id := range builtin.Q1Engines {
		if !strings.Contains(out, id) {
			t.Errorf("the list does not include %s:\n%s", id, out)
		}
	}
	if !strings.Contains(out, "not set up here") {
		t.Errorf("the list does not say that nothing is set up:\n%s", out)
	}
	// Every one of them is `unverified` here, and the list says so rather than
	// letting a user assume otherwise.
	if !strings.Contains(out, "unverified") {
		t.Errorf("the list does not carry the platform claim:\n%s", out)
	}
}

func TestEngineShowNamesWhatTheEngineDoesNotDo(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"engine", "show", builtin.QuakeSpasm}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	out := stdout.String()
	for _, want := range []string{"host_dedicated", "this engine does not do it", "GPL-2.0-or-later", "quakespasm"} {
		if !strings.Contains(out, want) {
			t.Errorf("`engine show` does not mention %q:\n%s", want, out)
		}
	}
}

// Detection is a suggestion. The command says so in as many words, because the
// difference between a suggestion and a decision is the whole reason a user
// has to type the path.
func TestEngineDetectRecordsNothing(t *testing.T) {
	game := gameDir(t)
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"engine", "detect", "--near", game}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	out := stdout.String()
	if !strings.Contains(out, game) || !strings.Contains(out, "id1/pak0.pak") {
		t.Errorf("the game was not detected:\n%s", out)
	}
	if !strings.Contains(out, "Nothing was recorded") {
		t.Errorf("detection does not say that it decided nothing:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(env.ConfigPath), "bindings.json")); err == nil {
		t.Error("detection wrote a binding")
	}
}

func TestEngineBindThenCheckThenPreview(t *testing.T) {
	game := gameDir(t)
	enginePath := programFile(t, "quakespasm")
	env, stdout, stderr := testEnv(t)

	// Nothing is set up yet, so `check` says what is missing rather than
	// producing a command that would not work.
	if code := Run(env, []string{"engine", "check", builtin.QuakeSpasm, "--action", "play_map"}); code == 0 {
		t.Fatalf("check passed with nothing set up:\n%s", stdout)
	}
	if !strings.Contains(stderr.String(), "engine bind") {
		t.Errorf("the check does not say how to fix it:\n%s", stderr)
	}

	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"engine", "bind", builtin.QuakeSpasm,
		"--engine", enginePath, "--game-root", game, "--content-root", game}); code != 0 {
		t.Fatalf("bind exit code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), enginePath) {
		t.Errorf("bind did not report what it recorded:\n%s", stdout)
	}

	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"engine", "check", builtin.QuakeSpasm, "--action", "play_map"}); code != 0 {
		t.Fatalf("check exit code = %d after binding, stderr = %s", code, stderr)
	}

	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	code := Run(env, []string{"engine", "preview", builtin.QuakeSpasm,
		"--action", "play_map", "--map", "e1m1", "--mod", "mymod"})
	if code != 0 {
		t.Fatalf("preview exit code = %d, stderr = %s", code, stderr)
	}
	out := stdout.String()
	for _, want := range []string{enginePath, "-basedir", game, "-game", "mymod", "+map", "e1m1", "(client)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the preview does not contain %q:\n%s", want, out)
		}
	}
}

// An action the engine does not have is refused with the list of what it does,
// before anything is started.
func TestEnginePreviewRefusesAnActionTheEngineDoesNotHave(t *testing.T) {
	game := gameDir(t)
	env, _, _ := testEnv(t)
	if code := Run(env, []string{"engine", "bind", builtin.QuakeSpasm,
		"--engine", programFile(t, "quakespasm"), "--game-root", game, "--content-root", game}); code != 0 {
		t.Fatal("bind failed")
	}
	env, _, stderr := testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"engine", "check", builtin.QuakeSpasm, "--action", "host_dedicated"}); code == 0 {
		t.Fatal("QuakeSpasm accepted host_dedicated")
	}
	if !strings.Contains(stderr.String(), "leaves out what its engine does not do") {
		t.Errorf("the refusal does not explain itself:\n%s", stderr)
	}
}

func TestEngineStageAndUnstageFromTheCommandLine(t *testing.T) {
	game := gameDir(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "maps", "level.bsp"), []byte("BSP"), 0o600); err != nil {
		t.Fatal(err)
	}

	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"engine", "stage", "--game-root", game, "--mod", "mymap", "--from", source, "--dry-run"}); code != 0 {
		t.Fatalf("dry run exit code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), "maps/level.bsp") {
		t.Errorf("the dry run did not list what it would copy:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(game, "mymap")); err == nil {
		t.Fatal("the dry run staged something")
	}

	env, stdout, stderr = testEnv(t)
	if code := Run(env, []string{"engine", "stage", "--game-root", game, "--mod", "mymap", "--from", source}); code != 0 {
		t.Fatalf("stage exit code = %d, stderr = %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(game, "mymap", "maps", "level.bsp")); err != nil {
		t.Fatalf("nothing was staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(game, "mymap", engine.StampName)); err != nil {
		t.Fatalf("no staging record was written: %v", err)
	}

	env, stdout, stderr = testEnv(t)
	if code := Run(env, []string{"engine", "unstage", "--game-root", game, "--mod", "mymap"}); code != 0 {
		t.Fatalf("unstage exit code = %d, stderr = %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(game, "mymap")); err == nil {
		t.Error("the staged directory survived")
	}
	if _, err := os.Stat(filepath.Join(game, "id1", "pak0.pak")); err != nil {
		t.Error("unstaging reached the base game")
	}
}

func TestEngineStageWillNotWriteOverTheBaseGame(t *testing.T) {
	game := gameDir(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "pak0.pak"), []byte("MINE"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"engine", "stage", "--game-root", game, "--mod", "id1", "--from", source}); code == 0 {
		t.Fatal("staging over id1 was allowed")
	}
	if !strings.Contains(stderr.String(), "already owns") {
		t.Errorf("the refusal does not say why:\n%s", stderr)
	}
	body, err := os.ReadFile(filepath.Join(game, "id1", "pak0.pak"))
	if err != nil || string(body) != "PACK" {
		t.Errorf("the base game was touched: %q, %v", body, err)
	}
}

// The whole loop from the command line: content built somewhere else is staged
// into a game directory, the engine is started against it, and the staged copy
// is removed afterwards — with the engine's own record of its argv as the
// evidence that it was pointed at the staged directory.
func TestEngineRunStagesPlaysAndCleansUp(t *testing.T) {
	game := gameDir(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "maps", "level.bsp"), []byte("BSP"), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	env, _, _ := testEnv(t)
	// The fixture engine profile is imported the way any profile is: a file in
	// the profile directory. Nothing marks it as special.
	profiles := filepath.Join(filepath.Dir(env.ConfigPath), "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "fixture.engine.json"), enginefixture.ProfileJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	content := t.TempDir()
	env, stdout, stderr := testEnvAt(t, env.ConfigPath)
	if code := Run(env, []string{"engine", "bind", enginefixture.ProfileID,
		"--engine", self, "--game-root", game, "--content-root", content, "--approve"}); code != 0 {
		t.Fatalf("bind exit code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), "approved") {
		t.Errorf("bind --approve did not record an approval:\n%s", stdout)
	}

	env, stdout, stderr = testEnvAt(t, env.ConfigPath)
	code := Run(env, []string{"engine", "run", enginefixture.ProfileID,
		"--action", "play_map", "--map", "level", "--mod", "mymap", "--stage", source})
	if code != 0 {
		t.Fatalf("run exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr.String(), "staged 1 files") {
		t.Errorf("the run did not report what it staged:\n%s", stderr)
	}
	if !strings.Contains(stderr.String(), "(client)") {
		t.Errorf("the run does not say what kind of session it started:\n%s", stderr)
	}

	record, err := enginefixture.ReadRecord(filepath.Join(content, enginefixture.RecordName))
	if err != nil {
		t.Fatalf("%v", err)
	}
	want := strings.Join([]string{"-basedir", game, "-game", "mymap", "+map", "level"}, " ")
	if got := strings.Join(record.Argv, " "); got != want {
		t.Errorf("argv:\n  got  %q\n  want %q", got, want)
	}

	// Cleanup ran, and stopped at the base game.
	if _, err := os.Stat(filepath.Join(game, "mymap")); err == nil {
		t.Error("the staged copy survived the run")
	}
	if _, err := os.Stat(filepath.Join(game, "id1", "pak0.pak")); err != nil {
		t.Error("cleanup reached the base game")
	}
}
