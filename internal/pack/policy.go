package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Asset safety: what this policy is, and what it deliberately is not.
//
// # The problem
//
// The files that go into a Quake package come from several places at once. Some
// were compiled by a build this program supervised. Some are the author's own
// textures and sounds. And some, on a machine where somebody has been playing
// the game, are id Software's — because the working directory for making a
// Quake map is very often the directory the game is installed in, and a
// `--from .` that sweeps it up will happily package `gfx/palette.lmp` next to
// the map that needed it.
//
// # What would be easy, and why it is not done
//
// The easy version is a list of filenames. `pak0.pak`, `progs.dat`,
// `gfx/palette.lmp`, and so on. It is easy, it is what most tools do, and it is
// wrong in both directions: it refuses a user's own `progs.dat` from a total
// conversion they wrote themselves, and it passes id's `e1m1.bsp` the moment
// somebody renames it. Worse than either, it *implies a legal conclusion* — "we
// checked, this is clean" — that a filename cannot support and this program has
// no standing to make.
//
// # What is done instead
//
// Every candidate is classified by **where its bytes came from**, and the
// classification is ordered by how strong the evidence is. A file whose digest
// a build manifest vouches for was made here. A file selected out of a
// directory that holds an installed game is, on that evidence alone, something
// a person should look at before publishing it. A file about which nothing is
// known is **not refused and not included** — it is held for review, which is
// the honest thing to do with an unanswered question.
//
// Filenames still appear, as [Decision.Hints]. They explain a decision that was
// already made on other grounds, and each one says in its own text that it
// decided nothing.
//
// # The one exact rule
//
// [AssetCorpus] identifies a specific released file by SHA-256. That is exact:
// a digest is either the digest of id's `pak0.pak` or it is not, and renaming
// the file does not change it. The built-in corpus ships **empty**, and
// [BuiltinCorpus] explains why at the point where somebody will want to know.

// Provenance is what is known about where a candidate's bytes came from.
type Provenance string

const (
	// ProvenanceBuilt: a build manifest lists this exact content, by digest.
	ProvenanceBuilt Provenance = "built"
	// ProvenanceAuthored: it was selected from a directory the user declared as
	// their own content.
	ProvenanceAuthored Provenance = "authored"
	// ProvenanceDeclared: the user asserted a right to distribute it.
	ProvenanceDeclared Provenance = "declared"
	// ProvenanceGameContent: it was selected out of an installed game's content
	// directory, which says where it was found and not who wrote it.
	ProvenanceGameContent Provenance = "game_content"
	// ProvenanceKnownAsset: its digest is a released commercial file's.
	ProvenanceKnownAsset Provenance = "known_asset"
	// ProvenanceUnknown: nothing above applies, and that is a fact rather than
	// a failure.
	ProvenanceUnknown Provenance = "unknown"
)

// Verdict is what the policy decided to do about a candidate.
type Verdict string

const (
	// Include: the evidence is sufficient and it is packaged.
	Include Verdict = "include"
	// Review: a person has to look at it before it is packaged. Not an error —
	// the normal outcome for a file this program has no information about.
	Review Verdict = "review"
	// Refuse: it is packaged only on an explicit, recorded authorization.
	Refuse Verdict = "refuse"
)

// Decision is one candidate and what was decided about it. It is what a preview
// prints, and what the sidecar manifest records.
type Decision struct {
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`

	Provenance Provenance `json:"provenance"`
	Verdict    Verdict    `json:"verdict"`
	// Rule names the rule that decided, so a person disagreeing with a verdict
	// knows which sentence to argue with.
	Rule string `json:"rule"`
	// Reason is that sentence.
	Reason string `json:"reason"`
	// Hints are observations that did not decide anything and are printed
	// anyway, because a person reviewing a listing is better at this than a
	// program is.
	Hints []string `json:"hints,omitempty"`

	// Acknowledged and Authorized record how a review verdict was resolved.
	Acknowledged bool `json:"acknowledged,omitempty"`
	Authorized   bool `json:"authorized,omitempty"`
	// Resolution is the reason the person gave.
	Resolution string `json:"resolution,omitempty"`
}

// Packaged reports a decision that ends with the file in the archive.
func (d Decision) Packaged() bool {
	return d.Verdict == Include || d.Acknowledged || d.Authorized
}

// KnownAsset is one released file, identified by content.
type KnownAsset struct {
	// SHA256 is the digest, in this program's usual `sha256:` spelling.
	SHA256 string `json:"sha256"`
	// Release names what this file is part of, in words a person recognises:
	// "Quake 1.06 registered, id1/pak1.pak".
	Release string `json:"release"`
	Note    string `json:"note,omitempty"`
}

// AssetCorpusSchemaVersion versions the corpus file.
const AssetCorpusSchemaVersion = "aucom.known-assets/1.0"

// AssetCorpus is a set of known released files.
type AssetCorpus struct {
	SchemaVersion string `json:"schema_version"`
	// Source says where this list came from and who stands behind it. Required,
	// because a digest list with no provenance of its own is exactly the kind
	// of unaccountable blacklist this policy exists to avoid being.
	Source string       `json:"source"`
	Assets []KnownAsset `json:"assets"`
}

// BuiltinCorpus is the corpus this build ships with, and it is empty.
//
// # Why it is empty, and why that is the correct content
//
// A digest list is only useful if it is right, and it is only right if somebody
// computed it from the actual released files. This repository has not, and a
// list of plausible-looking hashes copied from a forum post would be worse than
// no list at all: it would produce confident refusals of files it had never
// seen, and confident silence about the ones it got wrong. A wrong digest here
// is not a near miss — it is a rule that never fires, wearing the costume of
// one that does.
//
// The protection a user actually gets does not come from this list. It comes
// from the rules that need no corpus: content selected out of a game
// installation is held for review, and content nothing is known about is held
// for review. Those cover the case this policy exists for — the sweep of a
// working directory that is also a game directory — and they cover it without
// claiming to have identified anything.
//
// Supply a real corpus with `--known-assets`, or put one at `known-assets.json`
// in the configuration directory. [LoadAssetCorpus] documents the format.
func BuiltinCorpus() *AssetCorpus {
	return &AssetCorpus{
		SchemaVersion: AssetCorpusSchemaVersion,
		Source:        "built in: empty, deliberately — see pack.BuiltinCorpus",
	}
}

// LoadAssetCorpus reads a corpus file:
//
//	{
//	  "schema_version": "aucom.known-assets/1.0",
//	  "source": "digests computed from a retail Quake CD, 2026-09-06, by <who>",
//	  "assets": [
//	    {"sha256": "sha256:…", "release": "Quake 1.06 registered, id1/pak1.pak"}
//	  ]
//	}
func LoadAssetCorpus(path string) (*AssetCorpus, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pack: reading %s: %w", path, err)
	}
	var corpus AssetCorpus
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		return nil, fmt.Errorf("pack: %s is not a readable known-asset list: %w", path, err)
	}
	if corpus.SchemaVersion != AssetCorpusSchemaVersion {
		return nil, fmt.Errorf("pack: %s is %q; this build reads %q", path, corpus.SchemaVersion, AssetCorpusSchemaVersion)
	}
	if strings.TrimSpace(corpus.Source) == "" {
		return nil, fmt.Errorf("pack: %s has no `source`; a list of digests that does not say who computed it "+
			"cannot be argued with, and this policy refuses to act on one", path)
	}
	for i, asset := range corpus.Assets {
		if !strings.HasPrefix(asset.SHA256, "sha256:") || len(asset.SHA256) != len("sha256:")+64 {
			return nil, fmt.Errorf("pack: %s asset %d has %q, which is not a `sha256:`-prefixed digest", path, i, asset.SHA256)
		}
		if strings.TrimSpace(asset.Release) == "" {
			return nil, fmt.Errorf("pack: %s asset %d does not say what release it belongs to", path, i)
		}
	}
	return &corpus, nil
}

// Lookup finds a digest in the corpus.
func (c *AssetCorpus) Lookup(digest string) (KnownAsset, bool) {
	if c == nil {
		return KnownAsset{}, false
	}
	for _, asset := range c.Assets {
		if strings.EqualFold(asset.SHA256, digest) {
			return asset, true
		}
	}
	return KnownAsset{}, false
}

// Policy holds the evidence the rules are evaluated against.
type Policy struct {
	// KnownAssets is the exact-identification corpus. Nil means the built-in
	// one, which is empty.
	KnownAssets *AssetCorpus
	// GameRoots are directories that hold an installed game's content — a
	// launch configuration's game root, or a directory the user named. A
	// candidate found inside one is held for review.
	GameRoots []string
	// AuthoredRoots are directories the user declared as their own work.
	AuthoredRoots []string
	// BuildOutputs maps a content digest to the build output that has it, so a
	// compiled file is recognised by what it is rather than by where it sits.
	BuildOutputs map[string]string
	// Authorizations are archive paths a person authorized, with the reason.
	Authorizations map[string]string
	// Acknowledgements are archive paths a person reviewed and accepted.
	Acknowledgements map[string]bool
}

// Candidate is one file offered for packaging, before any decision.
type Candidate struct {
	// Path is the member path it would occupy.
	Path string
	// Source is where it was read from on this machine, absolute.
	Source string
	Size   int64
	SHA256 string
	// FromBuild names the build output it came from, when the caller selected
	// it that way. Evidence in its own right, independent of the digest match,
	// and both are recorded.
	FromBuild string
}

// Decide classifies one candidate.
//
// The rules are evaluated in order and the first match wins. The order is by
// strength of evidence, not by severity, and that is a deliberate choice with a
// consequence worth naming: a file that a build manifest vouches for is
// included even if it sits inside a game directory, because "this program
// compiled these exact bytes" is a stronger statement than "this file was found
// in that folder". Building straight into `id1/maps/` is a normal thing to do
// and it should not turn the author's own output into a suspect.
//
// The one rule that outranks it is the corpus, because content identity beats
// everything: if a file's digest is a released commercial file's, then whatever
// else is true, it did not become the author's by being copied into a build
// directory.
func (p Policy) Decide(candidate Candidate) Decision {
	decision := Decision{
		Path:   candidate.Path,
		Source: candidate.Source,
		Size:   candidate.Size,
		SHA256: candidate.SHA256,
		Hints:  hintsFor(candidate),
	}

	// 1. Content identity.
	//
	// A match makes the provenance certain, and it is recorded as such whether
	// or not the file ends up packaged. An authorization overrides the verdict
	// and does not overwrite the finding: a manifest that said only "the user
	// authorized this" would have lost the sentence that matters, which is
	// *what* they authorized.
	if asset, found := p.corpus().Lookup(candidate.SHA256); found {
		decision.Provenance = ProvenanceKnownAsset
		identity := fmt.Sprintf("its content is byte-for-byte %s", asset.Release)
		if asset.Note != "" {
			identity += " (" + asset.Note + ")"
		}
		if reason, authorized := p.Authorizations[candidate.Path]; authorized {
			decision.Verdict = Include
			decision.Rule = "authorized-known-asset"
			decision.Reason = identity + ", and you asserted the right to distribute it: " + reason
			decision.Authorized = true
			decision.Resolution = reason
			return decision
		}
		decision.Verdict = Refuse
		decision.Rule = "known-asset-digest"
		decision.Reason = identity + ". Packaging it redistributes that file; " +
			"if you hold the right to, authorize it explicitly and say so"
		return decision
	}

	// 2. An explicit authorization for a file the corpus did not identify.
	if reason, ok := p.Authorizations[candidate.Path]; ok {
		decision.Provenance = ProvenanceDeclared
		decision.Verdict = Include
		decision.Rule = "authorized"
		decision.Reason = "you asserted the right to distribute it: " + reason
		decision.Authorized = true
		decision.Resolution = reason
		return decision
	}

	// 3. A build produced these exact bytes.
	if source, ok := p.BuildOutputs[candidate.SHA256]; ok {
		decision.Provenance = ProvenanceBuilt
		decision.Verdict = Include
		decision.Rule = "build-output"
		decision.Reason = "a build manifest records this exact content as " + source
		return decision
	}
	if candidate.FromBuild != "" {
		decision.Provenance = ProvenanceBuilt
		decision.Verdict = Include
		decision.Rule = "build-output"
		decision.Reason = "it was selected from this build's output " + candidate.FromBuild
		return decision
	}

	// 4. Selected out of an installed game.
	if root, ok := containingRoot(p.GameRoots, candidate.Source); ok {
		decision.Provenance = ProvenanceGameContent
		decision.Verdict = Review
		decision.Rule = "game-content-root"
		decision.Reason = fmt.Sprintf("it was selected out of %s, which is an installed game's content directory. "+
			"That says where it was found, not who wrote it — your own mod lives there too — so it needs a look before it ships", root)
		return decision
	}

	// 5. The user's own tree.
	if root, ok := containingRoot(p.AuthoredRoots, candidate.Source); ok {
		decision.Provenance = ProvenanceAuthored
		decision.Verdict = Include
		decision.Rule = "authored-root"
		decision.Reason = "it came from " + root + ", which you declared as your own content"
		return decision
	}

	// 6. Nothing is known.
	decision.Provenance = ProvenanceUnknown
	decision.Verdict = Review
	decision.Rule = "unknown-provenance"
	decision.Reason = "nothing here says where this came from: it is not a build output, " +
		"and it is not under a directory you declared as your own"
	return decision
}

func (p Policy) corpus() *AssetCorpus {
	if p.KnownAssets != nil {
		return p.KnownAssets
	}
	return BuiltinCorpus()
}

// containingRoot reports the first declared root a path lies inside.
func containingRoot(roots []string, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	cleaned := filepath.Clean(path)
	for _, root := range roots {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if cleaned == root {
			return root, true
		}
		relative, err := filepath.Rel(root, cleaned)
		if err != nil {
			continue
		}
		if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return root, true
		}
	}
	return "", false
}

// baseGameDirectories are the content directory names the three games use.
// They appear in hints and nowhere else — see the note on [Decision.Hints].
var baseGameDirectories = map[string]string{
	"id1":    "Quake",
	"baseq2": "Quake II",
	"baseq3": "Quake III Arena",
}

// hintsFor produces the observations a reviewer might otherwise have to make
// themselves.
//
// Every string it returns ends by saying what it is not. That is not padding:
// the failure mode this whole policy is built against is a tool that shows a
// filename match and lets the reader hear "we checked", so a hint that does not
// disclaim itself is a hint that will be misread.
func hintsFor(candidate Candidate) []string {
	var hints []string
	base := strings.ToLower(pathBase(candidate.Path))
	if looksLikeIDArchive(base) {
		hints = append(hints, fmt.Sprintf(
			"%q is the naming id Software used for the archives it shipped; that is a name, and it decides nothing", base))
	}
	seen := map[string]bool{}
	for _, source := range []string{candidate.Path, filepath.ToSlash(candidate.Source)} {
		for _, element := range strings.Split(source, "/") {
			game, known := baseGameDirectories[strings.ToLower(element)]
			if !known || seen[element] {
				continue
			}
			seen[element] = true
			hints = append(hints, fmt.Sprintf(
				"a path element is %q, which is %s's content directory; a directory name is not evidence about a file's author", element, game))
		}
	}
	return hints
}

// looksLikeIDArchive matches `pak<digit>.pak` and `pak<digit>.pk3`.
func looksLikeIDArchive(base string) bool {
	for _, suffix := range []string{".pak", ".pk3"} {
		if stem, ok := strings.CutSuffix(base, suffix); ok {
			if digit, isPak := strings.CutPrefix(stem, "pak"); isPak && len(digit) == 1 && digit[0] >= '0' && digit[0] <= '9' {
				return true
			}
		}
	}
	return false
}

func pathBase(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// sortDecisions puts a listing in the order a person reads it: the ones needing
// attention first, then everything else by path.
//
// A preview that buries the two files needing review under four hundred that do
// not is a preview nobody reads to the end, and the whole mechanism depends on
// somebody reading it.
func sortDecisions(decisions []Decision) {
	rank := func(d Decision) int {
		switch {
		case d.Verdict == Refuse && !d.Authorized:
			return 0
		case d.Verdict == Review && !d.Acknowledged:
			return 1
		case d.Provenance == ProvenanceGameContent || d.Provenance == ProvenanceKnownAsset:
			return 2
		case d.Provenance == ProvenanceUnknown:
			return 3
		}
		return 4
	}
	sort.SliceStable(decisions, func(i, j int) bool {
		if a, b := rank(decisions[i]), rank(decisions[j]); a != b {
			return a < b
		}
		return decisions[i].Path < decisions[j].Path
	})
}
