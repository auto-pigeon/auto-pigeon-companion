package texturebundle_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
)

// A fixture bundle is built the way AUB builds one, because a test that
// constructed a shape AUB never writes would prove nothing about the bundles
// this package actually receives.

type fixtureFile struct {
	path string
	body []byte
	// source is the declared texture source this member belongs to.
	source string
	// mode, when non-zero, is written into the archive header. It is how the
	// symlink and device cases are built.
	mode fs.FileMode
	// declared, when false, writes the member without declaring it.
	undeclared bool
	// digest and bytes override what the manifest declares, for the
	// mismatch cases.
	digest string
	bytes  *int64
}

type fixture struct {
	mapID    string
	revision int
	game     string
	// wads is the declaration, in order.
	wads []string
	// requirements maps each declared WAD to the member paths it resolves to,
	// in order. A declaration absent from this map is declared and not included.
	requirements []fixtureRequirement
	files        []fixtureFile

	compilerReady    bool
	compilerRefusals []string
	unresolved       []string

	schema string
	// extra members written into the archive but not into `files`.
	extra []fixtureFile
	// declareOnly are members the manifest declares and the archive does not
	// carry.
	declareOnly []fixtureFile
	// omitManifest writes no manifest.json.
	omitManifest bool
	// omitLicenses writes no LICENSES.md.
	omitLicenses bool
	// rawManifest replaces the encoded manifest entirely.
	rawManifest []byte
}

type fixtureRequirement struct {
	name     string
	status   string
	origin   string
	included bool
	note     string
	paths    []string
}

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

// build assembles the ZIP.
func (f fixture) build(t testing.TB) []byte {
	t.Helper()

	schema := f.schema
	if schema == "" {
		schema = texturebundle.Schema
	}
	byPath := map[string]texturebundle.File{}
	declared := []texturebundle.File{}
	for _, file := range append(append([]fixtureFile{}, f.files...), f.declareOnly...) {
		if file.undeclared {
			continue
		}
		digest := file.digest
		if digest == "" {
			digest = digestOf(file.body)
		}
		size := int64(len(file.body))
		if file.bytes != nil {
			size = *file.bytes
		}
		record := texturebundle.File{Path: file.path, Source: file.source, SHA256: digest, Bytes: size}
		byPath[file.path] = record
		declared = append(declared, record)
	}

	requirements := []texturebundle.Requirement{}
	for order, requirement := range f.requirements {
		entry := texturebundle.Requirement{
			Order: order, Name: requirement.name, Game: f.game, Kind: "wad",
			Origin: requirement.origin, Status: requirement.status,
			Included: requirement.included, Note: requirement.note,
			Files: []texturebundle.File{},
		}
		for _, path := range requirement.paths {
			entry.Files = append(entry.Files, byPath[path])
		}
		requirements = append(requirements, entry)
	}

	manifest := texturebundle.Manifest{
		SchemaVersion: schema, MapID: f.mapID, Revision: f.revision, Game: f.game,
		MapName: "dm_1", ExportedAt: "2026-09-21T00:00:00Z",
		WADsDeclared: f.wads, Requirements: requirements, Files: declared,
		CompilerReady: f.compilerReady, CompilerRefusals: f.compilerRefusals,
		Unresolved: f.unresolved,
	}
	if manifest.CompilerRefusals == nil {
		manifest.CompilerRefusals = []string{}
	}

	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	write := func(file fixtureFile) {
		header := &zip.FileHeader{Name: file.path, Method: zip.Deflate}
		if file.mode != 0 {
			header.SetMode(file.mode)
		}
		out, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if len(file.body) > 0 {
			if _, err = out.Write(file.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, file := range f.files {
		write(file)
	}
	for _, file := range f.extra {
		write(file)
	}
	if !f.omitLicenses {
		write(fixtureFile{path: texturebundle.LicensesName, body: []byte("# Attribution\n")})
	}
	if !f.omitManifest {
		raw := f.rawManifest
		if raw == nil {
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			raw = encoded
		}
		write(fixtureFile{path: texturebundle.ManifestName, body: raw})
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	return buffer.Bytes()
}

// twoOrderedWADs is the shape every ordering test starts from: two declared
// WADs, the second declared after the first, both carried.
func twoOrderedWADs() fixture {
	first := []byte("WAD2" + "first-wall")
	second := []byte("WAD2" + "second-wall")

	return fixture{
		mapID: "map0000000001", revision: 7, game: "quake1",
		wads: []string{"first.wad", "second.wad"},
		files: []fixtureFile{
			{path: "first.wad", body: first, source: "first.wad"},
			{path: "second.wad", body: second, source: "second.wad"},
		},
		requirements: []fixtureRequirement{
			{name: "first.wad", status: "resolved", included: true, paths: []string{"first.wad"}},
			{name: "second.wad", status: "resolved", included: true, paths: []string{"second.wad"}},
		},
		compilerReady: true,
	}
}

func newCache(t testing.TB) *texturebundle.Cache {
	t.Helper()

	cache, err := texturebundle.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return cache
}
