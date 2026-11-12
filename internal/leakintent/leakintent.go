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
// [Receive] only records. The page lists the requests with [List], resolves
// each against AUB itself, prepares one, and a person presses Build; a
// dismissed or expired request is simply gone, and every other request stays
// pending (NEW_502, issue #35: a second link no longer replaces the first).
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

// SchemaVersion versions the file. 2.0 (NEW_502, issue #35) holds a LIST of
// pending requests: a second link of the same kind is kept beside the first
// instead of retiring it.
const SchemaVersion = "aucom.leak-test-intent/2.0"

// FileName is the file, in the configuration directory. It is not the 1.0
// file's name, and that is the version rule: a Companion older than 2.0 reads
// and writes only [LegacyFileName], so it can never overwrite the list with its
// single request. What an older program leaves there — its request, its used
// and retired ids, its repeat — is imported on the next read and the legacy
// file removed, so a used link stays used across the upgrade.
const FileName = "leak-test-requests.json"

// LegacyFileName and LegacySchemaVersion are the 1.0 file: one request.
const (
	LegacyFileName      = "leak-test-intent.json"
	LegacySchemaVersion = "aucom.leak-test-intent/1.0"
)

// PendingLimit bounds the list however many links arrive; past it the oldest
// pending request is dropped (not retired: it was never built, and a person
// may open it again).
const PendingLimit = 16

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

// Pending is one request the editor asked for that nobody has built or
// dismissed yet.
type Pending struct {
	Request    aub.LeakTestLink `json:"request"`
	ReceivedAt time.Time        `json:"received_at"`
	ExpiresAt  time.Time        `json:"expires_at"`
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
	// Retired are the requests that were built or dismissed (NEW_380). One
	// press is one job: the same link delivered again — a second click on Open
	// again, a link the operating system held back, an old tab — must not put a
	// used request back under a Build button.
	Retired []Retired `json:"retired,omitempty"`
	// Repeat is the newest delivery of a link that was already used.
	Repeat *Repeat `json:"repeat,omitempty"`
}

// legacyIntent is the 1.0 file, read only to be imported.
type legacyIntent struct {
	SchemaVersion     string            `json:"schema_version"`
	Request           *aub.LeakTestLink `json:"request,omitempty"`
	ReceivedAt        time.Time         `json:"received_at,omitempty"`
	ExpiresAt         time.Time         `json:"expires_at,omitempty"`
	Retired           []Retired         `json:"retired,omitempty"`
	Repeat            *Repeat           `json:"repeat,omitempty"`
	LaunchedByVersion string            `json:"launched_by_version,omitempty"`
}

// Path is the intent file for a configuration directory.
func Path(configDir string) string { return filepath.Join(configDir, FileName) }

func legacyPath(path string) string { return filepath.Join(filepath.Dir(path), LegacyFileName) }

func options() lockfile.Options {
	return lockfile.Options{Program: "auto-pigeon-companion", Timeout: 10 * time.Second}
}

// Receive records a link the operating system delivered. A different request
// is ADDED beside any already pending; the same request delivered again only
// renews its lifetime. A link whose request was already built or dismissed
// records nothing, and that is not an error: the delivery is simply old.
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
		intent.add(Pending{Request: link, ReceivedAt: now, ExpiresAt: now.Add(Lifetime), LaunchedByVersion: launchedBy})
	})
}

// add puts one request in the list: in place of the same id, else at the end.
// The same id naming other bytes replaces the first under that one id: it is
// what the editor asks about now, and the page checks every field again.
//
// A link with no request id (an editor older than request ids) cannot be told
// apart from another one, so it replaces any other id-less request, as every
// link did before NEW_502.
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
func Newest(path string, now time.Time) (*aub.LeakTestLink, time.Time, error) {
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

// Dismiss forgets every pending request: the older page's dismiss, which named
// none. A page that names one uses [Consume].
func Dismiss(path string, now time.Time) error {
	return update(path, now, func(intent *Intent) {
		intent.Requests = nil
	})
}

// DismissWithoutID forgets the pending requests that carry no request id —
// a malformed or pre-request-id link (AUCOM #22, NEW_503). Nothing can be
// built for one and no result can be returned for it, so its notice is
// dismissed by this, and every request that HAS an id stays.
func DismissWithoutID(path string, now time.Time) error {
	return update(path, now, func(intent *Intent) {
		kept := intent.Requests[:0]
		for _, item := range intent.Requests {
			if item.Request.RequestID != "" {
				kept = append(kept, item)
			}
		}
		intent.Requests = kept
	})
}

// Consume removes only the named request — a reviewed build about to start, or
// a dismissal — and retires its id. Every other pending request stays.
func Consume(path, requestID string, now time.Time) (*aub.LeakTestLink, error) {
	var consumed *aub.LeakTestLink
	err := update(path, now, func(intent *Intent) {
		index := intent.index(requestID)
		if index < 0 {
			return
		}
		copy := intent.Requests[index].Request
		consumed = &copy
		intent.retire(requestID, now)
		intent.Requests = append(intent.Requests[:index], intent.Requests[index+1:]...)
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
		legacy := legacyPath(path)
		if _, err := os.Stat(legacy); errors.Is(err, os.ErrNotExist) {
			// A 1.0 file written after this look is imported on the next read.
			return publish(path, "", now, mutate)
		}
		// The one lock order: this file's lock, then the 1.0 file's. The 1.0
		// lock — the one an older program takes before it writes — is held
		// from the read of its file, through the list holding that record
		// being on disk, until the 1.0 file is gone (NEW_502A). Released any
		// earlier, an older program could write a second request between the
		// read and the removal, and the removal would delete it unread.
		return lockfile.With(legacy, options(), func() error {
			return publish(path, legacy, now, mutate)
		})
	})
}

// publish is one read-modify-write of the list, with the 1.0 file at legacy
// imported into it when legacy is not "". Its caller holds every lock it needs.
// The 1.0 file is removed only once the list holding its record is on disk: a
// failed save leaves both files as they were, and the next read imports the
// 1.0 record again.
func publish(path, legacy string, now time.Time, mutate func(*Intent)) error {
	intent := load(path)
	imported := legacy != "" && importLegacy(legacy, &intent)
	afterLegacyRead()
	prune(&intent, now)
	mutate(&intent)
	prune(&intent, now)

	if err := save(path, intent); err != nil {
		return err
	}
	if imported {
		if err := os.Remove(legacy); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// afterLegacyRead runs between the read of a 1.0 file and its removal. Only a
// test sets it, to put an older program's write in exactly that gap.
var afterLegacyRead = func() {}

// importLegacy moves a 1.0 file into the list: its pending request (unless
// that id is already retired here), its retired and used ids, and its repeat.
// Its caller holds the 1.0 file's own lock, the one an older program takes,
// and keeps it until the 1.0 file is removed. It reports whether there was a
// 1.0 file to remove. A file that is not 1.0 is left.
func importLegacy(legacy string, intent *Intent) bool {
	raw, err := fsshare.ReadFile(legacy)
	if err != nil {
		return false
	}
	var old legacyIntent
	if json.Unmarshal(raw, &old) != nil || old.SchemaVersion != LegacySchemaVersion {
		return false
	}
	for _, item := range old.Retired {
		if item.RequestID == "" {
			continue
		}
		if held := intent.find(item.RequestID); held != nil {
			// Used wins over merely retired, and the build it names is kept.
			held.Used = held.Used || item.Used
			if held.BuildID == "" {
				held.BuildID = item.BuildID
			}
			if held.Until.Before(item.Until) {
				held.Until = item.Until
			}
			continue
		}
		intent.Retired = append(intent.Retired, item)
	}
	if len(intent.Retired) > retiredLimit {
		intent.Retired = intent.Retired[len(intent.Retired)-retiredLimit:]
	}
	if old.Repeat != nil && (intent.Repeat == nil || intent.Repeat.ReceivedAt.Before(old.Repeat.ReceivedAt)) {
		copy := *old.Repeat
		intent.Repeat = &copy
	}
	if old.Request != nil && !intent.retired(old.Request.RequestID) {
		item := Pending{Request: *old.Request, ReceivedAt: old.ReceivedAt, ExpiresAt: old.ExpiresAt,
			LaunchedByVersion: old.LaunchedByVersion}
		if valid(item) {
			if index := intent.index(item.Request.RequestID); index < 0 || intent.Requests[index].ReceivedAt.Before(item.ReceivedAt) {
				intent.add(item)
			}
		}
	}
	return true
}

// valid re-validates what is on disk rather than trusting it: the file is the
// user's own, but a request that no longer parses is not one to show, and a
// lifetime longer than this program grants is not one to keep.
func valid(item Pending) bool {
	if _, err := aub.ParseLeakTestLink(item.Request.Link()); err != nil {
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
		if valid(item) && !seen[item.Request.RequestID] && !intent.retired(item.Request.RequestID) {
			seen[item.Request.RequestID] = true
			kept = append(kept, item)
		}
	}
	intent.Requests = kept

	return intent
}

func prune(intent *Intent, now time.Time) {
	pending := intent.Requests[:0]
	for _, item := range intent.Requests {
		if now.Before(item.ExpiresAt) {
			pending = append(pending, item)
		}
	}
	intent.Requests = pending
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

// replace publishes a saved list. A test swaps it to fail a publication.
var replace = fsshare.Replace

func save(path string, intent Intent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if len(intent.Requests) == 0 && len(intent.Retired) == 0 && intent.Repeat == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}
	raw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".leak-test-requests-*")
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
	if err = replace(temporary.Name(), path); err != nil {
		return fmt.Errorf("leakintent: %w", err)
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
