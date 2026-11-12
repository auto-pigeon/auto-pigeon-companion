// The editor's build requests, shown at the top of Build & Run (NEW_508: it was
// a strip above every area; from another area Activity has the way back).
//
// The editor's Build button opens `autopigeon://build/request/<id>`, and the
// link only RECORDS the request on this machine. This file watches for them
// from wherever the page is, says so with one card per request and opens
// Build & Run's own form (play.js) on the exact saved revision, with the map
// and the default tool and engine chosen; the person presses Build or Build &
// Run there (HITL, 2026-10-31: no Review build). It is leakrequest.js's
// pattern for a different request — not a leak test, and nothing here touches
// one.
//
//   - Several requests may be pending at once (NEW_502, issue #35): two maps,
//     two tabs, or the same request opened again with a new delivery id. Each
//     has its own card, is resolved, dismissed and built by its own id, and a
//     second one NEVER replaces the one open in the form (issue #33: that
//     replacement is what dropped a request at the WAD folder step).
//   - The form holds at most one request (`formID`). A request that arrives
//     while the form holds none is opened by itself; any other waits on its
//     card until a person opens it.
//   - The watch is cheap and local: `/api/v1/build-request/request` answers
//     from this machine and calls nobody. Each request is resolved against the
//     account's server once, and again only when somebody asks (Retry, a
//     sign-in, the window coming back into view).
//   - An answer is used only for the request it names, and only while that
//     request's card is the one it was asked for (`card.generation`).
//   - Nothing here runs anything. "Opened" is not "started".

"use strict";

(() => {
  const { $, el, api, withBusy, record, t } = window.AUCOM;

  const WATCH_MS = 2000;
  const box = $("build-request");

  // One card per pending request, in the order they arrived. A card is
  // { request, details, resolving, generation, autoOpen, opened }: `request`
  // is the watch's entry, `details` what resolving it said.
  const cards = new Map();
  // formID is the request taken into Build & Run from THIS page, or null.
  let formID = null;
  let generation = 0;
  let reading = false;
  let drawn = "";

  // The association rule, one copy, which play.js asks too: the form still
  // builds what the editor asked for only while it is the saved map, the exact
  // revision and the same Run after build.
  function stillAsked(request, choice) {
    return Boolean(request && choice) && choice.source === "saved" && choice.asset_id === request.asset_id &&
      choice.revision_id === request.revision_id && choice.run_after === request.run_after;
  }
  window.AUCOM.buildRequest = { stillAsked, inForm: () => formID };

  function when(value) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "" : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  function gameName(id) {
    return { quake1: t("Quake 1"), quake2: t("Quake II"), quake3: t("Quake III") }[id] || id || t("unknown game");
  }

  function button(text, kind, onClick) {
    const control = el("button", { text, attrs: { type: "button", class: kind } });
    control.addEventListener("click", () => withBusy(control, onClick));
    return control;
  }

  // sameRequest: does an answer speak about this request?
  function sameRequest(body, request) {
    return Boolean(body && request) && body.request_id === request.request_id &&
      (body.intent === undefined || request.intent === undefined || body.intent === request.intent);
  }

  function renderCard(card) {
    const seen = card.request;
    const details = card.details;
    const runAfter = seen.run_after === true;
    const action = runAfter ? t("Build & Run") : t("Build");
    const head = el("p", { className: "leak-request__title",
      text: runAfter ? t("Build & Run requested by the editor") : t("Build requested by the editor") });
    const lines = [head];
    if (details?.ok && details.body.revision) {
      lines.push(el("p", { className: "leak-request__what", text: t("{name}, saved revision {n} — received {time}", {
        name: details.body.name || details.body.asset_id, n: details.body.revision, time: when(seen.received_at) }) }));
    } else {
      lines.push(el("p", { className: "leak-request__what", text: t("Received {time}", { time: when(seen.received_at) }) }));
    }
    const state = el("p", { className: "leak-request__state" });
    const actions = el("div", { className: "row-actions" });
    // Bound to the request THIS card shows, not to whatever is on screen when
    // the click is handled. Every card can be dismissed (issue #35).
    const shown = seen.request_id;
    const dismiss = button(t("Dismiss"), "secondary", () => dismissRequest(shown));
    dismiss.dataset.action = "dismiss";
    actions.append(dismiss);

    if (!details) {
      state.textContent = t("Checking the request with your account…");
    } else if (details.body.sign_in_required) {
      state.textContent = t("Sign in to the account the editor uses. The Companion cannot read this request while signed out.");
      actions.append(button(t("Sign in"), "primary", async () => window.AUCOM.openSignIn?.()));
    } else if (details.ok && details.body.not_found) {
      // AUB says unknown, expired, another account's and an unreadable map
      // alike, on purpose; so does this.
      state.className = "leak-request__state message error";
      state.textContent = t("This account cannot find the request. The Companion may be signed in to another server or account, or the request expired.");
      actions.append(button(t("Retry"), "secondary", async () => resolve(shown, true)));
    } else if (!details.ok) {
      state.className = "leak-request__state message error";
      state.textContent = details.status === 409
        ? t("This request cannot be built: {why}.", { why: details.body.error })
        : t("The request could not be checked: {why}", { why: details.body.error || t("the account server did not answer") });
      if (details.status !== 409) actions.append(button(t("Retry"), "secondary", async () => resolve(shown, true)));
    } else if (details.body.unsupported) {
      // Not Quake 1 by default, and no review: nothing was run and nothing will be.
      state.className = "leak-request__state message error";
      state.textContent = t("This saved revision is a {game} map. Build & Run builds {list} maps only, so nothing will run.", {
        game: gameName(details.body.game_profile), list: (details.body.supported_profiles || []).map(gameName).join(", ") });
    } else if (formID === shown && details.body.game_profile === "quake3") {
      // A Quake III build ends at its BSP whichever the editor asked for
      // (NEW_504): the button says Build, and so does this.
      state.textContent = t("Ready in Build & Run. Nothing runs until you press Build; a Quake III build then goes on to Package and run this map.");
    } else if (formID === shown) {
      state.textContent = t("Ready in Build & Run. Nothing runs until you press {action}.", { action });
    } else {
      // Another request is in the form, or none is: this one waits for a
      // person, and opening it leaves the other pending on its own card.
      state.textContent = formID
        ? t("Waiting: the form is open on another request. Open this one to build it; the other stays here.")
        : t("Waiting. Open it to build it.");
      const open = button(t("Open this request"), "primary", async () => review(shown));
      open.dataset.action = "open";
      actions.append(open);
    }
    lines.push(state);
    // The request is HERE either way. Whether the editor knows is a separate
    // fact, said only when it does not.
    const relay = (details?.body?.relay || seen.relay)?.state;
    const told = relay === "retrying"
      ? t("The editor has not been told yet: the account server is not answering. The Companion keeps trying; the request stays here.")
      : relay === "stopped" ? t("The editor cannot be told about this request: the account server rejected it.") : "";
    if (told) lines.push(el("p", { className: "leak-request__relay hint", text: told }));
    return el("div", { className: "leak-request__card", attrs: { "data-request-id": shown, "data-kind": "build" },
      children: [...lines, actions] });
  }

  // render draws every card, and only when what they say changed: a card
  // rebuilt every two seconds would take the focus off its own buttons.
  function render() {
    if (!cards.size) { drawn = ""; box.hidden = true; box.replaceChildren(); return; }
    const nodes = [...cards.values()].map(renderCard);
    const signature = JSON.stringify([formID, [...cards.keys()], nodes.map(text)]);
    if (signature === drawn && !box.hidden) return;
    drawn = signature;
    box.hidden = false;
    const focused = box.contains?.(document.activeElement) ? document.activeElement : null;
    const where = focused ? [focused.closest?.(".leak-request__card")?.dataset?.requestId, focused.dataset?.action] : null;
    box.replaceChildren(...nodes);
    if (where?.[0]) box.querySelector?.(`[data-request-id="${where[0]}"] button[data-action="${where[1]}"]`)?.focus();
  }
  function text(node) {
    return [node.textContent || "", ...Array.from(node.children || [], text)].join("|");
  }

  // resolve asks the account's server about ONE request, naming it: an answer
  // about anything else, or for a card that has since gone, is thrown away.
  async function resolve(id, force) {
    const card = cards.get(id);
    if (!card || card.resolving || (card.details && !force)) return;
    card.resolving = true;
    const own = card.generation;
    const asked = card.request;
    if (force) { card.details = null; render(); }
    const answer = await api("/api/v1/build-request/pending?request_id=" + encodeURIComponent(asked.request_id));
    card.resolving = false;
    if (cards.get(id) !== card || card.generation !== own) return; // Gone, or asked again meanwhile.
    if (answer.ok && !answer.body.pending) { forget(id); return; }
    if (answer.ok && (answer.body.replaced || !sameRequest(answer.body, asked))) { watch(); return; }
    card.details = answer;
    render();
    // A request that arrived while the form held none is opened by itself,
    // once; never over another request in the form.
    if (answer.ok && card.autoOpen && !card.opened && !formID && answer.body.revision_id && !answer.body.unsupported &&
        !answer.body.not_found && !answer.body.sign_in_required) {
      card.opened = true;
      await review(id);
    }
  }

  // forget: one request is gone — started, dismissed, expired. A form that was
  // building it for the editor is told; every other card stays.
  function forget(id) {
    const card = cards.get(id);
    if (!card) return;
    card.generation += 1;
    cards.delete(id);
    const was = formID === id;
    if (was) formID = null;
    generation += 1;
    render();
    if (was) document.dispatchEvent(new CustomEvent("aucom:build-request-gone", { detail: { id } }));
  }

  async function watch() {
    if (reading) return;
    reading = true;
    const { ok, body } = await api("/api/v1/build-request/request");
    reading = false;
    if (!ok) return; // The local server is away; the stale-page card says so.
    const listed = Array.isArray(body.requests) ? body.requests : body.pending ? [body] : [];
    // Gone: no longer listed, or the same id now naming another intent.
    for (const [id, card] of [...cards]) {
      if (!listed.some((entry) => sameRequest(entry, card.request))) forget(id);
    }
    for (const entry of listed) {
      const card = cards.get(entry.request_id);
      if (card) {
        if ((entry.relay?.state || "") !== (card.request.relay?.state || "")) card.request = entry;
        continue;
      }
      // Opened by itself only when the form holds no request and no other
      // card is already about to be opened.
      const autoOpen = !formID && ![...cards.values()].some((other) => other.autoOpen && !other.opened);
      cards.set(entry.request_id, { request: entry, details: null, resolving: false, generation: 0, autoOpen, opened: false });
      record(entry.run_after ? t("Build & Run requested by the editor") : t("Build requested by the editor"), "", "running");
      window.AUCOM.requestArrived?.(entry);
    }
    render();
    for (const id of [...cards.keys()]) resolve(id, false);
  }

  // review takes one request into Build & Run. It starts nothing: the form
  // checks the choices by itself, and the person presses the button. A request
  // that was in the form stays pending on its own card. The form shows its
  // area itself (adoptBuildRequest): asking for it here as well started a
  // second refresh of the form racing the first (issue #33).
  async function review(id) {
    const card = cards.get(id);
    if (!card?.details?.ok || !sameRequest(card.details.body, card.request) || !card.details.body.revision_id) return;
    const own = card.generation;
    const request = card.details.body;
    const told = await api("/api/v1/build-request/review", { method: "POST", body: { request_id: request.request_id } });
    if (cards.get(id) !== card || card.generation !== own) return;
    if (!told.ok) { card.details = told; render(); return; }
    formID = request.request_id;
    render();
    await window.AUCOM.areas.play?.adoptBuildRequest?.(request);
  }

  // Dismiss names the request it dismisses, so another one that arrived while
  // this card was on screen is not the one forgotten.
  async function dismissRequest(id) {
    if (!id) return;
    await api("/api/v1/build-request/dismiss", { method: "POST", body: { request_id: id } });
    forget(id);
    watch();
  }

  // The form stopped building what the editor asked for: a person chose
  // something else, and that request is over.
  document.addEventListener("aucom:build-request-dropped", (event) => {
    const id = event.detail?.id;
    if (id && formID === id) dismissRequest(id);
  });
  // The form started it: the request is consumed, and the watch says so.
  document.addEventListener("aucom:build-request-started", (event) => {
    if (event.detail?.id && formID === event.detail.id) formID = null;
    watch();
  });

  function retryable(card) {
    return card.details && (!card.details.ok && card.details.status !== 409 || card.details.body?.not_found);
  }

  window.setInterval(watch, WATCH_MS);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) return;
    watch();
    for (const [id, card] of cards) if (retryable(card)) resolve(id, true);
  });
  window.addEventListener("focus", watch);
  // A sign-in or a sign-out changes what resolving a request says.
  document.addEventListener("aucom:status", () => {
    for (const [id, card] of cards) {
      if (!card.details || card.details.body?.sign_in_required || card.details.body?.not_found || !card.details.ok) resolve(id, true);
    }
  });
  watch();
})();
