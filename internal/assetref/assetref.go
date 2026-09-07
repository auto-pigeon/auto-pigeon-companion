// Package assetref resolves `aub:<type>/<id>[@<revision>][#<file>]` into a file
// on this machine, and into the record that says where it came from.
//
// # Why a reference and not a path
//
// A path names bytes on this machine and says nothing about where they came
// from. A build whose input was a downloaded map, recorded as a path, is a build
// nobody can trace back: the manifest carries the digest, so it can prove two
// builds read the same bytes, and it cannot say which version of whose map those
// bytes were.
//
// So an input may instead name an ASSET AND A REVISION. It is resolved through
// the local cache first, fetched only when the cache does not hold it, verified
// before anything reads it, and recorded in the manifest as [build.SourceRef] —
// asset, exact revision, and whether that revision can be fetched again.
//
// # And why the revision is resolved BEFORE the build, once
//
// `@current` is resolved at the moment the build starts, and what goes into the
// manifest is the version it resolved TO — never the word `current`. A build
// retried tomorrow re-reads the manifest's revision id from the cache and gets
// the same bytes, which is the whole of "a newer remote revision must not
// silently change an already queued build".
//
// # Why it is its own package
//
// The CLI and the GUI both build from AUB assets, and both need exactly this:
// parse, resolve through the cache, verify, materialize, record the source.
// Two copies of that would be two chances for a `--input` and a form field to
// disagree about which revision a build actually read.
package assetref

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
)

// Prefix marks an input value as an asset reference rather than a path.
const Prefix = "aub:"

// Ref is a parsed `aub:<type>/<id>[@<revision>][#<file>]`.
type Ref struct {
	AssetType string
	AssetID   string

	// Revision is a revision id, or `current`, or empty meaning `current`.
	Revision string

	// File names which of the revision's files this input is, for a revision
	// that has more than one. Empty means the revision's single file, and a
	// revision with several then names them rather than picking one — a build
	// that silently took the first file of a prefab package would be a build
	// nobody could explain.
	File string
}

// Is reports whether an input value is an asset reference.
func Is(value string) bool { return strings.HasPrefix(value, Prefix) }

// Parse reads `aub:map/abc123@rev456#e1m1.apmap`.
func Parse(value string) (Ref, error) {
	if !Is(value) {
		return Ref{}, fmt.Errorf("%q is not an asset reference", value)
	}
	body := strings.TrimPrefix(value, Prefix)

	ref := Ref{}
	if body, ref.File = cutLast(body, "#"); body == "" {
		return Ref{}, fmt.Errorf("%q names no asset", value)
	}
	body, ref.Revision = cutLast(body, "@")

	assetType, assetID, found := strings.Cut(body, "/")
	if !found || assetType == "" || assetID == "" {
		return Ref{}, fmt.Errorf(
			"%q is not an asset reference: want aub:<type>/<asset_id>[@<revision>][#<file>]", value)
	}
	ref.AssetType, ref.AssetID = assetType, assetID
	if ref.Revision == "" {
		ref.Revision = aub.CurrentRevision
	}

	return ref, nil
}

// cutLast splits on the LAST occurrence of sep, returning the head and the tail.
//
// Last rather than first, because a file name may contain a `#` or an `@` and an
// asset id may not: this package's own refusal to accept anything but
// `[A-Za-z0-9._-]` in an id is what makes the last separator the right one.
func cutLast(value, sep string) (string, string) {
	index := strings.LastIndex(value, sep)
	if index < 0 {
		return value, ""
	}

	return value[:index], value[index+len(sep):]
}

// Input is one resolved reference: where the bytes are now, and what they are.
type Input struct {
	Path   string
	Source build.SourceRef
}

// ResolveAll turns every `aub:` input into a local file and a SourceRef.
//
// Inputs that are already paths are left alone, and a call with no asset
// references touches neither the cache nor the network — a build from local
// files stays exactly what it was.
//
// `syncer` is OPTIONAL: an offline Companion, or one whose session has lapsed,
// still builds from what it has already verified. What it cannot do is resolve
// `current` or fetch something new, and [assetsync.Resolve] says so by name.
func ResolveAll(ctx context.Context, store *assetsync.Store, syncer *assetsync.Syncer,
	inputs map[string]string, stage string,
) (map[string]string, map[string]build.SourceRef, error) {
	refs := map[string]Ref{}
	for name, value := range inputs {
		if !Is(value) {
			continue
		}
		ref, err := Parse(value)
		if err != nil {
			return nil, nil, fmt.Errorf("the input %q: %w", name, err)
		}
		refs[name] = ref
	}
	if len(refs) == 0 {
		return inputs, nil, nil
	}
	if store == nil {
		return nil, nil, fmt.Errorf("no local asset cache is available to resolve %d asset reference(s)",
			len(refs))
	}

	resolved := map[string]string{}
	sources := map[string]build.SourceRef{}
	for name, value := range inputs {
		ref, isRef := refs[name]
		if !isRef {
			resolved[name] = value

			continue
		}
		input, err := Materialize(ctx, store, syncer, ref, stage, name)
		if err != nil {
			return nil, nil, fmt.Errorf("the input %q: %w", name, err)
		}
		resolved[name] = input.Path
		sources[name] = input.Source
	}

	return resolved, sources, nil
}

// Materialize resolves one reference to a file on disk.
func Materialize(ctx context.Context, store *assetsync.Store, syncer *assetsync.Syncer,
	ref Ref, stage, name string) (Input, error) {
	record, err := assetsync.Resolve(ctx, store, syncer, ref.AssetType, ref.AssetID, ref.Revision)
	if err != nil {
		return Input{}, err
	}

	file, err := ChooseFile(record, ref.File)
	if err != nil {
		return Input{}, err
	}

	dir := filepath.Join(stage, name)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return Input{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	// Materialize verifies every file against its recorded digest before writing
	// one, so a cache that lost a block is caught here rather than by a compiler
	// producing something strange.
	if _, err = store.Materialize(record, dir); err != nil {
		return Input{}, err
	}

	return Input{
		Path:   filepath.Join(dir, filepath.FromSlash(file.Path)),
		Source: Source(record),
	}, nil
}

// Source is the manifest's record of where a revision came from.
func Source(record assetsync.RevisionRecord) build.SourceRef {
	return build.SourceRef{
		Backend:        record.Source,
		AssetType:      record.AssetType,
		AssetID:        record.AssetID,
		DisplayName:    record.DisplayName,
		RevisionID:     record.RevisionID,
		Revision:       record.Revision,
		Refetchable:    record.Immutable && record.RevisionID != "",
		ManifestSHA256: record.ManifestSHA256,
		ContentSHA256:  record.ContentSHA256,
		FetchedAt:      record.SyncedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ChooseFile picks which of a revision's files an input is.
//
// A revision with one file needs no name. A revision with several REQUIRES one:
// picking the first would make the answer depend on an ordering the build did not
// choose, and the failure would be a compiler being handed a `prefab.json` where
// it expected geometry.
func ChooseFile(record assetsync.RevisionRecord, wanted string) (assetsync.FileRecord, error) {
	if wanted != "" {
		for _, file := range record.Files {
			if file.Path == wanted {
				return file, nil
			}
		}

		return assetsync.FileRecord{}, fmt.Errorf(
			"revision %s holds no file %q; it holds: %s",
			record.Key(), wanted, strings.Join(FilePaths(record), ", "))
	}
	if len(record.Files) == 1 {
		return record.Files[0], nil
	}
	if len(record.Files) == 0 {
		return assetsync.FileRecord{}, fmt.Errorf("revision %s holds no files", record.Key())
	}

	return assetsync.FileRecord{}, fmt.Errorf(
		"revision %s holds %d files, so the reference must name one with #<file>: %s",
		record.Key(), len(record.Files), strings.Join(FilePaths(record), ", "))
}

// FilePaths is every file a revision holds, for a message that has to list them.
func FilePaths(record assetsync.RevisionRecord) []string {
	names := make([]string, 0, len(record.Files))
	for _, file := range record.Files {
		names = append(names, file.Path)
	}

	return names
}
