// Build & Run — the main product journey, and the Activity surface.
//
// Five steps, one confirmation, and everything after that confirmation happens
// on the server. This file does NOT sequence the work: it collects the exact
// identities, asks the server what it would do, sends one request, and then
// reads a durable record back. A browser tab that orchestrated eight stages
// would be a tab whose being closed lost the run.
//
// Three rules from `AUCOM/AUE/AUT 246I1` are visible here:
//
//   - tabs for small fixed choices, searchable selects for unbounded ones. Maps,
//     revisions, build profiles and engines are lists that grow; turning fifty
//     maps into fifty tabs is not an improvement.
//   - short confirmations are a status line; decisions and destructive
//     conflicts are a modal; live progress and detail are the Activity drawer.
//   - nothing is appended to the bottom of a working page, and no job result
//     ever opens a new browser tab.

"use strict";

(() => {
  const { $, el, api, setMessage, announce, when, bytes, t } = window.AUCOM;

  // The engine actions Build & Run offers, in the order they are shown, with
  // what each means in plain words. An engine's profile names the actions it
  // has; these are the ones that make sense for a map that was just built.
  const ACTIONS = {
    play_map: {
      title: "Play it",
      text: "Start the game on your map, just you. The quickest way to walk round what you built.",
    },
    host_listen: {
      title: "Host it and play",
      text: "Start a game others can join, and play in it yourself. It shows in Live Games while it runs.",
    },
    host_dedicated: {
      title: "Dedicated server",
      text: "Run a server for others without playing in it yourself. It keeps going until you stop it.",
    },
  };

  // What "Who can see it" says at the start of every launch. See state.listing.
  const FRESH_VISIBILITY = "private";

  // Every textures question is numbered, and only the latest answer is shown
  // (refreshPlan, checkOwnWads).
  let texturesAsked = 0;

  // The whole of this page's state. Exact identities, never display labels: a
  // request carrying "dm1, latest" would mean something different tomorrow.
  const state = {
    step: 1,
    maps: [],
    revisions: [],
    pipelines: [],
    engines: [],
    map: null,
    revision: null,
    sourceFile: "",
    mapName: "",
    pipeline: "",
    strict: false,
    engine: "",
    action: "",
    mod: "auto-pigeon",
    gameRoot: "",
    plan: null,
    // ownWadsDir is the folder the person confirmed, in the review, for WADs
    // the deployment did not send. Never remembered in the URL: it is a
    // path on this machine.
    ownWadsDir: "",
    // lanListings is the Auto-Pigeon server's answer, not a choice: whether it
    // lists a game on a LAN address for everyone (a development server) or only
    // for its host. Never remembered — it is read from the server each time.
    lanListings: false,
    // listing is how a hosted game appears in Live Games (step 3). Private
    // until somebody chooses otherwise (NEW_247A2): a game is seen by everyone
    // only because its host picked Everyone for this launch. Never remembered
    // in the URL, so a reload — a fresh session — is Private again too.
    listing: { enabled: true, title: "", visibility: FRESH_VISIBILITY, host: "", port: "" },
    // planKey is what the plan was computed for. Any change to an identity
    // invalidates it, and the page says so rather than reviewing a stale one.
    planKey: "",
  };

  // --- remembered choices ------------------------------------------------------
  //
  // A reload used to empty the wizard (246I1.1, seen live). The choices are kept
  // in this page's own URL query — exact identities, the map's name in the game
  // and the mod folder name; never a path, never a token, and nothing in
  // browser storage (TestThePageStoresNothingItShouldNot) — and restored once,
  // as the lists they belong to arrive. A remembered revision is used only if
  // that exact revision is still listed; otherwise the newest is chosen, which
  // is what a fresh visit does anyway.
  const REMEMBERED = {
    map: "asset_id", rev: "revision_id", name: "map_name", build: "pipeline",
    strict: "strict", engine: "engine", action: "action", mod: "mod", step: "step",
  };
  let pendingMap = "";
  let pendingRevision = "";

  function remember() {
    const values = {
      // While the lists are still arriving, the choice a link or a reload
      // named is the pending one; writing "" there would drop it from the URL.
      asset_id: state.map?.asset_id || pendingMap, revision_id: state.revision?.revision_id || pendingRevision,
      map_name: $("play-map-name").value.trim(), pipeline: state.pipeline,
      strict: state.strict ? "1" : "", engine: state.engine, action: state.action,
      mod: state.mod === "auto-pigeon" ? "" : state.mod, step: state.step > 1 ? String(state.step) : "",
    };
    const query = new URLSearchParams(window.location.search);
    for (const [key, field] of Object.entries(REMEMBERED)) {
      if (values[field]) query.set(key, values[field]);
      else query.delete(key);
    }
    const search = query.toString();
    const url = window.location.pathname + (search ? "?" + search : "") + window.location.hash;
    if (url !== window.location.pathname + window.location.search + window.location.hash) {
      window.history.replaceState(window.history.state, "", url);
    }
  }

  function restore() {
    const query = new URLSearchParams(window.location.search);
    const saved = {};
    for (const [key, field] of Object.entries(REMEMBERED)) saved[field] = query.get(key) || "";
    if (!Object.values(saved).some(Boolean)) return;
    pendingMap = saved.asset_id;
    pendingRevision = saved.revision_id;
    state.pipeline = saved.pipeline;
    state.strict = saved.strict === "1";
    state.engine = saved.engine;
    state.action = saved.action;
    state.mod = saved.mod || "auto-pigeon";
    state.step = Math.min(5, Math.max(1, Number(saved.step) || 1));
    if (saved.map_name) {
      $("play-map-name").value = saved.map_name;
      state.mapName = saved.map_name;
    }
    $("play-mod").value = state.mod;
    $("play-strict").checked = state.strict;
  }

  // --- step navigation --------------------------------------------------------

  function show(step) {
    state.step = step;
    remember();
    for (const panel of document.querySelectorAll("#area-play .bwiz-panel")) {
      panel.hidden = Number(panel.dataset.step) !== step;
    }
    markSteps();
    summarize();
    // "Started" describes the run that was started, not the next one.
    if (step !== 5) setMessage("play-start-message", "");
    // Not while the lists are still arriving: a plan asked for then would be
    // a plan for choices that are not restored yet.
    if (step === 4 && !loading) refreshPlan();
    if (step === 5 && !loading) renderFinalSummary();
    if (step === 1) showAutobuild();
    const panel = $("play-step-" + step);
    panel?.querySelector("h3")?.focus?.();
  }

  // What each step chose, written under its name in the step bar: the value
  // itself large and bright, its qualifier small beneath it. The step bar is
  // the one place a person can read the whole journey at a glance, so what was
  // chosen must stand out from the labels around it.
  function summarize() {
    const put = (n, value, detail, empty = t("not chosen yet")) => {
      const node = $("play-step-summary-" + n);
      node.replaceChildren();
      if (!value) {
        if (empty) node.append(el("span", { className: "bwiz-step__empty", text: empty }));
        return;
      }
      node.append(el("span", { className: "bwiz-step__value", text: value }));
      if (detail) node.append(el("span", { className: "bwiz-step__detail", text: detail }));
    };
    put(1, state.revision ? state.map?.display_name || state.map?.asset_id || "" : "",
      state.revision ? t("revision {n}", { n: state.revision.revision }) +
        ($("play-map-name").value.trim() ? ` · +map ${$("play-map-name").value.trim()}` : "") : "");
    put(2, nameOfPipeline(state.pipeline), state.strict ? t("strict") : "");
    put(3, state.engine ? nameOfEngine(state.engine) : "",
      state.engine ? [actionTitle(state.action), state.mod].filter(Boolean).join(" · ") : "");
    put(4, state.plan ? t("checked") : "", "", t("not checked yet"));
    const node5 = $("play-step-summary-5");
    node5.replaceChildren();
    markSteps();
  }

  // The current step is teal, a step whose choice is made carries a tick, and a
  // step that would stop the run is red — the same three states the Build
  // wizard draws. Before this only aria-current was set, and the stylesheet
  // styles classes, so the step a person was on looked like every other.
  function markSteps() {
    const done = {
      1: Boolean(state.revision),
      2: Boolean(state.pipeline),
      3: Boolean(state.engine && state.action && state.gameRoot),
      4: Boolean(state.plan),
      5: false,
    };
    const attention = {
      3: Boolean(state.engine) && !state.gameRoot,
      4: state.plan?.textures?.compiler_ready === false && !state.plan?.textures?.ready_with_own_wads,
    };
    // Each "Next" is active only when its step has what it needs, and says
    // what is missing when it is not (operator, 2026-09-23).
    const hosting = state.action === "host_listen" || state.action === "host_dedicated";
    const listed = !hosting || !state.listing.enabled ||
      (String(state.listing.host || "").trim() !== "" && Number(state.listing.port) > 0);
    const missing = {
      2: !state.revision ? t("Choose a map and one of its revisions first.")
        : !String(state.mapName || "").trim() ? t("Give the map a name in the game first.") : "",
      3: !state.pipeline ? t("Choose a build profile first.") : "",
      4: !state.engine ? t("Choose an engine first.")
        : !state.action ? t("Choose how to play it first.")
        : !state.gameRoot ? t("Set up where the game is first: Setup, in Profiles.")
        : !listed ? t("Give the address and port players connect to first.") : "",
      5: !state.plan ? t("The review is not ready yet.")
        : attention[4] ? t("Some textures are missing: use your own copy first.") : "",
    };
    for (const button of document.querySelectorAll("#area-play [data-go]")) {
      const target = Number(button.dataset.go);
      const why = target > state.step ? missing[target] || "" : "";
      button.disabled = Boolean(why);
      button.title = why;
    }
    for (const tab of document.querySelectorAll("#play-steps .bwiz-step")) {
      const n = Number(tab.dataset.step);
      const current = n === state.step;
      tab.classList.toggle("current", current);
      tab.classList.toggle("done", !current && done[n]);
      tab.classList.toggle("attention", !current && Boolean(attention[n]));
      if (current) tab.setAttribute("aria-current", "step");
      else tab.removeAttribute("aria-current");
    }
  }

  function actionTitle(id) {
    return ACTIONS[id]?.title ? t(ACTIONS[id].title) : "";
  }

  function nameOfPipeline(id) {
    return state.pipelines.find((p) => p.id === id)?.name || id || "";
  }

  function nameOfEngine(id) {
    return state.engines.find((e) => e.id === id)?.name || id || "";
  }

  // Any change to an identity throws the plan away and SAYS SO. A review that
  // silently described the previous choice would be the worst kind of review.
  function invalidate(why) {
    setMessage("play-start-message", "");
    remember();
    markSteps();
    if (!state.plan) return;
    state.plan = null;
    state.planKey = "";
    setMessage("play-review-message", why);
    announce(why);
  }

  function planKey() {
    return [
      state.map?.asset_id, state.revision?.revision_id, state.sourceFile, state.mapName,
      state.pipeline, String(state.strict), state.engine, state.action, state.mod, state.ownWadsDir,
      JSON.stringify(listingBody() || null),
    ].join("|");
  }

  // --- step 1: the map ---------------------------------------------------------

  // Every map in the account, a page at a time. The first 200 used to be all
  // the list ever held, so an older map — dm2, on an account with a few
  // hundred — could not be chosen at all (operator, 2026-09-22).
  const MAP_PAGES = 25;
  async function loadMaps() {
    let items = [];
    let cursor = "";
    for (let page = 0; page < MAP_PAGES; page += 1) {
      const query = new URLSearchParams({ type: "map", limit: "200" });
      if (cursor) query.set("cursor", cursor);
      const { ok, body } = await api("/api/v1/library/catalog?" + query.toString());
      if (!ok) {
        setMessage("play-map-message", body.error, "error");
        break;
      }
      items = items.concat(body.items || []);
      cursor = body.has_more ? body.next_cursor || "" : "";
      if (!cursor) break;
    }
    // A remembered map the listing did not reach is asked for by id, so a link
    // to a map always opens on it.
    if (pendingMap && !items.some((m) => m.asset_id === pendingMap)) {
      const { ok, body } = await api(`/api/v1/library/assets/map/${encodeURIComponent(pendingMap)}`);
      if (ok && body.asset) items.push(body.asset);
    }
    // Newest saved first (NEW_265): the map somebody has just saved in the
    // editor is the one they came here to build. Sorted once every page has
    // arrived, so the order never depends on which page answered first.
    items.sort(newestSavedFirst);
    state.maps = items;
    const select = $("play-map");
    select.replaceChildren(el("option", { text: t("Choose a map…"), attrs: { value: "" } }));
    for (const map of state.maps) {
      select.append(el("option", {
        text: map.display_name || map.asset_id,
        attrs: { value: map.asset_id },
      }));
    }
    if (!state.map && pendingMap) {
      state.map = state.maps.find((m) => m.asset_id === pendingMap) || null;
      pendingMap = "";
    }
    if (state.map) select.value = state.map.asset_id;
  }

  // newestSavedFirst orders maps by when their current revision was saved, as
  // AUB recorded it on the revision — never by this browser's clock, a title,
  // or the order the catalog's pages arrived in. Two maps saved at the same
  // instant, or maps whose revision carries no time (a legacy row), fall back
  // to a stable order: name, then the map's id, which is unique. Maps with no
  // saved time come after every map that has one.
  function savedAt(map) {
    const stamp = Date.parse(map?.current_revision?.created_at || "");
    return Number.isNaN(stamp) ? null : stamp;
  }

  function newestSavedFirst(a, b) {
    const at = savedAt(a);
    const bt = savedAt(b);
    if (at !== bt) {
      if (at === null) return 1;
      if (bt === null) return -1;
      return bt - at;
    }
    const byName = (a.display_name || "").localeCompare(b.display_name || "", undefined, { numeric: true, sensitivity: "base" });
    if (byName) return byName;
    return String(a.asset_id).localeCompare(String(b.asset_id));
  }
  window.AUCOM.newestSavedFirst = newestSavedFirst;

  // Two changes of the map in quick succession are two requests, and the one
  // that answers second is not necessarily the one that was asked last. Each
  // load carries the map it was asked about; an answer for a map that is no
  // longer chosen is dropped rather than written over the current one.
  //
  // Without this the select could hold one revision while `state.revision` held
  // another — or nothing — and the request built from it would name no
  // revision at all, which the coordinator correctly refuses.
  let revisionRequest = 0;

  async function loadRevisions(assetID, { keep = false } = {}) {
    const request = ++revisionRequest;
    const select = $("play-revision");
    select.replaceChildren();
    // Reloading the same map's list keeps the revision that was chosen: a
    // second refresh of the page must not quietly move a review to the newest.
    // A person re-choosing the map still gets the newest (see the map's `change`).
    if (keep && state.revision && state.map?.asset_id === assetID) pendingRevision = state.revision.revision_id;
    state.revisions = [];
    state.revision = null;
    if (!assetID) return;
    const { ok, body } = await api(`/api/v1/library/assets/map/${encodeURIComponent(assetID)}`);
    if (request !== revisionRequest) return;
    if (!ok) {
      setMessage("play-map-message", body.error, "error");
      return;
    }
    state.revisions = body.revisions || [];
    if (body.history_error) {
      setMessage("play-map-message",
        "This server keeps no per-version history for maps, so an exact revision cannot be pinned: " +
        body.history_error, "error");
    }
    for (const revision of state.revisions) {
      select.append(el("option", {
        text: `${revision.revision} — ${when(revision.created_at)}${revision.immutable ? "" : " (not pinnable)"}`,
        attrs: { value: revision.revision_id },
      }));
    }
    // The newest, because that is what somebody who has just saved wants. It
    // is still an EXACT revision: nothing here ever sends the word `current`.
    if (state.revisions.length) {
      const remembered = state.revisions.find((r) => r.revision_id === pendingRevision);
      pendingRevision = "";
      const chosen = (remembered || state.revisions[0]).revision_id;
      select.value = chosen;
      await chooseRevision(chosen);
    }
  }

  // Returns once the revision's files are known and the map's name in the
  // game is suggested, so a review asked for next has a name to review.
  async function chooseRevision(revisionID) {
    state.revision = state.revisions.find((r) => r.revision_id === revisionID) || null;
    invalidate("You changed the revision, so the textures this build would use have to be checked again.");
    summarize();
    await loadSourceFiles();
    summarize();
  }

  let sourceFileRequest = 0;

  async function loadSourceFiles() {
    const request = ++sourceFileRequest;
    const field = $("play-source-file-field");
    const select = $("play-source-file");
    select.replaceChildren();
    state.sourceFile = "";
    field.hidden = true;
    if (!state.map || !state.revision) return;
    const path = `/api/v1/library/assets/map/${encodeURIComponent(state.map.asset_id)}` +
      `/${encodeURIComponent(state.revision.revision_id)}`;
    const { ok, body } = await api(path);
    if (request !== sourceFileRequest || !ok) return;
    const files = body.files || [];
    if (files.length <= 1) {
      state.sourceFile = files[0]?.path || "";
      suggestMapName();
      return;
    }
    // More than one compatible file: the person names which, because a build
    // that silently took the first file of a package is a build nobody could
    // explain.
    field.hidden = false;
    for (const file of files) {
      select.append(el("option", { text: file.path, attrs: { value: file.path } }));
    }
    select.value = files[0].path;
    state.sourceFile = files[0].path;
    suggestMapName();
  }

  // A suggestion, not a rule: the engine's own sanitizing rules are the
  // authority, and the field is editable because a person may want another name.
  function suggestMapName() {
    if ($("play-map-name").value.trim()) return;
    const stem = (state.sourceFile || state.map?.display_name || "")
      .replace(/\.[^.]*$/, "")
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, "_")
      .replace(/^_+|_+$/g, "");
    if (!stem) return;
    $("play-map-name").value = stem.slice(0, 64);
    state.mapName = $("play-map-name").value;
  }

  // Auto-build on the chosen map (autobuild.js): shown under the map while a
  // signed-in person is on step 1.
  function showAutobuild() {
    window.AUCOM.autobuildPanel?.show(state.map, Boolean(window.AUCOM.status?.authenticated));
  }
  window.AUCOM.play = {
    pipelines: () => state.pipelines,
    pipeline: () => state.pipeline,
    // Auto-build tells Activity a build of its own has started.
    pollActivity: () => poll(),
  };

  // --- step 2: the build profile -------------------------------------------------

  let pipelineRefresh = 0;
  let engineRefresh = 0;
  async function loadPipelines() {
    const sequence = ++pipelineRefresh;
    const { ok, body } = await api("/api/v1/build/pipelines");
    if (sequence !== pipelineRefresh) return;
    if (!ok) { state.pipelines = []; state.pipeline = ""; $("play-pipeline").replaceChildren(); renderStages(); renderSteps(); return; }
    state.pipelines = body.items || [];
    const select = $("play-pipeline");
    state.pipeline = window.AUCOM.executionChoices(select, state.pipelines, state.pipeline, window.AUCOM.pipelineReady,
      (pipeline) => pipeline.name + (window.AUCOM.pipelineReady(pipeline) ? "" : " — needs setup: " + (pipeline.readiness?.problems || []).map((p) => p.summary).join(" ")),
      "Choose a ready build profile…");
    renderStages();
  }

  function renderStages() {
    const list = $("play-stages");
    list.replaceChildren();
    const pipeline = state.pipelines.find((p) => p.id === state.pipeline);
    $("play-pipeline-note").textContent = pipeline?.summary || "";
    for (const step of pipeline?.steps || []) {
      list.append(el("li", {
        text: `${step.title || step.id} — ${step.provider?.name || step.capability}`,
      }));
    }
  }

  // --- step 3: the engine ---------------------------------------------------------

  async function loadEngines() {
    const sequence = ++engineRefresh;
    const { ok, body } = await api("/api/v1/engines");
    if (sequence !== engineRefresh) return;
    if (!ok) { state.engines = []; state.engine = ""; $("play-engine").replaceChildren(); renderActions(); renderSteps(); return; }
    state.engines = body.items || [];
    const select = $("play-engine");
    state.engine = window.AUCOM.executionChoices(select, state.engines, state.engine,
      (engine) => Object.keys(ACTIONS).some((id) => window.AUCOM.actionReady(engine, id)),
      (engine) => `${engine.name} — ${Object.keys(ACTIONS).some((id) => window.AUCOM.actionReady(engine, id)) ? t("Ready to start") : t("Needs setup") + ": " + (engine.readiness?.problems || []).map(p => p.summary).join(" ")}`,
      "Choose a ready engine…");
    renderActions();
  }

  function renderActions() {
    const select = $("play-action");
    const choices = $("play-action-choices");
    select.replaceChildren();
    choices.replaceChildren();
    const engine = state.engines.find((e) => e.id === state.engine);
    // Only what makes sense for one freshly built map, in the order a person
    // would pick them. "Join a server" and "Play a mod or package" belong to
    // Live Games and Run: offered here, as they were, they were four tabs whose
    // meaning nobody could guess (operator, 2026-09-22).
    const offered = Object.keys(ACTIONS)
      .map((id) => (engine?.actions || []).find((action) => action.id === id))
      .filter(Boolean);
    for (const action of offered) {
      select.append(el("option", { text: (action.title || action.id) + (window.AUCOM.actionReady(engine, action.id) ? "" : " — needs setup: " + (engine.action_problems?.[action.id] || []).map(p => p.summary).join(" ")), attrs: { value: action.id, disabled: window.AUCOM.actionReady(engine, action.id) ? null : "disabled" } }));
    }
    if (state.action && offered.some((a) => a.id === state.action && window.AUCOM.actionReady(engine, a.id))) select.value = state.action;
    else select.value = offered.find((a) => window.AUCOM.actionReady(engine, a.id))?.id || "";
    state.action = select.value || "";
    for (const action of offered) {
      const meaning = ACTIONS[action.id];
      const input = el("input", {
        attrs: { type: "radio", name: "play-action-choice", value: action.id, id: "play-action-" + action.id },
      });
      input.disabled = !window.AUCOM.actionReady(engine, action.id);
      input.setAttribute("aria-disabled", String(input.disabled));
      input.checked = !input.disabled && action.id === state.action;
      input.addEventListener("change", () => {
        if (input.disabled || !input.checked) return;
        select.value = action.id;
        select.dispatchEvent(new Event("change", { bubbles: true }));
      });
      choices.append(el("label", {
        className: "choice-card",
        attrs: { for: input.id },
        children: [
          input,
          el("span", { className: "choice-card__title", text: t(meaning.title) }),
          el("span", { className: "choice-card__text", text: t(meaning.text) }),
        ],
      }));
    }
    $("play-action-fieldset").hidden = !engine;
    if (engine && !offered.length) {
      choices.append(el("p", { className: "muted", text: t("This engine's profile offers no way to play a single map.") }));
    }
    const root = engine?.binding?.roots?.game_root || "";
    state.gameRoot = root;
    $("play-game-root").textContent = root
      ? `The game is at ${root}. The Companion will create ${state.mod} beside it and never write into id1.`
      : "This engine has no game folder set on this machine yet — set one under Profiles first.";
    // Only once an engine is chosen: before that there is nothing to be wrong
    // about, and an error on an untouched form reads as a fault.
    const missing = Boolean(state.engine) && !root;
    $("play-game-root").textContent = !state.engine
      ? "Choose an engine to see where the game is."
      : $("play-game-root").textContent;
    // What stops this engine starting this action, in the words the server
    // uses for Profiles too (NEW_265): one decision, so the dropdown's "Needs
    // setup" always comes with the thing that is actually missing, and an
    // engine Profiles calls ready is never called otherwise here.
    const problems = engine ? (engine.action_problems?.[state.action] || []) : [];
    if (engine && problems.length) {
      setMessage("play-run-message", t("{engine} cannot start yet: {why} Set it up in Profiles.", {
        engine: engine.name, why: problems.map((problem) => problem.summary).join(" "),
      }), "error");
    } else {
      setMessage("play-run-message", missing ? "Choose this engine's game folder before continuing." : "",
        missing ? "error" : "");
    }
    renderListing();
  }

  // --- step 3: the listing of a hosted game ------------------------------------

  function hosting() {
    return state.action === "host_listen" || state.action === "host_dedicated";
  }

  // The listing the request carries, or nothing: only a hosted game, and only
  // when the person left "List this game" ticked.
  function listingBody() {
    if (!hosting() || !state.listing.enabled) return undefined;
    return {
      title: state.listing.title.trim() || state.map?.display_name || state.mapName || "",
      visibility: state.listing.visibility,
      endpoint_host: state.listing.host.trim(),
      endpoint_port: Number(state.listing.port) || 0,
    };
  }

  // An address only this network can reach. A production Auto-Pigeon server
  // lists such a game privately and never publishes the address, so the page
  // offers only what the server will accept rather than a choice it would
  // refuse. A development server may list LAN games for everyone
  // (`lan_listings` in the listing defaults), and then every choice is offered.
  function localAddress(host) {
    const text = String(host || "").trim().toLowerCase();
    if (!text || text === "localhost" || text.endsWith(".local") || text.endsWith(".lan")) return true;
    const parts = text.split(".").map(Number);
    if (parts.length === 4 && parts.every((n) => Number.isInteger(n) && n >= 0 && n <= 255)) {
      const [a, b] = parts;
      return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) ||
        (a === 169 && b === 254) || (a === 100 && b >= 64 && b <= 127);
    }
    return text === "::1" || text.startsWith("fe80:") || text.startsWith("fc") || text.startsWith("fd");
  }

  function applyAddressRule() {
    const host = String(state.listing.host || "").trim().toLowerCase();
    const loopback = host === "localhost" || host === "::1" || /^127\./.test(host);
    const local = localAddress(host) && (loopback || !state.lanListings);
    const select = $("play-listing-visibility");
    for (const option of select.options) option.disabled = local && option.value !== "private";
    if (local && state.listing.visibility !== "private") state.listing.visibility = "private";
    select.value = state.listing.visibility;
    $("play-listing-local").hidden = !local;
    renderHelpToConnect();
  }

  let listingDefaultsFor = "";
  async function renderListing() {
    const box = $("play-listing");
    box.hidden = !hosting();
    if (box.hidden) return;
    $("play-list-it").checked = state.listing.enabled;
    $("play-listing-fields").hidden = !state.listing.enabled;
    if (!state.listing.title) $("play-listing-title").placeholder = state.map?.display_name || "";
    $("play-listing-title").value = state.listing.title;
    $("play-listing-visibility").value = state.listing.visibility;
    // The suggestions, once per engine and action: this machine's address on
    // the way to the Auto-Pigeon server, and the port the engine's profile or
    // its game uses. Suggestions only — both fields stay the person's.
    const key = state.engine + "|" + state.action;
    if (listingDefaultsFor !== key) {
      listingDefaultsFor = key;
      const { ok, body } = await api(`/api/v1/play/listing-defaults?engine=${encodeURIComponent(state.engine)}` +
        `&action=${encodeURIComponent(state.action)}`);
      if (ok) {
        state.lanListings = body.lan_listings === true;
        if (!state.listing.host && body.host) state.listing.host = body.host;
        if (!state.listing.port && body.port) state.listing.port = String(body.port);
        $("play-listing-hint").textContent = body.port_source === "game_default"
          ? t("The port is this game's own default. Change it if your engine is set to listen on another one.")
          : body.port_source === "profile" ? t("The port is the one this engine's profile starts the server on.") : "";
      }
    }
    $("play-listing-host").value = state.listing.host;
    $("play-listing-port").value = state.listing.port;
    applyAddressRule();
  }

  // --- "Help to connect" (NEW_247A2) ---------------------------------------------
  //
  // A game everyone can see is one people outside this network must reach, and
  // that takes a UDP port forwarded by hand on the host's router. The gallery's
  // help page says how, and how to test it from outside. It is guidance, not a
  // check: whether the address answers is still the server's "unverified" or
  // "reachable". The address is the one the Companion built from the gallery
  // the Auto-Pigeon server named (host_help_url, see footer.js); with none, the
  // page says so rather than guessing one.
  function publicListing(listing) {
    return Boolean(listing) && listing.visibility === "public";
  }

  function helpToConnect() {
    const href = String(window.AUCOM.siteLinks?.host_help_url || "");
    if (/^https?:\/\/[^/\s]+\/\S*$/i.test(href)) {
      return el("p", {
        className: "listing-help",
        children: [el("a", {
          className: "button-link",
          text: t("Help to connect"),
          attrs: { href, target: "_blank", rel: "noopener noreferrer", "data-help-to-connect": "link" },
        })],
      });
    }
    return el("p", {
      className: "hint listing-help",
      text: t("Help to connect is not available: this Auto-Pigeon server has not said where its website is."),
      attrs: { "data-help-to-connect": "unavailable" },
    });
  }

  // Beside the networking fields, while Everyone is chosen: the host prepares
  // the router before launching, not after.
  function renderHelpToConnect() {
    const box = $("play-listing-help");
    const show = hosting() && state.listing.enabled && publicListing(state.listing);
    box.hidden = !show;
    box.replaceChildren(...(show ? [helpToConnect()] : []));
  }

  // The review's listing card: AUB's own preview of what will be published.
  async function listingCard() {
    const card = el("section", { className: "panel review-card review-card--wide" });
    card.append(el("h4", { text: t("Live Games listing") }));
    const body = listingBody();
    if (!body) {
      card.append(el("p", { className: "muted", text: t("Not listed: nobody else will see this game in Live Games.") }));
      return card;
    }
    card.append(el("p", { className: "muted small", text: t("Checking what the listing will say…") }));
    const { ok, body: preview } = await api("/api/v1/play/listing-preview", { method: "POST", body: requestBody() });
    card.replaceChildren(el("h4", { text: t("Live Games listing") }));
    if (!ok) {
      card.append(el("p", { className: "message error", text: preview.error }));
      return card;
    }
    card.append(el("dl", {
      className: "summary-list",
      children: [
        ...line(t("Title"), body.title),
        ...line(t("Seen by"), preview.audience || preview.visibility),
        ...line(t("Players connect to"), preview.endpoint || `${body.endpoint_host}:${body.endpoint_port}`),
        ...line(t("Reachable"), preview.reachability || ""),
      ].flat(),
    }));
    if ((preview.exposed_fields || []).length) {
      card.append(el("p", { className: "muted small", text: t("Everything the listing will say about you:") }));
      card.append(el("ul", {
        className: "plain exposed-list",
        children: preview.exposed_fields.map((field) => el("li", {
          className: field.withheld ? "muted" : "",
          text: `${field.field}: ${field.value || "—"} · ${t("seen by {who}", { who: field.seen_by })}` +
            (field.withheld ? " · " + t("withheld") : ""),
        })),
      }));
    }
    for (const line_ of preview.network_guidance || []) card.append(el("p", { className: "muted small", text: line_ }));
    if (publicListing(body)) card.append(helpToConnect());
    card.append(el("p", {
      className: "muted small",
      text: t("Pressing Build & Run lists the game once it is running, with the map uploaded for people who join. The listing ends when the game stops."),
    }));
    return card;
  }

  // --- step 4: the review -----------------------------------------------------------

  async function refreshPlan() {
    const key = planKey();
    if (state.plan && state.planKey === key) return;
    const review = $("play-review");
    review.replaceChildren(el("p", { className: "muted", text: "Working out what this would do…" }));
    const { ok, body } = await api("/api/v1/play/plan", { method: "POST", body: requestBody() });
    if (!ok) {
      state.plan = null;
      review.replaceChildren();
      setMessage("play-review-message", body.error, "error");
      return;
    }
    state.plan = body;
    state.planKey = key;
    setMessage("play-review-message", "");
    renderReview(body);
    summarize();

    // The ordered WAD set is part of what the review is FOR, and the plan does
    // not download anything — so when this machine holds no verified bundle for
    // the revision yet, the review fetches and verifies one now. It is the same
    // bundle the build will read, cached by its own digest, so pressing Build &
    // Run afterwards costs nothing extra. What is never done is INVENTING the
    // list: until this returns, the review says the textures will be fetched.
    if (body.textures && body.textures.known === false && window.AUCOM.status?.authenticated) {
      const asked = ++texturesAsked;
      const { ok: gotTextures, body: textures } = await api(
        `/api/v1/play/textures?asset_id=${encodeURIComponent(state.map.asset_id)}` +
        `&revision=${encodeURIComponent(state.revision.revision)}`
      );
      // A plan that changed underneath this is one whose textures are not these.
      if (state.planKey !== key) return;
      // Nor is an answer a later question has already replaced: this fetch
      // verifies the bundle and can take seconds, and "Use this folder" pressed
      // meanwhile is answered first — this one landing on top of it would put
      // the WAD warning back over the answer the person asked for.
      if (asked !== texturesAsked) return;
      if (gotTextures && textures.known) {
        state.plan.textures = textures;
        renderReview(state.plan);
      } else if (!gotTextures) {
        setMessage("play-review-message", textures.error, "error");
      }
    }
  }

  function requestBody() {
    return {
      asset_type: "map",
      asset_id: state.map?.asset_id || "",
      revision_id: state.revision?.revision_id || "",
      revision_number: state.revision?.revision || 0,
      source_file: state.sourceFile,
      pipeline: state.pipeline,
      strict: state.strict,
      engine: state.engine,
      action: state.action,
      mod: state.mod,
      map: $("play-map-name").value.trim(),
      own_wads_dir: state.ownWadsDir || undefined,
      listing: listingBody(),
    };
  }

  // The review is a grid of cards, two to a row on a wide window: what is
  // downloaded beside its textures, the programs beside where files go, and
  // the command across the whole width at the bottom (operator, 2026-09-22:
  // "the right side of the page is always empty").
  function renderReview(plan) {
    const review = $("play-review");
    review.replaceChildren();
    review.className = "review-grid";

    review.append(section(t("What will be downloaded"), [
      line(t("Map"), state.map?.display_name || plan.map.asset_id),
      line(t("Revision"), String(plan.map.revision)),
      line(t("Map file"), plan.map.source_file || t("the revision's only file")),
      line(t("Name in the game"), plan.run?.map || $("play-map-name").value.trim()),
    ]));

    review.append(texturesSection(plan.textures));
    if (plan.textures_warning) {
      review.append(el("p", { className: "message error", attrs: { role: "alert" }, text: t(plan.textures_warning) }));
    }

    const programs = [line(t("Build profile"), nameOfPipeline(plan.build.pipeline))];
    const pipeline = state.pipelines.find((p) => p.id === plan.build.pipeline);
    for (const step of pipeline?.steps || []) {
      programs.push(line(step.title || step.id,
        `${step.provider?.name || step.capability} ${step.provider?.version || ""}`.trim()));
    }
    programs.push(line(t("Extractor"), t("Auto-Pigeon Extractor, when the map needs converting")));
    programs.push(line(t("Engine"), `${nameOfEngine(state.engine)} · ${actionTitle(state.action)}`));
    review.append(section(t("Which programs will run"), programs));

    // Once the verified bundle is known, the WAD line names the actual files
    // rather than a placeholder for them.
    const carried = (plan.textures?.known ? plan.textures.wads || [] : [])
      .filter((wad) => wad.included)
      .flatMap((wad) => (wad.files || []).map((file) => file.path));
    const files = plan.writes.files.flatMap((file) =>
      carried.length && file.includes("<the map's declared WADs")
        ? carried.map((path) => `${plan.run.mod}/wads/${path}`)
        : [file]);
    review.append(el("section", {
      className: "panel review-card",
      children: [
        el("h4", { text: t("Where files will be written") }),
        el("dl", { className: "summary-list", children: line(t("Folder"), plan.writes.directory).flat() }),
        el("ul", { className: "file-list", children: files.map((file) => el("li", { children: [el("code", { text: file })] })) }),
        el("p", { className: "muted small", text: t("Never written to: {what}.", { what: plan.writes.never_writes }) }),
      ],
    }));

    review.append(launchSection(plan.launch));
    if (hosting()) {
      const placeholder = el("section", { className: "panel review-card review-card--wide" });
      review.append(placeholder);
      listingCard().then((card) => placeholder.replaceWith(card));
    }
    if (plan.build_preview_note) {
      review.append(el("p", { className: "muted small review-note", text: plan.build_preview_note }));
    }
  }

  function texturesSection(textures) {
    const children = [el("h4", { text: t("Textures") })];
    if (!textures) return el("section", { className: "panel review-card", children });
    if (!textures.known) {
      children.push(el("p", { className: "muted", text: textures.message }));
      return el("section", { className: "panel review-card", children });
    }
    children.push(el("p", { className: "muted small", text: t("The texture WADs, in the order the map declares them.") }));
    const rows = (textures.wads || []).map((wad) => {
      const files = wad.included ? (wad.files || []) : [];
      return el("li", {
        children: [
          el("span", { className: "wad-order", text: String(wad.order + 1) }),
          el("span", { className: "wad-name", text: wad.name }),
          el("span", {
            className: "muted",
            text: files.length
              ? files.map((f) => bytes(f.bytes)).join(", ")
              : wad.note || t("not in this bundle"),
          }),
        ],
      });
    });
    children.push(el("ol", { className: "wad-list", children: rows }));
    children.push(el("p", {
      className: "muted small",
      text: t("Later declarations win a name two WADs both hold. The compiler reads these files; a compiled Quake 1 map carries its own textures, so the game does not read these files at run time — they are kept with the map so the build can be inspected and repeated."),
    }));
    if (!textures.compiler_ready) children.push(textures.own_wads_possible ? ownWadsOffer(textures) : refusalNotice(textures));
    return el("section", { className: "panel review-card", children });
  }

  // Why a WAD was not sent, by the reason AUB gives for it (NEW_313A). What is
  // known is what the deployment has on record about those exact bytes — never
  // whose the file is, which nothing here may claim. A bundle that gives no
  // reason (every 1.1 manifest), or one this page does not know, gets the one
  // sentence that is true of all of them.
  function notSentSentence(wad, reason) {
    switch (reason) {
      case "declared_withheld":
        return t("{wad} was not sent: the operator of this Auto-Pigeon deployment has declared that its bytes are not to be redistributed.", { wad });
      case "digest_mismatch":
        return t("{wad} was not sent: the deployment's copy is not the exact file its redistribution permission names.", { wad });
      case "declaration_incomplete":
        return t("{wad} was not sent: the deployment's redistribution record for it does not state a credit and terms.", { wad });
      case "source_unreadable":
        return t("{wad} was not sent: the deployment could not read its copy of the file.", { wad });
      case "policy_unavailable":
        return t("{wad} was not sent: the deployment's redistribution records could not be read.", { wad });
      default:
        return t("{wad} was not sent: this Auto-Pigeon deployment has no redistribution permission on record for that file's exact bytes.", { wad });
    }
  }

  // The reason AUB gave for one declared WAD, matched by its file name.
  function notSentReason(textures, name) {
    const base = (value) => String(value || "").split(/[\\/]/).pop().toLowerCase();
    const wad = (textures?.wads || []).find((w) => !w.included && base(w.name) === base(name));
    return wad?.redistribution?.reason || "";
  }

  // The refusal codes AUB gives, as sentences a person can act on. A code this
  // page does not know is still shown, as it came, rather than dropped.
  function refusalSentence(refusal, textures) {
    const [code, ...rest] = String(refusal).split(":");
    const subject = rest.join(":").trim();
    switch (code.trim()) {
      case "wad_bytes_not_carried":
        return notSentSentence(subject, notSentReason(textures, subject));
      case "wad_inventory_incomplete":
        return t("Auto-Pigeon could not list what is inside one of the map's WADs.");
      case "texture_source_private":
        return t("{source} belongs to somebody else, who has not shared it.", { source: subject });
      case "texture_missing":
      case "texture_source_missing":
        return t("Nothing the map declares supplies {what}.", { what: subject });
      default:
        return refusal;
    }
  }

  function refusalNotice(textures) {
    return el("div", {
      className: "panel notice error",
      children: [
        el("p", { children: [el("strong", { text: t("This map cannot be compiled yet.") })] }),
        el("ul", {
          className: "plain",
          children: (textures.compiler_refusals || []).map((r) => el("li", { text: refusalSentence(r, textures) })),
        }),
        el("p", {
          className: "muted",
          text: t("The Companion will not start the extractor or a compiler, and will not quietly use a similarly named WAD from a folder on this computer."),
        }),
      ],
    });
  }

  // "Use my own copy": the answer to a WAD the deployment did not send, and
  // only to that. The person names the folder and presses the button; nothing
  // is taken from a folder on this machine on the page's own initiative.
  function ownWadsOffer(textures) {
    const names = textures.own_wads_needed || [];
    const confirmed = textures.own_wads_dir && textures.own_wads_dir === state.ownWadsDir;
    const box = el("div", { className: "own-wads" });
    box.append(el("p", {
      children: [el("strong", { text: t("Not sent with this map's textures: {wads}.", { wads: names.join(", ") }) })],
    }));
    box.append(el("ul", {
      className: "plain",
      children: names.map((name) => el("li", { text: notSentSentence(name, notSentReason(textures, name)) })),
    }));
    box.append(el("p", {
      className: "muted small",
      text: t("You can supply your own copy: name the folder that has it. Only these files are taken from it, by their exact names, and the build records each one."),
    }));
    const input = el("input", {
      attrs: { type: "text", id: "play-own-wads-dir", spellcheck: "false", "aria-label": t("Folder with your own WADs") },
    });
    // The suggestion is where a compiler conventionally looks for WADs, and is
    // only a starting point the person can overwrite — it is not a claim that
    // the file is there or belongs there. It is spelled with this machine's
    // own separator: a Windows game folder followed by "/id1" read as a typo
    // (NEW_310).
    const separator = /\\/.test(state.gameRoot || "") ? "\\" : "/";
    input.value = state.ownWadsDir || (state.gameRoot ? state.gameRoot.replace(/[\\/]+$/, "") + separator + "id1" : "");
    const use = el("button", { text: t("Use this folder"), className: "primary", attrs: { type: "button" } });
    use.addEventListener("click", () => window.AUCOM.withBusy(use, async () => {
      state.ownWadsDir = input.value.trim();
      await checkOwnWads();
    }));
    box.append(el("div", { className: "own-wads__row", children: [input, use] }));
    if (confirmed) {
      if (textures.own_wads_error) {
        box.append(el("p", { className: "message error", text: textures.own_wads_error }));
      }
      const list = el("ul", { className: "plain own-wads__list" });
      for (const wad of textures.own_wads || []) {
        list.append(el("li", {
          className: wad.found ? "ok" : "missing",
          text: wad.found
            ? t("{wad} — found, {size}", { wad: wad.name, size: bytes(wad.bytes) })
            : t("{wad} — not in this folder", { wad: wad.name }),
        }));
      }
      box.append(list);
      box.append(el("p", {
        className: textures.ready_with_own_wads ? "message ok" : "message error",
        text: textures.ready_with_own_wads
          ? t("Ready: the build will use your own copy.")
          : t("This folder does not have every WAD the map needs."),
      }));
    }
    return box;
  }

  async function checkOwnWads() {
    // A page opened from a link at step 4 shows its step before the map and
    // revision the link names have arrived. A press in that window waits for
    // them rather than returning silently — which it used to do, and a button
    // that does nothing is the one thing a person cannot debug.
    await ready;
    if (!state.map || !state.revision) {
      setMessage("play-review-message", t("Choose a map and one of its revisions first (step 1)."), "error");
      return;
    }
    if (!state.plan) await refreshPlan();
    if (!state.plan) return; // refreshPlan has said why.
    const query = new URLSearchParams({
      asset_id: state.map.asset_id, revision: String(state.revision.revision), own_wads_dir: state.ownWadsDir,
    });
    const asked = ++texturesAsked;
    const { ok, body } = await api("/api/v1/play/textures?" + query.toString());
    if (asked !== texturesAsked) return;
    if (!ok) {
      setMessage("play-review-message", body.error, "error");
      return;
    }
    state.plan.textures = body;
    state.planKey = planKey();
    remember();
    renderReview(state.plan);
    summarize();
  }

  // The command as it would be typed on THIS machine, for DISPLAY: the paths
  // are already this machine's (C:\\… on Windows), and the quoting and the
  // prompt follow its shell — cmd/PowerShell double quotes and ">" on Windows,
  // POSIX single quotes and "$" elsewhere (operator, 2026-09-22). The program
  // is still started with these words one by one, never through a shell.
  function onWindows() {
    return /^windows\//.test(window.AUCOM.status?.platform || "");
  }

  function shellQuote(word) {
    const text = String(word);
    if (onWindows()) {
      if (text && !/[\s"&|<>^%]/.test(text)) return text;
      return '"' + text.replace(/"/g, '""') + '"';
    }
    if (text && /^[A-Za-z0-9_@%+=:,./-]+$/.test(text)) return text;
    return "'" + text.replace(/'/g, "'\\''") + "'";
  }

  function commandBlock(argv) {
    const text = argv.map(shellQuote).join(" ");
    const copy = el("button", { text: t("Copy"), className: "secondary shell__copy", attrs: { type: "button" } });
    copy.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(text);
        copy.textContent = t("Copied");
      } catch {
        copy.textContent = t("Select and copy it");
      }
      window.setTimeout(() => { copy.textContent = t("Copy"); }, 1600);
    });
    return el("div", {
      className: "shell",
      children: [
        copy,
        el("pre", {
          className: "shell__code",
          attrs: { "aria-label": t("The exact command") },
          children: [
            el("span", { className: "shell__prompt", text: onWindows() ? "> " : "$ ", attrs: { "aria-hidden": "true" } }),
            el("code", { text }),
          ],
        }),
      ],
    });
  }

  function launchSection(launch) {
    const children = [el("h4", { text: t("The exact command") })];
    if (!launch?.known) {
      children.push(el("p", { className: "muted", text: launch?.message || t("Not resolvable yet.") }));
      return el("section", { className: "panel review-card review-card--wide", children });
    }
    children.push(el("p", {
      className: "muted small",
      text: t("What the Companion will start once the map is built. The program is given these words exactly, one by one; it is shown here as you would type it in a terminal."),
    }));
    children.push(commandBlock([launch.executable, ...(launch.args || [])]));
    return el("section", { className: "panel review-card review-card--wide", children });
  }

  function section(title, lines) {
    return el("section", {
      className: "panel review-card",
      children: [el("h4", { text: title }), el("dl", { className: "summary-list", children: lines.flat() })],
    });
  }

  function line(term, value) {
    return [el("dt", { text: term }), el("dd", { text: value })];
  }

  // --- step 5: one confirmation -------------------------------------------------------

  function renderFinalSummary() {
    const summary = $("play-final-summary");
    const textures = state.plan?.textures;
    const own = textures?.ready_with_own_wads && textures.own_wads_dir === state.ownWadsDir;
    summary.replaceChildren(
      ...line(t("Map"), `${state.map?.display_name || ""} · ${t("revision {n}", { n: state.revision?.revision ?? "" })}`),
      ...line(t("Build"), nameOfPipeline(state.pipeline)),
      ...line(t("Run"), `${nameOfEngine(state.engine)} · ${actionTitle(state.action)} → ${state.gameRoot}/${state.mod}`),
      ...line(t("Name in the game"), $("play-map-name").value.trim()),
      ...(own ? line(t("Your own WADs"), `${(textures.own_wads_needed || []).join(", ")} ← ${state.ownWadsDir}`) : []),
      ...(listingBody() ? line(t("Live Games"), `${listingBody().title} · ${$("play-listing-visibility").selectedOptions[0]?.textContent || ""}` +
        ` · ${listingBody().endpoint_host}:${listingBody().endpoint_port}`) : []),
    );
    const reviewed = Boolean(state.plan);
    const ready = reviewed && (textures?.compiler_ready !== false ||
      Boolean(textures?.ready_with_own_wads && textures.own_wads_dir === state.ownWadsDir));
    $("play-start").disabled = !ready;
    setMessage("play-start-message", ready ? "" : !reviewed ? t("The review is not ready yet — see step 4.") :
      "This map's textures are not complete enough to compile — see step 4.", ready ? "" : "error");
  }

  async function start() {
    setMessage("play-start-message", "");
    const { ok, body } = await api("/api/v1/play/runs", { method: "POST", body: requestBody() });
    if (!ok) {
      setMessage("play-start-message", body.error, "error");
      return;
    }
    // Everyone was this launch's choice, not the next one's (NEW_247A2): the
    // next launch starts Private. The summary is drawn again, so what is
    // pressed next is what is on the screen, and the review, asked for with
    // the listing it previews, is worked out again when it is next opened.
    if (state.listing.visibility !== FRESH_VISIBILITY) {
      state.listing.visibility = FRESH_VISIBILITY;
      renderListing();
      if (state.step === 5) renderFinalSummary();
    }
    // A short confirmation is a status line; the DETAIL is in Activity, which
    // opens on its own because that is where the run now lives.
    setMessage("play-start-message", "Started. Progress is in the Activity panel.");
    announce("Build and run started.");
    openActivity();
    watch(body.id);
  }

  // --- Activity ---------------------------------------------------------------------

  let pollTimer = null;
  let watching = null;

  function openActivity() {
    $("activity").hidden = false;
    $("activity-open").setAttribute("aria-expanded", "true");
    $("activity-heading").focus();
    // Catch the output up at once rather than at the next slow poll.
    poll();
  }

  function closeActivity() {
    $("activity").hidden = true;
    $("activity-open").setAttribute("aria-expanded", "false");
    $("activity-open").focus();
  }

  function watch(id) {
    watching = id;
    poll();
  }

  let polling = false;
  async function poll() {
    // One chain of polls at a time: a poll asked for while one is out is the
    // one already out.
    if (polling) return;
    polling = true;
    try {
      await pollOnce();
    } finally {
      polling = false;
    }
  }

  async function pollOnce() {
    clearTimeout(pollTimer);
    const { ok, body } = await api("/api/v1/play/runs");
    const current = ok ? renderActivity(body.items || []) : [];
    const active = (body.items || []).some((run) => run.active);
    $("activity-open").hidden = !(body.items || []).length;
    $("activity-count").textContent = active ? "running" : "";
    // Polled rather than streamed: one small request a second while something
    // is running, and a slow one while nothing is. A server-sent stream would
    // be a second transport for a page that already has one.
    // An open drawer is polled every few seconds even when nothing is
    // running, because a run can start without this page — Auto-build starts
    // one, and so can another window — and a drawer fifteen seconds behind it
    // showed the previous run's output under the new one (NEW_265, seen live).
    await pollOutputs(current);
    // Scheduled once this poll, its outputs included, is done, so a slow read
    // never overlaps the next one.
    pollTimer = setTimeout(poll, active ? 1000 : !$("activity").hidden ? 3000 : 15000);
  }

  // Activity is polled once a second while a run is active. A card is rebuilt
  // only when its run changed — a new stage, an error, a listing — and then in
  // place, so whatever a person opened (Technical details, the earlier runs),
  // the button they had focused and the output they had scrolled back through
  // are all where they left them. The elapsed time is the one thing that
  // changes every second, and it is updated as text.
  function openKeys(root) {
    return new Set([...root.querySelectorAll("details[data-open-key]")]
      .filter((node) => node.open).map((node) => node.dataset.openKey));
  }

  function reopen(root, keys) {
    for (const node of root.querySelectorAll("details[data-open-key]")) {
      if (keys.has(node.dataset.openKey)) node.open = true;
    }
  }

  // cards: run id → { node, key }; outputs: run id → the run's output view.
  const cards = new Map();
  const outputs = new Map();
  let earlierNode = null;
  let earlierKey = "";
  let layoutKey = "";

  // What a card shows, minus the clock: a key that changes only when the card
  // has something new to say.
  function cardKey(run) {
    const { elapsed_ms: _elapsed, build_steps: steps, ...rest } = run;
    return JSON.stringify([rest, (steps || []).map((step) => [step.id, step.state, step.job_id])]);
  }

  // What is happening now is the card; history is one line each, folded, and
  // in full on the Jobs page. Before 246I1.1 every run of the last twenty was a
  // full card, so the drawer was mostly the past.
  function renderActivity(runs) {
    const body = $("activity-body");
    if (!runs.length) {
      cards.clear();
      layoutKey = "";
      body.replaceChildren(el("p", { className: "muted", text: "Nothing has been built and run yet." }));
      return [];
    }
    const recent = runs.slice(0, 20);
    const active = recent.filter((run) => run.active);
    const current = active.length ? active : recent.slice(0, 1);
    const earlier = recent.filter((run) => !current.includes(run));

    const keep = openKeys(body);
    const wantLayout = current.map((run) => run.id).join("|") + "#" + (earlier.length ? "earlier" : "");
    if (wantLayout !== layoutKey) {
      // A different set of current runs: the one moment the drawer is laid
      // out again. The output views are carried over by run id.
      layoutKey = wantLayout;
      cards.clear();
      earlierKey = "";
      earlierNode = null;
      body.replaceChildren();
      for (const run of current) {
        const node = runCard(run, { current: true });
        cards.set(run.id, { node, key: cardKey(run) });
        body.append(node);
        for (const scroller of node.querySelectorAll(".joblog__scroll")) {
          if (scroller.dataset.scrollTop) scroller.scrollTop = Number(scroller.dataset.scrollTop);
        }
      }
    } else {
      for (const run of current) {
        const held = cards.get(run.id);
        const key = cardKey(run);
        if (held && held.key === key) {
          const clock_ = held.node.querySelector(".activity-run__elapsed");
          if (clock_) clock_.textContent = elapsed(run.elapsed_ms);
          continue;
        }
        const node = runCard(run, { current: true });
        replaceKeepingFocus(held.node, node);
        cards.set(run.id, { node, key });
      }
    }

    const wantEarlier = JSON.stringify(earlier.map((run) => [run.id, run.state, run.error || ""]));
    if (earlier.length && wantEarlier !== earlierKey) {
      earlierKey = wantEarlier;
      const node = el("details", {
        className: "activity-earlier",
        attrs: { "data-open-key": "earlier" },
        children: [
          el("summary", { text: t("Earlier runs ({n})", { n: earlier.length }) }),
          ...earlier.map((run) => el("details", {
            className: "activity-earlier__run",
            attrs: { "data-open-key": `run:${run.id}` },
            children: [
              el("summary", { children: [
                el("strong", { text: `${run.map || run.asset_id} — ${run.title}` }),
                el("span", { className: "muted", text: ` · ${elapsed(run.elapsed_ms) || "<1s"} · ${clock(run.created_at)}` }),
              ] }),
              runCard(run, { current: false }),
            ],
          })),
        ],
      });
      if (earlierNode) replaceKeepingFocus(earlierNode, node);
      else body.append(node);
      earlierNode = node;
    }
    reopen(body, keep);
    // Views for runs that are no longer current are let go.
    for (const id of [...outputs.keys()]) {
      if (!current.some((run) => run.id === id)) outputs.delete(id);
    }
    return current;
  }

  // replaceKeepingFocus swaps a card for its new version and puts keyboard
  // focus back on the same control, found by its data-focus-key, and the
  // output view's scroll position back where it was.
  function replaceKeepingFocus(oldNode, newNode) {
    const focused = document.activeElement;
    const focusKey = oldNode.contains(focused) ? focused?.dataset?.focusKey : "";
    oldNode.replaceWith(newNode);
    for (const scroller of newNode.querySelectorAll(".joblog__scroll")) {
      if (scroller.dataset.scrollTop) scroller.scrollTop = Number(scroller.dataset.scrollTop);
    }
    if (focusKey) newNode.querySelector(`[data-focus-key="${CSS.escape(focusKey)}"]`)?.focus();
  }

  // --- the tools' own output, in the card ---------------------------------------
  //
  // The same job-scoped source, cursor and view as the Jobs page (joboutput.js).
  // It follows the step that is running, labelled with the program and the
  // stage; when the next stage starts, the finished one's last lines stay above
  // it, and once the run is over the last stage's output stays on the card.

  // The job whose output a run's card should show now, and how to label it.
  function jobToFollow(run) {
    const steps = run.build_steps || [];
    const label = (step, fallback) => {
      const program = step?.program || "";
      const title = step?.title || fallback || "";
      return [program, title].filter(Boolean).join(" · ") || t("Build step");
    };
    if (run.current_job) {
      const step = steps.find((candidate) => candidate.job_id === run.current_job);
      return { job: run.current_job, label: label(step, run.current_step), running: true };
    }
    const ran = steps.filter((step) => step.job_id);
    if (!ran.length) return null;
    const last = ran[ran.length - 1];
    return { job: last.job_id, label: label(last, last.id), running: false };
  }

  function outputFor(run) {
    let entry = outputs.get(run.id);
    if (!entry) {
      const link = el("a", { className: "activity-run__joblink", text: t("Open the full log in Jobs") });
      link.hidden = true;
      const view = window.AUCOM.outputView({ label: t("What the tools printed"), maxChars: 60000, jobLink: link });
      entry = { view, link };
      outputs.set(run.id, entry);
    }
    return entry;
  }

  // pollOutputs reads the next output of every current run's step. Only while
  // the drawer is open: nobody is reading it otherwise, and it catches up from
  // the same cursor when it is opened again.
  async function pollOutputs(current) {
    if ($("activity").hidden) return;
    for (const run of current) {
      const target = jobToFollow(run);
      if (!target) continue;
      const entry = outputs.get(run.id);
      if (!entry) continue;
      const { view, link } = entry;
      if (view.job !== target.job) {
        // The previous stage's final lines are read before the next begins,
        // so a stage never ends mid-sentence in the drawer.
        if (view.job && !view.complete) await view.poll();
        view.follow(target.job, target.label, { fromStart: target.running });
        link.href = window.AUCOM.areas.jobs?.href?.(target.job) || "#jobs/" + encodeURIComponent(target.job);
        link.hidden = false;
      }
      if (!view.complete) {
        await view.poll();
        const scroller = view.root.querySelector(".joblog__scroll");
        if (scroller) scroller.dataset.scrollTop = String(scroller.scrollTop);
      }
    }
  }

  function clock(iso) {
    const date = new Date(iso);
    return Number.isNaN(date.getTime()) ? "" : date.toLocaleString([], {
      month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
    });
  }

  function runCard(run, { current = false } = {}) {
    // "Open in Jobs" opens another page, so it sits top right, in the head,
    // not in the run's text (operator, 2026-09-23).
    const headTools = el("div", { className: "activity-run__tools", children: [
      el("span", { className: "muted activity-run__elapsed", text: elapsed(run.elapsed_ms) }),
    ] });
    if (run.build_id) {
      const jobs = el("button", { text: "Open in Jobs", attrs: { type: "button", class: "secondary", "data-focus-key": "jobs" } });
      // Never a new browser tab: the Jobs page is in this window.
      jobs.addEventListener("click", () => { window.location.hash = "#jobs"; });
      headTools.append(jobs);
    }
    const head = el("div", {
      className: "activity-run__head",
      children: [el("strong", { text: `${run.map || run.asset_id} — ${run.title}` +
        (run.trigger === "auto_build" ? " · " + t("Auto-build") : "") }), headTools],
    });
    const stages = el("ol", {
      className: "activity-stages",
      children: (run.stages || []).map((stage) => el("li", {
        // A cancelled stage is not a failed one: it is neutral, not red.
        className: stage.cancelled ? "cancelled" : stage.error ? "failed" : stage.finished_at ? "done" : "running",
        children: [
          el("span", { text: stage.title }),
          el("span", { className: "muted", text: stage.finished_at ? " " + duration(stage.duration_ms) : "" }),
        ],
      })),
    });

    const children = [head, stages];
    if (run.state === "cancelled") {
      children.push(el("p", {
        className: "message",
        text: `You cancelled this during ${(run.cancelled_at_title || "the run").toLowerCase()}.` +
          (run.installed_nothing ? " Nothing was installed." : ""),
      }));
    } else if (run.error) {
      // One sentence here; the extractor's or compiler's whole output is in
      // Technical details, not painted across the card.
      children.push(el("p", {
        className: "message error",
        text: `${run.failed_at_title || "It stopped"}: ${run.error_summary || run.error}`,
      }));
    }
    if (run.remedy) children.push(el("p", { className: "muted", text: run.remedy }));
    for (const warning of run.warnings || []) {
      children.push(el("p", { className: "message warning", text: warning }));
    }
    if (run.listing) {
      const listing = run.listing;
      const label = {
        registering: t("Listing in Live Games…"),
        listed: t("Listed in Live Games as “{title}” ({visibility}).", { title: listing.title, visibility: listing.visibility }),
        failed: t("Not listed in Live Games."),
        ended: t("Its Live Games listing has ended."),
      }[listing.state] || listing.state;
      children.push(el("p", {
        className: "message" + (listing.state === "listed" ? " ok" : listing.state === "failed" ? " error" : ""),
        text: label + (listing.message ? " " + listing.message : ""),
      }));
      // While a game everyone can see is being listed or is listed — the time
      // somebody finds out whether players outside can reach it.
      if (publicListing(listing) && (listing.state === "registering" || listing.state === "listed")) {
        children.push(helpToConnect());
      }
    }

    const actions = el("div", { className: "activity-run__actions row-actions" });
    if (run.can_cancel) {
      const cancel = el("button", { text: "Cancel", attrs: { type: "button", "data-focus-key": "cancel" } });
      cancel.addEventListener("click", () => cancelRun(run));
      actions.append(cancel);
    }
    if (run.can_retry) {
      const retry = el("button", { text: "Try again", attrs: { type: "button", "data-focus-key": "retry" } });
      retry.addEventListener("click", () => retryRun(run.id));
      actions.append(retry);
    }
    if (actions.children.length) children.push(actions);

    // The tools' own output, above the technical facts (NEW_265). One view
    // per run, carried from card to card, so its text and cursor survive a
    // redraw; shown once there is a build step to read.
    if (current && jobToFollow(run)) {
      const { view } = outputFor(run);
      const scroller = view.root.querySelector(".joblog__scroll");
      if (scroller) scroller.dataset.scrollTop = String(scroller.scrollTop);
      children.push(view.root);
    }

    // The technical facts, behind a disclosure. Present for whoever needs them
    // and not in the way of whoever does not.
    children.push(details(run));

    return el("section", { className: "panel activity-run", children });
  }

  function wadOrigin(origin) {
    switch (origin) {
      case "installed": return t("installed on the deployment");
      case "user": return t("uploaded texture source");
      case "embedded": return t("carried inside the map");
      case "own_copy": return t("your own copy");
      default: return origin || t("origin not stated");
    }
  }

  function wadSourceText(source) {
    if (!source.staged) return notSentSentence(source.name, source.not_sent_reason);
    const parts = [wadOrigin(source.origin)];
    if (source.revision) parts.push(t("revision {n}", { n: source.revision }));
    if (source.sha256) parts.push(`sha256 ${source.sha256}`);
    if (source.credit) parts.push(t("credit: {credit}", { credit: source.credit }));
    return parts.join(" · ");
  }

  function details(run) {
    const rows = [];
    if (run.revision) rows.push(...line("Map revision", String(run.revision)));
    if (run.bundle) {
      rows.push(...line("Texture bundle", run.bundle.digest));
      rows.push(...line("WADs, in order", (run.bundle.wads_declared || []).join(", ")));
    }
    // Which source supplied each WAD the build read (NEW_313A): its origin,
    // its revision when it has one, the digest it was verified at and, for a
    // WAD installed on the deployment and sent under a declaration, the credit
    // that declaration states.
    for (const source of run.wad_sources || []) {
      rows.push(...line(t("WAD {name}", { name: source.name }), wadSourceText(source)));
    }
    if ((run.notices || []).length) {
      rows.push(...line(t("Third-party notices"),
        `${run.notices.map((n) => n.path).join(", ")} — ${run.notices_dir}`));
    }
    if (run.extractor) {
      rows.push(...line("Extractor",
        `${run.extractor.version}${run.extractor.verified ? ", verified" : ", a local override and NOT verified"}`));
    }
    if (run.build_id) rows.push(...line("Build", run.build_id));
    if (run.current_job) rows.push(...line("Running job", `${run.current_step} · ${run.current_job}`));
    if (run.installed_dir) rows.push(...line("Installed into", run.installed_dir));
    for (const file of run.installed || []) rows.push(...line(file.path, file.sha256));
    if (run.launch) {
      rows.push(...line("Command", [run.launch.executable, ...(run.launch.args || [])].join(" ")));
    }
    if (run.error && run.state !== "cancelled") {
      rows.push(el("dt", { text: "What it reported, in full" }),
        el("dd", { children: [el("pre", { className: "activity-raw", text: run.error })] }));
    }
    return el("details", {
      attrs: { "data-open-key": `tech:${run.id}` },
      children: [
        el("summary", { text: "Technical details" }),
        el("dl", { className: "summary-list", children: rows }),
      ],
    });
  }

  // A finished stage always shows a duration. Under a second is "<1s", not
  // "0s" — a stage that took 400 ms did not take nothing.
  function duration(ms) {
    if (!ms || ms < 1000) return "<1s";
    return elapsed(ms);
  }

  function elapsed(ms) {
    if (!ms) return "";
    const seconds = Math.round(ms / 1000);
    if (seconds < 60) return `${seconds}s`;
    return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  }

  // Cancelling is a decision with a consequence, so it is a modal question and
  // not a button that just acts.
  async function cancelRun(run) {
    const confirmed = await window.AUCOM.confirmModal?.({
      title: "Stop this build and run?",
      body: "The compiler is stopped and anything already installed is removed, so the game folder " +
        "is left as it was. Everything already downloaded stays in the cache.",
      confirm: "Stop it",
    });
    if (confirmed === false) return;
    const { ok, body } = await api(`/api/v1/play/runs/${encodeURIComponent(run.id)}/cancel`, { method: "POST" });
    if (!ok) announce(body.error);
    poll();
  }

  async function retryRun(id) {
    const { ok, body } = await api(`/api/v1/play/runs/${encodeURIComponent(id)}/retry`, { method: "POST" });
    if (!ok) {
      announce(body.error);
      return;
    }
    announce("Trying again.");
    watch(body.id);
    poll();
  }

  // --- wiring ------------------------------------------------------------------------

  for (const tab of document.querySelectorAll("#play-steps .bwiz-step")) {
    tab.addEventListener("click", () => show(Number(tab.dataset.step)));
  }
  for (const button of document.querySelectorAll("#area-play [data-go]")) {
    button.addEventListener("click", () => show(Number(button.dataset.go)));
  }

  // `change` only, and every change re-reads the revisions — including a
  // re-selection of the same map, which is how somebody who has just saved in
  // the editor gets the version they saved. Overlapping loads are handled by
  // revisionRequest rather than by refusing to start one.
  $("play-map").addEventListener("change", (event) => {
    state.map = state.maps.find((m) => m.asset_id === event.target.value) || null;
    invalidate("You changed the map, so its textures have to be checked again.");
    showAutobuild();
    loadRevisions(event.target.value);
  });
  $("play-revision").addEventListener("change", (event) => chooseRevision(event.target.value));
  $("play-source-file").addEventListener("change", (event) => {
    state.sourceFile = event.target.value;
    invalidate("You changed which file is built.");
  });
  $("play-map-name").addEventListener("input", (event) => {
    state.mapName = event.target.value.trim();
    invalidate("You changed the map's name in the game.");
  });
  $("play-pipeline").addEventListener("change", (event) => {
    state.pipeline = event.target.value;
    invalidate("You changed the build profile, so what it would run has to be worked out again.");
    renderStages();
    summarize();
  });
  $("play-strict").addEventListener("change", (event) => {
    state.strict = event.target.checked;
    invalidate("You changed how strict the build is.");
  });
  $("play-engine").addEventListener("change", (event) => {
    state.engine = event.target.value;
    invalidate("You changed the engine, so the command it would run has to be worked out again.");
    renderActions();
    summarize();
  });
  $("play-action").addEventListener("change", (event) => {
    state.action = event.target.value;
    invalidate("You changed what the engine does.");
    renderListing();
    summarize();
  });
  $("play-list-it").addEventListener("change", (event) => {
    state.listing.enabled = event.target.checked;
    $("play-listing-fields").hidden = !state.listing.enabled;
    renderHelpToConnect();
    invalidate("You changed whether the game is listed.");
  });
  for (const [id, field] of [["play-listing-title", "title"], ["play-listing-visibility", "visibility"],
    ["play-listing-host", "host"], ["play-listing-port", "port"]]) {
    $(id).addEventListener(id.endsWith("visibility") ? "change" : "input", (event) => {
      state.listing[field] = event.target.value;
      if (field === "host") applyAddressRule();
      if (field === "visibility") renderHelpToConnect();
      invalidate("You changed the Live Games listing.");
    });
  }
  $("play-mod").addEventListener("input", (event) => {
    state.mod = event.target.value.trim() || "auto-pigeon";
    invalidate("You changed the folder this installs into.");
    renderActions();
  });
  $("play-start").addEventListener("click", start);
  // The gallery's address arrives from the footer's one question to the server.
  document.addEventListener("aucom:site-links", () => {
    renderHelpToConnect();
    if (!$("activity").hidden) poll();
  });

  $("activity-open").addEventListener("click", () => {
    if ($("activity").hidden) openActivity();
    else closeActivity();
  });
  $("activity-close").addEventListener("click", closeActivity);
  // Escape closes a non-destructive panel. It does not stop the run.
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !$("activity").hidden) closeActivity();
  });
  for (const link of document.querySelectorAll("[data-area-link]")) {
    link.addEventListener("click", () => { window.location.hash = "#" + link.dataset.areaLink; });
  }

  let restored = false;
  let loading = false;
  // Settles when the lists the current refresh asked for have arrived and the
  // remembered map and revision are chosen again. See checkOwnWads.
  let ready = Promise.resolve();
  window.AUCOM.areas.play = {
    async refresh() {
      if (!restored) {
        restored = true;
        restore();
      }
      const signedIn = Boolean(window.AUCOM.status?.authenticated);
      $("play-signed-out").hidden = signedIn;
      // The step a link or a reload named is shown at once; the lists fill
      // in behind it rather than step 1 standing in for a few seconds.
      loading = true;
      // Nothing on the review may be pressed while the map it is about is
      // still arriving: the review is drawn again once it has.
      if (state.step >= 4) {
        state.plan = null;
        state.planKey = "";
        $("play-review").replaceChildren(el("p", { className: "muted", text: t("Loading the map this page was opened on…") }));
      }
      // The one button is pressed only after a review of these exact choices.
      // A reload (or a link) that named step 5 has no review any more, and
      // whatever the person confirmed in it — their own WADs among it — is
      // not remembered: so it lands on the review, which is worked out again,
      // rather than on a button that would start the old choices unseen
      // (NEW_310A, live on Windows).
      if (state.step === 5) state.step = 4;
      show(state.step);
      let settle;
      ready = new Promise((resolve) => { settle = resolve; });
      try {
        await Promise.all([loadPipelines(), loadEngines(), signedIn ? loadMaps() : null]);
        if (signedIn && state.map) await loadRevisions(state.map.asset_id, { keep: true });
      } finally {
        loading = false;
        settle();
      }
      show(state.step);
      poll();
    },
  };

  // Activity is not an area: it belongs to the window, and a run started here
  // is still running when somebody is looking at Profiles. So the poll starts
  // as soon as the page does.
  poll();
})();
