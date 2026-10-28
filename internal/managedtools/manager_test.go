package managedtools_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
)

const (
	ericwRepo  = "auto-pigeon/ericw-tools"
	q3map2Repo = "auto-pigeon/q3map2"
)

// testHost is the Companion side, done for real where it matters: Verify runs
// every program and reads what it prints, as the executor's version check
// does; Activate and Busy record and answer.
type testHost struct {
	mu        sync.Mutex
	activated []string
	busy      []string
	failBind  error
	verifyErr error
	verified  int
}

func (h *testHost) Verify(ctx context.Context, tool managedtools.Tool, record managedtools.InstallRecord) error {
	h.mu.Lock()
	forced := h.verifyErr
	h.verified++
	h.mu.Unlock()
	if forced != nil {
		return forced
	}
	want := mt.Banner(tool.ID, record.Version)
	for name, path := range record.Programs {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		output, err := exec.CommandContext(ctx, path).CombinedOutput()
		cancel()
		if err != nil {
			return errors.New(name + ": " + err.Error())
		}
		if !strings.Contains(string(output), want) {
			return errors.New(name + " printed " + string(output) + ", not " + want)
		}
	}
	return nil
}

func (h *testHost) Activate(tool managedtools.Tool, record managedtools.InstallRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failBind != nil {
		return h.failBind
	}
	h.activated = append(h.activated, string(tool.ID)+" "+record.Version)
	return nil
}

func (h *testHost) Busy(managedtools.Tool, []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.busy...)
}

func (h *testHost) setBusy(busy ...string) {
	h.mu.Lock()
	h.busy = busy
	h.mu.Unlock()
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type rig struct {
	t      *testing.T
	server *mt.Server
	root   string
	state  string
	host   *testHost
	clock  *clock
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	return &rig{
		t: t, server: mt.NewServer(t),
		root:  filepath.Join(dir, "tools root with spaces — ünïcode"),
		state: filepath.Join(dir, "config", managedtools.StateFileName),
		host:  &testHost{},
		clock: &clock{now: time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC)},
	}
}

func (r *rig) manager(change ...func(*managedtools.Options)) *managedtools.Manager {
	r.t.Helper()
	probes, err := managedtools.BuiltinProbes()
	if err != nil {
		r.t.Fatal(err)
	}
	options := managedtools.Options{
		ToolsRoot: r.root, StatePath: r.state, Source: r.server.Source(r.t), Host: r.host,
		Probes: probes, Now: r.clock.Now, Rand: func() float64 { return 0.5 },
		FreeSpace: func(string) int64 { return 1 << 40 },
	}
	for _, c := range change {
		c(&options)
	}
	m, err := managedtools.New(options)
	if err != nil {
		r.t.Fatal(err)
	}
	return m
}

func (r *rig) publish(repo string, spec mt.Spec) *mt.Release {
	return r.server.Publish(repo, mt.Build(r.t, spec))
}

func (r *rig) publishBoth(ericw, q3 string) {
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: ericw, Prerelease: true})
	r.publish(q3map2Repo, mt.Spec{Tool: managedtools.Q3Map2, Version: q3, Prerelease: true})
}

func tool(status managedtools.Status, id managedtools.ToolID) managedtools.ToolStatus {
	for _, t := range status.Tools {
		if t.ID == id {
			return t
		}
	}
	return managedtools.ToolStatus{}
}

func archiveDownloads(s *mt.Server) int {
	n := 0
	s.Mutate(func() {
		for name, count := range s.Downloads {
			if strings.HasSuffix(name, ".zip") {
				n += count
			}
		}
	})
	return n
}

// M1: a fresh root installs each tool once, and a second launch is a no-op.
func TestAFreshRootInstallsEachToolOnceAndASecondLaunchDoesNothing(t *testing.T) {
	r := newRig(t)
	r.publishBoth("1.4536", "2.5.17n-ap.3031")
	first := r.manager()
	first.Step(context.Background())

	status := first.Status()
	for _, id := range []managedtools.ToolID{managedtools.EricW, managedtools.Q3Map2} {
		view := tool(status, id)
		if view.Phase != managedtools.PhaseReady || view.Active == nil {
			t.Fatalf("%s is %s (%s); want ready", id, view.Phase, view.Detail)
		}
		active := view.Active
		if active.ActualSHA256 != active.ExpectedSHA256 || active.Signed || active.Repository == "" || active.Commit == "" {
			t.Errorf("%s's install record is incomplete: %+v", id, active)
		}
		if !strings.HasPrefix(active.Dir, r.root) {
			t.Errorf("%s installed at %s, outside the tools root", id, active.Dir)
		}
		for name, path := range active.Programs {
			if !strings.HasPrefix(path, active.Dir) {
				t.Errorf("%s's %s is %s, outside its installation", id, name, path)
			}
		}
	}
	if status.SettingUp {
		t.Error("the setup card is still shown after both tools are ready")
	}
	if got := archiveDownloads(r.server); got != 2 {
		t.Fatalf("%d archive downloads; want one per tool", got)
	}
	if len(r.host.activated) != 2 {
		t.Fatalf("activations %v; want one per tool (EricW once, for Quake 1 and Quake II alike)", r.host.activated)
	}

	// Restarting a minute later: nothing is due, nothing is downloaded.
	r.clock.Advance(time.Minute)
	requests := r.server.Requests[ericwRepo]
	second := r.manager()
	second.Step(context.Background())
	if r.server.Requests[ericwRepo] != requests {
		t.Errorf("a restart a minute later asked GitHub again (%d -> %d requests)", requests, r.server.Requests[ericwRepo])
	}
	if got := archiveDownloads(r.server); got != 2 {
		t.Errorf("a second launch downloaded again (%d archive downloads)", got)
	}
}

// M1: two Companions starting together download each archive once.
func TestTwoCompanionsStartingTogetherDownloadEachArchiveOnce(t *testing.T) {
	r := newRig(t)
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4536", Prerelease: true})
	hold := make(chan struct{})
	r.server.Mutate(func() { r.server.Hold = hold })
	a, b := r.manager(), r.manager()
	done := make(chan struct{})
	go func() { a.Step(context.Background()); close(done) }()
	waitFor(t, func() bool { return archiveDownloads(r.server) == 1 })
	b.Step(context.Background()) // finds the check claimed, or the install lock held
	close(hold)
	<-done
	b.Step(context.Background())
	if got := archiveDownloads(r.server); got != 1 {
		t.Fatalf("%d archive downloads for two Companions; want 1", got)
	}
	if view := tool(b.Status(), managedtools.EricW); view.Phase != managedtools.PhaseReady {
		t.Fatalf("the second Companion sees %s (%s)", view.Phase, view.Detail)
	}
}

// M1: an interrupted download leaves nothing that looks installed, and the
// next attempt completes.
func TestAnInterruptedDownloadLeavesNothingAndTheNextAttemptCompletes(t *testing.T) {
	r := newRig(t)
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4536", Prerelease: true})
	hold := make(chan struct{})
	r.server.Mutate(func() { r.server.Hold = hold })
	m := r.manager()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Step(ctx); close(done) }()
	waitFor(t, func() bool { return archiveDownloads(r.server) == 1 })
	cancel()
	<-done
	if view := tool(m.Status(), managedtools.EricW); view.Phase == managedtools.PhaseReady {
		t.Fatal("an interrupted download reads ready")
	}
	entries, _ := os.ReadDir(filepath.Join(r.root, "ericw-tools"))
	for _, entry := range entries {
		if entry.Name() != ".staging" && !strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("an interrupted download left %s", entry.Name())
		}
	}
	r.server.Mutate(func() { r.server.Hold = nil })
	if err := m.Retry(managedtools.EricW); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Phase != managedtools.PhaseReady {
		t.Fatalf("after Retry: %s (%s)", view.Phase, view.Detail)
	}
}

// M1/M3: offline, a check fails quickly, says why, and backs off; Retry asks
// again at once; a working installation stays usable while offline.
func TestOfflineChecksBackOffAndRetryAsksAgain(t *testing.T) {
	r := newRig(t)
	r.server.Fail.Store(true)
	m := r.manager()
	started := time.Now()
	m.Step(context.Background())
	if time.Since(started) > 5*time.Second {
		t.Fatalf("an offline step took %s", time.Since(started))
	}
	view := tool(m.Status(), managedtools.EricW)
	if view.Phase != managedtools.PhaseFailed || !strings.Contains(view.Detail, "503") {
		t.Fatalf("offline reads %s (%q)", view.Phase, view.Detail)
	}
	if wait := view.Check.NextAttempt.Sub(r.clock.Now()); wait < time.Minute || wait > 2*time.Minute {
		t.Errorf("the first retry is in %s; want about a minute", wait)
	}
	requests := r.server.Requests[ericwRepo]
	m.Step(context.Background())
	if r.server.Requests[ericwRepo] != requests {
		t.Error("a step inside the backoff asked again")
	}
	r.server.Fail.Store(false)
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4536", Prerelease: true})
	_ = m.Retry(managedtools.EricW)
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Phase != managedtools.PhaseReady {
		t.Fatalf("after Retry online: %s (%s)", view.Phase, view.Detail)
	}
	// Offline again, an hour later: the installation is still ready.
	r.server.Fail.Store(true)
	r.clock.Advance(2 * time.Hour)
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Phase != managedtools.PhaseReady || view.Check.LastError == "" {
		t.Fatalf("offline with an installation: %s, last error %q", view.Phase, view.Check.LastError)
	}
}

// M3: about hourly, across restarts; a 304 is a successful unchanged check;
// a 429 is honoured.
func TestChecksAreHourlyAcrossRestartsAndHonour304And429(t *testing.T) {
	r := newRig(t)
	r.publishBoth("1.4536", "2.5.17n-ap.3031")
	r.manager().Step(context.Background())
	base := r.server.Requests[ericwRepo]
	for restart := 0; restart < 9; restart++ {
		r.clock.Advance(6 * time.Minute)
		r.manager().Step(context.Background()) // a restart every 6 minutes, 54 in all
	}
	if r.server.Requests[ericwRepo] != base {
		t.Fatalf("restarts within the hour asked GitHub %d more times", r.server.Requests[ericwRepo]-base)
	}
	r.clock.Advance(10 * time.Minute) // 64 minutes: past the hour and its jitter
	m := r.manager()
	m.Step(context.Background())
	if r.server.Requests[ericwRepo] != base+1 {
		t.Fatalf("after an hour: %d requests; want exactly one more", r.server.Requests[ericwRepo]-base)
	}
	view := tool(m.Status(), managedtools.EricW)
	if view.Check.LastSuccess != r.clock.Now() || view.Phase != managedtools.PhaseReady {
		t.Fatalf("a 304 is not a successful check: %+v", view.Check)
	}

	r.server.Mutate(func() { r.server.RateLimit = 30 * time.Minute })
	r.clock.Advance(2 * time.Hour)
	m.Step(context.Background())
	view = tool(m.Status(), managedtools.EricW)
	if !view.Check.RateLimitedUntil.Equal(r.clock.Now().Add(30*time.Minute)) || view.Check.NextAttempt.Before(view.Check.RateLimitedUntil) {
		t.Fatalf("Retry-After was not honoured: %+v", view.Check)
	}
	requests := r.server.Requests[ericwRepo]
	outcomes := m.CheckNow(context.Background())
	if r.server.Requests[ericwRepo] != requests || outcomes[0].RetryAt.IsZero() {
		t.Fatalf("Check for updates inside a rate limit asked anyway, or said nothing: %+v", outcomes[0])
	}
}

// M2: every reason a release is not installed, and an older eligible one
// still chosen when the newest is not.
func TestIneligibleReleasesAreRefusedForTheirReasonAndAnOlderOneIsChosen(t *testing.T) {
	other := "windows-amd64"
	if mt.ThisTarget() == other {
		other = "linux-amd64"
	}
	refusedBanner := "2.0.0-alpha11+auto-pigeon.1.4505"
	cases := []struct {
		name   string
		spec   mt.Spec
		mutate func(*mt.Release)
		reason managedtools.Reason
	}{
		{"no build for this platform", mt.Spec{Version: "1.4540", Targets: []string{other}}, nil, managedtools.ReasonNoTarget},
		{"unknown manifest schema", mt.Spec{Version: "1.4540", Schema: "auto-pigeon-ericw-release-manifest/2"}, nil, managedtools.ReasonManifestUnsupported},
		{"not qualified", mt.Spec{Version: "1.4540", Unqualified: true}, nil, managedtools.ReasonNotQualified},
		{"refused identity", mt.Spec{Version: "1.4540", Banner: refusedBanner}, nil, managedtools.ReasonRefusedIdentity},
		{"not an Auto-Pigeon build", mt.Spec{Version: "1.4540", Banner: "2.0.0-alpha11"}, nil, managedtools.ReasonNotAccepted},
		{"digest disagrees", mt.Spec{Version: "1.4540"}, func(release *mt.Release) {
			for name := range release.Files {
				if strings.HasSuffix(name, ".zip") {
					release.Digests = map[string]*string{name: mt.Digest([]byte("other bytes"))}
				}
			}
		}, managedtools.ReasonDigestDisagrees},
		{"no manifest", mt.Spec{Version: "1.4540"}, func(release *mt.Release) {
			delete(release.Files, managedtools.ManifestAssetName)
		}, managedtools.ReasonNoManifest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4537", Prerelease: true})
			c.spec.Tool, c.spec.Prerelease = managedtools.EricW, true
			release := mt.Build(t, c.spec)
			if c.mutate != nil {
				c.mutate(release)
			}
			r.server.Publish(ericwRepo, release)
			m := r.manager()
			m.Step(context.Background())
			view := tool(m.Status(), managedtools.EricW)
			if view.Active == nil || view.Active.Version != "1.4537" {
				t.Fatalf("the older eligible release was not chosen: %s %s", view.Phase, view.Detail)
			}
			if len(view.Newer) != 1 || view.Newer[0].Reason != c.reason {
				t.Fatalf("the newer release's verdict is %+v; want %s", view.Newer, c.reason)
			}
		})
	}
}

func TestDraftsMalformedTagsStableChannelAndTheBaselineAreRespected(t *testing.T) {
	r := newRig(t)
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4534", Prerelease: false}) // older than the baseline
	draft := mt.Build(t, mt.Spec{Tool: managedtools.EricW, Version: "1.4550"})
	draft.Draft = true
	r.server.Publish(ericwRepo, draft)
	malformed := mt.Build(t, mt.Spec{Tool: managedtools.EricW, Version: "1.4551"})
	malformed.Tag = "auto-pigeon-ericw-v1.45x1"
	r.server.Publish(ericwRepo, malformed)
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4539", Prerelease: true})

	stable := r.manager(func(o *managedtools.Options) { o.Channel = managedtools.ChannelStable })
	stable.Step(context.Background())
	view := tool(stable.Status(), managedtools.EricW)
	if view.Active != nil || view.Phase != managedtools.PhaseUnsupported {
		t.Fatalf("stable installed %v (%s): a prerelease or a release older than the baseline", view.Active, view.Phase)
	}
	if len(view.Newer) != 1 || view.Newer[0].Reason != managedtools.ReasonChannel {
		t.Fatalf("stable's verdicts: %+v", view.Newer)
	}

	beta := newRig(t)
	beta.server = r.server
	m := beta.manager()
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Active == nil || view.Active.Version != "1.4539" {
		t.Fatalf("beta did not install the newest prerelease: %+v", view)
	}
}

func TestReleaseVersionsAreOrderedAsNumbers(t *testing.T) {
	ericw, _ := managedtools.Find(managedtools.EricW)
	q3, _ := managedtools.Find(managedtools.Q3Map2)
	for _, c := range []struct {
		tool         managedtools.Tool
		older, newer string
	}{
		{ericw, "1.9", "1.10"}, {ericw, "1.4535", "1.4536"}, {ericw, "1.999", "2.0"},
		{q3, "2.5.17n-ap.3026", "2.5.17n-ap.3030"}, {q3, "2.5.17n-ap.999", "2.5.17n-ap.1000"},
		{q3, "2.5.17-ap.4000", "2.5.17n-ap.1"}, {q3, "2.5.17n-ap.3030", "2.5.18-ap.1"},
	} {
		a, err := c.tool.Version(c.older)
		if err != nil {
			t.Fatal(err)
		}
		b, err := c.tool.Version(c.newer)
		if err != nil {
			t.Fatal(err)
		}
		if a.Compare(b) >= 0 || b.Compare(a) <= 0 {
			t.Errorf("%s is not older than %s", c.older, c.newer)
		}
	}
	for _, bad := range []string{"1.4535-beta", "v1.4535", "1", "1..2", ""} {
		if _, err := ericw.Version(bad); err == nil {
			t.Errorf("%q parsed as an EricW version", bad)
		}
	}
}
