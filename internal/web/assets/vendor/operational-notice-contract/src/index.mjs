// @auto-pigeon/operational-notice-contract — one bounded notice contract for AUB and every client.
//
// ## What a notice is, and what it is not
//
// An operator publishes a scheduled-maintenance (or other operational) notice ONCE, in AUB, and
// AUP, AUG and AUCOM render it. This package is the part they must agree on: the read response,
// how a client derives the phase from SERVER time, which notices a caller may see, how dismissal is
// keyed, how often to poll, and what may be cached. It has no transport and no UI — each client
// fetches through its own AUB client and renders in its own idiom.
//
// It is not a notification platform: no per-user delivery, no read receipts, no push. And it has a
// stated limit every client repeats to its users: it can announce PLANNED downtime and keep showing
// a notice it already fetched while AUB is down, but it cannot tell a first-time or offline client
// about an UNPLANNED outage — that needs a status channel that does not depend on AUB, which is
// backlog, not this package.
//
// ## Server time, never the local clock
//
// Every phase is computed from `server_time` plus the time elapsed on THIS machine since the
// response arrived. A client whose clock is an hour wrong still shows a notice at the right moment
// and hides it at the right moment, because the local clock is only ever used to measure an
// interval, never to read the date.

import rules from "../schema/notice-rules.json" with { type: "json" };
import responseSchema from "../schema/operational-notices-response-1.0.schema.json" with { type: "json" };

export { rules as noticeRules, responseSchema as noticeResponseSchema };

export const NOTICE_SCHEMA = rules.response_schema;
export const NOTICE_SEVERITIES = Object.freeze([...rules.severities]);
export const NOTICE_SURFACES = Object.freeze([...rules.surfaces]);
export const NOTICE_VISIBILITIES = Object.freeze([...rules.visibilities]);
export const NOTICE_LIMITS = Object.freeze({ ...rules.limits });
export const NOTICE_POLL = Object.freeze({ ...rules.poll });
export const NOTICE_TELEMETRY_EVENTS = Object.freeze([...rules.telemetry.events]);

const ID = new RegExp(rules.patterns.id);
const INSTANT = new RegExp(rules.patterns.instant);
const hex = (code) => `\\u{${code}}`;
const REMOVED = new RegExp(`[${rules.text.removed_ranges.map(([a, b]) => `${hex(a)}-${hex(b)}`).join("")}]`, "u");
const REMOVED_GLOBAL = new RegExp(REMOVED.source, "gu");
const REFUSED = rules.text.refused_patterns.map((pattern) => new RegExp(pattern));

const isObject = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
const instantMs = (value) => (typeof value === "string" && INSTANT.test(value) ? Date.parse(value) : NaN);

/**
 * Is this title/body acceptable to the STORE? `{ ok, reason }`. The store refuses rather than
 * cleans, so an operator learns at write time; `reason` is one of `required`, `too_long`,
 * `invisible_or_control_character`, `markup_not_allowed`, `line_break_not_allowed`.
 */
export function checkNoticeText(value, { field }) {
  const max = field === "title" ? NOTICE_LIMITS.title : NOTICE_LIMITS.body;
  if (typeof value !== "string") return { ok: false, reason: "required" };
  if (field === "title" && value.trim() === "") return { ok: false, reason: "required" };
  if (Array.from(value).length > max) return { ok: false, reason: "too_long" };
  if (field === "title" && /[\n\r]/.test(value)) return { ok: false, reason: "line_break_not_allowed" };
  if (/\r/.test(value) || REMOVED.test(value)) return { ok: false, reason: "invisible_or_control_character" };
  if (REFUSED.some((pattern) => pattern.test(value))) return { ok: false, reason: "markup_not_allowed" };
  return { ok: true };
}

/** Is this schedule acceptable to the STORE, at `nowMs`? `{ ok, reason }`. */
export function checkNoticeSchedule({ show_from, starts_at, ends_at }, nowMs) {
  const show = instantMs(show_from);
  const start = instantMs(starts_at);
  const end = instantMs(ends_at);
  if ([show, start, end].some(Number.isNaN)) return { ok: false, reason: "instant_invalid" };
  if (!(show <= start)) return { ok: false, reason: "show_after_start" };
  if (!(start < end)) return { ok: false, reason: "end_not_after_start" };
  if (start - show > NOTICE_LIMITS.max_lead_seconds * 1000) return { ok: false, reason: "lead_too_long" };
  if (end - start > NOTICE_LIMITS.max_duration_seconds * 1000) return { ok: false, reason: "duration_too_long" };
  if (end - nowMs > NOTICE_LIMITS.max_future_seconds * 1000) return { ok: false, reason: "too_far_in_future" };
  return { ok: true };
}

/**
 * What a CLIENT does with one notice from the wire: keep it only if it is well formed, strip what
 * the store should already have refused, and make a critical notice non-dismissible whatever the
 * data says. Returns the clean notice or `undefined`.
 */
export function normalizeNotice(raw) {
  if (!isObject(raw)) return undefined;
  if (!ID.test(String(raw.id)) || !Number.isInteger(raw.revision) || raw.revision < 1) return undefined;
  if (!NOTICE_SEVERITIES.includes(raw.severity) || !NOTICE_VISIBILITIES.includes(raw.visibility)) return undefined;
  const show = instantMs(raw.show_from);
  const start = instantMs(raw.starts_at);
  const end = instantMs(raw.ends_at);
  if ([show, start, end].some(Number.isNaN) || !(show <= start && start < end)) return undefined;
  const clean = (text, max, multiline) => {
    if (typeof text !== "string") return "";
    let out = text.replace(/\r\n?/g, "\n").replace(REMOVED_GLOBAL, "");
    if (!multiline) out = out.replace(/\n+/g, " ");
    const points = Array.from(out.trim());
    return points.length > max ? `${points.slice(0, max - 1).join("")}…` : points.join("");
  };
  const title = clean(raw.title, NOTICE_LIMITS.title, false);
  if (!title) return undefined;
  return {
    id: raw.id,
    revision: raw.revision,
    title,
    body: clean(raw.body, NOTICE_LIMITS.body, true),
    severity: raw.severity,
    show_from: raw.show_from,
    starts_at: raw.starts_at,
    ends_at: raw.ends_at,
    visibility: raw.visibility,
    dismissible: raw.severity === "critical" ? false : raw.dismissible === true,
  };
}

const rank = (notice) => rules.severity_rank[notice.severity];
const byOrder = (a, b) =>
  rank(a) - rank(b) || instantMs(a.starts_at) - instantMs(b.starts_at) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

/**
 * Parse a read response. `{ ok: true, response, dropped }` or `{ ok: false, reason }`; never throws.
 * Malformed notices are DROPPED individually (and counted) rather than failing the whole response,
 * so one bad row cannot hide a critical notice.
 */
export function parseNoticeResponse(value) {
  if (!isObject(value) || value.schema !== NOTICE_SCHEMA) return { ok: false, reason: "schema_unsupported" };
  if (Number.isNaN(instantMs(value.server_time))) return { ok: false, reason: "server_time_invalid" };
  if (!NOTICE_SURFACES.includes(value.surface)) return { ok: false, reason: "surface_invalid" };
  if (!NOTICE_VISIBILITIES.includes(value.visibility)) return { ok: false, reason: "visibility_invalid" };
  if (!Array.isArray(value.notices)) return { ok: false, reason: "notices_invalid" };
  const kept = [];
  let dropped = 0;
  for (const raw of value.notices.slice(0, NOTICE_LIMITS.max_notices * 5)) {
    const notice = normalizeNotice(raw);
    if (!notice || (value.visibility === "public" && notice.visibility !== "public")) dropped += 1;
    else kept.push(notice);
  }
  kept.sort(byOrder);
  dropped += Math.max(0, kept.length - NOTICE_LIMITS.max_notices) + Math.max(0, value.notices.length - NOTICE_LIMITS.max_notices * 5);
  const pollAfter = Number.isInteger(value.poll_after_seconds) ? value.poll_after_seconds : NOTICE_POLL.default_seconds;
  return {
    ok: true,
    dropped,
    response: {
      schema: NOTICE_SCHEMA,
      server_time: value.server_time,
      surface: value.surface,
      visibility: value.visibility,
      poll_after_seconds: Math.min(NOTICE_POLL.max_seconds, Math.max(NOTICE_POLL.min_seconds, pollAfter)),
      notices: kept.slice(0, NOTICE_LIMITS.max_notices),
    },
  };
}

/**
 * The offset between the server's clock and this machine's, from one exchange. `requestStartMs`
 * and `responseEndMs` are this machine's wall clock around the request; the server's time is
 * taken to be the midpoint. Store this OFFSET, not the server time: `serverNow = Date.now() + offset`
 * stays right however wrong the local clock is, as long as it keeps running.
 */
export function clockOffsetMs(serverTime, requestStartMs, responseEndMs) {
  const server = instantMs(serverTime);
  if (Number.isNaN(server)) return undefined;
  return Math.round(server - (requestStartMs + responseEndMs) / 2);
}

/** `pending` | `upcoming` | `active` | `expired`, at a SERVER instant. */
export function noticePhase(notice, serverNowMs) {
  if (serverNowMs < instantMs(notice.show_from)) return "pending";
  if (serverNowMs < instantMs(notice.starts_at)) return "upcoming";
  if (serverNowMs < instantMs(notice.ends_at)) return "active";
  return "expired";
}

/** The local dismissal key: one account (or `anonymous`) + one notice + one REVISION. */
export function dismissalKey(account, notice) {
  const who = typeof account === "string" && account ? account : "anonymous";
  return `${who}:${notice.id}:${notice.revision}`;
}

/**
 * The notices this client shows NOW: `[{ notice, phase }]`, ordered. Eligible when the phase is
 * upcoming or active, the visibility allows this caller, and it is not dismissed — a critical notice
 * cannot be dismissed. Active before upcoming within one severity.
 */
export function selectVisibleNotices({ response, authenticated, serverNowMs, account, dismissed }) {
  if (!response || !Array.isArray(response.notices)) return [];
  const isDismissed = (notice) =>
    notice.dismissible && notice.severity !== "critical" && dismissed && typeof dismissed.has === "function"
      && dismissed.has(dismissalKey(authenticated ? account : undefined, notice));
  return response.notices
    .filter((notice) => notice.visibility === "public" || authenticated === true)
    .map((notice) => ({ notice, phase: noticePhase(notice, serverNowMs) }))
    .filter(({ notice, phase }) => (phase === "upcoming" || phase === "active") && !isDismissed(notice))
    .sort((a, b) => rank(a.notice) - rank(b.notice)
      || (a.phase === b.phase ? 0 : a.phase === "active" ? -1 : 1)
      || byOrder(a.notice, b.notice));
}

/**
 * What may be written to browser storage: the PUBLIC notices only, with the offset. An
 * authenticated notice is never cached, so signing out — or another person opening this browser —
 * can never surface one from disk.
 */
export function cacheableResponse(response, offsetMs) {
  if (!response) return undefined;
  return {
    response: { ...response, visibility: "public", notices: response.notices.filter((n) => n.visibility === "public") },
    offset_ms: Number.isFinite(offsetMs) ? offsetMs : 0,
  };
}

/**
 * Read a cache entry back: parsed through the same rules as the wire, and emptied of every notice
 * already expired at the server instant `serverNowMs` — a cached notice is never shown after its
 * server-derived end.
 */
export function restoreCachedResponse(entry, localNowMs) {
  if (!isObject(entry)) return undefined;
  const parsed = parseNoticeResponse(entry.response);
  if (!parsed.ok) return undefined;
  const offset = Number.isFinite(entry.offset_ms) ? entry.offset_ms : 0;
  const serverNowMs = localNowMs + offset;
  return {
    offset_ms: offset,
    response: {
      ...parsed.response,
      visibility: "public",
      notices: parsed.response.notices.filter((n) => n.visibility === "public" && noticePhase(n, serverNowMs) !== "expired"),
    },
  };
}

/**
 * How long to wait before the next fetch. `failures` is consecutive failures (0 after a success);
 * `random` is a number in [0, 1) the caller supplies, so a test can pin it.
 */
export function nextPollDelayMs({ failures = 0, pollAfterSeconds, random = Math.random() } = {}) {
  // Symmetric jitter (±jitter_fraction), then clamped, so the promise "every 60-120 seconds" and
  // the backoff ceiling hold exactly, whatever the random draw.
  const spread = 1 + NOTICE_POLL.jitter_fraction * (2 * Math.min(1, Math.max(0, random)) - 1);
  if (failures <= 0) {
    const base = Math.min(NOTICE_POLL.max_seconds, Math.max(NOTICE_POLL.min_seconds,
      Number.isFinite(pollAfterSeconds) ? pollAfterSeconds : NOTICE_POLL.default_seconds));
    return Math.round(Math.min(NOTICE_POLL.max_seconds, Math.max(NOTICE_POLL.min_seconds, base * spread)) * 1000);
  }
  const backoff = Math.min(NOTICE_POLL.backoff_max_seconds, NOTICE_POLL.backoff_base_seconds * 2 ** (Math.min(failures, 16) - 1));
  return Math.round(Math.min(NOTICE_POLL.backoff_max_seconds, Math.max(NOTICE_POLL.min_seconds, backoff * spread)) * 1000);
}

/**
 * The server's selection, as a reference for AUB's Go implementation and its vectors: from stored
 * rows (with `enabled`, `surfaces`), the notices a caller on `surface` may be served at `nowMs`.
 * Enabled, targeted at this surface, visible to this caller, already inside the look-ahead window
 * and not yet ended; ordered by severity, start and id; at most `max_notices`.
 */
export function selectServerNotices(rows, { surface, authenticated, nowMs }) {
  const horizon = nowMs + NOTICE_LIMITS.lookahead_seconds * 1000;
  return (Array.isArray(rows) ? rows : [])
    .filter((row) => row && row.enabled === true && Array.isArray(row.surfaces) && row.surfaces.includes(surface))
    .filter((row) => row.visibility === "public" || (authenticated === true && row.visibility === "authenticated"))
    .filter((row) => instantMs(row.show_from) <= horizon && instantMs(row.ends_at) > nowMs)
    .map((row) => ({
      id: row.id,
      revision: row.revision,
      title: row.title,
      body: row.body,
      severity: row.severity,
      show_from: row.show_from,
      starts_at: row.starts_at,
      ends_at: row.ends_at,
      visibility: row.visibility,
      dismissible: row.severity === "critical" ? false : row.dismissible === true,
    }))
    .sort(byOrder)
    .slice(0, NOTICE_LIMITS.max_notices);
}
