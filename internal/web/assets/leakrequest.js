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
  const { $, el, api, withBusy, record, t } = window.AUCOM;

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
    return { "ericw-qbsp": "qbsp (EricW)", q3map2: "Q3Map2" }[id] || id || t("the compiler");
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
    } else if (reviewing === seen.request_id) {
      state.textContent = t("In review. The Build area shows the exact command; nothing runs until you press Build.");
      actions.append(button(t("Go to the review"), "secondary", async () => window.AUCOM.showArea("build")));
    } else {
      state.textContent = t("Its content still matches what the editor asked about. It is a {game} map, so the test is {compiler}'s. Review the compiler command first; nothing runs until you press Build.", {
        game: gameName(details.body.game_profile), compiler: compilerName(details.body.compiler) });
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
