// The cross-stack correlation id: one user action, one value, four components.
//
// ## The convention
//
// A correlation id is **128 random bits, written as 32 lowercase hexadecimal characters**. It is
// minted at the point a PERSON does something — a click in AUP, a page load in AUG — and then
// travels, unchanged, for as far as that action reaches:
//
//     AUP  mints it at the interaction boundary
//      |   HTTP:      X-Auto-Pigeon-Correlation-Id: <id>
//      v
//     AUB  reads the header; puts it on the job it schedules and on its own incidents
//      |   job record: correlation_id
//      v
//     AUE  reads it from the job it was handed; puts it on its worker incidents
//
//     AUP  ---- Colyseus message field `correlationId` ---->  AUC
//
// Every incident raised anywhere along that path carries it, and so does the GlitchTip event, as
// the tag `correlation_id`. That is what makes "the editor froze, the backend logged a timeout and
// a worker died" one story instead of three coincidences.
//
// ## Why random and not structured
//
// It carries NO information: not a user id, not an account, not a map, not a hostname, not a
// timestamp. It is an unauthenticated value that will appear in a public bug report, so anything
// encoded in it is something published. Randomness also makes it safe to mint on either side of a
// boundary without coordination.
//
// ## Why the same shape as incident_id
//
// Both are 32 hex characters, and neither is derivable from the other. `incident_id` identifies ONE
// incident across its own escalations (3s, 10s, 30s); `correlation_id` identifies the USER ACTION
// several incidents in different components may each be about. Reusing one shape means one
// validator, one pattern in the schema, and one rule in redaction — nothing here matches a bare
// 32-character hex string, precisely so that these two survive.

/** The HTTP header every Auto-Pigeon service reads and forwards. */
export const CORRELATION_HEADER = "X-Auto-Pigeon-Correlation-Id";

/** Its lowercase form, which is what a fetch/Node header map will actually be keyed by. */
export const CORRELATION_HEADER_LOWER = CORRELATION_HEADER.toLowerCase();

/** The field name carrying it on a Colyseus message and on an AUB job record. */
export const CORRELATION_FIELD = "correlationId";

/** The GlitchTip/Sentry tag it is sent as. */
export const CORRELATION_TAG = "correlation_id";

const PATTERN = /^[0-9a-f]{32}$/;

/** Does this value satisfy the convention? */
export function isCorrelationId(value) {
  return typeof value === "string" && PATTERN.test(value);
}

/**
 * Mint a new one.
 *
 * Uses the Web Crypto CSPRNG, which browsers and node have both had globally for years. There is
 * deliberately no `Math.random()` fallback: an id that is predictable in some deployments and not
 * in others is worse than a loud failure, because nothing downstream can tell which kind it got.
 */
export function newCorrelationId() {
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  let out = "";
  for (const byte of bytes) out += byte.toString(16).padStart(2, "0");
  return out;
}

/**
 * Read a correlation id out of an incoming request's headers, or `undefined`.
 *
 * Accepts a `Headers` instance, a plain object, or node's `IncomingMessage.headers` (where a
 * repeated header arrives as an array). A malformed value is treated as absent rather than
 * propagated: a caller that forwards whatever it was sent turns one client's typo into a value
 * three services then record.
 */
export function correlationIdFromHeaders(headers) {
  if (!headers) return undefined;
  let raw;
  if (typeof headers.get === "function") {
    raw = headers.get(CORRELATION_HEADER);
  } else {
    for (const [key, value] of Object.entries(headers)) {
      if (key.toLowerCase() === CORRELATION_HEADER_LOWER) {
        raw = value;
        break;
      }
    }
  }
  if (Array.isArray(raw)) raw = raw[0];
  if (typeof raw !== "string") return undefined;
  const normalised = raw.trim().toLowerCase();
  return isCorrelationId(normalised) ? normalised : undefined;
}

/**
 * What an outgoing request should carry: the id it was given, or a fresh one.
 *
 * This is the whole propagation rule in one function, so no component has to decide it again.
 */
export function correlationHeaders(existing) {
  const id = isCorrelationId(existing) ? existing : newCorrelationId();
  return { [CORRELATION_HEADER]: id };
}
