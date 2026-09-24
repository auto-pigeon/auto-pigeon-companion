// Package joinintent holds the ONE thing an `autopigeon://join/…` link leaves on
// this machine between the moment the operating system hands it over and the
// moment the Companion's page shows the game — `AUB/AUG/AUCOM/AUT 244F`.
//
// # Why a file, and why so little in it
//
// A link arrives in a new process (`companion game open <link>`), and the page
// that has to show it is served by another one (`companion serve`). Something has
// to cross between them. That something is a file in the configuration directory,
// written by one writer at a time under internal/lockfile, readable only by this
// user, and holding the least that works:
//
//   - the opaque ticket id, for at most the ticket's own two-minute life, until the
//     server redeems it — then it is removed;
//   - the game id it resolved to, and its title, for ten minutes, so a page reload
//     still knows which game the person clicked;
//   - a digest of each redeemed ticket, so the same link delivered twice by the
//     operating system maps to the same game without spending anything again.
//
// No endpoint, no map, no account, no session. The ticket is never logged, never
// rendered, and never kept past its expiry: every read prunes.
//
// # Nothing here launches or redeems on arrival
//
// [Receive] only records. Redeeming is [Take], called by the server when the page
// asks, and it never starts anything: the page then shows the readiness for the
// game, and a join still needs a fresh review and an approval.
package joinintent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
)

// SchemaVersion versions the file.
const SchemaVersion = "aucom.join-intent/1.0"

// FileName is the file, in the configuration directory.
const FileName = "join-intent.json"

// Lifetimes.
const (
	// TicketLifetime is AUB's join-ticket TTL: a ticket kept longer is kept past
	// the only moment it could have been worth anything.
	TicketLifetime = 2 * time.Minute
	// IntentLifetime is how long "the person clicked this game" is remembered.
	IntentLifetime = 10 * time.Minute
	// MaxRedeemed bounds the dedupe list.
	MaxRedeemed = 16
)

// Redeemed is one ticket this machine already spent, by digest.
type Redeemed struct {
	Digest    string    `json:"ticket_digest"`
	GameID    string    `json:"game_id"`
	Title     string    `json:"title,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Intent is the file.
type Intent struct {
	SchemaVersion string `json:"schema_version"`
	// TicketID is present only between Receive and Take, and never past
	// TicketExpiresAt.
	TicketID        string    `json:"ticket_id,omitempty"`
	TicketExpiresAt time.Time `json:"ticket_expires_at,omitempty"`

	GameID    string    `json:"game_id,omitempty"`
	Title     string    `json:"title,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// Problem is why the last link could not be redeemed, in words for a person.
	Problem string `json:"problem,omitempty"`

	Redeemed []Redeemed `json:"redeemed,omitempty"`
}

// Pending is what the page is told.
type Pending struct {
	GameID  string `json:"game_id,omitempty"`
	Title   string `json:"title,omitempty"`
	Waiting bool   `json:"waiting,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// Path is the intent file for a configuration directory.
func Path(configDir string) string { return filepath.Join(configDir, FileName) }

func options() lockfile.Options {
	return lockfile.Options{Program: "auto-pigeon-companion", Timeout: 10 * time.Second}
}

// Digest is a ticket's dedupe key. A digest rather than the id so the list of
// spent tickets is not itself a list of tickets.
func Digest(ticketID string) string {
	sum := sha256.Sum256([]byte("aucom-join-ticket\x00" + ticketID))

	return hex.EncodeToString(sum[:])
}

// Receive records a link the operating system delivered. It makes no network
// request and starts nothing.
func Receive(path, ticketID string, now time.Time) error {
	if ticketID == "" {
		return errors.New("joinintent: no ticket")
	}

	return update(path, now, func(intent *Intent) error {
		digest := Digest(ticketID)
		for _, spent := range intent.Redeemed {
			if spent.Digest == digest {
				// The same link, delivered again: the same game, nothing spent.
				intent.TicketID, intent.TicketExpiresAt, intent.Problem = "", time.Time{}, ""
				intent.GameID, intent.Title = spent.GameID, spent.Title
				intent.ExpiresAt = now.Add(IntentLifetime)

				return nil
			}
		}
		intent.TicketID = ticketID
		intent.TicketExpiresAt = now.Add(TicketLifetime)
		intent.GameID, intent.Title, intent.Problem = "", "", ""
		intent.ExpiresAt = now.Add(IntentLifetime)

		return nil
	})
}

// Redeemer spends a ticket and names the game. A *transient* error — the network,
// not a refusal — should be returned wrapped in ErrTransient, so the ticket is
// kept for a retry within its life.
type Redeemer func(ticketID string) (gameID, title string, err error)

// ErrTransient marks a redemption failure worth retrying.
var ErrTransient = errors.New("joinintent: the link could not be checked right now")

// Take redeems a pending ticket at most once and reports the pending game.
func Take(path string, redeem Redeemer, now time.Time) (Pending, error) {
	var pending Pending
	err := update(path, now, func(intent *Intent) error {
		if intent.TicketID != "" {
			ticket := intent.TicketID
			gameID, title, err := redeem(ticket)
			switch {
			case err == nil:
				intent.TicketID, intent.TicketExpiresAt, intent.Problem = "", time.Time{}, ""
				intent.GameID, intent.Title = gameID, title
				intent.ExpiresAt = now.Add(IntentLifetime)
				intent.Redeemed = append(intent.Redeemed, Redeemed{Digest: Digest(ticket), GameID: gameID,
					Title: title, ExpiresAt: now.Add(IntentLifetime)})
				if len(intent.Redeemed) > MaxRedeemed {
					intent.Redeemed = intent.Redeemed[len(intent.Redeemed)-MaxRedeemed:]
				}
			case errors.Is(err, ErrTransient):
				pending.Waiting = true
				intent.Problem = "The link could not be checked yet. The Companion will try again while it is valid."
			default:
				// Spent or refused. A one-use ticket is not kept for a second try.
				intent.TicketID, intent.TicketExpiresAt = "", time.Time{}
				intent.Problem = err.Error()
			}
		}
		pending.GameID, pending.Title, pending.Problem = intent.GameID, intent.Title, intent.Problem

		return nil
	})

	return pending, err
}

// Dismiss forgets the pending game (not the dedupe list).
func Dismiss(path string, now time.Time) error {
	return update(path, now, func(intent *Intent) error {
		intent.TicketID, intent.TicketExpiresAt = "", time.Time{}
		intent.GameID, intent.Title, intent.Problem = "", "", ""
		intent.ExpiresAt = time.Time{}

		return nil
	})
}

// Prune removes everything expired and nothing else. Called by the server when it
// starts, so a ticket left by a handler whose server never came up does not
// outlive its TTL by more than one start.
func Prune(path string, now time.Time) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return update(path, now, func(*Intent) error { return nil })
}

func update(path string, now time.Time, mutate func(*Intent) error) error {
	return lockfile.With(path, options(), func() error {
		intent := load(path)
		prune(&intent, now)
		if err := mutate(&intent); err != nil {
			return err
		}
		prune(&intent, now)

		return save(path, intent)
	})
}

func load(path string) Intent {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Intent{SchemaVersion: SchemaVersion}
	}
	var intent Intent
	if json.Unmarshal(raw, &intent) != nil || intent.SchemaVersion != SchemaVersion {
		// An unreadable intent is a lost click, not a fault: start again.
		return Intent{SchemaVersion: SchemaVersion}
	}

	return intent
}

func prune(intent *Intent, now time.Time) {
	if intent.TicketID != "" && !now.Before(intent.TicketExpiresAt) {
		intent.TicketID, intent.TicketExpiresAt = "", time.Time{}
		if intent.GameID == "" {
			intent.Problem = "The link expired before it could be opened. Open the game from Games instead."
		}
	}
	if !intent.ExpiresAt.IsZero() && !now.Before(intent.ExpiresAt) {
		intent.GameID, intent.Title, intent.Problem = "", "", ""
		intent.ExpiresAt = time.Time{}
	}
	kept := intent.Redeemed[:0]
	for _, spent := range intent.Redeemed {
		if now.Before(spent.ExpiresAt) {
			kept = append(kept, spent)
		}
	}
	intent.Redeemed = kept
	intent.SchemaVersion = SchemaVersion
}

func save(path string, intent Intent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if intent.TicketID == "" && intent.GameID == "" && intent.Problem == "" && len(intent.Redeemed) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}
	raw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".join-intent-*")
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
	if err = os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("joinintent: %w", err)
	}

	return nil
}
