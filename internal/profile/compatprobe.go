package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// CompatibilityProbe is what a tool profile requires of the programs a user
// points it at, and how to ask each one (NEW_323D).
//
// A [VersionProbe] asks ONE executable what it is, for display. That was not
// enough to be right about a build: the built-in ericw-tools 2.0 profile passes
// `-logfile` to vis and light, its "find them on PATH" route admits that what
// is on PATH is usually 0.18.1, and a binding to 0.18.1 was called ready — qbsp
// then ran, and vis refused the option. Nothing had asked vis anything, and a
// qbsp banner says nothing about the vis beside it.
//
// So the requirement is per executable, it is a statement the DOCUMENT makes
// about itself, and only the selected profile's own statement is ever applied:
// a community profile written for 0.18.1 declares that contract, or none, and
// stays usable.
//
// Everything here is literal. An identity is accepted by prefix and substring
// and never ordered: `2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505` and
// `2.0.0-alpha11` are different identities, not an older and a newer one.
type CompatibilityProbe struct {
	// Args is what each probed executable is started with. It must be a
	// harmless invocation — these programs print their banner and usage when
	// given nothing — and is usually empty.
	Args []Arg `json:"args,omitempty"`
	// Parse says where in the output the program names itself.
	Parse VersionParse `json:"parse" aucom:"required"`
	// TimeoutSeconds bounds one probe. Zero means ten.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Accept is the identities this document was written for. The identity a
	// program prints must satisfy at least one rule.
	Accept []IdentityRule `json:"accept" aucom:"required"`
	// Refuse is identities this document NAMES as ones it must not be run with,
	// each with the reason a reader is shown (NEW_323E). It is a statement about
	// measured releases of the program this document describes — the built-in
	// Auto-Pigeon build names 1.4502 and 1.4505, which put solid in a map's air
	// — and it is matched literally, like Accept, never as "older than". Only
	// the selected profile's own list is ever applied: another profile bound to
	// the same file is not affected by it.
	Refuse []RefusedIdentity `json:"refuse,omitempty"`
	// Executables is which of the profile's executables are held to this, and
	// what else each one's own output must show. An executable not named here
	// is not probed and nothing is required of it.
	Executables []ProbedExecutable `json:"executables" aucom:"required"`
}

// IdentityRule accepts an identity that starts with Prefix and contains
// Contains. An empty member accepts anything; a rule with both empty is
// refused by validation, because it would accept every program there is.
type IdentityRule struct {
	Prefix   string `json:"prefix,omitempty"`
	Contains string `json:"contains,omitempty"`
}

// Matches reports whether an identity satisfies the rule.
func (r IdentityRule) Matches(identity string) bool {
	return strings.HasPrefix(identity, r.Prefix) && strings.Contains(identity, r.Contains)
}

// RefusedIdentity is an identity a document names as one it must not be run
// with, and why. Suffix is there because a release number is the END of what
// these programs print: `+auto-pigeon.1.4505` as a substring would also name a
// later `1.45051`.
type RefusedIdentity struct {
	Prefix   string `json:"prefix,omitempty"`
	Contains string `json:"contains,omitempty"`
	Suffix   string `json:"suffix,omitempty"`
	// Reason is one sentence, shown as written: what is wrong with it and what
	// to use instead.
	Reason string `json:"reason" aucom:"required"`
}

// Matches reports whether an identity is the one this entry names.
func (r RefusedIdentity) Matches(identity string) bool {
	return strings.HasPrefix(identity, r.Prefix) && strings.Contains(identity, r.Contains) && strings.HasSuffix(identity, r.Suffix)
}

// ProbedExecutable is one executable held to the probe.
type ProbedExecutable struct {
	// Name is one of the profile's declared executables.
	Name string `json:"name" aucom:"required"`
	// Mentions is literal text this executable's own probe output must
	// contain — an option its action passes, as its usage spells it.
	Mentions []string `json:"mentions,omitempty"`
}

// CompatibilityProbeActionPrefix begins the id of the action a probe runs as.
// A declared action id is a token of lower-case letters, digits and
// underscores, so no document can declare an action with this id.
const CompatibilityProbeActionPrefix = "compatibility-probe."

// DefaultCompatibilityProbeTimeout is the bound when the document names none.
const DefaultCompatibilityProbeTimeout = 10

// For returns what is required of one executable, if anything is.
func (p *CompatibilityProbe) For(executable string) (ProbedExecutable, bool) {
	if p == nil {
		return ProbedExecutable{}, false
	}
	for _, candidate := range p.Executables {
		if candidate.Name == executable {
			return candidate, true
		}
	}
	return ProbedExecutable{}, false
}

// Timeout is the bound on one probe, in seconds.
func (p *CompatibilityProbe) Timeout() int {
	if p == nil || p.TimeoutSeconds <= 0 {
		return DefaultCompatibilityProbeTimeout
	}
	return p.TimeoutSeconds
}

// Extract reads the identity out of captured output.
func (p *CompatibilityProbe) Extract(output string) (string, bool) {
	return VersionProbe{Parse: p.Parse}.Extract(output)
}

// Accepts reports whether an identity is one this document was written for.
func (p *CompatibilityProbe) Accepts(identity string) bool {
	for _, rule := range p.Accept {
		if rule.Matches(identity) {
			return true
		}
	}
	return false
}

// Refused returns the entry that names an identity, if one does.
func (p *CompatibilityProbe) Refused(identity string) (RefusedIdentity, bool) {
	for _, entry := range p.Refuse {
		if entry.Matches(identity) {
			return entry, true
		}
	}
	return RefusedIdentity{}, false
}

// AcceptText renders the accepted identities for a sentence.
func (p *CompatibilityProbe) AcceptText() string {
	parts := make([]string, 0, len(p.Accept))
	for _, rule := range p.Accept {
		switch {
		case rule.Prefix != "" && rule.Contains != "":
			parts = append(parts, rule.Prefix+"… with "+rule.Contains)
		case rule.Prefix != "":
			parts = append(parts, rule.Prefix+"…")
		default:
			parts = append(parts, "…"+rule.Contains+"…")
		}
	}
	return strings.Join(parts, " or ")
}

// Requirement is the digest of everything material this document requires of
// one executable. Evidence recorded against one requirement says nothing about
// another: changing an accepted identity, the probe's arguments or a required
// option makes every earlier answer for that executable stale.
func (p *CompatibilityProbe) Requirement(executable string) string {
	probed, ok := p.For(executable)
	if !ok {
		return ""
	}
	// Refuse is omitted when empty, so a document that names nothing keeps the
	// digest it had before the member existed and no recorded answer goes stale.
	material, err := json.Marshal(struct {
		Args     []Arg             `json:"args"`
		Parse    VersionParse      `json:"parse"`
		Accept   []IdentityRule    `json:"accept"`
		Refuse   []RefusedIdentity `json:"refuse,omitempty"`
		Probed   ProbedExecutable  `json:"probed"`
		Contract string            `json:"contract"`
	}{p.Args, p.Parse, p.Accept, p.Refuse, probed, "aucom.compatibility-probe/1"})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// action is the probe of one executable as the executor runs it: the same
// type, the same queue, the same record as a compile (ADR-0003). It reads no
// input, declares no output and reaches no root but the job's own workspace,
// which is also where ericw-tools 2.x leaves the `vis.log` it writes even when
// asked nothing.
func (p *CompatibilityProbe) action(executable string) Action {
	return Action{
		ID:             CompatibilityProbeActionPrefix + executable,
		Title:          "Check what " + executable + " is",
		Executable:     executable,
		Args:           append([]Arg(nil), p.Args...),
		WorkingDir:     &WorkingDir{Root: RootWorkspace},
		TimeoutSeconds: p.Timeout(),
	}
}

// CompatibilityProbeExecutable reads the executable out of a probe action id.
func CompatibilityProbeExecutable(actionID string) (string, bool) {
	return strings.CutPrefix(actionID, CompatibilityProbeActionPrefix)
}

func (p *CompatibilityProbe) validate(c *collector, executables map[string]bool) {
	c.child(field("args"), func(c *collector) {
		s := scope{platform: true}
		for i, arg := range p.Args {
			c.child(index(i), func(c *collector) { arg.validate(c, s) })
		}
	})
	c.child(field("parse"), func(c *collector) {
		VersionProbe{Parse: p.Parse}.validateParse(c)
	})
	c.child(field("timeout_seconds"), func(c *collector) {
		if p.TimeoutSeconds < 0 || p.TimeoutSeconds > 300 {
			c.addf("is %d; a compatibility probe's timeout must be between 0 and 300 seconds", p.TimeoutSeconds)
		}
	})
	c.child(field("accept"), func(c *collector) {
		if len(p.Accept) == 0 {
			c.fixf("name at least one identity this document was written for", "is empty: a probe that accepts nothing refuses every program")
		}
		if len(p.Accept) > maxListLength {
			c.addf("has %d entries, over the %d limit", len(p.Accept), maxListLength)
			return
		}
		for i, rule := range p.Accept {
			c.child(index(i), func(c *collector) {
				if rule.Prefix == "" && rule.Contains == "" {
					c.fixf("give a `prefix`, a `contains`, or both", "is empty, and an empty rule accepts every program")
				}
				c.child(field("prefix"), func(c *collector) { checkText(c, rule.Prefix, 128, false) })
				c.child(field("contains"), func(c *collector) { checkText(c, rule.Contains, 128, false) })
			})
		}
	})
	c.child(field("refuse"), func(c *collector) {
		if len(p.Refuse) > maxListLength {
			c.addf("has %d entries, over the %d limit", len(p.Refuse), maxListLength)
			return
		}
		for i, entry := range p.Refuse {
			c.child(index(i), func(c *collector) {
				if entry.Prefix == "" && entry.Contains == "" && entry.Suffix == "" {
					c.fixf("give a `prefix`, a `contains`, a `suffix`, or several", "is empty, and an empty entry refuses every program")
				}
				c.child(field("prefix"), func(c *collector) { checkText(c, entry.Prefix, 128, false) })
				c.child(field("contains"), func(c *collector) { checkText(c, entry.Contains, 128, false) })
				c.child(field("suffix"), func(c *collector) { checkText(c, entry.Suffix, 128, false) })
				c.child(field("reason"), func(c *collector) {
					if strings.TrimSpace(entry.Reason) == "" {
						c.fixf("say in one sentence what is wrong with it and what to use instead", "is required and empty")
						return
					}
					checkText(c, entry.Reason, 400, false)
				})
			})
		}
	})
	c.child(field("executables"), func(c *collector) {
		if len(p.Executables) == 0 {
			c.fixf("name the executables this is required of", "is empty: a probe of nothing requires nothing")
		}
		validateNamed(c, p.Executables, func(e ProbedExecutable) string { return e.Name }, func(c *collector, e ProbedExecutable) {
			c.child(field("name"), func(c *collector) {
				if e.Name == "" {
					c.fixf("name one of the profile's executables", "is required and empty")
					return
				}
				if !executables[e.Name] {
					c.fixf("declared executables are: "+strings.Join(sortedKeys(executables), ", "),
						"names the undeclared executable %q", e.Name)
				}
			})
			c.child(field("mentions"), func(c *collector) {
				if len(e.Mentions) > maxListLength {
					c.addf("has %d entries, over the %d limit", len(e.Mentions), maxListLength)
					return
				}
				for i, mention := range e.Mentions {
					c.child(index(i), func(c *collector) {
						checkText(c, mention, 128, true)
					})
				}
			})
		})
	})
}
