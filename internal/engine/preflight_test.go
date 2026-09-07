package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

func builtinEngine(t *testing.T, id string) (profile.Profile, string) {
	t.Helper()
	entry, err := builtin.Find(id)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return entry.Profile, entry.Digest
}

// A binding for a built-in profile that is complete and correct, which every
// test below then breaks in exactly one way.
func workingBinding(t *testing.T, document profile.Profile, digest string) (binding.LocalBinding, string, string) {
	t.Helper()
	game := gameRoot(t)
	content := t.TempDir()
	enginePath := filepath.Join(t.TempDir(), "quakespasm")
	writeFile(t, enginePath, "#!/bin/false\n")
	return binding.LocalBinding{
		SchemaVersion:  binding.SchemaVersion,
		ProfileID:      document.Metadata().ID,
		ProfileVersion: document.Metadata().Version,
		ProfileDigest:  digest,
		Trust:          profile.TrustBuiltin,
		Executables:    map[string]string{"engine": enginePath},
		Roots:          map[string]string{profile.RootGame: game, profile.RootContent: content},
		UpdatedAt:      time.Now().UTC(),
	}, game, content
}

func linux() engine.Checker {
	return engine.Checker{Platform: profile.Platform{OS: "linux", Arch: "amd64"}}
}

func TestAWorkingSetupHasNothingToSay(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, _, _ := workingBinding(t, document, digest)
	if problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap); len(problems) != 0 {
		t.Fatalf("a working setup reported %v", problems)
	}
}

func TestMissingEngineIsReportedWithThePathThatIsNotThere(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, _, _ := workingBinding(t, document, digest)
	gone := local.Executables["engine"]
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap)
	if !problems.Has(engine.FaultMissingEngine) {
		t.Fatalf("a missing engine reported %v", problems)
	}
	if !strings.Contains(problems.Error(), gone) {
		t.Errorf("the message does not name the path that is missing: %s", problems.Error())
	}
}

func TestMissingGameDataSaysWhichDirectoryWasLookedIn(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, game, _ := workingBinding(t, document, digest)
	if err := os.RemoveAll(filepath.Join(game, "id1")); err != nil {
		t.Fatal(err)
	}
	problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap)
	if !problems.Has(engine.FaultMissingGameData) {
		t.Fatalf("a game root with no game reported %v", problems)
	}
	message := problems.Error()
	for _, want := range []string{"id1", game, "never downloads or copies game data"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not mention %q: %s", want, message)
		}
	}
}

// The failure that looks like nothing at all: the directory is visibly there,
// the engine finds no game, and no message anybody reads mentions capitals.
func TestGameDataSpelledWithTheWrongCaseIsCalledOut(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, game, _ := workingBinding(t, document, digest)
	if err := os.Rename(filepath.Join(game, "id1"), filepath.Join(game, "Id1")); err != nil {
		t.Skipf("this filesystem does not distinguish the two spellings: %v", err)
	}
	if _, err := os.Stat(filepath.Join(game, "id1")); err == nil {
		t.Skip("this filesystem does not distinguish the two spellings")
	}
	problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap)
	if !problems.Has(engine.FaultMissingGameData) {
		t.Fatalf("a mis-spelled base directory reported %v", problems)
	}
	if !strings.Contains(problems.Error(), "Id1") || !strings.Contains(problems.Error(), "case-sensitive") {
		t.Errorf("the message does not explain the spelling: %s", problems.Error())
	}
}

// Ironwail does not run on macOS, and saying so before anything starts is the
// difference between a sentence and a wasted afternoon.
func TestAnUnsupportedPlatformIsRefusedWithTheReason(t *testing.T) {
	document, digest := builtinEngine(t, builtin.Ironwail)
	local, _, _ := workingBinding(t, document, digest)
	checker := engine.Checker{Platform: profile.Platform{OS: "darwin", Arch: "arm64"}}
	problems := checker.Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap)
	if !problems.Has(engine.FaultUnsupportedPlatform) {
		t.Fatalf("Ironwail on macOS reported %v", problems)
	}
	if !strings.Contains(problems.Error(), "macOS") {
		t.Errorf("the refusal does not say why: %s", problems.Error())
	}
}

// An engine that does not host says so by declaring no action, and the message
// has to make clear that this is the profile being honest rather than
// incomplete.
func TestAnActionTheEngineDoesNotHaveSaysSoAndListsWhatItDoes(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, _, _ := workingBinding(t, document, digest)
	problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionHostDedicated)
	if !problems.Has(engine.FaultUnknownAction) {
		t.Fatalf("host_dedicated on QuakeSpasm reported %v", problems)
	}
	message := problems.Error()
	for _, want := range []string{"host_dedicated", "play_map", "leaves out what its engine does not do"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not mention %q: %s", want, message)
		}
	}
}

func TestAStaleBindingIsNamedRatherThanUsed(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, _, _ := workingBinding(t, document, digest)
	local.ProfileDigest = "sha256:" + strings.Repeat("0", 64)
	problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap)
	if !problems.Has(engine.FaultStaleBinding) {
		t.Fatalf("a binding for a different document reported %v", problems)
	}
	if !strings.Contains(problems.Error(), "profile diff") {
		t.Errorf("the message does not say how to see what changed: %s", problems.Error())
	}
}

func TestAnUnsetRootIsReportedWithTheFlagThatSetsIt(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, _, _ := workingBinding(t, document, digest)
	delete(local.Roots, profile.RootGame)
	problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap)
	if !problems.Has(engine.FaultUnboundRoot) {
		t.Fatalf("an unset game root reported %v", problems)
	}
	if !strings.Contains(problems.Error(), "--game-root") {
		t.Errorf("the message does not say how to set it: %s", problems.Error())
	}
}

// Nothing built in needs approving; everything else does, and saying which is
// the point of the trust states.
func TestACommunityProfileWithNoGrantIsRefusedBeforeAnythingStarts(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, _, _ := workingBinding(t, document, digest)
	local.Trust = profile.TrustCommunity
	problems := linux().Check(document, profile.TrustCommunity, digest, local, profile.ActionPlayMap)
	if !problems.Has(engine.FaultNotAuthorized) {
		t.Fatalf("an ungranted community profile reported %v", problems)
	}

	local.Grant = profile.NewGrant(document, profile.TrustCommunity, digest, time.Now())
	problems = linux().Check(document, profile.TrustCommunity, digest, local, profile.ActionPlayMap)
	if problems.Has(engine.FaultNotAuthorized) {
		t.Fatalf("an approved community profile is still refused: %v", problems)
	}
}

// Every shipped profile declares the base directory twice — once for loose
// files and once for the PAK in it — and the message a user with a mis-set
// game root sees must not read "there is no id1 or id1 directory".
func TestTheMissingGameDataMessageDoesNotRepeatTheDirectory(t *testing.T) {
	for _, id := range builtin.Q1Engines {
		document, digest := builtinEngine(t, id)
		local, game, _ := workingBinding(t, document, digest)
		if err := os.RemoveAll(filepath.Join(game, "id1")); err != nil {
			t.Fatal(err)
		}
		problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap)
		message := problems.Error()
		if strings.Contains(message, "id1 or id1") {
			t.Errorf("%s: %s", id, message)
		}
		if !strings.Contains(message, "no id1 directory") {
			t.Errorf("%s does not name the directory it looked for: %s", id, message)
		}
	}
}

// A Fix line that names a flag the command does not accept is worse than no Fix
// line: it fails with "flag provided but not defined" and leaves the user with
// nothing.
func TestEveryFixSuggestsAFlagThatExists(t *testing.T) {
	document, digest := builtinEngine(t, builtin.QuakeSpasm)
	local, _, _ := workingBinding(t, document, digest)
	for _, role := range []string{profile.RootGame, profile.RootContent, profile.RootProject, profile.RootToolInstall} {
		delete(local.Roots, role)
	}
	problems := linux().Check(document, profile.TrustBuiltin, digest, local, profile.ActionPlayMap)
	if len(problems) == 0 {
		t.Fatal("removing every root reported nothing")
	}
	for _, problem := range problems {
		for _, flag := range []string{"--project-root", "--root tool_root= ", "--root project_root= "} {
			if strings.Contains(problem.Fix, flag) {
				t.Errorf("the fix names %q, which `engine bind` does not accept: %s", flag, problem.Fix)
			}
		}
	}
}
