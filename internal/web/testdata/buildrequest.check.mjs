// The editor's build request notice (buildrequest.js), run on the shipped file
// against a page and a local server this script controls. Every answer is
// released by hand, so "watch saw A, resolve asked, B replaced A, resolve
// answered" is an exact order and not a matter of timing.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/buildrequest.js", import.meta.url), "utf8");

function element(tag, options = {}) {
  const node = {
    tag, children: [...(options.children || [])], listeners: {}, hidden: false, dataset: {}, attrs: { ...(options.attrs || {}) },
    textContent: options.text || "", className: options.className || options.attrs?.class || "",
    append(...items) { node.children.push(...items); },
    prepend(...items) { node.children.unshift(...items); },
    replaceChildren(...items) { node.children = items; },
    addEventListener(type, listener) { node.listeners[type] = listener; },
  };
  return node;
}
const all = (node) => node.children.flatMap((child) => [child, ...all(child)]);
const text = (node) => [node.textContent, ...all(node).map((child) => child.textContent)].join(" | ");
const buttonNamed = (node, name) => all(node).find((child) => child.tag === "button" && child.textContent === name);

const A = { pending: true, request_id: "a".repeat(32), intent: "build", run_after: false, received_at: "2026-10-28T10:00:00Z" };
const B = { pending: true, request_id: "b".repeat(32), intent: "build_run", run_after: true, received_at: "2026-10-28T10:00:05Z" };
const resolved = (request, name, revision) => ({ ...request, name, asset_id: "map-" + request.request_id[0], revision,
  revision_id: "rev-" + request.request_id[0], content_sha256: "1".repeat(64), game_profile: "quake1",
  preferred_tool: "auto-pigeon.ericw-tools.auto-pigeon-build.q1", normal_pipeline: "auto-pigeon.q1.normal" });

function load() {
  const box = element("section");
  const calls = [], waiting = [], adopted = [], events = [];
  const listeners = {};
  let watch = null;
  const window = {
    AUCOM: {
      $: (id) => (id === "build-request" ? box : element("div")), el: element, record() {}, showArea() {},
      t: (message, slots = {}) => message.replace(/\{(\w+)\}/g, (_, key) => String(slots[key])),
      withBusy: (_control, work) => work(),
      areas: { play: { adoptBuildRequest: async (request) => { adopted.push(request); } } },
      api(path, options = {}) {
        const call = { path, method: options.method || "GET", body: options.body };
        calls.push(call);
        return new Promise((resolve) => waiting.push({ call, resolve }));
      },
    },
    setInterval(callback) { watch = callback; },
    addEventListener() {},
  };
  const document = {
    hidden: false,
    addEventListener(type, listener) { (listeners[type] ||= []).push(listener); },
    dispatchEvent(event) { events.push(event); for (const listener of listeners[event.type] || []) listener(event); },
  };
  globalThis.CustomEvent ||= class { constructor(type, init = {}) { this.type = type; this.detail = init.detail; } };
  new Function("window", "document", source)(window, document);
  const settle = async () => { for (let turn = 0; turn < 20; turn += 1) await Promise.resolve(); };
  const answer = async (path, status, body) => {
    const index = waiting.findIndex((entry) => entry.call.path.startsWith(path));
    assert.ok(index >= 0, `the page has no unanswered ${path}; it asked ${calls.map((c) => c.method + " " + c.path).join(", ")}`);
    const [entry] = waiting.splice(index, 1);
    entry.resolve({ ok: status >= 200 && status < 300, status, body });
    await settle();
    return entry.call;
  };
  return { box, calls, adopted, events, document, window, answer, settle, tick: async () => { watch(); await settle(); } };
}

const REQUEST = "/api/v1/build-request/request";
const PENDING = "/api/v1/build-request/pending";

const list = (...requests) => ({ pending: requests.length > 0, requests, ...(requests.at(-1) || {}) });
const cardOf = (box, id) => box.children.find((card) => card.attrs?.["data-request-id"] === id);

// 1. One request: resolved by its own id, opened in the form by itself, and
//    its card says so — with its own Dismiss (issue #35).
{
  const p = load();
  await p.answer(REQUEST, 200, list(A));
  assert.match(text(p.box), /Build requested by the editor/);
  const asked = p.calls.find((call) => call.path.startsWith(PENDING));
  assert.equal(asked.path, PENDING + "?request_id=" + A.request_id, "resolve must name the request it asks about");
  await p.answer(PENDING, 200, resolved(A, "first", 3));
  const told = await p.answer("/api/v1/build-request/review", 200, { reviewing: true });
  assert.deepEqual(told.body, { request_id: A.request_id });
  assert.equal(p.adopted.length, 1);
  assert.equal(p.adopted[0].request_id, A.request_id);
  assert.match(text(p.box), /first, saved revision 3/);
  assert.match(text(p.box), /Ready in Build & Run\. Nothing runs until you press Build\./);
  assert.ok(buttonNamed(p.box, "Dismiss"), "every card can be dismissed");
}

// 2. An older server that resolves whatever is pending answers B's details to
//    a question about A: the page compares the identity itself.
{
  const p = load();
  await p.answer(REQUEST, 200, list(A));
  await p.answer(PENDING, 200, resolved(B, "second", 9));
  assert.doesNotMatch(text(p.box), /second/, "B's name was put on A's notice");
  assert.equal(p.adopted.length, 0);
}

// 3. Issue #33/#35: B arrives while A is being resolved and opened. A's answer
//    is still A's, A is the one opened, and B waits on its own card — it never
//    replaces the request in the form. Opening B leaves A pending (no dismiss).
{
  const p = load();
  await p.answer(REQUEST, 200, list(A));
  await p.tick();
  await p.answer(REQUEST, 200, list(A, B));
  assert.equal(p.box.children.length, 2, "both requests have a card");
  assert.ok(p.calls.some((call) => call.path === PENDING + "?request_id=" + B.request_id), "B is resolved by its own question");
  await p.answer(PENDING + "?request_id=" + A.request_id, 200, resolved(A, "first", 3));
  await p.answer("/api/v1/build-request/review", 200, { reviewing: true });
  await p.answer(PENDING + "?request_id=" + B.request_id, 200, resolved(B, "second", 9));
  assert.equal(p.adopted.length, 1, "the second link took the form");
  assert.equal(p.adopted[0].request_id, A.request_id);
  assert.match(text(cardOf(p.box, A.request_id)), /first, saved revision 3/);
  assert.match(text(cardOf(p.box, A.request_id)), /Ready in Build & Run/);
  assert.match(text(cardOf(p.box, B.request_id)), /second, saved revision 9/);
  assert.match(text(cardOf(p.box, B.request_id)), /Waiting: the form is open on another request/);
  assert.equal(p.events.filter((event) => event.type === "aucom:build-request-gone").length, 0);
  // A person opens B: the form moves to B, A stays pending on its card.
  buttonNamed(cardOf(p.box, B.request_id), "Open this request").listeners.click();
  await p.settle();
  const second = await p.answer("/api/v1/build-request/review", 200, { reviewing: true });
  assert.deepEqual(second.body, { request_id: B.request_id });
  assert.equal(p.adopted.at(-1).request_id, B.request_id);
  assert.match(text(cardOf(p.box, A.request_id)), /Waiting: the form is open on another request/);
  assert.equal(p.calls.filter((call) => call.path === "/api/v1/build-request/dismiss").length, 0, "opening B dismissed A");
  // B is started: B's card goes, A's stays, and A is not pushed into the form.
  p.document.dispatchEvent(new CustomEvent("aucom:build-request-started", { detail: { id: B.request_id } }));
  await p.settle();
  await p.answer(REQUEST, 200, list(A));
  assert.equal(p.box.children.length, 1);
  assert.ok(cardOf(p.box, A.request_id));
  assert.equal(p.adopted.length, 2, "A was opened again without a person asking");
  assert.match(text(p.box), /Waiting\. Open it to build it\./);
  // Dismissing A names A.
  buttonNamed(cardOf(p.box, A.request_id), "Dismiss").listeners.click();
  await p.settle();
  const dismissed = await p.answer("/api/v1/build-request/dismiss", 200, { dismissed: true });
  assert.deepEqual(dismissed.body, { request_id: A.request_id });
  assert.equal(p.box.hidden, true);
}

// 4. A request the account cannot find keeps its card and offers Retry; an
//    unsupported game is never opened; Dismiss names the request it shows and
//    leaves the other card alone.
{
  const p = load();
  await p.answer(REQUEST, 200, list(A, B));
  await p.answer(PENDING + "?request_id=" + A.request_id, 200, { ...A, not_found: true });
  assert.match(text(cardOf(p.box, A.request_id)), /This account cannot find the request/);
  buttonNamed(cardOf(p.box, A.request_id), "Retry").listeners.click();
  await p.settle();
  await p.answer(PENDING + "?request_id=" + A.request_id, 200,
    { ...A, unsupported: true, game_profile: "quake3", supported_profiles: ["quake1", "quake2"], name: "q3dm" });
  assert.match(text(cardOf(p.box, A.request_id)), /This saved revision is a Quake III map\. Build & Run builds Quake 1, Quake II maps only/);
  assert.equal(p.adopted.length, 0, "an unsupported game was opened");
  buttonNamed(cardOf(p.box, A.request_id), "Dismiss").listeners.click();
  await p.settle();
  const dismissed = await p.answer("/api/v1/build-request/dismiss", 200, { dismissed: true });
  assert.deepEqual(dismissed.body, { request_id: A.request_id });
  assert.equal(cardOf(p.box, A.request_id), undefined);
  assert.ok(cardOf(p.box, B.request_id), "dismissing A took B's card");
}

// 5. A request taken into the form that goes away (dismissed elsewhere,
//    expired) tells the form, which drops it; a request that was not in the
//    form going away tells it nothing.
{
  const p = load();
  await p.answer(REQUEST, 200, list(B, A));
  await p.answer(PENDING + "?request_id=" + B.request_id, 200, resolved(B, "second", 9));
  await p.answer("/api/v1/build-request/review", 200, { reviewing: true });
  await p.tick();
  await p.answer(REQUEST, 200, list(B));
  assert.equal(p.events.filter((event) => event.type === "aucom:build-request-gone").length, 0, "A's going was told to B's form");
  await p.tick();
  await p.answer(REQUEST, 200, { pending: false, requests: [] });
  const gone = p.events.find((event) => event.type === "aucom:build-request-gone");
  assert.equal(gone?.detail.id, B.request_id);
}

// 5b. The form dropping its request dismisses that one only; the same link
//     delivered again is the same card.
{
  const p = load();
  await p.answer(REQUEST, 200, list(A));
  await p.answer(PENDING, 200, resolved(A, "first", 3));
  await p.answer("/api/v1/build-request/review", 200, { reviewing: true });
  await p.tick();
  await p.answer(REQUEST, 200, list({ ...A, received_at: "2026-10-28T10:01:00Z" }, B));
  assert.equal(p.box.children.length, 2, "the same link again became another card");
  p.document.dispatchEvent(new CustomEvent("aucom:build-request-dropped", { detail: { id: B.request_id } }));
  await p.settle();
  assert.equal(p.calls.filter((call) => call.path === "/api/v1/build-request/dismiss").length, 0, "a drop about a request not in the form dismissed it");
  p.document.dispatchEvent(new CustomEvent("aucom:build-request-dropped", { detail: { id: A.request_id } }));
  await p.settle();
  const dropped = await p.answer("/api/v1/build-request/dismiss", 200, { dismissed: true });
  assert.deepEqual(dropped.body, { request_id: A.request_id });
}

// 6. The association rule play.js asks: the form builds what the editor asked
//    for only while it is the saved map, the exact revision and the same Run
//    after build.
{
  const p = load();
  const { stillAsked } = p.window.AUCOM.buildRequest;
  const request = { id: B.request_id, asset_id: "map-b", revision_id: "rev-b", run_after: true };
  const choice = { source: "saved", asset_id: "map-b", revision_id: "rev-b", run_after: true };
  assert.equal(stillAsked(request, choice), true);
  assert.equal(stillAsked(request, { ...choice, asset_id: "map-c" }), false, "another map");
  assert.equal(stillAsked(request, { ...choice, revision_id: "rev-newer" }), false, "another revision");
  assert.equal(stillAsked(request, { ...choice, run_after: false }), false, "Run after build switched off");
  assert.equal(stillAsked(request, { ...choice, source: "local" }), false, "a file on this computer");
  assert.equal(stillAsked(request, { ...choice, revision_id: undefined }), false, "no revision chosen");
  assert.equal(stillAsked(null, choice), false);
}

console.log("buildrequest.check.mjs: 7 cases passed");
