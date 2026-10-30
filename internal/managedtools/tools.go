// Package managedtools installs and updates the two map-compiler toolchains
// Auto-Pigeon maintains — its builds of ericw-tools and of q3map2 — from their
// official GitHub releases, and nothing else (NEW_367, ADR-0010).
//
// # Why this exists beside a Companion that "downloads no program"
//
// ADR-0008 removed every download path, and that decision still stands for
// everything a profile document can name. The operator then authorised, on
// 2026-10-28, exactly two exceptions: these two toolchains, installed on first
// launch and offered as updates while the Companion runs. So this package is
// deliberately small and deliberately closed:
//
//   - The sources are compiled in ([Tools]). No profile, binding, config file,
//     environment variable or page can add a repository, a host or a URL. A
//     release-mode build has no override at all; tests reach a loopback server
//     through [NewLoopbackSource], which refuses every other host.
//   - Everything a release says about itself is checked before a byte is
//     extracted: the release manifest's schema, tag, version and target, its
//     SHA-256 and size for this platform, GitHub's own asset digest when it
//     publishes one, and then every file in the archive against the archive's
//     own file list.
//   - A digest published in the same release proves the bytes are the ones the
//     release describes. It does not prove who published them. Nothing here is
//     called "signed", because nothing is.
//   - Installing is not running. The version probe and every build still go
//     through the executor and the approval rules that already govern them.
//
// # Layout on disk
//
//	<tools root>/<tool>/<version>-<digest8>/      one verified installation
//	<tools root>/<tool>/<version>-<digest8>/install.json
//	<tools root>/<tool>/.staging/<random>/         a download in progress
//	<config dir>/managed-tools.json                schedule, offers, choices
//
// An installation is never changed after it is complete: an update is a new
// directory, and a job pins the absolute paths it was given, so no program is
// replaced under a running compile.
package managedtools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ToolID names one managed toolchain.
type ToolID string

const (
	// EricW is Auto-Pigeon's build of ericw-tools: qbsp, vis and light for
	// Quake 1 and Quake II. One installation serves both games.
	EricW ToolID = "ericw-tools"
	// Q3Map2 is Auto-Pigeon's build of q3map2, for Quake III.
	Q3Map2 ToolID = "q3map2"
	// Companion is this program's own release bundle: the Companion and the
	// extractor beside it (NEW_368). Offered like a toolchain, installed by
	// replacing the bundle this process runs from — see self.go.
	Companion ToolID = "companion"
)

// Kind is what an installation of a target is for.
type Kind string

const (
	// KindToolchain is a compiler toolchain, installed into its own versioned
	// folder and bound to profiles.
	KindToolchain Kind = ""
	// KindApplication is the Companion's own bundle.
	KindApplication Kind = "application"
)

// Tool is one allowlisted toolchain and everything this program checks about
// its releases.
type Tool struct {
	ID    ToolID
	Kind  Kind
	Title string
	// Owner and Repo are the GitHub repository. Compiled in, never configured.
	Owner, Repo string
	// TagPrefix is the release tag family: a tag is TagPrefix + version.
	TagPrefix string
	// ManifestSchemas are the release-manifest schemas this build can read;
	// ArchiveSchemas the same for the file list inside an archive. A release
	// with any other schema is "unsupported" and is never guessed at.
	ManifestSchemas []string
	ArchiveSchemas  []string
	// Programs are the executables an installation must contain, relative to
	// its root, without the platform's suffix.
	Programs []string
	// Profiles are the built-in profiles an installation is bound to, when
	// nobody chose something else for them.
	Profiles []string
	// Baseline is the release this Companion shipped against. Nothing older
	// is installed or offered: it is the oldest release this build was
	// qualified with, and the profiles refuse some older ones by name.
	Baseline string
	// parse turns a release version into its ordering sequence.
	parse func(string) (Sequence, error)
}

// Tools is the whole allowlist.
var Tools = []Tool{
	{
		ID:              EricW,
		Title:           "EricW tools (Auto-Pigeon build)",
		Owner:           "auto-pigeon",
		Repo:            "ericw-tools",
		TagPrefix:       "auto-pigeon-ericw-v",
		ManifestSchemas: []string{"auto-pigeon-ericw-release-manifest/1"},
		ArchiveSchemas:  []string{"auto-pigeon-ericw-archive-manifest/1"},
		Programs:        []string{"bin/qbsp", "bin/vis", "bin/light", "bin/bspinfo", "bin/bsputil"},
		Profiles: []string{
			"auto-pigeon.ericw-tools.q1", "auto-pigeon.ericw-tools.q2",
			"auto-pigeon.ericw-tools.auto-pigeon-build.q1", "auto-pigeon.ericw-tools.auto-pigeon-build.q2",
		},
		Baseline: "1.4535",
		parse:    parseEricWVersion,
	},
	{
		ID:              Q3Map2,
		Title:           "q3map2 (Auto-Pigeon build)",
		Owner:           "auto-pigeon",
		Repo:            "q3map2",
		TagPrefix:       "auto-pigeon-q3map2-v",
		ManifestSchemas: []string{"auto-pigeon-q3map2-release-manifest/1"},
		ArchiveSchemas:  []string{"auto-pigeon-q3map2-archive-manifest/1"},
		Programs:        []string{"bin/q3map2"},
		Profiles:        []string{"auto-pigeon.q3map2", "auto-pigeon.q3map2.auto-pigeon-build"},
		Baseline:        "2.5.17n-ap.3030",
		parse:           parseQ3Map2Version,
	},
}

// Self is the Companion's own release, the second kind of official target
// (NEW_368, ADR-0010). Kept out of [Tools], which is the list of toolchains
// profiles are bound to; a coordinator with [Options.Self] set adds it.
var Self = Tool{
	ID:              Companion,
	Kind:            KindApplication,
	Title:           "Auto-Pigeon Companion",
	Owner:           "auto-pigeon",
	Repo:            "auto-pigeon-companion",
	TagPrefix:       "v",
	ManifestSchemas: []string{"aucom.release-manifest/2.0"},
	ArchiveSchemas:  []string{"aucom.bundle-manifest/2.0"},
	// The baseline is the running version, which only the coordinator
	// knows; nothing older than it is ever offered.
	parse: parseCompanionVersion,
}

// Find is the allowlisted target with this id.
func Find(id ToolID) (Tool, bool) {
	for _, tool := range Tools {
		if tool.ID == id {
			return tool, true
		}
	}
	if id == Self.ID {
		return Self, true
	}
	return Tool{}, false
}

// ForProfile is the tool whose installation a built-in profile is bound to.
func ForProfile(profileID string) (Tool, bool) {
	for _, tool := range Tools {
		for _, id := range tool.Profiles {
			if id == profileID {
				return tool, true
			}
		}
	}
	return Tool{}, false
}

// ReleasesURL is the human release page, for a manual link.
func (t Tool) ReleasesURL() string {
	return "https://github.com/" + t.Owner + "/" + t.Repo + "/releases"
}

// Version parses a release version of this tool.
func (t Tool) Version(version string) (Sequence, error) { return t.parse(version) }

// VersionFromTag is the version a tag of this tool's family names.
func (t Tool) VersionFromTag(tag string) (string, bool) {
	if !strings.HasPrefix(tag, t.TagPrefix) {
		return "", false
	}
	version := strings.TrimPrefix(tag, t.TagPrefix)
	if _, err := t.parse(version); err != nil {
		return "", false
	}
	return version, true
}

// Sequence orders releases of one tool. Compared element by element as
// numbers, never as strings: 1.10 is newer than 1.9, ap.3030 newer than
// ap.3026, and a publication date decides nothing.
type Sequence []int

// Compare is -1, 0 or 1.
func (s Sequence) Compare(other Sequence) int {
	for i := 0; i < len(s) || i < len(other); i++ {
		a, b := 0, 0
		if i < len(s) {
			a = s[i]
		}
		if i < len(other) {
			b = other[i]
		}
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
	}
	return 0
}

var ericwVersion = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,9}){1,3}$`)

// parseEricWVersion reads "1.4535": dot-separated numbers.
func parseEricWVersion(version string) (Sequence, error) {
	if !ericwVersion.MatchString(version) {
		return nil, fmt.Errorf("%q is not an ericw-tools release version", version)
	}
	return numbers(strings.Split(version, "."))
}

var companionVersion = regexp.MustCompile(`^[0-9]{1,4}\.[0-9]{1,7}$`)

// parseCompanionVersion reads "1.318": 1 and this repository's commit count.
// "unknown" — an unstamped development build — does not parse, and so is
// never ordered against a release.
func parseCompanionVersion(version string) (Sequence, error) {
	if !companionVersion.MatchString(version) {
		return nil, fmt.Errorf("%q is not a Companion release version", version)
	}
	return numbers(strings.Split(version, "."))
}

var q3map2Version = regexp.MustCompile(`^([0-9]{1,4})\.([0-9]{1,4})\.([0-9]{1,4})([a-z]?)-ap\.([0-9]{1,9})$`)

// parseQ3Map2Version reads "2.5.17n-ap.3030": upstream's version, its letter,
// and the Auto-Pigeon build number. The letter orders after its number ("n"
// is 14; none is 0), so 2.5.17 < 2.5.17n < 2.5.18.
func parseQ3Map2Version(version string) (Sequence, error) {
	match := q3map2Version.FindStringSubmatch(version)
	if match == nil {
		return nil, fmt.Errorf("%q is not a q3map2 release version", version)
	}
	sequence, err := numbers(match[1:4])
	if err != nil {
		return nil, err
	}
	letter := 0
	if match[4] != "" {
		letter = int(match[4][0]-'a') + 1
	}
	build, err := strconv.Atoi(match[5])
	if err != nil {
		return nil, err
	}
	return append(sequence, letter, build), nil
}

func numbers(parts []string) (Sequence, error) {
	out := make(Sequence, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// Target is a release's name for a platform: linux-amd64, windows-amd64,
// macos-arm64. Go's "darwin" is the releases' "macos".
func Target(goos, goarch string) string {
	if goos == "darwin" {
		goos = "macos"
	}
	return goos + "-" + goarch
}

// exeSuffix is the platform's executable suffix.
func exeSuffix(target string) string {
	if strings.HasPrefix(target, "windows-") {
		return ".exe"
	}
	return ""
}
