package q3pack

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
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
)

// RecordSchemaVersion versions [Record].
const RecordSchemaVersion = "aucom.q3-package/1.0"

// RecordFileName is the record written beside an archive in its own directory.
const RecordFileName = "q3-package.json"

// Acceptance is somebody saying, in writing, that the archive may be written
// without something an engine needs.
//
// It is `--accept-missing --reason` for this package (`aucom.quake3-maturity`,
// rule 4): never a way to skip the review, always printed back, and recorded
// with the list it covered so that a package found later says what its author
// knew it lacked.
type Acceptance struct {
	Reason string `json:"reason"`
	// NotCarried is what was accepted: the files an engine needs and this
	// archive does not have.
	NotCarried []string `json:"not_carried"`
}

// Options is what writing needs beyond the plan.
type Options struct {
	// Dir is the directory the archive, its `internal/pack` manifest and its
	// record are written into. It is created, and the archive is never written
	// over one that is already there.
	Dir string
	// Companion is the build of this program, for the record.
	Companion string
	// AcceptReason accepts the dependencies the archive will not carry. Empty
	// means none are accepted, and a plan with any is refused.
	AcceptReason string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Record is one package this program wrote.
type Record struct {
	SchemaVersion string    `json:"schema_version"`
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Companion     string    `json:"companion,omitempty"`
	// Plan is the review the archive was written from.
	Plan *Plan `json:"plan"`
	// Archive is the file, by name, size and digest.
	Archive pack.ArchiveRef `json:"archive"`
	// Grants are the answers the archive was written under, verbatim.
	Grants     []Grant     `json:"grants,omitempty"`
	Acceptance *Acceptance `json:"acceptance,omitempty"`
	// Complete is false when the archive lacks something an engine needs and
	// the base game does not supply.
	Complete bool `json:"complete"`

	// Directory, ArchivePath and ManifestPath are where it is on this machine.
	Directory    string `json:"directory,omitempty"`
	ArchivePath  string `json:"archive_path,omitempty"`
	ManifestPath string `json:"manifest_path,omitempty"`
}

// ErrBlocked reports a plan that may not be written as it stands.
var ErrBlocked = errors.New("q3pack: the package is held")

// Create writes a prepared plan as an archive, with `internal/pack`'s manifest
// and this package's record beside it.
func Create(prepared *Prepared, options Options) (*Record, error) {
	if prepared == nil || prepared.packPlan == nil {
		return nil, errors.New("q3pack: nothing was prepared")
	}
	plan := prepared.Plan
	reason := strings.TrimSpace(options.AcceptReason)
	if err := plan.Blocked(reason != ""); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBlocked, err)
	}
	if options.Dir == "" {
		return nil, errors.New("q3pack: no directory to write the package into")
	}
	if err := os.MkdirAll(options.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("q3pack: creating %s: %w", options.Dir, err)
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}

	buildRef, tools, _ := build.PackageRefs(prepared.manifest)
	var authorized []string
	for _, member := range plan.Members {
		if member.Disposition != BuildOutput {
			authorized = append(authorized, member.Path)
		}
	}
	sort.Strings(authorized)
	result, err := pack.Create(prepared.packPlan, pack.Options{
		Output:    filepath.Join(options.Dir, plan.ArchiveName),
		Label:     plan.Map.Name,
		Companion: options.Companion,
		Build:     buildRef,
		Tools:     tools,
		Now:       options.Now,
		Review:    pack.ReviewRecord{Authorized: authorized, Reason: grantsSummary(prepared.grants)},
	})
	if err != nil {
		return nil, fmt.Errorf("q3pack: %w", err)
	}

	record := &Record{
		SchemaVersion: RecordSchemaVersion,
		ID:            RecordID(plan.ArchiveName, result.Manifest.Archive.SHA256),
		CreatedAt:     now().UTC(),
		Companion:     options.Companion,
		Plan:          plan,
		Archive:       result.Manifest.Archive,
		Grants:        prepared.grants,
		Complete:      len(plan.NotCarried) == 0,
		Directory:     options.Dir,
		ArchivePath:   result.Archive,
		ManifestPath:  result.ManifestPath,
	}
	if len(plan.NotCarried) > 0 {
		record.Acceptance = &Acceptance{Reason: reason, NotCarried: append([]string(nil), plan.NotCarried...)}
	}
	if err := record.save(filepath.Join(options.Dir, RecordFileName)); err != nil {
		return nil, err
	}
	return record, nil
}

// RecordID names a package by its archive and the first bytes of its digest,
// so the same bytes always have the same id and two different archives of one
// map never share one.
func RecordID(archiveName, digest string) string {
	stem := strings.TrimSuffix(archiveName, filepath.Ext(archiveName))
	digest = hexDigest(digest)
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return stem + "-" + digest
}

// grantsSummary is the grants as the `internal/pack` manifest's one reason.
func grantsSummary(grants []Grant) string {
	parts := make([]string, 0, len(grants))
	for _, grant := range grants {
		if grant.Basis == BasisNotRedistributable {
			continue
		}
		parts = append(parts, grantTarget(grant)+": "+grantSentence(&grant))
	}
	return strings.Join(parts, "; ")
}

func (r *Record) save(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("q3pack: encoding the package record: %w", err)
	}
	// Refused when one is there: the directory is this package's own, and a
	// record that could be written over is a record that can say something the
	// archive beside it does not.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("q3pack: writing the package record: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("q3pack: writing the package record: %w", err)
	}
	return file.Close()
}

// LoadRecord reads a package's record from its directory and checks it against
// the archive beside it.
//
// The check is the point. A record is a claim about a file; an archive that
// was edited, replaced or truncated after it was written is not the package the
// record describes, and installing it on the record's word would be installing
// something nobody reviewed.
func LoadRecord(dir string) (*Record, error) {
	data, err := os.ReadFile(filepath.Join(dir, RecordFileName))
	if err != nil {
		return nil, fmt.Errorf("q3pack: %s holds no Quake III package this program wrote: %w", dir, err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("q3pack: the package record in %s does not parse: %w", dir, err)
	}
	if record.SchemaVersion != RecordSchemaVersion {
		return nil, fmt.Errorf("q3pack: the package record in %s is %q, and this build reads %q",
			dir, record.SchemaVersion, RecordSchemaVersion)
	}
	if record.Plan == nil || record.Archive.File == "" || record.Archive.File != filepath.Base(record.Archive.File) {
		return nil, fmt.Errorf("q3pack: the package record in %s names no archive", dir)
	}
	archive := filepath.Join(dir, record.Archive.File)
	digest, size, err := hashFile(archive)
	if err != nil {
		return nil, fmt.Errorf("q3pack: the package's archive: %w", err)
	}
	if size != record.Archive.Size || digest != hexDigest(record.Archive.SHA256) {
		return nil, fmt.Errorf("q3pack: %s is not the archive its record describes: the record says %d bytes, "+
			"sha256 %s, and the file is %d bytes, sha256 %s. Create the package again",
			record.Archive.File, record.Archive.Size, hexDigest(record.Archive.SHA256), size, digest)
	}
	record.Directory, record.ArchivePath = dir, archive
	record.ManifestPath = pack.ManifestPathFor(archive)
	return &record, nil
}

// Store keeps the packages this program wrote, one directory each, named by
// [RecordID].
type Store struct {
	Dir string
}

// Create writes a package into the store.
//
// The archive is written into a directory of its own and that directory is
// then renamed to the package's id, which contains the archive's digest. So
// creating the same package twice lands on the same id — and the second one is
// compared with the first rather than written over it: the same bytes are the
// package that is already there, and anything else is refused.
func (s Store) Create(prepared *Prepared, options Options) (*Record, bool, error) {
	if s.Dir == "" {
		return nil, false, errors.New("q3pack: no package store")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, false, fmt.Errorf("q3pack: creating %s: %w", s.Dir, err)
	}
	staging, err := os.MkdirTemp(s.Dir, ".writing-")
	if err != nil {
		return nil, false, fmt.Errorf("q3pack: preparing the package store: %w", err)
	}
	defer os.RemoveAll(staging)
	options.Dir = staging
	record, err := Create(prepared, options)
	if err != nil {
		return nil, false, err
	}
	final := filepath.Join(s.Dir, record.ID)
	if existing, err := LoadRecord(final); err == nil {
		// The id carries the digest, so this is the same archive. Said rather
		// than assumed: LoadRecord has just re-hashed the one that is there.
		return existing, true, nil
	} else if _, statErr := os.Stat(final); statErr == nil {
		return nil, false, fmt.Errorf("q3pack: %s is already in the package store and is not a package this "+
			"program can read (%v). It was not written over", record.ID, err)
	}
	if err := os.Rename(staging, final); err != nil {
		return nil, false, fmt.Errorf("q3pack: publishing the package: %w", err)
	}
	record.Directory = final
	record.ArchivePath = filepath.Join(final, record.Archive.File)
	record.ManifestPath = pack.ManifestPathFor(record.ArchivePath)
	return record, false, nil
}

// Get reads one package by id.
func (s Store) Get(id string) (*Record, error) {
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return nil, fmt.Errorf("q3pack: %q is not a package id", id)
	}
	return LoadRecord(filepath.Join(s.Dir, id))
}

// List reads every package in the store, newest first. A directory that does
// not hold a readable package is skipped: listing is not the place a damaged
// one is reported, [Store.Get] is.
func (s Store) List() ([]*Record, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("q3pack: reading the package store: %w", err)
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
