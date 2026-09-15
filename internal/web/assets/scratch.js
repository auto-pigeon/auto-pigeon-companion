// Profiles → Write a profile from scratch (NEW_244D).
//
// The template wizard edits a tested document and cannot add an action. This
// panel writes a tool, a pipeline or an engine from nothing: any number of
// programs, actions with their arguments, inputs, outputs, parameters and
// folders, and pipeline stages wired to each other with parameters of their
// own.
//
// It follows the wizard's rule: the page never builds the document. It keeps
// what the person typed as plain fields, posts them to /api/v1/profiles/compose
// as `scratch`, and shows what the Companion composed, validated and digested.
// Installing is the same import route every other document takes, and the
// result is `local` — refused until somebody approves it in its review, where
// its programs and folders are also set up.

"use strict";

(() => {
  const { $, el, api, setMessage, withBusy, record } = window.AUCOM;

  const ROOT_ROLES = ["content_root", "game_root", "project_root", "build_root", "tool_root"];
  const OPTION_TYPES = ["text", "bool", "integer", "enum"];
  const ENGINE_ACTIONS = ["play_map", "play_package", "join_server", "host_listen", "host_dedicated"];

  // The whole form, as data. Rendering reads it; every field writes it back on
  // input, so a structural change (add, remove) can redraw without losing
  // anything typed.
  const state = {
    tool: { programs: [], actions: [] },
    engine: { programs: [], actions: [] },
    pipeline: { inputs: [], steps: [], outputs: [] },
  };
  let composed = null;
  let providers = []; // installed actions a pipeline stage can use, by capability

  const kind = () => $("scratch-kind").value;

  // --- small builders --------------------------------------------------------

  let counter = 0;
  function text(label, value, onInput, { placeholder = "", hint = "", grow = false, list = "" } = {}) {
    const id = "scratch-f-" + ++counter;
    const input = el("input", { attrs: { id, type: "text", value: value || "", placeholder, spellcheck: "false", autocomplete: "off" } });
    if (list) input.setAttribute("list", list);
    input.addEventListener("input", () => onInput(input.value));
    const children = [el("label", { text: label, attrs: { for: id } }), input];
    if (hint) children.push(el("span", { className: "hint", text: hint }));
    return el("div", { className: "field" + (grow ? " grow" : ""), children });
  }

  function select(label, value, choices, onChange) {
    const id = "scratch-f-" + ++counter;
    const node = el("select", { attrs: { id } });
    for (const choice of choices) {
      const [optionValue, optionText] = Array.isArray(choice) ? choice : [choice, choice];
      node.append(el("option", { text: optionText, attrs: { value: optionValue } }));
    }
    node.value = value ?? "";
    node.addEventListener("change", () => onChange(node.value));
    return el("div", { className: "field", children: [el("label", { text: label, attrs: { for: id } }), node] });
  }

  function check(label, value, onChange) {
    const id = "scratch-f-" + ++counter;
    const box = el("input", { attrs: { id, type: "checkbox" } });
    box.checked = Boolean(value);
    box.addEventListener("change", () => onChange(box.checked));
    return el("label", { className: "check", attrs: { for: id }, children: [box, document.createTextNode(" " + label)] });
  }

  function removeButton(label, onRemove) {
    const button = el("button", { text: "Remove", attrs: { type: "button", class: "secondary", "aria-label": label } });
    button.addEventListener("click", () => { onRemove(); render(); });
    return button;
  }

  function addButton(label, onAdd) {
    const button = el("button", { text: label, attrs: { type: "button", class: "secondary" } });
    button.addEventListener("click", () => { onAdd(); render(); });
    return button;
  }

  function list(title, note, rows, addLabel, onAdd) {
    const box = el("fieldset", { className: "scratch-list" });
    box.append(el("legend", { text: title }));
    if (note) box.append(el("p", { className: "muted", text: note }));
    for (const row of rows) box.append(row);
    box.append(el("div", { className: "row-actions", children: [addButton(addLabel, onAdd)] }));
    return box;
  }

  const splitList = (value) => value.split(",").map((item) => item.trim()).filter(Boolean);

  // --- a tool or an engine: programs and actions ----------------------------

  function programsEditor(doc) {
    const rows = doc.programs.map((program, index) =>
      el("div", {
        className: "row scratch-row",
        children: [
          text("Program name", program.name, (v) => (program.name = v), { placeholder: "qbsp" }),
          text("Title", program.title, (v) => (program.title = v), { placeholder: "Map compiler" }),
          text("File inside its folder", program.file, (v) => (program.file = v), {
            placeholder: "bin/qbsp", grow: true,
            hint: "relative to the folder you will choose in its setup; {platform.exe_suffix} adds .exe on Windows",
          }),
          removeButton("Remove this program", () => doc.programs.splice(index, 1)),
        ],
      })
    );
    return list("Programs", "Every program this profile starts. There is no limit and no required name.",
      rows, "Add a program", () => doc.programs.push({ name: "", title: "", file: "" }));
  }

  function actionEditor(doc, action, index, engine) {
    const card = el("div", { className: "panel scratch-card" });
    const head = el("div", { className: "row scratch-row" });
    if (engine) {
      head.append(select("Action", action.id, ENGINE_ACTIONS, (v) => (action.id = v)));
    } else {
      head.append(text("Action id", action.id, (v) => (action.id = v), { placeholder: "compile" }));
    }
    head.append(
      text("Title", action.title, (v) => (action.title = v), { placeholder: "Compile the map" }),
      select("Program", action.executable, [["", "choose…"], ...doc.programs.map((p) => p.name).filter(Boolean)],
        (v) => (action.executable = v)),
    );
    if (!engine) {
      head.append(text("Capability it provides", action.capability, (v) => (action.capability = v), {
        placeholder: "mygame.bsp.compile", hint: "what a pipeline stage asks for" }));
    }
    head.append(removeButton("Remove this action", () => doc.actions.splice(index, 1)));
    card.append(head);

    const argsId = "scratch-f-" + ++counter;
    const args = el("textarea", { attrs: { id: argsId, rows: "4", spellcheck: "false" } });
    args.value = (action.args || []).join("\n");
    args.addEventListener("input", () => (action.args = args.value.split("\n")));
    card.append(el("div", {
      className: "field",
      children: [
        el("label", { text: "Arguments, one per line", attrs: { for: argsId } }),
        args,
        el("span", {
          className: "hint",
          text: "{input.NAME} {output.NAME} {option.NAME} {root.ROLE} are filled in when it runs. " +
            "A line ending in [if OPTION] is passed only when that yes/no parameter is on; [if OPTION=VALUE] when it equals VALUE.",
        }),
      ],
    }));

    card.append(list("Inputs", "Files the action is handed, each copied into the job's own folder first.",
      (action.inputs || []).map((port, i) => el("div", {
        className: "row scratch-row",
        children: [
          text("Name", port.name, (v) => (port.name = v), { placeholder: "map" }),
          text("Role", port.role, (v) => (port.role = v), { placeholder: "mygame.map" }),
          text("Extensions", (port.extensions || []).join(", "), (v) => (port.extensions = splitList(v)), { placeholder: ".map" }),
          select("Put beside", port.stage_with || "", [["", "its own folder"], ...(action.inputs || []).map((p) => p.name).filter((n) => n && n !== port.name)],
            (v) => (port.stage_with = v)),
          check("Required", port.required, (v) => (port.required = v)),
          removeButton("Remove this input", () => action.inputs.splice(i, 1)),
        ],
      })),
      "Add an input", () => (action.inputs = [...(action.inputs || []), { name: "", role: "", extensions: [], required: true }])));

    card.append(list("Outputs", "Files the action leaves behind. A path is relative to the job's folder; \"rewrites\" names an input it changes in place.",
      (action.outputs || []).map((output, i) => el("div", {
        className: "row scratch-row",
        children: [
          text("Name", output.name, (v) => (output.name = v), { placeholder: "bsp" }),
          text("Role", output.role, (v) => (output.role = v), { placeholder: "mygame.bsp" }),
          text("Path", output.path, (v) => (output.path = v), { placeholder: "{option.name}.bsp" }),
          select("Or rewrites", output.in_place || "", [["", "—"], ...(action.inputs || []).map((p) => p.name).filter(Boolean)],
            (v) => (output.in_place = v)),
          check("Optional", output.optional, (v) => (output.optional = v)),
          removeButton("Remove this output", () => action.outputs.splice(i, 1)),
        ],
      })),
      "Add an output", () => (action.outputs = [...(action.outputs || []), { name: "", role: "", path: "" }])));

    card.append(list("Parameters", "What a pipeline stage may set. Nothing else reaches the command line.",
      (action.options || []).map((option, i) => el("div", {
        className: "row scratch-row",
        children: [
          text("Name", option.name, (v) => (option.name = v), { placeholder: "threads" }),
          select("Type", option.type || "text", OPTION_TYPES, (v) => { option.type = v; render(); }),
          text("Default", option.default, (v) => (option.default = v), { placeholder: option.type === "bool" ? "false" : "" }),
          ...(option.type === "enum"
            ? [text("Values", (option.values || []).join(", "), (v) => (option.values = splitList(v)), { placeholder: "fast, full" })]
            : []),
          removeButton("Remove this parameter", () => action.options.splice(i, 1)),
        ],
      })),
      "Add a parameter", () => (action.options = [...(action.options || []), { name: "", type: "text", default: "" }])));

    card.append(list("Folders it uses", "Besides its job folder. Where each folder is on this machine is chosen in the profile's setup, never written here.",
      (action.roots || []).map((root, i) => el("div", {
        className: "row scratch-row",
        children: [
          select("Folder", root.role, ROOT_ROLES, (v) => (root.role = v)),
          select("Access", root.access || "read", ["read", "read_write"], (v) => (root.access = v)),
          text("What for", root.purpose, (v) => (root.purpose = v), { placeholder: "find the WADs the map names", grow: true }),
          removeButton("Remove this folder", () => action.roots.splice(i, 1)),
        ],
      })),
      "Add a folder", () => (action.roots = [...(action.roots || []), { role: "content_root", access: "read", purpose: "" }])));
    return card;
  }

  function actionsEditor(doc, engine) {
    const box = el("div");
    box.append(el("h4", { text: engine ? "What the engine can do" : "Actions" }));
    doc.actions.forEach((action, index) => box.append(actionEditor(doc, action, index, engine)));
    box.append(el("div", {
      className: "row-actions",
      children: [addButton("Add an action", () => doc.actions.push({
        id: engine ? "play_map" : "", title: "", executable: "", args: [], inputs: [], outputs: [], options: [], roots: [],
      }))],
    }));
    return box;
  }

  // --- a pipeline ------------------------------------------------------------

  function providerFor(capability) {
    return providers.find((item) => item.capability === capability);
  }

  function sourcesBefore(stepIndex) {
    const sources = state.pipeline.inputs.filter((p) => p.name).map((p) => [`pipeline.${p.name}`, `the pipeline's ${p.name}`]);
    state.pipeline.steps.slice(0, stepIndex).forEach((step) => {
      for (const output of providerFor(step.capability)?.outputs || []) {
        sources.push([`${step.id}.${output.name}`, `${step.id} → ${output.name}`]);
      }
    });
    return sources;
  }

  function pipelineEditor() {
    const doc = state.pipeline;
    const box = el("div");
    box.append(list("Inputs", "What a person chooses when they press Build.",
      doc.inputs.map((port, i) => el("div", {
        className: "row scratch-row",
        children: [
          text("Name", port.name, (v) => (port.name = v), { placeholder: "map" }),
          text("Title", port.title, (v) => (port.title = v), { placeholder: "Map source" }),
          text("Role", port.role, (v) => (port.role = v), { placeholder: "mygame.map" }),
          text("Extensions", (port.extensions || []).join(", "), (v) => (port.extensions = splitList(v)), { placeholder: ".map" }),
          check("Required", port.required, (v) => (port.required = v)),
          removeButton("Remove this input", () => doc.inputs.splice(i, 1)),
        ],
      })),
      "Add an input", () => doc.inputs.push({ name: "", title: "", role: "", extensions: [], required: true })));

    const stages = el("div");
    stages.append(el("h4", { text: "Stages, in order" }));
    if (providers.length === 0) {
      stages.append(el("p", { className: "muted", text: "No installed tool provides a capability yet. Write and install a tool first." }));
    }
    doc.steps.forEach((step, index) => {
      const card = el("div", { className: "panel scratch-card" });
      const capabilities = [["", "choose…"], ...providers.map((item) => [item.capability, `${item.capability} — ${item.profileName}: ${item.title}`])];
      card.append(el("div", {
        className: "row scratch-row",
        children: [
          text("Stage id", step.id, (v) => (step.id = v), { placeholder: "compile" }),
          text("Title", step.title, (v) => (step.title = v), { placeholder: "Compile the map" }),
          select("Capability", step.capability, capabilities, (v) => { step.capability = v; step.inputs = {}; step.options = {}; render(); }),
          removeButton("Remove this stage", () => doc.steps.splice(index, 1)),
        ],
      }));
      const provider = providerFor(step.capability);
      if (provider) {
        card.append(el("p", { className: "muted", text: `Run by ${provider.profileName} (${provider.profileId}), action ${provider.actionId}.` }));
        const wiring = el("fieldset", { className: "scratch-list", children: [el("legend", { text: "Where each input comes from" })] });
        for (const input of provider.inputs || []) {
          wiring.append(select(`${input.name}${input.required ? " (required)" : ""}`, step.inputs[input.name] || "",
            [["", "not supplied"], ...sourcesBefore(index)], (v) => (step.inputs[input.name] = v)));
        }
        card.append(wiring);
        const params = el("fieldset", { className: "scratch-list", children: [el("legend", { text: "Parameters for this stage" })] });
        if ((provider.options || []).length === 0) params.append(el("p", { className: "muted", text: "This action declares no parameters." }));
        for (const option of provider.options || []) {
          const hint = `${option.type}${option.default !== undefined ? ", default " + option.default : ""}`;
          if (option.type === "bool") {
            params.append(select(option.title || option.name, step.options[option.name] || "",
              [["", `default (${option.default ?? "unset"})`], "true", "false"], (v) => (step.options[option.name] = v)));
          } else if (option.type === "enum") {
            params.append(select(option.title || option.name, step.options[option.name] || "",
              [["", `default (${option.default ?? "unset"})`], ...(option.values || []).map((v) => v.value || v)], (v) => (step.options[option.name] = v)));
          } else {
            params.append(text(option.title || option.name, step.options[option.name], (v) => (step.options[option.name] = v), { placeholder: option.default || "", hint }));
          }
        }
        card.append(params);
      }
      stages.append(card);
    });
    stages.append(el("div", {
      className: "row-actions",
      children: [addButton("Add a stage", () => doc.steps.push({ id: "", title: "", capability: "", inputs: {}, options: {} }))],
    }));
    box.append(stages);

    const outputSources = [];
    doc.steps.forEach((step) => {
      for (const output of providerFor(step.capability)?.outputs || []) outputSources.push([`${step.id}.${output.name}`, `${step.id} → ${output.name}`]);
    });
    box.append(list("Results", "Which stage outputs the build publishes.",
      doc.outputs.map((output, i) => el("div", {
        className: "row scratch-row",
        children: [
          text("Name", output.name, (v) => (output.name = v), { placeholder: "bsp" }),
          text("Title", output.title, (v) => (output.title = v), { placeholder: "The finished BSP" }),
          text("Role", output.role, (v) => (output.role = v), { placeholder: "mygame.bsp" }),
          select("From", output.from, [["", "choose…"], ...outputSources], (v) => (output.from = v)),
          check("Optional", output.optional, (v) => (output.optional = v)),
          removeButton("Remove this result", () => doc.outputs.splice(i, 1)),
        ],
      })),
      "Add a result", () => doc.outputs.push({ name: "", title: "", role: "", from: "" })));
    return box;
  }

  // --- render, compose, install ---------------------------------------------

  function render() {
    const body = $("scratch-body");
    body.replaceChildren();
    const current = kind();
    $("scratch-tool-fields").hidden = current !== "tool";
    $("scratch-engine-fields").hidden = current !== "engine";
    if (current === "tool" || current === "engine") {
      const doc = state[current];
      body.append(programsEditor(doc), actionsEditor(doc, current === "engine"));
    } else {
      body.append(pipelineEditor());
    }
    composed = null;
    $("scratch-install").disabled = true;
  }

  function request() {
    const current = kind();
    const scratch = { kind: current, game_family: $("scratch-game").value || undefined };
    if (current === "tool" || current === "engine") {
      const doc = state[current];
      scratch.executables = doc.programs;
      scratch.actions = doc.actions.map((action) => ({ ...action, args: (action.args || []).filter((line) => line.trim()) }));
      if (current === "tool") scratch.tool_version = $("scratch-tool-version").value.trim() || undefined;
    } else {
      scratch.inputs = state.pipeline.inputs;
      scratch.steps = state.pipeline.steps;
      scratch.outputs = state.pipeline.outputs;
    }
    return {
      scratch,
      id: $("scratch-id").value.trim() || undefined,
      name: $("scratch-name").value.trim() || undefined,
      version: $("scratch-version").value.trim() || undefined,
      summary: $("scratch-summary").value.trim() || undefined,
      publisher_name: $("scratch-publisher").value.trim() || undefined,
      license_spdx: $("scratch-license").value.trim() || undefined,
      runtime: current === "engine" ? $("scratch-runtime").value.trim() || undefined : undefined,
      engine_version: current === "engine" ? $("scratch-engine-version").value.trim() || undefined : undefined,
    };
  }

  async function compose() {
    const { ok, body } = await api("/api/v1/profiles/compose", { method: "POST", body: request() });
    const out = $("scratch-json");
    composed = null;
    $("scratch-install").disabled = true;
    if (!ok) {
      setMessage("scratch-message", body.error || "the profile could not be composed", "error");
      out.textContent = "";
      return;
    }
    out.textContent = JSON.stringify(body.document, null, 2);
    if (!body.valid) {
      setMessage("scratch-message", "Not valid yet — " + body.error, "error");
      return;
    }
    composed = body;
    $("scratch-install").disabled = false;
    setMessage("scratch-message",
      `Valid: ${body.name} ${body.version} (${body.id}), digest ${String(body.digest).slice(0, 19)}…. Nothing is installed until you press Install.`, "ok");
  }

  async function install() {
    if (!composed) return;
    const { ok, body } = await api("/api/v1/profiles/import", {
      method: "POST",
      body: { document: composed.document, replace: $("scratch-replace").checked },
    });
    if (!ok) {
      setMessage("scratch-message", body.error || "the profile could not be installed", "error");
      return;
    }
    const id = composed.id;
    record(`Installed ${composed.name}`, id, "ok");
    setMessage("scratch-message",
      `Installed ${composed.name} as a local profile. It cannot run until you approve it — its review is open below, with its setup.`, "ok");
    await refreshProviders();
    await window.AUCOM.areas.profiles?.refresh?.();
    await window.AUCOM.areas.profiles?.open?.(id);
  }

  async function refreshProviders() {
    const { ok, body } = await api("/api/v1/profiles");
    if (!ok) return;
    providers = [];
    for (const item of body.items || []) {
      if (item.kind !== "tool") continue;
      for (const action of item.actions || []) {
        if (!action.capability || providers.some((p) => p.capability === action.capability)) continue;
        providers.push({
          capability: action.capability, title: action.title, actionId: action.id,
          profileId: item.id, profileName: item.name,
          inputs: action.inputs || [], outputs: action.outputs || [], options: action.options || [],
        });
      }
    }
    providers.sort((a, b) => a.capability.localeCompare(b.capability));
  }

  $("scratch-kind").addEventListener("change", render);
  $("scratch-compose").addEventListener("click", (event) => withBusy(event.currentTarget, compose));
  $("scratch-install").addEventListener("click", (event) => withBusy(event.currentTarget, install));

  window.AUCOM.scratch = {
    state,
    async refresh() {
      await refreshProviders();
      render();
    },
  };
})();
