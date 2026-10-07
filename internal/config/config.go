// Package config owns the Companion's on-disk local state: where it lives per platform,
// what it holds, and how it is read and written.
//
// # What belongs here
//
// Only settings the user or a previous run established: which AUB instance to
// talk to, which port the local GUI server prefers, where jobs and assets are
// kept, and the current AUB session. Anything derived at runtime stays in
// memory.
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
	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
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
// One JWT, refreshed by presenting it to `auth-refresh`. There is no separate
// refresh token, and there is no server-side revocation: AUB's capability
// document says so itself (`session.revocable` is false), which is why signing
// out is described as local.
//
// Expires is derived from the LIFETIME THE DEPLOYMENT DECLARED, read from
// `GET /api/companion/v1/capabilities`. It is zero when nothing has read one
// yet, and zero means unknown — see Valid.
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
	// JobsDir overrides where the job store lives. Empty means
	// DefaultJobsDir.
	JobsDir string `json:"jobs_dir,omitempty"`
	// ProfilesDir overrides where imported profile documents are read from.
	// Empty means DefaultProfilesDir.
	ProfilesDir string `json:"profiles_dir,omitempty"`
	// JobConcurrency is how many jobs run at once. Zero lets the executor
	// choose from the machine.
	JobConcurrency int `json:"job_concurrency,omitempty"`
	// GameRoots maps a game name to the directory its executable lives under,
	// filling the {game_root} placeholder in a launch config's executable
	// pattern. See internal/launch.
	GameRoots map[string]string `json:"game_roots,omitempty"`
	// Language is the page's language, as a code the page offers ("en",
	// "it", …). Empty means automatic: the browser's own, when it is a
	// translated one.
	Language string `json:"language,omitempty"`
	// AssetCacheDir overrides where AUB assets are cached. Empty means
	// DefaultAssetCacheDir.
	AssetCacheDir string `json:"asset_cache_dir,omitempty"`

	// IncidentDSN is where incident reports go, when the environment's
	// AUCOM_INCIDENT_DSN does not say. Empty — the ordinary case — means
	// nothing is sent. See internal/incident. The file is 0600, and the value
	// is never printed.
	IncidentDSN string `json:"incident_dsn,omitempty"`
	// IncidentEnvironment is the deployment incidents are filed under
	// (development, production, test), when AUCOM_INCIDENT_ENVIRONMENT does
	// not say.
	IncidentEnvironment string `json:"incident_environment,omitempty"`

	// Session is the current AUB login, if any.
	Session Session `json:"session,omitempty"`
	// MigratedFromLauncher records that the retired Auto-Pigeon Launcher's
	// configuration has already been folded into this file. It is what makes
	// Migrate idempotent without deleting the Launcher's own file — see
	// migrate.go.
	MigratedFromLauncher bool `json:"migrated_from_launcher,omitempty"`
	// URIHandler records that this machine's `autopigeon://` handler was
	// registered for the Companion, and for which program. Written at first
	// use and when the handler is registered from Settings; absent means the
	// Companion has never registered it. See internal/cli/urihandler_firstuse.go.
	URIHandler *URIHandler `json:"uri_handler,omitempty"`
	// LeakTestPipelines is the pipeline the user pinned for each game's leak
	// test, keyed by the APMap game ("quake1", "quake3"). Nothing is pinned
	// out of the box (NEW_310, HITL): the Companion asks when the editor's
	// first request for that game arrives.
	LeakTestPipelines map[string]string `json:"leak_test_pipelines,omitempty"`
}

// URIHandler is the record of a registered `autopigeon://` handler.
type URIHandler struct {
	// Executable is the program the handler was pointed at.
	Executable string `json:"executable"`
	// Method is how: urischeme.MethodRegistry, MethodXDG.
	Method string `json:"method,omitempty"`
	// At is when, in UTC.
	At time.Time `json:"at"`
	// How says whether the Companion registered it at first use
	// ("registered"), took it over from an older Companion ("taken over"),
	// found it already pointing here ("found"), or a person pressed the
	// button in Settings ("settings").
	How string `json:"how,omitempty"`
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
	"no Auto-Pigeon server is chosen yet: choose Auto-Pigeon or Auto-Pigeon beta in Settings " +
		"(or set " + EnvAUBBaseURL + ")")

// Default returns the configuration a first run uses. AUBBaseURL is
// deliberately empty — see EnvAUBBaseURL.
func Default() Config {
	return Config{Port: DefaultPort}
}

// AUB resolves the effective AUB address: the environment variable if set,
// otherwise the config file's value, otherwise ErrAUBNotConfigured.
//
// Under the release rule (release_policy.go) the environment variable can only
// have come from the config.json beside the executable, and a saved address
// that is not an official deployment is set aside and reported.
func (c Config) AUB() (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv(EnvAUBBaseURL)); fromEnv != "" {
		return fromEnv, nil
	}
	if trimmed := strings.TrimSpace(c.AUBBaseURL); trimmed != "" {
		if OfficialOnly() && !IsOfficialBackend(trimmed) {
			NoteIgnoredAddress(trimmed, "an earlier Settings choice saved in this user's config.json")
			return "", ErrAUBNotConfigured
		}
		return trimmed, nil
	}
	return "", ErrAUBNotConfigured
}

// SavedAUB is the address in the per-user file that is in effect: the saved
// value, or "" when the release rule sets it aside.
func (c Config) SavedAUB() string {
	trimmed := strings.TrimSpace(c.AUBBaseURL)
	if OfficialOnly() && trimmed != "" && !IsOfficialBackend(trimmed) {
		return ""
	}
	return trimmed
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

// EnvJobsDir and EnvProfilesDir override the two directories the executor
// uses. They exist for two real cases: a machine whose home directory is on a
// small disk, and a test that must not touch the developer's own state.
const (
	EnvJobsDir     = "AUCOM_JOBS_DIR"
	EnvProfilesDir = "AUCOM_PROFILES_DIR"
)

// DefaultJobsDir is where job records, logs and published artifacts live.
//
// Under the *cache* directory, alongside the tool cache, because everything in
// it is reproducible: a job's artifacts are files the user asked a tool to
// produce, and the ones they wanted kept were written where they asked. A user
// clearing caches loses build history, not work.
func DefaultJobsDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("config: locating the user cache directory: %w", err)
	}
	return filepath.Join(base, AppDirName, "jobs"), nil
}

// Jobs resolves the effective job store directory.
func (c Config) Jobs() (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv(EnvJobsDir)); fromEnv != "" {
		return fromEnv, nil
	}
	if c.JobsDir != "" {
		return c.JobsDir, nil
	}
	return DefaultJobsDir()
}

// DefaultProfilesDir is where imported profile documents are read from.
//
// Under the *config* directory, not the cache: an imported profile is
// something the user chose and reviewed, and a cache clean must not silently
// remove the description of the toolchain their projects are built with.
func DefaultProfilesDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "profiles"), nil
}

// Profiles resolves the effective profile directory.
func (c Config) Profiles() (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv(EnvProfilesDir)); fromEnv != "" {
		return fromEnv, nil
	}
	if c.ProfilesDir != "" {
		return c.ProfilesDir, nil
	}
	return DefaultProfilesDir()
}

// EnvOffline names the environment variable that forbids every network access.
//
// Offline changes what is *available*, never what is checked: an installed
// package still has its digest and its revocation status verified, because
// both of those are local facts recorded when it was installed. What offline
// cannot do is install something new, and it says so.
const EnvOffline = "AUCOM_OFFLINE"

// Offline reports whether the environment forbids network access.
func Offline() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvOffline))) {
	case "", "0", "false", "no":
		return false
	}
	return true
}

// BindingsPath is the file recording what is installed on this machine and
// what the user granted it. It sits beside config.json, and like it, it is
// state the user chose rather than something re-derivable.
func BindingsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bindings.json"), nil
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
	raw, err := fsshare.ReadFile(path)
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
// by a rename — because a config file truncated by a crash or a full disk would
// take the stored session with it, and a half-written JSON document is
// indistinguishable from a corrupt one on the next load.
//
// It is also serialised against every other writer of this file, through
// [lockfile]. Atomicity alone stops a torn write and does nothing about a lost
// one: the GUI server and a `companion` invocation in a terminal are two
// processes, and two of these calls that read the same file and write different
// fields end with one field silently gone.
//
// A caller that read the file, changed something and is writing it back wants
// [Update] rather than this, because the read has to be inside the lock too.
func SaveTo(path string, value Config) error {
	return lockfile.With(path, lockOptions(), func() error { return saveLocked(path, value) })
}

// Update is the read-modify-write every caller that changes one field should
// use.
//
// Load, mutate, save, all inside one lock. mutate is handed the configuration
// as it is on disk *now* — not as the caller read it earlier — which is the
// whole point: a second instance that changed the port between then and now has
// its change carried forward instead of overwritten. A first run has no file,
// and mutate is handed [Default].
//
// The mutated value is returned so a caller can report what it wrote.
func Update(path string, mutate func(*Config) error) (Config, error) {
	var result Config
	err := lockfile.With(path, lockOptions(), func() error {
		current, err := LoadFrom(path)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if mutate != nil {
			if err := mutate(&current); err != nil {
				return err
			}
		}
		if err := saveLocked(path, current); err != nil {
			return err
		}
		result = current
		return nil
	})
	return result, err
}

// lockOptions labels this program in the lock file, so a person told the file
// is busy is told by what.
func lockOptions() lockfile.Options {
	return lockfile.Options{Program: "auto-pigeon-companion"}
}

// saveLocked is the write itself. It assumes the caller holds the lock.
func saveLocked(path string, value Config) error {
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
	if err := fsshare.Replace(tempName, path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}

// EnvAssetCacheDir overrides where AUB assets are cached, for the same two real
// cases EnvJobsDir exists for: a home directory on a small disk, and a test that
// must not touch the developer's own state.
const EnvAssetCacheDir = "AUCOM_ASSET_CACHE_DIR"

// DefaultAssetCacheDir is where assets synced from AUB are kept.
//
// Under the *cache* directory, beside the tool cache and the job store, because
// everything in it is re-fetchable: an asset is somebody's map on a backend, and
// the local copy is a copy. A user clearing caches loses a download, never work —
// and a build that pinned a revision says so in its manifest, so what was lost is
// nameable rather than merely gone.
//
// This is deliberately NOT where a user's own exported or built files go: those
// are written where they asked.
func DefaultAssetCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("config: locating the user cache directory: %w", err)
	}

	return filepath.Join(base, AppDirName, "assets"), nil
}

// AssetCache resolves the effective asset cache directory.
func (c Config) AssetCache() (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv(EnvAssetCacheDir)); fromEnv != "" {
		return fromEnv, nil
	}
	if c.AssetCacheDir != "" {
		return c.AssetCacheDir, nil
	}

	return DefaultAssetCacheDir()
}
