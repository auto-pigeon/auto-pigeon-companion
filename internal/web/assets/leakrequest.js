// The editor's leak-test request, shown above every area (NEW_307W).
//
// A link from the editor only RECORDS a request on this machine. The page that
// showed it used to be the Build area alone, and only when somebody entered
// it: a Companion already open on My Maps said nothing, and the new tab the
// operating system opened was in another browser. So this file watches for a
// request from wherever the page is, and says so in one notice.
//
// Three rules:
//
//   - The watch is cheap and local. `/api/v1/leak-test/request` answers from
//     this machine and calls nobody. The saved revision is resolved against
//     the account's server ONCE per request (and again only when somebody
//     asks: Retry, a sign-in, the window coming back into view).
//   - The watch does not stop for a page nobody is looking at. The person who
//     just pressed Test in Companion is looking at the EDITOR, so this window
//     is behind it, and a browser calls a covered window hidden (found live:
//     a Companion tab under the editor's window never noticed the request).
//     A hidden page still learns of it, and says so in its tab title.
//   - One read at a time, and an answer about a request that has since been
//     replaced is thrown away. `generation` is what tells them apart.
//   - Nothing here runs anything. Review takes the request into the Build
//     wizard; the wizard shows the command; a person presses Build.

"use strict";

(() => {
  const { $, el, api, withBusy, record, t, pipelineReady, executionChoices, setupCauses, badge } = window.AUCOM;

  const WATCH_MS = 2000;
  const box = $("leak-request");

  // `seen` is the request the notice is about; `details` is what resolving it
  // said; `reviewing` is the request taken into the wizard from THIS page.
  let seen = null;
  let details = null;
  let reviewing = null;
  let generation = 0;
  let reading = false;
  let resolving = false;
  const title = document.title;

  function when(value) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "" : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  // The two names a reader sees for what the server calls a game and a
  // compiler. An id this page has no name for is shown as it is.
  function gameName(id) {
    return { quake1: t("Quake 1"), quake2: t("Quake II"), quake3: t("Quake III") }[id] || id || t("unknown game");
  }
  function compilerName(id) {
    return { "ericw-qbsp": "qbsp (EricW)", "ericw-qbsp-q2": "qbsp (EricW, Quake II mode)", q3map2: "Q3Map2" }[id] || id || t("the compiler");
  }

  function button(text, kind, onClick) {
    const control = el("button", { text, attrs: { type: "button", class: kind } });
    control.addEventListener("click", () => withBusy(control, onClick));
    return control;
  }

  function render() {
    // The tab strip is the one thing visible of a window that is behind another.
    document.title = seen && reviewing !== seen.request_id ? t("Leak test requested") + " — " + title : title;
    if (!seen) { box.hidden = true; box.replaceChildren(); return; }
    box.hidden = false;
    const name = details?.body?.name || seen.asset_id;
    const head = el("p", { className: "leak-request__title", text: t("Leak test requested by the editor") });
    const what = el("p", { className: "leak-request__what", text: t("{name}, saved revision {n} — received {time}", {
      name, n: seen.revision, time: when(seen.received_at) }) });
    const state = el("p", { className: "leak-request__state" });
    const actions = el("div", { className: "row-actions" });
    // Bound to the request THIS notice shows, not to whatever is on screen
    // when the click is handled.
    const shown = seen.request_id;
    const dismiss = button(t("Dismiss"), "secondary", () => dismissRequest(shown));

    if (!details) {
      state.textContent = t("Checking the saved revision with your account…");
    } else if (details.body.sign_in_required) {
      state.textContent = t("Sign in to the account this map is in. The Companion cannot read the saved revision, or return a result, while signed out.");
      actions.append(button(t("Sign in"), "primary", async () => window.AUCOM.openSignIn?.()));
    } else if (!details.ok) {
      state.className = "leak-request__state message error";
      state.textContent = details.status === 409
        ? t("This request cannot be used: {why}. Press Test in Companion in the editor again.", { why: details.body.error })
        : details.status === 404 || details.status === 403
          ? t("This account cannot read that map: {why}. Check that the Companion is signed in to the same server and account as the editor.", { why: details.body.error })
          : t("The saved revision could not be checked: {why}", { why: details.body.error || t("the account server did not answer") });
      if (details.status !== 409) actions.append(button(t("Retry"), "secondary", async () => resolve(true)));
    } else if (details.body.unsupported) {
      // Not a Quake 1 test by default, and not a Retry: the game has no leak
      // test here, and nothing was run (Q3_018).
      state.className = "leak-request__state message error";
      state.textContent = t("This saved revision is a {game} map. The Companion has a leak test for {list} only, so nothing was run and nothing will be.", {
        game: gameName(details.body.game_profile), list: (details.body.supported_profiles || []).map(gameName).join(", ") });
    } else if (details.body.needs_pipeline) {
      // Nothing is pinned for this game (NEW_310, HITL): the user chooses
      // which installed pipeline tests it, once, and the Companion keeps it.
      state.textContent = (details.body.pinned_problem
        ? t("The pipeline pinned for {game} leak tests cannot be used any more: {why}.", { game: gameName(details.body.game_profile), why: details.body.pinned_problem }) + " "
        : t("No pipeline is chosen yet for testing {game} maps for leaks on this computer.", { game: gameName(details.body.game_profile) }) + " ")
        + t("Choose one; it is kept for every later request.");
      actions.append(button(t("Choose leak-test pipeline"), "primary", async () => openPin(details.body.game_profile, true)));
      if (askedFor !== seen.request_id) { askedFor = seen.request_id; openPin(details.body.game_profile, true); }
    } else if (reviewing === seen.request_id) {
      state.textContent = t("In review. The Build area shows the exact command; nothing runs until you press Build.");
      actions.append(button(t("Go to the review"), "secondary", async () => window.AUCOM.showArea("build")));
    } else {
      state.textContent = t("Its content still matches what the editor asked about. It is a {game} map, so the test is {compiler}'s. Review the compiler command first; nothing runs until you press Build.", {
        game: gameName(details.body.game_profile), compiler: compilerName(details.body.compiler) });
      actions.append(button(t("Change pipeline"), "secondary", async () => openPin(details.body.game_profile, false)));
      actions.append(button(t("Review leak test"), "primary", review));
    }
    actions.prepend(dismiss);
    // The request is HERE either way. Whether the editor's tab knows is a
    // separate fact, said only when it does not.
    const relay = seen.relay?.state;
    const told = relay === "retrying"
      ? t("The editor has not been told yet: the account server is not answering. The Companion keeps trying; the request stays here.")
      : relay === "stopped"
        ? t("The editor cannot be told about this request: the account server rejected it. The review above says why.")
        : "";
    const lines = [head, what, state];
    if (told) lines.push(el("p", { className: "leak-request__relay hint", text: told }));
    box.replaceChildren(...lines, actions);
  }

  // sameRequest: does an answer speak about the request on screen? Every field
  // both sides carry must agree — an id reused for other bytes is not it.
  function sameRequest(body, request) {
    if (!body || !request || body.request_id !== request.request_id) return false;
    return ["asset_id", "revision", "content_sha256"].every((key) =>
      body[key] === undefined || request[key] === undefined || body[key] === request[key]);
  }

  // resolve asks the account's server about the request on screen. `force` is
  // a person asking again; without it an answered request is not asked twice.
  //
  // It NAMES the request it asks about. The pending request can be replaced
  // between the watch that showed A and this question, and the answer would
  // then be about B: the server says so (`replaced`) instead of resolving B,
  // and an answer that names anything but the request on screen is thrown
  // away and the watch re-reads — B is never shown, reviewed or built as A.
  async function resolve(force) {
    if (!seen || resolving || (details && !force)) return;
    resolving = true;
    const own = generation;
    const asked = seen;
    if (force) { details = null; render(); }
    const answer = await api("/api/v1/leak-test/pending?request_id=" + encodeURIComponent(asked.request_id));
    resolving = false;
    if (own !== generation) { watch(); return; } // Another request arrived meanwhile.
    if (answer.ok && !answer.body.pending) { forget(); return; }
    if (answer.ok && (answer.body.replaced || !sameRequest(answer.body, asked))) { watch(); return; }
    details = answer;
    render();
  }

  function forget() {
    generation += 1;
    seen = details = reviewing = null;
    render();
  }

  async function watch() {
    if (reading) return;
    reading = true;
    const { ok, body } = await api("/api/v1/leak-test/request");
    reading = false;
    if (!ok) return; // The local server is away; the stale-page banner says so.
    if (!body.pending) { if (seen) forget(); return; }
    if (!seen || !sameRequest(body, seen)) {
      generation += 1;
      seen = body;
      details = reviewing = null;
      render();
      record(t("Leak test requested by the editor"), t("map {id}, revision {n}", { id: body.asset_id, n: body.revision }), "running");
    } else if ((body.relay?.state || "") !== (seen.relay?.state || "")) {
      // Whether the editor has been told changed; the request did not.
      seen = body;
      render();
    }
    resolve(false);
  }

  async function review() {
    if (!seen || !details?.ok || !sameRequest(details.body, seen)) return;
    if (details.body.needs_pipeline || !details.body.pipeline) return; // nothing pinned: the dialog asks first
    const own = generation;
    const request = details.body;
    const told = await api("/api/v1/leak-test/reviewing", { method: "POST", body: { request_id: request.request_id } });
    if (own !== generation) return;
    if (!told.ok) { details = told; render(); return; }
    window.AUCOM.showArea("build");
    const refused = await window.AUCOM.areas.build.adoptLeakRequest(request);
    if (own !== generation) return;
    if (refused) {
      box.querySelector(".leak-request__state").textContent = refused;
      return;
    }
    reviewing = request.request_id;
    render();
  }

  // --- choosing the pipeline -------------------------------------------------
  //
  // The dialog lists every installed pipeline that can answer this game's leak
  // test (the server decides which can), with the readiness Build & Run shows.
  // One that needs setup is listed disabled and cannot be chosen; what it waits
  // for is ONE closed "Other pipelines (n need setup)" disclosure under the
  // select (core.js setupActions), each row linking to the tool to set up
  // (NEW_345). Only the SELECTED pipeline's state is said in the open. Pinning
  // sends the choice to the server, which refuses a pipeline that cannot be
  // this game's leak test. Choosing runs nothing: a pin is a setting.
  let askedFor = null; // the request whose dialog opened by itself, once
  let pinGame = "";
  let pinThenReview = false;
  // The request the dialog was opened for, and the generation it was seen in:
  // a pin confirmed after that request was replaced reviews nothing.
  let pinFor = null;
  let pinGeneration = 0;
  // Each opening of the dialog is a session. A pin answered after the dialog
  // was closed and opened again — for a newer request — belongs to the old
  // opening, and neither closes the new one nor reviews anything (NEW_345).
  let pinSession = 0;
  let pinOpener = null;
  let choices = [];
  const pinModal = $("leak-pin-modal");

  function chosen() {
    return choices.find((item) => item.id === $("leak-pin-select").value) || null;
  }

  // The selected pipeline's state, in one place: Ready with its full name and
  // its own summary, or what it waits for with a link to fix it. Nothing about
  // the others is said here.
  function renderPinStatus() {
    const status = $("leak-pin-status");
    const item = chosen();
    $("leak-pin-confirm").disabled = !item || !pipelineReady(item);
    $("leak-pin-select").title = item?.name || "";
    if (!choices.some(pipelineReady) && !$("leak-pin-field").hidden) {
      status.replaceChildren(el("p", { text: choices.length
        ? t("No pipeline is ready yet. Set up the tool one of them needs, then come back.")
        : t("No installed pipeline can test {game} maps.", { game: gameName(pinGame) }) }));
      return;
    }
    if (!item) { status.replaceChildren(); return; }
    if (pipelineReady(item)) {
      status.replaceChildren(
        el("p", { className: "leak-pin__selected", children: [badge(t("Ready"), "ok"), document.createTextNode(" "), el("strong", { text: item.name })] }),
        ...(item.summary ? [el("p", { className: "leak-pin__summary", text: item.summary })] : []));
      return;
    }
    status.replaceChildren(
      el("p", { className: "leak-pin__selected", children: [badge(t("Needs setup"), "blocked"), document.createTextNode(" "), el("strong", { text: item.name })] }),
      ...setupCauses(item).map((cause) => el("p", { className: "leak-pin__summary", children: [
        document.createTextNode(cause.text + " "),
        el("a", { text: t("Set up"), attrs: { href: "#profiles/" + encodeURIComponent(cause.profile), "aria-label": t("Set up {name}", { name: cause.name }) } }),
      ] })));
  }

  async function loadPin() {
    const own = ++pinGeneration;
    const select = $("leak-pin-select");
    const message = $("leak-pin-message");
    const { ok, body } = await api("/api/v1/leak-test/pipelines?game=" + encodeURIComponent(pinGame));
    if (own !== pinGeneration) return; // A newer refresh owns the dialog.
    message.textContent = "";
    message.className = "message";
    $("leak-pin-retry-row").hidden = ok;
    $("leak-pin-field").hidden = !ok;
    if (!ok) {
      // One failure, one remedy: the list is not shown half-empty, and what
      // was chosen is kept for when it answers again.
      choices = [];
      message.className = "message error";
      message.textContent = t("The pipelines could not be listed: {why}", { why: body.error || t("the Companion did not answer") });
      renderPinStatus();
      return;
    }
    const previous = select.value || body.pinned || "";
    choices = body.choices || [];
    const label = (item) => item.name + (item.builtin ? " — " + t("built in") : "") +
      (pipelineReady(item) ? "" : " — " + t("needs setup"));
    const anyReady = choices.some(pipelineReady);
    executionChoices(select, choices, anyReady ? previous : "", pipelineReady, label,
      !choices.length ? t("No installed pipeline can test these maps")
        : choices.filter(pipelineReady).length === 1 ? t("Choose a pipeline (1 ready)…")
          : t("Choose a pipeline ({n} ready)…", { n: choices.filter(pipelineReady).length }),
      false, true, undefined, { setupLabel: anyReady ? "pipelines" : "all-pipelines" });
    // Nothing ready: no dead list to pick from. What to do is said first, and
    // the rows that say what each pipeline needs are the content, open.
    const setup = $("leak-pin-field").querySelector(".choice-setup");
    select.hidden = !anyReady;
    $("leak-pin-label").hidden = !anyReady;
    if (!anyReady && setup) setup.open = true;
    renderPinStatus();
  }

  async function openPin(game, thenReview) {
    pinGame = game;
    pinThenReview = thenReview;
    pinSession += 1;
    pinFor = seen ? { request: seen.request_id, generation } : null;
    if (pinModal.hidden) pinOpener = document.activeElement;
    $("leak-pin-intro").textContent = t("Choose a pipeline for {game} leak tests. You can change it later.", { game: gameName(game) })
      + (thenReview ? " " + t("Next, you review the test; nothing runs until you press Build.") : "");
    pinModal.hidden = false;
    $("leak-pin-select").focus();
    await loadPin();
    if (!pinModal.hidden && !pinModal.contains(document.activeElement)) {
      ($("leak-pin-select").hidden ? $("leak-pin-close") : $("leak-pin-select")).focus();
    }
  }

  // Closing changes nothing: no pin, no request, no job (NEW_310: Cancel starts
  // nothing and keeps the request). Focus goes back where it came from.
  function closePin({ restoreFocus = true } = {}) {
    if (pinModal.hidden) return;
    pinModal.hidden = true;
    pinGeneration += 1;
    pinSession += 1;
    if (restoreFocus && pinOpener && document.contains(pinOpener)) pinOpener.focus();
    pinOpener = null;
  }

  $("leak-pin-select").addEventListener("change", renderPinStatus);
  $("leak-pin-retry").addEventListener("click", () => withBusy($("leak-pin-retry"), loadPin));
  $("leak-pin-confirm").addEventListener("click", () => withBusy($("leak-pin-confirm"), async () => {
    const pipeline = $("leak-pin-select").value;
    if (!pipeline || !pipelineReady(chosen())) return;
    // Whose pin this is, decided now — not after the answer, by which time the
    // dialog may be showing a newer request (NEW_345).
    const session = pinSession;
    const asked = pinFor;
    const game = pinGame;
    const continueToReview = pinThenReview;
    const { ok, body } = await api("/api/v1/leak-test/pipelines", { method: "POST", body: { game, pipeline } });
    if (session !== pinSession) {
      // Answered for an opening that is gone. The pin, if recorded, is the
      // game's setting; a dialog open now shows it. Nothing is reviewed.
      if (!pinModal.hidden) loadPin();
      return;
    }
    if (!ok) {
      $("leak-pin-message").className = "message error";
      $("leak-pin-message").textContent = t("The pipeline could not be used: {why}", { why: body.error || t("the Companion did not answer") });
      // The busy button let focus fall to the page; it goes back to the choice.
      $("leak-pin-select").focus();
      return;
    }
    record(t("Leak tests for {game} use {pipeline}", { game: gameName(game), pipeline }), "", "ok");
    closePin({ restoreFocus: false });
    // A read that started before the pin answers for the time before it: wait
    // for it, then ask again, so the review is of the pinned pipeline (found
    // live on Windows: the continuation reviewed a stale "nothing pinned").
    while (resolving) await new Promise((done) => setTimeout(done, 100));
    await resolve(true);
    // Review only the request the dialog was opened for, still current, and
    // only with the pipeline just pinned (NEW_310A): a request that replaced
    // it meanwhile, or a pin changed elsewhere, is shown, never started.
    const still = asked && seen && seen.request_id === asked.request && generation === asked.generation;
    if (continueToReview && still && details?.ok && details.body.pipeline === pipeline && !details.body.needs_pipeline) await review();
    else box.querySelector("button.primary, button")?.focus();
  }));
  for (const close of [$("leak-pin-cancel"), $("leak-pin-close")]) close.addEventListener("click", () => closePin());
  pinModal.querySelector("[data-dismiss=leak-pin]")?.addEventListener("click", () => closePin());
  pinModal.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); closePin(); return; }
    // Focus stays inside the open dialog: Tab past the last control comes back
    // to the first, Shift+Tab the other way (the sign-in dialog's rule).
    if (event.key !== "Tab") return;
    const focusable = [...$("leak-pin-dialog").querySelectorAll("button, select, a[href], summary")]
      .filter((node) => !node.disabled && node.offsetParent !== null);
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  });
  // A "Set up" link goes to Profiles: the dialog closes, and the request's
  // notice is where to come back to — choosing again reads the list afresh.
  $("leak-pin-dialog").addEventListener("click", (event) => {
    if (event.target.closest?.('a[href^="#profiles/"]')) closePin({ restoreFocus: false });
  });
  // Back from another window with the dialog's game still unanswered: list again.
  document.addEventListener("visibilitychange", () => { if (!document.hidden && !pinModal.hidden) loadPin(); });

  // Dismiss names the request it is dismissing, so a newer one that arrived
  // while this notice was on screen is not the one forgotten.
  async function dismissRequest(id) {
    if (!id) return;
    await api("/api/v1/leak-test/dismiss", { method: "POST", body: { request_id: id } });
    if (seen && seen.request_id === id) forget();
    watch();
  }

  window.setInterval(watch, WATCH_MS);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) { watch(); if (details && !details.ok && details.status !== 409) resolve(true); } });
  window.addEventListener("focus", watch);
  // A sign-in or a sign-out changes what resolving the request says.
  document.addEventListener("aucom:status", () => { if (seen && (!details || details.body?.sign_in_required || !details.ok)) resolve(true); });
  watch();
})();
