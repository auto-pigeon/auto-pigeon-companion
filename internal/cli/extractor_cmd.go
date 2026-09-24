package cli

// `companion extractor` — the separately licensed extractor this Companion
// runs.
//
// Everything here is about ONE question a user is entitled to a straight answer
// to: which program is about to run on my machine, and who says it is the right
// one. The Companion is MIT and the extractor is AGPL-3.0; a release ships the
// extractor as a separate file beside the Companion, and nothing downloads it.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetref"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
)

const extractorUsage = `usage:
  companion extractor status    which extractor this Companion would run, and what checked it
  companion extractor version   run the extractor and print its version
  companion extractor convert <file.apmap>
                                turn an APMap into the .map a compiler reads, the way a build does
`

func runExtractor(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, extractorUsage)

		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, extractorUsage)

		return 0
	case "status":
		return extractorStatus(env, args[1:])
	case "version":
		return extractorVersion(env, args[1:])
	case "convert":
		return extractorConvert(env, args[1:])
	case "plan", "install":
		fmt.Fprintf(env.Stderr, "error: `companion extractor %s` was removed: the extractor ships beside the "+
			"Companion in its release, and nothing downloads it.\n", args[0])

		return 2
	}
	fmt.Fprintf(env.Stderr, "error: unknown extractor subcommand %q\n\n", args[0])
	fmt.Fprint(env.Stderr, extractorUsage)

	return 2
}

// extractorResolver is the one resolver every extractor path goes through, so
// the override and the bundled lookup cannot be wired differently by two
// callers.
func extractorResolver() *aue.Resolver {
	return &aue.Resolver{Override: aue.OverridePath()}
}

func extractorStatus(env *Env, args []string) int {
	set := newFlagSet(env, "extractor status")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	status := extractorResolver().Status()

	switch {
	case status.Mode == aue.ModeDeveloperOverride:
		fmt.Fprintln(env.Stdout, "extractor: UNVERIFIED developer override")
		fmt.Fprintln(env.Stdout, "  "+status.Path)
		fmt.Fprintln(env.Stdout, "  "+aue.UnverifiedNote)
	case status.Available && status.Verified:
		fmt.Fprintln(env.Stdout, "extractor: shipped with this Companion, checked against its bundle manifest")
		fmt.Fprintln(env.Stdout, "  "+status.Path)
	case status.Available:
		fmt.Fprintln(env.Stdout, "extractor: shipped with this Companion")
		fmt.Fprintln(env.Stdout, "  "+status.Path)
		fmt.Fprintln(env.Stdout, "  "+status.Note)
	default:
		fmt.Fprintf(env.Stdout, "extractor: none — %s\n", status.Reason)
	}
	if status.Reason != "" && status.Available {
		fmt.Fprintln(env.Stdout, "  "+status.Reason)
	}

	return 0
}

func extractorVersion(env *Env, args []string) int {
	set := newFlagSet(env, "extractor version")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	ctx, stop := signalContext()
	defer stop()

	runner, err := extractorResolver().Resolve(ctx)
	if err != nil {
		return fail(env, err)
	}
	// The warning goes to stderr and the version to stdout, so that
	// `companion extractor version` still scripts into a variable while the
	// person running it still sees that nothing verified what answered.
	if provenance := runner.Provenance(); !provenance.Verified {
		note := provenance.Note
		if note == "" {
			note = aue.UnverifiedNote
		}
		fmt.Fprintln(env.Stderr, "warning: "+note)
	}
	stdout, err := runner.Run(ctx, "version")
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintln(env.Stdout, strings.TrimSpace(string(stdout)))

	return 0
}

// extractorConvert converts one APMap through the SAME path a build takes —
// `assetref.ConvertAPMapInputs` over the resolved runner — so a release's
// acceptance run, or a person checking their install, exercises the real
// subprocess, the digest check and the handshake rather than a second caller
// that could disagree with the build (NEW_247A). The `.map` is written where a
// build writes it: `converted-<name>/` beside the input.
func extractorConvert(env *Env, args []string) int {
	set := newFlagSet(env, "extractor convert")
	rest, code, ok := parseFlags(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 || !strings.EqualFold(filepath.Ext(rest[0]), ".apmap") {
		fmt.Fprint(env.Stderr, "error: name one .apmap file\n\n"+extractorUsage)

		return 2
	}
	input, err := filepath.Abs(rest[0])
	if err != nil {
		return fail(env, err)
	}

	ctx, stop := signalContext()
	defer stop()

	runner, err := extractorResolver().Resolve(ctx)
	if err != nil {
		return fail(env, err)
	}
	name := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	converted, _, err := assetref.ConvertAPMapInputs(ctx, runner, map[string]string{name: input}, nil)
	if err != nil {
		return fail(env, err)
	}
	digest, err := aue.FileDigest(converted[name])
	if err != nil {
		return fail(env, err)
	}
	provenance := runner.Provenance()
	verified := "UNVERIFIED"
	if provenance.Verified {
		verified = "verified against the bundle manifest"
	}
	fmt.Fprintln(env.Stdout, converted[name])
	fmt.Fprintf(env.Stdout, "  %s\n", digest)
	fmt.Fprintf(env.Stdout, "  converted by the %s extractor %s (protocol %s), %s\n",
		provenance.Mode, provenance.Version, provenance.Protocol, verified)

	return 0
}

// extractorRunner is what a long-running server holds: a resolver that has not
// resolved anything yet. A server whose extractor cannot be resolved still
// serves everything else, and says so on its status route rather than refusing
// to start.
func extractorRunner(env *Env, warn func(format string, args ...any)) *aue.LazyRunner {
	resolver := extractorResolver()
	if status := resolver.Status(); !status.Available && warn != nil {
		warn("extractor: %s", status.Reason)
	}

	return aue.NewLazyRunner(resolver)
}
