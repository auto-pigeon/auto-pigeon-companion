package cli

import (
	"fmt"
	"sort"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/maturity"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Where the work-in-progress statement reaches the command line.
//
// `AUP/AUCOM 215` requires the badge and its sentence in every place a
// work-in-progress game can be selected, loaded, edited, exported or compiled.
// On the command line that is: listing profiles, showing one, listing engines,
// showing one, listing pipelines, previewing a build and running one.
//
// All of them go through the three functions here rather than each formatting
// their own, so the sentence a user reads is the same sentence wherever they
// meet it — and so a new surface that forgets to call one is a visible omission
// rather than a slightly different wording.

// documentFamily reads the AUB engine family a profile declares.
//
// A tool profile may legitimately declare none — an archive packer applies to a
// file format, not to a game — so the empty string is a normal answer and
// [maturity.Of] treats it as undeclared.
func documentFamily(p profile.Profile) string {
	switch document := p.(type) {
	case *profile.ToolProfile:
		if document.GameProfile == nil {
			return ""
		}
		return document.GameProfile.EngineFamily
	case *profile.EngineProfile:
		return document.GameProfile.EngineFamily
	case *profile.PipelineProfile:
		if document.GameProfile == nil {
			return ""
		}
		return document.GameProfile.EngineFamily
	}
	return ""
}

// maturityBadge is the short label for a table, or "" when there is nothing to
// say. Callers put it in a column; the sentence goes below.
func maturityBadge(family string) string { return maturity.Of(family).Badge }

// printMaturityNote writes the full statement, indented, when the family has
// one. It is a no-op for a stable or undeclared family, so a caller may call it
// unconditionally — which is the point: a conditional at every call site is a
// conditional somebody eventually gets wrong.
func printMaturityNote(env *Env, family string, indent string) {
	statement := maturity.Of(family)
	if statement.Message == "" {
		return
	}
	fmt.Fprintf(env.Stdout, "%s%s\n", indent, statement.Message)
	if statement.FeedbackInvited {
		fmt.Fprintf(env.Stdout, "%sReport a compatibility issue with `companion feedback compatibility`.\n", indent)
	}
}

// sortedFamilies turns the set a listing collected into a stable order.
func sortedFamilies(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for family := range set {
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}
