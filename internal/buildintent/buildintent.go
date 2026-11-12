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
// [Receive] only records. The page lists the requests with [List], resolves
// each against AUB itself, takes one into Build & Run's review, and a person
// presses Build or Build & Run. That start [Consume]s exactly the request it
// was for and leaves every other one pending. A dismissed or expired request
// is simply gone.
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

// SchemaVersion versions the file. 2.0 (NEW_502, issues #33 and #35) holds a
// LIST of pending requests: a second link no longer replaces the first, so two
// presses of Build in the editor — two maps, two tabs, or the same request
// opened again with a new delivery id — are both kept and each is built or
// dismissed by its own id.
const SchemaVersion = "aucom.build-request-intent/2.0"

// FileName is the file, in the configuration directory. It is not the 1.0
// file's name, and that is the version rule: a Companion older than 2.0 reads
// and writes only [LegacyFileName], never sees this file and so can never
// overwrite a list with its single request. Whatever an older program (or an
// older link handler) leaves in the legacy file is imported here on the next
// read, and the legacy file is removed.
const FileName = "build-requests.json"

// LegacyFileName and LegacySchemaVersion are the 1.0 file: one request.
const (
	LegacyFileName      = "build-request-intent.json"
	LegacySchemaVersion = "aucom.build-request-intent/1.0"
)

// PendingLimit bounds the list however many links arrive. Past it the oldest
// request is dropped: it would expire long before anybody built sixteen.
const PendingLimit = 16

// Lifetime is how long "the editor asked for this build" is remembered. Longer
// than a leak test's ten minutes, because a build request may wait for a
// compiler to be set up before anybody presses Build — and no longer than AUB's
// own request, which expires after the same half hour.
const Lifetime = 30 * time.Minute

// Pending is one request the editor asked for and nobody has built or
// dismissed yet.
type Pending struct {
	Request    aub.BuildRequestLink `json:"request"`
	ReceivedAt time.Time            `json:"received_at"`
	ExpiresAt  time.Time            `json:"expires_at"`
	// LaunchedByVersion is the version of the program the link started
	// (NEW_368). When a Companion was already running, that program handed the
	// link to it, and the running one is what serves it: the two are told
	// apart in the editor.
	LaunchedByVersion string `json:"launched_by_version,omitempty"`
}

// Intent is the file.
type Intent struct {
	SchemaVersion string    `json:"schema_version"`
	Requests      []Pending `json:"requests,omitempty"`
}

// legacyIntent is the 1.0 file, read only to be imported.
type legacyIntent struct {
	SchemaVersion     string                `json:"schema_version"`
	Request           *aub.BuildRequestLink `json:"request,omitempty"`
	ReceivedAt        time.Time             `json:"received_at,omitempty"`
	ExpiresAt         time.Time             `json:"expires_at,omitempty"`
	LaunchedByVersion string                `json:"launched_by_version,omitempty"`
}

// Path is the intent file for a configuration directory.
func Path(configDir string) string { return filepath.Join(configDir, FileName) }

func legacyPath(path string) string { return filepath.Join(filepath.Dir(path), LegacyFileName) }

func options() lockfile.Options {
	return lockfile.Options{Program: "auto-pigeon-companion", Timeout: 10 * time.Second}
}

// Receive records a link the operating system delivered. A different request
// is ADDED beside any already pending; the same request delivered again only
// renews its lifetime.
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
		intent.add(Pending{Request: link, ReceivedAt: now, ExpiresAt: now.Add(Lifetime), LaunchedByVersion: launchedBy})
	})
}

// add puts one request in the list: in place of the same id, else at the end.
// An id is bound to the intent it first named: the same id with another
// intent is a different link, and it replaces the first (the newer click is
// what the editor asks now), still under its one id.
func (i *Intent) add(item Pending) {
	for index := range i.Requests {
		if i.Requests[index].Request.RequestID == item.Request.RequestID {
			i.Requests[index] = item
			return
		}
	}
	i.Requests = append(i.Requests, item)
	if len(i.Requests) > PendingLimit {
		i.Requests = append([]Pending(nil), i.Requests[len(i.Requests)-PendingLimit:]...)
	}
}

func (i *Intent) index(requestID string) int {
	if requestID == "" {
		return -1
	}
	for index := range i.Requests {
		if i.Requests[index].Request.RequestID == requestID {
			return index
		}
	}
	return -1
}

// List is every pending request, oldest first.
func List(path string, now time.Time) ([]Pending, error) {
	var list []Pending
	err := update(path, now, func(intent *Intent) {
		list = append([]Pending(nil), intent.Requests...)
	})
	return list, err
}

// Newest is the most recently received pending request and when it arrived,
// or nil: what a reader that knows only one request is shown.
func Newest(path string, now time.Time) (*aub.BuildRequestLink, time.Time, error) {
	list, err := List(path, now)
	if err != nil || len(list) == 0 {
		return nil, time.Time{}, err
	}
	newest := list[0]
	for _, item := range list[1:] {
		if !item.ReceivedAt.Before(newest.ReceivedAt) {
			newest = item
		}
	}
	request := newest.Request
	return &request, newest.ReceivedAt, nil
}

// Get is the pending request with this id, or nil.
func Get(path, requestID string, now time.Time) (*Pending, error) {
	var found *Pending
	err := update(path, now, func(intent *Intent) {
		if index := intent.index(requestID); index >= 0 {
			copy := intent.Requests[index]
			found = &copy
		}
	})
	return found, err
}

// Consume removes the request a start is for, and returns it — only that one,
// and only while it is pending. Every other request stays.
func Consume(path, requestID string, now time.Time) (*aub.BuildRequestLink, error) {
	return take(path, requestID, now)
}

// Dismiss forgets the request a person dismissed — that one, and only while it
// is pending. It returns what it forgot, or nil.
func Dismiss(path, requestID string, now time.Time) (*aub.BuildRequestLink, error) {
	return take(path, requestID, now)
}

func take(path, requestID string, now time.Time) (*aub.BuildRequestLink, error) {
	var taken *aub.BuildRequestLink
	err := update(path, now, func(intent *Intent) {
		index := intent.index(requestID)
		if index < 0 {
			return
		}
		copy := intent.Requests[index].Request
		taken = &copy
		intent.Requests = append(intent.Requests[:index], intent.Requests[index+1:]...)
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
		imported, err := importLegacy(path, &intent)
		if err != nil {
			return err
		}
		prune(&intent, now)
		mutate(&intent)
		prune(&intent, now)

		if err := save(path, intent); err != nil {
			return err
		}
		if imported {
			// Only once the list holding it is on disk.
			if err := os.Remove(legacyPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	})
}

// importLegacy moves a 1.0 file's request into the list, under the 1.0 file's
// own lock, which is the one an older program takes. It reports whether there
// was a 1.0 file to remove. A file that is not 1.0 is left where it is.
func importLegacy(path string, intent *Intent) (bool, error) {
	legacy := legacyPath(path)
	if _, err := os.Stat(legacy); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	imported := false
	err := lockfile.With(legacy, options(), func() error {
		raw, err := fsshare.ReadFile(legacy)
		if err != nil {
			return nil
		}
		var old legacyIntent
		if json.Unmarshal(raw, &old) != nil || old.SchemaVersion != LegacySchemaVersion {
			return nil
		}
		imported = true
		if old.Request != nil {
			item := Pending{Request: *old.Request, ReceivedAt: old.ReceivedAt, ExpiresAt: old.ExpiresAt,
				LaunchedByVersion: old.LaunchedByVersion}
			if valid(item) {
				if index := intent.index(item.Request.RequestID); index < 0 || intent.Requests[index].ReceivedAt.Before(item.ReceivedAt) {
					intent.add(item)
				}
			}
		}
		return nil
	})
	return imported, err
}

// valid re-validates what is on disk rather than trusting it: the file is the
// user's own, but a request that no longer parses is not one to show, and a
// lifetime longer than this program grants is not one to keep.
func valid(item Pending) bool {
	if _, err := aub.ParseBuildRequestLink(item.Request.Link()); err != nil {
		return false
	}
	return !item.ExpiresAt.IsZero() && item.ExpiresAt.Sub(item.ReceivedAt) <= Lifetime
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
	kept := intent.Requests[:0]
	seen := map[string]bool{}
	for _, item := range intent.Requests {
		if valid(item) && !seen[item.Request.RequestID] {
			seen[item.Request.RequestID] = true
			kept = append(kept, item)
		}
	}
	intent.Requests = kept

	return intent
}

func prune(intent *Intent, now time.Time) {
	kept := intent.Requests[:0]
	for _, item := range intent.Requests {
		if now.Before(item.ExpiresAt) {
			kept = append(kept, item)
		}
	}
	intent.Requests = kept
	intent.SchemaVersion = SchemaVersion
}

func save(path string, intent Intent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if len(intent.Requests) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}
	raw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".build-requests-*")
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

// Receipt is when one pending request arrived and the version of the program
// that received it — "" when that was not recorded. Zero time when that
// request is not pending.
func Receipt(path, requestID string, now time.Time) (time.Time, string, error) {
	item, err := Get(path, requestID, now)
	if err != nil || item == nil {
		return time.Time{}, "", err
	}
	return item.ReceivedAt, item.LaunchedByVersion, nil
}
