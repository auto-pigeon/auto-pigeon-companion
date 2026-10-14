package web

import (
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// One answer to "can this profile's programs start on this machine" (NEW_265).
//
// Build & Run's engine list said vkQuake "Needs setup" while Profiles said it
// was ready, because the two asked different questions: the engine list ran
// [engine.Checker] per action, and Profiles looked at whether the document was
// approved and whether any path was recorded. Both surfaces now read this one
// function, which is the checker — the same trust, digest and binding the
// executor authorizes against — so a missing program, an unset game folder or a
// withdrawn approval is named in both places, and nothing is called ready in
// one and not the other.

// readiness is the answer, per action and overall.
type readiness struct {
	// Ready is true when at least one action can start: an engine that cannot
	// host a dedicated server can still play a map.
	Ready bool
	// PerAction is every action's own problems, by action id.
	PerAction map[string]engine.Problems
	// Problems is what stops the profile's first action, the one a person
	// checks: an engine's play_map, a tool's first action. Empty when Ready.
	Problems engine.Problems
}

func installReadiness(entry job.CatalogEntry, local binding.LocalBinding) readiness {
	checker := engine.Checker{Platform: currentPlatform()}
	actions := entry.Profile.ActionList()
	out := readiness{PerAction: make(map[string]engine.Problems, len(actions))}
	primary := ""
	for _, action := range actions {
		if primary == "" || action.ID == profile.ActionPlayMap {
			primary = action.ID
		}
		problems := checker.Check(entry.Profile, entry.Trust, entry.Digest, local, action.ID)
		out.PerAction[action.ID] = problems
		if len(problems) == 0 {
			out.Ready = true
		}
	}
	if !out.Ready && primary != "" {
		out.Problems = out.PerAction[primary]
	}
	// A program that is not what the profile was written for, or a folder that
	// is not the one it needs, makes the whole
	// installation one that needs setting up, whichever of its other programs
	// can start (NEW_323D). Without this a tool whose qbsp is right and whose
	// vis is 0.18.1 read "ready" here — one action can start — while every
	// pipeline that uses it read "needs setup". PerAction is untouched: an
	// action whose own program is fine is still startable on its own.
	seen := map[string]bool{}
	var wrong engine.Problems
	for _, action := range actions {
		for _, problem := range out.PerAction[action.ID] {
			switch problem.Fault {
			// A recorded FOLDER that is the wrong folder is the same kind of
			// fact (Q2_008, found live): a Quake II tool bound to the folder
			// above baseq2 read "ready" here because bspinfo reads no game
			// folder, while every pipeline that compiles with it needed setup.
			// An unbound folder stays per action, as before.
			case engine.FaultToolIncompatible, engine.FaultToolUnusable, engine.FaultToolUnverified,
				engine.FaultMissingGameData, engine.FaultMissingRoot, engine.FaultMissingLibrary:
				if !seen[problem.Summary] {
					seen[problem.Summary] = true
					wrong = append(wrong, problem)
				}
			}
		}
	}
	if len(wrong) > 0 {
		out.Ready = false
		out.Problems = wrong
	}
	return out
}

// view is the readiness as a response carries it.
func (r readiness) view() map[string]any {
	return map[string]any{"ready": r.Ready, "problems": describeProblems(r.Problems)}
}

// Pipeline setup uses the runner's resolver and each resolved action's same
// checker as Profiles and engine execution. Map inputs are supplied later.
func pipelineReadiness(pipeline *profile.PipelineProfile, resolver *build.Resolver, set *binding.Set) readiness {
	out := readiness{Ready: true}
	if err := resolver.CheckSteps(pipeline.Steps); err != nil {
		return readiness{Problems: engine.Problems{{Fault: "conflicting_providers", Summary: err.Error(), Fix: "Inspect the tool profiles in Profiles."}}}
	}
	if _, err := pipeline.Resolve(resolver); err != nil {
		return readiness{Problems: engine.Problems{{Fault: "unresolved_pipeline", Summary: err.Error(), Fix: "Configure this pipeline's dependencies in Profiles."}}}
	}
	for _, step := range pipeline.Steps {
		entry, exists := resolver.EntryForStep(step)
		_, action, provided := resolver.ProviderForStep(step)
		if !exists || !provided {
			continue
		} // Resolve above owns missing capabilities.
		local, _ := set.Find(entry.Profile.Metadata().ID)
		problems := installReadiness(entry, local).PerAction[action.ID]
		if step.Optional {
			continue
		}
		for _, problem := range problems {
			problem.Summary = step.Title + ": " + problem.Summary
			out.Problems = append(out.Problems, problem)
		}
	}
	out.Ready = len(out.Problems) == 0
	return out
}
