package catalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

// The catalogue this project publishes is checked here, in the build, rather
// than at the moment somebody signs it.
//
// A catalogue is reviewed as a diff — four URLs, four digests, four sizes — and
// a digest is exactly the kind of thing a reviewer cannot check by reading. What
// a test can check is everything else: that the document is well formed, that
// the packages the built-in profiles ask for are actually in it, and that the
// executables the profiles resolve are the ones the catalogue vouches for. A
// profile naming `bin/qbsp` and a catalogue publishing `qbsp` would install
// cleanly and fail at the point a user pressed the button.

const publishedCatalog = "../../catalog/ericw-tools.catalog.json"

func loadPublished(t *testing.T) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile(publishedCatalog)
	if err != nil {
		t.Fatalf("%v", err)
	}
	decoded, err := catalog.DecodeCatalog(raw)
	if err != nil {
		t.Fatalf("the published catalogue does not decode: %v", err)
	}
	return decoded
}

func TestThePublishedCatalogueIsValid(t *testing.T) {
	decoded := loadPublished(t)
	// Signing attributes an artifact that names no signer to the signing key,
	// which is why the document in the repository leaves it out; validation
	// happens after that, so this test supplies the same thing signing would.
	const signer = "the-key-that-will-sign-it"
	for p := range decoded.Packages {
		for a := range decoded.Packages[p].Artifacts {
			decoded.Packages[p].Artifacts[a].Signer = signer
		}
	}
	if err := decoded.Validate(map[string]bool{signer: true}); err != nil {
		t.Fatalf("the published catalogue is not valid:\n%v", err)
	}
}

func TestTheCataloguePublishesWhatTheBuiltInProfilesAskFor(t *testing.T) {
	decoded := loadPublished(t)

	entries, err := builtin.Load()
	if err != nil {
		t.Fatalf("%v", err)
	}
	asked := 0
	for _, entry := range entries {
		tool, isTool := entry.Profile.(*profile.ToolProfile)
		if !isTool {
			continue
		}
		for _, option := range tool.Acquisition {
			if option.Mode != profile.AcquireManagedDownload {
				continue
			}
			asked++
			pkg, found := decoded.Find(option.CatalogPackage, "")
			if !found {
				t.Errorf("%s offers a managed download of %q, which the published catalogue does not carry",
					entry.File, option.CatalogPackage)
				continue
			}
			if pkg.Version != tool.ToolVersion {
				t.Errorf("%s describes %s and the catalogue publishes %s", entry.File, tool.ToolVersion, pkg.Version)
			}
			// Every platform the profile offers the download on has a build.
			for _, platform := range option.Platforms {
				artifact, ok := pkg.ArtifactFor(platform)
				if !ok {
					t.Errorf("%s offers a managed download on %s, which the catalogue has no build for (it has: %s)",
						entry.File, platform, strings.Join(pkg.Platforms(), ", "))
					continue
				}
				// And the profile's own executable paths are among the ones the
				// catalogue vouched for, resolved for that platform.
				vouched := map[string]bool{}
				for _, path := range artifact.ExecutablePaths() {
					vouched[path] = true
				}
				for _, declared := range tool.Executables {
					want := strings.ReplaceAll(declared.File, "{platform.exe_suffix}", platform.ExeSuffix())
					if artifact.Root != "" {
						want = artifact.Root + "/" + want
					}
					if !vouched[want] {
						t.Errorf("%s declares the executable %q, which resolves to %q on %s; the catalogue vouches for %v",
							entry.File, declared.Name, want, platform, artifact.ExecutablePaths())
					}
				}
			}
		}
	}
	if asked == 0 {
		t.Error("no built-in profile offers a managed download, so this test checked nothing")
	}
}

// The one fact in the catalogue that is a claim about the outside world, kept
// here so a change to it is a change a reviewer sees.
//
// This digest is the archive AUT installed as its compiler oracle and pinned in
// `auto-pigeon-tools/ericw-tools-contract.json`. The two repositories arrived at
// it independently — AUT by hashing the copy on the machine, this catalogue by
// downloading from the URL — and a build the Companion installs that is not the
// build the acceptance gates measure is the failure this pins shut.
func TestTheLinuxArtifactIsTheArchiveAUTQualified(t *testing.T) {
	const (
		wantDigest = "sha256:986531ff66d692fa732b7f75a6c871dcbd152b98721d1c2475b76d3367f040e2"
		wantSize   = 14594502
		wantURL    = "https://github.com/ericwa/ericw-tools/releases/download/v0.18.1/ericw-tools-v0.18.1-Linux.zip"
	)
	pkg, found := loadPublished(t).Find("ericw-tools.q1", "0.18.1")
	if !found {
		t.Fatal("the catalogue does not carry ericw-tools.q1 0.18.1")
	}
	artifact, ok := pkg.ArtifactFor(profile.Platform{OS: "linux", Arch: "amd64"})
	if !ok {
		t.Fatal("the catalogue has no linux/amd64 build")
	}
	if artifact.SHA256 != wantDigest {
		t.Errorf("the linux artifact is %s, and AUT qualified %s", artifact.SHA256, wantDigest)
	}
	if artifact.Size != wantSize {
		t.Errorf("the linux artifact is %d bytes, and AUT qualified %d", artifact.Size, wantSize)
	}
	if artifact.URL != wantURL {
		t.Errorf("the linux artifact comes from %s, not %s", artifact.URL, wantURL)
	}
}

// The experimental Quake II toolchain's Linux archive is the copy that was
// unpacked and run on this machine while `AUP/AUCOM 215` was written — the one
// every measured claim in `ericw-tools-q2.tool.json` came from. It is pinned
// here for the same reason the Q1 artifact is: a build the Companion installs
// that is not the build the claims were measured against makes the claims about
// nothing.
func TestTheQuake2LinuxArtifactIsTheArchiveThatWasMeasured(t *testing.T) {
	const (
		wantDigest = "sha256:c87d669c615f92163c21e6e154268c0c2e3de4e78c26b6bd5a2a2e7996a9fe75"
		wantSize   = 22898562
		wantURL    = "https://github.com/ericwa/ericw-tools/releases/download/2.0.0-alpha7/ericw-tools-2.0.0-alpha7-Linux.zip"
	)
	pkg, found := loadPublished(t).Find("ericw-tools.q2", "2.0.0-alpha7")
	if !found {
		t.Fatal("the catalogue does not carry ericw-tools.q2 2.0.0-alpha7")
	}
	artifact, ok := pkg.ArtifactFor(profile.Platform{OS: "linux", Arch: "amd64"})
	if !ok {
		t.Fatal("the catalogue has no linux/amd64 build of the Quake II toolchain")
	}
	if artifact.SHA256 != wantDigest {
		t.Errorf("the linux artifact is %s, and %s was measured", artifact.SHA256, wantDigest)
	}
	if artifact.Size != wantSize {
		t.Errorf("the linux artifact is %d bytes, and %d was measured", artifact.Size, wantSize)
	}
	if artifact.URL != wantURL {
		t.Errorf("the linux artifact comes from %s, not %s", artifact.URL, wantURL)
	}
	// The 2.x archives have no directory inside them: `qbsp` is at the top
	// level, where v0.18.1 puts `ericw-tools-v0.18.1-Linux/bin/qbsp`. A `root`
	// copied across from the Q1 entry would install cleanly and resolve every
	// executable to a path that does not exist.
	if artifact.Root != "" {
		t.Errorf("the Quake II artifact declares the root %q; the 2.x archives have none", artifact.Root)
	}
	for _, want := range []string{"qbsp", "vis", "light"} {
		found := false
		for _, path := range artifact.ExecutablePaths() {
			if path == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the Quake II artifact does not vouch for %q; it vouches for %v", want, artifact.ExecutablePaths())
		}
	}
}

// The Quake II entry is a pre-release, and the notice a user has to accept says
// so in those words. Every other fact about it is checked structurally; that it
// is not a finished release is the one a reader has to be told.
func TestTheQuake2NoticeSaysItIsAPreRelease(t *testing.T) {
	pkg, found := loadPublished(t).Find("ericw-tools.q2", "")
	if !found {
		t.Fatal("the catalogue does not carry ericw-tools.q2")
	}
	if !pkg.RequiresAcceptance {
		t.Error("a GPL download whose notice nobody has to see is a notice nobody sees")
	}
	for _, phrase := range []string{"PRE-RELEASE", "2.0.0-alpha7", "GPL-2.0-or-later", "Embree"} {
		if !strings.Contains(pkg.License.Notice, phrase) {
			t.Errorf("the Quake II notice does not mention %q:\n%s", phrase, pkg.License.Notice)
		}
	}
	if !strings.Contains(pkg.License.CorrespondingSource, "2.0.0-alpha7") {
		t.Errorf("the corresponding-source offer is %q, and does not name the exact pre-release",
			pkg.License.CorrespondingSource)
	}
}

// A copyleft package must carry the corresponding-source offer and the notice
// the user acknowledges before anything is downloaded. `Package.validate`
// enforces the first; this checks the notice actually says the true thing,
// because a notice that named the wrong licence would pass every structural
// check there is.
func TestTheGPLNoticeSaysWhatIsActuallyBeingConveyed(t *testing.T) {
	pkg, found := loadPublished(t).Find("ericw-tools.q1", "")
	if !found {
		t.Fatal("the catalogue does not carry ericw-tools.q1")
	}
	if pkg.License.SPDX != "GPL-3.0-or-later" {
		t.Errorf("the licence is %q; the official binaries link Embree and their own README says such "+
			"builds are GPLv3+", pkg.License.SPDX)
	}
	if !pkg.RequiresAcceptance {
		t.Error("a GPL download whose notice nobody has to see is a notice nobody sees")
	}
	if !strings.Contains(pkg.License.CorrespondingSource, "v0.18.1") {
		t.Errorf("the corresponding-source offer is %q, and does not name the exact version",
			pkg.License.CorrespondingSource)
	}
	for _, phrase := range []string{"GPL-2.0-or-later", "Embree", "v0.18.1"} {
		if !strings.Contains(pkg.License.Notice, phrase) {
			t.Errorf("the notice does not mention %q, so a reader cannot tell why the binary's terms "+
				"differ from the project's:\n%s", phrase, pkg.License.Notice)
		}
	}
}

// The document in the repository is JSON a person reviews in a diff, so it is
// indented, has a trailing newline, and leads with the members that say what it
// is. Cosmetic, and worth a test only because a catalogue that is hard to read
// is a catalogue whose digests nobody reads either.
func TestThePublishedCatalogueIsReadable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(publishedCatalog))
	if err != nil {
		t.Fatalf("%v", err)
	}
	text := string(raw)
	if !strings.HasSuffix(text, "}\n") {
		t.Error("the file does not end with a closing brace and a newline")
	}
	if strings.Contains(text, "\t") {
		t.Error("the file is indented with tabs; every JSON document here uses two spaces")
	}
	var order []string
	decoder := json.NewDecoder(strings.NewReader(text))
	if _, err := decoder.Token(); err != nil { // the opening brace
		t.Fatalf("%v", err)
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatalf("%v", err)
		}
		order = append(order, key.(string))
		var discard json.RawMessage
		if err := decoder.Decode(&discard); err != nil {
			t.Fatalf("%v", err)
		}
	}
	want := []string{"schema_version", "catalog_id", "serial", "issued_at", "expires_at", "packages"}
	for i, member := range want {
		if i >= len(order) || order[i] != member {
			t.Errorf("the members are %v; a catalogue leads with %v so the first screen says what it is", order, want)
			break
		}
	}
}
