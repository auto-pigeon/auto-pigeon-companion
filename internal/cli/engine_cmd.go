package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// `companion engine` — pointing the Companion at an engine you already have,
// and starting it.
//
// # Why this is not `companion launch`
//
// `launch` takes a game name and uses a launch config AUB supplies. This takes
// a *profile* and an *action*, which is the model everything else in the
// program is built on: what runs is a document, what it may reach is declared,
// and starting it is a job. Nothing here knows what flags Ironwail takes.
//
// # Nothing here obtains a game
//
// `detect` reports directories that look like an installed Quake and writes
// nothing down. `bind` records where things are, and only from paths named on
// the command line — a detected path becomes a binding when somebody types it,
// and not before. No subcommand copies, downloads or uploads game data, and
// there is no flag that makes one.

const engineUsage = `usage:
  companion engine list [--json]                    engine profiles, and what each claims on this machine
  companion engine show <profile>                   its platforms, actions, layouts and local setup
  companion engine detect [--near <dir>] [--json]   directories that look like an installed game; records nothing
  companion engine bind <profile> --engine <path> [--game-root <dir>] [--content-root <dir>] [--approve]
                                                    record where the engine and the game are on this machine
  companion engine check <profile> --action <id>    what would stop it running here, before anything starts
  companion engine preview <profile> --action <id> [launch flags]   the exact command, starting nothing
  companion engine run <profile> --action <id> [launch flags]       start it as a supervised job
  companion engine stage --game-root <dir> --mod <name> --from <dir>
  companion engine unstage --game-root <dir> --mod <name>

launch flags:
  --map <name>       the map to load          (runtime map_name)
  --mod <name>       the game directory       (runtime mod_name)
  --package <name>   the mod or package       (runtime package_name)
  --server <host>    the server to join       (runtime server_host)
  --port <n>         its port                 (runtime server_port)
  --option n=v       an option the action declares (repeatable)
  --stage <dir>      stage this directory into the game as --mod first, and remove it afterwards
  --keep-staged      leave the staged copy in place when the game exits
`

func runEngine(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, engineUsage)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, engineUsage)
		return 0
	case "list":
		return engineList(env, rest)
	case "show":
		return engineShow(env, rest)
	case "detect":
		return engineDetect(env, rest)
	case "bind":
		return engineBind(env, rest)
	case "check":
		return engineRun(env, rest, modeCheck)
	case "preview":
		return engineRun(env, rest, modePreview)
	case "run":
		return engineRun(env, rest, modeRun)
	case "stage":
		return engineStage(env, rest)
	case "unstage":
		return engineUnstage(env, rest)
	}
	fmt.Fprintf(env.Stderr, "error: unknown engine subcommand %q\n\n", args[0])
	fmt.Fprint(env.Stderr, engineUsage)
	return 2
}

// currentPlatform is the machine a claim is being checked against.
func currentPlatform() profile.Platform {
	return profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// openEngines returns every engine profile this machine can see, from the same
// catalog the executor reads: the ones compiled in, plus any in the profile
// directory. There is no separate list of engines.
func openEngines(env *Env) ([]job.CatalogEntry, string, error) {
	settings, err := loadSettings(env)
	if err != nil {
		return nil, "", err
	}
	_, profilesDir, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return nil, "", err
	}
	entries, err := job.NewCatalog(profilesDir).List()
	if err != nil {
		return nil, bindingsPath, err
	}
	out := make([]job.CatalogEntry, 0, len(entries))
	for _, entry := range entries {
		if _, isEngine := entry.Profile.(*profile.EngineProfile); isEngine {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Profile.Metadata().ID < out[j].Profile.Metadata().ID
	})
	return out, bindingsPath, nil
}

func findEngine(env *Env, id string) (job.CatalogEntry, string, error) {
	entries, bindingsPath, err := openEngines(env)
	if err != nil {
		return job.CatalogEntry{}, bindingsPath, err
	}
	available := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Profile.Metadata().ID == id {
			return entry, bindingsPath, nil
		}
		available = append(available, entry.Profile.Metadata().ID)
	}
	return job.CatalogEntry{}, bindingsPath, fmt.Errorf("no engine profile has the id %q (this machine has: %s)",
		id, strings.Join(available, ", "))
}

func localFor(bindingsPath, profileID string) binding.LocalBinding {
	set, err := binding.LoadFile(bindingsPath)
	if err != nil && !errors.Is(err, binding.ErrNoFile) {
		return binding.LocalBinding{}
	}
	local, _ := set.Find(profileID)
	return local
}

func engineList(env *Env, args []string) int {
	set := newFlagSet(env, "engine list")
	asJSON := set.Bool("json", false, "print the list as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	entries, bindingsPath, err := openEngines(env)
	if err != nil {
		return fail(env, err)
	}
	platform := currentPlatform()

	type row struct {
		ID       string           `json:"id"`
		Name     string           `json:"name"`
		Version  string           `json:"engine_version"`
		Trust    profile.Trust    `json:"trust"`
		Support  profile.Support  `json:"support"`
		Note     string           `json:"note,omitempty"`
		Actions  []string         `json:"actions"`
		Bound    bool             `json:"bound"`
		Platform profile.Platform `json:"platform"`
	}
	rows := make([]row, 0, len(entries))
	for _, entry := range entries {
		document := entry.Profile.(*profile.EngineProfile)
		status, note := document.SupportFor(platform)
		actions := make([]string, 0, len(document.Actions))
		for _, action := range document.Actions {
			actions = append(actions, action.ID)
		}
		local := localFor(bindingsPath, document.ID)
		rows = append(rows, row{
			ID: document.ID, Name: document.Name, Version: document.EngineVersion,
			Trust: entry.Trust, Support: status, Note: note, Actions: actions,
			Bound: len(local.Executables) > 0, Platform: platform,
		})
	}
	if *asJSON {
		return printJSON(env, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(env.Stdout, "no engine profiles")
		return 0
	}
	for _, r := range rows {
		bound := "not set up here"
		if r.Bound {
			bound = "set up here"
		}
		fmt.Fprintf(env.Stdout, "%-36s %-12s %-11s %s\n", r.ID, r.Version, r.Support, bound)
		fmt.Fprintf(env.Stdout, "  %s\n", strings.Join(r.Actions, ", "))
		if r.Note != "" {
			fmt.Fprintf(env.Stdout, "  %s: %s\n", r.Platform, r.Note)
		}
	}
	return 0
}

func engineShow(env *Env, args []string) int {
	set := newFlagSet(env, "engine show")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: engine show takes one profile id")
		return 2
	}
	entry, bindingsPath, err := findEngine(env, rest[0])
	if err != nil {
		return fail(env, err)
	}
	document := entry.Profile.(*profile.EngineProfile)
	platform := currentPlatform()

	fmt.Fprintf(env.Stdout, "%s %s (%s)\n", document.Name, document.EngineVersion, document.ID)
	fmt.Fprintf(env.Stdout, "  %s\n", document.Summary)
	fmt.Fprintf(env.Stdout, "  %s\n", entry.Trust.Describe())
	if document.Source != nil {
		fmt.Fprintf(env.Stdout, "  upstream: %s\n", document.Source.Homepage)
	}
	fmt.Fprintf(env.Stdout, "  licence:  %s\n", document.License.SPDX)
	if document.LastQualified != "" {
		fmt.Fprintf(env.Stdout, "  checked:  %s against %s %s\n", document.LastQualified, document.Name, document.EngineVersion)
	}

	fmt.Fprintln(env.Stdout, "\nplatforms:")
	for _, support := range document.Platforms {
		here := "  "
		if support.Platform == platform {
			here = "* "
		}
		fmt.Fprintf(env.Stdout, "  %s%-16s %-11s %s\n", here, support.Platform, support.Status, support.Note)
	}

	fmt.Fprintln(env.Stdout, "\nactions:")
	for _, action := range document.Actions {
		fmt.Fprintf(env.Stdout, "  %-16s %-17s %s\n", action.ID, action.SessionRole, action.Title)
	}
	missing := make([]string, 0)
	for _, id := range profile.EngineActions {
		if _, found := document.ActionByID(id); !found {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		does := "it"
		if len(missing) > 1 {
			does = "them"
		}
		fmt.Fprintf(env.Stdout, "  not offered: %s — this engine does not do %s\n", strings.Join(missing, ", "), does)
	}

	if len(document.ContentLayouts) > 0 {
		fmt.Fprintln(env.Stdout, "\ncontent:")
		for _, l := range document.ContentLayouts {
			where := l.Root
			if l.Path != "" {
				where += "/" + l.Path
			}
			fmt.Fprintf(env.Stdout, "  %-10s %-14s %s\n", l.ID, l.Kind, where)
		}
	}

	local := localFor(bindingsPath, document.ID)
	fmt.Fprintln(env.Stdout, "\non this machine:")
	if len(local.Executables) == 0 && len(local.Roots) == 0 {
		fmt.Fprintf(env.Stdout, "  nothing recorded — `companion engine bind %s --engine <path>`\n", document.ID)
		return 0
	}
	for _, name := range sortedNames(local.Executables) {
		fmt.Fprintf(env.Stdout, "  %-14s %s\n", name, local.Executables[name])
	}
	for _, role := range sortedNames(local.Roots) {
		fmt.Fprintf(env.Stdout, "  %-14s %s\n", role, local.Roots[role])
	}
	if local.Grant != nil {
		fmt.Fprintf(env.Stdout, "  approved       %s\n", local.Grant.GrantedAt.Format(time.RFC3339))
	} else if entry.Trust != profile.TrustBuiltin {
		fmt.Fprintln(env.Stdout, "  approved       no — add --approve to `engine bind`")
	}
	return 0
}

func engineDetect(env *Env, args []string) int {
	set := newFlagSet(env, "engine detect")
	asJSON := set.Bool("json", false, "print the candidates as JSON")
	var near stringList
	set.Var(&near, "near", "also consider this directory (repeatable); the engine's own folder is a good guess")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	candidates := engine.Scanner{Extra: near}.Detect()
	if *asJSON {
		return printJSON(env, candidates)
	}
	if len(candidates) == 0 {
		fmt.Fprintln(env.Stdout, "no installed game found in the usual places.")
		fmt.Fprintln(env.Stdout, "Point the Companion at your own copy: `companion engine bind <profile> --game-root <dir>`.")
		return 0
	}
	for _, candidate := range candidates {
		fmt.Fprintf(env.Stdout, "%-8s %s\n", candidate.Source, candidate.Path)
		fmt.Fprintf(env.Stdout, "         found %s\n", candidate.Evidence)
		if candidate.Note != "" {
			fmt.Fprintf(env.Stdout, "         %s\n", candidate.Note)
		}
	}
	fmt.Fprint(env.Stdout, "\nNothing was recorded. These are guesses about your machine, not decisions:\n"+
		"pass the one you want to `companion engine bind ... --game-root <path>`.\n")
	return 0
}

func engineBind(env *Env, args []string) int {
	set := newFlagSet(env, "engine bind")
	enginePath := set.String("engine", "", "the engine executable on this machine")
	gameRoot := set.String("game-root", "", "the directory the game is installed in")
	contentRoot := set.String("content-root", "", "your project directory")
	approve := set.Bool("approve", false, "record that you approve everything this profile asks for")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: engine bind takes one profile id")
		return 2
	}
	entry, bindingsPath, err := findEngine(env, rest[0])
	if err != nil {
		return fail(env, err)
	}
	document := entry.Profile.(*profile.EngineProfile)
	if *enginePath == "" && *gameRoot == "" && *contentRoot == "" && !*approve {
		fmt.Fprintln(env.Stderr, "error: engine bind needs something to record: --engine, --game-root, --content-root or --approve")
		return 2
	}

	set2, err := binding.LoadFile(bindingsPath)
	if err != nil && !errors.Is(err, binding.ErrNoFile) {
		return fail(env, err)
	}
	local, existed := set2.Find(document.ID)
	if !existed || local.ProfileDigest != entry.Digest {
		// A binding is against one exact document. When the document has
		// changed, what was recorded against the old one — including the
		// approval — does not carry over.
		local = binding.LocalBinding{Trust: entry.Trust}
	}
	local.ProfileID = document.ID
	local.ProfileVersion = document.Version
	local.ProfileDigest = entry.Digest
	local.Trust = entry.Trust
	local.Acquisition = profile.AcquireUserPath
	if *enginePath != "" {
		absolute, err := filepath.Abs(*enginePath)
		if err != nil {
			return fail(env, err)
		}
		if info, err := os.Stat(absolute); err != nil || info.IsDir() {
			fmt.Fprintf(env.Stderr, "error: %s is not a program on this machine\n", absolute)
			return 1
		}
		if local.Executables == nil {
			local.Executables = map[string]string{}
		}
		for _, executable := range document.Executables {
			local.Executables[executable.Name] = absolute
		}
	}
	for _, pair := range []struct {
		role, value string
	}{{profile.RootGame, *gameRoot}, {profile.RootContent, *contentRoot}} {
		if pair.value == "" {
			continue
		}
		absolute, err := filepath.Abs(pair.value)
		if err != nil {
			return fail(env, err)
		}
		if info, err := os.Stat(absolute); err != nil || !info.IsDir() {
			fmt.Fprintf(env.Stderr, "error: %s is not a directory on this machine\n", absolute)
			return 1
		}
		if local.Roots == nil {
			local.Roots = map[string]string{}
		}
		local.Roots[pair.role] = absolute
	}
	if *approve {
		local.Grant = profile.NewGrant(document, entry.Trust, entry.Digest, time.Now())
	}
	local.UpdatedAt = time.Now().UTC()
	if err := set2.Put(local); err != nil {
		return fail(env, err)
	}
	if err := binding.SaveFile(bindingsPath, set2); err != nil {
		return fail(env, err)
	}

	fmt.Fprintf(env.Stdout, "recorded for %s:\n", document.ID)
	for _, name := range sortedNames(local.Executables) {
		fmt.Fprintf(env.Stdout, "  %-14s %s\n", name, local.Executables[name])
	}
	for _, role := range sortedNames(local.Roots) {
		fmt.Fprintf(env.Stdout, "  %-14s %s\n", role, local.Roots[role])
	}
	if *approve {
		fmt.Fprintf(env.Stdout, "  approved       everything %s asks for, against %s\n", document.ID, entry.Digest)
	}
	return 0
}

// launchFlags is the shape check, preview and run share.
type launchFlags struct {
	action     *string
	mapName    *string
	modName    *string
	pkgName    *string
	serverHost *string
	serverPort *string
	options    pairs
	stage      *string
	keepStaged *bool
	wait       *bool
}

func registerLaunchFlags(set *flag.FlagSet) *launchFlags {
	f := &launchFlags{
		action:     set.String("action", "", "which action of the profile to run"),
		mapName:    set.String("map", "", "the map to load"),
		modName:    set.String("mod", "", "the game directory"),
		pkgName:    set.String("package", "", "the mod or package to play"),
		serverHost: set.String("server", "", "the server to join"),
		serverPort: set.String("port", "", "the server's port"),
		options:    pairs{},
		stage:      set.String("stage", "", "stage this directory into the game as --mod before starting"),
		keepStaged: set.Bool("keep-staged", false, "leave the staged copy in place afterwards"),
		wait:       set.Bool("wait", true, "wait for the game to exit"),
	}
	set.Var(f.options, "option", "an option the action declares, as name=value (repeatable)")
	return f
}

func (f *launchFlags) runtime() map[string]string {
	out := map[string]string{}
	for name, value := range map[string]string{
		profile.RuntimeMapName:     *f.mapName,
		profile.RuntimeModName:     *f.modName,
		profile.RuntimePackageName: *f.pkgName,
		profile.RuntimeServerHost:  *f.serverHost,
		profile.RuntimeServerPort:  *f.serverPort,
	} {
		if value != "" {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type launchMode int

const (
	modeCheck launchMode = iota
	modePreview
	modeRun
)

func engineRun(env *Env, args []string, mode launchMode) int {
	names := map[launchMode]string{modeCheck: "engine check", modePreview: "engine preview", modeRun: "engine run"}
	set := newFlagSet(env, names[mode])
	flags := registerLaunchFlags(set)
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintf(env.Stderr, "error: %s takes one profile id\n", names[mode])
		return 2
	}
	if *flags.action == "" {
		fmt.Fprintf(env.Stderr, "error: %s requires --action (one of: %s)\n", names[mode], strings.Join(profile.EngineActions, ", "))
		return 2
	}
	entry, bindingsPath, err := findEngine(env, rest[0])
	if err != nil {
		return fail(env, err)
	}
	local := localFor(bindingsPath, entry.Profile.Metadata().ID)

	// The preflight runs for all three modes, and only `run` treats it as
	// fatal. Seeing the command a broken setup *would* produce is exactly what
	// somebody repairing that setup wants.
	problems := engine.Checker{Platform: currentPlatform()}.
		Check(entry.Profile, entry.Trust, entry.Digest, local, *flags.action)
	for _, problem := range problems {
		fmt.Fprintf(env.Stderr, "%s\n  %s\n", problem.Summary, problem.Fix)
	}
	if mode == modeCheck {
		if len(problems) == 0 {
			fmt.Fprintf(env.Stdout, "%s %s would run here.\n", entry.Profile.Metadata().ID, *flags.action)
			return 0
		}
		return 1
	}
	if mode == modeRun && len(problems) > 0 {
		return 1
	}

	request := job.Request{
		ProfileID: entry.Profile.Metadata().ID,
		ActionID:  *flags.action,
		Runtime:   flags.runtime(),
		Options:   flags.options.orNil(),
		Label:     *flags.action,
	}

	ctx, stop := signalContext()
	defer stop()

	service, _, err := openJobs(ctx, env, mode == modeRun, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	if mode == modePreview {
		previewed, err := service.Preview(request)
		if err != nil {
			return fail(env, err)
		}
		printPreview(env, previewed)
		if previewed.Error != "" {
			fmt.Fprintf(env.Stderr, "\nthis would not run yet: %s\n", previewed.Error)
			return 1
		}
		return 0
	}

	if *flags.stage != "" {
		if *flags.modName == "" {
			fmt.Fprintln(env.Stderr, "error: --stage needs --mod: the game directory the content is staged into is what the engine is given for -game")
			return 2
		}
		staging := engine.Staging{
			GameRoot:  local.Roots[profile.RootGame],
			ModName:   *flags.modName,
			Source:    *flags.stage,
			ProfileID: request.ProfileID,
		}
		stamp, err := staging.Stage()
		if err != nil {
			return fail(env, err)
		}
		fmt.Fprintf(env.Stderr, "staged %d files into %s\n", len(stamp.Files), staging.Dir())
		if !*flags.keepStaged {
			defer func() {
				kept, err := engine.Unstage(staging.GameRoot, staging.ModName)
				switch {
				case err != nil:
					fmt.Fprintf(env.Stderr, "warning: %v\n", err)
				case len(kept) > 0:
					fmt.Fprintf(env.Stderr, "left %d changed file(s) in %s: %s\n",
						len(kept), staging.Dir(), strings.Join(kept, ", "))
				default:
					fmt.Fprintf(env.Stderr, "removed the staged copy from %s\n", staging.Dir())
				}
			}()
		}
	}

	submitted, err := service.SubmitWatched(request, env.Stdout)
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stderr, "job %s: %s%s\n", submitted.ID, submitted.ActionID, sessionSuffix(submitted))
	if !*flags.wait {
		return 0
	}
	finished, err := service.Wait(ctx, submitted.ID)
	if err != nil {
		return fail(env, err)
	}
	printOutcome(env, finished)
	if finished.Succeeded() {
		return 0
	}
	return 1
}

func engineStage(env *Env, args []string) int {
	set := newFlagSet(env, "engine stage")
	gameRoot := set.String("game-root", "", "the directory the game is installed in")
	mod := set.String("mod", "", "the game directory to stage into")
	from := set.String("from", "", "the directory whose contents are staged")
	dryRun := set.Bool("dry-run", false, "list what would be staged and copy nothing")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *gameRoot == "" || *mod == "" || *from == "" {
		fmt.Fprintln(env.Stderr, "error: engine stage needs --game-root, --mod and --from")
		return 2
	}
	staging := engine.Staging{GameRoot: *gameRoot, ModName: *mod, Source: *from}
	if *dryRun {
		planned, err := staging.Plan()
		if err != nil {
			return fail(env, err)
		}
		for _, name := range planned {
			fmt.Fprintln(env.Stdout, name)
		}
		fmt.Fprintf(env.Stdout, "%d file(s) would be copied into %s\n", len(planned), staging.Dir())
		return 0
	}
	stamp, err := staging.Stage()
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "staged %d file(s) into %s\n", len(stamp.Files), staging.Dir())
	fmt.Fprintf(env.Stdout, "start the engine with --mod %s; `companion engine unstage` removes exactly these files\n", *mod)
	return 0
}

func engineUnstage(env *Env, args []string) int {
	set := newFlagSet(env, "engine unstage")
	gameRoot := set.String("game-root", "", "the directory the game is installed in")
	mod := set.String("mod", "", "the staged game directory to remove")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *gameRoot == "" || *mod == "" {
		fmt.Fprintln(env.Stderr, "error: engine unstage needs --game-root and --mod")
		return 2
	}
	kept, err := engine.Unstage(*gameRoot, *mod)
	if err != nil {
		return fail(env, err)
	}
	if len(kept) == 0 {
		fmt.Fprintf(env.Stdout, "removed the staged copy from %s\n", filepath.Join(*gameRoot, *mod))
		return 0
	}
	fmt.Fprintf(env.Stdout, "removed the staged copy from %s, and left %d file(s) that had changed since:\n",
		filepath.Join(*gameRoot, *mod), len(kept))
	for _, name := range kept {
		fmt.Fprintf(env.Stdout, "  %s\n", name)
	}
	return 0
}
