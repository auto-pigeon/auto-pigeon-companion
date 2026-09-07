package catalog

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

// KeyringSchemaVersion is the keyring document format.
const KeyringSchemaVersion = "aucom.keyring/1.0"

// Key roles. A role is checked, not decorative: an anchor that could also sign
// a catalogue would be an anchor whose private key is online, and the whole
// point of the two levels is that it is not.
const (
	// RoleAnchor signs the keyring, and nothing else.
	RoleAnchor = "anchor"
	// RoleCatalog signs a catalogue, and nothing else.
	RoleCatalog = "catalog"
)

// Key statuses.
const (
	// StatusActive: may sign, inside its validity window.
	StatusActive = "active"
	// StatusRetired: no longer signs new documents. Rotation's normal end
	// state — the key is not compromised, it is simply superseded, and saying
	// so is different from saying it was revoked.
	StatusRetired = "retired"
	// StatusRevoked: compromised or withdrawn. Everything it signed is
	// refused, and this machine remembers the revocation permanently.
	StatusRevoked = "revoked"
)

var (
	keyRoles    = []string{RoleAnchor, RoleCatalog}
	keyStatuses = []string{StatusActive, StatusRetired, StatusRevoked}
)

// Key is one public key and what it is allowed to do.
type Key struct {
	// KeyID is derived from the key, never chosen. See [KeyID].
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	// PublicKey is standard base64 of the raw 32-byte Ed25519 public key.
	PublicKey string `json:"public_key"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	// NotBefore and NotAfter bound the key. Both required: a key with no end
	// is a key that is still valid after everyone who knew about it has left,
	// and rotation with no overlap window is rotation that breaks every client
	// at once.
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	// RevokedAt and Reason are required for a revoked key. A revocation with
	// no reason is one nobody can act on.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Comment   string     `json:"comment,omitempty"`
}

// Public decodes the key's public half.
func (k Key) Public() (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(k.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("catalog: key %s has a public key that is not base64", k.KeyID)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("catalog: key %s has a %d-byte public key; Ed25519 keys are %d",
			k.KeyID, len(raw), ed25519.PublicKeySize)
	}
	public := ed25519.PublicKey(raw)
	if got := KeyID(public); got != k.KeyID {
		return nil, fmt.Errorf("catalog: the key published as %s is really %s; a key id is derived from the key, not chosen", k.KeyID, got)
	}
	return public, nil
}

func (k Key) validate() error {
	switch {
	case strings.TrimSpace(k.KeyID) == "":
		return errors.New("catalog: a key has no key_id")
	case k.Algorithm != AlgorithmEd25519:
		return fmt.Errorf("catalog: key %s uses %q; this build accepts %s", k.KeyID, k.Algorithm, AlgorithmEd25519)
	case !contains(keyRoles, k.Role):
		return fmt.Errorf("catalog: key %s has the role %q; the roles are %s", k.KeyID, k.Role, strings.Join(keyRoles, ", "))
	case !contains(keyStatuses, k.Status):
		return fmt.Errorf("catalog: key %s has the status %q; the statuses are %s", k.KeyID, k.Status, strings.Join(keyStatuses, ", "))
	case k.NotBefore.IsZero() || k.NotAfter.IsZero():
		return fmt.Errorf("catalog: key %s must say both not_before and not_after", k.KeyID)
	case !k.NotAfter.After(k.NotBefore):
		return fmt.Errorf("catalog: key %s expires at or before it begins", k.KeyID)
	case k.Status == StatusRevoked && (k.RevokedAt == nil || k.RevokedAt.IsZero() || strings.TrimSpace(k.Reason) == ""):
		return fmt.Errorf("catalog: key %s is revoked but does not say when and why", k.KeyID)
	case k.Status != StatusRevoked && k.RevokedAt != nil:
		return fmt.Errorf("catalog: key %s carries a revocation date but is %q", k.KeyID, k.Status)
	}
	if _, err := k.Public(); err != nil {
		return err
	}
	return nil
}

// usable reports whether a key may sign at a moment, and why not when it may
// not. The sentence is the error a user reads, so it says what to do about it.
func (k Key) usable(role string, now time.Time) (string, bool) {
	switch {
	case k.Status == StatusRevoked:
		return fmt.Sprintf("was revoked on %s: %s", k.RevokedAt.UTC().Format(time.RFC3339), k.Reason), false
	case k.Status == StatusRetired:
		return "has been retired and no longer signs new documents", false
	case k.Role != role:
		return fmt.Sprintf("is a %s key and may not sign a %s document", k.Role, role), false
	case now.Before(k.NotBefore):
		return fmt.Sprintf("is not valid until %s", k.NotBefore.UTC().Format(time.RFC3339)), false
	case !now.Before(k.NotAfter):
		return fmt.Sprintf("expired on %s", k.NotAfter.UTC().Format(time.RFC3339)), false
	}
	return "", true
}

// Keyring says which keys may sign a catalogue.
type Keyring struct {
	SchemaVersion string `json:"schema_version"`
	KeyringID     string `json:"keyring_id"`
	// Serial increases with every publication. It is the rollback protection:
	// a keyring with a lower serial than one this machine has already accepted
	// is a valid signature over a superseded answer.
	Serial    int64     `json:"serial"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Keys      []Key     `json:"keys"`
}

// Validate checks the document's own consistency, before any question of who
// signed it.
func (k *Keyring) Validate() error {
	switch {
	case k.SchemaVersion != KeyringSchemaVersion:
		return fmt.Errorf("catalog: the keyring is %q; this build reads %q", k.SchemaVersion, KeyringSchemaVersion)
	case strings.TrimSpace(k.KeyringID) == "":
		return errors.New("catalog: the keyring has no keyring_id")
	case k.Serial < 1:
		return fmt.Errorf("catalog: the keyring's serial is %d; serials start at 1 and only increase", k.Serial)
	case k.IssuedAt.IsZero() || k.ExpiresAt.IsZero():
		return errors.New("catalog: the keyring must say both issued_at and expires_at")
	case !k.ExpiresAt.After(k.IssuedAt):
		return errors.New("catalog: the keyring expires at or before it was issued")
	case len(k.Keys) == 0:
		return errors.New("catalog: the keyring names no keys")
	}
	seen := map[string]bool{}
	catalogKeys := 0
	for _, key := range k.Keys {
		if err := key.validate(); err != nil {
			return err
		}
		if seen[key.KeyID] {
			return fmt.Errorf("catalog: the keyring names key %s twice", key.KeyID)
		}
		seen[key.KeyID] = true
		if key.Role == RoleCatalog {
			catalogKeys++
		}
	}
	if catalogKeys == 0 {
		return errors.New("catalog: the keyring names no catalogue signing key, so nothing could ever sign a catalogue")
	}
	return nil
}

// Find returns a key by id.
func (k *Keyring) Find(keyID string) (Key, bool) {
	for _, key := range k.Keys {
		if key.KeyID == keyID {
			return key, true
		}
	}
	return Key{}, false
}

// Anchors is the set of keys a build accepts as the root of the catalogue.
//
// It is deliberately not compiled in. This repository ships no private key, so
// a compiled-in anchor would be either a placeholder that verifies nothing or a
// key whose private half is somewhere this project cannot vouch for. A build
// with no anchors refuses every managed download and says so — see
// [ErrNoAnchors] — which is the only honest behaviour available: the
// alternative, downloading and running something unverified because
// verification was not configured, is the failure mode this whole package
// exists to make impossible.
type Anchors struct {
	Keys []Key `json:"keys"`
}

// ErrNoAnchors reports that no catalogue trust anchor is configured. It names
// the variable, because an unconfigured trust root is something the user has to
// act on and a message that does not say what to set is not actionable.
var ErrNoAnchors = errors.New(
	"no catalogue trust anchor is configured: set " + EnvAnchorsPath +
		" or the \"catalog_anchors_path\" field in config.json to a keys file, " +
		"or use an acquisition mode that does not download")

// EnvAnchorsPath names the environment variable holding the path to the trust
// anchor file, and EnvCatalogURL the environment variable holding the address
// the signed keyring and catalogue are fetched from.
//
// Both are configuration and neither has a default. The address of another
// component is never compiled into this program — a wrong address that looks
// deliberate is worse than a missing one that says so — and a trust anchor even
// less so.
const (
	EnvAnchorsPath = "AUCOM_CATALOG_ANCHORS"
	EnvCatalogURL  = "AUCOM_CATALOG_URL"
)

// LoadAnchors reads a trust anchor file.
func LoadAnchors(path string) (*Anchors, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s does not exist", ErrNoAnchors, path)
		}
		return nil, fmt.Errorf("catalog: reading the trust anchors from %s: %w", path, err)
	}
	return DecodeAnchors(raw)
}

// DecodeAnchors reads a trust anchor file's contents.
func DecodeAnchors(raw []byte) (*Anchors, error) {
	var anchors Anchors
	if err := decodeStrict(raw, &anchors); err != nil {
		return nil, err
	}
	if len(anchors.Keys) == 0 {
		return nil, fmt.Errorf("%w: the file names no keys", ErrNoAnchors)
	}
	seen := map[string]bool{}
	for _, key := range anchors.Keys {
		if err := key.validate(); err != nil {
			return nil, err
		}
		if key.Role != RoleAnchor {
			return nil, fmt.Errorf("catalog: the anchor file names %s, which is a %s key; an anchor file holds anchors", key.KeyID, key.Role)
		}
		if seen[key.KeyID] {
			return nil, fmt.Errorf("catalog: the anchor file names key %s twice", key.KeyID)
		}
		seen[key.KeyID] = true
	}
	return &anchors, nil
}

// Marshal renders anchors for storage.
func (a *Anchors) Marshal() ([]byte, error) {
	encoded, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("catalog: encoding the trust anchors: %w", err)
	}
	return append(encoded, '\n'), nil
}

// keySet is a verifier over a fixed set of keys with a role and a moment.
type keySet struct {
	keys    map[string]Key
	role    string
	now     time.Time
	revoked map[string]string
}

func (s keySet) permits(keyID string) (ed25519.PublicKey, string, bool) {
	if reason, ok := s.revoked[keyID]; ok {
		return nil, "was revoked on this machine: " + reason, false
	}
	key, ok := s.keys[keyID]
	if !ok {
		return nil, "is not a key this build trusts for a " + s.role + " document", false
	}
	if reason, ok := key.usable(s.role, s.now); !ok {
		return nil, reason, false
	}
	public, err := key.Public()
	if err != nil {
		return nil, err.Error(), false
	}
	return public, "", true
}

func newKeySet(keys []Key, role string, now time.Time, revoked map[string]string) keySet {
	set := keySet{keys: make(map[string]Key, len(keys)), role: role, now: now, revoked: revoked}
	for _, key := range keys {
		set.keys[key.KeyID] = key
	}
	return set
}

// SortedKeyIDs is every key id in a keyring, for display.
func (k *Keyring) SortedKeyIDs() []string {
	ids := make([]string, 0, len(k.Keys))
	for _, key := range k.Keys {
		ids = append(ids, key.KeyID)
	}
	sort.Strings(ids)
	return ids
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
