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
//   - Nothing here runs anything. A request that can run is prepared by itself
//     (NEW_380, HITL: no separate Review step): the form opens on the exact saved
//     revision, its automatic check runs, and Build is enabled. A person presses
//     it; the command stays under Technical details. A received or prepared
//     request is neither compiling nor finished, and the editor is told only
//     that it is ready.
//   - What the Build button is aimed at follows what this notice shows. A
//     request replaced, dismissed, gone or no longer this account's is dropped
//     from the form in the same turn, before anything newer is prepared, and the
//     server checks the press again whatever the page believes.

"use strict";

(() => {
  const { $, el, api, withBusy, record, t, pipelineReady, executionChoices, setupCauses, setupLink, trapFocus, badge } = window.AUCOM;

  const WATCH_MS = 2000;
  const box = $("leak-request");

  // `seen` is the request the notice is about; `details` is what resolving it
  // said. `form` is how far THIS page got with it: "" nothing yet, "preparing"
  // the form is being filled and checked, "ready" Build is enabled, "blocked" the
  // check says why not, "busy" another build is running here, "left" a person
  // changed the form into another build. `formWhy` is the sentence for the last
  // two. `formKey` is what was prepared, so one request, pipeline and setup is
  // prepared once and a changed pin or a recovered setup prepares again.
  let seen = null;
  let details = null;
  let form = "";
  let formWhy = "";
  let formKey = "";
  let told = null; // the request the editor was told is ready
  let generation = 0;
  let reading = false;
  let resolving = false;
  let checkingSetup = false;
  let account = null;
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
  // The maintained build a pipeline's compile stage runs, with the version the
  // program itself reported when it has been asked.
  function toolName(body) {
    const name = body.pipeline_tool || compilerName(body.compiler);
    return body.pipeline_tool_version ? t("{name}, installed {version}", { name, version: body.pipeline_tool_version }) : name;
  }

  function button(text, kind, onClick) {
    const control = el("button", { text, attrs: { type: "button", class: kind } });
    control.addEventListener("click", () => withBusy(control, onClick));
    return control;
  }

  function render() {
    // The tab strip is the one thing visible of a window that is behind another.
    document.title = seen && form !== "ready" ? t("Leak test requested") + " — " + title : title;
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
      actions.append(button(t("Choose leak-test pipeline"), "primary", async () => openPin(details.body.game_profile)));
      if (askedFor !== seen.request_id) { askedFor = seen.request_id; openPin(details.body.game_profile); }
    } else {
      renderPrepared(details.body, state, actions);
    }
    actions.prepend(dismiss);
    // The request is HERE either way. Whether the editor's tab knows is a
    // separate fact, said only when it does not.
    const relay = seen.relay?.state;
    const heard = relay === "retrying"
      ? t("The editor has not been told yet: the account server is not answering. The Companion keeps trying; the request stays here.")
      : relay === "stopped"
        ? t("The editor cannot be told about this request: the account server rejected it. The line above says why.")
        : "";
    const lines = [head, what, state];
    if (heard) lines.push(el("p", { className: "leak-request__relay hint", text: heard }));
    box.replaceChildren(...lines, actions);
  }

  // What a request that can be built says: which game, which compiler, which
  // pipeline, and how far it is from Build. Everything here is the server's or
  // the form's own answer; nothing is called ready because a button was removed.
  function renderPrepared(body, state, actions) {
    const identity = t("{game} map. Compiler: {tool}. Pipeline: {pipeline}.", {
      game: gameName(body.game_profile), tool: toolName(body), pipeline: body.pipeline_name || body.pipeline });
    const change = () => button(t("Change pipeline"), "secondary", async () => openPin(body.game_profile));
    const toBuild = () => button(t("Go to Build"), "secondary", async () => window.AUCOM.showArea("build"));
    if (!pipelineReady(body)) {
      state.textContent = identity + " " + t("It needs setup before it can run.");
      for (const cause of setupCauses(body)) {
        state.append(el("span", { className: "leak-request__cause", children: [document.createTextNode(" " + cause.text + " "), setupLink(cause)] }));
      }
      actions.append(change());
    } else if (form === "ready") {
      state.textContent = identity + " " + t("Ready: nothing runs until you press Build.")
        + (stagesLine(body.pipeline_steps) ? " " + stagesLine(body.pipeline_steps) : "");
      actions.append(change(), toBuild());
    } else if (form === "busy") {
      state.textContent = identity + " " + formWhy;
      actions.append(toBuild());
    } else if (form === "left") {
      state.textContent = identity + " " + t("The form below no longer builds this leak test, so Build there will not answer the editor.");
      actions.append(button(t("Open leak test"), "primary", async () => { formKey = ""; await prepare(); }));
    } else if (form === "blocked") {
      state.className = "leak-request__state message error";
      state.textContent = identity + " " + formWhy;
      actions.append(change(), button(t("Retry"), "secondary", async () => { formKey = ""; dropForm(); await resolve(true); }));
    } else {
      state.textContent = identity + " " + t("Checking setup…");
      actions.append(change());
    }
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
  // away and the watch re-reads — B is never shown, prepared or built as A.
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
    await prepare();
  }

  // dropForm takes the request out of the Build form, now, in this turn: from
  // here on the button cannot be aimed at it.
  function dropForm() {
    const id = seen?.request_id;
    form = formWhy = "";
    if (id) window.AUCOM.areas.build?.dropLeakRequest?.(id);
  }

  function forget() {
    dropForm();
    generation += 1;
    seen = details = told = null;
    formKey = "";
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
      dropForm();
      generation += 1;
      seen = body;
      details = told = null;
      formKey = "";
      render();
      record(t("Leak test requested by the editor"), t("map {id}, revision {n}", { id: body.asset_id, n: body.revision }), "running");
    } else if ((body.relay?.state || "") !== (seen.relay?.state || "")) {
      // Whether the editor has been told changed; the request did not.
      seen = body;
      render();
    }
    resolve(false);
    recheckSetup();
  }

  // A request waiting for setup: is it done? Asked of this machine alone, on the
  // watch that is already running, so a tool installed or a folder chosen enables
  // Build without another request. Only then is the account's server asked again.
  async function recheckSetup() {
    const body = details?.ok ? details.body : null;
    if (!body?.pipeline || pipelineReady(body) || checkingSetup) return;
    checkingSetup = true;
    const own = generation;
    const { ok, body: listed } = await api("/api/v1/leak-test/pipelines?game=" + encodeURIComponent(body.game_profile));
    checkingSetup = false;
    if (own !== generation || !ok) return;
    const item = (listed.choices || []).find((choice) => choice.id === body.pipeline);
    if (listed.pinned !== body.pipeline || pipelineReady(item)) resolve(true);
  }

  // prepare fills the Build form with the request on screen, once per request,
  // pipeline and setup. It starts nothing: the form checks the exact choices by
  // itself and says how that went (`aucom:leak-form`). `current` is how the form
  // knows, after its own waits, that this is still the request to prepare.
  async function prepare() {
    if (!seen || !details?.ok || !sameRequest(details.body, seen)) return;
    const request = details.body;
    if (request.sign_in_required || request.unsupported || request.needs_pipeline || !request.pipeline || !pipelineReady(request)) return;
    const key = JSON.stringify([request.request_id, request.revision_id, request.pipeline]);
    if (key === formKey) return;
    const own = generation;
    formKey = key;
    form = "preparing";
    render();
    window.AUCOM.showArea("build");
    const refused = await window.AUCOM.areas.build.adoptLeakRequest(request, () => own === generation && formKey === key);
    if (own !== generation || formKey !== key) return;
    if (refused) {
      form = "busy";
      formWhy = refused;
      formKey = "";
      render();
    }
  }

  // The form's own account of itself. Nothing about any other request counts.
  document.addEventListener("aucom:leak-form", async ({ detail }) => {
    if (!seen || detail?.request_id !== seen.request_id) return;
    form = detail.state === "ok" ? "ready" : detail.state;
    formWhy = detail.why || "";
    render();
    // The editor's "ready to build" is true only from here. An answer to this
    // about a request since replaced changes nothing on screen.
    if (form === "ready" && told !== seen.request_id) {
      told = seen.request_id;
      const own = generation;
      const answer = await api("/api/v1/leak-test/reviewing", { method: "POST", body: { request_id: seen.request_id } });
      if (own === generation && !answer.ok) watch();
    }
  });
  // Another build on this page ended: a request that was waiting for it goes on.
  document.addEventListener("aucom:build-ended", () => { if (form === "busy") { formKey = ""; prepare(); } });
  // The server refused the press, or the request was used: the request is asked
  // about again, and what it says now is what the notice shows.
  document.addEventListener("aucom:leak-build-refused", ({ detail }) => {
    if (!seen || detail?.request_id !== seen.request_id) return;
    dropForm();
    formKey = "";
    resolve(true);
  });
  document.addEventListener("aucom:leak-build-started", () => watch());

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
  let pinGeneration = 0;
  // Each opening of the dialog is a session. A pin answered after the dialog
  // was closed and opened again — for a newer request — belongs to the old
  // opening, and neither closes the new one nor prepares anything (NEW_345).
  let pinSession = 0;
  let pinOpener = null;
  let choices = [];
  const pinModal = $("leak-pin-modal");

  function chosen() {
    return choices.find((item) => item.id === $("leak-pin-select").value) || null;
  }

  // What a pipeline runs, said before it is chosen or built (NEW_340A): a
  // pinned pipeline may run more stages than the one compile stage a leak test
  // needs, and those run only when the map is sealed.
  function stagesLine(steps) {
    if (!steps || !steps.length) return "";
    const titles = steps.map((step) => step.title || step.id).join(", ");
    return steps.length === 1
      ? t("It runs one stage: {stages}.", { stages: titles })
      : t("It runs {n} stages: {stages}. A leak test needs only the first; the others run when the map is sealed.", { n: steps.length, stages: titles });
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
      // Nothing installed can test this game at all: the one thing to do is
      // in Profiles, where a pipeline is installed or written (New profile).
      status.replaceChildren(choices.length
        ? el("p", { text: t("No pipeline is ready yet. Set up the tool one of them needs, then come back.") })
        : el("p", { children: [document.createTextNode(t("No installed pipeline can test {game} maps.", { game: gameName(pinGame) }) + " "),
          el("a", { text: t("Open Profiles"), attrs: { href: "#profiles" } })] }));
      return;
    }
    if (!item) { status.replaceChildren(); return; }
    if (pipelineReady(item)) {
      status.replaceChildren(
        el("p", { className: "leak-pin__selected", children: [badge(t("Ready"), "ok"), document.createTextNode(" "), el("strong", { text: item.name })] }),
        ...(item.summary ? [el("p", { className: "leak-pin__summary", text: item.summary })] : []),
        ...(stagesLine(item.steps) ? [el("p", { className: "leak-pin__summary", text: stagesLine(item.steps) })] : []));
      return;
    }
    status.replaceChildren(
      el("p", { className: "leak-pin__selected", children: [badge(t("Needs setup"), "blocked"), document.createTextNode(" "), el("strong", { text: item.name })] }),
      ...setupCauses(item).map((cause) => el("p", { className: "leak-pin__summary", children: [
        document.createTextNode(cause.text + " "), setupLink(cause)] })));
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

  async function openPin(game) {
    pinGame = game;
    pinSession += 1;
    if (pinModal.hidden) pinOpener = document.activeElement;
    $("leak-pin-intro").textContent = t("Choose a pipeline for {game} leak tests. You can change it later.", { game: gameName(game) })
      + " " + t("Nothing runs until you press Build.");
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
    const game = pinGame;
    const { ok, body } = await api("/api/v1/leak-test/pipelines", { method: "POST", body: { game, pipeline } });
    if (session !== pinSession) {
      // Answered for an opening that is gone. The pin, if recorded, is the
      // game's setting; a dialog open now shows it. Nothing is prepared.
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
    // for it, then ask again, so what is prepared is the pinned pipeline (found
    // live on Windows: the continuation worked from a stale "nothing pinned").
    // `resolve` prepares whatever the request on screen now says, so a request
    // that replaced this one meanwhile is shown as itself, and a pin changed
    // elsewhere is the pipeline prepared. Neither starts anything.
    while (resolving) await new Promise((done) => setTimeout(done, 100));
    dropForm();
    formKey = "";
    await resolve(true);
    if (!pinModal.hidden) return;
    if (!box.querySelector("button.primary, button")) return;
    box.querySelector("button.primary, button").focus?.();
  }));
  for (const close of [$("leak-pin-cancel"), $("leak-pin-close")]) close.addEventListener("click", () => closePin());
  pinModal.querySelector("[data-dismiss=leak-pin]")?.addEventListener("click", () => closePin());
  pinModal.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); closePin(); return; }
    trapFocus(event, $("leak-pin-dialog"));
  });
  // A "Set up" link goes to Profiles: the dialog closes, and the request's
  // notice is where to come back to — choosing again reads the list afresh.
  $("leak-pin-dialog").addEventListener("click", (event) => {
    if (event.target.closest?.('a[href^="#profiles"]')) closePin({ restoreFocus: false });
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
  // A sign-in or a sign-out changes what resolving the request says, and another
  // account is another reader: what this page prepared for the last one is not
  // left under a Build button.
  document.addEventListener("aucom:status", () => {
    const status = window.AUCOM.status;
    const now = JSON.stringify([Boolean(status?.authenticated), status?.email || "", status?.user_id || ""]);
    const changed = account !== null && account !== now;
    account = now;
    if (!seen) return;
    if (changed) { dropForm(); formKey = ""; resolve(true); return; }
    if (!details || details.body?.sign_in_required || !details.ok) resolve(true);
  });
  watch();
})();
