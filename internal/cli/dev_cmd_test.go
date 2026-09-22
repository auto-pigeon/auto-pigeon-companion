package cli

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// faultStore is a fake incident backend: it records what arrived.
type faultStore struct {
	mu     sync.Mutex
	events []map[string]any
}

func (f *faultStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var event map[string]any
	_ = json.Unmarshal(body, &event)
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func faultEnv(t *testing.T, vars map[string]string) (*Env, *strings.Builder, *strings.Builder) {
	t.Helper()
	env, _, _ := testEnv(t)
	var stdout, stderr strings.Builder
	env.Stdout, env.Stderr = &stdout, &stderr
	env.Lookenv = func(name string) (string, bool) {
		value, ok := vars[name]
		return value, ok
	}
	return env, &stdout, &stderr
}

func TestDevFaultsAreRefusedUnlessUnlocked(t *testing.T) {
	for _, value := range []string{"", "0", "true", "yes"} {
		vars := map[string]string{}
		if value != "" {
			vars[EnvE2EFaults] = value
		}
		env, stdout, stderr := faultEnv(t, vars)
		for _, fault := range []string{"job", "readiness"} {
			if code := Run(env, []string{"dev", "fault", fault}); code != 2 {
				t.Errorf("%s=%q: dev fault %s exited %d, want 2", EnvE2EFaults, value, fault, code)
			}
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), EnvE2EFaults) {
			t.Errorf("the refusal must name the switch and run nothing: stdout=%q stderr=%q", stdout, stderr)
		}
	}
}

// The job fault runs a genuinely failing supervised job, and its failure is
// reported through the same hook the real executor uses — with the
// correlation id the caller carried.
func TestTheJobFaultFailsARealJobAndReportsIt(t *testing.T) {
	store := &faultStore{}
	backend := httptest.NewServer(store)
	defer backend.Close()
	dsn := strings.Replace(backend.URL, "http://", "http://e2ekey@", 1) + "/9"
	env, stdout, stderr := faultEnv(t, map[string]string{
		EnvE2EFaults: "1", "AUCOM_INCIDENT_DSN": dsn, "AUCOM_INCIDENT_ENVIRONMENT": "test",
	})
	correlation := "abcdefabcdefabcdefabcdefabcdef12"
	if code := Run(env, []string{"dev", "fault", "job", "--correlation-id", correlation}); code != 0 {
		t.Fatalf("dev fault job = %d\nstderr: %s", code, stderr)
	}
	var report faultReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("the fault printed %q: %v", stdout, err)
	}
	if report.Code != "aucom.job_failed" || report.Operation != "job.exit_nonzero" || !report.Sent ||
		report.CorrelationID != correlation || report.Origin != "inherited" || report.JobID == "" {
		t.Errorf("fault report = %+v", report)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.events) != 1 {
		t.Fatalf("the backend received %d events, want 1", len(store.events))
	}
	encoded, _ := json.Marshal(store.events[0])
	tags, _ := store.events[0]["tags"].(map[string]any)
	if tags["incident_id"] != report.IncidentID || tags["correlation_id"] != correlation || tags["incident_code"] != "aucom.job_failed" {
		t.Errorf("the event's tags do not match the report: %v", tags)
	}
	// The failing program is this binary under some temporary directory: none
	// of that may travel.
	for _, forbidden := range []string{"dev fault exit", "aucom-fault-", "/tmp/", "e2ekey"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("the event carries %q: %s", forbidden, encoded)
		}
	}
}

func TestTheReadinessFaultRunsTheRealCheckAgainstAnUnreachableBackend(t *testing.T) {
	store := &faultStore{}
	backend := httptest.NewServer(store)
	defer backend.Close()
	dsn := strings.Replace(backend.URL, "http://", "http://e2ekey@", 1) + "/9"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unreachable := "http://" + listener.Addr().String()
	listener.Close()

	env, stdout, stderr := faultEnv(t, map[string]string{EnvE2EFaults: "1", "AUCOM_INCIDENT_DSN": dsn})
	if code := Run(env, []string{"dev", "fault", "readiness", "--aub", unreachable, "--bound", "2s"}); code != 0 {
		t.Fatalf("dev fault readiness = %d\nstderr: %s", code, stderr)
	}
	var report faultReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("the fault printed %q: %v", stdout, err)
	}
	if report.Code != "aucom.readiness_failed" || report.Origin != "minted" || !report.Sent {
		t.Errorf("fault report = %+v", report)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	encoded, _ := json.Marshal(store.events)
	if strings.Contains(string(encoded), listener.Addr().String()) {
		t.Errorf("the readiness event names the backend's address: %s", encoded)
	}

	// A backend that answers cannot produce the fault, and says so.
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer healthy.Close()
	env, _, stderr = faultEnv(t, map[string]string{EnvE2EFaults: "1"})
	if code := Run(env, []string{"dev", "fault", "readiness", "--aub", healthy.URL}); code != 1 {
		t.Errorf("a healthy backend = %d, want 1; stderr: %s", code, stderr)
	}
}
