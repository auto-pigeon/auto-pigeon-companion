// Package buildintent holds the one thing an `autopigeon://build/request/…`
// link leaves on this machine between the moment the operating system hands it
// over and the moment the Companion's page shows it.
//
// It is internal/leakintent's shape for a different request, kept apart on
// purpose: a build request is not a leak test, and one is never recorded, read
// or consumed as the other. The link carries an opaque id and what the editor
// asked for (build, or build and run) — no map, no revision, no path and
// nothing secret — so the file holds the parsed link, when it arrived and when
// it stops mattering. What the request is about is AUB's, and is asked of AUB
// with the Companion's own session each time it is shown.
//
// # Nothing here runs anything
//
// [Receive] only records. The page reads the request with [Read], resolves it
// against AUB itself, takes it into Build & Run's review, and a person presses
// Build or Build & Run. That start [Consume]s exactly the request it was for.
// A dismissed or expired request is simply gone.
package buildintent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
)

// SchemaVersion versions the file.
const SchemaVersion = "aucom.build-request-intent/1.0"

// FileName is the file, in the configuration directory.
const FileName = "build-request-intent.json"

// Lifetime is how long "the editor asked for this build" is remembered. Longer
// than a leak test's ten minutes, because a build request may wait for a
// compiler to be set up before anybody presses Build — and no longer than AUB's
// own request, which expires after the same half hour.
const Lifetime = 30 * time.Minute

// Intent is the file.
type Intent struct {
	SchemaVersion string                `json:"schema_version"`
	Request       *aub.BuildRequestLink `json:"request,omitempty"`
	ReceivedAt    time.Time             `json:"received_at,omitempty"`
	ExpiresAt     time.Time             `json:"expires_at,omitempty"`
	// LaunchedByVersion is the version of the program the link started
	// (NEW_368). When a Companion was already running, that program handed the
	// link to it, and the running one is what serves it: the two are told
	// apart in the editor. Optional, so an older Companion reads and rewrites
	// this file as before (and drops it).
	LaunchedByVersion string `json:"launched_by_version,omitempty"`
}

// Path is the intent file for a configuration directory.
func Path(configDir string) string { return filepath.Join(configDir, FileName) }

func options() lockfile.Options {
	return lockfile.Options{Program: "auto-pigeon-companion", Timeout: 10 * time.Second}
}

// Receive records a link the operating system delivered. A newer link replaces
// an older one: the editor asks about the map it has open now.
func Receive(path string, link aub.BuildRequestLink, now time.Time) error {
	return ReceiveFrom(path, link, "", now)
}

// ReceiveFrom is Receive, recording the version of the program that received
// the link from the operating system.
func ReceiveFrom(path string, link aub.BuildRequestLink, launchedBy string, now time.Time) error {
	if _, err := aub.ParseBuildRequestLink(link.Link()); err != nil {
		return fmt.Errorf("buildintent: %w", err)
	}

	return update(path, now, func(intent *Intent) {
		request := link
		intent.Request, intent.ReceivedAt, intent.ExpiresAt = &request, now, now.Add(Lifetime)
		intent.LaunchedByVersion = launchedBy
	})
}

// Read is the pending request and when it arrived, or nil.
func Read(path string, now time.Time) (*aub.BuildRequestLink, time.Time, error) {
	var (
		request  *aub.BuildRequestLink
		received time.Time
	)
	err := update(path, now, func(intent *Intent) {
		request, received = intent.Request, intent.ReceivedAt
	})

	return request, received, err
}

// Consume removes the request a start is for, and returns it — only when it is
// the one pending. A newer editor click is never erased by an older button.
func Consume(path, requestID string, now time.Time) (*aub.BuildRequestLink, error) {
	return take(path, requestID, now)
}

// Dismiss forgets the request a person dismissed — that one, and only while it
// is the one pending. It returns what it forgot, or nil.
func Dismiss(path, requestID string, now time.Time) (*aub.BuildRequestLink, error) {
	return take(path, requestID, now)
}

func take(path, requestID string, now time.Time) (*aub.BuildRequestLink, error) {
	var taken *aub.BuildRequestLink
	err := update(path, now, func(intent *Intent) {
		if intent.Request == nil || requestID == "" || intent.Request.RequestID != requestID {
			return
		}
		copy := *intent.Request
		taken = &copy
		forget(intent)
	})
	return taken, err
}

// Prune removes an expired request and nothing else.
func Prune(path string, now time.Time) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return update(path, now, func(*Intent) {})
}

func update(path string, now time.Time, mutate func(*Intent)) error {
	return lockfile.With(path, options(), func() error {
		intent := load(path)
		prune(&intent, now)
		mutate(&intent)
		prune(&intent, now)

		return save(path, intent)
	})
}

func load(path string) Intent {
	raw, err := fsshare.ReadFile(path)
	if err != nil {
		return Intent{SchemaVersion: SchemaVersion}
	}
	var intent Intent
	if json.Unmarshal(raw, &intent) != nil || intent.SchemaVersion != SchemaVersion {
		return Intent{SchemaVersion: SchemaVersion}
	}
	if intent.Request != nil {
		// Whatever is on disk is re-validated, never trusted: the file is the
		// user's own, but a request that no longer parses is not one to show,
		// and a lifetime longer than this program grants is not one to keep.
		if _, err := aub.ParseBuildRequestLink(intent.Request.Link()); err != nil ||
			intent.ExpiresAt.Sub(intent.ReceivedAt) > Lifetime {
			forget(&intent)
		}
	}

	return intent
}

func forget(intent *Intent) {
	intent.Request, intent.ReceivedAt, intent.ExpiresAt = nil, time.Time{}, time.Time{}
}

func prune(intent *Intent, now time.Time) {
	if intent.Request != nil && !now.Before(intent.ExpiresAt) {
		forget(intent)
	}
	intent.SchemaVersion = SchemaVersion
}

func save(path string, intent Intent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if intent.Request == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}
	raw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".build-request-intent-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err = temporary.Chmod(0o600); err != nil {
		temporary.Close()

		return err
	}
	if _, err = temporary.Write(raw); err != nil {
		temporary.Close()

		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = fsshare.Replace(temporary.Name(), path); err != nil {
		return fmt.Errorf("buildintent: %w", err)
	}

	return nil
}

// Receipt is when the pending request arrived and the version of the program
// that received it — "" when that was not recorded. Zero time when nothing is
// pending.
func Receipt(path string, now time.Time) (time.Time, string, error) {
	var (
		received time.Time
		by       string
	)
	err := update(path, now, func(intent *Intent) {
		if intent.Request != nil {
			received, by = intent.ReceivedAt, intent.LaunchedByVersion
		}
	})
	return received, by, err
}
