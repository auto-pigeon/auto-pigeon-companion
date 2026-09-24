package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// AUCOM/AUT 228: the whole journey for a TOOL profile somebody wrote, through
// the shipped CLI and nothing else.
//
// AUT/AUCOM 219's defect 6 was that this journey stopped after "bound": there
// was no command that could approve a tool profile, so the only way to run one
// was to start the local server and use the HTTP API. What follows is the walk
// that was impossible, asserted step by step — refused, reviewed, approved, run,
// changed, refused again, re-approved, withdrawn, refused again.

// grantFixtureID is the profile the journey approves. A different id from
// `test.cli.echo` so that this test's document and the pre-granted one cannot be
// confused for each other.
const grantFixtureID = "test.cli.grant"

// writeUngrantedFixture installs a tool profile and a binding that says where
// its program is — and no grant. That is exactly the state `acquire resolve
// --bind` leaves behind, and the state 219 found had no way forward from.
//
// text is what the fixture's one option defaults to, so a caller can change the
// document by changing one string and get a different digest for it.
func writeUngrantedFixture(t *testing.T, env *Env, text string) string {
	t.Helper()
	dir := filepath.Dir(env.ConfigPath)
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	document := []byte(`{
	  "schema_version": "aucom.profile/1.0",
	  "kind": "tool",
	  "id": "` + grantFixtureID + `",
	  "version": "1.0.0",
	  "name": "A tool profile somebody wrote",
	  "summary": "A hand-written tool profile, of the kind a person drops into their profile directory.",
	  "publisher": { "name": "A Companion user" },
	  "license": { "spdx": "MIT", "name": "MIT" },
	  "tool_version": "0.0.0-fixture",
	  "platforms": [{ "platform": { "os": "` + runtime.GOOS + `", "arch": "` + runtime.GOARCH + `" }, "status": "supported" }],
	  "acquisition": [{ "mode": "user_path", "title": "Point at a copy you already have", "hint": "choose the program" }],
	  "executables": [{ "name": "helper", "title": "The program", "file": "helper{platform.exe_suffix}" }],
	  "actions": [{
	    "id": "say",
	    "title": "Say something",
	    "executable": "helper",
	    "args": ["-aucom-test-helper", "echo", "{option.text}"],
	    "options": [{ "name": "text", "title": "Text", "type": "text", "default": "` + text + `", "max_length": 64 }],
	    "roots": [{ "role": "workspace", "access": "read_write", "purpose": "scratch space" }],
	    "timeout_seconds": 60
	  }]
	}`)
	if err := os.WriteFile(filepath.Join(profiles, "written-by-hand.tool.json"), document, 0o600); err != nil {
		t.Fatal(err)
	}
	decoded, err := profile.Decode(document)
	if err != nil {
		t.Fatalf("the fixture profile is invalid: %v", err)
	}
	digest, err := profile.Digest(decoded)
	if err != nil {
		t.Fatal(err)
	}
	// The binding: where the program is, and nothing about permission.
	if _, err := binding.Update(filepath.Join(dir, "bindings.json"), func(set *binding.Set) error {
		local, _ := set.Find(grantFixtureID)
		local.ProfileID = grantFixtureID
		local.ProfileVersion = "1.0.0"
		local.ProfileDigest = digest
		local.Trust = profile.TrustLocal
		local.Acquisition = profile.AcquireUserPath
		local.Executables = map[string]string{"helper": selfPath(t)}
		local.Grant = nil
		return set.Put(local)
	}); err != nil {
		t.Fatal(err)
	}
	return digest
}

// run is one CLI invocation against a shared config, with its own buffers.
func run(t *testing.T, env *Env, args ...string) (int, string, string) {
	t.Helper()
	fresh, stdout, stderr := testEnv(t)
	fresh.ConfigPath = env.ConfigPath
	code := Run(fresh, args)
	return code, stdout.String(), stderr.String()
}

func TestAToolProfileSomebodyWroteIsApprovedRunAndWithdrawnFromTheCommandLine(t *testing.T) {
	env, _, _ := testEnv(t)
	digest := writeUngrantedFixture(t, env, "first-version")

	// 1. Bound, and refused. Binding says where the program is; it is not
	//    permission to start it.
	code, _, stderr := run(t, env, "job", "run", "--profile", grantFixtureID, "--action", "say")
	if code == 0 {
		t.Fatal("an unapproved profile ran")
	}
	if !strings.Contains(stderr, "has not been reviewed yet") {
		t.Errorf("the refusal does not say what is missing:\n%s", stderr)
	}

	// 2. The review: what it would be allowed to do, and its digest.
	code, stdout, stderr := run(t, env, "profile", "review", grantFixtureID)
	if code != 0 {
		t.Fatalf("profile review exited %d: %s", code, stderr)
	}
	for _, want := range []string{
		"A tool profile somebody wrote", digest, "approved: no",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the review does not contain %q:\n%s", want, stdout)
		}
	}

	// 3. Asking to grant with nothing decided prints the review and refuses,
	//    naming the exact command. Exit 2: the invocation was incomplete, and a
	//    script must not read it as an approval.
	code, stdout, stderr = run(t, env, "profile", "grant", grantFixtureID)
	if code != 2 {
		t.Errorf("`profile grant` with no flags exited %d, want 2", code)
	}
	if !strings.Contains(stdout, "A tool profile somebody wrote") || !strings.Contains(stdout, digest) {
		t.Errorf("it did not show what would be approved:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--digest="+digest) || !strings.Contains(stderr, "--approve") {
		t.Errorf("it did not name the command to run:\n%s", stderr)
	}

	// 4. And each half alone is still not a decision.
	if code, _, _ := run(t, env, "profile", "grant", grantFixtureID, "--approve"); code != 2 {
		t.Errorf("`--approve` with no digest exited %d, want 2", code)
	}
	if code, _, _ := run(t, env, "profile", "grant", grantFixtureID, "--digest="+digest); code != 2 {
		t.Errorf("a digest with no `--approve` exited %d, want 2", code)
	}
	if local := storedBinding(t, env, grantFixtureID); local.Grant != nil {
		t.Fatal("a refused approval recorded a grant")
	}

	// 5. A digest that is not this document is refused, and says both.
	stale := "sha256:" + strings.Repeat("00", 32)
	code, _, stderr = run(t, env, "profile", "grant", grantFixtureID, "--digest="+stale, "--approve")
	if code != 1 {
		t.Errorf("a stale digest exited %d, want 1", code)
	}
	if !strings.Contains(stderr, stale) || !strings.Contains(stderr, digest) {
		t.Errorf("the refusal does not name what was approved and what is here:\n%s", stderr)
	}

	// 6. The approval itself.
	code, stdout, stderr = run(t, env, "profile", "grant", grantFixtureID, "--digest="+digest, "--approve")
	if code != 0 {
		t.Fatalf("profile grant exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, digest) || !strings.Contains(stdout, "approved test.cli.grant") {
		t.Errorf("the approval does not say what it approved:\n%s", stdout)
	}
	local := storedBinding(t, env, grantFixtureID)
	if local.Grant == nil || local.Grant.Digest != digest {
		t.Fatalf("the stored grant is %+v", local.Grant)
	}

	// 7. And now it runs — a real process, started by the executor.
	code, stdout, stderr = run(t, env, "job", "run", "--profile", grantFixtureID, "--action", "say")
	if code != 0 {
		t.Fatalf("the approved profile did not run: exit %d, %s", code, stderr)
	}
	if !strings.Contains(stdout, "first-version") || !strings.Contains(stdout, "succeeded") {
		t.Errorf("the job did not run the program:\n%s", stdout)
	}

	// 8. The document changes. The approval was of bytes, so it no longer
	//    applies, and the executor says so rather than running the new one.
	second := writeUngrantedFixtureKeepingTheBinding(t, env, "second-version")
	if second == digest {
		t.Fatal("editing the document did not change its digest")
	}
	code, _, stderr = run(t, env, "job", "run", "--profile", grantFixtureID, "--action", "say")
	if code == 0 {
		t.Fatal("a document that changed after approval still ran")
	}
	if !strings.Contains(stderr, "has changed since it was approved") {
		t.Errorf("the refusal does not say the document changed:\n%s", stderr)
	}
	if code, _, _ := run(t, env, "profile", "grant", grantFixtureID, "--digest="+digest, "--approve"); code != 1 {
		t.Error("the digest approved before is still accepted for the new document")
	}

	// 9. Re-approved as itself, and running again.
	if code, _, stderr := run(t, env, "profile", "grant", grantFixtureID, "--digest="+second, "--approve"); code != 0 {
		t.Fatalf("re-approving exited %d: %s", code, stderr)
	}
	code, stdout, stderr = run(t, env, "job", "run", "--profile", grantFixtureID, "--action", "say")
	if code != 0 {
		t.Fatalf("the re-approved profile did not run: exit %d, %s", code, stderr)
	}
	if !strings.Contains(stdout, "second-version") {
		t.Errorf("the job ran the old document:\n%s", stdout)
	}

	// 10. Withdrawing needs saying so, and then it is refused again.
	code, _, stderr = run(t, env, "profile", "withdraw", grantFixtureID)
	if code != 2 || !strings.Contains(stderr, "--confirm") {
		t.Errorf("withdrawing without --confirm exited %d:\n%s", code, stderr)
	}
	if local := storedBinding(t, env, grantFixtureID); local.Grant == nil {
		t.Fatal("an unconfirmed withdrawal took the approval away")
	}
	code, stdout, stderr = run(t, env, "profile", "withdraw", grantFixtureID, "--confirm")
	if code != 0 {
		t.Fatalf("profile withdraw exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "cannot run until it is approved again") {
		t.Errorf("the withdrawal does not say what it did:\n%s", stdout)
	}
	code, _, stderr = run(t, env, "job", "run", "--profile", grantFixtureID, "--action", "say")
	if code == 0 {
		t.Fatal("a withdrawn profile still ran")
	}
	if !strings.Contains(stderr, "has not been reviewed yet") {
		t.Errorf("the refusal after a withdrawal does not say what is missing:\n%s", stderr)
	}

	// And the setup survived it: where the program is is not part of what was
	// approved.
	if local := storedBinding(t, env, grantFixtureID); local.Executables["helper"] != selfPath(t) {
		t.Errorf("withdrawing forgot where the program is: %+v", local.Executables)
	}
}

// writeUngrantedFixtureKeepingTheBinding rewrites the document in place and
// leaves the binding — including its grant — exactly as it was. That is what
// editing a file on disk does, and the point is that nothing about the binding
// notices.
func writeUngrantedFixtureKeepingTheBinding(t *testing.T, env *Env, text string) string {
	t.Helper()
	before := storedBinding(t, env, grantFixtureID)
	digest := writeUngrantedFixture(t, env, text)
	if _, err := binding.Update(filepath.Join(filepath.Dir(env.ConfigPath), "bindings.json"),
		func(set *binding.Set) error { return set.Put(before) }); err != nil {
		t.Fatal(err)
	}
	return digest
}

func storedBinding(t *testing.T, env *Env, id string) binding.LocalBinding {
	t.Helper()
	set, err := binding.LoadFile(filepath.Join(filepath.Dir(env.ConfigPath), "bindings.json"))
	if err != nil {
		t.Fatalf("reading the binding store: %v", err)
	}
	local, _ := set.Find(id)
	return local
}

// The JSON shape a script reads, and the one field it must not have to compute:
// whether this profile may run.
func TestTheReviewIsReadableByAScript(t *testing.T) {
	env, _, _ := testEnv(t)
	digest := writeUngrantedFixture(t, env, "scripted")

	code, stdout, stderr := run(t, env, "profile", "review", grantFixtureID, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var before map[string]any
	if err := json.Unmarshal([]byte(stdout), &before); err != nil {
		t.Fatalf("the review is not JSON: %v\n%s", err, stdout)
	}
	if before["digest"] != digest {
		t.Errorf("digest = %v, want %s", before["digest"], digest)
	}
	if before["authorized"] != false {
		t.Errorf("authorized = %v, want false", before["authorized"])
	}
	if _, held := before["authorization_error"]; !held {
		t.Error("the review does not say why it may not run")
	}
	if permissions, ok := before["permissions"].([]any); !ok || len(permissions) == 0 {
		t.Errorf("the review lists no permissions: %v", before["permissions"])
	}

	if code, _, stderr := run(t, env, "profile", "grant", grantFixtureID,
		"--digest="+digest, "--approve"); code != 0 {
		t.Fatalf("granting exited %d: %s", code, stderr)
	}
	_, stdout, _ = run(t, env, "profile", "review", grantFixtureID, "--json")
	var after map[string]any
	if err := json.Unmarshal([]byte(stdout), &after); err != nil {
		t.Fatal(err)
	}
	if after["authorized"] != true {
		t.Errorf("authorized = %v after an approval", after["authorized"])
	}
	grant, ok := after["grant"].(map[string]any)
	if !ok || grant["digest"] != digest {
		t.Errorf("the recorded grant is %v", after["grant"])
	}
}

// A profile id nothing knows about is a bad invocation with a message that says
// what this machine does have, not a stack trace.
func TestApprovingSomethingThatIsNotHereSaysSo(t *testing.T) {
	env, _, _ := testEnv(t)
	for _, args := range [][]string{
		{"profile", "review", "example.nothing.here"},
		{"profile", "grant", "example.nothing.here", "--digest=sha256:x", "--approve"},
		{"profile", "withdraw", "example.nothing.here", "--confirm"},
	} {
		code, _, stderr := run(t, env, args...)
		if code != 1 {
			t.Errorf("%v exited %d, want 1", args, code)
		}
		if !strings.Contains(stderr, "example.nothing.here") {
			t.Errorf("%v does not name what was asked for:\n%s", args, stderr)
		}
	}
}

// Each of the three takes exactly one id.
func TestTheApprovalCommandsCheckTheirArguments(t *testing.T) {
	env, _, _ := testEnv(t)
	for _, args := range [][]string{
		{"profile", "review"},
		{"profile", "review", "a", "b"},
		{"profile", "grant"},
		{"profile", "withdraw"},
	} {
		if code, _, _ := run(t, env, args...); code != 2 {
			t.Errorf("%v exited %d, want 2", args, code)
		}
	}
}

// A tool profile is not routed through the engine commands, and the engine
// commands still refuse it — the separation 228 was asked to keep.
func TestEngineBindStillRefusesAToolProfile(t *testing.T) {
	env, _, _ := testEnv(t)
	writeUngrantedFixture(t, env, "not-an-engine")
	code, _, stderr := run(t, env, "engine", "bind", grantFixtureID, "--approve")
	if code == 0 {
		t.Fatal("`engine bind` accepted a tool profile")
	}
	if !strings.Contains(stderr, "engine profile") {
		t.Errorf("the refusal does not say what `engine bind` is for:\n%s", stderr)
	}
}

// The usage text names them, because a command missing from `profile --help` is
// a command nobody finds.
func TestTheProfileUsageNamesTheApprovalCommands(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run(env, []string{"profile", "--help"}); code != 0 {
		t.Fatal("profile --help did not exit 0")
	}
	for _, want := range []string{
		"companion profile review", "companion profile grant", "companion profile withdraw",
		"--approve", "--confirm",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("`profile --help` does not mention %q:\n%s", want, stdout)
		}
	}
}
