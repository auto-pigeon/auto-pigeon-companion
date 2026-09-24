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
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
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
			local = binding.LocalBinding{Trust: profile.TrustLocal}
		}
		local.ProfileID = meta.ID
		local.ProfileVersion = meta.Version
		local.ProfileDigest = digest
		if local.Trust == "" {
			local.Trust = profile.TrustLocal
		}
		local.Acquisition = result.Mode
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
