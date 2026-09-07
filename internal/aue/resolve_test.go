package aue_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
)

// The acceptance: a clean authorized install downloads, verifies and runs the
// compatible extractor. Every step is real — the catalogue and the
// compatibility manifest are signed and verified over TLS, the artifact is
// downloaded and digest-checked, the executable is exec'd, and the protocol it
// reports is compared before anything else is asked of it.
func TestACleanInstallDownloadsVerifiesAndRuns(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.require("1.171", "1.0")
	f.publish()

	runner, err := f.resolver(nil).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	record := runner.Provenance()
	if !record.Verified || record.Mode != aue.ModeManaged {
		t.Fatalf("provenance = %+v", record)
	}
	if record.Version != "1.171" || record.Digest != f.digestOf("1.171") {
		t.Errorf("provenance = %+v", record)
	}
	if record.Signer != f.catalogKeyID {
		t.Errorf("signer = %q, want %q", record.Signer, f.catalogKeyID)
	}
	if record.Protocol != "1.0" || record.MinProtocol != "1.0" {
		t.Errorf("protocol = %q / %q", record.Protocol, record.MinProtocol)
	}
	// The licence travels from the catalogue entry and is not restated here.
	if record.License.SPDX != "AGPL-3.0-only" || record.License.CorrespondingSource == "" {
		t.Errorf("licence = %+v", record.License)
	}

	stdout, err := runner.Run(context.Background(), "version")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(string(stdout)) != "1.171" {
		t.Errorf("version = %q", stdout)
	}
	if f.downloads != 1 {
		t.Errorf("downloads = %d, want 1", f.downloads)
	}
}

// An UPGRADE: a new compatibility manifest, at a higher serial, names a newer
// build, and the Companion moves to it.
func TestAnUpgradeMovesToTheVersionTheManifestNames(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{version: "1.171"})
	f.require("1.171", "1.0")
	f.publish()

	first, err := f.resolver(nil).Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Provenance().Version != "1.171" {
		t.Fatalf("first = %s", first.Provenance().Version)
	}

	f.addRelease("1.180", script{version: "1.180"})
	f.document.Serial = 2
	f.require("1.180", "1.0")
	f.compatibility.Serial = 2
	f.publish()

	second, err := f.resolver(nil).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve after upgrade: %v", err)
	}
	if second.Provenance().Version != "1.180" {
		t.Errorf("after upgrade = %s, want 1.180", second.Provenance().Version)
	}
	stdout, err := second.Run(context.Background(), "version")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdout)) != "1.180" {
		t.Errorf("the upgraded build reports %q", stdout)
	}
}

// A DOWNGRADE of the documents themselves is refused. This is the case that
// makes withdrawal stick: an attacker who can serve stale bytes replays the
// manifest that named the bad build, and the ratchet says no.
func TestAReplayedCompatibilityManifestIsRefused(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.addRelease("1.180", script{version: "1.180"})
	f.require("1.180", "1.0")
	f.compatibility.Serial = 2
	f.publish()

	if _, err := f.resolver(nil).Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The superseded manifest, served again.
	f.require("1.171", "1.0")
	f.compatibility.Serial = 1
	f.publish()

	_, err := f.resolver(nil).Resolve(context.Background())
	if !errors.Is(err, catalog.ErrRollback) {
		t.Fatalf("err = %v, want catalog.ErrRollback", err)
	}
}

// A COMPATIBLE ROLLBACK is a different thing and is allowed: a NEW manifest, at
// a HIGHER serial, deliberately naming an older build. That is what withdrawing
// a bad release looks like, and refusing it would leave a publisher unable to
// undo one.
func TestACompatibleRollbackToAnOlderBuildIsAllowed(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{version: "1.171"})
	f.addRelease("1.180", script{version: "1.180"})
	f.require("1.180", "1.0")
	f.compatibility.Serial = 2
	f.publish()

	if _, err := f.resolver(nil).Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 1.180 turned out to be bad. A new manifest, at a higher serial, says so.
	f.require("1.171", "1.0")
	f.compatibility.Serial = 3
	f.publish()

	runner, err := f.resolver(nil).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve after rollback: %v", err)
	}
	if runner.Provenance().Version != "1.171" {
		t.Errorf("after rollback = %s, want 1.171", runner.Provenance().Version)
	}
	stdout, err := runner.Run(context.Background(), "version")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdout)) != "1.171" {
		t.Errorf("the rolled-back build reports %q", stdout)
	}
}

// OFFLINE cached execution. No network at all: the requirement comes from the
// pin this machine recorded when it last verified one, the executable comes
// from the cache, and the protocol check still runs against the recorded
// minimum. Offline means no network and no fewer checks.
func TestOfflineRunsTheCachedBuildAndStillChecksTheProtocol(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.require("1.171", "1.0")
	f.publish()

	if _, err := f.resolver(nil).Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := f.downloads

	offline := f.resolver(func(r *aue.Resolver, o *acquire.Options) {
		r.Offline, r.Install = true, false
		o.Offline = true
		// The server is gone as far as this resolver is concerned: an offline
		// run that reached it would be a test that proved nothing.
		o.CatalogURL = ""
	})
	runner, err := offline.Resolve(context.Background())
	if err != nil {
		t.Fatalf("offline Resolve: %v", err)
	}
	if !runner.Provenance().Verified || runner.Provenance().Version != "1.171" {
		t.Errorf("offline provenance = %+v", runner.Provenance())
	}
	if runner.Provenance().Protocol != "1.0" {
		t.Errorf("the offline path skipped the protocol handshake: %+v", runner.Provenance())
	}
	if f.downloads != before {
		t.Errorf("the offline path fetched something: %d downloads, was %d", f.downloads, before)
	}
}

// Offline with nothing recorded is a refusal that says what to do, not a guess.
func TestOfflineWithNoRecordedRequirementRefuses(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.require("1.171", "1.0")
	f.publish()

	offline := f.resolver(func(r *aue.Resolver, o *acquire.Options) {
		r.Offline, r.Install = true, false
		o.Offline = true
	})
	_, err := offline.Resolve(context.Background())
	if !errors.Is(err, catalog.ErrOffline) {
		t.Fatalf("err = %v, want catalog.ErrOffline", err)
	}
	if !strings.Contains(err.Error(), "run once with the network") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
}

// A build whose contract this Companion cannot drive is refused BEFORE it is
// used for anything. A later major is not a newer version of an earlier one.
func TestAnIncompatibleProtocolIsRefusedBeforeTheBuildIsUsed(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{protocol: "2.0"})
	f.require("1.171", "1.0")
	f.publish()

	_, err := f.resolver(nil).Resolve(context.Background())
	if err == nil {
		t.Fatal("a build speaking protocol 2.0 was accepted under a 1.0 requirement")
	}
	if !containsAll(err.Error(), "2.0", "1.0", "different contracts") {
		t.Errorf("the refusal does not name both numbers: %v", err)
	}
}

// And the same in the other direction: a build too old for what the manifest
// requires.
func TestABuildOlderThanTheRequiredMinorIsRefused(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{protocol: "1.0"})
	f.require("1.171", "1.4")
	f.publish()

	if _, err := f.resolver(nil).Resolve(context.Background()); err == nil {
		t.Fatal("a build speaking 1.0 was accepted under a 1.4 requirement")
	}
}

// The extractor is a subprocess and a subprocess can produce anything. A
// handshake that decoded a partial document would leave the Companion acting on
// a protocol number it invented.
func TestMalformedProtocolOutputIsRefused(t *testing.T) {
	for name, answer := range map[string]string{
		"not JSON at all":      "this is not json",
		"a truncated document": `{"schema_version":"aue-invocation-protocol/1.0","protocol":`,
		"two documents":        `{"protocol":"1.0"} {"protocol":"9.9"}`,
		"only whitespace":      " ",
	} {
		f := newFixture(t)
		f.addRelease("1.171", script{malformedProtocol: answer})
		f.require("1.171", "1.0")
		f.publish()

		_, err := f.resolver(nil).Resolve(context.Background())
		if err == nil {
			t.Errorf("%s: accepted", name)

			continue
		}
		if !errors.Is(err, aue.ErrOutputNotJSON) && !strings.Contains(err.Error(), "declares no invocation protocol") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// A crash is surfaced with its exit code and its stderr, not flattened. The
// extractor's own contract publishes the table: 1 is the operation failing and
// 2 is the invocation being wrong, and those go to different people.
func TestACrashKeepsItsExitCodeAndItsStderr(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{behaviour: "echo 'the map is not a map' >&2\nexit 1\n"})
	f.require("1.171", "1.0")
	f.publish()

	runner, err := f.resolver(nil).Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	_, err = runner.Run(context.Background(), "summarize", "broken.map")
	var exitErr *aue.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %T %v, want *aue.ExitError", err, err)
	}
	if exitErr.ExitCode != 1 || exitErr.Subcommand != "summarize" {
		t.Errorf("exit = %+v", exitErr)
	}
	if exitErr.Stderr != "the map is not a map" {
		t.Errorf("stderr = %q", exitErr.Stderr)
	}
}

// A platform this release does not publish is a refusal that names it, and
// nothing is downloaded or run.
func TestAPlatformTheManifestDoesNotCoverIsRefused(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.require("1.171", "1.0", "plan9/mips")
	f.publish()

	_, err := f.resolver(nil).Resolve(context.Background())
	var missing *catalog.ErrNoRequirement
	if !errors.As(err, &missing) {
		t.Fatalf("err = %T %v, want *catalog.ErrNoRequirement", err, err)
	}
	if missing.Platform != here() {
		t.Errorf("the refusal names %s and this machine is %s", missing.Platform, here())
	}
	if f.downloads != 0 {
		t.Errorf("something was downloaded for a platform with no rule: %d", f.downloads)
	}
}

// A catalogue with no compatibility manifest cannot say which build goes with
// this Companion, and the Companion does not choose one for itself.
func TestACatalogueWithNoCompatibilityManifestRefusesRatherThanGuessing(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.serveCompatibility = false
	f.publish()

	_, err := f.resolver(nil).Resolve(context.Background())
	if !errors.Is(err, aue.ErrNoCompatibilityManifest) {
		t.Fatalf("err = %v, want ErrNoCompatibilityManifest", err)
	}
	if f.downloads != 0 {
		t.Errorf("something was downloaded with nothing saying which build to install: %d", f.downloads)
	}
}

// A withdrawn build is refused on every later use, including one that is
// already installed. Revocation is by digest and it is sticky.
func TestARevokedBuildIsRefusedEvenWhenItIsAlreadyInstalled(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.require("1.171", "1.0")
	f.publish()

	if _, err := f.resolver(nil).Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}

	f.document.Serial = 2
	f.document.Revocations = []catalog.Revocation{{
		Digest: f.digestOf("1.171"), Reason: "a bad build", At: testNow,
	}}
	f.publish()

	_, err := f.resolver(nil).Resolve(context.Background())
	if !errors.Is(err, catalog.ErrRevoked) {
		t.Fatalf("err = %v, want catalog.ErrRevoked", err)
	}
}

// An edited cache entry is not run. `Use` re-hashes the executables against the
// install record on every use, which is exactly the case "verify at install"
// misses.
func TestAnEditedCacheEntryIsRefused(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.require("1.171", "1.0")
	f.publish()

	runner, err := f.resolver(nil).Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(runner.Path(), []byte("#!/bin/sh\necho pwned\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err = f.resolver(nil).Resolve(context.Background()); !errors.Is(err, acquire.ErrTampered) {
		t.Fatalf("err = %v, want acquire.ErrTampered", err)
	}
}

// The developer override is the ONE unverified path, it is taken only because
// the user set an environment variable, and everything that shows an extractor
// shows that nothing verified it.
func TestTheDeveloperOverrideIsUnverifiedAndSaysSo(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "my-aue")
	if err := os.WriteFile(path, []byte(script{version: "9.9-local"}.body()), 0o700); err != nil {
		t.Fatal(err)
	}

	resolver := f.resolver(func(r *aue.Resolver, _ *acquire.Options) { r.Override = path })
	runner, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	record := runner.Provenance()
	if record.Verified || record.Mode != aue.ModeDeveloperOverride {
		t.Fatalf("provenance = %+v", record)
	}
	if !strings.Contains(strings.Join(record.Describe(), "\n"), "UNVERIFIED") {
		t.Errorf("the description does not say it is unverified: %v", record.Describe())
	}
	if record.Digest != "" || record.Signer != "" || record.Version != "" {
		t.Errorf("an override carries managed facts it has no right to: %+v", record)
	}
	if f.downloads != 0 {
		t.Errorf("the override path fetched something: %d", f.downloads)
	}

	status := resolver.Status()
	if status.Verified || status.Mode != aue.ModeDeveloperOverride || status.Note == "" {
		t.Errorf("status = %+v", status)
	}
}

// And a failed managed resolution NEVER becomes an override. That is the
// property that makes "there are exactly two ways" true rather than aspirational.
func TestAFailedManagedResolutionDoesNotFallBackToAnything(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.serveCompatibility = false
	f.publish()

	// An override EXISTS on this machine, and is not configured for this
	// resolver. Nothing may reach for it.
	path := filepath.Join(t.TempDir(), "my-aue")
	if err := os.WriteFile(path, []byte(script{}.body()), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := f.resolver(nil).Resolve(context.Background()); err == nil {
		t.Fatal("a managed resolution with no compatibility manifest succeeded")
	}
}

// When the artifact is served by a backend that authorizes downloads, the hook
// rewrites the URL and nothing else changes: the size, the digest and the
// signature chain are checked exactly as they are for a public URL.
func TestAnAuthorizedDownloadVerifiesExactlyAsAPublicOneDoes(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.require("1.171", "1.0")
	f.publish()

	resolver := f.resolver(func(_ *aue.Resolver, o *acquire.Options) { f.authorizing(o) })
	runner, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(f.authorized) != 1 {
		t.Fatalf("the authorize hook was called %d times", len(f.authorized))
	}
	record := runner.Provenance()
	if !record.Verified || record.Digest != f.digestOf("1.171") {
		t.Errorf("an authorized download produced a different verification result: %+v", record)
	}
	// And the recorded source URL carries no credential: RedactURL strips the
	// query, which is where an authorization lives.
	install, _, err := resolver.Acquirer.Use(record.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(install.SourceURL, "grant") {
		t.Errorf("the install record holds the authorization: %q", install.SourceURL)
	}
}

// And a backend that will not authorize the download is a refusal that says so,
// with nothing installed. This is the signed-out case: the artifact is hosted
// by a backend, the hook is installed whatever the session state, and the error
// names the backend rather than letting a route that only exists behind a grant
// answer 404 to a direct fetch.
func TestABackendThatWillNotAuthorizeIsARefusalAndInstallsNothing(t *testing.T) {
	f := newFixture(t)
	f.addRelease("1.171", script{})
	f.require("1.171", "1.0")
	f.publish()

	resolver := f.resolver(func(_ *aue.Resolver, o *acquire.Options) {
		o.Authorize = func(context.Context, catalog.Artifact) (string, error) {
			return "", errors.New("this Companion is not signed in; run `companion auth login` first")
		}
	})
	_, err := resolver.Resolve(context.Background())
	if err == nil {
		t.Fatal("an unauthorized download succeeded")
	}
	if !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("err = %v", err)
	}
	if f.downloads != 0 {
		t.Errorf("something was fetched anyway: %d", f.downloads)
	}
	if installed, findErr := resolver.Acquirer.FindInstalled(aue.ComponentID, "1.171"); findErr == nil && installed != nil {
		t.Error("a refused download left a cache entry")
	}
}
