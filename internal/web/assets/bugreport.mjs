// Report a bug — AUG's dialog, in the Companion (operator, 2026-09-22).
//
// The document is built ONCE by the shared incident contract, vendored byte for
// byte under vendor/incident-contract, and every control below renders that one
// value: the field-by-field preview, the exact text, both downloads, the
// prefilled GitHub issue and the body AUB files. So what the person reviewed is
// what is published, exactly as in AUG (`auto-pigeon-gallery/src/incidents/
// BugReportDialog.tsx`), whose steps and words this follows.
//
// Two steps. Compose: what the report is (NEW_247H) — the application, a fixed
// fact; a type and an area, chosen by the person (or, for a report about an
// incident, preselected from the contract and still changeable) — then four
// fields whose labels follow the type. Nothing is built until Review, and
// Review is refused until a type and an area are chosen. Review: the document,
// the three labels it carries, and three ways out — keep a copy, file it
// yourself on GitHub, or send it through Auto-Pigeon after ticking that it is
// published publicly. Nothing leaves this page before one of those presses.
// The report never carries a map, a path, a profile, an account or a token:
// the contract has no field for them.
//
// Every classification rule — types, areas, the incident's starting area, the
// headings, the labels — is the contract's, through bugreport-model.mjs.

import {
  BUG_REPORT_REPOSITORY,
  newIncidentId,
  prefilledIssueUrl,
  renderIssue,
  renderReportText,
  reportDownloadNames,
  reportJsonDownload,
} from "./vendor/incident-contract/src/index.mjs";
import {
  APPLICATION,
  areaChoices,
  buildReport,
  fieldLabels,
  initialClassification,
  missingChoices,
  reportLabels,
  reportTypeChoices,
} from "./bugreport-model.mjs";

const { $, el, api } = window.AUCOM;
const t = (english, values) => window.AUCOM.t(english, values);

const dialog = $("bug-dialog");
const body = $("bug-body");
const repository = `https://github.com/${BUG_REPORT_REPOSITORY}`;

let fields = { summary: "", steps: "", expected: "", actual: "" };
let reportId = "";
let built = null;
let sendState = "idle";
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

function warning() {
  return el("p", {
    className: "bug-warning",
    attrs: { role: "note" },
    text: t("Bug reports are public and permanent: whatever you send is published at {repository}, where anyone can read it. Do not include anything private.",
      { repository }),
  });
}

function field(name, label, multiline, rows) {
  const id = "bug-field-" + name;
  const input = multiline
    ? el("textarea", { attrs: { id, rows: String(rows) } })
    : el("input", { attrs: { id, type: "text", maxlength: "400", required: "" } });
  input.value = fields[name];
  input.addEventListener("input", () => { fields[name] = input.value; });
  const caption = el("label", { text: t(label), attrs: { for: id } });
  return { node: el("div", { className: "field", children: [caption, input] }), caption };
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
  // one starts with nothing chosen. Either way the person can change it.
  ({ reportType, area } = initialClassification(incident));
}

function classification() {
  const wrap = el("div", { className: "bug-classify" });

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
  wrap.append(el("fieldset", { children: [el("legend", { text: t("Type") }), types] }));

  const select = el("select", { attrs: { id: "bug-area" } });
  select.append(el("option", { text: t("Choose an area"), attrs: { value: "" } }));
  for (const choice of areaChoices()) {
    select.append(el("option", { text: t(choice.name), attrs: { value: choice.id } }));
  }
  select.value = area;
  select.addEventListener("change", () => { area = select.value; relabel(); });
  wrap.append(el("div", { className: "field", children: [el("label", { text: t("Area"), attrs: { for: "bug-area" } }), select] }));
  return wrap;
}

let relabel = () => {};

function compose(message) {
  built = null;
  const form = el("form", { className: "bug-form" });
  form.append(
    el("p", { className: "muted", text: t("Tell us what went wrong or what you would like. Before anything leaves this page you will see exactly what the report contains, and choose how to send it.") }),
    el("p", {
      className: "bug-fact",
      children: [el("span", { className: "bug-fact__term", text: t("Application") }), document.createTextNode(t("Auto-Pigeon Companion ({application})", { application: APPLICATION }))],
    }),
  );

  // "What it is about": offered only when this Companion raised something. Choosing an
  // incident preselects the contract's type and area; choosing nothing makes
  // it a new report, where the person chooses both.
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
    });
    form.append(el("div", { className: "field", children: [el("label", { text: t("What it is about"), attrs: { for: "bug-incident" } }), about] }));
  }

  form.append(classification());
  if (incident) {
    form.append(el("p", { className: "muted small", attrs: { id: "bug-suggested" }, text: t("Type and area were suggested from the incident. Change them if the report is about something else.") }));
  }

  const summary = field("summary", "", false);
  const steps = field("steps", "", true, 4);
  const expected = field("expected", "", true, 2);
  const actual = field("actual", "", true, 2);
  form.append(summary.node, steps.node, expected.node, actual.node);
  if (message) form.append(el("p", { className: "message error", text: message }));

  const why = el("p", { className: "muted small", attrs: { id: "bug-review-why", role: "status" } });
  const submit = el("button", { text: t("Review report"), className: "primary", attrs: { type: "submit", "aria-describedby": "bug-review-why" } });
  form.append(why, el("div", { className: "modal-actions", children: [submit] }));

  // The labels follow the type, and Review waits for both choices.
  relabel = () => {
    const labels = fieldLabels(reportType);
    summary.caption.textContent = t(labels.summary);
    steps.caption.textContent = t(labels.steps);
    expected.caption.textContent = t(labels.expected);
    actual.caption.textContent = t(labels.actual);
    const missing = missingChoices({ reportType, area });
    submit.disabled = missing.length > 0;
    why.textContent = missing.length === 2 ? t("Choose a type and an area to review the report.")
      : missing.includes("report_type") ? t("Choose a type to review the report.")
        : missing.includes("area") ? t("Choose an area to review the report.") : "";
  };
  relabel();

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (missingChoices({ reportType, area }).length) {
      relabel();
      return;
    }
    if (!fields.summary.trim()) {
      compose(t("Write a one-line summary first."));
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
  (body.querySelector(incident || reportType ? "#bug-field-summary" : "#bug-type-" + reportTypeChoices()[0].id) || body.querySelector("#bug-field-summary"))?.focus();
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
  const text = renderReportText(document_);
  const names = reportDownloadNames(document_);
  const orNot = (value) => value || t("(not given)");
  // The headings of the type the DOCUMENT says, and the labels the contract
  // derives from it — not from the controls, which the person may have
  // changed and not yet reviewed.
  const headings = fieldLabels(document_.report_type);
  const labels = reportLabels(document_) || [];
  const typeName = reportTypeChoices().find((choice) => choice.id === document_.report_type)?.name || document_.report_type;
  const areaName = areaChoices().find((choice) => choice.id === document_.area)?.name || document_.area;
  const rows = [
    [t("Application"), t("Auto-Pigeon Companion ({application})", { application: document_.component })],
    [t("Type"), t(typeName)],
    [t("Area"), t(areaName)],
    [t(headings.summary), document_.user.summary],
    [t(headings.steps), orNot(document_.user.steps)],
    [t(headings.expected), orNot(document_.user.expected)],
    [t(headings.actual), orNot(document_.user.actual)],
    ...(document_.incident ? [[t("Incident"), [document_.incident.code, document_.incident.operation].filter(Boolean).join(" · ")]] : []),
    [t("Report"), document_.report_id],
    [t("Created"), document_.created_at],
    [t("Version"), `${document_.component} ${document_.release} (${document_.environment})`],
    [t("Browser"), [document_.client?.browser, document_.client?.os].filter(Boolean).join(" · ") || t("(not given)")],
  ];
  const preview = el("section", {
    attrs: { "aria-label": t("What the report contains") },
    children: [
      el("h3", { text: t("What the report contains") }),
      el("dl", { className: "summary-list", children: rows.flatMap(([term, value]) => [el("dt", { text: term }), el("dd", { text: value })]) }),
      el("h3", { text: t("Labels on GitHub") }),
      // The label names themselves, untranslated: they are what the issue shows.
      el("ul", { className: "bug-labels", attrs: { id: "bug-labels", "aria-label": t("Labels on GitHub") }, children: labels.map((label) => el("li", { text: label })) }),
      el("details", { children: [el("summary", { text: t("The exact text") }), el("pre", { className: "output", text })] }),
    ],
  });

  const saved = el("p", { className: "muted small", attrs: { role: "status" } });
  const keep = el("section", {
    className: "bug-route",
    children: [
      el("h3", { text: t("Keep a copy") }),
      el("div", {
        className: "row",
        children: [
          buttonTo(t("Download .txt"), () => { save(names.text, text, "text/plain"); saved.textContent = t("Saved {file}.", { file: names.text }); }),
          buttonTo(t("Download .json"), () => { save(names.json, reportJsonDownload(document_), "application/json"); saved.textContent = t("Saved {file}.", { file: names.json }); }),
        ],
      }),
      saved,
    ],
  });

  const opened = el("p", { className: "muted small", attrs: { role: "status" } });
  const github = el("section", { className: "bug-route", children: [el("h3", { text: t("File it yourself on GitHub") })] });
  if (built.prefill?.fits) {
    const link = el("a", {
      className: "button-link",
      text: t("Open prefilled GitHub issue"),
      attrs: { href: prefilledIssueUrl(document_), target: "_blank", rel: "noopener noreferrer" },
    });
    link.addEventListener("click", () => {
      opened.textContent = t("GitHub opened in a new tab. The report is filed only when you press Submit new issue there.");
    });
    github.append(
      link,
      el("p", { className: "muted small", text: t("GitHub adds the three labels to a prefilled issue only if your account may label issues in {repository}. Sending it through Auto-Pigeon always adds them.", { repository }) }),
      opened,
    );
  } else {
    github.append(el("p", { className: "muted", text: t("This report is too long for GitHub's prefilled form. Download it and attach the file to a new issue instead.") }));
  }

  const server = el("section", { className: "bug-route", children: [el("h3", { text: t("Send through Auto-Pigeon") })] });
  const serverBody = el("div", { children: [el("p", { className: "muted", text: t("Checking whether this server can file reports…") })] });
  server.append(serverBody);
  routeStatus().then((state) => drawServer(serverBody, state, document_));

  const actions = el("div", { className: "modal-actions" });
  if (sendState === "idle") actions.append(buttonTo(t("Edit"), () => compose()));
  actions.append(buttonTo(t("Close"), () => dialog.close()));

  body.replaceChildren(warning(), preview, keep, github, server, actions);
}

function buttonTo(label, onClick, className = "") {
  const button = el("button", { text: label, className, attrs: { type: "button" } });
  button.addEventListener("click", onClick);
  return button;
}

async function routeStatus() {
  const { ok, body: answer } = await api("/api/v1/bug-reports/status");
  if (!ok) return "unknown";
  return answer.server_route === "available" ? "available" : answer.server_route === "unavailable" ? "unavailable" : "unknown";
}

function drawServer(node, state, document_) {
  node.replaceChildren();
  if (state === "unavailable") {
    node.append(el("p", { className: "muted", text: t("This server is not configured to file reports. Keep a copy or file it yourself on GitHub.") }));
    return;
  }
  if (state === "unknown") {
    node.append(el("p", { className: "muted", text: t("Could not check whether this server can file reports. Keep a copy or file it yourself on GitHub.") }));
    return;
  }
  if (!window.AUCOM.status?.authenticated) {
    node.append(el("p", { className: "muted", text: t("Log in to send it through Auto-Pigeon. Keeping a copy and filing it yourself on GitHub work without an account.") }));
    return;
  }
  node.append(el("p", { className: "muted", text: t("Auto-Pigeon will file exactly this text at {repository} on your behalf.", { repository }) }));
  const ack = el("input", { attrs: { type: "checkbox", id: "bug-ack" } });
  const send = el("button", { text: sendState === "idle" ? t("Send report") : t("Send again"), className: "primary", attrs: { type: "button" } });
  send.disabled = true;
  ack.addEventListener("change", () => { send.disabled = !ack.checked; });
  const outcome = el("p", { className: "message", attrs: { role: "status" } });
  send.addEventListener("click", async () => {
    send.disabled = true;
    send.textContent = t("Sending…");
    sendState = "sent";
    const { status, body: answer } = await api("/api/v1/bug-reports", {
      method: "POST",
      body: { document: document_, confirm: true },
    });
    const final = showOutcome(outcome, status, answer);
    send.textContent = t("Send again");
    send.disabled = final || !ack.checked;
  });
  node.append(
    el("label", { className: "check", children: [ack, document.createTextNode(" " + t("I understand this is published publicly"))] }),
    el("div", { className: "row", children: [send] }),
    outcome,
  );
  // The issue AUB would file, for whoever wants to see its exact form.
  node.append(el("details", {
    children: [el("summary", { text: t("The exact text") }), el("pre", { className: "output", text: renderIssue(document_, { route: "server" }).body })],
  }));
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
  // What this Companion raised, offered as a choice under "What it is about" — never
  // chosen for the person. A failed answer just means none are offered.
  const { ok, body: answer } = await api("/api/v1/bug-reports/incidents");
  incidents = ok && Array.isArray(answer?.incidents) ? answer.incidents : [];
  setIncident(about);
  compose();
  if (!dialog.open) dialog.showModal();
}

$("bug-report-open").addEventListener("click", () => { openReport(); });
$("bug-close").addEventListener("click", () => dialog.close());
window.AUCOM.reportBug = (options = {}) => openReport(options.incident || null);
