package cli

import (
	"fmt"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// toolchainArgs shows or sets a person's own argument tokens for one program
// of a toolchain or engine — the command-line half of "Your own arguments" in
// Profiles (NEW_265), through the same writer ([binding.SetArguments]) and the
// same preview ([binding.EffectiveCommands]).
//
//	companion toolchain args <id>                                 every program's tokens and the commands
//	companion toolchain args <id> <program> --set=-nopercent      replace that program's tokens
//	companion toolchain args <id> <program> --reset               back to the profile's own command
//
// Each --set is one argv element exactly as typed: nothing splits or quotes it.
//
// Since 2026-10-03 parameters belong to a PIPELINE's stages, not to the build
// tool every pipeline shares (operator: "in build tools you set up the paths
// and metadata of tools, in pipelines you pick a tool and add the
// parameters"). The same command does it, given a pipeline and a stage:
//
//	companion toolchain args <pipeline>                           every stage, its tool and its tokens
//	companion toolchain args <pipeline> <stage> --set=-nopercent  replace that stage's tokens
//	companion toolchain args <pipeline> <stage> --reset           back to the pipeline's own command
//
// A build tool takes no new tokens; ones recorded before still apply and
// `--reset` removes them. An engine is not a stage of anything and keeps its own.
func toolchainArgs(env *Env, args []string) int {
	set := newFlagSet(env, env.group()+" args")
	var tokens stringList
	set.Var(&tokens, "set", "one argument token, exactly as the program receives it (repeatable, in order)")
	reset := set.Bool("reset", false, "remove this program's own arguments")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) < 1 || len(rest) > 2 || (len(rest) == 1 && (len(tokens) > 0 || *reset)) ||
		(len(rest) == 2 && len(tokens) == 0 && !*reset) || (*reset && len(tokens) > 0) {
		fmt.Fprintf(env.Stderr, "error: %s args takes an id; or an id, a program and --set=<token>... or --reset\n", env.group())
		return 2
	}

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	_, profilesDir, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return fail(env, err)
	}
	entry, err := job.NewCatalog(profilesDir).Lookup(rest[0])
	if err != nil {
		return fail(env, err)
	}
	meta := entry.Profile.Metadata()

	if pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile); isPipeline {
		return pipelineStageArgs(env, entry, pipeline, bindingsPath, rest, tokens)
	}
	if _, isTool := entry.Profile.(*profile.ToolProfile); isTool && len(tokens) > 0 {
		fmt.Fprintf(env.Stderr, "error: arguments are set on a pipeline stage, not on the build tool.\n"+
			"       %s args <pipeline> <stage> --set=<token>...   (see `companion build pipelines`)\n"+
			"       Tokens recorded on %s earlier still apply; --reset removes them.\n", env.group(), meta.ID)
		return 1
	}

	var local binding.LocalBinding
	if len(rest) == 2 {
		program := rest[1]
		if !declaresExecutable(entry.Profile, program) {
			fmt.Fprintf(env.Stderr, "error: %s declares no program called %q\n", meta.ID, program)
			return 1
		}
		local, err = binding.SetArguments(bindingsPath, meta.ID, meta.Version, entry.Digest, entry.Trust, program, tokens)
		if err != nil {
			return fail(env, err)
		}
		if len(tokens) == 0 {
			fmt.Fprintf(env.Stdout, "%s: %s runs with the profile's own arguments only\n", meta.ID, program)
		} else {
			fmt.Fprintf(env.Stdout, "%s: %s now gets %d argument(s) of your own\n", meta.ID, program, len(tokens))
		}
	} else {
		loaded, loadErr := binding.LoadFile(bindingsPath)
		if loadErr == nil {
			local, _ = loaded.Find(meta.ID)
		}
	}

	for _, command := range binding.EffectiveCommands(entry.Profile, local, currentPlatform()) {
		own := local.Arguments[command.Executable]
		fmt.Fprintf(env.Stdout, "\n%s (%s)\n", command.Title, command.Executable)
		if len(own) > 0 {
			fmt.Fprintf(env.Stdout, "  your own arguments: %s\n", strings.Join(quoteAll(own), " "))
		}
		if command.Error != "" {
			fmt.Fprintf(env.Stdout, "  cannot be resolved here yet: %s\n", command.Error)
			continue
		}
		fmt.Fprintf(env.Stdout, "  %s\n", strings.Join(quoteAll(command.Argv), " "))
	}
	return 0
}

// pipelineStageArgs is `args` for a pipeline: the tokens of its stages.
func pipelineStageArgs(env *Env, entry job.CatalogEntry, pipeline *profile.PipelineProfile, bindingsPath string, rest []string, tokens []string) int {
	meta := entry.Profile.Metadata()
	var local binding.LocalBinding
	if len(rest) == 2 {
		stage, known := rest[1], false
		for _, step := range pipeline.Steps {
			known = known || step.ID == stage
		}
		if !known {
			fmt.Fprintf(env.Stderr, "error: %s has no stage called %q\n", meta.ID, stage)
			return 1
		}
		var err error
		local, err = binding.SetStepArguments(bindingsPath, meta.ID, meta.Version, entry.Digest, entry.Trust, stage, tokens)
		if err != nil {
			return fail(env, err)
		}
		if len(tokens) == 0 {
			fmt.Fprintf(env.Stdout, "%s: stage %s runs with the pipeline's own arguments only\n", meta.ID, stage)
		} else {
			fmt.Fprintf(env.Stdout, "%s: stage %s now gets %d argument(s) of your own\n", meta.ID, stage, len(tokens))
		}
	} else if loaded, err := binding.LoadFile(bindingsPath); err == nil {
		local, _ = loaded.Find(meta.ID)
	}
	for _, step := range pipeline.Steps {
		fmt.Fprintf(env.Stdout, "\n%s (%s) — %s\n", step.Title, step.ID, step.Capability)
		if own := local.StepArguments[step.ID]; len(own) > 0 {
			fmt.Fprintf(env.Stdout, "  your own arguments: %s\n", strings.Join(quoteAll(own), " "))
		} else {
			fmt.Fprintf(env.Stdout, "  no arguments of your own\n")
		}
	}
	fmt.Fprintf(env.Stdout, "\nThe exact command of each stage is shown by `companion build preview` and in the Build area's last step.\n")
	return 0
}

func declaresExecutable(document profile.Profile, name string) bool {
	switch typed := document.(type) {
	case *profile.ToolProfile:
		for _, executable := range typed.Executables {
			if executable.Name == name {
				return true
			}
		}
	case *profile.EngineProfile:
		for _, executable := range typed.Executables {
			if executable.Name == name {
				return true
			}
		}
	}
	return false
}

// quoteAll renders argv for reading; nothing re-executes it.
func quoteAll(words []string) []string {
	out := make([]string, len(words))
	for i, word := range words {
		if word != "" && !strings.ContainsAny(word, " \t\"'\\$`") {
			out[i] = word
			continue
		}
		out[i] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
	}
	return out
}
