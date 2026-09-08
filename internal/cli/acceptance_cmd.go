package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/nativeacceptance"
)

// `companion acceptance` — the native operator acceptance kit.
//
// One command on the machine the artifact is for. It drives this program as a
// child process, lane by lane, and writes a small result bundle somebody else
// can merge into a platform matrix. See internal/nativeacceptance for why the
// bundle is constructed rather than collected, and why nothing here runs
// against the operator's own configuration.

const acceptanceUsage = `usage:
  companion acceptance run [--out <dir>] [selection] [owned game]
                                    run the lanes and write a result bundle
  companion acceptance verify <dir|bundle.json>
                                    validate a bundle and its digest
  companion acceptance lanes        what a run performs, in order
  companion acceptance schema       the bundle format this build reads and writes
  companion acceptance fixture --out <dir>
                                    write the synthetic source map and texture
  companion acceptance noise [--say <text>] [--lines <n>] [--both] [--seconds <s>] [--exit <n>]
                                    the kit's own program, for the approval and log-bound lanes

run flags:
  --out <dir>              where the bundle is written (default: acceptance-bundle)
  --work <dir>             a scratch directory this run owns (default: a temporary one)
  --artifact-dir <dir>     the unpacked release directory (default: beside this program)
  --only <a,b>             run only these lanes
  --skip <a,b>             run everything but these lanes
  --tool-path <dir>        a directory holding an EricW build you already have
  --json                   print the bundle to stdout as well

owned game (both are needed, and neither has a default):
  --engine <path>          an engine executable you already have
  --game-root <dir>        a game installation you already own
  --game-family <name>     quake1 (default), quake2, quake3
  --engine-profile <id>    override the engine profile
  --engine-action <id>     override the action (default play_map)
  --game-deadline <secs>   how long a launch is given to become ready (default 25)

reported by the entry point, never guessed:
  --entry-point <name>     posix, powershell or direct
  --shell <text>           what the entry point is
  --host-arch <text>       what the operating system calls this machine
  --checksums <state>      pass, fail or not_available
  --checksum-detail <text> one line about it

No lane copies, archives, hashes or uploads game data, and there is no flag that
makes one. An owned-game launch records the family, the profile, this platform,
the command shape with paths replaced, the signal and the elapsed time.
`

func runAcceptance(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, acceptanceUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, acceptanceUsage)
		return 0
	case "run":
		return acceptanceRun(env, args[1:])
	case "verify":
		return acceptanceVerify(env, args[1:])
	case "lanes":
		return acceptanceLanes(env, args[1:])
	case "schema":
		return acceptanceSchema(env, args[1:])
	case "fixture":
		return acceptanceFixture(env, args[1:])
	case "noise":
		return acceptanceNoise(env, args[1:])
	}
	fmt.Fprintf(env.Stderr, "error: unknown acceptance subcommand %q\n", args[0])
	fmt.Fprint(env.Stderr, acceptanceUsage)
	return 2
}

func acceptanceRun(env *Env, args []string) int {
	set := newFlagSet(env, "acceptance run")
	out := set.String("out", "acceptance-bundle", "where the bundle is written")
	work := set.String("work", "", "a scratch directory this run owns")
	artifactDir := set.String("artifact-dir", "", "the unpacked release directory")
	only := set.String("only", "", "run only these lanes")
	skip := set.String("skip", "", "run everything but these lanes")
	toolPath := set.String("tool-path", "", "a directory holding an EricW build you already have")
	enginePath := set.String("engine", "", "an engine executable you already have")
	gameRoot := set.String("game-root", "", "a game installation you already own")
	gameFamily := set.String("game-family", "quake1", "quake1, quake2 or quake3")
	engineProfile := set.String("engine-profile", "", "override the engine profile")
	engineAction := set.String("engine-action", "", "override the action")
	gameDeadline := set.Float64("game-deadline", 25, "seconds a launch is given to become ready")
	entryPoint := set.String("entry-point", "direct", "posix, powershell or direct")
	shell := set.String("shell", "", "what the entry point is")
	hostArch := set.String("host-arch", "", "what the operating system calls this machine")
	checksums := set.String("checksums", "", "pass, fail or not_available")
	checksumDetail := set.String("checksum-detail", "", "one line about the checksum result")
	asJSON := set.Bool("json", false, "print the bundle to stdout as well")
	if _, code, ok := parseInterspersed(env, set, args); !ok {
		return code
	}

	onlyLanes, skipLanes := splitLanes(*only), splitLanes(*skip)
	if unknown := nativeacceptance.CheckLanes(append(append([]string{}, onlyLanes...), skipLanes...)); len(unknown) > 0 {
		fmt.Fprintf(env.Stderr, "error: no such lane: %s\nlanes: %s\n",
			strings.Join(unknown, ", "), strings.Join(nativeacceptance.LaneIDs, ", "))
		return 2
	}
	// Both, or neither. A launch against an engine with no game root is a
	// launch that cannot start a game, and reporting the refusal as a product
	// failure would be a statement about a missing flag.
	if (*enginePath == "") != (*gameRoot == "") {
		fmt.Fprintln(env.Stderr,
			"error: --engine and --game-root go together: an owned-game launch needs the engine and the installation it plays")
		return 2
	}
	state := nativeacceptance.State(*checksums)
	if *checksums != "" && state != nativeacceptance.Pass && state != nativeacceptance.Fail &&
		state != nativeacceptance.NotAvailable {
		fmt.Fprintf(env.Stderr, "error: --checksums is pass, fail or not_available, not %q\n", *checksums)
		return 2
	}

	options := nativeacceptance.Options{
		Work:              *work,
		ArtifactDir:       *artifactDir,
		EntryPoint:        *entryPoint,
		Shell:             *shell,
		HostArch:          *hostArch,
		ChecksumsVerified: state,
		ChecksumDetail:    *checksumDetail,
		ToolPath:          *toolPath,
		EngineRoot:        *enginePath,
		GameRoot:          *gameRoot,
		GameFamily:        *gameFamily,
		EngineProfile:     *engineProfile,
		EngineAction:      *engineAction,
		GameDeadline:      time.Duration(*gameDeadline * float64(time.Second)),
		Only:              onlyLanes,
		Skip:              skipLanes,
		Progress:          func(line string) { fmt.Fprintln(env.Stderr, line) },
	}
	bundle, err := nativeacceptance.Run(context.Background(), options)
	if err != nil {
		return fail(env, err)
	}
	problems := bundle.Validate()
	document, err := bundle.Encode()
	if err != nil {
		return fail(env, err)
	}
	if err := writeBundle(*out, document); err != nil {
		return fail(env, err)
	}
	if *asJSON {
		env.Stdout.Write(document)
	}
	printBundleSummary(env, bundle, *out, nativeacceptance.Digest(document), problems)
	if len(problems) > 0 {
		return 1
	}
	if bundle.Verdict != nativeacceptance.Pass {
		return 1
	}
	return 0
}

// writeBundle puts the document and a digest of it in one directory.
//
// A digest is not a signature and nothing here pretends otherwise: anybody who
// can replace the document can replace the file beside it. What it makes
// detectable is a corrupted or truncated transfer between the operator's
// machine and the one that merges it, which is the same honesty
// `build/release.sh` publishes SHA256SUMS under.
func writeBundle(directory string, document []byte) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "bundle.json"), document, 0o644); err != nil {
		return err
	}
	line := fmt.Sprintf("%s  bundle.json\n", nativeacceptance.Digest(document))
	return os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(line), 0o644)
}

func printBundleSummary(env *Env, bundle *nativeacceptance.Bundle, out, digest string, problems []string) {
	fmt.Fprintf(env.Stdout, "\n%s %s on %s\n", bundle.Verdict, bundle.BundleID, bundle.Platform.Target)
	for _, lane := range bundle.Lanes {
		fmt.Fprintf(env.Stdout, "  %-14s %-14s %s\n", lane.State, lane.ID, lane.Title)
		if lane.Reason != "" {
			fmt.Fprintf(env.Stdout, "  %-14s %s\n", "", lane.Reason)
		}
	}
	for _, row := range bundle.Game {
		fmt.Fprintf(env.Stdout, "  %-14s game %-9s %s (%s)\n", row.State, row.Row, row.Signal, row.Family)
	}
	fmt.Fprintf(env.Stdout, "\n  %d pass, %d fail, %d skipped, %d not applicable, %d not available\n",
		bundle.Counts.Pass, bundle.Counts.Fail, bundle.Counts.Skipped,
		bundle.Counts.NotApplicable, bundle.Counts.NotAvailable)
	fmt.Fprintf(env.Stdout, "  written to %s\n  sha256 %s\n", out, digest)
	if len(problems) > 0 {
		fmt.Fprintln(env.Stderr, "\nthis bundle may NOT be published:")
		for _, problem := range problems {
			fmt.Fprintln(env.Stderr, "  "+problem)
		}
	}
}

func acceptanceVerify(env *Env, args []string) int {
	set := newFlagSet(env, "acceptance verify")
	asJSON := set.Bool("json", false, "print the bundle's own summary as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: acceptance verify takes one bundle directory or bundle.json")
		return 2
	}
	document, sums, path, err := readBundle(rest[0])
	if err != nil {
		return fail(env, err)
	}
	var bundle nativeacceptance.Bundle
	if err := json.Unmarshal(document, &bundle); err != nil {
		fmt.Fprintf(env.Stderr, "error: %s is not a bundle document: %v\n", path, err)
		return 1
	}
	problems := bundle.Validate()
	digest := nativeacceptance.Digest(document)
	if sums != "" && !strings.Contains(sums, digest) {
		problems = append(problems,
			"the digest beside this document does not match it, so the transfer cannot be trusted")
	}
	if *asJSON {
		summary := map[string]any{
			"bundle_id": bundle.BundleID, "platform": bundle.Platform.Target,
			"verdict": bundle.Verdict, "digest": digest, "problems": problems,
			"checksums_present": sums != "",
		}
		encoded, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Fprintln(env.Stdout, string(encoded))
	} else {
		fmt.Fprintf(env.Stdout, "%s %s on %s\n  sha256 %s\n",
			bundle.Verdict, bundle.BundleID, bundle.Platform.Target, digest)
		if sums == "" {
			fmt.Fprintln(env.Stdout, "  no SHA256SUMS beside it: the transfer is unverified")
		}
	}
	if len(problems) > 0 {
		fmt.Fprintln(env.Stderr, "this bundle may NOT be merged:")
		for _, problem := range problems {
			fmt.Fprintln(env.Stderr, "  "+problem)
		}
		return 1
	}
	return 0
}

// readBundle accepts a directory or the document itself, because an operator
// who was handed one file should not have to know which of the two a command
// wanted.
func readBundle(target string) (document []byte, sums string, path string, err error) {
	path = target
	if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
		path = filepath.Join(target, "bundle.json")
	}
	document, err = os.ReadFile(path)
	if err != nil {
		return nil, "", path, err
	}
	if data, readErr := os.ReadFile(filepath.Join(filepath.Dir(path), "SHA256SUMS")); readErr == nil {
		sums = string(data)
	}
	return document, sums, path, nil
}

func acceptanceLanes(env *Env, args []string) int {
	set := newFlagSet(env, "acceptance lanes")
	asJSON := set.Bool("json", false, "print the list as JSON")
	if _, code, ok := parseInterspersed(env, set, args); !ok {
		return code
	}
	if *asJSON {
		encoded, _ := json.MarshalIndent(nativeacceptance.LaneIDs, "", "  ")
		fmt.Fprintln(env.Stdout, string(encoded))
		return 0
	}
	for _, id := range nativeacceptance.LaneIDs {
		fmt.Fprintf(env.Stdout, "  %-12s %s\n", id, nativeacceptance.LaneTitle(id))
	}
	fmt.Fprintln(env.Stdout, "\npurge is last because it deletes what every lane above it produced.")
	return 0
}

func acceptanceSchema(env *Env, args []string) int {
	set := newFlagSet(env, "acceptance schema")
	if _, code, ok := parseInterspersed(env, set, args); !ok {
		return code
	}
	fmt.Fprintf(env.Stdout, "%s\nkit version %s\n\nstates: %s\n",
		nativeacceptance.Schema, nativeacceptance.KitVersion,
		strings.Join(nativeacceptance.StateNames(), ", "))
	fmt.Fprintln(env.Stdout, `
A bundle is built from typed facts this program put in it. It has no member a
byte of game data, a log, a credential, a user name or an absolute path could
travel in, and a document in which anything that still looks like an absolute
path survived is refused by `+"`companion acceptance verify`"+`.`)
	return 0
}

func acceptanceFixture(env *Env, args []string) int {
	set := newFlagSet(env, "acceptance fixture")
	out := set.String("out", ".", "where to write the fixture")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 0 {
		fmt.Fprintln(env.Stderr, "error: acceptance fixture takes no positional arguments")
		return 2
	}
	files, err := nativeacceptance.FixtureFiles()
	if err != nil {
		return fail(env, err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return fail(env, err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(*out, name), files[name], 0o644); err != nil {
			return fail(env, err)
		}
		fmt.Fprintf(env.Stdout, "%s  %d bytes\n", name, len(files[name]))
	}
	fmt.Fprintf(env.Stdout, "\n%s.\nSynthetic and project-authored; no id Software asset is used.\n",
		nativeacceptance.FixtureSummary())
	return 0
}

func acceptanceNoise(env *Env, args []string) int {
	set := newFlagSet(env, "acceptance noise")
	say := set.String("say", "", "print one line and stop")
	lines := set.Int("lines", 0, "how many lines of filler to write")
	both := set.Bool("both", false, "write to stderr as well")
	seconds := set.Float64("seconds", 0, "stay alive this long")
	exit := set.Int("exit", 0, "the status to end with")
	if _, code, ok := parseInterspersed(env, set, args); !ok {
		return code
	}
	return nativeacceptance.Noise(env.Stdout, env.Stderr, nativeacceptance.NoiseOptions{
		Say: *say, Lines: *lines, Both: *both, Seconds: *seconds, Exit: *exit,
	})
}

func splitLanes(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	lanes := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			lanes = append(lanes, trimmed)
		}
	}
	return lanes
}
