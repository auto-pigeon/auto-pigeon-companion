package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3install"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3run"
)

// `companion package map` — one finished Quake III build as one PK3 that
// carries the map and only what somebody said may be redistributed, installed
// beside a game and loaded in an engine.
//
// It is a sibling of `package preview` / `package create` and not a mode of
// them, because it answers a different question. Those package what you SELECT
// and review what it lacks; this works out what the compiled map needs, twice —
// what the compiler read and what an engine will read — and packages the second
// set under the grants you give. Quake 1 and Quake II packaging is the other
// pair, unchanged.

const packageMapUsage = `usage:
  companion package map preview --build <id> [grants] [--include <path>]... [--name <map>] [--json]
                                             what the archive would hold and why; writes nothing
  companion package map create  --build <id> [grants] [--include <path>]... [--name <map>]
                                [--out <dir>] [--accept-missing --reason <text>] [--json]
  companion package map list [--json]
  companion package map show <package> [--json]

  companion package map install <package> --engine <profile> [--into managed|game-folder]
                                [--mod <name>] [--base-game <name>] [--game-root <dir>] [--dry-run] [--json]
  companion package map installed [--json]
  companion package map run <installation> --engine <profile> [--action <id>]
                                [--option name=value]... [--wait <duration>] [--check] [--json]
  companion package map uninstall <installation>

<package> is an id from ` + "`package map list`" + `, or a directory ` + "`--out`" + ` wrote.

grants — who may redistribute a file the map needs (nothing is packaged without one):
  --own-archive <sha256>             an archive whose contents are your own work (repeatable)
  --own-loose                        the loose files of your content folder are your own work
  --own-path <path>                  one file is your own work (repeatable)
  --licensed-archive <sha256>=<licence>   you hold this licence for an archive's contents
  --licensed-loose <licence>
  --licensed-path <path>=<licence>
  --deny-archive <sha256>            it is not yours to redistribute, and stays out
  --deny-loose
  --deny-path <path>
  --statement <text>                 recorded verbatim with every grant given here

An archive is named by its digest, which ` + "`preview`" + ` prints under "sources".
`

func runPackageMap(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, packageMapUsage)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "preview":
		return packageMapPlan(env, rest, true)
	case "create":
		return packageMapPlan(env, rest, false)
	case "list":
		return packageMapList(env, rest)
	case "show":
		return packageMapShow(env, rest)
	case "install":
		return packageMapInstall(env, rest)
	case "installed":
		return packageMapInstalled(env, rest)
	case "run":
		return packageMapRun(env, rest)
	case "uninstall":
		return packageMapUninstall(env, rest)
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, packageMapUsage)
		return 0
	}
	fmt.Fprintf(env.Stderr, "error: unknown package map subcommand %q\n\n", args[0])
	fmt.Fprint(env.Stderr, packageMapUsage)
	return 2
}

// q3PackagesDir and q3InstallsDir are where packages and installations are
// kept: beside the builds, because they are the next two things a build
// becomes.
func q3PackagesDir(env *Env, settings config.Config) (string, error) {
	jobsDir, _, _, err := statePaths(env, settings)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(jobsDir), "q3-packages"), nil
}

func q3InstallsDir(env *Env, settings config.Config) (string, error) {
	jobsDir, _, _, err := statePaths(env, settings)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(jobsDir), "q3-installs"), nil
}

// failClassed prints an error with its class, when it has one, and returns 1.
func failClassed(env *Env, err error) int {
	if class := failure.Of(err); class != "" {
		fmt.Fprintf(env.Stderr, "error [%s]: %v\n", class, err)
		return 1
	}
	return fail(env, err)
}

// grantFlags are the grant flags `preview` and `create` share.
type grantFlags struct {
	ownArchive, ownPath, licensedArchive, licensedPath, denyArchive, denyPath repeatable
	ownLoose, denyLoose                                                       *bool
	licensedLoose, statement                                                  *string
}

func registerGrantFlags(set *flag.FlagSet) *grantFlags {
	f := &grantFlags{
		ownLoose:      set.Bool("own-loose", false, "the loose files of your content folder are your own work"),
		denyLoose:     set.Bool("deny-loose", false, "the loose files are not yours to redistribute"),
		licensedLoose: set.String("licensed-loose", "", "the licence you hold the loose files under"),
		statement:     set.String("statement", "", "recorded verbatim with every grant given here"),
	}
	set.Var(&f.ownArchive, "own-archive", "an archive (by sha256) whose contents are your own work (repeatable)")
	set.Var(&f.ownPath, "own-path", "one file that is your own work (repeatable)")
	set.Var(&f.licensedArchive, "licensed-archive", "an archive you hold a licence for, as sha256=licence (repeatable)")
	set.Var(&f.licensedPath, "licensed-path", "one file you hold a licence for, as path=licence (repeatable)")
	set.Var(&f.denyArchive, "deny-archive", "an archive (by sha256) that is not yours to redistribute (repeatable)")
	set.Var(&f.denyPath, "deny-path", "one file that is not yours to redistribute (repeatable)")
	return f
}

func (f *grantFlags) grants() ([]q3pack.Grant, error) {
	var out []q3pack.Grant
	statement := strings.TrimSpace(*f.statement)
	add := func(grant q3pack.Grant) {
		grant.Statement = statement
		out = append(out, grant)
	}
	for _, digest := range f.ownArchive {
		add(q3pack.Grant{Archive: digest, Basis: q3pack.BasisOwnWork})
	}
	for _, path := range f.ownPath {
		add(q3pack.Grant{Path: path, Basis: q3pack.BasisOwnWork})
	}
	for _, digest := range f.denyArchive {
		add(q3pack.Grant{Archive: digest, Basis: q3pack.BasisNotRedistributable})
	}
	for _, path := range f.denyPath {
		add(q3pack.Grant{Path: path, Basis: q3pack.BasisNotRedistributable})
	}
	for _, raw := range f.licensedArchive {
		digest, licence, found := strings.Cut(raw, "=")
		if !found || strings.TrimSpace(licence) == "" {
			return nil, fmt.Errorf("--licensed-archive %q is not `sha256=licence`", raw)
		}
		add(q3pack.Grant{Archive: digest, Basis: q3pack.BasisLicensed, Licence: licence})
	}
	for _, raw := range f.licensedPath {
		path, licence, found := strings.Cut(raw, "=")
		if !found || strings.TrimSpace(licence) == "" {
			return nil, fmt.Errorf("--licensed-path %q is not `path=licence`", raw)
		}
		add(q3pack.Grant{Path: path, Basis: q3pack.BasisLicensed, Licence: licence})
	}
	loose := 0
	if *f.ownLoose {
		loose++
		add(q3pack.Grant{Loose: true, Basis: q3pack.BasisOwnWork})
	}
	if *f.denyLoose {
		loose++
		add(q3pack.Grant{Loose: true, Basis: q3pack.BasisNotRedistributable})
	}
	if strings.TrimSpace(*f.licensedLoose) != "" {
		loose++
		add(q3pack.Grant{Loose: true, Basis: q3pack.BasisLicensed, Licence: *f.licensedLoose})
	}
	if loose > 1 {
		return nil, errors.New("--own-loose, --licensed-loose and --deny-loose are three answers to one question; give one")
	}
	return out, nil
}

// packageMapPlan is `preview` and `create`, which build the same plan from the
// same flags; `create` then writes it.
func packageMapPlan(env *Env, args []string, previewOnly bool) int {
	name := "package map create"
	if previewOnly {
		name = "package map preview"
	}
	set := newFlagSet(env, name)
	buildID := set.String("build", "", "the finished Quake III build to package")
	mapName := set.String("name", "", "the name the map is packaged and loaded under (default: the build's own)")
	output := set.String("out", "", "write the package into this directory instead of the package store")
	knownAssets := set.String("known-assets", "", "a list of released-asset digests to identify content against")
	acceptMissing := set.Bool("accept-missing", false, "write the package although an engine needs something it will not carry; requires --reason")
	reason := set.String("reason", "", "why the package is written without what it lacks, recorded verbatim")
	asJSON := set.Bool("json", false, "print the plan, or the package record, as JSON")
	var include repeatable
	set.Var(&include, "include", "carry this file too, by the path an engine looks it up under (repeatable)")
	grants := registerGrantFlags(set)
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *buildID == "" {
		fmt.Fprintf(env.Stderr, "error: %s requires --build; `companion build list` shows the builds on this machine\n", name)
		return 2
	}
	if *acceptMissing && strings.TrimSpace(*reason) == "" {
		fmt.Fprint(env.Stderr, "error: --accept-missing requires --reason: shipping a map without something it "+
			"needs is a decision, and a decision nobody wrote down is one nobody can stand behind\n")
		return 2
	}
	if !*acceptMissing && strings.TrimSpace(*reason) != "" {
		fmt.Fprint(env.Stderr, "error: --reason is what --accept-missing records; without it there is nothing to give a reason for\n")
		return 2
	}
	given, err := grants.grants()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	manifest, err := loadBuildManifest(env, settings, *buildID)
	if err != nil {
		return fail(env, err)
	}
	request := q3pack.Request{Manifest: manifest, MapName: *mapName, Grants: given, Include: include}
	if *knownAssets != "" {
		corpus, err := pack.LoadAssetCorpus(*knownAssets)
		if err != nil {
			return fail(env, err)
		}
		request.KnownAssets = corpus
	}
	prepared, err := q3pack.Prepare(request)
	if err != nil {
		return fail(env, err)
	}
	defer prepared.Close()
	plan := prepared.Plan

	if previewOnly {
		if *asJSON {
			return printJSON(env, plan)
		}
		printPackagePlan(env, plan)
		if err := plan.Blocked(false); err != nil {
			fmt.Fprintf(env.Stdout, "\nthis package will not be written as it stands (%d reason(s)):\n", len(plan.Problems))
			for _, problem := range plan.Problems {
				fmt.Fprintf(env.Stdout, "  [%s] %s\n", problem.Code, problem.Message)
			}
			fmt.Fprint(env.Stdout, grantGuidance)
		}
		return 0
	}

	options := q3pack.Options{Companion: env.Version}
	if *acceptMissing {
		options.AcceptReason = *reason
	}
	// The review is printed before anything is refused or accepted: a refusal
	// with no listing is a refusal nobody can act on, and an acceptance nobody
	// was shown the list for is not one.
	if len(plan.Problems) > 0 {
		printPackagePlan(envToStderr(env), plan)
		fmt.Fprintln(env.Stderr)
		for _, problem := range plan.Problems {
			fmt.Fprintf(env.Stderr, "  [%s] %s\n", problem.Code, problem.Message)
		}
	}
	var record *q3pack.Record
	existed := false
	if *output != "" {
		options.Dir = *output
		record, err = q3pack.Create(prepared, options)
	} else {
		var dir string
		if dir, err = q3PackagesDir(env, settings); err != nil {
			return fail(env, err)
		}
		record, existed, err = q3pack.Store{Dir: dir}.Create(prepared, options)
	}
	if err != nil {
		if errors.Is(err, q3pack.ErrBlocked) {
			fmt.Fprintf(env.Stderr, "\nerror [%s]: %v\n", failure.PackageHeld, err)
			fmt.Fprint(env.Stderr, grantGuidance)
			return 1
		}
		return fail(env, err)
	}
	if record.Acceptance != nil {
		fmt.Fprintf(env.Stderr, "\nnote: --accept-missing wrote this package without %d file(s) an engine needs.\n", len(record.Acceptance.NotCarried))
		fmt.Fprintf(env.Stderr, "      reason: %s\n", record.Acceptance.Reason)
	}
	if *asJSON {
		return printJSON(env, record)
	}
	if existed {
		fmt.Fprintf(env.Stdout, "already in the package store, with the same bytes: %s\n", record.ID)
	} else {
		fmt.Fprintf(env.Stdout, "wrote %s\n", record.ArchivePath)
	}
	fmt.Fprintf(env.Stdout, "  package   %s\n", record.ID)
	fmt.Fprintf(env.Stdout, "  sha256    %s\n", strings.TrimPrefix(record.Archive.SHA256, "sha256:"))
	fmt.Fprintf(env.Stdout, "  members   %d, %d bytes unpacked, %d bytes as an archive\n",
		record.Archive.Entries, record.Archive.TotalSize, record.Archive.Size)
	fmt.Fprintf(env.Stdout, "  for       %s/%s\n", record.Plan.GameDir, record.Archive.File)
	if !record.Complete {
		fmt.Fprintf(env.Stdout, "  INCOMPLETE: %d file(s) an engine needs are not in it\n", len(record.Plan.NotCarried))
	}
	fmt.Fprintf(env.Stdout, "\n%s\n", maturity.Quake3Message)
	return 0
}

// grantGuidance is what to do about a held package, said once.
const grantGuidance = `
  A file from your content is packaged only when you say it may be redistributed:
    --own-archive <sha256> | --own-loose | --own-path <path>            it is your own work
    --licensed-archive <sha256>=<licence> | --licensed-path <path>=<licence>   you hold a licence that permits it
    --deny-archive <sha256> | --deny-path <path>                        it is not yours to ship
  If the map is meant to go out without it, pass --accept-missing --reason "…".
`

// envToStderr is an Env whose standard output is its standard error, so one
// printer serves a listing that is the answer and a listing that explains a
// refusal.
func envToStderr(env *Env) *Env {
	copied := *env
	copied.Stdout = env.Stderr
	return &copied
}

// printPackagePlan is the review a person reads.
func printPackagePlan(env *Env, plan *q3pack.Plan) {
	out := env.Stdout
	fmt.Fprintf(out, "package of build %s: the map %q\n", plan.BuildID, plan.Map.Name)
	if plan.Map.AssetID != "" {
		fmt.Fprintf(out, "  saved map %s, revision %d (%s)\n", plan.Map.AssetID, plan.Map.Revision, plan.Map.RevisionID)
	}
	fmt.Fprintf(out, "  archive   %s/%s   (%s)\n", plan.GameDir, plan.ArchiveName, plan.Reproducibility)

	fmt.Fprintf(out, "\nmembers, in the order the archive stores them (%d, %d bytes):\n", len(plan.Members), plan.TotalBytes)
	for _, member := range plan.Members {
		fmt.Fprintf(out, "  %10d  %s  %-14s %s\n", member.Size, shortDigest(member.SHA256), member.Disposition, member.Path)
		detail := member.From
		if member.Licence != "" {
			detail += "; licence: " + member.Licence
		}
		if member.Statement != "" {
			detail += "; " + member.Statement
		}
		fmt.Fprintf(out, "              %s\n", detail)
	}

	fmt.Fprintln(out, "\nsources — what the build's content came from, and what you said about each:")
	if len(plan.Sources) == 0 {
		fmt.Fprintln(out, "  (the build read no content of yours)")
	}
	for _, source := range plan.Sources {
		answer := "NO GRANT — nothing from it is packaged"
		if source.Grant != nil {
			answer = string(source.Grant.Basis)
			if source.Grant.Licence != "" {
				answer += " (" + source.Grant.Licence + ")"
			}
		}
		if source.Kind == "archive" {
			fmt.Fprintf(out, "  %s/%s  sha256 %s\n", source.Game, source.Name, source.SHA256)
		} else {
			fmt.Fprintf(out, "  %s\n", source.Name)
		}
		origin := ""
		if source.Origin != "" {
			origin = ", bound to the saved map (" + source.Origin + ")"
		}
		fmt.Fprintf(out, "      %d file(s) an engine needs%s; %s\n", source.RuntimeFiles, origin, answer)
		for _, hint := range source.Hints {
			fmt.Fprintf(out, "      hint: %s\n", hint)
		}
	}

	fmt.Fprintf(out, "\ndependencies (%d):\n", len(plan.Dependencies))
	for _, dependency := range plan.Dependencies {
		fmt.Fprintf(out, "  %-22s %-7s %s\n", dependency.Disposition, needs(dependency.RequiredAtCompile, dependency.RequiredAtRuntime), dependency.Name)
		fmt.Fprintf(out, "      %s, from %s\n", dependency.Kind, dependency.From)
		if dependency.Note != "" {
			fmt.Fprintf(out, "      note: %s\n", dependency.Note)
		}
		for _, file := range dependency.Files {
			line := fmt.Sprintf("      %-13s %-7s %s — %s", file.Role, needs(file.Compile, file.Runtime), file.Path, file.Disposition)
			if file.Reason != "" {
				line += ": " + file.Reason
			}
			fmt.Fprintln(out, line)
		}
	}
	fmt.Fprintln(out, "  (C = the compiler reads it, R = an engine reads it)")

	if len(plan.Limits) > 0 {
		fmt.Fprintln(out, "\nwhat this review did not look at:")
		for _, limit := range plan.Limits {
			fmt.Fprintf(out, "  - %s\n", limit)
		}
	}
}

// needs is which program reads something, as two letters.
func needs(compile, runtime bool) string {
	switch {
	case compile && runtime:
		return "C+R"
	case compile:
		return "C"
	case runtime:
		return "R"
	}
	return "-"
}

func shortDigest(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

// loadPackage reads a package by id from the store, or from a directory.
func loadPackage(env *Env, settings config.Config, ref string) (*q3pack.Record, error) {
	if info, err := os.Stat(ref); err == nil && info.IsDir() {
		return q3pack.LoadRecord(ref)
	}
	dir, err := q3PackagesDir(env, settings)
	if err != nil {
		return nil, err
	}
	return q3pack.Store{Dir: dir}.Get(ref)
}

func packageMapList(env *Env, args []string) int {
	set := newFlagSet(env, "package map list")
	asJSON := set.Bool("json", false, "print the packages as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	dir, err := q3PackagesDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	records, err := q3pack.Store{Dir: dir}.List()
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		if records == nil {
			records = []*q3pack.Record{}
		}
		return printJSON(env, records)
	}
	if len(records) == 0 {
		fmt.Fprintln(env.Stdout, "no Quake III map packages on this machine; make one with `companion package map create --build <id>`")
		return 0
	}
	for _, record := range records {
		state := "complete"
		if !record.Complete {
			state = fmt.Sprintf("INCOMPLETE (%d not carried)", len(record.Plan.NotCarried))
		}
		fmt.Fprintf(env.Stdout, "%s\n  map %s, build %s, %d members, %s, %s\n",
			record.ID, record.Plan.Map.Name, record.Plan.BuildID, record.Archive.Entries,
			record.CreatedAt.Format(time.RFC3339), state)
	}
	return 0
}

func packageMapShow(env *Env, args []string) int {
	set := newFlagSet(env, "package map show")
	asJSON := set.Bool("json", false, "print the package record as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: package map show takes one package")
		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	record, err := loadPackage(env, settings, rest[0])
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, record)
	}
	fmt.Fprintf(env.Stdout, "%s\n  %s\n  sha256 %s, %d bytes\n\n", record.ID, record.ArchivePath,
		strings.TrimPrefix(record.Archive.SHA256, "sha256:"), record.Archive.Size)
	printPackagePlan(env, record.Plan)
	if record.Acceptance != nil {
		fmt.Fprintf(env.Stdout, "\nwritten without %d file(s) an engine needs, accepted with the reason: %s\n",
			len(record.Acceptance.NotCarried), record.Acceptance.Reason)
	}
	return 0
}

// engineGameRoot is the game folder an engine profile is bound to on this
// machine, unless one was named outright.
func engineGameRoot(env *Env, engineID, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	entry, bindingsPath, err := findEngine(env, engineID)
	if err != nil {
		return "", err
	}
	root := localFor(bindingsPath, entry.Profile.Metadata().ID).Roots[profile.RootGame]
	if root == "" {
		return "", failure.As(failure.GameDataMissing, fmt.Errorf(
			"%s has no game folder set on this machine. Set it with `companion engine bind %s --game-root <dir>`, "+
				"or name one here with --game-root", entry.Profile.Metadata().Name, engineID))
	}
	return root, nil
}

func packageMapInstall(env *Env, args []string) int {
	set := newFlagSet(env, "package map install")
	engineID := set.String("engine", "", "the engine profile whose game folder the map is installed beside")
	into := set.String("into", string(q3install.Managed), "`managed` (a directory the Companion owns; the game folder is not written) or `game-folder`")
	mod := set.String("mod", "", "install into this game directory instead of the package's own")
	baseGame := set.String("base-game", "", "the name of this game folder's base directory, when it is not baseq3 (a free standalone game)")
	gameRoot := set.String("game-root", "", "the game folder, instead of the one the engine is bound to")
	dryRun := set.Bool("dry-run", false, "say where it would go and what would be searched before it; write nothing")
	asJSON := set.Bool("json", false, "print the installation as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: package map install takes one package")
		return 2
	}
	if *engineID == "" && *gameRoot == "" {
		fmt.Fprintln(env.Stderr, "error: package map install requires --engine (or --game-root): the game folder is the engine's own setup")
		return 2
	}
	kind := q3install.Kind(strings.ReplaceAll(*into, "-", "_"))
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	record, err := loadPackage(env, settings, rest[0])
	if err != nil {
		return fail(env, err)
	}
	root, err := engineGameRoot(env, *engineID, *gameRoot)
	if err != nil {
		return failClassed(env, err)
	}
	dir, err := q3InstallsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	request := q3install.Request{Package: record, Kind: kind, GameRoot: root, Game: *mod, BaseGame: *baseGame, Dir: dir}

	var installation *q3install.Installation
	if *dryRun {
		installation, err = q3install.Preview(request)
	} else {
		ctx, stop := signalContext()
		defer stop()
		installation, err = q3install.Install(ctx, request)
	}
	if err != nil {
		return failClassed(env, err)
	}
	if *asJSON {
		return printJSON(env, installation)
	}
	verb := "installed"
	if *dryRun {
		verb = "would install"
	}
	fmt.Fprintf(env.Stdout, "%s %s\n", verb, installation.Archive.Path)
	fmt.Fprintf(env.Stdout, "  installation  %s\n", installation.ID)
	fmt.Fprintf(env.Stdout, "  target        %s\n", describeTarget(installation))
	fmt.Fprintf(env.Stdout, "  engine reads  fs_basepath %s, fs_game %s\n", installation.BasePath, installation.FSGame)
	printLoadOrder(env, installation)
	if !installation.Complete {
		fmt.Fprintf(env.Stdout, "  INCOMPLETE: the package does not carry %d file(s) an engine needs\n", len(installation.NotCarried))
	}
	return 0
}

func describeTarget(installation *q3install.Installation) string {
	if installation.Kind == q3install.Managed {
		return fmt.Sprintf("managed — %s is NOT written; its %s is read through links", installation.GameRoot, installation.Game)
	}
	return fmt.Sprintf("the game folder %s — one file, never over another", installation.GameRoot)
}

func printLoadOrder(env *Env, installation *q3install.Installation) {
	order := installation.LoadOrder
	if len(order.Searched) > 0 {
		fmt.Fprintf(env.Stdout, "  searched before it in %s: %s\n", installation.Game, strings.Join(order.Searched, ", "))
	}
	for _, shadow := range order.Shadowed {
		fmt.Fprintf(env.Stdout, "  LOSES: %s is also in %s, which the engine reads first\n", shadow.Path, shadow.By)
	}
	for _, shadow := range order.Overrides {
		fmt.Fprintf(env.Stdout, "  hides: %s in %s\n", shadow.Path, shadow.By)
	}
}

func packageMapInstalled(env *Env, args []string) int {
	set := newFlagSet(env, "package map installed")
	asJSON := set.Bool("json", false, "print the installations as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	dir, err := q3InstallsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	installations, err := q3install.List(dir)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		if installations == nil {
			installations = []*q3install.Installation{}
		}
		return printJSON(env, installations)
	}
	if len(installations) == 0 {
		fmt.Fprintln(env.Stdout, "no Quake III map package is installed by this program")
		return 0
	}
	for _, installation := range installations {
		fmt.Fprintf(env.Stdout, "%s\n  map %s, %s\n  %s\n", installation.ID, installation.MapName,
			describeTarget(installation), installation.Archive.Path)
	}
	return 0
}

func packageMapUninstall(env *Env, args []string) int {
	set := newFlagSet(env, "package map uninstall")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: package map uninstall takes one installation")
		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	dir, err := q3InstallsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	installation, err := q3install.Load(dir, rest[0])
	if err != nil {
		return fail(env, err)
	}
	if err := q3install.Remove(dir, rest[0]); err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "removed %s\n", installation.Archive.Path)
	return 0
}

func packageMapRun(env *Env, args []string) int {
	set := newFlagSet(env, "package map run")
	engineID := set.String("engine", "", "the engine profile to run")
	action := set.String("action", "play_map", "which action of the profile loads the map")
	wait := set.Duration("wait", q3run.DefaultWait, "how long to wait for the engine to say the map loaded")
	check := set.Bool("check", false, "stop the engine once it has answered; the exit status says whether the map loaded")
	asJSON := set.Bool("json", false, "print what the run observed as JSON")
	options := pairs{}
	set.Var(options, "option", "an option the action declares, as name=value (repeatable)")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: package map run takes one installation")
		return 2
	}
	if *engineID == "" {
		fmt.Fprintln(env.Stderr, "error: package map run requires --engine; `companion engine list` shows the ones set up here")
		return 2
	}
	// Why this engine could not run it here, said before anything starts.
	entry, bindingsPath, err := findEngine(env, *engineID)
	if err != nil {
		return failClassed(env, failure.As(failure.ToolUnavailable, err))
	}
	if err := q3run.Preflight(entry, localFor(bindingsPath, entry.Profile.Metadata().ID), *action, currentPlatform()); err != nil {
		return failClassed(env, err)
	}
	ctx, stop := signalContext()
	defer stop()
	service, settings, err := openJobs(ctx, env, true, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()
	dir, err := q3InstallsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	installation, err := q3install.Load(dir, rest[0])
	if err != nil {
		return fail(env, err)
	}
	if !*asJSON {
		fmt.Fprintf(env.Stderr, "%s\n", maturity.Quake3Message)
	}
	result, err := q3run.Launch(ctx, service, q3run.Request{
		Installation: installation, EngineProfileID: *engineID, ActionID: *action,
		Options: options.orNil(), Wait: *wait, Label: "Run " + installation.MapName,
		Started: func(id string) {
			if !*asJSON {
				fmt.Fprintf(env.Stderr, "engine job %s started; waiting for the engine's own word about the map…\n", id)
			}
		},
	})
	if err != nil {
		return failClassed(env, err)
	}

	// The engine lives in this process's job service, so a run that returned
	// would take the engine with it. `--check` is the mode that means to: it
	// asks whether the map loads and stops the engine once it has its answer.
	running := result.MapLoad != q3run.Refused && !result.State.Terminal()
	if *check && running {
		if cancelled, cancelErr := service.Cancel(result.JobID); cancelErr == nil && cancelled != nil {
			result.EngineStopped = true
			result.State, result.ExitCode = q3run.Settle(service, result.JobID, cancelled)
		}
		running = false
	}
	if *asJSON {
		printJSON(env, result)
	} else {
		printRun(env, result)
	}
	if running {
		fmt.Fprintf(env.Stderr, "\nthe engine is running (pid %d). It stops when you close it, or with Ctrl-C here.\n", result.PID)
		if _, waitErr := service.Wait(ctx, result.JobID); waitErr != nil && ctx.Err() != nil {
			// Ctrl-C: stop the engine and stay until it is gone, so the job is
			// recorded as stopped by somebody and not as lost.
			if cancelled, cancelErr := service.Cancel(result.JobID); cancelErr == nil {
				q3run.Settle(service, result.JobID, cancelled)
			}
			fmt.Fprintln(env.Stderr, "the engine was stopped")
		}
	}
	if result.OK() {
		return 0
	}
	return 1
}

func printRun(env *Env, result *q3run.Result) {
	fmt.Fprintf(env.Stdout, "map load   %s\n", strings.ToUpper(result.MapLoad))
	fmt.Fprintf(env.Stdout, "  %s\n", result.Message)
	if result.Evidence != "" {
		fmt.Fprintf(env.Stdout, "  the engine said: %s\n", result.Evidence)
	}
	if result.Hint != "" {
		fmt.Fprintf(env.Stdout, "  %s\n", result.Hint)
	}
	if result.FailureClass != "" {
		fmt.Fprintf(env.Stdout, "  class      %s\n", result.FailureClass)
	}
	pid := "none — no process was started"
	if result.PID != 0 {
		pid = fmt.Sprintf("%d", result.PID)
	}
	fmt.Fprintf(env.Stdout, "engine pid %s\n", pid)
	fmt.Fprintf(env.Stdout, "job        %s (%s)   `companion job logs %s` is the engine's whole output\n", result.JobID, result.State, result.JobID)
	fmt.Fprintf(env.Stdout, "fs_game    %s\n", result.FSGame)
	fmt.Fprintf(env.Stdout, "base path  %s\n", result.BasePath)
	if result.Executable != "" {
		fmt.Fprintf(env.Stdout, "command    %s %s\n", result.Executable, strings.Join(result.Args, " "))
	}
	if result.EngineStopped {
		fmt.Fprintln(env.Stdout, "the engine was stopped")
	}
}

// buildIsQuake3 reports a build this command packages. Used by `package
// create` to point a Quake III build at the command that reviews its rights.
func buildIsQuake3(manifest *build.Manifest) bool {
	return manifest != nil && manifest.EngineFamily == q3pack.FamilyQuake3
}
