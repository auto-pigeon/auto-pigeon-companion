package joinintent_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/joinintent"
)

func TestALinkIsRecordedNotRedeemedAndRedeemedOnlyOnce(t *testing.T) {
	path := joinintent.Path(t.TempDir())
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	if err := joinintent.Receive(path, "tkt-secret", now); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	if !strings.Contains(string(raw), "tkt-secret") {
		t.Fatal("the ticket is not pending")
	}

	calls := 0
	redeem := func(ticket string) (string, string, error) {
		calls++
		if ticket != "tkt-secret" {
			t.Fatalf("redeemed %q", ticket)
		}

		return "game1", "Friday", nil
	}
	pending, err := joinintent.Take(path, redeem, now.Add(time.Second))
	if err != nil || pending.GameID != "game1" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	pending, _ = joinintent.Take(path, redeem, now.Add(2*time.Second))
	if calls != 1 || pending.GameID != "game1" {
		t.Fatalf("a reload redeemed again (calls %d) or lost the game (%+v)", calls, pending)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "tkt-secret") {
		t.Fatal("the ticket is still on disk after it was spent")
	}

	// The operating system delivers the same link twice.
	if err = joinintent.Receive(path, "tkt-secret", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	pending, _ = joinintent.Take(path, redeem, now.Add(4*time.Second))
	if calls != 1 || pending.GameID != "game1" {
		t.Fatalf("a duplicate delivery spent the ticket again: calls %d, %+v", calls, pending)
	}
}

func TestATicketNeverOutlivesItsLifetime(t *testing.T) {
	path := joinintent.Path(t.TempDir())
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	joinintent.Receive(path, "tkt-old", now)

	if err := joinintent.Prune(path, now.Add(joinintent.TicketLifetime)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "tkt-old") {
		t.Fatal("an expired ticket survived a prune")
	}
	pending, _ := joinintent.Take(path, func(string) (string, string, error) {
		t.Fatal("an expired ticket was redeemed")
		return "", "", nil
	}, now.Add(joinintent.TicketLifetime+time.Second))
	if pending.GameID != "" || pending.Problem == "" {
		t.Fatalf("pending = %+v; an expired link says so", pending)
	}
	if err := joinintent.Prune(path, now.Add(joinintent.IntentLifetime+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an empty intent file was left behind: %v", err)
	}
}

func TestARefusedTicketIsNotKeptButATransientFailureIs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, joinintent.FileName)
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	joinintent.Receive(path, "tkt-net", now)
	pending, _ := joinintent.Take(path, func(string) (string, string, error) {
		return "", "", joinintent.ErrTransient
	}, now.Add(time.Second))
	if !pending.Waiting {
		t.Fatalf("pending = %+v", pending)
	}
	calls := 0
	pending, _ = joinintent.Take(path, func(string) (string, string, error) {
		calls++
		return "", "", errors.New("That join link has already been used.")
	}, now.Add(2*time.Second))
	if calls != 1 || pending.Problem == "" || pending.GameID != "" {
		t.Fatalf("refused: calls %d, %+v", calls, pending)
	}
	joinintent.Take(path, func(string) (string, string, error) {
		calls++
		return "", "", nil
	}, now.Add(3*time.Second))
	if calls != 1 {
		t.Fatal("a refused one-use ticket was presented a second time")
	}
}
