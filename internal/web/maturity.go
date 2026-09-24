package web

import (
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The work-in-progress statement, as the local API serves it.
//
// It rides on every description of a profile and on every pipeline row rather
// than being fetched separately, because a page that had to ask a second
// question to find out whether to warn is a page that will one day draw the row
// before the answer arrives — and a warning that appears a moment after the
// button is a warning the user has already clicked past.
//
// The shape is the same everywhere and is rendered by one function in
// `assets/core.js`, for the same reason [maturity.Statement] carries the whole
// sentence rather than its parts: five call sites formatting it themselves is
// five slightly different warnings.

// describeMaturity renders what this build says about a game family.
//
// `state` is always present, including `undeclared`, so a reader can tell "this
// build says nothing about this family" from "this build has not been asked".
func describeMaturity(family string) map[string]any {
	statement := maturity.Of(family)
	return map[string]any{
		"family":           statement.Family,
		"state":            string(statement.State),
		"work_in_progress": statement.IsWorkInProgress(),
		"badge":            statement.Badge,
		"message":          statement.Message,
		"feedback_invited": statement.FeedbackInvited,
	}
}

// documentFamily reads the AUB engine family a profile declares, or "" for a
// document that applies to a file format rather than to a game.
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
