package builtin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_323E: the two profiles for the Auto-Pigeon build of ericw-tools name ONE
// release, and every fact they state about it is the release's own.
//
// testdata/auto-pigeon-ericw-release-manifest.json is the `release-manifest.json`
// the fork's workflow published with that release, byte for byte. The profiles
// were found naming 1.4505 — a release measured as putting solid in a map's air
// — two releases after the repair, because nothing tied their prose to a
// release. A new release is pinned by replacing the fixture and the profiles
// together; this test fails until both say the same thing.
func TestTheForkProfilesNameTheReleaseTheManifestDescribes(t *testing.T) {
	raw, err := os.ReadFile("testdata/auto-pigeon-ericw-release-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Tag     string `json:"tag"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Banner  string `json:"version_banner"`
		Assets  []struct {
			Name       string `json:"name"`
			Platform   string `json:"platform"`
			SHA256     string `json:"sha256"`
			Kind       string `json:"kind"`
			Acceptance struct {
				Verdict string `json:"verdict"`
			} `json:"acceptance"`
		} `json:"assets"`
		Targets map[string]struct {
			Status string `json:"status"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(manifest.Banner, "+auto-pigeon."+manifest.Version) || !strings.HasSuffix(manifest.Tag, "-v"+manifest.Version) {
		t.Fatalf("the fixture does not describe one release: %s %s %s", manifest.Tag, manifest.Version, manifest.Banner)
	}
	for _, id := range []string{"auto-pigeon.ericw-tools.auto-pigeon-build.q1", "auto-pigeon.ericw-tools.auto-pigeon-build.q2"} {
		entry, err := Find(id)
		if err != nil {
			t.Fatal(err)
		}
		tool := entry.Profile.(*profile.ToolProfile)
		encoded, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		document := string(encoded)
		if tool.ToolVersion != manifest.Banner {
			t.Errorf("%s: tool_version is %q, the release prints %q", id, tool.ToolVersion, manifest.Banner)
		}
		for _, want := range []string{
			"Auto-Pigeon build " + manifest.Version,
			"releases/tag/" + manifest.Tag,
			"tree/" + manifest.Tag,
			"`ericw-tools " + manifest.Banner + "`",
		} {
			if !strings.Contains(document, want) {
				t.Errorf("%s does not say %q", id, want)
			}
		}
		// Every toolchain archive the release published and accepted, by name
		// and digest; and nothing is called published that the release left out.
		for _, asset := range manifest.Assets {
			if asset.Kind != "toolchain" {
				continue
			}
			if asset.Acceptance.Verdict != "pass" || manifest.Targets[asset.Platform].Status != "published" {
				t.Errorf("the fixture lists %s without a passed acceptance and a published target", asset.Name)
			}
			if !strings.Contains(document, asset.Name+" "+asset.SHA256) {
				t.Errorf("%s does not give %s with its SHA-256 %s", id, asset.Name, asset.SHA256)
			}
		}
		for platform, target := range manifest.Targets {
			if target.Status != "published" && strings.Contains(document, "-"+platform+".zip") {
				t.Errorf("%s names an archive for %s, which the release did not publish", id, platform)
			}
		}
	}

	// The Quake 1 profile holds its programs to a probe, and that probe names
	// the two releases measured as defective — and not the pinned one.
	entry, _ := Find("auto-pigeon.ericw-tools.auto-pigeon-build.q1")
	probe := entry.Profile.(*profile.ToolProfile).CompatibilityProbe
	if probe == nil {
		t.Fatal("the Quake 1 profile declares no compatibility probe")
	}
	for identity, refused := range map[string]bool{
		"2.0.0-alpha11-16-gd4e4a75f+auto-pigeon.1.4502": true,
		"2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505": true,
		"2.0.0-alpha11-24-gd278362b+auto-pigeon.1.4510": false,
		"2.0.0-alpha11-27-g69db3ffa+auto-pigeon.1.4513": false,
		manifest.Banner: false,
	} {
		if !probe.Accepts(identity) {
			t.Errorf("%s is not an identity this profile accepts at all", identity)
		}
		if entry, named := probe.Refused(identity); named != refused {
			t.Errorf("%s: refused %v (%q), want %v", identity, named, entry.Reason, refused)
		}
	}
}

// NEW_340: the profile for the Auto-Pigeon build of q3map2 names ONE release,
// and every fact it states about that release is the release's own.
//
// testdata/auto-pigeon-q3map2-release-manifest.json is the `release-manifest.json`
// the fork's workflow published with release 2.5.17n-ap.3026, byte for byte. A
// new release is pinned by replacing the fixture and the profile together.
func TestTheQ3ForkProfileNamesTheReleaseTheManifestDescribes(t *testing.T) {
	raw, err := os.ReadFile("testdata/auto-pigeon-q3map2-release-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Tag     string `json:"tag"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Banner  string `json:"version_banner"`
		Assets  []struct {
			Name       string `json:"name"`
			Platform   string `json:"platform"`
			SHA256     string `json:"sha256"`
			Kind       string `json:"kind"`
			Acceptance struct {
				Verdict string `json:"verdict"`
			} `json:"acceptance"`
		} `json:"assets"`
		Targets map[string]struct {
			Status string `json:"status"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(manifest.Banner, manifest.Version+"+g"+manifest.Commit[:8]+".upstream.") || !strings.HasSuffix(manifest.Tag, "-v"+manifest.Version) {
		t.Fatalf("the fixture does not describe one release: %s %s %s", manifest.Tag, manifest.Version, manifest.Banner)
	}
	entry, err := Find(Q3Map2AutoPigeon)
	if err != nil {
		t.Fatal(err)
	}
	if !IsAlternativeTool(Q3Map2AutoPigeon) || AlternativeOf(Q3Map2AutoPigeon) != Q3Map2 {
		t.Fatalf("%s must stand beside %s as an alternative", Q3Map2AutoPigeon, Q3Map2)
	}
	tool := entry.Profile.(*profile.ToolProfile)
	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	document := string(encoded)
	if tool.ToolVersion != manifest.Banner {
		t.Errorf("tool_version is %q, the release prints %q", tool.ToolVersion, manifest.Banner)
	}
	for _, want := range []string{
		"Auto-Pigeon build " + manifest.Version,
		"releases/tag/" + manifest.Tag,
		"tree/" + manifest.Tag,
		"`" + manifest.Banner + "`",
	} {
		if !strings.Contains(document, want) {
			t.Errorf("the profile does not say %q", want)
		}
	}
	for _, asset := range manifest.Assets {
		if asset.Kind != "toolchain" {
			continue
		}
		if asset.Acceptance.Verdict != "pass" || manifest.Targets[asset.Platform].Status != "published" {
			t.Errorf("the fixture lists %s without a passed acceptance and a published target", asset.Name)
		}
		if !strings.Contains(document, asset.Name+" "+asset.SHA256) {
			t.Errorf("the profile does not give %s with its SHA-256 %s", asset.Name, asset.SHA256)
		}
	}
	// One program, in bin/ beside its libraries: the folder the user chooses is
	// the unpacked one.
	if len(tool.Executables) != 1 || tool.Executables[0].File != "bin/q3map2{platform.exe_suffix}" {
		t.Errorf("executables: %+v", tool.Executables)
	}
	// The probe accepts the fork's banners, refuses the release published with an
	// out-of-bounds read, and accepts neither upstream's banner nor a bare version.
	probe := tool.CompatibilityProbe
	if probe == nil {
		t.Fatal("the profile declares no compatibility probe")
	}
	for identity, want := range map[string]string{
		manifest.Banner: "accepted",
		"2.5.17n-ap.3020+gcf682e5b.upstream.82161339": "accepted",
		"2.5.17n-ap.3017+g84d76af1.upstream.82161339": "refused",
		"2.5.17n-git-68ecbed":                         "not accepted",
		"2.5.17n-ap.3026":                             "not accepted",
	} {
		_, refused := probe.Refused(identity)
		got := map[bool]string{true: "accepted", false: "not accepted"}[probe.Accepts(identity)]
		if got == "accepted" && refused {
			got = "refused"
		}
		if got != want {
			t.Errorf("%s: %s, want %s", identity, got, want)
		}
	}
}
