// Package maturity says how finished a game family's support is, in one place,
// in words a user reads.
//
// # Why this is code and not a member of a profile document
//
// A profile document is written by whoever publishes it. If "is Quake II
// finished?" were a member of that document, the answer would be whatever the
// publisher typed — and the first community Q2 toolchain to declare itself
// stable would be a community document switching off a warning this build
// stands behind. `AUP/AUCOM 215` requires the opposite: the badge appears
// "in every AUP location where Q2 can be selected, loaded, edited, exported or
// compiled", which is only true if nothing can opt out of it.
//
// So the statement is keyed on AUB's `engine_family` — the one field a profile
// carries that AUB owns and that AUP already reads — and this build answers it.
// A document may say which family it is for; it may not say what this program
// thinks of that family.
//
// # One sentence, and it is the same sentence everywhere
//
// [Quake2Message] is quoted verbatim from the prompt that required it, and
// AUP carries a byte-identical copy in `frontend/src/services/gameMaturity.ts`.
// Neither repository can read the other's tree at test time, so both pin
// [MessageDigest] — the same arrangement `internal/profile`'s portability
// corpus already uses. Changing the wording is a change in two repositories, in
// one task, or in neither.
//
// # What it deliberately does not do
//
// It does not gate anything. A work-in-progress family is fully usable: the
// compiler runs, the package is written, the engine starts. What this package
// produces is a sentence, because the honest failure mode of an unfinished path
// is a user who does not know it is unfinished, not a user who was allowed to
// try.
package maturity

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// State is how finished this build's support for a game family is.
//
// Three states, and the third is not a synonym for either of the others.
// `Undeclared` means this build has nothing to say — which is the right answer
// for a family it ships no profiles for, and the wrong answer to dress up as
// "stable".
type State string

const (
	// Stable: the path is qualified and this build stands behind it.
	Stable State = "stable"
	// WorkInProgress: usable, incomplete, and the user is told so every time.
	WorkInProgress State = "work_in_progress"
	// Undeclared: this build makes no claim about the family.
	Undeclared State = "undeclared"
)

// Quake2Message is the exact sentence `AUP/AUCOM 215` requires, and the reason
// this package exists.
//
// It is one string rather than a badge and a body concatenated at each call
// site, because five surfaces joining two fragments with their own separator is
// five slightly different sentences.
const Quake2Message = "Quake II — Work in progress. Core editing is available, " +
	"but some textures, entities, compilation and engine workflows may be incomplete."

// MessageDigest is the SHA-256 of [Quake2Message], pinned so that AUP's copy
// and this one cannot drift apart unnoticed. See the package comment.
const MessageDigest = "9c560b2be9cd7ea0057a67d1d82f57ac7b066f393fc000551bfdf1b60c291ccf"

// Badge is the short label a chip shows. The long form is [Statement.Message].
const workInProgressBadge = "Work in progress"

// Statement is everything a surface needs to render the claim: nothing is
// computed at the call site, so two surfaces cannot render it differently.
type Statement struct {
	// Family is AUB's engine family this statement is about.
	Family string
	State  State
	// Badge is the chip text. Empty when [State] is [Stable] or [Undeclared] —
	// a stable path gets no chip, because a badge on everything is a badge
	// nobody reads.
	Badge string
	// Message is the full sentence. Empty for anything but [WorkInProgress].
	Message string
	// FeedbackInvited reports whether this family's surfaces should offer
	// `Report compatibility issue`. It travels with the statement rather than
	// being re-derived, because "show the warning" and "offer the report" are
	// the same decision and splitting them is how one of them gets forgotten.
	FeedbackInvited bool
}

// WorkInProgress reports the state without the rest of the statement.
func (s Statement) IsWorkInProgress() bool { return s.State == WorkInProgress }

// statements is the closed table. A family absent from it is [Undeclared],
// which is why `quake3` is not listed: this build ships no Quake III profile,
// and a "stable" claim about a path with nothing on it would be a claim about
// nothing.
var statements = map[string]Statement{
	"quake1": {Family: "quake1", State: Stable},
	"quake2": {
		Family:          "quake2",
		State:           WorkInProgress,
		Badge:           workInProgressBadge,
		Message:         Quake2Message,
		FeedbackInvited: true,
	},
}

// Of returns what this build says about a game family.
//
// An unknown or empty family is [Undeclared] rather than an error: a tool
// profile that operates on a file format rather than on a game declares no
// family at all, and that is legitimate — see [profile.GameProfileRef.Empty].
func Of(engineFamily string) Statement {
	family := strings.ToLower(strings.TrimSpace(engineFamily))
	if s, known := statements[family]; known {
		return s
	}
	return Statement{Family: family, State: Undeclared}
}

// IsWorkInProgress is the predicate form, for a caller that only branches.
func IsWorkInProgress(engineFamily string) bool { return Of(engineFamily).IsWorkInProgress() }

// Families lists every family this build has a statement for, sorted, so a
// test or a `--help` can enumerate them without reaching into the table.
func Families() []string {
	names := make([]string, 0, len(statements))
	for name := range statements {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DigestOf is what the pinning test computes. Exported so AUP's mirror, a
// documentation check or a future second consumer measures the same way.
func DigestOf(message string) string {
	sum := sha256.Sum256([]byte(message))
	return hex.EncodeToString(sum[:])
}
