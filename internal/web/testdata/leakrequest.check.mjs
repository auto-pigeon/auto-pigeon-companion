// NEW_307W1 (L): the pending request is replaced while the page is still
// resolving the one it shows.
//
// Runs leakrequest.js itself — the file the page loads — against a page and a
// local server this script controls. Every answer is released by hand, so the
// order "watch saw A, resolve asked, B replaced A, resolve answered" is exact
// and not a matter of timing.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/leakrequest.js", import.meta.url), "utf8");

function element(tag, options = {}) {
  const node = {
    tag, children: [...(options.children || [])], listeners: {}, hidden: false, isConnected: true, dataset: {}, attrs: { ...(options.attrs || {}) },
    textContent: options.text || "", className: options.className || options.attrs?.class || "",
    append(...items) { node.children.push(...items); },
    prepend(...items) { node.children.unshift(...items); },
    replaceChildren(...items) { node.children = items; },
    addEventListener(type, listener) { node.listeners[type] = listener; },
    contains() { return false; },
    querySelector(selector) {
      const name = selector.replace(/^\./, "");
      return all(node).find((child) => child.className.split(" ").includes(name)) || null;
    },
  };
  return node;
}
const all = (node) => node.children.flatMap((child) => [child, ...all(child)]);
const text = (node) => [node.textContent, ...all(node).map((child) => child.textContent)].join(" | ");
const buttonNamed = (node, name) => all(node).find((child) => child.tag === "button" && child.textContent === name);

const A = { pending: true, request_id: "a".repeat(32), asset_id: "map-one", revision: 3, content_sha256: "1".repeat(64), received_at: "2026-10-03T10:00:00Z" };
const B = { pending: true, request_id: "b".repeat(32), asset_id: "map-two", revision: 9, content_sha256: "2".repeat(64), received_at: "2026-10-03T10:00:05Z" };
const resolved = (request, name) => ({ ...request, name, revision_id: "rev-" + request.request_id[0], game_profile: "quake1", compiler: "ericw-qbsp",
  pipeline: "auto-pigeon.q1.leak-test", pipeline_name: "Quake 1 — leak test", pipeline_tool: "EricW tools, Auto-Pigeon build 1.4556 (Quake 1)",
  readiness: { ready: true, problems: [] },
  source_ref: `aub:map/${request.asset_id}@rev-${request.request_id[0]}#${name}.apmap`, files: [] });
const needsTool = { ready: false, problems: [{ fault: "missing_engine", summary: "x", profile: "auto-pigeon.ericw-tools.auto-pigeon-build.q1", executable: "qbsp" }] };

// Load the page with a watch the test can fire.
function load() {
  const box = element("div");
  const ids = { "leak-request": box };
  const byId = (id) => (ids[id] ||= Object.assign(element(id === "leak-pin-select" ? "select" : "div"), { id, value: "", focus() {}, disabled: false }));
  const calls = [], waiting = [], adopted = [], dropped = [], moved = [];
  const heard = {};
  let watch = null;
  // What adoptLeakRequest answers: nothing, or a sentence. A test can hold it.
  const form = { refuse: "", hold: null };
  const window = {
    AUCOM: {
      $: byId, el: element, record() {}, showArea(area) { moved.push(area); },
      pipelineReady: (item) => item?.readiness?.ready === true,
      executionChoices: (select, items, previous, ready) => items.find(ready)?.id || "",
      badge: (text, kind) => element("span", { text, className: "badge " + kind }),
      setupCauses: (item) => (item.readiness?.problems || []).map((problem) => ({ profile: problem.profile, name: "the tool", text: "the tool: qbsp not set up yet." })),
      setupLink: (cause) => element("a", { text: "Set up" }),
      t: (message, slots = {}) => message.replace(/\{(\w+)\}/g, (_, key) => String(slots[key])),
      withBusy: (_control, work) => work(),
      status: { authenticated: true, email: "one@example" },
      areas: { build: {
        adoptLeakRequest: async (request, current) => {
          if (form.hold) await form.hold;
          if (!current()) return "";
          adopted.push(request);
          return form.refuse;
        },
        dropLeakRequest: (id) => { dropped.push(id); },
      } },
      api(path, options = {}) {
        const call = { path, method: options.method || "GET", body: options.body };
        calls.push(call);
        return new Promise((resolve) => waiting.push({ call, resolve }));
      },
    },
    setInterval(callback) { watch = callback; },
    addEventListener() {},
  };
  const document = { title: "Companion", hidden: false, createTextNode: (text) => element("text", { text }),
    addEventListener(type, listener) { (heard[type] ||= []).push(listener); } };
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
  const unanswered = (path) => waiting.filter((entry) => entry.call.path.startsWith(path)).length;
  // The Build form says how its automatic check went.
  const say = async (type, detail) => { for (const listener of heard[type] || []) listener({ detail }); await settle(); };
  const checked = (id, state, why) => say("aucom:leak-form", { request_id: id, state, why });
  return { box, byId, calls, adopted, dropped, moved, form, window, answer, unanswered, say, checked, tick: async () => { watch(); await settle(); }, settle };
}
const posted = (p, path) => p.calls.filter((call) => call.method === "POST" && call.path === path);
const BUILD = "/api/v1/build/runs";

const REQUEST = "/api/v1/leak-test/request";
const PENDING = "/api/v1/leak-test/pending";

// 1. B replaces A while A is being resolved, and the server (as it now does)
//    says "replaced". Nothing about B is shown under A.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  assert.match(text(p.box), /map-one, saved revision 3/);
  const asked = p.calls.find((call) => call.path.startsWith(PENDING));
  assert.equal(asked.path, PENDING + "?request_id=" + A.request_id, "resolve must name the request it asks about");
  await p.answer(PENDING, 200, { pending: true, replaced: true, request_id: B.request_id, asset_id: B.asset_id, revision: B.revision });
  assert.equal(p.adopted.length, 0, "a replaced request was prepared");
  assert.doesNotMatch(text(p.box), /map-two/, "B's facts were shown before the watch re-read");
  // The discarded answer made the page re-read at once.
  await p.answer(REQUEST, 200, B);
  assert.match(text(p.box), /map-two, saved revision 9/);
  const second = p.calls.filter((call) => call.path.startsWith(PENDING)).at(-1);
  assert.equal(second.path, PENDING + "?request_id=" + B.request_id);
  await p.answer(PENDING, 200, resolved(B, "second"));
  assert.match(text(p.box), /second, saved revision 9/);
  assert.deepEqual(p.adopted.map((item) => item.request_id), [B.request_id], "B was not prepared by itself");
  assert.equal(buttonNamed(p.box, "Review leak test"), undefined, "a Review step survived");
}

// 2. The same race against an OLDER server that resolves whatever is pending:
//    it answers with B's details to a question about A. The page compares the
//    identity itself and throws the answer away.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(B, "second"));
  assert.doesNotMatch(text(p.box), /second/, "B's name was put on A's notice");
  assert.equal(p.adopted.length, 0, "A was prepared with B's source");
  // Same id, other bytes: also not this request.
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, { ...resolved(A, "first"), content_sha256: "f".repeat(64) });
  assert.equal(p.adopted.length, 0, "an answer about other bytes was accepted");
}

// 3. NEW_502, issue #35: the watch sees B while A's resolve is still in
//    flight. A's late answer — a valid answer about A — lands on A's card and
//    never on B's; A, which arrived while the form held nothing, is the one
//    prepared; B waits on its own card until a person opens it, and opening B
//    leaves A pending.
{
  const p = load();
  const list = (...requests) => ({ pending: true, requests, ...requests.at(-1) });
  const cardOf = (id) => p.box.children.find((card) => card.attrs?.["data-request-id"] === id);
  await p.answer(REQUEST, 200, A);
  await p.tick();
  await p.answer(REQUEST, 200, list(A, B));
  assert.equal(p.box.children.length, 2, "B replaced A instead of joining it");
  assert.match(text(cardOf(B.request_id)), /map-two, saved revision 9/);
  await p.answer(PENDING + "?request_id=" + A.request_id, 200, resolved(A, "first"));
  assert.match(text(cardOf(A.request_id)), /first/);
  assert.doesNotMatch(text(cardOf(B.request_id)), /first/, "A's answer was shown on B's card");
  await p.answer(PENDING + "?request_id=" + B.request_id, 200, resolved(B, "second"));

  // The form is prepared with A, and only A, without anybody pressing anything.
  assert.equal(p.adopted.length, 1);
  assert.equal(p.adopted[0].request_id, A.request_id);
  assert.equal(p.adopted[0].source_ref, "aub:map/map-one@rev-a#first.apmap");
  assert.deepEqual(p.moved, ["build"], "the page did not open on Build");
  assert.match(text(cardOf(B.request_id)), /Waiting: the Build form is open on another leak test/);
  // The editor is told it is ready only when it is, and then about A.
  assert.equal(posted(p, "/api/v1/leak-test/reviewing").length, 0, "the editor was told before the check passed");
  await p.checked(B.request_id, "ok");
  assert.equal(p.unanswered("/api/v1/leak-test/reviewing"), 0, "a check about B, not in the form, told the editor");
  await p.checked(A.request_id, "ok");
  const told = await p.answer("/api/v1/leak-test/reviewing", 200, { reviewing: true });
  assert.deepEqual(told.body, { request_id: A.request_id });
  assert.match(text(cardOf(A.request_id)), /nothing runs until you press Build/i);
  assert.equal(posted(p, BUILD).length, 0, "opening the request started a build");
  // A person opens B: A leaves the form (not dismissed), B is prepared.
  buttonNamed(cardOf(B.request_id), "Open this request").listeners.click();
  await p.settle();
  assert.deepEqual(p.dropped, [A.request_id]);
  assert.equal(p.adopted.at(-1).request_id, B.request_id);
  assert.equal(posted(p, "/api/v1/leak-test/dismiss").length, 0, "opening B dismissed A");
  assert.match(text(cardOf(A.request_id)), /Waiting: the Build form is open on another leak test/);
  // Dismissing A names A and leaves B in the form.
  buttonNamed(cardOf(A.request_id), "Dismiss").listeners.click();
  await p.settle();
  const dismissed = await p.answer("/api/v1/leak-test/dismiss", 200, { dismissed: true });
  assert.deepEqual(dismissed.body, { request_id: A.request_id });
  assert.equal(cardOf(A.request_id), undefined);
  assert.ok(cardOf(B.request_id));
  assert.deepEqual(p.dropped, [A.request_id], "dismissing A took B out of the form");
}

// 4. The form is being prepared for A when B replaces it: A's preparation
//    lands nowhere, A's form is dropped at once, and a slow acknowledgement of
//    A does not mark B ready.
{
  const p = load();
  let release;
  p.form.hold = new Promise((done) => { release = done; });
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  await p.tick();
  await p.answer(REQUEST, 200, B);
  assert.deepEqual(p.dropped, [A.request_id], "the Build button stayed aimed at A while the notice showed B");
  release();
  p.form.hold = null;
  await p.settle();
  assert.equal(p.adopted.length, 0, "the replaced request was taken into the form");
  assert.match(text(p.box), /map-two, saved revision 9/);
  // A's check answers late: it says nothing about B.
  await p.checked(A.request_id, "ok");
  assert.equal(p.unanswered("/api/v1/leak-test/reviewing"), 0, "a late check of A told the editor B was ready");
  assert.doesNotMatch(text(p.box), /nothing runs until you press Build/i, "A's check marked B ready");
  // A's acknowledgement in flight when B arrives does not touch B either.
  await p.answer(PENDING, 200, resolved(B, "second"));
  await p.checked(B.request_id, "ok");
  await p.tick();
  await p.answer(REQUEST, 200, { ...A, request_id: "c".repeat(32), asset_id: "map-three", revision: 1 });
  await p.answer("/api/v1/leak-test/reviewing", 200, { reviewing: true });
  assert.match(text(p.box), /map-three, saved revision 1/);
  assert.doesNotMatch(text(p.box), /nothing runs until you press Build/i, "a slow acknowledgement of B marked C ready");
}

// 5. Dismiss names the request it was showing; when B arrived meanwhile, B's
//    notice stays.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  const dismiss = buttonNamed(p.box, "Dismiss");
  await p.tick();
  await p.answer(REQUEST, 200, B); // B is now on screen...
  dismiss.listeners.click();       // ...and A's Dismiss, pressed a moment before, runs
  await p.settle();
  const call = p.calls.find((entry) => entry.path === "/api/v1/leak-test/dismiss");
  assert.deepEqual(call.body, { request_id: A.request_id }, "a Dismiss pressed on A's notice named another request");
  await p.answer("/api/v1/leak-test/dismiss", 200, { dismissed: false });
  await p.answer(REQUEST, 200, B);
  assert.match(text(p.box), /map-two, saved revision 9/, "the newer request's notice was removed by the older Dismiss");
}

// 6. The notice says when the editor has not been told, and stops saying it.
{
  const p = load();
  await p.answer(REQUEST, 200, { ...A, relay: { state: "retrying", desired: "received", failures: 2 } });
  await p.answer(PENDING, 200, resolved(A, "first"));
  assert.match(text(p.box), /The editor has not been told yet/);
  assert.equal(p.adopted.length, 1, "an unreachable relay must not block the preparation");
  await p.tick();
  await p.answer(REQUEST, 200, { ...A, relay: { state: "delivered", desired: "received", delivered: "received" } });
  assert.doesNotMatch(text(p.box), /has not been told/);
  assert.match(text(p.box), /first, saved revision 3/, "the resolved details were lost when the relay recovered");
  assert.equal(p.unanswered(PENDING), 0, "a relay change asked the account server again");
}

// 7. NEW_310: nothing is pinned for the map's game. The notice says so, prepares
//    nothing, and the choice opens by itself, asking for that game's choices.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", compiler: "ericw-qbsp", needs_pipeline: true });
  assert.match(text(p.box), /No pipeline is chosen yet for testing Quake 1 maps/);
  assert.equal(p.adopted.length, 0, "an unpinned request was prepared");
  assert.ok(buttonNamed(p.box, "Choose leak-test pipeline"));
  assert.equal(p.unanswered("/api/v1/leak-test/pipelines?game=quake1"), 1, "the choice did not open by itself");
}

// 8. NEW_310A: Cancel starts nothing and keeps the request; the dialog can
//    open again from the notice.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true });
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: { ready: true } }] });
  assert.equal(p.byId("leak-pin-modal").hidden, false, "the dialog did not open");
  p.byId("leak-pin-cancel").listeners.click();
  await p.settle();
  assert.equal(p.byId("leak-pin-modal").hidden, true, "Cancel did not close the dialog");
  assert.ok(!p.calls.some((c) => c.method === "POST"), "Cancel posted something: " + p.calls.filter((c) => c.method === "POST").map((c) => c.path));
  assert.equal(p.adopted.length, 0, "Cancel prepared the build");
  assert.match(text(p.box), /first, saved revision 3/, "Cancel forgot the request");
  assert.ok(buttonNamed(p.box, "Choose leak-test pipeline"), "the notice no longer offers the choice");
}

// 9. NEW_310A: a pin confirmed after the request it was opened for was
//    replaced starts nothing; B is prepared with what B resolves to.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true });
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: { ready: true } }] });
  // B replaces A while the dialog is open.
  await p.tick();
  await p.answer(REQUEST, 200, B);
  p.byId("leak-pin-select").value = "auto-pigeon.q1.leak-test";
  p.byId("leak-pin-confirm").listeners.click();
  await p.settle();
  await p.answer("/api/v1/leak-test/pipelines", 200, { game: "quake1", pinned: "auto-pigeon.q1.leak-test" });
  // Whatever resolves are outstanding answer for B, pinned now.
  while (p.unanswered(PENDING)) await p.answer(PENDING, 200, resolved(B, "second"));
  await p.settle();
  assert.equal(p.adopted.every((item) => item.request_id === B.request_id), true, "a pin made for A prepared A");
  assert.equal(posted(p, BUILD).length, 0);
  assert.match(text(p.box), /second, saved revision 9/);
}

// 10. NEW_380: the pin is confirmed and the request continues straight to the
//     prepared form, with whatever pipeline the server now says is pinned.
{
  const q = load();
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await q.answer(REQUEST, 200, A);
  await q.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true });
  await q.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: { ready: true } }] });
  q.byId("leak-pin-select").value = "auto-pigeon.q1.leak-test";
  q.byId("leak-pin-confirm").listeners.click();
  await q.settle();
  await q.answer("/api/v1/leak-test/pipelines", 200, { game: "quake1", pinned: "auto-pigeon.q1.leak-test" });
  await q.answer(PENDING, 200, resolved(A, "first"));
  assert.equal(q.adopted.length, 1, "the pinned request did not continue to Build");
  assert.equal(q.adopted[0].pipeline, "auto-pigeon.q1.leak-test");
}

// 11. NEW_310A: the pin is gone or unusable: the notice says why and offers
//     the choice again; nothing is chosen for the user.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true, pinned_problem: "the pipeline local.pipeline.removed is not installed" });
  assert.match(text(p.box), /cannot be used any more: the pipeline local.pipeline.removed is not installed/);
  assert.equal(p.adopted.length, 0);
  assert.ok(buttonNamed(p.box, "Choose leak-test pipeline"));
  assert.ok(!p.calls.some((c) => c.method === "POST"), "a pipeline was pinned for the user");
}

// 12. NEW_345: the pin for A is answered after the dialog was closed and
//     opened again for B. A's answer neither closes B's dialog nor reviews
//     anything: it belongs to an opening that is gone.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinnedA } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinnedA, game_profile: "quake1", needs_pipeline: true });
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: { ready: true } }] });
  p.byId("leak-pin-select").value = "auto-pigeon.q1.leak-test";
  p.byId("leak-pin-confirm").listeners.click();
  await p.settle();
  p.byId("leak-pin-cancel").listeners.click(); // closed while the pin is in flight
  await p.settle();
  await p.tick();
  await p.answer(REQUEST, 200, B);
  const { pipeline: _noneB, ...unpinnedB } = resolved(B, "second");
  await p.answer(PENDING, 200, { ...unpinnedB, game_profile: "quake1", needs_pipeline: true });
  assert.equal(p.byId("leak-pin-modal").hidden, false, "B's dialog did not open");
  await p.answer("/api/v1/leak-test/pipelines", 200, { game: "quake1", pinned: "auto-pigeon.q1.leak-test" });
  assert.equal(p.byId("leak-pin-modal").hidden, false, "A's late pin closed B's dialog");
  assert.equal(p.adopted.length, 0, "A's late pin prepared a build");
}

// 13. NEW_345: the list cannot be read. One message, one Retry, no half-empty
//     select; Retry reads it again and the list comes back.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true });
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 500, { error: "the catalog could not be read" });
  assert.match(p.byId("leak-pin-message").textContent, /could not be listed: the catalog could not be read/);
  assert.equal(p.byId("leak-pin-retry-row").hidden, false, "no Retry after a failed list");
  assert.equal(p.byId("leak-pin-field").hidden, true, "an empty select was shown after a failed list");
  assert.equal(p.byId("leak-pin-confirm").disabled, true, "Use pipeline was offered with nothing listed");
  p.byId("leak-pin-retry").listeners.click();
  await p.settle();
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: { ready: true } }] });
  assert.equal(p.byId("leak-pin-retry-row").hidden, true);
  assert.equal(p.byId("leak-pin-field").hidden, false);
  assert.equal(p.byId("leak-pin-message").textContent, "");
}

// 14. NEW_345: nothing is ready. No dead list to choose from, a sentence that
//     says what to do, and Use pipeline stays disabled.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true });
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "", choices: [
    { id: "auto-pigeon.q1.leak-test", name: "Quake 1 — leak test", readiness: { ready: false, problems: [{ fault: "missing_engine", summary: "x", profile: "auto-pigeon.ericw-tools.auto-pigeon-build.q1", executable: "qbsp" }] } }] });
  assert.equal(p.byId("leak-pin-select").hidden, true, "a list with nothing to choose was shown");
  assert.match(text(p.byId("leak-pin-status")), /No pipeline is ready yet/);
  p.byId("leak-pin-select").value = "auto-pigeon.q1.leak-test";
  p.byId("leak-pin-confirm").listeners.click();
  await p.settle();
  assert.ok(!p.calls.some((c) => c.method === "POST"), "an unready pipeline was pinned");
}

// 15. NEW_380: a missing tool keeps Build away with the setup link, the form is
//     not touched, and when the setup is done the same notice prepares it,
//     with no second request and no Review.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, { ...resolved(A, "first"), readiness: needsTool });
  assert.equal(p.adopted.length, 0, "a request that cannot run was taken into the form");
  assert.match(text(p.box), /qbsp not set up yet/, "the notice does not say what is missing");
  assert.ok(all(p.box).some((node) => node.tag === "a" && node.textContent === "Set up"), "no way to the tool");
  assert.equal(posted(p, "/api/v1/leak-test/reviewing").length, 0, "the editor was told it is ready");
  // Still missing: asked locally, and the account's server is not bothered.
  await p.tick();
  await p.answer(REQUEST, 200, A);
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "auto-pigeon.q1.leak-test", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: needsTool }] });
  assert.equal(p.unanswered(PENDING), 0, "a setup still missing asked the account server again");
  assert.equal(p.adopted.length, 0);
  // Installed: the next watch sees it, and the revision is checked once more.
  await p.tick();
  await p.answer(REQUEST, 200, A);
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "auto-pigeon.q1.leak-test", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: { ready: true } }] });
  await p.answer(PENDING, 200, resolved(A, "first"));
  assert.equal(p.adopted.length, 1, "the recovered setup did not prepare the build");
}

// 16. NEW_380: the form's check says something stops it: the notice says the
//     same thing, with Retry, and does not tell the editor it is ready.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  await p.checked(A.request_id, "blocked", "The texture bundle of this revision could not be fetched.");
  assert.match(text(p.box), /texture bundle of this revision could not be fetched/);
  assert.equal(posted(p, "/api/v1/leak-test/reviewing").length, 0);
  assert.doesNotMatch(text(p.box), /nothing runs until you press Build/i);
  buttonNamed(p.box, "Retry").listeners.click();
  await p.settle();
  await p.answer(PENDING, 200, resolved(A, "first"));
  assert.equal(p.adopted.length, 2, "Retry did not prepare the build again");
}

// 17. NEW_380: a build is running here. The form says so, nothing is lost, and
//     when that build ends the request is prepared without anybody asking.
{
  const p = load();
  p.form.refuse = "A build is running here.";
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  assert.match(text(p.box), /A build is running here/);
  p.form.refuse = "";
  await p.say("aucom:build-ended", {});
  assert.equal(p.adopted.length, 2, "the end of the other build did not prepare this one");
}

// 18. NEW_380: signing out or switching account drops the prepared form and
//     asks again; a status that changed nothing does neither.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  await p.checked(A.request_id, "ok");
  await p.answer("/api/v1/leak-test/reviewing", 200, { reviewing: true });
  await p.say("aucom:status", { authenticated: true });
  assert.equal(p.dropped.length, 0, "an unchanged status dropped the form");
  p.window.AUCOM.status = { authenticated: true, email: "two@example" };
  await p.say("aucom:status", { authenticated: true });
  assert.deepEqual(p.dropped, [A.request_id], "another account kept a Build button for the first one's request");
  assert.equal(p.unanswered(PENDING), 1, "the request was not asked about again");
  await p.answer(PENDING, 200, { pending: true, sign_in_required: true, ...A });
  assert.match(text(p.box), /Sign in to the account this map is in/);
}

// 19. NEW_380: the server refused the press (a new save, a sign-out). The
//     request is asked about again, and what it says now is what is shown.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  await p.checked(A.request_id, "ok");
  await p.answer("/api/v1/leak-test/reviewing", 200, { reviewing: true });
  await p.say("aucom:leak-build-refused", { request_id: A.request_id, code: "revision_changed" });
  await p.answer(PENDING, 409, { error: "the saved map revision changed or cannot be pinned; ask the editor again" });
  assert.match(text(p.box), /This request cannot be used/);
  assert.ok(p.dropped.includes(A.request_id), "the form kept the refused request");
}

// 20. NEW_380: the form was changed by hand: a Build there is not this leak
//     test any more, and the notice says so and offers the way back.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  await p.checked(A.request_id, "left");
  assert.match(text(p.box), /no longer builds this leak test/);
  buttonNamed(p.box, "Open leak test").listeners.click();
  await p.settle();
  assert.equal(p.adopted.length, 2);
}

// 21. NEW_380: the request is gone (dismissed elsewhere, expired, built): the
//     form is told, and a Build press cannot use it.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  await p.tick();
  await p.answer(REQUEST, 200, { pending: false });
  assert.deepEqual(p.dropped, [A.request_id]);
  assert.equal(p.box.hidden, true);
}

console.log("leakrequest: replacement, stale answers, dismiss, relay notice, unpinned game, cancel, pin ordering and recovery, a late pin of a closed dialog, list failure, nothing ready, direct Build, setup recovery, a blocked check, a running build, an account change, a refused press and a form changed by hand — ok");
