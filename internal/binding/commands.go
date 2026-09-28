package binding

import (
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// EffectiveCommand is one action's command as it would be resolved on this
// machine now (NEW_265): the profile's defaults, this machine's paths and
// this person's own argument tokens, and where in the argv those went.
type EffectiveCommand struct {
	ActionID   string
	Title      string
	Executable string
	// Argv is the program and then each argument, one element each.
	Argv []string
	// CustomArgs are the person's own tokens; CustomAt is the index in Argv
	// where they begin (argv[0] is the program).
	CustomArgs []string
	CustomAt   int
	// Error is why the action cannot be resolved here yet.
	Error string
}

// EffectiveCommands resolves every action of a document through the same
// [profile.Resolve] a job uses. What a job will be given is only known once it
// has a job folder and input files, so those parts are placeholders in angle
// brackets — `<job folder>`, `<map>` — and are said to be.
func EffectiveCommands(document profile.Profile, local LocalBinding, platform profile.Platform) []EffectiveCommand {
	var out []EffectiveCommand
	for _, action := range document.ActionList() {
		item := EffectiveCommand{ActionID: action.ID, Title: action.Title, Executable: action.Executable}
		roots := map[string]string{profile.RootWorkspace: "<job folder>", profile.RootBuild: "<build folder>"}
		for _, ref := range action.Roots {
			if ref.Role != profile.RootWorkspace && !ref.Optional {
				roots[ref.Role] = "<" + strings.ReplaceAll(ref.Role, "_", " ") + ">"
			}
		}
		for role, path := range local.Roots {
			if role != profile.RootWorkspace {
				roots[role] = path
			}
		}
		if roots[profile.RootToolInstall] == "" {
			roots[profile.RootToolInstall] = "<program folder>"
		}
		inputs := map[string]string{}
		for _, input := range action.Inputs {
			if !input.Required {
				continue
			}
			extension := ""
			if len(input.Extensions) > 0 {
				extension = input.Extensions[0]
			}
			inputs[input.Name] = filepath.Join("<job folder>", "input", input.StageGroup(), "<"+input.Name+">"+extension)
		}
		executables := map[string]string{}
		for name, path := range local.Executables {
			executables[name] = path
		}
		invocation, err := profile.Resolve(document, action.ID, profile.Request{
			Platform:    platform,
			Roots:       roots,
			Executables: executables,
			Inputs:      inputs,
			Runtime: map[string]string{
				profile.RuntimeMapName: "<map>", profile.RuntimeModName: "<mod>",
				profile.RuntimePackageName: "<package>", profile.RuntimeServerHost: "<server>",
				profile.RuntimeServerPort: "<port>",
			},
			ExtraArgs: local.Arguments[action.Executable],
		})
		if err != nil {
			item.Error = err.Error()
			out = append(out, item)
			continue
		}
		item.Argv = append([]string{invocation.Command.Executable}, invocation.Command.Args...)
		item.CustomArgs = invocation.CustomArgs
		item.CustomAt = invocation.CustomArgsAt + 1
		out = append(out, item)
	}
	return out
}
