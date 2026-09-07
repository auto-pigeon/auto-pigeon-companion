package hostgame_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/hostgame"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// fakeBackend is AUB, without one.
//
// The lifecycle's interesting states are a beat that is REFUSED and a stop that
// arrives with a particular reason, and neither is something a live server
// produces on demand. Everything it records is under a mutex because the beat
// loop is a goroutine and the assertions are not.
type fakeBackend struct {
	mu sync.Mutex

	registered []aub.HostedGameRegistration
	beats      int
	beatBodies []aub.HeartbeatBody
	stops      []string

	// beatErr, when set, is returned from the nth beat onward.
	beatErrAfter int
	beatErr      error

	game aub.HostedGame
}

func newBackend() *fakeBackend {
	return &fakeBackend{
		game: aub.HostedGame{
			ID:            "gme1",
			Title:         "Friday deathmatch",
			State:         "live",
			Reason:        "running",
			HeartbeatSecs: 30,
		},
	}
}

func (f *fakeBackend) PreviewHostedGame(context.Context, aub.HostedGameRegistration) (
	aub.HostedGamePreview, error,
) {
	return aub.HostedGamePreview{Visibility: "public", Audience: "everybody"}, nil
}

func (f *fakeBackend) RegisterHostedGame(_ context.Context, r aub.HostedGameRegistration) (
	aub.HostedGameResult, error,
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered = append(f.registered, r)

	return aub.HostedGameResult{Game: f.game}, nil
}

func (f *fakeBackend) HeartbeatHostedGame(_ context.Context, _ string, body aub.HeartbeatBody) (
	aub.HostedGameResult, error,
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beats++
	f.beatBodies = append(f.beatBodies, body)
	if f.beatErr != nil && f.beats >= f.beatErrAfter {
		return aub.HostedGameResult{}, f.beatErr
	}

	return aub.HostedGameResult{Game: f.game}, nil
}

func (f *fakeBackend) StopHostedGame(_ context.Context, _ string, reason string) (
	aub.HostedGameResult, error,
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, reason)
	ended := f.game
	ended.State = "offline"
	ended.Reason = reason

	return aub.HostedGameResult{Game: ended}, nil
}

func (f *fakeBackend) counts() (beats int, stops []string, registered []aub.HostedGameRegistration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.beats, append([]string(nil), f.stops...),
		append([]aub.HostedGameRegistration(nil), f.registered...)
}

// fakeJobs is one supervised process whose state a test moves.
type fakeJobs struct {
	mu      sync.Mutex
	current *job.Job
	missing bool
}

func hostingJob(state job.State) *job.Job {
	return &job.Job{
		ID:          "job1",
		State:       state,
		SessionRole: profile.SessionListen,
	}
}

func (f *fakeJobs) Get(string) (*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing {
		return nil, errors.New("no such job")
	}

	return f.current, nil
}

func (f *fakeJobs) set(state job.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = hostingJob(state)
}

func advertiserFor(t *testing.T, backend *fakeBackend, jobs *fakeJobs) *hostgame.Advertiser {
	t.Helper()

	return hostgame.NewForTest(backend, jobs, time.Millisecond)
}

// A game is listed after its host has read the preview, and never before.
func TestAnUnconfirmedRegistrationIsRefusedBeforeAnyRequestIsMade(t *testing.T) {
	backend, jobs := newBackend(), &fakeJobs{current: hostingJob(job.Running)}
	advertiser := advertiserFor(t, backend, jobs)

	_, err := advertiser.Start(context.Background(),
		aub.HostedGameRegistration{Title: "x", MapID: "m", EngineRuntime: "quakespasm"},
		hostgame.StartOptions{JobID: "job1"})
	if !errors.Is(err, hostgame.ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
	if _, _, registered := backend.counts(); len(registered) != 0 {
		t.Fatalf("%d registrations were sent; the refusal must happen before the request", len(registered))
	}
}

// A job that is not a server is not a game people can join.
func TestAJobThatIsNotAServerCannotBeAdvertised(t *testing.T) {
	backend := newBackend()
	client := &job.Job{ID: "job1", State: job.Running, SessionRole: profile.SessionClient}
	jobs := &fakeJobs{current: client}
	advertiser := advertiserFor(t, backend, jobs)

	_, err := advertiser.Start(context.Background(),
		aub.HostedGameRegistration{ConfirmExposure: true}, hostgame.StartOptions{JobID: "job1"})
	if !errors.Is(err, hostgame.ErrNotHosting) {
		t.Fatalf("err = %v, want ErrNotHosting: advertising a single-player session would put an "+
			"address in a listing that nothing is listening on", err)
	}
}

// Every way a supervised job ends maps onto the word AUB has for it — and never
// onto the one AUB owns.
func TestEveryEndingIsReportedWithTheReasonItActuallyHad(t *testing.T) {
	for _, ending := range []struct {
		name  string
		state job.State
		want  string
	}{
		{"the user cancelled it", job.Cancelled, aub.ReasonHostStopped},
		{"the engine quit cleanly", job.Succeeded, aub.ReasonHostStopped},
		{"the process failed", job.Failed, aub.ReasonHostCrashed},
		{"the Companion was killed with it", job.Interrupted, aub.ReasonHostCrashed},
	} {
		t.Run(ending.name, func(t *testing.T) {
			backend, jobs := newBackend(), &fakeJobs{current: hostingJob(job.Running)}
			advertiser := advertiserFor(t, backend, jobs)

			if _, err := advertiser.Start(context.Background(),
				aub.HostedGameRegistration{ConfirmExposure: true},
				hostgame.StartOptions{JobID: "job1", PollInterval: time.Millisecond},
			); err != nil {
				t.Fatal(err)
			}
			jobs.set(ending.state)
			advertiser.Wait("gme1")

			_, stops, _ := backend.counts()
			if len(stops) != 1 || stops[0] != ending.want {
				t.Fatalf("stops = %v, want [%s]", stops, ending.want)
			}
			for _, stop := range stops {
				if stop == "heartbeat_missed" || stop == "superseded" {
					t.Fatalf("this program sent %q, which is a conclusion AUB draws from its own "+
						"clock; a client that could send it would be able to write a history that "+
						"did not happen", stop)
				}
			}
		})
	}
}

// A lease is renewed while the process is alive, and the beat carries a count
// only when the host can actually take one.
func TestTheLeaseIsRenewedWhileTheProcessIsAliveAndSaysNothingItCannotKnow(t *testing.T) {
	backend, jobs := newBackend(), &fakeJobs{current: hostingJob(job.Running)}
	advertiser := advertiserFor(t, backend, jobs)

	if _, err := advertiser.Start(context.Background(),
		aub.HostedGameRegistration{ConfirmExposure: true},
		hostgame.StartOptions{JobID: "job1", PollInterval: time.Millisecond},
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { beats, _, _ := backend.counts(); return beats >= 3 })
	jobs.set(job.Cancelled)
	advertiser.Wait("gme1")

	backend.mu.Lock()
	bodies := append([]aub.HeartbeatBody(nil), backend.beatBodies...)
	backend.mu.Unlock()
	for _, body := range bodies {
		if body.PlayersCurrent != nil || body.PlayersMax != nil {
			t.Fatalf("a beat reported an occupancy nobody counted: %+v", body)
		}
	}
}

// A host that CAN count is asked before each beat, and the answer travels.
func TestAHostThatCanCountIsAskedBeforeEachBeat(t *testing.T) {
	backend, jobs := newBackend(), &fakeJobs{current: hostingJob(job.Running)}
	advertiser := advertiserFor(t, backend, jobs)

	var asked int
	var mu sync.Mutex
	occupancy := func() (int, int, bool) {
		mu.Lock()
		defer mu.Unlock()
		asked++

		return 2, 8, true
	}
	if _, err := advertiser.Start(context.Background(),
		aub.HostedGameRegistration{ConfirmExposure: true},
		hostgame.StartOptions{JobID: "job1", PollInterval: time.Millisecond, Occupancy: occupancy},
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { beats, _, _ := backend.counts(); return beats >= 2 })
	jobs.set(job.Cancelled)
	advertiser.Wait("gme1")

	backend.mu.Lock()
	bodies := append([]aub.HeartbeatBody(nil), backend.beatBodies...)
	backend.mu.Unlock()
	if len(bodies) == 0 || bodies[0].PlayersCurrent == nil || *bodies[0].PlayersCurrent != 2 {
		t.Fatalf("the beat did not carry the count the host reported: %+v", bodies)
	}
}

// A refused beat ends the loop rather than spinning against a lease that has
// lapsed.
func TestARefusedBeatEndsTheLoop(t *testing.T) {
	backend, jobs := newBackend(), &fakeJobs{current: hostingJob(job.Running)}
	backend.beatErrAfter = 1
	backend.beatErr = errors.New("409 hosted_game_offline")
	advertiser := advertiserFor(t, backend, jobs)

	if _, err := advertiser.Start(context.Background(),
		aub.HostedGameRegistration{ConfirmExposure: true},
		hostgame.StartOptions{JobID: "job1", PollInterval: time.Millisecond},
	); err != nil {
		t.Fatal(err)
	}
	advertiser.Wait("gme1")

	beats, stops, _ := backend.counts()
	if beats != 1 {
		t.Fatalf("beats = %d, want 1: a beat AUB refused means the lease has lapsed, and "+
			"continuing to beat achieves nothing", beats)
	}
	if len(stops) != 0 {
		t.Fatalf("stops = %v; a lapsed lease has already ended and stopping it again is noise", stops)
	}
}

// Signing out ends every advertisement rather than leaving somebody else's
// listing showing a game that stopped.
func TestClosingEndsEveryAdvertisementWithAReason(t *testing.T) {
	backend, jobs := newBackend(), &fakeJobs{current: hostingJob(job.Running)}
	advertiser := advertiserFor(t, backend, jobs)

	if _, err := advertiser.Start(context.Background(),
		aub.HostedGameRegistration{ConfirmExposure: true},
		hostgame.StartOptions{JobID: "job1", PollInterval: time.Hour},
	); err != nil {
		t.Fatal(err)
	}
	advertiser.Close(context.Background(), aub.ReasonOwnerSignedOut)

	_, stops, _ := backend.counts()
	if len(stops) != 1 || stops[0] != aub.ReasonOwnerSignedOut {
		t.Fatalf("stops = %v, want [%s]", stops, aub.ReasonOwnerSignedOut)
	}
	if len(advertiser.Active()) != 0 {
		t.Fatal("an advertisement survived Close")
	}
}

// Stopping twice is one ending.
func TestStoppingTwiceIsOneEnding(t *testing.T) {
	backend, jobs := newBackend(), &fakeJobs{current: hostingJob(job.Running)}
	advertiser := advertiserFor(t, backend, jobs)

	if _, err := advertiser.Start(context.Background(),
		aub.HostedGameRegistration{ConfirmExposure: true},
		hostgame.StartOptions{JobID: "job1", PollInterval: time.Hour},
	); err != nil {
		t.Fatal(err)
	}
	if err := advertiser.Stop(context.Background(), "gme1", aub.ReasonHostStopped); err != nil {
		t.Fatal(err)
	}
	_, stops, _ := backend.counts()
	if len(stops) != 1 {
		t.Fatalf("stops = %v, want one", stops)
	}
}

// The identity a restart presents is stable, opaque, and carries no path.
func TestTheProcessIdentityIsStableOpaqueAndCarriesNoPath(t *testing.T) {
	first := hostgame.ProcessIdentity("/home/somebody/.config/auto-pigeon", "map1", "203.0.113.4", 26000)
	again := hostgame.ProcessIdentity("/home/somebody/.config/auto-pigeon", "map1", "203.0.113.4", 26000)
	if first != again {
		t.Fatal("the identity changed between two calls, so a restart could never reclaim anything")
	}
	for _, leak := range []string{"home", "somebody", "auto-pigeon", "map1", "203.0.113.4", "26000"} {
		if contains(first, leak) {
			t.Fatalf("the identity carries %q; it is sent to a server and stored beside a listing", leak)
		}
	}

	// A different game, a different address, or a different installation is a
	// different server the same person is entitled to run beside this one.
	for _, different := range []string{
		hostgame.ProcessIdentity("/home/somebody/.config/auto-pigeon", "map2", "203.0.113.4", 26000),
		hostgame.ProcessIdentity("/home/somebody/.config/auto-pigeon", "map1", "203.0.113.5", 26000),
		hostgame.ProcessIdentity("/home/somebody/.config/auto-pigeon", "map1", "203.0.113.4", 26001),
		hostgame.ProcessIdentity("/home/other/.config/auto-pigeon", "map1", "203.0.113.4", 26000),
	} {
		if different == first {
			t.Fatal("two different servers share one identity, so one would reclaim the other's lease")
		}
	}
}

// An endpoint is not guessed at.
func TestAPortIsNeverInvented(t *testing.T) {
	if _, _, err := hostgame.SplitEndpoint("203.0.113.4"); err == nil {
		t.Fatal("a bare address was accepted; a default port here is this program deciding which " +
			"port somebody's server is on, and being wrong looks exactly like a firewall problem")
	}
	host, port, err := hostgame.SplitEndpoint(" 203.0.113.4:26000 ")
	if err != nil || host != "203.0.113.4" || port != 26000 {
		t.Fatalf("host, port, err = %q, %d, %v", host, port, err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}

	return -1
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the beat loop")
}
