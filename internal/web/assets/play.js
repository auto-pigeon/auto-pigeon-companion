// Build & Run — the main product journey, and the Activity surface.
//
// One form (NEW_363): the map, the build profile, whether to run it after the
// build, and a review whose one primary action says what it does — "Review
// build" until a review of these exact choices is on screen, then "Build &
// Run" or "Build". Everything after that press happens on the server. This file does NOT sequence the work: it collects the exact
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
    // "saved" (a map revision from the account, this file) or "local" (a file
    // on this computer, built by build.js). One form, two sources.
    source: "saved",
    // "Run after build": off, the run stops once the map is compiled
    // (playrun.Request.BuildOnly) and the primary says Build.
    runAfter: true,
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
    // A Quake II build is played from a package (Q2_020): what the review was
    // told about the content folder's own files. The package panel's answers,
    // asked once for all of them; never remembered in the URL.
    grant: { basis: "", licence: "", accept: false, reason: "" },
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
    strict: "strict", engine: "engine", action: "action", mod: "mod", src: "source", run: "run_after",
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
      mod: state.mod === "auto-pigeon" ? "" : state.mod,
      source: state.source === "local" ? "local" : "", run_after: state.runAfter ? "" : "0",
    };
    const query = new URLSearchParams(window.location.search);
    // The wizard's step (before NEW_363) means nothing to the form.
    query.delete("step");
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
    if (saved.source) state.source = saved.source === "local" ? "local" : "saved";
    state.runAfter = saved.run_after !== "0";
    $("play-run-after").checked = state.runAfter;
    if (saved.map_name) {
      $("play-map-name").value = saved.map_name;
      state.mapName = saved.map_name;
    }
    $("play-mod").value = state.mod;
    $("play-strict").checked = state.strict;
  }

  // --- the form ------------------------------------------------------------------

  // What stops this form being reviewed or started, in one sentence, or "".
  // Said beside the primary rather than on a disabled button nobody can ask
  // (operator, 2026-09-23).
  function missingSentence() {
    if (!window.AUCOM.status?.authenticated) return t("Sign in to build a saved map.");
    if (!state.revision) return t("Choose a map and one of its revisions first.");
    if (!String(state.mapName || "").trim()) return t("Give the map a name in the game first.");
    const pipeline = state.pipelines.find((p) => p.id === state.pipeline);
    if (!pipeline) return t("Choose a build profile first.");
    if (!window.AUCOM.pipelineReady(pipeline)) return t("The chosen build profile needs setup first.");
    if (!state.runAfter) return "";
    const listed = !hosting() || !state.listing.enabled ||
      (String(state.listing.host || "").trim() !== "" && Number(state.listing.port) > 0);
    if (!state.engine) return t("Choose an engine first.");
    if (!state.action) return t("Choose how to play it first.");
    if (!state.gameRoot) return t("Set up where the game is first: Setup, in Profiles.");
    if (!listed) return t("Give the address and port players connect to first.");
    return "";
  }

  // The review is current only for the exact choices it was worked out for.
  function reviewed() {
    return Boolean(state.plan) && state.planKey === planKey();
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

  // The engine family of the chosen build profile, as its document says.
  function family() {
    return state.pipelines.find((p) => p.id === state.pipeline)?.engine_family || "";
  }

  // A Quake II map is played from a package (Q2_020): built, packaged with the
  // files the person says may travel, installed in a mod folder of its own and
  // accepted on the engine's word — the package panel's path, from here.
  function packaged() {
    return family() === "quake2";
  }

  // Which engines can play what the build profile makes is the server's word,
  // one verdict per engine on the pipeline's own row (NEW_330, `engine_fit`):
  // the documents' declared games, never a name. Until NEW_330 the page held
  // only Quake II to it, so a Quake 1 build offered Yamagi as "Ready to start".
  // No build profile chosen yet is no verdict yet.
  function engineFit(engine) {
    const pipeline = state.pipelines.find((p) => p.id === state.pipeline);
    return pipeline?.engine_fit?.[engine?.id] || { fits: true };
  }

  function engineFits(engine) {
    return engineFit(engine).fits !== false;
  }

  // The game's own data folder — id1, baseq2, baseq3 — as the chosen engine
  // declares it, for every sentence that says where files go (NEW_330).
  function baseFolder(engine) {
    return engine?.base_folder || state.engines.find((e) => e.id === state.engine)?.base_folder || "";
  }

  // Any change to an identity throws the plan away and SAYS SO. A review that
  // silently described the previous choice would be the worst kind of review.
  function invalidate(why) {
    setMessage("play-start-message", "");
    remember();
    if (!state.plan) {
      renderFinalSummary();
      return;
    }
    state.plan = null;
    state.planKey = "";
    $("play-review").replaceChildren();
    $("play-review-attention").replaceChildren();
    setMessage("play-review-message", t(why));
    announce(t(why));
    renderFinalSummary();
  }

  function planKey() {
    return [
      state.map?.asset_id, state.revision?.revision_id, state.sourceFile, state.mapName,
      state.pipeline, String(state.strict), state.engine, state.action, state.mod, state.ownWadsDir,
      JSON.stringify(listingBody() || null),
    ].join("|");
  }

  // --- the map ------------------------------------------------------------------

  // Every map in the account, a page at a time. The first 200 used to be all
  // the list ever held, so an older map — dm2, on an account with a few
  // hundred — could not be chosen at all (operator, 2026-09-22).
  const MAP_PAGES = 25;
  // The account could not be asked for its maps because the server did not
  // answer (502 from the local API): said in words, with the address, the
  // server's own error kept beside it, and a way to ask again once it is back
  // (NEW_330 A11). A map file on this computer still builds under Build.
  function mapsUnreachable(detail) {
    const address = (window.AUCOM.status?.aub_base_url || "").replace(/\/+$/, "");
    const box = $("play-map-message");
    box.className = "message error";
    box.replaceChildren(
      document.createTextNode(t("The Auto-Pigeon server at {address} did not answer, so your maps cannot be listed. A map file on this computer still builds under Build.",
        { address: address || t("its address") }) + " "),
    );
    const retry = el("button", { text: t("Try again"), attrs: { type: "button" } });
    retry.addEventListener("click", () => window.AUCOM.withBusy(retry, async () => {
      box.replaceChildren();
      await loadMaps();
    }));
    box.append(retry, el("span", { className: "muted small", text: " " + (detail || "") }));
    announce(box.firstChild.textContent);
  }

  // Which account an answer belongs to: the server and the person signed in
  // when it was asked. A list asked for one account and answered after a
  // sign-out, a switch of account or of server is not this account's list —
  // nor is its error (NEW_360 F2: a stale 401 stayed under the map list after
  // a successful sign-in).
  function accountKey() {
    const status = window.AUCOM.status || {};
    return [status.aub_base_url || "", status.email || "", Boolean(status.authenticated)].join("|");
  }

  // A refusal of the map list, as one sentence and the action that fixes it,
  // chosen by the code the Companion classified from AUB's status and reason
  // (internal/web/failurewords.go) — never by reading the English. The
  // server's own words stay one click away under Technical details.
  function mapsRefused(status, body) {
    const code = body.code || (status === 502 || status === 503 ? "server_unavailable" : "");
    if (code === "server_unavailable") {
      mapsUnreachable(body.error);
      return;
    }
    const box = $("play-map-message");
    box.className = "message error";
    box.replaceChildren();
    if (code === "sign_in_required") {
      box.append(document.createTextNode(t("Your sign-in has expired, so your maps cannot be listed.") + " "));
      const signIn = el("button", { text: t("Sign in"), attrs: { type: "button" } });
      signIn.addEventListener("click", () => $("sign-in-open").click());
      box.append(signIn);
    } else if (code === "legal_review_required") {
      // Only the person can review and accept them, on the Auto-Pigeon site
      // the account is on; this program never accepts anything for them.
      box.append(document.createTextNode(t("The Terms or the notices changed. Review and accept them on the Auto-Pigeon site to list your maps.") + " "));
      const site = window.AUCOM.officialSite?.(window.AUCOM.status || {});
      if (site) {
        box.append(el("a", { text: t("Review them on {site}", { site: site.host }),
          attrs: { href: site.url, target: "_blank", rel: "noopener noreferrer" } }), document.createTextNode(" "));
      }
      // Once they are accepted there, the list is asked for again here.
      const again = el("button", { text: t("Try again"), attrs: { type: "button" } });
      again.addEventListener("click", () => window.AUCOM.withBusy(again, loadMaps));
      box.append(again);
    } else {
      box.append(document.createTextNode(t("The Auto-Pigeon server refused to list your maps.") + " "));
      const retry = el("button", { text: t("Try again"), attrs: { type: "button" } });
      retry.addEventListener("click", () => window.AUCOM.withBusy(retry, loadMaps));
      box.append(retry);
    }
    box.append(technicalDetails(body.error));
    announce(box.firstChild.textContent);
  }

  // The server's exact words, folded: for whoever has to report it.
  function technicalDetails(text) {
    if (!text) return document.createTextNode("");
    return el("details", { className: "message-details", children: [
      el("summary", { text: t("What the server answered") }),
      el("pre", { className: "activity-raw", text }),
    ] });
  }

  let mapsAsked = 0;
  async function loadMaps() {
    const asked = ++mapsAsked;
    const account = accountKey();
    const current = () => asked === mapsAsked && account === accountKey();
    let items = [];
    let cursor = "";
    for (let page = 0; page < MAP_PAGES; page += 1) {
      const query = new URLSearchParams({ type: "map", limit: "200" });
      if (cursor) query.set("cursor", cursor);
      const { ok, status, body } = await api("/api/v1/library/catalog?" + query.toString());
      if (!current()) return;
      if (!ok) {
        mapsRefused(status, body);
        return;
      }
      items = items.concat(body.items || []);
      cursor = body.has_more ? body.next_cursor || "" : "";
      if (!cursor) break;
    }
    // This account's list arrived: whatever an earlier question failed with is
    // no longer true.
    setMessage("play-map-message", "");
    // A remembered map the listing did not reach is asked for by id, so a link
    // to a map always opens on it.
    if (pendingMap && !items.some((m) => m.asset_id === pendingMap)) {
      const { ok, body } = await api(`/api/v1/library/assets/map/${encodeURIComponent(pendingMap)}`);
      if (!current()) return;
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
    const { ok, status, body } = await api(`/api/v1/library/assets/map/${encodeURIComponent(assetID)}`);
    if (request !== revisionRequest) return;
    if (!ok) {
      mapsRefused(status, body);
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
    await loadSourceFiles();
    renderFinalSummary();
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

  // The name this page last suggested. A field still holding it holds the
  // page's name, not the person's, so choosing another map replaces it
  // (Q2_020: choosing one map and then another kept the first map's name, and
  // the second map was compiled, installed and started under it). A name the
  // person typed, or one a link or a reload restored, is theirs and stays.
  let suggestedMapName = "";

  // A suggestion, not a rule: the engine's own sanitizing rules are the
  // authority, and the field is editable because a person may want another name.
  function suggestMapName() {
    const current = $("play-map-name").value.trim();
    if (current && current !== suggestedMapName) return;
    const stem = (state.sourceFile || state.map?.display_name || "")
      .replace(/\.[^.]*$/, "")
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, "_")
      .replace(/^_+|_+$/g, "");
    if (!stem) return;
    $("play-map-name").value = stem.slice(0, 64);
    state.mapName = $("play-map-name").value;
    suggestedMapName = state.mapName;
  }

  // Auto-build on the chosen map (autobuild.js) when it is a saved one, under
  // the build profile it uses — the one chosen here. A file on this computer
  // has no saved revisions to watch, so it is not offered there.
  function showAutobuild() {
    const saved = state.source === "saved" ? state.map : null;
    window.AUCOM.autobuildPanel?.show(saved, Boolean(window.AUCOM.status?.authenticated));
  }
  window.AUCOM.play = {
    pipelines: () => state.pipelines,
    pipeline: () => state.pipeline,
    // Auto-build tells Activity a build of its own has started.
    pollActivity: () => poll(),
  };

  // --- the build profile ---------------------------------------------------------

  let pipelineRefresh = 0;
  let engineRefresh = 0;
  async function loadPipelines() {
    const sequence = ++pipelineRefresh;
    const { ok, body } = await api("/api/v1/build/pipelines");
    if (sequence !== pipelineRefresh) return;
    if (!ok) { state.pipelines = []; state.pipeline = ""; $("play-pipeline").replaceChildren(); renderStages(); renderFinalSummary(); return; }
    state.pipelines = body.items || [];
    const select = $("play-pipeline");
    state.pipeline = window.AUCOM.executionChoices(select, state.pipelines, state.pipeline, window.AUCOM.pipelineReady,
      // What it waits for is in the closed list under the select (NEW_345).
      (pipeline) => pipeline.name + (window.AUCOM.pipelineReady(pipeline) ? "" : " — " + t("needs setup")),
      // retain (NEW_323E's rule, here too since NEW_330): a chosen build
      // profile that loses readiness — a program path removed in Profiles —
      // stays chosen, greyed out and saying why, and is ready again when the
      // path is repaired. Dropping it made the person choose it again, and the
      // next refresh could have chosen another one behind them.
      "Choose a ready build profile…", false, true, undefined, { setupLabel: "build-profiles" });
    renderStages();
    // The engines may have arrived first; which of them fit is the family's.
    if (state.engines.length) renderEngines();
    pipelineChanged();
  }

  function renderStages() {
    const list = $("play-stages");
    list.replaceChildren();
    const pipeline = state.pipelines.find((p) => p.id === state.pipeline);
    for (const step of pipeline?.steps || []) {
      list.append(el("li", {
        text: `${step.title || step.id} — ${step.provider?.name || step.capability}`,
      }));
    }
    renderPipelineState();
  }

  // The programs that run, by their own name and version — what meaningfully
  // identifies a build profile in one line ("ericw-tools 2.0.0-alpha11").
  function programsOf(pipeline) {
    const names = (pipeline?.steps || []).map((step) => step.provider
      ? `${step.provider.name} ${step.provider.version || ""}`.trim() : "").filter(Boolean);
    return [...new Set(names)].join(", ");
  }

  // The SELECTED build profile's state, beside the list (NEW_345's rule): ready
  // and what runs, or what it waits for and where that is set up. The other
  // profiles' setup is the closed list under it, from executionChoices.
  function renderPipelineState() {
    const node = $("play-pipeline-state");
    node.replaceChildren();
    node.className = "choice-state";
    for (const stale of node.parentElement.querySelectorAll(".wip")) stale.remove();
    const pipeline = state.pipelines.find((p) => p.id === state.pipeline);
    if (!pipeline) {
      if (state.pipelines.length && !state.pipelines.some(window.AUCOM.pipelineReady)) {
        node.textContent = t("No build profile is ready on this computer yet. Set one up from the list below.");
        node.classList.add("choice-state--attention");
      }
      return;
    }
    if (window.AUCOM.pipelineReady(pipeline)) {
      node.append(window.AUCOM.badge(t("ready"), "succeeded"), document.createTextNode(" " + programsOf(pipeline)));
    } else {
      node.classList.add("choice-state--attention");
      for (const cause of window.AUCOM.setupCauses(pipeline)) {
        node.append(document.createTextNode(cause.text + " "), window.AUCOM.setupLink(cause));
      }
    }
    // A work-in-progress game says so, beside the profile that builds it.
    const note = window.AUCOM.maturityNote(pipeline.maturity, () =>
      window.AUCOM.openCompatibilityReport({
        family: pipeline.engine_family, operation: "compile",
        about: `About the ${pipeline.name} build profile.`,
        profiles: [{ role: "pipeline", id: pipeline.id, version: pipeline.version }],
      }));
    if (note) node.after(note);
  }

  // Auto-build reads the profile chosen here; it is told when that changes.
  function pipelineChanged() {
    document.dispatchEvent(new CustomEvent("aucom:play-pipeline"));
  }

  // --- run after build: the engine ---------------------------------------------

  async function loadEngines() {
    const sequence = ++engineRefresh;
    const { ok, body } = await api("/api/v1/engines");
    if (sequence !== engineRefresh) return;
    if (!ok) { state.engines = []; state.engine = ""; $("play-engine").replaceChildren(); renderActions(); renderFinalSummary(); return; }
    state.engines = body.items || [];
    renderEngines();
  }

  // What stops an engine, in the words Profiles uses. The engine's own
  // problems first; an engine that is ready in itself and only an ACTION of
  // it is not (Q2_020, seen live: Yamagi's "Play a map" needed a folder) is
  // named by that action's problems, rather than by "Needs setup:" and
  // nothing after it.
  function offeredProblems(engine) {
    const own = engine.readiness?.problems || [];
    const offered = Object.keys(ACTIONS).filter((id) => !packaged() || id === "play_map");
    return own.length ? own : offered.flatMap((id) => engine.action_problems?.[id] || []);
  }

  function renderEngines() {
    const select = $("play-engine");
    const ready = (engine) => Object.keys(ACTIONS).some((id) => window.AUCOM.actionReady(engine, id));
    const previous = state.engine;
    state.engine = window.AUCOM.executionChoices(select, state.engines, state.engine,
      (engine) => engineFits(engine) && ready(engine),
      (engine) => `${engine.name} — ${!engineFits(engine) ? t("does not play this build profile's maps") : ready(engine) ? t("Ready to start") : t("Needs setup")}`,
      "Choose a ready engine…", false, false, engineFits, { setupLabel: "engines", setupProblems: offeredProblems });
    // A build profile for another game clears an engine that cannot play it,
    // at once, and says why — the person's saved setup is untouched. The why
    // stays, said against the build profile chosen NOW, until an engine is
    // chosen: arrowing through the list is several changes, and the first
    // one's message must not be wiped by the next (seen live, NEW_330).
    const lost = state.engines.find((e) => e.id === previous);
    if (previous && !state.engine && lost && !engineFits(lost)) state.droppedEngine = previous;
    if (state.engine) state.droppedEngine = "";
    const dropped = state.engines.find((e) => e.id === state.droppedEngine);
    const why = dropped && !engineFits(dropped) ? engineFit(dropped).reason || "" : "";
    if (why !== $("play-engine-message").textContent) {
      setMessage("play-engine-message", why);
      if (why) announce(why);
    }
    renderActions();
  }

  function renderActions() {
    const select = $("play-action");
    select.replaceChildren();
    const engine = state.engines.find((e) => e.id === state.engine);
    // Only what makes sense for one freshly built map, in the order a person
    // would pick them. "Join a server" and "Play a mod or package" belong to
    // Live Games and Run: offered here, as they were, they were four tabs whose
    // meaning nobody could guess (operator, 2026-09-22).
    // A Quake II map is played on this computer; hosting one is not offered
    // here (Q2_020), so only "Play it" is.
    const offered = Object.keys(ACTIONS)
      .filter((id) => !packaged() || id === "play_map")
      .map((id) => (engine?.actions || []).find((action) => action.id === id))
      .filter(Boolean);
    for (const action of offered) {
      const ready = window.AUCOM.actionReady(engine, action.id);
      // An option's own label says only that it needs setup (aucom.ux); what
      // it waits for is said below, for the action chosen.
      select.append(el("option", {
        text: actionTitle(action.id) + (ready ? "" : " — " + t("needs setup")),
        attrs: { value: action.id, disabled: ready ? null : "disabled" },
      }));
    }
    if (state.action && offered.some((a) => a.id === state.action && window.AUCOM.actionReady(engine, a.id))) select.value = state.action;
    else select.value = offered.find((a) => window.AUCOM.actionReady(engine, a.id))?.id || "";
    state.action = select.value || "";
    // A choice only when there genuinely is one: an engine that offers one way
    // to play a freshly built map (a Quake II build's "Play it") has nothing to
    // choose, and a select of one says nothing (NEW_363).
    const choices = $("play-action-choices");
    choices.replaceChildren();
    for (const action of offered) {
      const input = el("input", {
        attrs: { type: "radio", name: "play-action-choice", value: action.id, id: "play-action-" + action.id },
      });
      input.disabled = !window.AUCOM.actionReady(engine, action.id);
      input.checked = !input.disabled && action.id === state.action;
      input.addEventListener("change", () => {
        if (input.disabled || !input.checked) return;
        select.value = action.id;
        select.dispatchEvent(new Event("change", { bubbles: true }));
      });
      choices.append(el("label", {
        className: "segmented__option",
        attrs: { for: input.id },
        children: [input, el("span", { text: actionTitle(action.id) + (input.disabled ? " — " + t("needs setup") : "") })],
      }));
    }
    $("play-action-field").hidden = !engine || offered.length < 2;
    $("play-action-note").textContent = ACTIONS[state.action] ? t(ACTIONS[state.action].text) : "";
    const root = engine?.binding?.roots?.game_root || "";
    state.gameRoot = root;
    // The mod folder field: a Quake II package's own folder (aup-<map>) is
    // what an untouched field means, and it says so rather than showing the
    // Quake 1 default it would not use.
    const modField = $("play-mod");
    if (packaged() && state.mod === "auto-pigeon") {
      modField.value = "";
      modField.placeholder = modFolder();
    } else {
      modField.placeholder = "";
      if (modField.value !== state.mod && document.activeElement !== modField) modField.value = state.mod;
    }
    const base = baseFolder(engine);
    $("play-mod-where").textContent = base
      ? t("(created beside {base} in the game folder)", { base })
      : t("(created in the game folder)");
    $("play-game-root").textContent = root
      ? (base
        ? t("The game is at {root}. The Companion will create {mod} beside {base} and never write into {base}.",
          { root, mod: packaged() ? modFolder() : state.mod, base })
        : t("The game is at {root}. The Companion will create {mod} there.", { root, mod: packaged() ? modFolder() : state.mod }))
      : t("This engine has no game folder set on this machine yet — set one under Profiles first.");
    // Only once an engine is chosen: before that there is nothing to be wrong
    // about, and an error on an untouched form reads as a fault.
    const missing = Boolean(state.engine) && !root;
    $("play-game-root").textContent = !state.engine
      ? t("Choose an engine to see where the game is.")
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
    } else if (engine && !offered.length) {
      setMessage("play-run-message", t("This engine's profile offers no way to play a single map."), "error");
    } else {
      setMessage("play-run-message", missing ? t("Choose this engine's game folder before continuing.") : "",
        missing ? "error" : "");
    }
    renderListing();
    // The action and the game folder decide what the primary may do.
    renderFinalSummary();
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

  // --- the review ------------------------------------------------------------------

  async function refreshPlan() {
    const key = planKey();
    if (state.plan && state.planKey === key) return;
    const review = $("play-review");
    review.replaceChildren();
    busyMessage("play-review-message", t("Working out what this would do…"));
    const { ok, body } = await api("/api/v1/play/plan", { method: "POST", body: requestBody() });
    // A plan for choices changed while it was worked out is not shown.
    if (key !== planKey()) return;
    if (!ok) {
      state.plan = null;
      review.replaceChildren();
      setMessage("play-review-message", body.error, "error");
      renderFinalSummary();
      return;
    }
    state.plan = body;
    state.planKey = key;
    setMessage("play-review-message", "");
    renderReview(body);
    renderFinalSummary();

    // The ordered WAD set is part of what the review is FOR, and the plan does
    // not download anything — so when this machine holds no verified bundle for
    // the revision yet, the review fetches and verifies one now. It is the same
    // bundle the build will read, cached by its own digest, so pressing Build &
    // Run afterwards costs nothing extra. What is never done is INVENTING the
    // list: until this returns, the review says the textures will be fetched.
    // A Quake II run's review does not list WADs: its texture export (account
    // texture sources at their bound revisions) is downloaded by the run itself,
    // and its other .wal files come from the base game folder (Q2_015A). The
    // plan's texture message says so.
    if (!body.packaged && body.textures && body.textures.known === false && window.AUCOM.status?.authenticated) {
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
        renderFinalSummary();
      } else if (!gotTextures) {
        serverFailure("play-review-message", textures);
      }
    }
  }

  // The mod folder: for a Quake II package, one per map (aup-<name>) unless
  // somebody named another.
  function modFolder() {
    if (packaged() && state.mod === "auto-pigeon") return "aup-" + ($("play-map-name").value.trim() || "<map>");
    return state.mod;
  }

  // The review's answer about the content folder's files, as the package
  // planner's grants. Nothing when nobody answered: the plan then names each
  // file and the run stops before installing.
  function grantsBody() {
    if (!packaged() || !state.grant.basis) return undefined;
    const grant = { loose: true, basis: state.grant.basis };
    if (state.grant.basis === "licensed") grant.licence = state.grant.licence.trim();
    return [grant];
  }

  function requestBody() {
    if (!state.runAfter) {
      // Build only (NEW_363): the same coordinator, and nothing installed or
      // started — so no engine, folder, listing or package answers.
      return {
        asset_type: "map",
        asset_id: state.map?.asset_id || "",
        revision_id: state.revision?.revision_id || "",
        revision_number: state.revision?.revision || 0,
        source_file: state.sourceFile,
        pipeline: state.pipeline,
        strict: state.strict,
        map: $("play-map-name").value.trim(),
        own_wads_dir: state.ownWadsDir || undefined,
        build_only: true,
      };
    }
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
      mod: packaged() && state.mod === "auto-pigeon" ? "" : state.mod,
      map: $("play-map-name").value.trim(),
      own_wads_dir: state.ownWadsDir || undefined,
      listing: listingBody(),
      grants: grantsBody(),
      accept_reason: packaged() && state.grant.accept ? state.grant.reason.trim() || undefined : undefined,
    };
  }

  // The review, in two parts (NEW_363). What only the person can settle before
  // this build can run — a texture the server did not send, a Quake II
  // package's own files, a Live Games listing — is in the open, under the
  // summary. Everything else that will happen — what is downloaded, which
  // programs run at which version, where files are written, the exact command —
  // is under Technical details, as a grid of cards two to a row on a wide window
  // (operator, 2026-09-22). It used to be a whole step, and then a second step
  // that said most of it again.
  function renderReview(plan) {
    const attention = $("play-review-attention");
    attention.replaceChildren();
    const review = $("play-review");
    review.replaceChildren();
    review.className = "review-grid";

    if (plan.textures?.known && plan.textures.compiler_ready === false) {
      attention.append(plan.textures.own_wads_possible ? ownWadsOffer(plan.textures) : refusalNotice(plan.textures));
    }
    if (plan.textures_warning) {
      attention.append(el("p", { className: "message error", attrs: { role: "alert" }, text: t(plan.textures_warning) }));
    }
    if (plan.packaged) attention.append(packageSection(plan));
    if (hosting() && state.runAfter) {
      const placeholder = el("section", { className: "panel review-card" });
      attention.append(placeholder);
      listingCard().then((card) => placeholder.replaceWith(card));
    }

    review.append(section(t("What will be downloaded"), [
      line(t("Map"), state.map?.display_name || plan.map.asset_id),
      line(t("Revision"), String(plan.map.revision)),
      line(t("Map file"), plan.map.source_file || t("the revision's only file")),
      line(t("Name in the game"), plan.run?.map || $("play-map-name").value.trim()),
    ]));

    review.append(texturesSection(plan.textures));

    const programs = [line(t("Build profile"), nameOfPipeline(plan.build.pipeline))];
    const pipeline = state.pipelines.find((p) => p.id === plan.build.pipeline);
    for (const step of pipeline?.steps || []) {
      programs.push(line(step.title || step.id,
        `${step.provider?.name || step.capability} ${step.provider?.version || ""}`.trim()));
    }
    programs.push(line(t("Extractor"), t("Auto-Pigeon Extractor, when the map needs converting")));
    if (!plan.build_only) programs.push(line(t("Engine"), `${nameOfEngine(state.engine)} · ${actionTitle(state.action)}`));
    review.append(section(t("Which programs will run"), programs));

    if (plan.writes) {
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
    }

    if (plan.launch) review.append(launchSection(plan.launch));
    if (plan.build_preview_note) {
      review.append(el("p", { className: "muted small review-note", text: plan.build_preview_note }));
    }
  }

  // The package a Quake II run will make (Q2_020): what goes in, and the one
  // question only the person can answer — whether the files of their content
  // folder that the map names may travel with it. The same answers the
  // package panel asks for, given once for all of them.
  const GRANT_BASES = [
    ["", "Not answered — stop and name each file"],
    ["own_work", "It is my own work"],
    ["licensed", "Licensed — I will name the licence"],
    ["not_redistributable", "It may not be redistributed"],
  ];

  function packageSection(plan) {
    const basis = el("select", { attrs: { id: "play-grant-basis", "aria-describedby": "play-grant-note" } });
    for (const [value, label] of GRANT_BASES) basis.append(el("option", { text: t(label), attrs: { value } }));
    basis.value = state.grant.basis;
    const licence = el("input", { attrs: { id: "play-grant-licence", type: "text", placeholder: t("e.g. CC-BY-4.0"), value: state.grant.licence } });
    const licenceField = el("p", { className: "field", children: [
      el("label", { text: t("Licence"), attrs: { for: "play-grant-licence" } }), licence] });
    licenceField.hidden = state.grant.basis !== "licensed";
    const accept = el("input", { attrs: { id: "play-grant-accept", type: "checkbox" } });
    accept.checked = state.grant.accept;
    const reason = el("textarea", { attrs: { id: "play-grant-reason", rows: "2", placeholder: t("Why the package may leave these files out") } });
    reason.value = state.grant.reason;
    reason.hidden = !state.grant.accept;
    basis.addEventListener("change", () => {
      state.grant.basis = basis.value;
      licenceField.hidden = basis.value !== "licensed";
      renderFinalSummary();
    });
    licence.addEventListener("input", () => { state.grant.licence = licence.value; });
    accept.addEventListener("change", () => {
      state.grant.accept = accept.checked;
      reason.hidden = !accept.checked;
      renderFinalSummary();
    });
    reason.addEventListener("input", () => { state.grant.reason = reason.value; });
    return el("section", {
      className: "panel review-card",
      children: [
        el("h4", { text: t("The package") }),
        el("p", { className: "muted small", text: plan.maturity_message || "" }),
        el("p", { className: "muted", attrs: { id: "play-grant-note" }, text: plan.package?.message || "" }),
        el("p", { className: "field", children: [
          el("label", { text: t("Your own files in the package"), attrs: { for: "play-grant-basis" } }), basis] }),
        licenceField,
        el("p", { className: "field", children: [
          el("label", { attrs: { for: "play-grant-accept" }, children: [accept,
            el("span", { text: " " + t("Accept in writing what the package will not carry") })] }), reason] }),
      ],
    });
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
    // Why each was not sent, once (NEW_345). A WAD whose row above already
    // says why (the deployment has no texture source of that name at all) is
    // not given the general redistribution sentence too: the two read as two
    // different reasons for one fact. A specific reason AUB gave is always said.
    const said = (name) => (textures.wads || []).find((wad) => !wad.included && wad.name === name && wad.note);
    const why = names.filter((name) => notSentReason(textures, name) || !said(name));
    if (why.length) {
      box.append(el("ul", {
        className: "plain",
        children: why.map((name) => el("li", { text: notSentSentence(name, notSentReason(textures, name)) })),
      }));
    }
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
      setMessage("play-review-message", t("Choose a map and one of its revisions first."), "error");
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
    renderFinalSummary();
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

  // --- the summary and the one primary action ----------------------------------------

  // The review's summary: what this form would build, from the choices on
  // screen; then the one primary action. What it says is what it does.
  function renderFinalSummary() {
    if (state.source !== "saved") return;
    const summary = $("play-final-summary");
    const textures = state.plan?.textures;
    const own = textures?.ready_with_own_wads && textures.own_wads_dir === state.ownWadsDir;
    const listing = state.runAfter ? listingBody() : undefined;
    summary.replaceChildren(
      ...line(t("Map"), state.revision
        ? `${state.map?.display_name || ""} · ${t("revision {n}", { n: state.revision.revision })}` : t("not chosen yet")),
      ...line(t("Build profile"), nameOfPipeline(state.pipeline) || t("not chosen yet")),
      ...line(t("Then"), !state.runAfter ? t("Nothing starts: the build stops once the map is compiled.")
        : state.engine ? `${nameOfEngine(state.engine)} · ${actionTitle(state.action)}` +
          (state.gameRoot ? ` → ${state.gameRoot}/${modFolder()}` : "") : t("not chosen yet")),
      ...(packaged() && state.runAfter ? line(t("Your own files in the package"),
        t((GRANT_BASES.find(([value]) => value === state.grant.basis) || GRANT_BASES[0])[1]) +
        (state.grant.accept ? " · " + t("what it will not carry is accepted in writing") : "")) : []),
      ...(own ? line(t("Your own WADs"), `${(textures.own_wads_needed || []).join(", ")} ← ${state.ownWadsDir}`) : []),
      ...(listing ? line(t("Live Games"), `${listing.title} · ${$("play-listing-visibility").selectedOptions[0]?.textContent || ""}` +
        ` · ${listing.endpoint_host}:${listing.endpoint_port}`) : []),
    );
    const why = missingSentence();
    const current = reviewed();
    const texturesOk = !current || state.plan.packaged || textures?.compiler_ready !== false || Boolean(own);
    const button = $("play-start");
    button.textContent = !current ? t("Review build") : state.runAfter ? t("Build & Run") : t("Build");
    button.disabled = Boolean(why) || !texturesOk;
    // A message the button's own press wrote ("Started.") stays until the
    // next change; otherwise this is what stops the press, or nothing.
    if (!$("play-start-message").dataset.pressed) {
      setMessage("play-start-message", why || (!texturesOk ? t("This map's textures are not complete enough to compile — see the review.") : ""));
    }
  }

  // The primary: work the review out, or — once it is on screen for exactly
  // these choices — start. Never both in one press: what is started is what
  // was reviewed.
  async function primary() {
    delete $("play-start-message").dataset.pressed;
    if (!reviewed()) {
      await refreshPlan();
      if (reviewed()) {
        announce(state.runAfter ? t("Review ready. Press Build & Run to start.") : t("Review ready. Press Build to start."));
      }
      return;
    }
    await start();
  }

  async function start() {
    setMessage("play-start-message", "");
    const { ok, body } = await api("/api/v1/play/runs", { method: "POST", body: requestBody() });
    if (!ok) {
      setMessage("play-start-message", body.code === "auto_build_running"
        ? t("An automatic build of this revision is already running. Follow it with View progress.") : body.error, "error");
      $("play-start-message").dataset.pressed = "1";
      return;
    }
    // Everyone was this launch's choice, not the next one's (NEW_247A2): the
    // next launch starts Private. The review, asked for with the listing it
    // previews, is worked out again before the next start.
    if (state.listing.visibility !== FRESH_VISIBILITY) {
      state.listing.visibility = FRESH_VISIBILITY;
      renderListing();
      invalidate("The next game you host starts Private again, so the review is worked out again.");
    }
    // The run has its card now; the form is a draft for the NEXT build, and
    // starting another is a new decision, made after its own review — not a
    // second press of a button that still says Build.
    state.plan = null;
    state.planKey = "";
    $("play-review").replaceChildren();
    $("play-review-attention").replaceChildren();
    setMessage("play-review-message", "");
    setMessage("play-start-message", t("Started."), "ok");
    $("play-start-message").dataset.pressed = "1";
    announce(state.runAfter ? t("Build and run started.") : t("Build started."));
    watch(body.id);
  }

  function busyMessage(id, text) {
    window.AUCOM.busy(id, text);
  }

  // One sentence for a failure the Companion classified (failurewords.go),
  // in this page's language, and the server's own words under Technical
  // details. Used for a run's card and for the review's texture check.
  const FAILURE_WORDS = {
    sign_in_required: ["Your sign-in has expired, so the download could not finish.", "Sign in again, then press Try again."],
    legal_review_required: ["The Terms or the notices changed, so the server will not send anything until you review them.",
      "Review and accept them on the Auto-Pigeon site, then press Try again."],
    map_access_denied: ["Your account may not read this map.", "Ask the map's owner to share it with you."],
    refused: ["The Auto-Pigeon server refused this download.", "Technical details show what it answered."],
    server_unavailable: ["The Auto-Pigeon server is temporarily unavailable, so the download could not finish.",
      "Try again in a moment. The same revision is used, and nothing was compiled."],
    revision_changed: ["The map was saved again after this revision was chosen.", "Choose the revision again, then build."],
    download_damaged: ["What was downloaded did not match what the server declared, so nothing was compiled.",
      "Press Try again to download it again. If it happens again, report it."],
  };

  function serverFailure(id, body) {
    const words = FAILURE_WORDS[body.code];
    const box = $(id);
    if (!words) {
      setMessage(id, body.error, "error");
      return;
    }
    box.className = "message error";
    box.replaceChildren(document.createTextNode(t(words[0]) + " " + t(words[1])), technicalDetails(body.error));
    announce(t(words[0]));
  }
  window.AUCOM.failureWords = (run) => {
    const words = FAILURE_WORDS[run?.failure_code];
    return words ? { summary: t(words[0]), remedy: t(words[1]) } : null;
  };

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
    if (ok) renderCurrent(body.items || []);
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

  // --- the current run's card -----------------------------------------------------
  //
  // The run this window started, or a build somebody started by hand that is
  // running now (another tab, an earlier visit): one card, with the identities
  // pinned in the run's own record — never the form's, which is a draft for the
  // NEXT build — and one way to its progress, the Activity drawer. An automatic
  // build is shown by Auto-build, under the build profile.
  let currentCardKey = "";
  // A run this window saw running without starting it (another tab): followed
  // to its end, so its result is said here too rather than the card vanishing.
  let observed = "";
  function renderCurrent(runs) {
    const run = runs.find((r) => r.id === watching) || runs.find((r) => r.id === observed) ||
      runs.find((r) => r.active && r.trigger !== "auto_build");
    if (run && run.active && run.id !== watching) observed = run.id;
    const card = $("play-current");
    if (!run) {
      card.hidden = true;
      currentCardKey = "";
      return;
    }
    const stage = run.active ? (run.stages || []).at(-1)?.title || run.title : "";
    const key = JSON.stringify([run.id, run.state, stage, run.failure_code, run.error_summary, run.can_retry,
      nameOfPipeline(run.pipeline), nameOfEngine(run.engine)]);
    card.hidden = false;
    // Redrawn only when the run moved on, so a focused button is not replaced
    // under the keyboard, and the polite status line speaks once per stage.
    if (key === currentCardKey) return;
    currentCardKey = key;
    const name = run.map || run.asset_id;
    $("play-current-title").textContent = run.active ? t("Building {map}", { map: name })
      : run.state === "succeeded" ? (run.build_only ? t("{map} is built", { map: name }) : t("{map} is built and started", { map: name }))
        : run.state === "cancelled" ? t("{map}: cancelled", { map: name })
          : t("{map}: the build stopped", { map: name });
    $("play-current-detail").textContent = [t("revision {n}", { n: run.revision }), nameOfPipeline(run.pipeline),
      run.build_only ? "" : nameOfEngine(run.engine)].filter(Boolean).join(" · ");
    if (run.active) setMessage("play-current-status", t("Now: {stage}", { stage }), "busy");
    else if (run.state === "succeeded") {
      setMessage("play-current-status", run.build_only ? t("Built. Nothing was started.") : t("The game was started."), "ok");
    } else if (run.state === "cancelled") setMessage("play-current-status", t("You cancelled it."), "");
    else {
      setMessage("play-current-status", `${run.failed_at_title || t("It stopped")}: ${failureSummary(run)} ${failureRemedy(run)}`.trim(), "error");
    }
    // One primary per state: the recovery when it failed, else the way to
    // its progress.
    const actions = $("play-current-actions");
    actions.replaceChildren();
    const failed = !run.active && run.state !== "succeeded" && run.state !== "cancelled";
    if (failed && run.can_retry) {
      const retry = el("button", { text: t("Try again"), className: "primary", attrs: { type: "button" } });
      retry.addEventListener("click", () => window.AUCOM.withBusy(retry, () => retryRun(run.id)));
      actions.append(retry);
    }
    const progress = el("button", { text: t("View progress"), className: failed && run.can_retry ? "secondary" : "primary",
      attrs: { type: "button", "aria-controls": "activity" } });
    progress.addEventListener("click", openActivity);
    actions.append(progress);
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
        text: `${run.failed_at_title || "It stopped"}: ${failureSummary(run)}`,
      }));
    }
    const remedy = failureRemedy(run);
    if (remedy) children.push(el("p", { className: "muted", text: remedy }));
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

  // A run's failure in this page's language when the Companion classified it
  // (a download refused or interrupted), else the server's own sentence.
  function failureSummary(run) {
    return window.AUCOM.failureWords(run)?.summary || run.error_summary || run.error || "";
  }

  function failureRemedy(run) {
    return window.AUCOM.failureWords(run)?.remedy || run.remedy || "";
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
    // A Quake II run's package, where it was installed, and the engine's word.
    if (run.package) {
      rows.push(...line(t("Package"), `${run.package.archive} · ${t("{n} member(s)", { n: run.package.members })} · sha256 ${run.package.sha256}` +
        (run.package.reused ? " · " + t("identical to one already made") : "") +
        (run.package.complete ? "" : " · " + t("does not carry: {files}", { files: (run.package.not_carried || []).join(", ") }))));
    }
    if (run.installation) rows.push(...line(t("Installed into"), `${run.installation.mod_dir} (fs_game ${run.installation.fs_game})`));
    if (run.launch) {
      rows.push(...line("Command", [run.launch.executable, ...(run.launch.args || [])].join(" ")));
      if (run.launch.map_load) {
        rows.push(...line(t("The engine's word"), `${run.launch.map_load}${run.launch.loaded_from ? " — " + run.launch.loaded_from : ""}` +
          (run.launch.message ? ". " + run.launch.message : "")));
      }
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
      // A Quake II run installs a package whole or not at all (Q2_020): a
      // stop removes nothing, and says so rather than promising a removal.
      body: run.packaged
        ? t("The compiler or the engine is stopped. A Quake II package is installed whole or not at all, so " +
          "nothing half-installed is left; one already installed stays under Run, where it can be removed. " +
          "Everything already downloaded stays in the cache.")
        : "The compiler is stopped and anything already installed is removed, so the game folder " +
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

  // Saved map or a file on this computer: one form, two sources. A file is
  // built by build.js — its inputs, build profile list, check and Build are
  // that file's — and shown in the same blocks; Run after build is not
  // offered for it, and its finished build offers Play.
  function setSource(source, { remembered = true } = {}) {
    state.source = source === "local" ? "local" : "saved";
    const local = state.source === "local";
    $("play-source-saved").checked = !local;
    $("play-source-local").checked = local;
    for (const id of ["play-saved-map", "play-pipeline-field", "play-stages", "play-strict-field",
      "play-final-summary", "play-review-attention", "play-review-message", "play-review-details", "play-start"]) {
      $(id).hidden = local;
    }
    for (const id of ["play-local-map", "build-pipeline-field", "build-stages", "build-strict-field",
      "build-message", "build-review-details", "build-start"]) {
      $(id).hidden = !local;
    }
    $("play-signed-out").hidden = local || Boolean(window.AUCOM.status?.authenticated);
    $("play-run-after").disabled = local;
    $("play-run-after").checked = !local && state.runAfter;
    $("play-run-after-note").hidden = !local;
    $("play-run-after-note").textContent = local ? t("For a file on this computer, play the build when it finishes.") : "";
    $("play-run-fields").hidden = local || !state.runAfter;
    showAutobuild();
    if (remembered) remember();
    const hash = local ? "#play/local" : "#play";
    if (!$("area-play").hidden && window.location.hash !== hash) {
      window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search + hash);
    }
    if (local) window.AUCOM.areas.build?.refresh?.();
    else renderFinalSummary();
  }
  for (const radio of [$("play-source-saved"), $("play-source-local")]) {
    radio.addEventListener("change", () => { if (radio.checked) setSource(radio.value); });
  }
  $("play-run-after").addEventListener("change", (event) => {
    state.runAfter = event.target.checked;
    $("play-run-fields").hidden = !state.runAfter;
    invalidate(state.runAfter ? "The game starts after the build now, so the review is worked out again."
      : "Nothing starts after the build now, so the review is worked out again.");
    renderListing();
  });

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
    // A Quake II package's folder is named after the map.
    if (packaged()) renderActions();
  });
  // Why an engine was just cleared is read with the list it was cleared from,
  // and the chosen build profile's state with its list.
  $("play-engine").setAttribute("aria-describedby", "play-engine-message");
  $("play-pipeline").setAttribute("aria-describedby", "play-pipeline-state");
  $("play-pipeline").addEventListener("change", (event) => {
    state.pipeline = event.target.value;
    invalidate("You changed the build profile, so what it would run has to be worked out again.");
    renderStages();
    // Which engines fit depends on the family (a Quake II build needs a
    // Quake II engine), so the engine list is drawn again.
    renderEngines();
    pipelineChanged();
  });
  $("play-strict").addEventListener("change", (event) => {
    state.strict = event.target.checked;
    invalidate("You changed how strict the build is.");
  });
  $("play-engine").addEventListener("change", (event) => {
    state.engine = event.target.value;
    invalidate("You changed the engine, so the command it would run has to be worked out again.");
    renderActions();
  });
  $("play-action").addEventListener("change", (event) => {
    state.action = event.target.value;
    $("play-action-note").textContent = ACTIONS[state.action] ? t(ACTIONS[state.action].text) : "";
    invalidate("You changed what the engine does.");
    renderListing();
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
  // withBusy puts the button back as it found it; what it may do next is
  // decided again from the state the press left.
  $("play-start").addEventListener("click", (event) =>
    window.AUCOM.withBusy(event.currentTarget, primary).then(renderFinalSummary));
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
  // Settles when the lists the current refresh asked for have arrived and the
  // remembered map and revision are chosen again. See checkOwnWads.
  let ready = Promise.resolve();
  window.AUCOM.areas.play = {
    // `argument` is the view a route asked for (app.js aliases): "local" for a
    // file on this computer (the old `#build`), "autobuild" for the automatic
    // build checkbox, "saved" for a saved map.
    async refresh(argument) {
      if (!restored) {
        restored = true;
        restore();
      }
      const signedIn = Boolean(window.AUCOM.status?.authenticated);
      if (argument === "local") state.source = "local";
      else if (argument === "saved" || argument === "autobuild") state.source = "saved";
      else if (!signedIn && !new URLSearchParams(window.location.search).get("src")) state.source = "local";
      setSource(state.source);
      // Coming back to the form is not a confirmation of it: the review is
      // worked out again for the lists as they are now, before anything can
      // start (NEW_310A: a reload must never land on a button that would start
      // choices nobody has just seen).
      state.plan = null;
      state.planKey = "";
      $("play-review").replaceChildren();
      $("play-review-attention").replaceChildren();
      let settle;
      ready = new Promise((resolve) => { settle = resolve; });
      try {
        await Promise.all([loadPipelines(), loadEngines(), signedIn ? loadMaps() : null]);
        if (signedIn && state.map) await loadRevisions(state.map.asset_id, { keep: true });
      } finally {
        settle();
      }
      showAutobuild();
      renderFinalSummary();
      if (argument === "autobuild") {
        const box = $("play-autobuild-on");
        (box && !$("play-autobuild").hidden ? box : $("play-map")).focus();
      }
      poll();
    },
    // build.js reads and sets the build profile a file is built with, so the
    // choice carries across when somebody switches source.
    pipeline: () => state.pipeline,
  };

  // Activity is not an area: it belongs to the window, and a run started here
  // is still running when somebody is looking at Profiles. So the poll starts
  // as soon as the page does.
  poll();
})();
