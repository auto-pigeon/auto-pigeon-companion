package build

import (
	"fmt"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Which installed tool implements a capability, and what happens when two do.
//
// A pipeline names capabilities so that swapping compilers is a change to what
// is installed rather than a change to the document. The other side of that
// bargain is this: something on the machine has to decide, and the decision has
// to be one a user can predict. So a second provider of the same capability is
// **refused and named**, exactly as [job.DirCatalog] refuses two profiles with
// the same id. Ranking them — the newest, the built-in one, the first read —
// would make "which compiler built this" depend on a rule nobody was told.

// Catalog is the profile lookup a resolver reads. [job.Catalog] satisfies it.
type Catalog interface {
	List() ([]job.CatalogEntry, error)
}

// Resolver answers a pipeline's capability questions from a profile catalog.
type Resolver struct {
	providers map[string]provider
	// Conflicts is every capability more than one installed profile claimed,
	// with the profiles that claimed it. Held rather than returned so that a
	// build fails on a conflict it actually needed, and a conflict in some
	// unrelated corner of a user's profile directory does not stop everything.
	Conflicts map[string][]string
}

type provider struct {
	entry  job.CatalogEntry
	tool   *profile.ToolProfile
	action profile.Action
}

// NewResolver indexes a catalog by capability.
func NewResolver(catalog Catalog) (*Resolver, error) {
	entries, err := catalog.List()
	if err != nil {
		return nil, err
	}
	r := &Resolver{providers: map[string]provider{}, Conflicts: map[string][]string{}}
	for _, entry := range entries {
		tool, isTool := entry.Profile.(*profile.ToolProfile)
		if !isTool {
			continue
		}
		for _, action := range tool.Actions {
			if action.Capability == "" {
				continue
			}
			if first, taken := r.providers[action.Capability]; taken {
				r.Conflicts[action.Capability] = uniqueSorted(append(r.Conflicts[action.Capability],
					first.entry.Profile.Metadata().ID, tool.Meta.ID))
				continue
			}
			r.providers[action.Capability] = provider{entry: entry, tool: tool, action: action}
		}
	}
	return r, nil
}

// Provider implements [profile.Resolver].
func (r *Resolver) Provider(capability string) (*profile.ToolProfile, profile.Action, bool) {
	found, ok := r.providers[capability]
	if !ok {
		return nil, profile.Action{}, false
	}
	return found.tool, found.action, true
}

// Entry returns the catalog entry behind a capability, which is what carries
// the digest and the trust a build has to record.
func (r *Resolver) Entry(capability string) (job.CatalogEntry, bool) {
	found, ok := r.providers[capability]
	return found.entry, ok
}

// CheckConflicts refuses a build whose steps need a capability two installed
// profiles both claim.
func (r *Resolver) CheckConflicts(capabilities []string) error {
	var problems []string
	for _, capability := range capabilities {
		if claimants, clash := r.Conflicts[capability]; clash {
			problems = append(problems, fmt.Sprintf("  %s is provided by %s", capability, strings.Join(claimants, " and ")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("build: more than one installed profile provides a capability this pipeline needs:\n%s\n"+
		"This is refused rather than ranked: picking one would decide which compiler built your map by a rule "+
		"nobody told you. Remove or rename one of them.", strings.Join(problems, "\n"))
}

// Capabilities lists what the resolver can provide, for the message a user gets
// when a pipeline needs something they have not installed.
func (r *Resolver) Capabilities() []string {
	out := make([]string, 0, len(r.providers))
	for capability := range r.providers {
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := values[:0]
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
