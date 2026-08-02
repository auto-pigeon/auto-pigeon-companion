// Package config loads and saves AUC's local configuration.
//
// The file lives in the OS-appropriate per-user config directory
// (os.UserConfigDir), which is:
//
//	Linux    $XDG_CONFIG_HOME/auto-pigeon-companion/config.json (or ~/.config/...)
//	macOS    ~/Library/Application Support/auto-pigeon-companion/config.json
//	Windows  %AppData%\auto-pigeon-companion\config.json
//
// The file holds an AUB session token, so it is written with 0600 and the
// containing directory with 0700. On Windows those bits are largely advisory,
// which is a known and accepted limitation of storing a token in a file at
// all; moving the token into the platform credential stores (Keychain, DPAPI,
// libsecret) would mean a CGO or third-party dependency and is out of scope
// for the stdlib-first architecture.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// AppDirName is the per-user directory AUC owns inside the OS config root.
const AppDirName = "auto-pigeon-companion"

// FileName is the config file inside AppDirName.
const FileName = "config.json"

// DefaultServerAddr is the listen address for GUI mode. Binding to the
// loopback interface — never 0.0.0.0 — is deliberate: the server exposes an
// authenticated user's session to whatever can reach it. Port 0 asks the OS
// for a free port, which avoids collisions with anything already listening;
// the chosen port is printed and used for the browser URL.
const DefaultServerAddr = "127.0.0.1:0"

// TODO(confirm-aub-base-url): Andrea to confirm the real dev/staging/prod base
// URLs for AUB (auto-pigeon-backend, a Pocketbase instance). This placeholder
// is a local Pocketbase default and is certainly not the deployed URL.
const DefaultAUBBaseURL = "http://127.0.0.1:8090"

// Session is the stored AUB authentication state.
//
// TODO(confirm-aub-auth-shape): the field set mirrors a generic Pocketbase
// auth response (token plus the authenticated record). The real collection
// name and record fields are unconfirmed, so nothing here should be treated as
// final.
type Session struct {
	Token      string    `json:"token,omitempty"`
	UserID     string    `json:"user_id,omitempty"`
	Email      string    `json:"email,omitempty"`
	ObtainedAt time.Time `json:"obtained_at"`
}

// Valid reports whether the session carries a token at all. It deliberately
// does not check expiry: Pocketbase token lifetimes are configured
// server-side, so "still accepted" is a question only AUB can answer.
func (session Session) Valid() bool { return session.Token != "" }

// Config is the whole on-disk document.
type Config struct {
	AUBBaseURL string  `json:"aub_base_url"`
	ServerAddr string  `json:"server_addr"`
	Session    Session `json:"session"`
}

// Default is the config used when no file exists yet.
func Default() Config {
	return Config{AUBBaseURL: DefaultAUBBaseURL, ServerAddr: DefaultServerAddr}
}

// Dir returns the directory holding the config file.
func Dir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate user config directory: %w", err)
	}
	return filepath.Join(root, AppDirName), nil
}

// Path returns the full config file path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the config file, returning Default() when the file does not exist
// yet. A missing file is the first-run case, not an error; a malformed file is
// an error, because silently resetting a user's settings is worse than saying
// the file is broken.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Default(), err
	}
	return LoadFrom(path)
}

// LoadFrom reads a config file from an explicit path. Tests use this; Load is
// the production entry point.
func LoadFrom(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), fmt.Errorf("cannot read config %s: %w", path, err)
	}
	config := Default()
	if err := json.Unmarshal(raw, &config); err != nil {
		return Default(), fmt.Errorf("cannot parse config %s: %w", path, err)
	}
	if config.AUBBaseURL == "" {
		config.AUBBaseURL = DefaultAUBBaseURL
	}
	if config.ServerAddr == "" {
		config.ServerAddr = DefaultServerAddr
	}
	return config, nil
}

// Save writes the config to its standard location.
func Save(config Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(path, config)
}

// SaveTo writes the config to an explicit path, creating the directory as
// needed. The write is atomic (temp file plus rename) so an interrupted save
// cannot leave a truncated config behind.
func SaveTo(path string, config Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create config directory %s: %w", dir, err)
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode config: %w", err)
	}
	encoded = append(encoded, '\n')

	temp, err := os.CreateTemp(dir, FileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("cannot create temporary config in %s: %w", dir, err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("cannot set permissions on %s: %w", tempName, err)
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("cannot write %s: %w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("cannot close %s: %w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("cannot replace config %s: %w", path, err)
	}
	return nil
}
