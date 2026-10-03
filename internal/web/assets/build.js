// The Build area: stage progress, the exact command, bounded live output,
// cancellation, warnings and artifacts.
//
// The page never holds a build's state as the truth. It starts one, is given an
// id, and from then on polls the manifest the runner writes after every stage.
// That is what makes this area survive a reload: everything it shows is on
// disk, so a page that has just been refreshed and a page that has been open
// the whole time show the same thing.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, when, terminal,
    maturityBadge, maturityNote, openCompatibilityReport, t } = window.AUCOM;

  let pipelines = [];
  let inputFields = new Map();
  let currentBuild = null;
  let poller = null;
  // The wizard. `step` is the panel on screen; `checked` is what the last check
  // (step 3) found for the choices as they are now — "ok", "blocked", or null
  // once anything in steps 1 and 2 changed after it; `outcome` is the state of
  // the build step 4 is showing.
  let step = 1;
  let checked = null;
  let outcome = null;
  // Bumped by every change to steps 1 and 2, so a check that was out while the
  // choices changed cannot mark the new choices checked.
  let generation = 0;
  let leakRequestID = null;
  // localMap is a file chosen in My Maps › On this computer (NEW_265): a path
  // on this machine, labelled as exactly that wherever the wizard shows it.
  let localMap = null;

  // An operating-system link only asks. The user's own page resolves the saved revision and
  // displays it here; the ordinary Build preview and explicit Build press still own execution.
  // The request itself is shown by leakrequest.js, above every area, so a page
  // that is open on My Maps sees it too (NEW_307W). What is here is the one
  // thing only this area can do: take the reviewed request into the wizard.
  // It returns a sentence when it cannot, and nothing when it did.
  async function adoptLeakRequest(body) {
    if (outcome === "running") {
      return t("A build is running here. Review this leak test when it has finished, or cancel it first.");
    }
    await refreshPipelines();
    const pipeline = pipelines.find((item) => item.id === body.pipeline);
    if (!pipeline) {
      return body.game_profile === "quake3"
        ? t("The Quake III leak-test pipeline is unavailable on this computer. Set up Q3Map2 in Profiles, then review this request again.")
        : t("The leak-test pipeline is unavailable on this computer. Set up a Quake 1 compiler (qbsp) in Profiles, then review this request again.");
    }
    const name = body.name || body.asset_id;
    $("build-pipeline").value = body.pipeline;
    window.AUCOM.chosenRevision = { asset_type: "map", asset_id: body.asset_id,
      display_name: name, revision_id: body.revision_id, revision: body.revision,
      files: body.files };
    renderPipeline();
    revisionChosen();
    for (const row of inputFields.values()) {
      if (row.kind === "map") { row.source.value = "asset"; row.apply(); }
    }
    $("build-label").value = t("Leak test: {name}, revision {n}", { name, n: body.revision });
    leakRequestID = body.request_id || null;
    choicesChanged();
    showStep(2);
    return "";
  }

  // How returning a leak build's result to the editor went, apart from what
  // the compiler found. A delivery that failed is sent again from here; that
  // never compiles anything.
  function leakVerdict(manifest, leak) {
    const compile = (manifest.steps || []).find((step) => step.id === "compile");
    // A compiler whose run the Companion reads itself (Q3Map2) comes with its
    // reading. Three facts stay apart in the sentence: how the process exited,
    // how the step ended, and what the run says about leaks — a leaked Quake
    // III map is exit 0, a failed step and a leak, all at once (Q3_018).
    if (leak?.diagnostic) {
      const how = t("Q3Map2 exited {code}; the compile step ended as {state}.", {
        code: compile?.exit_code ?? "—", state: compile?.state || t("not run") });
      const said = {
        leak: leak.diagnostic.evidence?.includes("route")
          ? t("Q3Map2 found a leak in this saved revision and wrote a line file (.lin) with {n} points, running from outside the map to the entity it reached.", { n: leak.diagnostic.route_points })
          : t("Q3Map2 found a leak in this saved revision, and left no usable line file: there is no route to draw."),
        no_leak: t("Q3Map2 found no leak in this saved revision. That is a statement about this run, not proof the map is sealed."),
        no_interior: leak.diagnostic.evidence?.includes("entity_in_solid")
          ? t("Not tested: every entity Q3Map2 could start from is inside a solid brush, so it flooded nothing. It prints “leaked” for that too; it is not a hole it found.")
          : t("Not tested: no entity stands in open space, so Q3Map2 flooded nothing. It prints “leaked” for that too; it is not a hole it found."),
        incomplete: t("No verdict: the run did not get far enough to say whether the map leaks ({why}). Its output says more.", {
          why: (leak.diagnostic.evidence || []).join(", ") || t("unknown") }),
      }[leak.diagnostic.outcome] || t("No verdict.");
      const notes = [];
      if (leak.diagnostic.evidence?.includes("shader_image_missing")) {
        notes.push(t("Some shaders had no image or definition Q3Map2 could find; it gave them its default flags, so what counts as solid may differ from the game's."));
      }
      if (leak.diagnostic.evidence?.includes("version_unqualified")) {
        notes.push(t("This Q3Map2 is not the version this reading was measured on (2.5.17n)."));
      }
      return [said, how, ...notes].join(" ");
    }
    if (leak?.unreadable) return t("This leak test left nothing to read: {why}", { why: leak.unreadable });
    const route = (manifest.outputs || []).some((output) => output.name === (leak?.pointfile_output || "pts") && output.path && !output.missing);
    if (route) return t("The compiler wrote a leak route (.pts): this run found a leak in this saved revision.");
    if (!compile || compile.skipped || !compile.state) {
      return t("The leak test did not reach the compiler: {why}. That is not a result about leaks.", {
        why: sentenceOf(manifest.error) || t("it stopped before the compile stage") });
    }
    if (compile.state === "succeeded") {
      return t("This compiler run found no leak. That is a statement about this run of this saved revision, not proof the map is sealed.");
    }
    return t("The compiler ended as {state} and wrote no leak route. Its output says why; this is not a no-leak result.", { state: compile.state });
  }

  function renderLeakReturn(manifest, item, attempt = 0) {
    const id = manifest.build_id;
    api(`/api/v1/leak-test/runs/${encodeURIComponent(id)}/return`).then(({ ok, body }) => {
      if (currentBuild !== id || !item.isConnected) return;
      const line = item.querySelector(".leak-return") || item.appendChild(el("p", { className: "leak-return" }));
      line.replaceChildren();
      if (!ok) { line.textContent = body.error || t("The return state could not be read."); return; }
      const waiting = !body.requested || body.state === "pending" || body.state === "sending";
      if (!body.requested && attempt > 4) {
        line.textContent = t("This build was not started from an editor's request, so nothing is returned automatically. Download the result and import it in the editor.");
        return;
      }
      if (waiting) {
        line.textContent = body.requested ? t("Sending the result to the editor…") : t("The result is ready on this computer.");
        if (attempt < 40) window.setTimeout(() => renderLeakReturn(manifest, item, attempt + 1), 1500);
        return;
      }
      if (body.state === "returned") {
        line.textContent = t("Returned to the editor. The tab that asked imports it for saved revision {n}.", { n: body.revision });
        line.className = "leak-return message ok";
        return;
      }
      line.className = "leak-return message error";
      line.append(document.createTextNode(t("The result was not returned to the editor: {why}", { why: body.error || t("the server refused it") }) + " "));
      const retry = el("button", { text: t("Retry"), attrs: { type: "button", class: "secondary" } });
      retry.addEventListener("click", () => withBusy(retry, async () => {
        await api(`/api/v1/leak-test/runs/${encodeURIComponent(id)}/return`, { method: "POST" });
        renderLeakReturn(manifest, item);
      }));
      line.append(retry);
    });
  }

  function currentPipeline() {
    return pipelines.find((item) => item.id === $("build-pipeline").value);
  }

  async function refreshPipelines() {
    const select = $("build-pipeline");
    const { ok, body } = await api("/api/v1/build/pipelines");
    // Read AFTER the await, not before: a refresh that started while the user
    // was choosing a pipeline must not put back the one that was selected
    // when it started (NEW_244D found two overlapping refreshes doing exactly
    // that, and a build then ran a pipeline nobody chose).
    const previous = select.value;
    // What the user already filled in survives a refresh of the same pipeline.
    // Leaving Build to set a compiler up in Profiles and coming back used to
    // come back to an empty map field.
    const kept = previous ? keptInputs() : null;
    if (!ok) {
      setMessage("build-message", body.error || "could not read the pipelines", "error");
      return;
    }
    pipelines = body.items || [];
    select.replaceChildren();
    for (const pipeline of pipelines) {
      // A <select> holds text and not markup, so the badge word goes into the
      // option itself. It has to be visible *before* the pipeline is chosen:
      // that is the moment the choice is made.
      const mark = pipeline.maturity && pipeline.maturity.work_in_progress
        ? ` [${pipeline.maturity.badge}]`
        : "";
      select.append(
        el("option", {
          text: `${pipeline.name}${mark} — ${pipeline.summary}`,
          attrs: { value: pipeline.id },
        })
      );
    }
    if (pipelines.length === 0) {
      setMessage("build-message", "No pipeline profiles are installed on this machine.", "error");
      return;
    }
    if (previous && pipelines.some((item) => item.id === previous)) select.value = previous;
    renderPipeline();
    if (kept && select.value === previous) restoreInputs(kept);
  }

  function keptInputs() {
    const values = new Map();
    for (const [name, row] of inputFields) {
      values.set(name, { source: row.source.value, path: row.file.input.value, choice: row.fileChoice.value });
    }
    return values;
  }

  function restoreInputs(values) {
    for (const [name, value] of values) {
      const row = inputFields.get(name);
      if (!row) continue;
      row.source.value = value.source;
      row.file.input.value = value.path;
      row.apply();
      if (value.choice) row.fileChoice.value = value.choice;
    }
  }

  // renderPipeline draws the stages and the inputs of whichever pipeline is
  // selected, and says exactly what is missing when one cannot run.
  function renderPipeline() {
    const pipeline = currentPipeline();
    const stages = $("build-stages");
    const inputs = $("build-inputs");
    stages.replaceChildren();
    inputs.replaceChildren();
    inputFields = new Map();
    if (!pipeline) return;

    $("build-pipeline-note").textContent = pipeline.runnable
      ? t("{n} stage(s). Version {version}.", { n: pipeline.steps.length, version: pipeline.version })
      : "This pipeline cannot run here: no tool installed on this machine does " +
        (pipeline.missing_capabilities.length === 1 ? "one of its stages" : `${pipeline.missing_capabilities.length} of its stages`) +
        ". Install the tool that does, then approve it in Profiles.";
    $("build-pipeline-note").className = pipeline.runnable ? "muted" : "message error";

    // Above the stages and above the Build button, because this is the surface
    // where a Quake II map is compiled and exported.
    //
    // Removed before it is added: renderPipeline runs on every change of the
    // selector, and a note appended each time would stack up one warning per
    // pipeline the user looked at.
    for (const stale of $("build-pipeline-note").parentElement.querySelectorAll(".wip")) stale.remove();
    const note = maturityNote(pipeline.maturity, () =>
      openCompatibilityReport({
        family: pipeline.engine_family,
        operation: "compile",
        about: `About the ${pipeline.name} pipeline.`,
        profiles: [{ role: "pipeline", id: pipeline.id, version: pipeline.version }],
      })
    );
    if (note) $("build-pipeline-note").after(note);

    for (const step of pipeline.steps) {
      const line = el("li");
      line.append(el("span", { className: "stage-name", text: step.title || step.id }));
      if (step.provider) {
        const detail = `${step.provider.name} ${step.provider.version}`;
        line.append(el("span", { className: "stage-detail", text: detail }));
        if (step.provider_trust) line.append(badge(step.provider_trust));
        if (step.provider_authorized === false) {
          line.append(badge("not approved", "failed"));
        }
        if (step.provider_version) {
          line.append(el("span", { className: "stage-detail", text: "installed " + step.provider_version }));
        }
      } else {
        line.append(el("span", { className: "stage-detail", text: "no installed tool does this step yet — add one in Profiles" }));
        line.classList.add("failed");
      }
      stages.append(line);
    }

    for (const input of pipeline.inputs || []) {
      inputs.append(inputRow(input));
    }
    revisionChosen();
  }

  // inputRow is one declared pipeline input, and what it offers is decided by
  // that input's OWN declared kind rather than by one assumption applied to all
  // of them. `source_kind` comes from the artifact role the document already
  // declares, classified once in Go by internal/profile.InputSourceKind — the
  // browser is not a second implementation of that rule.
  //
  // 246I defects 3 and 4: every row used to offer "A file on this machine" and
  // "A map from My Maps", so a Texture WAD field asked for a single WAD (which
  // is not what a Quake 1 build reads) and the map field would not present the
  // revision the user had just downloaded.
  function inputRow(input) {
    const id = "build-input-" + input.name;
    const kind = input.source_kind || "file";
    const source = el("select", { attrs: { id: id + "-source" } });
    // `required` is the field the contract actually has. `optional` never
    // existed on InputSpec, so the one optional input there is — the Quake 1
    // wad — has never been labelled optional on this page.
    const optional = input.required === false || input.required === undefined;
    const titled = `${input.title || input.name}${optional ? " (optional)" : ""}`;

    if (kind === "textures") {
      // The local choice is a FOLDER: a Quake 1 map reads every WAD it is
      // connected to, so asking for one file was asking the wrong question.
      source.append(el("option", { text: "A folder on this machine", attrs: { value: "folder" } }));
    } else if (kind === "directory") {
      source.append(el("option", { text: "A folder on this machine", attrs: { value: "folder" } }));
    } else {
      source.append(el("option", { text: "A file on this machine", attrs: { value: "file" } }));
    }

    const file = window.AUCOM.pathField({
      id,
      kind: kind === "textures" || kind === "directory" ? "directory" : "open-file",
      label: titled,
      hint:
        kind === "textures"
          ? "The folder holding the texture packages this map is connected to."
          : "",
    });

    const assetNote = el("p", { className: "build-chosen", attrs: { id: id + "-asset" } });
    // Where a file chosen in My Maps came from, said under the field: a file
    // on this computer, not a revision of a map in the account.
    const localNote = el("p", { className: "build-chosen build-chosen--local", attrs: { id: id + "-local" } });
    localNote.hidden = true;
    const fileChoice = el("select", { attrs: { id: id + "-file", "aria-label": "Which file of that revision" } });
    const fileChoiceField = el("div", {
      className: "field",
      children: [el("label", { text: "File", attrs: { for: id + "-file" } }), fileChoice],
    });
    fileChoiceField.hidden = true;

    // Only a real choice is a dropdown: with one way to supply this input,
    // "Where Map source comes from: A file on this machine" was a select with
    // one option. It appears once My Maps has sent a map over.
    const sourceField = el("div", {
      className: "field",
      children: [el("label", { text: "Where " + (input.title || input.name) + " comes from", attrs: { for: id + "-source" } }), source],
    });
    const wrapper = el("div", {
      className: "build-input",
      children: [
        sourceField,
        file.container,
        localNote,
        assetNote,
        fileChoiceField,
      ],
    });

    const apply = () => {
      sourceField.hidden = source.options.length < 2;
      const usingAsset = source.value === "asset";
      file.container.hidden = usingAsset;
      assetNote.hidden = !usingAsset;
      fileChoiceField.hidden = !usingAsset || fileChoice.options.length < 2;
      describeLocal();
    };
    const describeLocal = () => {
      const chosen = localMap && kind === "map" && source.value !== "asset" && file.input.value.trim() === localMap.path;
      localNote.hidden = !chosen;
      if (!chosen) return;
      localNote.replaceChildren(
        el("span", { className: "build-chosen__name", text: localMap.name }),
        el("span", { className: "build-chosen__detail", text: t("a file on this computer, chosen in My Maps — not a map from your account. It is copied into the build; the original is never changed.") }),
        el("span", { className: "build-chosen__detail mono", text: localMap.path }),
      );
    };
    source.addEventListener("change", apply);
    file.input.addEventListener("input", describeLocal);
    file.input.addEventListener("change", describeLocal);
    apply();

    inputFields.set(input.name, { input, kind, source, file, assetNote, localNote, fileChoice, fileChoiceField, apply });
    return wrapper;
  }

  // revisionChosen is called by the Library when the user picks a revision.
  // --- the wizard -------------------------------------------------------------

  function chosenFiles() {
    const names = [];
    for (const [, row] of inputFields) {
      if (row.source.value === "asset") {
        if (window.AUCOM.chosenRevision) names.push(window.AUCOM.chosenRevision.display_name);
      } else if (row.file.input.value.trim()) {
        names.push(row.file.input.value.trim().split(/[\\/]/).pop());
      }
    }
    return [...new Set(names)];
  }

  function missingRequired() {
    const pipeline = currentPipeline();
    const body = requestBody();
    return (pipeline?.inputs || []).filter((input) => input.required && !body.inputs[input.name]);
  }

  function renderSteps() {
    const pipeline = currentPipeline();
    const files = chosenFiles();
    const missing = missingRequired();
    // Three steps: Check and Build were one step in two panels, and the
    // operator asked for the Check step to go (2026-09-23). Panel 4 (the
    // running build) is shown under step 3's tab.
    const summaries = {
      1: pipeline ? pipeline.name : "",
      2: files.length ? files.join(", ") : "nothing chosen yet",
      3: outcome ? outcome : checked === "ok" ? "ready to build" : checked === "blocked" ? "something is missing" : "not checked yet",
    };
    const done = { 1: Boolean(pipeline), 2: Boolean(pipeline) && missing.length === 0 && files.length > 0,
      3: outcome === "succeeded" };
    const attention = { 3: checked === "blocked" || (Boolean(outcome) && !["succeeded", "running", "queued"].includes(outcome)) };
    const shown = step === 4 ? 3 : step;
    for (let n = 1; n <= 3; n += 1) {
      const tab = $("build-step-tab-" + n);
      tab.classList.toggle("current", n === shown);
      tab.classList.toggle("done", n !== shown && done[n]);
      tab.classList.toggle("attention", n !== shown && !done[n] && Boolean(attention[n]));
      if (n === shown) tab.setAttribute("aria-current", "step");
      else tab.removeAttribute("aria-current");
      $("build-step-summary-" + n).textContent = summaries[n];
    }
    $("build-start").disabled = checked !== "ok";
  }

  // showStep puts one panel on screen. Arriving at the check runs it, because a
  // check of choices that have since changed is not a check of anything.
  function showStep(n, { check = true, focus = true } = {}) {
    step = n;
    for (let i = 1; i <= 4; i += 1) $("build-step-" + i).hidden = i !== n;
    renderSteps();
    if (focus) {
      const heading = $("build-step-" + n).querySelector("h3");
      if (heading) {
        heading.setAttribute("tabindex", "-1");
        heading.focus({ preventScroll: true });
      }
      $("build-steps").scrollIntoView({ block: "nearest" });
    }
    if (n === 3 && check && checked === null) preview($("build-preview"));
  }

  function choicesChanged() {
    generation += 1;
    checked = null;
    renderSteps();
  }

  // acceptableFiles narrows a revision's files to the ones this input says it
  // accepts. A revision carries everything that was uploaded to it, so offering
  // qbsp's map input a `.wad` is offering a build that cannot start. The
  // extensions are the document's own advisory list, not a guess at the bytes.
  function acceptableFiles(files, extensions) {
    const allowed = (extensions || []).map((extension) => extension.toLowerCase());
    if (allowed.length === 0) return files || [];
    return (files || []).filter((file) =>
      allowed.some((extension) => (file.path || "").toLowerCase().endsWith(extension))
    );
  }

  // revisionChosen is called by the Library when the user picks a revision.
  //
  // It touches MAP inputs and only map inputs (246I defect 3): the old version
  // walked every row, so choosing a map wrote its revision over the texture row
  // as well. Selecting or changing the map must not disturb the textures, and
  // selecting textures must not disturb the map.
  function revisionChosen() {
    const chosen = window.AUCOM.chosenRevision;
    for (const [, row] of inputFields) {
      if (row.kind !== "map") continue;
      const already = [...row.source.options].find((option) => option.value === "asset");
      if (!chosen) {
        // No revision, so there is no concrete choice to offer. The option goes
        // away rather than sitting there meaning nothing.
        if (already) already.remove();
        row.assetNote.textContent = "Nothing chosen yet. Pick a map revision in My Maps first.";
        row.fileChoice.replaceChildren();
        row.apply();
        continue;
      }
      // The concrete map, named in the selector itself, so "Where Map source
      // comes from" presents the thing that was just downloaded instead of a
      // category the user has to translate.
      const label = t("Downloaded map: {name} — revision {n}", { name: chosen.display_name, n: chosen.revision });
      if (already) {
        already.textContent = label;
      } else {
        const option = el("option", { text: label, attrs: { value: "asset" } });
        row.source.prepend(option);
        // It is also the default: a map downloaded a moment ago is the expected
        // answer, not an alternative to go looking for.
        row.source.value = "asset";
      }
      row.apply();
      // The map the build will read, said plainly and large enough to check
      // at a glance before pressing Build (operator, NEW_244D).
      row.assetNote.replaceChildren(
        el("span", { className: "build-chosen__name", text: chosen.display_name }),
        el("span", { className: "build-chosen__detail", text: `revision ${chosen.revision}, downloaded to this computer` })
      );
      row.fileChoice.replaceChildren();
      for (const file of acceptableFiles(chosen.files, row.input.extensions)) {
        row.fileChoice.append(el("option", { text: file.path, attrs: { value: file.path } }));
      }
      row.apply();
    }
  }

  function requestBody() {
    const pipeline = currentPipeline();
    const inputs = {};
    for (const [name, row] of inputFields) {
      if (row.source.value === "asset") {
        const chosen = window.AUCOM.chosenRevision;
        if (!chosen) continue;
        const file = row.fileChoice.value ? "#" + row.fileChoice.value : "";
        inputs[name] =
          `aub:${chosen.asset_type}/${chosen.asset_id}@${chosen.revision_id || "current"}${file}`;
      } else if (row.file.input.value.trim()) {
        inputs[name] = row.file.input.value.trim();
      }
    }
    return {
      pipeline: pipeline?.id || "",
      inputs,
      label: $("build-label").value.trim() || undefined,
      strict: $("build-strict").checked,
      leak_request_id: pipeline?.leak_test ? leakRequestID || undefined : undefined,
    };
  }

  // inputTitle names a declared input the way its field is labelled.
  function inputTitle(name) {
    const input = (currentPipeline()?.inputs || []).find((item) => item.name === name);
    return input?.title || name;
  }

  // explain turns the two refusals a first build meets into what to do about
  // them. The executor's own sentence is the record for the CLI; this is the
  // next step (NEW_244D: a preview used to say "This is
  // what would run" over "(no command resolved)" and hide both).
  function explain(text, step) {
    // A refusal that reached the whole build rather than one stage still names
    // the stage it is about when the text carries its profile id (operator,
    // 2026-09-23: "This stage needs a program that is not installed on this
    // computer" named neither).
    step = step || stageNamedIn(text);
    const missingInput =
      /needs "([^"]+)", and nothing supplies/.exec(text || "") ||
      /the required input "([^"]+)" was not supplied/.exec(text || "");
    if (missingInput) {
      return { kind: "input", name: missingInput[1],
        advice: `Choose the ${inputTitle(missingInput[1])} first: use Browse… beside that field, or pick a map revision in My Maps.` };
    }
    const gone = /the input "([^"]+)": there is no such file or folder: (.+?) \(/.exec(text || "");
    if (gone) {
      return { kind: "input", name: gone[1],
        advice: `The file chosen for ${inputTitle(gone[1])} is not there: ${gone[2]}. Choose it again with Browse….` };
    }
    // These four refusals were one sentence, and they are not one situation.
    // 246I: the operator met "This stage's program is not set up on this machine
    // yet. Say where it is, once, in Profiles." when the missing thing was the
    // EricW qbsp/vis/light toolchain — which this Companion can fetch and verify
    // itself. The sentence named neither the stage nor the program, and sent
    // somebody to a form instead of offering the action.
    //
    // `step` is the stage this came from, when the caller knows it. The
    // executor's own sentence carries a profile id, a root-role name and a CLI
    // command, and none of those belong on the page.
    if (/is not installed on this machine|no program is recorded/.test(text || "")) {
      return { kind: "setup", missing: "program", advice: setupSentence(step, "is not installed on this computer") };
    }
    // Three spellings of "a folder this machine has not been told about". The
    // third was found by running a Quake II preview on Windows: the resolver
    // says `{root.game_root} in "..." has no value (known: build_root,
    // tool_root, workspace)`, which matched none of the patterns above and so
    // reached the page whole — a root-role name and a list of internal tokens,
    // which is precisely what this is supposed to keep off it. The role name is
    // used to CHOOSE the wording and is never shown.
    if (/root is not configured|not configured on this machine/.test(text || "") ||
        /\{root\.[a-z_]+\} in .* has no value/.test(text || "")) {
      return { kind: "setup", missing: "folder", advice: setupSentence(step, "needs a folder on this computer that has not been chosen yet") };
    }
    return null;
  }

  // setupSentence names the stage and the program, in the user's words.
  function setupSentence(step, ending) {
    const stage = step?.title ? `The ${step.title} stage` : "One stage of this build";
    const program = step?.provider?.name || step?.profile?.name;
    const where = " Set it up in Profiles › Build Tools, or choose another way to build in step 1.";
    if (program) return `${stage} needs ${program}, but it ${ending}.${where}`;
    return `${stage} needs a program that ${ending}.${where}`;
  }

  // stageNamedIn is the pipeline stage whose tool profile a message names.
  function stageNamedIn(text) {
    const pipeline = currentPipeline();
    for (const candidate of pipeline?.steps || []) {
      const id = candidate.provider?.id || candidate.profile?.id;
      if (id && String(text || "").includes(id)) return candidate;
    }
    return null;
  }

  // setupActions is the one thing a blocked stage offers: Setup, which opens
  // the tool's page in Profiles. Setup happens there and nowhere else
  // (operator, 2026-09-23) — the folder chooser and the verified download that
  // used to be drawn here are on that page.
  function setupActions(profile) {
    const setup = el("button", { text: t("Setup"), attrs: { type: "button", class: "primary" } });
    setup.addEventListener("click", () => window.AUCOM.areas.profiles?.configure?.(profile.id));
    return el("div", { className: "row-actions", children: [setup] });
  }

  // sentenceOf is the line a person reads: a refusal's first line. A refusal
  // from the extractor leads with its own reason and keeps everything the
  // extractor printed underneath (its terminal record, an incident envelope);
  // run together they were one paragraph nobody could read, shown twice
  // (Q3_010, headed run: `[q3map_shader_unsafe]` was on the page and lost in
  // it). The rest goes behind "Technical details", never away.
  function sentenceOf(text) {
    return String(text || "").split("\n")[0];
  }

  function technicalDetails(text) {
    const lines = String(text || "").split("\n");
    if (lines.length < 2) return null;
    const details = el("details", { className: "stage-findings" });
    details.append(el("summary", { text: t("Technical details") }));
    details.append(el("pre", { className: "output", text: lines.slice(1).join("\n") }));
    return details;
  }

  // recordOnce puts a line in Activity unless this window already recorded the
  // same event. Activity is a list of things that happened, and re-reading a
  // result is not one of them.
  const recorded = new Set();
  function recordOnce(key, summary, detail, kind) {
    if (recorded.has(key)) return;
    recorded.add(key);
    record(summary, detail, kind);
  }

  function problemBlock(text, profile, step) {
    const why = explain(text, step);
    const block = el("div", { className: "problem" });
    block.append(el("p", { children: [el("strong", { text: why ? why.advice : sentenceOf(text) })] }));
    const technical = why ? null : technicalDetails(text);
    if (technical) block.append(technical);
    // The executor's own sentence names profile ids, root roles and a CLI
    // command; when the advice above already says what to do, the page does not
    // repeat it (NEW_244D, operator: no internal ids on the page). A refusal
    // the page cannot explain is still shown whole, because that is all there is.
    if (why?.kind === "setup" && profile?.id) {
      block.append(setupActions(profile));
    }
    if (why?.kind === "input") {
      const field = $("build-input-" + why.name);
      if (field) {
        const focus = el("button", { text: t("Choose the {what}", { what: inputTitle(why.name) }), attrs: { type: "button", class: "secondary" } });
        focus.addEventListener("click", () => {
          showStep(2, { focus: false });
          field.focus();
        });
        block.append(el("div", { className: "row-actions", children: [focus] }));
      }
    }
    return block;
  }

  // One check at a time. withBusy restores the disabled state it found, so a
  // second check started while the first was out found the button disabled and
  // left it that way for good (NEW_244D wizard: arriving back on step 3 while a
  // check ran).
  let checking = null;

  function preview(button) {
    if (step !== 3) showStep(3, { check: false });
    if (!checking) {
      checking = (async () => {
        // Choices that changed while a check was out (a chosen revision's file
        // list arriving, say) are checked again, a bounded number of times.
        for (let round = 0; round < 3; round += 1) {
          const asked = generation;
          await runPreview(button, asked);
          if (asked === generation || step !== 3) break;
        }
      })().finally(() => { checking = null; });
    }
    return checking;
  }

  async function runPreview(button, asked) {
    await withBusy(button, async () => {
      busy("build-message", "Resolving every stage…");
      const { ok, body } = await api("/api/v1/build/preview", { method: "POST", body: requestBody() });
      const out = $("build-preview-out");
      out.hidden = false;
      out.replaceChildren();
      // A result for choices that have changed since is still shown — it is
      // what was asked — but it does not mark the new choices checked.
      const verdict = (value) => (asked === generation ? value : null);
      if (!ok) {
        checked = verdict("blocked");
        // A refused check is about THESE choices; the last build's outcome is
        // not theirs, and left on the step's tab it read "succeeded" over a
        // refusal.
        outcome = null;
        renderSteps();
        setMessage("build-message", explain(body.error)?.advice || sentenceOf(body.error) || "the preview failed", "error");
        out.append(problemBlock(body.error || "the preview failed"));
        // A refusal met at the check never becomes a build, so this is the only
        // record of it in Activity: its kind, and the refusal's own sentence.
        // Keyed on the sentence, not the whole text: the extractor's raw output
        // carries a fresh incident id each time, so the same refusal re-read
        // on a return to this area looked new.
        recordOnce("check:" + sentenceOf(body.error), "Build check refused",
          (body.class ? "[" + body.class + "] " : "") + sentenceOf(body.error), "failed");
        return;
      }
      const blocked = (body.steps || []).filter((step) => step.error);
      checked = verdict(blocked.length > 0 ? "blocked" : "ok");
      renderSteps();
      if (blocked.length > 0) {
        setMessage("build-message",
          "This build cannot start yet. What is missing is said under the stage that needs it; nothing has started.",
          "error");
      } else {
        setMessage("build-message", "Everything is in place. This is what will run; nothing has started. Press Build.", "ok");
      }
      out.append(el("h4", { text: "Commands" }));
      for (const step of body.steps || []) {
        const block = el("div");
        block.append(
          el("p", {
            children: [
              el("strong", { text: step.title || step.id }),
              el("span", { className: "stage-detail", text: ` ${step.profile?.name || ""} ${step.profile?.version || ""}` }),
            ],
          })
        );
        if (step.error) block.append(problemBlock(step.error, step.profile, step));
        if (step.command?.shell) block.append(el("pre", { className: "output", text: step.command.shell }));
        out.append(block);
      }
    });
  }

  async function start(button) {
    await withBusy(button, async () => {
      busy("build-message", "Starting…");
      const { ok, body } = await api("/api/v1/build/runs", { method: "POST", body: requestBody() });
      if (!ok) {
        setMessage("build-message", explain(body.error)?.advice || sentenceOf(body.error) || "the build could not be started", "error");
        // The refusal's own words, whole, in Activity — and its kind, when the
        // Companion classified it (Q3_010: an extractor refusal such as
        // `q3map_shader_unsafe` never becomes a build, so this line and the
        // message above are the only places it can be read).
        record("Build could not be started", (body.class ? "[" + body.class + "] " : "") + sentenceOf(body.error), "failed");
        return;
      }
      setMessage("build-message", "The build has started.", "ok");
      outcome = "running";
      setMessage("build-result", "", "");
      showStep(4, { focus: false });
      record(`Build started: ${$("build-label").value.trim() || currentPipeline()?.name || "untitled"}`, "", "running");
      currentBuild = body.build;
      leakRequestID = null;
      $("build-current-panel").hidden = false;
      $("build-current-title").textContent = "Building " + ($("build-label").value.trim() || currentPipeline()?.name || "");
      $("build-current-title").setAttribute("tabindex", "-1");
      $("build-current-title").focus();
      poll();
    });
  }

  // poll re-reads the manifest. It is a poll rather than a stream because the
  // manifest is the truth about a build, and a page reading a stream would be
  // reading something else — one that a reload could not reconstruct.
  function poll() {
    if (poller) window.clearTimeout(poller);
    if (!currentBuild) return;
    const tick = async () => {
      const { ok, body } = await api("/api/v1/build/runs/" + encodeURIComponent(currentBuild));
      if (!ok) {
        setMessage("build-message", body.error || "lost track of this build", "error");
        return;
      }
      renderProgress(body);
      if (body.live) {
        poller = window.setTimeout(tick, 700);
        return;
      }
      const state = body.manifest.state;
      outcome = state;
      renderSteps();
      // A compiler that exited 0 while it warned (Q3Map2's `Couldn't find image
      // for shader`) did not produce a finished map, and the result says so.
      const warned = (body.manifest.steps || []).reduce((sum, step) =>
        sum + (step.diagnostics || []).filter((d) => d.severity === "warning").length, 0);
      // A build somebody cancelled is not an error, and is not announced as one.
      setMessage(
        "build-result",
        state === "succeeded" && warned
          ? t("The build ran to the end, but the compiler warned {warnings} time(s), so this is not a complete result. Read the warnings in the stages below.", { warnings: warned })
          : state === "succeeded"
          ? "The build succeeded. Its outputs are listed below."
          : state === "cancelled"
            ? "You cancelled this build. The stage that was running was stopped with everything it had started; what it had written is listed below."
            : state === "interrupted"
              ? "This build was interrupted: the Companion stopped while it ran. Nothing was run again; build it again when you are ready."
              : `The build ${state}: ${body.manifest.error || body.error || "see the stages below"}`,
        state === "succeeded" ? (warned ? "warning" : "ok") : state === "cancelled" || state === "interrupted" ? "" : "error"
      );
      // Once per build and outcome. Coming back to this area polls the open
      // build again, and each return used to add the same line: four builds,
      // eleven entries (Q3_010, headed run).
      recordOnce(`${body.manifest.build_id}:${state}`,
        `Build ${state}: ${body.manifest.label || body.manifest.pipeline?.name || "untitled"}`,
        sentenceOf(body.manifest.error), state);
      await refreshHistory();
    };
    tick();
  }

  function renderProgress(body) {
    const manifest = body.manifest;
    const list = $("build-progress");
    list.replaceChildren();
    $("build-cancel").disabled = !body.live;
    // A finished build has nothing to cancel; the button is not left standing
    // there disabled and red.
    $("build-cancel").hidden = !body.live;
    $("build-output-heading").textContent = body.live ? "Output so far" : "Output";
    $("build-current-empty").hidden = true;
    // Play this build: a finished build with a level goes straight to Run.
    // A Quake III map is not played by dropping its BSP into a mod folder: its
    // textures, shaders and models have to travel with it. That build gets the
    // Package → Install → Run panel instead (q3package.js), which is told about
    // every render and ignores the ones for a build it is already showing.
    const quake3 = manifest.engine_family === "quake3";
    $("build-play").hidden = quake3 || !(manifest.state === "succeeded" &&
      (manifest.outputs || []).some((output) => output.name === "bsp" && output.path && !output.missing));
    window.AUCOM.q3package?.show(manifest, Boolean(body.live));
    if (body.live) {
      outcome = manifest.state;
      renderSteps();
    }
    $("build-current-title").textContent =
      `${body.live ? "Building" : "Build"}: ${manifest.label || manifest.pipeline?.name || "this build"} — ${manifest.state}`;
    if (!body.live && body.leak_test) {
      const resultButton = el("button", { text: t("Download leak result"), attrs: { type: "button", class: "secondary" } });
      resultButton.addEventListener("click", () => withBusy(resultButton, async () => {
        const response = await api(`/api/v1/leak-test/runs/${encodeURIComponent(manifest.build_id)}/result`);
        if (!response.ok) {
          setMessage("build-message", response.body.error || "The leak result could not be downloaded.", "error");
          return;
        }
        const blob = new Blob([JSON.stringify(response.body)], { type: "application/json" });
        const url = URL.createObjectURL(blob);
        const anchor = el("a", { attrs: { href: url, download: manifest.build_id + "-leak-result.json" } });
        anchor.click();
        window.setTimeout(() => URL.revokeObjectURL(url), 1000);
      }));
      // What the compiler found, then how returning it went, then the file:
      // three things, kept apart (NEW_307W).
      const item = el("li", { className: "leak-outcome", children: [
        el("p", { className: "leak-verdict", text: leakVerdict(manifest, body.leak_test) }), resultButton] });
      list.append(item);
      renderLeakReturn(manifest, item);
    }

    // The finished build is the other moment a compatibility report is worth
    // offering: the user has just seen what happened and has the build id that
    // fills in the diagnostics. The manifest carries the family (see
    // internal/build/manifest.go), so this needs no second lookup.
    // Removed from where it is PUT: beside the title, inside the panel's head.
    // The selector here used to be `#build-current-panel > .wip`, which names a
    // child of the panel; the note is a grandchild, so nothing was ever
    // removed and every poll of a build added another copy — three on a
    // three-poll build, seven on a slower one (operator, 2026-10-01).
    for (const stale of $("build-current-title").parentElement.querySelectorAll(".wip")) stale.remove();
    const buildNote = maturityNote(
      { work_in_progress: true, badge: "Work in progress", message: body.maturity_message, feedback_invited: true },
      () =>
        openCompatibilityReport({
          family: manifest.engine_family,
          operation: "compile",
          build_id: manifest.build_id,
          about: `About the build "${manifest.label || manifest.pipeline?.name || "untitled"}" with ${manifest.pipeline?.name || "this pipeline"}.`,
        })
    );
    if (body.maturity_message && buildNote) $("build-current-title").after(buildNote);

    for (const step of manifest.steps || []) {
      // While the build is live, a stage it has not reached yet is waiting. The
      // manifest pre-marks those as skipped ("the build stopped before this
      // step") so a crash leaves a true record; on a running build that
      // sentence is not true yet (NEW_244D rehearsal).
      const pending = body.live && step.skipped && !step.state;
      const line = el("li", { className: pending ? "" : step.state || (step.skipped ? "skipped" : "") });
      line.append(el("span", { className: "stage-name", text: step.title || step.id }));
      line.append(badge(pending ? "waiting" : step.skipped && !step.state ? "skipped" : step.state || "waiting", pending ? "queued" : undefined));
      // A step the build never reached has no tool resolved for it yet; the badge
      // already says it was skipped, so nothing stands in for a name.
      const provider = step.profile?.name ? `${step.profile.name} ${step.profile.version}` : step.skipped ? "" : "no tool recorded";
      line.append(el("span", { className: "stage-detail", text: provider }));
      if (step.duration_ms) {
        line.append(el("span", { className: "stage-detail", text: `${step.duration_ms} ms` }));
      }
      const warnings = (step.diagnostics || []).filter((d) => d.severity === "warning").length;
      const errors = (step.diagnostics || []).filter((d) => d.severity === "error").length;
      if (warnings || errors) {
        line.append(
          el("span", {
            className: "stage-detail",
            text: t("{errors} error finding(s), {warnings} warning(s)", { errors, warnings }),
          })
        );
      }
      if (step.error && !pending) line.append(el("span", { className: "stage-detail", text: step.error }));
      // What KIND of failure it was, as the token the manifest carries
      // (Q3_010): "the map leaks" and "the compiler is not installed" are told
      // apart by this, not by their wording.
      if (step.failure_class && !pending) {
        line.append(el("span", { className: "stage-detail failure-class", text: t("Kind of failure: {kind}", { kind: step.failure_class }) }));
      }
      // The stage's own job: its whole output, live while it runs, and its
      // artifacts. A link, so it opens from the keyboard and survives a reload.
      if (step.job_id) {
        line.append(el("a", { className: "stage-detail stage-job", text: t("Open this stage's job and its output"),
          attrs: { href: "#jobs/" + encodeURIComponent(step.job_id) } }));
      }
      const found = findingsList(step);
      if (found) line.append(found);
      if (step.command?.shell) {
        const details = el("details");
        details.append(el("summary", { text: "command" }));
        details.append(el("pre", { className: "output", text: step.command.shell }));
        line.append(details);
      }
      list.append(line);
    }

    const read = whatWasRead(manifest);
    if (read) list.append(read);

    if (manifest.outputs?.length) {
      const outputs = el("li");
      outputs.append(el("span", { className: "stage-name", text: "Artifacts" }));
      for (const output of manifest.outputs) {
        if (output.missing) {
          outputs.append(
            el("span", {
              className: "stage-detail",
              text: `${output.name}: ${output.optional ? "not produced (optional)" : "declared and not produced"}`,
            })
          );
          continue;
        }
        // Addressed by the output's declared name; the file's own name is what
        // it is saved as.
        const filename = (output.path || output.name).split(/[\\/]/).pop();
        outputs.append(
          window.AUCOM.downloadButton(
            filename,
            `/api/v1/build/runs/${encodeURIComponent(manifest.build_id)}/output/${encodeURIComponent(output.name)}`,
            filename
          )
        );

      }
      list.append(outputs);
    }

    const log = $("build-log");
    if (body.log === undefined) {
      // A build this process is not running has no live log: its stages' own
      // job records hold the output, and showing the previous build's log here
      // would be showing somebody the wrong bytes.
      log.textContent = "";
      $("build-log-note").textContent =
        "This build is not running in this Companion. Each stage's own job holds its output.";
    } else {
      const wasAtBottom = log.scrollTop + log.clientHeight >= log.scrollHeight - 8;
      log.textContent = body.log;
      if (wasAtBottom) log.scrollTop = log.scrollHeight;
      $("build-log-note").textContent = body.log_dropped
        ? `Showing the last ${log.textContent.length} characters; ${body.log_dropped} earlier bytes were not kept in this view. Each stage's own job holds the full log.`
        : "Each stage's own job holds the full log.";
    }
  }

  // findingsList is every error and warning a stage's rules recognised, with
  // the compiler's own line beside the rule's sentence. The count alone used to
  // be all the page showed (Q3_007: a missing model and a missing image were
  // "1 error finding(s), 1 warning(s)" and nothing said which).
  function findingsList(step) {
    const findings = (step.diagnostics || []).filter((d) => d.severity === "error" || d.severity === "warning");
    if (!findings.length) return null;
    const details = el("details", { className: "stage-findings" });
    // Open when the stage failed or warned: these are why.
    details.open = true;
    details.append(el("summary", { text: t("Findings ({count})", { count: findings.length }) }));
    const rows = el("ul");
    for (const finding of findings.slice(0, 50)) {
      const row = el("li");
      row.append(badge(finding.severity, finding.severity === "error" ? "failed" : "queued"));
      row.append(document.createTextNode(" " + (finding.message || finding.raw || "")));
      if (finding.class) row.append(el("span", { className: "stage-detail", text: " [" + finding.class + "]" }));
      if (finding.fatal) row.append(el("span", { className: "stage-detail", text: " " + t("This line is what failed the stage.") }));
      if (finding.raw && finding.raw !== finding.message) row.append(el("pre", { className: "output", text: finding.raw }));
      rows.append(row);
    }
    details.append(rows);
    if (findings.length > 50) {
      details.append(el("p", { className: "muted", text: t("{more} more in the stage's job.", { more: findings.length - 50 }) }));
    }
    return details;
  }

  // whatWasRead says what the build compiled and what the compiler was allowed
  // to see (Q3_010): the APMap the `.map` was converted from, the game data
  // staged for a Quake III build, and each package the saved map is bound to
  // with the digest it was verified at. Every value is the manifest's own.
  function whatWasRead(manifest) {
    const lines = [];
    for (const input of manifest.inputs || []) {
      const c = input.conversion;
      if (!c) continue;
      lines.push(t("{name}: converted from the map document {document} (revision {revision}), {digest}", {
        name: input.name, document: c.document_id || c.source_name, revision: c.document_revision, digest: c.source_sha256 }));
      if (c.manifest) {
        lines.push(t("Conversion record: {shaders} shader(s), {models} model(s), {warnings} warning(s) — {digest}", {
          shaders: c.manifest.shaders, models: c.manifest.models, warnings: c.manifest.warnings, digest: c.manifest.sha256 }));
      }
    }
    const data = manifest.game_data;
    if (data) {
      lines.push(t("Game data staged for this build: base folder {base}, mod folder {mod}.", {
        base: data.base_game, mod: data.fs_game || t("none") }));
      for (const root of data.roots || []) {
        for (const game of root.games || []) {
          if (!game.present) continue;
          lines.push(t("{role} › {game}: {archives} archive(s), {loose} loose file(s)", {
            role: root.role === "game_root" ? t("Base game data") : t("Your content"),
            game: game.name, archives: (game.archives || []).length, loose: game.loose_files }));
          for (const archive of game.archives || []) lines.push("    " + archive.name + " — " + archive.sha256);
        }
      }
      for (const bound of data.packages || []) {
        lines.push(bound.staged
          ? t("Bound package {name} — {digest} (staged)", { name: bound.root + "/" + bound.archive_name, digest: bound.sha256 })
          : t("Bound package {name} — {digest} (NOT staged: {reason})", { name: bound.root + "/" + bound.archive_name, digest: bound.sha256, reason: bound.reason }));
      }
      for (const finding of data.findings || []) lines.push(finding.message);
    }
    if (!lines.length && !manifest.failure_class) return null;
    const item = el("li");
    item.append(el("span", { className: "stage-name", text: t("What this build read") }));
    if (manifest.failure_class) {
      item.append(el("span", { className: "stage-detail failure-class", text: t("Kind of failure: {kind}", { kind: manifest.failure_class }) }));
    }
    if (lines.length) {
      const details = el("details", { className: "stage-findings" });
      details.open = Boolean((data?.findings || []).length);
      details.append(el("summary", { text: t("Sources and staged game data") }));
      details.append(el("pre", { className: "output", text: lines.join("\n") }));
      item.append(details);
    }
    return item;
  }

  // reattach opens the build this Companion is running when the page has none
  // open — after a reload, which forgets everything the window knew. The build
  // itself never depended on the page; what a reload lost was the panel with
  // its Cancel button, so a person watching a twenty-minute lighting pass had
  // to find it again in a list (Q3_010, headed run). Only a build running HERE
  // is taken, only when nothing is open, and nothing is started: this reads.
  async function reattach() {
    if (currentBuild) return;
    const { ok, body } = await api("/api/v1/build/runs?limit=25");
    if (!ok) return;
    const live = (body.items || []).find((item) => item.live);
    if (!live) return;
    currentBuild = live.manifest.build_id;
    outcome = live.manifest.state;
    $("build-current-panel").hidden = false;
    setMessage("build-result", "", "");
    showStep(4, { focus: false });
  }

  async function refreshHistory() {
    const list = $("build-history");
    const { ok, body } = await api("/api/v1/build/runs?limit=25");
    list.replaceChildren();
    if (!ok) {
      list.append(el("li", { className: "muted", text: body.error || "could not read past builds" }));
      return;
    }
    const items = body.items || [];
    if (items.length === 0) {
      list.append(el("li", { className: "muted", text: "No builds yet." }));
      return;
    }
    for (const item of items) {
      const manifest = item.manifest;
      const head = el("div", { className: "row-head" });
      head.append(el("strong", { text: manifest.label || manifest.pipeline?.name || "Untitled build" }));
      head.append(badge(manifest.state));
      if (item.live) head.append(badge("running here", "running"));
      const detail = el("p", { className: "muted" });
      detail.textContent = [
        manifest.pipeline?.name,
        manifest.duration_ms ? manifest.duration_ms + " ms" : "",
        when(manifest.started_at),
      ]
        .filter(Boolean)
        .join(" · ");
      const open = el("button", { text: "Open", attrs: { type: "button", class: "secondary" } });
      open.addEventListener("click", () => {
        // Opened from Builds on this machine: the build is watched in Build.
        if ($("area-build").hidden) window.AUCOM.showArea("build");
        currentBuild = manifest.build_id;
        $("build-current-panel").hidden = false;
        outcome = manifest.state;
        setMessage("build-result", "", "");
        showStep(4, { focus: false });
        poll();
        $("build-current-title").setAttribute("tabindex", "-1");
        $("build-current-title").focus();
      });
      list.append(el("li", { children: [head, detail, el("div", { className: "row-actions", children: [open] })] }));
    }
  }

  // applyLocalMap puts the file chosen in My Maps into the map field of the
  // pipeline on screen — again after a pipeline change, because the rows are
  // drawn per pipeline. A field somebody has since typed something else into
  // is theirs and is left alone.
  function applyLocalMap() {
    if (!localMap) return;
    for (const row of inputFields.values()) {
      if (row.kind !== "map") continue;
      const typed = row.file.input.value.trim();
      if (typed && typed !== localMap.path) continue;
      row.source.value = "file";
      row.file.input.value = localMap.path;
      row.apply();
    }
  }

  // A map and a WAD chosen for one pipeline stay chosen when another is picked:
  // inputs are matched by name, so only what both pipelines declare carries
  // over (NEW_244D rehearsal: switching fast preview to normal emptied the map).
  $("build-pipeline").addEventListener("change", () => {
    const kept = keptInputs();
    renderPipeline();
    restoreInputs(kept);
    applyLocalMap();
    choicesChanged();
  });
  for (const id of ["build-inputs", "build-strict"]) {
    $(id).addEventListener("input", choicesChanged);
    $(id).addEventListener("change", choicesChanged);
  }
  for (const tab of document.querySelectorAll("#build-steps .bwiz-step")) {
    // Step 3 is where a build is started AND watched: once there is one, its
    // tab shows it; "Build this again" goes back to the commands.
    tab.addEventListener("click", () => {
      const n = Number(tab.dataset.step);
      showStep(n === 3 && currentBuild ? 4 : n);
    });
  }
  for (const go of document.querySelectorAll("#area-build [data-go]")) {
    go.addEventListener("click", () => showStep(Number(go.dataset.go)));
  }
  $("build-preview").addEventListener("click", (event) => {
    checked = null;
    preview(event.currentTarget);
  });
  $("build-start").addEventListener("click", (event) => start(event.currentTarget));
  $("build-history-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshHistory));
  $("build-play").addEventListener("click", async () => {
    if (!currentBuild) return;
    window.AUCOM.showArea("run");
    await window.AUCOM.areas.run?.chooseBuild?.(currentBuild);
  });
  $("build-cancel").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      if (!currentBuild) return;
      const { ok, body } = await api(`/api/v1/build/runs/${encodeURIComponent(currentBuild)}/cancel`, {
        method: "POST",
      });
      if (!ok) {
        setMessage("build-message", body.error || "could not cancel this build", "error");
        return;
      }
      setMessage("build-message", "Stopping. The stage records what happened, and the manifest is completed rather than abandoned.", "");
      // Not recorded here: the build's own end state is, once, when the poll
      // reads it ("Build cancelled: <label>"). Recording the press as well put
      // two lines in Activity for one cancel.
    })
  );

  window.AUCOM.areas.build = {
    // From My Maps: a revision chosen there is a new choice for step 2. The
    // same redraw run by renderPipeline is not, or re-drawing the inputs while
    // a check was out would un-check the choices it was checking.
    revisionChosen() {
      revisionChosen();
      // The map input takes the chosen revision. Choosing one in My Maps and
      // then having to find a "Where Map source comes from" select as well was
      // two acts for one decision (NEW_244D, with the operator's own install).
      const rows = [...inputFields.values()];
      const mapRow = rows.find((row) => /map/i.test(row.input.name) && !/wad/i.test(row.input.name)) || rows[0];
      if (mapRow && window.AUCOM.chosenRevision) {
        mapRow.source.value = "asset";
        mapRow.apply();
      }
      choicesChanged();
    },
    // From My Maps › On this computer: a map file of one's own, into the
    // ordinary wizard at its Map step. Nothing else about the build changes —
    // the pipeline, the WAD folder and the check are the wizard's own.
    async useLocalFile(path) {
      const name = String(path).split(/[\\/]/).pop();
      localMap = { path, name };
      window.AUCOM.showArea("build");
      if (!pipelines.length) await refreshPipelines();
      applyLocalMap();
      if (!$("build-label").value.trim()) $("build-label").value = t("Local file: {name}", { name });
      choicesChanged();
      showStep(2);
      const row = [...inputFields.values()].find((candidate) => candidate.kind === "map");
      if (!row) {
        setMessage("build-message", t("The build profile chosen in step 1 takes no map file. Choose one that does; {name} stays chosen.", { name }), "error");
        showStep(1);
      }
    },
    adoptLeakRequest,
    async refresh() {
      await refreshPipelines();
      applyLocalMap();
      await refreshHistory();
      await reattach();
      renderSteps();
      // Back from Profiles after "Set up …": the check that sent the person
      // there is out of date, so it runs again rather than waiting for a click.
      if (step === 3 && checked !== "ok") {
        checked = null;
        preview($("build-preview"));
      }
      if (currentBuild) poll();
    },
    showStep,
  };
  window.AUCOM.areas.builds = { refresh: refreshHistory };
})();
