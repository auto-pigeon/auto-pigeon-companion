// Package leakintent holds the one thing an `autopigeon://leaktest/…` link
// leaves on this machine between the moment the operating system hands it over
// and the moment the Companion's page shows it (`AUP 264` follow-up).
//
// It is internal/joinintent's shape for a different request, and deliberately
// smaller: a leak-test link carries no ticket, nothing to redeem and nothing
// secret — a map id this account can already read, a revision number and that
// revision's content digest. So the file holds the parsed link, when it arrived
// and when it stops mattering. Nothing else: no session, no path, no result.
//
// # Nothing here runs anything
//
// [Receive] only records. The page reads the request with [Read], resolves it
// against AUB itself, shows the exact command, and a person presses Run; a
// dismissed or expired request is simply gone.
package leakintent

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
const SchemaVersion = "aucom.leak-test-intent/1.0"

// FileName is the file, in the configuration directory.
const FileName = "leak-test-intent.json"

// Lifetime is how long "the editor asked for this leak test" is remembered. The
// same ten minutes a clicked game is: long enough to start the Companion and
// sign in, short enough that yesterday's click is not today's surprise.
const Lifetime = 10 * time.Minute

// Intent is the file.
type Intent struct {
	SchemaVersion string            `json:"schema_version"`
	Request       *aub.LeakTestLink `json:"request,omitempty"`
	ReceivedAt    time.Time         `json:"received_at,omitempty"`
	ExpiresAt     time.Time         `json:"expires_at,omitempty"`
}

// Path is the intent file for a configuration directory.
func Path(configDir string) string { return filepath.Join(configDir, FileName) }

func options() lockfile.Options {
	return lockfile.Options{Program: "auto-pigeon-companion", Timeout: 10 * time.Second}
}

// Receive records a link the operating system delivered. A newer link replaces
// an older one: the editor asks about the revision it has open now.
func Receive(path string, link aub.LeakTestLink, now time.Time) error {
	if _, err := aub.ParseLeakTestLink(link.Link()); err != nil {
		return fmt.Errorf("leakintent: %w", err)
	}

	return update(path, now, func(intent *Intent) {
		request := link
		intent.Request, intent.ReceivedAt, intent.ExpiresAt = &request, now, now.Add(Lifetime)
	})
}

// Read is the pending request, or nil.
func Read(path string, now time.Time) (*aub.LeakTestLink, time.Time, error) {
	var (
		request  *aub.LeakTestLink
		received time.Time
	)
	err := update(path, now, func(intent *Intent) {
		request, received = intent.Request, intent.ReceivedAt
	})

	return request, received, err
}

// Dismiss forgets the pending request.
func Dismiss(path string, now time.Time) error {
	return update(path, now, func(intent *Intent) {
		intent.Request, intent.ReceivedAt, intent.ExpiresAt = nil, time.Time{}, time.Time{}
	})
}

// Consume removes only the request a reviewed build is about to start. A
// newer editor click is never erased by an older Build button.
func Consume(path, requestID string, now time.Time) (*aub.LeakTestLink, error) {
	var consumed *aub.LeakTestLink
	err := update(path, now, func(intent *Intent) {
		if intent.Request == nil || intent.Request.RequestID != requestID || requestID == "" {
			return
		}
		copy := *intent.Request
		consumed = &copy
		intent.Request, intent.ReceivedAt, intent.ExpiresAt = nil, time.Time{}, time.Time{}
	})
	return consumed, err
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
		// user's own, but a request that no longer parses is not one to show.
		if _, err := aub.ParseLeakTestLink(intent.Request.Link()); err != nil {
			intent.Request = nil
		}
	}

	return intent
}

func prune(intent *Intent, now time.Time) {
	if intent.Request != nil && !now.Before(intent.ExpiresAt) {
		intent.Request, intent.ReceivedAt, intent.ExpiresAt = nil, time.Time{}, time.Time{}
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
	temporary, err := os.CreateTemp(filepath.Dir(path), ".leak-test-intent-*")
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
		return fmt.Errorf("leakintent: %w", err)
	}

	return nil
}
