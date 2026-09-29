// Package autobuild rebuilds a hosted map when a new revision of it is saved
// (NEW_265).
//
// # What it is
//
// A person switches Auto-build on for one map in their account and chooses a
// build profile. While the Companion is running, it asks AUB about that map
// roughly every thirty seconds, with the person's own session, and when a NEW
// revision appears it submits one ordinary build of it — the same Build & Run
// coordinator a person starts by hand, in its build-only mode, so the map and
// its textures come down the same authenticated, verified path and the build
// runs through the same job service. There is no daemon, nothing runs while the
// Companion is closed, and no game is ever launched by it.
//
// # The rules that make it safe to leave on
//
//   - The revision current when it is switched on is the BASELINE and is not
//     built; "Build current revision now" is a separate, explicit action.
//   - A revision is identified by AUB's revision id, number and content digest,
//     never by a title or a timestamp alone.
//   - One build per map at a time. Revisions saved while one runs are coalesced
//     into the newest, which is built once that one finishes.
//   - A revision is submitted at most once. The submission is written down
//     BEFORE the build is started, so a crash between the two cannot lead to a
//     second submission on restart; it is reported as a failure instead.
//   - A failed build is never retried on its own. It is shown with its revision
//     and its job, and Retry is a button.
//   - AUB being unreachable backs off, visibly and boundedly; a refused session
//     or a map that is gone stops checking and says why.
//
// # Where it lives
//
// One JSON file beside the jobs and builds, written only inside the
// cross-process lock (internal/lockfile), so the server's poller, a page action
// and a `companion autobuild` command in a terminal never lose each other's
// changes.
package autobuild

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
)

// SchemaVersion versions the state file.
const SchemaVersion = "aucom.autobuild/1.0"

// Revision is one saved version of a map, as AUB names it.
type Revision struct {
	ID            string `json:"revision_id"`
	Number        int    `json:"revision"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
}

// Key is the revision's identity: its id, its number and its content digest.
// Two answers with the same key are the same bytes; the title and the time are
// not part of it.
func (r Revision) Key() string {
	return r.ID + "#" + strconv.Itoa(r.Number) + "#" + r.ContentSHA256
}

// Valid reports whether AUB's answer names a revision at all.
func (r Revision) Valid() bool { return r.ID != "" && r.Number > 0 }

// Attempt is one build of one revision.
type Attempt struct {
	Revision  Revision  `json:"revision"`
	RunID     string    `json:"run_id,omitempty"`
	Pipeline  string    `json:"pipeline"`
	Manual    bool      `json:"manual,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// FinishedAt, State and Error are filled once the run has stopped.
	FinishedAt time.Time `json:"finished_at,omitempty"`
	State      string    `json:"state,omitempty"`
	Error      string    `json:"error,omitempty"`
	// Submitter names the Service instance that wrote this attempt down, so
	// a submission still being started is not mistaken for one a crash
	// interrupted (NEW_265A). Empty in files written before it existed.
	Submitter string `json:"submitter,omitempty"`
}

// Halt reasons: why a map is no longer being checked.
const (
	HaltAccess  = "access"
	HaltMissing = "missing"
)

// Entry is one map's auto-build.
type Entry struct {
	AssetID     string `json:"asset_id"`
	DisplayName string `json:"display_name,omitempty"`
	PipelineID  string `json:"pipeline_id"`
	Enabled     bool   `json:"enabled"`
	// Generation counts the changes a person made to this map's auto-build —
	// switched on, switched off, another build profile (NEW_265A). A question
	// to AUB carries the generation it was asked under, and its answer is
	// applied only if nothing has changed since: an answer that arrives after
	// the switch went off, or after it went off and on again, belongs to a
	// configuration that no longer exists. It is in the file, so it holds
	// across processes and across a restart; a timestamp would not tell two
	// changes made in the same instant apart.
	Generation uint64 `json:"generation,omitempty"`

	EnabledAt time.Time `json:"enabled_at,omitempty"`
	// Baseline is the revision that was current when it was switched on.
	Baseline *Revision `json:"baseline,omitempty"`
	// Observed is the newest revision AUB has reported, and when it was asked.
	Observed    *Revision `json:"observed,omitempty"`
	LastCheckAt time.Time `json:"last_check_at,omitempty"`
	// NextCheckAt is when the next question is due: thirty seconds after an
	// answer, longer after each failure in a row.
	NextCheckAt time.Time `json:"next_check_at,omitempty"`
	CheckError  string    `json:"check_error,omitempty"`
	Failures    int       `json:"failures,omitempty"`
	// Halted says why checking stopped: the session was refused, or the map is
	// gone. Switching Auto-build on again clears it.
	Halted string `json:"halted,omitempty"`

	// Pending is the newest revision waiting for a build.
	Pending *Revision `json:"pending,omitempty"`
	// Running is the build in progress, at most one.
	Running *Attempt `json:"running,omitempty"`
	// LastBuilt is the last build that succeeded; Failed the last that did not.
	LastBuilt *Attempt `json:"last_built,omitempty"`
	Failed    *Attempt `json:"failed,omitempty"`
	// Submitted is every revision key a build was started for, newest last,
	// bounded. It is what makes "never build a revision twice" survive a
	// restart.
	Submitted []string `json:"submitted,omitempty"`
}

// maxSubmitted bounds the remembered keys. Far more than the revisions a map
// gains between two builds; the newest are the ones that can recur.
const maxSubmitted = 200

func (e *Entry) submitted(key string) bool {
	for _, seen := range e.Submitted {
		if seen == key {
			return true
		}
	}
	return false
}

func (e *Entry) remember(key string) {
	if e.submitted(key) {
		return
	}
	e.Submitted = append(e.Submitted, key)
	if len(e.Submitted) > maxSubmitted {
		e.Submitted = append([]string(nil), e.Submitted[len(e.Submitted)-maxSubmitted:]...)
	}
}

// State is the whole file.
type State struct {
	SchemaVersion string  `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

// Find returns the entry for a map, and whether there is one.
func (s *State) Find(assetID string) (*Entry, bool) {
	for i := range s.Entries {
		if s.Entries[i].AssetID == assetID {
			return &s.Entries[i], true
		}
	}
	return nil, false
}

func (s *State) put(entry Entry) *Entry {
	if existing, found := s.Find(entry.AssetID); found {
		*existing = entry
		return existing
	}
	s.Entries = append(s.Entries, entry)
	sort.Slice(s.Entries, func(i, j int) bool { return s.Entries[i].AssetID < s.Entries[j].AssetID })
	found, _ := s.Find(entry.AssetID)
	return found
}

// Load reads the state file. A missing file is an empty state.
func Load(path string) (*State, error) {
	raw, err := fsshare.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{SchemaVersion: SchemaVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("autobuild: reading %s: %w", path, err)
	}
	state := &State{}
	if err := json.Unmarshal(raw, state); err != nil {
		return nil, fmt.Errorf("autobuild: reading %s: %w", path, err)
	}
	if state.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("autobuild: %s is %q; this build reads %q", path, state.SchemaVersion, SchemaVersion)
	}
	return state, nil
}

// errUnchanged, returned by a mutation, means it decided to change nothing:
// Update then writes nothing and reports no error.
var errUnchanged = errors.New("autobuild: nothing to change")

// Update is the one way the file changes: read, change and write inside the
// cross-process lock. It is held for the read, the change and the write only —
// never across a question to AUB or the start of a build.
func Update(path string, mutate func(*State) error) (*State, error) {
	var result *State
	err := lockfile.With(path, lockfile.Options{Program: "auto-pigeon-companion"}, func() error {
		state, err := Load(path)
		if err != nil {
			return err
		}
		if err := mutate(state); err != nil {
			if errors.Is(err, errUnchanged) {
				result = state
				return nil
			}
			return err
		}
		state.SchemaVersion = SchemaVersion
		raw, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, append(raw, '\n'), 0o600); err != nil {
			return err
		}
		if err := fsshare.Replace(temporary, path); err != nil {
			return err
		}
		result = state
		return nil
	})
	return result, err
}
