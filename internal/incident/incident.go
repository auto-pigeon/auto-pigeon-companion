package incident

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// SchemaVersion is the envelope contract's version, not the Companion's.
const SchemaVersion = "1.0"

// Severities and environments, as the envelope schema closes them.
const (
	SeverityWarning = "warning"
	SeverityError   = "error"
	SeverityFatal   = "fatal"

	EnvironmentDevelopment = "development"
	EnvironmentProduction  = "production"
	EnvironmentTest        = "test"
)

// CorrelationHeader is the workspace-wide HTTP header a correlation id travels
// in (AULIBS correlation.mjs). A value that is not 32 lowercase hex characters
// is treated as absent and never forwarded.
const CorrelationHeader = "X-Auto-Pigeon-Correlation-Id"

// Correlation origins, sent as the `correlation_origin` tag so a reader can
// tell an id this process minted (the story starts here) from one it inherited
// (the story started somewhere else and this is a later chapter).
const (
	OriginInherited = "inherited"
	OriginMinted    = "minted"
)

const occurredAtFormat = "2006-01-02T15:04:05.000Z"

var (
	hexID       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	occurredAt  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z$`)
	subsystemRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	codeRe      = regexp.MustCompile(`^[a-z0-9]+(?:[._][a-z0-9]+)+$`)
	releaseRe   = regexp.MustCompile(`^(1\.[0-9]+|unknown)$`)
)

// Incident is the closed envelope (incident-envelope-1.0.schema.json). There
// is no field a command line, a path, an account or a token can occupy, and
// that is the first line of defence; redaction is the second.
type Incident struct {
	SchemaVersion string
	IncidentID    string
	OccurredAt    string
	Severity      string
	Component     string
	Subsystem     string
	Code          string
	Operation     string
	Message       string
	DurationMS    *int64
	CorrelationID string
	// CorrelationOrigin is OriginInherited or OriginMinted. Not an envelope
	// field: it travels only as an event tag.
	CorrelationOrigin string
	Release           string
	Environment       string
	Recoverable       bool
	UserAction        string
}

// Draft is what a call site supplies; New fills in the rest.
type Draft struct {
	Severity    string
	Subsystem   string
	Code        string
	Operation   string
	Message     string
	Duration    time.Duration
	Correlation string
	UserAction  string
	Recoverable bool
}

// New builds an incident for component AUCOM. A correlation id that satisfies
// the convention is inherited; anything else — absent or malformed — is
// replaced by a fresh one marked as minted here.
func New(d Draft, release, environment string, now time.Time) Incident {
	inc := Incident{
		SchemaVersion: SchemaVersion,
		IncidentID:    NewID(),
		OccurredAt:    now.UTC().Format(occurredAtFormat),
		Severity:      d.Severity,
		Component:     Component,
		Subsystem:     d.Subsystem,
		Code:          d.Code,
		Operation:     d.Operation,
		Message:       d.Message,
		Release:       release,
		Environment:   environment,
		Recoverable:   d.Recoverable,
		UserAction:    d.UserAction,
	}
	if d.Duration > 0 {
		ms := d.Duration.Milliseconds()
		if ms > 86400000 {
			ms = 86400000
		}
		inc.DurationMS = &ms
	}
	if IsCorrelationID(d.Correlation) {
		inc.CorrelationID, inc.CorrelationOrigin = d.Correlation, OriginInherited
	} else {
		inc.CorrelationID, inc.CorrelationOrigin = NewID(), OriginMinted
	}
	return inc
}

// NewID mints 128 random bits as 32 lowercase hex characters — the shape of
// both an incident id and a correlation id. It encodes nothing.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on a supported platform; if it ever did,
		// an all-zero id is at least not a predictable-looking real one.
		return strings.Repeat("0", 32)
	}
	return hex.EncodeToString(b[:])
}

// newTransportID is a version-4 UUID without dashes: the store endpoint's
// `event_id`, fresh per transmission (INC-01: GlitchTip refuses a repeat).
func newTransportID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

// IsCorrelationID reports whether a value satisfies the convention.
func IsCorrelationID(value string) bool { return hexID.MatchString(value) }

// CorrelationFromHeader reads the correlation header's value, normalised, or
// "" when it is absent or malformed.
func CorrelationFromHeader(raw string) string {
	normalised := strings.ToLower(strings.TrimSpace(raw))
	if IsCorrelationID(normalised) {
		return normalised
	}
	return ""
}

// Redact cleans every free-text field. Typed fields are left alone: an id or an
// enum cannot carry a secret, and running a path pattern over an id is how a
// record stops being correlatable.
func Redact(inc Incident) Incident {
	inc.Message = RedactString(inc.Message)
	inc.Operation = RedactString(inc.Operation)
	inc.UserAction = RedactString(inc.UserAction)
	return inc
}

// Validate checks the envelope against the schema's rules and the taxonomy.
// An invalid incident is not sent: an invalid record is worse than a missing
// one, because it is a record a reader believes.
func Validate(inc Incident) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if inc.SchemaVersion != SchemaVersion {
		add("schema_version must be %q", SchemaVersion)
	}
	if !hexID.MatchString(inc.IncidentID) {
		add("incident_id is not 32 lowercase hex characters")
	}
	if !occurredAt.MatchString(inc.OccurredAt) {
		add("occurred_at is not an RFC 3339 UTC instant with milliseconds")
	}
	switch inc.Severity {
	case SeverityWarning, SeverityError, SeverityFatal:
	default:
		add("severity %q is not warning, error or fatal", inc.Severity)
	}
	known := false
	for _, c := range Components {
		known = known || c == inc.Component
	}
	if !known {
		add("component %q is not in the envelope's enum", inc.Component)
	}
	if inc.Subsystem != "" && (len(inc.Subsystem) > 64 || !subsystemRe.MatchString(inc.Subsystem)) {
		add("subsystem %q is not an identifier", inc.Subsystem)
	}
	if !codeRe.MatchString(inc.Code) || len(inc.Code) > 64 {
		add("code %q is malformed", inc.Code)
	} else if entry, ok := Lookup(inc.Code); !ok {
		add("code %q is not in the taxonomy", inc.Code)
	} else if entry.Component != inc.Component {
		add("code %q belongs to %s, not %s", inc.Code, entry.Component, inc.Component)
	}
	if inc.Operation != "" && utf8.RuneCountInString(inc.Operation) > 96 {
		add("operation is longer than 96 characters")
	}
	if n := utf8.RuneCountInString(inc.Message); n < 1 || n > 512 {
		add("message must be 1 to 512 characters")
	}
	if inc.DurationMS != nil && (*inc.DurationMS < 0 || *inc.DurationMS > 86400000) {
		add("duration_ms is out of range")
	}
	if inc.CorrelationID != "" && !hexID.MatchString(inc.CorrelationID) {
		add("correlation_id is not 32 lowercase hex characters")
	}
	if inc.Release != "" && !releaseRe.MatchString(inc.Release) {
		add("release %q is not 1.<n> or unknown", inc.Release)
	}
	switch inc.Environment {
	case "", EnvironmentDevelopment, EnvironmentProduction, EnvironmentTest:
	default:
		add("environment %q is not development, production or test", inc.Environment)
	}
	if n := utf8.RuneCountInString(inc.UserAction); inc.UserAction != "" && n > 256 {
		add("user_action is longer than 256 characters")
	}
	return problems
}

// Event renders an incident as the one JSON event GlitchTip's store endpoint
// takes — the same shape AULIBS' `toSentryEvent` produces, field for field, so
// one query finds every component's incidents by `incident_code`.
//
// Nothing else is attached: no request, no user, no exception, no frames, no
// server name derived from the machine.
func Event(inc Incident) map[string]any {
	safe := Redact(inc)
	tags := map[string]any{
		"incident_id":   safe.IncidentID,
		"incident_code": safe.Code,
		"component":     safe.Component,
		"recoverable":   fmt.Sprint(safe.Recoverable),
	}
	if safe.Subsystem != "" {
		tags["subsystem"] = safe.Subsystem
	}
	if safe.Operation != "" {
		tags["operation"] = safe.Operation
	}
	if IsCorrelationID(safe.CorrelationID) {
		tags["correlation_id"] = safe.CorrelationID
		if safe.CorrelationOrigin != "" {
			tags["correlation_origin"] = safe.CorrelationOrigin
		}
	}
	release := safe.Release
	if release == "" {
		release = "unknown"
	}
	environment := safe.Environment
	if environment == "" {
		environment = EnvironmentDevelopment
	}
	context := map[string]any{
		"schema_version": safe.SchemaVersion,
		"incident_id":    safe.IncidentID,
	}
	if safe.DurationMS != nil {
		context["duration_ms"] = *safe.DurationMS
	}
	if safe.UserAction != "" {
		context["user_action"] = safe.UserAction
	}
	event := map[string]any{
		"event_id":    newTransportID(),
		"timestamp":   safe.OccurredAt,
		"level":       safe.Severity,
		"logger":      "auto-pigeon." + strings.ToLower(safe.Component),
		"platform":    "go",
		"server_name": "auto-pigeon-companion",
		"release":     release,
		"environment": environment,
		"message":     safe.Message,
		"tags":        tags,
		"fingerprint": []string{safe.Component, safe.Code, safe.Subsystem, safe.Operation},
		"contexts":    map[string]any{"incident": context},
	}
	if safe.Operation != "" {
		event["transaction"] = safe.Operation
	}
	return scrub(event)
}

// scrub is the last thing that happens to an outgoing event: the contract's
// three kinds of rule over the whole of it, whoever built it. Keys the event
// format itself needs (`message`, `contexts`, …) are not in the dropped list,
// and the correlation and incident ids are 32-hex strings no pattern matches.
func scrub(event map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range event {
		switch key {
		case "tags":
			tags := map[string]any{}
			for name, v := range value.(map[string]any) {
				if DroppedKey(name) || DeniedKey(name) {
					continue
				}
				tags[name] = RedactValue(v)
			}
			out[key] = tags
		case "contexts":
			contexts := map[string]any{}
			for name, v := range value.(map[string]any) {
				contexts[name] = RedactValue(v)
			}
			out[key] = contexts
		default:
			out[key] = RedactValue(value)
		}
	}
	return out
}
