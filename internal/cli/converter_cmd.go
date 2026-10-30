package cli

// `companion converter` — the map converter this Companion runs. `companion extractor` is the former
// spelling, kept as an alias: it runs this same converter, and never the Auto-Pigeon Extractor.
//
// Everything here is about ONE question a user is entitled to a straight answer to: which program is
// about to run on my machine, and who says it is the right one. A release ships auto-pigeon-mapio as a
// separate file beside the Companion, and nothing downloads it.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetref"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
)

const converterUsage = `usage:
  companion converter status    which converter this Companion would run, and what checked it
  companion converter version   run the converter and print its version
  companion converter convert <file.apmap>
                                turn an APMap into the .map a compiler reads, the way a build does
`

func runConverter(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, converterUsage)

		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, converterUsage)

		return 0
	case "status":
		return converterStatus(env, args[1:])
	case "version":
		return converterVersion(env, args[1:])
	case "convert":
		return converterConvert(env, args[1:])
	case "plan", "install":
		fmt.Fprintf(env.Stderr, "error: `companion converter %s` was removed: the converter ships beside the "+
			"Companion in its release, and nothing downloads it.\n", args[0])

		return 2
	}
	fmt.Fprintf(env.Stderr, "error: unknown converter subcommand %q\n\n", args[0])
	fmt.Fprint(env.Stderr, converterUsage)

	return 2
}

// converterResolver is the one resolver every converter path goes through, so
// the override and the bundled lookup cannot be wired differently by two
// callers.
func converterResolver() *converter.Resolver {
	return &converter.Resolver{Override: converter.OverridePath()}
}

func converterStatus(env *Env, args []string) int {
	set := newFlagSet(env, "converter status")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	status := converterResolver().Status()
	if note := converter.RetiredOverrideNote(); note != "" {
		fmt.Fprintln(env.Stderr, "warning: "+note)
	}

	switch {
	case status.Mode == converter.ModeDeveloperOverride:
		fmt.Fprintln(env.Stdout, "converter: UNVERIFIED developer override")
		fmt.Fprintln(env.Stdout, "  "+status.Path)
		fmt.Fprintln(env.Stdout, "  "+converter.UnverifiedNote)
	case status.Available && status.Verified:
		fmt.Fprintln(env.Stdout, "converter: shipped with this Companion, checked against its bundle manifest")
		fmt.Fprintln(env.Stdout, "  "+status.Path)
	case status.Available:
		fmt.Fprintln(env.Stdout, "converter: shipped with this Companion")
		fmt.Fprintln(env.Stdout, "  "+status.Path)
		fmt.Fprintln(env.Stdout, "  "+status.Note)
	default:
		fmt.Fprintf(env.Stdout, "converter: none — %s\n", status.Reason)
	}
	if status.Reason != "" && status.Available {
		fmt.Fprintln(env.Stdout, "  "+status.Reason)
	}

	return 0
}

func converterVersion(env *Env, args []string) int {
	set := newFlagSet(env, "converter version")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	ctx, stop := signalContext()
	defer stop()

	runner, err := converterResolver().Resolve(ctx)
	if err != nil {
		return fail(env, err)
	}
	// The warning goes to stderr and the version to stdout, so that
	// `companion converter version` still scripts into a variable while the
	// person running it still sees that nothing verified what answered.
	if provenance := runner.Provenance(); !provenance.Verified {
		note := provenance.Note
		if note == "" {
			note = converter.UnverifiedNote
		}
		fmt.Fprintln(env.Stderr, "warning: "+note)
	}
	if note := converter.RetiredOverrideNote(); note != "" {
		fmt.Fprintln(env.Stderr, "warning: "+note)
	}
	stdout, err := runner.Run(ctx, "version")
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintln(env.Stdout, strings.TrimSpace(string(stdout)))

	return 0
}

// converterConvert converts one APMap through the SAME path a build takes —
// `assetref.ConvertAPMapInputs` over the resolved runner — so a release's
// acceptance run, or a person checking their install, exercises the real
// subprocess, the digest check and the handshake rather than a second caller
// that could disagree with the build (NEW_247A). The `.map` is written where a
// build writes it: `converted-<name>/` beside the input.
func converterConvert(env *Env, args []string) int {
	set := newFlagSet(env, "converter convert")
	rest, code, ok := parseFlags(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 || !strings.EqualFold(filepath.Ext(rest[0]), ".apmap") {
		fmt.Fprint(env.Stderr, "error: name one .apmap file\n\n"+converterUsage)

		return 2
	}
	input, err := filepath.Abs(rest[0])
	if err != nil {
		return fail(env, err)
	}

	ctx, stop := signalContext()
	defer stop()

	runner, err := converterResolver().Resolve(ctx)
	if err != nil {
		return fail(env, err)
	}
	name := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	converted, _, err := assetref.ConvertAPMapInputs(ctx, runner, map[string]string{name: input}, nil)
	if err != nil {
		return fail(env, err)
	}
	digest, err := converter.FileDigest(converted[name])
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
	fmt.Fprintf(env.Stdout, "  converted by the %s converter %s (protocol %s), %s\n",
		provenance.Mode, provenance.Version, provenance.Protocol, verified)
	if provenance.License != "" {
		fmt.Fprintf(env.Stdout, "  %s, licence %s, go/mapio %s\n", converter.ExecutableBase, provenance.License, provenance.Library)
	}

	return 0
}

// converterRunner is what a long-running server holds: a resolver that has not
// resolved anything yet. A server whose converter cannot be resolved still
// serves everything else, and says so on its status route rather than refusing
// to start.
func converterRunner(env *Env, warn func(format string, args ...any)) *converter.LazyRunner {
	resolver := converterResolver()
	if status := resolver.Status(); !status.Available && warn != nil {
		warn("converter: %s", status.Reason)
	}
	if note := converter.RetiredOverrideNote(); note != "" && warn != nil {
		warn("%s", note)
	}

	return converter.NewLazyRunner(resolver)
}
