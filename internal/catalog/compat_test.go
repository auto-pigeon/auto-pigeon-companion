package catalog_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

var compatNow = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

func compatibility(requirements ...catalog.Requirement) *catalog.Compatibility {
	return &catalog.Compatibility{
		SchemaVersion: catalog.CompatibilitySchemaVersion,
		DocumentID:    "test-compatibility",
		Serial:        1,
		IssuedAt:      compatNow.Add(-time.Hour),
		ExpiresAt:     compatNow.Add(30 * 24 * time.Hour),
		Components: []catalog.Component{{
			Component:    "auto-pigeon.extractor",
			Requirements: requirements,
		}},
	}
}

func rule(min, below, version string) catalog.Requirement {
	return catalog.Requirement{
		MinCompanion: min, BelowCompanion: below, Version: version, MinProtocol: "1.0",
	}
}

// The ordinary case, and the half-open range: a rule covers its lower bound and
// not its upper.
func TestARuleCoversItsLowerBoundAndNotItsUpper(t *testing.T) {
	document := compatibility(rule("0.1.0", "0.3.0", "1.171"), rule("0.3.0", "", "1.180"))
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}

	for version, want := range map[string]string{
		"0.1.0": "1.171",
		"0.2.9": "1.171",
		"0.3.0": "1.180",
		"9.9.9": "1.180",
	} {
		requirement, err := document.Requirement("auto-pigeon.extractor", version, profile.Platform{OS: "linux", Arch: "amd64"})
		if err != nil {
			t.Errorf("Companion %s: %v", version, err)

			continue
		}
		if requirement.Version != want {
			t.Errorf("Companion %s resolves to %s, want %s", version, requirement.Version, want)
		}
	}

	// And below every rule there is no answer, rather than the oldest one.
	_, err := document.Requirement("auto-pigeon.extractor", "0.0.9", profile.Platform{OS: "linux", Arch: "amd64"})
	var missing *catalog.ErrNoRequirement
	if !errors.As(err, &missing) {
		t.Errorf("a Companion below every rule got %v", err)
	}
}

// Overlap is REFUSED rather than resolved by document order. A rule that
// depends on which one a reader's eye reaches first is a rule nobody can
// review, and a publisher who split a range and got the boundary wrong by one
// release would silently install the older build for everybody in the overlap.
func TestOverlappingRulesAreRefused(t *testing.T) {
	cases := map[string]*catalog.Compatibility{
		"two open-ended rules":     compatibility(rule("0.1.0", "", "1.171"), rule("0.3.0", "", "1.180")),
		"a range inside a range":   compatibility(rule("0.1.0", "1.0.0", "1.171"), rule("0.3.0", "0.4.0", "1.180")),
		"boundaries off by one":    compatibility(rule("0.1.0", "0.3.1", "1.171"), rule("0.3.0", "", "1.180")),
		"the same range twice":     compatibility(rule("0.1.0", "0.3.0", "1.171"), rule("0.1.0", "0.3.0", "1.180")),
		"one platform-less rule":   compatibility(rule("0.1.0", "", "1.171"), platformed(rule("0.1.0", "", "1.180"), "linux/amd64")),
		"two rules, one platform ": compatibility(platformed(rule("0.1.0", "", "1.171"), "linux/amd64", "darwin/arm64"), platformed(rule("0.1.0", "", "1.180"), "darwin/arm64")),
	}
	for name, document := range cases {
		if err := document.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// Two rules that differ only by platform do NOT overlap, and that is the
	// case the check must not refuse — it is how a release with one lagging
	// platform is expressed.
	document := compatibility(
		platformed(rule("0.1.0", "", "1.171"), "linux/amd64"),
		platformed(rule("0.1.0", "", "1.170"), "darwin/arm64"),
	)
	if err := document.Validate(); err != nil {
		t.Errorf("two platform-disjoint rules were refused: %v", err)
	}
	requirement, err := document.Requirement("auto-pigeon.extractor", "0.2.0", profile.Platform{OS: "darwin", Arch: "arm64"})
	if err != nil || requirement.Version != "1.170" {
		t.Errorf("darwin/arm64 resolved to %+v, %v", requirement, err)
	}
}

func platformed(requirement catalog.Requirement, platforms ...string) catalog.Requirement {
	requirement.Platforms = platforms

	return requirement
}

// A rule with no lower bound applies to every version that ever existed,
// including ones written before the component did.
func TestARuleWithNoLowerBoundIsRefused(t *testing.T) {
	document := compatibility(catalog.Requirement{Version: "1.171", MinProtocol: "1.0"})
	if err := document.Validate(); err == nil {
		t.Fatal("a rule with no min_companion was accepted")
	}
}

// A rule with no minimum protocol cannot tell a Companion whether it may drive
// the build it names, which is the whole reason the field exists.
func TestARuleWithNoMinimumProtocolIsRefused(t *testing.T) {
	document := compatibility(catalog.Requirement{MinCompanion: "0.1.0", Version: "1.171"})
	if err := document.Validate(); err == nil {
		t.Fatal("a rule with no min_protocol was accepted")
	}

	document = compatibility(catalog.Requirement{MinCompanion: "0.1.0", Version: "1.171", MinProtocol: "v1"})
	if err := document.Validate(); err == nil {
		t.Fatal("a rule with an unparseable min_protocol was accepted")
	}
}

// A range that ends before it begins is a typo that would match nothing, and it
// is better found by a publisher than by every user.
func TestARangeThatEndsBeforeItBeginsIsRefused(t *testing.T) {
	for _, document := range []*catalog.Compatibility{
		compatibility(rule("0.3.0", "0.1.0", "1.171")),
		compatibility(rule("0.3.0", "0.3.0", "1.171")),
	} {
		if err := document.Validate(); err == nil {
			t.Error("accepted")
		}
	}
}

// The protocol rule, in the one place it is implemented on this side: majors
// equal, minor at least the required one.
func TestTheProtocolRuleAcceptsALaterMinorAndNeverALaterMajor(t *testing.T) {
	for name, pair := range map[string][2]string{
		"exactly the minimum": {"1.0", "1.0"},
		"a later minor":       {"1.4", "1.0"},
	} {
		if err := catalog.ProtocolSatisfies(pair[0], pair[1]); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, pair := range map[string][2]string{
		"an earlier minor": {"1.0", "1.4"},
		"a later major":    {"2.0", "1.0"},
		"an earlier major": {"1.0", "2.0"},
	} {
		if err := catalog.ProtocolSatisfies(pair[0], pair[1]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Strict, and the strictness is the point: comparing a number this build cannot
// parse is how a Companion ends up running an executable it does not understand.
func TestProtocolParsingRefusesEverythingItCannotCompare(t *testing.T) {
	for _, value := range []string{"", "1", "v1.0", "1.0.0", "1.x", "1.01", "1.0-rc1"} {
		if _, _, err := catalog.ParseProtocol(value); err == nil {
			t.Errorf("ParseProtocol(%q) accepted", value)
		}
	}
}

// A component nobody wrote a rule for, and a component the document has never
// heard of, are different answers: the first is a publisher's omission for a
// program they do describe, and the second is asking about the wrong program.
func TestAMissingComponentAndAMissingRuleAreDifferentAnswers(t *testing.T) {
	document := compatibility(platformed(rule("0.1.0", "", "1.171"), "linux/amd64"))

	_, err := document.Requirement("auto-pigeon.extractor", "0.2.0", profile.Platform{OS: "plan9", Arch: "mips"})
	var missing *catalog.ErrNoRequirement
	if !errors.As(err, &missing) {
		t.Errorf("a platform with no rule got %v", err)
	}

	_, err = document.Requirement("example.other", "0.2.0", profile.Platform{OS: "linux", Arch: "amd64"})
	if errors.As(err, &missing) {
		t.Errorf("an unknown component was reported as a missing rule: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "example.other") {
		t.Errorf("err = %v", err)
	}
}

// The document is refused whole when its own frame is wrong, before any rule is
// read.
func TestTheDocumentsOwnFrameIsChecked(t *testing.T) {
	cases := map[string]func(*catalog.Compatibility){
		"another schema":     func(c *catalog.Compatibility) { c.SchemaVersion = "aucom.compatibility/9.0" },
		"no id":              func(c *catalog.Compatibility) { c.DocumentID = "" },
		"serial zero":        func(c *catalog.Compatibility) { c.Serial = 0 },
		"no issue time":      func(c *catalog.Compatibility) { c.IssuedAt = time.Time{} },
		"expires before use": func(c *catalog.Compatibility) { c.ExpiresAt = c.IssuedAt },
		"a component twice": func(c *catalog.Compatibility) {
			c.Components = append(c.Components, c.Components[0])
		},
		"a component with no rules": func(c *catalog.Compatibility) {
			c.Components[0].Requirements = nil
		},
		"an id that is not one": func(c *catalog.Compatibility) { c.Components[0].Component = "Not An Id" },
	}
	for name, damage := range cases {
		document := compatibility(rule("0.1.0", "", "1.171"))
		damage(document)
		if err := document.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// It decodes strictly, like every other signed document here: an unknown member
// is a document this build does not understand, not one to read hopefully.
func TestAnUnknownMemberIsRefused(t *testing.T) {
	document := compatibility(rule("0.1.0", "", "1.171"))
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	damaged := strings.Replace(string(encoded), `"serial":1`, `"serial":1,"surprise":true`, 1)

	if _, err := catalog.DecodeCompatibility([]byte(damaged)); err == nil {
		t.Fatal("a document with an unknown member was accepted")
	}
}
