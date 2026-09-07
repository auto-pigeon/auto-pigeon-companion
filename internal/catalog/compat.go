package catalog

// The compatibility manifest: which build of a separately distributed program
// THIS Companion is supposed to run.
//
// # Why it is a second document and not more fields on the catalogue
//
// The catalogue answers *what is downloadable, and are these the right bytes*.
// This answers *which of them should this build ask for*. They change on
// different occasions and for different reasons — a new AUE release adds a
// catalogue entry and changes nothing here; a Companion release that needs a
// newer extractor changes this and adds no artifact — and a publisher should be
// able to make one of those changes without re-signing the other document.
//
// The stronger reason is that the two must not be able to disagree. Every fact
// about an artifact — its exact size, its digest, its signer, its licence, its
// source — lives in the catalogue and ONLY there. This document names a package
// id and a version and nothing else about the bytes, so there is no second copy
// of a digest anywhere for a careless edit to make wrong.
//
// # It is verified by the same chain
//
// Same keyring, same signing roles, same serial ratchet, same expiry rules.
// [Verifier.VerifyCompatibility] is [Verifier.VerifyCatalog] with a different
// document type, deliberately: a second trust model for the document that
// decides what to run would be the weakest link in the first one.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// CompatibilitySchemaVersion is the versioned identity of the document.
const CompatibilitySchemaVersion = "aucom.compatibility/1.0"

// maxRequirements bounds one component's rule list. A publisher expressing more
// than this has a rule set nobody can review, which is a different problem from
// the one this document solves.
const maxRequirements = 64

// Requirement is one rule: for these Companion versions, on these platforms,
// run this version of the component.
type Requirement struct {
	// MinCompanion is the lowest Companion version this rule applies to,
	// inclusive. Required — a rule with no lower bound applies to every version
	// that ever existed, including ones written before the component did.
	MinCompanion string `json:"min_companion"`
	// BelowCompanion is the first Companion version this rule does NOT apply
	// to, exclusive. Empty means "and everything after", which is what the
	// newest rule always says.
	//
	// Half-open on purpose. Inclusive-on-both-ends ranges have to be adjusted
	// on both sides every time one is split, and the adjustment is where an
	// overlap or a gap gets introduced.
	BelowCompanion string `json:"below_companion,omitempty"`

	// Platforms narrows the rule. Empty means every platform, which is the
	// common case; a list is for the release where one platform's build lagged.
	Platforms []string `json:"platforms,omitempty"`

	// Version is the component version to install: a version of the package id
	// this requirement's component names, resolved through the catalogue.
	Version string `json:"version"`

	// MinProtocol is the lowest invocation protocol this Companion can drive.
	// Checked against what the executable REPORTS about itself, after it is
	// verified and before anything else is asked of it — so a build whose
	// contract this Companion cannot speak is refused rather than run and
	// misread.
	MinProtocol string `json:"min_protocol"`

	// Note is one sentence for a person reading `companion acquire plan`.
	Note string `json:"note,omitempty"`
}

// Component is one separately distributed program and the rules for it.
type Component struct {
	// Component is the catalogue package id — the same string an acquisition
	// option names, so there is exactly one identifier for the program.
	Component string `json:"component"`
	// Program is the human name, for a message.
	Program      string        `json:"program,omitempty"`
	Requirements []Requirement `json:"requirements"`
}

// Compatibility is the signed map from a Companion build to the component
// versions it should run.
type Compatibility struct {
	SchemaVersion string `json:"schema_version"`
	DocumentID    string `json:"document_id"`
	// Serial increases with every publication. Same ratchet as the catalogue's.
	Serial     int64       `json:"serial"`
	IssuedAt   time.Time   `json:"issued_at"`
	ExpiresAt  time.Time   `json:"expires_at"`
	Components []Component `json:"components"`
}

// Validate checks the document against itself.
//
// It takes no signer set, unlike [Catalog.Validate]: nothing here is attributed
// to a key, because nothing here vouches for bytes. What vouches for bytes is
// the catalogue entry this document points at, and that check is unchanged.
func (c *Compatibility) Validate() error {
	switch {
	case c.SchemaVersion != CompatibilitySchemaVersion:
		return fmt.Errorf("catalog: the compatibility manifest is %q; this build reads %q",
			c.SchemaVersion, CompatibilitySchemaVersion)
	case strings.TrimSpace(c.DocumentID) == "":
		return fmt.Errorf("catalog: the compatibility manifest has no document_id")
	case c.Serial < 1:
		return fmt.Errorf("catalog: the compatibility manifest's serial is %d; serials start at 1 and only increase", c.Serial)
	case c.IssuedAt.IsZero() || c.ExpiresAt.IsZero():
		return fmt.Errorf("catalog: the compatibility manifest must say both issued_at and expires_at")
	case !c.ExpiresAt.After(c.IssuedAt):
		return fmt.Errorf("catalog: the compatibility manifest expires at or before it was issued")
	}

	seen := map[string]bool{}
	for _, component := range c.Components {
		if !idPattern.MatchString(component.Component) {
			return fmt.Errorf("catalog: %q is not a package id; ids are namespaced and lower-case, such as `example.qbsp`",
				component.Component)
		}
		if seen[component.Component] {
			return fmt.Errorf("catalog: %s is listed twice in the compatibility manifest", component.Component)
		}
		seen[component.Component] = true
		if err := component.validate(); err != nil {
			return err
		}
	}

	return nil
}

func (c Component) validate() error {
	switch {
	case len(c.Requirements) == 0:
		return fmt.Errorf("catalog: %s has no compatibility requirements, so no Companion could resolve it", c.Component)
	case len(c.Requirements) > maxRequirements:
		return fmt.Errorf("catalog: %s has %d compatibility requirements, over the %d this build reads",
			c.Component, len(c.Requirements), maxRequirements)
	}
	for index, requirement := range c.Requirements {
		if err := requirement.validate(c.Component); err != nil {
			return err
		}
		// Overlap is refused rather than resolved by order.
		//
		// Two rules that both match make the answer depend on which one a
		// reader's eye reaches first, and a publisher who splits a range and
		// gets the boundary wrong by one release gets a document that quietly
		// installs the older build for everybody in the overlap. Refusing it is
		// a check a publisher runs before signing; a precedence rule is a bug
		// that ships.
		for _, earlier := range c.Requirements[:index] {
			if platform, overlaps := requirement.overlaps(earlier); overlaps {
				return fmt.Errorf("catalog: %s has two requirements that both match Companion %s on %s; "+
					"compatibility ranges must not overlap, because a rule that depends on which one is read "+
					"first is a rule nobody can review",
					c.Component, requirement.MinCompanion, platform)
			}
		}
	}

	return nil
}

func (r Requirement) validate(component string) error {
	where := fmt.Sprintf("catalog: %s's requirement for Companion %s", component, r.MinCompanion)
	switch {
	case strings.TrimSpace(r.MinCompanion) == "":
		return fmt.Errorf("catalog: %s has a requirement with no min_companion; a rule with no lower bound "+
			"applies to every version that ever existed", component)
	case strings.TrimSpace(r.Version) == "":
		return fmt.Errorf("%s names no version to install", where)
	case strings.TrimSpace(r.MinProtocol) == "":
		return fmt.Errorf("%s declares no min_protocol, so nothing could tell whether this Companion can "+
			"drive the build it names", where)
	}
	if _, _, err := ParseProtocol(r.MinProtocol); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if r.BelowCompanion != "" && compareVersions(r.MinCompanion, r.BelowCompanion) >= 0 {
		return fmt.Errorf("%s ends at or before it begins (%s .. %s)", where, r.MinCompanion, r.BelowCompanion)
	}
	for _, platform := range r.Platforms {
		if _, err := parsePlatform(platform); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
	}

	return nil
}

// covers reports whether this rule applies to a Companion version.
func (r Requirement) covers(version string) bool {
	if compareVersions(version, r.MinCompanion) < 0 {
		return false
	}

	return r.BelowCompanion == "" || compareVersions(version, r.BelowCompanion) < 0
}

// appliesTo reports whether this rule applies to a platform. An empty list is
// every platform.
func (r Requirement) appliesTo(platform profile.Platform) bool {
	if len(r.Platforms) == 0 {
		return true
	}
	for _, name := range r.Platforms {
		if candidate, err := parsePlatform(name); err == nil && candidate == platform {
			return true
		}
	}

	return false
}

// overlaps reports whether two rules can both match one Companion version on
// one platform, and names such a platform.
func (r Requirement) overlaps(other Requirement) (string, bool) {
	// Version ranges are half-open, so they overlap unless one ends at or
	// before the other begins.
	if r.BelowCompanion != "" && compareVersions(r.BelowCompanion, other.MinCompanion) <= 0 {
		return "", false
	}
	if other.BelowCompanion != "" && compareVersions(other.BelowCompanion, r.MinCompanion) <= 0 {
		return "", false
	}
	if len(r.Platforms) == 0 || len(other.Platforms) == 0 {
		name := "every platform"
		if len(r.Platforms) > 0 {
			name = r.Platforms[0]
		} else if len(other.Platforms) > 0 {
			name = other.Platforms[0]
		}

		return name, true
	}
	for _, name := range r.Platforms {
		for _, candidate := range other.Platforms {
			if name == candidate {
				return name, true
			}
		}
	}

	return "", false
}

// ErrNoRequirement reports that no rule in the document covers this build on
// this machine.
//
// Its own error because it is a publisher's omission rather than a fault, and a
// caller renders it differently: there is nothing for the user to fix, and the
// honest message names the version and the platform that found no rule.
type ErrNoRequirement struct {
	Component string
	Version   string
	Platform  profile.Platform
}

func (e *ErrNoRequirement) Error() string {
	return fmt.Sprintf("catalog: the compatibility manifest names no %s build for Companion %s on %s",
		e.Component, e.Version, e.Platform)
}

// Requirement resolves one component for one Companion version on one platform.
//
// Exactly one rule can match, because [Component.validate] refuses a document
// where two could. So this returns the match rather than the first match, and a
// later change that introduced a precedence order would have to remove that
// check first — which is the point.
func (c *Compatibility) Requirement(component, companionVersion string, platform profile.Platform) (Requirement, error) {
	for _, candidate := range c.Components {
		if candidate.Component != component {
			continue
		}
		for _, requirement := range candidate.Requirements {
			if requirement.covers(companionVersion) && requirement.appliesTo(platform) {
				return requirement, nil
			}
		}

		return Requirement{}, &ErrNoRequirement{Component: component, Version: companionVersion, Platform: platform}
	}

	return Requirement{}, fmt.Errorf("catalog: the compatibility manifest %s names no component %q",
		c.DocumentID, component)
}

// ComponentIDs lists the component ids the document carries, in document order.
func (c *Compatibility) ComponentIDs() []string {
	out := make([]string, 0, len(c.Components))
	for _, component := range c.Components {
		out = append(out, component.Component)
	}

	return out
}

// ParseProtocol reads a `major.minor` invocation protocol number.
//
// Strict, and the strictness is the point: a protocol number with a build
// suffix, a leading `v` or a third component is one this build cannot compare,
// and comparing it anyway is how a Companion ends up running an executable it
// does not understand. It mirrors the extractor's own parser exactly — see that
// program's `internal/protocol` — because two parsers that disagree about what
// "1.0" means is the same failure as no parser at all.
func ParseProtocol(value string) (major, minor int, err error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, 0, fmt.Errorf("catalog: no protocol version given; there is no default")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("catalog: %q is not a protocol version; the form is major.minor", value)
	}
	for index, part := range parts {
		number, convErr := strconv.Atoi(part)
		if convErr != nil || number < 0 || (len(part) > 1 && part[0] == '0') {
			return 0, 0, fmt.Errorf("catalog: %q is not a protocol version; %q is not a non-negative decimal number",
				value, parts[index])
		}
		if index == 0 {
			major = number

			continue
		}
		minor = number
	}

	return major, minor, nil
}

// ProtocolSatisfies reports whether a build's declared protocol meets a
// required minimum.
//
// The rule, in one place on this side of the boundary: the majors must be EQUAL
// and the build's minor at least the required one. Not ">= major", because a
// later major is defined as breaking — running one would be running against a
// contract that has been replaced.
func ProtocolSatisfies(speaks, minimum string) error {
	speaksMajor, speaksMinor, err := ParseProtocol(speaks)
	if err != nil {
		return err
	}
	wantMajor, wantMinor, err := ParseProtocol(minimum)
	if err != nil {
		return err
	}
	switch {
	case speaksMajor != wantMajor:
		return fmt.Errorf("catalog: this build speaks invocation protocol %s and %s is required; "+
			"major %d and major %d are different contracts, and a later one is not a newer version of an earlier one",
			speaks, minimum, speaksMajor, wantMajor)
	case speaksMinor < wantMinor:
		return fmt.Errorf("catalog: this build speaks invocation protocol %s and %s is required",
			speaks, minimum)
	}

	return nil
}

// parsePlatform reads `os/arch`.
func parsePlatform(value string) (profile.Platform, error) {
	os, arch, found := strings.Cut(strings.TrimSpace(value), "/")
	if !found || strings.TrimSpace(os) == "" || strings.TrimSpace(arch) == "" {
		return profile.Platform{}, fmt.Errorf("%q is not a platform; the form is os/arch, such as linux/amd64", value)
	}

	return profile.Platform{OS: os, Arch: arch}, nil
}

// DecodeCompatibility reads an *unsigned* compatibility document — the thing a
// publisher writes and then signs. See [DecodeKeyring].
func DecodeCompatibility(data []byte) (*Compatibility, error) {
	var document Compatibility
	if err := decodeStrict(data, &document); err != nil {
		return nil, err
	}

	return &document, nil
}
