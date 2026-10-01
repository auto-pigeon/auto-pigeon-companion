// Package q3pack turns one finished Quake III build into one PK3 that carries
// the compiled map and only the files somebody said they may redistribute.
//
// # What it is for
//
// A Quake III engine resolves every name a map uses inside `baseq3` (or a mod
// directory) and the PK3 archives it finds there. A map that was built against
// a folder of textures, a bound package and the base game therefore runs on the
// author's machine and nowhere else until its dependencies travel with it — and
// the obvious way to make them travel, zipping up the folder the compiler read,
// republishes the base game and every third-party pack that happened to be in
// it.
//
// So this package does three separate things and keeps them separate:
//
//  1. It works out what the map NEEDS, twice: what the compiler looked for
//     (read from the map source) and what an engine will look for (read from
//     the compiled BSP). `internal/q3deps` says why those differ.
//  2. It decides what may be PACKAGED. A file from the user's content is
//     packaged only under a grant — "this is my own work", or "I hold this
//     licence" — given for the archive, the folder or the one file it came
//     from. Nothing is inferred from a name, a path or the fact that a public
//     repository contains the file. The base game is never packaged.
//  3. It WRITES through `internal/pack`, which is the one bounded,
//     deterministic PK3 writer this program has: byte-ordered members, fixed
//     timestamps, no case-colliding paths, no overwrite. There is no second
//     ZIP writer here and there must not be one.
//
// # What a plan says
//
// [Plan] is the review a person reads before anything is written: every member
// in archive order with its digest and the grant that let it in, every
// dependency with whether the compile and the engine need it and what became of
// it, and every reason the archive would not be written as it stands. A
// dependency an engine needs and the archive does not carry is a reason — a
// compiler that exited 0 is not evidence that a package is complete.
package q3pack

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3deps"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// PlanSchemaVersion versions [Plan].
const PlanSchemaVersion = "aucom.q3-package-plan/1.0"

// FamilyQuake3 is the engine family a build must name to be packaged here.
const FamilyQuake3 = "quake3"

// TargetID is the `internal/pack` target every archive here is written under.
const TargetID = "quake3-pk3"

// ErrNotQuake3 reports a build of another game. Quake 1 and Quake II packaging
// is `companion package create`, unchanged.
var ErrNotQuake3 = errors.New("q3pack: this is not a Quake III build")

// Basis is the ground a grant stands on.
type Basis string

const (
	// BasisOwnWork: the person made it.
	BasisOwnWork Basis = "own_work"
	// BasisLicensed: somebody else made it and its licence permits
	// redistribution. The licence is named, and recorded.
	BasisLicensed Basis = "licensed"
	// BasisNotRedistributable: the person says it is not theirs to ship. It is
	// a decision like the other two, recorded like the other two, and it is
	// what tells "nobody has answered" apart from "the answer is no".
	BasisNotRedistributable Basis = "not_redistributable"
)

// Grant is one answer to "may this be redistributed", for one archive, for the
// loose files of the content folder, or for one file. Exactly one of Archive,
// Loose and Path is set. A grant for a file outranks the grant for the archive
// or folder it is in.
type Grant struct {
	// Archive is the SHA-256 of a PK3 the build read, in hex. A digest and not
	// a name: two archives can share a name, and a grant must not move from one
	// to the other because one of them was renamed.
	Archive string `json:"archive,omitempty"`
	// Loose covers the files that are not in any archive.
	Loose bool `json:"loose,omitempty"`
	// Path is one file, by the path an engine looks it up under.
	Path string `json:"path,omitempty"`

	Basis Basis `json:"basis"`
	// Licence names the licence, for BasisLicensed: an SPDX identifier or the
	// licence's own title.
	Licence string `json:"licence,omitempty"`
	// Statement is whatever else the person wants recorded, verbatim.
	Statement string `json:"statement,omitempty"`
}

// Disposition is what became of a dependency, or of one of its files.
type Disposition string

const (
	// BuildOutput: this build compiled it. The map itself.
	BuildOutput Disposition = "build_output"
	// UserAuthored: packaged, under an own-work grant.
	UserAuthored Disposition = "user_authored"
	// Licensed: packaged, under a licence the person named.
	Licensed Disposition = "licensed"
	// BaseGame: the installed game has it. Never packaged, and not a problem
	// for somebody who has the same game.
	BaseGame Disposition = "base_game"
	// ThirdPartyUnresolved: it is in the content the build read and nobody has
	// said whether it may be redistributed. Not packaged.
	ThirdPartyUnresolved Disposition = "third_party_unresolved"
	// Blocked: it will not be packaged, and the reason is stated.
	Blocked Disposition = "blocked"
	// Missing: nothing the build read has it.
	Missing Disposition = "missing"
	// CompileOnly: only the editor and the compiler read it, so a package
	// without it is complete.
	CompileOnly Disposition = "compile_only"
)

// rank orders dispositions worst first, so the thing a person has to act on is
// the thing they read first.
var rank = map[Disposition]int{
	Missing: 0, Blocked: 1, ThirdPartyUnresolved: 2,
	Licensed: 3, UserAuthored: 4, BuildOutput: 5, BaseGame: 6, CompileOnly: 7,
}

// carried reports a disposition that ends with the file in the archive.
func (d Disposition) carried() bool { return d == UserAuthored || d == Licensed || d == BuildOutput }

// unmet reports a disposition that leaves an engine without the file.
func (d Disposition) unmet() bool {
	return d == Missing || d == Blocked || d == ThirdPartyUnresolved
}

// Member is one file of the archive, in the order the archive stores it.
type Member struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// Disposition is why it is in: this build's output, or a grant.
	Disposition Disposition `json:"disposition"`
	// Source is the id of the [Source] it came from; empty for the map.
	Source string `json:"source,omitempty"`
	// Licence and Statement are the grant's, repeated on the member so the
	// listing can be read without cross-referencing.
	Licence   string `json:"licence,omitempty"`
	Statement string `json:"statement,omitempty"`
	// From is where it was read, in words.
	From string `json:"from"`
}

// Source is one place the build's content came from: an archive, or the loose
// files of the content folder. It is the unit a person grants rights for.
type Source struct {
	// ID is `archive:<sha256>` or `loose`.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Name is the archive's file name, as the build staged it.
	Name   string `json:"name,omitempty"`
	Game   string `json:"game,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
	// Origin is `account` or `cache` for a package the saved map is bound to,
	// and empty for something in the content folder.
	Origin string `json:"origin,omitempty"`
	// RuntimeFiles is how many files an engine needs from here.
	RuntimeFiles int `json:"runtime_files"`
	// Grant is the answer given for this source, when one was.
	Grant *Grant `json:"grant,omitempty"`
	// Hints are observations that decide nothing: the name id Software used for
	// its own archives. Each says so itself.
	Hints []string `json:"hints,omitempty"`
}

// File is one file a dependency needs.
type File struct {
	// Path is what an engine looks up, lower-cased.
	Path string `json:"path"`
	Role string `json:"role"`
	// Compile and Runtime say which program reads it.
	Compile bool `json:"required_at_compile"`
	Runtime bool `json:"required_at_runtime"`
	// Disposition is what became of it.
	Disposition Disposition `json:"disposition"`
	// Source is the [Source] it was found in.
	Source string `json:"source,omitempty"`
	// Member is the path it is packaged under, in the capitalisation of the
	// file it was read from. Empty when it is not packaged.
	Member string `json:"member,omitempty"`
	// Reason says why, when the disposition is one a person has to act on.
	Reason string `json:"reason,omitempty"`
}

// Dependency is one thing the map names and what became of it.
type Dependency struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// From says where the name was read: a face of the map source, a surface of
	// the compiled map, an entity key.
	From string `json:"from"`
	// RequiredAtCompile: the compiler looks for it. RequiredAtRuntime: an
	// engine does. Both are read from the files themselves — the map source and
	// the BSP — and not derived from each other.
	RequiredAtCompile bool `json:"required_at_compile"`
	RequiredAtRuntime bool `json:"required_at_runtime"`
	// Disposition is the worst of its files that an engine needs, or
	// `compile_only` when an engine needs none of them.
	Disposition Disposition `json:"disposition"`
	Files       []File      `json:"files,omitempty"`
	Note        string      `json:"note,omitempty"`
}

// Problem is one reason the archive would not be written as it stands.
type Problem struct {
	// Code is one of the Problem* tokens.
	Code string `json:"code"`
	// Subject is the path, the dependency or the source it is about.
	Subject string `json:"subject,omitempty"`
	Message string `json:"message"`
	// Acceptable marks a problem a person may accept in writing — a dependency
	// the archive will not carry. A collision or an unsafe path is not one.
	Acceptable bool `json:"acceptable"`
}

// The problem codes.
const (
	ProblemMissing    = "runtime_dependency_missing"
	ProblemUnresolved = "rights_unresolved"
	ProblemBlocked    = "rights_blocked"
	ProblemDuplicate  = "duplicate_path"
	ProblemUnsafePath = "unsafe_path"
	ProblemPack       = "package_refused"
)

// MapIdentity is which map this is.
type MapIdentity struct {
	Name string `json:"name"`
	// AssetID, RevisionID and Revision identify a saved map on the account.
	// Empty for a map built from a file.
	AssetID     string `json:"asset_id,omitempty"`
	RevisionID  string `json:"revision_id,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	// SourceSHA256 is the digest of the map source the build compiled.
	SourceSHA256 string `json:"source_sha256,omitempty"`
}

// Plan is what would be packaged, and why.
type Plan struct {
	SchemaVersion string      `json:"schema_version"`
	BuildID       string      `json:"build_id"`
	Map           MapIdentity `json:"map"`
	// ArchiveName is `auto-pigeon-<map id>-<revision>.pk3`.
	ArchiveName string `json:"archive_name"`
	// BaseGame and FSGame are the game folders the build read; GameDir is the
	// one the archive is meant for — the mod when the build named one, the base
	// game otherwise.
	BaseGame string `json:"base_game"`
	FSGame   string `json:"fs_game,omitempty"`
	GameDir  string `json:"game_dir"`

	// Members is the archive, in the order it is written: byte-wise by path.
	Members    []Member `json:"members"`
	TotalBytes int64    `json:"total_bytes"`
	// Reproducibility is the promise the writer makes about these bytes.
	Reproducibility string `json:"reproducibility"`

	Sources      []Source     `json:"sources"`
	Dependencies []Dependency `json:"dependencies"`
	// Counts is how many dependencies ended in each disposition.
	Counts map[Disposition]int `json:"counts"`
	// NotCarried are the files an engine needs that the archive will not have
	// and the base game does not supply.
	NotCarried []string `json:"not_carried,omitempty"`

	Problems []Problem `json:"problems,omitempty"`
	// Limits are the sentences about what this review did not look at.
	Limits []string `json:"limits,omitempty"`
}

// Blocked is the error an archive that may not be written fails with, or nil.
// `accepted` says the person accepted, in writing, the dependencies the archive
// will not carry; a problem that is not acceptable blocks either way.
func (p *Plan) Blocked(accepted bool) error {
	var held []Problem
	for _, problem := range p.Problems {
		if problem.Acceptable && accepted {
			continue
		}
		held = append(held, problem)
	}
	if len(held) == 0 {
		return nil
	}
	first := held[0]
	if len(held) == 1 {
		return errors.New(first.Message)
	}
	return fmt.Errorf("%d things stop this package, starting with: %s", len(held), first.Message)
}

// Request is what a plan is made from.
type Request struct {
	// Manifest is the finished build.
	Manifest *build.Manifest
	// MapName overrides the name the map is packaged and loaded under. Empty
	// means the build's own.
	MapName string
	Grants  []Grant
	// Include names extra files to carry, by the path an engine would look
	// them up under: a licence text, a level shot. Each needs a grant like any
	// other file.
	Include []string
	// KnownAssets identifies released commercial files by digest. Nil means
	// `internal/pack`'s built-in corpus.
	KnownAssets *pack.AssetCorpus
	// WorkDir is where members read out of an archive are held until the
	// package is written. Empty means the system's temporary directory.
	WorkDir string
}

// Prepared is a plan with everything needed to write it. Close removes the
// files it extracted.
type Prepared struct {
	Plan *Plan

	manifest *build.Manifest
	packPlan *pack.Plan
	grants   []Grant
	work     string
}

// Close removes what the plan extracted to read. Safe to call twice.
func (p *Prepared) Close() error {
	if p == nil || p.work == "" {
		return nil
	}
	work := p.work
	p.work = ""
	return os.RemoveAll(work)
}

// candidate is one content file an engine needs, on its way to a decision.
type candidate struct {
	vfs     string
	role    string
	source  string // Source.ID
	archive string // the archive on this machine, when it is in one
	entry   string // its name inside that archive
	loose   string // the file on this machine, when it is loose
	member  string // the path it would be packaged under

	disposition Disposition
	reason      string
	grant       *Grant
	pathGrant   bool
	file        string // where its bytes are, once materialized
}

// Prepare reads a build and decides what its package would be. Nothing is
// written outside a temporary directory, which [Prepared.Close] removes.
func Prepare(request Request) (*Prepared, error) {
	manifest := request.Manifest
	if manifest == nil {
		return nil, errors.New("q3pack: no build to package")
	}
	if manifest.EngineFamily != FamilyQuake3 {
		return nil, fmt.Errorf("%w: build %s is a %q build. Package a Quake or Quake II build with "+
			"`companion package create --build %s`", ErrNotQuake3, manifest.BuildID,
			manifest.EngineFamily, manifest.BuildID)
	}
	level, err := build.PlayableLevel(manifest)
	if err != nil {
		return nil, fmt.Errorf("q3pack: build %s: %w", manifest.BuildID, err)
	}
	mapName := strings.TrimSpace(request.MapName)
	if mapName == "" {
		mapName = level.MapName
	}
	if err := engine.CheckMapName(mapName); err != nil {
		return nil, fmt.Errorf("q3pack: %w", err)
	}
	stage := manifest.GameData
	if stage == nil {
		return nil, fmt.Errorf("q3pack: build %s records no staged game data, so there is nothing to say "+
			"what its dependencies resolved to. Build the map again with this version of the Companion",
			manifest.BuildID)
	}
	grants, err := normalizeGrants(request.Grants)
	if err != nil {
		return nil, err
	}

	roots, err := stagedRoots(manifest, stage)
	if err != nil {
		return nil, err
	}
	scan := q3deps.Scan{ContentRoots: roots.content, GameRoots: roots.game}

	plan := &Plan{
		SchemaVersion: PlanSchemaVersion,
		BuildID:       manifest.BuildID,
		Map:           mapIdentity(manifest, mapName),
		BaseGame:      stage.BaseGame,
		FSGame:        stage.FSGame,
		GameDir:       stage.BaseGame,
		Counts:        map[Disposition]int{},
	}
	if stage.FSGame != "" {
		plan.GameDir = stage.FSGame
	}
	plan.ArchiveName = archiveName(plan.Map)

	// What the engine will look for, from the file the engine reads.
	runtimeReferences, err := q3deps.ParseBSP(level.BSP)
	if err != nil {
		return nil, err
	}
	extras, err := extraReferences(request.Include, mapName)
	if err != nil {
		return nil, err
	}
	runtime, err := q3deps.Resolve([]string{level.BSP}, append(runtimeReferences, extras...), scan)
	if err != nil {
		return nil, err
	}
	// What the compiler looked for, from the source it compiled.
	var compile *q3deps.Report
	if source := mapSource(manifest); source != "" {
		if compile, err = q3deps.Discover([]string{source}, scan); err != nil {
			return nil, err
		}
	} else {
		plan.Limits = append(plan.Limits, "what the COMPILE needed: the map source this build compiled is no "+
			"longer on this machine, so only what an engine needs is listed")
	}

	sources := newSources(stage, roots, grants)
	work, err := os.MkdirTemp(request.WorkDir, "aucom-q3pack-")
	if err != nil {
		return nil, fmt.Errorf("q3pack: preparing to read the build's content: %w", err)
	}
	prepared := &Prepared{Plan: plan, manifest: manifest, grants: grants, work: work}
	fail := func(err error) (*Prepared, error) {
		_ = prepared.Close()
		return nil, err
	}

	// Every content file an engine needs, decided once however many
	// dependencies name it.
	target, _ := pack.TargetByID(TargetID)
	candidates := map[string]*candidate{}
	var order []string
	optional := map[string]bool{}
	for _, reference := range extras {
		if reference.From == companionFrom {
			optional[reference.Name] = true
		}
	}
	for _, resolution := range runtime.Resolutions {
		for _, file := range resolution.Files {
			if file.CompileOnly || !file.Found() || file.Where != q3deps.InContent {
				continue
			}
			if _, seen := candidates[file.Path]; seen {
				continue
			}
			entry, err := sources.candidate(file, target)
			if err != nil {
				return fail(err)
			}
			candidates[file.Path] = entry
			order = append(order, file.Path)
		}
	}
	sort.Strings(order)
	if err := sources.unusedGrants(); err != nil {
		return fail(err)
	}

	// The bytes of every candidate a grant lets in, where pack can hash them.
	if err := materialize(candidates, order, work); err != nil {
		return fail(err)
	}

	// The archive: the map, and what was granted.
	buildRef, _, digests := build.PackageRefs(manifest)
	bspMember := "maps/" + mapName + ".bsp"
	files := []pack.FileSource{{
		Path: bspMember, File: level.BSP,
		FromBuild: fmt.Sprintf("the compiled map (%s)", buildRef.BuildID),
	}}
	claimed := map[string]string{pack.CaseKey(bspMember): "the map this build compiled"}
	for _, vfs := range order {
		entry := candidates[vfs]
		if !entry.disposition.carried() {
			continue
		}
		key := pack.CaseKey(entry.member)
		if holder, taken := claimed[key]; taken {
			entry.disposition = Blocked
			entry.reason = fmt.Sprintf("%s would be packaged twice: it is also %s. An archive holds one file "+
				"per path, and which one an engine got would depend on the order it read them in", entry.member, holder)
			plan.Problems = append(plan.Problems, Problem{Code: ProblemDuplicate, Subject: entry.member, Message: entry.reason})
			continue
		}
		claimed[key] = "the file from " + sources.describe(entry.source)
		files = append(files, pack.FileSource{Path: entry.member, File: entry.file})
	}
	collected, err := pack.Collect(nil, files, target)
	if err != nil {
		return fail(fmt.Errorf("q3pack: %w", err))
	}

	// A released commercial file is recognised by what it is. A grant for the
	// archive or the folder it sits in does not cover it: only a grant that
	// names the file does, which is somebody asserting the right for THAT file.
	corpus := request.KnownAssets
	if corpus == nil {
		corpus = pack.BuiltinCorpus()
	}
	byMember := map[string]*candidate{}
	for _, vfs := range order {
		byMember[candidates[vfs].member] = candidates[vfs]
	}
	policy := pack.Policy{
		KnownAssets:    corpus,
		BuildOutputs:   digests,
		Authorizations: map[string]string{},
	}
	kept := collected[:0]
	for _, item := range collected {
		entry := byMember[item.Path]
		if entry == nil { // the map itself
			kept = append(kept, item)
			continue
		}
		if asset, known := corpus.Lookup(item.SHA256); known && !entry.pathGrant {
			entry.disposition = Blocked
			entry.reason = fmt.Sprintf("its content is byte-for-byte %s. A grant for the archive or folder it is in "+
				"does not cover a released commercial file; only a grant naming this file does", asset.Release)
			continue
		}
		policy.Authorizations[item.Path] = grantSentence(entry.grant)
		kept = append(kept, item)
	}
	packPlan, err := pack.NewPlan(kept, policy, target)
	if err != nil {
		return fail(fmt.Errorf("q3pack: %w", err))
	}
	if err := packPlan.Blocked(); err != nil {
		plan.Problems = append(plan.Problems, Problem{Code: ProblemPack, Message: err.Error()})
	}
	prepared.packPlan = packPlan

	for _, decision := range packPlan.Included() {
		member := Member{Path: decision.Path, Size: decision.Size, SHA256: decision.SHA256}
		if entry := byMember[decision.Path]; entry != nil {
			member.Disposition = entry.disposition
			member.Source = entry.source
			member.From = sources.describe(entry.source)
			if entry.grant != nil {
				member.Licence, member.Statement = entry.grant.Licence, entry.grant.Statement
			}
		} else {
			member.Disposition = BuildOutput
			member.From = "this build's compiled map"
		}
		plan.Members = append(plan.Members, member)
		plan.TotalBytes += decision.Size
	}
	sort.Slice(plan.Members, func(i, j int) bool { return plan.Members[i].Path < plan.Members[j].Path })
	plan.Reproducibility = string(target.Reproducibility())

	plan.Dependencies = dependencies(compile, runtime, candidates, optional)
	notCarried := map[string]bool{}
	for _, dependency := range plan.Dependencies {
		plan.Counts[dependency.Disposition]++
		for _, file := range dependency.Files {
			if !file.Runtime || !file.Disposition.unmet() || notCarried[file.Path] {
				continue
			}
			notCarried[file.Path] = true
			plan.NotCarried = append(plan.NotCarried, file.Path)
			plan.Problems = append(plan.Problems, problemFor(dependency, file))
		}
	}
	sort.Strings(plan.NotCarried)
	sort.SliceStable(plan.Problems, func(i, j int) bool {
		if plan.Problems[i].Acceptable != plan.Problems[j].Acceptable {
			return !plan.Problems[i].Acceptable
		}
		return plan.Problems[i].Subject < plan.Problems[j].Subject
	})
	plan.Sources = sources.list()
	plan.Limits = append(plan.Limits, runtime.Limits...)
	plan.Limits = append(plan.Limits,
		"external lightmaps and any other file the compiler wrote beside the map: the archive carries the `.bsp` only",
		"whether the base game somebody else has installed is the one this build read; a file marked "+
			"`base_game` is found only by a player who has the same game")
	return prepared, nil
}

// problemFor is the sentence for one file an engine needs and will not get.
func problemFor(dependency Dependency, file File) Problem {
	problem := Problem{Subject: file.Path, Acceptable: true}
	switch file.Disposition {
	case Missing:
		problem.Code = ProblemMissing
		problem.Message = fmt.Sprintf("%s (%s of %s) is in nothing this build read. An engine will draw the "+
			"default texture, or drop the model or sound, in its place", file.Path, file.Role, dependency.Name)
	case ThirdPartyUnresolved:
		problem.Code = ProblemUnresolved
		problem.Message = fmt.Sprintf("%s (%s of %s) is not packaged: %s", file.Path, file.Role, dependency.Name, file.Reason)
	default:
		problem.Code = ProblemBlocked
		problem.Message = fmt.Sprintf("%s (%s of %s) is not packaged: %s", file.Path, file.Role, dependency.Name, file.Reason)
	}
	return problem
}

// grantSentence is a grant as the package manifest records it.
func grantSentence(grant *Grant) string {
	if grant == nil {
		return ""
	}
	var text string
	switch grant.Basis {
	case BasisOwnWork:
		text = "your own work"
	case BasisLicensed:
		text = "licensed for redistribution under " + grant.Licence
	}
	if grant.Statement != "" {
		text += " — " + grant.Statement
	}
	return text
}

// mapIdentity is which map a build compiled.
func mapIdentity(manifest *build.Manifest, name string) MapIdentity {
	identity := MapIdentity{Name: name}
	for _, input := range manifest.Inputs {
		if !isMapSource(input) {
			continue
		}
		identity.SourceSHA256 = input.SHA256
		if input.Source != nil {
			identity.AssetID = input.Source.AssetID
			identity.RevisionID = input.Source.RevisionID
			identity.Revision = input.Source.Revision
			identity.DisplayName = input.Source.DisplayName
		}
		break
	}
	return identity
}

func isMapSource(input build.FileRecord) bool {
	return input.Role == "q3.map.source" || input.Name == "source_map"
}

// mapSource is the `.map` the build compiled, when it is still on this machine.
func mapSource(manifest *build.Manifest) string {
	for _, input := range manifest.Inputs {
		if !isMapSource(input) || input.Path == "" {
			continue
		}
		if info, err := os.Stat(input.Path); err == nil && info.Mode().IsRegular() {
			return input.Path
		}
	}
	return ""
}

// archiveName is `auto-pigeon-<map id>-<revision>.pk3`.
//
// A saved map is named by the account's own id and revision number, so two
// revisions of one map are two archives and neither replaces the other. A map
// built from a file has neither, and is named by what it is called and the
// first bytes of its source's digest — which changes when the source does, for
// the same reason.
func archiveName(identity MapIdentity) string {
	id, revision := safeToken(identity.AssetID), ""
	if id != "" {
		revision = fmt.Sprintf("%d", identity.Revision)
	} else {
		id = safeToken(identity.Name)
		digest := strings.TrimPrefix(identity.SourceSHA256, "sha256:")
		if len(digest) > 12 {
			digest = digest[:12]
		}
		revision = "local"
		if digest != "" {
			revision = "local-" + safeToken(digest)
		}
	}
	return "auto-pigeon-" + id + "-" + revision + ".pk3"
}

// safeToken keeps the letters, digits, `_` and `-` of a value, lower-cased.
func safeToken(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	token := b.String()
	if len(token) > 48 {
		token = token[:48]
	}
	return token
}

// companionFrom marks the files a map conventionally travels with.
const companionFrom = "map companion"

// extraReferences are the files to carry that the BSP does not name: the ones
// the person asked for, and the two a Quake III map conventionally ships with —
// its level shot and its arena script — when the content has them.
func extraReferences(include []string, mapName string) ([]q3deps.Reference, error) {
	var out []q3deps.Reference
	seen := map[string]bool{}
	for _, raw := range include {
		name := strings.ToLower(path.Clean(strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")))
		if name == "." || name == "" || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("q3pack: %q is not a path inside a game directory", raw)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, q3deps.Reference{Name: name, Raw: raw, Kind: q3deps.KindFile, From: "you asked for it", Count: 1})
	}
	for _, name := range []string{
		"levelshots/" + mapName + ".jpg", "levelshots/" + mapName + ".tga", "scripts/" + mapName + ".arena",
	} {
		if !seen[name] {
			out = append(out, q3deps.Reference{Name: name, Raw: name, Kind: q3deps.KindFile, From: companionFrom, Count: 1})
		}
	}
	return out, nil
}

// stagedDirs are the game directories a build staged, as the scan reads them.
type stagedDirs struct {
	content []string
	game    []string
}

// stagedRoots lists the staged game directories of a build, the mod before the
// base game, and refuses a build whose staged data has been removed.
func stagedRoots(manifest *build.Manifest, stage *q3vfs.Stage) (stagedDirs, error) {
	var dirs stagedDirs
	present := false
	for _, root := range stage.Roots {
		var games []string
		if stage.FSGame != "" {
			games = append(games, filepath.Join(root.Path, stage.FSGame))
		}
		games = append(games, filepath.Join(root.Path, stage.BaseGame))
		if info, err := os.Stat(root.Path); err == nil && info.IsDir() {
			present = true
		}
		if root.Role == q3vfs.PackagesRole {
			dirs.content = append(dirs.content, games...)
		} else {
			dirs.game = append(dirs.game, games...)
		}
	}
	if !present {
		return dirs, fmt.Errorf("q3pack: the game data build %s staged is no longer on this machine, so what "+
			"its dependencies resolved to cannot be read. Build the map again", manifest.BuildID)
	}
	return dirs, nil
}

// normalizeGrants checks every grant and puts its target in one spelling.
func normalizeGrants(grants []Grant) ([]Grant, error) {
	out := make([]Grant, 0, len(grants))
	seen := map[string]bool{}
	for _, grant := range grants {
		targets := 0
		if grant.Archive != "" {
			targets++
		}
		if grant.Loose {
			targets++
		}
		if grant.Path != "" {
			targets++
		}
		if targets != 1 {
			return nil, errors.New("q3pack: a grant names exactly one thing: an archive by digest, the loose files, or one path")
		}
		grant.Archive = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(grant.Archive), "sha256:"))
		if grant.Path != "" {
			grant.Path = strings.ToLower(path.Clean(strings.ReplaceAll(strings.TrimSpace(grant.Path), `\`, "/")))
		}
		grant.Licence = strings.TrimSpace(grant.Licence)
		grant.Statement = strings.TrimSpace(grant.Statement)
		switch grant.Basis {
		case BasisOwnWork, BasisNotRedistributable:
		case BasisLicensed:
			if grant.Licence == "" {
				return nil, fmt.Errorf("q3pack: the grant for %s says it is licensed and names no licence. "+
					"Name it — an SPDX identifier, or the licence's own title — so the package can say what "+
					"it is distributed under", grantTarget(grant))
			}
		default:
			return nil, fmt.Errorf("q3pack: the grant for %s has the basis %q; it is one of %s, %s and %s",
				grantTarget(grant), grant.Basis, BasisOwnWork, BasisLicensed, BasisNotRedistributable)
		}
		key := grantTarget(grant)
		if seen[key] {
			return nil, fmt.Errorf("q3pack: %s has two grants; give it one", key)
		}
		seen[key] = true
		out = append(out, grant)
	}
	return out, nil
}

func grantTarget(grant Grant) string {
	switch {
	case grant.Archive != "":
		return "the archive " + grant.Archive
	case grant.Loose:
		return "the loose files"
	}
	return "the file " + grant.Path
}

// hashFile is a file's SHA-256, in hex.
func hashFile(name string) (string, int64, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
