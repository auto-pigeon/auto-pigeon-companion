package managedtools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
)

// StateSchema versions managed-tools.json.
const StateSchema = "aucom.managed-tools/1"

// StateFileName is the state file, beside config.json.
const StateFileName = "managed-tools.json"

// State is everything this package remembers between runs. It is written
// only through [UpdateState], under the same cross-process lock every other
// mutable local file of this program uses.
type State struct {
	Schema string                `json:"schema"`
	Tools  map[ToolID]*ToolState `json:"tools"`
}

// Intent is what the person chose to do with an offered update.
type Intent string

const (
	IntentNone Intent = ""
	// IntentNow installs it now and activates it once no work uses the
	// installation it replaces.
	IntentNow Intent = "now"
	// IntentOnExit installs it in the background and activates it when the
	// Companion next closes normally.
	IntentOnExit Intent = "on_exit"
)

// InstallRef is one complete, verified installation on disk. The
// install.json inside Dir is the full record; this is its index entry.
type InstallRef struct {
	Version     string    `json:"version"`
	Tag         string    `json:"tag"`
	AssetID     int64     `json:"asset_id"`
	SHA256      string    `json:"sha256"`
	Dir         string    `json:"dir"`
	CompletedAt time.Time `json:"completed_at"`
}

// Identity is the bytes this installation was made from.
func (r InstallRef) Identity() BytesIdentity {
	return BytesIdentity{Version: r.Version, AssetID: r.AssetID, SHA256: r.SHA256}
}

// CheckState is one tool's release-check schedule.
type CheckState struct {
	LastAttempt time.Time `json:"last_attempt,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	// NextAttempt is when the next automatic check is due. A claimed check
	// moves it forward before it starts, so a second Companion that reads
	// the file meanwhile does not start one of its own.
	NextAttempt time.Time `json:"next_attempt,omitempty"`
	ETag        string    `json:"etag,omitempty"`
	Failures    int       `json:"failures,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	// RateLimitedUntil is what GitHub last asked for; nothing asks before.
	RateLimitedUntil time.Time `json:"rate_limited_until,omitempty"`
	// Best and Newer are the last evaluation, kept so a 304 — "nothing
	// changed" — still has an answer.
	Best  *Candidate `json:"best,omitempty"`
	Newer []Verdict  `json:"newer,omitempty"`
}

// Dismissal is an offer somebody cancelled without "don't ask again": it is
// not shown again in this run of the Companion, nor for a day.
type Dismissal struct {
	BytesIdentity
	Session string    `json:"session"`
	At      time.Time `json:"at"`
}

// IntegrityEvent is a release whose version this Companion knew with one set
// of bytes and now sees with another. It is never installed, and it is shown.
type IntegrityEvent struct {
	Known BytesIdentity `json:"known"`
	Seen  BytesIdentity `json:"seen"`
	At    time.Time     `json:"at"`
}

// SetupFailure is the last failed attempt to install or stage a version.
type SetupFailure struct {
	Version     string    `json:"version,omitempty"`
	Error       string    `json:"error"`
	At          time.Time `json:"at"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt"`
}

// ToolState is one tool's part of the state.
type ToolState struct {
	// Active is the installation managed bindings point at.
	Active *InstallRef `json:"active,omitempty"`
	// Previous is the one before it, kept for rollback.
	Previous *InstallRef `json:"previous,omitempty"`
	// Installed is every complete installation this program made and has not
	// removed. Bounded by the retention policy.
	Installed []InstallRef `json:"installed,omitempty"`
	Check     CheckState   `json:"check"`
	// Intent, IntentFor: what the person chose, and for which bytes.
	Intent    Intent         `json:"intent,omitempty"`
	IntentFor *BytesIdentity `json:"intent_for,omitempty"`
	// Skipped is "don't ask again for this version": exact bytes, per tool.
	Skipped   []BytesIdentity `json:"skipped,omitempty"`
	Dismissed *Dismissal      `json:"dismissed,omitempty"`
	Integrity *IntegrityEvent `json:"integrity,omitempty"`
	Failure   *SetupFailure   `json:"failure,omitempty"`
	// SelfLast is the Companion's last replacement of its own bundle.
	SelfLast *SelfResult `json:"self_last,omitempty"`
}

func newState() *State { return &State{Schema: StateSchema, Tools: map[ToolID]*ToolState{}} }

// Tool is this tool's part, created when absent.
func (s *State) Tool(id ToolID) *ToolState {
	if s.Tools == nil {
		s.Tools = map[ToolID]*ToolState{}
	}
	state := s.Tools[id]
	if state == nil {
		state = &ToolState{}
		s.Tools[id] = state
	}
	return state
}

// LoadState reads the state file; a missing one is an empty state.
func LoadState(path string) (*State, error) {
	raw, err := fsshare.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newState(), nil
	}
	if err != nil {
		return nil, err
	}
	state := newState()
	if err := json.Unmarshal(raw, state); err != nil {
		return nil, fmt.Errorf("managedtools: %s is not readable: %w", path, err)
	}
	if state.Schema != StateSchema {
		return nil, fmt.Errorf("managedtools: %s is %q; this build reads %s", path, state.Schema, StateSchema)
	}
	return state, nil
}

// UpdateState is the only way this program changes the state file.
func UpdateState(path string, mutate func(*State) error) (*State, error) {
	var result *State
	err := lockfile.With(path, lockfile.Options{Program: "auto-pigeon-companion managed tools"}, func() error {
		state, err := LoadState(path)
		if err != nil {
			return err
		}
		if err := mutate(state); err != nil {
			return err
		}
		if err := saveState(path, state); err != nil {
			return err
		}
		result = state
		return nil
	})
	return result, err
}

func saveState(path string, state *State) error {
	state.Schema = StateSchema
	for _, tool := range state.Tools {
		sort.SliceStable(tool.Installed, func(i, j int) bool {
			return tool.Installed[i].CompletedAt.Before(tool.Installed[j].CompletedAt)
		})
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "managed-tools-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(append(encoded, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return fsshare.Replace(name, path)
}

// skipped reports whether these exact bytes were skipped.
func (t *ToolState) skipped(identity BytesIdentity) bool {
	for _, s := range t.Skipped {
		if s == identity {
			return true
		}
	}
	return false
}

// knownBytes is the identity this program already associates with a
// version — an installation, or a skip — when there is one.
func (t *ToolState) knownBytes(version string) (BytesIdentity, bool) {
	for _, installed := range t.Installed {
		if installed.Version == version {
			return installed.Identity(), true
		}
	}
	for _, s := range t.Skipped {
		if s.Version == version {
			return s, true
		}
	}
	return BytesIdentity{}, false
}

// installed is the complete installation of these exact bytes, if any.
func (t *ToolState) installed(identity BytesIdentity) (InstallRef, bool) {
	for _, installed := range t.Installed {
		if installed.Identity() == identity {
			return installed, true
		}
	}
	return InstallRef{}, false
}
