// Report a bug — AUG's dialog, in the Companion (operator, 2026-09-22), laid
// out as AUP's corrected report (NEW_253).
//
// The document is built ONCE by the shared incident contract, vendored byte for
// byte under vendor/incident-contract, and every control below renders that one
// value: the field-by-field preview, the exact text, both downloads, the
// prefilled GitHub issue and the body AUB files. So what the person reviewed is
// what is published.
//
// Two steps, one skeleton (NEW_253, after AUP's `BugReportDialog.tsx`):
//
//   Write — the public warning; what it is about (only when this Companion
//   raised something); one classification band: the application, a printed
//   fact, the Type as a radio group (Bug on a fresh report) and the Area,
//   which a cold report must choose; then Summary, Steps, Expected, Actual,
//   each with its hint, whose labels follow the type. Footer: Close bottom
//   left, Review report bottom right. Review stays disabled, saying why, until
//   an area is chosen and a summary written. Nothing is built until Review.
//
//   Review — the public warning; the field list (the three GitHub labels as
//   chips first); the exact text; "How to file it": Download .txt, Download
//   .json and Open prefilled GitHub issue in one row, their notes, the one
//   route line, the send result. Footer: Close and Edit bottom left; bottom
//   right the public-consent tick together with Send through Auto-Pigeon,
//   offered only when the server route is (AUB configured AND signed in) and
//   the report has not reached a final outcome.
//
// Close never sends. Edit keeps the draft — the words, the type (a deliberate
// Feature request stays one) and the area — and is offered only until a send
// was attempted, so a retry sends the very document the first attempt did.
// The report never carries a map, a path, a profile, an account or a token:
// the contract has no field for them.
//
// Every classification rule — types, areas, the incident's starting area, the
// headings, the labels — is the contract's, through bugreport-model.mjs, which
// also holds this dialog's DOM-free decisions (the Bug default, why Review is
// blocked, the titles, the route line and when Send is offered) so node can
// exercise them in bugreport_model_test.go.

import {
  BUG_REPORT_REPOSITORY,
  newIncidentId,
  prefilledIssueUrl,
  renderReportText,
  reportDownloadNames,
  reportJsonDownload,
} from "./vendor/incident-contract/src/index.mjs";
import {
  APPLICATION,
  ROUTE_LINES,
  areaChoices,
  buildReport,
  dialogTitle,
  fieldHints,
  fieldLabels,
  initialClassification,
  reportLabels,
  reportTypeChoices,
  reviewBlocked,
  serverRoute,
  suggestedArea,
} from "./bugreport-model.mjs";

const { $, el, api } = window.AUCOM;
const t = (english, values) => window.AUCOM.t(english, values);

const dialog = $("bug-dialog");
const body = $("bug-body");
const title = $("bug-title");
const repository = `https://github.com/${BUG_REPORT_REPOSITORY}`;
const shownRepository = `github.com/${BUG_REPORT_REPOSITORY}`;

let fields = { summary: "", steps: "", expected: "", actual: "" };
let reportId = "";
let built = null;
// "idle" until the first press of Send; "sent" after it (Edit is withdrawn).
let sendState = "idle";
// True once AUB's answer was final — filed, outcome unknown, reused,
// unavailable: Send is not offered again for this report.
let sendFinal = false;
// True while one POST is in flight: a second press sends nothing.
let sending = false;
// What the report is about and how it is classified. `incident` is an entry of
// GET /api/v1/bug-reports/incidents, or null for a cold report; `incidents`
// is that list, for the dialog's "What it is about" choice.
let incident = null;
let incidents = [];
let reportType = "";
let area = "";

function clientFacts() {
  const client = {};
  if (typeof navigator.userAgent === "string") client.userAgent = navigator.userAgent;
  if (typeof navigator.hardwareConcurrency === "number") client.cores = navigator.hardwareConcurrency;
  if (typeof navigator.deviceMemory === "number") client.memoryGb = navigator.deviceMemory;
  if (typeof navigator.language === "string") client.language = navigator.language;
  client.viewportWidth = window.innerWidth;
  client.viewportHeight = window.innerHeight;
  if (typeof window.devicePixelRatio === "number") client.pixelRatio = window.devicePixelRatio;
  return client;
}

function build() {
  const status = window.AUCOM.status || {};
  return buildReport({
    reportType,
    area,
    fields,
    incident,
    reportId,
    release: /^1\.\d+$/.test(status.version || "") ? status.version : "unknown",
    environment: status.incident_environment || "unknown",
    client: clientFacts(),
  });
}

function setTitle(step, type) {
  title.textContent = t(dialogTitle(step, type));
}

function warning() {
  return el("p", {
    className: "bug-warning",
    attrs: { role: "note", id: "bug-warning" },
    text: t("If you file this report it becomes PUBLIC at {repository}, where anyone can read it, permanently. Do not type anything private into it.",
      { repository: shownRepository }),
  });
}

function buttonTo(label, onClick, className = "", id = null) {
  const button = el("button", { text: label, className, attrs: { type: "button", id } });
  button.addEventListener("click", onClick);
  return button;
}

// The one footer both steps share: a top border, the left side, a gap that
// pushes whatever follows hard right.
function footer(left, right) {
  return el("div", {
    className: "bug-foot",
    children: [...left, el("span", { className: "bug-foot__gap", attrs: { "aria-hidden": "true" } }), ...right],
  });
}

function field(name, multiline, rows) {
  const id = "bug-field-" + name;
  const hintId = "bug-hint-" + name;
  const input = multiline
    ? el("textarea", { attrs: { id, rows: String(rows), "aria-describedby": hintId } })
    : el("input", { attrs: { id, type: "text", maxlength: "400", required: "", "aria-describedby": hintId } });
  input.value = fields[name];
  input.addEventListener("input", () => {
    fields[name] = input.value;
    if (name === "summary") relabel();
  });
  const caption = el("label", { attrs: { for: id } });
  const hint = el("small", { className: "hint bug-hint", attrs: { id: hintId } });
  return { node: el("div", { className: "field", children: [caption, input, hint] }), caption, hint };
}

// A short, human description of an incident this process raised — display
// only; its area comes from the contract, never from these words.
function describeIncident(entry) {
  const at = typeof entry.occurred_at === "string" ? new Date(entry.occurred_at) : null;
  const time = at && !Number.isNaN(at.getTime()) ? at.toLocaleTimeString() : "";
  const what = entry.code === "aucom.job_failed" ? t("A job failed")
    : entry.code === "aucom.readiness_failed" ? t("The Auto-Pigeon server did not pass its readiness check")
      : t("Incident {code}", { code: entry.code });
  return [what, entry.operation, time].filter(Boolean).join(" · ");
}

function setIncident(next) {
  incident = next;
  // A report about an incident starts where the contract says it does; a cold
  // one starts as a Bug with no area (NEW_253). Either way the person can
  // change both.
  ({ reportType, area } = initialClassification(incident));
}

// The classification band: the application (a fact), the Type (a radio
// group) and the Area, in one row that wraps to one column when narrow.
function classification() {
  const wrap = el("div", { className: "bug-classify", attrs: { id: "bug-classification" } });

  wrap.append(el("div", {
    className: "field bug-fact",
    attrs: { id: "bug-application" },
    children: [
      el("span", { className: "bug-fact__term", text: t("Application") }),
      el("strong", { className: "bug-fact__value", text: APPLICATION, attrs: { translate: "no" } }),
      el("small", { className: "hint", text: t("Every report from the Companion is filed as {application}.", { application: APPLICATION }) }),
    ],
  }));

  const types = el("div", { className: "bug-types" });
  for (const choice of reportTypeChoices()) {
    const radio = el("input", { attrs: { type: "radio", name: "bug-type", id: "bug-type-" + choice.id, value: choice.id } });
    radio.checked = reportType === choice.id;
    radio.addEventListener("change", () => {
      if (!radio.checked) return;
      reportType = choice.id;
      relabel();
    });
    types.append(el("label", { className: "check", attrs: { for: radio.id }, children: [radio, document.createTextNode(" " + t(choice.name))] }));
  }
  wrap.append(el("fieldset", { className: "field bug-type", attrs: { id: "bug-type" }, children: [el("legend", { text: t("Type") }), types] }));

  const select = el("select", { attrs: { id: "bug-area", "aria-describedby": "bug-suggested" } });
  select.append(el("option", { text: t("Choose an area"), attrs: { value: "" } }));
  for (const choice of areaChoices()) {
    select.append(el("option", { text: t(choice.name), attrs: { value: choice.id } }));
  }
  select.value = area;
  select.addEventListener("change", () => { area = select.value; relabel(); });
  const suggested = el("small", { className: "hint", attrs: { id: "bug-suggested" } });
  wrap.append(el("div", { className: "field", children: [el("label", { text: t("Area"), attrs: { for: "bug-area" } }), select, suggested] }));
  return { node: wrap, suggested };
}

let relabel = () => {};

function compose(message) {
  built = null;
  const form = el("form", { className: "bug-form", attrs: { id: "bug-compose" } });

  // "What it is about": offered only when this Companion raised something. Choosing an
  // incident preselects the contract's type and area; choosing nothing makes
  // it a new report: a Bug, with the area still to choose.
  if (incidents.length || incident) {
    const about = el("select", { attrs: { id: "bug-incident" } });
    about.append(el("option", { text: t("Nothing in particular: a new report"), attrs: { value: "" } }));
    const offered = incident && !incidents.some((entry) => entry.incident_id === incident.incident_id) ? [incident, ...incidents] : incidents;
    for (const entry of offered) {
      about.append(el("option", { text: describeIncident(entry), attrs: { value: entry.incident_id } }));
    }
    about.value = incident ? incident.incident_id : "";
    about.addEventListener("change", () => {
      setIncident(offered.find((entry) => entry.incident_id === about.value) || null);
      compose();
      $("bug-incident")?.focus();
    });
    form.append(el("div", { className: "field", children: [el("label", { text: t("What it is about"), attrs: { for: "bug-incident" } }), about] }));
  }
  if (incident) {
    form.append(el("p", {
      className: "bug-note",
      attrs: { id: "bug-about" },
      text: t("This report is about the incident {code}. Its code, severity and correlation id are included; its message is not.", { code: incident.code }),
    }));
  }

  const band = classification();
  form.append(band.node);

  const summary = field("summary", false);
  const steps = field("steps", true, 4);
  const expected = field("expected", true, 2);
  const actual = field("actual", true, 2);
  form.append(summary.node, steps.node, expected.node, actual.node);
  if (message) form.append(el("p", { className: "message error", attrs: { role: "alert" }, text: message }));

  const why = el("p", { className: "bug-note", attrs: { id: "bug-review-why", role: "status" } });
  form.append(why, el("p", {
    className: "bug-note",
    attrs: { id: "bug-collected" },
    text: t("The Companion adds its release, the environment, the incident's code and correlation id when the report is about one, and coarse browser facts. It never adds a map, a path, a profile, an account, an e-mail address, a network address or a credential. You will see all of it before anything can be filed."),
  }));

  const submit = el("button", { text: t("Review report"), className: "primary", attrs: { type: "submit", id: "bug-review", "aria-describedby": "bug-review-why" } });
  form.append(footer([buttonTo(t("Close"), () => dialog.close(), "", "bug-foot-close")], [submit]));

  // The labels and hints follow the type; the title follows it too; Review
  // waits for the area and the summary, and says which is missing.
  const suggestion = suggestedArea(incident);
  relabel = () => {
    const labels = fieldLabels(reportType);
    const hints = fieldHints(reportType);
    for (const [key, part] of Object.entries({ summary, steps, expected, actual })) {
      part.caption.textContent = t(labels[key]);
      part.hint.textContent = t(hints[key]);
    }
    band.suggested.textContent = incident && suggestion && area === suggestion
      ? t("Suggested from the incident. Change it if another area fits better.") : "";
    band.suggested.hidden = !band.suggested.textContent;
    setTitle("compose", reportType);
    const reason = reviewBlocked({ reportType, area, summary: fields.summary });
    submit.disabled = Boolean(reason);
    if (reason) submit.title = t(reason);
    else submit.removeAttribute("title");
    const text = reason ? t(reason) : "";
    if (why.textContent !== text) why.textContent = text;
    why.hidden = !text;
  };
  relabel();

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (reviewBlocked({ reportType, area, summary: fields.summary })) {
      relabel();
      return;
    }
    const result = build();
    if (!result.ok) {
      const codes = result.errors.join(", ");
      compose(result.errors.includes("component_invalid")
        ? t("The shared bug-report contract does not accept reports from the Companion yet, so this report cannot be built. Tell the operator: \"AUCOM\" must be added to its components.")
        : t("The report could not be built ({codes}).", { codes }));
      return;
    }
    built = result;
    review();
  });
  body.replaceChildren(warning(), form);
  focusCompose(message);
}

// Where the person has something to do first: the area of a cold report, else
// the words (and the words after a message about them).
function focusCompose(message) {
  body.querySelector(message || area ? "#bug-field-summary" : "#bug-area")?.focus();
}

function save(filename, text, type) {
  const href = URL.createObjectURL(new Blob([text], { type }));
  const anchor = el("a", { attrs: { href, download: filename } });
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(href), 0);
}

function review() {
  const document_ = built.document;
  setTitle("review", document_.report_type);
  const text = renderReportText(document_);
  const names = reportDownloadNames(document_);
  const orNot = (value) => value || t("(not given)");
  // The headings of the type the DOCUMENT says, and the labels the contract
  // derives from it — not from the controls, which the person may have
  // changed and not yet reviewed.
  const headings = fieldLabels(document_.report_type);
  const labels = reportLabels(document_) || [];
  const client = document_.client || {};
  const viewport = client.viewport_width !== undefined ? `${client.viewport_width}x${client.viewport_height}` : "";

  // The label names themselves, never translated: they are what the issue shows.
  const chips = el("ul", {
    className: "bug-labels",
    attrs: { id: "bug-labels", "aria-label": t("GitHub labels"), translate: "no" },
    children: labels.map((label) => el("li", { text: label })),
  });
  const rows = [
    [t("GitHub labels"), chips],
    [t(headings.summary), document_.user.summary],
    [t(headings.steps), orNot(document_.user.steps)],
    [t(headings.expected), orNot(document_.user.expected)],
    [t(headings.actual), orNot(document_.user.actual)],
    [t("Version"), `${document_.component} ${document_.release} (${document_.environment})`],
    ...(document_.incident ? [[t("Incident"), [document_.incident.code, document_.incident.severity, document_.incident.operation].filter(Boolean).join(" · ")]] : []),
    ...(document_.correlation_id ? [[t("Correlation"), document_.correlation_id]] : []),
    [t("Browser"), [client.browser, client.os, viewport, client.language].filter(Boolean).join(" · ") || t("(not given)")],
    [t("Recent activity"), t("{count} entries; {omitted} older or unusable entries omitted",
      { count: Array.isArray(document_.recent) ? document_.recent.length : 0, omitted: document_.omitted_recent ?? 0 })],
  ];
  // What the person typed is shown as typed: never run through translation.
  const list = el("dl", {
    className: "summary-list bug-fields",
    attrs: { id: "bug-fields" },
    children: rows.flatMap(([term, value]) => [
      el("dt", { text: term }),
      typeof value === "string" ? el("dd", { text: value, attrs: { translate: "no" } }) : el("dd", { children: [value] }),
    ]),
  });

  const exact = [
    el("h3", { text: t("Exact text") }),
    el("pre", { className: "output bug-text", attrs: { id: "bug-text", tabindex: "0", "aria-label": t("Exact text") }, text }),
  ];

  // How to file it: the three ways the person files it themselves, their
  // notes, the one line about the server route, and what a send answered.
  const downloaded = el("p", { className: "bug-note", attrs: { id: "bug-downloaded", role: "status" } });
  downloaded.hidden = true;
  const noteDownloaded = () => {
    downloaded.textContent = t("Downloaded. The file is exactly the text shown above.");
    downloaded.hidden = false;
  };
  const opened = el("p", { className: "bug-note", attrs: { id: "bug-issue-opened", role: "status" } });
  opened.hidden = true;
  const fits = Boolean(built.prefill?.fits);
  const tooLong = t("This report is too long for GitHub's prefilled form. Download it and attach the file to a new issue instead.");
  let issue;
  if (fits) {
    issue = el("a", {
      className: "button-link",
      text: t("Open prefilled GitHub issue"),
      attrs: { id: "bug-open-issue", href: prefilledIssueUrl(document_), target: "_blank", rel: "noopener noreferrer" },
    });
    issue.addEventListener("click", () => {
      opened.textContent = t("GitHub's form opened in a new tab with this exact text. The report is filed only when you press Submit on GitHub.");
      opened.hidden = false;
    });
  } else {
    issue = el("button", { text: t("Open prefilled GitHub issue"), attrs: { type: "button", id: "bug-open-issue", title: tooLong } });
    issue.disabled = true;
  }
  const actions = el("div", {
    className: "bug-file-actions",
    children: [
      buttonTo(t("Download .txt"), () => { save(names.text, text, "text/plain"); noteDownloaded(); }, "", "bug-download-txt"),
      buttonTo(t("Download .json"), () => { save(names.json, reportJsonDownload(document_), "application/json"); noteDownloaded(); }, "", "bug-download-json"),
      issue,
    ],
  });
  const notes = [downloaded];
  if (fits) {
    // GitHub honours a prefilled `labels` parameter only for someone allowed
    // to label issues there; the server route is the one whose labels are
    // guaranteed. Said beside the button, rather than implied.
    notes.push(el("p", {
      className: "bug-note",
      attrs: { id: "bug-labels-prefilled" },
      text: t("The prefilled issue asks GitHub for the labels {labels}. GitHub applies them only when your account may label issues in {repository}; otherwise the issue arrives without them. Sending through Auto-Pigeon always applies them.",
        { labels: labels.join(", "), repository: shownRepository }),
    }));
  } else {
    notes.push(el("p", { className: "bug-note", attrs: { id: "bug-too-long" }, text: tooLong }));
  }
  notes.push(opened);
  const routeLine = el("p", { className: "bug-note bug-route-line", attrs: { id: "bug-route" }, text: t(ROUTE_LINES.checking) });
  const outcome = el("p", { className: "message bug-result", attrs: { id: "bug-result", role: "status", tabindex: "-1" } });
  const how = el("section", {
    className: "bug-routes",
    attrs: { "aria-labelledby": "bug-routes-title" },
    children: [el("h3", { text: t("How to file it"), attrs: { id: "bug-routes-title" } }), actions, ...notes, routeLine, outcome],
  });

  // The footer: Close and Edit bottom left; the consent tick and Send bottom
  // right, drawn once the route is known to be offered.
  const close = buttonTo(t("Close"), () => dialog.close(), "", "bug-foot-close");
  const edit = sendState === "idle" ? buttonTo(t("Edit"), () => compose(), "", "bug-edit") : null;
  const group = el("div", { className: "bug-send", attrs: { id: "bug-send-group" } });
  group.hidden = true;

  body.replaceChildren(warning(), list, ...exact, how, footer(edit ? [close, edit] : [close], [group]));
  title.focus();

  routeStatus().then((route) => {
    const state = serverRoute({ route, authenticated: Boolean(window.AUCOM.status?.authenticated), final: sendFinal });
    routeLine.textContent = t(ROUTE_LINES[state.state]);
    if (state.sendOffered) drawSend(group, document_, { outcome, close, edit });
  });
}

async function routeStatus() {
  const { ok, body: answer } = await api("/api/v1/bug-reports/status");
  if (!ok) return "unknown";
  return answer.server_route === "available" ? "available" : answer.server_route === "unavailable" ? "unavailable" : "unknown";
}

// The bottom-right group: the consent sentence first, then its box, then the
// one button it gates — together, right-aligned, so the tick cannot be read as
// being about the downloads. Unticked, Send is disabled and says why.
function drawSend(group, document_, { outcome, close, edit }) {
  const ack = el("input", { attrs: { type: "checkbox", id: "bug-ack" } });
  const consent = el("label", {
    className: "check bug-consent",
    attrs: { for: "bug-ack" },
    children: [el("span", { text: t("I understand this report will be public on GitHub, for anyone to read.") }), ack],
  });
  const send = el("button", {
    text: sendState === "idle" ? t("Send through Auto-Pigeon") : t("Send again"),
    className: "primary",
    attrs: { type: "button", id: "bug-send" },
  });
  const gate = () => {
    send.disabled = sending || !ack.checked;
    if (ack.checked) send.removeAttribute("title");
    else send.title = t("Confirm that you understand the report is public first.");
  };
  ack.addEventListener("change", gate);
  gate();
  send.addEventListener("click", async () => {
    if (sending || sendFinal || !ack.checked) return;
    sending = true;
    gate();
    ack.disabled = true;
    close.disabled = true;
    if (edit) edit.disabled = true;
    send.textContent = t("Sending…");
    sendState = "sent";
    const { status, body: answer } = await api("/api/v1/bug-reports", {
      method: "POST",
      body: { document: document_, confirm: true },
    });
    const final = showOutcome(outcome, status, answer);
    sending = false;
    close.disabled = false;
    // Edit is offered only until a send was attempted: a retry must send the
    // very document the first attempt did.
    edit?.remove();
    if (final) {
      sendFinal = true;
      group.replaceChildren();
      group.hidden = true;
      outcome.focus();
    } else {
      send.textContent = t("Send again");
      ack.disabled = false;
      gate();
    }
    outcome.scrollIntoView?.({ block: "nearest" });
  });
  group.replaceChildren(consent, send);
  group.hidden = false;
}

// AUB's answer, as AUG maps it (bugReportRoute.ts `outcomeFor`). Returns true
// when the Send control must be withdrawn: filed, outcome unknown, reused,
// unavailable — never a blind retry.
function showOutcome(node, status, answer) {
  const code = answer?.data?.reason?.code ?? answer?.code ?? null;
  const set = (text, kind = "") => { node.className = "message" + (kind ? " " + kind : ""); node.replaceChildren(text); };
  if (status === 200 || status === 201) {
    const number = Number.isInteger(answer?.number) ? answer.number : null;
    if (number === null) {
      set(t("It may or may not have been filed. Check the tracker before sending it again; it will not be retried automatically."));
      return true;
    }
    const url = typeof answer.url === "string" && answer.url.startsWith(repository + "/issues/") ? answer.url : null;
    set(answer.replayed ? t("This report was already filed as issue #{number}.", { number }) : t("Filed as issue #{number}.", { number }), "ok");
    if (url) node.append(" ", el("a", { text: t("Open issue #{number}", { number }), attrs: { href: url, target: "_blank", rel: "noopener noreferrer" } }));
    return true;
  }
  if (status === 401) { set(t("Your session has ended. Log in again and send: the same report, with the same id, is kept."), "error"); return false; }
  if (status === 404 || (status === 503 && (code === null || code === "bug_reporting_unavailable"))) {
    set(t("Filing through Auto-Pigeon is not enabled on this server. Nothing was sent; you can still keep a copy or file it yourself on GitHub."));
    return true;
  }
  if (code === "bug_report_outcome_unknown" || status === 0) {
    set(t("It may or may not have been filed. Check the tracker before sending it again; it will not be retried automatically."));
    return true;
  }
  if (status === 409 && code === "bug_report_in_progress") { set(t("This report is already being sent. Wait a moment before sending again.")); return false; }
  if (status === 409 && code === "report_id_reused") { set(t("A different report with this id was already filed. Keep a copy or file it yourself on GitHub."), "error"); return true; }
  if (status === 429) { set(t("Too many reports were sent recently. Try again later.")); return false; }
  if (status === 422) { set(t("The server refused the report as invalid ({codes}). Nothing was sent.", { codes: code || "—" }), "error"); return false; }
  if (status === 502) { set(t("The bug tracker could not be reached or refused the report. Nothing was filed; you can send it again."), "error"); return false; }
  set(t("The server refused the report ({status}). Nothing was sent.", { status: code || String(status) }), "error");
  return false;
}

// Opens the dialog on a fresh report: cold from the footer, or about one
// incident this process raised (the Jobs area's failed job). A fresh id each
// time; the id is kept across Edit so a retry sends the very document the
// first attempt did.
async function openReport(about = null) {
  fields = { summary: "", steps: "", expected: "", actual: "" };
  reportId = newIncidentId();
  sendState = "idle";
  sendFinal = false;
  sending = false;
  // What this Companion raised, offered as a choice under "What it is about" — never
  // chosen for the person. A failed answer just means none are offered.
  const { ok, body: answer } = await api("/api/v1/bug-reports/incidents");
  incidents = ok && Array.isArray(answer?.incidents) ? answer.incidents : [];
  setIncident(about);
  compose();
  if (!dialog.open) dialog.showModal();
  // showModal focuses the header's close button; the report starts where the
  // person has something to do.
  focusCompose();
}

$("bug-report-open").addEventListener("click", () => { openReport(); });
$("bug-close").addEventListener("click", () => dialog.close());
window.AUCOM.reportBug = (options = {}) => openReport(options.incident || null);
