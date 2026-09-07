package cli

import (
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The publishing half of `companion profile`, exercised through Run.
//
// What is contested here is the GATE, not the network: nothing may be published
// or installed without an explicit confirmation, and the preview must be
// available with no session at all — a person deciding whether to publish should
// not have to sign in to find out what they would be disclosing.

func TestPreviewingNeedsNoSessionAndSendsNothing(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"profile", "preview", fixture("valid/minimal.tool.json")}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	for _, want := range []string{
		"example.minimal", "sha256:", "everything the document would make public",
		"does not relicense", "Nothing has been sent",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the preview does not say %q:\n%s", want, stdout)
		}
	}
}

// TestPublishingWithoutConfirmingPrintsThePreviewAndStops.
//
// Exit 2, which in this CLI means "you have not finished asking" rather than
// "something failed" — and it happens BEFORE any attempt to reach a backend, so
// the answer is the same signed in or not.
func TestPublishingWithoutConfirmingPrintsThePreviewAndStops(t *testing.T) {
	env, stdout, _ := testEnv(t)
	code := Run(env, []string{"profile", "publish", fixture("valid/minimal.tool.json")})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stdout.String(), "Add --confirm") {
		t.Errorf("the preview does not say how to go ahead:\n%s", stdout)
	}
	if strings.Contains(stdout.String(), "published") {
		t.Errorf("it claims to have published something:\n%s", stdout)
	}
}

// TestPublishingADocumentThatNamesOneMachineIsRefusedBeforeAnyNetwork.
//
// The gate is the export, so this fails identically whether or not there is a
// backend to send it to — which is what "structurally impossible" means here.
func TestPublishingADocumentThatNamesOneMachineIsRefusedBeforeAnyNetwork(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	for document, phrase := range map[string]string{
		"malicious/absolute-path-leak.tool.json": "on one machine",
		"malicious/home-path-leak.tool.json":     "home directory",
		"malicious/loopback-host.tool.json":      "loopback",
		"malicious/private-host.tool.json":       "private address",
		"malicious/token-leak.tool.json":         "JSON Web Token",
	} {
		code := Run(env, []string{"profile", "publish", "--confirm", fixture(document)})
		if code != 1 {
			t.Fatalf("%s: exit code = %d, want 1", document, code)
		}
		if !strings.Contains(stderr.String(), phrase) {
			t.Errorf("%s: the refusal does not say %q:\n%s", document, phrase, stderr)
		}
		if strings.Contains(stdout.String(), "published") {
			t.Errorf("%s: it claims to have published something:\n%s", document, stdout)
		}
		stderr.Reset()
		stdout.Reset()
	}
}

// TestTheCatalogNeedsASessionAndSaysSo.
func TestTheCatalogNeedsASessionAndSaysSo(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"profile", "catalog"}); code == 0 {
		t.Fatal("the catalog answered with no session")
	}
	if !strings.Contains(stderr.String(), "auth login") {
		t.Errorf("the refusal does not say what to do:\n%s", stderr)
	}
}

// TestYankingWithoutAReasonIsRefusedBeforeAnyNetwork.
func TestYankingWithoutAReasonIsRefusedBeforeAnyNetwork(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"profile", "yank", "listing1", "1.0.0"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "worse than none") {
		t.Errorf("the refusal does not say why a reason is required:\n%s", stderr)
	}
}

// TestTheUsageNamesEveryPublishingSubcommand.
func TestTheUsageNamesEveryPublishingSubcommand(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run(env, []string{"profile", "--help"}); code != 0 {
		t.Fatal("profile --help failed")
	}
	for _, command := range []string{
		"preview", "publish", "catalog", "published", "install", "yank", "report",
	} {
		if !strings.Contains(stdout.String(), "companion profile "+command) {
			t.Errorf("the usage does not name %q:\n%s", command, stdout)
		}
	}
}

// AUT/AUCOM 219: a successful install used to end by naming `companion profile
// bind`, which is not a command — the `profile` dispatcher answers `unknown
// profile command "bind"`. A reader following it cannot tell whether they
// mistyped, whether their build is too old, or whether the install left
// something undone, so the message is checked against the dispatchers rather
// than against somebody's memory of what the commands are called.
func TestTheAdviceAfterAnInstallNamesACommandThisBuildHas(t *testing.T) {
	for _, kind := range []profile.Kind{profile.KindTool, profile.KindEngine, profile.KindPipeline} {
		advice := nextStepAfterInstall(kind, t.TempDir(), "example.profile")
		fields := strings.Fields(strings.TrimPrefix(
			advice[strings.Index(advice, "`")+1:], "companion "))
		if len(fields) < 2 {
			t.Fatalf("%s: the advice names no command: %s", kind, advice)
		}
		group, sub := fields[0], fields[1]

		env, _, stderr := testEnv(t)
		Run(env, []string{group, sub, "--help"})
		if strings.Contains(stderr.String(), "unknown "+group+" command") {
			t.Errorf("%s: the advice names `companion %s %s`, which this build does not have:\n%s",
				kind, group, sub, stderr)
		}
	}
}

// AUT/AUCOM 219: `profile publish`, `install`, `yank` and `report` printed
// usage lines with the flags AFTER the positionals and then refused exactly
// that — Go's flag package stops at the first non-flag argument, and these four
// were the only commands in the binary still using it. A usage line that does
// not parse is worse than no usage line: the reader concludes the command is
// broken or that they have the wrong build.
func TestEveryPublishingUsageLineParses(t *testing.T) {
	// Each row is the invocation as the usage prints it, with the flags last.
	// What is asserted is only that it was not REFUSED for its shape: these run
	// with no session, so a network refusal is the expected ending and is fine.
	for _, invocation := range [][]string{
		{"profile", "publish", "some.tool.json", "--confirm"},
		{"profile", "install", "listing-id", "--approve"},
		{"profile", "yank", "listing-id", "1.0.0", "--reason=withdrawn"},
		{"profile", "report", "listing-id", "--category=other"},
	} {
		env, _, stderr := testEnv(t)
		Run(env, invocation)
		if strings.Contains(stderr.String(), "companion profile preview <file>") {
			t.Errorf("%v was answered with the usage dump:\n%s", invocation, stderr)
		}
	}
}
