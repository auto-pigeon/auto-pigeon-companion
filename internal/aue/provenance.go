package aue

import (
	"fmt"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// ComponentID is the extractor's package id in the catalogue and in the
// compatibility manifest. One identifier, in both documents and in every
// message, so nothing has to be mapped.
const ComponentID = "auto-pigeon.extractor"

// ProgramName is the human name, for a message.
const ProgramName = "Auto-Pigeon Extractor"

// The two modes, and there is no third.
const (
	// ModeManaged is a verified cache entry at the version the signed
	// compatibility manifest names for this Companion on this platform.
	ModeManaged = "managed"
	// ModeDeveloperOverride is an on-disk executable the user named. Nothing
	// verified it and nothing claims to have.
	ModeDeveloperOverride = "developer_override"
)

// UnverifiedNote is the sentence shown wherever an override is described.
//
// Fixed text, and shown every time rather than once: an override is a
// development convenience that behaves exactly like the real thing until it
// does not, and the whole cost of getting that confused is paid by whoever is
// debugging the difference.
const UnverifiedNote = "This extractor was named by " + EnvBinaryOverride + ". Nothing verified it: no " +
	"catalogue signature, no digest, no compatibility rule. It is a local development override, it is " +
	"never uploaded or published, and results produced with it are not results a managed extractor produced."

// Provenance is where this executable came from and what, if anything,
// vouched for it.
//
// It travels with the runner and is shown by every surface that shows an
// extractor. `Verified` is the one field that matters and it is a plain bool
// rather than something derived at a call site: a caller that has to compute
// "was this verified" from four other fields is a caller that will one day
// compute it wrong.
type Provenance struct {
	Mode     string `json:"mode"`
	Verified bool   `json:"verified"`

	// Path is where the executable is on this machine. Local, and shown to the
	// person running the program, who is entitled to know what is about to be
	// executed on their behalf.
	Path string `json:"path"`

	// The managed facts. Empty on an override, which is the point: an override
	// has no version the catalogue knows, no digest anything checked, and no
	// signer.
	PackageID     string           `json:"package_id,omitempty"`
	Version       string           `json:"version,omitempty"`
	Platform      profile.Platform `json:"platform,omitempty"`
	Digest        string           `json:"digest,omitempty"`
	Signer        string           `json:"signer,omitempty"`
	CatalogID     string           `json:"catalog_id,omitempty"`
	CatalogSerial int64            `json:"catalog_serial,omitempty"`

	// License and Source are the extractor's own, carried from the catalogue
	// entry. This program does not restate them: it is under a different
	// licence, and a program under one licence must not be the thing that says
	// what a program under another is licensed as.
	License     profile.License `json:"license,omitempty"`
	Source      profile.Source  `json:"source,omitempty"`
	Aggregation string          `json:"aggregation,omitempty"`

	// Protocol is what the executable reported about itself; MinProtocol is
	// what the compatibility manifest required. Both, because a support
	// conversation about a refusal needs the pair and not the verdict.
	Protocol    string `json:"protocol,omitempty"`
	MinProtocol string `json:"min_protocol,omitempty"`

	// Note is the unverified warning on an override, and empty otherwise.
	Note string `json:"note,omitempty"`
}

// Describe renders the provenance for a person, worst news first.
func (p Provenance) Describe() []string {
	if p.Mode == ModeDeveloperOverride {
		return []string{
			"UNVERIFIED developer override",
			p.Path,
			UnverifiedNote,
		}
	}
	lines := []string{
		fmt.Sprintf("%s %s (%s), verified", ProgramName, p.Version, p.Platform),
		p.Path,
		"digest  " + p.Digest,
	}
	if p.Signer != "" {
		lines = append(lines, fmt.Sprintf("signed by %s in catalogue %s serial %d",
			p.Signer, p.CatalogID, p.CatalogSerial))
	}
	if p.Protocol != "" {
		lines = append(lines, fmt.Sprintf("invocation protocol %s (this build requires at least %s)",
			p.Protocol, p.MinProtocol))
	}
	if p.License.SPDX != "" {
		licence := "licence " + p.License.SPDX
		if p.License.CorrespondingSource != "" {
			licence += " — corresponding source: " + p.License.CorrespondingSource
		}
		lines = append(lines, licence)
	}
	if p.Aggregation != "" {
		lines = append(lines, p.Aggregation)
	}

	return lines
}

// fromInstall builds the managed provenance from a verified cache entry.
func fromInstall(install *acquire.Install, path, minProtocol string) Provenance {
	return Provenance{
		Mode: ModeManaged, Verified: true, Path: path,
		PackageID: install.PackageID, Version: install.Version, Platform: install.Platform,
		Digest: install.Digest, Signer: install.Signer,
		CatalogID: install.CatalogID, CatalogSerial: install.CatalogSerial,
		License: install.License, Source: install.Source, Aggregation: install.Aggregation,
		MinProtocol: minProtocol,
	}
}

// requirementNote renders the compatibility rule that chose this version, for a
// plan a person reads before anything is downloaded.
func requirementNote(requirement catalog.Requirement) string {
	note := fmt.Sprintf("%s %s, protocol %s or later", ProgramName, requirement.Version, requirement.MinProtocol)
	if strings.TrimSpace(requirement.Note) != "" {
		note += " — " + strings.TrimSpace(requirement.Note)
	}

	return note
}
