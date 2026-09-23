package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/publish"
)

// The half of `companion profile` that talks to a backend.
//
// Everything above it in profile_cmd.go is inert — it reads a file and says
// things about it. These six reach a deployment: they offer a document to other
// people, find what other people have offered, and take one.
//
// Two properties they all share. **Nothing publishes or installs without an
// explicit confirmation**, and the confirmation is a flag rather than a prompt so
// that a person and a script make the same decision the same way. And **nothing
// runs**: installing writes a document and a binding, and what runs is still
// `companion job`, behind the grant this recorded.

const profilePublishUsage = `usage:
  companion profile preview <file>                       what publishing it would disclose
  companion profile publish <file> --confirm             publish it, after the preview
  companion profile catalog [filters]                    what this deployment has published
  companion profile published <listing-id>               one listing and its versions
  companion profile install <listing-id>[@<version>]     review it; --approve to install
  companion profile yank <listing-id> <version> --reason=<why>
  companion profile report <listing-id> --category=<c> [--detail=<text>]
`

// toolchainPublishUsage is the same listing in the canonical spelling. See
// toolchainUsage in profile_cmd.go for why it is written out rather than derived.
const toolchainPublishUsage = `usage:
  companion toolchain preview <file>                     what publishing it would disclose
  companion toolchain publish <file> --confirm           publish it, after the preview
  companion toolchain catalog [filters]                  what this deployment has published
  companion toolchain published <listing-id>             one listing and its versions
  companion toolchain install <listing-id>[@<version>]   review it; --approve to install
  companion toolchain yank <listing-id> <version> --reason=<why>
  companion toolchain report <listing-id> --category=<c> [--detail=<text>]
`

// publishUsage is the publishing help for the spelling that was typed. It goes
// to stderr on a bad invocation, which is why it may differ between the two
// spellings: AUP/AUCOM 200F §F9 pins STDOUT and the exit status, and a usage
// block naming a command the reader did not type would be a worse kind of
// identical.
func publishUsage(env *Env) string {
	if env.group() == "profile" {
		return profilePublishUsage
	}
	return toolchainPublishUsage
}

// profilePreview prints the export gate's answer without sending anything.
func profilePreview(env *Env, args []string) int {
	set := newFlagSet(env, "profile preview")
	asJSON := set.Bool("json", false, "print the preview as JSON")
	// Interspersed, for the reason the four commands below already carry: this
	// command's own usage line prints the flag AFTER the positional, and Go's
	// flag package stops at the first non-flag argument — so `profile preview <x>
	// --json` was answered with a usage dump. AUT/AUCOM 219 fixed publish,
	// install, yank and report; these two take a positional too and were missed,
	// which AUT/AUCOM 232 found by typing the documented invocation.
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, publishUsage(env))

		return 2
	}
	document, ok := readProfile(env, rest[0])
	if !ok {
		return 1
	}
	preview, err := publish.PreviewOf(document)
	if err != nil {
		fmt.Fprintf(env.Stderr, "%s cannot be published:\n", rest[0])
		printProblems(env, err)

		return 1
	}
	if *asJSON {
		return printJSON(env, preview)
	}

	return printPublishPreview(env, preview)
}

func printPublishPreview(env *Env, preview publish.Preview) int {
	fmt.Fprintf(env.Stdout, "%s %s (%s)\n", preview.ProfileID, preview.Version, preview.Kind)
	fmt.Fprintf(env.Stdout, "  name        %s\n", preview.Name)
	fmt.Fprintf(env.Stdout, "  licence     %s (of the program this configures)\n", preview.License)
	fmt.Fprintf(env.Stdout, "  digest      %s\n", preview.Digest)
	fmt.Fprintf(env.Stdout, "  bytes       %d\n", preview.Bytes)
	fmt.Fprintln(env.Stdout)
	fmt.Fprintln(env.Stdout, "This is everything the document would make public:")
	kind := ""
	for _, disclosure := range preview.Disclosures {
		if disclosure.Kind != kind {
			kind = disclosure.Kind
			fmt.Fprintf(env.Stdout, "\n  %s\n", kind)
		}
		fmt.Fprintf(env.Stdout, "    %-44s %s\n", disclosure.Path, disclosure.Value)
	}
	fmt.Fprintf(env.Stdout, "\n%s\n", preview.LicenceNote)
	fmt.Fprintln(env.Stdout, "\nNothing has been sent. Add --confirm to `companion toolchain publish` to send it.")

	return 0
}

func profilePublish(env *Env, args []string) int {
	set := newFlagSet(env, "profile publish")
	visibility := set.String("visibility", aub.PublishedPrivate,
		"private, unlisted or public")
	confirm := set.Bool("confirm", false,
		"send it; without this the preview is printed and nothing is published")
	// Interspersed: `profile publish`'s own usage line prints the flags AFTER the
	// positionals, and Go's flag package stops at the first non-flag argument —
	// so the documented invocation was answered with a usage dump. AUT/AUCOM 219
	// found all four of these; every other command in this binary already parsed
	// this way (`game join`, `engine bind`, `acquire plan`).
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, publishUsage(env))

		return 2
	}
	document, ok := readProfile(env, rest[0])
	if !ok {
		return 1
	}
	preview, err := publish.PreviewOf(document)
	if err != nil {
		fmt.Fprintf(env.Stderr, "%s cannot be published:\n", rest[0])
		printProblems(env, err)

		return 1
	}
	if !*confirm {
		printPublishPreview(env, preview)

		return 2
	}

	client, err := publishClient(env)
	if err != nil {
		return fail(env, err)
	}
	result, err := publish.Publish(context.Background(), client, preview, *visibility, true)
	if err != nil {
		return reportAUBError(env, err)
	}
	verb := "published"
	if !result.Created {
		verb = "already published, unchanged"
	}
	fmt.Fprintf(env.Stdout, "%s %s %s\n", result.Profile.ProfileID, result.Version.Version, verb)
	fmt.Fprintf(env.Stdout, "  listing     %s\n", result.Profile.ID)
	fmt.Fprintf(env.Stdout, "  digest      %s\n", result.Version.Digest)
	fmt.Fprintf(env.Stdout, "  visibility  %s\n", result.Profile.Visibility)
	fmt.Fprintf(env.Stdout, "  trust       %s (awarded by the deployment, never claimed here)\n",
		result.Profile.Trust)

	return 0
}

func profileCatalog(env *Env, args []string) int {
	set := newFlagSet(env, "profile catalog")
	query := aub.ProfileQuery{}
	set.BoolVar(&query.Mine, "mine", false, "list your own listings at every visibility")
	set.StringVar(&query.Kind, "kind", "", "tool, engine or pipeline")
	set.StringVar(&query.Game, "game", "", "an engine family, e.g. quake1")
	set.StringVar(&query.Platform, "platform", "", "an os/arch, e.g. linux/amd64")
	set.StringVar(&query.Capability, "capability", "", "a capability id")
	set.StringVar(&query.Trust, "trust", "", "community, verified or builtin")
	set.StringVar(&query.Text, "q", "", "search text")
	limit := set.Int("limit", 25, "how many to list")
	asJSON := set.Bool("json", false, "print the page as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	query.Limit = *limit

	client, err := publishClient(env)
	if err != nil {
		return fail(env, err)
	}
	page, err := client.ProfileCatalog(context.Background(), query)
	if err != nil {
		return reportAUBError(env, err)
	}
	if *asJSON {
		return printJSON(env, page)
	}
	if len(page.Profiles) == 0 {
		fmt.Fprintln(env.Stdout, "nothing published here matches.")

		return 0
	}
	for _, listing := range page.Profiles {
		fmt.Fprintf(env.Stdout, "%-22s %-8s %-10s %-9s %s\n",
			listing.ID, listing.Kind, listing.Trust, listing.LatestVersion, listing.ProfileID)
		fmt.Fprintf(env.Stdout, "  %s — %s\n", listing.Name, listing.Summary)
		fmt.Fprintf(env.Stdout, "  by %s | %s | %s\n",
			publisherName(listing), listing.LicenseSPDX, strings.Join(listing.Platforms, " "))
	}
	if page.NextCursor != "" {
		fmt.Fprintf(env.Stdout, "\nmore: --limit and a cursor (%s)\n", page.NextCursor)
	}
	fmt.Fprintln(env.Stdout,
		"\nCompatibility is not endorsement: a listing reports what its author declared.")

	return 0
}

func publisherName(listing aub.PublishedProfile) string {
	if listing.Publisher.Nickname != "" {
		return listing.Publisher.Nickname
	}
	if listing.Publisher.DeclaredName != "" {
		return listing.Publisher.DeclaredName + " (declared, unverified)"
	}

	return listing.Publisher.UserID
}

func profilePublished(env *Env, args []string) int {
	set := newFlagSet(env, "profile published")
	asJSON := set.Bool("json", false, "print the listing as JSON")
	// Interspersed, for the reason the four commands below already carry: this
	// command's own usage line prints the flag AFTER the positional, and Go's
	// flag package stops at the first non-flag argument — so `profile published <x>
	// --json` was answered with a usage dump. AUT/AUCOM 219 fixed publish,
	// install, yank and report; these two take a positional too and were missed,
	// which AUT/AUCOM 232 found by typing the documented invocation.
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, publishUsage(env))

		return 2
	}
	client, err := publishClient(env)
	if err != nil {
		return fail(env, err)
	}
	detail, err := client.PublishedProfileByID(context.Background(), rest[0])
	if err != nil {
		return reportAUBError(env, err)
	}
	if *asJSON {
		return printJSON(env, detail)
	}
	listing := detail.Profile
	fmt.Fprintf(env.Stdout, "%s (%s)\n", listing.Name, listing.ProfileID)
	fmt.Fprintf(env.Stdout, "  kind        %s\n", listing.Kind)
	fmt.Fprintf(env.Stdout, "  publisher   %s\n", publisherName(listing))
	fmt.Fprintf(env.Stdout, "  licence     %s (of the program this configures)\n", listing.LicenseSPDX)
	if listing.Source.Homepage != "" {
		fmt.Fprintf(env.Stdout, "  homepage    %s\n", listing.Source.Homepage)
	}
	if listing.Source.Repository != "" {
		fmt.Fprintf(env.Stdout, "  repository  %s\n", listing.Source.Repository)
	}
	fmt.Fprintf(env.Stdout, "  trust       %s (this deployment's word, not this machine's)\n", listing.Trust)
	fmt.Fprintf(env.Stdout, "  maintained  %s\n", listing.Maintenance)
	if listing.MaintenanceNote != "" {
		fmt.Fprintf(env.Stdout, "              %s\n", listing.MaintenanceNote)
	}
	if listing.SupersededBy != "" {
		fmt.Fprintf(env.Stdout, "  superseded  by listing %s\n", listing.SupersededBy)
	}
	fmt.Fprintln(env.Stdout, "\n  versions")
	for _, version := range detail.Versions {
		state := ""
		if version.Yanked {
			state = "  WITHDRAWN: " + version.YankReason
			if version.SupersededByVersion != "" {
				state += " (use " + version.SupersededByVersion + ")"
			}
		}
		fmt.Fprintf(env.Stdout, "    %-12s %s%s\n", version.Version, version.Digest, state)
	}

	return 0
}

// profileInstall is the whole install pipeline, and it stops before writing
// anything unless somebody said yes.
func profileInstall(env *Env, args []string) int {
	set := newFlagSet(env, "profile install")
	approve := set.Bool("approve", false,
		"install it, granting what the review lists; without this nothing is written")
	asJSON := set.Bool("json", false, "print the plan as JSON")
	// Interspersed: `profile install`'s own usage line prints the flags AFTER the
	// positionals, and Go's flag package stops at the first non-flag argument —
	// so the documented invocation was answered with a usage dump. AUT/AUCOM 219
	// found all four of these; every other command in this binary already parsed
	// this way (`game join`, `engine bind`, `acquire plan`).
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, publishUsage(env))

		return 2
	}
	listingID, version := rest[0], ""
	if at := strings.LastIndexByte(listingID, '@'); at > 0 {
		listingID, version = listingID[:at], listingID[at+1:]
	}

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	client, err := publishClientFrom(settings)
	if err != nil {
		return fail(env, err)
	}
	_, profilesDir, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return fail(env, err)
	}

	plan, err := publish.PlanInstall(context.Background(), client,
		job.NewCatalog(profilesDir), listingID, version)
	if err != nil {
		return reportInstallError(env, err)
	}
	if *asJSON {
		printJSON(env, plan)
	} else {
		printPlan(env, plan)
	}
	if !*approve {
		fmt.Fprintln(env.Stdout, "\nNothing was written. Add --approve to install and grant this.")

		return 2
	}

	local, err := publish.Apply(plan, publish.InstallPaths{
		Profiles: profilesDir, Bindings: bindingsPath,
	}, true)
	if err != nil {
		return reportInstallError(env, err)
	}
	fmt.Fprintf(env.Stdout, "\ninstalled %s %s (%s)\n",
		local.ProfileID, local.ProfileVersion, local.Trust)
	fmt.Fprintln(env.Stdout, nextStepAfterInstall(plan.Profile().Metadata().Kind, profilesDir,
		local.ProfileID))

	return 0
}

// nextStepAfterInstall names the command that actually exists.
//
// It said `companion profile bind`, which is not a command: `profile` has no
// `bind`, and running it answers `unknown profile command "bind"`. A program
// that ends a successful operation by naming a command of its own that does not
// exist is worse than one that says nothing — the reader has no way to tell
// whether they mistyped it, whether their build is too old, or whether the
// install left something half-done.
//
// The two kinds are bound by two different commands, so the answer depends on
// which one was installed: an engine by `engine bind`, which records the
// executable AND the approval together; a tool by `acquire resolve`, which
// records where the programs are and nothing about approval — the grant for an
// installed profile was already made by the `--approve` that got here.
func nextStepAfterInstall(kind profile.Kind, profilesDir, id string) string {
	switch kind {
	case profile.KindEngine:
		return "run `companion engine bind " + id +
			" --engine <path>` to say where the engine is on this machine."
	case profile.KindPipeline:
		// A pipeline binds nothing of its own: it names capabilities, and the
		// tool profiles that provide them are what get bound.
		return "run `companion build preview --pipeline " + id +
			"` to see which of its stages still needs a tool bound."
	default:
		return "run `companion acquire resolve " +
			filepath.Join(profilesDir, id+".tool.json") +
			" --mode <mode> --bind` to say where the program is on this machine\n" +
			"(`companion acquire resolve` with no --mode lists the routes this profile offers)."
	}
}

func printPlan(env *Env, plan publish.Plan) {
	meta := plan.Profile().Metadata()
	fmt.Fprintf(env.Stdout, "%s %s (%s)\n", meta.ID, meta.Version, meta.Kind)
	fmt.Fprintf(env.Stdout, "  name        %s\n", meta.Name)
	fmt.Fprintf(env.Stdout, "  licence     %s (of the program this configures)\n", meta.License.SPDX)
	fmt.Fprintf(env.Stdout, "  digest      %s (recomputed here)\n", plan.Digest)
	fmt.Fprintf(env.Stdout, "  canonical   %t\n", plan.Canonical)
	fmt.Fprintf(env.Stdout, "  deployment  says %q\n", plan.DeploymentTrust)
	fmt.Fprintf(env.Stdout, "  this machine will record %q — nothing here verified a signature\n",
		plan.Trust)
	if plan.Yanked {
		fmt.Fprintf(env.Stdout, "\n  WITHDRAWN by its publisher: %s\n", plan.YankReason)
		if plan.Superseded != "" {
			fmt.Fprintf(env.Stdout, "  they suggest version %s instead\n", plan.Superseded)
		}
	}

	fmt.Fprintln(env.Stdout, "\n  what changed")
	if plan.FirstInstall {
		fmt.Fprintln(env.Stdout, "    nothing is installed under this id yet")
	} else {
		fmt.Fprintf(env.Stdout, "    %s\n",
			strings.ReplaceAll(strings.TrimSpace(plan.Diff.String()), "\n", "\n    "))
		if plan.Escalates() {
			fmt.Fprintln(env.Stdout,
				"    this asks for MORE than what is installed was granted")
		}
	}

	fmt.Fprintf(env.Stdout, "\n%s\n", publish.LicenceNote)
}

func profileYank(env *Env, args []string) int {
	set := newFlagSet(env, "profile yank")
	reason := set.String("reason", "", "why this version was withdrawn (required)")
	superseded := set.String("superseded-by", "", "the version that replaces it")
	// Interspersed: `profile yank`'s own usage line prints the flags AFTER the
	// positionals, and Go's flag package stops at the first non-flag argument —
	// so the documented invocation was answered with a usage dump. AUT/AUCOM 219
	// found all four of these; every other command in this binary already parsed
	// this way (`game join`, `engine bind`, `acquire plan`).
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 2 {
		fmt.Fprint(env.Stderr, publishUsage(env))

		return 2
	}
	if strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(env.Stderr,
			"error: --reason is required; a withdrawal nobody can explain is worse than none")

		return 2
	}
	client, err := publishClient(env)
	if err != nil {
		return fail(env, err)
	}
	version, err := client.YankPublishedVersion(context.Background(),
		rest[0], rest[1], *reason, *superseded)
	if err != nil {
		return reportAUBError(env, err)
	}
	fmt.Fprintf(env.Stdout, "%s withdrawn: %s\n", version.Version, version.YankReason)
	fmt.Fprintln(env.Stdout,
		"it stays readable, so anybody already using it is told why rather than finding it gone.")

	return 0
}

func profileReport(env *Env, args []string) int {
	set := newFlagSet(env, "profile report")
	category := set.String("category", "",
		"malicious, broken, licence, impersonates or other")
	version := set.String("version", "", "the version this is about, if one")
	detail := set.String("detail", "", "what is wrong, in your own words")
	// Interspersed: `profile report`'s own usage line prints the flags AFTER the
	// positionals, and Go's flag package stops at the first non-flag argument —
	// so the documented invocation was answered with a usage dump. AUT/AUCOM 219
	// found all four of these; every other command in this binary already parsed
	// this way (`game join`, `engine bind`, `acquire plan`).
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 || strings.TrimSpace(*category) == "" {
		fmt.Fprint(env.Stderr, publishUsage(env))

		return 2
	}
	client, err := publishClient(env)
	if err != nil {
		return fail(env, err)
	}
	id, err := client.ReportPublishedProfile(context.Background(),
		rest[0], *category, *version, *detail)
	if err != nil {
		return reportAUBError(env, err)
	}
	fmt.Fprintf(env.Stdout, "reported (%s). This deployment's operator reads it; "+
		"nothing about the listing changed.\n", id)

	return 0
}

// publishClient is a signed-in AUB client, or an error a person can act on.
func publishClient(env *Env) (*aub.Client, error) {
	settings, err := loadSettings(env)
	if err != nil {
		return nil, err
	}

	return publishClientFrom(settings)
}

func publishClientFrom(settings config.Config) (*aub.Client, error) {
	if !settings.Session.Valid() {
		return nil, errors.New("not signed in: run `companion auth login --email <address>`")
	}

	return newClient(settings)
}

func reportInstallError(env *Env, err error) int {
	switch {
	case errors.Is(err, publish.ErrDigestMismatch):
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		fmt.Fprintln(env.Stderr,
			"nothing was written. These bytes are not the ones the catalog named.")

		return 1
	case errors.Is(err, publish.ErrNotCanonical):
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		fmt.Fprintln(env.Stderr,
			"nothing was written. A digest over a non-canonical encoding names bytes "+
				"nobody else would produce for the same document.")

		return 1
	case errors.Is(err, publish.ErrVersionMoved):
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		fmt.Fprintln(env.Stderr,
			"nothing was written. One of these two is not what you reviewed.")

		return 1
	}
	var problems profile.Problems
	if errors.As(err, &problems) {
		fmt.Fprintln(env.Stderr, "the published document is not valid:")
		printProblems(env, err)

		return 1
	}

	return reportAUBError(env, err)
}
