package cli

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// `companion acquire` — obtaining a tool, and being able to say what was
// obtained.
//
// Every subcommand here is either inert or refuses. `plan` fetches and verifies
// the catalogue and downloads nothing; `install` is the only one that writes an
// executable to disk, and it will not do it without a verified chain, a matching
// digest and — where the licence says so — a recorded acknowledgement. There is
// no flag that turns any of that off, because a flag that turns verification off
// is the flag every hostile instruction on the internet tells a user to pass.

const acquireUsage = `usage:
  companion acquire plan    <package> [--version <v>]   what installing it would involve, downloads nothing
  companion acquire install <package> [--version <v>]   download, verify and install
  companion acquire accept  <package> [--version <v>]   record that you were shown the licence notice
  companion acquire list                                what is installed on this machine
  companion acquire verify  [<digest>]                  re-hash cached files against their install records
  companion acquire use     <package> [--version <v>]   check and print an installed package's tool root
  companion acquire gc      [--dry-run]                 remove cache entries nothing refers to
  companion acquire resolve <profile.json> [--mode <m>] [--user-path <dir>] [--root role=path] [--bind]
                                                        get a profile's executables by one of the four routes

flags:
  --offline    never touch the network; installed packages stay usable
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
	case "plan":
		return acquirePlan(env, args[1:])
	case "install":
		return acquireInstall(env, args[1:])
	case "accept":
		return acquireAccept(env, args[1:])
	case "list":
		return acquireList(env, args[1:])
	case "verify":
		return acquireVerify(env, args[1:])
	case "use":
		return acquireUse(env, args[1:])
	case "gc":
		return acquireGC(env, args[1:])
	case "resolve":
		return acquireResolve(env, args[1:])
	}
	fmt.Fprintf(env.Stderr, "error: unknown acquire command %q\n\n", args[0])
	fmt.Fprint(env.Stderr, acquireUsage)
	return 2
}

// acquirePaths resolves everything an acquirer needs from this invocation's
// configuration. An explicit --config puts all of it beside that file, for the
// same reason the job store does: asking for a different config file must not
// leave a test writing into the developer's real cache.
func acquirePaths(env *Env, settings config.Config, offline bool) (acquire.Options, error) {
	options := acquire.Options{Offline: offline || config.Offline()}
	if env.ConfigPath != "" {
		dir := filepath.Dir(env.ConfigPath)
		options.CacheDir = filepath.Join(dir, "packages")
		options.StatePath = filepath.Join(dir, "catalog-state.json")
		options.AcceptancePath = filepath.Join(dir, "license-acceptance.json")
	} else {
		cacheDir, err := settings.ToolCache()
		if err != nil {
			return options, err
		}
		statePath, err := config.CatalogStatePath()
		if err != nil {
			return options, err
		}
		acceptancePath, err := config.LicenseAcceptancePath()
		if err != nil {
			return options, err
		}
		options.CacheDir, options.StatePath, options.AcceptancePath = cacheDir, statePath, acceptancePath
	}
	// A missing address or anchor file is not an error here: `list`, `verify`,
	// `use` and `gc` all work without either, and refusing to open the cache
	// because nothing is configured to download into it would be a strange way
	// to answer "what have I already got".
	if url, err := settings.Catalog(); err == nil {
		options.CatalogURL = url
	}
	if anchors, err := settings.CatalogAnchors(); err == nil {
		options.AnchorsPath = anchors
	}
	return options, nil
}

func newAcquirer(env *Env, args []string, name string, extra func(set *flagSet)) (*acquire.Acquirer, []string, int, bool) {
	set := newFlagSet(env, name)
	offline := set.Bool("offline", false, "never touch the network")
	if extra != nil {
		extra(set)
	}
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return nil, nil, code, false
	}
	settings, err := loadSettings(env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return nil, nil, 1, false
	}
	options, err := acquirePaths(env, settings, *offline)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return nil, nil, 1, false
	}
	acquirer, err := acquire.New(options)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return nil, nil, 1, false
	}
	return acquirer, rest, 0, true
}

// flagSet is what a subcommand's flag hook is handed. An alias rather than a
// wrapper, so a hook registers flags exactly as any other command does.
type flagSet = flag.FlagSet

func acquirePlan(env *Env, args []string) int {
	var version *string
	acquirer, rest, code, ok := newAcquirer(env, args, "acquire plan", func(set *flagSet) {
		version = set.String("version", "", "pin a catalogue version")
	})
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, "error: acquire plan takes exactly one package\n")
		return 2
	}
	ctx, cancel := signalContext()
	defer cancel()
	plan, err := acquirer.Plan(ctx, rest[0], *version)
	if err != nil {
		return acquireError(env, err)
	}
	fmt.Fprint(env.Stdout, plan.Text())
	return 0
}

func acquireInstall(env *Env, args []string) int {
	var version *string
	acquirer, rest, code, ok := newAcquirer(env, args, "acquire install", func(set *flagSet) {
		version = set.String("version", "", "pin a catalogue version")
	})
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, "error: acquire install takes exactly one package\n")
		return 2
	}
	ctx, cancel := signalContext()
	defer cancel()
	install, err := acquirer.Install(ctx, rest[0], *version)
	if err != nil {
		return acquireError(env, err)
	}
	entry, _ := acquirer.Cache().EntryPath(install.Digest)
	fmt.Fprintf(env.Stdout, "installed %s %s for %s\n", install.PackageID, install.Version, install.Platform)
	fmt.Fprintf(env.Stdout, "  digest    %s\n", install.Digest)
	fmt.Fprintf(env.Stdout, "  vouched   key %s, catalogue %s serial %d\n", install.Signer, install.CatalogID, install.CatalogSerial)
	fmt.Fprintf(env.Stdout, "  licence   %s\n", install.License.SPDX)
	fmt.Fprintf(env.Stdout, "  tool root %s\n", install.ToolRoot(entry))
	fmt.Fprintf(env.Stdout, "\n%s\n", install.Aggregation)
	return 0
}

func acquireAccept(env *Env, args []string) int {
	var version *string
	acquirer, rest, code, ok := newAcquirer(env, args, "acquire accept", func(set *flagSet) {
		version = set.String("version", "", "pin a catalogue version")
	})
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, "error: acquire accept takes exactly one package\n")
		return 2
	}
	ctx, cancel := signalContext()
	defer cancel()
	plan, err := acquirer.Accept(ctx, rest[0], *version)
	if err != nil {
		return acquireError(env, err)
	}
	fmt.Fprintf(env.Stdout, "recorded that you were shown the %s notice for %s %s\n",
		plan.Package.License.SPDX, plan.Package.ID, plan.Package.Version)
	fmt.Fprint(env.Stdout, "\nThis is a local note that the notice was shown. It is not a licence, it grants you\n"+
		"nothing, and it does not change what the licence requires of anyone.\n")
	return 0
}

func acquireList(env *Env, args []string) int {
	acquirer, rest, code, ok := newAcquirer(env, args, "acquire list", nil)
	if !ok {
		return code
	}
	if len(rest) != 0 {
		fmt.Fprint(env.Stderr, "error: acquire list takes no arguments\n")
		return 2
	}
	installs, err := acquirer.Cache().List()
	if err != nil {
		return acquireError(env, err)
	}
	if len(installs) == 0 {
		fmt.Fprintf(env.Stdout, "nothing is installed in %s\n", acquirer.Cache().Root())
		return 0
	}
	state, err := acquirer.State()
	if err != nil {
		return acquireError(env, err)
	}
	for _, install := range installs {
		fmt.Fprintf(env.Stdout, "%s %s  %s  %s  %s\n", install.PackageID, install.Version,
			install.Platform, install.License.SPDX, install.Digest)
		if revocation, revoked := state.ArtifactRevocation(install.Digest); revoked {
			fmt.Fprintf(env.Stdout, "  REVOKED on %s: %s\n",
				revocation.At.UTC().Format(time.RFC3339), revocation.Reason)
		}
	}
	return 0
}

func acquireVerify(env *Env, args []string) int {
	acquirer, rest, code, ok := newAcquirer(env, args, "acquire verify", nil)
	if !ok {
		return code
	}
	installs, err := acquirer.Cache().List()
	if err != nil {
		return acquireError(env, err)
	}
	failed := false
	checked := 0
	for _, install := range installs {
		if len(rest) == 1 && install.Digest != rest[0] && install.PackageID != rest[0] {
			continue
		}
		checked++
		if _, err := acquirer.Cache().VerifyEntry(install.Digest); err != nil {
			fmt.Fprintf(env.Stderr, "%s %s: %v\n", install.PackageID, install.Version, err)
			failed = true
			continue
		}
		fmt.Fprintf(env.Stdout, "%s %s: %s, unchanged since installation\n",
			install.PackageID, install.Version, plural(len(install.Files), "file"))
	}
	if checked == 0 {
		fmt.Fprint(env.Stderr, "error: nothing matched\n")
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

func acquireUse(env *Env, args []string) int {
	var version *string
	acquirer, rest, code, ok := newAcquirer(env, args, "acquire use", func(set *flagSet) {
		version = set.String("version", "", "pin an installed version")
	})
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, "error: acquire use takes exactly one package\n")
		return 2
	}
	install, err := acquirer.FindInstalled(rest[0], *version)
	if err != nil {
		return acquireError(env, err)
	}
	_, root, err := acquirer.Use(install.Digest)
	if err != nil {
		return acquireError(env, err)
	}
	fmt.Fprintf(env.Stdout, "%s\n", root)
	return 0
}

func acquireGC(env *Env, args []string) int {
	var dryRun *bool
	acquirer, rest, code, ok := newAcquirer(env, args, "acquire gc", func(set *flagSet) {
		dryRun = set.Bool("dry-run", false, "say what would be removed and remove nothing")
	})
	if !ok {
		return code
	}
	if len(rest) != 0 {
		fmt.Fprint(env.Stderr, "error: acquire gc takes no arguments\n")
		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	references, err := cacheReferences(env, settings)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	collected, err := acquirer.Collect(references, *dryRun)
	if err != nil {
		return acquireError(env, err)
	}
	fmt.Fprint(env.Stdout, collected.Text())
	return 0
}

// acquireResolve is the four acquisition routes, reachable from a terminal.
//
// It is also the only thing that writes a binding's record of which downloads
// it depends on, which is what the cache collector reads. That is not a
// coincidence: a program that could install a package without anything
// recording that it uses one would be a program whose collector deletes a
// working toolchain the first time it runs.
func acquireResolve(env *Env, args []string) int {
	var (
		mode     *string
		userPath *string
		version  *string
		bind     *bool
		roots    rootFlags
	)
	acquirer, rest, code, ok := newAcquirer(env, args, "acquire resolve", func(set *flagSet) {
		mode = set.String("mode", "", "which acquisition route to use; default is the profile's first")
		userPath = set.String("user-path", "", "the directory you are pointing at, for `user_path`")
		version = set.String("version", "", "pin a catalogue version, for `managed_download`")
		bind = set.Bool("bind", false, "record the result in bindings.json")
		set.Var(&roots, "root", "role=path; repeat for several")
	})
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

	ctx, cancel := signalContext()
	defer cancel()
	result, err := acquirer.Resolve(ctx, acquire.Request{
		Option:      option,
		Executables: tool.Executables,
		UserPath:    *userPath,
		Roots:       roots.values(),
		Version:     *version,
	})
	if err != nil {
		return acquireError(env, err)
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
func chooseAcquisition(tool *profile.ToolProfile, mode string) (profile.AcquisitionOption, error) {
	platform := acquire.CurrentPlatform()
	var modes []string
	for _, option := range tool.Acquisition {
		modes = append(modes, string(option.Mode))
		if mode != "" && string(option.Mode) != mode {
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
	set, err := binding.LoadFile(bindingsPath)
	if err != nil && !errors.Is(err, binding.ErrNoFile) {
		return err
	}
	digest, err := profile.Digest(document)
	if err != nil {
		return err
	}
	meta := document.Metadata()
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
	if result.Install != nil {
		local.Installs = pinInstall(local.Installs, binding.PinnedInstall{
			PackageID: result.Install.PackageID,
			Version:   result.Install.Version,
			Digest:    result.Install.Digest,
			Platform:  result.Install.Platform.String(),
			PinnedAt:  time.Now().UTC(),
		})
	}
	local.UpdatedAt = time.Now().UTC()
	if err := set.Put(local); err != nil {
		return err
	}
	return binding.SaveFile(bindingsPath, set)
}

// pinInstall puts the newly resolved download first and keeps the others.
//
// Kept rather than replaced: an older pinned version is still referenced, and
// the collector reads this list. Dropping it here would be how "preserve
// multiple pinned versions" turns into "the last one wins" without anybody
// deciding to.
func pinInstall(existing []binding.PinnedInstall, current binding.PinnedInstall) []binding.PinnedInstall {
	out := []binding.PinnedInstall{current}
	for _, install := range existing {
		if install.Digest != current.Digest {
			out = append(out, install)
		}
	}
	return out
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

// cacheReferences asks every thing that could hold a cache entry.
//
// Both sources are read from disk at the moment of collection rather than
// cached anywhere: a binding added in another window, or a job that finished a
// second ago, is a reference, and a collector working from a stale list would
// delete a toolchain that something started using while it was thinking.
func cacheReferences(env *Env, settings config.Config) ([]acquire.Reference, error) {
	jobsDir, _, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return nil, err
	}
	bindings, err := binding.LoadFile(bindingsPath)
	if err != nil && !errors.Is(err, binding.ErrNoFile) {
		return nil, err
	}
	store, err := job.OpenStore(jobsDir)
	if err != nil {
		return nil, err
	}
	jobs, err := store.List()
	if err != nil {
		return nil, err
	}
	return acquire.ReferencesFrom(bindings, jobs), nil
}

// acquireError turns the errors a user is most likely to hit into an
// explanation and an exit code, rather than a wrapped chain.
func acquireError(env *Env, err error) int {
	switch {
	case errors.Is(err, catalog.ErrNoAnchors), errors.Is(err, catalog.ErrNoCatalogURL):
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		fmt.Fprint(env.Stderr, "\nManaged downloads are refused until both are configured. Nothing is downloaded\n"+
			"unverified in the meantime, and the other acquisition modes — a path you choose,\n"+
			"a command on PATH, a copy that came with a game — do not need either.\n")
	case errors.Is(err, catalog.ErrOffline):
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
	case errors.Is(err, catalog.ErrRollback):
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		fmt.Fprint(env.Stderr, "\nThis is what a replay of a withdrawn catalogue looks like. It is worth finding out\n"+
			"where the answer came from before doing anything about it.\n")
	case errors.Is(err, catalog.ErrRevoked):
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
	case errors.Is(err, acquire.ErrTampered):
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		fmt.Fprint(env.Stderr, "\nThe cached copy is not the one that was installed. It has not been replaced\n"+
			"automatically: re-downloading over it would erase the only evidence of what changed.\n"+
			"Remove the entry deliberately, or investigate it first.\n")
	case errors.Is(err, acquire.ErrLicenseNotAccepted):
		fmt.Fprintf(env.Stderr, "%v\n", err)
	default:
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
	}
	return 1
}
