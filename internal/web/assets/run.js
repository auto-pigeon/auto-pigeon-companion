// The Run area: an engine on this machine, set up, and started.
//
// Two halves, drawn apart on purpose. The profile is portable and contains no
// path belonging to anybody; the binding is nothing but this machine's paths and
// what this user approved. Which half a value came from is the difference
// between something safe to hand to somebody else and something that is not.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, when,
    maturityBadge, maturityNote, openCompatibilityReport, t } = window.AUCOM;

  let engines = [];
  let engineRefresh = 0;

  function current() {
    return engines.find((engine) => engine.id === $("run-engine").value);
  }

  async function refreshEngines() {
    const select = $("run-engine");
    const sequence = ++engineRefresh;
    const { ok, body } = await api("/api/v1/engines");
    if (sequence !== engineRefresh) return;
    const previous = select.value;
    if (!ok) {
      engines = [];
      select.replaceChildren();
      renderEngine();
      setMessage("run-message", body.error || "could not read the engine profiles", "error");
      return;
    }
    engines = body.items || [];
    window.AUCOM.executionChoices(select, engines, previous, (engine) => engine.ready === true,
      (engine) => `${engine.name}${engine.maturity?.work_in_progress ? ` [${engine.maturity.badge}]` : ""} — ${engine.ready ? "ready" : "needs setup: " + (engine.readiness?.problems || []).map((p) => p.summary).join(" ")}`,
      "Choose a ready engine…", true);
    renderEngine();
  }

  function renderEngine() {
    const engine = current();
    const detail = $("run-engine-detail");
    detail.replaceChildren();
    if (!engine) {
      $("run-action").replaceChildren();
      $("run-preview").disabled = true;
      $("run-launch").disabled = true;
      detail.append(el("p", { className: "muted", text: "Choose a ready engine. Set up unavailable engines in Profiles, then Refresh." }));
      const setup = el("button", { text: "Profiles / setup", attrs: { type: "button" } });
      setup.addEventListener("click", () => window.AUCOM.showArea("profiles"));
      detail.append(setup);
      return;
    }

    // The engine at a glance: its name and trust, one sentence, and whether it
    // can start. Everything else a person rarely needs — where it looks for
    // content, what the profile author checked, what this machine recorded —
    // is behind a disclosure, so Start is not below a wall of facts.
    renderActions(engine);
    const perAction = engine.action_problems || {};
    const ready = window.AUCOM.actionReady(engine, $("run-action").value);
    const card = el("div", { className: "engine-card" });
    const head = el("div", { className: "engine-card__head" });
    head.append(el("strong", { className: "engine-card__name", text: engine.name }));
    head.append(el("span", { className: "muted", text: engine.version }));
    head.append(badge(engine.trust));
    const wip = maturityBadge(engine.maturity);
    if (wip) head.append(wip);
    head.append(el("span", {
      className: "engine-card__state " + (ready ? "ok" : "pending"),
      text: ready ? t("Ready to start") : t("Needs setup"),
    }));
    // Setup, right of the state: the call to action while something is
    // missing, an ordinary button once it is ready (operator, 2026-09-23).
    const setupButton = el("button", {
      text: t("Setup"),
      attrs: { type: "button", class: ready ? "secondary engine-card__setup" : "primary engine-card__setup" },
    });
    // Setup happens in Profiles and nowhere else (operator, 2026-09-23): the
    // engine's own page, with Profiles lit in the side menu.
    setupButton.addEventListener("click", () => window.AUCOM.showArea("profiles/" + encodeURIComponent(engine.id)));
    head.append(setupButton);
    card.append(head);
    if (engine.summary) card.append(el("p", { className: "engine-card__summary", text: engine.summary }));
    // Before the binding fields and before the Play button: this is where a
    // Quake II game is selected and started.
    const note = maturityNote(engine.maturity, () =>
      openCompatibilityReport({
        family: engine.engine_family,
        operation: "run",
        about: `About ${engine.name}.`,
        profiles: [{ role: "engine", id: engine.id, version: engine.version }],
      })
    );
    if (note) card.append(note);

    const about = el("details", { className: "engine-more" });
    about.append(el("summary", { text: t("About this profile") }));
    if (engine.trust_description) about.append(el("p", { className: "muted", text: engine.trust_description }));
    about.append(el("p", {
      className: "muted",
      text: [
        "runtime " + engine.runtime,
        "engine " + engine.engine_version,
        engine.last_qualified ? "author last checked " + engine.last_qualified : "",
      ].filter(Boolean).join(" · "),
    }));
    const support = engine.platform_support || {};
    about.append(el("p", {
      className: "muted",
      text: `On ${support.platform?.os}/${support.platform?.arch} this profile is ${support.status}` +
        (support.note ? ": " + support.note : "."),
    }));
    if ((engine.content_layouts || []).length) {
      about.append(el("h4", { text: "Where this engine looks for content" }));
      const layouts = el("ul", { className: "plain" });
      for (const layout of engine.content_layouts) {
        layouts.append(el("li", {
          // A layout with no fixed path (a mod directory, named when it is
          // played) is the root itself plus "a directory you name" — never
          // the word "undefined" (NEW_244D).
          text: `${layout.title || layout.id}: ${layout.root}/${layout.path || "<a directory you name>"} (${layout.kind})` +
            (layout.note ? " — " + layout.note : ""),
        }));
      }
      about.append(layouts);
    }
    card.append(about);

    // --- the local half
    const local = el("details", { className: "engine-more" });
    local.append(el("summary", { text: "What this machine has recorded" }));
    if (!engine.binding) {
      local.append(el("p", { className: "muted", text: "Nothing yet. Press Setup to set it up in Profiles." }));
      local.open = true;
    } else {
      const rows = el("dl", { className: "paths" });
      for (const [name, value] of Object.entries(engine.binding.executables || {})) {
        rows.append(el("dt", { text: name === "engine" ? t("Program") : name }));
        rows.append(el("dd", { text: value }));
      }
      for (const [role, value] of Object.entries(engine.binding.roots || {})) {
        rows.append(el("dt", { text: window.AUCOM.folderTitle(role) }));
        rows.append(el("dd", { text: value }));
      }
      rows.append(el("dt", { text: t("Approved") }));
      rows.append(el("dd", {
        text: engine.binding.granted ? t("yes, on {when}", { when: when(engine.binding.granted_at) }) : t("no"),
      }));
      local.append(rows);
    }
    card.append(local);
    detail.append(card);

    // --- what is stopping it
    const problems = el("div");
    const blocking = perAction[$("run-action").value] || [];
    if (blocking.length) {
      problems.append(el("h4", { text: "Before this can start" }));
      for (const problem of blocking) {
        problems.append(
          el("div", {
            className: "problem",
            children: [
              el("p", { text: pageSummary(problem, engine) }),
              el("p", { className: "fix", text: pageFix(problem) }),
            ],
          })
        );
      }
    }
    detail.append(problems);
  }

  // The engine preflight writes each remedy for the terminal
  // (`companion engine bind <profile id> …`, `companion toolchain show`). On
  // this page the remedy is a control right here, so that is what it names
  // (NEW_244D rehearsal: Run told a person to type commands naming profile
  // ids, and a stale setup's summary quoted two document digests).
  const PAGE_FIX = {
    missing_engine: "Press Setup and choose where the program is.",
    unbound_root: "Press Setup and choose the folder.",
    missing_root: "It was moved or removed. Press Setup and choose it again.",
    not_authorized: "Press Setup and approve it.",
    stale_binding: "It changed since it was approved. Press Setup and approve it again.",
    missing_game_data: "Press Setup and choose your own installed copy as the game directory — “Look for installed games” lists the likely places. The Companion never downloads or copies game data.",
    unsupported_platform: "Choose an engine whose profile supports this computer.",
  };

  function pageFix(problem) {
    return PAGE_FIX[problem.fault] || (/`companion /.test(problem.fix || "") ? "Press Setup to set it up in Profiles." : problem.fix);
  }

  function pageSummary(problem, engine) {
    if (problem.fault === "not_authorized") {
      // The preflight quotes the authorizer's own error, which names the
      // profile id and lists permission ids; the list is on this page in words.
      return `${engine.name} has not been approved on this machine yet.`;
    }
    if (problem.fault === "stale_binding") {
      return `The setup recorded for ${engine.name} was approved for a different version of its profile.`;
    }
    return problem.summary;
  }

  function renderActions(engine) {
    const select = $("run-action");
    const previous = select.value;
    window.AUCOM.executionChoices(select, engine.actions || [], previous,
      (action) => window.AUCOM.actionReady(engine, action.id),
      (action) => `${action.title || action.id}${window.AUCOM.actionReady(engine, action.id) ? "" : " — needs setup"}`,
      "Choose a ready action…");
    for (const option of select.options) option.dataset.role = engine.actions?.find((a) => a.id === option.value)?.session_role || "";
    if (!select.value) select.value = (engine.actions || []).find((a) => window.AUCOM.actionReady(engine, a.id))?.id || "";
    $("run-preview").disabled = !window.AUCOM.actionReady(engine, select.value);
    $("run-launch").disabled = $("run-preview").disabled;
    renderSessionNote();
  }

  // renderSessionNote says which of client, listen server and dedicated server
  // the chosen action is. An engine profile's action vocabulary is closed
  // exactly so this sentence can be true.
  function renderSessionNote() {
    const option = $("run-action").selectedOptions[0];
    const role = option?.dataset.role || "";
    const text = {
      client: "You play. Nobody else can join this.",
      listen: "You play, and other people can join your game.",
      dedicated: "A server with no local player. Nothing will open a window.",
    }[role];
    $("run-session-note").textContent = text || "";
    $("run-server-row").hidden = $("run-action").value !== "join_server";
  }

  // The runtime value names are the profile schema's, not this page's: an
  // engine profile writes {runtime.map_name} in its argv, and a form that
  // invented its own spelling would resolve to nothing.
  function launchBody() {
    const engine = current();
    if (!window.AUCOM.actionReady(engine, $("run-action").value)) return null;
    const runtime = {};
    const set = (name, value) => {
      if (value.trim()) runtime[name] = value.trim();
    };
    set("map_name", $("run-map").value);
    set("mod_name", $("run-mod").value);
    set("package_name", $("run-mod").value);
    set("server_host", $("run-server-host").value);
    set("server_port", $("run-server-port").value);
    return { profile: engine?.id || "", action: $("run-action").value, runtime };
  }

  async function previewLaunch(button) {
    if (!launchBody()) return;
    await withBusy(button, async () => {
      busy("run-message", "Resolving…");
      const { ok, body } = await api("/api/v1/jobs/preview", { method: "POST", body: launchBody() });
      const out = $("run-preview-out");
      out.hidden = false;
      out.replaceChildren();
      if (!ok) {
        out.hidden = true;
        setMessage("run-message", body.error || "the command could not be resolved", "error");
        return;
      }
      setMessage("run-message", "This is the exact command. Nothing has started.", "ok");
      out.append(el("pre", { className: "output", text: body.command?.shell || "(no command)" }));
      if (body.command?.argv) {
        const details = el("details");
        details.append(el("summary", { text: "argument by argument" }));
        const list = el("ol", { className: "mono" });
        for (const argument of body.command.argv) list.append(el("li", { text: argument }));
        details.append(list);
        out.append(details);
      }
    });
  }

  // --- a map from a build ------------------------------------------------------
  //
  // A finished build's level, staged where this engine looks for it before the
  // game starts: <game directory>/<mod>/maps/<map>.bsp. Playing what you just
  // built used to need a hand-made maps/ folder and `companion engine stage`
  // (NEW_244D, with the operator's own vkQuake).
  let playableBuilds = [];

  // mapNameFor is build.MapName: the source map's name, as `+map` accepts it.
  function mapNameFor(manifest) {
    const input = (manifest.inputs || []).find((item) => item.name === "source_map") || (manifest.inputs || [])[0];
    const base = String(input?.path || "").split(/[\\/]/).pop().replace(/\.[^.]*$/, "");
    const name = base.toLowerCase().replace(/[^a-z0-9_-]/g, "_").replace(/^_+|_+$/g, "").slice(0, 56);
    return name || "level";
  }

  async function refreshBuilds() {
    const select = $("run-build");
    const previous = select.value;
    const { ok, body } = await api("/api/v1/build/runs?limit=30");
    if (!ok) return;
    playableBuilds = (body.items || [])
      .map((item) => item.manifest)
      .filter((manifest) => manifest.state === "succeeded" && (manifest.outputs || []).some((output) => output.name === "bsp" && output.path && !output.missing));
    select.replaceChildren(el("option", { text: "None — type the map name below", attrs: { value: "" } }));
    for (const manifest of playableBuilds) {
      select.append(el("option", {
        text: `${manifest.label || manifest.pipeline?.name || "Untitled build"} — ${mapNameFor(manifest)} · ${when(manifest.started_at)}`,
        attrs: { value: manifest.build_id },
      }));
    }
    if (previous && playableBuilds.some((manifest) => manifest.build_id === previous)) select.value = previous;
    describeChosenBuild();
  }

  function describeChosenBuild() {
    const manifest = playableBuilds.find((item) => item.build_id === $("run-build").value);
    const note = $("run-build-note");
    if (!manifest) {
      note.textContent = "";
      return;
    }
    if (!$("run-mod").value.trim()) $("run-mod").value = "auto-pigeon";
    $("run-map").value = mapNameFor(manifest);
    note.textContent = `Start copies this build's level into your game directory as ${$("run-mod").value.trim()}/maps/${$("run-map").value}.bsp first, and replaces what the Companion staged there before.`;
  }

  async function stageChosenBuild() {
    const buildID = $("run-build").value;
    if (!buildID) return true;
    busy("run-message", "Copying the build's level into the game directory…");
    const engine = current();
    const { ok, body } = await api(`/api/v1/engines/${encodeURIComponent(engine.id)}/stage-build`, {
      method: "POST",
      body: { build_id: buildID, mod: $("run-mod").value.trim(), map: $("run-map").value.trim() },
    });
    if (!ok) {
      setMessage("run-message", body.error || "the build's level could not be copied into the game", "error");
      record("Could not stage the build", body.error, "failed");
      return false;
    }
    $("run-mod").value = body.mod;
    $("run-map").value = body.map;
    record(`Staged ${body.label || "a build"} as ${body.mod}/maps/${body.map}.bsp`, (body.files || []).join(", "), "ok");
    return true;
  }

  async function launch(button) {
    if (!launchBody()) return;
    await withBusy(button, async () => {
      if (!(await stageChosenBuild())) return;
      busy("run-message", "Starting…");
      const { ok, body } = await api("/api/v1/jobs", { method: "POST", body: launchBody() });
      if (!ok) {
        setMessage("run-message", body.error || "it could not be started", "error");
        record("Launch refused", body.error, "failed");
        return;
      }
      setMessage(
        "run-message",
        "Started. It is in the Jobs area, with its command and its output.",
        "ok"
      );
      record(`Started ${current()?.name || "engine"}`, body.command?.shell || "", "running");
      window.AUCOM.areas.jobs?.refresh?.();
    });
  }

  $("run-engine").addEventListener("change", renderEngine);
  $("run-action").addEventListener("change", () => {
    renderSessionNote();
    renderEngine();
  });
  $("run-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshEngines));
  $("run-preview").addEventListener("click", (event) => previewLaunch(event.currentTarget));
  $("run-launch").addEventListener("click", (event) => launch(event.currentTarget));

  $("run-build").addEventListener("change", describeChosenBuild);

  // The refresh showing this area starts. chooseBuild waits for it: otherwise
  // the refresh rebuilt the engine list after the choice and put the previous
  // engine back (NEW_244D, live on the operator's install).
  let refreshing = Promise.resolve();

  window.AUCOM.areas.run = {
    refresh() {
      refreshing = (async () => {
        await refreshEngines();
        await refreshBuilds();
      })();
      return refreshing;
    },
    // From Build step 4: this build, ready to play in the engine chosen here.
    async chooseBuild(buildID) {
      await refreshing.catch(() => {});
      await refreshBuilds();
      // An engine that can play a map on this machine, rather than whichever
      // profile happens to be first in the list: the live check picked
      // DarkPlaces, not set up, over the operator's ready vkQuake (NEW_244D).
      // Ready means it offers play_map and nothing stops it; failing that, one
      // this machine already has a setup for (its approval may just need
      // renewing after a profile update), rather than one nobody set up.
      const plays = (engine) => engine && (engine.actions || []).some((action) => action.id === "play_map");
      const ready = (engine) => plays(engine) && window.AUCOM.actionReady(engine, "play_map");
      const setUp = (engine) => plays(engine) && engine.binding && Object.keys(engine.binding.executables || {}).length > 0;
      if (!ready(current())) {
        const candidate = engines.find(ready);
        if (candidate) {
          $("run-engine").value = candidate.id;
          renderEngine();
        }
      }
      $("run-build").value = buildID;
      if ([...$("run-action").options].some((option) => option.value === "play_map")) {
        $("run-action").value = "play_map";
        $("run-action").dispatchEvent(new Event("change", { bubbles: true }));
      }
      describeChosenBuild();
      $("run-build").scrollIntoView({ block: "center" });
      $("run-build").focus();
    },
  };
})();
