// The Run area: an engine on this machine, set up, and started.
//
// Two halves, drawn apart on purpose. The profile is portable and contains no
// path belonging to anybody; the binding is nothing but this machine's paths and
// what this user approved. Which half a value came from is the difference
// between something safe to hand to somebody else and something that is not.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, when, permissionBlock,
    maturityBadge, maturityNote, openCompatibilityReport } = window.AUCOM;

  let engines = [];
  let bindingInputs = new Map();

  function current() {
    return engines.find((engine) => engine.id === $("run-engine").value);
  }

  async function refreshEngines() {
    const select = $("run-engine");
    const previous = select.value;
    const { ok, body } = await api("/api/v1/engines");
    if (!ok) {
      setMessage("run-message", body.error || "could not read the engine profiles", "error");
      return;
    }
    engines = body.items || [];
    select.replaceChildren();
    for (const engine of engines) {
      // The badge word goes into the option text, because a <select> holds no
      // markup and the choice is made before the detail panel is drawn.
      const mark = engine.maturity && engine.maturity.work_in_progress
        ? ` [${engine.maturity.badge}]`
        : "";
      select.append(
        el("option", {
          text: `${engine.name}${mark} — ${engine.ready ? "ready" : "needs setup"}`,
          attrs: { value: engine.id },
        })
      );
    }
    if (previous && engines.some((engine) => engine.id === previous)) select.value = previous;
    renderEngine();
  }

  function renderEngine() {
    const engine = current();
    const detail = $("run-engine-detail");
    detail.replaceChildren();
    bindingInputs = new Map();
    $("run-binding-fields").replaceChildren();
    $("run-detected").replaceChildren();
    if (!engine) return;

    // --- the portable half
    const portable = el("div", { className: "panel" });
    portable.append(el("h4", { text: "The profile" }));
    const head = el("p");
    head.append(el("strong", { text: engine.name + " " + engine.version }));
    head.append(document.createTextNode(" "));
    head.append(badge(engine.trust));
    const wip = maturityBadge(engine.maturity);
    if (wip) {
      head.append(document.createTextNode(" "));
      head.append(wip);
    }
    portable.append(head);
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
    if (note) portable.append(note);
    portable.append(el("p", { className: "muted", text: engine.trust_description || "" }));
    portable.append(el("p", { text: engine.summary || "" }));
    portable.append(
      el("p", {
        className: "muted",
        text: [
          "runtime " + engine.runtime,
          "engine " + engine.engine_version,
          engine.last_qualified ? "author last checked " + engine.last_qualified : "",
        ]
          .filter(Boolean)
          .join(" · "),
      })
    );
    const support = engine.platform_support || {};
    portable.append(
      el("p", {
        className: "muted",
        text:
          `On ${support.platform?.os}/${support.platform?.arch} this profile is ${support.status}` +
          (support.note ? ": " + support.note : "."),
      })
    );
    if ((engine.content_layouts || []).length) {
      portable.append(el("h4", { text: "Where this engine looks for content" }));
      const layouts = el("ul");
      for (const layout of engine.content_layouts) {
        layouts.append(
          el("li", {
            // A layout with no fixed path (a mod directory, named when it is
            // played) is the root itself plus "a directory you name" — never
            // the word "undefined" (NEW_244D).
            text: `${layout.title || layout.id}: ${layout.root}/${layout.path || "<a directory you name>"} (${layout.kind})` +
              (layout.note ? " — " + layout.note : ""),
          })
        );
      }
      portable.append(layouts);
    }
    detail.append(portable);

    // --- the local half
    const local = el("div", { className: "panel" });
    local.append(el("h4", { text: "What this machine has recorded" }));
    if (!engine.binding) {
      local.append(el("p", { className: "muted", text: "Nothing yet. Fill in the setup below." }));
    } else {
      const rows = el("dl", { className: "paths" });
      for (const [name, value] of Object.entries(engine.binding.executables || {})) {
        rows.append(el("dt", { text: name }));
        rows.append(el("dd", { text: value }));
      }
      for (const [role, value] of Object.entries(engine.binding.roots || {})) {
        rows.append(el("dt", { text: role }));
        rows.append(el("dd", { text: value }));
      }
      rows.append(el("dt", { text: "approved" }));
      rows.append(
        el("dd", {
          text: engine.binding.granted
            ? `yes, on ${when(engine.binding.granted_at)}`
            : "no",
        })
      );
      local.append(rows);
    }
    detail.append(local);

    // --- what is stopping it
    const problems = el("div");
    const perAction = engine.action_problems || {};
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

    renderActions(engine);
    renderBindingFields(engine);
  }

  // The engine preflight writes each remedy for the terminal
  // (`companion engine bind <profile id> …`, `companion toolchain show`). On
  // this page the remedy is a control right here, so that is what it names
  // (NEW_244D rehearsal: Run told a person to type commands naming profile
  // ids, and a stale setup's summary quoted two document digests).
  const PAGE_FIX = {
    missing_engine: "Choose the program under “Set up this engine on this machine” below, then press Save setup.",
    unbound_root: "Choose the folder under “Set up this engine on this machine” below, then press Save setup.",
    missing_root: "It was moved or removed. Choose it again below, then press Save setup.",
    not_authorized: "Read what it asks for below, tick the approval, then press Save setup.",
    stale_binding: "Read what it asks for below, tick the approval, then press Save setup again.",
    missing_game_data: "Choose your own installed copy as the game directory below — “Look for installed games” lists the likely places. The Companion never downloads or copies game data.",
    unsupported_platform: "Choose an engine whose profile supports this computer.",
  };

  function pageFix(problem) {
    return PAGE_FIX[problem.fault] || (/`companion /.test(problem.fix || "") ? "Set it up below, then press Save setup." : problem.fix);
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
    select.replaceChildren();
    for (const action of engine.actions || []) {
      const problems = (engine.action_problems || {})[action.id] || [];
      select.append(
        el("option", {
          text: `${action.title || action.id}${problems.length ? " — needs setup" : ""}`,
          attrs: { value: action.id, "data-role": action.session_role || "" },
        })
      );
    }
    if (previous && [...select.options].some((option) => option.value === previous)) select.value = previous;
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

  function renderBindingFields(engine) {
    const container = $("run-binding-fields");
    container.replaceChildren();
    const binding = engine.binding || {};

    for (const executable of engine.executables || []) {
      const field = window.AUCOM.pathField({
        id: "run-exe-" + executable.name,
        kind: "open-file",
        label: `${executable.title || executable.name} program`,
        value: (binding.executables || {})[executable.name] || "",
        hint: `The profile looks for a file named ${window.AUCOM.programFileName(executable.file)}.`,
      });
      bindingInputs.set("executable:" + executable.name, field);
      container.append(field.container);
    }

    // Every root the profile's actions declare, not a fixed list of two: a
    // profile may name a root this page has never heard of, and a form built
    // around the common ones would be a form that cannot set that one up.
    const roles = new Set();
    for (const action of engine.actions || []) {
      for (const root of action.roots || []) {
        if (root.role && root.role !== "workspace") roles.add(root.role);
      }
    }
    for (const role of [...roles].sort()) {
      const field = window.AUCOM.pathField({
        id: "run-root-" + role,
        kind: "directory",
        label: rootLabel(role),
        value: (binding.roots || {})[role] || "",
      });
      bindingInputs.set("root:" + role, field);
      container.append(field.container);
    }

    if ((engine.permissions || []).length) {
      const review = el("details", { attrs: { id: "run-permissions" } });
      review.append(el("summary", { text: `What ${engine.name} asks to be allowed to do (${engine.permissions.length})` }));
      for (const permission of engine.permissions) {
        review.append(permissionBlock(permission));
      }
      container.append(review);
    }
    const approve = el("label", { className: "check" });
    const box = el("input", { attrs: { type: "checkbox", id: "run-approve" } });
    if (engine.binding?.granted) box.checked = true;
    approve.append(box);
    approve.append(
      document.createTextNode(
        engine.trust === "builtin"
          ? "Approve what this profile asks for (a built-in profile arrived inside the Companion and needs no approval; recording one does no harm)"
          : "I have read what this profile asks for and I approve it"
      )
    );
    container.append(approve);
  }

  function rootLabel(role) {
    return (
      {
        game_root: "Game directory (the folder that contains id1)",
        content_root: "Your project directory",
        project_root: "Your project directory",
      }[role] || role.replace(/_/g, " ")
    );
  }

  async function saveBinding(button) {
    const engine = current();
    if (!engine) return;
    await withBusy(button, async () => {
      const executables = {};
      const roots = {};
      for (const [key, field] of bindingInputs) {
        const [kind, name] = key.split(":");
        const value = field.input.value.trim();
        if (kind === "executable") executables[name] = value;
        else roots[name] = value;
      }
      const approve = $("run-approve")?.checked || false;
      busy("run-setup-message", "Recording…");
      const { ok, body } = await api(`/api/v1/profiles/${encodeURIComponent(engine.id)}/bind`, {
        method: "POST",
        body: { executables, roots, approve, digest: approve ? engine.digest : undefined },
      });
      if (!ok) {
        setMessage("run-setup-message", body.error || "could not record this setup", "error");
        return;
      }
      setMessage("run-setup-message", "Recorded. It is in the list above and survives a restart.", "ok");
      record(`Set up ${engine.name}`, Object.values(executables).filter(Boolean).join(", "), "ok");
      await refreshEngines();
    });
  }

  async function forgetBinding(button) {
    const engine = current();
    if (!engine || !engine.binding) return;
    await withBusy(button, async () => {
      const { ok, body } = await api(`/api/v1/profiles/${encodeURIComponent(engine.id)}/unbind`, {
        method: "POST",
      });
      if (!ok) {
        setMessage("run-setup-message", body.error || "could not forget this setup", "error");
        return;
      }
      // Said explicitly, because "forget" is the kind of word people expect to
      // delete something.
      setMessage(
        "run-setup-message",
        "Forgotten. Nothing on disk was deleted: the engine, the game data and anything staged into it are yours.",
        "ok"
      );
      record(`Forgot the setup for ${engine.name}`, "no files were removed", "ok");
      await refreshEngines();
    });
  }

  async function detect(button) {
    await withBusy(button, async () => {
      const list = $("run-detected");
      list.replaceChildren(el("li", { className: "muted", text: "Looking…" }));
      const near = bindingInputs.get("executable:engine")?.input.value.trim();
      const query = near ? "?near=" + encodeURIComponent(near.replace(/[^/\\]*$/, "")) : "";
      const { ok, body } = await api("/api/v1/engines/detect" + query);
      list.replaceChildren();
      if (!ok) {
        list.append(el("li", { className: "muted", text: body.error || "nothing could be scanned" }));
        return;
      }
      const items = body.items || [];
      if (items.length === 0) {
        list.append(
          el("li", {
            className: "muted",
            text: "No installed game was found in the usual places. Choose the folder yourself above.",
          })
        );
        return;
      }
      for (const candidate of items) {
        const head = el("div", { className: "row-head" });
        head.append(el("strong", { text: candidate.path }));
        head.append(badge(candidate.source, "queued"));
        const detail = el("p", { className: "muted" });
        detail.textContent =
          `found ${candidate.evidence} in ${candidate.base_dir}` + (candidate.note ? " — " + candidate.note : "");
        const use = el("button", { text: "Use this folder", attrs: { type: "button" } });
        use.addEventListener("click", () => {
          // A candidate is a proposal. It fills the field in; nothing is
          // recorded until the person presses Save.
          const field = bindingInputs.get("root:game_root");
          if (field) {
            field.input.value = candidate.path;
            field.input.focus();
            setMessage("run-setup-message", "Filled in. Press Save setup to record it.", "");
          }
        });
        list.append(el("li", { children: [head, detail, el("div", { className: "row-actions", children: [use] })] }));
      }
    });
  }

  // The runtime value names are the profile schema's, not this page's: an
  // engine profile writes {runtime.map_name} in its argv, and a form that
  // invented its own spelling would resolve to nothing.
  function launchBody() {
    const engine = current();
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

  async function launch(button) {
    await withBusy(button, async () => {
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
  $("run-detect").addEventListener("click", (event) => detect(event.currentTarget));
  $("run-save-binding").addEventListener("click", (event) => saveBinding(event.currentTarget));
  $("run-forget").addEventListener("click", (event) => forgetBinding(event.currentTarget));
  $("run-preview").addEventListener("click", (event) => previewLaunch(event.currentTarget));
  $("run-launch").addEventListener("click", (event) => launch(event.currentTarget));

  window.AUCOM.areas.run = { refresh: refreshEngines };
})();
