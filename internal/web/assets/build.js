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
  const { $, el, api, setMessage, busy, withBusy, record, badge, shortDigest, when, terminal,
    maturityBadge, maturityNote, openCompatibilityReport } = window.AUCOM;

  let pipelines = [];
  let inputFields = new Map();
  let currentBuild = null;
  let poller = null;

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
      ? `${pipeline.steps.length} stage(s). Version ${pipeline.version}.`
      : "This pipeline cannot run here: nothing on this machine provides " +
        pipeline.missing_capabilities.join(", ") +
        ". Install the tool that does, then approve it in Profiles.";
    $("build-pipeline-note").className = pipeline.runnable ? "muted" : "message error";

    // Above the stages and above the Build button, because this is the surface
    // where a Quake II map is compiled and exported.
    //
    // Removed before it is added: renderPipeline runs on every change of the
    // selector, and a note appended each time would stack up one warning per
    // pipeline the user looked at.
    for (const stale of document.querySelectorAll("#area-build > .panel > .wip")) stale.remove();
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
        const detail = `${step.provider.name} ${step.provider.version} · ${step.provider.action}`;
        line.append(el("span", { className: "stage-detail", text: detail }));
        if (step.provider_trust) line.append(badge(step.provider_trust));
        if (step.provider_authorized === false) {
          line.append(badge("not approved", "failed"));
        }
        if (step.provider_version) {
          line.append(el("span", { className: "stage-detail", text: "installed " + step.provider_version }));
        }
      } else {
        line.append(el("span", { className: "stage-detail", text: "no provider for " + step.capability }));
        line.classList.add("failed");
      }
      stages.append(line);
    }

    for (const input of pipeline.inputs || []) {
      inputs.append(inputRow(input));
    }
    revisionChosen();
  }

  // inputRow is one declared pipeline input. It offers whichever of the two
  // sources makes sense: a revision chosen in the Library, or a file on this
  // machine chosen through the desktop's own chooser.
  function inputRow(input) {
    const id = "build-input-" + input.name;
    const source = el("select", { attrs: { id: id + "-source" } });
    source.append(el("option", { text: "A file on this machine", attrs: { value: "file" } }));
    source.append(el("option", { text: "A revision from the Library", attrs: { value: "asset" } }));

    const file = window.AUCOM.pathField({
      id,
      kind: "open-file",
      label: `${input.title || input.name}${input.optional ? " (optional)" : ""}`,
      hint: input.description || "",
    });

    const assetNote = el("p", { className: "muted", attrs: { id: id + "-asset" } });
    const fileChoice = el("select", { attrs: { id: id + "-file", "aria-label": "Which file of that revision" } });
    const fileChoiceField = el("div", {
      className: "field",
      children: [el("label", { text: "File", attrs: { for: id + "-file" } }), fileChoice],
    });
    fileChoiceField.hidden = true;

    const wrapper = el("div", {
      className: "panel",
      children: [
        el("div", {
          className: "field",
          children: [el("label", { text: "Where " + (input.title || input.name) + " comes from", attrs: { for: id + "-source" } }), source],
        }),
        file.container,
        assetNote,
        fileChoiceField,
      ],
    });

    const apply = () => {
      const usingAsset = source.value === "asset";
      file.container.hidden = usingAsset;
      assetNote.hidden = !usingAsset;
      fileChoiceField.hidden = !usingAsset || fileChoice.options.length < 2;
    };
    source.addEventListener("change", apply);
    apply();

    inputFields.set(input.name, { input, source, file, assetNote, fileChoice, fileChoiceField, apply });
    return wrapper;
  }

  // revisionChosen is called by the Library when the user picks a revision.
  function revisionChosen() {
    const chosen = window.AUCOM.chosenRevision;
    for (const [, row] of inputFields) {
      if (!chosen) {
        row.assetNote.textContent = "Nothing chosen yet. Pick a revision in the Library area first.";
        row.fileChoice.replaceChildren();
        row.apply();
        continue;
      }
      row.assetNote.textContent =
        `${chosen.display_name} · ${chosen.asset_type}/${chosen.asset_id}` +
        (chosen.revision_id ? ` @ ${chosen.revision_id}` : " @ current") +
        ` (revision ${chosen.revision})`;
      row.fileChoice.replaceChildren();
      for (const file of chosen.files || []) {
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
    };
  }

  // inputTitle names a declared input the way its field is labelled.
  function inputTitle(name) {
    const input = (currentPipeline()?.inputs || []).find((item) => item.name === name);
    return input?.title || name;
  }

  // explain turns the two refusals a first build meets into what to do about
  // them. The executor's own sentence stays beside it, because it is the
  // record; this is the next step (NEW_244D: a preview used to say "This is
  // what would run" over "(no command resolved)" and hide both).
  function explain(text) {
    const missingInput =
      /needs "([^"]+)", and nothing supplies/.exec(text || "") ||
      /the required input "([^"]+)" was not supplied/.exec(text || "");
    if (missingInput) {
      return { kind: "input", name: missingInput[1],
        advice: `Choose the ${inputTitle(missingInput[1])} first: use Browse… beside that field, or pick a revision in the Library.` };
    }
    if (/is not installed on this machine|root is not configured|not configured on this machine|no program is recorded/.test(text || "")) {
      return { kind: "setup",
        advice: "This stage's program is not set up on this machine yet. Say where it is, once, in Profiles." };
    }
    return null;
  }

  function problemBlock(text, profile) {
    const why = explain(text);
    const block = el("div", { className: "problem" });
    block.append(el("p", { children: [el("strong", { text: why ? why.advice : text })] }));
    if (why) block.append(el("p", { className: "fix", text: "The Companion said: " + text }));
    if (why?.kind === "setup" && profile?.id) {
      const setup = el("button", { text: `Set up ${profile.name || profile.id}`, attrs: { type: "button", class: "primary" } });
      setup.addEventListener("click", async () => {
        window.AUCOM.showArea("profiles");
        await window.AUCOM.areas.profiles?.open?.(profile.id);
      });
      block.append(el("div", { className: "row-actions", children: [setup] }));
    }
    if (why?.kind === "input") {
      const field = $("build-input-" + why.name);
      if (field) {
        const focus = el("button", { text: `Choose the ${inputTitle(why.name)}`, attrs: { type: "button", class: "secondary" } });
        focus.addEventListener("click", () => field.focus());
        block.append(el("div", { className: "row-actions", children: [focus] }));
      }
    }
    return block;
  }

  async function preview(button) {
    await withBusy(button, async () => {
      busy("build-message", "Resolving every stage…");
      const { ok, body } = await api("/api/v1/build/preview", { method: "POST", body: requestBody() });
      const out = $("build-preview-out");
      out.hidden = false;
      out.replaceChildren();
      if (!ok) {
        setMessage("build-message", explain(body.error)?.advice || body.error || "the preview failed", "error");
        out.append(problemBlock(body.error || "the preview failed"));
        return;
      }
      const blocked = (body.steps || []).filter((step) => step.error);
      if (blocked.length > 0) {
        setMessage("build-message",
          "This build cannot start yet. What is missing is said under the stage that needs it; nothing has started.",
          "error");
      } else {
        setMessage("build-message", "This is what would run. Nothing has started.", "ok");
      }
      out.append(el("h4", { text: "Commands" }));
      for (const step of body.steps || []) {
        const block = el("div");
        block.append(
          el("p", {
            children: [
              el("strong", { text: step.title || step.id }),
              el("span", { className: "stage-detail", text: ` ${step.profile?.id || ""} ${step.profile?.version || ""}` }),
            ],
          })
        );
        if (step.error) block.append(problemBlock(step.error, step.profile));
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
        setMessage("build-message", explain(body.error)?.advice || body.error || "the build could not be started", "error");
        record("Build could not be started", body.error, "failed");
        return;
      }
      setMessage("build-message", `Build ${body.build} started.`, "ok");
      record(`Build ${body.build} started`, body.pipeline, "running");
      currentBuild = body.build;
      $("build-current-panel").hidden = false;
      $("build-current-title").textContent = "Building " + body.build;
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
      setMessage(
        "build-message",
        state === "succeeded"
          ? "The build succeeded. Its outputs are listed below."
          : `The build ${state}: ${body.manifest.error || body.error || "see the stages below"}`,
        state === "succeeded" ? "ok" : "error"
      );
      record(`Build ${currentBuild} ${state}`, body.manifest.error || "", state);
      await refreshHistory();
    };
    tick();
  }

  function renderProgress(body) {
    const manifest = body.manifest;
    const list = $("build-progress");
    list.replaceChildren();
    $("build-cancel").disabled = !body.live;
    $("build-current-title").textContent =
      `${body.live ? "Building" : "Build"} ${manifest.build_id} — ${manifest.state}`;

    // The finished build is the other moment a compatibility report is worth
    // offering: the user has just seen what happened and has the build id that
    // fills in the diagnostics. The manifest carries the family (see
    // internal/build/manifest.go), so this needs no second lookup.
    for (const stale of document.querySelectorAll("#build-current-panel > .wip")) stale.remove();
    const buildNote = maturityNote(
      { work_in_progress: true, badge: "Work in progress", message: body.maturity_message, feedback_invited: true },
      () =>
        openCompatibilityReport({
          family: manifest.engine_family,
          operation: "compile",
          build_id: manifest.build_id,
          about: `About build ${manifest.build_id} with ${manifest.pipeline?.id || "this pipeline"}.`,
        })
    );
    if (body.maturity_message && buildNote) $("build-current-title").after(buildNote);

    for (const step of manifest.steps || []) {
      const line = el("li", { className: step.state || (step.skipped ? "skipped" : "") });
      line.append(el("span", { className: "stage-name", text: step.title || step.id }));
      line.append(badge(step.skipped && !step.state ? "skipped" : step.state || "waiting"));
      const provider = step.profile ? `${step.profile.id} ${step.profile.version}` : step.capability;
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
            text: `${errors} error finding(s), ${warnings} warning(s)`,
          })
        );
      }
      if (step.error) line.append(el("span", { className: "stage-detail", text: step.error }));
      if (step.command?.shell) {
        const details = el("details");
        details.append(el("summary", { text: "command" }));
        details.append(el("pre", { className: "output", text: step.command.shell }));
        line.append(details);
      }
      list.append(line);
    }

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
        outputs.append(el("span", { className: "stage-detail", text: shortDigest(output.sha256) }));
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
      head.append(el("strong", { text: manifest.label || manifest.pipeline?.id || manifest.build_id }));
      head.append(badge(manifest.state));
      if (item.live) head.append(badge("running here", "running"));
      const detail = el("p", { className: "mono" });
      detail.textContent = [
        manifest.build_id,
        manifest.pipeline?.id,
        manifest.duration_ms ? manifest.duration_ms + " ms" : "",
        when(manifest.started_at),
      ]
        .filter(Boolean)
        .join(" · ");
      const open = el("button", { text: "Open", attrs: { type: "button", class: "secondary" } });
      open.addEventListener("click", () => {
        currentBuild = manifest.build_id;
        $("build-current-panel").hidden = false;
        poll();
        $("build-current-title").setAttribute("tabindex", "-1");
        $("build-current-title").focus();
      });
      list.append(el("li", { children: [head, detail, el("div", { className: "row-actions", children: [open] })] }));
    }
  }

  $("build-pipeline").addEventListener("change", renderPipeline);
  $("build-preview").addEventListener("click", (event) => preview(event.currentTarget));
  $("build-start").addEventListener("click", (event) => start(event.currentTarget));
  $("build-history-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshHistory));
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
      record(`Build ${currentBuild} cancelled`, (body.cancelled_jobs || []).join(", "), "cancelled");
    })
  );

  window.AUCOM.areas.build = {
    revisionChosen,
    async refresh() {
      await refreshPipelines();
      await refreshHistory();
      if (currentBuild) poll();
    },
  };
})();
