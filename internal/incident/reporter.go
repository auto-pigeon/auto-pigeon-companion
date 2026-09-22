package incident

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// QueueDepth bounds how many events wait to be sent. A Companion whose
// incident backend is down must not grow a queue without bound; past this an
// event is dropped and counted.
const QueueDepth = 32

// SendTimeout bounds one POST. A slow incident backend delays nothing but the
// next event.
const SendTimeout = 5 * time.Second

// Reporter is the one thing in this repository that talks to the incident
// backend.
//
// # Nothing here can fail the Companion
//
// Every method is safe on a nil receiver and on an unconfigured reporter.
// Capture never blocks on the network: it validates, logs one local line and
// hands the event to a bounded queue drained by one goroutine. A send failure
// produces exactly one `reporting.telemetry_unavailable` line in this
// process's own log per outage, and NOTHING else — in particular it never
// raises an incident, because an incident about the sink would go to the sink.
type Reporter struct {
	cfg    Config
	target dsn
	ok     bool
	logf   func(format string, args ...any)
	http   *http.Client

	queue chan []byte
	start sync.Once
	// pending counts events queued or being sent. A counter rather than a
	// WaitGroup, because Capture may add while Flush waits.
	pending atomic.Int64

	mu                sync.Mutex
	unavailableLogged bool

	sent, dropped, failed atomic.Int64
}

// Options configures a reporter.
type Options struct {
	// Logf receives the local transcript: one line per raised incident and one
	// per outage of the backend. Nil discards.
	Logf func(format string, args ...any)
	// HTTPClient sends. Nil means one with SendTimeout and no cookie jar.
	HTTPClient *http.Client
}

// NewReporter builds a reporter. It is usable whatever the configuration.
func NewReporter(cfg Config, options Options) *Reporter {
	r := &Reporter{cfg: cfg, logf: options.Logf, http: options.HTTPClient}
	if r.logf == nil {
		r.logf = func(string, ...any) {}
	}
	if r.http == nil {
		r.http = &http.Client{Timeout: SendTimeout}
	}
	if cfg.Enabled() {
		r.target, r.ok = parseDSN(cfg.DSN)
		if !r.ok {
			// The value is never printed: a DSN carries an ingestion key.
			r.logf("incident: %s is set but is not a DSN this program can use; incidents stay local", EnvDSN)
		}
	}
	r.queue = make(chan []byte, QueueDepth)
	return r
}

// Config is the resolved configuration.
func (r *Reporter) Config() Config {
	if r == nil {
		return Config{}
	}
	return r.cfg
}

// Enabled reports whether events are being sent.
func (r *Reporter) Enabled() bool { return r != nil && r.ok }

// Counters reports what the transport has done: sent, dropped, failed.
func (r *Reporter) Counters() (sent, dropped, failed int64) {
	if r == nil {
		return 0, 0, 0
	}
	return r.sent.Load(), r.dropped.Load(), r.failed.Load()
}

// Capture reports one incident and returns it (redacted). It fills in the
// release and environment from the configuration, redacts, validates, logs,
// and queues.
func (r *Reporter) Capture(d Draft) Incident {
	if r == nil {
		return Incident{}
	}
	inc := Redact(New(d, r.cfg.Release, r.cfg.Environment, time.Now()))
	if problems := Validate(inc); len(problems) > 0 {
		r.logf("incident: not sent, the envelope is invalid: %v", problems)
		return inc
	}
	// The local record first, and unconditionally: it exists whether or not a
	// backend does. Identifiers and the code only — never the message.
	r.logf("incident %s raised: code=%s subsystem=%s operation=%s correlation=%s (%s)",
		inc.IncidentID, inc.Code, inc.Subsystem, inc.Operation, inc.CorrelationID, inc.CorrelationOrigin)
	if !r.ok {
		return inc
	}
	body, err := json.Marshal(Event(inc))
	if err != nil {
		return inc
	}
	r.start.Do(func() { go r.drain() })
	r.pending.Add(1)
	select {
	case r.queue <- body:
	default:
		r.pending.Add(-1)
		r.dropped.Add(1)
	}
	return inc
}

func (r *Reporter) drain() {
	for body := range r.queue {
		r.send(body)
		r.pending.Add(-1)
	}
}

func (r *Reporter) send(body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), SendTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.target.store, bytes.NewReader(body))
	if err != nil {
		r.unavailable("the request could not be built")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	// The key goes in the header, not the URL: net/http puts the URL in every
	// error it returns, and a key in an error is a key in a log.
	request.Header.Set("X-Sentry-Auth", fmt.Sprintf(
		"Sentry sentry_version=7, sentry_key=%s, sentry_client=auto-pigeon-companion/%s", r.target.key, r.cfg.Release))
	response, err := r.http.Do(request)
	if err != nil {
		r.unavailable(classify(err))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		r.unavailable(fmt.Sprintf("the store answered HTTP %d", response.StatusCode))
		return
	}
	r.sent.Add(1)
	r.mu.Lock()
	r.unavailableLogged = false
	r.mu.Unlock()
}

// classify names a send failure without quoting it: the error text carries the
// URL, and the URL is the backend's address.
func classify(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "the send timed out"
	default:
		return "the incident backend could not be reached"
	}
}

// unavailable records `reporting.telemetry_unavailable` locally, once per
// outage. Never escalated, never sent, never able to fail anything.
func (r *Reporter) unavailable(reason string) {
	r.failed.Add(1)
	r.dropped.Add(1)
	r.mu.Lock()
	first := !r.unavailableLogged
	r.unavailableLogged = true
	r.mu.Unlock()
	if first {
		r.logf("incident: %s (%s); incidents are being kept locally", CodeTelemetryUnavailable, reason)
	}
}

// Flush waits, boundedly, for queued events. It reports whether the queue
// drained; a false answer is logged as the backend being unavailable.
func (r *Reporter) Flush(timeout time.Duration) bool {
	if !r.Enabled() {
		return true
	}
	deadline := time.Now().Add(timeout)
	for r.pending.Load() > 0 {
		if time.Now().After(deadline) {
			r.unavailable("queued incidents were still unsent when the Companion stopped")
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}
