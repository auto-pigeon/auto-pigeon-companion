package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/buildintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// NEW_368: the server that serves an editor's request says which Companion it
// is — once per link delivery, whatever number of polls — and a newer program
// that only handed the link to this running one is named as that, not as the
// receiver.

func (m *machine) handoffAcksSoon(want int) []map[string]any {
	m.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.backend.mu.Lock()
		got := append([]map[string]any(nil), m.backend.handoffAcks...)
		m.backend.mu.Unlock()
		if len(got) >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// handoffAckPostsSettled is the number of posts once none has arrived for a
// moment.
func (m *machine) handoffAckPostsSettled() int {
	last := -1
	for quiet := 0; quiet < 10; {
		time.Sleep(30 * time.Millisecond)
		m.backend.mu.Lock()
		got := m.backend.handoffAckPosts
		m.backend.mu.Unlock()
		if got == last {
			quiet++
		} else {
			quiet = 0
		}
		last = got
	}
	return last
}

func TestTheServingCompanionAcknowledgesABuildRequestOncePerDeliveryAndNamesTheLauncher(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	link := m.editorBuildRequest(aub.BuildIntentBuildRun, "quake1", nil)
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	// A newer program received the link and handed it to this running one.
	if err := buildintent.ReceiveFrom(buildintent.Path(dir), link, "1.400", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Signed out: nothing can be posted, and nothing is.
	m.call(http.MethodGet, "/api/v1/build-request/pending", nil)
	if posts := m.handoffAckPostsSettled(); posts != 0 {
		t.Fatalf("signed out, %d acknowledgement(s) were posted", posts)
	}
	m.signIn()
	for range 3 {
		m.call(http.MethodGet, "/api/v1/build-request/pending?request_id="+link.RequestID, nil)
	}
	acks := m.handoffAcksSoon(1)
	if posts := m.handoffAckPostsSettled(); posts != 1 || len(acks) != 1 {
		t.Fatalf("three polls of one delivery posted %d acknowledgement(s)", posts)
	}
	ack := acks[0]
	who, _ := ack["companion"].(map[string]any)
	capabilities, _ := who["capabilities"].([]any)
	if ack["request_id"] != link.RequestID || ack["kind"] != "build" || ack["map_id"] != m.backend.asset.assetID ||
		ack["revision"] != float64(m.backend.asset.revision) || ack["content_sha256"] != m.backend.asset.digest() ||
		who["version"] != "test" || who["launched_by_version"] != "1.400" || who["protocol"] != float64(HandoffProtocol) ||
		len(capabilities) != 3 || len(who["instance_id"].(string)) != 16 {
		t.Fatalf("acknowledgement: %v", ack)
	}
	// The editor opens the link again: a new delivery, a new acknowledgement,
	// from the same running instance.
	time.Sleep(5 * time.Millisecond)
	if err := buildintent.ReceiveFrom(buildintent.Path(dir), link, "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	m.call(http.MethodGet, "/api/v1/build-request/pending?request_id="+link.RequestID, nil)
	acks = m.handoffAcksSoon(2)
	if len(acks) != 2 {
		t.Fatalf("a second delivery was not acknowledged: %d", len(acks))
	}
	second, _ := acks[1]["companion"].(map[string]any)
	if second["instance_id"] != who["instance_id"] || second["launched_by_version"] != nil {
		t.Fatalf("second acknowledgement: %v", acks[1])
	}
	// The relay is what it was: `received` once.
	if got := m.buildStatusesSettled(); strings.Join(got, "|") != link.RequestID+" received" {
		t.Fatalf("statuses: %v", got)
	}
}

func TestALeakRequestIsAcknowledgedAndAnAUBWithoutTheRouteIsNotAskedAgain(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	m.backend.mu.Lock()
	m.backend.handoffAcksAbsent = true
	m.backend.mu.Unlock()
	dir, err := m.server.configDir()
	if err != nil {
		t.Fatal(err)
	}
	link := aub.LeakTestLink{AssetID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
		ContentSHA256: m.backend.asset.digest(), RequestID: strings.Repeat("b", 32)}
	if err := leakintent.ReceiveFrom(leakintent.Path(dir), link, "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if status, body := m.call(http.MethodGet, "/api/v1/leak-test/request", nil); status != http.StatusOK || body["request_id"] != link.RequestID {
			t.Fatalf("the watch: %d %v", status, body)
		}
	}
	if posts := m.handoffAckPostsSettled(); posts != 1 {
		t.Fatalf("an AUB without the route was asked %d times for one delivery", posts)
	}
	// The relay does not depend on it.
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.backend.mu.Lock()
		statuses := append([]string(nil), m.backend.leakStatuses...)
		m.backend.mu.Unlock()
		if len(statuses) > 0 && statuses[0] == link.RequestID+" received" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the leak relay stalled: %v", statuses)
		}
		time.Sleep(20 * time.Millisecond)
	}

	m.backend.mu.Lock()
	m.backend.handoffAcksAbsent = false
	m.backend.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	if err := leakintent.ReceiveFrom(leakintent.Path(dir), link, "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
	acks := m.handoffAcksSoon(1)
	if len(acks) != 1 || acks[0]["kind"] != "leak_test" || acks[0]["map_id"] != link.AssetID {
		t.Fatalf("leak acknowledgement: %v", acks)
	}
}
