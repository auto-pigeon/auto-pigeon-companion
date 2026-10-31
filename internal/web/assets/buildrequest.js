// The editor's build request, shown above every area.
//
// The editor's Build button opens `autopigeon://build/request/<id>`, and the
// link only RECORDS the request on this machine. This file watches for one from
// wherever the page is, says so in one notice and opens Build & Run's own form
// (play.js) on the exact saved revision, with the map and the default tool and
// engine chosen; the person presses Build or Build & Run there (HITL,
// 2026-10-31: no Review build, no Dismiss). It is leakrequest.js's pattern for a
// different request — not a leak test, and nothing here touches one.
//
//   - The watch is cheap and local: `/api/v1/build-request/request` answers
//     from this machine and calls nobody. The request is resolved against the
//     account's server once per request, and again only when somebody asks
//     (Retry, a sign-in, the window coming back into view).
//   - One read at a time, and an answer about a request that has since been
//     replaced is thrown away. `generation` is what tells them apart.
//   - Nothing here runs anything. "Opened" is not "started".

"use strict";

(() => {
  const { $, el, api, withBusy, record, t } = window.AUCOM;

  const WATCH_MS = 2000;
  const box = $("build-request");

  // `seen` is the request the notice is about; `details` is what resolving it
  // said; `reviewing` is the request taken into Build & Run from THIS page.
  let seen = null;
  let details = null;
  let reviewing = null;
  let opened = null;
  let generation = 0;
  let reading = false;
  let resolving = false;

  // The association rule, one copy, which play.js asks too: the form still
  // builds what the editor asked for only while it is the saved map, the exact
  // revision and the same Run after build.
  function stillAsked(request, choice) {
    return Boolean(request && choice) && choice.source === "saved" && choice.asset_id === request.asset_id &&
      choice.revision_id === request.revision_id && choice.run_after === request.run_after;
  }
  window.AUCOM.buildRequest = { stillAsked };

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

  function render() {
    if (!seen) { box.hidden = true; box.replaceChildren(); return; }
    box.hidden = false;
    const runAfter = seen.run_after === true;
    const action = runAfter ? t("Build & Run") : t("Build");
    const head = el("p", { className: "leak-request__title",
      text: runAfter ? t("Build & Run requested by the editor") : t("Build requested by the editor") });
    const lines = [head];
    if (details?.ok && details.body.revision) {
      lines.push(el("p", { className: "leak-request__what", text: t("{name}, saved revision {n} — received {time}", {
        name: details.body.name || details.body.asset_id, n: details.body.revision, time: when(seen.received_at) }) }));
    }
    const state = el("p", { className: "leak-request__state" });
    const actions = el("div", { className: "row-actions" });
    // Bound to the request THIS notice shows, not to whatever is on screen
    // when the click is handled.
    const shown = seen.request_id;
    const dismiss = () => button(t("Dismiss"), "secondary", () => dismissRequest(shown));

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
      actions.append(dismiss(), button(t("Retry"), "secondary", async () => resolve(true)));
    } else if (!details.ok) {
      state.className = "leak-request__state message error";
      state.textContent = details.status === 409
        ? t("This request cannot be built: {why}.", { why: details.body.error })
        : t("The request could not be checked: {why}", { why: details.body.error || t("the account server did not answer") });
      actions.append(dismiss());
      if (details.status !== 409) actions.append(button(t("Retry"), "secondary", async () => resolve(true)));
    } else if (details.body.unsupported) {
      // Not Quake 1 by default, and no review: nothing was run and nothing will be.
      state.className = "leak-request__state message error";
      state.textContent = t("This saved revision is a {game} map. Build & Run builds {list} maps only, so nothing will run.", {
        game: gameName(details.body.game_profile), list: (details.body.supported_profiles || []).map(gameName).join(", ") });
      actions.append(dismiss());
    } else {
      state.textContent = t("Ready in Build & Run. Nothing runs until you press {action}.", { action });
      if (opened !== seen.request_id) { opened = seen.request_id; review(); }
    }
    lines.push(state);
    // The request is HERE either way. Whether the editor knows is a separate
    // fact, said only when it does not.
    const relay = (details?.body?.relay || seen.relay)?.state;
    const told = relay === "retrying"
      ? t("The editor has not been told yet: the account server is not answering. The Companion keeps trying; the request stays here.")
      : relay === "stopped" ? t("The editor cannot be told about this request: the account server rejected it.") : "";
    if (told) lines.push(el("p", { className: "leak-request__relay hint", text: told }));
    box.replaceChildren(...lines, actions);
  }

  // sameRequest: does an answer speak about the request on screen?
  function sameRequest(body, request) {
    return Boolean(body && request) && body.request_id === request.request_id &&
      (body.intent === undefined || request.intent === undefined || body.intent === request.intent);
  }

  // resolve asks the account's server about the request on screen, NAMING it:
  // an answer about anything else is thrown away and the watch re-reads.
  async function resolve(force) {
    if (!seen || resolving || (details && !force)) return;
    resolving = true;
    const own = generation;
    const asked = seen;
    if (force) { details = null; render(); }
    const answer = await api("/api/v1/build-request/pending?request_id=" + encodeURIComponent(asked.request_id));
    resolving = false;
    if (own !== generation) { watch(); return; } // Another request arrived meanwhile.
    if (answer.ok && !answer.body.pending) { forget(); return; }
    if (answer.ok && (answer.body.replaced || !sameRequest(answer.body, asked))) { watch(); return; }
    details = answer;
    render();
  }

  // forget: the request on screen is gone — started, dismissed, expired or
  // replaced. A form that was building it for the editor is told.
  function forget() {
    const was = reviewing;
    generation += 1;
    seen = details = reviewing = null;
    render();
    if (was) document.dispatchEvent(new CustomEvent("aucom:build-request-gone", { detail: { id: was } }));
  }

  async function watch() {
    if (reading) return;
    reading = true;
    const { ok, body } = await api("/api/v1/build-request/request");
    reading = false;
    if (!ok) return; // The local server is away; the stale-page banner says so.
    if (!body.pending) { if (seen) forget(); return; }
    if (!seen || !sameRequest(body, seen)) {
      if (seen) forget();
      generation += 1;
      seen = body;
      details = reviewing = null;
      render();
      record(body.run_after ? t("Build & Run requested by the editor") : t("Build requested by the editor"), "", "running");
    } else if ((body.relay?.state || "") !== (seen.relay?.state || "")) {
      seen = body;
      render();
    }
    resolve(false);
  }

  // review takes the request into Build & Run. It starts nothing: the form
  // checks the choices by itself, and the person presses the button.
  async function review() {
    if (!seen || !details?.ok || !sameRequest(details.body, seen) || !details.body.revision_id) return;
    const own = generation;
    const request = details.body;
    const told = await api("/api/v1/build-request/review", { method: "POST", body: { request_id: request.request_id } });
    if (own !== generation) return;
    if (!told.ok) { details = told; render(); return; }
    reviewing = request.request_id;
    render();
    window.AUCOM.showArea("play");
    await window.AUCOM.areas.play?.adoptBuildRequest?.(request);
  }

  // Dismiss names the request it dismisses, so a newer one that arrived while
  // this notice was on screen is not the one forgotten.
  async function dismissRequest(id) {
    if (!id) return;
    await api("/api/v1/build-request/dismiss", { method: "POST", body: { request_id: id } });
    if (seen && seen.request_id === id) forget();
    watch();
  }

  // The form stopped building what the editor asked for: a person chose
  // something else, and the request is over.
  document.addEventListener("aucom:build-request-dropped", (event) => {
    if (seen && reviewing === event.detail?.id) dismissRequest(event.detail.id);
  });
  // The form started it: the request is consumed, and the watch says so.
  document.addEventListener("aucom:build-request-started", () => watch());

  window.setInterval(watch, WATCH_MS);
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) { watch(); if (details && (!details.ok && details.status !== 409 || details.body?.not_found)) resolve(true); }
  });
  window.addEventListener("focus", watch);
  // A sign-in or a sign-out changes what resolving the request says.
  document.addEventListener("aucom:status", () => {
    if (seen && (!details || details.body?.sign_in_required || details.body?.not_found || !details.ok)) resolve(true);
  });
  watch();
})();
