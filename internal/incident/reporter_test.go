package incident

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
)

// Canaries: things that exist on a failed job's record, or in the process,
// and must never reach an event.
const (
	canaryToken   = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJjYW5hcnkifQ.Y2FuYXJ5LXNpZ25hdHVyZQ"
	canaryHome    = "/home/canary-user/quake/id1"
	canaryArg     = "-canary-argument-7c1e"
	canaryEmail   = "canary.person@example.test"
	canaryProfile = "canary.private.profile"
	canaryError   = "exec " + canaryHome + "/qbsp " + canaryArg + " failed for " + canaryEmail
)

var canaries = []string{canaryToken, "canary-user", canaryArg, canaryEmail, canaryProfile, "qbsp"}

// store is a fake GlitchTip store endpoint that records what arrived.
type store struct {
	mu     sync.Mutex
	bodies []string
	auth   []string
	paths  []string
	status int
	hold   chan struct{}
}

func (s *store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.hold != nil {
		<-s.hold
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, string(body))
	s.auth = append(s.auth, r.Header.Get("X-Sentry-Auth"))
	s.paths = append(s.paths, r.URL.String())
	status := s.status
	s.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
}

func (s *store) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

func newStore(t *testing.T) (*store, string) {
	t.Helper()
	s := &store{}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	dsn := strings.Replace(server.URL, "http://", "http://publickey123@", 1) + "/42"
	return s, dsn
}

type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, fmt.Sprintf(format, args...))
}

func (l *lines) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.all, "\n")
}

// failedJob is a job record with every sensitive thing a real one carries.
func failedJob() *job.Job {
	code := 3
	return &job.Job{
		ID: "20260922T120000Z-0123456789ab", State: job.Failed,
		ProfileID: canaryProfile, ActionID: "compile", ProfileName: "Canary " + canaryEmail,
		Request: job.Request{
			ProfileID: canaryProfile, ActionID: "compile",
			Inputs:      map[string]string{"map": canaryHome + "/maps/e1m1.map"},
			Executables: map[string]string{"qbsp": canaryHome + "/qbsp"},
			Label:       "build for " + canaryEmail,
		},
		Command: &job.CommandPreview{
			Executable: canaryHome + "/qbsp", Args: []string{canaryArg, canaryHome + "/maps/e1m1.map"},
			WorkingDir: canaryHome, Shell: canaryHome + "/qbsp " + canaryArg,
			Env: []job.EnvEntry{{Name: "AUB_TOKEN", Value: canaryToken, Source: job.EnvFromDocument}},
		},
		Workspace: canaryHome + "/workspace", ExitCode: &code, Error: canaryError,
		CreatedAt: time.Now().Add(-2 * time.Second), StartedAt: time.Now().Add(-2 * time.Second), FinishedAt: time.Now(),
	}
}

func assertNoCanary(t *testing.T, where, text string) {
	t.Helper()
	for _, canary := range canaries {
		if strings.Contains(text, canary) {
			t.Errorf("%s carries %q:\n%s", where, canary, text)
		}
	}
}

func TestAFailedJobIsReportedWithKindAndOutcomeAndNothingElse(t *testing.T) {
	s, dsn := newStore(t)
	var log lines
	reporter := NewReporter(Config{DSN: dsn, Environment: EnvironmentTest, Release: "1.412"}, Options{Logf: log.logf})
	var raised Incident
	hook := JobHook(reporter, func(inc Incident) { raised = inc })

	correlation := "0123456789abcdef0123456789abcdef"
	hook(failedJob(), correlation)
	if !reporter.Flush(5 * time.Second) {
		t.Fatal("the event was never delivered")
	}
	bodies := s.received()
	if len(bodies) != 1 {
		t.Fatalf("the store received %d events, want 1", len(bodies))
	}
	assertNoCanary(t, "the event", bodies[0])
	assertNoCanary(t, "the local transcript", log.text())

	var event map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &event); err != nil {
		t.Fatal(err)
	}
	tags, _ := event["tags"].(map[string]any)
	for name, want := range map[string]string{
		"incident_code": CodeJobFailed, "component": "AUCOM", "subsystem": "job.tool",
		"operation": "job.exit_nonzero", "correlation_id": correlation, "correlation_origin": OriginInherited,
		"incident_id": raised.IncidentID,
	} {
		if tags[name] != want {
			t.Errorf("tag %s = %v, want %s", name, tags[name], want)
		}
	}
	if event["release"] != "1.412" || event["environment"] != EnvironmentTest {
		t.Errorf("release/environment = %v/%v", event["release"], event["environment"])
	}
	if event["message"] != "A supervised job exited with status 3." {
		t.Errorf("message = %v", event["message"])
	}
	for _, forbidden := range []string{"request", "user", "exception", "threads", "modules", "extra", "breadcrumbs"} {
		if _, present := event[forbidden]; present {
			t.Errorf("the event carries %q", forbidden)
		}
	}
	if event["server_name"] != "auto-pigeon-companion" {
		t.Errorf("server_name = %v; never the machine's hostname", event["server_name"])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.Contains(s.auth[0], "sentry_key=publickey123") || strings.Contains(s.paths[0], "publickey123") {
		t.Errorf("the key must travel in X-Sentry-Auth and never in the URL: auth=%q path=%q", s.auth[0], s.paths[0])
	}
	if s.paths[0] != "/api/42/store/" {
		t.Errorf("posted to %q, want the store endpoint", s.paths[0])
	}
}

func TestOnlyAFailedJobIsReported(t *testing.T) {
	var log lines
	reporter := NewReporter(Config{Release: "unknown", Environment: EnvironmentTest}, Options{Logf: log.logf})
	hook := JobHook(reporter, nil)
	for _, state := range []job.State{job.Succeeded, job.Cancelled, job.Interrupted} {
		j := failedJob()
		j.State = state
		hook(j, "")
	}
	if strings.Contains(log.text(), "raised") {
		t.Errorf("a job that did not fail was reported:\n%s", log.text())
	}
}

func TestAJobOutcomeIsClassifiedWithoutItsText(t *testing.T) {
	timed := failedJob()
	timed.TimedOut, timed.TimeoutSeconds = true, 60
	notStarted := failedJob()
	notStarted.ExitCode, notStarted.Command = nil, nil
	session := failedJob()
	session.SessionRole = "client"
	for _, c := range []struct {
		job                  *job.Job
		subsystem, operation string
	}{
		{timed, "job.tool", "job.timeout"},
		{notStarted, "job.tool", "job.start_failed"},
		{session, "job.engine", "job.exit_nonzero"},
	} {
		draft := JobDraft(c.job, "")
		if draft.Subsystem != c.subsystem || draft.Operation != c.operation {
			t.Errorf("got %s/%s, want %s/%s", draft.Subsystem, draft.Operation, c.subsystem, c.operation)
		}
		encoded, _ := json.Marshal(Event(New(draft, "unknown", EnvironmentTest, time.Now())))
		assertNoCanary(t, "the event for "+c.operation, string(encoded))
		if problems := Validate(New(draft, "unknown", EnvironmentTest, time.Now())); len(problems) > 0 {
			t.Errorf("%s: %v", c.operation, problems)
		}
	}
}

func TestACorrelationIdIsInheritedOrMintedAndMarked(t *testing.T) {
	inherited := New(Draft{Correlation: "0123456789abcdef0123456789abcdef"}, "unknown", EnvironmentTest, time.Now())
	if inherited.CorrelationOrigin != OriginInherited || inherited.CorrelationID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("a valid id was not inherited: %+v", inherited)
	}
	for _, bad := range []string{"", "NOT-HEX", "0123456789ABCDEF0123456789ABCDEF"} {
		minted := New(Draft{Correlation: bad}, "unknown", EnvironmentTest, time.Now())
		if minted.CorrelationOrigin != OriginMinted || !IsCorrelationID(minted.CorrelationID) || minted.CorrelationID == bad {
			t.Errorf("for %q: %+v; want a fresh id marked minted", bad, minted)
		}
	}
	if got := CorrelationFromHeader("  0123456789ABCDEF0123456789abcdef "); got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("the header was not normalised: %q", got)
	}
	if got := CorrelationFromHeader("0123; drop table"); got != "" {
		t.Errorf("a malformed header was forwarded: %q", got)
	}
}

// A send failure is degraded, logged once, never recursive, never fatal.
func TestASendFailureIsDegradedAndNeverRecursive(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	listener.Close()
	var log lines
	reporter := NewReporter(Config{DSN: "http://secretkey987@" + closed + "/1", Environment: EnvironmentTest, Release: "unknown"},
		Options{Logf: log.logf})
	for i := 0; i < 5; i++ {
		reporter.Capture(JobDraft(failedJob(), ""))
	}
	reporter.Flush(10 * time.Second)
	sent, _, failed := reporter.Counters()
	if sent != 0 || failed != 5 {
		t.Errorf("sent=%d failed=%d, want 0 and 5", sent, failed)
	}
	text := log.text()
	if n := strings.Count(text, CodeTelemetryUnavailable); n != 1 {
		t.Errorf("the backend's absence was logged %d times, want once:\n%s", n, text)
	}
	if n := strings.Count(text, "raised"); n != 5 {
		t.Errorf("%d incidents raised, want exactly the 5 captured — a send failure must not raise one:\n%s", n, text)
	}
	if strings.Contains(text, "secretkey987") || strings.Contains(text, closed) {
		t.Errorf("the log names the DSN key or the backend address:\n%s", text)
	}
}

func TestTheQueueIsBounded(t *testing.T) {
	s, dsn := newStore(t)
	s.hold = make(chan struct{})
	reporter := NewReporter(Config{DSN: dsn, Environment: EnvironmentTest, Release: "unknown"}, Options{})
	start := time.Now()
	for i := 0; i < QueueDepth*3; i++ {
		reporter.Capture(JobDraft(failedJob(), ""))
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Capture blocked on a stuck backend for %s", elapsed)
	}
	_, dropped, _ := reporter.Counters()
	if dropped < int64(QueueDepth) {
		t.Errorf("dropped %d of %d with the backend stuck; the queue is not bounded at %d", dropped, QueueDepth*3, QueueDepth)
	}
	close(s.hold)
	reporter.Flush(10 * time.Second)
}

func TestNothingIsSentWithoutADSNAndABadOneIsNeverPrinted(t *testing.T) {
	var log lines
	quiet := NewReporter(Config{Environment: EnvironmentTest, Release: "unknown"}, Options{Logf: log.logf})
	if quiet.Enabled() {
		t.Error("a reporter with no DSN says it sends")
	}
	if inc := quiet.Capture(JobDraft(failedJob(), "")); inc.IncidentID == "" {
		t.Error("an unconfigured reporter still builds and records the incident locally")
	}
	var nilReporter *Reporter
	nilReporter.Capture(Draft{})
	nilReporter.Flush(time.Millisecond)

	bad := NewReporter(Config{DSN: "not a dsn secretvalue", Release: "unknown"}, Options{Logf: log.logf})
	if bad.Enabled() || strings.Contains(log.text(), "secretvalue") {
		t.Errorf("a malformed DSN was used or printed:\n%s", log.text())
	}
}

func TestConfigComesFromTheEnvironmentThenTheFile(t *testing.T) {
	env := map[string]string{EnvDSN: "https://k@env.example.test/1", EnvEnvironment: "TEST"}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	cfg := LoadConfig(lookup, "https://k@file.example.test/2", "production", "1.77")
	if cfg.DSN != env[EnvDSN] || cfg.Environment != EnvironmentTest || cfg.Release != "1.77" {
		t.Errorf("environment did not win: %+v", cfg)
	}
	cfg = LoadConfig(func(string) (string, bool) { return "", false }, "https://k@file.example.test/2", "", "dev-build")
	if cfg.DSN != "https://k@file.example.test/2" || cfg.Release != "unknown" || cfg.Environment != EnvironmentDevelopment {
		t.Errorf("file fallback / release / default environment: %+v", cfg)
	}
	if cfg := LoadConfig(nil, "", "", "1.5"); cfg.Environment != EnvironmentProduction || cfg.Enabled() {
		t.Errorf("a stamped release with nothing configured: %+v", cfg)
	}
}

func TestReadinessFailuresAreReportedWithoutTheAddress(t *testing.T) {
	var log lines
	s, dsn := newStore(t)
	reporter := NewReporter(Config{DSN: dsn, Environment: EnvironmentTest, Release: "unknown"}, Options{Logf: log.logf})

	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := listener.Addr().String()
	listener.Close()
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer refused.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != aub.ReadinessPath || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		_, _ = w.Write([]byte(`{"code":200}`))
	}))
	defer healthy.Close()

	for _, c := range []struct {
		address, operation string
	}{
		{"http://" + closed, "readiness.unreachable"},
		{refused.URL, "readiness.refused"},
		{slow.URL, "readiness.timeout"},
	} {
		client, err := aub.New(c.address, nil)
		if err != nil {
			t.Fatal(err)
		}
		client.SetToken(canaryToken)
		inc, err := CheckReadiness(t.Context(), client, 300*time.Millisecond, reporter, "")
		if err == nil || inc.Code != CodeReadinessFailed || inc.Operation != c.operation || inc.Subsystem != "aub.link" {
			t.Errorf("%s: err=%v incident=%+v; want %s", c.address, err, inc, c.operation)
		}
	}
	client, _ := aub.New(healthy.URL, nil)
	client.SetToken(canaryToken)
	if inc, err := CheckReadiness(t.Context(), client, time.Second, reporter, ""); err != nil || inc.IncidentID != "" {
		t.Errorf("a healthy backend: err=%v incident=%+v", err, inc)
	}
	if inc, err := CheckReadiness(t.Context(), nil, time.Second, reporter, ""); err != nil || inc.IncidentID != "" {
		t.Error("no backend chosen is not a readiness failure")
	}

	reporter.Flush(5 * time.Second)
	bodies := strings.Join(s.received(), "\n")
	if strings.Count(bodies, CodeReadinessFailed) < 3 {
		t.Errorf("want three readiness events, got:\n%s", bodies)
	}
	for _, address := range []string{closed, strings.TrimPrefix(refused.URL, "http://"), strings.TrimPrefix(slow.URL, "http://"), "127.0.0.1"} {
		if strings.Contains(bodies, address) || strings.Contains(log.text(), address) {
			t.Errorf("a readiness report names the backend's address %s", address)
		}
	}
	assertNoCanary(t, "the readiness events", bodies)
}
