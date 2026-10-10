package web

import (
	"fmt"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// What a document still needs before it can run here, said BEFORE it is
// installed (NEW_323A).
//
// The New profile wizard's review used to end in one fixed sentence — "Next:
// approve these declarations, choose each executable…" — whatever the document
// was, and a pipeline whose stages were wired to nothing passed the review
// because its JSON parsed. NEW_323 installed exactly that: a pipeline the
// stage's Tool choice had emptied, valid as a document and unable to run.
//
// Two things follow, and both are the answers the rest of the Companion
// already gives rather than a second opinion:
//
//   - composeSetup is [installReadiness] and [pipelineReadiness] asked of the
//     document as it would be once installed: the same checker Profiles, the
//     engine list and the executor read. A document nobody has approved or set
//     up is never "ready" here, and the review says what is left.
//   - pipelineWiringRefusal is [profile.PipelineProfile.Resolve] against the
//     tools installed on this machine. When every stage's tool IS installed and
//     the wiring contradicts what that tool declares, the document cannot run
//     here as written and no setup will make it: compose says it is not valid
//     yet, and import refuses it — for a request that never went near the page
//     as much as for the wizard. A stage whose tool is not installed is a
//     different thing: that is setup still to do, and is listed as such.

// pipelineWiringRefusal is why this pipeline cannot run with the tools
// installed here, or nil. It is nil when a stage's tool is not installed:
// nothing can be said about wiring to an action nobody can read.
func pipelineWiringRefusal(pipeline *profile.PipelineProfile, resolver *build.Resolver) error {
	if resolver == nil {
		return nil
	}
	for _, step := range pipeline.Steps {
		if step.Tool == "" {
			// "Whichever tool provides it" is settled when it runs; two
			// providers are a setup question, not a wiring one.
			if _, _, ok := resolver.Provider(step.Capability); !ok {
				return nil
			}
			continue
		}
		if _, _, ok := resolver.ProviderFrom(step.Tool, step.Capability); !ok {
			return nil
		}
	}
	if _, err := pipeline.Resolve(resolver); err != nil {
		return fmt.Errorf("this pipeline's stages do not fit the tools installed here: %w", err)
	}
	return nil
}

// composeSetup is what is left to do after installing this document, as the
// review shows it. `ready` is the same answer Profiles and Build & Run will
// give once it is installed — no stricter and no kinder. For a tool or an
// engine that includes its approval, because the executor refuses an
// unapproved program. A pipeline starts nothing of its own: it is ready when
// its stages' tools are, and `approved` is reported beside it rather than
// folded into it, since that is what the build list does.
func (s *Server) composeSetup(document profile.Profile, digest string) map[string]any {
	meta := document.Metadata()
	var local binding.LocalBinding
	set, _, err := s.bindings()
	if err == nil {
		// A document being replaced keeps its paths, and its approval only
		// when the digest it was given for is this one.
		local, _ = set.Find(meta.ID)
	}
	entry := job.CatalogEntry{Profile: document, Trust: profile.TrustLocal, Digest: digest}
	approved := profile.Authorize(document, profile.TrustLocal, digest, local.Grant) == nil

	var answer readiness
	if pipeline, isPipeline := document.(*profile.PipelineProfile); isPipeline {
		if runner, err := s.buildRunner(nil); err == nil && set != nil {
			answer = pipelineReadiness(pipeline, runner.Resolver(), set)
		}
	} else {
		answer = installReadiness(entry, local)
		answer.Ready = answer.Ready && approved
	}
	return map[string]any{
		"approved": approved,
		"ready":    answer.Ready,
		"problems": describeProblems(answer.Problems),
	}
}
