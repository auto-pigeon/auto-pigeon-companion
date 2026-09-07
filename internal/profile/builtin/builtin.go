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
// All three toolchains and all seven pipelines are qualified by *measurement*.
// For Q1 the version is the one AUT pinned; for Q2 it is the 2.x pre-release
// that was unpacked and run here against a synthetic Quake II map; for Q3 it is
// Q3Map2 2.5.17n out of NetRadiant-custom's `20260114` release, run here
// against a synthetic Quake III map. What each program does was established by
// running it rather than read off a manual — which is how the Q2 document came
// to differ from the Q1 one in seven places, and the Q3 one from both in nine,
// that a copy-and-edit would have got wrong.
//
// The EricW archives are pinned by digest in the signed catalogue. Q3Map2's is
// not, and that is a difference in the *artifact* rather than in the standard:
// upstream publishes it only inside a bundle of the NetRadiant editor, in a
// container this program does not unpack, and `AUP/AUCOM 216` says not to
// install an editor to get at a compiler. So that document declares no managed
// download and says why, which is the honest version of "unavailable".
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
// [github.com/andrea-dintino/auto-pigeon-companion/internal/maturity] holds it,
// keyed on AUB's engine family, so a community profile cannot publish itself as
// stable Quake II or Quake III support and switch the warning off.
//
// The instrument that *is* measured is
// [github.com/andrea-dintino/auto-pigeon-companion/internal/enginefixture],
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
// keeps the first half from coming back.
package builtin

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
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
	// EricwQ1 is the qualified Quake 1 toolchain: ericw-tools 0.18.1.
	EricwQ1 = "auto-pigeon.ericw-tools.q1"
	// EricwQ2 is the experimental Quake II toolchain: ericw-tools 2.0.0-alpha7.
	//
	// A second document rather than a second version of the first, because the
	// two describe different programs with different archive layouts, different
	// log names and different outputs, and because the Q1 line must not be
	// moved onto a pre-release to gain a Q2 mode.
	EricwQ2 = "auto-pigeon.ericw-tools.q2"
	// The three Q1 pipelines, which differ only in the options they set.
	Q1FastPreview = "auto-pigeon.q1.fast-preview"
	Q1Normal      = "auto-pigeon.q1.normal"
	Q1Final       = "auto-pigeon.q1.final"
	// The two Q2 pipelines. Two rather than three: `final` for Quake 1 is
	// `normal` plus 4x supersampling, and there is no measured reason yet to
	// claim a third Quake II preset is a different build rather than a
	// different name for one.
	Q2FastPreview = "auto-pigeon.q2.fast-preview"
	Q2Normal      = "auto-pigeon.q2.normal"

	// Q3Map2 is the experimental Quake III toolchain: Q3Map2 2.5.17n, from
	// NetRadiant-custom's `20260114` release.
	//
	// One executable with three stage switches, where the two EricW documents
	// describe several programs — and the only toolchain here with no managed
	// download, because upstream ships it inside a map editor and this program
	// does not install one to get at it. See the document's own acquisition
	// note.
	Q3Map2 = "auto-pigeon.q3map2"
	// The two Q3 pipelines, for the same reason there are two Q2 ones.
	Q3FastPreview = "auto-pigeon.q3.fast-preview"
	Q3Normal      = "auto-pigeon.q3.normal"

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
func Load() ([]Entry, error) {
	names, err := fs.Glob(files, "*.json")
	if err != nil {
		return nil, fmt.Errorf("builtin: listing the embedded profiles: %w", err)
	}
	sort.Strings(names)

	entries := make([]Entry, 0, len(names))
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
		entries = append(entries, Entry{File: name, Profile: p, Digest: digest})
	}
	return entries, nil
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
