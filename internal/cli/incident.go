package cli

import (
	"fmt"
	"sync"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/incident"
)

// incidentFlush bounds how long a command waits, on its way out, for queued
// incident reports. A report is never worth holding a terminal hostage for.
const incidentFlush = 3 * time.Second

// incidentHolder is the one reporter an invocation uses, built on first need.
//
// A holder rather than a reporter on Env, because the reporter's configuration
// comes partly from config.json, which only a command that has loaded its
// settings can supply — and most commands never report anything and should
// not pay for a reporter at all.
type incidentHolder struct {
	once     sync.Once
	reporter *incident.Reporter
	// transcript makes the reporter's local lines reach stderr even with no
	// backend configured. `serve` sets it: there the operator has no other
	// view of what happened. A one-shot command prints them only when a
	// backend is configured, because the person already saw the job fail.
	transcript bool
}

// incidents is this invocation's reporter.
func (e *Env) incidents(settings config.Config) *incident.Reporter {
	if e.incidentState == nil {
		e.incidentState = &incidentHolder{}
	}
	holder := e.incidentState
	holder.once.Do(func() {
		cfg := incident.LoadConfig(e.lookenv, settings.IncidentDSN, settings.IncidentEnvironment, e.Version)
		var logf func(string, ...any)
		if holder.transcript || cfg.Enabled() {
			logf = func(format string, args ...any) { fmt.Fprintf(e.Stderr, format+"\n", args...) }
		}
		holder.reporter = incident.NewReporter(cfg, incident.Options{Logf: logf})
	})
	return holder.reporter
}

// flushIncidents waits, boundedly, for whatever this invocation queued.
func (e *Env) flushIncidents() {
	if e.incidentState == nil || e.incidentState.reporter == nil {
		return
	}
	e.incidentState.reporter.Flush(incidentFlush)
}
