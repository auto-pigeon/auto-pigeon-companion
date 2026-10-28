// A file on this computer, in Build & Run (NEW_363: until then the separate
// Build wizard): its inputs, the build profiles that take them, the check of
// the exact commands, and the build's stage progress, bounded live output,
// cancellation, warnings and artifacts. The form around it is play.js's.
//
// The page never holds a build's state as the truth. It starts one, is given an
// id, and from then on polls the manifest the runner writes after every stage.
// That is what makes this area survive a reload: everything it shows is on
// disk, so a page that has just been refreshed and a page that has been open
// the whole time show the same thing.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, when, terminal,
    maturityBadge, maturityNote, openCompatibilityReport, duration, t } = window.AUCOM;

  let pipelines = [];
  let pipelineRefresh = 0;
  // The pipeline the USER chose, kept across refreshes, reloads and a pipeline
  // losing its readiness (NEW_323E). Without it a choice that stopped being
  // ready emptied the list, and the next refresh put the first ready pipeline
  // there: a different pipeline behind the user's choice. Storage can be absent
  // or refused; then the choice lasts as long as the page.
  const CHOSEN_PIPELINE = "aucom.build.pipeline";
  let chosenPipeline = "";
  try { chosenPipeline = window.localStorage.getItem(CHOSEN_PIPELINE) || ""; } catch { /* not kept */ }
  function rememberPipeline(id) {
    chosenPipeline = id || "";
    try {
      if (chosenPipeline) window.localStorage.setItem(CHOSEN_PIPELINE, chosenPipeline);
      else window.localStorage.removeItem(CHOSEN_PIPELINE);
    } catch { /* not kept */ }
  }
  let inputFields = new Map();
  let currentBuild = null;
  let poller = null;
  // The last answer drawn, so a choice made on the page (fold or unfold the
  // output) redraws at once instead of waiting for the next poll.
  let lastBody = null;
  // `checked` is what the last check found for the choices as they are now —
  // "ok", "blocked", or null once anything changed after it; it is what the
  // one button says ("Review build" until "ok", then "Build"). `outcome` is the
  // state of the build the progress panel is showing.
  let checked = null;
  // reviewedSetup is the setup the last check was made against (setupKey):
  // which build profile, at which version, with which tools and what they
  // were ready for. A check is a statement about THAT request; once the setup
  // under it changes — a program set up in Profiles, a tool rebound — it says
  // nothing about the new one, and the one button goes back to "Review build"
  // (NEW_365, HITL review finding 3).
  let reviewedSetup = "";
  // checkedWhy is the one sentence a refused check said; starting is true from
  // the press until the server answered it.
  let checkedWhy = "";
  let starting = false;
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
    if (!pipeline || !window.AUCOM.pipelineReady(pipeline)) {
      if (pipeline) return "This leak-test pipeline needs setup: " + (pipeline.readiness?.problems || []).map(p => p.summary).join(" ") + " Set it up in Profiles, then review again.";
      return body.game_profile === "quake3"
        ? t("The Quake III leak-test pipeline is unavailable on this computer. Set up Q3Map2 in Profiles, then review this request again.")
        : t("The leak-test pipeline is unavailable on this computer. Set up a Quake 1 compiler (qbsp) in Profiles, then review this request again.");
    }
    const name = body.name || body.asset_id;
    $("build-pipeline").value = body.pipeline;
    rememberPipeline(body.pipeline);
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
    const compile = (manifest.steps || []).find((step) => step.id === (leak?.compile_step || "compile"));
    // A compiler whose run the Companion reads itself (qbsp for Quake II, Q3Map2) comes with its
    // reading. Three facts stay apart in the sentence: how the process exited,
    // how the step ended, and what the run says about leaks — a leaked Quake
    // III map is exit 0, a failed step and a leak, all at once (Q3_018).
    if (leak?.diagnostic && leak.compiler === "ericw-qbsp-q2") {
      // Quake II (Q2_010): qbsp in its Quake II mode. A leak exits 1; a map
      // with nothing to flood, or a texture it could not open, exits 0 with a
      // BSP — measured, so neither the exit nor the BSP is the verdict.
      const how = t("qbsp exited {code}; the compile step ended as {state}.", {
        code: compile?.exit_code ?? "—", state: compile?.state || t("not run") });
      const evidence = leak.diagnostic.evidence || [];
      const said = {
        leak: evidence.includes("route")
          ? t("qbsp found a leak in this saved revision and wrote a point file (.pts) with {n} points, running from the entity it reached to outside the map.", { n: leak.diagnostic.route_points })
          : t("qbsp found a leak in this saved revision, and left no usable point file: there is no route to draw."),
        no_leak: t("qbsp found no leak in this saved revision. That is a statement about this run, not proof the map is sealed."),
        no_interior: t("Not tested: no entity stands in open space — there is none, or every one is inside a solid brush — so qbsp flooded nothing. It still wrote a BSP and exited 0; that is not a seal."),
        incomplete: t("No verdict: the run does not say whether the map leaks ({why}). Its output says more.", {
          why: evidence.join(", ") || t("unknown") }),
      }[leak.diagnostic.outcome] || t("No verdict.");
      const notes = [];
      if (evidence.includes("texture_missing")) {
        notes.push(t("qbsp could not open a texture this map names (its .wal), so it never read that face's contents: a room it closes is not called sealed."));
      }
      if (evidence.includes("brush_invalid")) {
        notes.push(t("qbsp dropped a brush it could not read; this run is about the map without it."));
      }
      if (evidence.includes("version_unqualified")) {
        notes.push(t("This qbsp is not the build this reading was measured on (the Auto-Pigeon build of ericw-tools, release 1.4518)."));
      }
      return [said, how, ...notes].join(" ");
    }
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
    const sequence = ++pipelineRefresh;
    const { ok, body } = await api("/api/v1/build/pipelines");
    // Read AFTER the await, not before: a refresh that started while the user
    // was choosing a pipeline must not put back the one that was selected
    // when it started (NEW_244D found two overlapping refreshes doing exactly
    // that, and a build then ran a pipeline nobody chose).
    if (sequence !== pipelineRefresh) return;
    // Nothing chosen for a file yet: the build profile chosen for a saved map
    // carries over, when it takes a file too (NEW_363: one form).
    const previous = select.value || chosenPipeline || window.AUCOM.areas.play?.pipeline?.() || "";
    // What the user already filled in survives a refresh of the same pipeline.
    // Leaving Build to set a compiler up in Profiles and coming back used to
    // come back to an empty map field.
    const kept = previous ? keptInputs() : null;
    if (!ok) {
      pipelines = [];
      select.replaceChildren();
      renderPipeline();
      setMessage("build-message", body.error || "could not read the pipelines", "error");
      return;
    }
    pipelines = body.items || [];
    window.AUCOM.executionChoices(select, pipelines, previous, window.AUCOM.pipelineReady,
      // What an unready pipeline waits for is said once, under the list, in
      // a closed "Other pipelines" disclosure (NEW_345) — not in its option,
      // where every problem made one line of the select hundreds of words long.
      (pipeline) => `${pipeline.name}${pipeline.maturity?.work_in_progress ? ` [${pipeline.maturity.badge}]` : ""} — ${window.AUCOM.pipelineReady(pipeline) ? pipeline.summary : t("needs setup")}`,
      "Choose a ready pipeline…", true, true, undefined, { setupLabel: "pipelines" });
    renderPipeline();
    if (kept && select.value === previous) restoreInputs(kept);
    if (checked !== null && reviewStale(reviewedSetup, currentPipeline())) forgetReview();
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
    if (!pipeline) {
      $("build-pipeline-note").textContent = "Choose a ready pipeline. Set up its dependencies in Profiles, then Refresh.";
      renderSteps();
      return;
    }

    $("build-pipeline-note").textContent = pipeline.runnable
      ? t("{n} stage(s). Version {version}.", { n: pipeline.steps.length, version: pipeline.version })
      : "This pipeline cannot run here: no tool installed on this machine does " +
        (pipeline.missing_capabilities.length === 1 ? "one of its stages" : `${pipeline.missing_capabilities.length} of its stages`) +
        ". Install the tool that does, then approve it in Profiles.";
    $("build-pipeline-note").className = pipeline.runnable ? "muted" : "message error";
    if (pipeline.runnable && !window.AUCOM.pipelineReady(pipeline)) {
      // Still selected because it is the one that was chosen; nothing can be
      // previewed or started with it until it is set up.
      $("build-pipeline-note").textContent = t("This is the pipeline you chose, and it needs setup: {problems} Nothing else was chosen for you. Set it up in Profiles, or choose another pipeline.",
        { problems: (pipeline.readiness?.problems || []).map((p) => p.summary).join(" ") });
      $("build-pipeline-note").className = "message error";
    }

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
      accepts: kind === "map" ? savedRevisionNote : undefined,
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

  // The one button and the sentence beside it (NEW_365, operator): the button
  // always says Build, and is enabled once these exact choices, on this exact
  // setup, have been checked and nothing stops them. Until then the sentence
  // beside it says, visibly and once, that the setup is being checked or what
  // stops it, with the link that fixes it — never a tooltip, and never a second
  // press to "review". The check (POST /api/v1/build/preview) starts nothing.
  // The sentence renderPrimary/renderSteps last wrote beside the button.
  let buildWritten = "";
  function renderSteps() {
    const button = $("build-start");
    button.textContent = t("Build");
    button.setAttribute("aria-describedby", "build-start-message");
    const blocked = buildBlocked();
    button.disabled = Boolean(blocked) || starting;
    const status = $("build-start-message");
    if (!status || starting) return;
    if (blocked?.checking) scheduleCheck();
    // Redrawn only when what it says changes (see play.js renderPrimary).
    const key = !blocked ? "" : blocked.checking ? "checking" : `${blocked.text}|${blocked.link?.getAttribute?.("href") || ""}`;
    // Skipped only while the sentence on screen is still the one written here:
    // another writer (a change clearing it) makes it drawn again.
    if (status.dataset.shown === key && status.textContent === buildWritten) return;
    status.dataset.shown = key;
    if (!blocked) {
      setMessage(status, "");
    } else if (blocked.checking) {
      busy(status, t("Checking setup…"));
    } else {
      setMessage(status, blocked.text, blocked.error ? "error" : "");
      if (blocked.link) status.append(" ", blocked.link);
    }
    buildWritten = status.textContent;
  }

  // buildBlocked is what stops the button now, or null.
  function buildBlocked() {
    const pipeline = currentPipeline();
    if (!pipeline) return { text: t("Choose a ready build profile first.") };
    if (!window.AUCOM.pipelineReady(pipeline)) {
      const cause = (window.AUCOM.setupCauses?.(pipeline) || [])[0];
      return { text: t("The chosen build profile needs setup first."), link: cause ? window.AUCOM.setupLink(cause) : null };
    }
    if (missingRequired().length > 0 || chosenFiles().length === 0) return { text: t("Choose the map file first.") };
    if (outcome === "running") return { text: t("A build is running; it can be cancelled below.") };
    if (checked === "blocked") return { text: checkedWhy || t("This build cannot start yet; what is missing is said in Technical details."), error: true };
    if (checked !== "ok") return { checking: true };
    return null;
  }

  // The check runs by itself once the choices are complete and unchecked:
  // a quarter of a second after the last change, one at a time (preview()
  // coalesces), and a late answer for older choices never marks these checked.
  let checkTimer = null;
  function scheduleCheck() {
    clearTimeout(checkTimer);
    checkTimer = setTimeout(() => {
      const blocked = buildBlocked();
      if (blocked?.checking) preview($("build-start")).then(renderSteps);
    }, 250);
  }

  // showStep (kept for the callers it had as a wizard: My Maps, the leak-test
  // request, a refused input) brings the part of the form that step was to
  // the reader: 1 the build profile, 2 the map, 3 the check, 4 the progress.
  function showStep(n, { focus = true } = {}) {
    renderSteps();
    const target = { 1: "build-pipeline", 2: "build-inputs", 3: "build-start", 4: "build-step-4" }[n];
    const node = $(target);
    if (n === 4) $("build-step-4").hidden = !currentBuild;
    if (!node || !focus) return;
    node.scrollIntoView({ block: "nearest" });
    const control = n === 2 ? node.querySelector("input, select, button") : node;
    if (n === 4) {
      $("build-current-title").setAttribute("tabindex", "-1");
      $("build-current-title").focus({ preventScroll: true });
    } else control?.focus?.({ preventScroll: true });
  }

  // A change event that leaves the request exactly as it was checked — the
  // `change` a text field fires when the pointer leaves it for the Build
  // button — keeps the check: dropping it disabled the button under the click
  // (NEW_365, seen live). Anything that did change the request drops it.
  let checkedRequest = "";
  function choicesChanged() {
    if (checked !== null && checkedRequest && JSON.stringify(requestBody()) === checkedRequest &&
      !reviewStale(reviewedSetup, currentPipeline())) {
      return;
    }
    generation += 1;
    checked = null;
    renderSteps();
  }

  // setupKey is what a review depends on besides the form: the build profile
  // and version, every stage's tool, and the readiness the server decided for
  // it. Nothing here is the form's own choices; those are `generation`.
  function setupKey(pipeline) {
    if (!pipeline) return "";
    return JSON.stringify([pipeline.id, pipeline.version, pipeline.digest || "",
      (pipeline.steps || []).map((step) => [step.id, step.provider?.id || "", step.provider?.version || "",
        step.provider_version || "", step.provider_authorized === true, step.provider_bound === true]),
      pipeline.readiness || null]);
  }

  // reviewStale reports whether the review on screen was made against another
  // setup than the one chosen now.
  function reviewStale(reviewed, pipeline) {
    return reviewed !== "" && reviewed !== setupKey(pipeline);
  }

  // forgetReview drops a review the setup has moved out from under: the
  // commands it showed are not what would run now, so they are not left on
  // screen under a button that would start them.
  function forgetReview() {
    generation += 1;
    checked = null;
    reviewedSetup = "";
    const out = $("build-preview-out");
    out.hidden = true;
    out.replaceChildren();
    checkedWhy = "";
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

  // A map input also takes a saved revision of the account, written
  // `aub:map/<id>@<revision>#<file>` — the build fetches and verifies it
  // (internal/assetref). Typed into the field, it was answered "is not an
  // absolute path" while the build accepted it (NEW_353A, O6). Only the shape
  // is judged here; whether the revision exists is the build's to say.
  function savedRevisionNote(value) {
    return /^aub:map\/[A-Za-z0-9_-]+@[A-Za-z0-9_-]+(#\S+)?$/.test(value)
      ? t("A saved map revision from your account. The build downloads it and checks its digest.")
      : "";
  }

  function requestBody() {
    const pipeline = currentPipeline();
    const inputs = {};
    const roots = {};
    for (const [name, row] of inputFields) {
      if (row.source.value === "asset") {
        const chosen = window.AUCOM.chosenRevision;
        if (!chosen) continue;
        const file = row.fileChoice.value ? "#" + row.fileChoice.value : "";
        inputs[name] =
          `aub:${chosen.asset_type}/${chosen.asset_id}@${chosen.revision_id || "current"}${file}`;
      } else if (row.file.input.value.trim()) {
        // NEW_322: a textures row is a FOLDER (246I), and a folder is a root,
        // never an input — an input is one file. It used to be sent as
        // `inputs.wad`, so the check said "Everything is in place" and the
        // build then failed with "is not a regular file". It travels as the
        // build's own `content_root`, the folder the compile stage is given
        // with -wadpath, for this build only.
        if (row.kind === "textures") roots.content_root = row.file.input.value.trim();
        else inputs[name] = row.file.input.value.trim();
      }
    }
    return {
      pipeline: pipeline?.id || "",
      inputs,
      roots: Object.keys(roots).length ? roots : undefined,
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
    if (!checking) {
      checking = (async () => {
        // Choices that changed while a check was out (a chosen revision's file
        // list arriving, say) are checked again, a bounded number of times.
        for (let round = 0; round < 3; round += 1) {
          const asked = generation;
          await runPreview(button, asked);
          if (asked === generation) break;
        }
      })().finally(() => { checking = null; });
    }
    return checking;
  }

  async function runPreview(button, asked) {
    await (async () => {
      const { ok, body } = await api("/api/v1/build/preview", { method: "POST", body: requestBody() });
      const out = $("build-preview-out");
      out.hidden = false;
      out.replaceChildren();
      // A result for choices that have changed since is still shown — it is
      // what was asked — but it does not mark the new choices checked.
      const verdict = (value) => {
        if (asked !== generation) return null;
        reviewedSetup = setupKey(currentPipeline());
        checkedRequest = JSON.stringify(requestBody());
        return value;
      };
      if (!ok) {
        checked = verdict("blocked");
        if (checked) checkedWhy = explain(body.error)?.advice || sentenceOf(body.error) || t("the check failed");
        // A refused check is about THESE choices; the last build's outcome is
        // not theirs, and left on the step's tab it read "succeeded" over a
        // refusal.
        outcome = null;
        renderSteps();
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
      if (checked === "blocked") {
        const first = blocked[0];
        checkedWhy = t("{stage}: {why}", { stage: first.title || first.id,
          why: explain(first.error, first)?.advice || sentenceOf(first.error) });
      }
      renderSteps();
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
    })();
  }

  async function start(button) {
    if (starting) return;
    starting = true;
    button.disabled = true;
    $("build-start-message").dataset.shown = "pressed";
    busy("build-start-message", t("Starting…"));
    await (async () => {
      const { ok, body } = await api("/api/v1/build/runs", { method: "POST", body: requestBody() });
      starting = false;
      if (!ok && body.code === "already_running" && body.build) {
        // Another press or another tab started exactly this: follow that one.
        currentBuild = body.build;
        outcome = "running";
        $("build-current-panel").hidden = false;
        $("build-step-4").hidden = false;
        renderSteps();
        setMessage("build-start-message", t("This build is already running. Its progress is below."), "");
        poll();
        return;
      }
      if (!ok) {
        checked = "blocked";
        checkedWhy = explain(body.error)?.advice || sentenceOf(body.error) || t("the build could not be started");
        renderSteps();
        // The refusal's own words, whole, in Activity — and its kind, when the
        // Companion classified it (Q3_010: an extractor refusal such as
        // `q3map_shader_unsafe` never becomes a build, so this line and the
        // message above are the only places it can be read).
        record("Build could not be started", (body.class ? "[" + body.class + "] " : "") + sentenceOf(body.error), "failed");
        return;
      }
      outcome = "running";
      setMessage("build-start-message", t("Started. Progress is below."), "ok");
      setMessage("build-result", "", "");
      record(`Build started: ${$("build-label").value.trim() || currentPipeline()?.name || "untitled"}`, "", "running");
      currentBuild = body.build;
      leakRequestID = null;
      $("build-current-panel").hidden = false;
      $("build-step-4").hidden = false;
      $("build-current-title").textContent = "Building " + ($("build-label").value.trim() || currentPipeline()?.name || "");
      $("build-current-title").setAttribute("tabindex", "-1");
      $("build-current-title").focus();
      renderSteps();
      setMessage("build-start-message", t("Started. Progress is below."), "ok");
      poll();
    })();
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
              // The manifest's own sentence: "the Companion stopped while this
              // build was running", or — for a build whose record was copied,
              // moved or could not be verified — that, and that nothing was
              // stopped. A copy must not claim the original's Companion stopped.
              ? `This build was interrupted: ${body.manifest.error || "nothing here knows how it ended"}. Build it again when you are ready.`
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

  const since = (stamp) => {
    const at = Date.parse(stamp || "");
    return Number.isFinite(at) && at > 0 ? Math.max(0, Date.now() - at) : NaN;
  };

  // What somebody opened or closed stays that way. The panel is rebuilt from
  // the manifest on every poll, so a <details> made fresh each time snapped
  // back to its default seven times in five seconds; the choice is kept here,
  // per build, and given back to the element that replaces the one it was made on.
  const openChoices = new Map();
  function sticky(details, key, openByDefault) {
    const id = `${currentBuild}:${key}`;
    details.open = openChoices.has(id) ? openChoices.get(id) : Boolean(openByDefault);
    details.addEventListener("toggle", () => openChoices.set(id, details.open));
    return details;
  }

  // foldRepeats shortens a run of consecutive lines that differ only in their
  // numbers — a compiler that warns once per entity prints the same sentence
  // forty times — to its first two lines and a count. Nothing is reordered and
  // nothing else is touched; the stage's own job always holds every line.
  function foldRepeats(text) {
    const lines = String(text).split("\n");
    const shape = (line) => line.replace(/-?\d+(?:\.\d+)?/g, "#");
    const out = [];
    let hidden = 0;
    for (let at = 0; at < lines.length;) {
      let end = at + 1;
      const key = shape(lines[at]);
      while (end < lines.length && key.trim() && shape(lines[end]) === key) end += 1;
      if (end - at >= 4) {
        out.push(lines[at], lines[at + 1], "    " + t("… {n} more lines like the two above", { n: end - at - 2 }));
        hidden += end - at - 2;
      } else for (let k = at; k < end; k += 1) out.push(lines[k]);
      at = end;
    }
    return { text: out.join("\n"), hidden };
  }

  // problemGroups is every error and warning of every stage, the same finding
  // counted once: errors first, then the most repeated. A stage that warned
  // forty times about forty lights is one row that says forty.
  function problemGroups(manifest) {
    const groups = new Map();
    for (const step of manifest.steps || []) {
      for (const finding of step.diagnostics || []) {
        if (finding.severity !== "error" && finding.severity !== "warning") continue;
        const key = [finding.severity, finding.class || "", finding.rule_id || "", finding.message || finding.raw || ""].join("\u0000");
        let group = groups.get(key);
        if (!group) {
          group = { severity: finding.severity, message: finding.message || finding.raw || "", class: finding.class, hint: finding.hint,
            fatal: false, count: 0, stages: [], raws: [] };
          groups.set(key, group);
        }
        group.count += 1;
        group.fatal = group.fatal || Boolean(finding.fatal);
        const stage = step.title || step.id;
        if (!group.stages.includes(stage)) group.stages.push(stage);
        if (finding.raw && finding.raw !== finding.message && !group.raws.includes(finding.raw)) group.raws.push(finding.raw);
      }
    }
    return [...groups.values()].sort((a, b) =>
      (a.severity === b.severity ? 0 : a.severity === "error" ? -1 : 1) || Number(b.fatal) - Number(a.fatal) || b.count - a.count);
  }

  function renderProblems(manifest) {
    const groups = problemGroups(manifest);
    const section = $("build-problems");
    const list = $("build-problems-list");
    list.replaceChildren();
    section.hidden = !groups.length;
    if (!groups.length) return;
    const errors = groups.filter((g) => g.severity === "error").reduce((n, g) => n + g.count, 0);
    const warnings = groups.filter((g) => g.severity === "warning").reduce((n, g) => n + g.count, 0);
    $("build-problems-heading").textContent = errors && warnings
      ? t("Errors and warnings — {errors} error(s), {warnings} warning(s)", { errors, warnings })
      : errors ? t("Errors — {errors}", { errors }) : t("Warnings — {warnings}", { warnings });
    const SHOWN = 30;
    groups.slice(0, SHOWN).forEach((group, index) => {
      const row = el("li", { className: "problem " + group.severity });
      const head = el("div", { className: "problem__head" });
      head.append(badge(group.severity, group.severity === "error" ? "failed" : "warning"));
      head.append(el("span", { className: "problem__message", text: group.message }));
      if (group.count > 1) head.append(el("span", { className: "problem__count", text: t("× {n}", { n: group.count }) }));
      row.append(head);
      const meta = [t("in {stages}", { stages: group.stages.join(", ") })];
      if (group.class) meta.push("[" + group.class + "]");
      if (group.fatal) meta.push(t("This is what failed the stage."));
      row.append(el("div", { className: "problem__meta muted", text: meta.join(" · ") }));
      if (group.raws.length || group.hint) {
        // Open for an error, closed for a warning: the sentence above is the
        // finding, and this is the evidence and the remedy for whoever wants them.
        const details = sticky(el("details", { className: "problem__more" }), "problem:" + index + ":" + group.message, group.severity === "error");
        details.append(el("summary", { text: group.raws.length
          ? t("The compiler's own line, and what to do") : t("What to do") }));
        if (group.raws.length) {
          details.append(el("pre", { className: "output", text: group.raws.slice(0, 5).join("\n")
            + (group.raws.length > 5 ? "\n" + t("… and {n} more different lines in the stage's job", { n: group.raws.length - 5 }) : "") }));
        }
        if (group.hint) details.append(el("p", { className: "muted stage-finding-hint", text: group.hint }));
        row.append(details);
      }
      list.append(row);
    });
    const more = $("build-problems-more");
    more.hidden = groups.length <= SHOWN;
    more.textContent = groups.length > SHOWN ? t("{more} more kinds of finding are in the stages' own jobs.", { more: groups.length - SHOWN }) : "";
  }

  function renderProgress(body) {
    const manifest = body.manifest;
    const list = $("build-progress");
    list.replaceChildren();
    $("build-cancel").disabled = !body.live;
    // A finished build has nothing to cancel; the button is not left standing
    // there disabled and red.
    $("build-cancel").hidden = !body.live;
    $("build-output-heading").textContent = body.live ? t("Output so far") : t("Output");
    $("build-step-4").hidden = false;
    // Play this build: a finished build with a level goes straight to Run.
    // A Quake III map is not played by dropping its BSP into a mod folder: its
    // textures, shaders and models have to travel with it. That build gets the
    // Package → Install → Run panel instead (q3package.js), which is told about
    // every render and ignores the ones for a build it is already showing.
    // A Quake II map is packaged too (Q2_009): a PAK in a mod folder of its
    // own, run by an engine that has to say it opened that package.
    const packaged = manifest.engine_family === "quake3" || manifest.engine_family === "quake2";
    $("build-play").hidden = packaged || !(manifest.state === "succeeded" &&
      (manifest.outputs || []).some((output) => output.name === "bsp" && output.path && !output.missing));
    window.AUCOM.q3package?.show(manifest, Boolean(body.live));
    if (body.live) {
      outcome = manifest.state;
      renderSteps();
    }
    // The title is what was built and the badge is how it stands: the state is
    // not buried at the end of a sentence.
    const title = $("build-current-title");
    title.replaceChildren(document.createTextNode((manifest.label || manifest.pipeline?.name || t("this build")) + " "), badge(manifest.state));

    // One line: where the build is, and for how long.
    const steps = manifest.steps || [];
    const runningAt = steps.findIndex((step) => step.state === "running");
    const whole = body.live ? since(manifest.started_at) : Date.parse(manifest.finished_at || "") - Date.parse(manifest.started_at || "");
    const status = $("build-status");
    if (body.live && runningAt >= 0) {
      const elapsed = since(steps[runningAt].started_at);
      status.textContent = t("Stage {n} of {total} — {title}", { n: runningAt + 1, total: steps.length, title: steps[runningAt].title || steps[runningAt].id })
        + (Number.isFinite(elapsed) ? " · " + t("running for {time}", { time: duration(elapsed) }) : "")
        + (Number.isFinite(whole) ? " · " + t("the whole build so far: {time}", { time: duration(whole) }) : "");
    } else if (body.live) {
      status.textContent = t("Starting…");
    } else {
      const ran = steps.filter((step) => step.state).length;
      status.textContent = t("{ran} of {total} stage(s) ran", { ran, total: steps.length })
        + (Number.isFinite(whole) && whole > 0 ? " · " + t("{time} in all", { time: duration(whole) }) : "");
    }

    const leak = $("build-leak");
    leak.replaceChildren();
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
      leak.append(item);
      renderLeakReturn(manifest, item);
    }
    leak.hidden = !leak.childElementCount;

    // The finished build is the other moment a compatibility report is worth
    // offering: the user has just seen what happened and has the build id that
    // fills in the diagnostics. The manifest carries the family (see
    // internal/build/manifest.go), so this needs no second lookup.
    // The note has a place of its own (`#build-note`) and that place is emptied
    // before it is filled: every poll of a build used to add another copy —
    // three on a three-poll build, seven on a slower one (operator, 2026-10-01)
    // — and, sitting inside the head, it pushed Cancel out of the title's row.
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
    $("build-note").replaceChildren(...(body.maturity_message && buildNote ? [buildNote] : []));

    renderProblems(manifest);

    for (const step of steps) {
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
      line.append(el("span", { className: "stage-detail stage-tool", text: provider }));
      const elapsed = step.state === "running" ? since(step.started_at) : step.duration_ms;
      line.append(el("span", { className: "stage-detail stage-time", text: elapsed ? duration(elapsed) : "",
        attrs: step.duration_ms ? { title: `${step.duration_ms} ms` } : {} }));
      const warnings = (step.diagnostics || []).filter((d) => d.severity === "warning").length;
      const errors = (step.diagnostics || []).filter((d) => d.severity === "error").length;
      // Only what there is: "0 error finding(s), 1 warning(s)" made a reader subtract.
      const counts = [];
      if (errors) counts.push(t("{n} error finding(s)", { n: errors }));
      if (warnings) counts.push(t("{n} warning(s)", { n: warnings }));
      line.append(el("span", { className: "stage-detail stage-count" + (errors ? " stage-error" : ""), text: counts.join(", ") }));
      // The stage's own job: its whole output, live while it runs, and its
      // artifacts. A link, so it opens from the keyboard and survives a reload.
      line.append(step.job_id
        ? el("a", { className: "stage-detail stage-job", text: t("Job and full output"), attrs: { href: "#jobs/" + encodeURIComponent(step.job_id) } })
        : el("span", { className: "stage-detail stage-job" }));
      if (step.error && !pending) line.append(el("span", { className: "stage-detail stage-wide stage-error", text: step.error }));
      // What KIND of failure it was, as the token the manifest carries
      // (Q3_010): "the map leaks" and "the compiler is not installed" are told
      // apart by this, not by their wording.
      if (step.failure_class && !pending) {
        line.append(el("span", { className: "stage-detail stage-wide failure-class", text: t("Kind of failure: {kind}", { kind: step.failure_class }) }));
      }
      if (step.command?.shell) {
        const details = sticky(el("details", { className: "stage-wide" }), "command:" + step.id, false);
        details.append(el("summary", { text: t("The command this stage ran") }));
        details.append(el("pre", { className: "output", text: step.command.shell }));
        line.append(details);
      }
      list.append(line);
    }

    const extras = $("build-extras");
    extras.replaceChildren();
    const read = whatWasRead(manifest);
    if (read) extras.append(read);

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
      extras.prepend(outputs);
    }
    $("build-files").hidden = !extras.childElementCount;

    const log = $("build-log");
    if (body.log === undefined) {
      // A build this process is not running has no live log: its stages' own
      // job records hold the output, and showing the previous build's log here
      // would be showing somebody the wrong bytes.
      log.textContent = "";
      log.hidden = true;
      $("build-log-fold").parentElement.hidden = true;
      $("build-log-note").textContent =
        "This build is not running in this Companion. Each stage's own job holds its output.";
    } else {
      log.hidden = false;
      $("build-log-fold").parentElement.hidden = false;
      const wasAtBottom = log.scrollTop + log.clientHeight >= log.scrollHeight - 8;
      const folded = $("build-log-fold").checked ? foldRepeats(body.log) : { text: body.log, hidden: 0 };
      // Only when it changed: writing the same text again throws away a selection somebody is copying.
      if (log.textContent !== folded.text) log.textContent = folded.text;
      if (wasAtBottom) log.scrollTop = log.scrollHeight;
      const notes = [];
      if (folded.hidden) notes.push(t("{n} repeated line(s) are folded above; untick the box to see every one.", { n: folded.hidden }));
      notes.push(body.log_dropped
        ? `Showing the last ${body.log.length} characters; ${body.log_dropped} earlier bytes were not kept in this view. Each stage's own job holds the full log.`
        : "Each stage's own job holds the full log.");
      $("build-log-note").textContent = notes.join(" ");
    }
    lastBody = body;
  }

  // whatWasRead says what the build compiled and what the compiler was allowed
  // to see (Q3_010): the APMap the `.map` was converted from, the game data
  // staged for a Quake III build, and each package the saved map is bound to
  // with the digest it was verified at. Every value is the manifest's own.
  function whatWasRead(manifest) {
    const lines = readLines(manifest);
    const data = manifest.game_data;
    if (!lines.length && !manifest.failure_class) return null;
    const item = el("li");
    item.append(el("span", { className: "stage-name", text: t("What this build read") }));
    if (manifest.failure_class) {
      item.append(el("span", { className: "stage-detail failure-class", text: t("Kind of failure: {kind}", { kind: manifest.failure_class }) }));
    }
    if (lines.length) {
      const details = sticky(el("details", { className: "stage-findings stage-wide" }), "read", Boolean((data?.findings || []).length));
      details.append(el("summary", { text: t("Sources and staged game data") }));
      details.append(el("pre", { className: "output", text: lines.join("\n") }));
      item.append(details);
    }
    return item;
  }

  // readLines is that panel's text, one fact a line, and touches no element —
  // so it can be run without a page (testdata/buildread.check.mjs).
  function readLines(manifest) {
    const lines = [];
    for (const input of manifest.inputs || []) {
      // The account's own name for what was built (Q3_012A). The document id
      // and revision on the next line are the APMap's: a map saved three times
      // read "revision 0" there and the panel named no account revision at all.
      const s = input.source;
      if (s && s.asset_id) {
        lines.push(s.revision_id
          ? t("{name}: from your account — {type} {title} ({asset}), account revision {revision}, revision id {revision_id}", {
            name: input.name, type: s.asset_type, title: s.display_name || s.asset_id, asset: s.asset_id, revision: s.revision, revision_id: s.revision_id })
          : t("{name}: from your account — {type} {title} ({asset}), account revision {revision}", {
            name: input.name, type: s.asset_type, title: s.display_name || s.asset_id, asset: s.asset_id, revision: s.revision }));
      }
      const c = input.conversion;
      if (!c) continue;
      lines.push(t("{name}: converted from the map document {document} (revision {revision}), {digest}", {
        name: input.name, document: c.document_id || c.source_name, revision: c.document_revision, digest: c.source_sha256 }));
      // The bytes the compiler was handed: the `.map` the extractor wrote.
      if (input.sha256) lines.push(t("{name}: the .map this build compiled — {digest}", { name: input.name, digest: input.sha256 }));
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
    return lines;
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
    renderSteps();
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
        // Opened from Builds on this machine: the build is watched in Build &
        // Run, on a file on this computer.
        window.AUCOM.showArea("build");
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
    rememberPipeline($("build-pipeline").value);
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
  // One button, one press: Build, for the choices the check was made for.
  $("build-start").addEventListener("click", async (event) => {
    if (checked !== "ok" || buildBlocked()) return;
    await start(event.currentTarget);
    renderSteps();
  });
  $("build-history-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshHistory));
  $("build-play").addEventListener("click", async () => {
    if (!currentBuild) return;
    window.AUCOM.showArea("run");
    await window.AUCOM.areas.run?.chooseBuild?.(currentBuild);
  });
  $("build-log-fold").addEventListener("change", () => { if (lastBody) renderProgress(lastBody); });
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
      // Back from Profiles after "Set up …": a review made before is dropped
      // by refreshPipelines when the setup under it changed. Nothing is
      // checked on the person's behalf — a check run on arrival turned the
      // button into "Build" for a request nobody had reviewed (NEW_365).
      if (currentBuild) poll();
    },
    showStep,
  };
  window.AUCOM.areas.builds = { refresh: refreshHistory };
})();
