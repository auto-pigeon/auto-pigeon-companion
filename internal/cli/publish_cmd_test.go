package cli

import (
	"strings"
	"testing"
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
