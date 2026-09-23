// Building an incident envelope, and turning one into a GlitchTip event.

import { SCHEMA_VERSION } from "./rules.mjs";
import { newCorrelationId, isCorrelationId, CORRELATION_TAG } from "./correlation.mjs";
import { redactIncident, redactText } from "./redact.mjs";

/** A fresh incident id: 128 random bits as 32 lowercase hex characters, like a correlation id. */
export function newIncidentId() {
  return newCorrelationId();
}

/**
 * A fresh **transport** event id: the value Sentry and GlitchTip store one EVENT under.
 *
 * It is not the incident id, and keeping the two apart is the whole of INC-01. `incident_id` names
 * one fault in the product's own terms and stays put while that fault escalates — 3 s, 10 s, 30 s,
 * recovery — so the ring buffer holds one row, the user sees one card, and a reader can ask about
 * one thing. `event_id` names one TRANSMISSION, and the ingest enforces that: GlitchTip's store
 * endpoint answers HTTP 422 `Duplicate event id` to the second event carrying an id it already
 * holds. Sending the stable incident id as the transport id therefore delivered the FIRST rung of
 * every stall and silently threw away every later one — OBS-07 measured 22 rejected sends in a
 * single stage, which is the entire escalation story of a stall being lost at the door.
 *
 * The two are tied back together by the `incident_id` TAG that {@link toSentryEvent} puts on every
 * event, so all the rungs of one stall are still one query. The id also stays in the closed
 * `contexts.incident` block, where it always was.
 *
 * ### Why a version-4 UUID and not the plain 128 random bits used everywhere else here
 *
 * Because this one is somebody else's field. Sentry's event payload documents `event_id` as a
 * *"hexadecimal string representing a uuid4 value"*, exactly 32 characters, no dashes. Today's
 * GlitchTip parses it with a UUID constructor that accepts any 32 hex characters — which is why the
 * old code worked at all — and satisfying the DOCUMENTED contract rather than what one server
 * version happens to tolerate is what survives the next upgrade. It is still 32 lowercase hex
 * characters, so it is the same shape to a reader and to redaction, where nothing matches a bare
 * 32-character hex string on purpose.
 *
 * `crypto.getRandomValues` rather than `crypto.randomUUID`, and that is not a style choice:
 * `randomUUID` is exposed only in a SECURE CONTEXT, and AUP is served over plain HTTP on a LAN. An
 * id generator that exists on `localhost` and is `undefined` at `192.168.0.33` fails in exactly the
 * deployment nobody tests. There is no `Math.random()` fallback here for the reason
 * `newCorrelationId` gives.
 */
export function newTransportEventId() {
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
  bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 1, RFC 4122
  let out = "";
  for (const byte of bytes) out += byte.toString(16).padStart(2, "0");
  return out;
}

/**
 * The `occurred_at` format the schema demands: UTC, RFC 3339, milliseconds, `Z`.
 *
 * A fixed shape rather than "whatever toISOString gives" so that the pattern in the schema can be
 * strict. `Date.prototype.toISOString` already produces exactly this for a valid date.
 */
export function formatOccurredAt(when = new Date()) {
  return new Date(when).toISOString().replace(/\.(\d{3})\d*Z$/, ".$1Z");
}

/**
 * Build an incident envelope.
 *
 * Fills in only what a caller cannot reasonably be asked to repeat at every call site — the schema
 * version, an id, a timestamp — and REFUSES to invent anything else. In particular there is no
 * default component, no default severity and no default release: a wrong component sends a reader
 * to the wrong repository, and a made-up release is the placeholder-version failure PREPROD-05
 * settled (an uninjected build reports `unknown`, and `unknown` is a legal value here).
 *
 * Undefined optional fields are omitted rather than written as `null`, because the schema is closed
 * and `null` is not one of the types any field accepts.
 *
 * The result is NOT redacted and NOT validated. Both are on-demand (AULIBS §2.1) and both are
 * separate calls: `redactIncident` then `validateIncident`.
 */
export function createIncident(fields) {
  const incident = {
    schema_version: SCHEMA_VERSION,
    incident_id: fields.incident_id ?? newIncidentId(),
    occurred_at: fields.occurred_at ?? formatOccurredAt(),
    severity: fields.severity,
    component: fields.component,
    code: fields.code,
    message: fields.message,
    recoverable: fields.recoverable,
  };
  const optional = [
    "subsystem",
    "operation",
    "duration_ms",
    "correlation_id",
    "release",
    "environment",
    "user_action",
  ];
  for (const key of optional) {
    if (fields[key] !== undefined) incident[key] = fields[key];
  }
  if (fields.evidence !== undefined) {
    const evidence = {};
    for (const [key, value] of Object.entries(fields.evidence)) {
      if (value !== undefined) evidence[key] = value;
    }
    incident.evidence = evidence;
  }
  return incident;
}

/**
 * The Sentry/GlitchTip event an incident becomes.
 *
 * This is a plain object in the shape the Sentry SDKs and the store endpoint both accept, so a
 * component may hand it to `captureEvent` or POST it. It is deliberately not an SDK call: which SDK
 * a component uses is that component's decision, and this package has no runtime dependency.
 *
 * Three things are guaranteed to be on every event because the acceptance for the whole
 * observability foundation rests on them: the RELEASE (`1.N`), the ENVIRONMENT, and the
 * CORRELATION ID as a tag.
 *
 * ### Two identities, and every call mints one of them
 *
 * `event_id` is a FRESH {@link newTransportEventId} on every call, because it identifies a
 * transmission and GlitchTip refuses a repeat with HTTP 422. `incident_id` is the stable one and it
 * travels twice: in the closed `contexts.incident` block where it always was, and now as a
 * SEARCHABLE TAG, which is what makes the four events of one escalating stall one query rather than
 * four unrelated rows. Nothing is encoded in either — both are random bits — so the tag publishes
 * nothing the context did not already carry.
 *
 * The `fingerprint` is here for the same reason. Once repeated transmissions stop being rejected,
 * the three rungs of one stall are three events whose MESSAGES differ ("after 3 seconds", "after 10
 * seconds"), and the default grouping would file them as three unrelated issues. Grouping on what
 * the fault IS — component, code, subsystem, operation — is what AUB and AUE have always done here,
 * and this is the line that makes the three lanes agree. It is deliberately not the message: a
 * message carrying a duration or a map name groups every occurrence separately, which is the
 * failure this prevents.
 *
 * The event is redacted on the way out. That is not belt-and-braces — this is the boundary where
 * the closed schema stops governing, because from here the payload is somebody else's format.
 */
export function toSentryEvent(incident, options = {}) {
  const safe = redactIncident(incident);
  const tags = {
    incident_id: safe.incident_id,
    incident_code: safe.code,
    component: safe.component,
    recoverable: String(safe.recoverable),
  };
  if (safe.subsystem) tags.subsystem = safe.subsystem;
  if (safe.operation) tags.operation = safe.operation;
  if (isCorrelationId(safe.correlation_id)) tags[CORRELATION_TAG] = safe.correlation_id;

  const event = {
    event_id: newTransportEventId(),
    timestamp: safe.occurred_at,
    level: safe.severity,
    logger: `auto-pigeon.${safe.component.toLowerCase()}`,
    platform: options.platform ?? "other",
    release: safe.release ?? options.release ?? "unknown",
    environment: safe.environment ?? options.environment ?? "development",
    transaction: safe.operation,
    message: safe.message,
    tags,
    // One stall is one issue, however many times it escalated. See the note above; the same four
    // fields, in the same order, as AUB's and AUE's Go reporters.
    fingerprint: [safe.component, safe.code, safe.subsystem ?? "", safe.operation ?? ""],
    // `contexts` is Sentry's own place for structured, non-searchable detail. The whole of it here
    // is the closed evidence object plus the two durations — nothing else may be added, or the
    // bound the envelope schema provides stops meaning anything the moment an event leaves.
    contexts: {
      incident: {
        schema_version: safe.schema_version,
        incident_id: safe.incident_id,
        duration_ms: safe.duration_ms,
        user_action: safe.user_action,
        evidence: safe.evidence,
      },
    },
  };
  // Frames for source-map symbolication, added AFTER redaction and only in the shape
  // `stackFrames` produces (`app:///<path>.js`, a plain function name, line, column). The exception
  // VALUE is the incident code, never the error message: the message is free text and already
  // travels, redacted, as `message`.
  const frames = Array.isArray(options.exception?.frames) ? options.exception.frames.filter(isSafeFrame).slice(-30) : [];
  if (frames.length) {
    event.exception = {
      values: [{
        type: /^[A-Z][A-Za-z0-9]{0,39}$/.test(options.exception.type ?? "") ? options.exception.type : "Error",
        value: safe.code,
        stacktrace: { frames },
      }],
    };
  }
  for (const key of Object.keys(event)) {
    if (event[key] === undefined) delete event[key];
  }
  return event;
}

const SAFE_PATH = /^app:\/\/\/(?:[A-Za-z0-9._-]+\/){0,8}[A-Za-z0-9._-]+\.m?js$/;
function isSafeFrame(frame) {
  return frame !== null && typeof frame === "object"
    && SAFE_PATH.test(frame.abs_path) && frame.filename === frame.abs_path
    && /^[A-Za-z0-9_$.<>?]{1,80}$/.test(frame.function)
    && Number.isInteger(frame.lineno) && frame.lineno > 0
    && Number.isInteger(frame.colno) && frame.colno > 0
    && frame.in_app === true
    && Object.keys(frame).length === 6;
}

/**
 * The bounded, sanitised text a user can read before choosing to file a public bug report.
 *
 * Every line is derived from the envelope, so there is nothing in it that was not already bounded
 * by the schema, and the whole thing goes through redaction anyway. What a user is shown is exactly
 * what would be published — the preview and the payload are the same string, which is the only
 * version of "preview before sending" worth having.
 */
export function toDiagnosticText(incident) {
  const safe = redactIncident(incident);
  const lines = [
    `incident      ${safe.incident_id}`,
    `code          ${safe.code}`,
    `component     ${safe.component}${safe.subsystem ? ` / ${safe.subsystem}` : ""}`,
    `severity      ${safe.severity}${safe.recoverable ? "" : " (session unreliable)"}`,
    `occurred at   ${safe.occurred_at}`,
  ];
  if (safe.operation) lines.push(`operation     ${safe.operation}`);
  if (safe.duration_ms !== undefined) lines.push(`duration      ${safe.duration_ms} ms`);
  if (safe.release) lines.push(`release       ${safe.release}`);
  if (safe.environment) lines.push(`environment   ${safe.environment}`);
  if (safe.correlation_id) lines.push(`correlation   ${safe.correlation_id}`);
  lines.push(`message       ${redactText(safe.message)}`);
  if (safe.user_action) lines.push(`what to do    ${safe.user_action}`);
  if (safe.evidence && Object.keys(safe.evidence).length > 0) {
    lines.push("evidence");
    for (const [key, value] of Object.entries(safe.evidence)) {
      lines.push(`  ${key.padEnd(18)}${value}`);
    }
  }
  return lines.join("\n");
}
