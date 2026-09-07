package acquire

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The fixture, and why it is an in-process HTTPS server.
//
// Everything the acquisition path does is over the network, and a test that
// mocked the transport would test the part of the code that is not interesting.
// So the whole thing runs for real: a TLS server this process starts, a
// catalogue signed by keys this test generated, archives built in memory, a
// cache in a temporary directory. No public network is touched and no fixture
// binary is committed — the "tool" is a shell script this file writes.
//
// TLS rather than plain HTTP because the code refuses a non-https artifact URL,
// and a test that needed that rule relaxed would be a test that stopped
// checking it. httptest's server has a certificate its own client trusts, which
// is exactly the shape of the real thing.

const fixturePlatformOS = "linux"

var testNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t   *testing.T
	dir string

	server *httptest.Server
	// requests counts artifact fetches, which is how a cache hit is told from
	// a second download.
	requests atomic.Int64
	// fail, when set, takes over an artifact request: it is how a truncated
	// body or a 500 is produced.
	fail func(w http.ResponseWriter, r *http.Request) bool

	anchorKey    ed25519.PrivateKey
	catalogKey   ed25519.PrivateKey
	catalogKeyID string
	anchorsPath  string

	keyring  *catalog.Keyring
	document *catalog.Catalog

	// artifacts maps a served path to its bytes.
	artifacts map[string][]byte

	keyringEnvelope []byte
	catalogEnvelope []byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), artifacts: map[string][]byte{}}

	anchor, err := catalog.GenerateKeyFile(catalog.RoleAnchor, "test anchor", testNow)
	if err != nil {
		t.Fatalf("%v", err)
	}
	signing, err := catalog.GenerateKeyFile(catalog.RoleCatalog, "test catalogue key", testNow)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if f.anchorKey, err = anchor.Private(); err != nil {
		t.Fatalf("%v", err)
	}
	if f.catalogKey, err = signing.Private(); err != nil {
		t.Fatalf("%v", err)
	}
	f.catalogKeyID = signing.KeyID

	anchorEntry, err := anchor.PublicEntry(testNow.AddDate(-1, 0, 0), testNow.AddDate(5, 0, 0))
	if err != nil {
		t.Fatalf("%v", err)
	}
	anchors := &catalog.Anchors{Keys: []catalog.Key{anchorEntry}}
	encoded, err := anchors.Marshal()
	if err != nil {
		t.Fatalf("%v", err)
	}
	f.anchorsPath = filepath.Join(f.dir, "anchors.json")
	if err := os.WriteFile(f.anchorsPath, encoded, 0o600); err != nil {
		t.Fatalf("%v", err)
	}

	catalogEntry, err := signing.PublicEntry(testNow.AddDate(-1, 0, 0), testNow.AddDate(1, 0, 0))
	if err != nil {
		t.Fatalf("%v", err)
	}
	f.keyring = &catalog.Keyring{
		SchemaVersion: catalog.KeyringSchemaVersion,
		KeyringID:     "test-keyring",
		Serial:        1,
		IssuedAt:      testNow.Add(-time.Hour),
		ExpiresAt:     testNow.Add(365 * 24 * time.Hour),
		Keys:          []catalog.Key{catalogEntry},
	}

	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	// The concurrency test closes connections faster than the server finishes
	// with them, and httptest logs each one. The noise is not a finding.
	f.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	t.Cleanup(f.server.Close)

	f.document = &catalog.Catalog{
		SchemaVersion: catalog.SchemaVersion,
		CatalogID:     "test-catalog",
		Serial:        1,
		IssuedAt:      testNow.Add(-time.Hour),
		ExpiresAt:     testNow.Add(30 * 24 * time.Hour),
	}
	return f
}

func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/" + catalog.KeyringFileName:
		w.Write(f.keyringEnvelope)
		return
	case "/" + catalog.CatalogFileName:
		w.Write(f.catalogEnvelope)
		return
	}
	body, ok := f.artifacts[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.requests.Add(1)
	if f.fail != nil && f.fail(w, r) {
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.Write(body)
}

// publish signs the current keyring and catalogue and makes them servable.
func (f *fixture) publish() {
	f.t.Helper()
	keyringEnvelope, err := catalog.Sign(f.keyring, f.anchorKey)
	if err != nil {
		f.t.Fatalf("%v", err)
	}
	catalogEnvelope, err := catalog.Sign(f.document, f.catalogKey)
	if err != nil {
		f.t.Fatalf("%v", err)
	}
	if f.keyringEnvelope, err = keyringEnvelope.Marshal(); err != nil {
		f.t.Fatalf("%v", err)
	}
	if f.catalogEnvelope, err = catalogEnvelope.Marshal(); err != nil {
		f.t.Fatalf("%v", err)
	}
}

// addPackage serves an artifact and lists it in the catalogue.
func (f *fixture) addPackage(id, version, path string, body []byte, kind string, executables []string, root string) catalog.Package {
	f.t.Helper()
	f.artifacts["/"+path] = body
	sum := sha256.Sum256(body)
	artifact := catalog.Artifact{
		Platform:    profile.Platform{OS: goos(), Arch: goarch()},
		URL:         f.server.URL + "/" + path,
		Kind:        kind,
		Size:        int64(len(body)),
		SHA256:      "sha256:" + hex.EncodeToString(sum[:]),
		Signer:      f.catalogKeyID,
		Root:        root,
		Executables: executables,
	}
	if kind == catalog.KindFile {
		artifact.File = executables[0]
	} else {
		artifact.UnpackedSize = 1 << 20
	}
	pkg := catalog.Package{
		ID:      id,
		Version: version,
		Name:    "Fixture " + id,
		Source:  profile.Source{Homepage: "https://example.invalid/" + id},
		License: profile.License{
			SPDX:                "GPL-2.0-or-later",
			CorrespondingSource: "https://example.invalid/" + id + "/source",
		},
		Artifacts: []catalog.Artifact{artifact},
	}
	f.document.Packages = append(f.document.Packages, pkg)
	return pkg
}

// options is what an acquirer built against this fixture uses.
func (f *fixture) options() Options {
	return Options{
		CacheDir:       filepath.Join(f.dir, "packages"),
		StatePath:      filepath.Join(f.dir, "catalog-state.json"),
		AcceptancePath: filepath.Join(f.dir, "license-acceptance.json"),
		AnchorsPath:    f.anchorsPath,
		CatalogURL:     f.server.URL,
		HTTP:           f.server.Client(),
		Now:            func() time.Time { return testNow },
	}
}

func (f *fixture) acquirer(mutate ...func(*Options)) *Acquirer {
	f.t.Helper()
	options := f.options()
	for _, apply := range mutate {
		apply(&options)
	}
	acquirer, err := New(options)
	if err != nil {
		f.t.Fatalf("%v", err)
	}
	return acquirer
}

// digestOf is the digest the catalogue records for a package's only artifact.
func (f *fixture) digestOf(id string) string {
	f.t.Helper()
	for _, pkg := range f.document.Packages {
		if pkg.ID == id {
			return pkg.Artifacts[0].SHA256
		}
	}
	f.t.Fatalf("no package %q in the fixture catalogue", id)
	return ""
}

// digestOfBytes is the digest a catalogue would record for some bytes.
func (f *fixture) digestOfBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tarEntry is one member of a fixture archive.
type tarEntry struct {
	Name     string
	Mode     int64
	Content  string
	Typeflag byte
	Linkname string
	// Size overrides the declared size, for the bomb test.
	Size int64
}

func tarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := entry.Typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := entry.Mode
		if mode == 0 {
			mode = 0o644
		}
		size := int64(len(entry.Content))
		if entry.Size != 0 {
			size = entry.Size
		}
		header := &tar.Header{
			Name: entry.Name, Mode: mode, Size: size,
			Typeflag: typeflag, Linkname: entry.Linkname, ModTime: testNow,
		}
		if typeflag == tar.TypeDir || typeflag == tar.TypeSymlink || typeflag == tar.TypeLink ||
			typeflag == tar.TypeChar || typeflag == tar.TypeBlock || typeflag == tar.TypeFifo {
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("%v", err)
		}
		if header.Size > 0 {
			content := entry.Content
			if int64(len(content)) < header.Size {
				content += string(bytes.Repeat([]byte{'x'}, int(header.Size)-len(content)))
			}
			if _, err := io.WriteString(writer, content[:header.Size]); err != nil {
				t.Fatalf("%v", err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("%v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("%v", err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.Name, Method: zip.Deflate}
		mode := fs.FileMode(entry.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if entry.Typeflag == tar.TypeSymlink {
			mode |= fs.ModeSymlink
		}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("%v", err)
		}
		content := entry.Content
		if entry.Typeflag == tar.TypeSymlink {
			content = entry.Linkname
		}
		if _, err := io.WriteString(file, content); err != nil {
			t.Fatalf("%v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("%v", err)
	}
	return buf.Bytes()
}

// toolScript is the fixture "tool": this repository's own bytes, not a
// third-party program, and executable enough to prove the permission bit
// survived extraction.
const toolScript = "#!/bin/sh\necho fixture tool\n"

// goodArchive is one tool at `tool-1.0/bin/fixture`, with a licence file
// alongside it so that extraction of more than one member is exercised.
func goodArchive(t *testing.T) []byte {
	t.Helper()
	return tarGz(t,
		tarEntry{Name: "tool-1.0", Typeflag: tar.TypeDir, Mode: 0o755},
		tarEntry{Name: "tool-1.0/bin", Typeflag: tar.TypeDir, Mode: 0o755},
		tarEntry{Name: "tool-1.0/bin/fixture", Mode: 0o755, Content: toolScript},
		tarEntry{Name: "tool-1.0/COPYING", Mode: 0o644, Content: "GPL-2.0-or-later, verbatim, in the real thing\n"},
	)
}
