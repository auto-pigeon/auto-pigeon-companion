// The "Setting up build tools" card (tools.js) showing the server's background
// checks (NEW_368), run on the shipped file against a page this script stubs:
// a running check is a spinner row and the card's title; a failure says why,
// with Retry, which asks the server — once — to run the check again.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/tools.js", import.meta.url), "utf8");

function element(tag, options = {}) {
  const node = {
    tag, children: [], listeners: {}, hidden: false, disabled: false, checked: false, value: "", dataset: {},
    textContent: options.text || "", className: options.className || "", attrs: options.attrs || {},
    style: {}, classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    append(...items) { node.children.push(...items); },
    prepend(...items) { node.children.unshift(...items); },
    replaceChildren(...items) { node.children = items; },
    addEventListener(type, listener) { node.listeners[type] = listener; },
    setAttribute(name, value) { node.attrs[name] = value; },
    removeAttribute(name) { delete node.attrs[name]; },
    querySelector: () => null, querySelectorAll: () => [], focus() {}, contains: () => false,
  };
  for (const child of options.children || []) node.children.push(child);
  return node;
}
const all = (node) => (node.children || []).flatMap((child) => (typeof child === "object" && child ? [child, ...all(child)] : []));
const text = (node) => [node.textContent, ...all(node).map((child) => child.textContent)].join(" | ");

const nodes = {};
const $ = (id) => (nodes[id] ??= element("div"));
const calls = [];
let answer = null;
const pending = [];
const window = {
  AUCOM: {
    $, el: element, record() {}, trapFocus() {}, areas: {},
    t: (message, slots = {}) => message.replace(/\{(\w+)\}/g, (_, key) => String(slots[key])),
    bytes: (n) => String(n), when: (v) => String(v),
    setMessage: (id, message) => { $(id).textContent = message; },
    withBusy: async (_button, work) => work(),
    api: async (path, init = {}) => { calls.push([init.method || "GET", path]); return { ok: true, body: answer(path) }; },
  },
  setTimeout: (fn) => { pending.push(fn); return pending.length; },
  clearTimeout() {},
  addEventListener() {},
  sessionStorage: { getItem: () => null, removeItem() {} },
};
const document = {
  createTextNode: (value) => ({ textContent: value }),
  querySelector: (selector) => (selector.startsWith("[data-dismiss") ? element("div") : null),
  dispatchEvent() {}, activeElement: null,
};
const tick = () => new Promise((resolve) => setImmediate(resolve));

// The server is checking a profile's programs at start-up.
answer = () => ({ available: false, tools: [], checks: [{ kind: "checking_programs", detail: "EricW tools (Quake)", since: "2026-10-29T16:00:00Z" }] });
new Function("window", "document", "CustomEvent", source)(window, document, class {});
await tick();
const card = $("tools-setup");
assert.equal(card.hidden, false, "a running check does not show the card");
assert.equal($("tools-setup-title").textContent, "Checking build tools…");
const rows = $("tools-setup-list").children;
assert.equal(rows.length, 1);
assert.ok(all(rows[0]).some((child) => child.className === "spinner"), "the running check has no activity indicator");
assert.match(text(rows[0]), /Checking build tools… \| .*EricW tools \(Quake\)/);
assert.equal($("tools-setup-retry").hidden, true, "Retry offered while the check runs");

// It failed: why, Retry, and the full message under Details.
answer = (path) => (path === "/api/v1/checks/retry"
  ? { available: false, tools: [], checks: [{ kind: "checking_programs", since: "2026-10-29T16:01:00Z" }] }
  : { available: false, tools: [], check_failures: [{ kind: "checking_programs", error: "EricW tools (Quake): vis did not answer", retry: true }] });
await pending.shift()();
await tick();
assert.equal(card.hidden, false);
assert.equal($("tools-setup-title").textContent, "A check did not finish");
assert.match(text($("tools-setup-list")), /Checking build tools did not finish: EricW tools \(Quake\): vis did not answer/);
assert.match(text($("tools-setup-details")), /vis did not answer/);
assert.equal($("tools-setup-retry").hidden, false, "no Retry for a failed check");

// Retry asks the server once, and shows the check running again.
const before = calls.length;
await $("tools-setup-retry").listeners.click({ currentTarget: $("tools-setup-retry") });
await tick();
const retried = calls.slice(before).filter(([method, path]) => method === "POST" && path === "/api/v1/checks/retry");
assert.equal(retried.length, 1);
assert.equal($("tools-setup-title").textContent, "Checking build tools…");

// Nothing running, nothing failed: the card goes.
answer = () => ({ available: false, tools: [] });
await pending.shift()();
await tick();
assert.equal(card.hidden, true, "the card stayed with nothing to say");

// The converter's first check has its own words.
answer = () => ({ available: false, tools: [], checks: [{ kind: "checking_converter" }] });
await pending.shift()();
await tick();
assert.equal($("tools-setup-title").textContent, "Checking the converter…");

console.log("ok: the setup card shows running checks, failures with Retry, and goes when they end");
