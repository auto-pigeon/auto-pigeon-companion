// Vanilla JS, no framework, no bundler, no npm. Loaded as classic scripts from
// the binary's embedded assets — see internal/web/embed.go for why.
//
// Every call goes to the Companion's own loopback server; there are no external
// requests and no third-party scripts, which is what keeps the GUI usable
// offline and keeps an AUB session from being exposed to anything but the
// Companion itself.
//
// This file is the part every area uses: the API call, the DOM helpers, the
// announcement channel, the path picker, and the activity record.

"use strict";

const AUCOM = (window.AUCOM = {
  // areas registers each area's refresh function, so navigation does not need
  // to know what any of them do.
  areas: {},
  // status is the last /api/status response. Areas read it rather than each
  // asking again.
  status: {},
  settings: {},
});

const $ = (id) => document.getElementById(id);
const el = (tag, options = {}) => {
  const node = document.createElement(tag);
  if (options.text !== undefined) node.textContent = options.text;
  if (options.className) node.className = options.className;
  if (options.html !== undefined) node.innerHTML = options.html;
  for (const [name, value] of Object.entries(options.attrs || {})) {
    if (value === null || value === undefined) continue;
    node.setAttribute(name, value);
  }
  for (const child of options.children || []) node.append(child);
  return node;
};

// The API token for this run, put into the page when it was served. Held in a
// variable and sent as a header: never in localStorage, never in a URL, and
// never a cookie the browser would attach on its own. See internal/web/auth.go.
const apiToken = document.querySelector('meta[name="aucom-api-token"]')?.content || "";

// api resolves to { ok, status, body } so callers never branch on throw versus
// reject, and so an error always has a `body.error` a page can show verbatim.
async function api(path, options = {}) {
  const init = {
    ...options,
    headers: {
      Accept: "application/json",
      "X-AUCOM-Token": apiToken,
      ...(options.headers || {}),
    },
  };
  if (init.body !== undefined && typeof init.body !== "string") {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(init.body);
  }
  try {
    const response = await fetch(path, init);
    const text = await response.text();
    let body = {};
    if (text) {
      try {
        body = JSON.parse(text);
      } catch {
        body = { error: text };
      }
    }
    if (response.status === 401 && body.code === "token_refused") {
      // The Companion was restarted after this page was loaded: the token in
      // the page belongs to the run that served it. Reloading fetches the new
      // one, and nothing the Companion recorded is lost by doing so.
      body.error =
        "this page was opened from an earlier start of the Companion, which has since restarted. " +
        "Reload the page to continue — every job, build and setting is still recorded.";
      const banner = document.getElementById("stale-page");
      if (banner) banner.hidden = false;
    }
    return { ok: response.ok, status: response.status, body };
  } catch (err) {
    // A fetch rejection here means the local server went away — the one failure
    // mode a user cannot diagnose from the page itself, so it is said plainly
    // rather than shown as a generic error.
    return {
      ok: false,
      status: 0,
      body: { error: `cannot reach the Companion: ${err.message}. It may have been stopped.` },
    };
  }
}

// announce speaks to assistive technology without changing the page.
//
// Polite, and it is deliberately the ONLY thing in this program that says
// something and then stops saying it. Everything a user might need to check
// afterwards is also written into a list that stays — see `record` below, and
// the job, build and cache lists that are read back from disk.
function announce(text) {
  const region = $("live-region");
  if (!region) return;
  // Cleared first, because a screen reader does not re-announce identical text
  // and two identical failures in a row is exactly when somebody needs telling.
  region.textContent = "";
  window.setTimeout(() => {
    region.textContent = text;
  }, 30);
}

// setMessage writes into one of the page's message paragraphs.
//
// `kind` is "error", "ok", "busy" or "" — and the class carries a text prefix in
// CSS rather than only a colour, so the state survives a monochrome display.
function setMessage(node, text, kind = "") {
  if (typeof node === "string") node = $(node);
  if (!node) return;
  node.textContent = text || "";
  node.className = "message" + (kind ? " " + kind : "");
  if (kind === "error" && text) announce(text);
}

function busy(node, text) {
  if (typeof node === "string") node = $(node);
  if (!node) return;
  node.className = "message busy";
  node.replaceChildren(el("span", { className: "spinner", attrs: { "aria-hidden": "true" } }), text);
}

// withBusy disables a control for the length of an operation and puts it back
// however the operation ends.
async function withBusy(button, work) {
  if (!button) return work();
  const wasDisabled = button.disabled;
  button.disabled = true;
  button.setAttribute("aria-busy", "true");
  try {
    return await work();
  } finally {
    button.disabled = wasDisabled;
    button.removeAttribute("aria-busy");
  }
}

// --- the activity record ----------------------------------------------------
//
// A persistent list of what this window has asked for and what it was told.
// It exists because a message that appears and disappears is not evidence: a
// user who looked away while a download finished, or whose build failed while
// they were in another area, must still be able to find out what happened.
//
// It is a convenience, not the record of truth. Every line points at something
// durable — a job, a build manifest, a cached revision — that the server has on
// disk and that survives a restart.

const activity = [];

function record(summary, detail, kind = "") {
  activity.unshift({
    at: new Date(),
    summary,
    detail: detail || "",
    kind,
  });
  if (activity.length > 200) activity.length = 200;
  renderActivity();
  return activity[0];
}

// clearActivity empties this window's convenience list. It touches nothing on
// the server: the jobs, builds and downloads each line points at are the record,
// and a button beside a list of things that happened must never be able to mean
// "delete the things that happened".
function clearActivity() {
  activity.length = 0;
  renderActivity();
}

function renderActivity() {
  const list = $("activity-log");
  if (!list) return;
  list.replaceChildren();
  if (activity.length === 0) {
    list.append(el("li", { className: "muted", text: "Nothing yet in this window." }));
    return;
  }
  for (const entry of activity) {
    const head = el("div", { className: "row-head" }, );
    head.append(el("strong", { text: entry.summary }));
    head.append(el("span", { className: "muted", text: entry.at.toLocaleTimeString() }));
    const item = el("li", { children: [head] });
    if (entry.kind) {
      head.append(el("span", { className: "badge " + entry.kind, text: entry.kind }));
    }
    if (entry.detail) item.append(el("div", { className: "mono", text: entry.detail }));
    list.append(item);
  }
}

// --- paths ------------------------------------------------------------------
//
// A field the user can type into, plus a Browse button that opens the desktop's
// own file chooser through the Companion. The browser is never given the
// filesystem: what comes back is the one path the user picked, and it goes
// through the same validation a typed one does. See internal/pathpick.

// --- tabs -------------------------------------------------------------------
//
// A category that decides what a panel below draws is a row of tabs, as in
// AUP, not a dropdown (NEW_244D, at the operator's request). The <select>
// stays in the document, hidden, as the one holder of the value: every area
// that already reads `.value` and listens for `change` keeps working, and the
// tabs are a view of it. Options added later — the Library's asset types come
// from the backend — redraw the tabs.
function tabsFor(selectId) {
  const select = document.getElementById(selectId);
  if (!select || select.dataset.tabs) return;
  select.dataset.tabs = "true";
  const label = document.querySelector(`label[for="${selectId}"]`);
  const bar = el("div", {
    className: "tabs",
    attrs: { role: "tablist", "aria-label": label ? label.textContent.trim() : selectId, id: selectId + "-tabs" },
  });
  const field = select.closest(".field") || select.parentElement;
  // Above the row the field sat in, so the tabs head what they choose between
  // rather than sitting beside a neighbouring field.
  const row = field.parentElement && field.parentElement.classList.contains("row") ? field.parentElement : null;
  if (row) row.before(bar);
  else field.after(bar);
  field.hidden = true;

  const draw = () => {
    bar.replaceChildren();
    const options = [...select.options];
    options.forEach((option, index) => {
      const selected = option.value === select.value;
      const tab = el("button", {
        text: option.textContent,
        attrs: {
          type: "button", role: "tab", "aria-selected": String(selected),
          tabindex: selected ? "0" : "-1", "data-value": option.value,
        },
      });
      tab.addEventListener("click", () => {
        if (select.value === option.value) return;
        select.value = option.value;
        select.dispatchEvent(new Event("change", { bubbles: true }));
        draw();
      });
      tab.addEventListener("keydown", (event) => {
        const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
        if (!step) return;
        event.preventDefault();
        const next = options[(index + step + options.length) % options.length];
        select.value = next.value;
        select.dispatchEvent(new Event("change", { bubbles: true }));
        draw();
        bar.querySelector(`[data-value="${CSS.escape(next.value)}"]`)?.focus();
      });
      bar.append(tab);
    });
  };
  select.addEventListener("change", draw);
  new MutationObserver(draw).observe(select, { childList: true });
  draw();
}

function pathField(options) {
  const { id, label, kind = "directory", value = "", hint = "", onChange } = options;
  const input = el("input", {
    attrs: { id, type: "text", value: value || "", spellcheck: "false", autocomplete: "off" },
  });
  const status = el("p", { className: "message", attrs: { id: id + "-status", role: "status" } });
  input.setAttribute("aria-describedby", status.id);

  const browse = el("button", { text: "Browse…", attrs: { type: "button", class: "secondary" } });
  // The button is only offered when this machine has a chooser to open. A
  // Browse button that opens nothing is worse than no button: the text field
  // beside it is the honest answer, and it always works.
  if (!AUCOM.settings.path_helper) {
    browse.hidden = true;
    browse.setAttribute("aria-hidden", "true");
  }

  const check = async () => {
    if (!input.value.trim()) {
      setMessage(status, "");
      input.removeAttribute("aria-invalid");
      return null;
    }
    const { body } = await api("/api/v1/paths/validate", {
      method: "POST",
      body: { kind, path: input.value },
    });
    if (body.valid) {
      input.value = body.path;
      input.removeAttribute("aria-invalid");
      setMessage(status, "");
      if (onChange) onChange(body.path);
      return body.path;
    }
    input.setAttribute("aria-invalid", "true");
    setMessage(status, body.error || "that path cannot be used", "error");
    return null;
  };

  input.addEventListener("change", check);

  browse.addEventListener("click", () =>
    withBusy(browse, async () => {
      const { ok, status: code, body } = await api("/api/v1/paths/pick", {
        method: "POST",
        body: { kind, title: label, start_dir: input.value || undefined },
      });
      if (ok && body.cancelled) {
        // Nothing went wrong: somebody was asked a question and said no.
        setMessage(status, "");
        return;
      }
      if (ok) {
        input.value = body.path;
        input.removeAttribute("aria-invalid");
        setMessage(status, "");
        if (onChange) onChange(body.path);
        input.focus();
        return;
      }
      if (code === 501) {
        // This machine has no file chooser. Say so once, hide the button, and
        // leave the text field — which is the thing that still works.
        browse.hidden = true;
        setMessage(status, "This machine has no file chooser the Companion can open — type the path instead.");
        input.focus();
        return;
      }
      setMessage(status, body.error || "the file chooser could not be opened", "error");
    })
  );

  // The input and its Browse button are ONE control row, and the hint is a
  // sibling below that row rather than a child of the box the button aligns
  // against. That ordering is the whole of 246I's first defect: the hint used
  // to sit inside `.field`, so `align-items: flex-end` aligned Browse to the
  // bottom of the HINT, and a field carrying one pushed its button out of line
  // with every field that did not. Alignment belongs to this component, so no
  // page re-solves it with a margin of its own.
  const control = el("div", { className: "path-field__control", children: [input, browse] });
  const field = el("div", {
    className: "field grow",
    children: [el("label", { text: label, attrs: { for: id } }), control],
  });
  if (hint) field.append(el("span", { className: "hint", text: hint }));
  const wrapper = el("div", { className: "path-field", children: [field] });
  const container = el("div", { children: [wrapper, status] });
  return { container, input, check };
}

// downloadButton fetches a file through the API and hands it to the browser.
//
// It is a button rather than a link, and that is forced by the design rather
// than chosen: every API route requires the token in a HEADER, and a plain
// <a href> navigation cannot set one. Fetching it here and handing the browser
// a blob is what lets the credential stay a header — which is the property that
// makes this server safe to leave listening (see internal/web/auth.go).
function downloadButton(label, url, filename) {
  const button = el("button", { text: label, attrs: { type: "button", class: "link" } });
  const status = el("span", { className: "muted" });
  button.addEventListener("click", () =>
    withBusy(button, async () => {
      status.textContent = " fetching…";
      let response;
      try {
        response = await fetch(url, { headers: { "X-AUCOM-Token": apiToken } });
      } catch (err) {
        status.textContent = " could not be fetched: " + err.message;
        return;
      }
      if (!response.ok) {
        const text = await response.text();
        status.textContent = " could not be fetched: " + text;
        return;
      }
      const blob = await response.blob();
      const href = URL.createObjectURL(blob);
      const anchor = el("a", { attrs: { href, download: filename } });
      document.body.append(anchor);
      anchor.click();
      anchor.remove();
      // Revoked on the next turn of the event loop: the click has already
      // started the save, and an object URL that is never revoked keeps the
      // whole file in memory for the life of the page.
      window.setTimeout(() => URL.revokeObjectURL(href), 0);
      status.textContent = " saved as " + filename;
    })
  );
  return el("span", { children: [button, status] });
}

// --- small formatters -------------------------------------------------------

function badge(text, kind) {
  return el("span", { className: "badge " + (kind || String(text)), text: String(text) });
}

// --- the work-in-progress statement ----------------------------------------
//
// One renderer, used by every area, because `AUP/AUCOM 215` asks for a
// *coherent* badge and message wherever an unfinished game can be selected,
// loaded, edited, exported or compiled. Five areas each writing their own two
// lines would be five slightly different warnings, and the difference between
// them is exactly what a reader notices instead of the warning.
//
// The object comes from the server on every profile, engine and pipeline — see
// internal/web/maturity.go — so no area has to know which families are
// unfinished, and a family that becomes finished stops warning everywhere at
// once.

function maturityBadge(maturity) {
  if (!maturity || !maturity.work_in_progress) return null;
  return badge(maturity.badge, "warning");
}

// maturityNote is the full sentence, with the report action beside it when the
// family invites one. `onReport` is the area's own handler; without one the
// note still renders, because the sentence is the part that must not be
// conditional.
function maturityNote(maturity, onReport) {
  if (!maturity || !maturity.work_in_progress) return null;
  const children = [el("span", { className: "wip__text", text: maturity.message })];
  if (maturity.feedback_invited && onReport) {
    const button = el("button", { className: "linklike", text: "Report compatibility issue" });
    button.type = "button";
    button.addEventListener("click", () => onReport(maturity));
    children.push(button);
  }
  return el("p", {
    className: "wip",
    attrs: { role: "note", "data-family": maturity.family || "" },
    children,
  });
}

// --- the compatibility report ----------------------------------------------
//
// One panel, opened by whichever area the user was on. The area supplies only
// *context* — which family, what operation, which profiles, which build — and
// never any content: the words are the user's and the document is composed by
// the server, by the same `feedback.Build` the command line calls.
//
// The page deliberately does not assemble the JSON. A page that did would be a
// second implementation of what may be shared, and the day the two disagreed
// would be the day a user sent something the page told them they were not
// sending.

let feedbackContext = null;

function openCompatibilityReport(context) {
  feedbackContext = context || {};
  const panel = $("feedback-panel");
  if (!panel) return;
  panel.hidden = false;
  $("feedback-context").textContent = feedbackContext.about || "";
  $("feedback-out").hidden = true;
  $("feedback-document").textContent = "";
  $("feedback-actions").replaceChildren();
  setMessage("feedback-message", "");
  // Cleared on every open. A panel that remembered the last set of ticks would
  // be a panel that attached something because of a decision made about a
  // different report.
  for (const name of ["versions", "profiles", "operation", "diagnostics"]) {
    $("feedback-share-" + name).checked = false;
  }
  $("feedback-summary").value = "";
  $("feedback-describe").value = "";
  $("feedback-heading").focus();
  panel.scrollIntoView({ block: "nearest" });
}

function feedbackConsent() {
  return {
    versions: $("feedback-share-versions").checked,
    profiles: $("feedback-share-profiles").checked,
    operation: $("feedback-share-operation").checked,
    diagnostics: $("feedback-share-diagnostics").checked,
  };
}

async function buildCompatibilityReport() {
  const summary = $("feedback-summary").value.trim();
  if (!summary) {
    setMessage("feedback-message", "Write one line saying what went wrong. That line is the report.", "error");
    return;
  }
  const context = feedbackContext || {};
  const request = {
    engine_family: context.family || "",
    summary,
    description: $("feedback-describe").value,
    operation: context.operation || "",
    share: feedbackConsent(),
    profiles: context.profiles || [],
    build_id: context.build_id || "",
  };
  const { ok, body } = await api("/api/v1/feedback/compatibility", { method: "POST", body: request });
  if (!ok) {
    setMessage("feedback-message", body.error || "the report could not be composed", "error");
    return;
  }
  $("feedback-document").textContent = body.document;
  $("feedback-out").hidden = false;

  const actions = $("feedback-actions");
  actions.replaceChildren();
  const save = el("button", { text: "Save it as a file", attrs: { type: "button" } });
  save.addEventListener("click", () => {
    const blob = new Blob([body.document], { type: "application/json" });
    const href = URL.createObjectURL(blob);
    const anchor = el("a", { attrs: { href, download: body.filename } });
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    window.setTimeout(() => URL.revokeObjectURL(href), 0);
  });
  actions.append(save);

  const notes = [];
  if (!Object.values(feedbackConsent()).some(Boolean)) {
    notes.push("Nothing was attached beyond your own words.");
  }
  if (body.messages_withheld > 0) {
    notes.push(
      body.messages_withheld +
        " message(s) were left out because they named a folder on this computer or something " +
        "shaped like a password. The warning and how often it happened are still there."
    );
  }
  notes.push("Nothing has been sent.");
  setMessage("feedback-message", notes.join(" "));
}

function wireCompatibilityReport() {
  const panel = $("feedback-panel");
  if (!panel) return;
  $("feedback-close").addEventListener("click", () => {
    panel.hidden = true;
    feedbackContext = null;
  });
  $("feedback-build").addEventListener("click", () =>
    withBusy($("feedback-build"), buildCompatibilityReport)
  );
}

function bytes(n) {
  if (n === undefined || n === null) return "";
  const units = ["B", "kB", "MB", "GB"];
  let value = Number(n);
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value < 10 && unit > 0 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
}

// A permission as a person reads it: the sentence, and how much it gives away.
// Its id is what a grant is recorded against, and stays on the wire (NEW_244D,
// operator: no internal ids or digests anywhere on the page).
function permissionBlock(permission) {
  const head = el("p");
  head.append(el("strong", { text: permission.summary || "An undescribed permission" }));
  if (permission.risk) {
    head.append(document.createTextNode(" "));
    head.append(badge(permission.risk + " risk", permission.risk === "high" ? "failed" : permission.risk === "medium" ? "warning" : "queued"));
  }
  return el("div", { className: "permission", children: [head] });
}

// programFileName is a declared program file as it is named on THIS machine:
// `{platform.exe_suffix}` is template syntax for the profile's author, and a
// hint that printed it asked a person to read a placeholder (NEW_244D).
function programFileName(file) {
  const windows = /^windows\//.test(AUCOM.status?.platform || "");
  return String(file || "").replaceAll("{platform.exe_suffix}", windows ? ".exe" : "");
}

// folderTitle names a root role the way a person calls that folder.
function folderTitle(role) {
  return {
    game_root: "Game directory",
    content_root: "Content folder",
    project_root: "Project folder",
    tool_root: "Program folder",
  }[role] || role.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase());
}

function when(value) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
}

function terminal(state) {
  return ["succeeded", "failed", "cancelled", "interrupted"].includes(state);
}

// confirmModal asks a question that has a consequence.
//
// `AUCOM/AUE/AUT 246I1` draws the line: a short confirmation of something that
// has already happened is a status line, and a DECISION — a permission, a
// licence acknowledgement, a destructive conflict — is a modal. A modal takes
// focus, traps it, returns it, and closes on Escape only when closing is not
// itself the destructive answer.
//
// Built on <dialog>, which the operating system's own accessibility layer
// already understands, rather than on a div pretending to be one.
function confirmModal({ title, body, confirm = "Continue", cancel = "Cancel" }) {
  return new Promise((resolve) => {
    const opener = document.activeElement;
    const confirmButton = el("button", { text: confirm, className: "primary", attrs: { type: "button" } });
    const cancelButton = el("button", { text: cancel, attrs: { type: "button" } });
    const dialog = el("dialog", {
      className: "modal",
      attrs: { "aria-labelledby": "modal-title" },
      children: [
        el("h2", { text: title, attrs: { id: "modal-title", tabindex: "-1" } }),
        el("p", { text: body }),
        el("div", { className: "modal-actions", children: [cancelButton, confirmButton] }),
      ],
    });
    const finish = (answer) => {
      dialog.close();
      dialog.remove();
      // Focus goes back where it came from. Without this a keyboard user is
      // left at the top of the document, in a page whose content changed
      // underneath them.
      opener?.focus?.();
      resolve(answer);
    };
    confirmButton.addEventListener("click", () => finish(true));
    cancelButton.addEventListener("click", () => finish(false));
    // Escape means cancel, which is the non-destructive answer.
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      finish(false);
    });
    document.body.append(dialog);
    dialog.showModal();
    dialog.querySelector("h2").focus();
  });
}

Object.assign(AUCOM, {
  $, el, api, announce, setMessage, busy, withBusy, confirmModal,
  record, renderActivity, clearActivity, pathField, downloadButton, tabsFor,
  badge, maturityBadge, maturityNote, bytes, when, terminal, permissionBlock, programFileName, folderTitle,
  openCompatibilityReport, wireCompatibilityReport,
  // Translation (i18n.js, loaded first). `t("English {slot}", {slot})`.
  t: (english, values) => window.AUCOM_I18N.t(english, values),
});
