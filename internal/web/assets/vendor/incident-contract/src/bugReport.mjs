// The user bug report — ONE document, previewed, downloaded and (only on explicit request) published.
//
// ## The rule everything here follows
//
// A report goes to https://github.com/auto-pigeon/bug-reports, which anybody can read forever. So
// the document a user is shown is the document that is downloaded and the document that is filed,
// byte for byte: `buildBugReport` makes it once, and every output — the field-by-field preview, the
// `.txt` and `.json` downloads, the prefilled GitHub form and AUB's server route — is a pure
// rendering of that one value. Nothing is added after consent. AUB re-validates a server-route
// submission against the same rules (bug-report-rules.json) and REFUSES a document that is not
// already canonical, rather than quietly publishing something the user never saw.
//
// ## Structural exclusion first, redaction second
//
// The document is CLOSED (bug-report-1.0.schema.json). An incident travels as its code, severity,
// subsystem and operation — never its message. Recent activity travels as machine names, durations
// and outcomes — never a breadcrumb's data. The machine facts are coarse: a browser family and
// major version, an OS family, a viewport, a primary language subtag. There is no field for a map,
// a map name, an annotation, an asset, an account, an address or a token, so a collector that tried
// to add one would produce an invalid document. The only free text is what the user typed, and it
// goes through the central redaction rules after invisible and direction-changing characters are
// removed.

import { redactText } from "./redact.mjs";
import { isCorrelationId } from "./correlation.mjs";
import { isIncidentCode } from "./codes.mjs";
import { newIncidentId, formatOccurredAt } from "./envelope.mjs";
import { BUG_REPORT_URL } from "./rules.mjs";
import bugReportRules from "../schema/bug-report-rules.json" with { type: "json" };
import bugReportSchema from "../schema/bug-report-1.0.schema.json" with { type: "json" };

export { bugReportRules, bugReportSchema };

/** The versioned identity of the document. */
export const BUG_REPORT_SCHEMA = bugReportRules.document_schema;

/** The one public repository a report may be filed in. Never configurable. */
export const BUG_REPORT_REPOSITORY = bugReportRules.repository;

/** Every bound, from the data file. */
export const BUG_REPORT_LIMITS = Object.freeze({ ...bugReportRules.limits });

const PATTERNS = Object.fromEntries(
  Object.entries(bugReportRules.patterns).map(([name, pattern]) => [name, new RegExp(pattern)]),
);
const ELLIPSIS = bugReportRules.text_rules.ellipsis;

const hex = (code) => `\\u{${code}}`;
const REMOVED = new RegExp(
  `[${bugReportRules.text_rules.removed_ranges.map(([from, to]) => `${hex(from)}-${hex(to)}`).join("")}]`,
  "gu",
);
const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/g;

function cutCodePoints(text, max) {
  const points = Array.from(text);
  if (points.length <= max) return text;
  return points.slice(0, Math.max(0, max - 1)).join("").replace(/[ \t\n]+$/, "") + ELLIPSIS;
}

/**
 * Sanitise one free-text field. The order is part of the contract — bug-report-rules.json says why.
 * Idempotent: `sanitizeReportText(sanitizeReportText(x, n), n) === sanitizeReportText(x, n)`.
 */
export function sanitizeReportText(value, max, { multiline = true } = {}) {
  if (typeof value !== "string") return "";
  let out = value.replace(LONE_SURROGATE, "\uFFFD");
  out = out.replace(/\r\n?|[\u2028\u2029]/g, "\n");
  out = out.replace(REMOVED, "");
  if (!multiline) out = out.replace(/[\n\t]+/g, " ").replace(/ {2,}/g, " ");
  out = redactText(out);
  out = out.replace(/[ \t]+$/gm, "");
  out = out.replace(/\n{3,}/g, "\n\n");
  out = out.replace(/^[ \t\n]+|[ \t\n]+$/g, "");
  return cutCodePoints(out, max);
}

/** Browser family and major version, from a user-agent string. Nothing finer. */
export function coarseBrowser(userAgent) {
  if (typeof userAgent !== "string" || !userAgent) return undefined;
  const rules = [
    [/Edg(?:A|iOS)?\/(\d{1,4})/, "Edge"],
    [/OPR\/(\d{1,4})/, "Opera"],
    [/(?:Firefox|FxiOS)\/(\d{1,4})/, "Firefox"],
    [/(?:Chrome|CriOS)\/(\d{1,4})/, "Chrome"],
    [/Version\/(\d{1,4})[^ ]* (?:Mobile\/\S+ )?Safari\//, "Safari"],
  ];
  for (const [pattern, name] of rules) {
    const match = pattern.exec(userAgent);
    if (match) return `${name} ${Number(match[1])}`;
  }
  return "other";
}

/** Operating-system family, from a user-agent string. */
export function coarseOs(userAgent) {
  if (typeof userAgent !== "string" || !userAgent) return undefined;
  if (/Windows/.test(userAgent)) return "Windows";
  if (/Android/.test(userAgent)) return "Android";
  if (/iPhone|iPad|iPod/.test(userAgent)) return "iOS";
  if (/CrOS/.test(userAgent)) return "ChromeOS";
  if (/Mac OS X|Macintosh/.test(userAgent)) return "macOS";
  if (/Linux/.test(userAgent)) return "Linux";
  return "other";
}

const intIn = (value, min, max) => {
  if (typeof value !== "number" || !Number.isFinite(value)) return undefined;
  const rounded = Math.round(value);
  return rounded >= min && rounded <= max ? rounded : undefined;
};

const isoOrUndefined = (value) => {
  if (value === undefined || value === null) return undefined;
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return undefined;
  const text = formatOccurredAt(at);
  return PATTERNS.created_at.test(text) ? text : undefined;
};

const machineName = (value, pattern, max) => {
  if (typeof value !== "string") return undefined;
  const name = value.trim().toLowerCase().replace(/\s+/g, "_").slice(0, max);
  return pattern.test(name) ? name : undefined;
};

function buildClient(client = {}) {
  const out = {};
  const browser = coarseBrowser(client.userAgent);
  if (browser && PATTERNS.browser.test(browser)) out.browser = browser;
  const os = coarseOs(client.userAgent);
  if (os) out.os = os;
  const width = intIn(client.viewportWidth, 0, 20000);
  const height = intIn(client.viewportHeight, 0, 20000);
  if (width !== undefined && height !== undefined) {
    out.viewport_width = width;
    out.viewport_height = height;
  }
  const ratio = intIn(typeof client.pixelRatio === "number" ? client.pixelRatio * 100 : undefined, 25, 1000);
  if (ratio !== undefined) out.pixel_ratio_pct = ratio;
  const cores = intIn(client.cores, 1, 1024);
  if (cores !== undefined) out.cores = cores;
  const memory = intIn(typeof client.memoryGb === "number" ? client.memoryGb * 1024 : undefined, 0, 1048576);
  if (memory !== undefined) out.memory_mb = memory;
  if (typeof client.language === "string") {
    const primary = client.language.split(/[-_]/)[0].toLowerCase();
    if (PATTERNS.language.test(primary)) out.language = primary;
  }
  if (typeof client.webgl2 === "boolean") out.webgl2 = client.webgl2;
  return out;
}

function buildIncident(incident) {
  if (!incident || typeof incident !== "object") return undefined;
  if (!isCorrelationId(incident.incident_id) || !isIncidentCode(incident.code)) return undefined;
  if (!["warning", "error", "fatal"].includes(incident.severity)) return undefined;
  const out = {
    incident_id: incident.incident_id,
    code: incident.code,
    severity: incident.severity,
    recoverable: incident.recoverable === true,
  };
  const subsystem = machineName(incident.subsystem, PATTERNS.subsystem, 48);
  if (subsystem) out.subsystem = subsystem;
  const operation = typeof incident.operation === "string"
    ? incident.operation.trim().toLowerCase().slice(0, 48)
    : undefined;
  if (operation && PATTERNS.operation.test(operation)) out.operation = operation;
  const occurred = isoOrUndefined(incident.occurred_at);
  if (occurred) out.occurred_at = occurred;
  return out;
}

function buildRecent(entries) {
  const out = [];
  let dropped = 0;
  for (const entry of Array.isArray(entries) ? entries : []) {
    const at = isoOrUndefined(entry?.at);
    const kind = ["incident", "breadcrumb", "operation"].includes(entry?.kind) ? entry.kind : undefined;
    const name = machineName(entry?.name, PATTERNS.recent_name, BUG_REPORT_LIMITS.recent_name);
    if (!at || !kind || !name) {
      dropped += 1;
      continue;
    }
    const item = { at, kind, name };
    const duration = intIn(entry.duration_ms, 0, 86400000);
    if (duration !== undefined) item.duration_ms = duration;
    const outcome = machineName(entry.outcome, PATTERNS.outcome, 24);
    if (outcome) item.outcome = outcome;
    if (isCorrelationId(entry.correlation_id)) item.correlation_id = entry.correlation_id;
    out.push(item);
  }
  return { entries: out, dropped };
}

/**
 * Build the ONE report document.
 *
 * `input` is what a component's collector gathered:
 *   { component, release, environment, reportId?, now?, user: { summary, steps, expected, actual },
 *     incident?, correlationId?, sessionCorrelationId?,
 *     client?: { userAgent, viewportWidth, viewportHeight, pixelRatio, cores, memoryGb, language, webgl2 },
 *     recent?: [{ at, kind, name, duration_ms?, outcome?, correlation_id? }] }
 *
 * Returns `{ ok: true, document, prefill }` or `{ ok: false, errors }` — never throws. `prefill`
 * says whether the document fits GitHub's prefilled-issue URL; to make it fit, the OLDEST recent
 * entries are omitted from the document itself (and counted in `omitted_recent`), so the preview,
 * the download and the prefilled issue are still one document.
 */
export function buildBugReport(input = {}) {
  const errors = [];
  if (!bugReportRules.components.includes(input.component)) errors.push("component_invalid");
  const summary = sanitizeReportText(input.user?.summary, BUG_REPORT_LIMITS.summary, { multiline: false });
  if (!summary) errors.push("summary_required");
  const reportId = input.reportId ?? newIncidentId();
  if (!PATTERNS.report_id.test(reportId)) errors.push("report_id_invalid");
  const createdAt = isoOrUndefined(input.now ?? Date.now());
  if (!createdAt) errors.push("created_at_invalid");
  if (errors.length) return { ok: false, errors };

  const release = typeof input.release === "string" && PATTERNS.release.test(input.release) ? input.release : "unknown";
  const environment = typeof input.environment === "string" && PATTERNS.environment.test(input.environment)
    ? input.environment
    : "unknown";
  const incident = buildIncident(input.incident);
  const correlation = isCorrelationId(input.correlationId)
    ? input.correlationId
    : isCorrelationId(input.incident?.correlation_id) ? input.incident.correlation_id : undefined;

  const document = {
    schema: BUG_REPORT_SCHEMA,
    report_id: reportId,
    created_at: createdAt,
    component: input.component,
    release,
    environment,
    kind: incident ? "incident" : "cold",
    user: {
      summary,
      steps: sanitizeReportText(input.user?.steps, BUG_REPORT_LIMITS.steps),
      expected: sanitizeReportText(input.user?.expected, BUG_REPORT_LIMITS.expected),
      actual: sanitizeReportText(input.user?.actual, BUG_REPORT_LIMITS.actual),
    },
    client: buildClient(input.client),
    recent: [],
    omitted_recent: 0,
  };
  if (incident) document.incident = incident;
  if (correlation) document.correlation_id = correlation;
  if (isCorrelationId(input.sessionCorrelationId)) document.session_correlation_id = input.sessionCorrelationId;

  const recent = buildRecent(input.recent);
  const keep = recent.entries.slice(-BUG_REPORT_LIMITS.recent);
  document.recent = keep;
  document.omitted_recent = recent.dropped + (recent.entries.length - keep.length);

  // Fit the prefilled URL by omitting the oldest activity FROM THE DOCUMENT, so there is still
  // only one document. A report whose own text is too long for a URL stays whole and says so.
  while (prefilledIssueUrl(document).length > BUG_REPORT_LIMITS.prefill_url && document.recent.length) {
    document.recent = document.recent.slice(1);
    document.omitted_recent += 1;
  }
  const fits = prefilledIssueUrl(document).length <= BUG_REPORT_LIMITS.prefill_url;
  return { ok: true, document, prefill: { fits } };
}

const sortKeys = (value) => {
  if (Array.isArray(value)) return value.map(sortKeys);
  if (value === null || typeof value !== "object") return value;
  return Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortKeys(value[key])]));
};

/**
 * The canonical JSON form: keys sorted at every level, no whitespace. This is what the `.json`
 * download contains (pretty-printed by `reportJsonDownload`) and what a server compares against.
 */
export function canonicalReportJson(document) {
  return JSON.stringify(sortKeys(document));
}

/** The `.json` download: the canonical document, indented for a human, plus a trailing newline. */
export function reportJsonDownload(document) {
  return `${JSON.stringify(sortKeys(document), null, 2)}\n`;
}

/**
 * Validate a document against the closed schema AND the text rules — returns `{ valid, errors }`,
 * never throws. A text field that is not already canonical (sanitising it would change it) is an
 * error: that is how a server refuses to publish something the user was not shown.
 */
export function validateBugReport(document) {
  const errors = [];
  const fail = (code) => errors.push(code);
  const isObject = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
  const closed = (value, allowed, path) => {
    for (const key of Object.keys(value)) if (!allowed.includes(key)) fail(`${path}.${key}:not_allowed`);
  };
  if (!isObject(document)) return { valid: false, errors: ["document:not_object"] };
  closed(document, Object.keys(bugReportSchema.properties), "document");
  for (const key of bugReportSchema.required) if (!(key in document)) fail(`document.${key}:required`);
  if (document.schema !== BUG_REPORT_SCHEMA) fail("schema:unsupported");
  if (!PATTERNS.report_id.test(String(document.report_id))) fail("report_id:invalid");
  if (!PATTERNS.created_at.test(String(document.created_at))) fail("created_at:invalid");
  if (!bugReportRules.components.includes(document.component)) fail("component:invalid");
  if (!PATTERNS.release.test(String(document.release))) fail("release:invalid");
  if (!PATTERNS.environment.test(String(document.environment))) fail("environment:invalid");
  if (!["cold", "incident"].includes(document.kind)) fail("kind:invalid");
  if ((document.kind === "incident") !== isObject(document.incident)) fail("kind:mismatch");

  const user = document.user;
  if (!isObject(user)) fail("user:invalid");
  else {
    closed(user, ["summary", "steps", "expected", "actual"], "user");
    const fields = [["summary", false], ["steps", true], ["expected", true], ["actual", true]];
    for (const [field, multiline] of fields) {
      const value = user[field];
      if (typeof value !== "string") { fail(`user.${field}:invalid`); continue; }
      if (sanitizeReportText(value, BUG_REPORT_LIMITS[field], { multiline }) !== value) fail(`user.${field}:not_canonical`);
    }
    if (user.summary === "") fail("user.summary:required");
  }

  if (document.incident !== undefined) {
    const incident = document.incident;
    if (!isObject(incident)) fail("incident:invalid");
    else {
      closed(incident, Object.keys(bugReportSchema.properties.incident.properties), "incident");
      if (!isCorrelationId(incident.incident_id)) fail("incident.incident_id:invalid");
      if (!isIncidentCode(incident.code)) fail("incident.code:unknown");
      if (!["warning", "error", "fatal"].includes(incident.severity)) fail("incident.severity:invalid");
      if (typeof incident.recoverable !== "boolean") fail("incident.recoverable:invalid");
      if (incident.subsystem !== undefined && !PATTERNS.subsystem.test(String(incident.subsystem))) fail("incident.subsystem:invalid");
      if (incident.operation !== undefined && !PATTERNS.operation.test(String(incident.operation))) fail("incident.operation:invalid");
      if (incident.occurred_at !== undefined && !PATTERNS.created_at.test(String(incident.occurred_at))) fail("incident.occurred_at:invalid");
    }
  }
  for (const key of ["correlation_id", "session_correlation_id"]) {
    if (document[key] !== undefined && !isCorrelationId(document[key])) fail(`${key}:invalid`);
  }

  const client = document.client;
  if (!isObject(client)) fail("client:invalid");
  else {
    const spec = bugReportSchema.properties.client.properties;
    closed(client, Object.keys(spec), "client");
    for (const [key, value] of Object.entries(client)) {
      const rule = spec[key];
      if (!rule) continue;
      if (rule.type === "integer" && !(Number.isInteger(value) && value >= rule.minimum && value <= rule.maximum)) fail(`client.${key}:invalid`);
      if (rule.type === "boolean" && typeof value !== "boolean") fail(`client.${key}:invalid`);
      if (rule.type === "string" && !(typeof value === "string" && new RegExp(rule.pattern).test(value))) fail(`client.${key}:invalid`);
      if (rule.enum && !rule.enum.includes(value)) fail(`client.${key}:invalid`);
    }
  }

  if (!Array.isArray(document.recent) || document.recent.length > BUG_REPORT_LIMITS.recent) fail("recent:invalid");
  else {
    document.recent.forEach((item, index) => {
      if (!isObject(item)) { fail(`recent.${index}:invalid`); return; }
      closed(item, ["at", "kind", "name", "duration_ms", "outcome", "correlation_id"], `recent.${index}`);
      if (!PATTERNS.created_at.test(String(item.at))) fail(`recent.${index}.at:invalid`);
      if (!["incident", "breadcrumb", "operation"].includes(item.kind)) fail(`recent.${index}.kind:invalid`);
      if (!PATTERNS.recent_name.test(String(item.name))) fail(`recent.${index}.name:invalid`);
      if (item.duration_ms !== undefined && !(Number.isInteger(item.duration_ms) && item.duration_ms >= 0 && item.duration_ms <= 86400000)) fail(`recent.${index}.duration_ms:invalid`);
      if (item.outcome !== undefined && !PATTERNS.outcome.test(String(item.outcome))) fail(`recent.${index}.outcome:invalid`);
      if (item.correlation_id !== undefined && !isCorrelationId(item.correlation_id)) fail(`recent.${index}.correlation_id:invalid`);
    });
  }
  if (!(Number.isInteger(document.omitted_recent) && document.omitted_recent >= 0 && document.omitted_recent <= 100000)) fail("omitted_recent:invalid");
  if (new TextEncoder().encode(canonicalReportJson(document)).length > BUG_REPORT_LIMITS.document_bytes) fail("document:too_large");
  return { valid: errors.length === 0, errors };
}

const orNotGiven = (text) => (text ? text : "(not given)");
const pad = (label) => label.padEnd(14);
const heading = (title) => [title, "-".repeat(title.length)];

/**
 * The canonical plain text — the `.txt` download, and the fenced body of both issue forms.
 * A pure function of the document.
 */
export function renderReportText(document) {
  const d = document;
  const lines = [
    `Auto-Pigeon bug report (${d.schema})`,
    `If you submit it, this report is published PUBLICLY at https://github.com/${BUG_REPORT_REPOSITORY}.`,
    "",
    `${pad("report")}${d.report_id}`,
    `${pad("created")}${d.created_at}`,
    `${pad("component")}${d.component}`,
    `${pad("release")}${d.release}`,
    `${pad("environment")}${d.environment}`,
    `${pad("kind")}${d.kind}`,
  ];
  if (d.correlation_id) lines.push(`${pad("correlation")}${d.correlation_id}`);
  if (d.session_correlation_id) lines.push(`${pad("session")}${d.session_correlation_id}`);
  lines.push("", ...heading("Summary"), d.user.summary);
  lines.push("", ...heading("Steps to reproduce"), orNotGiven(d.user.steps));
  lines.push("", ...heading("Expected result"), orNotGiven(d.user.expected));
  lines.push("", ...heading("Actual result"), orNotGiven(d.user.actual));
  if (d.incident) {
    const i = d.incident;
    lines.push("", ...heading("Incident"));
    lines.push(`${pad("incident")}${i.incident_id}`, `${pad("code")}${i.code}`, `${pad("severity")}${i.severity}`);
    if (i.subsystem) lines.push(`${pad("subsystem")}${i.subsystem}`);
    if (i.operation) lines.push(`${pad("operation")}${i.operation}`);
    if (i.occurred_at) lines.push(`${pad("occurred at")}${i.occurred_at}`);
    lines.push(`${pad("recoverable")}${i.recoverable ? "yes" : "no"}`);
  }
  const c = d.client;
  const clientLines = [];
  if (c.browser) clientLines.push(`${pad("browser")}${c.browser}`);
  if (c.os) clientLines.push(`${pad("os")}${c.os}`);
  if (c.viewport_width !== undefined) clientLines.push(`${pad("viewport")}${c.viewport_width}x${c.viewport_height}`);
  if (c.pixel_ratio_pct !== undefined) clientLines.push(`${pad("pixel ratio")}${c.pixel_ratio_pct}%`);
  if (c.cores !== undefined) clientLines.push(`${pad("cores")}${c.cores}`);
  if (c.memory_mb !== undefined) clientLines.push(`${pad("memory")}${c.memory_mb} MB`);
  if (c.language) clientLines.push(`${pad("language")}${c.language}`);
  if (c.webgl2 !== undefined) clientLines.push(`${pad("webgl2")}${c.webgl2 ? "yes" : "no"}`);
  if (clientLines.length) lines.push("", ...heading("Client"), ...clientLines);
  const omitted = d.omitted_recent ? `; ${d.omitted_recent} older or unusable entries omitted` : "";
  lines.push("", ...heading(`Recent activity (${d.recent.length}, oldest first${omitted})`));
  if (!d.recent.length) lines.push("(none)");
  for (const r of d.recent) {
    const parts = [r.at.slice(11, 23), r.kind.padEnd(10), r.name];
    if (r.duration_ms !== undefined) parts.push(`${r.duration_ms} ms`);
    if (r.outcome) parts.push(r.outcome);
    if (r.correlation_id) parts.push(r.correlation_id);
    lines.push(parts.join("  "));
  }
  lines.push(
    "",
    "Contains no map, map name, annotation, chat, asset, account, e-mail address, network address or credential.",
  );
  return `${lines.join("\n")}\n`;
}

/** The fence that no run of backticks inside `text` can close. */
function fenceFor(text) {
  let longest = 0;
  for (const match of text.matchAll(/`+/g)) longest = Math.max(longest, match[0].length);
  return "`".repeat(Math.max(3, longest + 1));
}

/**
 * The issue: `{ title, body }`. `route` is `"prefilled"` (the user files it on GitHub) or
 * `"server"` (AUB files it for a signed-in user). The whole report sits inside ONE code fence that
 * nothing in it can close, so no Markdown, HTML, mention or issue reference a user typed is
 * interpreted by GitHub — it is shown exactly as it was previewed.
 */
export function renderIssue(document, { route = "prefilled" } = {}) {
  const text = renderReportText(document);
  const fence = fenceFor(text);
  const via = route === "server"
    ? "Filed by Auto-Pigeon's server on behalf of a signed-in user who reviewed this exact text."
    : "Filed by the reporter from Auto-Pigeon's prefilled form, after reviewing this exact text.";
  const body = [
    `<!-- ${BUG_REPORT_SCHEMA} ${document.report_id} -->`,
    via,
    "",
    `${fence}text`,
    text.replace(/\n$/, ""),
    fence,
    "",
  ].join("\n");
  const title = cutCodePoints(`[${document.component}] ${document.user.summary}`, BUG_REPORT_LIMITS.title);
  return { title, body };
}

/** GitHub's documented prefilled new-issue URL for this document. Opens a form; submits nothing. */
export function prefilledIssueUrl(document) {
  const { title, body } = renderIssue(document, { route: "prefilled" });
  return `${BUG_REPORT_URL}/new?${new URLSearchParams({ title, body }).toString()}`;
}

/** The filenames of the two downloads, from the report id so a maintainer can match them up. */
export function reportDownloadNames(document) {
  const stem = `auto-pigeon-bug-report-${document.component.toLowerCase()}-${document.report_id.slice(0, 12)}`;
  return { text: `${stem}.txt`, json: `${stem}.json` };
}
