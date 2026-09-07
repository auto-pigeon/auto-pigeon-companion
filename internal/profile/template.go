package profile

import (
	"fmt"
	"sort"
	"strings"
)

// The template language.
//
// An argument is literal text with `{namespace.name}` placeholders in it. That
// is the whole language. There is no nesting, no concatenation operator, no
// conditional inside a placeholder, no arithmetic, no function call and no
// default-value syntax, and every one of those absences is deliberate: each is
// an evaluator, and an evaluator running over a document somebody else wrote is
// the thing this package exists to not have.
//
// What the language cannot express is handled outside it. A conditional
// argument is [ArgWhen] on the argument, which is total and inspectable. A
// derived path is a declared output. A per-machine path is a root role, filled
// in from a local binding that never travels with the document.
//
// A literal `{` or `}` cannot be written. Escaping would mean a second syntax
// layer whose only user would be a profile trying to look like something it is
// not, and no tool argument has ever needed a brace.

// Namespaces a placeholder may name.
const (
	NSRoot       = "root"       // a declared filesystem root, by role
	NSPlatform   = "platform"   // os, arch, exe_suffix
	NSExecutable = "executable" // a declared executable of this profile, resolved to a path
	NSInput      = "input"      // a declared input's resolved path
	NSOutput     = "output"     // a declared output's resolved path
	NSOption     = "option"     // a declared option's value
	NSRuntime    = "runtime"    // a value the user supplies at launch time
)

// Root roles. A profile says which *kind* of place it needs, never where that
// place is; the mapping from role to a path on this machine is a local binding.
const (
	RootWorkspace   = "workspace"    // the per-job scratch directory
	RootProject     = "project_root" // the user's map project — source files
	RootGame        = "game_root"    // an installed game's base directory
	RootContent     = "content_root" // where built content is published (a mod directory)
	RootToolInstall = "tool_root"    // where this profile's own executables live
	RootToolCache   = "tool_cache"   // the Companion's download cache
	// RootBuild is where one pipeline run's stages hand files to each other.
	//
	// It exists because a job workspace is per job and is deleted, and a
	// pipeline is several jobs: the `.bsp` `qbsp` produced has to survive the
	// compile job's cleanup and be readable by the vis job, and neither the
	// user's project nor the mod directory is the right place to put a build's
	// half-finished intermediates. It is created and owned by the build runner,
	// never named by the user.
	RootBuild = "build_root"
)

var rootRoles = []string{RootWorkspace, RootProject, RootGame, RootContent, RootToolInstall, RootToolCache, RootBuild}

// Platform placeholders.
var platformNames = []string{"os", "arch", "exe_suffix"}

// Runtime placeholders: the closed set of values a user supplies when they
// press the button. Closed rather than open because a profile that could invent
// a runtime name could ask the Companion to prompt for anything, including
// something that looks like a password field.
const (
	RuntimeMapName     = "map_name"
	RuntimePackageName = "package_name"
	RuntimeModName     = "mod_name"
	RuntimeServerHost  = "server_host"
	RuntimeServerPort  = "server_port"
)

var runtimeNames = []string{RuntimeMapName, RuntimePackageName, RuntimeModName, RuntimeServerHost, RuntimeServerPort}

// ref is one placeholder.
type ref struct {
	Namespace string
	Name      string
}

func (r ref) String() string { return "{" + r.Namespace + "." + r.Name + "}" }

// part is either literal text or a placeholder.
type part struct {
	literal string
	ref     ref
	isRef   bool
}

// template is a parsed argument template.
type template struct {
	source string
	parts  []part
}

// refs lists the placeholders a template uses, in order of first appearance.
func (t template) refs() []ref {
	var out []ref
	seen := map[ref]bool{}
	for _, p := range t.parts {
		if p.isRef && !seen[p.ref] {
			seen[p.ref] = true
			out = append(out, p.ref)
		}
	}
	return out
}

// parseTemplate splits a template into literals and placeholders. It validates
// only the shape; whether a placeholder is *allowed here* is scope.check's job,
// because that answer depends on which member of which action the string is.
func parseTemplate(s string) (template, error) {
	t := template{source: s}
	rest := s
	for {
		before, after, found := strings.Cut(rest, "{")
		if before != "" {
			if i := strings.IndexByte(before, '}'); i >= 0 {
				return template{}, fmt.Errorf("a stray `}` at offset %d; a literal brace cannot be written in a template", len(s)-len(rest)+i)
			}
			t.parts = append(t.parts, part{literal: before})
		}
		if !found {
			return t, nil
		}
		body, tail, closed := strings.Cut(after, "}")
		if !closed {
			return template{}, fmt.Errorf("an unclosed `{`; every placeholder must be written `{namespace.name}`")
		}
		if strings.ContainsAny(body, "{") {
			return template{}, fmt.Errorf("a nested `{` inside %q; placeholders do not nest", "{"+body+"}")
		}
		namespace, name, hasDot := strings.Cut(body, ".")
		if !hasDot || namespace == "" || name == "" {
			return template{}, fmt.Errorf("the placeholder `{%s}` is not `{namespace.name}`", body)
		}
		if strings.Contains(name, ".") {
			return template{}, fmt.Errorf("the placeholder `{%s}` has more than one dot", body)
		}
		t.parts = append(t.parts, part{ref: ref{Namespace: namespace, Name: name}, isRef: true})
		rest = tail
	}
}

// scope is what a template may refer to at one position in the document.
//
// Positions differ on purpose. An executable's file name inside its own install
// directory may mention the platform's `.exe` suffix and nothing else — a file
// name that could interpolate a game root would be a way to point the "run this
// program" member at any path on the machine. An argument may mention almost
// anything, because an argument is what the permission summary is *about*.
type scope struct {
	roots       bool
	platform    bool
	executables map[string]bool
	inputs      map[string]bool
	outputs     map[string]bool
	options     map[string]bool
	runtime     bool
}

// check validates a template's placeholders against this scope, reporting each
// fault with the name that was wrong and the names that would have been right.
func (s scope) check(c *collector, t template) {
	for _, r := range t.refs() {
		switch r.Namespace {
		case NSRoot:
			if !s.roots {
				s.reject(c, r, "a filesystem root")
				continue
			}
			if !contains(rootRoles, r.Name) {
				c.fixf("use one of: "+strings.Join(rootRoles, ", "), "names the unknown root role %q", r.Name)
			}
		case NSPlatform:
			if !s.platform {
				s.reject(c, r, "a platform value")
				continue
			}
			if !contains(platformNames, r.Name) {
				c.fixf("use one of: "+strings.Join(platformNames, ", "), "names the unknown platform value %q", r.Name)
			}
		case NSExecutable:
			s.checkDeclared(c, r, s.executables, "executable")
		case NSInput:
			s.checkDeclared(c, r, s.inputs, "input")
		case NSOutput:
			s.checkDeclared(c, r, s.outputs, "output")
		case NSOption:
			s.checkDeclared(c, r, s.options, "option")
		case NSRuntime:
			if !s.runtime {
				s.reject(c, r, "a runtime value")
				continue
			}
			if !contains(runtimeNames, r.Name) {
				c.fixf("use one of: "+strings.Join(runtimeNames, ", "), "names the unknown runtime value %q", r.Name)
			}
		default:
			c.fixf("placeholder namespaces are: "+strings.Join([]string{NSRoot, NSPlatform, NSExecutable, NSInput, NSOutput, NSOption, NSRuntime}, ", "),
				"uses the unknown placeholder namespace %q in %s", r.Namespace, r)
		}
	}
}

func (s scope) reject(c *collector, r ref, what string) {
	c.fixf("move it to an argument, where it is allowed", "refers to %s (%s), which this member may not use", what, r)
}

func (s scope) checkDeclared(c *collector, r ref, declared map[string]bool, what string) {
	if declared == nil {
		s.reject(c, r, "a declared "+what)
		return
	}
	if !declared[r.Name] {
		names := sortedKeys(declared)
		fix := "declare it first"
		if len(names) > 0 {
			fix = "declared " + what + "s are: " + strings.Join(names, ", ")
		}
		c.fixf(fix, "refers to the undeclared %s %q", what, r.Name)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// Env supplies the concrete values a template resolves against. Everything in
// it comes from the local machine — a binding, a job workspace, what the user
// typed — and none of it ever travels back into a document.
type Env struct {
	Roots       map[string]string
	Platform    Platform
	Executables map[string]string
	Inputs      map[string]string
	Outputs     map[string]string
	Options     map[string]string
	Runtime     map[string]string
}

// ErrUnresolved reports a placeholder with no value.
type ErrUnresolved struct {
	Ref      string
	Template string
	Known    []string
}

func (e *ErrUnresolved) Error() string {
	msg := fmt.Sprintf("profile: %s in %q has no value", e.Ref, e.Template)
	if len(e.Known) > 0 {
		msg += " (known: " + strings.Join(e.Known, ", ") + ")"
	}
	return msg
}

// resolve renders a template. An unresolved placeholder is an error and never
// an empty string: silently dropping one produces an argv that is subtly wrong
// — `-basedir` followed by the next flag — and that is far harder to diagnose
// than a refusal naming the placeholder.
func (t template) resolve(env Env) (string, error) {
	var b strings.Builder
	for _, p := range t.parts {
		if !p.isRef {
			b.WriteString(p.literal)
			continue
		}
		value, err := env.lookup(p.ref, t.source)
		if err != nil {
			return "", err
		}
		b.WriteString(value)
	}
	return b.String(), nil
}

func (e Env) lookup(r ref, source string) (string, error) {
	var (
		table map[string]string
		known []string
	)
	switch r.Namespace {
	case NSPlatform:
		switch r.Name {
		case "os":
			return e.Platform.OS, nil
		case "arch":
			return e.Platform.Arch, nil
		case "exe_suffix":
			return e.Platform.ExeSuffix(), nil
		}
		known = platformNames
	case NSRoot:
		table = e.Roots
	case NSExecutable:
		table = e.Executables
	case NSInput:
		table = e.Inputs
	case NSOutput:
		table = e.Outputs
	case NSOption:
		table = e.Options
	case NSRuntime:
		table = e.Runtime
	}
	if table != nil {
		if value, ok := table[r.Name]; ok {
			if value == "" {
				return "", &ErrUnresolved{Ref: r.String(), Template: source, Known: sortedKeys(table)}
			}
			return value, nil
		}
		known = sortedKeys(table)
	}
	sort.Strings(known)
	return "", &ErrUnresolved{Ref: r.String(), Template: source, Known: known}
}
