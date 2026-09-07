package cli

// `companion extractor` — the separately licensed extractor this Companion
// runs.
//
// Everything here is about ONE question a user is entitled to a straight answer
// to: which program is about to run on my machine, and who says it is the right
// one. The Companion is MIT and the extractor is AGPL-3.0; they are separate
// works, obtained separately, and this repository neither contains the
// extractor nor claims any licence over it.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

const extractorUsage = `usage:
  companion extractor status              what this machine has, and whether anything verified it
  companion extractor plan   [--offline]  which build this Companion should run
  companion extractor install [--offline] download and verify it
  companion extractor version [--offline] run the extractor and print its version
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
	case "plan":
		return extractorPlan(env, args[1:])
	case "install":
		return extractorInstall(env, args[1:])
	case "version":
		return extractorVersion(env, args[1:])
	}
	fmt.Fprintf(env.Stderr, "error: unknown extractor subcommand %q\n\n", args[0])
	fmt.Fprint(env.Stderr, extractorUsage)

	return 2
}

// extractorResolver builds the resolver every extractor path goes through.
//
// One constructor, so the override, the pin location and the AUB authorization
// hook cannot be wired differently by two callers. `install` decides whether a
// missing build may be downloaded, which is the difference between `plan` and
// `install` and between a status query and a run.
func extractorResolver(env *Env, offline, install bool) (*aue.Resolver, error) {
	settings, err := loadSettings(env)
	if err != nil {
		return nil, err
	}
	offline = offline || config.Offline()
	options, err := acquirePaths(env, settings, offline)
	if err != nil {
		return nil, err
	}

	// The authorization hook, installed whenever a backend address is
	// configured — NOT only when a session exists.
	//
	// The difference is the error a signed-out user gets. Installed
	// unconditionally, an artifact this backend hosts is always asked for a
	// grant, and a caller with no session is told that in a sentence naming the
	// backend. Installed only when authenticated, the published URL would be
	// fetched directly, and a route that exists only behind a grant answers 404
	// — a refusal that is correct and says the wrong thing.
	//
	// A catalogue whose artifacts sit on a public mirror is unaffected: the
	// hook returns the published URL and nothing is asked of anybody.
	if client, clientErr := newClient(settings); clientErr == nil {
		options.Authorize = func(ctx context.Context, artifact catalog.Artifact) (string, error) {
			if !client.HostsArtifact(artifact.URL) {
				return artifact.URL, nil
			}
			if !client.Authenticated() {
				return "", fmt.Errorf("%s is served by %s and this Companion is not signed in; "+
					"run `companion auth login` first", aue.ProgramName, client.BaseURL())
			}
			url, _, err := client.AUEDownload(ctx, artifact.SHA256)
			if err != nil {
				return "", fmt.Errorf("%s is served by %s and it did not authorize this download: %w",
					aue.ProgramName, client.BaseURL(), err)
			}

			return url, nil
		}
	}

	acquirer, err := acquire.New(options)
	if err != nil {
		return nil, err
	}
	pinPath, err := extractorPinPath(env)
	if err != nil {
		return nil, err
	}

	return &aue.Resolver{
		Acquirer:         acquirer,
		CompanionVersion: env.Version,
		Override:         aue.OverridePath(),
		PinPath:          pinPath,
		Offline:          offline,
		Install:          install,
	}, nil
}

// extractorPinPath puts the pin beside whatever configuration this invocation
// is using, the same way acquirePaths does for the catalogue state: an explicit
// --config must not leave a test writing into the developer's real one.
func extractorPinPath(env *Env) (string, error) {
	if env.ConfigPath != "" {
		return filepath.Join(filepath.Dir(env.ConfigPath), "extractor-pin.json"), nil
	}

	return config.ExtractorPinPath()
}

func extractorStatus(env *Env, args []string) int {
	set := newFlagSet(env, "extractor status")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	resolver, err := extractorResolver(env, true, false)
	if err != nil {
		return fail(env, err)
	}
	status := resolver.Status()

	switch {
	case status.Mode == aue.ModeDeveloperOverride:
		fmt.Fprintln(env.Stdout, "extractor: UNVERIFIED developer override")
		fmt.Fprintln(env.Stdout, "  "+aue.OverridePath())
		fmt.Fprintln(env.Stdout, "  "+aue.UnverifiedNote)
	case status.Available:
		fmt.Fprintf(env.Stdout, "extractor: %s %s, verified\n", aue.ProgramName, status.Version)
	default:
		fmt.Fprintf(env.Stdout, "extractor: none — %s\n", status.Reason)
	}
	if status.Required != "" {
		fmt.Fprintf(env.Stdout, "  required: %s (invocation protocol %s or later)\n",
			status.Required, status.MinProtocol)
	}
	if status.Reason != "" && status.Available {
		fmt.Fprintln(env.Stdout, "  "+status.Reason)
	}

	return 0
}

func extractorPlan(env *Env, args []string) int {
	set := newFlagSet(env, "extractor plan")
	offline := set.Bool("offline", false, "never touch the network")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	resolver, err := extractorResolver(env, *offline, false)
	if err != nil {
		return fail(env, err)
	}

	ctx, stop := signalContext()
	defer stop()

	plan, err := resolver.Plan(ctx)
	if err != nil {
		return fail(env, err)
	}
	if plan.Override != "" {
		fmt.Fprintln(env.Stdout, "UNVERIFIED developer override: "+plan.Override)
		fmt.Fprintln(env.Stdout, plan.Note)

		return 0
	}
	fmt.Fprintln(env.Stdout, plan.Note)
	if plan.Installed != nil {
		fmt.Fprintf(env.Stdout, "already installed: %s (%s)\n", plan.Installed.Version, plan.Installed.Digest)

		return 0
	}
	fmt.Fprintln(env.Stdout, "not installed; `companion extractor install` downloads and verifies it")

	return 0
}

func extractorInstall(env *Env, args []string) int {
	set := newFlagSet(env, "extractor install")
	offline := set.Bool("offline", false, "never touch the network")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	resolver, err := extractorResolver(env, *offline, true)
	if err != nil {
		return fail(env, err)
	}

	ctx, stop := signalContext()
	defer stop()

	runner, err := resolver.Resolve(ctx)
	if err != nil {
		return fail(env, err)
	}
	for _, line := range runner.Provenance().Describe() {
		fmt.Fprintln(env.Stdout, line)
	}

	return 0
}

func extractorVersion(env *Env, args []string) int {
	set := newFlagSet(env, "extractor version")
	offline := set.Bool("offline", false, "never touch the network")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	resolver, err := extractorResolver(env, *offline, true)
	if err != nil {
		return fail(env, err)
	}

	ctx, stop := signalContext()
	defer stop()

	runner, err := resolver.Resolve(ctx)
	if err != nil {
		return fail(env, err)
	}
	// The warning goes to stderr and the version to stdout, so that
	// `companion extractor version` still scripts into a variable while the
	// person running it still sees that nothing verified what answered.
	if !runner.Provenance().Verified {
		fmt.Fprintln(env.Stderr, "warning: "+aue.UnverifiedNote)
	}
	stdout, err := runner.Run(ctx, "version")
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintln(env.Stdout, strings.TrimSpace(string(stdout)))

	return 0
}

// extractorRunner is what a long-running server holds: a resolver that has not
// resolved anything yet.
//
// Nil, with the error printed, when the configuration cannot even be read. A
// server whose extractor cannot be resolved still serves everything else, and
// says so on its status route rather than refusing to start — the extractor is
// one feature of several, and a user who wanted to look at a job list should
// not be stopped by a catalogue they have not configured.
func extractorRunner(env *Env, warn func(format string, args ...any)) *aue.LazyRunner {
	resolver, err := extractorResolver(env, false, true)
	if err != nil {
		if warn != nil {
			warn("extractor unavailable: %v", err)
		}

		return nil
	}
	if status := resolver.Status(); !status.Available && warn != nil {
		warn("extractor: %s", status.Reason)
	}

	return aue.NewLazyRunner(resolver)
}
