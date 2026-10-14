package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
)

// `companion package map` drives the same three packages for a Quake II build
// as the page's panel does (Q2_009): preview, create, install, run with
// --check, uninstall — and every refusal on the way names its class.
func TestTheQuake2PackageMapJourneyOnTheCommandLine(t *testing.T) {
	env, _, _ := testEnv(t)
	settings, err := loadSettings(env)
	if err != nil {
		t.Fatal(err)
	}
	builds, err := buildsDir(env, settings)
	if err != nil {
		t.Fatal(err)
	}
	game := filepath.Join(t.TempDir(), "Quake II")
	if _, err := q2fixture.Write(filepath.Join(game, "baseq2"), map[string][]byte{
		"pak0.pak": q2fixture.PAK([]string{"pics/colormap.pcx"}, map[string][]byte{"pics/colormap.pcx": q2fixture.PCX()})}); err != nil {
		t.Fatal(err)
	}
	content, _ := q2fixture.Write(t.TempDir(), map[string][]byte{"textures/q2009/marker.wal": q2fixture.WAL("q2009/marker")})
	id, _ := build.NewID(time.Now())
	dir := filepath.Join(builds, id)
	bsp := filepath.Join(dir, "output", "q2009cli.bsp")
	q2fixture.Write(filepath.Dir(bsp), map[string][]byte{"q2009cli.bsp": q2fixture.BSP(`{ "classname" "worldspawn" }`, "q2009/marker")})
	manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id, EngineFamily: "quake2", State: job.Succeeded,
		StartedAt: time.Now().UTC(), Directory: dir, Steps: []build.Step{},
		Inputs:  []build.FileRecord{{Name: "source_map", Path: filepath.Join(dir, "input", "q2009cli.map")}},
		Roots:   []build.RootRecord{{Role: profile.RootGame, Path: filepath.Join(game, "baseq2")}, {Role: profile.RootContent, Path: content}},
		Outputs: []build.FileRecord{{Name: "bsp", Path: bsp}}}
	if err := manifest.Save(dir); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string, string) {
		env, stdout, stderr := testEnvAt(t, env.ConfigPath)
		code := Run(env, args)
		return code, stdout.String(), stderr.String()
	}

	code, out, _ := run("package", "map", "preview", "--build", id)
	if code != 0 || !strings.Contains(out, "third_party_unresolved") || !strings.Contains(out, "--own-loose") {
		t.Fatalf("preview = %d:\n%s", code, out)
	}
	if code, _, errText := run("package", "map", "create", "--build", id); code != 1 || !strings.Contains(errText, "[package_held]") {
		t.Fatalf("an ungranted create = %d: %s", code, errText)
	}
	if code, _, errText := run("package", "map", "create", "--build", id, "--own-archive", "abc"); code != 2 || !strings.Contains(errText, "opens no archive") {
		t.Errorf("an archive grant = %d: %s", code, errText)
	}
	code, out, errText := run("package", "map", "create", "--build", id, "--own-loose", "--json")
	if code != 0 {
		t.Fatalf("create = %d: %s", code, errText)
	}
	var record struct {
		ID      string `json:"id"`
		Entries []struct{ Path string }
	}
	if err := json.Unmarshal([]byte(out), &record); err != nil || !strings.HasPrefix(record.ID, "q2-q2009cli-") || len(record.Entries) != 2 {
		t.Fatalf("the package: %v %s", err, out)
	}
	if code, out, _ = run("package", "map", "list"); code != 0 || !strings.Contains(out, record.ID) {
		t.Errorf("list = %d: %s", code, out)
	}
	if code, _, errText = run("package", "map", "install", record.ID, "--game-root", game, "--mod", "baseq2"); code != 1 || !strings.Contains(errText, "[fs_game_invalid]") {
		t.Errorf("installing into baseq2 = %d: %s", code, errText)
	}
	if code, _, errText = run("package", "map", "install", record.ID, "--game-root", game, "--into", "game-folder"); code != 2 {
		t.Errorf("a Quake III install target = %d: %s", code, errText)
	}

	// Set up the fixture engine for this game folder, and install for it.
	self := mustExecutable(t)
	profiles := filepath.Join(filepath.Dir(env.ConfigPath), "profiles")
	os.MkdirAll(profiles, 0o755)
	os.WriteFile(filepath.Join(profiles, "fixture-q2.engine.json"), enginefixture.ProfileQ2JSON, 0o600)
	if code, _, errText = run("engine", "bind", enginefixture.ProfileQ2ID, "--engine", self, "--game-root", game,
		"--content-root", t.TempDir(), "--approve"); code != 0 {
		t.Fatalf("binding the fixture = %d: %s", code, errText)
	}
	code, out, errText = run("package", "map", "install", record.ID, "--engine", enginefixture.ProfileQ2ID)
	if code != 0 || !strings.Contains(out, "+set game aup-q2009cli") || !strings.Contains(out, "baseq2 is NOT written") {
		t.Fatalf("install = %d: %s %s", code, out, errText)
	}
	code, out, _ = run("package", "map", "installed")
	installation := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "q2i-") {
			installation = strings.TrimSpace(line)
		}
	}
	if code != 0 || installation == "" {
		t.Fatalf("installed = %d: %s", code, out)
	}
	code, out, errText = run("package", "map", "run", installation, "--engine", enginefixture.ProfileQ2ID,
		"--option", "behaviour=quake2_load", "--check", "--wait", "20s")
	if code != 0 || !strings.Contains(out, "map load   ACCEPTED") ||
		!strings.Contains(out, "opened from "+filepath.ToSlash(filepath.Join(game, "aup-q2009cli", "q2009cli.pak"))) {
		t.Fatalf("run = %d:\n%s\n%s", code, out, errText)
	}
	os.Remove(filepath.Join(game, "aup-q2009cli", "engine-record.json"))
	if code, out, errText = run("package", "map", "uninstall", installation); code != 0 {
		t.Fatalf("uninstall = %d: %s %s", code, out, errText)
	}
	if entries, _ := os.ReadDir(game); len(entries) != 1 {
		t.Errorf("the game folder holds %d entries after uninstall", len(entries))
	}
}
