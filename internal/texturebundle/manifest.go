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
// that size and digest, nothing undeclared is in the archive except the two
// contracted documents, and the manifest's own `map_id` and `revision` are the
// ones the caller asked for. Until all of that passes, the extraction lives in
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
	"sort"
	"strings"
)

// Schema is the manifest version this package reads.
//
// A bundle announcing a different schema is refused rather than parsed
// optimistically: the fields this package acts on — the digests, the
// declaration order, `compiler_ready` — are exactly the ones a silent schema
// change would move.
const Schema = "aub-map-texture-export/1.1"

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

	// Included says whether this source's original files are in the bundle.
	// False is not an error on its own — an installed source is named rather
	// than redistributed — and it is one of the things `compiler_ready` weighs.
	Included bool   `json:"included"`
	Note     string `json:"note,omitempty"`

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
}

// parseManifest decodes and checks the document's own consistency, before any
// member has been compared against it.
func parseManifest(raw []byte) (Manifest, error) {
	manifest := Manifest{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("texturebundle: the bundle's %s is not readable: %w", ManifestName, err)
	}
	if manifest.SchemaVersion != Schema {
		return Manifest{}, fmt.Errorf(
			"texturebundle: this bundle announces %q and this Companion reads %q",
			manifest.SchemaVersion, Schema)
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

	return manifest, nil
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
