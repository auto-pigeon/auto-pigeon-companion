// Package compat decides whether the program a binding names is the program
// the selected profile was written for (NEW_323D).
//
// # What was wrong
//
// The built-in ericw-tools 2.0 profile passes `-logfile` to vis and light. Its
// own "find them on PATH" route admits that PATH usually holds 0.18.1. A
// binding to 0.18.1 was nevertheless called ready by Profiles and by every
// pipeline list, because readiness asked three things — is it approved, is
// there a file, is the folder there — and none of them was "what is this
// file". qbsp 0.18.1 then compiled the map and vis 0.18.1 refused the option,
// which is a failed build a user has to open to learn that their setup was
// never going to work.
//
// # One decision, three readers
//
// A profile states what it requires of each program
// ([profile.CompatibilityProbe]); asking the program is a supervised job like
// any other (ADR-0003, [job.Service.CheckCompatibility]); the answer is kept in
// the binding as an [binding.ExecutableFact]. This package is the one place
// the three are put together:
//
//   - [Judge] is the decision. The pages read it through [engine.Checker] and
//     the executor reads it before it starts anything, so Profiles, Build & Run,
//     the HTTP API and the CLI cannot disagree.
//   - [Classify] reads a program's own output. It is the only thing that turns
//     bytes into "compatible" or "incompatible".
//
// # What a pass is not
//
// A compatible program has been shown to be the one the document describes.
// Nothing here grants an approval, chooses another program, or says anything
// about what the compiler does to a map.
package compat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Kind is how a finding stops an action, coarser than a [binding.FactState]
// and what a caller reacts to.
type Kind string

const (
	// Incompatible: the program was asked and it is not what the profile was
	// written for.
	Incompatible Kind = "tool_incompatible"
	// Unusable: the file is there and cannot be run as a program here.
	Unusable Kind = "tool_unusable"
	// Unverified: nothing has established what the program is — it was never
	// asked, it changed since it was, or it gave no readable answer.
	Unverified Kind = "tool_unverified"
)

// Finding is why one action's program does not satisfy its profile.
type Finding struct {
	Kind Kind
	// State is the recorded state, or "" when nothing is recorded for the
	// program as it is now.
	State      binding.FactState
	Executable string
	Path       string
	// Identity is what the program called itself, when it did.
	Identity string
	Summary  string
	Fix      string
}

func (f *Finding) Error() string { return f.Summary + " " + f.Fix }

// Requirement is what a document requires of the executable one of its actions
// runs. ok is false when it requires nothing: the document declares no probe,
// or the probe does not name this executable.
func Requirement(document profile.Profile, action profile.Action) (*profile.CompatibilityProbe, string, bool) {
	if _, isProbe := profile.CompatibilityProbeExecutable(action.ID); isProbe {
		return nil, "", false // asking is not something to refuse for not having asked
	}
	tool, isTool := document.(*profile.ToolProfile)
	if !isTool || tool.CompatibilityProbe == nil {
		return nil, "", false
	}
	if _, probed := tool.CompatibilityProbe.For(action.Executable); !probed {
		return nil, "", false
	}
	return tool.CompatibilityProbe, tool.CompatibilityProbe.Requirement(action.Executable), true
}

// ProbedExecutables is every executable of a document that is held to its
// probe AND run by one of its actions, in the document's order.
//
// The second half is deliberate. A probe starts a program, and what a person
// approves by reading a profile is its actions; a program named only by the
// probe — left behind when somebody removed the action that used it — is not
// required by anything, so it is not asked and never started on the probe's
// say-so alone.
func ProbedExecutables(document profile.Profile) []string {
	tool, isTool := document.(*profile.ToolProfile)
	if !isTool || tool.CompatibilityProbe == nil {
		return nil
	}
	run := map[string]bool{}
	for _, action := range tool.Actions {
		run[action.Executable] = true
	}
	names := make([]string, 0, len(tool.CompatibilityProbe.Executables))
	for _, probed := range tool.CompatibilityProbe.Executables {
		if run[probed.Name] {
			names = append(names, probed.Name)
		}
	}
	return names
}

// Classify reads what a program printed when probed.
//
// The order is the point. A program that does not name itself is UNKNOWN, not
// incompatible: nothing was learned. Only a program that named itself can be
// proven wrong, and it is proven wrong by its own words — an identity the
// document does not accept, or a usage that lacks an option the document
// passes.
func Classify(probe *profile.CompatibilityProbe, executable, stdout, stderr string) (state binding.FactState, identity string, missing []string, detail string) {
	output := stdout
	if probe.Parse.Stream == "stderr" {
		output = stderr
	}
	identity, named := probe.Extract(output)
	if !named {
		return binding.FactUnknownOutput, "", nil, "nothing it printed names it the way this profile reads a name"
	}
	if !probe.Accepts(identity) {
		return binding.FactIncompatible, identity, nil,
			fmt.Sprintf("it calls itself %s; this profile accepts %s", identity, probe.AcceptText())
	}
	probed, _ := probe.For(executable)
	for _, mention := range probed.Mentions {
		// Either stream: 0.18.1 prints usage on stdout and some builds on stderr.
		if !strings.Contains(stdout, mention) && !strings.Contains(stderr, mention) {
			missing = append(missing, mention)
		}
	}
	if len(missing) > 0 {
		return binding.FactIncompatible, identity, missing,
			fmt.Sprintf("it calls itself %s and its own usage does not list %s, which this profile passes it", identity, strings.Join(missing, ", "))
	}
	return binding.FactCompatible, identity, nil, ""
}

// Identity is a file's identity for evidence: its size and modification time,
// which a page may compare on every poll, and its SHA-256, which is read
// before anything is started on the strength of an earlier answer.
type Identity struct {
	Size       int64
	ModifiedAt time.Time
	SHA256     string
}

// Stat is the cheap half of an [Identity].
func Stat(path string) (Identity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Size: info.Size(), ModifiedAt: info.ModTime().UTC()}, nil
}

// Digest is the whole [Identity], reading the file once.
func Digest(path string) (Identity, error) {
	file, err := os.Open(path)
	if err != nil {
		return Identity{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Identity{}, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return Identity{}, err
	}
	return Identity{Size: info.Size(), ModifiedAt: info.ModTime().UTC(),
		SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil))}, nil
}

// Judge is the decision: nil when fact shows the program at path is what the
// profile requires, and otherwise why not.
//
// fact is the evidence that DESCRIBES the program as it is now — the caller
// decides that, by [binding.ExecutableFact.Describes] on a page and by digest
// in the executor — or nil when there is none.
func Judge(document profile.Profile, executable, path string, fact *binding.ExecutableFact) *Finding {
	meta := document.Metadata()
	finding := &Finding{Executable: executable, Path: path}
	subject := fmt.Sprintf("The %s recorded for %s", executable, meta.Name)
	rebind := `Choose the folder of a version this profile was written for under "Where these programs are on this machine" in Profiles, or use a profile written for the version you have. Nothing else was chosen for you.`
	recheck := "Press \"Check these programs\" in Profiles, or run `companion toolchain check " + meta.ID + "`."
	if fact == nil {
		finding.Kind = Unverified
		finding.Summary = fmt.Sprintf("%s has not been asked what it is since it was chosen or changed: %s.", subject, path)
		finding.Fix = recheck
		return finding
	}
	finding.State, finding.Identity = fact.State, fact.Identity
	switch fact.State {
	case binding.FactCompatible:
		return nil
	case binding.FactIncompatible:
		finding.Kind = Incompatible
		finding.Summary = fmt.Sprintf("%s is not the program this profile was written for (%s): %s.", subject, fact.Detail, path)
		finding.Fix = rebind
	case binding.FactUnknownOutput:
		finding.Kind = Unverified
		finding.Summary = fmt.Sprintf("%s did not say what it is when asked (%s), so it has not been shown to be the program this profile was written for: %s.", subject, fact.Detail, path)
		finding.Fix = rebind
	case binding.FactTimedOut:
		finding.Kind = Unverified
		finding.Summary = fmt.Sprintf("%s did not answer when asked what it is (%s): %s.", subject, fact.Detail, path)
		finding.Fix = recheck
	case binding.FactNotPermitted:
		finding.Kind = Unusable
		finding.Summary = fmt.Sprintf("%s may not be run (%s): %s.", subject, fact.Detail, path)
		finding.Fix = "Make the file executable, or choose a copy that is, then check it again."
	case binding.FactWrongArchitecture:
		finding.Kind = Unusable
		finding.Summary = fmt.Sprintf("%s is not a program this computer can run (%s): %s.", subject, fact.Detail, path)
		finding.Fix = "Choose the build for this operating system and processor."
	case binding.FactMissing:
		finding.Kind = Unusable
		finding.Summary = fmt.Sprintf("%s is not there: %s.", subject, path)
		finding.Fix = rebind
	default:
		finding.Kind = Unusable
		finding.Summary = fmt.Sprintf("%s could not be asked what it is (%s): %s.", subject, fact.Detail, path)
		finding.Fix = recheck
	}
	return finding
}
