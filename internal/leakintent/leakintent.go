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

// RetiredFor is how long a used or replaced request id is remembered: longer
// than any operating system holds a link, and short enough that the file stays
// small. retiredLimit bounds it however many requests a day brings.
const (
	RetiredFor   = 24 * time.Hour
	retiredLimit = 32
)

// RepeatFor is how long "this link was opened again" is shown: long enough to
// be read by the person who has just clicked, short enough that it is never
// found there the next morning.
const RepeatFor = 2 * time.Minute

// Retired is a request id that may not become pending again.
type Retired struct {
	RequestID string    `json:"request_id"`
	Until     time.Time `json:"until"`
	// Used says a build was started for it (as against replaced by a newer
	// click), and BuildID which one, once the build has an id (NEW_508, issue
	// #20). Both optional: an older file's entries read as "retired", which is
	// all that was known then.
	Used    bool   `json:"used,omitempty"`
	BuildID string `json:"build_id,omitempty"`
}

// Repeat is a link whose request was already built, delivered again: the
// second click on "Open", a link the operating system held back, an old tab.
// It never becomes a second build (that is what Retired is for); it is kept
// for a moment so the page can say so and point at the test that ran, instead
// of a click that appears to do nothing (NEW_508, issue #20).
type Repeat struct {
	RequestID  string    `json:"request_id"`
	BuildID    string    `json:"build_id,omitempty"`
	AssetID    string    `json:"asset_id,omitempty"`
	Revision   int       `json:"revision,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
	Until      time.Time `json:"until"`
}

// Intent is the file.
type Intent struct {
	SchemaVersion string            `json:"schema_version"`
	Request       *aub.LeakTestLink `json:"request,omitempty"`
	ReceivedAt    time.Time         `json:"received_at,omitempty"`
	ExpiresAt     time.Time         `json:"expires_at,omitempty"`
	// Retired are the requests that were built, or replaced by a newer click
	// (NEW_380). One press is one job: the same link delivered again — a second
	// click on Open again, a link the operating system held back, an old tab —
	// must not put a used request back under a Build button, and must not put an
	// older request over a newer one. Optional, so an older Companion reads this
	// file as before. A dismissed request is not here: a person may ask again.
	Retired []Retired `json:"retired,omitempty"`
	// Repeat is the newest delivery of a link that was already used.
	Repeat *Repeat `json:"repeat,omitempty"`
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
// an older one: the editor asks about the revision it has open now. A link whose
// request was already built or replaced records nothing, and that is not an
// error: the delivery is simply old.
func Receive(path string, link aub.LeakTestLink, now time.Time) error {
	return ReceiveFrom(path, link, "", now)
}

// ReceiveFrom is Receive, recording the version of the program that received
// the link from the operating system.
func ReceiveFrom(path string, link aub.LeakTestLink, launchedBy string, now time.Time) error {
	if _, err := aub.ParseLeakTestLink(link.Link()); err != nil {
		return fmt.Errorf("leakintent: %w", err)
	}

	return update(path, now, func(intent *Intent) {
		if link.RequestID != "" && intent.retired(link.RequestID) {
			// Old. Nothing becomes pending; a USED request is remembered for a
			// moment so the page can show the test that already ran.
			if item := intent.find(link.RequestID); item != nil && item.Used {
				intent.Repeat = &Repeat{
					RequestID: link.RequestID, BuildID: item.BuildID, AssetID: link.AssetID, Revision: link.Revision,
					ReceivedAt: now, Until: now.Add(RepeatFor),
				}
			}
			return
		}
		if intent.Request != nil && intent.Request.RequestID != link.RequestID {
			intent.retire(intent.Request.RequestID, now)
		}
		request := link
		intent.Request, intent.ReceivedAt, intent.ExpiresAt = &request, now, now.Add(Lifetime)
		intent.LaunchedByVersion = launchedBy
	})
}

func (i *Intent) retired(requestID string) bool {
	for _, item := range i.Retired {
		if item.RequestID == requestID {
			return true
		}
	}
	return false
}

func (i *Intent) find(requestID string) *Retired {
	for index := range i.Retired {
		if i.Retired[index].RequestID == requestID {
			return &i.Retired[index]
		}
	}
	return nil
}

func (i *Intent) retire(requestID string, now time.Time) {
	if requestID == "" || i.retired(requestID) {
		return
	}
	i.Retired = append(i.Retired, Retired{RequestID: requestID, Until: now.Add(RetiredFor)})
	if len(i.Retired) > retiredLimit {
		i.Retired = i.Retired[len(i.Retired)-retiredLimit:]
	}
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
		intent.retire(requestID, now)
		intent.Request, intent.ReceivedAt, intent.ExpiresAt = nil, time.Time{}, time.Time{}
	})
	return consumed, err
}

// Used marks a consumed request as the one a build was started for, and —
// once the build has an id — which build, so a later delivery of the same
// link can point at it. Consume alone does not say this: a request a person
// dismissed is consumed too, and that one was never run. It never retires
// anything: a request that is not already retired is left alone.
func Used(path, requestID, buildID string, now time.Time) error {
	if requestID == "" {
		return nil
	}
	return update(path, now, func(intent *Intent) {
		item := intent.find(requestID)
		if item == nil {
			return
		}
		item.Used = true
		if buildID != "" {
			item.BuildID = buildID
			if intent.Repeat != nil && intent.Repeat.RequestID == requestID {
				intent.Repeat.BuildID = buildID
			}
		}
	})
}

// Repeated is the used link that was delivered again a moment ago, or nil.
func Repeated(path string, now time.Time) (*Repeat, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var repeat *Repeat
	err := update(path, now, func(intent *Intent) {
		if intent.Repeat != nil {
			copy := *intent.Repeat
			repeat = &copy
		}
	})
	return repeat, err
}

// DismissRepeat forgets the repeat a person has read. Only the one named: a
// newer one that arrived meanwhile stays.
func DismissRepeat(path, requestID string, now time.Time) error {
	return update(path, now, func(intent *Intent) {
		if intent.Repeat != nil && intent.Repeat.RequestID == requestID {
			intent.Repeat = nil
		}
	})
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
	if intent.Repeat != nil && !now.Before(intent.Repeat.Until) {
		intent.Repeat = nil
	}
	kept := intent.Retired[:0]
	for _, item := range intent.Retired {
		if now.Before(item.Until) {
			kept = append(kept, item)
		}
	}
	intent.Retired = kept
	intent.SchemaVersion = SchemaVersion
}

func save(path string, intent Intent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if intent.Request == nil && len(intent.Retired) == 0 {
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
