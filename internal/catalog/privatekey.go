package catalog

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Signing keys on disk, and why this file is short.
//
// A private key file is a secret, and the only interesting thing a program can
// do with a secret is fail to protect it. So this does the least it can: one
// file, 0600, created with O_EXCL so an existing key is never silently
// overwritten, and a `warning` member inside the document itself so that a key
// pasted into a chat window or committed to a repository says what it is at the
// top.
//
// No key produced here is in this repository. The operational procedure — where
// an anchor key lives, who holds it, how a catalogue key is rotated — is in
// `README.md`, because it is a procedure about people and not a function.

// PrivateKeySchemaVersion versions the private key file.
const PrivateKeySchemaVersion = "aucom.signing-key/1.0"

// privateKeyWarning is written into every key file.
const privateKeyWarning = "PRIVATE KEY — anyone holding this file can sign an Auto-Pigeon catalogue " +
	"that this build will trust. Do not commit it, do not copy it, and revoke the key if it leaves this machine."

// PrivateKeyFile is a signing key as stored.
type PrivateKeyFile struct {
	SchemaVersion string `json:"schema_version"`
	Warning       string `json:"warning"`
	KeyID         string `json:"key_id"`
	Algorithm     string `json:"algorithm"`
	Role          string `json:"role"`
	// PrivateKey is standard base64 of the 64-byte Ed25519 private key.
	PrivateKey string    `json:"private_key"`
	PublicKey  string    `json:"public_key"`
	CreatedAt  time.Time `json:"created_at"`
	Comment    string    `json:"comment,omitempty"`
}

// GenerateKeyFile mints a key for a role.
func GenerateKeyFile(role, comment string, now time.Time) (*PrivateKeyFile, error) {
	if !contains(keyRoles, role) {
		return nil, fmt.Errorf("catalog: %q is not a key role; the roles are anchor and catalog", role)
	}
	public, private, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	return &PrivateKeyFile{
		SchemaVersion: PrivateKeySchemaVersion,
		Warning:       privateKeyWarning,
		KeyID:         KeyID(public),
		Algorithm:     AlgorithmEd25519,
		Role:          role,
		PrivateKey:    base64.StdEncoding.EncodeToString(private),
		PublicKey:     base64.StdEncoding.EncodeToString(public),
		CreatedAt:     now.UTC(),
		Comment:       comment,
	}, nil
}

// Private decodes the signing half.
func (f *PrivateKeyFile) Private() (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(f.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("catalog: key %s has a private key that is not base64", f.KeyID)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("catalog: key %s has a %d-byte private key; Ed25519 keys are %d",
			f.KeyID, len(raw), ed25519.PrivateKeySize)
	}
	private := ed25519.PrivateKey(raw)
	public, ok := private.Public().(ed25519.PublicKey)
	if !ok || KeyID(public) != f.KeyID {
		return nil, fmt.Errorf("catalog: the key file says %s and its private key is really %s", f.KeyID, KeyID(public))
	}
	return private, nil
}

// PublicEntry is the [Key] to publish for this signing key, in an anchor file
// or in a keyring.
func (f *PrivateKeyFile) PublicEntry(notBefore, notAfter time.Time) (Key, error) {
	key := Key{
		KeyID:     f.KeyID,
		Algorithm: f.Algorithm,
		PublicKey: f.PublicKey,
		Role:      f.Role,
		Status:    StatusActive,
		NotBefore: notBefore.UTC(),
		NotAfter:  notAfter.UTC(),
		Comment:   f.Comment,
	}
	if err := key.validate(); err != nil {
		return Key{}, err
	}
	return key, nil
}

// LoadPrivateKey reads a key file.
func LoadPrivateKey(path string) (*PrivateKeyFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("catalog: reading the signing key from %s: %w", path, err)
	}
	var file PrivateKeyFile
	if err := decodeStrict(raw, &file); err != nil {
		return nil, err
	}
	if file.SchemaVersion != PrivateKeySchemaVersion {
		return nil, fmt.Errorf("catalog: %s is %q; this build reads %q", path, file.SchemaVersion, PrivateKeySchemaVersion)
	}
	if _, err := file.Private(); err != nil {
		return nil, err
	}
	return &file, nil
}

// SavePrivateKey writes a key file, refusing to replace one that is already
// there. Overwriting a signing key is never what somebody meant.
func SavePrivateKey(path string, file *PrivateKeyFile) error {
	file.SchemaVersion = PrivateKeySchemaVersion
	file.Warning = privateKeyWarning
	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("catalog: encoding the signing key: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("catalog: creating %s: %w", dir, err)
		}
	}
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("catalog: writing the signing key to %s: %w", path, err)
	}
	if _, err := handle.Write(append(encoded, '\n')); err != nil {
		handle.Close()
		return fmt.Errorf("catalog: writing the signing key to %s: %w", path, err)
	}
	return handle.Close()
}
