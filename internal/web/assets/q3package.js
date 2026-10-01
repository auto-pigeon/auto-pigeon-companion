// Package → Install → Run, for a finished Quake III build.
//
// Three steps on one panel under the build's result, in the order a person
// does them: say what may be redistributed and read what the archive would
// hold; put it beside a game; start the engine and wait for the ENGINE to say
// the map loaded.
//
// Nothing is decided in this file. The plan, the refusals and their classes are
// the server's (internal/q3pack, internal/q3install, internal/q3run), and the
// same three are what `companion package map` drives — so what this panel shows
// and what the terminal prints cannot differ. What is here is the one thing the
// server cannot hold: the answers a person is in the middle of giving.
//
// The page never sends a folder. An archive is named by its digest, a game
// directory by its name, an engine by its profile id; where the game is comes
// from the engine's own setup.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, bytes, t } = window.AUCOM;

  // state is everything about the build this panel is showing. A new build
  // replaces it whole: answers given for one build's archives are not answers
  // for another's.
  let state = null;
  let poller = null;

  const BASIS = [
    ["", "No answer yet — nothing from it is packaged"],
    ["own_work", "It is my own work"],
    ["licensed", "I hold a licence that lets me redistribute it"],
    ["not_redistributable", "It is not mine to redistribute"],
  ];

  const DISPOSITION = {
    build_output: "this build's map",
    user_authored: "packaged — your own work",
    licensed: "packaged — licensed",
    base_game: "the base game supplies it; never packaged",
    third_party_unresolved: "NOT packaged — no grant",
    blocked: "NOT packaged — blocked",
    missing: "MISSING — nothing has it",
    compile_only: "only the compiler reads it; not needed in the package",
  };

  function dispositionBadge(disposition) {
    const kind = { missing: "failed", blocked: "failed", third_party_unresolved: "warning",
      user_authored: "ok", licensed: "ok", build_output: "ok" }[disposition] || "skipped";
    return badge(t(DISPOSITION[disposition] || disposition), kind);
  }

  function needs(compile, runtime) {
    if (compile && runtime) return t("compiler + engine");
    if (compile) return t("compiler only");
    if (runtime) return t("engine only");
    return "";
  }

  function short(digest) {
    return String(digest || "").replace(/^sha256:/, "").slice(0, 12);
  }

  // show is called by the Build page whenever it renders a build. It does
  // nothing for a build it is already showing, so a poll does not wipe the
  // answers somebody is giving.
  function show(manifest, live) {
    const panel = $("q3pkg-panel");
    if (!panel) return;
    const fits = manifest && !live && manifest.engine_family === "quake3" && manifest.state === "succeeded" &&
      (manifest.outputs || []).some((output) => output.name === "bsp" && output.path && !output.missing);
    if (!fits) {
      panel.hidden = true;
      if (!manifest || manifest.build_id !== state?.build) stopPolling();
      return;
    }
    panel.hidden = false;
    if (state?.build === manifest.build_id) return;
    stopPolling();
    state = { build: manifest.build_id, grants: {}, include: "", plan: null, held: "", acceptable: false,
      pkg: null, engines: [], installation: null, run: null };
    for (const id of ["q3pkg-create-message", "q3pkg-install-message", "q3pkg-run-message"]) setMessage(id, "");
    $("q3pkg-include").value = "";
    $("q3pkg-accept").checked = false;
    $("q3pkg-reason").value = "";
    $("q3pkg-created").replaceChildren();
    $("q3pkg-sources").replaceChildren();
    $("q3pkg-install-out").replaceChildren();
    $("q3pkg-run-out").replaceChildren();
    refreshPlan();
    restore();
  }

  function grantsBody() {
    const grants = [];
    for (const [id, answer] of Object.entries(state.grants)) {
      if (!answer.basis) continue;
      const grant = { basis: answer.basis };
      if (answer.basis === "licensed") grant.licence = answer.licence || "";
      if (id === "loose") grant.loose = true;
      else grant.archive = id.replace(/^archive:/, "");
      grants.push(grant);
    }
    return grants;
  }

  function packageBody() {
    const include = $("q3pkg-include").value.split(/[\s,]+/).filter(Boolean);
    return { build: state.build, grants: grantsBody(), include };
  }

  // A licensed answer with no licence named is not sent as one: the server
  // refuses it, rightly, and the place to say so is beside the empty field.
  function unnamedLicence() {
    return Object.values(state.grants).some((answer) => answer.basis === "licensed" && !String(answer.licence || "").trim());
  }

  async function refreshPlan() {
    const build = state.build;
    if (unnamedLicence()) {
      setMessage("q3pkg-plan-message", t("Name the licence you hold it under — an SPDX identifier such as CC-BY-4.0, or the licence's own title."), "warning");
      return;
    }
    busy("q3pkg-plan-message", t("Reading what the compiled map needs…"));
    // Answers can change faster than a plan comes back (an arrow key held in
    // the dropdown). Only the newest question's answer is shown.
    const asked = (state.planSeq = (state.planSeq || 0) + 1);
    const { ok, body } = await api("/api/v1/q3/packages/preview", { method: "POST", body: packageBody() });
    if (state?.build !== build || state.planSeq !== asked) return;
    if (!ok) {
      state.plan = null;
      $("q3pkg-plan").replaceChildren();
      setMessage("q3pkg-plan-message", body.error || t("The package could not be planned."), "error");
      $("q3pkg-create").disabled = true;
      return;
    }
    state.plan = body.plan;
    state.held = body.held || "";
    state.acceptable = Boolean(body.acceptable);
    renderPlan();
  }

  // renderSources draws the answers block — and draws it ONCE per set of
  // sources. Every answer refreshes the plan, and a refresh that rebuilt the
  // control somebody is in the middle of using took the focus out from under
  // them: with the keyboard, the first arrow key in the dropdown was also the
  // last. So the controls stay, and only the facts beside them are updated.
  function renderSources(plan) {
    const out = $("q3pkg-sources");
    const key = plan.sources.map((source) => source.id).join("|");
    if (state.sourcesKey === key && out.childElementCount) {
      for (const source of plan.sources) {
        const facts = out.querySelector(`[data-source-facts="${CSS.escape(source.id)}"]`);
        if (facts) facts.textContent = sourceFacts(source);
      }
      return;
    }
    state.sourcesKey = key;
    out.replaceChildren();
    out.append(el("h5", { text: t("Where your content came from — and whether it may be redistributed") }));
    if (!plan.sources.length) {
      out.append(el("p", { className: "muted", text: t("This build read no content of yours, so the archive holds the map and nothing else.") }));
    }
    for (const source of plan.sources) {
      const answer = state.grants[source.id] || (state.grants[source.id] = { basis: "", licence: "" });
      const row = el("div", { className: "q3pkg-source" });
      const title = source.kind === "archive" ? `${source.game}/${source.name}` : t("Loose files in your content folder");
      row.append(el("div", { className: "q3pkg-source__name", text: title }));
      row.append(el("div", { className: "muted", text: sourceFacts(source), attrs: { "data-source-facts": source.id } }));
      for (const hint of source.hints || []) row.append(el("div", { className: "muted", text: hint }));

      const selectId = "q3pkg-basis-" + source.id.replace(/[^a-z0-9]/gi, "-");
      const select = el("select", { attrs: { id: selectId } });
      for (const [value, label] of BASIS) {
        const option = el("option", { text: t(label), attrs: { value } });
        if (value === answer.basis) option.selected = true;
        select.append(option);
      }
      const licence = el("input", { attrs: { type: "text", id: selectId + "-licence", placeholder: "CC0-1.0", value: answer.licence || "" } });
      const licenceField = el("div", { className: "field", children: [
        el("label", { text: t("Licence"), attrs: { for: selectId + "-licence" } }), licence ] });
      licenceField.hidden = answer.basis !== "licensed";
      select.addEventListener("change", () => {
        answer.basis = select.value;
        licenceField.hidden = answer.basis !== "licensed";
        refreshPlan();
      });
      licence.addEventListener("change", () => {
        answer.licence = licence.value.trim();
        refreshPlan();
      });
      row.append(el("div", { className: "row", children: [
        el("div", { className: "field grow", children: [
          el("label", { text: t("May it be redistributed?"), attrs: { for: selectId } }), select ] }),
        licenceField ] }));
      out.append(row);
    }
  }

  function sourceFacts(source) {
    const facts = [t("{n} file(s) the engine needs", { n: source.runtime_files })];
    if (source.sha256) facts.push("sha256 " + short(source.sha256) + "…");
    if (source.origin) facts.push(t("a package the saved map is bound to"));
    return facts.join(" · ");
  }

  function renderPlan() {
    const plan = state.plan;
    const out = $("q3pkg-plan");
    out.replaceChildren();

    out.append(el("p", { className: "muted", text: t("The archive: {game}/{name}", { game: plan.game_dir, name: plan.archive_name }) }));

    renderSources(plan);

    // Members, in archive order.
    const members = el("details", { className: "q3pkg-block" });
    members.open = true;
    members.append(el("summary", { text: t("What the archive would hold, in order ({n} file(s), {size})", { n: plan.members.length, size: bytes(plan.total_bytes) }) }));
    const list = el("ol", { className: "q3pkg-members" });
    for (const member of plan.members) {
      const line = el("li");
      line.append(el("code", { text: member.path }));
      line.append(el("span", { className: "muted", text: ` ${bytes(member.size)} · sha256 ${short(member.sha256)}… · ` }));
      line.append(dispositionBadge(member.disposition));
      const why = [member.from];
      if (member.licence) why.push(t("licence: {licence}", { licence: member.licence }));
      line.append(el("div", { className: "muted", text: why.filter(Boolean).join(" · ") }));
      list.append(line);
    }
    members.append(list);
    out.append(members);

    // Dependencies: what the map names and what became of each.
    const deps = el("details", { className: "q3pkg-block" });
    deps.open = plan.problems?.length > 0;
    deps.append(el("summary", { text: t("What the map needs ({n})", { n: plan.dependencies.length }) }));
    const table = el("table", { className: "q3pkg-deps" });
    table.append(el("thead", { children: [ el("tr", { children: [
      el("th", { text: t("The map names") }), el("th", { text: t("Read by") }), el("th", { text: t("What became of it") }) ] }) ] }));
    const rows = el("tbody");
    for (const dependency of plan.dependencies) {
      const cell = el("td");
      cell.append(el("code", { text: dependency.name }));
      cell.append(el("div", { className: "muted", text: `${dependency.kind} · ${dependency.from}` }));
      const files = el("ul", { className: "q3pkg-files" });
      for (const file of dependency.files || []) {
        const item = el("li");
        item.append(el("code", { text: file.path }));
        item.append(el("span", { className: "muted", text: ` ${file.role} · ${needs(file.required_at_compile, file.required_at_runtime)} · ${t(DISPOSITION[file.disposition] || file.disposition)}` }));
        if (file.reason) item.append(el("div", { className: "muted", text: file.reason }));
        files.append(item);
      }
      cell.append(files);
      rows.append(el("tr", { children: [ cell,
        el("td", { text: needs(dependency.required_at_compile, dependency.required_at_runtime) }),
        el("td", { children: [dispositionBadge(dependency.disposition)] }) ] }));
    }
    table.append(rows);
    deps.append(table);
    out.append(deps);

    if (plan.limits?.length) {
      const limits = el("details", { className: "q3pkg-block" });
      limits.append(el("summary", { text: t("What this review did not look at") }));
      const items = el("ul");
      for (const limit of plan.limits) items.append(el("li", { className: "muted", text: limit }));
      limits.append(items);
      out.append(limits);
    }

    // Why it would not be written, when it would not.
    const problems = plan.problems || [];
    const acceptRow = $("q3pkg-accept-row");
    if (!problems.length) {
      setMessage("q3pkg-plan-message", t("Everything the engine needs is in the archive or in the base game."), "ok");
      acceptRow.hidden = true;
    } else {
      const fatal = problems.filter((problem) => !problem.acceptable);
      setMessage("q3pkg-plan-message",
        t("{n} thing(s) the engine needs would not be in this archive.", { n: problems.length }), "warning");
      const why = el("ul", { className: "q3pkg-problems" });
      for (const problem of problems) {
        const item = el("li");
        item.append(badge(problem.code, problem.acceptable ? "warning" : "failed"));
        item.append(el("span", { text: " " + problem.message }));
        why.append(item);
      }
      out.append(why);
      // Accepting is offered only for what a reason CAN accept.
      acceptRow.hidden = fatal.length > 0;
    }
    updateCreate();
  }

  function updateCreate() {
    const problems = state.plan?.problems || [];
    const accepting = $("q3pkg-accept").checked && $("q3pkg-reason").value.trim() !== "";
    const fatal = problems.some((problem) => !problem.acceptable);
    $("q3pkg-create").disabled = !state.plan || fatal || (problems.length > 0 && !accepting);
    $("q3pkg-reason-field").hidden = !$("q3pkg-accept").checked;
  }

  async function create(button) {
    await withBusy(button, async () => {
      busy("q3pkg-create-message", t("Writing the archive…"));
      const body = packageBody();
      if ($("q3pkg-accept").checked) body.accept_reason = $("q3pkg-reason").value.trim();
      const response = await api("/api/v1/q3/packages", { method: "POST", body });
      if (!response.ok) {
        const kind = response.body.class ? ` (${response.body.class})` : "";
        setMessage("q3pkg-create-message", (response.body.error || t("The package was not written.")) + kind, "error");
        record(t("Package not written"), response.body.error || "", "error");
        if (response.body.plan) {
          state.plan = response.body.plan;
          renderPlan();
        }
        return;
      }
      state.pkg = response.body.package;
      setMessage("q3pkg-create-message", response.body.already_existed
        ? t("This exact package was already written; it is the same bytes.") : t("The package was written."), "ok");
      record(t("Package written"), state.pkg.id, "ok");
      renderPackage();
      await refreshEngines();
    });
  }

  function renderPackage() {
    const out = $("q3pkg-created");
    out.replaceChildren();
    const pkg = state.pkg;
    if (!pkg) {
      $("q3pkg-step-install").hidden = true;
      $("q3pkg-step-run").hidden = true;
      return;
    }
    const facts = el("dl", { className: "q3pkg-facts" });
    const fact = (name, value) => {
      facts.append(el("dt", { text: name }));
      facts.append(el("dd", { children: [typeof value === "string" ? el("code", { text: value }) : value] }));
    };
    fact(t("Package"), pkg.id);
    fact(t("Archive"), pkg.archive.file);
    fact("SHA-256", String(pkg.archive.sha256).replace(/^sha256:/, ""));
    fact(t("Size"), `${bytes(pkg.archive.size)} — ${pkg.archive.entries} ${t("file(s)")}`);
    fact(t("On this computer"), pkg.archive_path || "");
    if (!pkg.complete) {
      fact(t("Complete"), badge(t("INCOMPLETE — {n} file(s) the engine needs are not in it", { n: (pkg.plan.not_carried || []).length }), "warning"));
      if (pkg.acceptance?.reason) fact(t("Accepted because"), pkg.acceptance.reason);
    } else {
      fact(t("Complete"), badge(t("complete"), "ok"));
    }
    out.append(facts);
    $("q3pkg-step-install").hidden = false;
  }

  // --- install ---------------------------------------------------------------

  async function refreshEngines() {
    const { ok, body } = await api("/api/v1/q3/engines");
    const select = $("q3pkg-engine");
    const kept = select.value;
    select.replaceChildren();
    state.engines = ok ? body.engines || [] : [];
    if (!ok) {
      setMessage("q3pkg-install-message", body.error || t("The engines could not be listed."), "error");
      return;
    }
    for (const engine of state.engines) {
      const label = engine.set_up ? engine.name : t("{name} — not set up", { name: engine.name });
      select.append(el("option", { text: label, attrs: { value: engine.id } }));
    }
    const ready = state.engines.find((engine) => engine.set_up);
    select.value = state.engines.some((engine) => engine.id === kept) ? kept : (ready || state.engines[0] || {}).id || "";
    engineChanged();
  }

  function currentEngine() {
    return state.engines.find((engine) => engine.id === $("q3pkg-engine").value);
  }

  function engineChanged() {
    const engine = currentEngine();
    const note = $("q3pkg-engine-note");
    const actions = $("q3pkg-action");
    actions.replaceChildren();
    if (!engine) {
      note.textContent = t("No Quake III engine profile is installed.");
      $("q3pkg-install").disabled = true;
      return;
    }
    note.replaceChildren();
    if (engine.problem) {
      note.append(el("span", { className: "message error", text: engine.problem }), " ");
      note.append(el("a", { text: t("Set up this engine in Profiles"), attrs: { href: "#profiles/" + encodeURIComponent(engine.id) } }));
    } else {
      note.textContent = t("Its game folder: {folder}", { folder: engine.game_root });
    }
    $("q3pkg-install").disabled = !engine.game_root;
    for (const action of engine.actions || []) {
      const label = action.reports_map_load ? action.title : t("{title} — does not report a map load", { title: action.title });
      actions.append(el("option", { text: label, attrs: { value: action.id } }));
    }
    actionChanged();
  }

  function actionChanged() {
    const engine = currentEngine();
    const action = (engine?.actions || []).find((candidate) => candidate.id === $("q3pkg-action").value);
    const note = $("q3pkg-action-note");
    if (!action) {
      note.textContent = "";
      return;
    }
    const role = { client: t("Opens the game's own window on this computer."),
      listen_server: t("Opens the game and lets other people join this computer."),
      dedicated_server: t("Starts a server with no window. Other people can reach this computer on its port.") }[action.session_role] || "";
    note.textContent = action.problem ? action.problem : role;
  }

  function installBody() {
    return {
      package: state.pkg.id,
      engine: $("q3pkg-engine").value,
      into: $("q3pkg-into").value,
      mod: $("q3pkg-mod").value.trim(),
      base_game: $("q3pkg-basegame").value.trim(),
    };
  }

  async function install(button, previewOnly) {
    if (!state.pkg) return;
    await withBusy(button, async () => {
      busy("q3pkg-install-message", previewOnly ? t("Checking where it would go…") : t("Installing…"));
      const response = await api(previewOnly ? "/api/v1/q3/installs/preview" : "/api/v1/q3/installs",
        { method: "POST", body: installBody() });
      if (!response.ok) {
        const kind = response.body.class ? ` (${response.body.class})` : "";
        setMessage("q3pkg-install-message", (response.body.error || t("It could not be installed.")) + kind, "error");
        if (!previewOnly) record(t("Package not installed"), response.body.error || "", "error");
        return;
      }
      const installation = response.body.installation;
      renderInstallation(installation, previewOnly);
      if (previewOnly) {
        setMessage("q3pkg-install-message", t("This is where it would go. Nothing was written."), "ok");
        return;
      }
      state.installation = installation;
      setMessage("q3pkg-install-message", t("Installed."), "ok");
      record(t("Package installed"), installation.archive.path, "ok");
      $("q3pkg-step-run").hidden = false;
      $("q3pkg-uninstall").hidden = false;
    });
  }

  function renderInstallation(installation, previewOnly) {
    const out = $("q3pkg-install-out");
    out.replaceChildren();
    const facts = el("dl", { className: "q3pkg-facts" });
    const fact = (name, value) => {
      facts.append(el("dt", { text: name }));
      facts.append(el("dd", { children: [typeof value === "string" ? el("code", { text: value }) : value] }));
    };
    fact(previewOnly ? t("Would be written") : t("Written"), installation.archive.path);
    fact(t("Your game folder"), installation.kind === "managed"
      ? t("{folder} — NOT written; read through links", { folder: installation.game_root })
      : t("{folder} — one file added, nothing replaced", { folder: installation.game_root }));
    fact(t("The engine is given"), `fs_basepath ${installation.base_path} · fs_game ${installation.fs_game}`);
    const order = installation.load_order || {};
    if ((order.searched_before || []).length) fact(t("Searched before it"), order.searched_before.join(", "));
    out.append(facts);
    for (const shadow of order.shadowed || []) {
      out.append(el("p", { className: "message warning", text: t("{path} is also in {by}, which the engine reads first — the engine uses that one.", { path: shadow.path, by: shadow.by }) }));
    }
    for (const shadow of order.overrides || []) {
      out.append(el("p", { className: "muted", text: t("{path} in this package hides the one in {by}.", { path: shadow.path, by: shadow.by }) }));
    }
    for (const limit of order.limits || []) out.append(el("p", { className: "muted", text: t("Not looked at: {limit}", { limit }) }));
  }

  async function uninstall(button) {
    if (!state.installation) return;
    await withBusy(button, async () => {
      const response = await api("/api/v1/q3/installs/" + encodeURIComponent(state.installation.id), { method: "DELETE" });
      if (!response.ok) {
        setMessage("q3pkg-install-message", response.body.error || t("It could not be removed."), "error");
        return;
      }
      record(t("Package removed"), state.installation.archive.path, "ok");
      state.installation = null;
      state.run = null;
      stopPolling();
      $("q3pkg-install-out").replaceChildren();
      $("q3pkg-run-out").replaceChildren();
      $("q3pkg-step-run").hidden = true;
      $("q3pkg-uninstall").hidden = true;
      setMessage("q3pkg-install-message", t("Removed. Your game folder is as it was."), "ok");
      setMessage("q3pkg-run-message", "");
    });
  }

  // --- run -------------------------------------------------------------------

  async function run(button) {
    if (!state.installation) return;
    await withBusy(button, async () => {
      busy("q3pkg-run-message", t("Starting the engine…"));
      const options = {};
      const port = $("q3pkg-port").value.trim();
      if (port) options.port = port;
      const response = await api("/api/v1/q3/runs", { method: "POST", body: {
        installation: state.installation.id, engine: $("q3pkg-engine").value, action: $("q3pkg-action").value, options } });
      if (!response.ok) {
        const kind = response.body.class ? ` (${response.body.class})` : "";
        setMessage("q3pkg-run-message", (response.body.error || t("The engine could not be started.")) + kind, "error");
        record(t("Engine not started"), response.body.error || "", "error");
        return;
      }
      state.run = response.body;
      renderRun();
      pollRun();
    });
  }

  function stopPolling() {
    if (poller) window.clearTimeout(poller);
    poller = null;
  }

  function pollRun() {
    stopPolling();
    const build = state?.build;
    const tick = async () => {
      if (!state || state.build !== build || !state.run) return;
      const { ok, body } = await api("/api/v1/q3/runs/" + encodeURIComponent(state.run.id));
      if (!state || state.build !== build) return;
      if (ok) {
        const before = state.run.state;
        state.run = body;
        renderRun();
        if (before === "waiting" && body.state !== "waiting") recordRun(body);
        // Keep looking while there is something that can still change: the
        // wait, or an engine that is up.
        if (body.state !== "waiting" && !body.engine_running) return;
      }
      poller = window.setTimeout(tick, 1000);
    };
    poller = window.setTimeout(tick, 400);
  }

  function recordRun(run) {
    const result = run.result;
    if (!result) {
      record(t("Engine not started"), run.error || "", "error");
      return;
    }
    record(t("Map load: {verdict}", { verdict: result.map_load }), `${result.message} ${result.evidence || ""}`.trim(),
      result.map_load === "accepted" ? "ok" : "error");
  }

  function renderRun() {
    const out = $("q3pkg-run-out");
    out.replaceChildren();
    const run = state.run;
    if (!run) return;
    const stop = $("q3pkg-stop");
    stop.hidden = !(run.state === "waiting" || run.engine_running);
    $("q3pkg-run").disabled = run.state === "waiting" || Boolean(run.engine_running);

    if (run.state === "waiting") {
      busy("q3pkg-run-message", t("The engine was started. Waiting for the engine itself to say the map loaded…"));
    } else if (run.state === "failed" && !run.result) {
      setMessage("q3pkg-run-message", (run.error || t("The engine could not be started.")) + (run.class ? ` (${run.class})` : ""), "error");
    }

    const facts = el("dl", { className: "q3pkg-facts" });
    const fact = (name, value) => {
      facts.append(el("dt", { text: name }));
      facts.append(el("dd", { children: [typeof value === "string" ? el("code", { text: value }) : value] }));
    };
    const result = run.result;
    if (result) {
      const verdict = { accepted: [t("LOADED — the engine said so"), "ok"],
        refused: [t("NOT LOADED"), "failed"],
        not_observed: [t("NOT OBSERVED — the engine did not say"), "warning"] }[result.map_load] || [result.map_load, "warning"];
      fact(t("Map load"), badge(verdict[0], verdict[1]));
      setMessage("q3pkg-run-message", result.message, result.map_load === "accepted" ? "ok" : result.map_load === "refused" ? "error" : "warning");
      if (result.evidence) fact(t("The engine said"), result.evidence);
      if (result.failure_class) fact(t("Kind of failure"), result.failure_class);
      if (result.hint) out.append(el("p", { className: result.map_load === "accepted" ? "message warning" : "muted", text: result.hint }));
    }
    const pid = run.engine_pid || result?.pid;
    fact(t("Engine process"), pid ? `pid ${pid}` : t("none yet"));
    if (run.engine_state) fact(t("Engine now"), badge(String(run.engine_state)));
    if (run.job_id) {
      fact(t("Its job and whole output"), el("a", { text: run.job_id, attrs: { href: "#jobs/" + encodeURIComponent(run.job_id) } }));
    }
    if (result) {
      fact("fs_game", result.fs_game);
      fact("fs_basepath", result.base_path);
    }
    out.prepend(facts);
    if (result?.executable) {
      const details = el("details", { className: "stage-findings" });
      details.append(el("summary", { text: t("Technical details") }));
      details.append(el("pre", { className: "output", text: [result.executable, ...(result.args || [])].join(" ") }));
      out.append(details);
    }
  }

  async function stopRun(button) {
    if (!state?.run) return;
    await withBusy(button, async () => {
      const response = await api(`/api/v1/q3/runs/${encodeURIComponent(state.run.id)}/cancel`, { method: "POST" });
      if (!response.ok) {
        setMessage("q3pkg-run-message", response.body.error || t("The engine could not be stopped."), "error");
        return;
      }
      record(t("Engine stopped"), state.run.job_id || "", "ok");
      pollRun();
    });
  }

  // restore puts back what a reload lost: the package of this build that is
  // INSTALLED — the one an engine may be running out of — and otherwise the
  // newest one, with its installation and the run that may still be up.
  //
  // Not simply "the newest package": a build can have several (one written
  // with the textures, one without), and a page that came back showing the
  // newest while an engine ran out of another showed no engine at all.
  async function restore() {
    const build = state.build;
    const packages = await api("/api/v1/q3/packages?build=" + encodeURIComponent(build));
    if (state?.build !== build || !packages.ok || !(packages.body.packages || []).length || state.pkg) return;
    const mine = packages.body.packages;
    const installs = await api("/api/v1/q3/installs");
    if (state?.build !== build) return;
    const installed = installs.ok
      ? (installs.body.installations || []).find((candidate) => mine.some((pkg) => pkg.id === candidate.package_id))
      : null;
    state.pkg = installed ? mine.find((pkg) => pkg.id === installed.package_id) : mine[0];
    // The answers the package was written under are the answers to show.
    for (const grant of state.pkg.grants || []) {
      const id = grant.loose ? "loose" : grant.archive ? "archive:" + grant.archive : "";
      if (id) state.grants[id] = { basis: grant.basis, licence: grant.licence || "" };
    }
    // Redrawn with those answers selected.
    state.sourcesKey = "";
    renderPackage();
    setMessage("q3pkg-create-message", t("A package of this build is already on this computer."), "ok");
    await refreshEngines();
    if (state?.build !== build) return;
    refreshPlan();
    if (!installed) return;
    state.installation = installed;
    $("q3pkg-into").value = installed.kind;
    if (installed.base_game !== "baseq3") $("q3pkg-basegame").value = installed.base_game;
    renderInstallation(installed, false);
    $("q3pkg-step-run").hidden = false;
    $("q3pkg-uninstall").hidden = false;
    const latest = await api(`/api/v1/q3/installs/${encodeURIComponent(installed.id)}/runs`);
    if (state?.build !== build || !latest.ok || !latest.body.run) return;
    state.run = latest.body.run;
    // The engine and the action the run used are the ones to show selected.
    if (state.engines.some((engine) => engine.id === state.run.engine)) {
      $("q3pkg-engine").value = state.run.engine;
      engineChanged();
      $("q3pkg-action").value = state.run.action;
      actionChanged();
    }
    renderRun();
    if (state.run.state === "waiting" || state.run.engine_running) pollRun();
  }

  function wire() {
    if (!$("q3pkg-panel")) return;
    $("q3pkg-include").addEventListener("change", refreshPlan);
    $("q3pkg-accept").addEventListener("change", updateCreate);
    $("q3pkg-reason").addEventListener("input", updateCreate);
    $("q3pkg-create").addEventListener("click", (event) => create(event.currentTarget));
    $("q3pkg-engine").addEventListener("change", engineChanged);
    $("q3pkg-action").addEventListener("change", actionChanged);
    $("q3pkg-install-preview").addEventListener("click", (event) => install(event.currentTarget, true));
    $("q3pkg-install").addEventListener("click", (event) => install(event.currentTarget, false));
    $("q3pkg-uninstall").addEventListener("click", (event) => uninstall(event.currentTarget));
    $("q3pkg-run").addEventListener("click", (event) => run(event.currentTarget));
    $("q3pkg-stop").addEventListener("click", (event) => stopRun(event.currentTarget));
  }

  wire();
  window.AUCOM.q3package = { show };
})();
