package profile

import (
	"fmt"
	"sort"
	"strings"
)

// Problem is one thing wrong with a document, located and actionable.
//
// Both halves are load-bearing. A profile is usually written by somebody who is
// not the person importing it, so "invalid profile" is useless twice over: the
// importer cannot fix it and the author never sees it. Path says which member of
// which element, in the document's own spelling, and Fix says what to write
// instead.
type Problem struct {
	// Path locates the member, as a JSON path into the document:
	// `actions[2].args[0]`, `executables[qbsp].file`. Empty for a problem with
	// the document as a whole.
	Path string
	// Message states what is wrong, in the present tense.
	Message string
	// Fix states what to do about it. Empty only when the message already is
	// the instruction.
	Fix string
}

func (p Problem) Error() string {
	var b strings.Builder
	if p.Path != "" {
		b.WriteString(p.Path)
		b.WriteString(": ")
	}
	b.WriteString(p.Message)
	if p.Fix != "" {
		b.WriteString(" — ")
		b.WriteString(p.Fix)
	}
	return b.String()
}

// Problems is every fault found in one document.
//
// Validation collects rather than stops at the first fault. Someone fixing a
// profile by hand against a validator that reports one error per run learns the
// document is wrong six times instead of learning what is wrong with it.
type Problems []Problem

func (ps Problems) Error() string {
	switch len(ps) {
	case 0:
		return "profile: no problems"
	case 1:
		return "profile: " + ps[0].Error()
	}
	lines := make([]string, 0, len(ps)+1)
	lines = append(lines, fmt.Sprintf("profile: %d problems:", len(ps)))
	for _, p := range ps {
		lines = append(lines, "  - "+p.Error())
	}
	return strings.Join(lines, "\n")
}

// ErrorOrNil is the usual "nil unless something went wrong" conversion; a
// Problems value that is non-nil but empty would otherwise be a non-nil error.
func (ps Problems) ErrorOrNil() error {
	if len(ps) == 0 {
		return nil
	}
	return ps
}

// collector accumulates problems while a document is walked.
type collector struct {
	path     []string
	problems Problems
}

// at returns a collector scoped to a child member; the parent is unchanged, so
// a walk can branch without the path leaking between siblings.
func (c *collector) at(step string) *collector {
	return &collector{path: append(append([]string{}, c.path...), step), problems: nil}
}

// here renders the current path. The leading separator is trimmed because the
// root of a document has no name, and `.publisher.name` reads like a typo.
func (c *collector) here() string { return strings.TrimPrefix(strings.Join(c.path, ""), ".") }

func (c *collector) addf(format string, args ...any) {
	c.problems = append(c.problems, Problem{Path: c.here(), Message: fmt.Sprintf(format, args...)})
}

func (c *collector) fixf(fix, format string, args ...any) {
	c.problems = append(c.problems, Problem{Path: c.here(), Message: fmt.Sprintf(format, args...), Fix: fix})
}

// merge folds an error from another checker into this collector, keeping the
// paths it already carries.
func (c *collector) merge(err error) {
	switch e := err.(type) {
	case nil:
	case Problems:
		c.problems = append(c.problems, e...)
	default:
		c.problems = append(c.problems, Problem{Path: c.here(), Message: e.Error()})
	}
}

// child runs fn against a sub-path and folds whatever it found back in.
func (c *collector) child(step string, fn func(*collector)) {
	sub := c.at(step)
	fn(sub)
	c.problems = append(c.problems, sub.problems...)
}

// field is the path step for an object member, index for an array element, and
// key for a member selected by name (an executable, an action) — the last one
// reads better than a positional index in an error a human has to act on.
func field(name string) string    { return "." + name }
func index(i int) string          { return fmt.Sprintf("[%d]", i) }
func key(name string) string      { return "[" + name + "]" }
func root(name string) *collector { return &collector{path: []string{name}} }

// sortedKeys is used wherever a map is reported on, so two runs over the same
// document produce the same error order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
