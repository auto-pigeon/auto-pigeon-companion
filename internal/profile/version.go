package profile

import (
	"fmt"
	"strconv"
	"strings"
)

// SchemaVersion is the version of the *document format*, not of the thing the
// document describes. It changes when this package changes what it will accept;
// a profile's own Version changes when its author changes what it says.
//
// Confusing the two is the classic forward-compatibility bug: a reader that
// treats "I don't know this profile version" as "I don't know this format"
// refuses documents it could have read perfectly well.
const SchemaVersion = "aucom.profile/1.0"

// SupportedSchemaVersions is every document format this build can read, oldest
// first. A document naming anything else is refused by name rather than being
// parsed hopefully — a format this build has never heard of is exactly the case
// where a partial read is worse than no read.
var SupportedSchemaVersions = []string{"aucom.profile/1.0"}

// LocalBindingSchemaVersion versions the machine-local binding record. It is
// separate from SchemaVersion because bindings are never published: the two
// formats have different compatibility obligations and tying them together
// would force a migration of local state every time the portable format moved.
const LocalBindingSchemaVersion = "aucom.local-binding/1.1"

// SupportedLocalBindingSchemaVersions is every binding format this build reads,
// oldest first. A stored binding is re-stamped to the current version the next
// time it is written.
//
// The list exists because the alternative is worse in both directions. Refusing
// an older binding outright would make an additive field — 1.1 added the record
// of which downloads a binding depends on — cost every user their grants.
// Reading anything at all, on the other hand, would mean honouring grants from
// a document this build only half understands. Naming the versions is the
// middle: an old one is read deliberately, and anything else is refused by
// name.
var SupportedLocalBindingSchemaVersions = []string{"aucom.local-binding/1.0", "aucom.local-binding/1.1"}

// LocalBindingSchemaSupported reports whether this build reads a binding
// format.
func LocalBindingSchemaSupported(version string) bool {
	for _, v := range SupportedLocalBindingSchemaVersions {
		if v == version {
			return true
		}
	}
	return false
}

// SchemaSupported reports whether this build can read a document format.
func SchemaSupported(version string) bool {
	for _, v := range SupportedSchemaVersions {
		if v == version {
			return true
		}
	}
	return false
}

// Version is a semantic version, restricted to the subset a profile may carry:
// three numeric components and an optional dot-separated pre-release. Build
// metadata (`+…`) is rejected, because it is ignored in comparison and two
// versions that differ only there would be two distinct immutable publications
// that compare equal.
type Version struct {
	Major, Minor, Patch int
	Pre                 []string
}

// ParseVersion parses a semantic version.
func ParseVersion(s string) (Version, error) {
	var v Version
	if s == "" {
		return v, fmt.Errorf("version is empty")
	}
	if strings.Contains(s, "+") {
		return v, fmt.Errorf("version %q carries build metadata; it is not permitted, because two versions differing only in build metadata compare equal and could not both be published", s)
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", s)
	}
	numbers := make([]int, 3)
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return v, fmt.Errorf("version %q has a non-canonical component %q", s, part)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return v, fmt.Errorf("version %q has a non-numeric component %q", s, part)
		}
		numbers[i] = n
	}
	v.Major, v.Minor, v.Patch = numbers[0], numbers[1], numbers[2]
	if hasPre {
		if pre == "" {
			return v, fmt.Errorf("version %q has an empty pre-release", s)
		}
		for _, id := range strings.Split(pre, ".") {
			if id == "" || !isVersionIdentifier(id) {
				return v, fmt.Errorf("version %q has an invalid pre-release identifier %q", s, id)
			}
			v.Pre = append(v.Pre, id)
		}
	}
	return v, nil
}

func isVersionIdentifier(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
		default:
			return false
		}
	}
	return true
}

// String renders the version in its canonical spelling, which round-trips.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Prerelease reports whether this version is a pre-release, which is the field
// a curated catalogue reads to keep an alpha line from becoming a default.
func (v Version) Prerelease() bool { return len(v.Pre) > 0 }

// Compare orders two versions by the semantic-version rules: numeric components
// first, then a pre-release sorting *before* the release it precedes, then
// identifier by identifier with numeric identifiers below alphanumeric ones.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}} {
		if pair[0] != pair[1] {
			return sign(pair[0] - pair[1])
		}
	}
	switch {
	case len(v.Pre) == 0 && len(other.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(other.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(other.Pre); i++ {
		if c := comparePreIdentifier(v.Pre[i], other.Pre[i]); c != 0 {
			return c
		}
	}
	return sign(len(v.Pre) - len(other.Pre))
}

func comparePreIdentifier(a, b string) int {
	an, aNum := strconv.Atoi(a)
	bn, bNum := strconv.Atoi(b)
	switch {
	case aNum == nil && bNum == nil:
		return sign(an - bn)
	case aNum == nil:
		return -1
	case bNum == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// VersionRange is a half-open interval, written as two explicit bounds rather
// than as a constraint expression.
//
// `>=1.2 <2 || ^3.1` is a small language, and a small language in a document
// that arrives from other people is a parser, an ambiguity and a disagreement
// between two implementations. Two optional bounds are unambiguous, trivially
// validated, and cover every case a compatibility declaration actually has.
type VersionRange struct {
	// AtLeast is the inclusive lower bound. Empty means unbounded below.
	AtLeast string `json:"at_least,omitempty"`
	// Below is the exclusive upper bound. Empty means unbounded above.
	Below string `json:"below,omitempty"`
}

// Includes reports whether a version falls in the range.
func (r VersionRange) Includes(v Version) bool {
	if r.AtLeast != "" {
		low, err := ParseVersion(r.AtLeast)
		if err != nil || v.Compare(low) < 0 {
			return false
		}
	}
	if r.Below != "" {
		high, err := ParseVersion(r.Below)
		if err != nil || v.Compare(high) >= 0 {
			return false
		}
	}
	return true
}

// String renders the range for a human.
func (r VersionRange) String() string {
	switch {
	case r.AtLeast == "" && r.Below == "":
		return "any version"
	case r.Below == "":
		return r.AtLeast + " or later"
	case r.AtLeast == "":
		return "before " + r.Below
	}
	return r.AtLeast + " up to (not including) " + r.Below
}

func (r VersionRange) validate(c *collector) {
	var low, high Version
	var lowOK, highOK bool
	if r.AtLeast != "" {
		v, err := ParseVersion(r.AtLeast)
		if err != nil {
			c.child(field("at_least"), func(c *collector) { c.addf("%v", err) })
		} else {
			low, lowOK = v, true
		}
	}
	if r.Below != "" {
		v, err := ParseVersion(r.Below)
		if err != nil {
			c.child(field("below"), func(c *collector) { c.addf("%v", err) })
		} else {
			high, highOK = v, true
		}
	}
	if lowOK && highOK && low.Compare(high) >= 0 {
		c.fixf("raise `below` above `at_least`, or drop one bound",
			"the range %s is empty: nothing is both at least %s and below %s", r, r.AtLeast, r.Below)
	}
}
