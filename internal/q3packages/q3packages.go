// Package q3packages reads the packages a saved Quake III map is bound to, and
// gets exactly those archives onto this machine.
//
// # What a map records, and who decides it
//
// The editor writes the binding into the map's own document: worldspawn's
// `extensions["auto-pigeon.packages"]`, schema `auto-pigeon.packages/1` — a base
// folder name, a mod folder name, and for each package its folder, its archive
// name and the SHA-256 of the archive (AUP `domain/packages.ts`, DESIGN §206).
// A binding is a DIGEST. It is not a URL, not a package id and not a file name
// on somebody's disk, and that is what makes it checkable here: whatever this
// package hands a build has been hashed and compares equal to what the map
// said, or it is not handed over.
//
// # Why a build does this itself (Q3_010)
//
// `Q3_007` built a saved map against its bound package by fetching the archive
// by hand and pointing a content folder at it, and recorded that the Companion
// "does not stage a map's package ledger by itself yet". A build that depends on
// a person re-creating the binding by hand is a build of whatever they happened
// to copy. So the ledger is read from the APMap the build was given, each bound
// archive is found in the account by digest and downloaded into the Companion's
// content-addressed cache, and the build stages those files and records them.
//
// # What it refuses
//
// A ledger in a schema this build does not know, a binding that is not a
// digest, an archive name that is a path, a package the account does not hold,
// and bytes that do not hash to the binding. A map with no ledger binds
// nothing, and that is not an error.
package q3packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// Extension and Schema name the ledger, as the editor writes it.
const (
	Extension = "auto-pigeon.packages"
	Schema    = "auto-pigeon.packages/1"
)

// maxAPMapBytes bounds how much of a document is read to find its ledger.
const maxAPMapBytes = 256 << 20

// Binding is one package a map is bound to.
type Binding struct {
	Game          string `json:"game"`
	Root          string `json:"root"`
	ArchiveName   string `json:"archive_name"`
	ArchiveSHA256 string `json:"archive_sha256"`
}

// Ledger is a map's package bindings.
type Ledger struct {
	SchemaVersion string    `json:"schema_version"`
	BaseRoot      string    `json:"base_root"`
	ModRoot       string    `json:"mod_root"`
	Packages      []Binding `json:"packages"`
}

// ReadLedger reads the ledger out of an APMap. A document with none returns
// (nil, nil): the map binds no package.
func ReadLedger(path string) (*Ledger, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var document struct {
		Entities []struct {
			Extensions map[string]json.RawMessage `json:"extensions"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(io.LimitReader(file, maxAPMapBytes)).Decode(&document); err != nil {
		return nil, fmt.Errorf("reading the map's package bindings: %w", err)
	}
	for _, entity := range document.Entities {
		raw, bound := entity.Extensions[Extension]
		if !bound {
			continue
		}
		var ledger Ledger
		if err := json.Unmarshal(raw, &ledger); err != nil {
			return nil, refuse("the map's package bindings are not readable: %v", err)
		}
		if ledger.SchemaVersion != Schema {
			// Unreadable, not empty — the editor's own rule. A build that
			// treated a newer ledger as "nothing bound" would compile the map
			// without the packages it says it needs.
			return nil, refuse("the map's package bindings are in the format %q; this build reads %s",
				ledger.SchemaVersion, Schema)
		}
		if ledger.BaseRoot == "" {
			ledger.BaseRoot = q3vfs.BaseGame
		}
		for i, binding := range ledger.Packages {
			if err := checkBinding(binding); err != nil {
				return nil, refuse("the map's package binding %d: %v", i+1, err)
			}
		}
		return &ledger, nil
	}
	return nil, nil
}

func refuse(format string, args ...any) error {
	return failure.As(failure.ContentRefused, fmt.Errorf(format, args...))
}

func checkBinding(binding Binding) error {
	if len(binding.ArchiveSHA256) != 64 || strings.Trim(binding.ArchiveSHA256, "0123456789abcdef") != "" {
		return fmt.Errorf("%q is not a SHA-256 digest", clip(binding.ArchiveSHA256))
	}
	if err := q3vfs.CheckArchiveName(binding.ArchiveName); err != nil {
		return err
	}
	if binding.Root == "" {
		return errors.New("it names no game folder")
	}
	if err := q3vfs.CheckFSGame(binding.Root); err != nil {
		return fmt.Errorf("its game folder: %w", err)
	}
	return nil
}

func clip(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// Account is what this package needs of AUB.
type Account interface {
	AssetPackages(ctx context.Context) ([]aub.AssetPackage, error)
	AssetPackageArchive(ctx context.Context, id string) (io.ReadCloser, int64, error)
}

// Cache is the content-addressed store the archives are kept in.
type Cache interface {
	Has(digest string) bool
	Object(digest string) (string, error)
	Publish(reader io.Reader, digest string, size int64) (string, error)
}

// Resolve gets every archive a ledger binds onto this machine and returns them
// as what a build stages.
//
// An archive already in the cache is used as it is: the cache names a file by
// the digest of its bytes and verified them when it stored them, so a build of
// a pinned map needs neither a session nor a network. Anything else is looked
// up in the account BY DIGEST and downloaded through the cache's own verifying
// writer, which refuses bytes that do not hash to the binding. account may be
// nil — signed out, or offline — and then a package that is not cached is the
// failure, said by name.
func Resolve(ctx context.Context, account Account, cache Cache, ledger *Ledger) ([]q3vfs.Package, error) {
	if ledger == nil || len(ledger.Packages) == 0 {
		return nil, nil
	}
	var held map[string]aub.AssetPackage
	out := make([]q3vfs.Package, 0, len(ledger.Packages))
	for _, binding := range ledger.Packages {
		pkg := q3vfs.Package{
			Root: binding.Root, ArchiveName: binding.ArchiveName, SHA256: "sha256:" + binding.ArchiveSHA256,
		}
		if cache.Has(binding.ArchiveSHA256) {
			path, err := cache.Object(binding.ArchiveSHA256)
			if err != nil {
				return nil, err
			}
			pkg.Path, pkg.Origin = path, q3vfs.OriginCache
			out = append(out, pkg)
			continue
		}
		if account == nil {
			return nil, missing("the map is bound to the package %s (sha256 %s), which is not on this machine, "+
				"and the Companion is not signed in to fetch it. Sign in, or build once while signed in: "+
				"a fetched package is kept and needs no account afterwards",
				binding.ArchiveName, binding.ArchiveSHA256)
		}
		if held == nil {
			rows, err := account.AssetPackages(ctx)
			if err != nil {
				return nil, failure.As(failure.GameDataMissing,
					fmt.Errorf("reading the account's packages for this map's bindings: %w", err))
			}
			held = make(map[string]aub.AssetPackage, len(rows))
			for _, row := range rows {
				// A row whose archive is gone cannot serve the bytes, and one
				// that IS available wins over one that is not.
				if previous, seen := held[row.ArchiveSHA256]; !seen || (!previous.ArchiveAvailable && row.ArchiveAvailable) {
					held[row.ArchiveSHA256] = row
				}
			}
		}
		row, found := held[binding.ArchiveSHA256]
		switch {
		case !found:
			return nil, missing("the map is bound to the package %s (sha256 %s), and this account holds no "+
				"package with that digest. Upload that exact archive under LOAD › PACKAGES in the editor, "+
				"or unbind it from the map", binding.ArchiveName, binding.ArchiveSHA256)
		case !row.ArchiveAvailable:
			return nil, missing("the map is bound to the package %s (sha256 %s); the account knows it and no "+
				"longer holds its archive", binding.ArchiveName, binding.ArchiveSHA256)
		}
		body, declared, err := account.AssetPackageArchive(ctx, row.ID)
		if err != nil {
			return nil, failure.As(failure.GameDataMissing,
				fmt.Errorf("downloading the bound package %s: %w", binding.ArchiveName, err))
		}
		path, err := cache.Publish(body, binding.ArchiveSHA256, declared)
		body.Close()
		if err != nil {
			// The bytes that arrived are not the bytes the map is bound to.
			return nil, failure.As(failure.ArchiveDamaged, fmt.Errorf(
				"the bound package %s did not arrive as the archive the map names (sha256 %s): %w",
				binding.ArchiveName, binding.ArchiveSHA256, err))
		}
		pkg.Path, pkg.Origin, pkg.PackageID = path, q3vfs.OriginAccount, row.ID
		out = append(out, pkg)
	}
	return out, nil
}

func missing(format string, args ...any) error {
	return failure.As(failure.GameDataMissing, fmt.Errorf(format, args...))
}
