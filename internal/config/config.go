// Package config owns the Companion's on-disk local state: where it lives per platform,
// what it holds, and how it is read and written.
//
// # What belongs here
//
// Only settings the user or a previous run established: which AUB instance to
// talk to, which port the local GUI server prefers, where downloaded external
// tools are cached, and the current AUB session. Anything derived at runtime
// stays in memory.
//
// # Why the directories come from the standard library
//
// os.UserConfigDir and os.UserCacheDir already encode the per-platform
// conventions the Companion needs — %AppData% on Windows, ~/Library/Application Support
// on macOS, $XDG_CONFIG_HOME (or ~/.config) elsewhere — so this package adds a
// single "auto-pigeon-companion" element under each and nothing more. Hand-rolled
// path logic would be six branches of the same answer with more ways to be
// wrong on a machine where XDG_CONFIG_HOME is set.
//
// The tool cache is deliberately under the *cache* directory rather than the
// config directory: downloaded GPL-2.0 tool binaries are reproducible content
// that the Companion can re-fetch at any time, and putting them there means a user
// clearing caches loses nothing but download time. See THIRD_PARTY_NOTICES.md
// for why those binaries live outside this repository's own license.
//
// # Token storage — a known gap
//
// The AUB session is written into config.json with 0600 permissions. That is
// the honest minimum, not a secure secret store: on a shared machine any
// process running as the same user can read it. Moving the token to the OS
// keychain (Keychain / DPAPI / Secret Service) needs either cgo or a
// third-party dependency, and both are ruled out for this session by the
// CGO-free, stdlib-first constraint. Flagged rather than silently accepted.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AppDirName is the single path element the Companion adds under the OS config
// and cache directories.
//
// The retired Launcher used "auto-pigeon-launcher" here. Its file is not
// abandoned: Migrate reads it and folds it in — see migrate.go and
// LauncherAppDirName.
const AppDirName = "auto-pigeon-companion"

// DefaultPort is the loopback port the GUI server prefers.
//
// TODO(andrea): no port is registered for the Companion. 8789 is simply an
// unassigned high port unlikely to collide with a dev server, inherited from
// the Launcher bootstrap; it is not a decision.
// Server startup falls back to an ephemeral port when this one is taken, so
// the value only affects whether the URL is stable across runs.
const DefaultPort = 8789

// ErrNotFound reports that no config file exists yet. Callers treat this as
// "use defaults", not as a failure — a first run has no config.
var ErrNotFound = errors.New("config: no config file")

// Session is a stored AUB authentication result.
//
// TODO(andrea): confirm AUB's token lifetime and whether it issues a separate
// refresh token. PocketBase's auth-with-password returns one JWT that is
// refreshed by presenting it to auth-refresh, which is what internal/aub
// assumes; Expires is the Companion's own local estimate, not a value AUB returns.
type Session struct {
	Token   string    `json:"token"`
	UserID  string    `json:"user_id,omitempty"`
	Email   string    `json:"email,omitempty"`
	Expires time.Time `json:"expires,omitempty"`
}

// Valid reports whether the session has a token that has not locally expired.
// A zero Expires means "unknown", which is treated as still valid — the server
// is the authority, and refusing to send a token AUB might still accept would
// log the user out for no reason.
func (s Session) Valid() bool {
	if s.Token == "" {
		return false
	}
	return s.Expires.IsZero() || time.Now().Before(s.Expires)
}

// Config is the whole of the Companion's persisted local state.
type Config struct {
	// AUBBaseURL is the auto-pigeon-backend instance to authenticate against.
	AUBBaseURL string `json:"aub_base_url"`
	// Port is the preferred loopback port for the GUI server.
	Port int `json:"port"`
	// ToolCacheDir overrides the default external-tool cache location. Empty
	// means DefaultToolCacheDir.
	ToolCacheDir string `json:"tool_cache_dir,omitempty"`
	// GameRoots maps a game name to the directory its executable lives under,
	// filling the {game_root} placeholder in a launch config's executable
	// pattern. See internal/launch.
	GameRoots map[string]string `json:"game_roots,omitempty"`
	// Session is the current AUB login, if any.
	Session Session `json:"session,omitempty"`
	// MigratedFromLauncher records that the retired Auto-Pigeon Launcher's
	// configuration has already been folded into this file. It is what makes
	// Migrate idempotent without deleting the Launcher's own file — see
	// migrate.go.
	MigratedFromLauncher bool `json:"migrated_from_launcher,omitempty"`
}

// EnvAUBBaseURL names the environment variable that supplies AUB's address
// when config.json does not, and overrides it when both are set.
//
// # Why there is no default value here
//
// Both bootstraps this package was merged from compiled in
// "http://127.0.0.1:8090" as a fallback. That was wrong twice over. It is a
// hardcoded location for another component, which the workspace rule in
// AGENTS.md forbids outright: a compiled-in address turns a misconfiguration
// into a plausible-looking wrong answer, and a loopback address in particular
// means "the reader's own machine", which on any other machine can never work.
// And 8090 is PocketBase's *framework* default, never Auto-Pigeon's — AUB is
// reached on 9190 — so the fallback would have sent a first run to a port
// nothing serves while looking deliberate.
//
// A missing address is therefore an error the user acts on, reported by name.
// See ErrAUBNotConfigured.
const EnvAUBBaseURL = "AUCOM_AUB_BASE_URL"

// ErrAUBNotConfigured reports that no AUB address is configured. It names the
// two places that supply one so the message is actionable wherever it surfaces
// — the CLI prints it, and the GUI shows it on the routes that need AUB.
var ErrAUBNotConfigured = errors.New(
	"no auto-pigeon-backend address is configured: set " + EnvAUBBaseURL +
		" or the \"aub_base_url\" field in config.json")

// Default returns the configuration a first run uses. AUBBaseURL is
// deliberately empty — see EnvAUBBaseURL.
func Default() Config {
	return Config{Port: DefaultPort}
}

// AUB resolves the effective AUB address: the environment variable if set,
// otherwise the config file's value, otherwise ErrAUBNotConfigured.
func (c Config) AUB() (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv(EnvAUBBaseURL)); fromEnv != "" {
		return fromEnv, nil
	}
	if trimmed := strings.TrimSpace(c.AUBBaseURL); trimmed != "" {
		return trimmed, nil
	}
	return "", ErrAUBNotConfigured
}

// Dir is the OS-appropriate directory holding config.json.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating the user config directory: %w", err)
	}
	return filepath.Join(base, AppDirName), nil
}

// Path is the config file itself.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// DefaultToolCacheDir is where downloaded external tool binaries are kept when
// Config.ToolCacheDir is empty.
func DefaultToolCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("config: locating the user cache directory: %w", err)
	}
	return filepath.Join(base, AppDirName, "tools"), nil
}

// ToolCache resolves the effective tool cache directory for this config.
func (c Config) ToolCache() (string, error) {
	if c.ToolCacheDir != "" {
		return c.ToolCacheDir, nil
	}
	return DefaultToolCacheDir()
}

// Load reads config.json, filling unset fields from Default. A missing file
// returns Default and ErrNotFound, so a caller that does not care about the
// distinction can ignore the error and use the value.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Default(), err
	}
	return LoadFrom(path)
}

// LoadFrom is Load against an explicit path. Tests use it; so does any future
// --config flag.
func LoadFrom(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), ErrNotFound
		}
		return Default(), fmt.Errorf("config: reading %s: %w", path, err)
	}

	// Unmarshalling onto the defaults rather than a zero value is what makes a
	// config file written by an older build — one with no "port" key — come
	// back with a usable port instead of 0.
	value := Default()
	if err := json.Unmarshal(raw, &value); err != nil {
		return Default(), fmt.Errorf("config: parsing %s: %w", path, err)
	}
	if value.Port == 0 {
		value.Port = DefaultPort
	}
	return value, nil
}

// Save writes the config to its OS-appropriate location, creating the
// directory if needed.
func Save(value Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(path, value)
}

// SaveTo is Save against an explicit path.
//
// The write is atomic — a temporary file in the destination directory followed
// by a rename — because a config file truncated by a crash or a full disk
// would take the stored session with it, and a half-written JSON document is
// indistinguishable from a corrupt one on the next load.
func SaveTo(path string, value Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: creating %s: %w", dir, err)
	}

	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}
	encoded = append(encoded, '\n')

	temp, err := os.CreateTemp(dir, "config-*.json")
	if err != nil {
		return fmt.Errorf("config: creating a temporary file in %s: %w", dir, err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName) // No-op once the rename below has succeeded.

	// 0600 before any content is written: the file holds a session token, and
	// widening then narrowing the mode leaves a window where it is readable.
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("config: securing %s: %w", tempName, err)
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("config: writing %s: %w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("config: closing %s: %w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}
