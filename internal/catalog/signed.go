package catalog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// AlgorithmEd25519 is the only signature algorithm this build accepts.
//
// One algorithm rather than a negotiated set. An envelope that offers a choice
// is an envelope where the weakest option is the one that matters, and the
// history of signed-document formats is mostly the history of that sentence.
// A second algorithm arrives with a new envelope version, not with a new value
// in this field.
const AlgorithmEd25519 = "ed25519"

// signingContext is prepended to every payload before it is signed.
//
// Domain separation: a signature is over "this program's signed document,
// version 1, followed by these bytes", so a signature produced for some other
// protocol by the same key cannot be presented here, and vice versa. The
// document's own `schema_version` distinguishes a keyring from a catalogue
// inside that space.
const signingContext = "auto-pigeon-companion/signed-document/1\n"

// ErrNoSignature reports an envelope no permitted key signed. It is the error
// every failure of the signature check funnels into, because "the signature is
// by an unknown key", "the signature does not verify" and "the only signer is
// revoked" are the same outcome from a caller's point of view: nothing vouches
// for these bytes.
var ErrNoSignature = errors.New("catalog: no valid signature by a permitted key")

// ErrNotCanonical reports a payload that is not the canonical encoding of what
// it decodes to. See the package comment: this is the check that makes a
// signature mean one document rather than two.
var ErrNotCanonical = errors.New("catalog: the signed payload is not in canonical form")

// Signature is one key's word on a payload.
type Signature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	// Signature is standard base64 of the raw Ed25519 signature.
	Signature string `json:"signature"`
}

// Signed is the wire form of a signed document: the exact bytes that were
// signed, and who signed them.
//
// The payload is carried as base64 rather than as embedded JSON, and that is
// not a stylistic choice. A signature is over bytes, and embedded JSON does not
// survive being re-encoded: `json.MarshalIndent` re-indents a `json.RawMessage`,
// a proxy may reformat, and every one of those produces a document whose
// signature no longer covers what is in front of the reader. Base64 makes the
// signed bytes an opaque value that nothing on the way is entitled to reformat.
//
// The cost is that the document is not readable in a text editor.
// `companion catalog show --json` prints the payload, which is the same
// trade-off every signed-envelope format makes.
type Signed struct {
	// Payload is standard base64 of the canonical document.
	Payload    string      `json:"payload"`
	Signatures []Signature `json:"signatures"`
}

// PayloadBytes decodes the signed bytes.
func (s *Signed) PayloadBytes() ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s.Payload)
	if err != nil {
		return nil, fmt.Errorf("catalog: the signed payload is not base64: %w", err)
	}
	return raw, nil
}

// KeyID is the identifier of an Ed25519 public key: the first sixteen bytes of
// its SHA-256, in hex.
//
// Derived rather than chosen, so a key cannot be published under two names and
// so an entry that claims a key id can be checked against the key it carries. A
// keyring whose key id does not match its own public key is refused.
func KeyID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:8])
}

// GenerateKey mints a signing key. It is used by `companion catalog keygen` and
// by the tests; no key it produces is in this repository.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("catalog: generating a signing key: %w", err)
	}
	return public, private, nil
}

// Sign produces a signed envelope over a document.
//
// The payload it writes is the canonical encoding, which is the only encoding
// [Verify] will accept. There is deliberately no way to sign bytes a caller
// supplies: a signer that could be handed arbitrary bytes could be handed
// non-canonical ones, and the document it signed would then be a different
// document from the one it was shown.
func Sign(document any, keys ...ed25519.PrivateKey) (*Signed, error) {
	if len(keys) == 0 {
		return nil, errors.New("catalog: signing needs at least one key")
	}
	payload, err := profile.CanonicalDocument(document)
	if err != nil {
		return nil, fmt.Errorf("catalog: canonicalizing for signature: %w", err)
	}
	envelope := &Signed{Payload: base64.StdEncoding.EncodeToString(payload)}
	for _, key := range keys {
		public, ok := key.Public().(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("catalog: a signing key is not Ed25519")
		}
		envelope.Signatures = append(envelope.Signatures, Signature{
			KeyID:     KeyID(public),
			Algorithm: AlgorithmEd25519,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, signingInput(payload))),
		})
	}
	return envelope, nil
}

func signingInput(payload []byte) []byte {
	input := make([]byte, 0, len(signingContext)+len(payload))
	input = append(input, signingContext...)
	return append(input, payload...)
}

// Marshal renders an envelope for transport, indented so a person can read what
// they are about to publish.
func (s *Signed) Marshal() ([]byte, error) {
	encoded, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("catalog: encoding the signed document: %w", err)
	}
	return append(encoded, '\n'), nil
}

// DecodeSigned reads an envelope, refusing members this build does not know.
func DecodeSigned(data []byte) (*Signed, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var s Signed
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("catalog: reading the signed document: %w", err)
	}
	if decoder.More() {
		return nil, errors.New("catalog: the signed document has trailing content")
	}
	if len(s.Payload) == 0 {
		return nil, errors.New("catalog: the signed document has no payload")
	}
	if len(s.Signatures) == 0 {
		return nil, fmt.Errorf("%w: the document carries none at all", ErrNoSignature)
	}
	return &s, nil
}

// Digest is the payload's SHA-256, as `sha256:<hex>`. It identifies one exact
// signed document in a log or a local record.
func (s *Signed) Digest() string {
	raw, err := s.PayloadBytes()
	if err != nil {
		return ""
	}
	return profile.DigestBytes(raw)
}

// verifier is what a signature check needs to know about the keys it may trust.
type verifier interface {
	// permits returns the public key for a key id, and whether that key may
	// sign at this moment. The reason is for the error message.
	permits(keyID string) (ed25519.PublicKey, string, bool)
}

// verifySignatures returns the key ids that vouched for the payload.
//
// Every signature is examined rather than stopping at the first valid one,
// because a catalogue entry names the key that signed it and that name has to
// be checked against the full set. A malformed signature by one key does not
// invalidate a good one by another; a document with no good one at all is
// refused.
func (s *Signed) verifySignatures(payload []byte, keys verifier) ([]string, error) {
	var (
		signers  []string
		rejected []string
		seen     = map[string]bool{}
	)
	input := signingInput(payload)
	for _, signature := range s.Signatures {
		if seen[signature.KeyID] {
			return nil, fmt.Errorf("catalog: key %s signs this document twice", signature.KeyID)
		}
		seen[signature.KeyID] = true

		if signature.Algorithm != AlgorithmEd25519 {
			rejected = append(rejected, fmt.Sprintf("%s uses %q, and this build only accepts %s",
				signature.KeyID, signature.Algorithm, AlgorithmEd25519))
			continue
		}
		public, reason, ok := keys.permits(signature.KeyID)
		if !ok {
			rejected = append(rejected, fmt.Sprintf("%s %s", signature.KeyID, reason))
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(signature.Signature)
		if err != nil {
			rejected = append(rejected, fmt.Sprintf("%s carries a signature that is not base64", signature.KeyID))
			continue
		}
		if len(raw) != ed25519.SignatureSize || !ed25519.Verify(public, input, raw) {
			rejected = append(rejected, fmt.Sprintf("%s signed something other than these bytes", signature.KeyID))
			continue
		}
		signers = append(signers, signature.KeyID)
	}
	if len(signers) == 0 {
		if len(rejected) == 0 {
			return nil, ErrNoSignature
		}
		return nil, fmt.Errorf("%w: %s", ErrNoSignature, strings.Join(rejected, "; "))
	}
	return signers, nil
}

// checkCanonical refuses a payload that is not the canonical encoding of the
// value it decoded to.
//
// The comparison is against a re-encoding of the *decoded* document, so it also
// catches a payload carrying a member that decoded to nothing: a duplicated
// key, a member this build drops, a different spelling of the same number. All
// of those are cases where the bytes a signature covers and the document this
// program acts on are not the same thing.
func checkCanonical(payload []byte, decoded any) error {
	canonical, err := profile.CanonicalDocument(decoded)
	if err != nil {
		return fmt.Errorf("catalog: canonicalizing the signed payload: %w", err)
	}
	if !bytes.Equal(payload, canonical) {
		return fmt.Errorf("%w: re-encoding what it says produces different bytes, so a signature over it "+
			"does not identify one document; publish it with `companion catalog sign`", ErrNotCanonical)
	}
	return nil
}

// decodeStrict reads a payload into a document type, refusing unknown members.
func decodeStrict(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("catalog: reading the signed payload: %w", err)
	}
	if decoder.More() {
		return errors.New("catalog: the signed payload has trailing content")
	}
	return nil
}

// DecodeKeyring reads an *unsigned* keyring document — the thing a publisher
// writes and then signs. It decodes strictly and does not validate: validation
// against the keys that are about to sign is the signer's job, and doing it
// here would make this function unusable for the document that fails it.
func DecodeKeyring(data []byte) (*Keyring, error) {
	var keyring Keyring
	if err := decodeStrict(data, &keyring); err != nil {
		return nil, err
	}
	return &keyring, nil
}

// DecodeCatalog reads an *unsigned* catalogue document. See [DecodeKeyring].
func DecodeCatalog(data []byte) (*Catalog, error) {
	var document Catalog
	if err := decodeStrict(data, &document); err != nil {
		return nil, err
	}
	return &document, nil
}
