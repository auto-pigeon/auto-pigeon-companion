package build

import "strings"

// Where a run's compiler version came from (AUCOM #49, NEW_633). The order is
// the order of trust: the program's own word about the bytes that ran, then the
// Companion's record of the release those bytes came from, then a probe from
// when the tool was set up. Never the profile's `tool_version`: that is what
// the document was WRITTEN FOR, and a managed install takes the newest
// verified release (HITL 2026-11-12), so the two part ways by design.
const (
	// RanFromExecutable: the compatibility probe asked these exact bytes —
	// the executable's SHA-256 matched when the build started.
	RanFromExecutable = "executable"
	// RanFromManagedInstall: the binding was the Companion's own install of an
	// official release, recorded when the build started.
	RanFromManagedInstall = "managed_install"
	// RanFromSetupProbe: a version probe answered when the tool was bound. It
	// is not tied to a digest, so it is said as "reported when set up".
	RanFromSetupProbe = "setup_probe"
	// RanNotRecorded: nothing this record holds says which build ran — a run
	// from before the identity was kept, or a program never asked. The label
	// says so; it is never filled in with the profile's number.
	RanNotRecorded = "not_recorded"
)

// Ran is what this record can say about the program that ran.
type Ran struct {
	// Source is one of the RanFrom… / RanNotRecorded tokens.
	Source string `json:"source"`
	// Identity is what the executable called itself when its probe asked
	// these bytes, or what the setup probe reported.
	Identity string `json:"identity,omitempty"`
	// ManagedTool and ManagedVersion name the Companion's installation.
	ManagedTool    string `json:"managed_tool,omitempty"`
	ManagedVersion string `json:"managed_version,omitempty"`
	// Label is the one line a person reads for the compiler of this run.
	Label string `json:"label"`
	// Profile is the profile, named as the profile: its name, its document
	// version and the program version it was written for.
	Profile string `json:"profile"`
}

// Ran says which program ran for this tool, from the evidence in the record.
// executablePath picks the executable whose identity counts — the one a step
// started; empty — or a path the record lists no executable at — takes the
// first executable that has one. A listed executable without an identity has
// none: another program's word is not borrowed for it.
func (t ToolRecord) Ran(executablePath string) Ran {
	ran := Ran{Source: RanNotRecorded, Profile: t.ProfileLabel()}
	listed := false
	for _, executable := range t.Executables {
		listed = listed || (executablePath != "" && executable.Path == executablePath)
	}
	for _, executable := range t.Executables {
		if executable.Identity == "" || (listed && executable.Path != executablePath) {
			continue
		}
		ran.Identity, ran.Source = executable.Identity, RanFromExecutable
		break
	}
	if t.Managed != nil && t.Managed.Version != "" {
		ran.ManagedTool, ran.ManagedVersion = t.Managed.Tool, t.Managed.Version
		if ran.Source == RanNotRecorded {
			ran.Source = RanFromManagedInstall
		}
	}
	if ran.Source == RanNotRecorded && t.ResolvedVersion != "" {
		ran.Identity, ran.Source = t.ResolvedVersion, RanFromSetupProbe
	}
	managed := strings.TrimSpace(ran.ManagedTool + " " + ran.ManagedVersion)
	switch {
	case managed != "" && ran.Source == RanFromExecutable:
		ran.Label = managed + " (" + ran.Identity + ")"
	case managed != "":
		ran.Label = managed
	case ran.Source == RanFromExecutable:
		ran.Label = ran.Identity
	case ran.Source == RanFromSetupProbe:
		ran.Label = ran.Identity + " (reported when it was set up)"
	default:
		ran.Label = "version not recorded for this run"
	}
	return ran
}

// ProfileLabel names the profile as the profile: "<name> <version>, written
// for <tool_version>" — so a build number in a profile's name is never read as
// the build that ran.
func (t ToolRecord) ProfileLabel() string {
	name := t.Profile.Name
	if strings.TrimSpace(name) == "" {
		name = t.Profile.ID
	}
	label := strings.TrimSpace(name + " " + t.Profile.Version)
	if t.ToolVersion != "" {
		label += ", written for " + t.ToolVersion
	}
	return label
}

// StepRan is Ran for the tool a step used, chosen by the step's profile and
// the executable it started. False when the manifest records no such tool.
func (m *Manifest) StepRan(step Step) (Ran, bool) {
	if step.Profile.ID == "" {
		return Ran{}, false
	}
	path := ""
	if step.Command != nil {
		path = step.Command.Executable
	}
	for _, tool := range m.Tools {
		if tool.Profile.ID == step.Profile.ID {
			return tool.Ran(path), true
		}
	}
	return Ran{}, false
}
