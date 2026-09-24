package aue_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
)

// bundle is a release directory: a fake extractor beside where the Companion
// would be, and — when listed — a bundle manifest naming it with its digest.
type bundle struct {
	t   *testing.T
	dir string
}

// protocolAnswer is what the fake says to `protocol --json`.
func protocolAnswer(protocol string) string {
	return `{"schema_version":"aue-invocation-protocol/1.0","protocol":"` + protocol + `","version":"0.9.0",` +
		`"license":{"spdx":"AGPL-3.0-only","corresponding_source":"https://example.org/aue-src"}}`
}

// newBundle writes the extractor as a shell script: `protocol` prints answer,
// anything else runs behaviour.
func newBundle(t *testing.T, answer, behaviour string) *bundle {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake extractor is a shell script")
	}
	b := &bundle{t: t, dir: t.TempDir()}
	script := "#!/bin/sh\nif [ \"$1\" = protocol ]; then\ncat <<'JSON'\n" + answer + "\nJSON\nexit 0\nfi\n" + behaviour
	if err := os.WriteFile(b.path(), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return b
}

func (b *bundle) path() string {
	return filepath.Join(b.dir, "auto-pigeon-extractor")
}

// list writes the bundle manifest with this digest for the extractor ("" means
// its real one).
func (b *bundle) list(digest string) {
	b.t.Helper()
	if digest == "" {
		data, err := os.ReadFile(b.path())
		if err != nil {
			b.t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	manifest := map[string]any{
		"schema_version": "aucom.bundle-manifest/1.0",
		"members": []map[string]any{
			{"path": "auto-pigeon-extractor", "product": "auto-pigeon-extractor", "sha256": digest},
		},
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(b.dir, aue.BundleManifestName), data, 0o600); err != nil {
		b.t.Fatal(err)
	}
}

func (b *bundle) resolver() *aue.Resolver { return &aue.Resolver{Dir: b.dir} }

// The release case: the extractor beside the Companion, listed with its digest,
// speaking this build's protocol. It is verified, and says what it is.
func TestABundledExtractorListedInTheManifestIsVerified(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "echo ok\n")
	b.list("")

	status := b.resolver().Status()
	if !status.Available || !status.Verified || status.Mode != aue.ModeBundled {
		t.Fatalf("status = %+v", status)
	}
	runner, err := b.resolver().Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	provenance := runner.Provenance()
	if !provenance.Verified || provenance.Protocol != "1.0" || provenance.Version != "0.9.0" ||
		provenance.License != "AGPL-3.0-only" || !strings.HasPrefix(provenance.Digest, "sha256:") {
		t.Errorf("provenance = %+v", provenance)
	}
	out, err := runner.Run(context.Background(), "summarize")
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("run = %q, %v", out, err)
	}
}

// A file the manifest does not describe is not the one the release shipped.
func TestABundledExtractorWhoseDigestDisagreesIsRefused(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "")
	b.list("sha256:" + strings.Repeat("0", 64))

	if status := b.resolver().Status(); status.Available {
		t.Errorf("status = %+v, want unavailable", status)
	}
	if _, err := b.resolver().Resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "bundle manifest lists") {
		t.Fatalf("err = %v, want a digest refusal", err)
	}
}

// No manifest — a development tree — still runs, still handshakes, and says
// nothing vouched for its bytes.
func TestAnUnlistedBundledExtractorRunsAndSaysSo(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "")

	runner, err := b.resolver().Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if provenance := runner.Provenance(); provenance.Verified || provenance.Note != aue.UnlistedNote {
		t.Errorf("provenance = %+v", provenance)
	}
}

// Nothing beside the Companion is an error naming what to do, and the override
// is never taken as a fallback for it.
func TestNoBundledExtractorIsASentenceNamingTheOverride(t *testing.T) {
	resolver := &aue.Resolver{Dir: t.TempDir()}
	_, err := resolver.Resolve(context.Background())
	if !errors.Is(err, aue.ErrNoExtractor) || !strings.Contains(err.Error(), aue.EnvBinaryOverride) {
		t.Fatalf("err = %v", err)
	}
	if status := resolver.Status(); status.Available || status.Reason == "" {
		t.Errorf("status = %+v", status)
	}
}

func TestAnIncompatibleProtocolIsRefusedBeforeTheBuildIsUsed(t *testing.T) {
	for _, protocol := range []string{"2.0", "0.9"} {
		b := newBundle(t, protocolAnswer(protocol), "")
		if _, err := b.resolver().Resolve(context.Background()); err == nil {
			t.Errorf("protocol %s: accepted", protocol)
		}
	}
}

func TestMalformedProtocolOutputIsRefused(t *testing.T) {
	for name, answer := range map[string]string{
		"not JSON at all":      "this is not json",
		"a truncated document": `{"schema_version":"aue-invocation-protocol/1.0","protocol":`,
		"two documents":        `{"protocol":"1.0"} {"protocol":"9.9"}`,
		"only whitespace":      " ",
	} {
		b := newBundle(t, answer, "")
		_, err := b.resolver().Resolve(context.Background())
		if err == nil {
			t.Errorf("%s: accepted", name)

			continue
		}
		if !errors.Is(err, aue.ErrOutputNotJSON) && !strings.Contains(err.Error(), "declares no invocation protocol") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestACrashKeepsItsExitCodeAndItsStderr(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "echo 'the map is not a map' >&2\nexit 1\n")
	runner, err := b.resolver().Resolve(context.Background())
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

// The override wins over a bundled copy, is not handshaken, and says it is
// unverified everywhere it is described.
func TestTheDeveloperOverrideIsUnverifiedAndSaysSo(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "")
	b.list("")
	override := newBundle(t, "not json", "")

	resolver := b.resolver()
	resolver.Override = override.path()
	runner, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	provenance := runner.Provenance()
	if provenance.Verified || provenance.Mode != aue.ModeDeveloperOverride || provenance.Path != override.path() {
		t.Errorf("provenance = %+v", provenance)
	}
	if lines := provenance.Describe(); len(lines) == 0 || !strings.Contains(lines[0], "UNVERIFIED") {
		t.Errorf("describe = %q", lines)
	}
}

func TestProtocolSatisfiesIsSameMajorAndAtLeastTheMinor(t *testing.T) {
	for _, c := range []struct {
		speaks, minimum string
		ok              bool
	}{
		{"1.0", "1.0", true}, {"1.3", "1.2", true}, {"1.1", "1.2", false},
		{"2.0", "1.0", false}, {"1", "1.0", false}, {"1.01", "1.0", false},
	} {
		if err := aue.ProtocolSatisfies(c.speaks, c.minimum); (err == nil) != c.ok {
			t.Errorf("%s vs %s: err = %v", c.speaks, c.minimum, err)
		}
	}
}

// A bundle manifest that is there and cannot be read vouches for nothing, and
// is a refusal rather than a quiet fall-back to "unlisted".
func TestAnUnreadableBundleManifestIsARefusal(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "")
	if err := os.WriteFile(filepath.Join(b.dir, aue.BundleManifestName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.resolver().Resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "bundle manifest") {
		t.Fatalf("err = %v, want a refusal naming the bundle manifest", err)
	}
	if status := b.resolver().Status(); status.Available {
		t.Errorf("status = %+v, want unavailable", status)
	}
}

// On macOS the Companion runs from inside its .app, so the extractor is in
// `Contents/MacOS/` beside it and the manifest is in `Contents/Resources/`,
// listing the extractor by its path from the archive root. A digest that
// agrees verifies it; one that does not refuses it — the same two answers as
// on every other platform (NEW_247A).
func TestAnExtractorInsideAMacOSAppIsCheckedAgainstTheAppsManifest(t *testing.T) {
	for _, tampered := range []bool{false, true} {
		b := newBundle(t, protocolAnswer("1.0"), "echo ok\n")
		app := filepath.Join(b.dir, "Auto-Pigeon Companion.app")
		macos := filepath.Join(app, "Contents", "MacOS")
		resources := filepath.Join(app, "Contents", "Resources")
		for _, dir := range []string{macos, resources} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Rename(b.path(), filepath.Join(macos, "auto-pigeon-extractor")); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(macos, "auto-pigeon-extractor"))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if tampered {
			digest = "sha256:" + strings.Repeat("1", 64)
		}
		manifest, _ := json.Marshal(map[string]any{"members": []map[string]any{{
			"path":    "Auto-Pigeon Companion.app/Contents/MacOS/auto-pigeon-extractor",
			"product": "auto-pigeon-extractor", "sha256": digest,
		}}})
		if err := os.WriteFile(filepath.Join(resources, aue.BundleManifestName), manifest, 0o600); err != nil {
			t.Fatal(err)
		}

		status := (&aue.Resolver{Dir: macos}).Status()
		if tampered {
			if status.Available || !strings.Contains(status.Reason, "not the extractor this release shipped") {
				t.Errorf("tampered: status = %+v, want a digest refusal", status)
			}
			continue
		}
		if !status.Available || !status.Verified {
			t.Errorf("status = %+v, want verified", status)
		}
	}
}
