// Package release describes what a release of this program contains, and what
// it deliberately does not.
//
// # Why this is code and not a table in a file
//
// Three claims have to keep being true for the licensing of this project to
// hold, and all three are the sort that a table drifts away from:
//
//  1. **Auto-Pigeon Companion is MIT and contains nothing that is not.** Its
//     Go module graph is the evidence, and [Dependencies] reads it out of the
//     binary with [debug.ReadBuildInfo] rather than out of `go.mod` — what is
//     linked in is what matters, and a build with a `replace` or a tool
//     dependency would say so there and not there.
//  2. **The GPL compilers and engines are separate programs** the user already
//     has on their machine, run as their own processes. Nothing downloads them.
//     [Components] derives them from the built-in profiles, so a toolchain
//     added without a licence and a corresponding-source URL cannot become
//     invisible here.
//  3. **The extractor is a third thing again** — AGPL-3.0, separately
//     licensed, shipped as its own file beside the Companion in the release
//     bundle, never inside the Companion's binary.
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

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

// ModulePath is this program's Go module.
const ModulePath = "github.com/andrea-dintino/auto-pigeon-companion"

// License is the Companion's own licence.
const License = "MIT"

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
	// which is why a component shipped beside the Companion must carry it.
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
// The Companion itself is first and is the only thing marked [InArtifact].
// Everything after it is derived from the built-in profiles, so a toolchain or
// an engine added to this build is in this list whether or not anybody
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
		Notice: "Auto-Pigeon Companion is MIT licensed. It contains no GPL tool, " +
			"no engine and no extractor: each of those is a separate program, run as its own process.",
	}, Extractor}

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
	sort.Slice(components[1:], func(i, j int) bool {
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
// beside the Companion (build/bundle-sidecar.sh), AGPL-3.0-only as its own
// `protocol` document declares.
var Extractor = Component{
	Name:                "auto-pigeon-extractor",
	Kind:                "tool",
	Distribution:        ShippedBeside,
	SPDX:                "AGPL-3.0-only",
	LicenseName:         "GNU Affero General Public License v3.0 only",
	CorrespondingSource: ExtractorSource,
	Repository:          ExtractorSource,
	Notice: "A separate program under its own licence, shipped as its own file beside the Companion " +
		"and run as its own process. The Companion does not link, embed or relicense it.",
}

// ExtractorSource is where the extractor's source is published.
const ExtractorSource = "https://github.com/andrea-dintino/auto-pigeon-extractor"

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

// SBOMLic is a licence expression.
type SBOMLic struct {
	License *SBOMLicense `json:"license,omitempty"`
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

	// The external programs. In the document, with their relationship stated,
	// because a component the user downloads later is not a component nobody
	// should be told about.
	for _, component := range components {
		if component.Distribution == InArtifact {
			continue
		}
		entry := SBOMEntry{
			Type:    "application",
			BOMRef:  "aucom:" + component.Name + "@" + component.Version,
			Name:    component.Name,
			Version: component.Version,
			Properties: []SBOMProp{
				{Name: "aucom:distribution", Value: string(component.Distribution)},
			},
		}
		if component.SPDX != "" && component.SPDX != "NOASSERTION" {
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
