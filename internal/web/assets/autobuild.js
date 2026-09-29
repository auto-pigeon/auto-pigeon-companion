// Auto-build, on the map chosen in Build & Run step 1 (NEW_265).
//
// The page shows and asks; it never polls AUB and never decides what to build.
// One poller in the Companion does that (internal/autobuild) whatever number of
// windows are open — this file only reads that poller's record for the chosen
// map, a local request every few seconds while the panel is on screen, and
// sends the switch, the build profile and the two explicit actions.

"use strict";

(() => {
  const { $, el, api, setMessage, withBusy, when, badge, t } = window.AUCOM;

  let assetID = "";
  let displayName = "";
  let timer = null;
  let actionsKey = "";

  function visible() {
    return !$("area-play").hidden && !$("play-step-1").hidden && Boolean(assetID);
  }

  // While a question to AUB is in flight, or the baseline is not yet taken,
  // the record is read every second so its answer shows as soon as it lands.
  let soon = false;

  function schedule() {
    clearTimeout(timer);
    if (visible()) timer = setTimeout(refresh, soon ? 1000 : 5000);
  }

  function pipelines() {
    return window.AUCOM.play?.pipelines?.() || [];
  }

  function fillPipelines(chosen) {
    const select = $("play-autobuild-pipeline");
    const want = pipelines().filter((p) => p.runnable);
    const have = [...select.options].map((o) => o.value).join("|");
    if (have !== want.map((p) => p.id).join("|")) {
      select.replaceChildren(...want.map((p) => el("option", { text: p.name, attrs: { value: p.id } })));
    }
    if (chosen && want.some((p) => p.id === chosen)) select.value = chosen;
    else if (!select.value) select.value = window.AUCOM.play?.pipeline?.() || want[0]?.id || "";
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

  function render(entry) {
    // A build Auto-build started is news for the Activity drawer now, not at
    // its next slow poll.
    const running = entry.running?.run_id || "";
    if (running && running !== runningFor) window.AUCOM.play?.pollActivity?.();
    runningFor = running;
    const on = $("play-autobuild-on");
    on.checked = Boolean(entry.enabled);
    on.setAttribute("aria-checked", String(Boolean(entry.enabled)));
    fillPipelines(entry.pipeline_id);

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
    $("play-autobuild-now").disabled = Boolean(entry.running);

    if (entry.check_error) setMessage("play-autobuild-message", entry.check_error, "error");
    else if (!entry.enabled && !entry.running) setMessage("play-autobuild-message", t("Auto-build is off for this map."), "");
    else setMessage("play-autobuild-message", "");
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
    const { ok, body } = await api(`/api/v1/autobuild/${encodeURIComponent(assetID)}/${what}`, {
      method: "POST", body: payload || {},
    });
    if (!ok) {
      setMessage("play-autobuild-message", body.error || t("That did not work."), "error");
      await refresh();
      return false;
    }
    render(body);
    schedule();
    return true;
  }

  $("play-autobuild-on").addEventListener("change", (event) => {
    const on = event.target.checked;
    withBusy(event.target, async () => {
      const pipeline = $("play-autobuild-pipeline").value;
      if (on && !pipeline) {
        event.target.checked = false;
        setMessage("play-autobuild-message", t("Choose a build profile first: none is installed and ready on this machine."), "error");
        return;
      }
      await (on ? act("enable", { pipeline, display_name: displayName }) : act("disable"));
    });
  });
  $("play-autobuild-pipeline").addEventListener("change", (event) => {
    if ($("play-autobuild-on").checked) act("pipeline", { pipeline: event.target.value });
  });
  $("play-autobuild-now").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    // The build profile on screen goes with it: a map never switched on gets
    // it recorded, and Auto-build stays off.
    await act("build-now", { pipeline: $("play-autobuild-pipeline").value, display_name: displayName });
  }));

  // Called by play.js whenever the chosen map changes or the page is shown.
  window.AUCOM.autobuildPanel = {
    show(map, signedIn) {
      const panel = $("play-autobuild");
      const next = map?.asset_id || "";
      panel.hidden = !next || !signedIn;
      displayName = map?.display_name || "";
      if (next !== assetID) {
        assetID = next;
        actionsKey = "";
        $("play-autobuild-status").replaceChildren();
        $("play-autobuild-actions").replaceChildren();
        setMessage("play-autobuild-message", "");
      }
      fillPipelines();
      if (!panel.hidden) refresh();
      else clearTimeout(timer);
    },
  };
})();
