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
        // A value set by code (a deep link) is the select's and not yet the
        // bar's: pressing the tab that already holds it redraws, rather than
        // leaving another tab looking chosen (NEW_330, `#new-profile/<engine>`
        // showed "A build pipeline" for an engine copy).
        if (select.value === option.value) { draw(); return; }
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
  // `aucom:sync`: code that sets the value without a change (which would run
  // the select's own handlers) still has the bar show it.
  select.addEventListener("aucom:sync", draw);
  new MutationObserver(draw).observe(select, { childList: true });
  draw();
}

function pathField(options) {
  // `accepts`, when given, is asked first: a value it describes is one the
  // caller's own server accepts though it is not a path (a Build map input
  // takes an `aub:` reference, NEW_357), and its sentence is shown instead
  // of a path check that could only refuse it.
  const { id, label, kind = "directory", value = "", hint = "", onChange, accepts } = options;
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
    const accepted = accepts ? accepts(input.value.trim()) : "";
    if (accepted) {
      input.removeAttribute("aria-invalid");
      setMessage(status, accepted);
      if (onChange) onChange(input.value.trim());
      return input.value.trim();
    }
    const { body } = await api("/api/v1/paths/validate", {
      method: "POST",
      body: { kind, path: input.value },
    });
    if (body.valid) {
      input.value = body.path;
      input.removeAttribute("aria-invalid");
      // A usable path the system may still refuse to run (macOS quarantine).
      setMessage(status, body.warning || "");
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
      // The chooser is another window; without this the button only looks
      // disabled for as long as it stays open (NEW_368).
      setMessage(status, "A file chooser is open. If you can't see it, look on the taskbar, or type the path in the box.");
      const { ok, status: code, body } = await api("/api/v1/paths/pick", {
        method: "POST",
        body: { kind, title: label, start_dir: input.value || undefined },
      });
      if (ok && body.cancelled) {
        // Nothing went wrong: somebody was asked a question and said no. What
        // was typed stays, and so does the place they were working (NEW_364).
        setMessage(status, "");
        input.focus();
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
      // The chooser is there but did not open or did not answer. The message
      // says so in words and ends with "type the path"; the cursor goes to the
      // box that always works. Browse stays: the next press may well succeed.
      const said = body.error || "The file chooser could not be opened. Type the path in the box instead";
      setMessage(status, said.charAt(0).toUpperCase() + said.slice(1) + (said.endsWith(".") ? "" : "."), "error");
      input.focus();
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

// duration says a span the way a person reads one. A lighting pass printed
// as "708399 ms" was a number nobody could place. (Moved from build.js by
// NEW_345 so that Jobs says how long a job took in the same words.)
function duration(ms) {
  if (!(ms >= 0)) return "";
  if (ms < 1000) return AUCOM.t("{n} ms", { n: Math.round(ms) });
  const seconds = ms / 1000;
  if (seconds < 60) return AUCOM.t("{n} s", { n: seconds < 10 ? seconds.toFixed(1) : Math.round(seconds) });
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return AUCOM.t("{m} min {s} s", { m: minutes, s: Math.round(seconds - minutes * 60) });
  return AUCOM.t("{h} h {m} min", { h: Math.floor(minutes / 60), m: minutes % 60 });
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
// `content` adds controls the decision is made with (a select, a field) below
// the sentence; `closeX` puts the canonical × in the corner (NEW_365: a
// decision a build needs before it can start). Both optional.
function confirmModal({ title, body, confirm = "Continue", cancel = "Cancel", content = [], closeX = false }) {
  // `cancel: null` is a notice: it has the one button.
  return new Promise((resolve) => {
    const opener = document.activeElement;
    const confirmButton = el("button", { text: confirm, className: "primary", attrs: { type: "button" } });
    const cancelButton = el("button", { text: cancel, attrs: { type: "button" } });
    const close = closeX
      ? el("button", { text: "\u00d7", className: "icon-button modal-close", attrs: { type: "button", "aria-label": AUCOM.t("Close") } })
      : null;
    const dialog = el("dialog", {
      className: "modal",
      attrs: { "aria-labelledby": "modal-title" },
      children: [
        el("div", { className: "modal-head", children: [el("h2", { text: title, attrs: { id: "modal-title", tabindex: "-1" } }), close].filter(Boolean) }),
        el("p", { text: body }),
        ...content,
        el("div", { className: "modal-actions", children: cancel === null ? [confirmButton] : [cancelButton, confirmButton] }),
      ],
    });
    close?.addEventListener("click", () => finish(false));
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

// Unknown readiness is pending. Execution choices consume the server's per
// action and pipeline results; configuration remains available in Profiles.
function actionReady(engine, id) {
  return Boolean(engine && Object.hasOwn(engine.action_problems || {}, id) && engine.action_problems[id].length === 0);
}
function pipelineReady(pipeline) { return pipeline?.readiness?.ready === true; }
// `retain` keeps a previous choice that is no longer ready SELECTED, greyed and
// with its reason, instead of emptying the list (NEW_323E): an emptied list is
// filled by `chooseReady` on the next refresh, and the first ready pipeline then
// stood where the user's own choice had been. A caller that retains must not
// start anything while the selected item is not ready.
// `offerable` (NEW_330) says which unavailable items setup could make
// available: an engine for another game is greyed out like one that needs
// setup, and gets no "Set up" link, because setting it up cannot make it play
// this build.
// `setupLabel` (NEW_345) names the collapsed list of what needs setup by what it
// lists (SETUP_LABELS: "pipelines" reads "Other pipelines (4 need setup)"), and
// `setupProblems(item)` replaces which of an item's problems are said, and
// `setupProfile(item)` answers `{ id, name }` of the profile an item is set up
// in when the items are not profiles themselves (an engine's actions are set
// up on the engine).
function executionChoices(select, items, previous, ready, label, placeholder, chooseReady = false, retain = false,
  offerable = () => true, setup = {}) {
  select.replaceChildren(el("option", { text: placeholder, attrs: { value: "" } }));
  for (const item of items) {
    const available = ready(item);
    const option = el("option", { text: label(item), attrs: { value: item.id } });
    option.disabled = !available;
    select.append(option);
  }
  select.value = items.some((item) => item.id === previous && (retain || ready(item))) ? previous
    : !previous && chooseReady ? items.find(ready)?.id || "" : "";
  // The selected item's own state is said beside the list by its caller; the
  // closed list is the OTHER items (NEW_345: a retained choice was counted in
  // "Other pipelines" too).
  setupActions(select, items.filter((item) => !ready(item) && offerable(item) && item.id !== select.value), setup);
  return select.value;
}

// A choice that cannot be made yet is greyed out in the list, and a greyed-out
// line is a dead end unless something beside it says where to go (NEW_323A).
//
// NEW_345: it says so ONCE, collapsed, and sends the person where the fix is.
// Four pipelines that wait for the same compiler used to print the same three
// sentences four times, and their links went to the pipelines' own pages,
// where there is nothing to set up. Now the list is one closed disclosure —
// "Other pipelines (4 need setup)" — whose rows name the item, what it waits
// for in a line, a "Set up" link to the profile that is actually missing, and
// the exact words of every problem one click further, never removed.
function setupActions(select, waiting, setup = {}) {
  const field = select.parentNode;
  if (!field?.querySelector) return;
  let aside = field.querySelector(".choice-setup");
  if (!waiting.length) { aside?.remove(); return; }
  const wasOpen = aside?.open === true;
  const opened = new Set([...(aside?.querySelectorAll("details[data-item][open]") || [])].map((node) => node.dataset.item));
  const fresh = el("details", { className: "choice-setup" });
  fresh.open = wasOpen;
  const [one, many] = SETUP_LABELS[setup.setupLabel] || SETUP_LABELS.default;
  fresh.append(el("summary", { text: waiting.length === 1 ? AUCOM.t(one) : AUCOM.t(many, { n: waiting.length }) }));
  fresh.append(setupRows(waiting, opened, setup));
  // A field may say where the list goes (`data-setup-slot`): the leak-test
  // chooser keeps the SELECTED pipeline's state between the select and it.
  const slot = field.querySelector("[data-setup-slot]");
  if (aside) aside.replaceWith(fresh); else if (slot) slot.append(fresh); else select.after(fresh);
}

// What the closed list is called, by what it lists: one sentence for one item
// and one for several, as each language says them.
const SETUP_LABELS = {
  default: ["Not ready (1 needs setup)", "Not ready ({n} need setup)"],
  pipelines: ["Other pipelines (1 needs setup)", "Other pipelines ({n} need setup)"],
  "all-pipelines": ["Pipelines (1 needs setup)", "Pipelines ({n} need setup)"],
  "build-profiles": ["Other build profiles (1 needs setup)", "Other build profiles ({n} need setup)"],
  engines: ["Other engines (1 needs setup)", "Other engines ({n} need setup)"],
  actions: ["Other actions (1 needs setup)", "Other actions ({n} need setup)"],
};

// setupRows is the rows of such a list, also used where the list is not under
// a select (the leak-test chooser with nothing ready yet).
function setupRows(waiting, opened = new Set(), setup = {}) {
  return el("ul", { className: "setup-rows", children: waiting.map((item) => {
    const row = el("li", { attrs: { "data-item": item.id } });
    row.append(el("span", { className: "setup-rows__name", text: item.name || item.id }));
    for (const cause of setupCauses(item, setup)) {
      row.append(el("span", { className: "setup-rows__cause", children: [document.createTextNode(cause.text + " "), setupLink(cause)] }));
    }
    const exact = (setup.setupProblems || setupProblems)(item);
    if (exact.length) {
      const details = el("details", { className: "setup-rows__exact", attrs: { "data-item": item.id } });
      details.open = opened.has(item.id);
      details.append(el("summary", { text: AUCOM.t("Details") }));
      details.append(el("ul", { className: "plain", children: exact.map((problem) =>
        el("li", { text: [problem.summary, problem.fix].filter(Boolean).join(" ") })) }));
      row.append(details);
    }
    return row;
  }) });
}

// setupLink is the one way a page sends somebody to set a profile up: its page
// in Profiles, named for a screen reader by the profile, not by "Set up" alone.
function setupLink(cause) {
  return el("a", { text: AUCOM.t("Set up"), attrs: {
    href: "#profiles/" + encodeURIComponent(cause.profile),
    "aria-label": AUCOM.t("Set up {name}", { name: cause.name }),
  } });
}

// trapFocus keeps Tab inside an open dialog: past the last control it comes
// back to the first, and Shift+Tab the other way. Call it from the dialog's
// keydown handler (the sign-in dialog's rule, shared since NEW_345).
function trapFocus(event, container, selector = "button, select, input, a[href], summary") {
  if (event.key !== "Tab") return;
  const focusable = [...container.querySelectorAll(selector)].filter((node) => !node.disabled && node.offsetParent !== null);
  if (!focusable.length) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
}

// setupProblems is what an item says stops it: its readiness problems, or —
// for an engine ready in itself and blocked only in an action — those.
function setupProblems(item) {
  const own = item?.readiness?.problems || [];
  if (own.length) return own;
  return Object.values(item?.action_problems || {}).flat();
}

// setupCauses groups an item's problems by the profile that has to be set up,
// from the profile id the server attaches to a pipeline's problem (NEW_345) —
// an engine or a tool is its own. Programs are named together by what is
// wrong with them ("qbsp, vis and light not set up yet"), from the fault and
// the program the server names; any other problem is said once per fault in
// the tool's own sentence (`detail`: the server's summary before it put the
// stage title in front). Nothing is read out of, or cut from, the English of
// a summary; the exact words stay one click away, under Details.
const PROGRAM_FAULTS = {
  missing_engine: "{programs} not set up yet",
  tool_incompatible: "{programs} not the version this profile was written for",
  tool_unverified: "{programs} did not say what they are",
  tool_unusable: "{programs} cannot be run here",
};
// A profile's own trust decision is said in words too (NEW_364): the
// checker's sentence for it carries the profile's id, which nobody reads.
const PROFILE_FAULTS = {
  not_authorized: "not approved on this machine yet",
  stale_binding: "changed since it was approved; approve it again",
};
function setupCauses(item, setup = {}) {
  const own = setup.setupProfile?.(item) || { id: item.id, name: item.name || item.id };
  const problemsOf = setup.setupProblems || setupProblems;
  // A list whose items carry one sentence instead of problems (a package's
  // engines) is said in that sentence.
  if (!problemsOf(item).length) {
    return item.problem ? [{ profile: own.id, name: own.name, text: item.problem }] : [];
  }
  const groups = new Map();
  for (const problem of problemsOf(item)) {
    const id = problem.profile || own.id;
    if (!groups.has(id)) groups.set(id, { profile: id, name: problem.profile_name || (id === own.id ? own.name : id), programs: new Map(), other: new Map(), sentences: new Set() });
    const group = groups.get(id);
    if (PROFILE_FAULTS[problem.fault]) {
      group.other.set(problem.fault, AUCOM.t("{name}: {what}.", { name: group.name, what: AUCOM.t(PROFILE_FAULTS[problem.fault]) }));
      group.sentences.add(problem.fault);
    } else if (PROGRAM_FAULTS[problem.fault] && problem.executable) {
      if (!group.programs.has(problem.fault)) group.programs.set(problem.fault, []);
      const names = group.programs.get(problem.fault);
      if (!names.includes(problem.executable)) names.push(problem.executable);
    } else {
      // The tool's own sentence (`detail`, without the stage title the server
      // puts in front of a pipeline's problem), once per fault; how many more
      // distinct sentences there are is counted, not dropped.
      const sentence = problem.detail || problem.summary;
      if (!group.other.has(problem.fault)) group.other.set(problem.fault, sentence);
      group.sentences.add(sentence);
    }
  }
  return [...groups.values()].map((group) => {
    const parts = [...group.programs].map(([fault, names]) => AUCOM.t(PROGRAM_FAULTS[fault], { programs: listOf(names) }));
    const said = parts.length ? [AUCOM.t("{name}: {what}.", { name: group.name, what: parts.join("; ") })] : [];
    const other = [...group.other.values()];
    if (other.length) said.push(other[0] + (group.sentences.size > 1 ? " " + AUCOM.t("(and {n} more)", { n: group.sentences.size - 1 }) : ""));
    return { profile: group.profile, name: group.name, text: said.join(" ") };
  });
}

// profileState is the ONE state a profile's row and page show (NEW_364), from
// the server's own `setup_state` (readiness.go `setupState`): installed,
// approved, compatible and ready are different facts and are never one green
// badge. Partly ready names what CAN start when that is one action, rather
// than promising every action works. `cause` is the first profile that has to
// be set up and what it needs, from the structured problem origin — for a
// pipeline whose light stage waits, the tool that owns light.
const PROFILE_STATES = {
  ready: ["Ready", "ok"],
  partly_ready: ["Partly ready", "warning"],
  needs_setup: ["Needs setup", "blocked"],
  needs_approval: ["Needs approval", "warning"],
  incompatible: ["Incompatible program", "blocked"],
  unavailable: ["Unavailable on this platform", "cancelled"],
};
function profileState(item) {
  const code = item?.setup_state?.state || (item?.readiness ? (item.readiness.ready ? "ready" : "needs_setup") : "");
  if (!code) return null;
  const [label, kind] = PROFILE_STATES[code] || [code, "blocked"];
  let text = AUCOM.t(label);
  const startable = item.setup_state?.ready_actions || [];
  if (code === "partly_ready" && startable.length === 1) {
    const action = (item.actions || []).find((candidate) => candidate.id === startable[0]);
    text = AUCOM.t("Ready: {action} only", { action: AUCOM.t(action?.title || startable[0]) });
  }
  const causes = code === "ready" || code === "partly_ready" ? [] : setupCauses(item);
  return { code, text, kind, cause: causes.find((cause) => cause.profile !== item.id) || null, causes };
}

// --- a running tool, in one line (NEW_365) ----------------------------------
//
// liveLine is the one rendering of a job's `live` view (GET /api/v1/jobs/{id},
// and `current_job_live` on a Build & Run run): the stage, what the tool said
// about its progress, its CPU and memory, and how long ago it last printed —
// "VIS · Full VIS · CPU 285% · RAM 640 MiB · last output 4 s ago". The Jobs
// page and the Activity drawer both call it on the same reading of the same
// sampler, so they cannot disagree. It never says "healthy" or "hung": quiet
// output is output inactivity, and CPU is not progress.
function mebibytes(n) {
  const mib = Number(n) / (1024 * 1024);
  return mib >= 1024 ? AUCOM.t("{n} GiB", { n: (mib / 1024).toFixed(1) }) : AUCOM.t("{n} MiB", { n: Math.round(mib) });
}

function liveLine(live, { stage = "", running = true } = {}) {
  if (!live) return { text: "", title: "" };
  const parts = [];
  if (stage) parts.push(stage);
  const progress = live.progress;
  if (running && progress?.phase) {
    parts.push(progress.percent !== undefined && progress.percent !== null
      ? AUCOM.t("{phase} {n}%", { phase: progress.phase, n: progress.percent }) : progress.phase);
  } else if (running && progress?.percent !== undefined && progress?.percent !== null) {
    parts.push(AUCOM.t("{n}%", { n: progress.percent }));
  }
  const now = Date.parse(live.now || "") || Date.now();
  const r = live.resources;
  let title = "";
  if (r) {
    title = AUCOM.t("CPU: 100% is one logical CPU fully busy, so four busy threads read about 400%. This machine has {n} logical CPUs. RAM is the resident memory of the tool's processes, added up.",
      { n: r.logical_cpus || "?" });
    const age = r.sampled_at ? Math.max(0, Math.round((now - Date.parse(r.sampled_at)) / 1000)) : 0;
    if (r.state === "live" && running) {
      parts.push(AUCOM.t("CPU {n}%", { n: Math.round(r.cpu_percent) }));
      parts.push(AUCOM.t("RAM {size}", { size: mebibytes(r.rss_bytes) }));
      if (age > 3) parts.push(AUCOM.t("reading {n} s old", { n: age }));
    } else if (r.state === "measuring" && running) {
      parts.push(AUCOM.t("CPU measuring…"));
      if (r.rss_bytes !== undefined) parts.push(AUCOM.t("RAM {size}", { size: mebibytes(r.rss_bytes) }));
    } else if (r.state === "unavailable") {
      parts.push(AUCOM.t("CPU and RAM not available here"));
      if (r.reason) title = r.reason;
    } else if (r.peak_rss_bytes) {
      parts.push(AUCOM.t("peak RAM {size}", { size: mebibytes(r.peak_rss_bytes) }));
      if (r.peak_cpu_percent) parts.push(AUCOM.t("peak CPU {n}%", { n: Math.round(r.peak_cpu_percent) }));
    }
  }
  if (running) {
    if (!live.supervised) {
      parts.push(AUCOM.t("recorded as running; this Companion is not supervising it"));
    } else if (live.last_output_at) {
      const quiet = Math.max(0, Math.round((now - Date.parse(live.last_output_at)) / 1000));
      parts.push(quiet >= 10 ? AUCOM.t("running; no output for {time}", { time: duration(quiet * 1000) })
        : AUCOM.t("last output {n} s ago", { n: quiet }));
    } else {
      parts.push(AUCOM.t("no output yet"));
    }
  }
  return { text: parts.join(" · "), title };
}

// listOf joins names the way the reader's language does ("a, b and c").
function listOf(names) {
  const language = window.AUCOM_I18N?.current?.() || "en";
  try {
    return new Intl.ListFormat(language, { style: "long", type: "conjunction" }).format(names);
  } catch {
    return names.join(", ");
  }
}

Object.assign(AUCOM, {
  $, el, api, announce, actionReady, pipelineReady, executionChoices, setMessage, busy, withBusy, confirmModal,
  setupRows, setupCauses, setupProblems, setupLink, profileState, trapFocus, listOf, duration, liveLine, mebibytes,
  record, renderActivity, clearActivity, pathField, downloadButton, tabsFor,
  badge, maturityBadge, maturityNote, bytes, when, terminal, programFileName, folderTitle,
  openCompatibilityReport, wireCompatibilityReport,
  // Translation (i18n.js, loaded first). `t("English {slot}", {slot})`.
  t: (english, values) => window.AUCOM_I18N.t(english, values),
});
