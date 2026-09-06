package profile

import "strings"

// Capabilities: what a profile can do, named so that a pipeline does not have
// to name who does it.
//
// A pipeline that said "run ericw-tools' qbsp, then ericw-tools' vis" would be
// a pipeline that only works for people who installed ericw-tools. One that
// says "compile a Q1 BSP, then compute visibility" works for anyone who has
// *something* that declares those capabilities — which is the difference
// between an extension model and a list of blessed tools with extra steps.

// Capability is one declared unit of work.
type Capability struct {
	// ID is a dot-separated role-style identifier: `q1.bsp.compile`.
	ID          string `json:"id" aucom:"required"`
	Title       string `json:"title" aucom:"required"`
	Description string `json:"description,omitempty"`
	// Consumes and Produces are artifact roles. A pipeline uses them to check
	// that a step's output can actually be a later step's input, before
	// anything runs.
	Consumes []string `json:"consumes,omitempty"`
	Produces []string `json:"produces,omitempty"`
}

func (cap Capability) validate(c *collector) {
	c.child(field("id"), func(c *collector) { checkArtifactRole(c, cap.ID) })
	c.child(field("title"), func(c *collector) { checkText(c, cap.Title, maxNameLength, true) })
	c.child(field("description"), func(c *collector) { checkText(c, cap.Description, maxTextLength, false) })
	for _, pair := range []struct {
		name  string
		roles []string
	}{{"consumes", cap.Consumes}, {"produces", cap.Produces}} {
		c.child(field(pair.name), func(c *collector) {
			if len(pair.roles) > maxListLength {
				c.addf("has %d entries, over the %d limit", len(pair.roles), maxListLength)
				return
			}
			for i, role := range pair.roles {
				c.child(index(i), func(c *collector) { checkArtifactRole(c, role) })
			}
		})
	}
}

// ContentLayout is a way a game stores the content an engine loads.
//
// Declared rather than assumed, because the difference between "loose files in
// a mod directory", "a PAK", and "a PK3 in baseq3" is the difference between a
// launch that works and one that silently loads the wrong map, and the engines
// do not agree about it.
type ContentLayout struct {
	ID    string `json:"id" aucom:"required"`
	Title string `json:"title" aucom:"required"`
	// Kind is the storage shape.
	Kind string `json:"kind" aucom:"required"`
	// Root is the role the content is placed under.
	Root string `json:"root" aucom:"required"`
	// Path is where inside that root, relative and POSIX-spelled.
	Path string `json:"path,omitempty"`
	Note string `json:"note,omitempty"`
}

var contentKinds = []string{"loose_files", "mod_directory", "pak", "pk3", "wad"}

func (l ContentLayout) validate(c *collector) {
	c.child(field("id"), func(c *collector) { checkToken(c, l.ID) })
	c.child(field("title"), func(c *collector) { checkText(c, l.Title, maxNameLength, true) })
	c.child(field("kind"), func(c *collector) {
		if !contains(contentKinds, l.Kind) {
			c.fixf("use one of: "+strings.Join(contentKinds, ", "), "is %q", l.Kind)
		}
	})
	c.child(field("root"), func(c *collector) {
		if !contains(rootRoles, l.Root) {
			c.fixf("use one of: "+strings.Join(rootRoles, ", "), "is %q, which is not a known root role", l.Root)
		}
	})
	c.child(field("path"), func(c *collector) { checkRelativePath(c, l.Path, false) })
	c.child(field("note"), func(c *collector) { checkText(c, l.Note, maxTextLength, false) })
}
