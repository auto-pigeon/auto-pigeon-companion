package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// A. The first `received` is refused by a server that is away, the request
// stays pending, the server comes back: the SAME request's status reaches it,
// with no second link, no new request id and no restart (`NEW_307W1`).
func TestALostFirstAcknowledgementIsDeliveredOnceTheServerIsBack(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	link := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: strings.Repeat("a", 32)}
	if err := leakintent.Receive(leakintent.Path(dir), link, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	m.backend.mu.Lock()
	m.backend.failLeakStatus = 1
	m.backend.mu.Unlock()

	deadline := time.Now().Add(10 * time.Second)
	for {
		// The page's own watch, every two seconds in life.
		if status, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); status != http.StatusOK || body["request_id"] != link.RequestID {
			t.Fatalf("the watch: %d %v", status, body)
		}
		m.backend.mu.Lock()
		posts, accepted := m.backend.leakStatusPosts, append([]string(nil), m.backend.leakStatuses...)
		m.backend.mu.Unlock()
		if len(accepted) > 0 {
			if accepted[0] != link.RequestID+" received" || posts < 2 {
				t.Fatalf("AUB accepted %v after %d POST(s)", accepted, posts)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the refused `received` was never sent again: %d POST(s) arrived, none accepted", posts)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
