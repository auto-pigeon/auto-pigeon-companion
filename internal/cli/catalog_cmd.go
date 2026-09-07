package cli

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// `companion catalog` — the publisher's side of the acquisition catalogue.
//
// It is in the shipped binary rather than in a script under `build/` for one
// reason: the signing procedure has to be executable, or it is a paragraph in a
// README that stops being true. Every rule the verifier enforces — canonical
// payload, derived key ids, role separation, serials that only go up — is a
// rule a publisher has to satisfy, and the cheapest way to keep the two halves
// in step is for both to be this package's problem.
//
// Nothing here can weaken verification. `sign` produces a document; whether any
// machine accepts it is decided by [catalog.Verifier] against anchors it was
// configured with, and no flag on this side reaches that decision.

const catalogUsage = `usage:
  companion catalog keygen  --role anchor|catalog --out <file> [--comment <text>]
  companion catalog sign    --key <file> [--key <file>]... <document.json>
  companion catalog verify  --anchors <file> --keyring <file> --catalog <file> [--state <file>]
  companion catalog show    [--anchors <file>] [--keyring <file>] [--catalog <file>] [--json]
  companion catalog status  what this machine remembers: serials, revocations
`

func runCatalog(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, catalogUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, catalogUsage)
		return 0
	case "keygen":
		return catalogKeygen(env, args[1:])
	case "sign":
		return catalogSign(env, args[1:])
	case "verify":
		return catalogVerify(env, args[1:])
	case "show":
		return catalogShow(env, args[1:])
	case "status":
		return catalogStatus(env, args[1:])
	}
	fmt.Fprintf(env.Stderr, "error: unknown catalog command %q\n\n", args[0])
	fmt.Fprint(env.Stderr, catalogUsage)
	return 2
}

func catalogKeygen(env *Env, args []string) int {
	set := newFlagSet(env, "catalog keygen")
	role := set.String("role", "", "anchor or catalog")
	out := set.String("out", "", "where to write the private key")
	comment := set.String("comment", "", "a note stored with the key")
	years := set.Int("years", 2, "how long the published key entry is valid for")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *role == "" || *out == "" {
		fmt.Fprint(env.Stderr, "error: catalog keygen requires --role and --out\n")
		return 2
	}
	now := time.Now().UTC()
	key, err := catalog.GenerateKeyFile(*role, *comment, now)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}
	if err := catalog.SavePrivateKey(*out, key); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	entry, err := key.PublicEntry(now, now.AddDate(*years, 0, 0))
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	published, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "wrote %s — %s key %s, private, mode 0600\n", *out, key.Role, key.KeyID)
	fmt.Fprintf(env.Stdout, "\nthe public entry to publish (in an anchor file for an anchor key, in the keyring for a catalogue key):\n\n%s\n", published)
	return 0
}

func catalogSign(env *Env, args []string) int {
	set := newFlagSet(env, "catalog sign")
	var keys stringList
	set.Var(&keys, "key", "a signing key file; repeat to sign with several")
	out := set.String("out", "", "where to write the signed document; default is standard output")
	rest, code, ok := parseFlags(env, set, args)
	if !ok {
		return code
	}
	if len(keys) == 0 || len(rest) != 1 {
		fmt.Fprint(env.Stderr, "error: catalog sign requires at least one --key and exactly one document\n")
		return 2
	}
	raw, err := os.ReadFile(rest[0])
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	document, role, kind, err := decodeUnsigned(raw)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	signers, keyIDs, err := loadSigningKeys(keys, role)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	// An artifact with no signer is attributed to the key that is signing.
	//
	// A catalogue kept in a repository cannot name the key id of whoever will
	// eventually sign it — key ids are derived from the key, and the key is not
	// in the repository. Leaving `signer` out therefore means "whoever signs
	// this", which is unambiguous with one key and a question with several.
	if err := attributeUnsigned(document, keyIDs); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	// Validated here, against the keys that are about to sign it, because one
	// of a catalogue's rules — every entry is attributed to a key that signed
	// the document — cannot be checked without knowing them. A publisher who
	// finds out at this point has published nothing yet.
	if err := validateUnsigned(document, keyIDs); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	envelope, err := catalog.Sign(document, signers...)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	encoded, err := envelope.Marshal()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if *out == "" {
		env.Stdout.Write(encoded)
		return 0
	}
	if err := os.WriteFile(*out, encoded, 0o644); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "wrote %s — a %s signed by the %s key, digest %s\n", *out, kind, role, envelope.Digest())
	return 0
}

// decodeUnsigned reads a document to be signed and reports what it is and which
// key role has to sign it, so that a catalogue key cannot be pointed at a
// keyring by mistake.
func decodeUnsigned(raw []byte) (document any, role, kind string, err error) {
	var head struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, "", "", fmt.Errorf("the document is not readable JSON: %w", err)
	}
	switch head.SchemaVersion {
	case catalog.KeyringSchemaVersion:
		keyring, err := catalog.DecodeKeyring(raw)
		if err != nil {
			return nil, "", "", err
		}
		return keyring, catalog.RoleAnchor, "keyring", nil
	case catalog.SchemaVersion:
		decoded, err := catalog.DecodeCatalog(raw)
		if err != nil {
			return nil, "", "", err
		}
		return decoded, catalog.RoleCatalog, "catalogue", nil
	}
	return nil, "", "", fmt.Errorf("the document is %q; a keyring is %q and a catalogue is %q",
		head.SchemaVersion, catalog.KeyringSchemaVersion, catalog.SchemaVersion)
}

func loadSigningKeys(paths []string, role string) ([]ed25519.PrivateKey, map[string]bool, error) {
	var keys []ed25519.PrivateKey
	ids := map[string]bool{}
	for _, path := range paths {
		file, err := catalog.LoadPrivateKey(path)
		if err != nil {
			return nil, nil, err
		}
		if file.Role != role {
			return nil, nil, fmt.Errorf("%s holds a %s key, and this document must be signed by the %s key",
				path, file.Role, role)
		}
		private, err := file.Private()
		if err != nil {
			return nil, nil, err
		}
		keys = append(keys, private)
		ids[file.KeyID] = true
	}
	return keys, ids, nil
}

// validateUnsigned applies the document's own rules before it is signed.
// attributeUnsigned fills in an artifact's `signer` when the document left it
// out and exactly one key is signing.
func attributeUnsigned(document any, keyIDs map[string]bool) error {
	catalogue, isCatalog := document.(*catalog.Catalog)
	if !isCatalog {
		return nil
	}
	var only string
	for id := range keyIDs {
		if only != "" {
			only = ""
			break
		}
		only = id
	}
	for p := range catalogue.Packages {
		for a := range catalogue.Packages[p].Artifacts {
			artifact := &catalogue.Packages[p].Artifacts[a]
			if strings.TrimSpace(artifact.Signer) != "" {
				continue
			}
			if only == "" {
				return fmt.Errorf("catalog: %s on %s names no signer, and this document is being signed by several keys; "+
					"say which one vouches for it", catalogue.Packages[p].ID, artifact.Platform)
			}
			artifact.Signer = only
		}
	}
	return nil
}

func validateUnsigned(document any, keyIDs map[string]bool) error {
	switch typed := document.(type) {
	case *catalog.Keyring:
		return typed.Validate()
	case *catalog.Catalog:
		return typed.Validate(keyIDs)
	}
	return fmt.Errorf("catalog: %T is not a signable document", document)
}

func catalogVerify(env *Env, args []string) int {
	set := newFlagSet(env, "catalog verify")
	anchors := set.String("anchors", "", "the trust anchor file")
	keyringPath := set.String("keyring", "", "the signed keyring")
	catalogPath := set.String("catalog", "", "the signed catalogue")
	statePath := set.String("state", "", "the local trust state to check serials against and update")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *anchors == "" || *keyringPath == "" || *catalogPath == "" {
		fmt.Fprint(env.Stderr, "error: catalog verify requires --anchors, --keyring and --catalog\n")
		return 2
	}
	verified, state, err := verifyFromFiles(*anchors, *keyringPath, *catalogPath, *statePath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if *statePath != "" {
		if err := catalog.SaveState(*statePath, state); err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return 1
		}
	}
	fmt.Fprint(env.Stdout, describeVerified(verified))
	return 0
}

func verifyFromFiles(anchorsPath, keyringPath, catalogPath, statePath string) (*catalog.Verified, *catalog.State, error) {
	anchors, err := catalog.LoadAnchors(anchorsPath)
	if err != nil {
		return nil, nil, err
	}
	keyringRaw, err := os.ReadFile(keyringPath)
	if err != nil {
		return nil, nil, err
	}
	catalogRaw, err := os.ReadFile(catalogPath)
	if err != nil {
		return nil, nil, err
	}
	keyringEnvelope, err := catalog.DecodeSigned(keyringRaw)
	if err != nil {
		return nil, nil, err
	}
	catalogEnvelope, err := catalog.DecodeSigned(catalogRaw)
	if err != nil {
		return nil, nil, err
	}
	state := catalog.NewState()
	if statePath != "" {
		if state, err = catalog.LoadState(statePath); err != nil {
			return nil, nil, err
		}
	}
	verifier := &catalog.Verifier{Anchors: anchors, State: state}
	verified, err := verifier.Verify(keyringEnvelope, catalogEnvelope)
	if err != nil {
		return nil, state, err
	}
	return verified, state, nil
}

func describeVerified(v *catalog.Verified) string {
	var b strings.Builder
	fmt.Fprintf(&b, "keyring   %s serial %d, expires %s\n", v.Keyring.KeyringID, v.Keyring.Serial,
		v.Keyring.ExpiresAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "          signed by %s\n", strings.Join(v.KeyringSigners, ", "))
	fmt.Fprintf(&b, "          keys: %s\n", strings.Join(v.Keyring.SortedKeyIDs(), ", "))
	fmt.Fprintf(&b, "catalogue %s serial %d, expires %s\n", v.Catalog.CatalogID, v.Catalog.Serial,
		v.Catalog.ExpiresAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "          signed by %s\n", strings.Join(v.CatalogSigners, ", "))
	fmt.Fprintf(&b, "          %d packages, %d revocations\n", len(v.Catalog.Packages), len(v.Catalog.Revocations))
	for _, pkg := range v.Catalog.Packages {
		fmt.Fprintf(&b, "  %s %s — %s [%s] for %s\n", pkg.ID, pkg.Version, pkg.Name, pkg.License.SPDX,
			strings.Join(pkg.Platforms(), ", "))
	}
	for _, revocation := range v.Catalog.Revocations {
		fmt.Fprintf(&b, "  revoked %s on %s: %s\n", revocation.Digest,
			revocation.At.UTC().Format(time.RFC3339), revocation.Reason)
	}
	return b.String()
}

func catalogShow(env *Env, args []string) int {
	set := newFlagSet(env, "catalog show")
	path := set.String("document", "", "a signed keyring or catalogue to print")
	asJSON := set.Bool("json", false, "print the payload as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *path == "" {
		fmt.Fprint(env.Stderr, "error: catalog show requires --document\n")
		return 2
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	envelope, err := catalog.DecodeSigned(raw)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if *asJSON {
		payload, err := envelope.PayloadBytes()
		if err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return 1
		}
		env.Stdout.Write(append(payload, '\n'))
		return 0
	}
	fmt.Fprintf(env.Stdout, "digest %s\n", envelope.Digest())
	for _, signature := range envelope.Signatures {
		fmt.Fprintf(env.Stdout, "signed by %s (%s)\n", signature.KeyID, signature.Algorithm)
	}
	fmt.Fprintf(env.Stdout, "\nThis prints what the document says. It says nothing about whether it verifies:\n"+
		"run `companion catalog verify` for that.\n")
	return 0
}

func catalogStatus(env *Env, args []string) int {
	set := newFlagSet(env, "catalog status")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	path, err := catalogStatePath(env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	state, err := catalog.LoadState(path)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "trust state %s\n", path)
	for _, id := range sortedMapKeys(state.KeyringSerial) {
		fmt.Fprintf(env.Stdout, "  keyring   %s: highest serial accepted %d\n", id, state.KeyringSerial[id])
	}
	for _, id := range sortedMapKeys(state.CatalogSerial) {
		fmt.Fprintf(env.Stdout, "  catalogue %s: highest serial accepted %d\n", id, state.CatalogSerial[id])
	}
	for _, id := range state.RevokedKeyIDs() {
		fmt.Fprintf(env.Stdout, "  revoked key      %s: %s\n", id, state.RevokedKeys[id])
	}
	for _, digest := range state.RevokedDigests() {
		revocation := state.RevokedArtifacts[digest]
		fmt.Fprintf(env.Stdout, "  revoked artifact %s: %s\n", digest, revocation.Reason)
	}
	if len(state.KeyringSerial) == 0 && len(state.CatalogSerial) == 0 {
		fmt.Fprint(env.Stdout, "  no catalogue has been accepted on this machine yet\n")
	}
	fmt.Fprint(env.Stdout, "\nSerials only go up and revocations are never forgotten. Deleting this file\n"+
		"would restore exactly the state a replayed old catalogue needs.\n")
	return 0
}

// catalogStatePath puts the trust state beside whichever config file this
// invocation uses.
func catalogStatePath(env *Env) (string, error) {
	if env.ConfigPath != "" {
		return filepath.Join(filepath.Dir(env.ConfigPath), "catalog-state.json"), nil
	}
	return config.CatalogStatePath()
}

// stringList collects a repeated flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ", ") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
