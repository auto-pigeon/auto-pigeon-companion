package managedtoolstest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// CompanionRepo is the Companion's own repository, as the stand-in serves it.
const CompanionRepo = "auto-pigeon/auto-pigeon-companion"

// CompanionSpec describes one Companion release in the real release and
// bundle manifest formats (NEW_368).
type CompanionSpec struct {
	Version string
	// Platform defaults to this machine's bundle platform (linux-amd64).
	Platform   string
	Prerelease bool
	Draft      bool
	// Verdict defaults to bundled_release; State to native_pass.
	Verdict string
	State   string
	// Companion and Converter are the two programs; default the compiled
	// fixture program.
	Companion, Converter []byte
	// ConverterVersion is what the bundle carries (default 1.245);
	// ReleaseConverterVersion what the release manifest names (default the
	// same): differing is a wrong Companion/converter pair.
	ConverterVersion        string
	ReleaseConverterVersion string
	// Schema overrides the release manifest's; NoConverter drops its converter.
	Schema      string
	NoConverter bool
	// Bundle changes the bundle manifest before it is written; Archive the
	// archive's entries after (names relative to the archive's root folder).
	Bundle  func(manifest map[string]any)
	Archive func(entries map[string][]byte) map[string][]byte
}

// ThisBundlePlatform is this machine's release bundle platform.
func ThisBundlePlatform() string { return managedtools.BundlePlatform(runtime.GOOS, runtime.GOARCH) }

// CompanionArchiveName is a Companion release archive's name.
func CompanionArchiveName(version, platform string) string {
	return "auto-pigeon-companion-" + version + "-" + platform + ".zip"
}

func sum(b []byte) string {
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:])
}

// CompanionEntries are a bundle's files, relative to its root folder, with
// its bundle manifest — what an unpacked release contains.
func CompanionEntries(t testing.TB, spec CompanionSpec) map[string][]byte {
	t.Helper()
	if spec.Platform == "" {
		spec.Platform = ThisBundlePlatform()
	}
	if spec.ConverterVersion == "" {
		spec.ConverterVersion = "1.245"
	}
	if spec.Companion == nil {
		spec.Companion = FixtureProgram(t)
	}
	if spec.Converter == nil {
		spec.Converter = FixtureProgram(t)
	}
	manifestPath, companion := managedtools.BundleLayout(spec.Platform)
	suffix := ""
	if strings.HasPrefix(spec.Platform, "windows-") {
		suffix = ".exe"
	}
	converter := "dependencies/auto-pigeon-mapio" + suffix
	sums := sum(spec.Companion) + "  " + strings.TrimPrefix(companion, "./") + "\n"
	entries := map[string][]byte{
		companion:    spec.Companion,
		converter:    spec.Converter,
		"SHA256SUMS": []byte(sums),
	}
	member := func(path, product, version string) map[string]any {
		return map[string]any{"path": path, "product": product, "version": version, "platform": spec.Platform,
			"size": len(entries[path]), "sha256": "sha256:" + sum(entries[path])}
	}
	manifest := map[string]any{
		"schema_version": "aucom.bundle-manifest/2.0",
		"product":        "auto-pigeon-companion",
		"version":        spec.Version,
		"platform":       spec.Platform,
		"companion":      companion,
		"members": []any{
			member("SHA256SUMS", "auto-pigeon-companion", spec.Version),
			member(companion, "auto-pigeon-companion", spec.Version),
			member(converter, "auto-pigeon-mapio", spec.ConverterVersion),
		},
		"converter": map[string]any{
			"product": "auto-pigeon-mapio", "version": spec.ConverterVersion, "platform": spec.Platform,
			"file": converter, "license": "Apache-2.0 AND MIT",
			"source_commit": strings.Repeat("d", 40),
		},
	}
	if spec.Bundle != nil {
		spec.Bundle(manifest)
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	entries[manifestPath] = encoded
	if spec.Archive != nil {
		entries = spec.Archive(entries)
	}
	return entries
}

// BuildCompanion makes a Companion release: its archive for one platform and
// its release-manifest.json.
func BuildCompanion(t testing.TB, spec CompanionSpec) *Release {
	t.Helper()
	if spec.Platform == "" {
		spec.Platform = ThisBundlePlatform()
	}
	if spec.Verdict == "" {
		spec.Verdict = "bundled_release"
	}
	if spec.State == "" {
		spec.State = "native_pass"
	}
	if spec.ConverterVersion == "" {
		spec.ConverterVersion = "1.245"
	}
	if spec.ReleaseConverterVersion == "" {
		spec.ReleaseConverterVersion = spec.ConverterVersion
	}
	name := CompanionArchiveName(spec.Version, spec.Platform)
	root := strings.TrimSuffix(name, ".zip")
	archive := Zip(t, root, CompanionEntries(t, spec), nil)
	schema := spec.Schema
	if schema == "" {
		schema = "aucom.release-manifest/2.0"
	}
	manifest := map[string]any{
		"schema": schema, "product": "auto-pigeon-companion", "version": spec.Version, "tag": "v" + spec.Version,
		"commit": strings.Repeat("a", 40), "prerelease": spec.Prerelease, "signed": false,
		"targets": []any{map[string]any{
			"target": strings.Replace(spec.Platform, "-", "/", 1), "verdict": spec.Verdict, "reason": "fixture",
			"aucom_state": spec.State, "converter_status": "published", "native_acceptance_in_ci": true,
			"archive": name, "size": len(archive), "sha256": "sha256:" + sum(archive),
		}},
	}
	if !spec.NoConverter {
		manifest["converter"] = map[string]any{"program": "auto-pigeon-mapio", "commit": strings.Repeat("d", 40),
			"version": spec.ReleaseConverterVersion, "protocol": "1.0", "license": "Apache-2.0 AND MIT"}
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	return &Release{Tag: "v" + spec.Version, Prerelease: spec.Prerelease, Draft: spec.Draft, Files: map[string][]byte{
		name: archive, managedtools.ManifestAssetName: encoded, "SHA256SUMS": []byte(sum(archive) + "  " + name + "\n"),
	}}
}
