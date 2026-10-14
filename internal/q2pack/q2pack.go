// Package q2pack plans and writes the PAK a finished Quake II build is played
// from: the map the build compiled, under the name it will be loaded by, and the
// files of the user's own content the engine needs and somebody said may travel
// with it.
//
// # What goes in, and what never does
//
// The BSP is read for what an engine will ask for (`internal/q2deps`); each
// request is resolved the way an engine with this package installed as a mod
// over the base game would resolve it. What the base game supplies is never
// packaged — a Quake II installation is a dependency of the package, not a part
// of it, and `pak0.pak` is not redistributable. What only the user's content
// folder holds is packaged when a grant covers it, and named when none does.
// What nothing holds is named before an archive is written: a package that
// loaded a map with missing textures and said nothing would be the misleading
// portable package this review exists to prevent. Accepting that, in writing,
// is possible and recorded; a reason cannot accept an unsafe or colliding path,
// or a map name the engine would satisfy from somewhere else.
//
// The map source, the compiler's `.texinfo.json`, its portal and leak files and
// its logs are never members: nothing here selects files by folder, only by
// what the compiled map names.
//
// # One writer
//
// The archive is written by `internal/pack`, which owns the PACK format, its
// name and size bounds, its collision checks and its member order. Nothing here
// writes archive bytes, lower-cases a member path, or writes over a package
// that is already there.
package q2pack

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2deps"
)

// FamilyQuake2 is AUB's engine family for Quake II.
const FamilyQuake2 = "quake2"

// PlanSchemaVersion versions [Plan].
const PlanSchemaVersion = "aucom.q2-package-plan/1.0"

// TargetID is the `internal/pack` target every Quake II package is written to.
const TargetID = "quake2-pak"

// ErrNotQuake2 reports a build of another family.
var ErrNotQuake2 = errors.New("q2pack: this is not a Quake II build")

// Basis is why a file may be redistributed, or why it may not.
type Basis string

const (
	BasisOwnWork            Basis = "own_work"
	BasisLicensed           Basis = "licensed"
	BasisNotRedistributable Basis = "not_redistributable"
)

// Grant is somebody saying whether files of their content may travel with the
// map. It covers the loose files of the content folder, or one virtual path.
type Grant struct {
	Basis Basis `json:"basis"`
	// Licence names the licence for BasisLicensed, and is required there.
	Licence string `json:"licence,omitempty"`
	// Loose covers every loose file of the content folder.
	Loose bool `json:"loose,omitempty"`
	// Path covers one virtual path, and outranks a Loose grant.
	Path string `json:"path,omitempty"`
	// Statement is recorded verbatim.
	Statement string `json:"statement,omitempty"`
}

// Disposition is what became of a file.
type Disposition string

const (
	BuildOutput          Disposition = "build_output"
	UserAuthored         Disposition = "user_authored"
	Licensed             Disposition = "licensed"
	Notice               Disposition = "notice"
	BaseGame             Disposition = "base_game"
	ThirdPartyUnresolved Disposition = "third_party_unresolved"
	Blocked              Disposition = "blocked"
	MissingFile          Disposition = "missing"
)

func (d Disposition) carried() bool {
	return d == BuildOutput || d == UserAuthored || d == Licensed || d == Notice
}

// Member is one file of the archive.
type Member struct {
	Path        string      `json:"path"`
	Size        int64       `json:"size"`
	SHA256      string      `json:"sha256"`
	Disposition Disposition `json:"disposition"`
	From        string      `json:"from"`
	Licence     string      `json:"licence,omitempty"`
	source      string
}

// File is one file a dependency resolved to, as the review lists it.
type File struct {
	Path              string      `json:"path"`
	Role              string      `json:"role"`
	RequiredAtCompile bool        `json:"required_at_compile"`
	RequiredAtRuntime bool        `json:"required_at_runtime"`
	Disposition       Disposition `json:"disposition"`
	Reason            string      `json:"reason,omitempty"`
}

// Dependency is one thing the compiled map names.
type Dependency struct {
	Name              string      `json:"name"`
	Kind              string      `json:"kind"`
	From              string      `json:"from"`
	Files             []File      `json:"files"`
	RequiredAtCompile bool        `json:"required_at_compile"`
	RequiredAtRuntime bool        `json:"required_at_runtime"`
	Disposition       Disposition `json:"disposition"`
}

// Problem is why the archive would not be written as it stands.
type Problem struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
	// Acceptable says a written reason may accept it.
	Acceptable bool `json:"acceptable"`
}

// Source is the content folder, as the page asks about it.
type Source struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	RuntimeFiles int      `json:"runtime_files"`
	Hints        []string `json:"hints,omitempty"`
}

// MapIdentity is the chain from what was compiled to what is packaged.
type MapIdentity struct {
	BuildID string `json:"build_id"`
	// Name is the stem the engine loads: `+map <name>`, `maps/<name>.bsp`.
	Name      string `json:"name"`
	BSPSHA256 string `json:"bsp_sha256"`
	BSPBytes  int64  `json:"bsp_bytes"`
	// SourceName and SourceSHA256 are the `.map` the build compiled.
	SourceName   string `json:"source_name,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	// Revision is the account revision the source came from, when it did.
	AssetID    string `json:"asset_id,omitempty"`
	RevisionID string `json:"revision_id,omitempty"`
	Revision   int    `json:"revision,omitempty"`
	// APMapSHA256 and DocumentRevision identify the APMap a conversion read.
	APMapSHA256      string `json:"apmap_sha256,omitempty"`
	DocumentID       string `json:"document_id,omitempty"`
	DocumentRevision int    `json:"document_revision,omitempty"`
}

// Plan is the whole review: what the archive holds, what the map needs and
// what became of each, and why it would not be written.
type Plan struct {
	SchemaVersion string      `json:"schema_version"`
	BuildID       string      `json:"build_id"`
	EngineFamily  string      `json:"engine_family"`
	Map           MapIdentity `json:"map"`
	ArchiveName   string      `json:"archive_name"`
	// GameDir is the mod folder an install uses unless told another.
	GameDir      string       `json:"game_dir"`
	BaseGame     string       `json:"base_game"`
	Sources      []Source     `json:"sources"`
	Members      []Member     `json:"members"`
	TotalBytes   int64        `json:"total_bytes"`
	Dependencies []Dependency `json:"dependencies"`
	Problems     []Problem    `json:"problems,omitempty"`
	// NotCarried is what an engine needs that neither the archive nor the base
	// game supplies.
	NotCarried []string `json:"not_carried,omitempty"`
	// Overrides are members whose path the base game also has: the package's
	// copy is the one the engine loads, because a mod is searched first.
	Overrides []string `json:"overrides,omitempty"`
	Limits    []string `json:"limits,omitempty"`
	Grants    []Grant  `json:"grants,omitempty"`

	packPlan *pack.Plan
}

// Blocked is nil when the plan may be written: with no problem, or — when
// accepted — with only problems a reason may accept.
func (p *Plan) Blocked(accepted bool) error {
	var held []string
	for _, problem := range p.Problems {
		if accepted && problem.Acceptable {
			continue
		}
		held = append(held, problem.Message)
	}
	if len(held) == 0 {
		return nil
	}
	return failure.As(failure.PackageHeld, fmt.Errorf("q2pack: %d thing(s) stop this package: %s",
		len(held), strings.Join(held, "; ")))
}

// Request is what to package.
type Request struct {
	Manifest *build.Manifest
	// MapName is the name it is loaded by. Empty means the source's name.
	MapName string
	Grants  []Grant
}

// BaseGameDir is the base game's directory name.
const BaseGameDir = "baseq2"

// DefaultGameDir is the mod folder a map is installed into unless another is
// named: one per map name, so two maps never share one and an install of one
// never replaces the other's.
func DefaultGameDir(mapName string) string { return "aup-" + mapName }

// maxMapName keeps `maps/<name>.bsp` inside a PAK member's 55 bytes.
const maxMapName = pack.PAKNameLength - len("maps/") - len(".bsp")

// Prepare reads a finished Quake II build and plans its package. It writes
// nothing.
func Prepare(request Request) (*Plan, error) {
	manifest := request.Manifest
	if manifest == nil {
		return nil, errors.New("q2pack: no build")
	}
	if manifest.EngineFamily != FamilyQuake2 {
		return nil, fmt.Errorf("%w: build %s is for %q", ErrNotQuake2, manifest.BuildID, manifest.EngineFamily)
	}
	level, err := build.PlayableLevel(manifest)
	if err != nil {
		return nil, err
	}
	compiled, err := q2deps.ParseBSP(level.BSP)
	if err != nil {
		return nil, failure.As(failure.OutputInvalid, err)
	}
	grants, err := normalizeGrants(request.Grants)
	if err != nil {
		return nil, failure.As(failure.InputInvalid, err)
	}
	name := strings.TrimSpace(request.MapName)
	if name == "" {
		name = level.MapName
	}
	if err := engine.CheckMapName(name); err != nil {
		return nil, failure.As(failure.InputInvalid, err)
	}
	if len(name) > maxMapName {
		return nil, failure.As(failure.InputInvalid, fmt.Errorf(
			"q2pack: %q is %d characters; maps/<name>.bsp must fit a PAK member's %d bytes, so a name is at most %d",
			name, len(name), pack.PAKNameLength, maxMapName))
	}

	roots := buildRoots(manifest)
	baseRoot := roots[profile.RootGame]
	if baseRoot == "" {
		return nil, failure.As(failure.GameDataMissing, fmt.Errorf(
			"q2pack: build %s records no base game folder, so what the base game supplies cannot be told apart from what is missing",
			manifest.BuildID))
	}
	base, err := q2deps.IndexDir(baseRoot)
	if err != nil {
		return nil, failure.As(failure.GameDataMissing, fmt.Errorf("q2pack: the base game folder this build read: %w", err))
	}
	var content *q2deps.Index
	if contentRoot := roots[profile.RootContent]; contentRoot != "" && !sameDir(contentRoot, baseRoot) {
		if content, err = q2deps.IndexDir(contentRoot); err != nil {
			return nil, failure.As(failure.InputInvalid, fmt.Errorf("q2pack: the content folder this build read: %w", err))
		}
	}
	report := q2deps.Resolve(compiled, content, base)

	bspDigest, bspSize, err := hashFile(level.BSP)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		SchemaVersion: PlanSchemaVersion,
		BuildID:       manifest.BuildID,
		EngineFamily:  FamilyQuake2,
		Map:           identity(manifest, name, bspDigest, bspSize),
		ArchiveName:   name + ".pak",
		GameDir:       DefaultGameDir(name),
		BaseGame:      BaseGameDir,
		Limits:        report.Limits,
		Grants:        grants,
	}
	if content != nil && len(content.Archives) > 0 {
		plan.Limits = append(plan.Limits, fmt.Sprintf(
			"the content folder's archives (%s) are not opened for packaging: a file only they hold is reported missing",
			strings.Join(content.Archives, ", ")))
	}

	// The engine must not be able to find this name anywhere but the package:
	// a map the base game or the content folder already has under it is the
	// downloaded original, and an engine that loaded that one would be showing
	// somebody a map that is not the one they built.
	mapMember := "maps/" + name + ".bsp"
	if location, taken := base.Find(mapMember); taken {
		plan.Problems = append(plan.Problems, Problem{Code: "map_name_taken", Path: mapMember, Message: fmt.Sprintf(
			"the base game already has %s (%s), so an engine could load that map instead of this one; choose another name",
			mapMember, describe(location))})
	}
	if location, taken := content.Find(mapMember); taken {
		plan.Problems = append(plan.Problems, Problem{Code: "map_name_taken", Path: mapMember, Message: fmt.Sprintf(
			"your content folder already has %s (%s) — a compiled map that is not this build; choose another name so nothing can load it in this one's place",
			mapMember, describe(location))})
	}

	plan.Members = append(plan.Members, Member{Path: mapMember, Size: bspSize, SHA256: bspDigest,
		Disposition: BuildOutput, From: "build " + manifest.BuildID, source: level.BSP})
	loose := Source{ID: "loose", Kind: "loose"}
	carriedContent := false
	members := map[string]bool{mapMember: true}
	for _, resolved := range report.Files {
		dependency := Dependency{Name: resolved.Name, Kind: string(resolved.Kind), From: resolved.From,
			RequiredAtRuntime: true}
		file := File{Path: firstNonEmpty(resolved.Path, resolved.Paths[0]), Role: string(resolved.Kind), RequiredAtRuntime: true}
		switch resolved.Where {
		case q2deps.FromBase:
			file.Disposition = BaseGame
			file.Reason = "the base game has it (" + describe(resolved.Location) + ")"
		case q2deps.Missing:
			file.Disposition = MissingFile
			file.Reason = "neither your content folder nor the base game has " + strings.Join(resolved.Paths, " or ")
			plan.NotCarried = append(plan.NotCarried, resolved.Paths[0])
			plan.Problems = append(plan.Problems, Problem{Code: "missing", Path: resolved.Paths[0], Acceptable: true,
				Message: fmt.Sprintf("%s (%s, %s) is in neither your content folder nor the base game", resolved.Paths[0], resolved.Kind, resolved.From)})
		case q2deps.FromContent:
			loose.RuntimeFiles++
			grant := grantFor(grants, resolved.Location.Path)
			file.Disposition, file.Reason = disposition(grant)
			if resolved.Problem != "" {
				plan.Problems = append(plan.Problems, Problem{Code: "content_invalid", Path: resolved.Location.Path, Message: resolved.Problem})
			}
			if file.Disposition.carried() {
				// By exact path: two spellings of one path are a collision
				// internal/pack names, not something to drop quietly here.
				for _, location := range append([]q2deps.Location{resolved.Location}, resolved.Also...) {
					if members[location.Path] {
						continue
					}
					members[location.Path] = true
					digest, size, err := hashFile(location.File)
					if err != nil {
						return nil, err
					}
					member := Member{Path: location.Path, Size: size, SHA256: digest, Disposition: file.Disposition,
						From: "your content folder", source: location.File}
					if grant != nil {
						member.Licence = grant.Licence
					}
					plan.Members = append(plan.Members, member)
					carriedContent = true
					if _, inBase := base.Find(location.Path); inBase {
						plan.Overrides = append(plan.Overrides, location.Path)
					}
				}
			} else {
				if _, inBase := base.Find(resolved.Location.Path); !inBase {
					plan.NotCarried = append(plan.NotCarried, resolved.Location.Path)
				}
				code := "no_grant"
				if file.Disposition == Blocked {
					code = "not_redistributable"
				}
				plan.Problems = append(plan.Problems, Problem{Code: code, Path: resolved.Location.Path, Acceptable: true,
					Message: fmt.Sprintf("%s is only in your content folder, and %s", resolved.Location.Path, file.Reason)})
			}
		}
		if resolved.Kind == q2deps.KindSky && resolved.Where == q2deps.FromContent && len(resolved.Also) == 0 {
			file.Reason += fmt.Sprintf("; only %s is there — a renderer that asks for the other format draws no sky here", resolved.Location.Path)
		}
		dependency.Disposition = file.Disposition
		dependency.Files = []File{file}
		plan.Dependencies = append(plan.Dependencies, dependency)
	}
	if carriedContent && content != nil {
		for _, notice := range notices(content) {
			path := "docs/" + name + "/" + strings.ToLower(filepath.Base(notice.Path))
			if members[path] || len(path) > pack.PAKNameLength {
				continue
			}
			members[path] = true
			digest, size, err := hashFile(notice.File)
			if err != nil {
				return nil, err
			}
			plan.Members = append(plan.Members, Member{Path: path, Size: size, SHA256: digest, Disposition: Notice,
				From: "the notice " + notice.Path + " in your content folder", source: notice.File})
		}
	}
	if loose.RuntimeFiles > 0 {
		if content != nil {
			loose.Hints = append(loose.Hints, fmt.Sprintf("%d loose file(s) in it; only what the map names is considered", content.LooseFiles()))
		}
		plan.Sources = append(plan.Sources, loose)
	}
	sort.Strings(plan.NotCarried)
	plan.NotCarried = unique(plan.NotCarried)

	// internal/pack decides the order, the bounds and the collisions.
	target, _ := pack.TargetByID(TargetID)
	candidates := make([]pack.Candidate, 0, len(plan.Members))
	authorizations := map[string]string{}
	for _, member := range plan.Members {
		candidates = append(candidates, pack.Candidate{Path: member.Path, Source: member.source, Size: member.Size,
			SHA256: member.SHA256, FromBuild: map[bool]string{true: "bsp"}[member.Disposition == BuildOutput]})
		if member.Disposition != BuildOutput {
			authorizations[member.Path] = string(member.Disposition) + ": " + member.From
		}
	}
	packPlan, err := pack.NewPlan(candidates, pack.Policy{Authorizations: authorizations}, target)
	if err != nil {
		return nil, failure.As(failure.ContentRefused, err)
	}
	for _, collision := range packPlan.Collisions {
		plan.Problems = append(plan.Problems, Problem{Code: "path_collision", Message: collision.Error()})
	}
	for _, decision := range packPlan.Refused() {
		plan.Problems = append(plan.Problems, Problem{Code: "refused", Path: decision.Path, Message: decision.Path + ": " + decision.Reason})
	}
	if len(plan.Members) > target.MaxEntries {
		plan.Problems = append(plan.Problems, Problem{Code: "too_many_members", Message: fmt.Sprintf(
			"%d members is more than the %d %s allows", len(plan.Members), target.MaxEntries, target.MaxEntriesNote)})
	}
	plan.packPlan = packPlan
	ordered := map[string]int{}
	for i, member := range packPlan.Members() {
		ordered[member.Path] = i
	}
	sort.SliceStable(plan.Members, func(i, j int) bool { return ordered[plan.Members[i].Path] < ordered[plan.Members[j].Path] })
	for _, member := range plan.Members {
		plan.TotalBytes += member.Size
	}
	return plan, nil
}

// buildRoots are the folders the build's compile read, by role: the ones the
// request supplied, and otherwise the ones the compiling tool's binding gave
// it — which is where they come from for a build started from the Build page.
func buildRoots(manifest *build.Manifest) map[string]string {
	roots := map[string]string{}
	for _, root := range manifest.Roots {
		roots[root.Role] = root.Path
	}
	compiler := ""
	for _, step := range manifest.Steps {
		if strings.HasSuffix(step.Capability, ".compile") || step.ID == "compile" {
			compiler = step.Profile.ID
			break
		}
	}
	for _, tool := range manifest.Tools {
		if compiler != "" && tool.Profile.ID != compiler {
			continue
		}
		for _, role := range []string{profile.RootGame, profile.RootContent} {
			if roots[role] == "" && tool.Roots[role] != "" {
				roots[role] = tool.Roots[role]
			}
		}
	}
	return roots
}

func identity(manifest *build.Manifest, name, digest string, size int64) MapIdentity {
	identity := MapIdentity{BuildID: manifest.BuildID, Name: name, BSPSHA256: digest, BSPBytes: size}
	for _, input := range manifest.Inputs {
		if input.Name != "source_map" && identity.SourceName != "" {
			continue
		}
		identity.SourceName, identity.SourceSHA256 = filepath.Base(input.Path), input.SHA256
		if input.Source != nil {
			identity.AssetID, identity.RevisionID, identity.Revision = input.Source.AssetID, input.Source.RevisionID, input.Source.Revision
		}
		if input.Conversion != nil {
			identity.APMapSHA256 = input.Conversion.SourceSHA256
			identity.DocumentID, identity.DocumentRevision = input.Conversion.DocumentID, input.Conversion.DocumentRevision
		}
	}
	return identity
}

func normalizeGrants(grants []Grant) ([]Grant, error) {
	out := make([]Grant, 0, len(grants))
	seen := map[string]bool{}
	for _, grant := range grants {
		grant.Licence = strings.TrimSpace(grant.Licence)
		grant.Path = strings.TrimSpace(grant.Path)
		switch grant.Basis {
		case BasisOwnWork, BasisNotRedistributable:
		case BasisLicensed:
			if grant.Licence == "" {
				return nil, errors.New("q2pack: a licensed grant names its licence — an SPDX identifier such as CC-BY-4.0, or the licence's own title")
			}
		default:
			return nil, fmt.Errorf("q2pack: %q is not a grant; it is own_work, licensed or not_redistributable", grant.Basis)
		}
		if grant.Loose == (grant.Path != "") {
			return nil, errors.New("q2pack: a grant covers either the loose files of the content folder or one path")
		}
		if grant.Path != "" {
			if err := pack.CheckEntryPath(grant.Path, pack.PAKNameLength); err != nil {
				return nil, fmt.Errorf("q2pack: the grant's path %q: %w", grant.Path, err)
			}
		}
		key := "loose"
		if grant.Path != "" {
			key = "path:" + strings.ToLower(grant.Path)
		}
		if seen[key] {
			return nil, fmt.Errorf("q2pack: two grants cover %s", strings.TrimPrefix(key, "path:"))
		}
		seen[key] = true
		out = append(out, grant)
	}
	return out, nil
}

func grantFor(grants []Grant, path string) *Grant {
	var loose *Grant
	for i := range grants {
		if grants[i].Path != "" && strings.EqualFold(grants[i].Path, path) {
			return &grants[i]
		}
		if grants[i].Loose {
			loose = &grants[i]
		}
	}
	return loose
}

func disposition(grant *Grant) (Disposition, string) {
	switch {
	case grant == nil:
		return ThirdPartyUnresolved, "nobody has said it may be redistributed, so it is not packaged"
	case grant.Basis == BasisOwnWork:
		return UserAuthored, "you said it is your own work"
	case grant.Basis == BasisLicensed:
		return Licensed, "you hold it under " + grant.Licence
	}
	return Blocked, "you said it is not yours to redistribute, so it stays out"
}

// notices are the files at the top of the content folder that are its notices.
func notices(content *q2deps.Index) []q2deps.Location {
	var out []q2deps.Location
	for _, name := range []string{"license", "licence", "copying", "notice", "credits", "readme"} {
		for _, ext := range []string{"", ".txt", ".md"} {
			for _, spelling := range []string{name + ext, strings.ToUpper(name) + ext, strings.ToUpper(name[:1]) + name[1:] + ext} {
				if location, ok := content.FindLoose(spelling); ok && !strings.Contains(location.Path, "/") {
					out = append(out, location)
				}
			}
		}
	}
	return out
}

func describe(location q2deps.Location) string {
	if location.Origin == q2deps.Archived {
		return "in " + location.Archive
	}
	return "loose, " + location.Path
}

func sameDir(a, b string) bool {
	left, errA := filepath.EvalSymlinks(a)
	right, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return left == right
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func unique(sorted []string) []string {
	out := sorted[:0]
	for i, value := range sorted {
		if i == 0 || value != sorted[i-1] {
			out = append(out, value)
		}
	}
	return out
}

// hashFile is a file's digest, spelled as `internal/pack` spells one.
func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("q2pack: reading %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("q2pack: reading %s: %w", filepath.Base(path), err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}
