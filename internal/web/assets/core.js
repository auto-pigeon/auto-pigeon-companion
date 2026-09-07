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

  const field = el("div", { className: "field grow", children: [el("label", { text: label, attrs: { for: id } }), input] });
  if (hint) field.append(el("span", { className: "hint", text: hint }));
  const wrapper = el("div", { className: "path-field", children: [field, browse] });
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

function shortDigest(digest) {
  if (!digest) return "";
  const body = String(digest).replace(/^sha256[:-]/, "");
  return body.length > 16 ? body.slice(0, 12) + "…" : body;
}

function when(value) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
}

function terminal(state) {
  return ["succeeded", "failed", "cancelled", "interrupted"].includes(state);
}

Object.assign(AUCOM, {
  $, el, api, announce, setMessage, busy, withBusy,
  record, renderActivity, clearActivity, pathField, downloadButton,
  badge, bytes, shortDigest, when, terminal,
});
