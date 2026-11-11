package cli

import (
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/acquire"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"os"
)

// `companion acquire` — finding a tool's programs on this machine.
//
// One subcommand, and it downloads nothing: the Companion downloads no program
// (operator, 2026-09-23). `resolve` finds a profile's executables by one of the
// three routes it declares — a folder you choose, a command on PATH, a copy
// that came with a game — and, with --bind, records them.

const acquireUsage = `usage:
  companion acquire resolve <profile.json> [--mode <m>] [--user-path <dir>] [--root role=path] [--bind]
                            find a profile's executables by one of its routes (user_path,
                            system_path, already_installed); nothing is downloaded
  companion acquire roots <profile id> --root role=path [--root role=path]
                            say where a TOOL profile's folders are (game_root, content_root, …),
                            by its id; its programs, arguments and approval are left as they are
`

func runAcquire(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, acquireUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, acquireUsage)
		return 0
	case "resolve":
		return acquireResolve(env, args[1:])
	case "roots":
		return acquireRoots(env, args[1:])
	case "plan", "install", "accept", "list", "verify", "use", "gc":
		fmt.Fprintf(env.Stderr, "error: `companion acquire %s` was removed: the Companion downloads no program.\n"+
			"Install the tool yourself, then point its profile at it (Profiles › Configure, or `acquire resolve`).\n", args[0])
		return 2
	}
	fmt.Fprintf(env.Stderr, "error: unknown acquire command %q\n\n", args[0])
	fmt.Fprint(env.Stderr, acquireUsage)
	return 2
}

// flagSet is what a subcommand's flag hook is handed. An alias rather than a
// wrapper, so a hook registers flags exactly as any other command does.
type flagSet = flag.FlagSet

// acquireResolve is the three acquisition routes, reachable from a terminal.
func acquireResolve(env *Env, args []string) int {
	var roots rootFlags
	set := newFlagSet(env, "acquire resolve")
	mode := set.String("mode", "", "which acquisition route to use; default is the profile's first")
	userPath := set.String("user-path", "", "the directory you are pointing at, for `user_path`")
	bind := set.Bool("bind", false, "record the result in bindings.json")
	set.Var(&roots, "root", "role=path; repeat for several")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, "error: acquire resolve takes exactly one profile document\n")
		return 2
	}
	document, ok := readProfile(env, rest[0])
	if !ok {
		return 1
	}
	tool, isTool := document.(*profile.ToolProfile)
	if !isTool {
		fmt.Fprintf(env.Stderr, "error: %s is a %s profile; acquisition applies to tool profiles\n",
			rest[0], document.Metadata().Kind)
		return 2
	}
	option, err := chooseAcquisition(tool, *mode)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}

	result, err := acquire.Resolve(acquire.Request{
		Option:      option,
		Executables: tool.Executables,
		UserPath:    *userPath,
		Roots:       roots.values(),
	})
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(env.Stdout, "%s %s via %s\n  %s\n", tool.Meta.ID, tool.Meta.Version, result.Mode, result.Description)
	for _, name := range sortedNames(result.Executables) {
		fmt.Fprintf(env.Stdout, "  %-16s %s\n", name, result.Executables[name])
	}
	if !*bind {
		fmt.Fprint(env.Stdout, "\nNothing was recorded. Add --bind to write this into bindings.json.\n")
		return 0
	}
	if err := writeBinding(env, document, result, roots.values()); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprint(env.Stdout, "\nrecorded in bindings.json\n")
	return 0
}

// acquireRoots records where a tool profile's folders are, and nothing else.
//
// Issue #36. A pipeline whose tool has no game or project folder reads "needs
// setup", and the fix it named was `acquire resolve <its profile file> --mode
// user_path --user-path <folder> --root … --bind`. For a built-in tool there is
// no profile file to name, and for a tool the Companion installed itself that
// command would replace the managed programs with a folder the person has to
// invent. A folder is a fact about this machine that has nothing to do with
// where the programs are, so it has its own command: by profile ID, as every
// other command names a profile, touching only `roots`.
func acquireRoots(env *Env, args []string) int {
	var roots rootFlags
	set := newFlagSet(env, "acquire roots")
	set.Var(&roots, "root", "role=path; repeat for several")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 || len(roots.values()) == 0 {
		fmt.Fprint(env.Stderr, "error: acquire roots takes one tool profile id and at least one --root role=path\n")
		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	_, profilesDir, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return fail(env, err)
	}
	entries, err := job.NewCatalog(profilesDir).List()
	if err != nil {
		return fail(env, err)
	}
	var entry *job.CatalogEntry
	var tools []string
	for index := range entries {
		if _, isTool := entries[index].Profile.(*profile.ToolProfile); !isTool {
			continue
		}
		tools = append(tools, entries[index].Profile.Metadata().ID)
		if entries[index].Profile.Metadata().ID == rest[0] {
			entry = &entries[index]
		}
	}
	if entry == nil {
		sort.Strings(tools)
		fmt.Fprintf(env.Stderr, "error: no tool profile has the id %q (this machine has: %s)\n", rest[0], strings.Join(tools, ", "))
		return 1
	}
	// Only a folder the profile declares, and only one that is there: a role
	// nothing reads, or a path that is not a directory, is refused before
	// anything is written.
	declared := map[string]bool{}
	for _, action := range entry.Profile.ActionList() {
		for _, ref := range action.Roots {
			declared[ref.Role] = true
		}
	}
	resolved := map[string]string{}
	for role, path := range roots.values() {
		if role == profile.RootWorkspace || role == profile.RootToolInstall {
			fmt.Fprintf(env.Stderr, "error: the %q folder is the Companion's own and is not something to record\n", role)
			return 2
		}
		if !declared[role] {
			names := make([]string, 0, len(declared))
			for name := range declared {
				names = append(names, name)
			}
			sort.Strings(names)
			fmt.Fprintf(env.Stderr, "error: %s reads no %q folder; it reads: %s\n", rest[0], role, strings.Join(names, ", "))
			return 2
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fail(env, err)
		}
		if info, err := os.Stat(absolute); err != nil || !info.IsDir() {
			fmt.Fprintf(env.Stderr, "error: %s is not a directory on this machine\n", absolute)
			return 1
		}
		resolved[role] = absolute
	}
	meta := entry.Profile.Metadata()
	if _, err := binding.Update(bindingsPath, func(set *binding.Set) error {
		local, _ := set.Find(meta.ID)
		if local.ProfileDigest != entry.Digest {
			// The approval is for one exact document; the paths are not.
			local.Grant = nil
		}
		local.ProfileID, local.ProfileVersion, local.ProfileDigest = meta.ID, meta.Version, entry.Digest
		local.Trust = local.Provenance(entry.Trust)
		if local.Roots == nil {
			local.Roots = map[string]string{}
		}
		for role, path := range resolved {
			local.Roots[role] = path
		}
		local.UpdatedAt = time.Now().UTC()
		return set.Put(local)
	}); err != nil {
		return fail(env, err)
	}
	for _, role := range sortedNames(resolved) {
		fmt.Fprintf(env.Stdout, "%s  %s = %s\n", meta.ID, role, resolved[role])
	}
	fmt.Fprint(env.Stdout, "recorded in bindings.json; its programs, arguments and approval are unchanged\n")
	return 0
}

// chooseAcquisition picks the route to use: the one the user named, or the
// profile's first that applies to this machine. The profile lists them in the
// order its author recommends, which is the only ordering anything here knows.
// A legacy `managed_download` route is skipped unless named, and then refused.
func chooseAcquisition(tool *profile.ToolProfile, mode string) (profile.AcquisitionOption, error) {
	platform := acquire.CurrentPlatform()
	var modes []string
	for _, option := range tool.Acquisition {
		modes = append(modes, string(option.Mode))
		if mode != "" && string(option.Mode) != mode {
			continue
		}
		if mode == "" && option.Mode == profile.AcquireManagedDownload {
			continue
		}
		if len(option.Platforms) > 0 && !containsPlatform(option.Platforms, platform) {
			continue
		}
		return option, nil
	}
	if mode != "" {
		return profile.AcquisitionOption{}, fmt.Errorf("%s offers no %q route for %s; it offers: %s",
			tool.Meta.ID, mode, platform, strings.Join(modes, ", "))
	}
	return profile.AcquisitionOption{}, fmt.Errorf("%s offers no acquisition route for %s", tool.Meta.ID, platform)
}

func containsPlatform(platforms []profile.Platform, want profile.Platform) bool {
	for _, platform := range platforms {
		if platform == want {
			return true
		}
	}
	return false
}

// writeBinding records what was resolved.
//
// A grant is not created here and an existing one is kept only while the
// document's digest is unchanged. Where a profile's executables are is a fact
// about this machine; whether the user approved what it does is a decision, and
// resolving an acquisition is not that decision.
func writeBinding(env *Env, document profile.Profile, result *acquire.Result, roots map[string]string) error {
	settings, err := loadSettings(env)
	if err != nil {
		return err
	}
	_, _, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return err
	}
	digest, err := profile.Digest(document)
	if err != nil {
		return err
	}
	meta := document.Metadata()

	// The read and the write inside one cross-process lock, so a resolution
	// running beside a GUI server cannot discard a grant the user recorded
	// there a moment ago. See [binding.Update].
	_, err = binding.Update(bindingsPath, func(set *binding.Set) error {
		local, existed := set.Find(meta.ID)
		if !existed || local.ProfileDigest != digest {
			// A person's own argument tokens are about the program, not the
			// document, and survive a new document like the paths the bind
			// commands keep (NEW_265).
			arguments := local.Arguments
			local = binding.LocalBinding{Trust: profile.TrustLocal, Arguments: arguments}
		}
		local.ProfileID = meta.ID
		local.ProfileVersion = meta.Version
		local.ProfileDigest = digest
		if local.Trust == "" {
			local.Trust = profile.TrustLocal
		}
		local.Acquisition = result.Mode
		local.Managed = nil
		local.Executables = result.Executables
		if local.Roots == nil && len(roots) > 0 {
			local.Roots = map[string]string{}
		}
		for role, path := range roots {
			if role == profile.RootWorkspace {
				continue
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			local.Roots[role] = absolute
		}
		if result.ToolRoot != "" {
			if local.Roots == nil {
				local.Roots = map[string]string{}
			}
			local.Roots[profile.RootToolInstall] = result.ToolRoot
		}
		local.UpdatedAt = time.Now().UTC()
		return set.Put(local)
	})
	return err
}

// rootFlags collects repeated `--root role=path` arguments.
type rootFlags []string

func (r *rootFlags) String() string { return strings.Join(*r, ", ") }
func (r *rootFlags) Set(value string) error {
	role, path, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(role) == "" || strings.TrimSpace(path) == "" {
		return fmt.Errorf("write it as role=path, for example game_root=/games/quake")
	}
	*r = append(*r, value)
	return nil
}

func (r *rootFlags) values() map[string]string {
	if len(*r) == 0 {
		return nil
	}
	out := make(map[string]string, len(*r))
	for _, entry := range *r {
		role, path, _ := strings.Cut(entry, "=")
		out[role] = path
	}
	return out
}

// plural is the difference between "1 files" and "1 file".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// stringList collects a repeated flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ", ") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}
