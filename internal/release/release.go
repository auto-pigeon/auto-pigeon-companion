// Package release describes what a release of this program contains, and what
// it deliberately does not.
//
// # Why this is code and not a table in a file
//
// Three claims have to keep being true for the licensing of this project to
// hold, and all three are the sort that a table drifts away from:
//
//  1. **Auto-Pigeon Companion's own code is MIT, and the only other thing in
//     its binary is Apache-2.0 contract data from auto-pigeon-libraries
//     (AULIBS).** Its Go module graph is the evidence that nothing else is
//     linked in, and [Dependencies] reads it out of the binary with
//     [debug.ReadBuildInfo] rather than out of `go.mod` — what is linked in is
//     what matters, and a build with a `replace` or a tool dependency would say
//     so there and not there. The AULIBS files are not Go modules; they are
//     listed by name in [Components] as [AULIBSContracts].
//  2. **The GPL compilers and engines are separate programs** the user already
//     has on their machine, run as their own processes. Nothing downloads them.
//     [Components] derives them from the built-in profiles, so a toolchain
//     added without a licence and a corresponding-source URL cannot become
//     invisible here.
//  3. **The extractor is a third thing again** — proprietary
//     ([ExtractorSPDX], (c) Andrea D'Intino, all rights reserved; NEW_247G),
//     shipped as its own file beside the Companion in the release bundle,
//     never inside the Companion's binary. An archive that carries it is
//     therefore never "an MIT archive": it holds an MIT program, Apache-2.0
//     AULIBS data inside that program, and a proprietary program beside it.
//
// A release therefore ships an SBOM and a checksum file that are *generated
// from* the program, not written beside it.
//
// # What an SBOM here is honest about
//
// It lists this module and its module graph, which for this program is empty,
// and it lists the external components with the relationship each has to the
// artifact. The extractor is in the document with
// `distribution: shipped-beside-in-the-release`: listing it as the Companion's
// contents would be as misleading as leaving it out.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// ModulePath is this program's Go module.
const ModulePath = "github.com/auto-pigeon/auto-pigeon-companion"

// License is the Companion's own licence. It covers this repository's own code
// and nothing shipped beside it.
const License = "MIT"

// AULIBSLicense is the licence of the auto-pigeon-libraries files compiled into
// the Companion's binary.
const AULIBSLicense = "Apache-2.0"

// ExtractorSPDX and ExtractorLicenseName are Auto-Pigeon Extractor's licence
// (NEW_247G): proprietary, owned by Andrea D'Intino, all rights reserved.
// A LicenseRef, because no SPDX list identifier describes it and inventing an
// open-source one would be a lie.
//
// This is the CURRENT POLICY, compiled into the component list a Companion
// build prints (`companion security audit`, `companion release sbom`). What a
// given release archive carries is quoted from the pinned extractor itself —
// its release manifest's `license.spdx` and its own LICENSE file, copied in as
// LICENSE-auto-pigeon-extractor.txt (build/release-plan.py) — so an archive
// never disagrees with the extractor build inside it. Extractor builds that
// were distributed under AGPL-3.0-only keep the rights that licence granted.
const (
	ExtractorSPDX        = "LicenseRef-Auto-Pigeon-Proprietary"
	ExtractorLicenseName = "Auto-Pigeon Proprietary Software License"
)

// Distribution says what relationship a component has to the released artifact.
// The distinction is the whole licensing argument, so it is a value rather than
// a sentence.
type Distribution string

const (
	// InArtifact means the component's bytes are inside what is shipped.
	InArtifact Distribution = "in-artifact"
	// ShippedBeside means the release bundle carries it as its own file next
	// to the Companion, under its own licence, and the Companion runs it as its
	// own process. The copyleft corresponding-source obligation attaches here.
	ShippedBeside Distribution = "shipped-beside-in-the-release"
	// UserSupplied means the user already has it and points the Companion at
	// it. Nothing is fetched and nothing is distributed.
	UserSupplied Distribution = "user-supplied"
)

// Component is one thing a user ends up running, and where it came from.
type Component struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// Kind is "application", "library", "tool" or "engine".
	Kind         string       `json:"kind"`
	Distribution Distribution `json:"distribution"`
	SPDX         string       `json:"spdx,omitempty"`
	LicenseName  string       `json:"license_name,omitempty"`
	LicenseURL   string       `json:"license_url,omitempty"`
	// CorrespondingSource is where the source for exactly this binary is. The
	// strong copyleft licences require it whenever a binary is distributed,
	// which is why a COPYLEFT component shipped beside the Companion must carry
	// it. A proprietary one carries none, and none is implied.
	CorrespondingSource string `json:"corresponding_source,omitempty"`
	Homepage            string `json:"homepage,omitempty"`
	Repository          string `json:"repository,omitempty"`
	Notice              string `json:"notice,omitempty"`
}

// Dependency is one Go module the binary was built from.
type Dependency struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
	Sum     string `json:"sum,omitempty"`
	Replace string `json:"replaced_by,omitempty"`
}

// Dependencies reads the running binary's module graph.
//
// From the binary, not from go.mod: `go.mod` states an intent and the binary
// states a fact, and the fact is what a user is running. A build with a
// `replace` directive, a vendored tree or a toolchain-injected module shows up
// here and nowhere else.
//
// It returns the main module's own entry excluded — that is [Self].
func Dependencies() ([]Dependency, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, false
	}
	out := make([]Dependency, 0, len(info.Deps))
	for _, dep := range info.Deps {
		if dep == nil {
			continue
		}
		entry := Dependency{Path: dep.Path, Version: dep.Version, Sum: dep.Sum}
		if dep.Replace != nil {
			entry.Replace = dep.Replace.Path
			if dep.Replace.Version != "" {
				entry.Replace += "@" + dep.Replace.Version
			}
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, true
}

// Self is the main module the binary was built from.
func Self() (Dependency, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Dependency{}, false
	}
	return Dependency{Path: info.Main.Path, Version: info.Main.Version, Sum: info.Main.Sum}, true
}

// Components is everything a user of this release can end up running, with its
// licence and its relationship to the artifact.
//
// The Companion itself is first. The only other things marked [InArtifact] are
// the [AULIBSContracts] compiled into it, under their own Apache-2.0 licence.
// Everything after those is derived from the built-in profiles, so a toolchain
// or an engine added to this build is in this list whether or not anybody
// remembered to write it down.
func Components(version string) ([]Component, error) {
	components := []Component{{
		Name:         "auto-pigeon-companion",
		Version:      version,
		Kind:         "application",
		Distribution: InArtifact,
		SPDX:         License,
		LicenseName:  "MIT License",
		Repository:   "https://" + ModulePath,
		Notice: "Auto-Pigeon Companion's own code is MIT licensed, Copyright (c) 2026 Andrea D'Intino. " +
			"Its binary also carries Apache-2.0 contract files from auto-pigeon-libraries, listed separately. " +
			"It contains no GPL tool, no engine and no extractor: each of those is a separate program, " +
			"run as its own process.",
	}}
	components = append(components, AULIBSContracts...)
	components = append(components, Extractor)

	entries, err := builtin.Load()
	if err != nil {
		return nil, fmt.Errorf("release: reading the built-in profiles: %w", err)
	}
	for _, entry := range entries {
		meta := entry.Profile.Metadata()
		if meta.Kind == profile.KindPipeline {
			// A pipeline is a recipe over other profiles' capabilities. It
			// carries no binary and would double-count the tools it names.
			continue
		}
		component := Component{
			Name:                meta.ID,
			Version:             meta.Version,
			Kind:                string(meta.Kind),
			Distribution:        distributionFor(entry),
			SPDX:                meta.License.SPDX,
			LicenseName:         meta.License.Name,
			LicenseURL:          meta.License.URL,
			CorrespondingSource: meta.License.CorrespondingSource,
			Notice:              meta.License.Notice,
		}
		if meta.Source != nil {
			component.Homepage = meta.Source.Homepage
			component.Repository = meta.Source.Repository
		}
		components = append(components, component)
	}
	sort.SliceStable(components[1:], func(i, j int) bool {
		return components[1+i].Name < components[1+j].Name
	})
	return components, nil
}

// distributionFor decides how a profile's program reaches the user's machine:
// always a program the user already has. The Companion downloads none
// (operator, 2026-09-23), and a built-in profile's legacy route would not
// change that.
func distributionFor(builtin.Entry) Distribution {
	return UserSupplied
}

// Extractor is Auto-Pigeon Extractor as a release carries it: its own file
// beside the Companion (build/bundle-sidecar.sh), proprietary.
//
// It carries NO corresponding source: that is a copyleft obligation, and the
// extractor is not copyleft. [ExtractorRepository] is where it is developed —
// a private repository — named for provenance, not offered as source.
var Extractor = Component{
	Name:         "auto-pigeon-extractor",
	Kind:         "tool",
	Distribution: ShippedBeside,
	SPDX:         ExtractorSPDX,
	LicenseName:  ExtractorLicenseName,
	Repository:   ExtractorRepository,
	Notice: "Proprietary: Copyright (c) 2026 Andrea D'Intino, all rights reserved. A separate program, " +
		"shipped as its own file beside the Companion with its own licence " +
		"(LICENSE-auto-pigeon-extractor.txt in a release bundle), and run as its own process. " +
		"The Companion's MIT licence does not cover it, and the Companion does not link, embed or " +
		"relicense it. Its licence grants no right to use it without the copyright owner's written " +
		"authorization.",
}

// ExtractorRepository is where the extractor is developed. It is private: a
// reference for provenance, not a source offer.
const ExtractorRepository = "https://github.com/auto-pigeon/auto-pigeon-extractor"

// AULIBSRepository is where the Apache-2.0 contract files compiled into the
// Companion come from.
const AULIBSRepository = "https://github.com/auto-pigeon/auto-pigeon-libraries"

// AULIBSContracts are the auto-pigeon-libraries packages whose files are
// compiled into the Companion's binary, byte for byte (the vendor tests in
// internal/web and internal/incident compare every copy with AULIBS). They are
// Apache-2.0 and stay Apache-2.0 inside an MIT program: listing them is what
// keeps the component list from calling the whole binary MIT.
var AULIBSContracts = []Component{
	{
		Name:         "@auto-pigeon/incident-contract",
		Kind:         "library",
		Distribution: InArtifact,
		SPDX:         AULIBSLicense,
		LicenseName:  "Apache License 2.0",
		LicenseURL:   "https://www.apache.org/licenses/LICENSE-2.0",
		Repository:   AULIBSRepository,
		Notice: "From auto-pigeon-libraries, unmodified: internal/incident/contract/ and " +
			"internal/web/assets/vendor/incident-contract/. Apache-2.0; not relicensed by being compiled " +
			"into an MIT program.",
	},
	{
		Name:         "@auto-pigeon/operational-notice-contract",
		Kind:         "library",
		Distribution: InArtifact,
		SPDX:         AULIBSLicense,
		LicenseName:  "Apache License 2.0",
		LicenseURL:   "https://www.apache.org/licenses/LICENSE-2.0",
		Repository:   AULIBSRepository,
		Notice: "From auto-pigeon-libraries, unmodified: internal/web/assets/vendor/operational-notice-contract/. " +
			"Apache-2.0; not relicensed by being compiled into an MIT program.",
	},
}

// isLicenseRef reports whether an identifier is an SPDX `LicenseRef-`, which is
// not on the SPDX licence list and so cannot be a CycloneDX `license.id`.
func isLicenseRef(spdx string) bool {
	return strings.HasPrefix(spdx, "LicenseRef-")
}

// --- SBOM -----------------------------------------------------------------

// SBOM is a CycloneDX document. Only the members this project can fill
// truthfully are here: an SBOM with invented fields is worse than a short one.
type SBOM struct {
	BOMFormat   string        `json:"bomFormat"`
	SpecVersion string        `json:"specVersion"`
	Version     int           `json:"version"`
	Metadata    SBOMMetadata  `json:"metadata"`
	Components  []SBOMEntry   `json:"components"`
	Properties  []SBOMProp    `json:"properties,omitempty"`
	Deps        []SBOMDepends `json:"dependencies,omitempty"`
}

// SBOMMetadata describes the document.
type SBOMMetadata struct {
	// Timestamp is omitted when it is zero, which is what makes two SBOMs of
	// one build byte-identical. A release passes the build's own time.
	Timestamp string     `json:"timestamp,omitempty"`
	Component SBOMEntry  `json:"component"`
	Tools     []SBOMProp `json:"properties,omitempty"`
}

// SBOMEntry is one CycloneDX component.
type SBOMEntry struct {
	Type       string     `json:"type"`
	BOMRef     string     `json:"bom-ref"`
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	Licenses   []SBOMLic  `json:"licenses,omitempty"`
	PURL       string     `json:"purl,omitempty"`
	Properties []SBOMProp `json:"properties,omitempty"`
}

// SBOMLic is a licence: a named licence, or an SPDX expression. A `LicenseRef-`
// identifier is not on the SPDX list, so it is written as an expression rather
// than as a `license.id` no validator would accept.
type SBOMLic struct {
	License    *SBOMLicense `json:"license,omitempty"`
	Expression string       `json:"expression,omitempty"`
}

// SBOMLicense is one named licence.
type SBOMLicense struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// SBOMProp is a name/value property.
type SBOMProp struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SBOMDepends is one dependency edge.
type SBOMDepends struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// BuildSBOM assembles the document for a release of this version.
//
// timestamp is optional: pass the zero time for a byte-identical document, and
// the build's own time for a release.
func BuildSBOM(version string, timestamp time.Time) (SBOM, error) {
	components, err := Components(version)
	if err != nil {
		return SBOM{}, err
	}
	self := SBOMEntry{
		Type:     "application",
		BOMRef:   "pkg:golang/" + ModulePath + "@" + version,
		Name:     "auto-pigeon-companion",
		Version:  version,
		PURL:     "pkg:golang/" + ModulePath + "@" + version,
		Licenses: []SBOMLic{{License: &SBOMLicense{ID: License}}},
	}

	document := SBOM{
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Version:     1,
		Metadata:    SBOMMetadata{Component: self},
	}
	if !timestamp.IsZero() {
		document.Metadata.Timestamp = timestamp.UTC().Format(time.RFC3339)
	}

	modules, ok := Dependencies()
	if !ok {
		document.Properties = append(document.Properties, SBOMProp{
			Name:  "aucom:module-graph",
			Value: "unavailable: this binary carries no build information",
		})
	} else {
		document.Properties = append(document.Properties, SBOMProp{
			Name:  "aucom:go-module-dependencies",
			Value: fmt.Sprint(len(modules)),
		})
	}
	for _, module := range modules {
		document.Components = append(document.Components, SBOMEntry{
			Type:    "library",
			BOMRef:  "pkg:golang/" + module.Path + "@" + module.Version,
			Name:    module.Path,
			Version: module.Version,
			PURL:    "pkg:golang/" + module.Path + "@" + module.Version,
		})
	}

	// Everything else: the AULIBS contract files compiled into the binary, and
	// the external programs. In the document, with their relationship stated,
	// because a component the user installs separately is not a component
	// nobody should be told about, and data compiled in under another licence
	// is not the Companion's to leave out.
	for _, component := range components {
		if component.Name == self.Name && component.Distribution == InArtifact {
			continue
		}
		kind := "application"
		if component.Kind == "library" {
			kind = "library"
		}
		entry := SBOMEntry{
			Type:    kind,
			BOMRef:  "aucom:" + component.Name + "@" + component.Version,
			Name:    component.Name,
			Version: component.Version,
			Properties: []SBOMProp{
				{Name: "aucom:distribution", Value: string(component.Distribution)},
			},
		}
		if isLicenseRef(component.SPDX) {
			entry.Licenses = []SBOMLic{{Expression: component.SPDX}}
			entry.Properties = append(entry.Properties, SBOMProp{
				Name: "aucom:license-name", Value: component.LicenseName})
		} else if component.SPDX != "" && component.SPDX != "NOASSERTION" {
			entry.Licenses = []SBOMLic{{License: &SBOMLicense{
				ID: component.SPDX, URL: component.LicenseURL}}}
		} else if component.SPDX == "NOASSERTION" {
			entry.Licenses = []SBOMLic{{License: &SBOMLicense{Name: "NOASSERTION"}}}
		}
		if component.CorrespondingSource != "" {
			entry.Properties = append(entry.Properties, SBOMProp{
				Name: "aucom:corresponding-source", Value: component.CorrespondingSource})
		}
		document.Components = append(document.Components, entry)
	}

	refs := make([]string, 0, len(document.Components))
	for _, component := range document.Components {
		refs = append(refs, component.BOMRef)
	}
	document.Deps = []SBOMDepends{{Ref: self.BOMRef, DependsOn: refs}}
	return document, nil
}

// Encode renders an SBOM as indented JSON with a trailing newline.
func (s SBOM) Encode() ([]byte, error) {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("release: encoding the SBOM: %w", err)
	}
	return append(raw, '\n'), nil
}

// --- checksums ------------------------------------------------------------

// Checksum is one released file and its digest.
type Checksum struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Checksums digests every regular file directly inside dir.
//
// Not recursive, and directories are skipped rather than walked: a release
// directory holds the artifacts a person downloads, and a .app bundle is
// published as its .zip. Walking would put thousands of bundle-internal paths
// into a file whose whole value is that somebody can read it.
func Checksums(dir string) ([]Checksum, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("release: reading %s: %w", dir, err)
	}
	out := make([]Checksum, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("release: %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		// A checksum file cannot digest itself, and one that tried would
		// produce a value that stops being true the moment it is written.
		if entry.Name() == ChecksumFileName {
			continue
		}
		digest, err := digestFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, Checksum{Name: entry.Name(), SHA256: digest, Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ChecksumFileName is the conventional name, in the format `sha256sum -c`
// reads.
const ChecksumFileName = "SHA256SUMS"

// FormatChecksums renders the `sha256sum` format: digest, two spaces, name.
func FormatChecksums(sums []Checksum) []byte {
	var b strings.Builder
	for _, sum := range sums {
		b.WriteString(sum.SHA256 + "  " + sum.Name + "\n")
	}
	return []byte(b.String())
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("release: reading %s: %w", path, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("release: reading %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
