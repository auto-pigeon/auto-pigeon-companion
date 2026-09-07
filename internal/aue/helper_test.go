package aue_test

// The fixture, and why the extractor is a real subprocess.
//
// Everything interesting here is at a boundary: a signed document verified over
// a network, a file made executable and exec'd, a contract asked of a process
// and read back off its stdout. A test that stubbed any of those would test the
// part that is not interesting. So the whole path runs for real — an in-process
// TLS server, a catalogue and a compatibility manifest signed by keys this test
// generated, a cache in a temporary directory, and an "extractor" that is a
// shell script this file writes.
//
// TLS rather than plain HTTP because the catalogue refuses a non-https artifact
// URL, and a test that needed that rule relaxed would be a test that stopped
// checking it.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

var testNow = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

// companionVersion is the Companion the compatibility rules are written for.
const companionVersion = "0.2.0"

func here() profile.Platform {
	return profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

type fixture struct {
	t   *testing.T
	dir string

	server    *httptest.Server
	artifacts map[string][]byte
	// downloads counts artifact fetches, which is how a cache hit is told from
	// a second download.
	downloads int
	// authorized records the URLs an Authorize hook was asked to rewrite.
	authorized []string

	anchorKey    ed25519.PrivateKey
	catalogKey   ed25519.PrivateKey
	catalogKeyID string
	anchorsPath  string

	keyring       *catalog.Keyring
	document      *catalog.Catalog
	compatibility *catalog.Compatibility

	keyringEnvelope       []byte
	catalogEnvelope       []byte
	compatibilityEnvelope []byte
	// serveCompatibility false makes the server answer 404 for the
	// compatibility document, which is the "this catalogue publishes none" case.
	serveCompatibility bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture extractor is a shell script")
	}

	f := &fixture{t: t, dir: t.TempDir(), artifacts: map[string][]byte{}, serveCompatibility: true}

	anchor, err := catalog.GenerateKeyFile(catalog.RoleAnchor, "test anchor", testNow)
	if err != nil {
		t.Fatal(err)
	}
	signing, err := catalog.GenerateKeyFile(catalog.RoleCatalog, "test catalogue key", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if f.anchorKey, err = anchor.Private(); err != nil {
		t.Fatal(err)
	}
	if f.catalogKey, err = signing.Private(); err != nil {
		t.Fatal(err)
	}
	f.catalogKeyID = signing.KeyID

	anchorEntry, err := anchor.PublicEntry(testNow.AddDate(-1, 0, 0), testNow.AddDate(5, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	anchors := &catalog.Anchors{Keys: []catalog.Key{anchorEntry}}
	encoded, err := anchors.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	f.anchorsPath = filepath.Join(f.dir, "anchors.json")
	if err = os.WriteFile(f.anchorsPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	catalogEntry, err := signing.PublicEntry(testNow.AddDate(-1, 0, 0), testNow.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	f.keyring = &catalog.Keyring{
		SchemaVersion: catalog.KeyringSchemaVersion, KeyringID: "test-keyring", Serial: 1,
		IssuedAt: testNow.Add(-time.Hour), ExpiresAt: testNow.Add(365 * 24 * time.Hour),
		Keys: []catalog.Key{catalogEntry},
	}
	f.document = &catalog.Catalog{
		SchemaVersion: catalog.SchemaVersion, CatalogID: "test-catalog", Serial: 1,
		IssuedAt: testNow.Add(-time.Hour), ExpiresAt: testNow.Add(30 * 24 * time.Hour),
	}
	f.compatibility = &catalog.Compatibility{
		SchemaVersion: catalog.CompatibilitySchemaVersion, DocumentID: "test-compatibility", Serial: 1,
		IssuedAt: testNow.Add(-time.Hour), ExpiresAt: testNow.Add(30 * 24 * time.Hour),
	}

	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	f.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	t.Cleanup(f.server.Close)

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
	case "/" + catalog.CompatibilityFileName:
		if !f.serveCompatibility {
			http.NotFound(w, r)

			return
		}
		w.Write(f.compatibilityEnvelope)

		return
	}
	body, ok := f.artifacts[r.URL.Path]
	if !ok {
		http.NotFound(w, r)

		return
	}
	f.downloads++
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.Write(body)
}

// publish signs all three documents and makes them servable.
func (f *fixture) publish() {
	f.t.Helper()

	keyring, err := catalog.Sign(f.keyring, f.anchorKey)
	if err != nil {
		f.t.Fatal(err)
	}
	document, err := catalog.Sign(f.document, f.catalogKey)
	if err != nil {
		f.t.Fatal(err)
	}
	compatibility, err := catalog.Sign(f.compatibility, f.catalogKey)
	if err != nil {
		f.t.Fatal(err)
	}
	if f.keyringEnvelope, err = keyring.Marshal(); err != nil {
		f.t.Fatal(err)
	}
	if f.catalogEnvelope, err = document.Marshal(); err != nil {
		f.t.Fatal(err)
	}
	if f.compatibilityEnvelope, err = compatibility.Marshal(); err != nil {
		f.t.Fatal(err)
	}
}

// script is the body of a fake extractor.
//
// It answers `protocol --json` with a real document — the same shape the
// extractor's own `internal/protocol` writes — so the handshake is exercised
// rather than stubbed, and `version` with a version string. `behaviour` is
// appended for the cases that need a crash, a hang or a flood.
type script struct {
	protocol string
	version  string
	// malformedProtocol makes `protocol --json` write something that is not one
	// JSON document.
	malformedProtocol string
	// behaviour is extra shell run for any other subcommand.
	behaviour string
}

func (s script) body() string {
	protocol := s.protocol
	if protocol == "" {
		protocol = "1.0"
	}
	version := s.version
	if version == "" {
		version = "1.171"
	}
	answer := fmt.Sprintf(`{"schema_version":"aue-invocation-protocol/1.0","protocol":"%s","protocol_major":1,`+
		`"protocol_minor":0,"version":"%s","product":{"name":"Auto-Pigeon Extractor",`+
		`"executable":"auto-pigeon-extractor","repository":"https://example.invalid/aue"},`+
		`"license":{"spdx":"AGPL-3.0-only","name":"AGPL","url":"https://example.invalid/agpl",`+
		`"corresponding_source":"https://example.invalid/aue","aggregation":"a separate program"}}`,
		protocol, version)
	if s.malformedProtocol != "" {
		answer = s.malformedProtocol
	}
	behaviour := s.behaviour
	if behaviour == "" {
		behaviour = "echo \"$*\"\n"
	}

	return "#!/bin/sh\ncase \"$1\" in\n" +
		"protocol) cat <<'JSON'\n" + answer + "\nJSON\n exit 0 ;;\n" +
		"version) echo '" + version + "'; exit 0 ;;\n" +
		"esac\n" + behaviour
}

// addRelease serves a fake extractor as a catalogue package at one version.
func (f *fixture) addRelease(version string, s script) catalog.Package {
	f.t.Helper()

	body := []byte(s.body())
	path := "aue-" + version
	f.artifacts["/"+path] = body
	sum := sha256.Sum256(body)
	pkg := catalog.Package{
		ID:      aue.ComponentID,
		Version: version,
		Name:    "Auto-Pigeon Extractor",
		Program: "Auto-Pigeon Extractor",
		Source:  profile.Source{Homepage: "https://example.invalid/aue"},
		License: profile.License{
			SPDX:                "AGPL-3.0-only",
			CorrespondingSource: "https://example.invalid/aue",
		},
		Artifacts: []catalog.Artifact{{
			Platform:    here(),
			URL:         f.server.URL + "/" + path,
			Kind:        catalog.KindFile,
			Size:        int64(len(body)),
			SHA256:      "sha256:" + hex.EncodeToString(sum[:]),
			Signer:      f.catalogKeyID,
			File:        "auto-pigeon-extractor",
			Executables: []string{"auto-pigeon-extractor"},
		}},
	}
	f.document.Packages = append(f.document.Packages, pkg)

	return pkg
}

// require sets the compatibility rule for this Companion.
func (f *fixture) require(version, minProtocol string, platforms ...string) {
	f.t.Helper()
	if len(platforms) == 0 {
		platforms = []string{here().String()}
	}
	f.compatibility.Components = []catalog.Component{{
		Component: aue.ComponentID,
		Program:   "Auto-Pigeon Extractor",
		Requirements: []catalog.Requirement{{
			MinCompanion: "0.1.0",
			Platforms:    platforms,
			Version:      version,
			MinProtocol:  minProtocol,
		}},
	}}
}

func (f *fixture) digestOf(version string) string {
	f.t.Helper()
	for _, pkg := range f.document.Packages {
		if pkg.Version == version {
			return pkg.Artifacts[0].SHA256
		}
	}
	f.t.Fatalf("no package at version %s", version)

	return ""
}

func (f *fixture) options() acquire.Options {
	return acquire.Options{
		CacheDir:       filepath.Join(f.dir, "packages"),
		StatePath:      filepath.Join(f.dir, "catalog-state.json"),
		AcceptancePath: filepath.Join(f.dir, "license-acceptance.json"),
		AnchorsPath:    f.anchorsPath,
		CatalogURL:     f.server.URL,
		HTTP:           f.server.Client(),
		Now:            func() time.Time { return testNow },
	}
}

// resolver builds a resolver against this fixture. Every test goes through it,
// so no test can accidentally construct one that skips a step.
func (f *fixture) resolver(mutate ...func(*aue.Resolver, *acquire.Options)) *aue.Resolver {
	f.t.Helper()

	options := f.options()
	resolver := &aue.Resolver{
		CompanionVersion: companionVersion,
		PinPath:          filepath.Join(f.dir, "extractor-pin.json"),
		Install:          true,
	}
	for _, apply := range mutate {
		if apply == nil {
			continue
		}
		apply(resolver, &options)
	}
	acquirer, err := acquire.New(options)
	if err != nil {
		f.t.Fatal(err)
	}
	resolver.Acquirer = acquirer

	return resolver
}

// authorizing is the Authorize hook shape a backend-hosted artifact uses: it
// rewrites the URL and records that it was asked.
func (f *fixture) authorizing(options *acquire.Options) {
	options.Authorize = func(_ context.Context, artifact catalog.Artifact) (string, error) {
		f.authorized = append(f.authorized, artifact.URL)

		return artifact.URL + "?grant=fixture", nil
	}
}

func containsAll(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			return false
		}
	}

	return true
}
