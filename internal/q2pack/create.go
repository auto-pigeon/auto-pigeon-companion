package q2pack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
)

// RecordSchemaVersion versions [Record].
const RecordSchemaVersion = "aucom.q2-package/1.0"

// RecordFileName is the record written beside an archive in its own directory.
const RecordFileName = "q2-package.json"

// ErrBlocked reports a plan that may not be written as it stands.
var ErrBlocked = errors.New("q2pack: the package is held")

// Acceptance is somebody saying, in writing, that the archive may be written
// without something an engine needs. Never a way to skip the review: it is
// printed back and recorded with what it covered.
type Acceptance struct {
	Reason     string   `json:"reason"`
	NotCarried []string `json:"not_carried"`
}

// Options is what writing needs beyond the plan.
type Options struct {
	// Dir is where the archive, its `internal/pack` manifest and the record go.
	Dir       string
	Companion string
	// AcceptReason accepts what a reason may accept. Empty accepts nothing.
	AcceptReason string
	Now          func() time.Time
	// Manifest is the build, for the provenance `internal/pack` records.
	Manifest *build.Manifest
}

// Record is one package this program wrote.
type Record struct {
	SchemaVersion string    `json:"schema_version"`
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Companion     string    `json:"companion,omitempty"`
	Plan          *Plan     `json:"plan"`
	// Archive is the file by name, size and digest; Entries are its members as
	// they were read back out of the written file.
	Archive    pack.ArchiveRef `json:"archive"`
	Entries    []pack.Entry    `json:"entries"`
	Grants     []Grant         `json:"grants,omitempty"`
	Acceptance *Acceptance     `json:"acceptance,omitempty"`
	Complete   bool            `json:"complete"`

	Directory    string `json:"directory,omitempty"`
	ArchivePath  string `json:"archive_path,omitempty"`
	ManifestPath string `json:"manifest_path,omitempty"`
}

// Member is the archive's entry for a path, as it was read back.
func (r *Record) Member(path string) (pack.Entry, bool) {
	for _, entry := range r.Entries {
		if strings.EqualFold(entry.Path, path) {
			return entry, true
		}
	}
	return pack.Entry{}, false
}

// Create writes a plan as an archive and reads it back before saying it did.
func Create(plan *Plan, options Options) (*Record, error) {
	if plan == nil || plan.packPlan == nil {
		return nil, errors.New("q2pack: nothing was planned")
	}
	reason := strings.TrimSpace(options.AcceptReason)
	if err := plan.Blocked(reason != ""); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBlocked, err)
	}
	if options.Dir == "" {
		return nil, errors.New("q2pack: no directory to write the package into")
	}
	if err := os.MkdirAll(options.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("q2pack: creating %s: %w", options.Dir, err)
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	var buildRef *pack.BuildRef
	var tools []pack.ToolRef
	if options.Manifest != nil {
		buildRef, tools, _ = build.PackageRefs(options.Manifest)
	}
	var authorized []string
	for _, member := range plan.Members {
		if member.Disposition != BuildOutput {
			authorized = append(authorized, member.Path)
		}
	}
	sort.Strings(authorized)
	result, err := pack.Create(plan.packPlan, pack.Options{
		Output: filepath.Join(options.Dir, plan.ArchiveName), Label: plan.Map.Name, Companion: options.Companion,
		Build: buildRef, Tools: tools, Now: options.Now,
		Review: pack.ReviewRecord{Authorized: authorized, Reason: grantsSummary(plan.Grants)},
	})
	if errors.Is(err, pack.ErrWouldOverwrite) {
		return nil, fmt.Errorf("%w: %s already holds %s. A map package is never written over",
			pack.ErrWouldOverwrite, options.Dir, plan.ArchiveName)
	}
	if err != nil {
		return nil, fmt.Errorf("q2pack: %w", err)
	}
	// The sidecar is a public file beside a public archive. `internal/pack`
	// records where each member was read from on this machine, which is a
	// path into somebody's home folder and nobody else's business.
	if err := strip(result.Manifest, result.ManifestPath); err != nil {
		return nil, err
	}
	entries, err := readBack(result.Archive, plan)
	if err != nil {
		os.Remove(result.Archive)
		os.Remove(result.ManifestPath)
		return nil, err
	}
	record := &Record{
		SchemaVersion: RecordSchemaVersion,
		ID:            RecordID(plan.ArchiveName, result.Manifest.Archive.SHA256),
		CreatedAt:     now().UTC(),
		Companion:     options.Companion,
		Plan:          plan,
		Archive:       result.Manifest.Archive,
		Entries:       entries,
		Grants:        plan.Grants,
		Complete:      len(plan.NotCarried) == 0,
		Directory:     options.Dir,
		ArchivePath:   result.Archive,
		ManifestPath:  result.ManifestPath,
	}
	if len(plan.NotCarried) > 0 || reason != "" && len(plan.Problems) > 0 {
		record.Acceptance = &Acceptance{Reason: reason, NotCarried: append([]string{}, plan.NotCarried...)}
	}
	if err := record.save(filepath.Join(options.Dir, RecordFileName)); err != nil {
		return nil, err
	}
	return record, nil
}

func strip(manifest *pack.Manifest, path string) error {
	for i := range manifest.Contents {
		manifest.Contents[i].Source = ""
	}
	for i := range manifest.Excluded {
		manifest.Excluded[i].Source = ""
	}
	if err := manifest.Save(path); err != nil {
		return fmt.Errorf("q2pack: rewriting the package manifest: %w", err)
	}
	return nil
}

// readBack opens the written archive as an engine would read its directory and
// re-reads every member whole: every planned member is there once, at its
// size and digest, and nothing else is.
func readBack(archive string, plan *Plan) ([]pack.Entry, error) {
	verification, err := pack.Verify(archive, pack.FormatPAK, pack.Budget{})
	if err != nil {
		return nil, failure.As(failure.ArchiveDamaged, fmt.Errorf("q2pack: reading back %s: %w", filepath.Base(archive), err))
	}
	if len(verification.Problems) > 0 {
		return nil, failure.As(failure.ArchiveDamaged, fmt.Errorf("q2pack: %s did not read back: %s",
			filepath.Base(archive), strings.Join(verification.Problems, "; ")))
	}
	if len(verification.Entries) != len(plan.Members) {
		return nil, failure.As(failure.ArchiveDamaged, fmt.Errorf("q2pack: %s holds %d members and %d were planned",
			filepath.Base(archive), len(verification.Entries), len(plan.Members)))
	}
	planned := map[string]Member{}
	for _, member := range plan.Members {
		planned[member.Path] = member
	}
	for _, entry := range verification.Entries {
		member, ok := planned[entry.Path]
		if !ok || member.Size != entry.Size || !strings.EqualFold(member.SHA256, entry.SHA256) {
			return nil, failure.As(failure.ArchiveDamaged, fmt.Errorf(
				"q2pack: %s's member %s is not what was planned", filepath.Base(archive), entry.Path))
		}
	}
	return verification.Entries, nil
}

// RecordID names a package by its archive and the first bytes of its digest.
func RecordID(archiveName, digest string) string {
	stem := strings.TrimSuffix(archiveName, filepath.Ext(archiveName))
	digest = strings.TrimPrefix(strings.ToLower(digest), "sha256:")
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return "q2-" + stem + "-" + digest
}

func grantsSummary(grants []Grant) string {
	parts := make([]string, 0, len(grants))
	for _, grant := range grants {
		if grant.Basis == BasisNotRedistributable {
			continue
		}
		target := "the content folder's loose files"
		if grant.Path != "" {
			target = grant.Path
		}
		sentence := "own work"
		if grant.Basis == BasisLicensed {
			sentence = "licensed under " + grant.Licence
		}
		parts = append(parts, target+": "+sentence)
	}
	return strings.Join(parts, "; ")
}

func (r *Record) save(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("q2pack: encoding the package record: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("q2pack: writing the package record: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("q2pack: writing the package record: %w", err)
	}
	return file.Close()
}

// LoadRecord reads a package's record and checks it against the archive beside
// it: an archive edited, replaced or truncated after it was written is not the
// package the record describes.
func LoadRecord(dir string) (*Record, error) {
	data, err := os.ReadFile(filepath.Join(dir, RecordFileName))
	if err != nil {
		return nil, fmt.Errorf("q2pack: %s holds no Quake II package this program wrote: %w", dir, err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("q2pack: the package record in %s does not parse: %w", dir, err)
	}
	if record.SchemaVersion != RecordSchemaVersion {
		return nil, fmt.Errorf("q2pack: the package record in %s is %q, and this build reads %q",
			dir, record.SchemaVersion, RecordSchemaVersion)
	}
	if record.Plan == nil || record.Archive.File == "" || record.Archive.File != filepath.Base(record.Archive.File) {
		return nil, fmt.Errorf("q2pack: the package record in %s names no archive", dir)
	}
	archive := filepath.Join(dir, record.Archive.File)
	digest, size, err := hashFile(archive)
	if err != nil {
		return nil, failure.As(failure.ArchiveDamaged, fmt.Errorf("q2pack: the package's archive: %w", err))
	}
	if size != record.Archive.Size || !strings.EqualFold(digest, record.Archive.SHA256) {
		return nil, failure.As(failure.ArchiveDamaged, fmt.Errorf(
			"q2pack: %s is not the archive its record describes (the record says %d bytes, %s; the file is %d bytes, %s). Create the package again",
			record.Archive.File, record.Archive.Size, record.Archive.SHA256, size, digest))
	}
	record.Directory, record.ArchivePath = dir, archive
	record.ManifestPath = pack.ManifestPathFor(archive)
	return &record, nil
}

// Store keeps the packages this program wrote, one directory each.
type Store struct {
	Dir string
}

// Create writes a package into the store. The same archive twice lands on the
// same id and is compared with the one already there, never written over it.
func (s Store) Create(plan *Plan, options Options) (*Record, bool, error) {
	if s.Dir == "" {
		return nil, false, errors.New("q2pack: no package store")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, false, fmt.Errorf("q2pack: creating %s: %w", s.Dir, err)
	}
	staging, err := os.MkdirTemp(s.Dir, ".writing-")
	if err != nil {
		return nil, false, fmt.Errorf("q2pack: preparing the package store: %w", err)
	}
	defer os.RemoveAll(staging)
	options.Dir = staging
	record, err := Create(plan, options)
	if err != nil {
		return nil, false, err
	}
	final := filepath.Join(s.Dir, record.ID)
	if existing, err := LoadRecord(final); err == nil {
		return existing, true, nil
	} else if _, statErr := os.Stat(final); statErr == nil {
		if failure.Of(err) != failure.ArchiveDamaged {
			return nil, false, fmt.Errorf("q2pack: %s is already in the package store and is not a package this "+
				"program can read (%v). It was not written over", record.ID, err)
		}
		// Its archive is no longer the bytes its record names (Q2_009, found
		// live: the refusal said "create the package again", and creating it
		// again was refused the same way). The damaged copy is kept, renamed
		// aside where nothing lists or installs it, and the package is
		// written fresh in its place.
		aside := filepath.Join(s.Dir, ".damaged-"+record.ID+"-"+filepath.Base(staging))
		if err := os.Rename(final, aside); err != nil {
			return nil, false, fmt.Errorf("q2pack: moving the damaged %s aside: %w", record.ID, err)
		}
	}
	if err := os.Rename(staging, final); err != nil {
		return nil, false, fmt.Errorf("q2pack: publishing the package: %w", err)
	}
	record.Directory = final
	record.ArchivePath = filepath.Join(final, record.Archive.File)
	record.ManifestPath = pack.ManifestPathFor(record.ArchivePath)
	return record, false, nil
}

// Get reads one package by id.
func (s Store) Get(id string) (*Record, error) {
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return nil, fmt.Errorf("q2pack: %q is not a package id", id)
	}
	return LoadRecord(filepath.Join(s.Dir, id))
}

// List reads every package in the store, newest first.
func (s Store) List() ([]*Record, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("q2pack: reading the package store: %w", err)
	}
	var out []*Record
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if record, err := LoadRecord(filepath.Join(s.Dir, entry.Name())); err == nil {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
