package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginewatch"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2install"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2run"
)

// `companion package map` for a Quake II build (Q2_009): the same subcommands
// as Quake III, driving internal/q2pack, internal/q2install and internal/q2run
// — the three the page's panel drives. A Quake II package is a PAK; it is
// installed into a mod folder of its own beside `baseq2` and run by a client
// that has to say it opened the map from that package.

// isQ2Package and isQ2Installation tell the families' ids apart.
func isQ2Package(id string) bool      { return strings.HasPrefix(id, "q2-") }
func isQ2Installation(id string) bool { return strings.HasPrefix(id, "q2i-") }

func q2PackagesDir(env *Env, settings config.Config) (string, error) {
	jobsDir, _, _, err := statePaths(env, settings)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(jobsDir), "q2-packages"), nil
}

func q2InstallsDir(env *Env, settings config.Config) (string, error) {
	jobsDir, _, _, err := statePaths(env, settings)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(jobsDir), "q2-installs"), nil
}

// q2Grants are the grant flags in Quake II's terms: the loose files of the
// content folder, or one path. An archive is not something a Quake II package
// is built from.
func (f *grantFlags) q2Grants() ([]q2pack.Grant, error) {
	if len(f.ownArchive)+len(f.licensedArchive)+len(f.denyArchive) > 0 {
		return nil, errors.New("a Quake II package carries loose files of your content folder; it opens no archive, so " +
			"--own-archive, --licensed-archive and --deny-archive say nothing here. Use --own-loose or --own-path")
	}
	statement := strings.TrimSpace(*f.statement)
	var out []q2pack.Grant
	add := func(grant q2pack.Grant) {
		grant.Statement = statement
		out = append(out, grant)
	}
	for _, path := range f.ownPath {
		add(q2pack.Grant{Path: path, Basis: q2pack.BasisOwnWork})
	}
	for _, path := range f.denyPath {
		add(q2pack.Grant{Path: path, Basis: q2pack.BasisNotRedistributable})
	}
	for _, raw := range f.licensedPath {
		path, licence, found := strings.Cut(raw, "=")
		if !found || strings.TrimSpace(licence) == "" {
			return nil, fmt.Errorf("--licensed-path %q is not `path=licence`", raw)
		}
		add(q2pack.Grant{Path: path, Basis: q2pack.BasisLicensed, Licence: licence})
	}
	loose := 0
	if *f.ownLoose {
		loose++
		add(q2pack.Grant{Loose: true, Basis: q2pack.BasisOwnWork})
	}
	if *f.denyLoose {
		loose++
		add(q2pack.Grant{Loose: true, Basis: q2pack.BasisNotRedistributable})
	}
	if strings.TrimSpace(*f.licensedLoose) != "" {
		loose++
		add(q2pack.Grant{Loose: true, Basis: q2pack.BasisLicensed, Licence: *f.licensedLoose})
	}
	if loose > 1 {
		return nil, errors.New("--own-loose, --licensed-loose and --deny-loose are three answers to one question; give one")
	}
	return out, nil
}

// packageMapQ2Plan is `preview` and `create` for a Quake II build.
func packageMapQ2Plan(env *Env, settings config.Config, manifest *build.Manifest, previewOnly bool,
	mapName, output, reason string, accept bool, grants *grantFlags, asJSON bool) int {
	given, err := grants.q2Grants()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}
	plan, err := q2pack.Prepare(q2pack.Request{Manifest: manifest, MapName: mapName, Grants: given})
	if err != nil {
		return failClassed(env, err)
	}
	if previewOnly {
		if asJSON {
			return printJSON(env, plan)
		}
		printQ2Plan(env, plan)
		if err := plan.Blocked(false); err != nil {
			fmt.Fprintf(env.Stdout, "\nthis package will not be written as it stands (%d reason(s)):\n", len(plan.Problems))
			for _, problem := range plan.Problems {
				fmt.Fprintf(env.Stdout, "  [%s] %s\n", problem.Code, problem.Message)
			}
			fmt.Fprint(env.Stdout, q2GrantGuidance)
		}
		return 0
	}
	options := q2pack.Options{Companion: env.Version, Manifest: manifest}
	if accept {
		options.AcceptReason = reason
	}
	if len(plan.Problems) > 0 {
		printQ2Plan(envToStderr(env), plan)
		fmt.Fprintln(env.Stderr)
		for _, problem := range plan.Problems {
			fmt.Fprintf(env.Stderr, "  [%s] %s\n", problem.Code, problem.Message)
		}
	}
	var record *q2pack.Record
	existed := false
	if output != "" {
		options.Dir = output
		record, err = q2pack.Create(plan, options)
	} else {
		dir, dirErr := q2PackagesDir(env, settings)
		if dirErr != nil {
			return fail(env, dirErr)
		}
		record, existed, err = q2pack.Store{Dir: dir}.Create(plan, options)
	}
	if err != nil {
		if errors.Is(err, q2pack.ErrBlocked) {
			fmt.Fprintf(env.Stderr, "\nerror [%s]: %v\n", failure.PackageHeld, err)
			fmt.Fprint(env.Stderr, q2GrantGuidance)
			return 1
		}
		return failClassed(env, err)
	}
	if record.Acceptance != nil {
		fmt.Fprintf(env.Stderr, "\nnote: --accept-missing wrote this package without %d file(s) an engine needs.\n", len(record.Acceptance.NotCarried))
		fmt.Fprintf(env.Stderr, "      reason: %s\n", record.Acceptance.Reason)
	}
	if asJSON {
		return printJSON(env, record)
	}
	if existed {
		fmt.Fprintf(env.Stdout, "already in the package store, with the same bytes: %s\n", record.ID)
	} else {
		fmt.Fprintf(env.Stdout, "wrote %s\n", record.ArchivePath)
	}
	fmt.Fprintf(env.Stdout, "  package   %s\n", record.ID)
	fmt.Fprintf(env.Stdout, "  sha256    %s\n", strings.TrimPrefix(record.Archive.SHA256, "sha256:"))
	fmt.Fprintf(env.Stdout, "  members   %d, read back from the written file\n", len(record.Entries))
	fmt.Fprintf(env.Stdout, "  map       maps/%s.bsp  sha256 %s (build %s)\n", record.Plan.Map.Name,
		strings.TrimPrefix(record.Plan.Map.BSPSHA256, "sha256:"), record.Plan.BuildID)
	fmt.Fprintf(env.Stdout, "  installs  into %s unless --mod names another folder\n", record.Plan.GameDir)
	if !record.Complete {
		fmt.Fprintf(env.Stdout, "  INCOMPLETE: %d file(s) an engine needs are not in it\n", len(record.Plan.NotCarried))
	}
	fmt.Fprintf(env.Stdout, "\n%s\n", maturity.Quake2Message)
	return 0
}

const q2GrantGuidance = `
  A file from your content folder is packaged only when you say it may be redistributed:
    --own-loose | --own-path <path>                          it is your own work
    --licensed-loose <licence> | --licensed-path <path>=<licence>   you hold a licence that permits it
    --deny-loose | --deny-path <path>                        it is not yours to ship
  If the map is meant to go out without it, pass --accept-missing --reason "…".
`

func printQ2Plan(env *Env, plan *q2pack.Plan) {
	out := env.Stdout
	fmt.Fprintf(out, "Quake II package of build %s: the map %q\n", plan.BuildID, plan.Map.Name)
	fmt.Fprintf(out, "  source    %s %s\n", plan.Map.SourceName, plan.Map.SourceSHA256)
	if plan.Map.AssetID != "" {
		fmt.Fprintf(out, "  saved map %s, revision %d (%s)\n", plan.Map.AssetID, plan.Map.Revision, plan.Map.RevisionID)
	}
	if plan.Map.APMapSHA256 != "" {
		fmt.Fprintf(out, "  APMap     %s, document revision %d\n", plan.Map.APMapSHA256, plan.Map.DocumentRevision)
	}
	fmt.Fprintf(out, "  BSP       %s, %d bytes\n", plan.Map.BSPSHA256, plan.Map.BSPBytes)
	fmt.Fprintf(out, "  archive   %s, installed into %s\n", plan.ArchiveName, plan.GameDir)
	fmt.Fprintf(out, "\nmembers, in the order the archive stores them (%d, %d bytes):\n", len(plan.Members), plan.TotalBytes)
	for _, member := range plan.Members {
		fmt.Fprintf(out, "  %10d  %s  %-14s %s\n", member.Size, shortDigest(member.SHA256), member.Disposition, member.Path)
		detail := member.From
		if member.Licence != "" {
			detail += "; licence: " + member.Licence
		}
		fmt.Fprintf(out, "              %s\n", detail)
	}
	fmt.Fprintf(out, "\nwhat the compiled map asks an engine for (%d):\n", len(plan.Dependencies))
	for _, dependency := range plan.Dependencies {
		for _, file := range dependency.Files {
			line := fmt.Sprintf("  %-22s %-8s %s", file.Disposition, dependency.Kind, file.Path)
			if file.Reason != "" {
				line += " — " + file.Reason
			}
			fmt.Fprintln(out, line)
		}
	}
	for _, path := range plan.Overrides {
		fmt.Fprintf(out, "  hides: %s in baseq2 — the package's copy is the one loaded\n", path)
	}
	if len(plan.Limits) > 0 {
		fmt.Fprintln(out, "\nnot looked at:")
		for _, limit := range plan.Limits {
			fmt.Fprintf(out, "  %s\n", limit)
		}
	}
}

func q2Store(env *Env, settings config.Config) (q2pack.Store, error) {
	dir, err := q2PackagesDir(env, settings)
	return q2pack.Store{Dir: dir}, err
}

func printQ2PackageList(env *Env, records []*q2pack.Record) {
	for _, record := range records {
		state := "complete"
		if !record.Complete {
			state = fmt.Sprintf("INCOMPLETE (%d not carried)", len(record.Plan.NotCarried))
		}
		fmt.Fprintf(env.Stdout, "%s\n  Quake II map %s, build %s, %d members, %s, %s\n",
			record.ID, record.Plan.Map.Name, record.Plan.BuildID, record.Archive.Entries,
			record.CreatedAt.Format(time.RFC3339), state)
	}
}

func packageMapQ2Show(env *Env, settings config.Config, id string, asJSON bool) int {
	store, err := q2Store(env, settings)
	if err != nil {
		return fail(env, err)
	}
	record, err := store.Get(id)
	if err != nil {
		return failClassed(env, err)
	}
	if asJSON {
		return printJSON(env, record)
	}
	fmt.Fprintf(env.Stdout, "%s\n  %s\n  sha256 %s, %d bytes\n\n", record.ID, record.ArchivePath,
		strings.TrimPrefix(record.Archive.SHA256, "sha256:"), record.Archive.Size)
	printQ2Plan(env, record.Plan)
	if record.Acceptance != nil {
		fmt.Fprintf(env.Stdout, "\nwritten without %d file(s) an engine needs, accepted with the reason: %s\n",
			len(record.Acceptance.NotCarried), record.Acceptance.Reason)
	}
	return 0
}

func packageMapQ2Install(env *Env, settings config.Config, id, engineID, mod, gameRoot string, dryRun, asJSON bool) int {
	store, err := q2Store(env, settings)
	if err != nil {
		return fail(env, err)
	}
	record, err := store.Get(id)
	if err != nil {
		return failClassed(env, err)
	}
	root, err := engineGameRoot(env, engineID, gameRoot)
	if err != nil {
		return failClassed(env, err)
	}
	dir, err := q2InstallsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	request := q2install.Request{Package: record, EngineProfileID: engineID, GameRoot: root, Mod: mod, Dir: dir}
	var installation *q2install.Installation
	if dryRun {
		installation, err = q2install.Preview(request)
	} else {
		ctx, stop := signalContext()
		defer stop()
		installation, err = q2install.Install(ctx, request)
	}
	if err != nil {
		return failClassed(env, err)
	}
	if asJSON {
		return printJSON(env, installation)
	}
	verb := "installed"
	if dryRun {
		verb = "would install"
	}
	fmt.Fprintf(env.Stdout, "%s %s\n", verb, installation.Archive.Path)
	fmt.Fprintf(env.Stdout, "  installation  %s\n", installation.ID)
	fmt.Fprintf(env.Stdout, "  game folder   %s — baseq2 is NOT written\n", installation.GameRoot)
	fmt.Fprintf(env.Stdout, "  engine is told  -datadir %s +set game %s +map %s\n", installation.BasePath, installation.FSGame, installation.MapName)
	fmt.Fprintf(env.Stdout, "  map in it     %s sha256 %s\n", installation.MapMember.Path, strings.TrimPrefix(installation.MapMember.SHA256, "sha256:"))
	if installation.Replaced != "" {
		fmt.Fprintf(env.Stdout, "  replaces      %s\n", installation.Replaced)
	}
	for _, shadow := range installation.LoadOrder.Overrides {
		fmt.Fprintf(env.Stdout, "  hides: %s in %s\n", shadow.Path, shadow.By)
	}
	for _, limit := range installation.LoadOrder.Limits {
		fmt.Fprintf(env.Stdout, "  not looked at: %s\n", limit)
	}
	if !installation.Complete {
		fmt.Fprintf(env.Stdout, "  INCOMPLETE: the package does not carry %d file(s) an engine needs\n", len(installation.NotCarried))
	}
	return 0
}

func packageMapQ2Uninstall(env *Env, settings config.Config, id string) int {
	dir, err := q2InstallsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	installation, err := q2install.Load(dir, id)
	if err != nil {
		return fail(env, err)
	}
	if err := q2install.Remove(dir, id); err != nil {
		return failClassed(env, err)
	}
	fmt.Fprintf(env.Stdout, "removed %s\n", installation.ModDir)
	return 0
}

func packageMapQ2Run(env *Env, id, engineID, action string, wait time.Duration, check, asJSON bool, options map[string]string) int {
	entry, bindingsPath, err := findEngine(env, engineID)
	if err != nil {
		return failClassed(env, failure.As(failure.ToolUnavailable, err))
	}
	local := localFor(bindingsPath, entry.Profile.Metadata().ID)
	if problems := (engine.Checker{Platform: currentPlatform()}).Check(entry.Profile, entry.Trust, entry.Digest, local, action); len(problems) > 0 {
		return failClassed(env, failure.As(failure.ToolUnavailable, problems))
	}
	ctx, stop := signalContext()
	defer stop()
	service, settings, err := openJobs(ctx, env, true, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()
	dir, err := q2InstallsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	installation, err := q2install.Load(dir, id)
	if err != nil {
		return fail(env, err)
	}
	if !asJSON {
		fmt.Fprintf(env.Stderr, "%s\n", maturity.Quake2Message)
	}
	result, err := q2run.Launch(ctx, service, q2run.Request{
		Installation: installation, EngineProfileID: engineID, ActionID: action, Options: options, Wait: wait,
		Label: "Run " + installation.MapName,
		Started: func(id string) {
			if !asJSON {
				fmt.Fprintf(env.Stderr, "engine job %s started; waiting for the engine's own word about the map…\n", id)
			}
		},
	})
	if err != nil {
		return failClassed(env, err)
	}
	running := result.MapLoad != q2run.Refused && !result.State.Terminal()
	if check && running {
		if cancelled, cancelErr := service.Cancel(result.JobID); cancelErr == nil && cancelled != nil {
			result.EngineStopped = true
			result.State, result.ExitCode = enginewatch.Settle(service, result.JobID, cancelled)
		}
		running = false
	}
	if asJSON {
		printJSON(env, result)
	} else {
		fmt.Fprintf(env.Stdout, "map load   %s\n  %s\n", strings.ToUpper(result.MapLoad), result.Message)
		if result.Evidence != "" {
			fmt.Fprintf(env.Stdout, "  the engine said: %s\n", result.Evidence)
		}
		if result.Hint != "" {
			fmt.Fprintf(env.Stdout, "  %s\n", result.Hint)
		}
		if result.FailureClass != "" {
			fmt.Fprintf(env.Stdout, "  class      %s\n", result.FailureClass)
		}
		fmt.Fprintf(env.Stdout, "opened from %s\n", firstNonEmptyString(result.LoadedFrom, "(the engine did not say)"))
		fmt.Fprintf(env.Stdout, "installed   %s  sha256 %s\n", result.Expected, strings.TrimPrefix(result.ArchiveSHA256, "sha256:"))
		fmt.Fprintf(env.Stdout, "engine pid %d, job %s (%s)   `companion job logs %s` is the engine's whole output\n",
			result.PID, result.JobID, result.State, result.JobID)
		if result.Executable != "" {
			fmt.Fprintf(env.Stdout, "command    %s %s\n", result.Executable, strings.Join(result.Args, " "))
		}
		if result.EngineStopped {
			fmt.Fprintln(env.Stdout, "the engine was stopped")
		}
	}
	if running {
		fmt.Fprintf(env.Stderr, "\nthe engine is running (pid %d). It stops when you close it, or with Ctrl-C here.\n", result.PID)
		if _, waitErr := service.Wait(ctx, result.JobID); waitErr != nil && ctx.Err() != nil {
			if cancelled, cancelErr := service.Cancel(result.JobID); cancelErr == nil {
				enginewatch.Settle(service, result.JobID, cancelled)
			}
			fmt.Fprintln(env.Stderr, "the engine was stopped")
		}
	}
	if result.OK() {
		return 0
	}
	return 1
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
