package pack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Writing the archive.
//
// # Stage, then publish
//
// The archive is written into a temporary file in the destination directory and
// renamed into place at the end. Same directory, because a rename across
// filesystems is a copy and stops being atomic; at the end, because a PAK's
// header is patched after its directory is written and a reader who opened the
// file in between would see a header full of zeroes.
//
// What that buys is narrow and worth stating exactly: at no point does the
// destination path hold a partially written archive. It either does not exist,
// or it is the previous one, or it is this one. A build that is interrupted
// leaves a `.writing` file, which is removed on the way out and is obviously
// junk if the process died before that.
//
// # Never the input
//
// [Create] refuses to write over a file it is about to read. That is not a
// theoretical courtesy: `companion package create --from . --out map.pak` run
// twice in a directory that is also the destination is the ordinary way to do
// it, and the second run would otherwise sweep up the first run's output,
// package the archive inside itself, and then overwrite it. The check is on the
// resolved paths of every selected source, so a symlink or a relative path
// cannot get around it.
//
// An existing destination is refused too, and needs [Options.Replace]. A
// package is a published artifact; silently replacing one is how a link
// somebody shared starts pointing at different bytes.

// ErrWouldOverwrite reports a destination this program will not write.
var ErrWouldOverwrite = errors.New("pack: refusing to overwrite")

// Options is everything [Create] needs beyond the plan.
type Options struct {
	// Output is the archive's path. Its directory must exist.
	Output string
	// Replace permits overwriting an existing archive at Output.
	Replace bool
	// Label is the caller's short name for this package.
	Label string
	// Companion is the build of this program, for the manifest.
	Companion string
	// Build and Tools are the provenance carried into the manifest.
	Build *BuildRef
	Tools []ToolRef
	// Review is what the person resolved, recorded verbatim.
	Review ReviewRecord
	// Now supplies the manifest's timestamp; nil means time.Now. A field so a
	// test can assert the whole manifest rather than every field but one.
	Now func() time.Time
	// EmbedManifest asks for a copy of the sidecar inside the archive. It is
	// honoured only if the target permits it, and refused loudly if not — a
	// silently ignored request is how a caller ends up believing a package
	// carries metadata it does not.
	EmbedManifest bool
}

// Result is what [Create] wrote.
type Result struct {
	Archive      string
	ManifestPath string
	Manifest     *Manifest
	Entries      []Entry
}

// Create writes a plan as an archive, with its sidecar manifest.
func Create(plan *Plan, options Options) (*Result, error) {
	if err := plan.Blocked(); err != nil {
		return nil, err
	}
	if options.Output == "" {
		return nil, errors.New("pack: no output path")
	}
	output, err := filepath.Abs(options.Output)
	if err != nil {
		return nil, fmt.Errorf("pack: resolving %s: %w", options.Output, err)
	}
	if options.EmbedManifest && !plan.Target.AllowsMetadata {
		return nil, fmt.Errorf("pack: %s does not permit Auto-Pigeon metadata inside the archive. "+
			"The manifest is written beside it as %s, which is where an engine will not read it and a person will",
			plan.Target.ID, filepath.Base(ManifestPathFor(output)))
	}
	if err := checkNotASource(output, plan); err != nil {
		return nil, err
	}
	if err := checkDestination(output, options.Replace); err != nil {
		return nil, err
	}

	members := plan.Members()
	// A target that permits metadata gets it as a member, and it has to be
	// counted before the archive is written: the manifest describes the archive
	// it is inside, so its own bytes are part of what the archive holds.
	// Chicken and egg, resolved by writing the embedded copy without the
	// archive's own digest in it and saying so in the field's documentation.
	manifestPath := ManifestPathFor(output)
	if options.EmbedManifest && plan.Target.AllowsMetadata {
		preliminary := buildManifest(plan, options, ArchiveRef{}, nil)
		encoded, err := preliminary.Encode()
		if err != nil {
			return nil, err
		}
		members = append(members, BytesMember(plan.Target.MetadataPath, encoded))
	}

	staged, err := os.CreateTemp(filepath.Dir(output), filepath.Base(output)+".writing-*")
	if err != nil {
		return nil, fmt.Errorf("pack: staging %s: %w", output, err)
	}
	stagedPath := staged.Name()
	// Removed unconditionally: after a successful rename the path no longer
	// exists and the failure is ignored, and on every other path this is the
	// only thing that cleans it up.
	defer func() {
		staged.Close()
		os.Remove(stagedPath)
	}()

	var entries []Entry
	switch plan.Target.Format {
	case FormatPAK:
		entries, err = WritePAK(staged, members, plan.Target)
	case FormatPK3:
		entries, err = WritePK3(staged, members, plan.Target)
	default:
		err = fmt.Errorf("pack: %q is not a format this build writes", plan.Target.Format)
	}
	if err != nil {
		return nil, err
	}
	if err := staged.Sync(); err != nil {
		return nil, fmt.Errorf("pack: flushing %s: %w", output, err)
	}
	info, err := staged.Stat()
	if err != nil {
		return nil, fmt.Errorf("pack: measuring %s: %w", output, err)
	}
	if err := staged.Close(); err != nil {
		return nil, fmt.Errorf("pack: closing %s: %w", output, err)
	}
	// 0o644 rather than the 0o600 every other file this program writes gets: a
	// package is made to be given to somebody, and CreateTemp's 0o600 would
	// make every published archive unreadable by a web server or a second user.
	if err := os.Chmod(stagedPath, 0o644); err != nil {
		return nil, fmt.Errorf("pack: setting permissions on %s: %w", output, err)
	}
	digest, err := fileDigest(stagedPath)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(stagedPath, output); err != nil {
		return nil, fmt.Errorf("pack: publishing %s: %w", output, err)
	}

	archive := ArchiveRef{
		File: filepath.Base(output), Format: plan.Target.Format,
		Size: info.Size(), SHA256: digest, Entries: len(entries),
	}
	for _, entry := range entries {
		archive.TotalSize += entry.Size
	}
	manifest := buildManifest(plan, options, archive, entries)
	if err := manifest.Save(manifestPath); err != nil {
		return nil, err
	}
	return &Result{Archive: output, ManifestPath: manifestPath, Manifest: manifest, Entries: entries}, nil
}

// buildManifest assembles the sidecar from the plan and what actually got
// written.
//
// The entry digests come from the write rather than from the plan, so the
// manifest records what is in the archive rather than what was intended to be.
// They agree — [copyMember] refuses a member whose length changed — and the
// point of taking them from the write anyway is that if they ever disagree, the
// manifest is right about the archive.
func buildManifest(plan *Plan, options Options, archive ArchiveRef, entries []Entry) *Manifest {
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	written := map[string]Entry{}
	for _, entry := range entries {
		written[entry.Path] = entry
	}
	contents := make([]Decision, 0, len(plan.Included()))
	for _, decision := range plan.Included() {
		if entry, ok := written[decision.Path]; ok {
			decision.Size = entry.Size
			decision.SHA256 = entry.SHA256
		}
		contents = append(contents, decision)
	}
	manifest := &Manifest{
		SchemaVersion:    ManifestSchemaVersion,
		Companion:        options.Companion,
		CreatedAt:        now().UTC(),
		Label:            options.Label,
		Target:           TargetRefOf(plan.Target),
		Archive:          archive,
		Build:            options.Build,
		Tools:            options.Tools,
		Contents:         contents,
		Review:           options.Review,
		MetadataEmbedded: options.EmbedManifest && plan.Target.AllowsMetadata,
		Excluded:         plan.Excluded(),
	}
	if manifest.MetadataEmbedded {
		manifest.MetadataPath = plan.Target.MetadataPath
	}
	return manifest
}

// checkNotASource refuses an output that is one of the plan's own inputs.
func checkNotASource(output string, plan *Plan) error {
	for _, source := range plan.SourcePaths() {
		if sameFilePath(source, output) {
			return fmt.Errorf("%w: %s is one of the files being packaged. "+
				"Write the archive somewhere outside the directory you are packaging", ErrWouldOverwrite, output)
		}
	}
	return nil
}

// checkDestination refuses an existing archive unless replacement was asked
// for, and refuses anything that is not a regular file outright.
func checkDestination(output string, replace bool) error {
	info, err := os.Lstat(output)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pack: checking %s: %w", output, err)
	}
	switch {
	case info.IsDir():
		return fmt.Errorf("%w: %s is a directory", ErrWouldOverwrite, output)
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%w: %s is a symbolic link, and following it would write somewhere this command did not name",
			ErrWouldOverwrite, output)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%w: %s is not a regular file", ErrWouldOverwrite, output)
	case !replace:
		return fmt.Errorf("%w: %s already exists. Pass --replace to write over it", ErrWouldOverwrite, output)
	}
	return nil
}

// sameFilePath compares two paths after cleaning, and case-insensitively where
// the filesystem is.
//
// Not os.SameFile: the output usually does not exist yet, so there is no second
// file to compare inodes with. This is the check that can be made before
// anything is created, which is when it has to happen.
func sameFilePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	if caseInsensitiveFilesystem() {
		return strings.EqualFold(a, b)
	}
	return false
}
