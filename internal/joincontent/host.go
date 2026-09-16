package joincontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
)

// The host's half: a finished build's compiled level becomes a package.
//
// Only what a build PRODUCED goes in — the BSP and, when there is one, its LIT.
// Nothing is read out of the user's game directory, and there is no flag that
// adds a file from anywhere else: a host who wants to ship a redistributable mod
// asset declares it in a manifest of their own and uploads it through AUB's API,
// where the declaration is theirs to make.

// Level is the compiled output a package is built from.
type Level struct {
	MapName string
	BSP     string
	Lit     string
}

// Built is a manifest with the local file each entry came from.
type Built struct {
	Manifest aub.JoinContentManifest
	Paths    map[string]string
	Digest   string
}

// Build hashes a level's files and describes them as a package for one map
// revision. `revision` 0 means "whatever is current" and AUB pins it.
func Build(level Level, mapID string, revision int, family string) (Built, error) {
	name := strings.TrimSpace(level.MapName)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(level.BSP), filepath.Ext(level.BSP))
	}
	name = strings.ToLower(name)
	built := Built{
		Manifest: aub.JoinContentManifest{SchemaVersion: aub.JoinContentSchema, MapID: mapID,
			MapRevision: revision, GameFamily: family},
		Paths: map[string]string{},
	}
	add := func(role, source, destination string) error {
		info, err := os.Stat(source)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("joincontent: %s is not a file this build produced", source)
		}
		sum, err := hashFile(source)
		if err != nil {
			return err
		}
		built.Manifest.Files = append(built.Manifest.Files, aub.JoinContentFile{
			Role: role, Destination: destination, Bytes: info.Size(), SHA256: sum})
		built.Paths[destination] = source

		return nil
	}
	if level.BSP == "" {
		return Built{}, errors.New("joincontent: the build has no compiled map to share")
	}
	if err := add(RoleBSP, level.BSP, "maps/"+name+".bsp"); err != nil {
		return Built{}, err
	}
	if level.Lit != "" {
		if err := add(RoleLit, level.Lit, "maps/"+name+".lit"); err != nil {
			return Built{}, err
		}
	}
	if _, err := Check(built.Manifest); err != nil {
		return Built{}, err
	}
	built.Digest = built.Manifest.Digest()

	return built, nil
}

// Uploader is the part of the AUB client an upload needs.
type Uploader interface {
	OwnJoinPackage(ctx context.Context, digest string) (aub.JoinPackage, error)
	UploadJoinPackage(ctx context.Context, manifest aub.JoinContentManifest,
		open func(aub.JoinContentFile) (io.ReadCloser, error)) (aub.JoinPackage, bool, error)
}

// Upload sends a built package unless the account already holds it, and returns
// the package AUB stored — whose digest, with revision 0 pinned, is what a
// registration names.
//
// The files are re-hashed as they are streamed, so a BSP rebuilt between Build
// and Upload is refused here rather than uploaded under the old digest.
func Upload(ctx context.Context, client Uploader, built Built) (aub.JoinPackage, error) {
	if built.Manifest.MapRevision > 0 {
		if existing, err := client.OwnJoinPackage(ctx, built.Digest); err == nil {
			return existing, nil
		}
	}
	pkg, _, err := client.UploadJoinPackage(ctx, built.Manifest,
		func(file aub.JoinContentFile) (io.ReadCloser, error) {
			source, ok := built.Paths[file.Destination]
			if !ok {
				return nil, fmt.Errorf("joincontent: %s has no source file", file.Destination)
			}
			handle, err := os.Open(source)
			if err != nil {
				return nil, err
			}

			return &hashingReader{file: handle, want: file}, nil
		})

	return pkg, err
}

type hashingReader struct {
	file *os.File
	want aub.JoinContentFile
	hash hash.Hash
	read int64
}

func (r *hashingReader) Read(p []byte) (int, error) {
	if r.hash == nil {
		r.hash = sha256.New()
	}
	n, err := r.file.Read(p)
	if n > 0 {
		r.hash.Write(p[:n])
		r.read += int64(n)
	}
	if errors.Is(err, io.EOF) &&
		(r.read != r.want.Bytes || hex.EncodeToString(r.hash.Sum(nil)) != r.want.SHA256) {
		return n, fmt.Errorf("joincontent: %s changed after it was hashed; build the package again",
			r.want.Destination)
	}

	return n, err
}

func (r *hashingReader) Close() error { return r.file.Close() }
