package catalog

// Turning a component's release manifest into catalogue entries.
//
// # Why this is in the shipped binary
//
// The same reason `keygen` and `sign` are: a publishing procedure has to be
// executable, or it is a paragraph in a README that stops being true. Composing
// a catalogue package by hand means copying five digests and a licence
// identifier out of one document into another, which is exactly the kind of
// transcription that is wrong once and signed for ever.
//
// # The manifest shape is transcribed, not imported
//
// `auto-pigeon-extractor` is a separate Go module and neither imports the
// other — the boundary that keeps this repository MIT while that one is
// AGPL-3.0. So its `release-manifest.json` is described here, strictly, and
// `TestTheReleaseManifestContractMatchesTheExtractor` reads that repository's
// source when a sibling checkout is present.
//
// # Nothing here signs anything
//
// It produces the two UNSIGNED documents a publisher then signs with
// `companion catalog sign`. Keeping the composition and the signature apart is
// what lets a publisher read what they are about to vouch for.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// ReleaseManifestSchema is the component release manifest this build reads.
const ReleaseManifestSchema = "aue-release-manifest/1.0"

// ReleaseArtifact is one built file, as the manifest declares it.
type ReleaseArtifact struct {
	Platform struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"platform"`
	File       string `json:"file"`
	Kind       string `json:"kind"`
	Executable string `json:"executable"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}

// ReleaseManifest is what a component's build produced.
type ReleaseManifest struct {
	SchemaVersion string `json:"schema_version"`
	Product       struct {
		Name       string `json:"name"`
		Executable string `json:"executable"`
		Repository string `json:"repository"`
	} `json:"product"`
	Version  string `json:"version"`
	BuildID  string `json:"build_id,omitempty"`
	Protocol string `json:"protocol"`
	License  struct {
		SPDX                string `json:"spdx"`
		Name                string `json:"name"`
		URL                 string `json:"url"`
		CorrespondingSource string `json:"corresponding_source"`
		Aggregation         string `json:"aggregation"`
	} `json:"license"`
	Source struct {
		Repository          string `json:"repository"`
		Commit              string `json:"commit,omitempty"`
		CorrespondingSource string `json:"corresponding_source"`
	} `json:"source"`
	Toolchain struct {
		Go         string   `json:"go"`
		CGOEnabled bool     `json:"cgo_enabled"`
		Flags      []string `json:"flags"`
	} `json:"toolchain"`
	Artifacts   []ReleaseArtifact `json:"artifacts"`
	Unsupported []struct {
		Platform struct {
			OS   string `json:"os"`
			Arch string `json:"arch"`
		} `json:"platform"`
		Reason string `json:"reason"`
	} `json:"unsupported"`
}

// DecodeReleaseManifest reads one, strictly.
func DecodeReleaseManifest(data []byte) (*ReleaseManifest, error) {
	var manifest ReleaseManifest
	if err := decodeStrict(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.SchemaVersion != ReleaseManifestSchema {
		return nil, fmt.Errorf("catalog: the release manifest is %q; this build reads %q",
			manifest.SchemaVersion, ReleaseManifestSchema)
	}

	return &manifest, nil
}

// ReleaseOptions is what turning a manifest into catalogue entries needs that
// the manifest does not say.
type ReleaseOptions struct {
	// PackageID is the catalogue id and the compatibility component id. One
	// string in both documents, so nothing has to be mapped.
	PackageID string
	// BaseURL is where the artifacts were uploaded to. The manifest cannot know
	// it: building a release and publishing one are two decisions.
	BaseURL string
	// Signer is the catalogue key that will vouch for these artifacts. Named
	// here because [Artifact.validate] requires every artifact to name a key
	// that actually signed the document, and a publisher composing a package
	// for a key they do not hold should find out now rather than at signing.
	Signer string
	// MinCompanion and BelowCompanion bound the compatibility rule. Empty
	// BelowCompanion means "and everything after", which is what the newest
	// rule always says.
	MinCompanion   string
	BelowCompanion string
	// Note is one sentence a person reads in `companion extractor plan`.
	Note string
}

// PackageFromRelease composes the catalogue package for one release.
//
// Every fact about the bytes comes from the manifest and none of it is
// recomputed here: this function does not open an artifact, and it must not.
// The digests it copies are the ones the build produced and the ones a
// downloader will check what it received against — a second computation here
// would be a second opinion, and two opinions about a digest is exactly the
// ambiguity a signature is supposed to remove.
func PackageFromRelease(manifest *ReleaseManifest, options ReleaseOptions) (Package, error) {
	if err := options.check(); err != nil {
		return Package{}, err
	}
	base, err := url.Parse(strings.TrimSpace(options.BaseURL))
	if err != nil || base.Host == "" {
		return Package{}, fmt.Errorf("catalog: %q is not a URL to publish artifacts under", options.BaseURL)
	}
	if base.Scheme != "https" {
		return Package{}, fmt.Errorf("catalog: artifacts are published over https; %q is %q",
			options.BaseURL, base.Scheme)
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}

	pkg := Package{
		ID:      options.PackageID,
		Version: strings.TrimSpace(manifest.Version),
		Name:    strings.TrimSpace(manifest.Product.Name),
		Program: strings.TrimSpace(manifest.Product.Name),
		Summary: fmt.Sprintf("%s %s, invocation protocol %s, built with %s",
			manifest.Product.Name, manifest.Version, manifest.Protocol, manifest.Toolchain.Go),
		Source: profile.Source{
			Homepage:   manifest.Product.Repository,
			Repository: manifest.Source.Repository,
		},
		License: profile.License{
			SPDX: manifest.License.SPDX,
			Name: manifest.License.Name,
			URL:  manifest.License.URL,
			// The offer travels from the manifest. A copyleft package composed
			// without one is refused by Package.validate, which is the second
			// of the two places this is checked: once where the release is
			// built, and once here where it is about to be signed.
			CorrespondingSource: manifest.Source.CorrespondingSource,
		},
	}
	for _, artifact := range manifest.Artifacts {
		platform := profile.Platform{OS: artifact.Platform.OS, Arch: artifact.Platform.Arch}
		if platform.Zero() {
			return Package{}, fmt.Errorf("catalog: the release manifest has an artifact with no platform")
		}
		executable := strings.TrimSpace(artifact.Executable)
		if executable == "" {
			return Package{}, fmt.Errorf("catalog: %s's artifact names no executable", platform)
		}
		pkg.Artifacts = append(pkg.Artifacts, Artifact{
			Platform: platform,
			URL:      base.JoinPath(artifact.File).String(),
			Kind:     artifact.Kind,
			Size:     artifact.Size,
			SHA256:   artifact.SHA256,
			Signer:   options.Signer,
			// The name the download is given inside the install, which is the
			// program's own name without the version. A consumer that had to
			// compose a version into a path would break on the next release.
			File:        executable,
			Executables: []string{executable},
		})
	}

	return pkg, nil
}

// ComponentFromRelease composes the compatibility entry for one release.
//
// The rule it produces is deliberately minimal: one version range, every
// platform the manifest published, the version it published, and the protocol
// that build declared. A publisher who needs a narrower rule edits the document
// before signing it — this produces the common case rather than a language for
// every case.
func ComponentFromRelease(manifest *ReleaseManifest, options ReleaseOptions) (Component, error) {
	if err := options.check(); err != nil {
		return Component{}, err
	}
	if _, _, err := ParseProtocol(manifest.Protocol); err != nil {
		return Component{}, fmt.Errorf("catalog: the release manifest's protocol: %w", err)
	}
	platforms := make([]string, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		platforms = append(platforms, artifact.Platform.OS+"/"+artifact.Platform.Arch)
	}

	return Component{
		Component: options.PackageID,
		Program:   strings.TrimSpace(manifest.Product.Name),
		Requirements: []Requirement{{
			MinCompanion:   options.MinCompanion,
			BelowCompanion: options.BelowCompanion,
			Platforms:      platforms,
			Version:        strings.TrimSpace(manifest.Version),
			// The MINIMUM is what this build speaks, which is the honest
			// answer: a Companion pinned to this release needs a build that
			// speaks at least what this one does, and a publisher who means
			// something looser edits it deliberately.
			MinProtocol: strings.TrimSpace(manifest.Protocol),
			Note:        strings.TrimSpace(options.Note),
		}},
	}, nil
}

// NewCompatibility starts a compatibility document a publisher can add
// components to.
func NewCompatibility(documentID string, serial int64, issued time.Time, validFor time.Duration) *Compatibility {
	return &Compatibility{
		SchemaVersion: CompatibilitySchemaVersion,
		DocumentID:    documentID,
		Serial:        serial,
		IssuedAt:      issued.UTC(),
		ExpiresAt:     issued.Add(validFor).UTC(),
	}
}

func (o ReleaseOptions) check() error {
	switch {
	case !idPattern.MatchString(o.PackageID):
		return fmt.Errorf("catalog: %q is not a package id; ids are namespaced and lower-case, such as `example.qbsp`",
			o.PackageID)
	case strings.TrimSpace(o.Signer) == "":
		return fmt.Errorf("catalog: a package names the catalogue key that vouches for it; pass --signer")
	case strings.TrimSpace(o.MinCompanion) == "":
		return fmt.Errorf("catalog: a compatibility rule with no min_companion applies to every Companion " +
			"that ever existed; pass --min-companion")
	}

	return nil
}

// MarshalIndented renders a document the way a publisher reviews and signs it.
func MarshalIndented(document any) ([]byte, error) {
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}

	return append(encoded, '\n'), nil
}
