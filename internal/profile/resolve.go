package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Resolution: turning a document plus a machine into one exact process.
//
// This is where the model's central claim is either true or not. A curated
// profile and a profile a user typed by hand go through this function and
// nothing else; there is no branch on trust, no built-in fast path, no
// `if profile.ID == …`. Two documents that describe the same command produce
// the same [Command], and [Command.Digest] is how a test says so.
//
// Resolution does not touch the filesystem and starts nothing. What it produces
// is a description the executor supervises and the interface previews — and the
// preview is the same value that runs, which is the only way a command preview
// is worth showing.

// Request is everything the machine contributes.
type Request struct {
	Platform Platform
	// Roots maps a root role to a path on this machine. It comes from a local
	// binding and a job workspace; nothing in it was ever in a document.
	Roots map[string]string
	// Executables maps a declared executable name to its resolved path,
	// overriding the profile's own relative file. A `user_path` acquisition
	// fills this in; a managed download leaves it empty and lets the profile's
	// `file` resolve under `tool_root`.
	Executables map[string]string
	// Inputs maps declared input names to paths.
	Inputs map[string]string
	// Options is what the user chose. Unset options fall back to the declared
	// default.
	Options map[string]string
	// Runtime is the launch-time values: the map to load, the server to join.
	Runtime map[string]string
	// HostEnv supplies values for the environment variables the action asked to
	// inherit. Names not in the action's `inherit` list are never read from it.
	HostEnv map[string]string
	// ExtraArgs are argument tokens this machine's setup adds to the action's
	// executable (NEW_265): literal words, one argv element each, never
	// templates and never split. They go in before the action's trailing
	// positional block — see [CustomArgsIndex] — and are checked by
	// [ValidateCustomArgs] here as well as where they are saved, so a binding
	// edited by hand cannot put a NUL or a template into a command.
	ExtraArgs []string
}

// Command is the part of an invocation that determines what actually runs.
//
// Split out from [Invocation] so that "these two profiles run the same thing"
// is a statement that can be made without "these two profiles are the same
// document" having to be true.
type Command struct {
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	WorkingDir string            `json:"working_dir"`
	Env        map[string]string `json:"env,omitempty"`
}

// Digest identifies a command by what it does. Paths are compared as they will
// be used, argument order is significant, and the environment is sorted.
func (c Command) Digest() string {
	var b strings.Builder
	b.WriteString(c.Executable)
	b.WriteByte('\x00')
	for _, a := range c.Args {
		b.WriteString(a)
		b.WriteByte('\x00')
	}
	b.WriteString(c.WorkingDir)
	b.WriteByte('\x00')
	for _, k := range sortedKeys(c.Env) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(c.Env[k])
		b.WriteByte('\x00')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// String renders the command the way a shell would show it.
//
// For display only. The quoting is approximate, and it has to be: the Companion
// never runs a command through a shell, so there is no shell whose quoting
// rules this could be correct for. Anything that re-executed this string would
// be introducing the shell this whole package exists to avoid.
func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	for _, part := range append([]string{c.Executable}, c.Args...) {
		if part == "" || strings.ContainsAny(part, " \t\"'\\") {
			part = `"` + strings.ReplaceAll(part, `"`, `\"`) + `"`
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// Invocation is a resolved action: the command, plus everything the executor
// needs to supervise it and everything the interface needs to explain it.
type Invocation struct {
	ProfileID      string `json:"profile_id"`
	ProfileVersion string `json:"profile_version"`
	ProfileDigest  string `json:"profile_digest,omitempty"`
	ActionID       string `json:"action_id"`
	Command        Command
	// CustomArgs are the [Request.ExtraArgs] that went into Command.Args, in
	// the order they appear there. Kept apart so a record can say which words
	// were the user's own.
	CustomArgs []string `json:"custom_args,omitempty"`
	// CustomArgsAt is the index in Command.Args where CustomArgs begin.
	// Meaningless when there are none.
	CustomArgsAt int `json:"custom_args_at,omitempty"`
	// Outputs maps declared output names to the paths they will appear at.
	Outputs map[string]string `json:"outputs,omitempty"`
	// OptionalOutputs names the outputs whose absence is not a failure.
	OptionalOutputs []string `json:"optional_outputs,omitempty"`
	// ReadRoots and WriteRoots are the resolved directories the executor
	// confines the process to.
	ReadRoots  []string `json:"read_roots,omitempty"`
	WriteRoots []string `json:"write_roots,omitempty"`
	// Network is what the action declared, flattened to a value: an invocation
	// always has an answer to "does this go online", even when the document
	// answered it by saying nothing. The executor enforces it; carrying it here
	// means the preview and the enforcement read the same value.
	Network          NetworkNeed      `json:"network"`
	TimeoutSeconds   int              `json:"timeout_seconds,omitempty"`
	SuccessExitCodes []int            `json:"success_exit_codes,omitempty"`
	SessionRole      SessionRole      `json:"session_role,omitempty"`
	Diagnostics      []DiagnosticRule `json:"diagnostics,omitempty"`
}

// Resolve turns one action of a profile into an invocation.
func Resolve(p Profile, actionID string, request Request) (Invocation, error) {
	meta := p.Metadata()
	action, found := p.ActionByID(actionID)
	if !found {
		ids := make([]string, 0)
		for _, a := range p.ActionList() {
			ids = append(ids, a.ID)
		}
		sort.Strings(ids)
		return Invocation{}, fmt.Errorf("profile: %s has no action %q (it has: %s)", meta.ID, actionID, strings.Join(ids, ", "))
	}
	if request.Platform.Zero() {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: no platform was given", meta.ID, actionID)
	}
	if err := checkActionPlatform(p, action, request.Platform); err != nil {
		return Invocation{}, err
	}

	options, err := resolveOptions(action, request.Options)
	if err != nil {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: %w", meta.ID, actionID, err)
	}
	if err := checkInputs(action, request.Inputs); err != nil {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: %w", meta.ID, actionID, err)
	}

	workingDir, err := resolveWorkingDir(action, request)
	if err != nil {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: %w", meta.ID, actionID, err)
	}

	// Outputs resolve first: their paths are values the argument array refers
	// to, so they have to exist before the arguments are rendered.
	env := Env{
		Roots:       request.Roots,
		Platform:    request.Platform,
		Executables: request.Executables,
		Inputs:      request.Inputs,
		Options:     options,
		Runtime:     request.Runtime,
	}
	outputs, optional, err := resolveOutputs(action, env, workingDir, request.Inputs)
	if err != nil {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: %w", meta.ID, actionID, err)
	}
	env.Outputs = outputs

	executable, err := resolveExecutable(p, action, request)
	if err != nil {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: %w", meta.ID, actionID, err)
	}

	args, insertAt, err := resolveArgs(action, env, options, request.Inputs)
	if err != nil {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: %w", meta.ID, actionID, err)
	}
	var custom []string
	if len(request.ExtraArgs) > 0 {
		if err := ValidateCustomArgs(request.ExtraArgs); err != nil {
			return Invocation{}, fmt.Errorf("profile: resolving %s/%s: your own arguments for %q: %w",
				meta.ID, actionID, action.Executable, err)
		}
		custom = append([]string(nil), request.ExtraArgs...)
		merged := make([]string, 0, len(args)+len(custom))
		merged = append(merged, args[:insertAt]...)
		merged = append(merged, custom...)
		merged = append(merged, args[insertAt:]...)
		args = merged
	}
	environment, err := resolveEnvironment(action, env, request.HostEnv)
	if err != nil {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: %w", meta.ID, actionID, err)
	}
	readRoots, writeRoots, err := resolveRoots(action, request.Roots)
	if err != nil {
		return Invocation{}, fmt.Errorf("profile: resolving %s/%s: %w", meta.ID, actionID, err)
	}

	exitCodes := action.SuccessExitCodes
	if len(exitCodes) == 0 {
		exitCodes = []int{0}
	}
	return Invocation{
		ProfileID:      meta.ID,
		ProfileVersion: meta.Version,
		ActionID:       action.ID,
		Command: Command{
			Executable: executable,
			Args:       args,
			WorkingDir: workingDir,
			Env:        environment,
		},
		CustomArgs:      custom,
		CustomArgsAt:    insertAt,
		Outputs:         outputs,
		OptionalOutputs: optional,
		ReadRoots:       readRoots,
		WriteRoots:      writeRoots,
		Network: func() NetworkNeed {
			if action.Network == nil {
				return NetworkNeed{}
			}
			return *action.Network
		}(),
		TimeoutSeconds:   action.TimeoutSeconds,
		SuccessExitCodes: exitCodes,
		SessionRole:      action.SessionRole,
		Diagnostics:      action.Diagnostics,
	}, nil
}

// checkActionPlatform refuses a platform the profile does not claim, and says
// which claim it is refusing on. An `unverified` platform is allowed to run:
// the profile said nobody had checked, which is information, not a prohibition.
func checkActionPlatform(p Profile, action Action, want Platform) error {
	if len(action.Platforms) > 0 {
		matched := false
		for _, candidate := range action.Platforms {
			if candidate == want {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("profile: %s does not offer %q on %s", p.Metadata().ID, action.ID, want)
		}
	}
	type supporter interface {
		SupportFor(Platform) (Support, string)
	}
	if s, ok := p.(supporter); ok {
		status, note := s.SupportFor(want)
		if status == Unsupported {
			return fmt.Errorf("profile: %s does not support %s: %s", p.Metadata().ID, want, note)
		}
	}
	return nil
}

func resolveOptions(action Action, chosen map[string]string) (map[string]string, error) {
	specs := map[string]OptionSpec{}
	for _, o := range action.Options {
		specs[o.Name] = o
	}
	for _, name := range sortedKeys(chosen) {
		if _, declared := specs[name]; !declared {
			declaredNames := sortedKeys(specs)
			if len(declaredNames) == 0 {
				return nil, fmt.Errorf("the option %q was set, but the action declares no options", name)
			}
			return nil, fmt.Errorf("the option %q was set, but the action declares only: %s", name, strings.Join(declaredNames, ", "))
		}
	}
	out := make(map[string]string, len(specs))
	for _, name := range sortedKeys(specs) {
		spec := specs[name]
		value, set := chosen[name]
		if !set {
			value = spec.Default
			if value == "" && spec.Type == OptionBool {
				value = "false"
			}
		}
		if value == "" {
			continue // unset and undefaulted; arguments that use it must be conditional
		}
		if err := spec.Check(value); err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, nil
}

func checkInputs(action Action, supplied map[string]string) error {
	declared := map[string]bool{}
	for _, in := range action.Inputs {
		declared[in.Name] = true
		if in.Required && strings.TrimSpace(supplied[in.Name]) == "" {
			return fmt.Errorf("the required input %q was not supplied", in.Name)
		}
	}
	for _, name := range sortedKeys(supplied) {
		if !declared[name] {
			return fmt.Errorf("the input %q was supplied, but the action does not declare it", name)
		}
	}
	return nil
}

func resolveWorkingDir(action Action, request Request) (string, error) {
	role, sub := RootWorkspace, ""
	if action.WorkingDir != nil {
		if action.WorkingDir.Root != "" {
			role = action.WorkingDir.Root
		}
		sub = action.WorkingDir.Path
	}
	base, ok := request.Roots[role]
	if !ok || base == "" {
		return "", fmt.Errorf("the working directory needs the %q root, which is not configured on this machine", role)
	}
	if sub == "" {
		return filepath.Clean(base), nil
	}
	return filepath.Join(base, filepath.FromSlash(sub)), nil
}

func resolveOutputs(action Action, env Env, workingDir string, inputs map[string]string) (map[string]string, []string, error) {
	outputs := make(map[string]string, len(action.Outputs))
	var optional []string
	for _, out := range action.Outputs {
		if out.InPlace != "" {
			// The path the executor staged the input at, verbatim. Not
			// recomputed and not guessed: the whole point of `in_place` is that
			// this is one path, known to one component, and the profile does
			// not get to have an opinion about it.
			staged, ok := inputs[out.InPlace]
			if !ok || strings.TrimSpace(staged) == "" {
				return nil, nil, fmt.Errorf("the output %q is the input %q rewritten in place, and that input was not supplied", out.Name, out.InPlace)
			}
			resolved := filepath.Clean(staged)
			if out.Extension != "" {
				resolved = strings.TrimSuffix(resolved, filepath.Ext(resolved)) + out.Extension
			}
			outputs[out.Name] = resolved
			if out.Optional {
				optional = append(optional, out.Name)
			}
			continue
		}
		t, err := parseTemplate(out.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("the output %q has an unreadable path: %w", out.Name, err)
		}
		rendered, err := t.resolve(Env{Options: env.Options, Runtime: env.Runtime})
		if err != nil {
			return nil, nil, fmt.Errorf("the output %q: %w", out.Name, err)
		}
		// Re-checked after resolution: a path that was contained as a template
		// can stop being contained once an option's value is in it.
		if err := containedRelative(rendered); err != nil {
			return nil, nil, fmt.Errorf("the output %q resolves to %q, which %w", out.Name, rendered, err)
		}
		outputs[out.Name] = filepath.Join(workingDir, filepath.FromSlash(rendered))
		if out.Optional {
			optional = append(optional, out.Name)
		}
	}
	return outputs, optional, nil
}

// containedRelative is the post-resolution containment check. Validation
// catches a document that is written to escape; this catches one that escapes
// only for certain option values, which is the interesting case.
func containedRelative(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("is empty")
	case strings.ContainsRune(p, '\x00'):
		return fmt.Errorf("contains a NUL byte")
	case strings.HasPrefix(p, "/"), strings.Contains(p, `\`):
		return fmt.Errorf("is not a relative POSIX path")
	case len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':':
		return fmt.Errorf("names a drive letter")
	}
	cleaned := path.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("leaves the directory it is written into")
	}
	return nil
}

func resolveExecutable(p Profile, action Action, request Request) (string, error) {
	if resolved, ok := request.Executables[action.Executable]; ok && resolved != "" {
		return filepath.Clean(resolved), nil
	}
	var file string
	for _, e := range executablesOf(p) {
		if e.Name == action.Executable {
			file = e.File
		}
	}
	if file == "" {
		return "", fmt.Errorf("the executable %q is not declared by this profile", action.Executable)
	}
	t, err := parseTemplate(file)
	if err != nil {
		return "", fmt.Errorf("the executable %q has an unreadable file name: %w", action.Executable, err)
	}
	rendered, err := t.resolve(Env{Platform: request.Platform})
	if err != nil {
		return "", err
	}
	if err := containedRelative(rendered); err != nil {
		return "", fmt.Errorf("the executable %q resolves to %q, which %w", action.Executable, rendered, err)
	}
	base, ok := request.Roots[RootToolInstall]
	if !ok || base == "" {
		return "", fmt.Errorf("the executable %q lives under the %q root, which is not configured; acquire the tool first, or point the Companion at an existing copy",
			action.Executable, RootToolInstall)
	}
	return filepath.Join(base, filepath.FromSlash(rendered)), nil
}

// executablesOf reaches the declared executables of whichever kind of profile
// this is. Pipelines have none, and resolve no actions of their own.
func executablesOf(p Profile) []Executable {
	switch v := p.(type) {
	case *ToolProfile:
		return v.Executables
	case *EngineProfile:
		return v.Executables
	}
	return nil
}

func resolveArgs(action Action, env Env, options map[string]string, inputs map[string]string) ([]string, int, error) {
	args := make([]string, 0, len(action.Args))
	positional := make([]bool, 0, len(action.Args))
	for i, arg := range action.Args {
		include, err := argIncluded(arg, options, inputs, env.Roots)
		if err != nil {
			return nil, 0, fmt.Errorf("argument %d: %w", i, err)
		}
		if !include {
			continue
		}
		t, err := parseTemplate(arg.Value)
		if err != nil {
			return nil, 0, fmt.Errorf("argument %d is unreadable: %w", i, err)
		}
		rendered, err := t.resolve(env)
		if err != nil {
			return nil, 0, fmt.Errorf("argument %d: %w", i, err)
		}
		if strings.ContainsRune(rendered, '\x00') {
			return nil, 0, fmt.Errorf("argument %d resolves to a value containing a NUL byte", i)
		}
		args = append(args, rendered)
		positional = append(positional, fileReference(arg.Value))
	}
	return args, customArgsIndex(args, positional), nil
}

// fileReference reports whether a declared argument is exactly one input or
// output path — the positional operands a compiler reads last.
func fileReference(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") || strings.Count(value, "{") != 1 {
		return false
	}
	return strings.HasPrefix(value, "{input.") || strings.HasPrefix(value, "{output.")
}

// customArgsIndex is where a user's own argument tokens go in a rendered argv.
//
// Options before operands: `qbsp [options] sourcefile [destfile]`, `vis
// [options] bspfile`, and a Quake engine's `-options` before its `+commands`.
// So the tokens are inserted before whichever comes first of
//
//   - the trailing run of input/output paths (qbsp's `level.map level.bsp`),
//     together with the flag just before that run when the first path is that
//     flag's value (`-o {output.bsp}` stays one pair); and
//   - the first `+command` (an engine's `+map e1m1`).
//
// With neither, they go at the end. Computed from the document's own
// declarations, so it is the same answer for every tool that follows the
// convention, and a preview shows exactly where they landed.
func customArgsIndex(args []string, positional []bool) int {
	at := len(args)
	for at > 0 && positional[at-1] {
		at--
	}
	if at < len(args) && at > 0 && strings.HasPrefix(args[at-1], "-") && !positional[at-1] {
		// `-o <path>`: the path is the flag's value, not an operand of its
		// own, and a token placed between them would become the value.
		if at == len(args)-1 {
			at--
		}
	}
	for i := 0; i < at; i++ {
		if strings.HasPrefix(args[i], "+") {
			return i
		}
	}
	return at
}

// MaxCustomArgs and MaxCustomArgBytes bound what one executable's own
// arguments may be. Generous for a person's flags; small enough that a setup
// file cannot be used to smuggle a payload into a command line.
const (
	MaxCustomArgs     = 32
	MaxCustomArgBytes = 512
)

// ValidateCustomArgs checks a user's own argument tokens.
//
// Each token is one argv element exactly as typed: nothing splits it and
// nothing quotes it, which is why a Windows path with spaces is one valid
// token. What is refused is what could not be an argument (an empty token, a
// NUL, a line break or another control character), what a profile renders as
// a template (`{…}` would look like `{root.game_root}` and is not expanded
// here), and more than a bound.
func ValidateCustomArgs(tokens []string) error {
	if len(tokens) > MaxCustomArgs {
		return fmt.Errorf("there are %d arguments; at most %d are allowed", len(tokens), MaxCustomArgs)
	}
	for i, token := range tokens {
		position := i + 1
		switch {
		case strings.TrimSpace(token) == "":
			return fmt.Errorf("argument %d is empty; remove it or type a value", position)
		case len(token) > MaxCustomArgBytes:
			return fmt.Errorf("argument %d is %d bytes long; at most %d are allowed", position, len(token), MaxCustomArgBytes)
		case strings.ContainsAny(token, "{}"):
			return fmt.Errorf("argument %d (%q) contains { or }, which profile templates use; your own arguments are passed exactly as typed and cannot use them", position, token)
		case token != strings.TrimSpace(token):
			return fmt.Errorf("argument %d (%q) starts or ends with a space; each box is one argument exactly as typed", position, token)
		}
		for _, r := range token {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("argument %d contains a control character (such as a line break or a tab); each box is one argument on one line", position)
			}
		}
	}
	return nil
}

func argIncluded(arg Arg, options, inputs, roots map[string]string) (bool, error) {
	if arg.When == nil {
		return true, nil
	}
	if arg.When.Root != "" {
		return strings.TrimSpace(roots[arg.When.Root]) != "", nil
	}
	if arg.When.Input != "" {
		return strings.TrimSpace(inputs[arg.When.Input]) != "", nil
	}
	value, set := options[arg.When.Option]
	if !set {
		return false, nil
	}
	if arg.When.Equals == "" {
		// No expected value: true for a boolean, "set to anything" otherwise.
		return value != "" && value != "false", nil
	}
	return value == arg.When.Equals, nil
}

func resolveEnvironment(action Action, env Env, hostEnv map[string]string) (map[string]string, error) {
	if action.Environment == nil || (len(action.Environment.Inherit) == 0 && len(action.Environment.Set) == 0) {
		return nil, nil
	}
	out := map[string]string{}
	for _, name := range action.Environment.Inherit {
		if value, present := hostEnv[name]; present {
			out[name] = value
		}
	}
	for _, name := range sortedKeys(action.Environment.Set) {
		t, err := parseTemplate(action.Environment.Set[name])
		if err != nil {
			return nil, fmt.Errorf("the environment variable %s is unreadable: %w", name, err)
		}
		rendered, err := t.resolve(env)
		if err != nil {
			return nil, fmt.Errorf("the environment variable %s: %w", name, err)
		}
		out[name] = rendered
	}
	return out, nil
}

func resolveRoots(action Action, roots map[string]string) (read, write []string, err error) {
	for _, r := range action.Roots {
		resolved, ok := roots[r.Role]
		if !ok || resolved == "" {
			if r.Optional {
				continue
			}
			return nil, nil, fmt.Errorf("the action needs the %q root, which is not configured on this machine", r.Role)
		}
		resolved = filepath.Clean(resolved)
		read = append(read, resolved)
		if r.Access == AccessReadWrite {
			write = append(write, resolved)
		}
	}
	sort.Strings(read)
	sort.Strings(write)
	return read, write, nil
}
