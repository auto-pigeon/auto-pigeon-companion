package builtin

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_317B (HITL, 2026-10-07): ericw-tools 2.0.0-alpha11's quicker tree build can lose wall
// material (upstream note 1), so the built-in Q1 compile passes `-forcegoodtree` unless the user
// turns the typed option off. Every built-in Q1 pipeline, the editor's leak test included, compiles
// through this action, so this is the argv a user's build gets.
func TestTheBuiltInQ1CompileForcesTheCarefulTreeBuildByDefault(t *testing.T) {
	entry, err := Find("auto-pigeon.ericw-tools.q1")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	request := profile.Request{
		Platform: profile.Platform{OS: "linux", Arch: "amd64"},
		Roots: map[string]string{
			profile.RootWorkspace:   filepath.Join(base, "workspace"),
			profile.RootToolInstall: filepath.Join(base, "ericw"),
		},
		Inputs: map[string]string{"source_map": filepath.Join(base, "workspace", "input", "dm2.map")},
	}
	defaulted, err := profile.Resolve(entry.Profile, "compile", request)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(defaulted.Command.Args, "-forcegoodtree") {
		t.Errorf("-forcegoodtree missing from the default compile: %v", defaulted.Command.Args)
	}
	// a flag, before the map and the BSP: qbsp reads options first
	if at, source := slices.Index(defaulted.Command.Args, "-forcegoodtree"), slices.Index(defaulted.Command.Args, request.Inputs["source_map"]); source >= 0 && at > source {
		t.Errorf("-forcegoodtree after the map source: %v", defaulted.Command.Args)
	}

	request.Options = map[string]string{"forcegoodtree": "false"}
	off, err := profile.Resolve(entry.Profile, "compile", request)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(off.Command.Args, "-forcegoodtree") {
		t.Errorf("-forcegoodtree passed with the option off: %v", off.Command.Args)
	}

	var option *profile.OptionSpec
	compile, ok := entry.Profile.ActionByID("compile")
	if !ok {
		t.Fatal("the Q1 tool profile has no compile action")
	}
	for index := range compile.Options {
		if compile.Options[index].Name == "forcegoodtree" {
			option = &compile.Options[index]
		}
	}
	if option == nil || option.Type != "bool" || option.Default != "true" || !option.Advanced {
		t.Fatalf("forcegoodtree must be a declared, advanced bool defaulting to true: %+v", option)
	}
}
