package catalog

import (
	"fmt"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Verified is a keyring and a catalogue that have passed every check, together
// with who vouched for them.
//
// It exists so that "verified" is a *type* and not a convention. Nothing in
// this package returns a bare [Catalog] from untrusted bytes, so no caller can
// hold one that has not been through [Verifier.Verify] — the failure mode where
// a refactor moves a check and one path stops performing it is not available.
type Verified struct {
	Keyring        *Keyring
	Catalog        *Catalog
	KeyringSigners []string
	CatalogSigners []string
	// KeyringDigest and CatalogDigest identify the exact documents, for the
	// install record and for a log a person has to reason about later.
	KeyringDigest string
	CatalogDigest string
	VerifiedAt    time.Time
}

// Verifier checks signed documents against a build's trust anchors and this
// machine's memory.
type Verifier struct {
	// Anchors is the trust root. Required: a verifier with none refuses
	// everything, which is the point.
	Anchors *Anchors
	// State carries the serial ratchet and the sticky revocations. It is
	// updated in place by a successful verification; persisting it is the
	// caller's, because only the caller knows where it lives.
	State *State
	// Now is the clock, a field so expiry is testable without waiting.
	Now func() time.Time
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now().UTC()
	}
	return time.Now().UTC()
}

func (v *Verifier) state() *State {
	if v.State == nil {
		v.State = NewState()
	}
	v.State.fill()
	return v.State
}

// ExpiredError reports a document past its expiry.
//
// Expiry is what bounds a freeze attack: without it, somebody who can control
// what a machine fetches can serve the newest catalogue they like forever, and
// the machine has no way to notice that it has stopped being told about new
// revocations. Its own error type because "this is stale" needs a different
// sentence from "this is forged" — one of them is fixed by fetching again.
type ExpiredError struct {
	Document  string
	ID        string
	Serial    int64
	ExpiredAt time.Time
	Now       time.Time
}

func (e *ExpiredError) Error() string {
	return fmt.Sprintf("catalog: the %s %s (serial %d) expired on %s and it is now %s; fetch a current one",
		e.Document, e.ID, e.Serial, e.ExpiredAt.UTC().Format(time.RFC3339), e.Now.UTC().Format(time.RFC3339))
}

// VerifyKeyring checks a signed keyring against the trust anchors.
func (v *Verifier) VerifyKeyring(envelope *Signed) (*Keyring, []string, error) {
	if v.Anchors == nil || len(v.Anchors.Keys) == 0 {
		return nil, nil, ErrNoAnchors
	}
	state := v.state()
	now := v.now()

	payload, err := envelope.PayloadBytes()
	if err != nil {
		return nil, nil, err
	}
	var keyring Keyring
	if err := decodeStrict(payload, &keyring); err != nil {
		return nil, nil, err
	}
	if err := checkCanonical(payload, &keyring); err != nil {
		return nil, nil, err
	}
	if err := keyring.Validate(); err != nil {
		return nil, nil, err
	}
	signers, err := envelope.verifySignatures(payload, newKeySet(v.Anchors.Keys, RoleAnchor, now, state.RevokedKeys))
	if err != nil {
		return nil, nil, err
	}
	if !now.Before(keyring.ExpiresAt) {
		return nil, nil, &ExpiredError{Document: "keyring", ID: keyring.KeyringID, Serial: keyring.Serial,
			ExpiredAt: keyring.ExpiresAt, Now: now}
	}
	if err := state.checkKeyringSerial(keyring.KeyringID, keyring.Serial); err != nil {
		return nil, nil, err
	}
	state.recordKeyring(&keyring, now)
	return &keyring, signers, nil
}

// VerifyCatalog checks a signed catalogue against a keyring that has itself
// been verified.
//
// The keyring is a parameter rather than something this method fetches, so that
// the order — anchors, then keyring, then catalogue — is visible at the call
// site and cannot be short-circuited by a caller that happens to have a
// [Keyring] value lying around from somewhere else.
func (v *Verifier) VerifyCatalog(keyring *Keyring, envelope *Signed) (*Catalog, []string, error) {
	if keyring == nil {
		return nil, nil, fmt.Errorf("catalog: a catalogue cannot be verified without a keyring")
	}
	state := v.state()
	now := v.now()

	payload, err := envelope.PayloadBytes()
	if err != nil {
		return nil, nil, err
	}
	var catalog Catalog
	if err := decodeStrict(payload, &catalog); err != nil {
		return nil, nil, err
	}
	if err := checkCanonical(payload, &catalog); err != nil {
		return nil, nil, err
	}
	signers, err := envelope.verifySignatures(payload, newKeySet(keyring.Keys, RoleCatalog, now, state.RevokedKeys))
	if err != nil {
		return nil, nil, err
	}
	signerSet := make(map[string]bool, len(signers))
	for _, id := range signers {
		signerSet[id] = true
	}
	// Validation comes after the signature check because one of its rules —
	// that every artifact's `signer` is a key that signed this document —
	// cannot be stated until the signers are known.
	if err := catalog.Validate(signerSet); err != nil {
		return nil, nil, err
	}
	if !now.Before(catalog.ExpiresAt) {
		return nil, nil, &ExpiredError{Document: "catalogue", ID: catalog.CatalogID, Serial: catalog.Serial,
			ExpiredAt: catalog.ExpiresAt, Now: now}
	}
	if err := state.checkCatalogSerial(catalog.CatalogID, catalog.Serial); err != nil {
		return nil, nil, err
	}
	state.recordCatalog(&catalog, now)
	return &catalog, signers, nil
}

// Verify is the whole chain: anchors vouch for the keyring, the keyring vouches
// for the catalogue, and this machine's memory has the last word on both.
func (v *Verifier) Verify(keyringEnvelope, catalogEnvelope *Signed) (*Verified, error) {
	keyring, keyringSigners, err := v.VerifyKeyring(keyringEnvelope)
	if err != nil {
		return nil, err
	}
	catalog, catalogSigners, err := v.VerifyCatalog(keyring, catalogEnvelope)
	if err != nil {
		return nil, err
	}
	return &Verified{
		Keyring:        keyring,
		Catalog:        catalog,
		KeyringSigners: keyringSigners,
		CatalogSigners: catalogSigners,
		KeyringDigest:  keyringEnvelope.Digest(),
		CatalogDigest:  catalogEnvelope.Digest(),
		VerifiedAt:     v.now(),
	}, nil
}

// Artifact resolves a package id, an optional pinned version and a platform to
// the exact bytes to fetch, refusing anything revoked.
//
// It is the only way out of a [Verified] into something downloadable, so the
// revocation check cannot be bypassed by a caller that walks the packages
// itself.
func (verified *Verified) Artifact(state *State, id, version string, platform profile.Platform) (Package, Artifact, error) {
	pkg, ok := verified.Catalog.Find(id, version)
	if !ok {
		if version == "" {
			return Package{}, Artifact{}, fmt.Errorf("catalog: %s names no package %q", verified.Catalog.CatalogID, id)
		}
		known := verified.Catalog.Versions(id)
		if len(known) == 0 {
			return Package{}, Artifact{}, fmt.Errorf("catalog: %s names no package %q", verified.Catalog.CatalogID, id)
		}
		return Package{}, Artifact{}, fmt.Errorf("catalog: %s has no version %q of %s; it has %v", verified.Catalog.CatalogID, version, id, known)
	}
	artifact, ok := pkg.ArtifactFor(platform)
	if !ok {
		return Package{}, Artifact{}, fmt.Errorf("catalog: %s %s has no build for %s; it has builds for %v",
			pkg.ID, pkg.Version, platform, pkg.Platforms())
	}
	if revocation, revoked := verified.Catalog.Revoked(artifact.SHA256); revoked {
		return Package{}, Artifact{}, fmt.Errorf("%w: %s %s for %s was withdrawn on %s: %s",
			ErrRevoked, pkg.ID, pkg.Version, artifact.Platform,
			revocation.At.UTC().Format(time.RFC3339), revocation.Reason)
	}
	if revocation, revoked := state.ArtifactRevocation(artifact.SHA256); revoked {
		return Package{}, Artifact{}, fmt.Errorf("%w: %s %s for %s was withdrawn on %s and this machine has not forgotten: %s",
			ErrRevoked, pkg.ID, pkg.Version, artifact.Platform,
			revocation.At.UTC().Format(time.RFC3339), revocation.Reason)
	}
	return pkg, artifact, nil
}
