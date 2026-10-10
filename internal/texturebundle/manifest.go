// Package texturebundle verifies and caches the map texture export AUB serves,
// and hands the build a read-only directory of original WAD files.
//
// # Why a package and not a download
//
// The bundle is a ZIP that arrives over the network, and everything inside it
// is somebody else's bytes with somebody else's names on them. Extracting it is
// hostile-input handling, not file copying: a member called `../../id1/pak0.pak`
// is a request to overwrite a game the user paid for, and a member that
// decompresses to forty gigabytes is a request to fill their disk. So there is
// one extractor, it is here, and every rule it enforces is a test.
//
// # What "verified" means
//
// The manifest declares each file's path, byte count and SHA-256. An entry is
// published only when every declared file is present exactly once at exactly
// that size and digest, every third-party notice the manifest lists is present
// at exactly its size and digest, nothing undeclared is in the archive except
// the two contracted documents, and the manifest's own `map_id` and `revision`
// are the ones the caller asked for. A requirement's `included: true` is a
// claim and never the proof: the proof is a declared member whose bytes hashed
// to the declared digest, and a requirement that claims to be carried and
// names no member is refused before anything is written. Until all of that passes, the extraction lives in
// a private temporary directory that is removed; the published entry appears in
// one rename.
//
// # Why the declaration order survives
//
// A Quake 1 map declares its WADs in order and a later declaration wins a name
// collision. That order is the map's content. It is carried through this
// package unchanged — never sorted, never deduplicated, never merged — because
// the compiler's answer depends on it and because AUB is the only thing
// entitled to decide what a map declares.
package texturebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Schema is the manifest version AUB writes now, and SchemaV11 is the one it
// wrote before `NEW_313A`; this package reads both and nothing else.
//
// A bundle announcing any other schema is refused rather than parsed
// optimistically: the fields this package acts on — the digests, the
// declaration order, `compiler_ready` — are exactly the ones a silent schema
// change would move.
//
// 1.2 adds, and only adds: a per-requirement `redistribution` decision for an
// installed source, and a top-level `notices` list of the third-party notice
// files that travel with a carried one. A 1.1 manifest has neither and is read
// exactly as it always was.
const (
	Schema    = "aub-map-texture-export/1.2"
	SchemaV11 = "aub-map-texture-export/1.1"
)

// acceptedSchema reports whether this package reads a manifest version.
func acceptedSchema(version string) bool {
	return version == Schema || version == SchemaV11
}

// NoticesDir is where a bundle's third-party notice files live: in the archive,
// and in a published entry beside `LICENSES.md`. It is deliberately not inside
// [ContentDir] — a notice is not a texture and a compiler is never pointed at
// it — and it is never merged into this program's own licence.
const NoticesDir = "NOTICES"

// Bounds on the notices one bundle may carry. Notices are short texts; these
// are the sizes past which a "notice" is something else.
const (
	MaxNotices     = 256
	MaxNoticeBytes = 1 << 20
)

// noticeName is the only shape a notice's file name may take: one plain
// segment. It becomes a path on this machine, so it is a vocabulary rather than
// a sanitizer — the same vocabulary AUB writes.
var noticeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// The origins AUB names. An absent origin is a manifest older than the field.
const (
	OriginInstalled = "installed"
	OriginUser      = "user"
	OriginEmbedded  = "embedded"
)

// The two redistribution decisions, and the reasons a source is withheld.
const (
	DecisionIncluded = "included"
	DecisionWithheld = "withheld"

	ReasonDeclared          = "declared"
	ReasonUndeclared        = "undeclared"
	ReasonDeclaredWithheld  = "declared_withheld"
	ReasonDigestMismatch    = "digest_mismatch"
	ReasonIncomplete        = "declaration_incomplete"
	ReasonUnreadable        = "source_unreadable"
	ReasonPolicyUnavailable = "policy_unavailable"
)

// Redistribution is the deployment's decision about one installed source's
// exact bytes, and — when they are carried — whose they are said to be and
// under what terms. It is the deployment operator's declaration, relayed; this
// program verifies that the bytes are the declared ones and asserts nothing
// further about them.
type Redistribution struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`

	SHA256 string `json:"sha256,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`

	Source        string   `json:"source,omitempty"`
	Credit        string   `json:"credit,omitempty"`
	Terms         string   `json:"terms,omitempty"`
	PrimaryNotice string   `json:"primary_notice,omitempty"`
	NoticeVersion string   `json:"notice_version,omitempty"`
	NoticePaths   []string `json:"notice_paths,omitempty"`
}

// Notice is one third-party notice file the bundle carries because a source in
// it requires one.
type Notice struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	// Sources are the declared texture sources this notice travels with.
	Sources []string `json:"sources,omitempty"`
}

// The two documents a bundle carries besides its declared payload.
const (
	ManifestName = "manifest.json"
	LicensesName = "LICENSES.md"
)

// File is one original file in the bundle, as the manifest declares it.
type File struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// Requirement is one declared texture source, in the map's own declaration
// order.
type Requirement struct {
	Order  int    `json:"order"`
	Name   string `json:"name"`
	Game   string `json:"game"`
	Kind   string `json:"kind"`
	Origin string `json:"origin,omitempty"`
	Status string `json:"status"`

	// Revision and SourceSHA256 identify which revision of an uploaded source,
	// and which bytes, the requirement resolved to. Absent when AUB did not say.
	Revision     int    `json:"revision,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`

	// Included says whether this source's original files are in the bundle.
	// False is not an error on its own — a source the deployment has no
	// redistribution permission on record for is named rather than sent — and
	// it is one of the things `compiler_ready` weighs. True is a CLAIM: what
	// makes a file usable is [Files], each verified against its digest.
	Included bool   `json:"included"`
	Note     string `json:"note,omitempty"`

	// Redistribution is present for an installed source in a 1.2 manifest.
	Redistribution *Redistribution `json:"redistribution,omitempty"`

	ReferencedTextures []string `json:"referenced_textures,omitempty"`
	MissingTextures    []string `json:"missing_textures,omitempty"`

	Files []File `json:"files"`
}

// Coverage is the map-level texture answer, passed through for display.
type Coverage struct {
	Supplied  []string          `json:"supplied,omitempty"`
	Missing   []string          `json:"missing,omitempty"`
	Unchecked []string          `json:"unchecked,omitempty"`
	Contested []string          `json:"contested,omitempty"`
	Winners   map[string]string `json:"winners,omitempty"`
}

// Manifest is the document at the root of a map texture export.
//
// Only the fields the Companion acts on or shows are decoded. An unknown field
// is ignored rather than refused, which is what makes a compatible AUB addition
// compatible; the SCHEMA VERSION is what guards the fields that are here.
type Manifest struct {
	SchemaVersion string `json:"schema_version"`

	MapID    string `json:"map_id"`
	MapName  string `json:"map_name,omitempty"`
	Revision int    `json:"revision"`
	Game     string `json:"game,omitempty"`

	ExportedAt string `json:"exported_at,omitempty"`

	// WADsDeclared is the Quake 1 declaration in the map's own order. Later
	// declarations win name collisions, so this is precedence and not decoration.
	WADsDeclared []string `json:"wads_declared,omitempty"`

	Requirements []Requirement `json:"requirements"`
	Files        []File        `json:"files"`

	Coverage Coverage `json:"coverage"`

	CompilerReady    bool     `json:"compiler_ready"`
	CompilerRefusals []string `json:"compiler_refusals"`
	Unresolved       []string `json:"unresolved,omitempty"`

	PaletteNotice   string `json:"palette_notice,omitempty"`
	ResourcesURL    string `json:"resources_url,omitempty"`
	InstalledNotice string `json:"installed_notice,omitempty"`

	// Notices are the third-party notice files in the bundle, each once. Empty
	// unless a carried source requires one.
	Notices []Notice `json:"notices,omitempty"`
}

// parseManifest decodes and checks the document's own consistency, before any
// member has been compared against it.
func parseManifest(raw []byte) (Manifest, error) {
	manifest := Manifest{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("texturebundle: the bundle's %s is not readable: %w", ManifestName, err)
	}
	if !acceptedSchema(manifest.SchemaVersion) {
		return Manifest{}, fmt.Errorf(
			"texturebundle: this bundle announces %q and this Companion reads %q and %q",
			manifest.SchemaVersion, Schema, SchemaV11)
	}
	if strings.TrimSpace(manifest.MapID) == "" {
		return Manifest{}, fmt.Errorf("texturebundle: the bundle's manifest names no map")
	}
	if manifest.CompilerReady && len(manifest.CompilerRefusals) > 0 {
		return Manifest{}, fmt.Errorf(
			"texturebundle: the bundle calls itself compiler-ready and lists %d refusal(s); it cannot be both",
			len(manifest.CompilerRefusals))
	}
	for index, file := range manifest.Files {
		if err := checkDeclaredFile(index, file); err != nil {
			return Manifest{}, err
		}
	}
	for _, requirement := range manifest.Requirements {
		for index, file := range requirement.Files {
			if err := checkDeclaredFile(index, file); err != nil {
				return Manifest{}, err
			}
		}
	}
	notices, err := declaredNotices(manifest)
	if err != nil {
		return Manifest{}, err
	}
	for _, requirement := range manifest.Requirements {
		if err := checkCarried(requirement, notices); err != nil {
			return Manifest{}, err
		}
	}

	return manifest, nil
}

// checkCarried refuses a requirement whose claim about being carried is not
// backed by a declared member.
//
// `included: true` is a flag in a document that arrived over a network. On its
// own it proves nothing, and treating it as proof is how a forged manifest
// would get a compiler started on bytes nobody verified — or on no bytes at
// all. So the flag must agree with the files: a carried requirement names at
// least one member (each of which the extractor then hashes), and a withheld
// one names none.
//
// An INSTALLED source is held to more, because its bytes are sent only under a
// declaration: it is carried only with a `redistribution` decision of
// `included` whose digest and size are those of a member the requirement names,
// with the credit the declaration states and at least one notice, every one of
// which the manifest lists. That is exactly what AUB writes for a carried
// installed source; anything less is not one.
func checkCarried(requirement Requirement, notices map[string]Notice) error {
	if requirement.Included && len(requirement.Files) == 0 {
		return fmt.Errorf(
			"texturebundle: requirement %q says it is included and names no file; a flag is not the bytes",
			requirement.Name)
	}
	if !requirement.Included && len(requirement.Files) > 0 {
		return fmt.Errorf(
			"texturebundle: requirement %q says it is not included and names %d file(s)",
			requirement.Name, len(requirement.Files))
	}
	verdict := requirement.Redistribution
	if verdict == nil {
		if requirement.Included && requirement.Origin == OriginInstalled {
			return fmt.Errorf(
				"texturebundle: requirement %q is an installed source carried with no redistribution decision",
				requirement.Name)
		}

		return nil
	}
	switch verdict.Decision {
	case DecisionWithheld:
		if requirement.Included {
			return fmt.Errorf(
				"texturebundle: requirement %q is withheld by its redistribution decision and says it is included",
				requirement.Name)
		}

		return nil
	case DecisionIncluded:
	default:
		return fmt.Errorf("texturebundle: requirement %q carries the redistribution decision %q, which is not one",
			requirement.Name, verdict.Decision)
	}
	if !requirement.Included {
		return fmt.Errorf(
			"texturebundle: requirement %q has a redistribution decision of included and is not in the bundle",
			requirement.Name)
	}
	if !isSHA256(verdict.SHA256) {
		return fmt.Errorf("texturebundle: requirement %q's redistribution decision names %q, which is not a SHA-256",
			requirement.Name, verdict.SHA256)
	}
	matched := false
	for _, file := range requirement.Files {
		if strings.EqualFold(file.SHA256, verdict.SHA256) && file.Bytes == verdict.Bytes {
			matched = true
		}
	}
	if !matched {
		return fmt.Errorf(
			"texturebundle: requirement %q's redistribution decision is for %s (%d bytes), which is not a file it names",
			requirement.Name, verdict.SHA256, verdict.Bytes)
	}
	if strings.TrimSpace(verdict.Credit) == "" {
		return fmt.Errorf("texturebundle: requirement %q is carried under a declaration that states no credit",
			requirement.Name)
	}
	if len(verdict.NoticePaths) == 0 {
		return fmt.Errorf("texturebundle: requirement %q is carried under a declaration that names no notice",
			requirement.Name)
	}
	for _, path := range verdict.NoticePaths {
		if _, listed := notices[path]; !listed {
			return fmt.Errorf(
				"texturebundle: requirement %q requires the notice %q, which the manifest's notices do not list",
				requirement.Name, path)
		}
	}

	return nil
}

// declaredNotices checks the manifest's notice list and keys it by path.
//
// A notice path becomes a path on this machine, so its SPELLING is checked
// here, before the archive is consulted: exactly `NOTICES/<one plain name>`.
// `NOTICES/../x`, `/etc/x`, `NOTICES/a/b` and a bare `x` are all refused — the
// last because a notice at the bundle root would be indistinguishable from a
// payload file.
func declaredNotices(manifest Manifest) (map[string]Notice, error) {
	if len(manifest.Notices) > MaxNotices {
		return nil, fmt.Errorf("texturebundle: the manifest lists %d notices and the limit is %d",
			len(manifest.Notices), MaxNotices)
	}
	out := make(map[string]Notice, len(manifest.Notices))
	folded := map[string]bool{}
	for _, notice := range manifest.Notices {
		name, inside := strings.CutPrefix(notice.Path, NoticesDir+"/")
		switch {
		case !inside || !noticeName.MatchString(name) || name == "." || name == "..":
			return nil, fmt.Errorf(
				"texturebundle: the notice path %q is not %s/<file name>", notice.Path, NoticesDir)
		case !isSHA256(notice.SHA256):
			return nil, fmt.Errorf("texturebundle: the notice %s declares %q, which is not a SHA-256",
				notice.Path, notice.SHA256)
		case notice.Bytes < 0 || notice.Bytes > MaxNoticeBytes:
			return nil, fmt.Errorf("texturebundle: the notice %s declares %d bytes and the limit is %d",
				notice.Path, notice.Bytes, MaxNoticeBytes)
		case folded[foldName(notice.Path)]:
			return nil, fmt.Errorf("texturebundle: the manifest lists the notice %s twice", notice.Path)
		}
		folded[foldName(notice.Path)] = true
		out[notice.Path] = notice
	}
	// One member is one thing. A path that is both a payload file and a notice
	// would be verified as whichever was read first and used as the other.
	for _, file := range manifest.Files {
		if folded[foldName(file.Path)] {
			return nil, fmt.Errorf("texturebundle: the manifest declares %s both as a file and as a notice", file.Path)
		}
	}

	return out, nil
}

func checkDeclaredFile(index int, file File) error {
	switch {
	case strings.TrimSpace(file.Path) == "":
		return fmt.Errorf("texturebundle: declared file %d has no path", index)
	case file.Bytes < 0:
		return fmt.Errorf("texturebundle: %s declares %d bytes", file.Path, file.Bytes)
	case !isSHA256(file.SHA256):
		return fmt.Errorf("texturebundle: %s declares %q, which is not a SHA-256", file.Path, file.SHA256)
	}

	return nil
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)

	return err == nil
}

// declaredFiles collapses the manifest's file list to one entry per path.
//
// The same member may be declared by two requirements — two WAD names resolving
// to byte-identical bytes is a real map, and AUB writes the member once and
// names it from both. That is legitimate and is preserved. Two declarations of
// one path that DISAGREE about its digest or size are not, and are refused
// here, because a later check would otherwise pass against whichever one it
// happened to read.
func declaredFiles(manifest Manifest) (map[string]File, error) {
	out := map[string]File{}
	for _, file := range manifest.Files {
		previous, seen := out[file.Path]
		if seen && (!strings.EqualFold(previous.SHA256, file.SHA256) || previous.Bytes != file.Bytes) {
			return nil, fmt.Errorf(
				"texturebundle: the manifest declares %s twice, with different bytes (%s/%d and %s/%d)",
				file.Path, previous.SHA256, previous.Bytes, file.SHA256, file.Bytes)
		}
		out[file.Path] = file
	}
	// Every file a requirement names must be in the top-level list too:
	// `files` is the flat form a reader acts on, and a member reachable only
	// through a requirement would be a member nothing verified.
	for _, requirement := range manifest.Requirements {
		for _, file := range requirement.Files {
			declared, present := out[file.Path]
			if !present {
				return nil, fmt.Errorf(
					"texturebundle: requirement %q names %s, which the manifest's file list does not declare",
					requirement.Name, file.Path)
			}
			if !strings.EqualFold(declared.SHA256, file.SHA256) || declared.Bytes != file.Bytes {
				return nil, fmt.Errorf(
					"texturebundle: requirement %q and the file list disagree about %s",
					requirement.Name, file.Path)
			}
		}
	}

	return out, nil
}

// OrderedWADs is the declaration, in the map's own order, paired with the
// archive members each declaration resolved to.
//
// It is what the review page shows and what the staged `wads` directory is
// built from. The order is the manifest's and is never re-sorted.
type OrderedWAD struct {
	Order    int    `json:"order"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Origin   string `json:"origin,omitempty"`
	Included bool   `json:"included"`
	Note     string `json:"note,omitempty"`
	Files    []File `json:"files"`

	Revision       int             `json:"revision,omitempty"`
	SourceSHA256   string          `json:"source_sha256,omitempty"`
	Redistribution *Redistribution `json:"redistribution,omitempty"`
}

// OrderedWADs reads the requirements in declaration order.
func (m Manifest) OrderedWADs() []OrderedWAD {
	out := make([]OrderedWAD, 0, len(m.Requirements))
	for index, requirement := range m.Requirements {
		order := requirement.Order
		// `order` is authoritative when AUB set it; the array index is the
		// fallback, and they agree in every bundle AUB writes.
		if order == 0 && index > 0 {
			order = index
		}
		out = append(out, OrderedWAD{
			Order: order, Name: requirement.Name, Status: requirement.Status,
			Origin: requirement.Origin, Included: requirement.Included,
			Note: requirement.Note, Files: requirement.Files,
			Revision: requirement.Revision, SourceSHA256: requirement.SourceSHA256,
			Redistribution: requirement.Redistribution,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })

	return out
}

// Digest is the identity of a bundle's bytes.
func Digest(bundle []byte) string {
	sum := sha256.Sum256(bundle)

	return hex.EncodeToString(sum[:])
}
