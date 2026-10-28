// Auto-build, on the saved map chosen in Build & Run (NEW_265; NEW_363 made it
// one checkbox under the build profile).
//
// The page shows and asks; it never polls AUB and never decides what to build.
// One poller in the Companion does that (internal/autobuild) whatever number of
// windows are open — this file only reads that poller's record for the chosen
// map, a local request every few seconds while the checkbox is on screen, and
// sends the switch, the build profile and the explicit actions.
//
// It has no build profile list of its own. Until NEW_363 it had one, filled
// with every pipeline whose stages had a provider (`runnable`), so a profile
// the manual form called "needs setup" could be chosen here (NEW_360 F1). Now
// automatic builds use the profile chosen in the form, judged by the same
// readiness decision (`pipelineReady`); a different one is applied only when
// somebody says so, and the server refuses an unready one as well.

"use strict";

(() => {
  const { $, el, api, setMessage, withBusy, when, badge, t } = window.AUCOM;

  let assetID = "";
  let displayName = "";
  let timer = null;
  let actionsKey = "";
  // The poller's last record for this map, drawn again when the form's build
  // profile changes.
  let last = null;

  function visible() {
    return !$("area-play").hidden && !$("play-autobuild").hidden && Boolean(assetID);
  }

  // While a question to AUB is in flight, or the baseline is not yet taken,
  // the record is read every second so its answer shows as soon as it lands.
  let soon = false;

  function schedule() {
    clearTimeout(timer);
    if (visible()) timer = setTimeout(refresh, soon ? 1000 : 5000);
  }

  // The build profile chosen in the form, and the one automatic builds use.
  function chosen() {
    const id = window.AUCOM.play?.pipeline?.() || "";
    return (window.AUCOM.play?.pipelines?.() || []).find((p) => p.id === id) || null;
  }

  function named(id) {
    return (window.AUCOM.play?.pipelines?.() || []).find((p) => p.id === id) || null;
  }

  function revisionText(revision) {
    return revision ? t("revision {n}", { n: revision.revision }) : "—";
  }

  function jobLink(attempt) {
    if (!attempt?.job_id) return null;
    return el("a", {
      text: t("open its job"),
      attrs: { href: "#jobs/" + encodeURIComponent(attempt.job_id), "data-autobuild-job": attempt.job_id },
    });
  }

  function row(term, ...value) {
    return [el("dt", { text: term }), el("dd", { children: value.filter(Boolean).map((v) => (typeof v === "string" ? document.createTextNode(v) : v)) })];
  }

  let runningFor = "";

  // The checkbox: it can be switched on only for a saved map with a READY
  // build profile; a profile that needs setup is said, with where to set it up,
  // and nothing is scheduled for it.
  function renderSwitch(entry) {
    const on = $("play-autobuild-on");
    const enabled = Boolean(entry?.enabled);
    const pipeline = chosen();
    const ready = Boolean(pipeline) && window.AUCOM.pipelineReady(pipeline);
    on.checked = enabled;
    on.disabled = !enabled && !ready;
    const message = $("play-autobuild-message");
    // An error the last action wrote stays until the next action.
    if (message.dataset.error) return;
    message.className = "message";
    message.replaceChildren();
    // The profile automatic builds use cannot build now: nothing more is
    // dispatched, the profile stays chosen, and this says what to repair.
    const used = enabled ? named(entry.pipeline_id) : null;
    if (enabled && (entry.unready || (used && !window.AUCOM.pipelineReady(used)))) {
      message.className = "message warning";
      message.append(document.createTextNode(t("Automatic builds wait until {name} is set up.",
        { name: used?.name || entry.pipeline_id }) + " "));
      for (const cause of used ? window.AUCOM.setupCauses(used) : []) {
        message.append(document.createTextNode(cause.text + " "), window.AUCOM.setupLink(cause));
      }
      return;
    }
    if (entry?.check_error) {
      setMessage(message, entry.check_error, "error");
      return;
    }
    if (!enabled && !ready) {
      message.textContent = t("Choose a ready build profile above to build new revisions automatically.");
    }
  }

  // "Apply to automatic builds": the form's profile changed while automatic
  // builds are on. They keep the one they had until this is pressed; a build
  // already running keeps its own.
  function renderApply(entry) {
    const box = $("play-autobuild-apply");
    const pipeline = chosen();
    const differs = Boolean(entry?.enabled) && Boolean(pipeline) && pipeline.id !== entry.pipeline_id;
    box.hidden = !differs;
    if (!differs) return;
    $("play-autobuild-apply-text").textContent = t("Automatic builds use {current}.",
      { current: named(entry.pipeline_id)?.name || entry.pipeline_id });
    const button = $("play-autobuild-apply-button");
    button.disabled = !window.AUCOM.pipelineReady(pipeline);
    button.setAttribute("aria-label", t("Apply {name} to automatic builds", { name: pipeline.name }));
  }

  function render(entry) {
    last = entry;
    // A build Auto-build started is news for the Activity drawer now, not at
    // its next slow poll.
    const running = entry.running?.run_id || "";
    if (running && running !== runningFor) window.AUCOM.play?.pollActivity?.();
    runningFor = running;
    renderSwitch(entry);
    renderApply(entry);
    // Expanded only while there is something to say: switched on, or a build
    // of its own still running after it was switched off.
    $("play-autobuild-expanded").hidden = !entry.enabled && !entry.running;
    $("play-autobuild-stop").hidden = !entry.enabled;

    const now = Date.parse(entry.now || "") || Date.now();
    soon = Boolean(entry.checking_since) || (Boolean(entry.enabled) && !entry.baseline && !entry.halted);
    const rows = [];
    if (entry.enabled || entry.last_check_at) {
      // A question in flight is said as such, with how long it has waited: a
      // slow server is not an up-to-date one (NEW_265A).
      let after = "";
      if (entry.checking_since) {
        const waited = Math.max(0, Math.round((now - Date.parse(entry.checking_since)) / 1000));
        after = " · " + t("asking the server now, for {s} s", { s: waited });
      } else if (entry.enabled && entry.next_check_at) {
        after = " · " + t("next check in {s} s", { s: Math.max(0, Math.round((Date.parse(entry.next_check_at) - now) / 1000)) });
      }
      rows.push(row(t("Checked"), entry.last_check_at ? when(entry.last_check_at) : t("not yet"), after));
    }
    if (entry.observed) {
      const baseline = entry.baseline && entry.baseline.revision_id === entry.observed.revision_id;
      rows.push(row(t("Saved on the server"), revisionText(entry.observed),
        baseline ? " · " + t("the starting point: not built") : ""));
    }
    if (entry.pending) rows.push(row(t("Waiting to build"), revisionText(entry.pending)));
    if (entry.running) {
      const link = jobLink(entry.running);
      rows.push(row(t("Building"), badge(t("running"), "running"), " " + revisionText(entry.running.revision),
        link ? " · " : "", link));
    }
    if (entry.last_built) {
      const link = jobLink(entry.last_built);
      rows.push(row(t("Last built"), badge(t("succeeded"), "succeeded"), " " + revisionText(entry.last_built.revision),
        entry.last_built.finished_at ? " · " + when(entry.last_built.finished_at) : "", link ? " · " : "", link));
    }
    if (entry.failed) {
      const link = jobLink(entry.failed);
      rows.push(row(t("Failed"), badge(entry.failed.state || "failed", "failed"), " " + revisionText(entry.failed.revision),
        entry.failed.error ? " — " + entry.failed.error : "", link ? " · " : "", link));
    }
    $("play-autobuild-status").replaceChildren(...rows.flat());

    // The Retry button is redrawn only when what it retries changes, so a
    // focused button is not replaced under the keyboard every five seconds.
    const key = JSON.stringify([entry.failed?.revision?.revision_id, entry.failed?.run_id, Boolean(entry.running)]);
    if (key !== actionsKey) {
      actionsKey = key;
      const actions = $("play-autobuild-actions");
      actions.replaceChildren();
      if (entry.failed && !entry.running) {
        const retry = el("button", { text: t("Retry revision {n}", { n: entry.failed.revision.revision }), attrs: { type: "button", id: "play-autobuild-retry" } });
        retry.addEventListener("click", () => withBusy(retry, () => act("retry")));
        actions.append(retry);
      }
    }
  }

  async function refresh() {
    clearTimeout(timer);
    if (!assetID) return;
    const asked = assetID;
    const { ok, body } = await api(`/api/v1/autobuild/${encodeURIComponent(asked)}`);
    if (asked !== assetID) return;
    if (!ok) setMessage("play-autobuild-message", body.error || t("Auto-build could not be read."), "error");
    else render(body);
    schedule();
  }

  async function act(what, payload) {
    const message = $("play-autobuild-message");
    delete message.dataset.error;
    const { ok, body } = await api(`/api/v1/autobuild/${encodeURIComponent(assetID)}/${what}`, {
      method: "POST", body: payload || {},
    });
    if (!ok) {
      await refresh();
      setMessage(message, body.error || t("That did not work."), "error");
      message.dataset.error = "1";
      return false;
    }
    render(body);
    schedule();
    return true;
  }

  // Switching it on needs a saved map, a ready profile and nothing else: it
  // builds nothing at once (the revision saved now is its starting point), never
  // starts the game, downloads no update and accepts no permission.
  $("play-autobuild-on").addEventListener("change", (event) => {
    const on = event.target.checked;
    withBusy(event.target, async () => {
      const pipeline = chosen();
      if (on && (!pipeline || !window.AUCOM.pipelineReady(pipeline))) {
        event.target.checked = false;
        renderSwitch(last);
        return;
      }
      // Off stops what would be scheduled next; a build already running is
      // left to finish, under its ordinary controls.
      await (on ? act("enable", { pipeline: pipeline.id, display_name: displayName }) : act("disable"));
    }).then(() => { if (last) renderSwitch(last); });
  });
  $("play-autobuild-stop").addEventListener("click", (event) => withBusy(event.currentTarget, () => act("disable")));
  $("play-autobuild-apply-button").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    const pipeline = chosen();
    if (pipeline) await act("pipeline", { pipeline: pipeline.id });
  }));
  // The form's build profile changed, or its list was read again (back from
  // Profiles after a repair): what the checkbox may do is decided again, at
  // once and without a reload.
  document.addEventListener("aucom:play-pipeline", () => {
    if (last) {
      renderSwitch(last);
      renderApply(last);
    } else if (!$("play-autobuild").hidden) {
      renderSwitch(null);
    }
  });

  // Called by play.js whenever the chosen saved map changes or the page is shown.
  window.AUCOM.autobuildPanel = {
    show(map, signedIn) {
      const panel = $("play-autobuild");
      const next = map?.asset_id || "";
      panel.hidden = !next || !signedIn;
      displayName = map?.display_name || "";
      if (next !== assetID) {
        assetID = next;
        actionsKey = "";
        last = null;
        $("play-autobuild-status").replaceChildren();
        $("play-autobuild-actions").replaceChildren();
        $("play-autobuild-expanded").hidden = true;
        $("play-autobuild-apply").hidden = true;
        delete $("play-autobuild-message").dataset.error;
        setMessage("play-autobuild-message", "");
        renderSwitch(null);
      }
      if (!panel.hidden) refresh();
      else clearTimeout(timer);
    },
  };
})();
