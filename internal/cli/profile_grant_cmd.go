package cli

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/approval"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// `profile review`, `profile grant` and `profile withdraw`: approving an
// INSTALLED document, by id.
//
// # Why these are separate from `profile show`
//
// `profile show` takes a FILE. It is the inert half of this package — read it,
// check it, digest it, print what it would ask for — and it works on a document
// that is nowhere near this machine's profile directory. Approval is the other
// thing entirely: it is about one of the documents the executor can actually
// see, it is recorded in the binding store beside the paths and the pins, and it
// is what the difference between "this is data" and "this may start a process"
// is made of. So it is addressed the way the executor addresses a profile — by
// id, against the same catalog — and never by a path.
//
// # The contract, and why it is three flags and not a prompt
//
//	companion profile review <id>
//	companion profile grant <id> --digest=sha256:… --approve
//	companion profile withdraw <id> --confirm
//
// A prompt would hang a script, and a script that cannot approve a profile is a
// script that has to start the GUI server to do it — which is precisely the
// browser-only path AUCOM/AUT 228 exists to remove. So the review is a command
// whose whole output is readable, and the approval names the digest that review
// printed. Supplying it is what makes the approval an approval of something: if
// the document changed in between, the digest no longer matches and the grant is
// refused rather than silently applied to bytes nobody read.
//
// `--approve` is not redundant beside `--digest`. A digest says WHICH document;
// `--approve` says that a person decided. `profile grant <id>` with neither
// prints the review and refuses, naming the exact command to run next, so the
// path a user stumbles into is the path that shows them what they are about to
// allow.

func profileReview(env *Env, args []string) int {
	set := newFlagSet(env, "profile review")
	asJSON := set.Bool("json", false, "print the review as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: toolchain review takes one toolchain id")
		return 2
	}
	service, err := openApprovals(env)
	if err != nil {
		return fail(env, err)
	}
	decision, err := service.Review(rest[0])
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, describeDecision(decision))
	}
	printDecision(env, decision)
	return 0
}

func profileGrant(env *Env, args []string) int {
	set := newFlagSet(env, "profile grant")
	digest := set.String("digest", "", "the digest of the document you reviewed; an approval names exactly what it approves")
	approve := set.Bool("approve", false, "record that you approve everything this profile asks for")
	asJSON := set.Bool("json", false, "print the resulting state as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: toolchain grant takes one toolchain id")
		return 2
	}
	id := rest[0]
	service, err := openApprovals(env)
	if err != nil {
		return fail(env, err)
	}

	// Nothing decided yet: show what would be approved, and the command that
	// would approve it. Exit 2, because the invocation was incomplete — a
	// script that leaves out `--approve` must not be able to read this as
	// success.
	if !*approve || *digest == "" {
		decision, err := service.Review(id)
		if err != nil {
			return fail(env, err)
		}
		printDecision(env, decision)
		fmt.Fprintln(env.Stderr)
		switch {
		case !*approve && *digest == "":
			fmt.Fprintln(env.Stderr, "error: nothing was approved. Read the report above, then:")
		case !*approve:
			fmt.Fprintln(env.Stderr, "error: --digest says which document; --approve says you decided. Then:")
		default:
			fmt.Fprintln(env.Stderr, "error: an approval names the exact document being approved. Then:")
		}
		fmt.Fprintf(env.Stderr, "  companion toolchain grant %s --digest=%s --approve\n", id, decision.Entry.Digest)
		return 2
	}

	decision, err := service.Grant(id, *digest)
	if err != nil {
		var stale *approval.StaleDigestError
		if errors.As(err, &stale) {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return 1
		}
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, describeDecision(decision))
	}
	fmt.Fprintf(env.Stdout, "approved %s, against %s\n",
		decision.Entry.Profile.Metadata().ID, decision.Entry.Digest)
	fmt.Fprintf(env.Stdout, "\nWithdraw it with `companion toolchain withdraw %s --confirm`.\n",
		decision.Entry.Profile.Metadata().ID)
	return 0
}

func profileWithdraw(env *Env, args []string) int {
	set := newFlagSet(env, "profile withdraw")
	confirm := set.Bool("confirm", false, "take the approval back; nothing else this profile was granted survives it")
	asJSON := set.Bool("json", false, "print the resulting state as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: toolchain withdraw takes one toolchain id")
		return 2
	}
	id := rest[0]
	service, err := openApprovals(env)
	if err != nil {
		return fail(env, err)
	}
	if !*confirm {
		fmt.Fprintf(env.Stderr,
			"error: withdrawing an approval stops %s running until it is approved again. Add --confirm:\n"+
				"  companion toolchain withdraw %s --confirm\n", id, id)
		return 2
	}
	decision, err := service.Withdraw(id)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, describeDecision(decision))
	}
	fmt.Fprintf(env.Stdout, "withdrew the approval for %s. It cannot run until it is approved again.\n",
		decision.Entry.Profile.Metadata().ID)
	// Said out loud, because it is the surprising half: a withdrawal is about
	// the decision and not about the setup.
	if len(decision.Binding.Executables) > 0 || len(decision.Binding.Roots) > 0 {
		fmt.Fprintln(env.Stdout, "Where its programs and directories are on this machine is unchanged.")
	}
	return 0
}

// openApprovals builds the approval service for one CLI invocation.
//
// The EXECUTOR's catalog chain, not a second one: documents in the profile
// directory, then the engine profiles generated from this machine's launch
// configs. Approving against a different catalog from the one that runs things
// would make it possible to approve a document that never runs, or to run one
// that was never offered for approval.
func openApprovals(env *Env) (approval.Service, error) {
	settings, err := loadSettings(env)
	if err != nil {
		return approval.Service{}, err
	}
	_, profilesDir, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return approval.Service{}, err
	}
	return approval.Service{
		Catalog:      job.NewCatalog(profilesDir),
		BindingsPath: bindingsPath,
	}, nil
}

// printDecision is the review a person reads before deciding: what the document
// is, what it would be allowed to do, its digest, and where this machine stands
// on it now.
func printDecision(env *Env, decision approval.Decision) {
	entry := decision.Entry
	fmt.Fprint(env.Stdout, decision.Report)
	fmt.Fprintf(env.Stdout, "\n  digest: %s\n", entry.Digest)
	fmt.Fprintf(env.Stdout, "  source: %s\n", entry.Source)

	grant := decision.Binding.Grant
	switch {
	case decision.Authorized && entry.Trust == profile.TrustBuiltin:
		fmt.Fprintln(env.Stdout, "  approved: it arrived with this build, which is the decision you made by installing it")
	case decision.Authorized:
		fmt.Fprintf(env.Stdout, "  approved: %s, against this exact document\n",
			grant.GrantedAt.Format(time.RFC3339))
	case grant != nil:
		fmt.Fprintf(env.Stdout, "  approved: no — the approval on this machine covers %s\n", grant.Digest)
	default:
		fmt.Fprintln(env.Stdout, "  approved: no — nothing has been granted, so it cannot run")
	}
	// The refusal, when it says something the report above did not. A
	// never-reviewed profile's refusal is the whole permission list again, and
	// printing a list twice is how a review stops being read; a widened or
	// changed one names only what is new, which is exactly what a second
	// decision has to be about.
	var notGranted *profile.NotGrantedError
	if decision.AuthorizationError != nil &&
		!(errors.As(decision.AuthorizationError, &notGranted) && notGranted.Reason == "not_reviewed") {
		fmt.Fprintf(env.Stdout, "\n  %s\n", decision.AuthorizationError)
	}
	fmt.Fprintln(env.Stdout)
	printMaturityNote(env, documentFamily(entry.Profile), "  ")
}

// describeDecision is the same review as JSON, for a script.
//
// `authorized` is the service's own verdict rather than something a reader has
// to assemble from `trust` and `grant`, for the reason [approval.Decision] gives.
func describeDecision(decision approval.Decision) map[string]any {
	meta := decision.Entry.Profile.Metadata()
	body := map[string]any{
		"id":         meta.ID,
		"kind":       meta.Kind,
		"version":    meta.Version,
		"name":       meta.Name,
		"trust":      decision.Entry.Trust,
		"digest":     decision.Entry.Digest,
		"source":     decision.Entry.Source,
		"report":     decision.Report,
		"authorized": decision.Authorized,
		"permissions": func() []map[string]any {
			permissions := decision.Entry.Profile.Permissions()
			out := make([]map[string]any, 0, len(permissions))
			for _, permission := range permissions {
				out = append(out, map[string]any{
					"id": permission.ID, "summary": permission.Summary, "risk": permission.Risk,
				})
			}
			return out
		}(),
	}
	if decision.AuthorizationError != nil {
		body["authorization_error"] = decision.AuthorizationError.Error()
	}
	if grant := decision.Binding.Grant; grant != nil {
		granted := append([]string(nil), grant.Granted...)
		sort.Strings(granted)
		body["grant"] = map[string]any{
			"digest": grant.Digest, "version": grant.Version, "trust": grant.Trust,
			"granted": granted, "granted_at": grant.GrantedAt.Format(time.RFC3339),
		}
	}
	body["binding"] = describeLocalBinding(decision.Binding)
	return body
}

// describeLocalBinding is the machine half, with nothing invented for a binding
// that does not exist yet.
func describeLocalBinding(local binding.LocalBinding) map[string]any {
	body := map[string]any{"bound": local.ProfileID != ""}
	if local.ProfileID == "" {
		return body
	}
	body["profile_digest"] = local.ProfileDigest
	body["acquisition"] = local.Acquisition
	if len(local.Executables) > 0 {
		body["executables"] = local.Executables
	}
	if len(local.Roots) > 0 {
		body["roots"] = local.Roots
	}
	return body
}
