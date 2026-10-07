package profile

import (
	"fmt"
	"strings"
)

// PipelineProfile is an ordered list of capabilities and the files that flow
// between them.
//
// # Why this is not a list of commands
//
// The obvious shape for "compile, then vis, then light" is three command lines.
// That shape would make a pipeline a script, and a script that arrives from
// another person is a program you are being asked to run — which is exactly
// what this whole model exists not to be. It would also bind the pipeline to
// one implementation of each stage, so a user with a different compiler could
// not run it.
//
// A pipeline therefore names *capabilities* and *wires*. Which tool provides
// `q1.bsp.compile` is a question answered on the machine, by what the user has
// installed and granted; the pipeline says only that the stage happens, what it
// consumes and where its output goes next.
//
// # A stage may name its tool (NEW_310, HITL 2026-10-06)
//
// A capability alone stops answering "which tool" the moment a second installed
// tool provides it — and the New profile wizard invites exactly that: a user's
// own EricW profile declares `q1.bsp.compile` like the built-in one. Before this
// every pipeline needing the capability was refused, the built-in leak test
// included, found live on Windows. HITL chose explicit over ranked: a stage may
// carry `tool`, the id of the tool profile that must provide its capability.
// A stage that names its tool is never ambiguous; a stage that does not keeps
// the refusal when two tools provide its capability. The built-in pipelines
// name the built-in tools.

// PipelineStep is one stage.
type PipelineStep struct {
	ID          string `json:"id" aucom:"required"`
	Title       string `json:"title" aucom:"required"`
	Description string `json:"description,omitempty"`
	// Capability is what this step needs done. It is resolved to a concrete
	// tool action at execution time, against the profiles the user has
	// installed and granted.
	Capability string `json:"capability" aucom:"required"`
	// Tool is the id of the tool profile that must provide Capability, when
	// the stage says. Empty means any installed tool that provides it — and a
	// refusal when more than one does. See the package note above.
	Tool string `json:"tool,omitempty"`
	// Inputs wire this step's declared inputs to earlier artifacts.
	Inputs []PipelineWire `json:"inputs,omitempty"`
	// Options are values for the resolved action's declared options. They are
	// checked against that action's [OptionSpec] when the pipeline is resolved,
	// so a pipeline cannot smuggle a value the tool's author did not allow.
	Options map[string]string `json:"options,omitempty"`
	// Optional marks a step that may be skipped — a preview pipeline that
	// leaves out lighting — without the pipeline failing.
	Optional bool `json:"optional,omitempty"`
}

// PipelineWire connects one of a step's inputs to an artifact that already
// exists: either the pipeline's own input or an earlier step's output.
type PipelineWire struct {
	// Name is the input name on the step's resolved action.
	Name string `json:"name" aucom:"required"`
	// From is `pipeline.<input>` or `<step>.<output>`. Two spellings, both
	// unambiguous, no expression to evaluate.
	From string `json:"from" aucom:"required"`
}

// PipelineOutput is an artifact the pipeline publishes when it finishes.
type PipelineOutput struct {
	Name  string `json:"name" aucom:"required"`
	Title string `json:"title,omitempty"`
	Role  string `json:"role" aucom:"required"`
	From  string `json:"from" aucom:"required"`
	// Optional marks an output that may be absent — a leak file that only
	// exists when there was a leak.
	Optional bool `json:"optional,omitempty"`
}

// PipelineProfile is the document.
type PipelineProfile struct {
	Meta
	GameProfile *GameProfileRef `json:"game_profile,omitempty"`
	// Inputs are what the user supplies to start the pipeline.
	Inputs  []InputSpec      `json:"inputs" aucom:"required"`
	Steps   []PipelineStep   `json:"steps" aucom:"required"`
	Outputs []PipelineOutput `json:"outputs,omitempty"`
}

// Metadata implements [Profile].
func (p *PipelineProfile) Metadata() Meta { return p.Meta }

// ActionList implements [Profile]. A pipeline runs no process of its own: its
// steps become actions belonging to whichever tool profiles resolve them.
func (p *PipelineProfile) ActionList() []Action { return nil }

// ActionByID implements [Profile].
func (p *PipelineProfile) ActionByID(string) (Action, bool) { return Action{}, false }

// Permissions implements [Profile].
//
// A pipeline asks for nothing on its own account. Every permission a run needs
// belongs to the tool profile that resolves one of its steps, and is granted
// there. Returning an empty set rather than a union is deliberate: a pipeline
// that appeared to grant permissions would be a way to acquire them without the
// user ever seeing the tool that would use them.
func (p *PipelineProfile) Permissions() []Permission { return nil }

// Validate implements [Profile].
func (p *PipelineProfile) Validate() error {
	c := root("")
	p.Meta.validate(c, KindPipeline)
	if p.GameProfile != nil {
		c.child(field("game_profile"), p.GameProfile.validate)
	}

	inputs := map[string]bool{}
	c.child(field("inputs"), func(c *collector) {
		if len(p.Inputs) == 0 {
			c.fixf("declare what the user supplies", "is empty: a pipeline with no input has nothing to work on")
		}
		validateNamed(c, p.Inputs, func(i InputSpec) string { return i.Name }, func(c *collector, i InputSpec) { i.validate(c) })
	})
	for _, i := range p.Inputs {
		inputs[i.Name] = true
	}

	// `produced` accumulates as the steps are walked, so a wire can only reach
	// backwards. That is what makes a cycle unrepresentable rather than
	// detectable: there is no point at which a later step's output is visible.
	produced := map[string]bool{}
	c.child(field("steps"), func(c *collector) {
		if len(p.Steps) == 0 {
			c.fixf("declare at least one step", "is empty")
		}
		validateNamed(c, p.Steps, func(s PipelineStep) string { return s.ID }, func(c *collector, s PipelineStep) {
			c.child(field("id"), func(c *collector) { checkToken(c, s.ID) })
			c.child(field("title"), func(c *collector) { checkText(c, s.Title, maxNameLength, true) })
			c.child(field("description"), func(c *collector) { checkText(c, s.Description, maxTextLength, false) })
			c.child(field("capability"), func(c *collector) { checkArtifactRole(c, s.Capability) })
			if s.Tool != "" {
				c.child(field("tool"), func(c *collector) { checkID(c, s.Tool) })
			}
			c.child(field("inputs"), func(c *collector) {
				for i, w := range s.Inputs {
					c.child(index(i), func(c *collector) {
						c.child(field("name"), func(c *collector) { checkToken(c, w.Name) })
						c.child(field("from"), func(c *collector) { checkWire(c, w.From, inputs, produced, s.ID) })
					})
				}
			})
			c.child(field("options"), func(c *collector) {
				if len(s.Options) > maxListLength {
					c.addf("has %d entries, over the %d limit", len(s.Options), maxListLength)
					return
				}
				for _, name := range sortedKeys(s.Options) {
					c.child(key(name), func(c *collector) {
						checkToken(c, name)
						value := s.Options[name]
						checkText(c, value, maxArgLength, false)
						checkNoShellSyntax(c, value)
						if strings.Contains(value, "{") {
							c.fixf("write a literal value; a pipeline option is not a template",
								"contains a `{`, and a pipeline option is not templated")
						}
					})
				}
			})
			// Recorded last, and inside the per-step call, so the wire check
			// above sees only the steps declared before this one. That is what
			// makes a cycle unrepresentable rather than merely detected.
			produced[s.ID] = true
		})
	})

	c.child(field("outputs"), func(c *collector) {
		validateNamed(c, p.Outputs, func(o PipelineOutput) string { return o.Name }, func(c *collector, o PipelineOutput) {
			c.child(field("name"), func(c *collector) { checkToken(c, o.Name) })
			c.child(field("title"), func(c *collector) { checkText(c, o.Title, maxNameLength, false) })
			c.child(field("role"), func(c *collector) { checkArtifactRole(c, o.Role) })
			c.child(field("from"), func(c *collector) { checkWire(c, o.From, inputs, produced, "") })
		})
	})
	// Portability is part of validity, not a separate step before export. A
	// document with a machine path or a credential in it is invalid wherever it
	// is read, and catching it only at export would mean the author's own
	// Companion accepted it happily right up until they tried to share it.
	c.merge(CheckPortable(p))
	return c.problems.ErrorOrNil()
}

// StepLogOutput is the reserved output every step has without declaring it: the
// text the step's program wrote to its standard output, as the executor stored
// it when the process ended — however it ended (`Q3_018`).
//
// A pipeline wires it like any other artifact, `"from": "compile.stdout"`, with
// the role [StepLogRole]. It exists because not every compiler writes a log
// file: EricW's qbsp does and declares it; Q3Map2 prints and writes none, and
// the only record of what it said about a leak is this.
const (
	StepLogOutput = "stdout"
	StepLogRole   = "aucom.step.stdout"
)

// checkWire validates one `pipeline.<input>` or `<step>.<output>` reference.
//
// `produced` holds the steps whose outputs are visible at this point. `self` is
// the step being validated, so a step wiring itself is named as such rather
// than reported as an unknown step.
func checkWire(c *collector, from string, inputs, produced map[string]bool, self string) {
	if from == "" {
		c.fixf("write `pipeline.<input>` or `<step>.<output>`", "is required and empty")
		return
	}
	source, name, ok := strings.Cut(from, ".")
	if !ok || source == "" || name == "" {
		c.fixf("write `pipeline.<input>` or `<step>.<output>`", "is %q, which is not a wire reference", from)
		return
	}
	if strings.Contains(name, ".") {
		c.fixf("write `pipeline.<input>` or `<step>.<output>`", "is %q, which has more than one dot", from)
		return
	}
	if source == "pipeline" {
		if !inputs[name] {
			c.fixf("declared pipeline inputs are: "+strings.Join(sortedKeys(inputs), ", "),
				"names the undeclared pipeline input %q", name)
		}
		return
	}
	if source == self {
		c.fixf("wire it to an earlier step, or to a pipeline input", "wires the step %q to its own output", self)
		return
	}
	if !produced[source] {
		fix := "a step may only use an artifact from a step declared before it"
		if len(produced) > 0 {
			fix = "earlier steps are: " + strings.Join(sortedKeys(produced), ", ")
		}
		c.fixf(fix, "names the step %q, which is not declared before this point", source)
	}
}

// Resolution: what a pipeline needs from the profiles installed on a machine.

// Resolver answers "which action implements this capability", which is a
// question about the machine and not about the document. The executor supplies
// one; this package defines the shape so a pipeline can be checked against a
// set of installed profiles without either side importing the other.
type Resolver interface {
	// Provider returns the profile and action implementing a capability.
	Provider(capability string) (*ToolProfile, Action, bool)
}

// ToolResolver is a [Resolver] that can also answer for one named tool: the
// action of tool profile `tool` that provides `capability`. A stage that names
// its tool is resolved through it; a resolver without it cannot resolve one.
type ToolResolver interface {
	Resolver
	ProviderFrom(tool, capability string) (*ToolProfile, Action, bool)
}

// providerFor resolves one step: from its named tool when it names one.
func providerFor(r Resolver, step PipelineStep) (*ToolProfile, Action, bool) {
	if step.Tool == "" {
		return r.Provider(step.Capability)
	}
	named, ok := r.(ToolResolver)
	if !ok {
		return nil, Action{}, false
	}
	return named.ProviderFrom(step.Tool, step.Capability)
}

// ResolvedStep is one step bound to a concrete action.
type ResolvedStep struct {
	Step    PipelineStep
	Profile *ToolProfile
	Action  Action
}

// Resolve binds every step to an installed tool action and checks the wiring
// and the options against what those actions actually declare.
//
// It runs nothing. Its value is that a pipeline fails here — before a user has
// waited through two stages of a three-stage compile — rather than at the point
// the third stage is handed an option it has never heard of.
func (p *PipelineProfile) Resolve(r Resolver) ([]ResolvedStep, error) {
	c := root("")
	out := make([]ResolvedStep, 0, len(p.Steps))
	// Artifact roles available so far, by wire reference.
	available := map[string]string{}
	for _, i := range p.Inputs {
		available["pipeline."+i.Name] = i.Role
	}

	c.child(field("steps"), func(c *collector) {
		for i, step := range p.Steps {
			c.child(index(i), func(c *collector) {
				profile, action, ok := providerFor(r, step)
				if !ok && step.Tool != "" {
					c.fixf("install "+step.Tool+", or choose another tool for this stage",
						"needs the capability %q from the tool %s, and no installed profile %s provides it",
						step.Capability, step.Tool, step.Tool)
					return
				}
				if !ok {
					c.fixf("install and grant a tool that provides it",
						"needs the capability %q, and no installed profile provides it", step.Capability)
					return
				}
				resolveStepInputs(c, step, action, available)
				resolveStepOptions(c, step, action)
				// Every step's own output text is an artifact too, under a
				// reserved name. Declared first, so an action that names an
				// output `stdout` itself keeps its own.
				available[step.ID+"."+StepLogOutput] = StepLogRole
				for _, o := range action.Outputs {
					available[step.ID+"."+o.Name] = o.Role
				}
				out = append(out, ResolvedStep{Step: step, Profile: profile, Action: action})
			})
		}
	})
	c.child(field("outputs"), func(c *collector) {
		for i, o := range p.Outputs {
			c.child(index(i), func(c *collector) {
				role, ok := available[o.From]
				if !ok {
					c.fixf("wire it to a pipeline input or a step output that exists",
						"reads %q, which no step produces", o.From)
					return
				}
				if role != o.Role {
					c.fixf(fmt.Sprintf("declare the role as %q, or wire it to something that produces %q", role, o.Role),
						"expects a %q artifact but %q produces a %q", o.Role, o.From, role)
				}
			})
		}
	})
	if err := c.problems.ErrorOrNil(); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveStepInputs(c *collector, step PipelineStep, action Action, available map[string]string) {
	wired := map[string]bool{}
	c.child(field("inputs"), func(c *collector) {
		for i, w := range step.Inputs {
			c.child(index(i), func(c *collector) {
				var spec InputSpec
				found := false
				for _, in := range action.Inputs {
					if in.Name == w.Name {
						spec, found = in, true
					}
				}
				if !found {
					names := make([]string, 0, len(action.Inputs))
					for _, in := range action.Inputs {
						names = append(names, in.Name)
					}
					c.fixf("the action's inputs are: "+strings.Join(names, ", "),
						"wires %q, which the action %q does not declare", w.Name, action.ID)
					return
				}
				role, ok := available[w.From]
				if !ok {
					c.fixf("wire it to a pipeline input or an earlier step's output",
						"reads %q, which nothing produces", w.From)
					return
				}
				if role != spec.Role {
					c.fixf(fmt.Sprintf("wire it to something that produces %q", spec.Role),
						"supplies a %q artifact where the action wants a %q", role, spec.Role)
				}
				wired[w.Name] = true
			})
		}
		for _, in := range action.Inputs {
			if in.Required && !wired[in.Name] {
				c.fixf(fmt.Sprintf("add {\"name\": %q, \"from\": \"…\"}", in.Name),
					"does not wire the required input %q of the action %q", in.Name, action.ID)
			}
		}
	})
}

func resolveStepOptions(c *collector, step PipelineStep, action Action) {
	c.child(field("options"), func(c *collector) {
		for _, name := range sortedKeys(step.Options) {
			c.child(key(name), func(c *collector) {
				var spec OptionSpec
				found := false
				for _, o := range action.Options {
					if o.Name == name {
						spec, found = o, true
					}
				}
				if !found {
					names := make([]string, 0, len(action.Options))
					for _, o := range action.Options {
						names = append(names, o.Name)
					}
					fix := "the action declares no options"
					if len(names) > 0 {
						fix = "the action's options are: " + strings.Join(names, ", ")
					}
					c.fixf(fix, "sets %q, which the action %q does not declare", name, action.ID)
					return
				}
				if err := spec.Check(step.Options[name]); err != nil {
					c.addf("%v", err)
				}
			})
		}
	})
}
