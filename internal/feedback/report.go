package feedback

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/maturity"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Schema is the report format's identifier, carried in the document so a
// recipient reads it as what it is rather than guessing from the shape.
const Schema = "aucom.compat-report/1.0"

// The caps. A compatibility report is a paragraph and a list, not an archive;
// a member that could grow without bound is a member somebody eventually pastes
// a log into.
const (
	MaxSummaryLength     = 200
	MaxDescriptionLength = 4000
	MaxDiagnostics       = 50
)

// Consent is what the user agreed to attach. Every field starts false.
//
// It is a value the caller fills from checkboxes the user actually ticked, and
// it is carried into the report so the recipient can read it. There is no
// "everything" constructor, deliberately: a default that attached more than
// nothing would make the first careless surface the one that decides.
type Consent struct {
	// Versions attaches the Companion's version, the editor's when the report
	// came from one, and the operating system and architecture.
	Versions bool `json:"versions"`
	// Profiles attaches the ids and versions of the game, tool, engine and
	// pipeline profiles that were in play.
	Profiles bool `json:"profiles"`
	// Operation attaches what was being done — `compile`, `package`, `play_map`.
	Operation bool `json:"operation"`
	// Diagnostics attaches which diagnostic rules fired, how often, and the
	// message the profile declares for each. Never the tool's own output.
	Diagnostics bool `json:"diagnostics"`
}

// Nothing reports the state a surface starts in.
func (c Consent) Nothing() bool {
	return !c.Versions && !c.Profiles && !c.Operation && !c.Diagnostics
}

// Versions is what this build and this machine are, at the coarsest useful
// grain.
//
// Platform is `linux/amd64` — an operating system and an instruction set, which
// is the whole of what a compatibility report needs and carries nothing about
// the person. There is no hostname, no username and no machine id, and there is
// nowhere to put one.
type Versions struct {
	Companion string `json:"companion,omitempty"`
	Editor    string `json:"editor,omitempty"`
	Platform  string `json:"platform,omitempty"`
}

// ProfileRef names one document that was in play.
type ProfileRef struct {
	// Role is `game`, `tool`, `engine` or `pipeline`.
	Role string `json:"role"`
	ID   string `json:"id"`
	// Version is the document's own version.
	Version string `json:"version,omitempty"`
	// ToolVersion is the upstream program's version, where the document
	// describes a program. The two are different facts and a report that
	// conflated them would send a maintainer to the wrong changelog.
	ToolVersion string `json:"tool_version,omitempty"`
}

var profileRoles = []string{"game", "tool", "engine", "pipeline"}

// Diagnostic is one rule that fired.
//
// Message is the profile document's own declared message. See the package
// comment for why the tool's line is not here and cannot be added.
type Diagnostic struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Message  string `json:"message,omitempty"`
	Count    int    `json:"count"`
}

// Input is everything a surface has to offer. What reaches the report is
// [Input] filtered by [Consent], never [Input] itself.
type Input struct {
	// EngineFamily is AUB's family the report is about. It decides whether
	// there is anything to report against in the first place.
	EngineFamily string
	// Summary and Description are the user's own words, and are the one part of
	// a report that is always included: they are why the user pressed the
	// button.
	Summary     string
	Description string
	// Operation is what was being done when it went wrong.
	Operation string

	CompanionVersion string
	EditorVersion    string
	Platform         string

	Profiles    []ProfileRef
	Diagnostics []Diagnostic
}

// Report is the finished document.
type Report struct {
	Schema string `json:"schema"`
	// CreatedOn is a date, `YYYY-MM-DD`, and not a timestamp. A maintainer
	// needs to know roughly when; a second-resolution clock reading is a weak
	// identifier and buys nothing.
	CreatedOn string `json:"created_on"`
	// EngineFamily and Maturity say what the report is about and what this
	// build claims about it — so a reader knows the user was told it was
	// unfinished before they wrote in.
	EngineFamily string `json:"engine_family"`
	Maturity     string `json:"maturity"`

	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`

	// Shared is what the user agreed to attach. Present even when nothing was,
	// because "did not share" and "shared, and there was none" are different
	// answers.
	Shared Consent `json:"shared"`

	Versions    *Versions    `json:"versions,omitempty"`
	Operation   string       `json:"operation,omitempty"`
	Profiles    []ProfileRef `json:"profiles,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Build assembles a report from what the user consented to share.
//
// `createdOn` is passed in rather than read from the clock so that a caller —
// and a test — decides what date the document carries.
func Build(in Input, consent Consent, createdOn string) (Report, error) {
	statement := maturity.Of(in.EngineFamily)

	report := Report{
		Schema:       Schema,
		CreatedOn:    strings.TrimSpace(createdOn),
		EngineFamily: strings.ToLower(strings.TrimSpace(in.EngineFamily)),
		Maturity:     string(statement.State),
		Summary:      collapse(in.Summary),
		Description:  strings.TrimSpace(in.Description),
		Shared:       consent,
	}

	if consent.Versions {
		versions := Versions{
			Companion: strings.TrimSpace(in.CompanionVersion),
			Editor:    strings.TrimSpace(in.EditorVersion),
			Platform:  strings.TrimSpace(in.Platform),
		}
		if versions != (Versions{}) {
			report.Versions = &versions
		}
	}
	if consent.Operation {
		report.Operation = collapse(in.Operation)
	}
	if consent.Profiles {
		// Sorted, so two reports of the same situation are the same document
		// and a maintainer reading twenty of them is comparing content rather
		// than iteration order.
		refs := append([]ProfileRef(nil), in.Profiles...)
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Role != refs[j].Role {
				return refs[i].Role < refs[j].Role
			}
			return refs[i].ID < refs[j].ID
		})
		report.Profiles = refs
	}
	if consent.Diagnostics {
		found := append([]Diagnostic(nil), in.Diagnostics...)
		sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
		report.Diagnostics = found
	}

	if err := Validate(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

// Validate refuses a report that must not be shared.
//
// It is called by [Build], and separately by anything that reads a report back
// from a file: a document that arrived from somewhere else has had nobody
// vouch for it.
//
// The central check is [profile.CheckPortable], which is the same scan that
// stops a profile document being published with a machine path or a credential
// in it. One rule, one implementation: a report and a profile are both
// documents that leave the machine, and they should not disagree about what may
// go in one.
func Validate(r Report) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if r.Schema != Schema {
		add("schema is %q, and this build writes %q", r.Schema, Schema)
	}
	if !isDate(r.CreatedOn) {
		add("created_on is %q; a report carries a date as YYYY-MM-DD", r.CreatedOn)
	}
	if strings.TrimSpace(r.Summary) == "" {
		add("summary is empty: a report with nothing in the user's own words is a report nobody can act on")
	}
	if len(r.Summary) > MaxSummaryLength {
		add("summary is %d bytes, over the %d-byte limit", len(r.Summary), MaxSummaryLength)
	}
	if len(r.Description) > MaxDescriptionLength {
		add("description is %d bytes, over the %d-byte limit — a compatibility report is a paragraph, not a log",
			len(r.Description), MaxDescriptionLength)
	}
	if r.Versions != nil && !r.Shared.Versions {
		add("versions are attached and the user did not agree to attach them")
	}
	if r.Operation != "" && !r.Shared.Operation {
		add("an operation is attached and the user did not agree to attach it")
	}
	if len(r.Profiles) > 0 && !r.Shared.Profiles {
		add("profiles are attached and the user did not agree to attach them")
	}
	if len(r.Diagnostics) > 0 && !r.Shared.Diagnostics {
		add("diagnostics are attached and the user did not agree to attach them")
	}
	if len(r.Diagnostics) > MaxDiagnostics {
		add("%d diagnostics are attached, over the limit of %d", len(r.Diagnostics), MaxDiagnostics)
	}
	for i, ref := range r.Profiles {
		if !contains(profileRoles, ref.Role) {
			add("profiles[%d].role is %q; the roles are %s", i, ref.Role, strings.Join(profileRoles, ", "))
		}
		if strings.TrimSpace(ref.ID) == "" {
			add("profiles[%d] names no profile", i)
		}
	}
	for i, d := range r.Diagnostics {
		if strings.TrimSpace(d.ID) == "" {
			add("diagnostics[%d] has no rule id", i)
		}
		if d.Count < 0 {
			add("diagnostics[%d] fired %d times", i, d.Count)
		}
	}

	// And the whole document, every string in it, against the two rules that
	// already govern anything that leaves this machine.
	//
	// [profile.CheckPortable] is the one that stops a profile being published
	// with a machine path, a home directory, a network address or an obvious
	// credential in it. A report is the same kind of object — a document that
	// leaves — so it gets the same rule rather than a second opinion about what
	// a path is.
	if err := profile.CheckPortable(r); err != nil {
		var problems profile.Problems
		if errors.As(err, &problems) {
			// Reported one member at a time, with the *report's* remedy rather
			// than the profile's. `CheckPortable` tells a profile author to
			// name a root role, which is the right advice for a document that
			// runs a program and nonsense in a bug report: what a person
			// writing one has to do is take the path out of their sentence.
			for _, problem := range problems {
				add("%s %s. Take it out: a report goes to somebody else's machine, "+
					"where it is at best meaningless and at worst yours to have lost",
					fieldName(problem.Path), problem.Message)
			}
		} else {
			add("%v", err)
		}
	}
	// [job.Redactor] is the other one: the patterns that recognise a credential
	// a *tool* printed. `CheckPortable` is written for authored, reviewed prose
	// and is deliberately narrow; a compatibility report is free text somebody
	// typed while something was broken, which is exactly where a pasted session
	// token lands.
	//
	// Here it is used as a detector rather than a filter, and that is the whole
	// point. Redacting quietly would produce a document the user believes they
	// wrote, containing `[redacted]` where their token was, with nothing to tell
	// them it happened. A refusal names the field and hands the decision back.
	if field := credentialField(r); field != "" {
		add("%s contains something shaped like a credential; it is not redacted for you, because a "+
			"report you did not write is a report you did not consent to send", field)
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("feedback: this report cannot be shared:\n  - %s", strings.Join(problems, "\n  - "))
}

// Encode renders a report for a person to read before they share it.
//
// Indented, with a trailing newline, because the user is expected to look at it
// — a report they cannot read is a consent they did not give.
func Encode(r Report) ([]byte, error) {
	if err := Validate(r); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("feedback: encoding the report: %w", err)
	}
	return append(out, '\n'), nil
}

// Decode reads a report back and validates it.
func Decode(data []byte) (Report, error) {
	var r Report
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return Report{}, fmt.Errorf("feedback: this is not a compatibility report: %w", err)
	}
	if err := Validate(r); err != nil {
		return Report{}, err
	}
	return r, nil
}

// collapse folds whitespace so a summary is one line however it was typed.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func isDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, ch := range s {
		if i == 4 || i == 7 {
			continue
		}
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// credentialField names the first member whose text the redactor would change.
//
// The redactor is the authority on what a credential looks like; asking whether
// it *would* act, rather than letting it act, is what turns one shared
// definition into a refusal.
func credentialField(r Report) string {
	redactor := job.NewRedactor()
	fields := []struct {
		name string
		text string
	}{
		{"the summary", r.Summary},
		{"the description", r.Description},
		{"the operation", r.Operation},
	}
	for _, d := range r.Diagnostics {
		fields = append(fields, struct {
			name string
			text string
		}{"the diagnostic " + d.ID, d.Message})
	}
	for _, p := range r.Profiles {
		fields = append(fields, struct {
			name string
			text string
		}{"the profile reference " + p.ID, p.ID + " " + p.Version + " " + p.ToolVersion})
	}
	if r.Versions != nil {
		fields = append(fields, struct {
			name string
			text string
		}{"the versions", r.Versions.Companion + " " + r.Versions.Editor + " " + r.Versions.Platform})
	}
	for _, f := range fields {
		if f.text == "" {
			continue
		}
		if redactor.Redact(f.text) != f.text {
			return f.name
		}
	}
	return ""
}

// Occurrence is one diagnostic as a run recorded it, before anything decides
// whether it may be shared.
//
// It exists so a caller can hand over what it has without first knowing the
// rules — the caller has a job record, not an opinion about what a path looks
// like.
type Occurrence struct {
	RuleID   string
	Severity string
	// Message is what the run recorded. A profile's diagnostic rules usually
	// declare a sentence; where one does not, the job record keeps the matched
	// line instead — and that line is the tool's own output, with the user's
	// filenames in it.
	Message string
}

// Summarize folds a run's diagnostics into the form a report carries: one entry
// per rule and severity, counted, with the message only where the message is
// safe to send.
//
// A message that is not safe is dropped and the entry is kept. That is the
// right way round: "`texture_missing` fired 14 times" is the fact a maintainer
// needs, and it is not worth losing because the accompanying line happened to
// contain the path of somebody's unreleased map. The caller is told how many
// messages were withheld so it can say so.
func Summarize(found []Occurrence) (diagnostics []Diagnostic, messagesWithheld int) {
	type key struct{ id, severity string }
	order := make([]key, 0, len(found))
	byKey := map[key]*Diagnostic{}
	for _, o := range found {
		k := key{strings.TrimSpace(o.RuleID), strings.TrimSpace(o.Severity)}
		if k.id == "" {
			continue
		}
		entry, seen := byKey[k]
		if !seen {
			entry = &Diagnostic{ID: k.id, Severity: k.severity}
			byKey[k] = entry
			order = append(order, k)
		}
		entry.Count++
		if entry.Message != "" {
			continue
		}
		if message := strings.TrimSpace(o.Message); message != "" {
			if shareable(message) {
				entry.Message = message
			} else {
				messagesWithheld++
			}
		}
	}
	for _, k := range order {
		diagnostics = append(diagnostics, *byKey[k])
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].ID < diagnostics[j].ID })
	return diagnostics, messagesWithheld
}

// shareable applies the two rules a whole report is checked against to one
// string, so a message can be dropped individually instead of failing the
// document it would have been part of.
func shareable(text string) bool {
	if err := profile.CheckPortable(struct {
		Text string `json:"text"`
	}{text}); err != nil {
		return false
	}
	return job.NewRedactor().Redact(text) == text
}

// fieldName turns a JSON path from [profile.CheckPortable] into something a
// person recognises. An empty path is the document itself, which for a report is
// the thing they just typed.
func fieldName(path string) string {
	switch {
	case path == "":
		return "this report"
	case strings.HasPrefix(path, "diagnostics"):
		return "a diagnostic in this report"
	case strings.HasPrefix(path, "profiles"):
		return "a profile reference in this report"
	}
	return "the " + strings.ReplaceAll(path, "_", " ")
}
