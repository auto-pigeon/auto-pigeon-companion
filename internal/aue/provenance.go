package aue

import (
	"fmt"
)

// ProgramName is the human name, for a message.
const ProgramName = "Auto-Pigeon Extractor"

// The two modes, and there is no third.
const (
	// ModeBundled is the executable shipped beside this Companion in its
	// release — `auto-pigeon-extractor[.exe]` in the same directory — checked
	// against the release's bundle manifest when one is there.
	ModeBundled = "bundled"
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
	"release manifest, no digest, no protocol check. It is a local development override, it is " +
	"never uploaded or published, and results produced with it are not results the bundled extractor produced."

// UnlistedNote is shown for a bundled executable no bundle manifest vouches for
// — a development tree, or a release unpacked without its manifest.
const UnlistedNote = "No bundle manifest beside this Companion lists this extractor, so nothing checked its " +
	"digest. It still had to pass the protocol check."

// Provenance is where this executable came from and what, if anything,
// vouched for it.
//
// It travels with the runner and is shown by every surface that shows an
// extractor. `Verified` is a plain bool rather than something derived at a
// call site: a caller that has to compute "was this verified" from four other
// fields is a caller that will one day compute it wrong.
type Provenance struct {
	Mode     string `json:"mode"`
	Verified bool   `json:"verified"`

	// Path is where the executable is on this machine.
	Path string `json:"path"`

	// Version, Protocol and the licence are what the executable reported about
	// itself in the handshake; MinProtocol is what this build requires. Empty
	// on an override, which is not asked.
	Version     string `json:"version,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	MinProtocol string `json:"min_protocol,omitempty"`
	License     string `json:"license,omitempty"`
	Source      string `json:"corresponding_source,omitempty"`

	// Digest is the executable's SHA-256 when the bundle manifest listed it.
	Digest string `json:"digest,omitempty"`

	// Note is the warning on an override or an unlisted bundled copy.
	Note string `json:"note,omitempty"`
}

// Describe renders the provenance for a person, worst news first.
func (p Provenance) Describe() []string {
	if p.Mode == ModeDeveloperOverride {
		return []string{"UNVERIFIED developer override", p.Path, UnverifiedNote}
	}
	state := "checked against this release's bundle manifest"
	if !p.Verified {
		state = "not listed in a bundle manifest"
	}
	lines := []string{fmt.Sprintf("%s %s, shipped with this Companion, %s", ProgramName, p.Version, state), p.Path}
	if p.Digest != "" {
		lines = append(lines, "digest  "+p.Digest)
	}
	if p.Protocol != "" {
		lines = append(lines, fmt.Sprintf("invocation protocol %s (this build requires at least %s)",
			p.Protocol, p.MinProtocol))
	}
	if p.License != "" {
		licence := "licence " + p.License
		if p.Source != "" {
			licence += " — corresponding source: " + p.Source
		}
		lines = append(lines, licence)
	}
	if p.Note != "" {
		lines = append(lines, p.Note)
	}

	return lines
}
