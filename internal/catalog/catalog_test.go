package catalog

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

func TestAValidChainVerifies(t *testing.T) {
	keyring := testKeyring(t)
	signer := testKey(t, "catalog-1.key.json").KeyID
	keyringEnvelope, catalogEnvelope := chain(t, keyring, testCatalog(t, signer))

	verified, err := testVerifier(t).Verify(reencode(t, keyringEnvelope), reencode(t, catalogEnvelope))
	if err != nil {
		t.Fatalf("a valid chain did not verify: %v", err)
	}
	if got, want := verified.KeyringSigners, []string{testKey(t, "anchor-1.key.json").KeyID}; !equal(got, want) {
		t.Errorf("keyring signers are %v, want %v", got, want)
	}
	if got, want := verified.CatalogSigners, []string{signer}; !equal(got, want) {
		t.Errorf("catalogue signers are %v, want %v", got, want)
	}
	if verified.CatalogDigest == "" || verified.KeyringDigest == "" {
		t.Error("a verified chain must identify the exact documents it verified")
	}
}

func TestAKeyIDIsDerivedFromTheKeyAndCannotBeChosen(t *testing.T) {
	key := publicEntry(t, "catalog-1.key.json", StatusActive)
	key.KeyID = "0000000000000000"
	if _, err := key.Public(); err == nil {
		t.Fatal("a key published under a key id that is not its own was accepted")
	} else if !strings.Contains(err.Error(), "derived from the key") {
		t.Errorf("the error does not explain why: %v", err)
	}
}

func TestAnAnchorMayNotSignACatalogueAndACatalogueKeyMayNotSignAKeyring(t *testing.T) {
	// A catalogue signed by the anchor key. The anchor is the trust root and
	// its private half is meant to be offline; a build that accepted it as a
	// catalogue signature would make the two levels one.
	document := testCatalog(t, testKey(t, "anchor-1.key.json").KeyID)
	catalogEnvelope, err := Sign(document, testPrivate(t, "anchor-1.key.json"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	keyring := testKeyring(t, publicEntry(t, "anchor-1.key.json", StatusActive))
	keyringEnvelope, err := Sign(keyring, testPrivate(t, "anchor-1.key.json"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	// The keyring itself is refused: it names no catalogue key at all.
	if _, _, err := testVerifier(t).VerifyKeyring(reencode(t, keyringEnvelope)); err == nil {
		t.Fatal("a keyring naming only an anchor key was accepted")
	}

	// And with a proper keyring, the anchor's signature on the catalogue is
	// not one of the keys permitted to sign one.
	good := testKeyring(t)
	goodEnvelope, _ := chain(t, good, document)
	_ = goodEnvelope
	verifier := testVerifier(t)
	verifiedKeyring, _, err := verifier.VerifyKeyring(reencode(t, mustSign(t, good, testPrivate(t, "anchor-1.key.json"))))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, _, err := verifier.VerifyCatalog(verifiedKeyring, reencode(t, catalogEnvelope)); !errors.Is(err, ErrNoSignature) {
		t.Fatalf("an anchor-signed catalogue was accepted, or failed for the wrong reason: %v", err)
	}
}

func TestAnEditedPayloadIsRefusedEvenThoughTheSignatureIsIntact(t *testing.T) {
	keyring := testKeyring(t)
	signer := testKey(t, "catalog-1.key.json").KeyID
	_, catalogEnvelope := chain(t, keyring, testCatalog(t, signer))

	edited := tamperPayload(t, catalogEnvelope, func(tree map[string]any) {
		packages := tree["packages"].([]any)
		artifact := packages[0].(map[string]any)["artifacts"].([]any)[0].(map[string]any)
		artifact["url"] = "https://elsewhere.invalid/other.tar.gz"
	})
	verifier := testVerifier(t)
	verifiedKeyring, _, err := verifier.VerifyKeyring(reencode(t, mustSign(t, keyring, testPrivate(t, "anchor-1.key.json"))))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, _, err := verifier.VerifyCatalog(verifiedKeyring, reencode(t, edited)); !errors.Is(err, ErrNoSignature) {
		t.Fatalf("an edited catalogue verified: %v", err)
	}
}

func TestANonCanonicalPayloadIsRefused(t *testing.T) {
	// The same document, re-encoded with an extra space. The signature is
	// wrong for it too, but the canonical check is what fires first and it is
	// the one that matters: it is what stops two parsers reading one signed
	// document differently.
	keyring := testKeyring(t)
	keyringEnvelope := mustSign(t, keyring, testPrivate(t, "anchor-1.key.json"))
	payload, err := keyringEnvelope.PayloadBytes()
	if err != nil {
		t.Fatalf("%v", err)
	}
	spaced := &Signed{
		Payload:    base64.StdEncoding.EncodeToString(append([]byte(" "), payload...)),
		Signatures: keyringEnvelope.Signatures,
	}
	if _, _, err := testVerifier(t).VerifyKeyring(spaced); err == nil {
		t.Fatal("a payload with leading whitespace was accepted")
	}
}

func TestADuplicatedMemberCannotSurviveTheCanonicalCheck(t *testing.T) {
	// Two parsers can disagree about which value a duplicated member has. The
	// canonical re-encoding has one of them, so it cannot equal a payload that
	// carried both, and the document is refused rather than acted on.
	keyring := testKeyring(t)
	envelope := mustSign(t, keyring, testPrivate(t, "anchor-1.key.json"))
	raw, err := envelope.PayloadBytes()
	if err != nil {
		t.Fatalf("%v", err)
	}
	payload := string(raw)
	doubled := strings.Replace(payload, `"serial":7`, `"serial":7,"serial":9`, 1)
	if doubled == payload {
		t.Fatal("the fixture payload does not contain the member this test duplicates")
	}
	tampered := &Signed{Payload: base64.StdEncoding.EncodeToString([]byte(doubled)), Signatures: envelope.Signatures}
	if _, _, err := testVerifier(t).VerifyKeyring(tampered); err == nil {
		t.Fatal("a payload naming the same member twice was accepted")
	}
}

func TestAnExpiredDocumentIsRefused(t *testing.T) {
	keyring := testKeyring(t)
	keyring.ExpiresAt = TestNow.Add(-time.Hour)
	envelope := mustSign(t, keyring, testPrivate(t, "anchor-1.key.json"))

	_, _, err := testVerifier(t).VerifyKeyring(reencode(t, envelope))
	var expired *ExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("an expired keyring was accepted, or failed for another reason: %v", err)
	}
	if !strings.Contains(expired.Error(), "fetch a current one") {
		t.Errorf("an expiry error should say what to do: %v", expired)
	}
}

func TestAKeyOutsideItsValidityWindowMayNotSign(t *testing.T) {
	key := publicEntry(t, "catalog-1.key.json", StatusActive)
	key.NotBefore = TestNow.Add(24 * time.Hour)
	key.NotAfter = TestNow.Add(48 * time.Hour)
	keyring := testKeyring(t, key)

	verifier := testVerifier(t)
	verifiedKeyring, _, err := verifier.VerifyKeyring(reencode(t, mustSign(t, keyring, testPrivate(t, "anchor-1.key.json"))))
	if err != nil {
		t.Fatalf("%v", err)
	}
	document := mustSign(t, testCatalog(t, key.KeyID), testPrivate(t, "catalog-1.key.json"))
	_, _, err = verifier.VerifyCatalog(verifiedKeyring, reencode(t, document))
	if !errors.Is(err, ErrNoSignature) || !strings.Contains(err.Error(), "not valid until") {
		t.Fatalf("a key signing before it was valid was accepted: %v", err)
	}
}

func TestRotationRetiresOneKeyAndActivatesAnother(t *testing.T) {
	// The normal end of a key's life: superseded, not compromised. Everything
	// it signed before stays signed; it simply stops signing new documents.
	retired := publicEntry(t, "catalog-1.key.json", StatusRetired)
	current := publicEntry(t, "catalog-2.key.json", StatusActive)
	keyring := testKeyring(t, retired, current)
	keyring.Serial = 8

	verifier := testVerifier(t)
	verifiedKeyring, _, err := verifier.VerifyKeyring(reencode(t, mustSign(t, keyring, testPrivate(t, "anchor-1.key.json"))))
	if err != nil {
		t.Fatalf("the rotated keyring did not verify: %v", err)
	}

	// The retired key can no longer sign.
	old := mustSign(t, testCatalog(t, retired.KeyID), testPrivate(t, "catalog-1.key.json"))
	if _, _, err := verifier.VerifyCatalog(verifiedKeyring, reencode(t, old)); err == nil {
		t.Fatal("a retired key signed a catalogue that was accepted")
	} else if !strings.Contains(err.Error(), "retired") {
		t.Errorf("the error does not say the key was retired: %v", err)
	}

	// The new one can.
	fresh := mustSign(t, testCatalog(t, current.KeyID), testPrivate(t, "catalog-2.key.json"))
	if _, _, err := verifier.VerifyCatalog(verifiedKeyring, reencode(t, fresh)); err != nil {
		t.Fatalf("the rotated-to key could not sign: %v", err)
	}
}

func TestARevokedKeyIsRefusedAndStaysRefusedForever(t *testing.T) {
	revoked := publicEntry(t, "catalog-1.key.json", StatusRevoked)
	current := publicEntry(t, "catalog-2.key.json", StatusActive)
	keyring := testKeyring(t, revoked, current)

	verifier := testVerifier(t)
	verifiedKeyring, _, err := verifier.VerifyKeyring(reencode(t, mustSign(t, keyring, testPrivate(t, "anchor-1.key.json"))))
	if err != nil {
		t.Fatalf("%v", err)
	}
	compromised := mustSign(t, testCatalog(t, revoked.KeyID), testPrivate(t, "catalog-1.key.json"))
	if _, _, err := verifier.VerifyCatalog(verifiedKeyring, reencode(t, compromised)); err == nil {
		t.Fatal("a revoked key signed a catalogue that was accepted")
	}

	// The revocation is now this machine's, not the document's. A later
	// keyring that quietly forgets it — which is exactly what whoever holds
	// the stolen key would publish — does not bring the key back.
	if _, held := verifier.State.RevokedKeys[revoked.KeyID]; !held {
		t.Fatal("the revocation was not recorded locally")
	}
	forgetful := testKeyring(t, publicEntry(t, "catalog-1.key.json", StatusActive))
	forgetful.Serial = 9
	verifiedAgain, _, err := verifier.VerifyKeyring(reencode(t, mustSign(t, forgetful, testPrivate(t, "anchor-1.key.json"))))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, _, err := verifier.VerifyCatalog(verifiedAgain, reencode(t, compromised)); err == nil {
		t.Fatal("a later keyring un-revoked a key")
	} else if !strings.Contains(err.Error(), "revoked on this machine") {
		t.Errorf("the error does not say the machine remembers: %v", err)
	}
}

func TestARolledBackSerialIsRefused(t *testing.T) {
	signer := testKey(t, "catalog-1.key.json").KeyID
	keyring := testKeyring(t)
	verifier := testVerifier(t)

	current := testCatalog(t, signer)
	current.Serial = 12
	verifiedKeyring, _, err := verifier.VerifyKeyring(reencode(t, mustSign(t, keyring, testPrivate(t, "anchor-1.key.json"))))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, _, err := verifier.VerifyCatalog(verifiedKeyring, reencode(t, mustSign(t, current, testPrivate(t, "catalog-1.key.json")))); err != nil {
		t.Fatalf("%v", err)
	}

	older := testCatalog(t, signer)
	older.Serial = 11
	_, _, err = verifier.VerifyCatalog(verifiedKeyring, reencode(t, mustSign(t, older, testPrivate(t, "catalog-1.key.json"))))
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("a correctly signed older catalogue was accepted: %v", err)
	}

	// The same serial again is fine: re-fetching what you already have is not
	// a rollback, and refusing it would make an idempotent fetch an error.
	same := testCatalog(t, signer)
	same.Serial = 12
	if _, _, err := verifier.VerifyCatalog(verifiedKeyring, reencode(t, mustSign(t, same, testPrivate(t, "catalog-1.key.json")))); err != nil {
		t.Fatalf("re-fetching the same serial failed: %v", err)
	}
}

func TestARevokedArtifactIsRefusedAndTheMachineRemembersIt(t *testing.T) {
	signer := testKey(t, "catalog-1.key.json").KeyID
	document := testCatalog(t, signer)
	digest := document.Packages[0].Artifacts[0].SHA256
	document.Revocations = []Revocation{{
		Digest: digest,
		Reason: "the published build was not the one that was built",
		At:     TestNow.Add(-time.Hour),
	}}
	keyringEnvelope, catalogEnvelope := chain(t, testKeyring(t), document)

	verifier := testVerifier(t)
	verified, err := verifier.Verify(reencode(t, keyringEnvelope), reencode(t, catalogEnvelope))
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, _, err = verified.Artifact(verifier.State, "fixture.tool", "", profile.Platform{OS: "linux", Arch: "amd64"})
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("a revoked artifact was offered for download: %v", err)
	}

	// And a later catalogue that drops the revocation does not restore it.
	clean := testCatalog(t, signer)
	clean.Serial = 13
	cleanVerified, err := verifier.Verify(reencode(t, keyringEnvelope), reencode(t, mustSign(t, clean, testPrivate(t, "catalog-1.key.json"))))
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, _, err = cleanVerified.Artifact(verifier.State, "fixture.tool", "", profile.Platform{OS: "linux", Arch: "amd64"})
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("a later catalogue un-revoked an artifact: %v", err)
	}
}

func TestAnArtifactAttributedToAKeyThatDidNotSignIsRefused(t *testing.T) {
	// The catalogue is signed by catalog-1 and its entry claims catalog-2. A
	// signature over the whole document does not make every claim inside it
	// true, and "who vouched for this entry" is a claim.
	document := testCatalog(t, testKey(t, "catalog-2.key.json").KeyID)
	keyring := testKeyring(t, publicEntry(t, "catalog-1.key.json", StatusActive), publicEntry(t, "catalog-2.key.json", StatusActive))
	keyringEnvelope, catalogEnvelope := chain(t, keyring, document)

	_, err := testVerifier(t).Verify(reencode(t, keyringEnvelope), reencode(t, catalogEnvelope))
	if err == nil {
		t.Fatal("an entry attributed to a key that did not sign the catalogue was accepted")
	}
	if !strings.Contains(err.Error(), "did not sign this catalogue") {
		t.Errorf("the error does not explain why: %v", err)
	}
}

func TestAnUnconfiguredTrustAnchorRefusesEverything(t *testing.T) {
	keyringEnvelope, _ := chain(t, testKeyring(t), testCatalog(t, testKey(t, "catalog-1.key.json").KeyID))
	verifier := &Verifier{State: NewState(), Now: func() time.Time { return TestNow }}
	_, _, err := verifier.VerifyKeyring(reencode(t, keyringEnvelope))
	if !errors.Is(err, ErrNoAnchors) {
		t.Fatalf("a verifier with no anchors accepted something: %v", err)
	}
	if !strings.Contains(err.Error(), EnvAnchorsPath) {
		t.Errorf("the error must name the variable to set: %v", err)
	}
}

func TestACatalogueURLMustBeHTTPSAndCarryNoCredentials(t *testing.T) {
	for _, bad := range []string{
		"http://example.invalid/tool.tar.gz",
		"ftp://example.invalid/tool.tar.gz",
		"https://user:secret@example.invalid/tool.tar.gz",
		"/tool.tar.gz",
	} {
		if err := checkArtifactURL(bad); err == nil {
			t.Errorf("%q was accepted as an artifact URL", bad)
		}
	}
	if err := checkArtifactURL("https://example.invalid/tool.tar.gz"); err != nil {
		t.Errorf("a plain https URL was refused: %v", err)
	}
}

func TestRedactURLKeepsOnlyWhatAPersonNeeds(t *testing.T) {
	cases := map[string]string{
		"https://cdn.invalid/a/b.tar.gz":                          "https://cdn.invalid/a/b.tar.gz",
		"https://cdn.invalid/a/b.tar.gz?X-Amz-Signature=deadbeef": "https://cdn.invalid/a/b.tar.gz [query redacted]",
		"https://user:hunter2@cdn.invalid/a/b.tar.gz":             "https://cdn.invalid/a/b.tar.gz [query redacted]",
	}
	for input, want := range cases {
		if got := RedactURL(input); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", input, got, want)
		}
		if strings.Contains(RedactURL(input), "deadbeef") || strings.Contains(RedactURL(input), "hunter2") {
			t.Errorf("RedactURL(%q) leaked a credential", input)
		}
	}
}

func TestTheTrustStateSurvivesARoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog-state.json")

	state := NewState()
	state.CatalogSerial["auto-pigeon-test"] = 12
	state.RevokedKeys["dead"] = "it leaked"
	state.RevokedArtifacts["sha256:"+strings.Repeat("ab", 32)] = Revocation{
		Digest: "sha256:" + strings.Repeat("ab", 32), Reason: "withdrawn", At: TestNow,
	}
	if err := SaveState(path, state); err != nil {
		t.Fatalf("%v", err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if loaded.CatalogSerial["auto-pigeon-test"] != 12 {
		t.Error("the serial ratchet did not survive")
	}
	if _, held := loaded.RevokedArtifacts["sha256:"+strings.Repeat("ab", 32)]; !held {
		t.Error("a revocation did not survive")
	}
	if err := loaded.checkCatalogSerial("auto-pigeon-test", 11); !errors.Is(err, ErrRollback) {
		t.Errorf("a reloaded ratchet does not hold: %v", err)
	}
}

func TestAMissingTrustStateIsEmptyAndACorruptOneIsAnError(t *testing.T) {
	dir := t.TempDir()
	state, err := LoadState(filepath.Join(dir, "absent.json"))
	if err != nil || state == nil {
		t.Fatalf("a machine that has never fetched a catalogue must have empty state: %v", err)
	}
	// A file that exists and cannot be read must not silently become empty
	// state: that would reset the ratchet, which is the one thing deleting the
	// file is supposed to be unable to do quietly.
	corrupt := filepath.Join(dir, "corrupt.json")
	if err := writeFile(corrupt, "{not json"); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := LoadState(corrupt); err == nil {
		t.Fatal("a corrupt trust state was read as empty")
	}
}

func TestUpstreamVersionsOrderNaturally(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.4", -1},
		{"1.10.0", "1.9.0", 1},
		{"2.0.0", "10.0.0", -1},
		{"20240115", "20231201", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.0-alpha", "1.0.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAnArchivePathThatEscapesOrIsAbsoluteIsRefused(t *testing.T) {
	for _, bad := range []string{
		"", "../etc/passwd", "/etc/passwd", "a/../../b", "C:/windows/system32",
		"~/bin/tool", "a//b", "a/./b", `a\b`, "a/b/", "aux/tool", "trailing ", "dot.",
	} {
		if err := CheckArchivePath(bad); err == nil {
			t.Errorf("the archive path %q was accepted", bad)
		}
	}
	for _, good := range []string{"tool", "bin/tool", "a/b/c.exe", "tool-1.2.3/bin/qbsp"} {
		if err := CheckArchivePath(good); err != nil {
			t.Errorf("the archive path %q was refused: %v", good, err)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
