package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// StateSchemaVersion versions the local catalogue trust state.
const StateSchemaVersion = "aucom.catalog-state/1.0"

// State is what this machine remembers about the catalogue, and it is the half
// of the trust model that no signed document can talk it out of.
//
// # Why it is not in the cache directory
//
// The tool cache is under the OS cache directory, on purpose: everything in it
// is re-downloadable and a user clearing caches loses nothing but time. This
// file is the opposite. Delete it and the machine forgets the highest serial it
// has accepted and every revocation it has ever seen, which is precisely the
// state an attacker replaying an old signed catalogue needs it to be in. So it
// lives beside `config.json`, with the bindings and the grants — the other
// things that record decisions rather than content.
//
// # What it is for
//
//   - **Rollback protection.** Serials only go up. A correctly signed catalogue
//     with a lower serial than one already accepted is a replay of a superseded
//     answer, and refusing it is the only thing that makes withdrawal stick
//     against someone who can serve stale bytes.
//   - **Sticky revocation.** A revocation is recorded the first time it is
//     seen and is never removed, not by a later document and not by going
//     offline. That is what makes it an emergency mechanism rather than a
//     suggestion.
type State struct {
	SchemaVersion string `json:"schema_version"`
	// KeyringSerial and CatalogSerial are the highest accepted so far, per
	// document id. Per id because a machine pointed at a second catalogue must
	// not have that catalogue's serials interfere with the first's.
	KeyringSerial map[string]int64 `json:"keyring_serial,omitempty"`
	CatalogSerial map[string]int64 `json:"catalog_serial,omitempty"`
	// RevokedKeys maps a key id to why it was revoked.
	RevokedKeys map[string]string `json:"revoked_keys,omitempty"`
	// RevokedArtifacts maps an artifact digest to its revocation.
	RevokedArtifacts map[string]Revocation `json:"revoked_artifacts,omitempty"`
	UpdatedAt        time.Time             `json:"updated_at,omitempty"`
}

// NewState returns empty state, stamped.
func NewState() *State {
	return &State{
		SchemaVersion:    StateSchemaVersion,
		KeyringSerial:    map[string]int64{},
		CatalogSerial:    map[string]int64{},
		RevokedKeys:      map[string]string{},
		RevokedArtifacts: map[string]Revocation{},
	}
}

func (s *State) fill() {
	if s.KeyringSerial == nil {
		s.KeyringSerial = map[string]int64{}
	}
	if s.CatalogSerial == nil {
		s.CatalogSerial = map[string]int64{}
	}
	if s.RevokedKeys == nil {
		s.RevokedKeys = map[string]string{}
	}
	if s.RevokedArtifacts == nil {
		s.RevokedArtifacts = map[string]Revocation{}
	}
}

// ErrRollback reports a signed document whose serial is lower than one already
// accepted. Its own error so a caller can tell "somebody is replaying an old
// catalogue at me" from "the signature is wrong", which are different incidents.
var ErrRollback = errors.New("catalog: rolled back")

// ErrRevoked reports something withdrawn: a key, or an artifact.
var ErrRevoked = errors.New("catalog: revoked")

// checkKeyringSerial enforces the ratchet for a keyring.
func (s *State) checkKeyringSerial(id string, serial int64) error {
	s.fill()
	if highest, ok := s.KeyringSerial[id]; ok && serial < highest {
		return fmt.Errorf("%w: keyring %s is serial %d and this machine has already accepted %d; "+
			"a correctly signed older document is a replay, not an update", ErrRollback, id, serial, highest)
	}
	return nil
}

// checkCatalogSerial enforces the ratchet for a catalogue.
func (s *State) checkCatalogSerial(id string, serial int64) error {
	s.fill()
	if highest, ok := s.CatalogSerial[id]; ok && serial < highest {
		return fmt.Errorf("%w: catalogue %s is serial %d and this machine has already accepted %d; "+
			"a correctly signed older document is a replay, not an update", ErrRollback, id, serial, highest)
	}
	return nil
}

// recordKeyring advances the keyring ratchet and folds in every key revocation
// the document carries.
func (s *State) recordKeyring(k *Keyring, now time.Time) {
	s.fill()
	if k.Serial > s.KeyringSerial[k.KeyringID] {
		s.KeyringSerial[k.KeyringID] = k.Serial
	}
	for _, key := range k.Keys {
		if key.Status == StatusRevoked {
			if _, known := s.RevokedKeys[key.KeyID]; !known {
				s.RevokedKeys[key.KeyID] = key.Reason
			}
		}
	}
	s.UpdatedAt = now.UTC()
}

// recordCatalog advances the catalogue ratchet and folds in every artifact
// revocation.
func (s *State) recordCatalog(c *Catalog, now time.Time) {
	s.fill()
	if c.Serial > s.CatalogSerial[c.CatalogID] {
		s.CatalogSerial[c.CatalogID] = c.Serial
	}
	for _, revocation := range c.Revocations {
		if _, known := s.RevokedArtifacts[revocation.Digest]; !known {
			s.RevokedArtifacts[revocation.Digest] = revocation
		}
	}
	s.UpdatedAt = now.UTC()
}

// ArtifactRevocation reports whether this machine has ever seen a digest
// revoked. It is consulted on use as well as on install, which is what makes a
// revocation reach a tool that is already on disk.
func (s *State) ArtifactRevocation(digest string) (Revocation, bool) {
	if s == nil || s.RevokedArtifacts == nil {
		return Revocation{}, false
	}
	revocation, ok := s.RevokedArtifacts[digest]
	return revocation, ok
}

// RevokedKeyIDs lists every permanently revoked key, for display.
func (s *State) RevokedKeyIDs() []string {
	if s == nil {
		return nil
	}
	ids := make([]string, 0, len(s.RevokedKeys))
	for id := range s.RevokedKeys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// RevokedDigests lists every permanently revoked artifact, for display.
func (s *State) RevokedDigests() []string {
	if s == nil {
		return nil
	}
	digests := make([]string, 0, len(s.RevokedArtifacts))
	for digest := range s.RevokedArtifacts {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	return digests
}

// LoadState reads the trust state. A missing file is empty state and no error:
// a machine that has never fetched a catalogue has none, and that is not a
// fault. A file that exists and cannot be read *is* an error — falling back to
// empty state there would silently reset the ratchet, which is exactly what
// deleting the file is supposed to be unable to do quietly.
func LoadState(path string) (*State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NewState(), nil
		}
		return nil, fmt.Errorf("catalog: reading the trust state from %s: %w", path, err)
	}
	var state State
	if err := decodeStrict(raw, &state); err != nil {
		return nil, fmt.Errorf("catalog: reading the trust state from %s: %w", path, err)
	}
	if state.SchemaVersion != StateSchemaVersion {
		return nil, fmt.Errorf("catalog: %s is %q; this build reads %q", path, state.SchemaVersion, StateSchemaVersion)
	}
	state.fill()
	return &state, nil
}

// SaveState writes the trust state atomically. Atomic because a truncated
// ratchet is an absent one.
func SaveState(path string, state *State) error {
	state.SchemaVersion = StateSchemaVersion
	state.fill()
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("catalog: encoding the trust state: %w", err)
	}
	encoded = append(encoded, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("catalog: creating %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, "catalog-state-*.json")
	if err != nil {
		return fmt.Errorf("catalog: creating a temporary file in %s: %w", dir, err)
	}
	name := temp.Name()
	defer os.Remove(name) // No-op once the rename below has succeeded.

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("catalog: securing %s: %w", name, err)
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("catalog: writing %s: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("catalog: closing %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("catalog: replacing %s: %w", name, err)
	}
	return nil
}
