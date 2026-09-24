package pack

import (
	"encoding/json"
	"fmt"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The sidecar, and why it is a sidecar.
//
// A package manifest records what went into an archive, where each file came
// from, which tools produced it and what was decided about the ones whose
// provenance was not obvious. All of that is information about the package, and
// none of it is information the game wants: an engine walking a PAK looking for
// `progs.dat` has no use for `manifest.json`, and a PK3 with an `aucom/`
// directory in it publishes the packager's toolchain to everyone who downloads
// the map.
//
// So it goes beside the archive, as `<name>.package.json`, and it goes *inside*
// only when the target says so — which no built-in target does. That is
// [Target.AllowsMetadata], and the rule is enforced in [Create] rather than
// left as a convention.
//
// It is versioned by name and refused rather than half-read, for the reason
// `internal/build`'s manifest gives: it travels, and it may be read by a build
// of the Companion that is not the one that wrote it.

// ManifestSchemaVersion versions the package manifest.
const ManifestSchemaVersion = "aucom.package-manifest/1.0"

// ManifestSuffix is appended to the archive's name to get the sidecar's.
const ManifestSuffix = ".package.json"

// DocumentRef identifies one profile document exactly. It mirrors the build
// manifest's shape deliberately: a person comparing the two should not have to
// translate.
type DocumentRef struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Name    string `json:"name,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// ToolRef is one tool that produced content in this package, copied from the
// build manifest that recorded it.
//
// Copied rather than referenced: a package is published and a build directory
// is not, so a reader who has the archive and the sidecar has to be able to
// answer "what compiled this" without the machine it was compiled on.
type ToolRef struct {
	Profile DocumentRef `json:"profile"`
	// ToolVersion is the upstream version the profile describes.
	ToolVersion string `json:"tool_version,omitempty"`
	// Executables are the digests of the programs that actually ran.
	Executables []ExecutableRef `json:"executables,omitempty"`
}

// ExecutableRef is one program, by digest.
type ExecutableRef struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256,omitempty"`
}

// BuildRef ties a package to the build that produced its contents.
type BuildRef struct {
	BuildID  string      `json:"build_id,omitempty"`
	Pipeline DocumentRef `json:"pipeline,omitempty"`
	// ReproducibleKey is `internal/build`'s digest over the recipe. Carried
	// here so that "was this built the same way" is answerable from the
	// package alone.
	ReproducibleKey string `json:"reproducible_key,omitempty"`
	Platform        string `json:"platform,omitempty"`
}

// ArchiveRef is the artifact this manifest is about.
type ArchiveRef struct {
	File   string `json:"file"`
	Format Format `json:"format"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// Entries and TotalSize are what the archive holds, so a reader can check
	// the sidecar against the archive without opening the archive.
	Entries   int   `json:"entries"`
	TotalSize int64 `json:"total_size"`
}

// ReviewRecord is what a person decided about the files whose provenance the
// policy could not establish.
//
// An empty record on a package that contained unknown files is not possible:
// [Create] refuses one. That is the whole mechanism — the record exists because
// the refusal exists, and a manifest with acknowledgements in it is evidence
// that somebody was asked.
type ReviewRecord struct {
	// Acknowledged are paths a person confirmed after being shown the listing.
	Acknowledged []string `json:"acknowledged,omitempty"`
	// Authorized are paths that matched a known proprietary asset and were
	// included anyway, on an explicit assertion of the right to distribute
	// them.
	Authorized []string `json:"authorized,omitempty"`
	// Reason is what the person typed. Required for an authorization, and
	// recorded verbatim: this is the sentence somebody has to stand behind.
	Reason string `json:"reason,omitempty"`
	// Reviewer is who did it, when the caller knows.
	Reviewer string `json:"reviewer,omitempty"`
}

// Manifest is the whole record of one package.
type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	// Companion is the build of this program that wrote it.
	Companion string    `json:"companion,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Label is the caller's short name for this package.
	Label string `json:"label,omitempty"`

	Target  TargetRef  `json:"target"`
	Archive ArchiveRef `json:"archive"`

	Build *BuildRef `json:"build,omitempty"`
	Tools []ToolRef `json:"tools,omitempty"`

	// Contents is every member, with the decision that let it in.
	Contents []Decision   `json:"contents"`
	Review   ReviewRecord `json:"review,omitempty"`

	// MetadataEmbedded says whether a copy of this manifest was written into
	// the archive itself, and where. False on every built-in target.
	MetadataEmbedded bool   `json:"metadata_embedded"`
	MetadataPath     string `json:"metadata_path,omitempty"`

	// Excluded is every candidate that was offered and not packaged, with the
	// reason. A package is defined as much by what was left out, and a listing
	// that only shows what got in cannot be reviewed.
	Excluded []Decision `json:"excluded,omitempty"`
}

// TargetRef is the packaging policy this archive was written under, recorded so
// that a reader never has to infer the compression or the reproducibility
// promise from the bytes.
type TargetRef struct {
	ID           string          `json:"id"`
	Title        string          `json:"title,omitempty"`
	Format       Format          `json:"format"`
	Compression  Compression     `json:"compression"`
	Reproducible Reproducibility `json:"reproducibility"`
	// ReproducibilityNote is the promise in words, because "per_build" is a
	// token and the sentence it stands for is what a reader actually needs.
	ReproducibilityNote string `json:"reproducibility_note,omitempty"`
}

// TargetRefOf describes a target for the manifest.
func TargetRefOf(t Target) TargetRef {
	ref := TargetRef{
		ID: t.ID, Title: t.Title, Format: t.Format,
		Compression: t.Compression, Reproducible: t.Reproducibility(),
	}
	switch ref.Reproducible {
	case Portable:
		ref.ReproducibilityNote = "the same members produce the same bytes on any machine and under any build of the Companion: " +
			"this container carries no timestamp, no permission bits and no compressor"
	case PerBuild:
		ref.ReproducibilityNote = "the same members produce the same bytes under this build of the Companion on any operating system, " +
			"and may differ under another build: the deflate encoder belongs to the Go standard library, not to this program"
	}
	return ref
}

// ManifestPathFor is the sidecar's path for an archive.
func ManifestPathFor(archivePath string) string { return archivePath + ManifestSuffix }

// Save writes the manifest beside its archive, staged and then renamed.
func (m *Manifest) Save(path string) error {
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("pack: encoding the package manifest: %w", err)
	}
	temporary := path + ".writing"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("pack: writing %s: %w", path, err)
	}
	if err := fsshare.Replace(temporary, path); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("pack: publishing %s: %w", path, err)
	}
	return nil
}

// Encode renders the manifest as it is written, for embedding.
func (m *Manifest) Encode() ([]byte, error) {
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("pack: encoding the package manifest: %w", err)
	}
	return append(encoded, '\n'), nil
}

// LoadManifest reads one sidecar.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pack: reading %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("pack: %s is not a readable package manifest: %w", path, err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return nil, fmt.Errorf("pack: %s is %q; this build reads %q", path, m.SchemaVersion, ManifestSchemaVersion)
	}
	return &m, nil
}

// findManifest looks for the sidecar beside an archive. A missing one is not an
// error: an archive somebody else made will not have one.
func findManifest(archivePath string) (*Manifest, string, error) {
	path := ManifestPathFor(archivePath)
	if _, err := os.Stat(path); err != nil {
		return nil, "", err
	}
	manifest, err := LoadManifest(path)
	if err != nil {
		return nil, path, err
	}
	return manifest, path, nil
}

// Disagreements compares a manifest against the archive it claims to describe.
//
// Every line it returns is a sentence a person can act on, and the comparison
// is deliberately whole rather than short-circuiting on the first mismatch: an
// archive that has been rebuilt has one difference and an archive that has been
// tampered with usually has several, and telling those apart is the reason to
// run this at all.
func (m *Manifest) Disagreements(archive *Inspection) []string {
	var issues []string
	if base := filepath.Base(archive.Path); m.Archive.File != "" && m.Archive.File != base {
		issues = append(issues, fmt.Sprintf("the manifest describes %q and this file is %q", m.Archive.File, base))
	}
	if m.Archive.Format != "" && m.Archive.Format != archive.Format {
		issues = append(issues, fmt.Sprintf("the manifest says %s and this is a %s", m.Archive.Format, archive.Format))
	}
	if m.Archive.Size != archive.Size {
		issues = append(issues, fmt.Sprintf("the manifest says %d bytes and this is %d", m.Archive.Size, archive.Size))
	}
	if m.Archive.SHA256 != "" && m.Archive.SHA256 != archive.SHA256 {
		issues = append(issues, fmt.Sprintf("the manifest's digest is %s and this file's is %s", m.Archive.SHA256, archive.SHA256))
	}

	declared := map[string]Decision{}
	for _, entry := range m.Contents {
		declared[entry.Path] = entry
	}
	present := map[string]Entry{}
	for _, entry := range archive.Entries {
		present[entry.Path] = entry
	}
	var missing, added []string
	for path := range declared {
		if _, ok := present[path]; !ok {
			missing = append(missing, path)
		}
	}
	for path, entry := range present {
		want, ok := declared[path]
		if !ok {
			added = append(added, path)
			continue
		}
		if want.Size != entry.Size {
			issues = append(issues, fmt.Sprintf("%s: the manifest says %d bytes and the archive holds %d", path, want.Size, entry.Size))
		}
		// Only when the archive was verified: inspection leaves digests empty
		// on purpose, and comparing against an empty one would report every
		// member of every inspected archive.
		if entry.SHA256 != "" && want.SHA256 != "" && want.SHA256 != entry.SHA256 {
			issues = append(issues, fmt.Sprintf("%s: the manifest's digest is %s and the archive's is %s", path, want.SHA256, entry.SHA256))
		}
	}
	sort.Strings(missing)
	sort.Strings(added)
	for _, path := range missing {
		issues = append(issues, fmt.Sprintf("%s is in the manifest and not in the archive", path))
	}
	for _, path := range added {
		issues = append(issues, fmt.Sprintf("%s is in the archive and not in the manifest", path))
	}
	return issues
}

// Describe is the human summary printed above a package listing.
func (m *Manifest) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %d members, %s\n", m.Archive.File, m.Target.ID, m.Archive.Entries, humanSize(m.Archive.Size))
	fmt.Fprintf(&b, "  reproducibility: %s — %s\n", m.Target.Reproducible, m.Target.ReproducibilityNote)
	if m.Build != nil && m.Build.BuildID != "" {
		fmt.Fprintf(&b, "  built by:        %s (%s)\n", m.Build.BuildID, m.Build.Pipeline.ID)
	}
	if len(m.Review.Acknowledged) > 0 || len(m.Review.Authorized) > 0 {
		fmt.Fprintf(&b, "  reviewed:        %d acknowledged, %d authorized\n",
			len(m.Review.Acknowledged), len(m.Review.Authorized))
	}
	return b.String()
}
