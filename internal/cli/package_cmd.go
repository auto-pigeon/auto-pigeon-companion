package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/pack"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/q3deps"
)

// `companion package` — turn what a build produced into something somebody
// else can install, and read an archive somebody else made.
//
// The command is deliberately in two halves. `preview` decides and prints;
// `create` decides again and writes. They build the same plan from the same
// flags, so what the preview showed is what gets packaged — and `create`
// refuses anything the preview flagged, which is what makes the preview worth
// reading rather than a formality.
//
// The provenance the policy reasons about is assembled here rather than in
// `internal/pack`, because it comes from this machine: the configured game
// roots, the build directory, the local config. The package layer takes it as
// evidence and knows nothing about where it was found.

const packageUsage = `usage:
  companion package targets [--json]                       the packaging targets this build writes
  companion package preview --target <id> [selection] [--json]
                                                           decide and print; write nothing
  companion package create  --target <id> --out <file> [selection] [review] [--replace] [--json]
  companion package inspect <archive> [--format pak|pk3] [--json]
                                                           read a directory; decompress nothing
  companion package verify  <archive> [--format pak|pk3] [--json]
                                                           read every member and check it
  companion package extract <archive> --dest <dir> [--only <path>]... [--replace] [--json]

selection:
  --from <dir>              package a directory's contents at the archive root (repeatable)
  --from-at <prefix>=<dir>  package a directory's contents under <prefix> (repeatable)
  --add <path>=<file>       package one file at one member path (repeatable)
  --build <build-id>        package a build's outputs, with its tools recorded in the manifest
  --source-root <dir>       a directory whose contents are your own work (repeatable)
  --game-root <dir>         an installed game's content directory (repeatable; configured
                            game roots are used automatically)
  --known-assets <file>     a list of released-asset digests to identify content against
  --compression store|deflate   override the target's default

review:
  --acknowledge <path>      accept one file the policy held for review (repeatable)
  --acknowledge-all         accept every held file; requires --reason
  --authorize <path>        package a file identified as a released asset (repeatable);
                            requires --reason
  --reason <text>           what you are asserting, recorded verbatim in the manifest
  --label <text>            a short name for this package

dependency review (Quake III):
  --map <file>              read this map source and check what it needs against the
                            package, your own content and the base game (repeatable)
  --accept-missing          package anyway when that review holds something;
                            requires --reason, and prints the review first
`

func runPackage(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, packageUsage)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "targets":
		return packageTargets(env, rest)
	case "preview":
		return packageBuild(env, rest, true)
	case "create":
		return packageBuild(env, rest, false)
	case "inspect":
		return packageInspect(env, rest, false)
	case "verify":
		return packageInspect(env, rest, true)
	case "extract":
		return packageExtract(env, rest)
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, packageUsage)
		return 0
	}
	fmt.Fprintf(env.Stderr, "error: unknown package subcommand %q\n\n", args[0])
	fmt.Fprint(env.Stderr, packageUsage)
	return 2
}

func packageTargets(env *Env, args []string) int {
	set := newFlagSet(env, "package targets")
	asJSON := set.Bool("json", false, "print the targets as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	targets := pack.Targets()
	if *asJSON {
		return printJSON(env, targets)
	}
	for _, target := range targets {
		fmt.Fprintf(env.Stdout, "%-12s %s\n", target.ID, target.Title)
		fmt.Fprintf(env.Stdout, "  format %s, %s by default, reproducibility %s\n",
			target.Format, target.Compression, target.Reproducibility())
		fmt.Fprintf(env.Stdout, "  at most %d members (%s), member paths up to %d bytes\n",
			target.MaxEntries, target.MaxEntriesNote, target.MaxNameLength)
		fmt.Fprintf(env.Stdout, "  Auto-Pigeon metadata inside the archive: %s\n\n",
			yesNo(target.AllowsMetadata))
	}
	return 0
}

func yesNo(value bool) string {
	if value {
		return "permitted"
	}
	return "not permitted; the manifest is written beside the archive"
}

// packageBuild is `preview` and `create`, which differ only in whether they
// open a file at the end.
func packageBuild(env *Env, args []string, previewOnly bool) int {
	name := "package create"
	if previewOnly {
		name = "package preview"
	}
	set := newFlagSet(env, name)
	targetID := set.String("target", "", "packaging target id (see `companion package targets`)")
	output := set.String("out", "", "where to write the archive")
	compression := set.String("compression", "", "override the target's compression: store or deflate")
	label := set.String("label", "", "a short name for this package")
	buildID := set.String("build", "", "package this build's outputs")
	knownAssets := set.String("known-assets", "", "a list of released-asset digests")
	reason := set.String("reason", "", "what you are asserting when you acknowledge or authorize")
	replace := set.Bool("replace", false, "write over an existing archive")
	acknowledgeAll := set.Bool("acknowledge-all", false, "accept every file held for review; requires --reason")
	embed := set.Bool("embed-manifest", false, "write the manifest inside the archive, where the target permits it")
	acceptMissing := set.Bool("accept-missing", false, "package anyway when the dependency review holds something; requires --reason")
	asJSON := set.Bool("json", false, "print the plan, or the result, as JSON")

	var from, fromAt, add, sourceRoots, gameRoots, acknowledge, authorize, mapSources repeatable
	set.Var(&from, "from", "package a directory's contents at the archive root (repeatable)")
	set.Var(&fromAt, "from-at", "package a directory under a prefix, as prefix=dir (repeatable)")
	set.Var(&add, "add", "package one file, as memberpath=file (repeatable)")
	set.Var(&sourceRoots, "source-root", "a directory whose contents are your own work (repeatable)")
	set.Var(&gameRoots, "game-root", "an installed game's content directory (repeatable)")
	set.Var(&acknowledge, "acknowledge", "accept one file held for review (repeatable)")
	set.Var(&authorize, "authorize", "package a file identified as a released asset (repeatable)")
	set.Var(&mapSources, "map", "review a Quake III map source's dependencies against this package (repeatable)")

	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *targetID == "" {
		fmt.Fprintf(env.Stderr, "error: %s requires --target; run `companion package targets` to see them\n", name)
		return 2
	}
	target, known := pack.TargetByID(*targetID)
	if !known {
		fmt.Fprintf(env.Stderr, "error: %q is not a packaging target; this build writes: %s\n",
			*targetID, strings.Join(pack.TargetIDs(), ", "))
		return 2
	}
	target, err := target.With(pack.Compression(*compression))
	if err != nil {
		return fail(env, err)
	}
	if !previewOnly && *output == "" {
		fmt.Fprint(env.Stderr, "error: package create requires --out\n")
		return 2
	}
	if len(authorize) > 0 && strings.TrimSpace(*reason) == "" {
		fmt.Fprint(env.Stderr, "error: --authorize requires --reason: an assertion nobody wrote down is not one anybody can stand behind\n")
		return 2
	}
	if *acknowledgeAll && strings.TrimSpace(*reason) == "" {
		fmt.Fprint(env.Stderr, "error: --acknowledge-all requires --reason\n")
		return 2
	}
	if *acceptMissing && strings.TrimSpace(*reason) == "" {
		fmt.Fprint(env.Stderr, "error: --accept-missing requires --reason: shipping a package without "+
			"something it depends on is a decision, and a decision nobody wrote down is one nobody can stand behind\n")
		return 2
	}
	if *acceptMissing && len(mapSources) == 0 {
		fmt.Fprint(env.Stderr, "error: --accept-missing has nothing to accept; it answers the review --map asks for\n")
		return 2
	}

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}

	dirs, files, err := packageSelection(from, fromAt, add)
	if err != nil {
		return fail(env, err)
	}

	// The build, when one was named: its outputs are the selection, its
	// digests are the evidence, and its tools go into the manifest.
	var (
		buildRef *pack.BuildRef
		toolRefs []pack.ToolRef
		outputs  = map[string]string{}
	)
	if *buildID != "" {
		manifest, err := loadBuildManifest(env, settings, *buildID)
		if err != nil {
			return fail(env, err)
		}
		buildFiles, refs, digests := packageFromBuild(manifest)
		files = append(files, buildFiles...)
		buildRef, toolRefs, outputs = refs.build, refs.tools, digests
	}
	if len(dirs) == 0 && len(files) == 0 {
		fmt.Fprintf(env.Stderr, "error: %s selected nothing; use --from, --from-at, --add or --build\n", name)
		return 2
	}

	policy := pack.Policy{
		GameRoots:        packageGameRoots(settings, gameRoots),
		AuthoredRoots:    append(absAll(sourceRoots), absAll(from)...),
		BuildOutputs:     outputs,
		Authorizations:   map[string]string{},
		Acknowledgements: map[string]bool{},
	}
	for _, path := range authorize {
		policy.Authorizations[path] = *reason
	}
	for _, path := range acknowledge {
		policy.Acknowledgements[path] = true
	}
	if *knownAssets != "" {
		corpus, err := pack.LoadAssetCorpus(*knownAssets)
		if err != nil {
			return fail(env, err)
		}
		policy.KnownAssets = corpus
	}

	candidates, err := pack.Collect(dirs, files, target)
	if err != nil {
		return fail(env, err)
	}
	plan, err := pack.NewPlan(candidates, policy, target)
	if err != nil {
		return fail(env, err)
	}
	// --acknowledge-all is applied after the first pass, so that the listing
	// records what it actually covered rather than an unbounded promise.
	var covered []string
	if *acknowledgeAll {
		for _, decision := range plan.NeedsReview() {
			policy.Acknowledgements[decision.Path] = true
			covered = append(covered, decision.Path)
		}
		sort.Strings(covered)
		if plan, err = pack.NewPlan(candidates, policy, target); err != nil {
			return fail(env, err)
		}
	}

	// The dependency review, when a map was named. It runs against the plan
	// rather than against the finished archive, because the point is to be
	// read BEFORE anything is written — and because a preview that did not run
	// it would be a preview of a different package from the one `create`
	// writes.
	var dependencies *q3deps.Report
	if len(mapSources) > 0 {
		dependencies, err = q3deps.Discover(mapSources, q3deps.Scan{
			Members:      plannedMembers(plan),
			ContentRoots: policy.AuthoredRoots,
			GameRoots:    policy.GameRoots,
		})
		if err != nil {
			return fail(env, err)
		}
	}

	if previewOnly {
		if *asJSON {
			return printJSON(env, previewDocument{Plan: plan, Dependencies: dependencies})
		}
		fmt.Fprint(env.Stdout, plan.Preview())
		if dependencies != nil {
			fmt.Fprint(env.Stdout, "\n"+dependencies.Describe())
		}
		if err := plan.Blocked(); err != nil {
			fmt.Fprintf(env.Stdout, "\nthis plan will not be written as it stands:\n  %v\n", err)
		}
		if dependencies != nil {
			if err := dependencies.Blocked(); err != nil {
				fmt.Fprintf(env.Stdout, "\nthe dependency review holds this package:\n  %v\n"+
					"  package it anyway with --accept-missing --reason \"…\", once you have read why.\n", err)
			}
		}
		return 0
	}

	// `create` refuses on the review unless somebody said, in writing, that
	// they had read it. `AUP/AUCOM 216` asks for "an explicit review, not a
	// silently incomplete PK3", and a review that wrote the archive anyway
	// would be the second thing wearing the name of the first.
	if dependencies != nil {
		if err := dependencies.Blocked(); err != nil {
			if !*acceptMissing {
				fmt.Fprint(env.Stderr, dependencies.Describe())
				fmt.Fprintf(env.Stderr, "\nerror: %v\n"+
					"  read the review above. Add what is missing, or, if it belongs somewhere else, "+
					"pass --accept-missing --reason \"…\".\n", err)
				return 1
			}
			// Printed even when it is being accepted. A review somebody
			// waved through without seeing is not one, and the archive that
			// results is the same archive either way.
			fmt.Fprint(env.Stderr, dependencies.Describe())
			fmt.Fprintf(env.Stderr, "\nnote: --accept-missing packaged despite the review: %v\n", err)
			fmt.Fprintf(env.Stderr, "      reason: %s\n", strings.TrimSpace(*reason))
		}
	}

	if len(covered) > 0 {
		fmt.Fprintf(env.Stderr, "note: --acknowledge-all accepted %d file(s):\n", len(covered))
		for _, path := range covered {
			fmt.Fprintf(env.Stderr, "  %s\n", path)
		}
	}
	result, err := pack.Create(plan, pack.Options{
		Output:        *output,
		Replace:       *replace,
		Label:         *label,
		Companion:     env.Version,
		Build:         buildRef,
		Tools:         toolRefs,
		EmbedManifest: *embed,
		Review: pack.ReviewRecord{
			Acknowledged: append(append([]string(nil), acknowledge...), covered...),
			Authorized:   authorize,
			Reason:       *reason,
		},
	})
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, result.Manifest)
	}
	fmt.Fprintf(env.Stdout, "wrote %s\n", result.Archive)
	fmt.Fprintf(env.Stdout, "      %s\n", result.ManifestPath)
	fmt.Fprint(env.Stdout, result.Manifest.Describe())
	return 0
}

// previewDocument is what `package preview --json` prints: the plan, with the
// dependency review beside it when one ran.
//
// The plan is embedded rather than nested, so the document a caller was already
// parsing keeps its shape and gains a member. A preview that changed shape when
// a flag was passed would break every reader that had not heard about the flag.
type previewDocument struct {
	*pack.Plan
	Dependencies *q3deps.Report `json:"dependencies,omitempty"`
}

// plannedMembers is the archive as the review has to see it: member path to the
// file on this machine it will be made from.
//
// [pack.Plan.Included] is every decision that ends with the file in the archive
// — including the ones a person acknowledged or authorized — which is exactly
// the set the review must treat as packaged. A file still held for review is
// not in it, so a dependency that depends on one is reported as not packaged,
// which is the true answer for a package that will not be written either.
func plannedMembers(plan *pack.Plan) map[string]string {
	members := map[string]string{}
	for _, decision := range plan.Included() {
		members[decision.Path] = decision.Source
	}
	return members
}

// packageSelection turns the selection flags into what pack.Collect takes.
func packageSelection(from, fromAt, add repeatable) ([]pack.DirSource, []pack.FileSource, error) {
	var dirs []pack.DirSource
	for _, dir := range from {
		dirs = append(dirs, pack.DirSource{Dir: dir})
	}
	for _, raw := range fromAt {
		prefix, dir, found := strings.Cut(raw, "=")
		if !found || prefix == "" || dir == "" {
			return nil, nil, fmt.Errorf("--from-at %q is not `prefix=dir`", raw)
		}
		dirs = append(dirs, pack.DirSource{Dir: dir, Prefix: prefix})
	}
	var files []pack.FileSource
	for _, raw := range add {
		member, file, found := strings.Cut(raw, "=")
		if !found || member == "" || file == "" {
			return nil, nil, fmt.Errorf("--add %q is not `memberpath=file`", raw)
		}
		files = append(files, pack.FileSource{Path: member, File: file})
	}
	return dirs, files, nil
}

type buildRefs struct {
	build *pack.BuildRef
	tools []pack.ToolRef
}

// packageFromBuild reads a build manifest into a selection, the evidence the
// policy needs, and the tool record the package manifest carries.
//
// The digest map is the interesting half: it is what lets a file be recognised
// as this build's output wherever it now sits, including inside a game
// directory. The paths are only how it was found.
func packageFromBuild(manifest *build.Manifest) ([]pack.FileSource, buildRefs, map[string]string) {
	files := make([]pack.FileSource, 0, len(manifest.Outputs))
	digests := map[string]string{}
	for _, output := range manifest.Outputs {
		if output.Missing || output.Path == "" {
			continue
		}
		files = append(files, pack.FileSource{
			Path:      packageMemberPath(output.Name, output.Path),
			File:      output.Path,
			FromBuild: fmt.Sprintf("%s (%s)", output.Name, manifest.BuildID),
		})
		if output.SHA256 != "" {
			digests[output.SHA256] = fmt.Sprintf("output %q of build %s", output.Name, manifest.BuildID)
		}
	}
	refs := buildRefs{
		build: &pack.BuildRef{
			BuildID: manifest.BuildID,
			Pipeline: pack.DocumentRef{
				ID: manifest.Pipeline.ID, Version: manifest.Pipeline.Version,
				Name: manifest.Pipeline.Name, Digest: manifest.Pipeline.Digest,
			},
			ReproducibleKey: manifest.ReproducibleKey,
			Platform:        manifest.Platform,
		},
	}
	for _, tool := range manifest.Tools {
		record := pack.ToolRef{
			Profile: pack.DocumentRef{
				ID: tool.Profile.ID, Version: tool.Profile.Version,
				Name: tool.Profile.Name, Digest: tool.Profile.Digest,
			},
			ToolVersion: tool.ToolVersion,
		}
		for _, exe := range tool.Executables {
			record.Executables = append(record.Executables, pack.ExecutableRef{Name: exe.Name, SHA256: exe.SHA256})
		}
		refs.tools = append(refs.tools, record)
	}
	return files, refs, digests
}

// packageMemberPath is where a build output lands inside a Quake archive.
//
// A `.bsp` goes under `maps/`, because that is where every engine in this
// family looks for one and a BSP at the archive root is a package that does not
// work. Everything else keeps its file name, at the root, and a user who wants
// it somewhere else says so with --add.
//
// # Why the FILE's name and not the output's
//
// A pipeline output is *named* — `bsp`, `lit`, `compile_log` — and that name
// carries no extension, because it is an identifier a pipeline wires with and
// not a filename. Deriving the member path from it put `level.bsp` into the
// archive as a member called `bsp`, at the root, where no engine would ever
// look for it: the `maps/` rule below could not fire, because `bsp` has no
// extension to match.
//
// It surfaced in `AUP/AUCOM 215`'s Quake II acceptance, and it was never a
// Quake II fault — every `--build` package this program has written has the
// same shape. So the member path comes from the file the build actually wrote,
// and the output's name is the fallback for a record that has no path.
func packageMemberPath(name, file string) string {
	base := filepath.Base(filepath.FromSlash(name))
	if file != "" {
		base = filepath.Base(file)
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".bsp", ".lit", ".ent":
		return "maps/" + base
	}
	return base
}

// packageGameRoots is every directory this machine knows holds an installed
// game, configured or named on the command line.
//
// Taken from the configuration automatically, which is the point: the accident
// this policy exists to catch is a `--from .` in a directory that is also a
// game directory, and a user who had to remember to pass `--game-root` would be
// a user who had already noticed.
func packageGameRoots(settings config.Config, extra []string) []string {
	seen := map[string]bool{}
	var roots []string
	for _, root := range settings.GameRoots {
		if root != "" && !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	for _, root := range absAll(extra) {
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots
}

func absAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if absolute, err := filepath.Abs(path); err == nil {
			out = append(out, absolute)
		} else {
			out = append(out, path)
		}
	}
	return out
}

func loadBuildManifest(env *Env, settings config.Config, id string) (*build.Manifest, error) {
	dir, err := buildsDir(env, settings)
	if err != nil {
		return nil, err
	}
	return build.Find(dir, id)
}

func packageInspect(env *Env, args []string, verify bool) int {
	name := "package inspect"
	if verify {
		name = "package verify"
	}
	set := newFlagSet(env, name)
	format := set.String("format", "", "pak or pk3; guessed from the name when omitted")
	asJSON := set.Bool("json", false, "print the result as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintf(env.Stderr, "error: %s takes exactly one archive\n", name)
		return 2
	}
	if verify {
		verification, err := pack.Verify(rest[0], pack.Format(*format), pack.Budget{})
		if err != nil {
			return fail(env, err)
		}
		if *asJSON {
			printJSON(env, verification)
		} else {
			printInspection(env, &verification.Inspection)
			fmt.Fprintf(env.Stdout, "\nverified %d of %d members\n", verification.Verified, len(verification.Entries))
			for _, problem := range verification.Problems {
				fmt.Fprintf(env.Stdout, "  problem: %s\n", problem)
			}
			if verification.ManifestPath != "" {
				fmt.Fprintf(env.Stdout, "  manifest: %s (%s)\n", verification.ManifestPath,
					map[bool]string{true: "agrees", false: "disagrees"}[verification.ManifestAgrees])
				for _, issue := range verification.ManifestIssues {
					fmt.Fprintf(env.Stdout, "    %s\n", issue)
				}
			}
		}
		if !verification.OK() {
			return 1
		}
		return 0
	}

	inspection, err := pack.Inspect(rest[0], pack.Format(*format), pack.Budget{})
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, inspection)
	}
	printInspection(env, inspection)
	return 0
}

func printInspection(env *Env, inspection *pack.Inspection) {
	fmt.Fprintln(env.Stdout, inspection.Summary())
	fmt.Fprintf(env.Stdout, "%s\n\n", inspection.SHA256)
	for _, entry := range inspection.Entries {
		fmt.Fprintf(env.Stdout, "  %10d  %-8s %s\n", entry.Size, entry.Compression, entry.Path)
	}
	for _, note := range inspection.Notes {
		fmt.Fprintf(env.Stdout, "\nnote: %s\n", note)
	}
	for _, collision := range inspection.Collisions {
		fmt.Fprintf(env.Stdout, "\ncollision: %s\n", collision.Error())
	}
}

func packageExtract(env *Env, args []string) int {
	set := newFlagSet(env, "package extract")
	dest := set.String("dest", "", "the directory to extract into")
	format := set.String("format", "", "pak or pk3; guessed from the name when omitted")
	replace := set.Bool("replace", false, "write over files that are already in the destination")
	asJSON := set.Bool("json", false, "print the result as JSON")
	var only repeatable
	set.Var(&only, "only", "extract just this member (repeatable)")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, "error: package extract takes exactly one archive\n")
		return 2
	}
	if *dest == "" {
		fmt.Fprint(env.Stderr, "error: package extract requires --dest\n")
		return 2
	}
	result, err := pack.Extract(rest[0], pack.Format(*format), pack.ExtractOptions{
		Dest: *dest, Replace: *replace, Only: only,
	})
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, result)
	}
	fmt.Fprintf(env.Stdout, "extracted %d file(s), %d bytes, into %s\n", len(result.Files), result.Bytes, result.Dest)
	for _, entry := range result.Files {
		fmt.Fprintf(env.Stdout, "  %s\n", entry.Path)
	}
	return 0
}

// repeatable is a flag that may be given more than once, keeping order.
//
// `pairs` next door is a map and refuses a repeated key, which is right for
// `name=value` options and wrong here: `--from a --from b` is two directories,
// not a mistake.
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, " ") }

func (r *repeatable) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("is empty")
	}
	*r = append(*r, value)
	return nil
}
