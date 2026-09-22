// Operational notices: the compact banner at the top of the page.
//
// Everything that decides WHAT is shown and WHEN is the shared AULIBS contract,
// vendored byte for byte under vendor/operational-notice-contract and checked
// against its source by internal/web/notices_vendor_test.go. This file is only
// the Companion's transport and rendering around it:
//
//   - fetch GET /api/v1/notices (the local server relays AUB's
//     /api/operational-notices?surface=aucom, with the session when there is
//     one) at start-up, then on the contract's cadence — one request in
//     flight, conditional on the last ETag, backing off after a failure;
//   - measure the offset between the server's clock and this one from
//     X-Auto-Pigeon-Server-Time, and derive every phase from SERVER time;
//   - re-select every second, so a notice appears and disappears at the right
//     moment without waiting for a poll;
//   - cache only the PUBLIC part of the last good answer (cacheableResponse),
//     so a planned-downtime notice survives the server being down and an
//     account-only notice never reaches storage;
//   - file dismissals under the account (dismissalKey) — a critical notice
//     cannot be dismissed;
//   - re-evaluate at once when somebody signs in or out (app.js dispatches
//     `aucom:status`), dropping account-only notices on sign-out.
//
// Title and body are rendered as TEXT nodes, never HTML. Only the contract's
// NOTICE_TELEMETRY_EVENTS are counted, and nothing about a notice leaves the
// page.

import {
  NOTICE_TELEMETRY_EVENTS,
  cacheableResponse,
  clockOffsetMs,
  dismissalKey,
  nextPollDelayMs,
  parseNoticeResponse,
  restoreCachedResponse,
  selectVisibleNotices,
} from "./vendor/operational-notice-contract/src/index.mjs";

const CACHE_KEY = "aucom.notices.cache/1";
const DISMISSED_KEY = "aucom.notices.dismissed/1";
const MAX_DISMISSED = 200;
const SEVERITY_WORDS = { critical: "Critical", maintenance: "Maintenance", warning: "Warning", info: "Notice" };

const token = document.querySelector('meta[name="aucom-api-token"]')?.content || "";
const banner = document.getElementById("notice-banner");
const list = document.getElementById("notice-list");

const state = {
  response: undefined,
  offset: 0,
  etag: "",
  authenticated: false,
  account: "",
  failures: 0,
  inFlight: false,
  fromCache: false,
  timer: 0,
  signedIn: undefined,
  rendered: "",
};

// The only telemetry: counters of the contract's three event names.
const counters = Object.fromEntries(NOTICE_TELEMETRY_EVENTS.map((name) => [name, 0]));
const count = (name) => {
  if (name in counters) counters[name] += 1;
};
window.AUCOM = window.AUCOM || {};
window.AUCOM.notices = { counters, refresh: () => fetchNotices().then(schedule) };

// Storage can be absent, full or refused (a private window); none of that may
// stop the banner, so every access is guarded.
function readStorage(key) {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}
function writeStorage(key, value) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Not cached: the next start fetches, as a first start does.
  }
}

function dismissedKeys() {
  try {
    const parsed = JSON.parse(readStorage(DISMISSED_KEY) || "[]");
    return Array.isArray(parsed) ? parsed.filter((key) => typeof key === "string") : [];
  } catch {
    return [];
  }
}

function dismiss(notice) {
  const key = dismissalKey(state.authenticated ? state.account : undefined, notice);
  const keys = dismissedKeys().filter((existing) => existing !== key);
  keys.push(key);
  writeStorage(DISMISSED_KEY, JSON.stringify(keys.slice(-MAX_DISMISSED)));
  render();
}

function restoreCache() {
  let entry;
  try {
    entry = JSON.parse(readStorage(CACHE_KEY) || "null");
  } catch {
    return;
  }
  const restored = restoreCachedResponse(entry, Date.now());
  if (restored) {
    state.response = restored.response;
    state.offset = restored.offset_ms;
    state.fromCache = true;
  }
}

function failed() {
  count("notices.fetch_failed");
  state.failures += 1;
  if (state.fromCache && state.response?.notices?.length) count("notices.stale_cache_used");
}

async function fetchNotices() {
  if (state.inFlight) return;
  state.inFlight = true;
  const headers = { Accept: "application/json", "X-AUCOM-Token": token };
  if (state.etag) headers["If-None-Match"] = state.etag;
  const requestStart = Date.now();
  try {
    const response = await fetch("/api/v1/notices", { headers, cache: "no-store" });
    const responseEnd = Date.now();
    const serverTime = response.headers.get("X-Auto-Pigeon-Server-Time");
    if (response.status === 304 && state.response) {
      const offset = clockOffsetMs(serverTime, requestStart, responseEnd);
      if (offset !== undefined) state.offset = offset;
      state.failures = 0;
    } else if (response.ok) {
      let body;
      try {
        body = await response.json();
      } catch {
        body = undefined;
      }
      const parsed = parseNoticeResponse(body);
      if (!parsed.ok) {
        count("notices.response_invalid");
        state.failures += 1;
      } else {
        state.response = parsed.response;
        state.fromCache = false;
        state.etag = response.headers.get("ETag") || "";
        state.authenticated = parsed.response.visibility === "authenticated";
        state.account = state.authenticated ? response.headers.get("X-AUCOM-Notice-Account") || "" : "";
        const offset = clockOffsetMs(serverTime || parsed.response.server_time, requestStart, responseEnd);
        if (offset !== undefined) state.offset = offset;
        const cacheable = cacheableResponse(parsed.response, state.offset);
        if (cacheable) writeStorage(CACHE_KEY, JSON.stringify(cacheable));
        state.failures = 0;
      }
    } else {
      failed();
    }
  } catch {
    failed();
  } finally {
    state.inFlight = false;
  }
  render();
}

function schedule() {
  window.clearTimeout(state.timer);
  const delay = nextPollDelayMs({ failures: state.failures, pollAfterSeconds: state.response?.poll_after_seconds });
  state.timer = window.setTimeout(() => fetchNotices().then(schedule), delay);
}

function when(instant) {
  const date = new Date(Date.parse(instant));
  try {
    return date.toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
  } catch {
    return date.toISOString();
  }
}

function noticeItem({ notice, phase }) {
  const item = document.createElement("li");
  item.className = `notice notice--${notice.severity}`;
  item.dataset.noticeId = notice.id;
  item.dataset.phase = phase;
  if (notice.severity === "critical") item.setAttribute("role", "alert");

  const severity = document.createElement("span");
  severity.className = "notice__severity";
  severity.textContent = `${SEVERITY_WORDS[notice.severity] || "Notice"}${phase === "upcoming" ? " · upcoming" : ""}`;

  const title = document.createElement("p");
  title.className = "notice__title";
  title.textContent = notice.title;

  const period = document.createElement("span");
  period.className = "notice__when";
  period.textContent = phase === "upcoming"
    ? `From ${when(notice.starts_at)} until ${when(notice.ends_at)}`
    : `Until ${when(notice.ends_at)}`;

  item.append(severity, title, period);
  if (notice.dismissible && notice.severity !== "critical") {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "secondary notice__dismiss";
    button.textContent = "Dismiss";
    button.setAttribute("aria-label", `Dismiss: ${notice.title}`);
    button.addEventListener("click", () => dismiss(notice));
    item.append(button);
  }
  if (notice.body) {
    const body = document.createElement("p");
    body.className = "notice__body";
    body.textContent = notice.body;
    item.append(body);
  }
  return item;
}

function render() {
  if (!banner || !list) return;
  const visible = selectVisibleNotices({
    response: state.response,
    authenticated: state.authenticated,
    serverNowMs: Date.now() + state.offset,
    account: state.account,
    dismissed: new Set(dismissedKeys()),
  });
  // Rebuilt only when what is shown changes, so the once-a-second tick never
  // takes focus away from a Dismiss button somebody is about to press.
  const signature = visible.map(({ notice, phase }) => `${notice.id}:${notice.revision}:${phase}`).join("|");
  if (signature === state.rendered && banner.hidden === (visible.length === 0)) return;
  state.rendered = signature;
  list.replaceChildren(...visible.map(noticeItem));
  banner.hidden = visible.length === 0;
}

// Sign-in and sign-out, as app.js learns of them.
document.addEventListener("aucom:status", (event) => {
  const signedIn = Boolean(event.detail?.authenticated);
  // The first status this module hears may be later than the page's first
  // one (a module runs after the classic scripts), so it is compared with
  // what the last answer was served as rather than taken as the baseline.
  if (state.signedIn === undefined) state.signedIn = state.authenticated;
  if (signedIn === state.signedIn) return;
  state.signedIn = signedIn;
  if (!signedIn && state.response) {
    // Account-only notices go the moment the account does — not at the next
    // answer from a server that may be down.
    state.response = {
      ...state.response,
      visibility: "public",
      notices: state.response.notices.filter((notice) => notice.visibility === "public"),
    };
    state.authenticated = false;
    state.account = "";
  }
  // The ETag was computed for the other visibility.
  state.etag = "";
  render();
  fetchNotices().then(schedule);
});

restoreCache();
render();
window.setInterval(render, 1000);
fetchNotices().then(schedule);
