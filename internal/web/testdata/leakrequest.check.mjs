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
    tag, children: [], listeners: {}, hidden: false, isConnected: true,
    textContent: options.text || "", className: options.className || options.attrs?.class || "",
    append(...items) { node.children.push(...items); },
    prepend(...items) { node.children.unshift(...items); },
    replaceChildren(...items) { node.children = items; },
    addEventListener(type, listener) { node.listeners[type] = listener; },
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
const resolved = (request, name) => ({ ...request, name, revision_id: "rev-" + request.request_id[0], pipeline: "auto-pigeon.q1.leak-test",
  source_ref: `aub:map/${request.asset_id}@rev-${request.request_id[0]}#${name}.apmap`, files: [] });

// Load the page with a watch the test can fire.
function load() {
  const box = element("div");
  const ids = { "leak-request": box };
  const byId = (id) => (ids[id] ||= Object.assign(element(id === "leak-pin-select" ? "select" : "div"), { id, value: "", focus() {}, disabled: false }));
  const calls = [], waiting = [], adopted = [];
  let watch = null;
  const window = {
    AUCOM: {
      $: byId, el: element, record() {}, showArea() {},
      pipelineReady: (item) => item?.readiness?.ready === true,
      executionChoices: (select, items, previous, ready) => items.find(ready)?.id || "",
      t: (message, slots = {}) => message.replace(/\{(\w+)\}/g, (_, key) => String(slots[key])),
      withBusy: (_control, work) => work(),
      areas: { build: { adoptLeakRequest: async (request) => { adopted.push(request); return ""; } } },
      api(path, options = {}) {
        const call = { path, method: options.method || "GET", body: options.body };
        calls.push(call);
        return new Promise((resolve) => waiting.push({ call, resolve }));
      },
    },
    setInterval(callback) { watch = callback; },
    addEventListener() {},
  };
  const document = { title: "Companion", hidden: false, addEventListener() {} };
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
  return { box, byId, calls, adopted, answer, unanswered, tick: async () => { watch(); await settle(); }, settle };
}

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
  assert.doesNotMatch(text(p.box), /Review leak test/, "a replaced request became reviewable");
  assert.doesNotMatch(text(p.box), /map-two/, "B's facts were shown before the watch re-read");
  // The discarded answer made the page re-read at once.
  await p.answer(REQUEST, 200, B);
  assert.match(text(p.box), /map-two, saved revision 9/);
  const second = p.calls.filter((call) => call.path.startsWith(PENDING)).at(-1);
  assert.equal(second.path, PENDING + "?request_id=" + B.request_id);
  await p.answer(PENDING, 200, resolved(B, "second"));
  assert.match(text(p.box), /second, saved revision 9/);
  assert.ok(buttonNamed(p.box, "Review leak test"));
}

// 2. The same race against an OLDER server that resolves whatever is pending:
//    it answers with B's details to a question about A. The page compares the
//    identity itself and throws the answer away.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(B, "second"));
  assert.doesNotMatch(text(p.box), /second/, "B's name was put on A's notice");
  assert.equal(buttonNamed(p.box, "Review leak test"), undefined, "A became reviewable with B's source");
  // Same id, other bytes: also not this request.
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, { ...resolved(A, "first"), content_sha256: "f".repeat(64) });
  assert.equal(buttonNamed(p.box, "Review leak test"), undefined, "an answer about other bytes was accepted");
}

// 3. The watch sees B while A's resolve is still in flight. A's late answer —
//    a perfectly valid answer about A — must not land on B's notice.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.tick();
  await p.answer(REQUEST, 200, B);
  assert.match(text(p.box), /map-two, saved revision 9/);
  await p.answer(PENDING, 200, resolved(A, "first"));
  assert.doesNotMatch(text(p.box), /first/, "A's answer was shown on B's notice");
  assert.equal(buttonNamed(p.box, "Review leak test"), undefined);
  // B is then resolved by its own question.
  await p.answer(REQUEST, 200, B);
  const asked = p.calls.filter((call) => call.path.startsWith(PENDING)).at(-1);
  assert.equal(asked.path, PENDING + "?request_id=" + B.request_id);
  await p.answer(PENDING, 200, resolved(B, "second"));

  // Review and Build are about B, and only B.
  buttonNamed(p.box, "Review leak test").listeners.click();
  await p.settle();
  const told = await p.answer("/api/v1/leak-test/reviewing", 200, { reviewing: true });
  assert.deepEqual(told.body, { request_id: B.request_id });
  assert.equal(p.adopted.length, 1);
  assert.equal(p.adopted[0].request_id, B.request_id);
  assert.equal(p.adopted[0].source_ref, "aub:map/map-two@rev-b#second.apmap");
  assert.match(text(p.box), /In review/);
}

// 4. Review is pressed for A, and B replaces it before the server answers:
//    A is not taken into the wizard, and B's notice is not marked "in review".
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  await p.answer(PENDING, 200, resolved(A, "first"));
  buttonNamed(p.box, "Review leak test").listeners.click();
  await p.settle();
  await p.tick();
  await p.answer(REQUEST, 200, B);
  await p.answer("/api/v1/leak-test/reviewing", 200, { reviewing: true });
  assert.equal(p.adopted.length, 0, "the replaced request was taken into the wizard");
  assert.doesNotMatch(text(p.box), /In review/);
  assert.match(text(p.box), /map-two, saved revision 9/);
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
  assert.ok(buttonNamed(p.box, "Review leak test"), "an unreachable relay must not block the review");
  await p.tick();
  await p.answer(REQUEST, 200, { ...A, relay: { state: "delivered", desired: "received", delivered: "received" } });
  assert.doesNotMatch(text(p.box), /has not been told/);
  assert.match(text(p.box), /first, saved revision 3/, "the resolved details were lost when the relay recovered");
  assert.equal(p.unanswered(PENDING), 0, "a relay change asked the account server again");
}

// 7. NEW_310: nothing is pinned for the map's game. The notice says so, offers
//    no Review, and the choice opens by itself, asking for that game's choices.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", compiler: "ericw-qbsp", needs_pipeline: true });
  assert.match(text(p.box), /No pipeline is chosen yet for testing Quake 1 maps/);
  assert.equal(buttonNamed(p.box, "Review leak test"), undefined, "an unpinned request became reviewable");
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
  assert.equal(p.adopted.length, 0, "Cancel started a review");
  assert.match(text(p.box), /first, saved revision 3/, "Cancel forgot the request");
  assert.ok(buttonNamed(p.box, "Choose leak-test pipeline"), "the notice no longer offers the choice");
}

// 9. NEW_310A: a pin confirmed after the request it was opened for was
//    replaced reviews nothing; B is shown, never started.
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
  assert.equal(p.unanswered("/api/v1/leak-test/reviewing"), 0, "a pin made for A started a review of B");
  assert.equal(p.adopted.length, 0);
  assert.match(text(p.box), /second, saved revision 9/);
}

// 10. NEW_310A: the pin is confirmed, but the request comes back pinned to
//     another pipeline (changed elsewhere): it is shown, not reviewed.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true });
  await p.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: { ready: true } }] });
  p.byId("leak-pin-select").value = "auto-pigeon.q1.leak-test";
  p.byId("leak-pin-confirm").listeners.click();
  await p.settle();
  await p.answer("/api/v1/leak-test/pipelines", 200, { game: "quake1", pinned: "auto-pigeon.q1.leak-test" });
  await p.answer(PENDING, 200, { ...resolved(A, "first"), pipeline: "local.pipeline.someone-elses" });
  assert.equal(p.unanswered("/api/v1/leak-test/reviewing"), 0, "a review started with a pipeline that was not the one pinned");
  // And with the pipeline just pinned, it does continue to Review.
  const q = load();
  await q.answer(REQUEST, 200, A);
  await q.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true });
  await q.answer("/api/v1/leak-test/pipelines?game=quake1", 200, { pinned: "", choices: [{ id: "auto-pigeon.q1.leak-test", readiness: { ready: true } }] });
  q.byId("leak-pin-select").value = "auto-pigeon.q1.leak-test";
  q.byId("leak-pin-confirm").listeners.click();
  await q.settle();
  await q.answer("/api/v1/leak-test/pipelines", 200, { game: "quake1", pinned: "auto-pigeon.q1.leak-test" });
  await q.answer(PENDING, 200, resolved(A, "first"));
  assert.equal(q.unanswered("/api/v1/leak-test/reviewing"), 1, "the pinned request did not continue to Review");
}

// 11. NEW_310A: the pin is gone or unusable: the notice says why and offers
//     the choice again; nothing is chosen for the user.
{
  const p = load();
  await p.answer(REQUEST, 200, A);
  const { pipeline: _none, ...unpinned } = resolved(A, "first");
  await p.answer(PENDING, 200, { ...unpinned, game_profile: "quake1", needs_pipeline: true, pinned_problem: "the pipeline local.pipeline.removed is not installed" });
  assert.match(text(p.box), /cannot be used any more: the pipeline local.pipeline.removed is not installed/);
  assert.equal(buttonNamed(p.box, "Review leak test"), undefined);
  assert.ok(buttonNamed(p.box, "Choose leak-test pipeline"));
  assert.ok(!p.calls.some((c) => c.method === "POST"), "a pipeline was pinned for the user");
}

console.log("leakrequest: replacement, stale answers, dismiss, relay notice, unpinned game, cancel, pin ordering and recovery — ok");
