package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/pack"
)

// packageFixture is a small map source tree, plus a directory standing in for
// an installed game.
func packageFixture(t *testing.T) (mine, game string) {
	t.Helper()
	mine = t.TempDir()
	game = t.TempDir()
	for path, content := range map[string]string{
		"maps/e1m1.bsp":   "the compiled level",
		"gfx/palette.lmp": "my palette",
	} {
		full := filepath.Join(mine, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("creating: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("writing: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(game, "id1"), 0o755); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if err := os.WriteFile(filepath.Join(game, "id1", "pak0.pak"), []byte("not really a pak"), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	return mine, game
}

func TestPackageTargetsListsWhatThisBuildWrites(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run(env, []string{"package", "targets"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"quake-pak", "quake2-pak", "quake3-pk3", "reproducibility", "not permitted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("targets output does not mention %q:\n%s", want, out)
		}
	}
}

func TestPackagePreviewWritesNothingAndShowsEveryDecision(t *testing.T) {
	mine, _ := packageFixture(t)
	env, stdout, _ := testEnv(t)
	out := filepath.Join(t.TempDir(), "mymap.pak")

	code := Run(env, []string{"package", "preview",
		"--target", "quake-pak", "--from", mine, "--out", out})
	if code != 0 {
		t.Fatalf("exit code = %d, stdout:\n%s", code, stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{"maps/e1m1.bsp", "gfx/palette.lmp", "authored", "2 to package"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the preview does not mention %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("preview wrote the archive")
	}
}

func TestPackageCreateWritesAnArchiveAndItsSidecar(t *testing.T) {
	mine, _ := packageFixture(t)
	env, stdout, stderr := testEnv(t)
	out := filepath.Join(t.TempDir(), "mymap.pak")

	code := Run(env, []string{"package", "create",
		"--target", "quake-pak", "--from", mine, "--out", out, "--label", "my map"})
	if code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("the archive is not there: %v", err)
	}
	manifest, err := pack.LoadManifest(out + pack.ManifestSuffix)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest.Companion != "test-version" {
		t.Fatalf("the manifest says the Companion is %q", manifest.Companion)
	}
	if manifest.Label != "my map" || len(manifest.Contents) != 2 {
		t.Fatalf("manifest is %+v", manifest)
	}

	// It reads back through the commands that read archives.
	inspectEnv, inspectOut, _ := testEnv(t)
	if code := Run(inspectEnv, []string{"package", "inspect", out}); code != 0 {
		t.Fatalf("inspect exit code = %d", code)
	}
	if !strings.Contains(inspectOut.String(), "maps/e1m1.bsp") {
		t.Fatalf("inspect output:\n%s", inspectOut.String())
	}
	verifyEnv, verifyOut, _ := testEnv(t)
	if code := Run(verifyEnv, []string{"package", "verify", out}); code != 0 {
		t.Fatalf("verify exit code = %d:\n%s", code, verifyOut.String())
	}
	if !strings.Contains(verifyOut.String(), "verified 2 of 2") {
		t.Fatalf("verify output:\n%s", verifyOut.String())
	}
	if !strings.Contains(verifyOut.String(), "agrees") {
		t.Fatalf("verify did not compare against the sidecar:\n%s", verifyOut.String())
	}
}

func TestPackageCreateRefusesUnreviewedContentAndTakesAnAcknowledgement(t *testing.T) {
	mine, _ := packageFixture(t)
	out := filepath.Join(t.TempDir(), "mymap.pak")

	// --add selects a file with no declared root behind it, so it is unknown.
	stray := filepath.Join(t.TempDir(), "stray.dat")
	if err := os.WriteFile(stray, []byte("who knows"), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}

	env, _, stderr := testEnv(t)
	args := []string{"package", "create", "--target", "quake-pak",
		"--from", mine, "--add", "stray.dat=" + stray, "--out", out}
	if code := Run(env, args); code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "need review") {
		t.Fatalf("stderr: %s", stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("the archive was written despite the refusal")
	}

	env2, _, stderr2 := testEnv(t)
	if code := Run(env2, append(append([]string{}, args...), "--acknowledge", "stray.dat")); code != 0 {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr2.String())
	}
	manifest, err := pack.LoadManifest(out + pack.ManifestSuffix)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(manifest.Review.Acknowledged) != 1 || manifest.Review.Acknowledged[0] != "stray.dat" {
		t.Fatalf("the review record is %+v", manifest.Review)
	}
}

func TestPackageUsesTheConfiguredGameRootsWithoutBeingAsked(t *testing.T) {
	// The accident this policy exists for: sweeping a directory that is also a
	// game directory. Nobody passed --game-root; the configuration knew.
	mine, game := packageFixture(t)
	env, stdout, _ := testEnv(t)
	settings := config.Config{GameRoots: map[string]string{"quake": game}}
	if err := config.SaveTo(env.ConfigPath, settings); err != nil {
		t.Fatalf("saving the config: %v", err)
	}

	code := Run(env, []string{"package", "preview", "--target", "quake-pak",
		"--from", mine, "--from-at", "id1=" + filepath.Join(game, "id1")})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	text := stdout.String()
	if !strings.Contains(text, "game_content") {
		t.Fatalf("content swept out of a configured game root was not flagged:\n%s", text)
	}
	if !strings.Contains(text, "1 awaiting review") {
		t.Fatalf("preview totals are wrong:\n%s", text)
	}
	if !strings.Contains(text, "id1") {
		t.Fatalf("the hint about the id1 directory is missing:\n%s", text)
	}
}

func TestPackageAuthorizeRequiresAReason(t *testing.T) {
	mine, _ := packageFixture(t)
	env, _, stderr := testEnv(t)
	code := Run(env, []string{"package", "create", "--target", "quake-pak", "--from", mine,
		"--out", filepath.Join(t.TempDir(), "x.pak"), "--authorize", "maps/e1m1.bsp"})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--reason") {
		t.Fatalf("stderr: %s", stderr.String())
	}
}

func TestPackageAcknowledgeAllNamesWhatItCovered(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.pak")
	env, _, stderr := testEnv(t)
	// No --source-root and --add-only selection: everything is unknown.
	stray := filepath.Join(t.TempDir(), "stray.dat")
	if err := os.WriteFile(stray, []byte("who knows"), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	code := Run(env, []string{"package", "create", "--target", "quake-pak",
		"--add", "a.dat=" + stray, "--add", "b.dat=" + stray, "--out", out,
		"--acknowledge-all", "--reason", "both are mine, written before this machine had a build"})
	if code != 0 {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "a.dat") || !strings.Contains(stderr.String(), "b.dat") {
		t.Fatalf("--acknowledge-all did not name what it covered:\n%s", stderr.String())
	}
	manifest, err := pack.LoadManifest(out + pack.ManifestSuffix)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(manifest.Review.Acknowledged) != 2 {
		t.Fatalf("the review record is %+v", manifest.Review)
	}
	if !strings.Contains(manifest.Review.Reason, "before this machine had a build") {
		t.Fatalf("the reason was not recorded verbatim: %q", manifest.Review.Reason)
	}
}

func TestPackageRefusesAnUnknownTargetAndSaysWhatThereIs(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"package", "create", "--target", "quake4-pk4", "--out", "x"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	for _, want := range []string{"quake-pak", "quake2-pak", "quake3-pk3"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr does not list %q:\n%s", want, stderr.String())
		}
	}
}

func TestPackageRefusesACompressionThePAKFormatCannotDo(t *testing.T) {
	env, _, stderr := testEnv(t)
	code := Run(env, []string{"package", "create", "--target", "quake-pak",
		"--compression", "deflate", "--out", "x", "--from", t.TempDir()})
	if code == 0 {
		t.Fatal("a deflated PAK was accepted")
	}
	if !strings.Contains(stderr.String(), "stores its members verbatim") {
		t.Fatalf("stderr: %s", stderr.String())
	}
}

func TestPackageExtractRoundTripsThroughTheCLI(t *testing.T) {
	mine, _ := packageFixture(t)
	out := filepath.Join(t.TempDir(), "mymap.pk3")
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"package", "create", "--target", "quake3-pk3", "--from", mine, "--out", out}); code != 0 {
		t.Fatalf("create exit code = %d; %s", code, stderr.String())
	}
	dest := filepath.Join(t.TempDir(), "unpacked")
	extractEnv, extractOut, extractErr := testEnv(t)
	if code := Run(extractEnv, []string{"package", "extract", out, "--dest", dest}); code != 0 {
		t.Fatalf("extract exit code = %d; %s", code, extractErr.String())
	}
	if !strings.Contains(extractOut.String(), "maps/e1m1.bsp") {
		t.Fatalf("extract output:\n%s", extractOut.String())
	}
	content, err := os.ReadFile(filepath.Join(dest, "maps", "e1m1.bsp"))
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(content) != "the compiled level" {
		t.Fatalf("extracted %q", content)
	}
	// A second extraction over the same destination is refused.
	againEnv, _, againErr := testEnv(t)
	if code := Run(againEnv, []string{"package", "extract", out, "--dest", dest}); code != 1 {
		t.Fatalf("a second extraction returned %d, want 1", code)
	}
	if !strings.Contains(againErr.String(), "--replace") {
		t.Fatalf("stderr: %s", againErr.String())
	}
}

func TestPackagePreviewJSONIsMachineReadable(t *testing.T) {
	mine, _ := packageFixture(t)
	env, stdout, _ := testEnv(t)
	if code := Run(env, []string{"package", "preview", "--target", "quake-pak", "--from", mine, "--json"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var plan struct {
		Target    map[string]any   `json:"Target"`
		Decisions []map[string]any `json:"Decisions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("the JSON plan does not parse: %v\n%s", err, stdout.String())
	}
	if len(plan.Decisions) != 2 {
		t.Fatalf("the plan has %d decisions", len(plan.Decisions))
	}
	for _, decision := range plan.Decisions {
		for _, field := range []string{"path", "source", "size", "provenance", "verdict", "rule", "reason"} {
			if _, ok := decision[field]; !ok {
				t.Fatalf("a decision has no %q: %v", field, decision)
			}
		}
	}
}

func TestPackageIsInTheUsageText(t *testing.T) {
	if !strings.Contains(UsageText(), "package") {
		t.Fatal("the package command is not in the usage listing")
	}
	found := false
	for _, name := range Names() {
		if name == "package" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the registry is %v", Names())
	}
}
