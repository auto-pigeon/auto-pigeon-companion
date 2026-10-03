// New profile: the form every profile is written with.
//
// One form, two ways of starting it (operator, 2026-10-03): from scratch, or
// filled in from a tested profile — and filled in means FILLED IN: every
// program, action, argument, input, output, parameter, folder and pipeline
// stage of the tested profile becomes a field here that can be changed,
// removed or added to. (NEW_244D's separate "template wizard" could rename a
// program and drop an action, and nothing else; it is gone.)
//
// A pipeline is where parameters are set: each stage picks a tool and carries
// its own arguments, and the same tool may be a stage twice.
//
// It follows the rule the page always had: it never builds the document. It
// keeps what the person typed as plain fields; profiles.js posts them to
// /api/v1/profiles/compose as `scratch` and shows what the Companion composed,
// validated and digested. A form filled from a tested profile says which
// (`based_on`), so what the form has no field for is kept — see
// internal/web/scratchfill.go. Installing is the same import route every other
// document takes, and the result is `local`: refused until somebody approves
// it in its review, where its programs and folders are also set up.

"use strict";

(() => {
  const { $, el, api } = window.AUCOM;

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
  let providers = []; // installed actions a pipeline stage can use, by capability
  let basedOn = ""; // the tested profile the fields were filled from, or ""

  const kind = () => $("wizard-kind").value;

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
            "A line ending in [if OPTION] is passed only when that yes/no parameter is on; [if OPTION=VALUE] when it equals VALUE; " +
            "[if folder ROLE] when that optional folder is set; [if input NAME] when that input was supplied.",
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
          check("Optional", root.optional, (v) => (root.optional = v)),
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

  // A stage id nobody has used yet, so adding the same tool a second time
  // gives a stage of its own without the person inventing a name first.
  function nextStageId(doc) {
    for (let n = doc.steps.length + 1; ; n += 1) {
      const id = "stage_" + n;
      if (!doc.steps.some((step) => step.id === id)) return id;
    }
  }

  // A stage's own arguments: one box, one argv element, exactly as typed.
  // They are this machine's setup of the pipeline (its `step_arguments`), not
  // part of the document, and are saved when the profile is installed.
  function stageArguments(step) {
    step.arguments = step.arguments || [];
    const box = el("fieldset", { className: "scratch-list", children: [el("legend", { text: "Arguments for this stage" })] });
    box.append(el("p", {
      className: "muted",
      text: "Extra words for this stage's command, before its input files — a flag the tool does not offer as a parameter. Each box is one argument exactly as you type it; nothing splits or quotes it. They stay on this computer: a profile you export carries none of them.",
    }));
    step.arguments.forEach((token, index) => {
      box.append(el("div", {
        className: "row scratch-row",
        children: [
          text("Argument", token, (v) => (step.arguments[index] = v), { placeholder: "-nopercent", grow: true }),
          removeButton("Remove this argument", () => step.arguments.splice(index, 1)),
        ],
      }));
    });
    box.append(el("div", { className: "row-actions", children: [addButton("Add an argument", () => step.arguments.push(""))] }));
    return box;
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
      // The tool is chosen by what it does here: an installed tool's action.
      // A stage a tested pipeline names that nothing installed provides yet
      // is still listed, so filling the form does not lose it.
      const capabilities = [["", "choose a tool…"], ...providers.map((item) => [item.capability, `${item.profileName}: ${item.title} — ${item.capability}`])];
      if (step.capability && !providerFor(step.capability)) capabilities.push([step.capability, `${step.capability} — no installed tool provides it yet`]);
      card.append(el("div", {
        className: "row scratch-row",
        children: [
          text("Stage id", step.id, (v) => (step.id = v), { placeholder: "compile", hint: "its own name in this pipeline; two stages may use the same tool" }),
          text("Title", step.title, (v) => (step.title = v), { placeholder: "Compile the map" }),
          select("Tool", step.capability, capabilities, (v) => { step.capability = v; step.inputs = {}; step.options = {}; render(); }),
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
      card.append(stageArguments(step));
      stages.append(card);
    });
    stages.append(el("div", {
      className: "row-actions",
      children: [addButton("Add a stage", () => doc.steps.push({ id: nextStageId(doc), title: "", capability: "", inputs: {}, options: {}, arguments: [] }))],
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

  // --- render, and what is posted --------------------------------------------

  function render() {
    const body = $("scratch-body");
    body.replaceChildren();
    const current = kind();
    if (current === "tool" || current === "engine") {
      const doc = state[current];
      body.append(programsEditor(doc), actionsEditor(doc, current === "engine"));
    } else {
      body.append(pipelineEditor());
    }
  }

  // request is the `scratch` member of a compose request: the fields, and the
  // tested profile they were filled from when there is one.
  function request() {
    const current = kind();
    const scratch = { kind: current, game_family: $("scratch-game").value || undefined, based_on: basedOn || undefined };
    if (current === "tool" || current === "engine") {
      const doc = state[current];
      scratch.executables = doc.programs;
      scratch.actions = doc.actions.map((action) => ({ ...action, args: (action.args || []).filter((line) => line.trim()) }));
      if (current === "tool") scratch.tool_version = $("wizard-tool-version").value.trim() || undefined;
    } else {
      scratch.inputs = state.pipeline.inputs;
      scratch.steps = state.pipeline.steps.map(({ arguments: _own, ...step }) => step);
      scratch.outputs = state.pipeline.outputs;
    }
    return scratch;
  }

  // stageTokens is every stage that has arguments of its own, for the
  // installer to record after the document is in.
  function stageTokens() {
    if (kind() !== "pipeline") return [];
    return state.pipeline.steps
      .map((step) => ({ stage: step.id, arguments: (step.arguments || []).filter((token) => token !== "") }))
      .filter((item) => item.stage && item.arguments.length > 0);
  }

  // clear empties the form for one kind: From scratch.
  function clear(current) {
    basedOn = "";
    if (current === "pipeline") state.pipeline = { inputs: [], steps: [], outputs: [] };
    else state[current] = { programs: [], actions: [] };
  }

  // fill puts a tested profile's fields into the form (the server's
  // /templates/{id}/scratch answer). Everything it fills stays editable.
  function fill(scratch) {
    const current = scratch.kind;
    basedOn = scratch.based_on || "";
    $("scratch-game").value = scratch.game_family || "";
    if (current === "pipeline") {
      state.pipeline = {
        inputs: (scratch.inputs || []).map((port) => ({ ...port, extensions: port.extensions || [] })),
        steps: (scratch.steps || []).map((step) => ({ ...step, inputs: step.inputs || {}, options: step.options || {}, arguments: [] })),
        outputs: scratch.outputs || [],
      };
    } else {
      state[current] = {
        programs: scratch.executables || [],
        actions: (scratch.actions || []).map((action) => ({
          ...action, args: action.args || [], inputs: action.inputs || [], outputs: action.outputs || [],
          options: (action.options || []).map((option) => ({ ...option, values: option.values || [] })),
          roots: action.roots || [],
        })),
      };
      if (current === "tool") $("wizard-tool-version").value = scratch.tool_version || "";
    }
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

  window.AUCOM.scratch = {
    state, render, request, stageTokens, clear, fill,
    async refresh() {
      await refreshProviders();
      render();
    },
  };
})();
