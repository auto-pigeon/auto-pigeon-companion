package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/compat"
)

// toolchainCheck asks a toolchain's programs what they are — the command-line
// half of "Check these programs" on its page (NEW_323D), and the same call:
// [job.Service.CheckCompatibility], run by the one executor as supervised
// jobs, recorded in the binding the page reads.
//
// It exits 0 when every program the toolchain holds to its probe is what the
// document was written for, and 1 when any is not or could not be asked. It
// changes no path and no approval.
func toolchainCheck(env *Env, args []string) int {
	set := newFlagSet(env, "toolchain check")
	asJSON := set.Bool("json", false, "print the answers as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(env.Stderr, "error: toolchain check takes one toolchain id")
		return 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, _, err := openJobs(ctx, env, true, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	entry, err := service.Catalog().Lookup(rest[0])
	if err != nil {
		return fail(env, err)
	}
	if len(compat.ProbedExecutables(entry.Profile)) == 0 {
		fmt.Fprintf(env.Stdout, "%s requires nothing of its programs, so there is nothing to check\n", rest[0])
		return 0
	}
	facts, err := service.CheckCompatibility(rest[0], true)
	if err != nil {
		return fail(env, err)
	}
	names := make([]string, 0, len(facts))
	for name := range facts {
		names = append(names, name)
	}
	sort.Strings(names)

	wrong := len(names) < len(compat.ProbedExecutables(entry.Profile))
	type row struct {
		Executable string `json:"executable"`
		binding.ExecutableFact
		Problem string `json:"problem,omitempty"`
	}
	rows := make([]row, 0, len(names))
	for _, name := range names {
		fact := facts[name]
		line := row{Executable: name, ExecutableFact: fact}
		if finding := compat.Judge(entry.Profile, name, fact.Path, &fact); finding != nil {
			wrong = true
			line.Problem = finding.Error()
		}
		rows = append(rows, line)
	}
	if *asJSON {
		encoder := json.NewEncoder(env.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{"profile": rest[0], "compatible": !wrong, "programs": rows}); err != nil {
			return fail(env, err)
		}
	} else {
		for _, line := range rows {
			identity := line.Identity
			if identity == "" {
				identity = "(did not say)"
			}
			fmt.Fprintf(env.Stdout, "%-8s %-18s %s  %s\n", line.Executable, line.State, identity, line.Path)
			if line.Problem != "" {
				fmt.Fprintf(env.Stdout, "         %s\n", line.Problem)
			}
		}
		if len(names) < len(compat.ProbedExecutables(entry.Profile)) {
			fmt.Fprintln(env.Stdout, "Some of its programs have no path on this machine yet, so they were not asked.")
		}
	}
	if wrong {
		return 1
	}
	return 0
}
