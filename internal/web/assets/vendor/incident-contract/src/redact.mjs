// Central redaction.
//
// ## What this is for
//
// An incident travels to two places a secret must never reach: a self-hosted error backend that
// several people can read, and a PUBLIC GitHub bug report the user is invited to file. So auth
// headers, cookies, passwords, invitation tokens, e-mail addresses, private APMap geometry,
// annotation text, private prefab contents and absolute local paths have to be gone before either
// happens.
//
// ## This is the SECOND line of defence, not the first
//
// The first is the envelope schema, which is closed: `additionalProperties: false` at every level
// and an `evidence` object made entirely of typed counters. There is no legal field for a map
// document or a request body, so the ordinary path cannot carry one. These rules exist for the
// free-text fields a human wrote, and for whatever a component attaches on the way OUT to the
// incident backend, where the schema no longer governs.
//
// ## The rules are DATA
//
// ../schema/redaction-rules.json, so that a Go implementation in AUB/AUE and a Python one in the
// tooling are the same rules rather than three sincere transcriptions. Every pattern avoids
// lookaround and backreferences so it compiles unchanged in JavaScript, RE2 and Python.
//
// ## Three kinds of rule, applied in this order
//
//   1. DROPPED KEYS — an exact key name whose whole value goes, however deep it is nested.
//      `geometry`, `annotations`, `prefab`, `body`, `headers`. Matching is exact rather than by
//      substring precisely so that `face_count` survives while `faces` does not: the counter is
//      evidence and the array is a map.
//   2. DENIED KEY SUBSTRINGS — any key CONTAINING one of these has its value replaced. `auth`,
//      `cookie`, `token`, `email`, `invite`. Substring here because the shapes are endless
//      (`authorization`, `authToken`, `x_auth`) and a value under such a key is never something
//      worth keeping.
//   3. VALUE PATTERNS — over every surviving string, wherever it came from. This is what catches
//      the address someone pasted into a message.
//
// ## What is deliberately NOT redacted
//
// A bare 32-character hexadecimal string. `incident_id` and `correlation_id` are exactly that, they
// are the two identifiers the whole cross-stack story is assembled from, and a redacted record
// nobody can correlate is a record nobody can act on.

import { redactionDocument } from "./rules.mjs";

const PLACEHOLDER = redactionDocument.placeholder;
const DROPPED = new Set(redactionDocument.dropped_keys.map((k) => k.toLowerCase()));
const DENIED = redactionDocument.denied_key_substrings.map((k) => k.toLowerCase());

// Compiled once. Each is used with a fresh `lastIndex` per call — `String.replace` with a `g`
// regex resets it itself, so these are safe to share.
const VALUE_PATTERNS = redactionDocument.value_patterns.map((rule) => ({
  name: rule.name,
  regex: new RegExp(rule.pattern, rule.flags),
  replacement: rule.replacement,
}));

/** Should this key's whole value be dropped? */
export function isDroppedKey(key) {
  return DROPPED.has(String(key).toLowerCase());
}

/** Should this key's value be replaced with the placeholder? */
export function isDeniedKey(key) {
  const lower = String(key).toLowerCase();
  return DENIED.some((needle) => lower.includes(needle));
}

/**
 * Apply the value patterns to one string.
 *
 * Exported on its own because a component often has a single sentence to clean — a user-facing
 * message, a log line about to be attached to a bug report — and should not have to build an
 * object to do it.
 */
export function redactText(value) {
  if (typeof value !== "string") return value;
  let out = value;
  for (const rule of VALUE_PATTERNS) out = out.replace(rule.regex, rule.replacement);
  return out;
}

/**
 * Redact any JSON-shaped value, recursively.
 *
 * Returns a new value; the input is never mutated. Cycles are broken with the placeholder rather
 * than followed, because an incident that hangs the reporter is worse than the incident.
 */
export function redactValue(value, seen = new WeakSet()) {
  if (typeof value === "string") return redactText(value);
  if (value === null || typeof value !== "object") return value;
  if (seen.has(value)) return PLACEHOLDER;
  seen.add(value);
  if (Array.isArray(value)) return value.map((item) => redactValue(item, seen));
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (isDroppedKey(key)) continue;
    if (isDeniedKey(key)) {
      out[key] = PLACEHOLDER;
      continue;
    }
    out[key] = redactValue(item, seen);
  }
  return out;
}

/**
 * Redact a whole incident envelope.
 *
 * Structurally the same as `redactValue`, and that is on purpose rather than laziness: the envelope
 * is already closed by its schema, so the only fields these rules can act on are the free-text ones
 * — and a caller that has extended an envelope on its way out gets the same treatment for the
 * extension. The result is still a valid envelope, so `validateIncident` can be run after.
 */
export function redactIncident(incident) {
  return redactValue(incident);
}
