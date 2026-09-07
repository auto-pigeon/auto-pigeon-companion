package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/feedback"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/maturity"
)

// `companion feedback` — report that a work-in-progress game did not do what
// you expected.
//
// The whole command is an argument about consent. Nothing is attached unless
// `--share` names it, the finished document is printed for the user to read,
// and nothing is sent anywhere: the last thing this command does is hand over
// a file. Somewhere for it to be sent is a later decision, and one nobody
// should be able to make on a user's behalf inside a command that also
// collects the material.

func runFeedback(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, "error: feedback needs a subcommand: compatibility\n")
		return 2
	}
	switch args[0] {
	case "compatibility", "compat":
		return feedbackCompatibility(env, args[1:])
	}
	fmt.Fprintf(env.Stderr, "error: unknown feedback subcommand %q\n", args[0])
	return 2
}

// shareable names what `--share` accepts, in the order the flag's help lists
// them. A closed vocabulary, because a typo in an opt-in list must be a refusal
// and never a silent "nothing was shared".
var shareNames = []string{"versions", "profiles", "operation", "diagnostics"}

func parseShare(value string) (feedback.Consent, error) {
	var consent feedback.Consent
	for _, name := range strings.Split(value, ",") {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "":
		case "all":
			consent = feedback.Consent{Versions: true, Profiles: true, Operation: true, Diagnostics: true}
		case "versions":
			consent.Versions = true
		case "profiles":
			consent.Profiles = true
		case "operation":
			consent.Operation = true
		case "diagnostics":
			consent.Diagnostics = true
		default:
			return feedback.Consent{}, fmt.Errorf("--share does not know %q; it accepts %s, or `all`",
				strings.TrimSpace(name), strings.Join(shareNames, ", "))
		}
	}
	return consent, nil
}

func feedbackCompatibility(env *Env, args []string) int {
	set := newFlagSet(env, "feedback compatibility")
	game := set.String("game", "", "the game family the report is about (quake2)")
	summary := set.String("summary", "", "one line in your own words")
	describe := set.String("describe", "", "what happened, at more length")
	operation := set.String("operation", "", "what you were doing: compile, package, play_map …")
	fromBuild := set.String("build", "", "take the operation, profiles and diagnostics from a build id")
	share := set.String("share", "", "what to attach: "+strings.Join(shareNames, ", ")+", or `all`. Nothing is attached by default")
	out := set.String("out", "", "write the report to this file instead of standard output")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	statement := maturity.Of(*game)
	if !statement.FeedbackInvited {
		fmt.Fprintf(env.Stderr, "error: this build has no compatibility report to file for %q.\n", *game)
		fmt.Fprintf(env.Stderr, "       Work-in-progress families: %s\n", strings.Join(workInProgressFamilies(), ", "))
		return 2
	}
	if strings.TrimSpace(*summary) == "" {
		fmt.Fprint(env.Stderr, "error: --summary is what the report is; nothing else in it is yours.\n")
		return 2
	}

	consent, err := parseShare(*share)
	if err != nil {
		return fail(env, err)
	}

	input := feedback.Input{
		EngineFamily:     *game,
		Summary:          *summary,
		Description:      *describe,
		Operation:        *operation,
		CompanionVersion: env.Version,
		Platform:         currentPlatform().String(),
	}

	withheld := 0
	if *fromBuild != "" {
		filled, dropped, code := fillFromBuild(env, input, *fromBuild)
		if code != 0 {
			return code
		}
		input, withheld = filled, dropped
	}

	report, err := feedback.Build(input, consent, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		return fail(env, err)
	}
	encoded, err := feedback.Encode(report)
	if err != nil {
		return fail(env, err)
	}

	if *out != "" {
		if err := os.WriteFile(filepath.Clean(*out), encoded, 0o600); err != nil {
			return fail(env, err)
		}
		fmt.Fprintf(env.Stdout, "wrote %s\n", *out)
	} else {
		fmt.Fprintf(env.Stdout, "%s", encoded)
	}

	// After the document, not before it: the point is that the user reads what
	// they are about to hand over.
	fmt.Fprintln(env.Stderr)
	if consent.Nothing() {
		fmt.Fprintf(env.Stderr, "Nothing was attached. Add --share %s (or `all`) to include more.\n",
			strings.Join(shareNames, ","))
	}
	if withheld > 0 {
		fmt.Fprintf(env.Stderr, "%d diagnostic message(s) were left out because they contained a path or "+
			"something shaped like a credential. The rule and the count are still there.\n", withheld)
	}
	fmt.Fprint(env.Stderr, "Nothing has been sent. This file is yours to read and to share if you choose.\n")
	return 0
}

// fillFromBuild reads a build manifest and fills in what a report may carry.
//
// It takes the operation, the documents and the diagnostics — and nothing else.
// The manifest also holds every input path, every output path and the job ids
// that lead to the logs, and none of that goes anywhere near the report: a
// report is assembled from named facts, not filtered out of a record.
func fillFromBuild(env *Env, input feedback.Input, buildID string) (feedback.Input, int, int) {
	settings, err := loadSettings(env)
	if err != nil {
		return input, 0, fail(env, err)
	}
	dir, err := buildsDir(env, settings)
	if err != nil {
		return input, 0, fail(env, err)
	}
	manifest, err := build.Find(dir, buildID)
	if err != nil {
		return input, 0, fail(env, err)
	}

	if input.EngineFamily == "" {
		input.EngineFamily = manifest.EngineFamily
	}
	if input.Operation == "" {
		input.Operation = "build"
	}
	input.Profiles = append(input.Profiles, feedback.ProfileRef{
		Role: "pipeline", ID: manifest.Pipeline.ID, Version: manifest.Pipeline.Version,
	})
	for _, tool := range manifest.Tools {
		input.Profiles = append(input.Profiles, feedback.ProfileRef{
			Role: "tool", ID: tool.Profile.ID, Version: tool.Profile.Version, ToolVersion: tool.ToolVersion,
		})
	}
	var found []feedback.Occurrence
	for _, step := range manifest.Steps {
		for _, d := range step.Diagnostics {
			found = append(found, feedback.Occurrence{
				RuleID: d.RuleID, Severity: string(d.Severity), Message: d.Message,
			})
		}
	}
	diagnostics, withheld := feedback.Summarize(found)
	input.Diagnostics = diagnostics
	return input, withheld, 0
}

func workInProgressFamilies() []string {
	var out []string
	for _, family := range maturity.Families() {
		if maturity.IsWorkInProgress(family) {
			out = append(out, family)
		}
	}
	sort.Strings(out)
	return out
}
