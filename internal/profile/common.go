package profile

import (
	"fmt"
	"strings"
)

// Kind names what a document describes. It is carried inside the document
// rather than inferred from a filename, so a file that is renamed, pasted into
// a chat window or served over HTTP still says what it is.
type Kind string

const (
	KindTool     Kind = "tool"
	KindEngine   Kind = "engine"
	KindPipeline Kind = "pipeline"
)

// Kinds is every document kind, in the order they are documented.
var Kinds = []Kind{KindTool, KindEngine, KindPipeline}

// Platform is one target, in Go's spelling because that is what the Companion
// is built with and a second vocabulary would need a translation table nobody
// would keep correct.
type Platform struct {
	OS   string `json:"os" aucom:"required"`
	Arch string `json:"arch" aucom:"required"`
}

// The platform vocabulary. Closed, because an unrecognised platform in a
// profile is a silent no-op — the profile simply never matches — and a typo
// that produces "this tool does not support your machine" is a bad bug to have
// to find by reading a document.
var (
	knownOS   = []string{"darwin", "freebsd", "linux", "windows"}
	knownArch = []string{"386", "amd64", "arm", "arm64"}
)

// ExeSuffix is the executable extension for the platform.
func (p Platform) ExeSuffix() string {
	if p.OS == "windows" {
		return ".exe"
	}
	return ""
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// Zero reports an unset platform, which several members treat as "all declared
// platforms" rather than as an error.
func (p Platform) Zero() bool { return p.OS == "" && p.Arch == "" }

func (p Platform) validate(c *collector) {
	c.child(field("os"), func(c *collector) {
		if !contains(knownOS, p.OS) {
			c.fixf("use one of: "+strings.Join(knownOS, ", "), "is %q, which is not a known operating system", p.OS)
		}
	})
	c.child(field("arch"), func(c *collector) {
		if !contains(knownArch, p.Arch) {
			c.fixf("use one of: "+strings.Join(knownArch, ", "), "is %q, which is not a known architecture", p.Arch)
		}
	})
}

// Support states how well a profile is known to work on a platform.
//
// Three states rather than a list of supported platforms, because "we have not
// tried this" and "this does not work" are different sentences and a user is
// entitled to both. A curated engine profile that claims macOS when upstream
// ships no macOS build is worse than one that says `unverified`: the first
// wastes an afternoon, the second is a starting point.
type Support string

const (
	// Supported: the author has run this profile on this platform.
	Supported Support = "supported"
	// Unverified: it is expected to work and nobody has checked.
	Unverified Support = "unverified"
	// Unsupported: it is known not to work, and the note says why.
	Unsupported Support = "unsupported"
)

var supportStates = []string{string(Supported), string(Unverified), string(Unsupported)}

// PlatformSupport is one platform and what is claimed about it.
type PlatformSupport struct {
	Platform Platform `json:"platform" aucom:"required"`
	Status   Support  `json:"status" aucom:"required"`
	// Note is required for anything but `supported`: a claim that something
	// does not work is only useful with the reason attached.
	Note string `json:"note,omitempty"`
}

func (s PlatformSupport) validate(c *collector) {
	c.child(field("platform"), s.Platform.validate)
	c.child(field("status"), func(c *collector) {
		if !contains(supportStates, string(s.Status)) {
			c.fixf("use one of: "+strings.Join(supportStates, ", "), "is %q", s.Status)
		}
	})
	c.child(field("note"), func(c *collector) {
		checkText(c, s.Note, maxSummaryLength, false)
		if s.Status != Supported && strings.TrimSpace(s.Note) == "" {
			c.fixf("say what is missing or untested on this platform",
				"is empty, and a %q platform must say why", s.Status)
		}
	})
}

// Publisher is who stands behind a profile.
//
// It is not an authentication mechanism and does not pretend to be one: a
// community document can say anything here. It exists so the *permission
// summary* has a name in it, and so a signed catalogue entry has something to
// disagree with when the signature says somebody else.
type Publisher struct {
	Name string `json:"name" aucom:"required"`
	// URL is where to find out who this is.
	URL string `json:"url,omitempty"`
	// KeyID names the signing key that is expected to vouch for this profile,
	// when one does. It is a claim the profile makes about itself; the catalogue
	// is what settles whether the claim is true.
	KeyID string `json:"key_id,omitempty"`
}

func (p Publisher) validate(c *collector) {
	c.child(field("name"), func(c *collector) { checkText(c, p.Name, maxNameLength, true) })
	c.child(field("url"), func(c *collector) { checkURL(c, p.URL, false) })
	c.child(field("key_id"), func(c *collector) {
		checkText(c, p.KeyID, 64, false)
		if p.KeyID != "" && strings.ContainsAny(p.KeyID, " \t/\\") {
			c.addf("contains whitespace or a path separator")
		}
	})
}

// Source is where the described program comes from — not where this document
// comes from. A user asked to approve running somebody else's compiler needs a
// link to that compiler's own project, and a curated profile needs somewhere to
// put the corresponding-source offer a copyleft licence requires.
type Source struct {
	Homepage   string `json:"homepage,omitempty"`
	Repository string `json:"repository,omitempty"`
	// ReleaseNotes is the page describing the specific upstream version this
	// profile was written against.
	ReleaseNotes string `json:"release_notes,omitempty"`
}

func (s Source) validate(c *collector) {
	c.child(field("homepage"), func(c *collector) { checkURL(c, s.Homepage, false) })
	c.child(field("repository"), func(c *collector) { checkURL(c, s.Repository, false) })
	c.child(field("release_notes"), func(c *collector) { checkURL(c, s.ReleaseNotes, false) })
}

// License is the described program's licence, carried in the document so no
// code path can handle a tool without it being visible.
//
// It says nothing about this document's own licence and nothing about the
// Companion's. A profile is configuration for an independent program; it does
// not relicense that program, and describing a GPL compiler from an MIT
// repository neither makes the compiler MIT nor makes the repository GPL.
type License struct {
	// SPDX is the identifier, e.g. `GPL-2.0-or-later`.
	SPDX string `json:"spdx" aucom:"required"`
	// Name is the human spelling, for a user who does not read SPDX.
	Name string `json:"name,omitempty"`
	// URL is the licence text.
	URL string `json:"url,omitempty"`
	// CorrespondingSource is where the source for the exact distributed binary
	// can be obtained. Required by the strong copyleft licences when a binary is
	// offered for download; recorded here so the acquisition path can show it
	// before anything is fetched.
	CorrespondingSource string `json:"corresponding_source,omitempty"`
	// Notice is licence text that must be shown to the user.
	Notice string `json:"notice,omitempty"`
}

func (l License) validate(c *collector) {
	c.child(field("spdx"), func(c *collector) {
		checkText(c, l.SPDX, 64, true)
		if l.SPDX != "" && strings.ContainsAny(l.SPDX, " \t") {
			c.fixf("use a single SPDX identifier such as `GPL-2.0-or-later`", "contains whitespace")
		}
	})
	c.child(field("name"), func(c *collector) { checkText(c, l.Name, maxNameLength, false) })
	c.child(field("url"), func(c *collector) { checkURL(c, l.URL, false) })
	c.child(field("corresponding_source"), func(c *collector) { checkURL(c, l.CorrespondingSource, false) })
	c.child(field("notice"), func(c *collector) { checkText(c, l.Notice, maxTextLength, false) })
}

// Compatibility is what this document needs from whatever reads it.
type Compatibility struct {
	// Companion is the range of Companion versions this profile was written
	// for. An out-of-range profile is refused with both numbers named, rather
	// than half-read.
	Companion VersionRange `json:"companion,omitempty"`
	// Replaces names profile ids this one supersedes, so a renamed profile can
	// be recognised as an update rather than as an unrelated second tool.
	Replaces []string `json:"replaces,omitempty"`
}

func (cmp Compatibility) validate(c *collector) {
	c.child(field("companion"), cmp.Companion.validate)
	c.child(field("replaces"), func(c *collector) {
		if len(cmp.Replaces) > maxListLength {
			c.addf("has %d entries, over the %d limit", len(cmp.Replaces), maxListLength)
			return
		}
		for i, id := range cmp.Replaces {
			c.child(index(i), func(c *collector) { checkID(c, id) })
		}
	})
}

// Meta is the identity every profile document carries, whatever its kind.
type Meta struct {
	// SchemaVersion is the document format. See [SchemaVersion].
	SchemaVersion string `json:"schema_version" aucom:"required"`
	// Kind is what this document describes.
	Kind Kind `json:"kind" aucom:"required"`
	// ID is the stable, namespaced, immutable identity. Two documents with the
	// same id and version must be byte-identical after canonicalization; that
	// is what makes a published version something a digest can stand for.
	ID string `json:"id" aucom:"required"`
	// Version is this document's own semantic version. It is immutable once
	// published: changing what a version says, rather than publishing a new
	// one, is how a reviewed and granted profile becomes a different program.
	Version string `json:"version" aucom:"required"`
	Name    string `json:"name" aucom:"required"`
	// Summary is the one line shown in a list and at the top of a permission
	// review.
	Summary     string    `json:"summary" aucom:"required"`
	Description string    `json:"description,omitempty"`
	Publisher   Publisher `json:"publisher" aucom:"required"`
	// Source and Compat are pointers so that an absent one is absent in the
	// canonical form too. A zero-valued struct would serialize as `{}` — a
	// member that is present, says nothing, and changes the digest.
	Source  *Source        `json:"source,omitempty"`
	License License        `json:"license" aucom:"required"`
	Compat  *Compatibility `json:"compatibility,omitempty"`
}

func (m Meta) validate(c *collector, want Kind) {
	c.child(field("schema_version"), func(c *collector) {
		switch {
		case m.SchemaVersion == "":
			c.fixf("set it to "+SchemaVersion, "is required and empty")
		case !SchemaSupported(m.SchemaVersion):
			c.fixf("this build reads: "+strings.Join(SupportedSchemaVersions, ", "),
				"is %q, which this build of the Companion cannot read", m.SchemaVersion)
		}
	})
	c.child(field("kind"), func(c *collector) {
		if m.Kind != want {
			c.fixf(fmt.Sprintf("set it to %q", want), "is %q, but this document is being read as a %s profile", m.Kind, want)
		}
	})
	c.child(field("id"), func(c *collector) { checkID(c, m.ID) })
	c.child(field("version"), func(c *collector) {
		if _, err := ParseVersion(m.Version); err != nil {
			c.fixf("use a semantic version such as 1.0.0", "%v", err)
		}
	})
	c.child(field("name"), func(c *collector) { checkText(c, m.Name, maxNameLength, true) })
	c.child(field("summary"), func(c *collector) { checkText(c, m.Summary, maxSummaryLength, true) })
	c.child(field("description"), func(c *collector) { checkText(c, m.Description, maxTextLength, false) })
	c.child(field("publisher"), m.Publisher.validate)
	if m.Source != nil {
		c.child(field("source"), m.Source.validate)
	}
	c.child(field("license"), m.License.validate)
	if m.Compat != nil {
		c.child(field("compatibility"), m.Compat.validate)
	}
}
