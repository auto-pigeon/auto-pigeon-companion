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
	// Companion and Extractor are the two programs; default the compiled
	// fixture program.
	Companion, Extractor []byte
	// ExtractorVersion is what the bundle carries (default 1.245);
	// ReleaseExtractorVersion what the release manifest names (default the
	// same): differing is a wrong Companion/extractor pair.
	ExtractorVersion        string
	ReleaseExtractorVersion string
	// Schema overrides the release manifest's; NoAUE drops its extractor.
	Schema string
	NoAUE  bool
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
	if spec.ExtractorVersion == "" {
		spec.ExtractorVersion = "1.245"
	}
	if spec.Companion == nil {
		spec.Companion = FixtureProgram(t)
	}
	if spec.Extractor == nil {
		spec.Extractor = FixtureProgram(t)
	}
	manifestPath, companion := managedtools.BundleLayout(spec.Platform)
	suffix := ""
	if strings.HasPrefix(spec.Platform, "windows-") {
		suffix = ".exe"
	}
	extractor := "dependencies/auto-pigeon-extractor" + suffix
	sums := sum(spec.Companion) + "  " + strings.TrimPrefix(companion, "./") + "\n"
	entries := map[string][]byte{
		companion:    spec.Companion,
		extractor:    spec.Extractor,
		"SHA256SUMS": []byte(sums),
	}
	member := func(path, product, version string) map[string]any {
		return map[string]any{"path": path, "product": product, "version": version, "platform": spec.Platform,
			"size": len(entries[path]), "sha256": "sha256:" + sum(entries[path])}
	}
	manifest := map[string]any{
		"schema_version": "aucom.bundle-manifest/1.2",
		"product":        "auto-pigeon-companion",
		"version":        spec.Version,
		"platform":       spec.Platform,
		"companion":      companion,
		"members": []any{
			member("SHA256SUMS", "auto-pigeon-companion", spec.Version),
			member(companion, "auto-pigeon-companion", spec.Version),
			member(extractor, "auto-pigeon-extractor", spec.ExtractorVersion),
		},
		"extractor": map[string]any{
			"product": "auto-pigeon-extractor", "version": spec.ExtractorVersion, "platform": spec.Platform,
			"file": extractor, "license": "LicenseRef-Auto-Pigeon-Proprietary",
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
	if spec.ExtractorVersion == "" {
		spec.ExtractorVersion = "1.245"
	}
	if spec.ReleaseExtractorVersion == "" {
		spec.ReleaseExtractorVersion = spec.ExtractorVersion
	}
	name := CompanionArchiveName(spec.Version, spec.Platform)
	root := strings.TrimSuffix(name, ".zip")
	archive := Zip(t, root, CompanionEntries(t, spec), nil)
	schema := spec.Schema
	if schema == "" {
		schema = "aucom.release-manifest/1.0"
	}
	manifest := map[string]any{
		"schema": schema, "product": "auto-pigeon-companion", "version": spec.Version, "tag": "v" + spec.Version,
		"commit": strings.Repeat("a", 40), "prerelease": spec.Prerelease, "signed": false,
		"targets": []any{map[string]any{
			"target": strings.Replace(spec.Platform, "-", "/", 1), "verdict": spec.Verdict, "reason": "fixture",
			"aucom_state": spec.State, "aue_status": "published", "native_acceptance_in_ci": true,
			"archive": name, "size": len(archive), "sha256": "sha256:" + sum(archive),
		}},
	}
	if !spec.NoAUE {
		manifest["aue"] = map[string]any{"repository": "auto-pigeon/auto-pigeon-extractor", "commit": strings.Repeat("d", 40),
			"version": spec.ReleaseExtractorVersion, "protocol": "1.0", "license": "LicenseRef-Auto-Pigeon-Proprietary"}
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	return &Release{Tag: "v" + spec.Version, Prerelease: spec.Prerelease, Draft: spec.Draft, Files: map[string][]byte{
		name: archive, managedtools.ManifestAssetName: encoded, "SHA256SUMS": []byte(sum(archive) + "  " + name + "\n"),
	}}
}
