package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// Telling the editor which Companion received its request (NEW_368).
//
// The server that SERVES a build or leak request — this one, whichever program
// the link started — posts a handoff acknowledgement to AUB each time a link
// delivers the request: its own version, the handoff protocol, what it can do,
// this running instance, and the version of the program the link started when
// that one only handed the request over. The editor reads it to tell "received"
// from "received and cannot do it" from "nothing answered".
//
// Best effort, beside the relay and never in its way: an AUB from before this
// answers 404, which is settled, not retried; a network failure is tried again
// on the page's next poll. One post per delivery, whatever number of tabs ask.

// HandoffProtocol is the editor handoff protocol this Companion speaks.
// Version 1: build and leak requests as of NEW_366/NEW_340A, acknowledged.
const HandoffProtocol = 1

// handoffCapabilities is what this Companion can be asked for.
var handoffCapabilities = []string{"build", "build_run", "leak_test"}

type handoffAcker struct {
	mu       sync.Mutex
	settled  map[string]bool
	inflight map[string]bool
	once     sync.Once
	instance string
}

// instanceID names this running server, for as long as it runs.
func (a *handoffAcker) instanceID() string {
	a.once.Do(func() {
		var raw [8]byte
		if _, err := rand.Read(raw[:]); err == nil {
			a.instance = hex.EncodeToString(raw[:])
		}
	})
	return a.instance
}

// claim reserves one delivery for posting. False when it is posted, or being.
func (a *handoffAcker) claim(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settled == nil || len(a.settled) > 256 {
		a.settled = map[string]bool{}
	}
	if a.inflight == nil {
		a.inflight = map[string]bool{}
	}
	if a.settled[key] || a.inflight[key] {
		return false
	}
	a.inflight[key] = true
	return true
}

func (a *handoffAcker) finish(key string, settled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.inflight, key)
	if settled {
		a.settled[key] = true
	}
}

// acknowledgeHandoff posts, in the background, that this server received the
// request a link delivered at `received`. Nothing without a session: AUB would
// refuse it, and the editor already tells that case apart from silence by the
// request's own state.
func (s *Server) acknowledgeHandoff(kind, requestID, mapID string, revision int, sha string, received time.Time, launchedBy string) {
	client := s.aubClient()
	if client == nil || requestID == "" || received.IsZero() {
		return
	}
	token := client.Token()
	if token == "" || aub.TokenExpired(token, time.Now()) {
		return
	}
	instance := s.handoffs.instanceID()
	if instance == "" {
		return
	}
	key := kind + "/" + requestID + "@" + received.UTC().Format(time.RFC3339Nano)
	if !s.handoffs.claim(key) {
		return
	}
	ack := aub.HandoffAck{Kind: kind, MapID: mapID, Revision: revision, ContentSHA256: sha,
		Companion: aub.HandoffCompanion{Version: s.version, Protocol: HandoffProtocol,
			Capabilities: append([]string(nil), handoffCapabilities...), InstanceID: instance}}
	if launchedBy != "" && launchedBy != s.version {
		ack.Companion.LaunchedByVersion = launchedBy
	}
	if ack.Companion.Version == "" {
		ack.Companion.Version = "unknown"
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.PublishHandoffAckAs(ctx, token, requestID, ack)
		var refused *aub.APIError
		// Answered, or refused for good (an AUB without the route, another
		// revision): settled. Anything else is tried at the next poll.
		settled := err == nil || (errors.As(err, &refused) && refused.StatusCode >= 400 && refused.StatusCode < 500)
		if err != nil {
			s.logf("handoff acknowledgement for %s: %v", requestID, err)
		}
		s.handoffs.finish(key, settled)
	}()
}
