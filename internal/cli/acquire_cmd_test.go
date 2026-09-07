package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The publisher's half and the user's half, exercised as commands.
//
// The package tests cover the mechanism; these cover the wiring — that a key
// written by `catalog keygen` is one `catalog sign` can use, that the signed
// documents `catalog verify` accepts are the ones a running Companion would,
// and that the commands a user reaches for when something is wrong say
// something useful.

func runCLI(t *testing.T, env *Env, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	local := *env
	local.Stdout = &stdout
	local.Stderr = &stderr
	code := Run(&local, args)
	return code, stdout.String(), stderr.String()
}

// acquireEnv is an Env with its own config directory. It is separate from
// cli_test.go's testEnv because these tests drive the command several times and
// want a fresh pair of buffers each time; see [runCLI].
func acquireEnv(t *testing.T) *Env {
	t.Helper()
	return &Env{
		Stdin:      strings.NewReader(""),
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Version:    "test-version",
		Lookenv:    func(string) (string, bool) { return "", false },
	}
}

func TestCatalogKeygenSignAndVerifyRoundTrip(t *testing.T) {
	env := acquireEnv(t)
	dir := filepath.Dir(env.ConfigPath)

	anchorKey := filepath.Join(dir, "anchor.key.json")
	code, out, errOut := runCLI(t, env, "catalog", "keygen", "--role", "anchor", "--out", anchorKey)
	if code != 0 {
		t.Fatalf("keygen exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "anchor key") {
		t.Errorf("keygen did not say what it made:\n%s", out)
	}
	if info, err := os.Stat(anchorKey); err != nil {
		t.Fatalf("%v", err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the private key is mode %04o, want 0600", info.Mode().Perm())
	}
	// A key file is never silently replaced.
	if code, _, _ := runCLI(t, env, "catalog", "keygen", "--role", "anchor", "--out", anchorKey); code == 0 {
		t.Error("keygen overwrote an existing signing key")
	}

	catalogKey := filepath.Join(dir, "catalog.key.json")
	if code, _, errOut := runCLI(t, env, "catalog", "keygen", "--role", "catalog", "--out", catalogKey); code != 0 {
		t.Fatalf("keygen exited %d: %s", code, errOut)
	}

	anchorFile := loadKeyFile(t, anchorKey)
	catalogFile := loadKeyFile(t, catalogKey)
	now := time.Now().UTC()

	anchorsPath := filepath.Join(dir, "anchors.json")
	anchorEntry, err := anchorFile.PublicEntry(now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0))
	if err != nil {
		t.Fatalf("%v", err)
	}
	writeJSON(t, anchorsPath, &catalog.Anchors{Keys: []catalog.Key{anchorEntry}})

	catalogEntry, err := catalogFile.PublicEntry(now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0))
	if err != nil {
		t.Fatalf("%v", err)
	}
	keyringPath := filepath.Join(dir, "keyring.unsigned.json")
	writeJSON(t, keyringPath, &catalog.Keyring{
		SchemaVersion: catalog.KeyringSchemaVersion,
		KeyringID:     "example",
		Serial:        1,
		IssuedAt:      now.Add(-time.Hour),
		ExpiresAt:     now.AddDate(1, 0, 0),
		Keys:          []catalog.Key{catalogEntry},
	})
	documentPath := filepath.Join(dir, "catalog.unsigned.json")
	writeJSON(t, documentPath, &catalog.Catalog{
		SchemaVersion: catalog.SchemaVersion,
		CatalogID:     "example",
		Serial:        1,
		IssuedAt:      now.Add(-time.Hour),
		ExpiresAt:     now.AddDate(0, 1, 0),
		Packages: []catalog.Package{{
			ID: "example.tool", Version: "1.0.0", Name: "Example Tool",
			Source:  profile.Source{Homepage: "https://example.invalid/tool"},
			License: profile.License{SPDX: "MIT"},
			Artifacts: []catalog.Artifact{{
				Platform: profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
				URL:      "https://example.invalid/tool/1.0.0.tar.gz",
				Kind:     catalog.KindTarGz, Size: 1024,
				SHA256:       "sha256:" + strings.Repeat("ab", 32),
				Signer:       catalogFile.KeyID,
				UnpackedSize: 4096,
				Executables:  []string{"bin/tool"},
			}},
		}},
	})

	signedKeyring := filepath.Join(dir, "keyring.json")
	if code, _, errOut := runCLI(t, env, "catalog", "sign", "--key", anchorKey, "--out", signedKeyring, keyringPath); code != 0 {
		t.Fatalf("signing the keyring exited %d: %s", code, errOut)
	}
	signedCatalog := filepath.Join(dir, "catalog.json")
	if code, _, errOut := runCLI(t, env, "catalog", "sign", "--key", catalogKey, "--out", signedCatalog, documentPath); code != 0 {
		t.Fatalf("signing the catalogue exited %d: %s", code, errOut)
	}

	// The wrong key for the document is refused rather than producing a
	// document that fails much later on somebody else's machine.
	if code, _, errOut := runCLI(t, env, "catalog", "sign", "--key", catalogKey, keyringPath); code == 0 {
		t.Error("a catalogue key signed a keyring")
	} else if !strings.Contains(errOut, "anchor key") {
		t.Errorf("the error does not say which key is needed: %s", errOut)
	}

	statePath := filepath.Join(dir, "catalog-state.json")
	code, out, errOut = runCLI(t, env, "catalog", "verify",
		"--anchors", anchorsPath, "--keyring", signedKeyring, "--catalog", signedCatalog, "--state", statePath)
	if code != 0 {
		t.Fatalf("verify exited %d: %s", code, errOut)
	}
	for _, want := range []string{"example.tool 1.0.0", anchorFile.KeyID, catalogFile.KeyID, "serial 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify's output does not mention %q:\n%s", want, out)
		}
	}

	// The ratchet is now on disk, and `catalog status` says so.
	code, out, errOut = runCLI(t, env, "catalog", "status")
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "highest serial accepted 1") {
		t.Errorf("status does not report the ratchet:\n%s", out)
	}

	// Editing a signed catalogue after the fact is refused.
	raw, err := os.ReadFile(signedCatalog)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// The payload is base64, so editing it means editing the encoded bytes —
	// which is exactly what a proxy or a mirror rewriting a document would
	// amount to. One character is enough.
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("%v", err)
	}
	payload := envelope["payload"].(string)
	envelope["payload"] = flipOneCharacter(payload)
	edited := filepath.Join(dir, "catalog.edited.json")
	writeJSON(t, edited, envelope)
	if code, _, _ := runCLI(t, env, "catalog", "verify",
		"--anchors", anchorsPath, "--keyring", signedKeyring, "--catalog", edited); code == 0 {
		t.Error("an edited signed catalogue verified")
	}
}

func TestAcquireWithNothingConfiguredRefusesAndSaysWhatToSet(t *testing.T) {
	env := acquireEnv(t)
	code, _, errOut := runCLI(t, env, "acquire", "install", "anything")
	if code == 0 {
		t.Fatal("a managed download happened with nothing configured")
	}
	if !strings.Contains(errOut, catalog.EnvAnchorsPath) && !strings.Contains(errOut, catalog.EnvCatalogURL) {
		t.Errorf("the error names neither variable to set:\n%s", errOut)
	}
	if !strings.Contains(errOut, "Nothing is downloaded\nunverified") {
		t.Errorf("the error does not say what happens instead:\n%s", errOut)
	}
}

func TestAcquireListAndGCWorkWithAnEmptyCache(t *testing.T) {
	env := acquireEnv(t)
	code, out, errOut := runCLI(t, env, "acquire", "list")
	if code != 0 {
		t.Fatalf("acquire list exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "nothing is installed") {
		t.Errorf("acquire list on an empty cache said: %q", out)
	}
	code, out, errOut = runCLI(t, env, "acquire", "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("acquire gc exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "cache is empty") {
		t.Errorf("acquire gc on an empty cache said: %q", out)
	}
}

func TestAcquireInstallEndToEndThroughTheCLI(t *testing.T) {
	// The CLI builds its own HTTP client, so the fixture's certificate has to
	// be trusted the way a real one would be: through the trust store Go
	// consults. SSL_CERT_FILE is that knob on Unix. On Windows it is not, and
	// Go caches the system pool the first time anything asks for it, so this
	// test skips rather than failing when it cannot get in first — the
	// mechanism itself is covered by internal/acquire's own tests, which drive
	// the same code with the fixture's own client.
	if runtime.GOOS == "windows" {
		t.Skip("SSL_CERT_FILE is not how Windows finds roots")
	}
	env := acquireEnv(t)
	dir := filepath.Dir(env.ConfigPath)

	published := newPublisher(t, dir)
	server := httptest.NewTLSServer(http.HandlerFunc(published.serve))
	defer server.Close()
	published.publish(server.URL)

	certPath := filepath.Join(dir, "fixture-ca.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	}), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	t.Setenv("SSL_CERT_FILE", certPath)
	if !trusts(server.URL) {
		t.Skip("this process had already cached a system certificate pool")
	}

	writeJSON(t, env.ConfigPath, map[string]any{
		"port":                 8789,
		"catalog_url":          server.URL,
		"catalog_anchors_path": published.anchorsPath,
	})

	code, out, errOut := runCLI(t, env, "acquire", "plan", "example.tool")
	if code != 0 {
		t.Fatalf("acquire plan exited %d: %s", code, errOut)
	}
	for _, want := range []string{"GPL-2.0-or-later", "example.invalid", catalog.Aggregation} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not mention %q:\n%s", want, out)
		}
	}

	code, out, errOut = runCLI(t, env, "acquire", "install", "example.tool")
	if code != 0 {
		t.Fatalf("acquire install exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "installed example.tool 1.0.0") {
		t.Errorf("install said: %s", out)
	}

	code, out, errOut = runCLI(t, env, "acquire", "list")
	if code != 0 {
		t.Fatalf("acquire list exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "example.tool 1.0.0") {
		t.Errorf("list does not show what was installed:\n%s", out)
	}

	if code, out, errOut = runCLI(t, env, "acquire", "verify"); code != 0 {
		t.Fatalf("acquire verify exited %d: %s\n%s", code, errOut, out)
	}
	code, out, errOut = runCLI(t, env, "acquire", "use", "example.tool")
	if code != 0 {
		t.Fatalf("acquire use exited %d: %s", code, errOut)
	}
	root := strings.TrimSpace(out)
	if _, err := os.Stat(filepath.Join(root, "bin", "tool")); err != nil {
		t.Fatalf("acquire use printed a root with no tool in it: %v", err)
	}

	// Nothing refers to it yet, so collection would take it.
	code, out, errOut = runCLI(t, env, "acquire", "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("acquire gc exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "nothing refers to it") {
		t.Errorf("gc would not collect an unreferenced entry:\n%s", out)
	}

	// Bind a profile to it, and now it is held.
	profilePath := filepath.Join(dir, "example.tool.json")
	if err := os.WriteFile(profilePath, []byte(exampleToolProfile), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	code, out, errOut = runCLI(t, env, "acquire", "resolve", profilePath, "--bind")
	if code != 0 {
		t.Fatalf("acquire resolve exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "managed_download") || !strings.Contains(out, "recorded in bindings.json") {
		t.Errorf("resolve did not record the binding:\n%s", out)
	}
	if !strings.Contains(out, published.keyID) {
		t.Errorf("resolve does not say who vouched:\n%s", out)
	}

	code, out, errOut = runCLI(t, env, "acquire", "gc")
	if code != 0 {
		t.Fatalf("acquire gc exited %d: %s", code, errOut)
	}
	if strings.Contains(out, "nothing refers to it") {
		t.Errorf("gc removed a toolchain a binding depends on:\n%s", out)
	}
	if !strings.Contains(out, "held by binding example.tool") {
		t.Errorf("gc does not say what holds the entry:\n%s", out)
	}
	if code, _, errOut := runCLI(t, env, "acquire", "verify"); code != 0 {
		t.Fatalf("the bound toolchain did not survive collection: %s", errOut)
	}
}

// exampleToolProfile is a tool profile whose only acquisition route is the
// fixture catalogue's package. It is written out by the test rather than
// committed, because it names a package that only exists inside it.
const exampleToolProfile = `{
  "schema_version": "aucom.profile/1.0",
  "kind": "tool",
  "id": "example.tool",
  "version": "1.0.0",
  "name": "Example Tool",
  "summary": "A tool profile bound to the fixture catalogue package.",
  "publisher": { "name": "Example" },
  "license": { "spdx": "GPL-2.0-or-later" },
  "tool_version": "1.0.0",
  "platforms": [{ "platform": { "os": "linux", "arch": "amd64" }, "status": "supported" }],
  "acquisition": [{
    "mode": "managed_download",
    "title": "Download it from the Auto-Pigeon catalogue",
    "catalog_package": "example.tool"
  }],
  "executables": [{ "name": "tool", "file": "bin/tool{platform.exe_suffix}" }],
  "actions": [{ "id": "run", "title": "Run it", "executable": "tool", "args": ["--help"] }]
}
`

// trusts reports whether the default HTTP client can reach the fixture. Go
// caches the system certificate pool the first time it is asked, so a test
// that runs after something else has already made a TLS connection cannot
// change it; skipping is more honest than a failure that depends on test order.
func trusts(url string) bool {
	response, err := http.Get(url)
	if err != nil {
		return false
	}
	response.Body.Close()
	return true
}

// publisher is a whole signed catalogue with one real archive behind it, served
// over TLS from this process.
type publisher struct {
	t           *testing.T
	anchorsPath string
	anchorKey   ed25519.PrivateKey
	catalogKey  ed25519.PrivateKey
	keyID       string
	archive     []byte
	keyring     []byte
	document    []byte
}

func newPublisher(t *testing.T, dir string) *publisher {
	t.Helper()
	now := time.Now().UTC()
	anchor, err := catalog.GenerateKeyFile(catalog.RoleAnchor, "cli test anchor", now)
	if err != nil {
		t.Fatalf("%v", err)
	}
	signing, err := catalog.GenerateKeyFile(catalog.RoleCatalog, "cli test catalogue key", now)
	if err != nil {
		t.Fatalf("%v", err)
	}
	p := &publisher{t: t, keyID: signing.KeyID, anchorsPath: filepath.Join(dir, "anchors.json")}
	if p.anchorKey, err = anchor.Private(); err != nil {
		t.Fatalf("%v", err)
	}
	if p.catalogKey, err = signing.Private(); err != nil {
		t.Fatalf("%v", err)
	}
	anchorEntry, err := anchor.PublicEntry(now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0))
	if err != nil {
		t.Fatalf("%v", err)
	}
	writeJSON(t, p.anchorsPath, &catalog.Anchors{Keys: []catalog.Key{anchorEntry}})

	// The "tool" is a script this test writes: this repository's own bytes.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	content := "#!/bin/sh\necho example tool\n"
	if err := writer.WriteHeader(&tar.Header{
		Name: "bin/tool", Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg, ModTime: now,
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatalf("%v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("%v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("%v", err)
	}
	p.archive = buf.Bytes()

	catalogEntry, err := signing.PublicEntry(now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0))
	if err != nil {
		t.Fatalf("%v", err)
	}
	keyring, err := catalog.Sign(&catalog.Keyring{
		SchemaVersion: catalog.KeyringSchemaVersion,
		KeyringID:     "cli-test",
		Serial:        1,
		IssuedAt:      now.Add(-time.Hour),
		ExpiresAt:     now.AddDate(1, 0, 0),
		Keys:          []catalog.Key{catalogEntry},
	}, p.anchorKey)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if p.keyring, err = keyring.Marshal(); err != nil {
		t.Fatalf("%v", err)
	}
	return p
}

// publish signs the catalogue once the server's address is known, since the
// artifact URL is part of what is signed.
func (p *publisher) publish(baseURL string) {
	p.t.Helper()
	sum := sha256.Sum256(p.archive)
	now := time.Now().UTC()
	document, err := catalog.Sign(&catalog.Catalog{
		SchemaVersion: catalog.SchemaVersion,
		CatalogID:     "cli-test",
		Serial:        1,
		IssuedAt:      now.Add(-time.Hour),
		ExpiresAt:     now.AddDate(0, 1, 0),
		Packages: []catalog.Package{{
			ID: "example.tool", Version: "1.0.0", Name: "Example Tool",
			Source: profile.Source{Homepage: "https://example.invalid/tool"},
			License: profile.License{
				SPDX:                "GPL-2.0-or-later",
				CorrespondingSource: "https://example.invalid/tool/source",
			},
			Artifacts: []catalog.Artifact{{
				Platform:     profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
				URL:          baseURL + "/example-tool-1.0.0.tar.gz",
				Kind:         catalog.KindTarGz,
				Size:         int64(len(p.archive)),
				SHA256:       "sha256:" + hex.EncodeToString(sum[:]),
				Signer:       p.keyID,
				UnpackedSize: 1 << 20,
				Executables:  []string{"bin/tool"},
			}},
		}},
	}, p.catalogKey)
	if err != nil {
		p.t.Fatalf("%v", err)
	}
	if p.document, err = document.Marshal(); err != nil {
		p.t.Fatalf("%v", err)
	}
}

func (p *publisher) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/" + catalog.KeyringFileName:
		w.Write(p.keyring)
	case "/" + catalog.CatalogFileName:
		w.Write(p.document)
	case "/example-tool-1.0.0.tar.gz":
		w.Write(p.archive)
	default:
		http.NotFound(w, r)
	}
}

// flipOneCharacter changes a base64 payload without changing its length, so
// the result still decodes and says something different.
func flipOneCharacter(payload string) string {
	middle := len(payload) / 2
	replacement := byte('A')
	if payload[middle] == 'A' {
		replacement = 'B'
	}
	return payload[:middle] + string(replacement) + payload[middle+1:]
}

func loadKeyFile(t *testing.T, path string) *catalog.PrivateKeyFile {
	t.Helper()
	key, err := catalog.LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return key
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
}
