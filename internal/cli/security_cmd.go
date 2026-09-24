package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/release"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/threat"
)

// `companion security` — the threat model, and what this build is made of.
//
// It is a command rather than a file in the repository because the two things a
// person actually asks are "what did you think about" and "what is in this
// binary", and the second one can only be answered by the binary. The module
// graph comes out of the running program with debug.ReadBuildInfo, not out of
// go.mod: what is linked in is what they are running.

const securityUsage = `usage:
  companion security matrix [--category <name>] [--json]
                                    the threat model: what was considered, what
                                    stops it, and the test that proves it
  companion security residual [--json]
                                    accepted risks, who accepted them, and when
                                    each is looked at again
  companion security audit [--json] what this build is made of: its module
                                    graph, and every external program it can run

Every matrix row names the tests that are its evidence. Renaming or deleting one
fails the build, which is what keeps the model from becoming a description of a
program that used to exist.
`

func runSecurity(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, securityUsage)
		return 2
	}
	switch args[0] {
	case "matrix":
		return securityMatrix(env, args[1:])
	case "residual", "residuals":
		return securityResidual(env, args[1:])
	case "audit":
		return securityAudit(env, args[1:])
	}
	fmt.Fprintf(env.Stderr, "error: unknown security subcommand %q\n", args[0])
	fmt.Fprint(env.Stderr, securityUsage)
	return 2
}

func securityMatrix(env *Env, args []string) int {
	set := newFlagSet(env, "security matrix")
	category := set.String("category", "", "show only one category")
	asJSON := set.Bool("json", false, "print the matrix as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	matrix := threat.Matrix()
	if *category != "" {
		matrix = threat.InCategory(threat.Category(*category))
		if len(matrix) == 0 {
			names := make([]string, 0, len(threat.Categories))
			for _, known := range threat.Categories {
				names = append(names, string(known))
			}
			return fail(env, fmt.Errorf("no category %q; there is %s", *category, strings.Join(names, ", ")))
		}
	}
	if *asJSON {
		return printJSON(env, matrix)
	}

	current := threat.Category("")
	for _, row := range matrix {
		if row.Category != current {
			current = row.Category
			fmt.Fprintf(env.Stdout, "\n== %s ==\n", current)
		}
		fmt.Fprintf(env.Stdout, "\n%s  %s\n", row.ID, row.Title)
		fmt.Fprintf(env.Stdout, "     at stake:   %s\n", row.Asset)
		fmt.Fprintf(env.Stdout, "     vector:     %s\n", wrapAt(row.Vector, 17))
		fmt.Fprintf(env.Stdout, "     mitigation: %s\n", wrapAt(row.Mitigation, 17))
		for i, evidence := range row.Evidence {
			label := "     evidence:  "
			if i > 0 {
				label = "                "
			}
			fmt.Fprintf(env.Stdout, "%s%s.%s\n", label, evidence.Package, evidence.Test)
		}
		if row.Manual != "" {
			fmt.Fprintf(env.Stdout, "     manual:     %s\n", wrapAt(row.Manual, 17))
		}
		if row.Residual != nil {
			fmt.Fprintf(env.Stdout, "     RESIDUAL:   %s\n", wrapAt(row.Residual.What, 17))
			fmt.Fprintf(env.Stdout, "                 accepted by %s, reviewed %s\n",
				row.Residual.Owner, row.Residual.Review)
		}
	}
	fmt.Fprintf(env.Stdout, "\n%d rows.\n", len(matrix))
	return 0
}

func securityResidual(env *Env, args []string) int {
	set := newFlagSet(env, "security residual")
	asJSON := set.Bool("json", false, "print as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	var accepted []threat.Row
	for _, row := range threat.Matrix() {
		if row.Residual != nil {
			accepted = append(accepted, row)
		}
	}
	if *asJSON {
		return printJSON(env, accepted)
	}
	if len(accepted) == 0 {
		fmt.Fprintln(env.Stdout, "no accepted residual risks are recorded.")
		return 0
	}
	overdue := threat.Expired(time.Now())
	for _, row := range accepted {
		fmt.Fprintf(env.Stdout, "\n%s  %s\n", row.ID, row.Title)
		fmt.Fprintf(env.Stdout, "     what: %s\n", wrapAt(row.Residual.What, 11))
		fmt.Fprintf(env.Stdout, "     why:  %s\n", wrapAt(row.Residual.Why, 11))
		fmt.Fprintf(env.Stdout, "     who:  %s\n", row.Residual.Owner)
		due := ""
		for _, expired := range overdue {
			if expired.ID == row.ID {
				due = "  — OVERDUE"
			}
		}
		fmt.Fprintf(env.Stdout, "     next: %s%s\n", row.Residual.Review, due)
	}
	fmt.Fprintf(env.Stdout, "\n%d accepted, %d overdue.\n", len(accepted), len(overdue))
	return 0
}

func securityAudit(env *Env, args []string) int {
	set := newFlagSet(env, "security audit")
	asJSON := set.Bool("json", false, "print as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	modules, haveBuildInfo := release.Dependencies()
	components, err := release.Components(env.Version)
	if err != nil {
		return fail(env, err)
	}

	if *asJSON {
		return printJSON(env, map[string]any{
			"version":             env.Version,
			"build_info":          haveBuildInfo,
			"go_module_deps":      modules,
			"go_module_dep_count": len(modules),
			"components":          components,
		})
	}

	fmt.Fprintf(env.Stdout, "auto-pigeon-companion %s\n\n", env.Version)
	if !haveBuildInfo {
		fmt.Fprintln(env.Stdout, "This binary carries no build information, so its module graph cannot be read.")
	} else if len(modules) == 0 {
		fmt.Fprintln(env.Stdout, "Go module dependencies: none.")
		fmt.Fprintln(env.Stdout, "Nothing outside the standard library is linked into this program. Its own code is")
		fmt.Fprintln(env.Stdout, "MIT; the auto-pigeon-libraries contract files compiled into it stay Apache-2.0, and")
		fmt.Fprintln(env.Stdout, "the extractor shipped beside it is proprietary. Each is listed below with its licence.")
	} else {
		fmt.Fprintf(env.Stdout, "Go module dependencies: %d\n", len(modules))
		for _, module := range modules {
			line := "  " + module.Path + " " + module.Version
			if module.Replace != "" {
				line += " (replaced by " + module.Replace + ")"
			}
			fmt.Fprintln(env.Stdout, line)
		}
	}

	byDistribution := map[release.Distribution][]release.Component{}
	for _, component := range components {
		byDistribution[component.Distribution] = append(byDistribution[component.Distribution], component)
	}
	for _, distribution := range []release.Distribution{
		release.InArtifact, release.ShippedBeside, release.UserSupplied,
	} {
		group := byDistribution[distribution]
		if len(group) == 0 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].Name < group[j].Name })
		fmt.Fprintf(env.Stdout, "\n%s (%d)\n", distributionHeading(distribution), len(group))
		for _, component := range group {
			fmt.Fprintf(env.Stdout, "  %-40s %s\n", component.Name, component.SPDX)
			if component.CorrespondingSource != "" {
				fmt.Fprintf(env.Stdout, "  %-40s source: %s\n", "", component.CorrespondingSource)
			}
		}
	}
	return 0
}

func distributionHeading(distribution release.Distribution) string {
	switch distribution {
	case release.InArtifact:
		return "In this artifact"
	case release.ShippedBeside:
		return "Shipped beside it in the release, under its own licence, run as its own process"
	default:
		return "Programs you already have, which this only configures"
	}
}

// wrapAt re-flows a long sentence under a label, indenting continuation lines.
func wrapAt(text string, indent int) string {
	const width = 96
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	line := 0
	for i, word := range words {
		if i > 0 && line+len(word)+1 > width-indent {
			b.WriteString("\n" + strings.Repeat(" ", indent))
			line = 0
		} else if i > 0 {
			b.WriteString(" ")
			line++
		}
		b.WriteString(word)
		line += len(word)
	}
	return b.String()
}
