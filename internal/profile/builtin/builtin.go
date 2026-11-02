// Package builtin holds the profile documents that ship inside the Companion.
//
// # Built in means "arrived with the program", and nothing else
//
// These documents are compiled in with //go:embed, so trusting them is the same
// act as trusting the binary: a user who installed this build has already made
// that decision, which is why [profile.Authorize] does not ask again for a
// built-in profile.
//
// What "built in" does *not* mean is a shortcut. Every document here is read by
// the same decoder, validated by the same rules, canonicalized by the same
// encoder and resolved by the same [profile.Resolve] as a file somebody
// downloads. `TestBuiltinAndUserAuthoredResolveIdentically` exists to keep that
// honest: it resolves a built-in sample and a user-authored equivalent and
// compares the resulting commands. If a privileged path ever appears, that test
// is what fails.
//
// # What "qualified" means here, and it is not one thing
//
// The three toolchains are Auto-Pigeon's own builds of ericw-tools and q3map2
// (HITL, NEW_380: "Offer the maintained Auto-Pigeon builds"), and each is
// qualified by *measurement*: what a program does was established by running it
// rather than read off a manual — which is how the Quake II document came to
// differ from the Quake 1 one in seven places, and the Quake III one from both
// in nine, that a copy-and-edit would have got wrong. Their releases are
// installed by internal/managedtools or unpacked by the user from the same
// GitHub pages.
//
// The upstream documents those measurements started from are not shipped any
// more. They are kept as test fixtures in testdata/retired, and [ReplacementFor]
// is what a binding, a pin or a stage that still names one is told.
//
// The ten engine profiles are qualified by *documentation*. Their command lines
// come from each engine's own published usage and source, and no build of any
// of them has been run by anybody here — eight upstream projects across three
// operating systems, none of which can be started without a copy of Quake or
// Quake II that is not ours to have. That is not a hedge, it is written into
// each document: every platform is `unverified` with a note saying so, and
// where upstream ships nothing the platform is `unsupported` with the reason. A
// profile that claimed `supported` on that evidence would be the
// plausible-looking wrong answer the rest of this repository is careful about.
//
// # Quake II and Quake III are work in progress, and no document here may say otherwise
//
// The Q2 and Q3 documents are shipped and usable. What they are not is
// finished, and that statement is not theirs to make:
// [github.com/auto-pigeon/auto-pigeon-companion/internal/maturity] holds it,
// keyed on AUB's engine family, so a community profile cannot publish itself as
// stable Quake II or Quake III support and switch the warning off.
//
// The instrument that *is* measured is
// [github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture],
// which records the argv it was started with. It proves the Companion builds
// the command line it says it builds. Nothing can make it prove that Ironwail
// accepts that command line.
//
// There was a `sample.q1-toolchain` here, and a `sample.q1-engine`, and both
// were retired rather than kept beside the qualified documents. Two built-in
// tool profiles both providing `q1.bsp.compile` would make "which tool runs
// this step" a question a pipeline resolves by iteration order; and the engine
// sample would be an eighth engine in a list of seven, describing an engine
// that does not exist. `TestNoTwoBuiltinToolsProvideTheSameCapability` is what
// keeps the first half from coming back — and why NEW_380 could drop the idea
// of an "alternative" tool altogether once the forks stood alone.
package builtin

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"sync"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

//go:embed *.json
var files embed.FS

// The ids of the documents this build ships.
//
// Constants rather than string literals scattered through the CLI and the
// tests, because a profile id is a published identity: it appears in a binding,
// in a job record and in a build manifest, and a typo in one of those places is
// a lookup that fails in a way nobody reads as a typo.
const (
	// EricwAutoPigeonQ1 and EricwAutoPigeonQ2 are the Auto-Pigeon fork's build of
	// ericw-tools, as published on its GitHub releases page: Quake 1 and the
	// experimental Quake II. Two documents rather than one, because the two games
	// read different inputs and write different outputs. Since NEW_380 every
	// built-in Quake 1 and Quake II pipeline names one of these on every stage.
	EricwAutoPigeonQ1 = "auto-pigeon.ericw-tools.auto-pigeon-build.q1"
	EricwAutoPigeonQ2 = "auto-pigeon.ericw-tools.auto-pigeon-build.q2"
	// The three Q1 pipelines, which differ only in the options they set.
	Q1FastPreview = "auto-pigeon.q1.fast-preview"
	Q1Normal      = "auto-pigeon.q1.normal"
	Q1Final       = "auto-pigeon.q1.final"
	// Q1LeakTest is not a build: it runs the compile step alone with the leak
	// test on, for the editor's `autopigeon://leaktest/` request. It publishes
	// the point file and the qbsp log and never a playable BSP.
	Q1LeakTest = "auto-pigeon.q1.leak-test"
	// The two Q2 pipelines. Two rather than three: `final` for Quake 1 is
	// `normal` plus 4x supersampling, and there is no measured reason yet to
	// claim a third Quake II preset is a different build rather than a
	// different name for one.
	Q2FastPreview = "auto-pigeon.q2.fast-preview"
	Q2Normal      = "auto-pigeon.q2.normal"
	// Q2LeakTest is the Quake II counterpart of Q1LeakTest (`Q2_010`): qbsp's
	// Quake II compile step alone with the leak test on. It publishes the
	// `.pts` point file and what qbsp printed.
	Q2LeakTest = "auto-pigeon.q2.leak-test"

	// Q3Map2AutoPigeon is the experimental Quake III toolchain: the Auto-Pigeon
	// fork's build of q3map2, as published on its GitHub releases page (NEW_340).
	// One executable with three stage switches, where the two EricW documents
	// describe several programs.
	Q3Map2AutoPigeon = "auto-pigeon.q3map2.auto-pigeon-build"
	// The two Q3 pipelines, for the same reason there are two Q2 ones.
	Q3FastPreview = "auto-pigeon.q3.fast-preview"
	Q3Normal      = "auto-pigeon.q3.normal"
	// Q3LeakTest is Q1LeakTest's counterpart and, like it, not a build: the
	// BSP stage alone with the leak test on. It publishes the `.lin` line file
	// and what Q3Map2 printed (`Q3_018`).
	Q3LeakTest = "auto-pigeon.q3.leak-test"

	// The curated Quake 1 engines. Each is one upstream project, and the id is
	// the name that project calls itself.
	Ironwail         = "auto-pigeon.engine.ironwail"
	VkQuake          = "auto-pigeon.engine.vkquake"
	QuakeSpasm       = "auto-pigeon.engine.quakespasm"
	QuakeSpasmSpiked = "auto-pigeon.engine.quakespasm-spiked"
	DarkPlaces       = "auto-pigeon.engine.darkplaces"
	FTEQW            = "auto-pigeon.engine.fteqw"
	// Q1Generic sends only the switches every id-derived engine documents, for
	// an engine this build has no profile for.
	Q1Generic = "auto-pigeon.engine.q1-generic"

	// The curated Quake II engines.
	//
	// YamagiQuake2 is the reference this build's Quake II path is written
	// against. FTEQWQ2 is FTEQW's Quake II side and declares one action, for
	// the reason its own document gives. Q2Generic is the fallback.
	YamagiQuake2 = "auto-pigeon.engine.yamagi-quake2"
	FTEQWQ2      = "auto-pigeon.engine.fteqw-q2"
	Q2Generic    = "auto-pigeon.engine.q2-generic"

	// The curated Quake III engines.
	//
	// IoQuake3 is the reference this build's Quake III path is written against
	// — it is the engine upstream's own documentation covers, and the one whose
	// dedicated server this build's host actions are written from. Q3Generic is
	// the fallback for any other id Tech 3 engine.
	IoQuake3  = "auto-pigeon.engine.ioquake3"
	Q3Generic = "auto-pigeon.engine.q3-generic"
)

// Q1Engines is every curated Quake 1 engine profile this build ships.
//
// Order is how they are listed, and it is deliberate rather than alphabetical:
// the generic fallback is last because it is the answer when none of the named
// ones is what you have.
var Q1Engines = []string{Ironwail, VkQuake, QuakeSpasm, QuakeSpasmSpiked, DarkPlaces, FTEQW, Q1Generic}

// Q1Pipelines is the three built-in Quake 1 pipelines, quickest first.
var Q1Pipelines = []string{Q1FastPreview, Q1Normal, Q1Final}

// DiagnosticPipelines answer a question about a map rather than build it: one
// step, no playable output. Listed apart so "every build pipeline is compile,
// vis, light" stays a statement about builds.
var DiagnosticPipelines = []string{Q1LeakTest, Q2LeakTest, Q3LeakTest}

// Q2Engines is every curated Quake II engine profile this build ships, in the
// order they are listed. Yamagi first because it is the one the Quake II path
// is written against; the generic fallback last, as for Quake 1.
var Q2Engines = []string{YamagiQuake2, FTEQWQ2, Q2Generic}

// Q2Pipelines is the two built-in Quake II pipelines, quickest first.
var Q2Pipelines = []string{Q2FastPreview, Q2Normal}

// Q3Engines is every curated Quake III engine profile this build ships, in the
// order they are listed. ioquake3 first because it is the one the Quake III
// path is written against; the generic fallback last, as for the other two
// families.
var Q3Engines = []string{IoQuake3, Q3Generic}

// Q3Pipelines is the two built-in Quake III pipelines, quickest first.
var Q3Pipelines = []string{Q3FastPreview, Q3Normal}

// Entry is one built-in document: what it says, what file it came from, and
// the digest of its canonical form.
type Entry struct {
	// File is the embedded file name, for error messages.
	File string
	// Profile is the decoded, validated document.
	Profile profile.Profile
	// Digest is the canonical digest. Recorded here so a binding can be written
	// against it exactly as one is for an imported profile.
	Digest string
}

// Trust is the state every entry here has.
func (e Entry) Trust() profile.Trust { return profile.TrustBuiltin }

// Load decodes every built-in document.
//
// It returns an error rather than panicking at init, and callers report it,
// because a build whose own profiles do not parse should say so where a user
// can see it — not fail to start with a stack trace.
//
// Every document goes through [profile.Decode] and [profile.Digest], the same
// decoder, rules and encoder as a downloaded file, once per process: the
// documents are compiled into the binary, so what that found cannot change
// while it runs. Before Q2_015A every call did all of it again — 27 documents,
// 0.23 s and 43 MB of allocation per call (now 12 ms and 1.9 MB) — and one
// Profiles page or one build resolver called it several times over: it was 66%
// of every CPU sample of `internal/web`'s whole test run on a workstation,
// before garbage collection, and the frame CI's 10-minute timeout caught that
// package in (run 38615467410). Each call still
// returns profiles of its own, decoded afresh from the same bytes into the
// type the full decode chose, so a caller that edits one (a template filled
// into the New profile form) changes nothing any other caller sees.
// `TestEveryLoadIsTheFullDecodeAndOwnsWhatItReturns` holds both halves.
func Load() ([]Entry, error) {
	verified.once.Do(func() { verified.documents, verified.err = verify() })
	if verified.err != nil {
		return nil, verified.err
	}
	entries := make([]Entry, 0, len(verified.documents))
	for _, document := range verified.documents {
		p, err := document.materialize()
		if err != nil {
			return nil, fmt.Errorf("builtin: %s could not be read again: %w", document.file, err)
		}
		entries = append(entries, Entry{File: document.file, Profile: p, Digest: document.digest})
	}
	return entries, nil
}

// verified is what the full decode of the embedded documents found.
var verified struct {
	once      sync.Once
	documents []verifiedDocument
	err       error
}

// verifiedDocument is one embedded document the full decoder accepted.
type verifiedDocument struct {
	file   string
	data   []byte
	kind   reflect.Type // the struct type [profile.Decode] returned a pointer to
	digest string
}

// materialize is the document as a new value of the type the full decode
// chose, read the way that decode's own typed step reads it (unknown members
// refused). The boundary checks and the validation it skips are the ones the
// same bytes already passed.
func (d verifiedDocument) materialize() (profile.Profile, error) {
	target := reflect.New(d.kind).Interface()
	decoder := json.NewDecoder(bytes.NewReader(d.data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	p, ok := target.(profile.Profile)
	if !ok {
		return nil, fmt.Errorf("%s is not a profile type", d.kind)
	}
	return p, nil
}

// verify is the full decode of every embedded document, in file-name order.
func verify() ([]verifiedDocument, error) {
	names, err := fs.Glob(files, "*.json")
	if err != nil {
		return nil, fmt.Errorf("builtin: listing the embedded profiles: %w", err)
	}
	sort.Strings(names)

	documents := make([]verifiedDocument, 0, len(names))
	seen := map[string]string{}
	for _, name := range names {
		data, err := files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("builtin: reading %s: %w", name, err)
		}
		p, err := profile.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("builtin: %s is not a valid profile: %w", name, err)
		}
		digest, err := profile.Digest(p)
		if err != nil {
			return nil, fmt.Errorf("builtin: %s cannot be digested: %w", name, err)
		}
		id := p.Metadata().ID
		if first, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("builtin: %s and %s both declare the id %q", first, name, id)
		}
		seen[id] = name
		kind := reflect.TypeOf(p)
		if kind.Kind() != reflect.Pointer {
			return nil, fmt.Errorf("builtin: %s decoded to %s, not a pointer to a profile", name, kind)
		}
		documents = append(documents, verifiedDocument{file: name, data: data, kind: kind.Elem(), digest: digest})
	}
	return documents, nil
}

// Find returns the built-in profile with an id.
func Find(id string) (Entry, error) {
	entries, err := Load()
	if err != nil {
		return Entry{}, err
	}
	for _, e := range entries {
		if e.Profile.Metadata().ID == id {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("builtin: no built-in profile has the id %q", id)
}

// retired maps every document earlier Companions shipped and this one does not
// to the maintained document that does its job (NEW_380).
//
// The table is the whole of what "migrating" a retired id may mean: it says
// which document a binding's folders and a pin may carry over to. It says nothing
// about programs. A folder somebody bound to ericw-tools 2.0.0-alpha11 is not an
// Auto-Pigeon build because a table says so: the replacement's own binding is
// filled by the managed installer or by the person, and its version check decides.
var retired = map[string]string{
	"auto-pigeon.ericw-tools.q1":                 EricwAutoPigeonQ1,
	"auto-pigeon.ericw-tools.q2":                 EricwAutoPigeonQ2,
	"auto-pigeon.q3map2":                         Q3Map2AutoPigeon,
	"auto-pigeon.q3.leak-test.auto-pigeon-build": Q3LeakTest,
}

// ReplacementFor is the maintained document a retired built-in id was replaced
// by. false for every id this build does not know as retired, which includes
// everything a user wrote.
func ReplacementFor(id string) (string, bool) {
	replacement, ok := retired[id]
	return replacement, ok
}

// Current is the profile a stored reference means today: its replacement when
// it names a retired document, unless something installed still answers to that
// id — then it is a person's own profile, and the reference is theirs. This is
// how a leak-test pin made before NEW_380 keeps working without being rewritten.
func Current(id string, installed func(string) bool) string {
	if replacement, ok := retired[id]; ok && !installed(id) {
		return replacement
	}
	return id
}

// Retired is every retired id, sorted.
func Retired() []string {
	ids := make([]string, 0, len(retired))
	for id := range retired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
