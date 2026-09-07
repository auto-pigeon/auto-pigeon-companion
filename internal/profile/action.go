package profile

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// An action is the only thing in this package that becomes a process.
//
// It is an executable chosen from the ones this profile declares, an argument
// array, a working directory expressed as a role plus a relative path, and an
// explicit statement of what it reads, writes, connects to and inherits. There
// is no member anywhere in it that holds a command line.

// Executable is one program a profile provides.
//
// A tool profile usually provides several — a compiler, a visibility stage, a
// light stage, two inspectors — and they are one profile because they are one
// download, one licence and one version. Modelling each as its own profile
// would make a five-part toolchain five acquisitions and five grants.
type Executable struct {
	Name  string `json:"name" aucom:"required"`
	Title string `json:"title,omitempty"`
	// File is the path to the program *inside this profile's own install
	// directory*, relative and POSIX-spelled. `{platform.exe_suffix}` is the
	// only placeholder permitted: a file name that could interpolate a game
	// root would turn "run the program this profile installed" into "run any
	// program on the machine".
	File string `json:"file" aucom:"required"`
}

func (e Executable) validate(c *collector) {
	c.child(field("name"), func(c *collector) { checkToken(c, e.Name) })
	c.child(field("title"), func(c *collector) { checkText(c, e.Title, maxNameLength, false) })
	c.child(field("file"), func(c *collector) {
		checkNoShellSyntax(c, e.File)
		t, err := parseTemplate(e.File)
		if err != nil {
			c.fixf("write `{platform.exe_suffix}` for the executable extension", "%v", err)
			return
		}
		scope{platform: true}.check(c, t)
		// Validate the literal shape with placeholders removed, so
		// `bin/qbsp{platform.exe_suffix}` is checked as `bin/qbsp`.
		checkRelativePath(c, stripPlaceholders(t), true)
	})
}

func stripPlaceholders(t template) string {
	var b strings.Builder
	for _, p := range t.parts {
		if !p.isRef {
			b.WriteString(p.literal)
		}
	}
	return b.String()
}

// ArgWhen makes an argument conditional. It is the whole of the language's
// control flow, and it is total: one named option or input, one optional
// expected value, no operators, no negation of a compound, nothing to evaluate.
//
// Conditionals live on the argument rather than inside the template because a
// condition that can be *seen* can be rendered in a command preview, and a
// preview that shows which arguments were included and why is the thing a user
// is actually approving.
type ArgWhen struct {
	// Option names a declared option.
	Option string `json:"option,omitempty"`
	// Input names a declared input; the condition is "the user supplied it".
	Input string `json:"input,omitempty"`
	// Equals is the value the option must have. Omitted means "is true" for a
	// boolean and "is set to anything" otherwise.
	Equals string `json:"equals,omitempty"`
}

// Arg is one element of an argument array.
//
// It is written as a bare string in the common case and as an object when it is
// conditional. The union is for the person writing the document by hand; the
// canonical form collapses it back to a string whenever there is no condition,
// so the two spellings of an unconditional argument have one digest.
type Arg struct {
	Value string   `json:"value" aucom:"required"`
	When  *ArgWhen `json:"when,omitempty"`
}

// UnmarshalJSON accepts either spelling, strictly in both branches.
func (a *Arg) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		a.Value, a.When = s, nil
		return nil
	}
	var raw struct {
		Value string   `json:"value"`
		When  *ArgWhen `json:"when"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	a.Value, a.When = raw.Value, raw.When
	return nil
}

// MarshalJSON writes the string spelling when there is no condition. This is
// what makes canonicalization total over the union.
func (a Arg) MarshalJSON() ([]byte, error) {
	if a.When == nil {
		return json.Marshal(a.Value)
	}
	return json.Marshal(struct {
		Value string   `json:"value"`
		When  *ArgWhen `json:"when"`
	}{a.Value, a.When})
}

func (a Arg) validate(c *collector, s scope) {
	c.child(field("value"), func(c *collector) {
		checkText(c, a.Value, maxArgLength, true)
		checkNoShellSyntax(c, a.Value)
		t, err := parseTemplate(a.Value)
		if err != nil {
			c.fixf("write `{namespace.name}` placeholders", "%v", err)
			return
		}
		s.check(c, t)
	})
	if a.When == nil {
		return
	}
	c.child(field("when"), func(c *collector) {
		switch {
		case a.When.Option == "" && a.When.Input == "":
			c.fixf("name an option or an input", "names neither an option nor an input")
		case a.When.Option != "" && a.When.Input != "":
			c.fixf("name one or the other", "names both an option and an input")
		case a.When.Option != "":
			if !s.options[a.When.Option] {
				c.fixf("declared options are: "+strings.Join(sortedKeys(s.options), ", "),
					"names the undeclared option %q", a.When.Option)
			}
		case a.When.Input != "":
			if !s.inputs[a.When.Input] {
				c.fixf("declared inputs are: "+strings.Join(sortedKeys(s.inputs), ", "),
					"names the undeclared input %q", a.When.Input)
			}
			if a.When.Equals != "" {
				c.fixf("drop `equals`", "sets `equals` on an input condition, which only tests whether the input was supplied")
			}
		}
		c.child(field("equals"), func(c *collector) { checkText(c, a.When.Equals, 256, false) })
	})
}

// InputSpec is a file an action consumes.
type InputSpec struct {
	Name  string `json:"name" aucom:"required"`
	Title string `json:"title,omitempty"`
	// Role is the artifact role — `q1.map.source`, `q1.bsp`, `wad` — which is
	// what a pipeline matches producers against.
	Role     string `json:"role" aucom:"required"`
	Required bool   `json:"required,omitempty"`
	// Extensions, when given, are the file extensions this input accepts, for
	// the file picker and for a legibility check. Advisory, not a guarantee.
	Extensions []string `json:"extensions,omitempty"`
	// StageWith names another input this one must be staged beside.
	//
	// Some tools do not take every file they read as an argument. `vis` is
	// handed a `.bsp` and then opens the `.prt` of the same stem in the same
	// directory; without it, it says `LoadPortals: couldn't read …` and exits
	// non-zero. The executor stages each input into its own directory so that
	// two inputs cannot collide, which is right by default and exactly wrong
	// for a sidecar — so a sidecar says so, and the two are staged together.
	//
	// One level only: the named input may not itself be staged with a third.
	// A chain would make "which directory does this file end up in" a question
	// with a traversal in it.
	StageWith string `json:"stage_with,omitempty"`
}

func (i InputSpec) validate(c *collector) {
	c.child(field("name"), func(c *collector) { checkToken(c, i.Name) })
	c.child(field("title"), func(c *collector) { checkText(c, i.Title, maxNameLength, false) })
	c.child(field("role"), func(c *collector) { checkArtifactRole(c, i.Role) })
	c.child(field("extensions"), func(c *collector) { checkExtensions(c, i.Extensions) })
	c.child(field("stage_with"), func(c *collector) {
		if i.StageWith != "" {
			checkToken(c, i.StageWith)
		}
	})
}

// StageGroup is the staging directory an input belongs to: its own name, or the
// name of the input it is a sidecar of.
func (i InputSpec) StageGroup() string {
	if i.StageWith != "" {
		return i.StageWith
	}
	return i.Name
}

// OutputSpec is a file an action produces.
type OutputSpec struct {
	Name  string `json:"name" aucom:"required"`
	Title string `json:"title,omitempty"`
	Role  string `json:"role" aucom:"required"`
	// Path is where the file appears, relative to the action's working
	// directory. `{option.…}` and `{runtime.…}` may appear in it; a root may
	// not, because an output that could name a root would be a write outside
	// the workspace dressed as a file name.
	//
	// Required unless InPlace is set, in which case there is no path to write:
	// the output is a file the tool was handed.
	Path string `json:"path,omitempty"`
	// InPlace names an input this output *is*, because the tool rewrote it
	// where it stood.
	//
	// `vis` and `light` do not write a new BSP; they read the one they were
	// given and save it back over itself. Declaring a path for that would mean
	// guessing where the executor staged the input, which is the executor's
	// business and not a profile's — and a wrong guess is a job that reports
	// "the action declared outputs it did not produce" while the tool sits on
	// disk having succeeded. Naming the input instead is exact.
	InPlace string `json:"in_place,omitempty"`
	// Extension is the file extension the tool used for a companion file it
	// wrote beside the one it was handed: `light` is given `level.bsp` and
	// writes `level.lit` next to it. Only meaningful with InPlace, whose path
	// it borrows everything but the extension from.
	Extension string `json:"extension,omitempty"`
	// Optional marks an output the tool may or may not produce — a leak file,
	// a log — so its absence is not a failure.
	Optional bool `json:"optional,omitempty"`
}

func (o OutputSpec) validate(c *collector, s scope) {
	c.child(field("name"), func(c *collector) { checkToken(c, o.Name) })
	c.child(field("title"), func(c *collector) { checkText(c, o.Title, maxNameLength, false) })
	c.child(field("role"), func(c *collector) { checkArtifactRole(c, o.Role) })
	c.child(field("in_place"), func(c *collector) {
		if o.InPlace == "" {
			return
		}
		checkToken(c, o.InPlace)
		if !s.inputs[o.InPlace] {
			c.fixf("declared inputs are: "+strings.Join(sortedKeys(s.inputs), ", "),
				"names the undeclared input %q", o.InPlace)
		}
	})
	c.child(field("extension"), func(c *collector) {
		if o.Extension == "" {
			return
		}
		if o.InPlace == "" {
			c.fixf("set `in_place` to the input it sits beside, or remove it",
				"is set on an output that is not written beside an input")
			return
		}
		checkExtensions(c, []string{o.Extension})
	})
	c.child(field("path"), func(c *collector) {
		switch {
		case o.InPlace != "" && o.Path != "":
			c.fixf("remove one of them", "is set on an output that is already the input %q rewritten in place", o.InPlace)
			return
		case o.InPlace != "":
			return
		case o.Path == "":
			c.fixf("say where the file appears, or set `in_place`", "is required and empty")
			return
		}
		checkNoShellSyntax(c, o.Path)
		t, err := parseTemplate(o.Path)
		if err != nil {
			c.fixf("write `{namespace.name}` placeholders", "%v", err)
			return
		}
		scope{options: s.options, runtime: s.runtime}.check(c, t)
		checkRelativePath(c, stripPlaceholders(t), true)
	})
}

// checkArtifactRole validates an artifact role. Roles are open — this package
// cannot know every file kind a community tool invents — but they are spelled
// like ids so two people naming the same thing land on the same string.
func checkArtifactRole(c *collector, role string) {
	if role == "" {
		c.fixf("name the kind of file, e.g. `q1.map.source`", "is required and empty")
		return
	}
	if len(role) > 64 {
		c.addf("is %d bytes long, over the 64-byte limit", len(role))
		return
	}
	for _, segment := range strings.Split(role, ".") {
		if !isIDSegment(segment) {
			c.fixf("use dot-separated lower-case segments, e.g. `q1.map.source`", "has an invalid segment %q", segment)
			return
		}
	}
}

func checkExtensions(c *collector, exts []string) {
	if len(exts) > 32 {
		c.addf("has %d entries, over the 32 limit", len(exts))
		return
	}
	for i, ext := range exts {
		c.child(index(i), func(c *collector) {
			if !strings.HasPrefix(ext, ".") {
				c.fixf("write it with a leading dot, e.g. `.map`", "is %q", ext)
				return
			}
			if len(ext) > 16 || strings.ContainsAny(ext[1:], "./\\ \t") {
				c.addf("is not a plain file extension: %q", ext)
			}
		})
	}
}

// OptionType is how an option's value is constrained.
type OptionType string

const (
	OptionBool    OptionType = "bool"
	OptionInteger OptionType = "integer"
	OptionEnum    OptionType = "enum"
	OptionText    OptionType = "text"
)

var optionTypes = []string{string(OptionBool), string(OptionInteger), string(OptionEnum), string(OptionText)}

// EnumValue is one choice of an enum option.
type EnumValue struct {
	Value string `json:"value" aucom:"required"`
	Title string `json:"title,omitempty"`
}

// OptionSpec is a knob a user may turn.
//
// # Why options are typed and closed rather than "extra arguments"
//
// The obvious design is a free-text "additional arguments" box appended to
// argv. It is also the design that makes every safety property in this package
// decorative: whatever the profile declared it reads and writes, the user can
// hand the tool `-o /etc/something`, and a command preview stops meaning
// anything because the profile no longer determines the command.
//
// So an override is a declared option with a type, a range and a place in the
// argument array chosen by the profile's author. A tool that needs a knob gets
// a knob; nothing gets a hole.
type OptionSpec struct {
	Name        string     `json:"name" aucom:"required"`
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	Type        OptionType `json:"type" aucom:"required"`
	// Default is the value used when the user sets nothing. Always written as
	// a string, whatever the type, so the document has one spelling per value
	// and canonicalization has no numbers to normalise.
	Default string `json:"default,omitempty"`
	// Values are the choices, for an enum.
	Values []EnumValue `json:"values,omitempty"`
	// Minimum and Maximum bound an integer.
	Minimum *int64 `json:"minimum,omitempty"`
	Maximum *int64 `json:"maximum,omitempty"`
	// MaxLength bounds a text option. Text options are restricted to the token
	// character set: letters, digits, `.`, `_` and `-`. Anything wider is a
	// path or a flag in disguise.
	MaxLength *int `json:"max_length,omitempty"`
	// Advanced marks an option that is hidden behind a disclosure in the UI.
	Advanced bool `json:"advanced,omitempty"`
}

func (o OptionSpec) validate(c *collector) {
	c.child(field("name"), func(c *collector) { checkToken(c, o.Name) })
	c.child(field("title"), func(c *collector) { checkText(c, o.Title, maxNameLength, false) })
	c.child(field("description"), func(c *collector) { checkText(c, o.Description, maxTextLength, false) })
	c.child(field("type"), func(c *collector) {
		if !contains(optionTypes, string(o.Type)) {
			c.fixf("use one of: "+strings.Join(optionTypes, ", "), "is %q", o.Type)
		}
	})
	c.child(field("values"), func(c *collector) {
		switch {
		case o.Type == OptionEnum && len(o.Values) == 0:
			c.fixf("list the choices", "is empty for an enum option")
		case o.Type != OptionEnum && len(o.Values) > 0:
			c.fixf("remove them, or set the type to `enum`", "lists choices for a %q option", o.Type)
		}
		seen := map[string]bool{}
		for i, v := range o.Values {
			c.child(index(i), func(c *collector) {
				c.child(field("value"), func(c *collector) {
					checkText(c, v.Value, 128, true)
					checkNoShellSyntax(c, v.Value)
					if seen[v.Value] {
						c.addf("repeats the value %q", v.Value)
					}
					seen[v.Value] = true
				})
				c.child(field("title"), func(c *collector) { checkText(c, v.Title, maxNameLength, false) })
			})
		}
	})
	c.child(field("minimum"), func(c *collector) {
		if o.Minimum != nil && o.Type != OptionInteger {
			c.fixf("remove it", "bounds a %q option", o.Type)
		}
	})
	c.child(field("maximum"), func(c *collector) {
		if o.Maximum != nil && o.Type != OptionInteger {
			c.fixf("remove it", "bounds a %q option", o.Type)
			return
		}
		if o.Minimum != nil && o.Maximum != nil && *o.Minimum > *o.Maximum {
			c.addf("is %d, below the minimum %d", *o.Maximum, *o.Minimum)
		}
	})
	c.child(field("max_length"), func(c *collector) {
		if o.MaxLength == nil {
			return
		}
		if o.Type != OptionText {
			c.fixf("remove it", "bounds a %q option", o.Type)
			return
		}
		if *o.MaxLength <= 0 || *o.MaxLength > maxArgLength {
			c.addf("is %d; it must be between 1 and %d", *o.MaxLength, maxArgLength)
		}
	})
	c.child(field("default"), func(c *collector) {
		if o.Default == "" {
			return
		}
		if err := o.Check(o.Default); err != nil {
			c.addf("%v", err)
		}
	})
}

// Check validates a value a user supplied against this option. It is the
// "illegal coercion" gate: an integer option never receives the string `x`, and
// a text option never receives a path.
func (o OptionSpec) Check(value string) error {
	switch o.Type {
	case OptionBool:
		if value != "true" && value != "false" {
			return fmt.Errorf("option %q is a boolean; %q is neither `true` nor `false`", o.Name, value)
		}
	case OptionInteger:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("option %q is an integer; %q is not a whole number", o.Name, value)
		}
		if o.Minimum != nil && n < *o.Minimum {
			return fmt.Errorf("option %q is %d, below its minimum of %d", o.Name, n, *o.Minimum)
		}
		if o.Maximum != nil && n > *o.Maximum {
			return fmt.Errorf("option %q is %d, above its maximum of %d", o.Name, n, *o.Maximum)
		}
	case OptionEnum:
		for _, v := range o.Values {
			if v.Value == value {
				return nil
			}
		}
		choices := make([]string, 0, len(o.Values))
		for _, v := range o.Values {
			choices = append(choices, v.Value)
		}
		return fmt.Errorf("option %q is %q, which is not one of: %s", o.Name, value, strings.Join(choices, ", "))
	case OptionText:
		max := 128
		if o.MaxLength != nil {
			max = *o.MaxLength
		}
		if len(value) > max {
			return fmt.Errorf("option %q is %d bytes, over its %d-byte limit", o.Name, len(value), max)
		}
		for i := 0; i < len(value); i++ {
			ch := value[i]
			if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-') {
				return fmt.Errorf("option %q contains %q; a text option may contain letters, digits, `.`, `_` and `-` only", o.Name, string(value[i]))
			}
		}
	default:
		return fmt.Errorf("option %q has the unknown type %q", o.Name, o.Type)
	}
	return nil
}

// Severity classifies a matched line of tool output.
type Severity string

const (
	SeverityError    Severity = "error"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
	SeverityProgress Severity = "progress"
)

var severities = []string{string(SeverityError), string(SeverityWarning), string(SeverityInfo), string(SeverityProgress)}

// MatchKind is how a diagnostic rule matches a line.
//
// There is no `regex`. A regular expression from a document somebody else wrote
// is a denial-of-service primitive — catastrophic backtracking needs no
// privileges and no exploit, only a pattern and a long line — and no diagnostic
// a compiler emits needs one. Three literal comparisons cover it.
type MatchKind string

const (
	MatchContains MatchKind = "contains"
	MatchPrefix   MatchKind = "prefix"
	MatchSuffix   MatchKind = "suffix"
)

var matchKinds = []string{string(MatchContains), string(MatchPrefix), string(MatchSuffix)}

// DiagnosticRule turns a line of tool output into something structured.
//
// Rules never suppress. The raw log is kept whatever they match, and a rule's
// effect is to add a classification, not to filter — a tool's own words are the
// evidence when its wrapper is wrong about it.
type DiagnosticRule struct {
	ID     string `json:"id" aucom:"required"`
	Stream string `json:"stream,omitempty"` // stdout, stderr, both (default)
	Match  string `json:"match" aucom:"required"`
	// Kind defaults to `contains`.
	Kind     MatchKind `json:"kind,omitempty"`
	Severity Severity  `json:"severity" aucom:"required"`
	// Message is what the user is shown instead of the raw line. The raw line
	// is still recorded.
	Message string `json:"message,omitempty"`
	// Hint is what to do about it.
	Hint string `json:"hint,omitempty"`
}

var diagnosticStreams = []string{"stdout", "stderr", "both"}

func (d DiagnosticRule) validate(c *collector) {
	c.child(field("id"), func(c *collector) { checkToken(c, d.ID) })
	c.child(field("stream"), func(c *collector) {
		if d.Stream != "" && !contains(diagnosticStreams, d.Stream) {
			c.fixf("use one of: "+strings.Join(diagnosticStreams, ", "), "is %q", d.Stream)
		}
	})
	c.child(field("match"), func(c *collector) { checkText(c, d.Match, 256, true) })
	c.child(field("kind"), func(c *collector) {
		if d.Kind != "" && !contains(matchKinds, string(d.Kind)) {
			c.fixf("use one of: "+strings.Join(matchKinds, ", ")+" (there is no regular-expression match, by design)", "is %q", d.Kind)
		}
	})
	c.child(field("severity"), func(c *collector) {
		if !contains(severities, string(d.Severity)) {
			c.fixf("use one of: "+strings.Join(severities, ", "), "is %q", d.Severity)
		}
	})
	c.child(field("message"), func(c *collector) { checkText(c, d.Message, maxSummaryLength, false) })
	c.child(field("hint"), func(c *collector) { checkText(c, d.Hint, maxTextLength, false) })
}

// Matches reports whether a line of output matches the rule.
func (d DiagnosticRule) Matches(stream, line string) bool {
	if d.Stream != "" && d.Stream != "both" && d.Stream != stream {
		return false
	}
	switch d.Kind {
	case MatchPrefix:
		return strings.HasPrefix(line, d.Match)
	case MatchSuffix:
		return strings.HasSuffix(line, d.Match)
	default:
		return strings.Contains(line, d.Match)
	}
}

// WorkingDir is where a process starts, as a role plus a relative path.
//
// Not a template, and not a path: a working directory that could be written as
// a string is a working directory that can be `/`, and a tool started in `/`
// with a relative output path writes wherever it likes.
type WorkingDir struct {
	Root string `json:"root" aucom:"required"`
	Path string `json:"path,omitempty"`
}

func (w WorkingDir) validate(c *collector) {
	c.child(field("root"), func(c *collector) {
		if !contains(rootRoles, w.Root) {
			c.fixf("use one of: "+strings.Join(rootRoles, ", "), "is %q, which is not a known root role", w.Root)
		}
	})
	c.child(field("path"), func(c *collector) { checkRelativePath(c, w.Path, false) })
}

// Action is one runnable thing a profile offers.
type Action struct {
	ID          string `json:"id" aucom:"required"`
	Title       string `json:"title" aucom:"required"`
	Description string `json:"description,omitempty"`
	// Capability is the capability id this action implements. A pipeline names
	// capabilities, never profiles, so that a user who installs a different
	// compiler with the same capability can run the same pipeline.
	Capability string `json:"capability,omitempty"`
	// Platforms restricts the action to some of the profile's platforms. Empty
	// means every platform the profile declares `supported` or `unverified`.
	Platforms []Platform `json:"platforms,omitempty"`
	// Executable names one of the profile's declared executables.
	Executable string `json:"executable" aucom:"required"`
	// Args is the argument array, in order.
	Args []Arg `json:"args,omitempty"`
	// WorkingDir defaults to the job workspace. A pointer, so that "not stated"
	// is absent from the canonical form rather than an empty object claiming a
	// root role of "".
	WorkingDir  *WorkingDir      `json:"working_dir,omitempty"`
	Inputs      []InputSpec      `json:"inputs,omitempty"`
	Outputs     []OutputSpec     `json:"outputs,omitempty"`
	Options     []OptionSpec     `json:"options,omitempty"`
	Diagnostics []DiagnosticRule `json:"diagnostics,omitempty"`
	// Roots is every filesystem root this action needs, with the access it
	// needs. An action reaches nothing that is not listed here.
	Roots []RootRef `json:"roots,omitempty"`
	// Network is whether it goes online. Absent means it does not.
	Network *NetworkNeed `json:"network,omitempty"`
	// Environment is what its process inherits. Absent means nothing, which is
	// also what an empty policy means; both are spelled the same way in the
	// canonical form.
	Environment *EnvironmentPolicy `json:"environment,omitempty"`
	// TimeoutSeconds bounds one run. Zero means the executor's default.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// SuccessExitCodes defaults to [0].
	SuccessExitCodes []int `json:"success_exit_codes,omitempty"`
	// SessionRole distinguishes a client from a listen server from a dedicated
	// server. Engine profiles must set it; treating every running process as a
	// public game is how a user ends up hosting one without knowing.
	SessionRole SessionRole `json:"session_role,omitempty"`
}

// SessionRole says what kind of thing an engine action starts.
type SessionRole string

const (
	SessionClient    SessionRole = "client"
	SessionListen    SessionRole = "listen_server"
	SessionDedicated SessionRole = "dedicated_server"
)

var sessionRoles = []string{string(SessionClient), string(SessionListen), string(SessionDedicated)}

// scopeFor builds the set of names this action's templates may refer to.
func (a Action) scopeFor(executables map[string]bool, runtime bool) scope {
	s := scope{
		roots:       true,
		platform:    true,
		executables: executables,
		inputs:      map[string]bool{},
		outputs:     map[string]bool{},
		options:     map[string]bool{},
		runtime:     runtime,
	}
	for _, i := range a.Inputs {
		s.inputs[i.Name] = true
	}
	for _, o := range a.Outputs {
		s.outputs[o.Name] = true
	}
	for _, o := range a.Options {
		s.options[o.Name] = true
	}
	return s
}

func (a Action) validate(c *collector, executables map[string]bool, capabilities map[string]bool, runtime bool) {
	s := a.scopeFor(executables, runtime)

	c.child(field("id"), func(c *collector) { checkToken(c, a.ID) })
	c.child(field("title"), func(c *collector) { checkText(c, a.Title, maxNameLength, true) })
	c.child(field("description"), func(c *collector) { checkText(c, a.Description, maxTextLength, false) })
	c.child(field("capability"), func(c *collector) {
		if a.Capability == "" {
			return
		}
		checkArtifactRole(c, a.Capability)
		if capabilities != nil && !capabilities[a.Capability] {
			c.fixf("declare it in the profile's `capabilities`", "names the undeclared capability %q", a.Capability)
		}
	})
	c.child(field("platforms"), func(c *collector) {
		for i, p := range a.Platforms {
			c.child(index(i), p.validate)
		}
	})
	c.child(field("executable"), func(c *collector) {
		if a.Executable == "" {
			c.fixf("name one of the profile's executables", "is required and empty")
			return
		}
		if !executables[a.Executable] {
			c.fixf("declared executables are: "+strings.Join(sortedKeys(executables), ", "),
				"names the undeclared executable %q", a.Executable)
		}
	})
	c.child(field("args"), func(c *collector) {
		if len(a.Args) > maxListLength {
			c.addf("has %d arguments, over the %d limit", len(a.Args), maxListLength)
			return
		}
		for i, arg := range a.Args {
			c.child(index(i), func(c *collector) { arg.validate(c, s) })
		}
	})
	if a.WorkingDir != nil {
		c.child(field("working_dir"), a.WorkingDir.validate)
	}
	c.child(field("inputs"), func(c *collector) {
		sidecar := map[string]bool{}
		for _, i := range a.Inputs {
			if i.StageWith != "" {
				sidecar[i.Name] = true
			}
		}
		validateNamed(c, a.Inputs, func(i InputSpec) string { return i.Name }, func(c *collector, i InputSpec) {
			i.validate(c)
			if i.StageWith == "" {
				return
			}
			c.child(field("stage_with"), func(c *collector) {
				switch {
				case i.StageWith == i.Name:
					c.fixf("remove it; an input is already staged in its own directory", "names the input itself")
				case !s.inputs[i.StageWith]:
					c.fixf("declared inputs are: "+strings.Join(sortedKeys(s.inputs), ", "),
						"names the undeclared input %q", i.StageWith)
				case sidecar[i.StageWith]:
					c.fixf("stage it with an input that is not itself a sidecar",
						"names %q, which is itself staged with another input", i.StageWith)
				}
			})
		})
	})
	c.child(field("outputs"), func(c *collector) {
		validateNamed(c, a.Outputs, func(o OutputSpec) string { return o.Name }, func(c *collector, o OutputSpec) { o.validate(c, s) })
	})
	c.child(field("options"), func(c *collector) {
		validateNamed(c, a.Options, func(o OptionSpec) string { return o.Name }, func(c *collector, o OptionSpec) { o.validate(c) })
	})
	c.child(field("diagnostics"), func(c *collector) {
		validateNamed(c, a.Diagnostics, func(d DiagnosticRule) string { return d.ID }, func(c *collector, d DiagnosticRule) { d.validate(c) })
	})
	c.child(field("roots"), func(c *collector) {
		seen := map[string]bool{}
		for i, r := range a.Roots {
			c.child(index(i), func(c *collector) {
				r.validate(c)
				if seen[r.Role] {
					c.fixf("declare each root once, with the wider access", "repeats the root role %q", r.Role)
				}
				seen[r.Role] = true
			})
		}
	})
	if a.Network != nil {
		c.child(field("network"), a.Network.validate)
	}
	if a.Environment != nil {
		c.child(field("environment"), func(c *collector) { a.Environment.validate(c, s) })
	}
	c.child(field("timeout_seconds"), func(c *collector) {
		if a.TimeoutSeconds < 0 {
			c.addf("is negative")
		}
		if a.TimeoutSeconds > 24*60*60 {
			c.fixf("a run longer than a day is a hang, not a build", "is %d seconds, over the one-day limit", a.TimeoutSeconds)
		}
	})
	c.child(field("success_exit_codes"), func(c *collector) {
		for i, code := range a.SuccessExitCodes {
			c.child(index(i), func(c *collector) {
				if code < 0 || code > 255 {
					c.addf("is %d; an exit status is between 0 and 255", code)
				}
			})
		}
	})
	c.child(field("session_role"), func(c *collector) {
		if a.SessionRole != "" && !contains(sessionRoles, string(a.SessionRole)) {
			c.fixf("use one of: "+strings.Join(sessionRoles, ", "), "is %q", a.SessionRole)
		}
	})

	// An action that writes an output must have write access to the root it
	// writes into. Catching this here rather than at run time turns "the build
	// failed with a permission error after four minutes of compiling" into a
	// message at import.
	if len(a.Outputs) > 0 {
		writeRoot := RootWorkspace
		if a.WorkingDir != nil && a.WorkingDir.Root != "" {
			writeRoot = a.WorkingDir.Root
		}
		writable := false
		for _, r := range a.Roots {
			if r.Role == writeRoot && r.Access == AccessReadWrite {
				writable = true
			}
		}
		if !writable && writeRoot != RootWorkspace {
			c.child(field("roots"), func(c *collector) {
				c.fixf(fmt.Sprintf("add {\"role\": %q, \"access\": \"read_write\", \"purpose\": \"…\"}", writeRoot),
					"does not grant write access to %q, but the action declares outputs written there", writeRoot)
			})
		}
	}
}

// validateNamed runs a per-item validation and reports duplicate names, which
// is the fault that otherwise shows up as one of two identically named things
// silently winning.
func validateNamed[T any](c *collector, items []T, name func(T) string, fn func(*collector, T)) {
	if len(items) > maxListLength {
		c.addf("has %d entries, over the %d limit", len(items), maxListLength)
		return
	}
	seen := map[string]int{}
	for i, item := range items {
		c.child(index(i), func(c *collector) {
			fn(c, item)
			n := name(item)
			if n == "" {
				return
			}
			if first, dup := seen[n]; dup {
				c.fixf("names must be unique", "repeats the name %q, already used at index %d", n, first)
				return
			}
			seen[n] = i
		})
	}
}
