package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LauncherAppDirName is the directory the retired Auto-Pigeon Launcher used
// under the OS config root. It is read, never written and never deleted — see
// Migrate.
const LauncherAppDirName = "auto-pigeon-launcher"

// BackupSuffix is appended to a config file before it is rewritten by a
// migration. One fixed name, not a timestamp: a migration that ran twice would
// otherwise leave a pile of backups, and the file being backed up is only ever
// the pre-migration one.
const BackupSuffix = ".pre-migration.bak"

// Migration is what Migrate did, reported rather than logged so the CLI, the
// tests and a future GUI banner all see the same account of it.
type Migration struct {
	// Performed is false when there was nothing to do. A second run on
	// already-migrated state reports false, which is what makes Migrate
	// idempotent in an observable way rather than only in effect.
	Performed bool
	// Sources are the files read, in the order they were consulted.
	Sources []string
	// Backups are the files written before anything was overwritten.
	Backups []string
	// Conflicts are fields both sources set to different values. Every one is
	// resolved in favour of the Companion's own file and recorded here; none is
	// resolved silently.
	Conflicts []Conflict
	// Notes are human-readable statements about what was carried over.
	Notes []string
}

// Conflict is one field two configurations disagreed about.
type Conflict struct {
	Field     string
	Kept      string
	Discarded string
}

func (c Conflict) String() string {
	return fmt.Sprintf("%s: kept %q from the Companion config, discarded %q from the Launcher config",
		c.Field, c.Kept, c.Discarded)
}

// Summary renders the migration for a terminal.
func (m Migration) Summary() string {
	if !m.Performed {
		return "configuration is already current; nothing to migrate"
	}
	var b strings.Builder
	b.WriteString("migrated local configuration\n")
	for _, source := range m.Sources {
		fmt.Fprintf(&b, "  read     %s\n", source)
	}
	for _, backup := range m.Backups {
		fmt.Fprintf(&b, "  backup   %s\n", backup)
	}
	for _, note := range m.Notes {
		fmt.Fprintf(&b, "  carried  %s\n", note)
	}
	for _, conflict := range m.Conflicts {
		fmt.Fprintf(&b, "  conflict %s\n", conflict)
	}
	return b.String()
}

// document is a config file decoded permissively enough to accept every schema
// this program has written.
//
// Both retired bootstraps are represented. The Launcher wrote "port" and a
// session with "expires"; the first Companion wrote "server_addr" — a whole
// host:port string — and a session with "obtained_at" and no expiry at all.
// Decoding both into one struct is what lets a single pass read either file
// without first having to guess which one it is holding.
type document struct {
	AUBBaseURL           string            `json:"aub_base_url"`
	Port                 int               `json:"port"`
	ServerAddr           string            `json:"server_addr"`
	ToolCacheDir         string            `json:"tool_cache_dir"`
	GameRoots            map[string]string `json:"game_roots"`
	MigratedFromLauncher bool              `json:"migrated_from_launcher"`
	Session              struct {
		Token      string    `json:"token"`
		UserID     string    `json:"user_id"`
		Email      string    `json:"email"`
		Expires    time.Time `json:"expires"`
		ObtainedAt time.Time `json:"obtained_at"`
	} `json:"session"`
}

// legacy reports whether this document was written by an older build and so
// needs rewriting even if nothing is merged into it.
func (d document) legacy() bool {
	return d.ServerAddr != "" || (d.Session.Token != "" && d.Session.Expires.IsZero() && !d.Session.ObtainedAt.IsZero())
}

// config turns a decoded document into the current Config.
func (d document) config() Config {
	port := d.Port
	if port == 0 && d.ServerAddr != "" {
		port = portOf(d.ServerAddr)
	}
	return Config{
		AUBBaseURL:   d.AUBBaseURL,
		Port:         port,
		ToolCacheDir: d.ToolCacheDir,
		GameRoots:    d.GameRoots,
		Session: Session{
			Token:  d.Session.Token,
			UserID: d.Session.UserID,
			Email:  d.Session.Email,
			// A legacy Companion session has no expiry. Leaving Expires zero is
			// deliberate: Session.Valid treats an unknown expiry as still
			// valid, so the token survives the migration and AUB stays the
			// authority on whether it still works. Inventing an expiry from
			// obtained_at would sign the user out on a guess.
			Expires: d.Session.Expires,
		},
	}
}

// portOf extracts the port from a "host:port" listen address. A zero or
// unparseable port yields 0, which Load fills from DefaultPort.
func portOf(addr string) int {
	index := strings.LastIndex(addr, ":")
	if index < 0 {
		return 0
	}
	port, err := strconv.Atoi(addr[index+1:])
	if err != nil || port < 0 || port > 65535 {
		return 0
	}
	return port
}

// readDocument decodes one config file. A missing file is (zero, false, nil).
func readDocument(path string) (document, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return document{}, false, nil
		}
		return document{}, false, fmt.Errorf("config: reading %s: %w", path, err)
	}
	var decoded document
	if err := json.Unmarshal(raw, &decoded); err != nil {
		// A file that cannot be parsed is never overwritten: saying it is
		// broken is recoverable, silently replacing a user's tokens is not.
		return document{}, false, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	return decoded, true, nil
}

// LauncherPath is where the retired Launcher kept its config.
func LauncherPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating the user config directory: %w", err)
	}
	return filepath.Join(base, LauncherAppDirName, "config.json"), nil
}

// Migrate brings local configuration up to the current schema, folding in the
// retired Launcher's config when one exists.
//
// It is safe to call on every run: with nothing to do it writes nothing and
// reports Performed false.
func Migrate() (Migration, error) {
	companion, err := Path()
	if err != nil {
		return Migration{}, err
	}
	launcher, err := LauncherPath()
	if err != nil {
		return Migration{}, err
	}
	return MigrateFiles(companion, launcher)
}

// MigrateFiles is Migrate against explicit paths. Tests use it, and so would a
// future --config flag.
//
// # What it does, and what it deliberately does not
//
// The Launcher's file is read and never touched: it belongs to a different
// installed program, and deleting another product's configuration to mark a
// job done is how people lose things. Idempotence comes instead from the
// "migrated_from_launcher" marker written into the Companion's own file, so a
// second run folds nothing in twice and a user who later edits the Launcher
// file does not get it silently re-imported.
//
// Where both files set a field, the Companion's value wins and the discarded
// one is reported as a Conflict. That direction is the conservative one: the
// Companion file is the config of the program actually being run, and a user
// who set something there did so more recently than they set the Launcher's.
func MigrateFiles(companionPath, launcherPath string) (Migration, error) {
	var report Migration

	current, currentExists, err := readDocument(companionPath)
	if err != nil {
		return report, err
	}
	if currentExists {
		report.Sources = append(report.Sources, companionPath)
	}

	legacy, legacyExists, err := readDocument(launcherPath)
	if err != nil {
		return report, err
	}
	// An already-marked Companion config does not consult the Launcher again.
	foldLauncher := legacyExists && !current.MigratedFromLauncher
	if foldLauncher {
		report.Sources = append(report.Sources, launcherPath)
	}

	if !foldLauncher && !current.legacy() {
		// Nothing to do. Note the marker is only written when there *was* a
		// Launcher file, so a machine that never had one keeps a config free of
		// a field that would mean nothing there.
		return report, nil
	}

	merged := current.config()

	if foldLauncher {
		from := legacy.config()
		merged, report.Conflicts, report.Notes = fold(merged, from, currentExists)
	}

	// The backup is written before anything is overwritten, and only when a
	// file is actually being replaced.
	if currentExists {
		backup := companionPath + BackupSuffix
		if err := copyFile(companionPath, backup); err != nil {
			return report, err
		}
		report.Backups = append(report.Backups, backup)
	}

	if legacyExists {
		merged.MigratedFromLauncher = true
	}
	if err := SaveTo(companionPath, merged); err != nil {
		return report, err
	}
	report.Performed = true
	return report, nil
}

// fold merges the Launcher's values into the Companion's, keeping the
// Companion's wherever both are set.
func fold(into, from Config, intoExists bool) (Config, []Conflict, []string) {
	var conflicts []Conflict
	var notes []string

	keepString := func(field string, target *string, incoming string) {
		if incoming == "" {
			return
		}
		if *target == "" {
			*target = incoming
			notes = append(notes, field+" from the Launcher config")
			return
		}
		if *target != incoming {
			conflicts = append(conflicts, Conflict{Field: field, Kept: *target, Discarded: incoming})
		}
	}

	keepString("aub_base_url", &into.AUBBaseURL, from.AUBBaseURL)
	keepString("tool_cache_dir", &into.ToolCacheDir, from.ToolCacheDir)

	if from.Port != 0 {
		switch {
		case into.Port == 0:
			into.Port = from.Port
			notes = append(notes, "port from the Launcher config")
		case into.Port != from.Port:
			conflicts = append(conflicts, Conflict{
				Field:     "port",
				Kept:      strconv.Itoa(into.Port),
				Discarded: strconv.Itoa(from.Port),
			})
		}
	}

	// Game roots merge per game rather than whole-map: a user with quake
	// configured in one file and quake2 in the other keeps both, which a
	// whole-value "the Companion wins" rule would silently halve.
	for game, root := range from.GameRoots {
		existing, ok := into.GameRoots[game]
		switch {
		case !ok || existing == "":
			if into.GameRoots == nil {
				into.GameRoots = map[string]string{}
			}
			into.GameRoots[game] = root
			notes = append(notes, "game root for "+game+" from the Launcher config")
		case existing != root:
			conflicts = append(conflicts, Conflict{
				Field: "game_roots." + game, Kept: existing, Discarded: root,
			})
		}
	}

	// The session is carried whole or not at all: a token belongs with the
	// account it authenticated, and splicing an email from one file onto a
	// token from another would produce a session that misreports who is signed
	// in.
	switch {
	case into.Session.Token == "" && from.Session.Token != "":
		into.Session = from.Session
		notes = append(notes, "AUB session from the Launcher config")
	case into.Session.Token != "" && from.Session.Token != "" && into.Session.Token != from.Session.Token:
		conflicts = append(conflicts, Conflict{
			Field:     "session",
			Kept:      describeSession(into.Session),
			Discarded: describeSession(from.Session),
		})
	}

	if !intoExists {
		notes = append(notes, "the whole configuration, since the Companion had none")
	}
	return into, conflicts, notes
}

// describeSession names a session without ever printing its token.
func describeSession(session Session) string {
	switch {
	case session.Email != "":
		return "the session for " + session.Email
	case session.UserID != "":
		return "the session for user " + session.UserID
	default:
		return "an anonymous session"
	}
}

// copyFile writes a 0600 copy of src at dst, replacing whatever is there.
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("config: reading %s for backup: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("config: creating the backup directory for %s: %w", dst, err)
	}
	// 0600 like the original: the backup holds the same token.
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		return fmt.Errorf("config: writing the backup %s: %w", dst, err)
	}
	return nil
}
