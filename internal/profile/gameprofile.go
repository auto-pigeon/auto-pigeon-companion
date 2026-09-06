package profile

import "strings"

// Game Profiles: referenced, never redefined.
//
// AUB already owns a document that says what a project *is* — its engine
// family, its map dialect, its texture model, its entity vocabulary, its prefab
// rule — and AUP reads that document rather than branching on a game name. That
// model exists, it is live, and it has an owner.
//
// So this package does not have one. A tool or engine profile that applies to
// Quake 1 says so by naming the Game Profile it applies to, and stops there. A
// second vocabulary here would drift from AUB's within one release, and the
// drift would show up as the Companion compiling a map with a dialect the
// editor disagrees about.

// EngineFamily mirrors AUB's `engine_family` vocabulary. It is a *projection*
// carried in the reference so a profile list can be filtered without a network
// round trip, exactly as AUP carries the same field on a map row. AUB remains
// authoritative; nothing here recomputes it.
var engineFamilies = []string{"quake1", "quake2", "quake3"}

// GameProfileRef names a Game Profile in AUB.
//
// # Why the reference is by slug and not by record id
//
// AUB's record id identifies a row in one AUB deployment. A profile document
// travels between people, and between deployments, so a record id inside a
// portable document is a machine identifier that will resolve to nothing, or —
// worse — to something else. The portable half of the reference is therefore
// the slug and the family; resolving that to a concrete record id on this
// account, against this AUB, is a local binding's job.
//
// [CheckPortable] enforces this: a `profile_id` member does not exist in the
// portable schema at all.
type GameProfileRef struct {
	// Slug is AUB's stable, human-readable identifier for the profile.
	Slug string `json:"slug" aucom:"required"`
	// EngineFamily is AUB's projection of the profile's family. Carried for
	// filtering; never authority, and never recomputed here.
	EngineFamily string `json:"engine_family" aucom:"required"`
	// QualifiedRevision records which revision of that Game Profile the author
	// checked this profile against.
	//
	// It is evidence, not a pin. AUB's Game Profiles are live documents and
	// `revision` is a change counter, not a version anything may depend on —
	// AUP's own client says so in as many words. A reader that treated this as
	// a dependency version would refuse to work the first time somebody fixed
	// a typo in an entity description.
	QualifiedRevision int `json:"qualified_revision,omitempty"`
}

func (g GameProfileRef) validate(c *collector) {
	c.child(field("slug"), func(c *collector) {
		checkText(c, g.Slug, 96, true)
		if g.Slug == "" {
			return
		}
		if g.Slug != strings.ToLower(g.Slug) {
			c.fixf("write the slug in lower case", "contains upper-case characters")
		}
		for i := 0; i < len(g.Slug); i++ {
			ch := g.Slug[i]
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
				c.fixf("a slug may contain lower-case letters, digits, hyphens and underscores",
					"contains %q", string(g.Slug[i]))
				break
			}
		}
	})
	c.child(field("engine_family"), func(c *collector) {
		if !contains(engineFamilies, g.EngineFamily) {
			c.fixf("use one of: "+strings.Join(engineFamilies, ", ")+" (AUB's vocabulary)",
				"is %q, which is not one of AUB's engine families", g.EngineFamily)
		}
	})
	c.child(field("qualified_revision"), func(c *collector) {
		if g.QualifiedRevision < 0 {
			c.addf("is negative")
		}
	})
}

// Empty reports a reference that names nothing, which is legitimate: a tool
// that operates on a file format rather than on a project — an archive packer,
// a texture converter — has no Game Profile to name.
func (g GameProfileRef) Empty() bool { return g.Slug == "" && g.EngineFamily == "" }
