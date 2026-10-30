package converter_test

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

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
)

// bundle is a release directory: a fake converter beside where the Companion
// would be, and — when listed — a bundle manifest naming it with its digest.
type bundle struct {
	t   *testing.T
	dir string
}

// protocolAnswer is what the fake says to `protocol --json`: what cmd/mapio prints.
func protocolAnswer(protocol string) string {
	return `{"schema_version":"auto-pigeon-mapio-protocol/1.0","program":"auto-pigeon-mapio","protocol":"` + protocol + `",` +
		`"version":"0.9.0","license":{"spdx":"Apache-2.0 AND MIT","source":"https://example.org/mapio-src"},` +
		`"library":{"version":"v0.0.0-20261030000000-abcdefabcdef"},` +
		`"capabilities":{"directions":["apmap-to-q1map","apmap-to-q2map","apmap-to-q3map"],` +
		`"apmap_current":"1.5","apmap_readable":["1.4","1.5"]}}`
}

// newBundle writes the converter as a shell script: `protocol` prints answer,
// anything else runs behaviour.
func newBundle(t *testing.T, answer, behaviour string) *bundle {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake converter is a shell script")
	}
	b := &bundle{t: t, dir: t.TempDir()}
	if err := os.MkdirAll(b.deps(), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = protocol ]; then\ncat <<'JSON'\n" + answer + "\nJSON\nexit 0\nfi\n" + behaviour
	if err := os.WriteFile(b.path(), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return b
}

// deps is the folder beside the Companion a release puts the converter and its
// manifest in.
func (b *bundle) deps() string {
	return filepath.Join(b.dir, converter.DependenciesDir)
}

func (b *bundle) path() string {
	return filepath.Join(b.deps(), "auto-pigeon-mapio")
}

// list writes the bundle manifest with this digest for the converter ("" means
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
			{"path": converter.DependenciesDir + "/auto-pigeon-mapio", "product": "auto-pigeon-mapio", "sha256": digest},
		},
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(b.deps(), converter.BundleManifestName), data, 0o600); err != nil {
		b.t.Fatal(err)
	}
}

func (b *bundle) resolver() *converter.Resolver { return &converter.Resolver{Dir: b.dir} }

// The release case: the converter beside the Companion, listed with its digest,
// speaking this build's protocol. It is verified, and says what it is.
func TestABundledConverterListedInTheManifestIsVerified(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "echo ok\n")
	b.list("")

	status := b.resolver().Status()
	if !status.Available || !status.Verified || status.Mode != converter.ModeBundled {
		t.Fatalf("status = %+v", status)
	}
	runner, err := b.resolver().Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	provenance := runner.Provenance()
	if !provenance.Verified || provenance.Protocol != "1.0" || provenance.Version != "0.9.0" ||
		provenance.License != "Apache-2.0 AND MIT" || !strings.HasPrefix(provenance.Digest, "sha256:") {
		t.Errorf("provenance = %+v", provenance)
	}
	out, err := runner.Run(context.Background(), "summarize")
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("run = %q, %v", out, err)
	}
}

// The handshake reports what the converter says it is: the licences, the library compiled into it and
// the APMap versions it speaks, so a build records them rather than assuming them.
func TestTheHandshakeRecordsWhatTheConverterIs(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "echo ok\n")
	b.list("")

	runner, err := b.resolver().Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	provenance := runner.Provenance()
	if provenance.Program != "auto-pigeon-mapio" || provenance.License != "Apache-2.0 AND MIT" ||
		provenance.Library == "" || provenance.APMapCurrent != "1.5" || len(provenance.APMapReadable) != 2 {
		t.Errorf("provenance = %+v", provenance)
	}
}

// The file at the converter's path answers to its identity, not to a protocol number. A full
// Auto-Pigeon Extractor speaks a protocol 1.x too, and it must not pass for a converter, verified or not.
func TestAnotherProgramAtTheConvertersPathIsRefused(t *testing.T) {
	extractor := `{"schema_version":"aue-invocation-protocol/1.0","protocol":"1.0","version":"1.300",` +
		`"license":{"spdx":"LicenseRef-Auto-Pigeon-Proprietary"}}`
	for name, list := range map[string]bool{"listed": true, "unlisted": false} {
		b := newBundle(t, extractor, "echo ran\n")
		if list {
			b.list("")
		}
		_, err := b.resolver().Resolve(context.Background())
		if err == nil || !strings.Contains(err.Error(), "calls itself") {
			t.Errorf("%s: an extractor passed the handshake: %v", name, err)
		}
	}
}

func TestAConverterMissingADirectionABuildNeedsIsRefused(t *testing.T) {
	answer := strings.Replace(protocolAnswer("1.0"), `"apmap-to-q3map"`, `"apmap-to-q9map"`, 1)
	b := newBundle(t, answer, "")
	if _, err := b.resolver().Resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "apmap-to-q3map") {
		t.Fatalf("a converter without a direction was accepted: %v", err)
	}
}

func TestAConverterNamingNoAPMapVersionIsRefused(t *testing.T) {
	answer := strings.Replace(protocolAnswer("1.0"), `"apmap_current":"1.5"`, `"apmap_current":""`, 1)
	b := newBundle(t, answer, "")
	if _, err := b.resolver().Resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "APMap") {
		t.Fatalf("a converter naming no APMap version was accepted: %v", err)
	}
}

// A setting that used to name a full extractor is a message, never a path.
func TestARetiredExtractorOverrideIsNeverRun(t *testing.T) {
	t.Setenv(converter.EnvBinaryOverride, "")
	for _, name := range converter.RetiredEnvBinaryOverrides {
		t.Setenv(name, "/some/auto-pigeon-extractor")
		if got := converter.OverridePath(); got != "" {
			t.Errorf("%s became the override: %q", name, got)
		}
		note := converter.RetiredOverrideNote()
		if !strings.Contains(note, name) || !strings.Contains(note, converter.EnvBinaryOverride) {
			t.Errorf("%s: note %q", name, note)
		}
		t.Setenv(name, "")
	}
	if note := converter.RetiredOverrideNote(); note != "" {
		t.Errorf("no retired setting, and a note: %q", note)
	}
}

func TestATerminalRecordIsReadAndAHistoricOneStillIs(t *testing.T) {
	for _, marker := range []string{converter.TerminalMarker, converter.HistoricTerminalMarker} {
		record, ok := converter.ReadTerminal("exit 1\n" + marker + `{"reason":"quake3_map","message":"refused","detail":"the shader."}`)
		if !ok || record.Reason != "quake3_map" || record.Sentence() != "refused: the shader." {
			t.Errorf("%s: %+v, %v", marker, record, ok)
		}
	}
}

// A file the manifest does not describe is not the one the release shipped.
func TestABundledConverterWhoseDigestDisagreesIsRefused(t *testing.T) {
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
func TestAnUnlistedBundledConverterRunsAndSaysSo(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "")

	runner, err := b.resolver().Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if provenance := runner.Provenance(); provenance.Verified || provenance.Note != converter.UnlistedNote {
		t.Errorf("provenance = %+v", provenance)
	}
}

// Nothing beside the Companion is an error naming what to do, and the override
// is never taken as a fallback for it.
func TestNoBundledConverterIsASentenceNamingTheOverride(t *testing.T) {
	resolver := &converter.Resolver{Dir: t.TempDir()}
	_, err := resolver.Resolve(context.Background())
	if !errors.Is(err, converter.ErrNoConverter) || !strings.Contains(err.Error(), converter.EnvBinaryOverride) {
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
		"a truncated document": `{"schema_version":"auto-pigeon-mapio-protocol/1.0","protocol":`,
		"two documents":        `{"program":"auto-pigeon-mapio","protocol":"1.0"} {"protocol":"9.9"}`,
		"only whitespace":      " ",
	} {
		b := newBundle(t, answer, "")
		_, err := b.resolver().Resolve(context.Background())
		if err == nil {
			t.Errorf("%s: accepted", name)

			continue
		}
		if !errors.Is(err, converter.ErrOutputNotJSON) && !strings.Contains(err.Error(), "declares no invocation protocol") {
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
	var exitErr *converter.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %T %v, want *converter.ExitError", err, err)
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
	if provenance.Verified || provenance.Mode != converter.ModeDeveloperOverride || provenance.Path != override.path() {
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
		if err := converter.ProtocolSatisfies(c.speaks, c.minimum); (err == nil) != c.ok {
			t.Errorf("%s vs %s: err = %v", c.speaks, c.minimum, err)
		}
	}
}

// A bundle manifest that is there and cannot be read vouches for nothing, and
// is a refusal rather than a quiet fall-back to "unlisted".
func TestAnUnreadableBundleManifestIsARefusal(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "")
	if err := os.WriteFile(filepath.Join(b.deps(), converter.BundleManifestName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.resolver().Resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "bundle manifest") {
		t.Fatalf("err = %v, want a refusal naming the bundle manifest", err)
	}
	if status := b.resolver().Status(); status.Available {
		t.Errorf("status = %+v, want unavailable", status)
	}
}

// On macOS the Companion runs from inside its .app, so the converter is in
// `Contents/MacOS/` beside it and the manifest is in `Contents/Resources/`,
// listing the converter by its path from the archive root. A digest that
// agrees verifies it; one that does not refuses it — the same two answers as
// on every other platform (NEW_247A).
func TestAnConverterInsideAMacOSAppIsCheckedAgainstTheAppsManifest(t *testing.T) {
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
		if err := os.Rename(b.path(), filepath.Join(macos, "auto-pigeon-mapio")); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(macos, "auto-pigeon-mapio"))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if tampered {
			digest = "sha256:" + strings.Repeat("1", 64)
		}
		manifest, _ := json.Marshal(map[string]any{"members": []map[string]any{{
			"path":    "Auto-Pigeon Companion.app/Contents/MacOS/auto-pigeon-mapio",
			"product": "auto-pigeon-mapio", "sha256": digest,
		}}})
		if err := os.WriteFile(filepath.Join(resources, converter.BundleManifestName), manifest, 0o600); err != nil {
			t.Fatal(err)
		}

		status := (&converter.Resolver{Dir: macos}).Status()
		if tampered {
			if status.Available || !strings.Contains(status.Reason, "not the converter this release shipped") {
				t.Errorf("tampered: status = %+v, want a digest refusal", status)
			}
			continue
		}
		if !status.Available || !status.Verified {
			t.Errorf("status = %+v, want verified", status)
		}
	}
}

// The converter is looked for in dependencies/ and only there: one left at the
// archive root, where releases before 1.160 put it, is not found, and nothing
// falls back to it.
func TestAnConverterBesideTheCompanionIsNotLookedFor(t *testing.T) {
	b := newBundle(t, protocolAnswer("1.0"), "echo ok\n")
	b.list("")
	if err := os.Rename(b.path(), filepath.Join(b.dir, "auto-pigeon-mapio")); err != nil {
		t.Fatal(err)
	}
	status := b.resolver().Status()
	if status.Available || !strings.Contains(status.Reason, filepath.Join(converter.DependenciesDir, "auto-pigeon-mapio")) {
		t.Fatalf("status = %+v; want unavailable, naming %s", status, filepath.Join(converter.DependenciesDir, "auto-pigeon-mapio"))
	}
}

// An older updater writes a migration bridge's converter as a plain file, because its schema names only
// the extractor as a program. The bridge's own manifest vouches for the bytes, so the Companion puts the
// bit back; nothing else ever gets that treatment.
func TestABridgesConverterGetsItsExecuteBitBackAndNothingElseDoes(t *testing.T) {
	write := func(t *testing.T, b *bundle, schema, kind, digest string) {
		t.Helper()
		document := map[string]any{"schema_version": schema, "members": []map[string]string{
			{"path": converter.DependenciesDir + "/auto-pigeon-mapio", "product": "auto-pigeon-mapio", "sha256": digest}}}
		if kind != "" {
			document["migration_bridge"] = map[string]string{"kind": kind}
		}
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(b.deps(), converter.BundleManifestName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(b.path(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	digestOf := func(t *testing.T, b *bundle) string {
		t.Helper()
		digest, err := converter.FileDigest(b.path())
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}

	t.Run("a bridge", func(t *testing.T) {
		b := newBundle(t, protocolAnswer("1.0"), "")
		write(t, b, "aucom.bundle-manifest/1.2", converter.BridgeKind, digestOf(t, b))
		resolver := &converter.Resolver{Dir: b.dir}
		runner, err := resolver.Resolve(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !runner.Provenance().Verified {
			t.Fatalf("provenance: %+v", runner.Provenance())
		}
	})
	for name, refused := range map[string]struct{ schema, kind, digest string }{
		"an ordinary bundle":     {"aucom.bundle-manifest/2.0", "", ""},
		"another bridge kind":    {"aucom.bundle-manifest/1.2", "something/else", ""},
		"bytes it does not list": {"aucom.bundle-manifest/1.2", converter.BridgeKind, "sha256:" + strings.Repeat("0", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBundle(t, protocolAnswer("1.0"), "")
			digest := refused.digest
			if digest == "" {
				digest = digestOf(t, b)
			}
			write(t, b, refused.schema, refused.kind, digest)
			resolver := &converter.Resolver{Dir: b.dir}
			if _, err := resolver.Resolve(context.Background()); err == nil {
				t.Fatal("a file that is not executable was run")
			}
			info, err := os.Stat(b.path())
			if err != nil || info.Mode().Perm() != 0o644 {
				t.Fatalf("its mode changed: %v %v", info.Mode(), err)
			}
		})
	}
}
