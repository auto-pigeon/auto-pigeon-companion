package binding

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FactState is what asking a program about itself established.
//
// The states are kept apart on purpose. "This is the wrong program" is a fact
// about the program; "it did not answer in time" and "it printed something
// this profile cannot read" are facts about the asking, and telling a user
// their compiler is incompatible when it merely timed out sends them to
// replace something that was never shown to be wrong.
type FactState string

const (
	// FactCompatible: it named itself, the profile accepts that identity, and
	// its output shows everything the profile requires of it.
	FactCompatible FactState = "compatible"
	// FactIncompatible: it named itself, and it is not what the profile was
	// written for, or its own usage lacks something the profile passes it.
	// The only state that is PROOF of an incompatibility.
	FactIncompatible FactState = "incompatible"
	// FactUnknownOutput: it ran, and nothing it printed names it.
	FactUnknownOutput FactState = "unknown_output"
	// FactTimedOut: it did not finish inside the probe's bound.
	FactTimedOut FactState = "timed_out"
	// FactMissing: there is no such file.
	FactMissing FactState = "missing"
	// FactNotPermitted: the file is there and may not be executed.
	FactNotPermitted FactState = "not_permitted"
	// FactWrongArchitecture: the operating system refused to load it as a
	// program for this machine.
	FactWrongArchitecture FactState = "wrong_architecture"
	// FactFailed: it could not be started or read, for another reason.
	FactFailed FactState = "failed"
)

var factStates = []FactState{FactCompatible, FactIncompatible, FactUnknownOutput, FactTimedOut,
	FactMissing, FactNotPermitted, FactWrongArchitecture, FactFailed}

// ExecutableFact is the recorded answer for one executable of one profile.
//
// It is evidence about ONE file and ONE requirement, and it says so: Path, Size
// and ModifiedAt are what a page can compare without reading the file, SHA256
// is what the executor compares before it starts anything, and Requirement is
// the digest of what the profile required when the question was asked
// ([profile.CompatibilityProbe.Requirement]). A fact that does not match all
// of them is about some other program or some other question, and is ignored.
//
// It grants nothing. Approval is `Grant`, a different member written by a
// different service, and no probe result touches it.
type ExecutableFact struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size,omitempty"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
	SHA256     string    `json:"sha256"`
	// Requirement is the digest of the requirement this answers.
	Requirement string    `json:"requirement"`
	State       FactState `json:"state"`
	// Identity is what the program called itself, when it did.
	Identity string `json:"identity,omitempty"`
	// Missing is the required text its output did not contain.
	Missing []string `json:"missing,omitempty"`
	// Detail is one sentence of why, for the states that need one.
	Detail string `json:"detail,omitempty"`
	// JobID is the supervised job that asked, so the bytes can be read.
	JobID     string    `json:"job_id,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Describes reports whether this fact is about the file at path as it is now
// — by the cheap comparison a page may make on every poll — and about the
// requirement the profile makes now.
func (f ExecutableFact) Describes(path string, size int64, modified time.Time, requirement string) bool {
	return f.Path == path && f.Size == size && f.ModifiedAt.Equal(modified) && f.Requirement == requirement
}

func (f ExecutableFact) validate() error {
	var problems []string
	if !filepath.IsAbs(f.Path) {
		problems = append(problems, fmt.Sprintf("its path %q is not absolute", f.Path))
	}
	for _, digest := range []string{f.SHA256, f.Requirement} {
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
			problems = append(problems, fmt.Sprintf("%q is not sha256:<64 hex digits>", digest))
		}
	}
	known := false
	for _, state := range factStates {
		known = known || state == f.State
	}
	if !known {
		problems = append(problems, fmt.Sprintf("its state is %q, which is not one", f.State))
	}
	if f.CheckedAt.IsZero() {
		problems = append(problems, "it has no checked_at; an answer with no time is a claim with no expiry")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(problems, "; "))
}

func sortedFactKeys(m map[string]ExecutableFact) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
