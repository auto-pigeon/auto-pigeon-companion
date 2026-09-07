package catalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
)

func releaseManifest(t *testing.T, edit ...func(map[string]any)) []byte {
	t.Helper()

	document := map[string]any{
		"schema_version": catalog.ReleaseManifestSchema,
		"product": map[string]any{
			"name": "Auto-Pigeon Extractor", "executable": "auto-pigeon-extractor",
			"repository": "https://example.invalid/aue",
		},
		"version":  "1.171",
		"protocol": "1.0",
		"license": map[string]any{
			"spdx": "AGPL-3.0-only", "name": "AGPL v3 only", "url": "https://example.invalid/agpl",
			"corresponding_source": "https://example.invalid/aue", "aggregation": "a separate program",
		},
		"source": map[string]any{
			"repository": "https://example.invalid/aue", "commit": "deadbeef",
			"corresponding_source": "https://example.invalid/aue",
		},
		"toolchain": map[string]any{"go": "go1.23.4", "cgo_enabled": false, "flags": []string{"-trimpath"}},
		"artifacts": []any{
			map[string]any{
				"platform": map[string]any{"os": "linux", "arch": "amd64"},
				"file":     "auto-pigeon-extractor-1.171-linux-amd64",
				"kind":     "file", "executable": "auto-pigeon-extractor",
				"size": 18854040, "sha256": "sha256:" + strings.Repeat("ab", 32),
			},
			map[string]any{
				"platform": map[string]any{"os": "windows", "arch": "amd64"},
				"file":     "auto-pigeon-extractor-1.171-windows-amd64.exe",
				"kind":     "file", "executable": "auto-pigeon-extractor.exe",
				"size": 19187200, "sha256": "sha256:" + strings.Repeat("cd", 32),
			},
		},
		"unsupported": []any{
			map[string]any{
				"platform": map[string]any{"os": "windows", "arch": "arm64"},
				"reason":   "runs the amd64 build under emulation",
			},
		},
	}
	for _, apply := range edit {
		apply(document)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	return encoded
}

func releaseOptions() catalog.ReleaseOptions {
	return catalog.ReleaseOptions{
		PackageID: "auto-pigeon.extractor", BaseURL: "https://releases.example/aue/1.171/",
		Signer: "test-key", MinCompanion: "0.1.0",
	}
}

// The composition, and the one thing it must not do: recompute a digest. The
// digests it copies are the ones the build produced and the ones a downloader
// checks what it received against; a second opinion about a digest is exactly
// the ambiguity a signature removes.
func TestAPackageCarriesTheManifestsOwnDigestsAndLicence(t *testing.T) {
	manifest, err := catalog.DecodeReleaseManifest(releaseManifest(t))
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := catalog.PackageFromRelease(manifest, releaseOptions())
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ID != "auto-pigeon.extractor" || pkg.Version != "1.171" {
		t.Fatalf("package = %+v", pkg)
	}
	if len(pkg.Artifacts) != 2 {
		t.Fatalf("artifacts = %d", len(pkg.Artifacts))
	}
	if pkg.Artifacts[0].SHA256 != "sha256:"+strings.Repeat("ab", 32) {
		t.Errorf("digest = %q", pkg.Artifacts[0].SHA256)
	}
	if pkg.Artifacts[0].Size != 18854040 {
		t.Errorf("size = %d", pkg.Artifacts[0].Size)
	}
	if pkg.Artifacts[0].URL != "https://releases.example/aue/1.171/auto-pigeon-extractor-1.171-linux-amd64" {
		t.Errorf("url = %q", pkg.Artifacts[0].URL)
	}
	// The name it is INSTALLED as carries no version, so a consumer never
	// composes one into a path.
	if pkg.Artifacts[0].File != "auto-pigeon-extractor" ||
		pkg.Artifacts[1].File != "auto-pigeon-extractor.exe" {
		t.Errorf("installed names = %q / %q", pkg.Artifacts[0].File, pkg.Artifacts[1].File)
	}
	if pkg.License.SPDX != "AGPL-3.0-only" || pkg.License.CorrespondingSource == "" {
		t.Errorf("licence = %+v", pkg.License)
	}

	// And it passes the catalogue's own rules, which is where the copyleft
	// check lives.
	if err := (&catalog.Catalog{
		SchemaVersion: catalog.SchemaVersion, CatalogID: "x", Serial: 1,
		IssuedAt: compatNow, ExpiresAt: compatNow.Add(time.Hour),
		Packages: []catalog.Package{pkg},
	}).Validate(map[string]bool{"test-key": true}); err != nil {
		t.Errorf("the composed package does not validate: %v", err)
	}
}

// A copyleft release with no corresponding source is refused, and it is refused
// at the point a publisher is composing rather than after they have signed.
func TestAReleaseWithNoCorrespondingSourceIsRefusedBeforeSigning(t *testing.T) {
	raw := releaseManifest(t, func(document map[string]any) {
		document["source"].(map[string]any)["corresponding_source"] = ""
	})
	manifest, err := catalog.DecodeReleaseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := catalog.PackageFromRelease(manifest, releaseOptions())
	if err != nil {
		t.Fatal(err)
	}

	err = (&catalog.Catalog{
		SchemaVersion: catalog.SchemaVersion, CatalogID: "x", Serial: 1,
		IssuedAt: compatNow, ExpiresAt: compatNow.Add(time.Hour),
		Packages: []catalog.Package{pkg},
	}).Validate(map[string]bool{"test-key": true})
	if err == nil {
		t.Fatal("an AGPL package with no corresponding source was accepted")
	}
	if !strings.Contains(err.Error(), "corresponding source") {
		t.Errorf("err = %v", err)
	}
}

// The compatibility entry names the platforms the manifest actually published,
// and the protocol that build declared.
func TestTheComponentNamesThePublishedPlatformsAndTheDeclaredProtocol(t *testing.T) {
	manifest, err := catalog.DecodeReleaseManifest(releaseManifest(t))
	if err != nil {
		t.Fatal(err)
	}

	component, err := catalog.ComponentFromRelease(manifest, releaseOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(component.Requirements) != 1 {
		t.Fatalf("requirements = %+v", component.Requirements)
	}
	requirement := component.Requirements[0]
	if requirement.Version != "1.171" || requirement.MinProtocol != "1.0" {
		t.Errorf("requirement = %+v", requirement)
	}
	want := []string{"linux/amd64", "windows/amd64"}
	if len(requirement.Platforms) != len(want) {
		t.Fatalf("platforms = %v", requirement.Platforms)
	}
	for index, platform := range want {
		if requirement.Platforms[index] != platform {
			t.Errorf("platforms = %v, want %v", requirement.Platforms, want)
		}
	}
	// The refused platform is NOT in the rule, which is the whole point of
	// deriving it from the artifacts rather than from a list.
	for _, platform := range requirement.Platforms {
		if platform == "windows/arm64" {
			t.Error("a platform the release does not publish is in the compatibility rule")
		}
	}
}

// The three things a publisher can get wrong that would otherwise be signed.
func TestTheComposerRefusesTheThreeThingsAPublisherGetsWrong(t *testing.T) {
	manifest, err := catalog.DecodeReleaseManifest(releaseManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*catalog.ReleaseOptions){
		"no signer":                    func(o *catalog.ReleaseOptions) { o.Signer = "" },
		"no lower bound":               func(o *catalog.ReleaseOptions) { o.MinCompanion = "" },
		"a package id that is not one": func(o *catalog.ReleaseOptions) { o.PackageID = "Not An Id" },
		"an http base url":             func(o *catalog.ReleaseOptions) { o.BaseURL = "http://releases.example/" },
		"no base url":                  func(o *catalog.ReleaseOptions) { o.BaseURL = "" },
	}
	for name, damage := range cases {
		options := releaseOptions()
		damage(&options)
		if _, err := catalog.PackageFromRelease(manifest, options); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A manifest this build does not read is refused by name rather than decoded
// hopefully.
func TestAReleaseManifestOfAnotherSchemaIsRefused(t *testing.T) {
	raw := releaseManifest(t, func(document map[string]any) {
		document["schema_version"] = "aue-release-manifest/9.0"
	})
	if _, err := catalog.DecodeReleaseManifest(raw); err == nil {
		t.Fatal("a manifest of another schema was accepted")
	}
}

// TestTheReleaseManifestContractMatchesTheExtractor reads the extractor's own
// source when a sibling checkout is present, and skips LOUDLY when it is not: a
// pass would claim a comparison that did not happen.
//
// The decoder here refuses unknown fields, so a field the extractor ADDS makes
// every manifest stop decoding. That is a loud failure and the right one, and
// it is better found here than by a publisher.
func TestTheReleaseManifestContractMatchesTheExtractor(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..",
		"auto-pigeon-extractor", "internal", "release", "release.go"))
	if err != nil {
		t.Skip("the extractor's checkout is not present; the release manifest contract was NOT compared")
	}
	text := string(source)

	if !strings.Contains(text, `ManifestSchema = "`+catalog.ReleaseManifestSchema+`"`) {
		t.Errorf("the extractor's release manifest schema is not %q", catalog.ReleaseManifestSchema)
	}
	for _, field := range []string{
		`json:"schema_version"`, `json:"product"`, `json:"version"`, `json:"build_id,omitempty"`,
		`json:"protocol"`, `json:"license"`, `json:"source"`, `json:"toolchain"`,
		`json:"artifacts"`, `json:"unsupported"`,
		`json:"platform"`, `json:"file"`, `json:"kind"`, `json:"executable"`,
		`json:"size"`, `json:"sha256"`, `json:"os"`, `json:"arch"`,
		`json:"go"`, `json:"cgo_enabled"`, `json:"flags"`, `json:"reason"`,
		`json:"repository"`, `json:"commit,omitempty"`, `json:"corresponding_source"`,
	} {
		if !strings.Contains(text, field) {
			t.Errorf("the extractor's release manifest does not carry %s", field)
		}
	}

	protocolSource, err := os.ReadFile(filepath.Join("..", "..", "..",
		"auto-pigeon-extractor", "internal", "protocol", "protocol.go"))
	if err != nil {
		t.Fatalf("the extractor is present and its protocol package is not: %v", err)
	}
	// The invocation protocol document this Companion reads back off the
	// executable, and the two constants both sides compare.
	for _, fragment := range []string{
		`json:"protocol"`, `json:"spdx"`, `json:"corresponding_source"`, `json:"aggregation"`,
		`json:"name"`, `json:"executable"`, `json:"repository"`,
	} {
		if !strings.Contains(string(protocolSource), fragment) {
			t.Errorf("the extractor's protocol document does not carry %s", fragment)
		}
	}
}
