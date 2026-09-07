package catalog

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The fixtures every test in this package builds on.
//
// A whole valid chain is a lot of document, and a test that spelled one out
// would be a test whose interesting line is on screen four. So the helpers
// below produce a chain that verifies, and each test breaks exactly one thing
// about it. When a test fails, the difference between it and [chain] is the
// thing being tested.

// TestNow is the moment the fixtures are valid at. Fixed rather than time.Now
// so that a test about expiry is a test about expiry and not about when it ran.
var TestNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func testKey(t *testing.T, name string) *PrivateKeyFile {
	t.Helper()
	key, err := LoadPrivateKey(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("loading the fixture key %s: %v", name, err)
	}
	return key
}

func testPrivate(t *testing.T, name string) ed25519.PrivateKey {
	t.Helper()
	private, err := testKey(t, name).Private()
	if err != nil {
		t.Fatalf("%v", err)
	}
	return private
}

func testAnchors(t *testing.T) *Anchors {
	t.Helper()
	anchors, err := LoadAnchors(filepath.Join("testdata", "anchors.json"))
	if err != nil {
		t.Fatalf("loading the fixture anchors: %v", err)
	}
	return anchors
}

// testKeyring is a keyring naming one active catalogue key.
func testKeyring(t *testing.T, keys ...Key) *Keyring {
	t.Helper()
	if len(keys) == 0 {
		keys = []Key{publicEntry(t, "catalog-1.key.json", StatusActive)}
	}
	return &Keyring{
		SchemaVersion: KeyringSchemaVersion,
		KeyringID:     "auto-pigeon-test",
		Serial:        7,
		IssuedAt:      TestNow.Add(-24 * time.Hour),
		ExpiresAt:     TestNow.Add(90 * 24 * time.Hour),
		Keys:          keys,
	}
}

func publicEntry(t *testing.T, name, status string) Key {
	t.Helper()
	key, err := testKey(t, name).PublicEntry(TestNow.Add(-365*24*time.Hour), TestNow.Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("%v", err)
	}
	key.Status = status
	if status == StatusRevoked {
		revoked := TestNow.Add(-time.Hour)
		key.RevokedAt = &revoked
		key.Reason = "the fixture says it was compromised"
	}
	return key
}

// testCatalog is a catalogue with one package, signed by catalog-1.
func testCatalog(t *testing.T, signer string) *Catalog {
	t.Helper()
	return &Catalog{
		SchemaVersion: SchemaVersion,
		CatalogID:     "auto-pigeon-test",
		Serial:        12,
		IssuedAt:      TestNow.Add(-24 * time.Hour),
		ExpiresAt:     TestNow.Add(30 * 24 * time.Hour),
		Packages: []Package{{
			ID:      "fixture.tool",
			Version: "1.2.3",
			Name:    "Fixture Tool",
			Source:  profile.Source{Homepage: "https://example.invalid/fixture"},
			License: profile.License{
				SPDX:                "GPL-2.0-or-later",
				CorrespondingSource: "https://example.invalid/fixture/source",
			},
			Artifacts: []Artifact{{
				Platform:     profile.Platform{OS: "linux", Arch: "amd64"},
				URL:          "https://example.invalid/fixture/1.2.3/linux-amd64.tar.gz",
				Kind:         KindTarGz,
				Size:         4096,
				SHA256:       "sha256:" + strings.Repeat("ab", 32),
				Signer:       signer,
				UnpackedSize: 65536,
				Executables:  []string{"bin/fixture"},
			}},
		}},
	}
}

// chain signs a keyring and a catalogue and returns both envelopes.
func chain(t *testing.T, keyring *Keyring, document *Catalog) (*Signed, *Signed) {
	t.Helper()
	keyringEnvelope, err := Sign(keyring, testPrivate(t, "anchor-1.key.json"))
	if err != nil {
		t.Fatalf("signing the keyring: %v", err)
	}
	catalogEnvelope, err := Sign(document, testPrivate(t, "catalog-1.key.json"))
	if err != nil {
		t.Fatalf("signing the catalogue: %v", err)
	}
	return keyringEnvelope, catalogEnvelope
}

// mustSign signs a document and fails the test if it cannot.
func mustSign(t *testing.T, document any, keys ...ed25519.PrivateKey) *Signed {
	t.Helper()
	envelope, err := Sign(document, keys...)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return envelope
}

// writeFile is the one-line file write the tests need.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func testVerifier(t *testing.T) *Verifier {
	t.Helper()
	return &Verifier{Anchors: testAnchors(t), State: NewState(), Now: func() time.Time { return TestNow }}
}

// reencode round-trips a signed envelope through JSON, which is what a real one
// goes through, so that a test cannot accidentally verify an in-memory value a
// transport would never produce.
func reencode(t *testing.T, envelope *Signed) *Signed {
	t.Helper()
	raw, err := envelope.Marshal()
	if err != nil {
		t.Fatalf("%v", err)
	}
	decoded, err := DecodeSigned(raw)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return decoded
}

// tamperPayload rewrites one member of a signed payload, leaving the signature
// as it was. It is how every "somebody edited this after it was signed" test is
// written.
func tamperPayload(t *testing.T, envelope *Signed, edit func(map[string]any)) *Signed {
	t.Helper()
	payload, err := envelope.PayloadBytes()
	if err != nil {
		t.Fatalf("%v", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(payload, &tree); err != nil {
		t.Fatalf("%v", err)
	}
	edit(tree)
	rewritten, err := profile.CanonicalDocument(tree)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return &Signed{Payload: base64.StdEncoding.EncodeToString(rewritten), Signatures: envelope.Signatures}
}
