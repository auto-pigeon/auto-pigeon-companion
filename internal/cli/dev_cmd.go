package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/incident"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// `companion dev fault` — controlled faults for the end-to-end telemetry test.
//
// # What it is for
//
// The observability E2E has to see a real `aucom.job_failed` and a real
// `aucom.readiness_failed` arrive in the incident backend. A synthetic send
// would prove only that a POST works; these go through the SAME functions the
// Companion uses for real:
//
//   - `fault job` runs a genuinely failing supervised job — a real
//     [job.Service], a real profile document decoded, validated and authorized
//     through a real grant, a real process that really exits non-zero — and the
//     failure is reported by the same [incident.JobHook] `serve` and
//     `companion job` install.
//   - `fault readiness` runs [incident.CheckReadiness], the check `serve` runs
//     at start, against a configured backend the operator made unreachable.
//
// # Why it is refused by default
//
// It is not a feature. Unless AUCOM_E2E_FAULTS=1 is in the REAL environment
// (a `.env` cannot supply it: it is not one of config.DevEnvKeys), every `dev`
// subcommand is refused with exit status 2 and nothing runs. The one exception
// is `fault exit`, the failing program the job fault starts: it has no effect
// but its exit status, and the executor deliberately passes a job none of the
// Companion's environment, so it could never see the flag.

// EnvE2EFaults unlocks `companion dev`.
const EnvE2EFaults = "AUCOM_E2E_FAULTS"

// faultExitStatus is what the failing program exits with.
const faultExitStatus = 3

const devUsage = `usage:
  companion dev fault job       [--correlation-id <32 hex>]
  companion dev fault readiness [--correlation-id <32 hex>] [--aub <url>] [--bound <duration>]

Refused unless ` + EnvE2EFaults + `=1 is set in the environment. Each prints one JSON
object naming the incident it raised.
`

func runDev(env *Env, args []string) int {
	if len(args) >= 2 && args[0] == "fault" && args[1] == "exit" {
		fmt.Fprintln(env.Stderr, "the controlled job fault: exiting with status", faultExitStatus)
		return faultExitStatus
	}
	if value, _ := env.lookenv(EnvE2EFaults); strings.TrimSpace(value) != "1" {
		fmt.Fprintf(env.Stderr, "error: companion dev is for end-to-end tests and is refused unless %s=1\n", EnvE2EFaults)
		return 2
	}
	if len(args) < 2 || args[0] != "fault" {
		fmt.Fprint(env.Stderr, devUsage)
		return 2
	}
	switch args[1] {
	case "job":
		return runFaultJob(env, args[2:])
	case "readiness":
		return runFaultReadiness(env, args[2:])
	default:
		fmt.Fprintf(env.Stderr, "error: unknown fault %q (want job or readiness)\n\n%s", args[1], devUsage)
		return 2
	}
}

// faultReport is what a fault prints: enough to find the event it caused.
type faultReport struct {
	Fault         string `json:"fault"`
	Code          string `json:"code"`
	IncidentID    string `json:"incident_id"`
	CorrelationID string `json:"correlation_id"`
	Origin        string `json:"correlation_origin"`
	Operation     string `json:"operation"`
	Release       string `json:"release"`
	Environment   string `json:"environment"`
	Sent          bool   `json:"sent"`
	JobID         string `json:"job_id,omitempty"`
}

// printFault prints what a fault raised. sentBefore is the reporter's sent
// counter from before the fault ran, so `sent` says whether THIS incident went.
func printFault(env *Env, reporter *incident.Reporter, fault string, inc incident.Incident, jobID string, sentBefore int64) int {
	if inc.IncidentID == "" {
		return fail(env, errors.New("the fault ran but raised no incident"))
	}
	sent := false
	if reporter.Enabled() {
		// Waited for here rather than on the way out, so `sent` is the truth.
		reporter.Flush(incidentFlush)
		after, _, _ := reporter.Counters()
		sent = after > sentBefore
	}
	encoded, _ := json.Marshal(faultReport{
		Fault: fault, Code: inc.Code, IncidentID: inc.IncidentID, CorrelationID: inc.CorrelationID,
		Origin: inc.CorrelationOrigin, Operation: inc.Operation, Release: inc.Release,
		Environment: inc.Environment, Sent: sent, JobID: jobID,
	})
	fmt.Fprintln(env.Stdout, string(encoded))
	return 0
}

func correlationFlag(env *Env, raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	if !incident.IsCorrelationID(raw) {
		fmt.Fprintln(env.Stderr, "error: --correlation-id must be 32 lowercase hexadecimal characters")
		return "", false
	}
	return raw, true
}

func runFaultReadiness(env *Env, args []string) int {
	set := newFlagSet(env, "dev fault readiness")
	correlation := set.String("correlation-id", "", "carry this correlation id instead of minting one")
	address := set.String("aub", "", "the backend to check; default is the configured one")
	bound := set.Duration("bound", aub.ReadinessBound, "how long to wait for an answer")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	id, ok := correlationFlag(env, *correlation)
	if !ok {
		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	target := strings.TrimSpace(*address)
	if target == "" {
		if target, err = settings.AUB(); err != nil {
			return fail(env, err)
		}
	}
	client, err := aub.New(target, nil)
	if err != nil {
		return fail(env, err)
	}
	reporter := env.incidents(settings)
	sentBefore, _, _ := reporter.Counters()
	inc, err := incident.CheckReadiness(context.Background(), client, *bound, reporter, id)
	if err == nil {
		return fail(env, errors.New("the configured backend answered its readiness check; point --aub (or "+
			"AUCOM_AUB_BASE_URL) at an address nothing answers on to cause the fault"))
	}
	return printFault(env, reporter, "readiness", inc, "", sentBefore)
}

// faultProfileID is the document the job fault runs. Never written anywhere
// but the fault's own temporary directory, which is removed afterwards.
const faultProfileID = "auto-pigeon.e2e.fault"

func faultProfile() ([]byte, error) {
	return json.Marshal(map[string]any{
		"schema_version": "aucom.profile/1.0",
		"kind":           "tool",
		"id":             faultProfileID,
		"version":        "1.0.0",
		"name":           "Controlled job fault",
		"summary":        "Runs the Companion's own failing program, so a supervised job fails for real.",
		"publisher":      map[string]any{"name": "Auto-Pigeon Companion"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"tool_version":   "1.0.0",
		"platforms": []map[string]any{
			{"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "status": "supported"},
		},
		"acquisition": []map[string]any{
			{"mode": "user_path", "title": "This program", "hint": "the Companion itself"},
		},
		"executables": []map[string]any{
			{"name": "companion", "title": "The Companion", "file": "companion{platform.exe_suffix}"},
		},
		"actions": []map[string]any{{
			"id":              "fail",
			"title":           "Fail on purpose",
			"executable":      "companion",
			"args":            []string{"dev", "fault", "exit"},
			"roots":           []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "scratch space"}},
			"timeout_seconds": 60,
		}},
	})
}

func runFaultJob(env *Env, args []string) int {
	set := newFlagSet(env, "dev fault job")
	correlation := set.String("correlation-id", "", "carry this correlation id instead of minting one")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	id, ok := correlationFlag(env, *correlation)
	if !ok {
		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	self, err := os.Executable()
	if err != nil {
		return fail(env, err)
	}
	// Its own store and catalogue, in a temporary directory: the fault must
	// not appear in the person's job list or leave a document in their
	// profiles. Everything else is the real executor.
	dir, err := os.MkdirTemp("", "aucom-fault-")
	if err != nil {
		return fail(env, err)
	}
	defer os.RemoveAll(dir)
	profiles := filepath.Join(dir, "profiles")
	document, err := faultProfile()
	if err == nil {
		err = os.MkdirAll(profiles, 0o700)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(profiles, "fault.tool.json"), document, 0o600)
	}
	if err != nil {
		return fail(env, err)
	}
	catalog := job.NewCatalog(profiles)
	entry, err := catalog.Lookup(faultProfileID)
	if err != nil {
		return fail(env, err)
	}
	// A real grant against the real digest, through the real Authorize: the
	// document is `local` and would be refused without one.
	grant := binding.LocalBinding{
		SchemaVersion: binding.SchemaVersion,
		ProfileID:     faultProfileID,
		ProfileDigest: entry.Digest,
		Trust:         entry.Trust,
		Grant: &profile.Grant{
			ProfileID: faultProfileID,
			Version:   entry.Profile.Metadata().Version,
			Digest:    entry.Digest,
			Trust:     entry.Trust,
			Granted:   profile.PermissionIDs(entry.Profile),
			GrantedAt: time.Now().UTC(),
		},
	}
	store, err := job.OpenStore(filepath.Join(dir, "jobs"))
	if err != nil {
		return fail(env, err)
	}
	reporter := env.incidents(settings)
	sentBefore, _, _ := reporter.Counters()
	var raised incident.Incident
	service, err := job.NewService(job.Options{
		Store:   store,
		Catalog: catalog,
		Bindings: func(profileID string) (binding.LocalBinding, bool) {
			return grant, profileID == faultProfileID
		},
		Concurrency: 1,
		OnFinished:  incident.JobHook(reporter, func(inc incident.Incident) { raised = inc }),
	})
	if err != nil {
		return fail(env, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := service.Start(ctx); err != nil {
		return fail(env, err)
	}
	submitted, err := service.Submit(job.Request{
		ProfileID:     faultProfileID,
		ActionID:      "fail",
		Executables:   map[string]string{"companion": self},
		Label:         "controlled job fault",
		CorrelationID: id,
	})
	if err != nil {
		service.Close()
		return fail(env, err)
	}
	finished, err := service.Wait(ctx, submitted.ID)
	service.Close()
	if err != nil {
		return fail(env, err)
	}
	if finished.State != job.Failed {
		return fail(env, fmt.Errorf("the controlled job ended %s, not failed", finished.State))
	}
	return printFault(env, reporter, "job", raised, finished.ID, sentBefore)
}
