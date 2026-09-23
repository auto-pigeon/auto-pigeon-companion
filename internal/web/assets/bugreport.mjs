// Report a bug — AUG's dialog, in the Companion (operator, 2026-09-22).
//
// The document is built ONCE by the shared incident contract, vendored byte for
// byte under vendor/incident-contract, and every control below renders that one
// value: the field-by-field preview, the exact text, both downloads, the
// prefilled GitHub issue and the body AUB files. So what the person reviewed is
// what is published, exactly as in AUG (`auto-pigeon-gallery/src/incidents/
// BugReportDialog.tsx`), whose steps and words this follows.
//
// Two steps. Compose: four fields, nothing built. Review: the document, and
// three ways out — keep a copy, file it yourself on GitHub, or send it through
// Auto-Pigeon after ticking that it is published publicly. Nothing leaves this
// page before one of those presses. The report never carries a map, a path, a
// profile, an account or a token: the contract has no field for them.

import {
  BUG_REPORT_REPOSITORY,
  buildBugReport,
  newIncidentId,
  prefilledIssueUrl,
  renderIssue,
  renderReportText,
  reportDownloadNames,
  reportJsonDownload,
} from "./vendor/incident-contract/src/index.mjs";

const { $, el, api } = window.AUCOM;
const t = (english, values) => window.AUCOM.t(english, values);

const dialog = $("bug-dialog");
const body = $("bug-body");
const repository = `https://github.com/${BUG_REPORT_REPOSITORY}`;

let fields = { summary: "", steps: "", expected: "", actual: "" };
let reportId = "";
let built = null;
let sendState = "idle";

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
  return buildBugReport({
    component: "AUCOM",
    release: /^1\.\d+$/.test(status.version || "") ? status.version : "unknown",
    environment: status.incident_environment || "unknown",
    reportId,
    user: { ...fields },
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
  return el("div", { className: "field", children: [el("label", { text: t(label), attrs: { for: id } }), input] });
}

function compose(message) {
  built = null;
  const form = el("form", { className: "bug-form" });
  form.append(
    el("p", { className: "muted", text: t("Describe what went wrong. Before anything leaves this page you will see exactly what the report contains, and choose how to send it.") }),
    field("summary", "Summary", false),
    field("steps", "Steps to reproduce", true, 4),
    field("expected", "Expected result", true, 2),
    field("actual", "Actual result", true, 2),
  );
  if (message) form.append(el("p", { className: "message error", text: message }));
  form.append(el("div", {
    className: "modal-actions",
    children: [el("button", { text: t("Review report"), className: "primary", attrs: { type: "submit" } })],
  }));
  form.addEventListener("submit", (event) => {
    event.preventDefault();
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
  body.querySelector("#bug-field-summary")?.focus();
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
  const rows = [
    [t("Summary"), document_.user.summary],
    [t("Steps to reproduce"), orNot(document_.user.steps)],
    [t("Expected result"), orNot(document_.user.expected)],
    [t("Actual result"), orNot(document_.user.actual)],
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
    github.append(link, opened);
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

$("bug-report-open").addEventListener("click", () => {
  // A fresh report each time the dialog opens from the footer; the id is kept
  // across Edit so a retry sends the very document the first attempt did.
  fields = { summary: "", steps: "", expected: "", actual: "" };
  reportId = newIncidentId();
  sendState = "idle";
  compose();
  dialog.showModal();
});
$("bug-close").addEventListener("click", () => dialog.close());
