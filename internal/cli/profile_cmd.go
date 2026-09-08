package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

// The `profile` subcommand: everything that can be done to a profile document
// without running anything.
//
// That boundary is the point. Reading a document, checking it, canonicalizing
// it, digesting it, listing what it would ask for and diffing it against
// another are all inert — none of them touches the network on the profile's
// behalf or starts a process — so every one of them is available before a user
// has decided anything. What is *not* here is running: acquiring a tool and
// executing an action need a grant, and they belong to the executor.
//
// It is also how the README's examples became executable. A format documented
// only in prose is a format whose examples are wrong within two releases.

const profileUsage = `usage:
  companion profile validate <file>...          check documents and report every fault
  companion profile show <file>                 what it is, what it would ask for, its digest
  companion profile canonicalize <file>         print the exact bytes a digest is taken over
  companion profile digest <file>...            print each document's digest
  companion profile diff <before> <after>       the normalized difference, and whether it escalates
  companion profile list                        the profiles built into this build
  companion profile schema [name]               list or print the published JSON Schema documents

approving an installed profile (by id, against the same catalog the executor reads):
  companion profile review <id>                 what it would be allowed to do, and whether it may
  companion profile grant <id> --digest=<d> --approve
  companion profile withdraw <id> --confirm

publishing and installing (these reach a backend):
  companion profile preview <file>              what publishing it would disclose
  companion profile publish <file> --confirm    publish it, after the preview
  companion profile catalog [filters]           what this deployment has published
  companion profile published <listing-id>      one listing and its versions
  companion profile install <listing-id>[@ver]  review it; --approve to install
  companion profile yank <listing-id> <version> --reason=<why>
  companion profile report <listing-id> --category=<c>
`

func runProfile(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, profileUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, profileUsage)
		return 0
	case "validate":
		return profileValidate(env, args[1:])
	case "show":
		return profileShow(env, args[1:])
	case "canonicalize":
		return profileCanonicalize(env, args[1:])
	case "digest":
		return profileDigest(env, args[1:])
	case "diff":
		return profileDiff(env, args[1:])
	case "list":
		return profileList(env, args[1:])
	case "schema":
		return profileSchema(env, args[1:])
	case "review":
		return profileReview(env, args[1:])
	case "grant":
		return profileGrant(env, args[1:])
	case "withdraw":
		return profileWithdraw(env, args[1:])
	case "preview":
		return profilePreview(env, args[1:])
	case "publish":
		return profilePublish(env, args[1:])
	case "catalog":
		return profileCatalog(env, args[1:])
	case "published":
		return profilePublished(env, args[1:])
	case "install":
		return profileInstall(env, args[1:])
	case "yank":
		return profileYank(env, args[1:])
	case "report":
		return profileReport(env, args[1:])
	}
	fmt.Fprintf(env.Stderr, "error: unknown profile command %q\n\n", args[0])
	fmt.Fprint(env.Stderr, profileUsage)
	return 2
}

// readProfile loads and validates one document, reporting every fault on its
// own line. The multi-line report is deliberate: a person repairing a profile
// wants the list, not the first item on it.
func readProfile(env *Env, path string) (profile.Profile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return nil, false
	}
	p, err := profile.Decode(data)
	if err != nil {
		fmt.Fprintf(env.Stderr, "%s is not a valid profile:\n", path)
		printProblems(env, err)
		return nil, false
	}
	return p, true
}

func printProblems(env *Env, err error) {
	problems, ok := err.(profile.Problems)
	if !ok {
		fmt.Fprintf(env.Stderr, "  %v\n", err)
		return
	}
	for _, problem := range problems {
		fmt.Fprintf(env.Stderr, "  %s\n", problem.Error())
	}
}

func profileValidate(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, "error: name at least one file\n")
		return 2
	}
	failed := false
	for _, path := range args {
		p, ok := readProfile(env, path)
		if !ok {
			failed = true
			continue
		}
		meta := p.Metadata()
		digest, err := profile.Digest(p)
		if err != nil {
			fmt.Fprintf(env.Stderr, "%s cannot be digested: %v\n", path, err)
			failed = true
			continue
		}
		fmt.Fprintf(env.Stdout, "%s: valid %s profile %s %s\n  %s\n", path, meta.Kind, meta.ID, meta.Version, digest)
	}
	if failed {
		return 1
	}
	return 0
}

func profileShow(env *Env, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(env.Stderr, "error: name exactly one file\n")
		return 2
	}
	p, ok := readProfile(env, args[0])
	if !ok {
		return 1
	}
	digest, err := profile.Digest(p)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}

	// A file named on the command line is not built in, whatever it claims: an
	// id is something a document says about itself, and built-in means the
	// document arrived with the program.
	trust := profile.TrustCommunity
	if entry, err := builtin.Find(p.Metadata().ID); err == nil && entry.Digest == digest {
		trust = profile.TrustBuiltin
	}

	fmt.Fprint(env.Stdout, profile.PermissionReport(p, trust))
	fmt.Fprintf(env.Stdout, "\n  digest: %s\n", digest)
	if trust != profile.TrustBuiltin {
		fmt.Fprint(env.Stdout, "\n  Nothing here has been granted. Importing a profile does not let it do any of the above.\n")
	}
	// Shown for an imported document as much as for a built-in one. The
	// statement is about the game, not about who wrote the profile, so a
	// community Quake II toolchain gets it too — which is the reason it is not
	// a member of the document.
	fmt.Fprintln(env.Stdout)
	printMaturityNote(env, documentFamily(p), "  ")
	return 0
}

func profileCanonicalize(env *Env, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(env.Stderr, "error: name exactly one file\n")
		return 2
	}
	p, ok := readProfile(env, args[0])
	if !ok {
		return 1
	}
	canonical, err := profile.Canonical(p)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "%s\n", canonical)
	return 0
}

func profileDigest(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, "error: name at least one file\n")
		return 2
	}
	failed := false
	for _, path := range args {
		p, ok := readProfile(env, path)
		if !ok {
			failed = true
			continue
		}
		digest, err := profile.Digest(p)
		if err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			failed = true
			continue
		}
		fmt.Fprintf(env.Stdout, "%s  %s\n", digest, path)
	}
	if failed {
		return 1
	}
	return 0
}

func profileDiff(env *Env, args []string) int {
	if len(args) != 2 {
		fmt.Fprint(env.Stderr, "error: name the installed document and the incoming one\n")
		return 2
	}
	before, ok := readProfile(env, args[0])
	if !ok {
		return 1
	}
	after, ok := readProfile(env, args[1])
	if !ok {
		return 1
	}
	diff, err := profile.DiffProfiles(before, after)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprint(env.Stdout, diff.String())
	if diff.Escalates() {
		fmt.Fprint(env.Stdout, "\nThis update asks for more than the installed version did, so it needs your decision again.\n")
	}
	return 0
}

func profileList(env *Env, args []string) int {
	if len(args) != 0 {
		fmt.Fprint(env.Stderr, "error: profile list takes no arguments\n")
		return 2
	}
	entries, err := builtin.Load()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Profile.Metadata().ID < entries[j].Profile.Metadata().ID
	})
	families := map[string]bool{}
	for _, entry := range entries {
		meta := entry.Profile.Metadata()
		family := documentFamily(entry.Profile)
		badge := maturityBadge(family)
		fmt.Fprintf(env.Stdout, "%-8s %-34s %-8s %-8s %s\n", meta.Kind, meta.ID, meta.Version, entry.Trust(), badge)
		fmt.Fprintf(env.Stdout, "         %s\n", meta.Summary)
		if badge != "" {
			families[family] = true
		}
	}
	// The sentence once, after the listing, rather than under every row: eight
	// Quake II documents would print it eight times and it would stop being
	// read on the second.
	for _, family := range sortedFamilies(families) {
		fmt.Fprintln(env.Stdout)
		printMaturityNote(env, family, "")
	}
	return 0
}

func profileSchema(env *Env, args []string) int {
	switch len(args) {
	case 0:
		for _, name := range profile.SchemaFiles() {
			fmt.Fprintln(env.Stdout, name)
		}
		return 0
	case 1:
		data, err := profile.SchemaFile(args[0])
		if err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			fmt.Fprintf(env.Stderr, "published schemas: %s\n", strings.Join(profile.SchemaFiles(), ", "))
			return 2
		}
		fmt.Fprintf(env.Stdout, "%s", data)
		return 0
	}
	fmt.Fprint(env.Stderr, "error: name at most one schema\n")
	return 2
}
