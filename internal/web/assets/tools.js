// Build tools the Companion installs and updates itself (NEW_367): the
// "Setting up build tools" card, the update dialog, and the Settings panel.
//
// The coordinator is server-side and there is one per Companion; every tab
// reads the same /api/v1/tools and every decision is the server's to record,
// so a Cancel in one tab is a Cancel in all of them.

"use strict";

(() => {
  const { $, el, api, setMessage, withBusy, record, t, bytes, when, trapFocus } = window.AUCOM;

  const FAST_MS = 2500;
  const SLOW_MS = 60000;
  let status = null;
  let timer = 0;
  let wasReady = {};
  let seenNotices = null;
  let offerShown = null; // { items: [{ tool, identity }], opener }
  let hiddenWaiting = {};

  const PHASE_WORDS = {
    disabled: "Off",
    not_checked: "Not checked yet",
    checking: "Checking for releases…",
    downloading: "Downloading",
    verifying: "Verifying",
    ready: "Ready",
    failed: "Failed",
    unsupported: "Not available for this computer",
  };

  // Background checks the server runs (NEW_368): words for each kind, while it
  // runs and when it failed. One server, so every tab shows the same rows.
  const CHECK_WORDS = {
    checking_programs: "Checking build tools…",
    checking_converter: "Checking the converter…",
  };
  const CHECK_FAILED = {
    checking_programs: "Checking build tools did not finish: {error}",
    checking_converter: "The converter could not be checked: {error}",
  };

  function phaseLine(tool) {
    let text = t(PHASE_WORDS[tool.phase] || tool.phase);
    if (tool.version && tool.phase !== "failed") text += " " + tool.version;
    if (tool.phase === "downloading" && tool.total > 0) {
      // A zero is left out of the JSON: nothing received yet is "0 B", not a blank.
      text += " — " + t("{done} of {total}", { done: bytes(tool.done || 0), total: bytes(tool.total) });
    }
    return text;
  }

  function detailsList(tool) {
    const rows = [];
    const add = (term, value) => {
      if (!value && value !== 0) return;
      rows.push(el("dt", { text: t(term) }), el("dd", { text: String(value), className: "mono" }));
    };
    const active = tool.active;
    if (active) {
      add("Release", active.tag);
      add("Version banner", active.version_banner);
      add("Download size", bytes(active.size));
      add("SHA-256", active.actual_sha256);
      add("Installed in", active.dir + " " + t("(the Companion's own tools folder)"));
      add("Source commit", active.source_commit);
      add("Qualification", active.qualification);
      add("Signed", t("No: a SHA-256 published in the same release shows the download is intact, not who published it"));
    }
    if (tool.offer) {
      add("Offered", tool.offer.tag + " (" + bytes(tool.offer.size) + ")");
    }
    if (tool.check?.last_success) add("Last checked", when(tool.check.last_success));
    if (tool.check?.next_attempt) add("Next check", when(tool.check.next_attempt));
    if (tool.check?.last_error) add("Last check failed", tool.check.last_error);
    if (tool.check?.rate_limited_until && new Date(tool.check.rate_limited_until) > new Date()) {
      add("GitHub asked to wait until", when(tool.check.rate_limited_until));
    }
    for (const verdict of tool.newer || []) {
      add(t("Not offered: {tag}", { tag: verdict.tag }), verdict.detail || verdict.reason);
    }
    if (tool.integrity) add("Integrity", t("Release {version} was published again with different bytes; it is not installed.", { version: tool.integrity.seen.version }));
    if (tool.failure) add("Last failure", tool.failure.error);
    return rows;
  }

  // --- installing ------------------------------------------------------------
  //
  // Nothing is downloaded until a person says so (HITL, 2026-10-31). The page
  // asks once per browser session; Cancel leaves Profiles' Install buttons.
  //
  // The Install window, the download it starts and the notice that ends it are
  // one interaction. Whoever comes next asks installSettled how it ended, and
  // is answered when it has: nothing polls for a dialog to go away.

  let asking = false;
  let installing = [];
  let interaction = null;
  let settleInteraction = () => {};
  let ended = { outcome: "none", moves: -1 };
  let settleFirstLook = () => {};
  const firstLook = new Promise((resolve) => { settleFirstLook = resolve; });

  function endInteraction(outcome) {
    const settle = settleInteraction;
    interaction = null;
    settleInteraction = () => {};
    ended = { outcome, moves: window.AUCOM.areaMoves };
    settle(outcome);
  }

  // How the interaction ended: "installed", "declined" or "failed", or "none"
  // when there was none since the person last moved to another area. Answers
  // once the page knows: the tools' first status decides what is asked first.
  window.AUCOM.installSettled = async () => {
    await firstLook;
    if (interaction) return interaction;
    return ended.moves === window.AUCOM.areaMoves ? ended.outcome : "none";
  };

  function declined() {
    try { return window.sessionStorage.getItem("aucom-tools-declined") === "1"; } catch { return asking; }
  }

  function decline() {
    try { window.sessionStorage.setItem("aucom-tools-declined", "1"); } catch { /* asked again next time */ }
  }

  async function install(ids) {
    installing = ids;
    const { ok, body } = await api("/api/v1/tools/install", { method: "POST", body: { tools: ids } });
    if (ok) {
      apply(body);
      schedule(FAST_MS);
    } else {
      installing = [];
      setMessage("tools-setup-message", body.error || "", "error");
    }
    return ok;
  }

  // The one review before anything is downloaded: which programs, from where,
  // into which folder, and why a build needs them (NEW_508: the destination is
  // said before the press, not after it). `asked` is a person pressing
  // "Install build tools" on Build & Run — an explicit ask, so an earlier
  // Cancel in this tab does not silence it.
  async function askToInstall({ asked = false } = {}) {
    const waiting = (status?.tools || []).filter((tool) => tool.awaiting_install);
    // Not while the install this offer started is still going: the server
    // lists a tool as awaiting until its download begins, and the same
    // question opened again over the progress it had just started (seen live,
    // NEW_508).
    if (asking || installing.length || !waiting.length || (!asked && declined()) || document.querySelector("dialog[open]")) return;
    asking = true;
    interaction = new Promise((resolve) => { settleInteraction = resolve; });
    // Every official build tool has its row (NEW_508C), from the one
    // catalogue the server reads — a tool that cannot be installed here stays
    // listed with the reason, instead of silently missing.
    const rows = (status?.tools || []).filter((tool) => !isSelf(tool));
    const yes = await window.AUCOM.confirmModal({
      title: t("Install the Auto-Pigeon build tools"),
      body: t("Building a map needs these compilers. They are downloaded from Auto-Pigeon's releases on GitHub and checked before use."),
      content: [el("ul", { className: "tools-list", children: rows.map((tool) => el("li", { className: "tools-row tools-row--" + tool.phase, children: [
        el("a", { text: tool.title, attrs: { href: tool.releases_url, target: "_blank", rel: "noopener noreferrer" } }),
        document.createTextNode(" — " + installLine(tool)),
      ] })) }),
      ...(status.root ? [el("p", { className: "muted", children: [document.createTextNode(t("Installed in:") + " "), el("code", { text: status.root })] })] : []),
      ...(status.root_error ? [el("p", { className: "message error", text: status.root_error })] : [])],
      confirm: t("Install"), cancel: t("Cancel"),
    });
    asking = false;
    if (!yes) {
      decline();
      endInteraction("declined");
    } else if (!await install(waiting.map((tool) => tool.id))) {
      endInteraction("failed");
    }
  }
  // One build tool's line in the Install window.
  function installLine(tool) {
    if (tool.awaiting_install) return t("will be installed");
    if (tool.phase === "ready") return t("installed: {version}", { version: tool.version });
    if (tool.phase === "unsupported" || tool.phase === "failed" || tool.phase === "disabled") {
      return t("not available: {why}", { why: tool.detail || t(PHASE_WORDS[tool.phase] || tool.phase) });
    }
    return phaseLine(tool);
  }

  // Build & Run offers the install again beside the build profile it blocks,
  // so a Cancel at first start is not a dead end (build.js renderPipeline).
  window.AUCOM.buildToolsWaiting = () => (status?.tools || []).some((tool) => tool.awaiting_install) && !installing.length;
  window.AUCOM.offerBuildTools = () => askToInstall({ asked: true });

  // The notice once everything a press of Install asked for is in place.
  async function tellInstalled() {
    if (!installing.length) return;
    const tools = (status?.tools || []).filter((tool) => installing.includes(tool.id));
    if (tools.some((tool) => tool.phase !== "ready" && tool.phase !== "failed" && tool.phase !== "unsupported")) return;
    const other = document.querySelector("dialog[open]");
    if (other) {
      other.addEventListener("close", () => tellInstalled(), { once: true });
      return;
    }
    const done = tools.some((tool) => tool.phase === "ready");
    installing = [];
    if (done) {
      await window.AUCOM.confirmModal({
        title: t("Build tools installed"), body: t("The build tools were installed in this folder:"),
        content: [el("p", { children: [el("code", { text: status.root || "" })] })], confirm: t("OK"), cancel: null,
      });
    }
    endInteraction(done ? "installed" : "failed");
  }

  // installButton is the Install beside a build tool in Profiles, or null.
  window.AUCOM.installButton = (profileId) => {
    const entry = Object.entries(status?.profiles || {}).find(([, rows]) => rows.some((row) => row.id === profileId));
    const tool = entry && (status.tools || []).find((row) => row.id === entry[0]);
    if (!tool?.awaiting_install) return null;
    const control = el("button", { text: t("Install"), className: "secondary", attrs: { type: "button" } });
    control.addEventListener("click", () => withBusy(control, () => install([tool.id])));
    return control;
  };

  // --- the setup card --------------------------------------------------------

  function renderSetup() {
    const card = $("tools-setup");
    const tools = status?.tools || [];
    const waiting = tools.filter((tool) => tool.pending?.staged && tool.pending.waiting?.length && !hiddenWaiting[tool.id + tool.pending.version]);
    const settingUp = Boolean(status?.available && status.setting_up);
    const checks = status?.checks || [];
    const checkFailures = status?.check_failures || [];
    card.hidden = !settingUp && waiting.length === 0 && checks.length === 0 && checkFailures.length === 0;
    // The card is an Activity entry (NEW_508). What the header's count makes of
    // it: "1" wants the person (a failure, an update held back by work), "0" is
    // work going on (tools installing), and "" is a routine check of what is
    // already installed — listed for whoever opens Activity, never counted.
    const failedNow = (status?.tools || []).some((tool) => settingUp && tool.phase === "failed");
    card.dataset.attention = failedNow || waiting.length || checkFailures.length ? "1" : settingUp ? "0" : "";
    if (card.hidden) return;
    $("tools-setup-title").textContent = settingUp ? t("Setting up build tools")
      : waiting.length ? t("Build tools update ready")
        : checks.length ? t(CHECK_WORDS[checks[0].kind] || "Checking build tools…")
          : t("A check did not finish");
    const list = $("tools-setup-list");
    list.replaceChildren();
    for (const row of checks) {
      // No percentage exists for these: the row says what runs, and the
      // card's own class shows it as ongoing.
      list.append(el("li", { className: "tools-row tools-row--checking", children: [
        el("span", { className: "spinner", attrs: { "aria-hidden": "true" } }),
        el("strong", { text: t(CHECK_WORDS[row.kind] || row.kind) }),
        ...(row.detail ? [document.createTextNode(" — " + row.detail)] : []),
      ] }));
    }
    for (const failure of checkFailures) {
      list.append(el("li", { className: "tools-row tools-row--failed", children: [
        el("p", { text: t(CHECK_FAILED[failure.kind] || "{error}", { error: failure.error }) }),
        ...(failure.kind === "checking_programs"
          ? [el("a", { text: t("See each profile's programs"), attrs: { href: "#profiles" } })] : []),
      ] }));
    }
    for (const tool of tools) {
      if (settingUp && !isSelf(tool)) {
        list.append(el("li", { className: "tools-row tools-row--" + tool.phase, children: [
          el("strong", { text: tool.title }), document.createTextNode(" — " + phaseLine(tool)),
          ...(tool.phase === "failed" || tool.phase === "unsupported"
            ? [el("p", { className: "muted", text: tool.detail || "" }),
              el("a", { text: t("Set it up by hand instead"), attrs: { href: "#profiles" } })]
            : []),
        ] }));
      }
    }
    for (const tool of waiting) {
      const row = el("li", { className: "tools-row", children: [
        el("p", { text: t("{tool} {version} is ready; waiting for current work to finish: {work}.", {
          tool: tool.title, version: tool.pending.version, work: tool.pending.waiting.join("; ") }) }),
      ] });
      const keep = el("button", { text: t("Keep waiting"), className: "secondary", attrs: { type: "button" } });
      keep.addEventListener("click", () => { hiddenWaiting[tool.id + tool.pending.version] = true; renderSetup(); });
      const cancel = el("button", { text: t("Cancel the update"), className: "secondary", attrs: { type: "button" } });
      cancel.addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
        const { ok, body } = await api(`/api/v1/tools/${encodeURIComponent(tool.id)}/cancel-pending`, { method: "POST", body: {} });
        if (ok) apply(body); else setMessage("tools-setup-message", body.error || "", "error");
      }));
      row.append(el("div", { className: "row", children: [keep, cancel] }));
      list.append(row);
    }
    const failed = tools.some((tool) => tool.phase === "failed");
    const retryChecks = checkFailures.some((failure) => failure.retry) && checks.length === 0;
    $("tools-setup-retry").hidden = !((settingUp && failed) || retryChecks);
    const details = $("tools-setup-details");
    details.replaceChildren();
    for (const tool of tools) {
      details.append(el("dt", { text: tool.title, className: "tools-details-head" }), el("dd", { text: "" }), ...detailsList(tool));
    }
    for (const failure of checkFailures) {
      details.append(el("dt", { text: t(CHECK_WORDS[failure.kind] || failure.kind) }),
        el("dd", { text: failure.error, className: "mono" }));
    }
  }

  $("tools-setup-retry").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    setMessage("tools-setup-message", "");
    if ((status?.check_failures || []).some((failure) => failure.retry)) {
      const { ok, body } = await api("/api/v1/checks/retry", { method: "POST", body: {} });
      if (!ok) { setMessage("tools-setup-message", body.error || "", "error"); return; }
      apply(body);
    }
    for (const tool of status?.tools || []) {
      if (tool.phase !== "failed") continue;
      const { ok, body } = await api(`/api/v1/tools/${encodeURIComponent(tool.id)}/retry`, { method: "POST", body: {} });
      if (!ok) { setMessage("tools-setup-message", body.error || "", "error"); return; }
      apply(body);
    }
    schedule(FAST_MS);
  }));

  // --- the update dialog -----------------------------------------------------

  function identityOf(offer) {
    return { version: offer.version, asset_id: offer.asset_id, sha256: offer.sha256 };
  }

  function sameIdentity(a, b) {
    return a && b && a.version === b.version && a.asset_id === b.asset_id && a.sha256 === b.sha256;
  }

  const isSelf = (tool) => tool.kind === "application";

  // A dialog never opens over another one — a profile's review, the sign-in,
  // Quit — or over a file picker the page opened: the offer waits, with the
  // badge in the header, for a moment when nothing else is asking.
  function busyElsewhere() {
    return Boolean(document.querySelector(".auth-modal:not([hidden]):not(#tools-update-modal)") ||
      document.querySelector("dialog[open]") || window.AUCOM.pickingFile);
  }

  // What the Activity entry announces: offers nobody cancelled in this run.
  // A cancelled one stays available — Settings, the sidebar's Update, the
  // CLI — and is announced again at the next start (NEW_508C).
  function offered() {
    return (status?.tools || []).filter((x) => x.offer && x.active && !x.offer_dismissed);
  }

  function maybeOffer() {
    if (op) {
      renderProgress();
      return;
    }
    const modal = $("tools-update-modal");
    const tools = status?.tools || [];
    if (offerShown) {
      // Answered elsewhere — another tab, the terminal — or replaced by a
      // newer check: this dialog is about bytes no longer on offer.
      const stale = offerShown.items.some((item) => {
        const tool = tools.find((x) => x.id === item.tool);
        return !tool?.offer || !sameIdentity(identityOf(tool.offer), item.identity);
      });
      if (stale) closeOffer();
      return;
    }
    const list = offered();
    $("tools-update-badge").hidden = list.length === 0;
    if (!list.length || !modal.hidden) return;
    // An update found by a check nobody asked for is ONE Activity entry and
    // opens nothing (NEW_508): a routine release does not interrupt a build
    // review. The dialog — the same three answers — opens when the person
    // presses that entry, or straight after their own "Check for updates".
    if (!askedToCheck || busyElsewhere()) return;
    askedToCheck = false;
    openOffer(list);
  }
  let askedToCheck = false;

  // One dialog for every update on offer, the Companion's own included, so
  // three releases on one check are one question, not three stacked dialogs.
  function openOffer(list) {
    offerShown = { items: list.map((tool) => ({ tool: tool.id, identity: identityOf(tool.offer) })), opener: document.activeElement };
    $("tools-update-badge").hidden = true;
    const one = list.length === 1 ? list[0] : null;
    $("tools-update-heading").textContent = one ? t("Update {tool}?", { tool: one.title }) : t("Updates available");
    const intro = (tool) => isSelf(tool)
      ? t("Auto-Pigeon Companion {from} is running. Version {to} is available.", { from: tool.active.version, to: tool.offer.version })
      : t("{tool} {from} is installed. Version {to} is available.", { tool: tool.title, from: tool.active.version, to: tool.offer.version });
    $("tools-update-intro").textContent = one ? intro(one) : t("Choose what to do with these updates.");
    const rows = $("tools-update-list");
    rows.replaceChildren();
    rows.hidden = Boolean(one);
    if (!one) {
      for (const tool of list) {
        rows.append(el("li", { className: "tools-row", children: [
          el("span", { text: intro(tool) + " " }),
          el("a", { text: t("Release notes"), attrs: { href: tool.offer.notes_url, target: "_blank", rel: "noopener noreferrer" } }),
        ] }));
      }
    }
    const notes = $("tools-update-notes");
    $("tools-update-notes-row").hidden = !one;
    if (one) {
      notes.href = one.offer.notes_url;
      notes.textContent = t("Release notes for {version}", { version: one.offer.version });
    }
    const self = list.find(isSelf);
    const note = $("tools-update-note");
    note.hidden = !self;
    if (self) {
      note.textContent = self.self?.mode === "manual"
        ? t("The Companion downloads and checks it; you put it in place: {why}.", { why: self.self.reason || "" })
        : t("Update now restarts the Companion once nothing is running; builds, downloads and games are never stopped for it.");
    }
    $("tools-update-skip").checked = false;
    $("tools-update-skip-label").textContent = one
      ? t("Don't notify me about this version ({version})", { version: one.offer.version })
      : t("Don't notify me about these versions");
    $("tools-update-offer").hidden = false;
    $("tools-update-progress").hidden = true;
    const details = $("tools-update-details");
    details.replaceChildren();
    for (const tool of list) {
      if (!one) details.append(el("dt", { text: tool.title, className: "tools-details-head" }), el("dd", { text: "" }));
      details.append(
        el("dt", { text: t("Download size") }), el("dd", { text: bytes(tool.offer.size) }),
        el("dt", { text: t("Installed in") }), el("dd", { text: isSelf(tool)
          ? t("replaces this Companion's own files in {root}; your settings, profiles, maps and build history are not touched, and {from} is kept to go back to", { root: tool.active.dir, from: tool.active.version })
          : t("the Companion's own tools folder, beside {from}, which is kept", { from: tool.active.version }) }),
        el("dt", { text: t("SHA-256") }), el("dd", { text: tool.offer.sha256, className: "mono" }),
        el("dt", { text: t("Qualification") }), el("dd", { text: tool.offer.qualification || "" }),
      );
      if (tool.offer.sidecar) {
        details.append(el("dt", { text: t("Bundled converter") }), el("dd", { text: tool.offer.sidecar.version + " (protocol " + tool.offer.sidecar.protocol + ")" }));
      }
      if (tool.failure && tool.failure.version === tool.offer.version) {
        details.append(el("dt", { text: t("Last attempt") }), el("dd", { text: tool.failure.error }));
      }
    }
    setMessage("tools-update-message", "");
    $("tools-update-modal").hidden = false;
    // Nothing that downloads or switches anything has the focus.
    $("tools-update-cancel").focus();
  }

  function closeOffer() {
    $("tools-update-modal").hidden = true;
    const opener = offerShown?.opener;
    offerShown = null;
    opener?.focus?.();
  }

  async function decide(decision, button) {
    if (!offerShown) return;
    await withBusy(button, async () => {
      const shown = offerShown;
      const dontAsk = decision === "cancel" && $("tools-update-skip").checked;
      let last = null;
      for (const item of shown.items) {
        const { ok, status: code, body } = await api(`/api/v1/tools/${encodeURIComponent(item.tool)}/decide`, {
          method: "POST",
          body: { decision, ...item.identity, dont_ask: dontAsk },
        });
        if (!ok) {
          setMessage("tools-update-message", code === 409 ? t("This update is no longer the one on offer.") : body.error || "", "error");
          if (code === 409) { closeOffer(); poll(); }
          return;
        }
        const title = (status?.tools || []).find((x) => x.id === item.tool)?.title || item.tool;
        if (decision === "now") record(t("Updating {tool} to {version}", { tool: title, version: item.identity.version }), "", "busy");
        if (decision === "on_exit") record(t("{tool} {version} will be installed when the Companion closes", { tool: title, version: item.identity.version }), "", "ok");
        last = body;
      }
      if (decision === "now") {
        // The dialog stays: it follows what it started to the end (NEW_508C).
        const targets = {};
        for (const item of shown.items) targets[item.tool] = item.identity.version;
        offerShown = null;
        startOperation({ targets, opener: shown.opener });
        apply(last);
        return;
      }
      closeOffer();
      apply(last);
    });
  }

  $("tools-update-now").addEventListener("click", (event) => decide("now", event.currentTarget));
  $("tools-update-later").addEventListener("click", (event) => decide("on_exit", event.currentTarget));
  $("tools-update-cancel").addEventListener("click", (event) => decide("cancel", event.currentTarget));
  $("tools-update-badge").addEventListener("click", () => {
    const list = offered();
    if (list.length && $("tools-update-modal").hidden) openOffer(list);
  });
  // The X and Escape are Cancel WITHOUT "don't ask again", whatever the box
  // says: only Cancel confirms the box. While an Update runs they only hide
  // the dialog: nothing is cancelled by closing a window.
  const dismiss = () => {
    if (op) {
      hideProgress();
      return;
    }
    $("tools-update-skip").checked = false;
    decide("cancel", $("tools-update-close"));
  };
  $("tools-update-close").addEventListener("click", dismiss);
  document.querySelector('[data-dismiss="tools-update"]').addEventListener("click", dismiss);
  $("tools-update-modal").addEventListener("keydown", (event) => {
    if (event.key === "Escape") dismiss();
    trapFocus(event, $("tools-update-dialog"), "button, input, a[href], summary");
  });

  // --- the Update operation (NEW_508C) ------------------------------------------
  //
  // One operation, from the sidebar's Update or an offer's "Update now": the
  // dialog stays open (or can be hidden, and reopened from the sidebar) and
  // says, per tool, what the server is doing — checking, downloading with the
  // real byte count, verifying, waiting for work, switching, the Companion's
  // own restart — and how it ended. Nothing here invents a percentage: the bar
  // is determinate only while bytes with a known total are moving. The
  // operation is kept in sessionStorage, so the page that reloads across the
  // Companion's restart opens it again and confirms the version that runs.

  const OP_KEY = "aucom:update-op";
  const OP_MAX_MS = 30 * 60 * 1000;
  let op = null; // { started, targets: { tool: version }, steps, checking, error, restarting, opener }
  let opTicker = 0;
  let opHidden = false;

  function saveOp() {
    if (!op) return;
    const { opener, ...kept } = op;
    try { window.sessionStorage.setItem(OP_KEY, JSON.stringify(kept)); } catch { /* storage refused: this page only */ }
  }

  function loadOp() {
    try {
      const raw = window.sessionStorage.getItem(OP_KEY);
      if (!raw) return null;
      const kept = JSON.parse(raw);
      if (kept && Date.now() - kept.started < OP_MAX_MS) return kept;
      window.sessionStorage.removeItem(OP_KEY);
    } catch { /* storage refused or unreadable */ }
    return null;
  }

  function endOperation() {
    op = null;
    opHidden = false;
    window.clearInterval(opTicker);
    try { window.sessionStorage.removeItem(OP_KEY); } catch { /* storage refused */ }
  }

  window.AUCOM.updateOperationActive = () => Boolean(op);
  // lifecycle.js: the Companion is stopping to put its update in place. The
  // dialog stays and says so; the page reloads when the new one answers.
  window.AUCOM.updateRestarting = () => {
    if (!op) return false;
    op.restarting = true;
    saveOp();
    if (!opHidden) showProgress();
    renderProgress();
    return true;
  };

  function startOperation({ targets = {}, steps = null, checking = false, opener = document.activeElement } = {}) {
    op = { started: Date.now(), targets, steps, checking, opener };
    opHidden = false;
    saveOp();
    showProgress();
  }

  function showProgress() {
    const modal = $("tools-update-modal");
    $("tools-update-offer").hidden = true;
    $("tools-update-progress").hidden = false;
    $("tools-update-heading").textContent = t("Updating");
    $("tools-update-intro").textContent = t("The Companion checks Auto-Pigeon's official releases and installs the newest verified versions.");
    setMessage("tools-update-message", "");
    if (modal.hidden) {
      modal.hidden = false;
      $("tools-update-hide").focus();
    }
    opHidden = false;
    window.clearInterval(opTicker);
    opTicker = window.setInterval(renderProgress, 1000);
    renderProgress();
  }

  function hideProgress() {
    opHidden = true;
    $("tools-update-modal").hidden = true;
    const opener = op?.opener;
    if (opener?.isConnected) opener.focus(); else $("sidebar-update")?.focus();
  }

  const TERMINAL = new Set(["current", "installed", "failed", "unavailable", "not_automatic", "manual_ready"]);

  // rowState is one target's state now: { key, text, done, total }.
  function rowState(id) {
    const tool = (status?.tools || []).find((x) => x.id === id);
    const step = (op.steps || []).find((x) => x.tool === id);
    const target = op.targets[id];
    if (step && ["current", "unavailable", "failed", "not_automatic"].includes(step.action)) {
      const words = {
        current: t("Up to date: {version}", { version: step.to || tool?.version || "" }),
        unavailable: t("Not available: {why}", { why: step.detail || "" }),
        failed: t("Failed: {why}", { why: step.detail || "" }),
        not_automatic: t("{version} is available; this copy is not updated automatically: {why}", { version: step.to, why: step.detail || "" }),
      };
      return { key: step.action, text: words[step.action] };
    }
    if (!tool) return { key: "checking", text: t("Checking…") };
    if (isSelf(tool)) {
      const last = tool.self?.last;
      if (tool.version === target && tool.phase === "ready") return { key: "installed", text: t("Installed: {version}", { version: target }) };
      if (last && last.to === target && ["rolled_back", "failed", "recovery_needed"].includes(last.outcome)) {
        return { key: "failed", text: t("Not installed; {from} is running: {why}", { from: last.from, why: last.detail || last.outcome }) };
      }
      if (op.restarting || tool.pending?.restarting) return { key: "restarting", text: t("Restarting the Companion to put {version} in place…", { version: target }) };
      if (tool.pending?.manual) return { key: "manual_ready", text: t("Downloaded and checked; put it in place yourself:") + " " + tool.pending.manual };
      if (tool.failure && tool.failure.version === target) return { key: "failed", text: t("Failed: {why}", { why: tool.failure.error }) };
    } else {
      if (tool.active && tool.active.version === target && !tool.pending && !tool.repair) {
        return { key: "installed", text: t("Installed: {version}", { version: target }) };
      }
      if (tool.failure && tool.failure.version === target) return { key: "failed", text: t("Failed: {why}", { why: tool.failure.error }) };
      if (tool.phase === "unsupported") return { key: "unavailable", text: t("Not available: {why}", { why: tool.detail || "" }) };
    }
    if (tool.phase === "downloading") {
      return { key: "downloading", text: tool.total > 0
        ? t("Downloading {version} — {done} of {total}", { version: tool.version, done: bytes(tool.done || 0), total: bytes(tool.total) })
        : t("Downloading {version}…", { version: tool.version }), done: tool.done || 0, total: tool.total || 0 };
    }
    if (tool.phase === "verifying") return { key: "verifying", text: t("Verifying {version}…", { version: tool.version }) };
    if (tool.phase === "checking") return { key: "checking", text: t("Checking…") };
    if (tool.pending?.waiting?.length) {
      return { key: "waiting", text: t("Downloaded and verified; waiting for: {work}", { work: tool.pending.waiting.join("; ") }) };
    }
    if (tool.pending?.staged) return { key: "switching", text: t("Switching to {version}…", { version: target }) };
    if (tool.repair) return { key: "switching", text: t("Replacing the installation in use: {why}", { why: tool.repair }) };
    return { key: "starting", text: t("Starting…") };
  }

  function renderProgress() {
    if (!op || $("tools-update-progress").hidden) return;
    const list = $("tools-update-steps");
    const ids = Object.keys(op.targets);
    for (const step of op.steps || []) if (!ids.includes(step.tool)) ids.push(step.tool);
    const rows = op.checking ? [] : ids.map((id) => {
      const tool = (status?.tools || []).find((x) => x.id === id);
      const step = (op.steps || []).find((x) => x.tool === id);
      return { id, title: tool?.title || step?.title || id, ...rowState(id) };
    });
    list.replaceChildren(...rows.map((row) => el("li", { className: "tools-row tools-row--" + row.key, attrs: { "data-tool": row.id, "data-state": row.key }, children: [
      el("strong", { text: row.title }), el("span", { text: row.text }),
      ...(row.key === "downloading" ? [row.total > 0
        ? el("progress", { attrs: { max: String(row.total), value: String(row.done), "aria-label": row.title } })
        : el("progress", { attrs: { "aria-label": row.title } })] : []),
    ] })));
    const running = op.checking || rows.some((row) => !TERMINAL.has(row.key));
    const failed = Boolean(op.error) || rows.some((row) => row.key === "failed");
    const seconds = Math.max(0, Math.round((Date.now() - op.started) / 1000));
    const bar = $("tools-update-bar");
    const stage = $("tools-update-stage");
    if (running) {
      bar.removeAttribute("value");
      const current = rows.find((row) => !TERMINAL.has(row.key));
      stage.textContent = op.checking ? t("Checking Auto-Pigeon's releases…") : current ? current.title + ": " + current.text : t("Working…");
      $("tools-update-elapsed").textContent = current?.key === "waiting"
        ? t("Waiting {seconds} s for the work it names to finish; nothing is stopped for it.", { seconds })
        : t("{seconds} s so far.", { seconds });
    } else {
      bar.max = 1;
      bar.value = 1;
      stage.textContent = failed ? t("Update finished with problems") : t("Everything is up to date");
      $("tools-update-elapsed").textContent = t("Took {seconds} s.", { seconds });
      window.clearInterval(opTicker);
    }
    const result = $("tools-update-result");
    result.hidden = running && !op.error;
    result.className = "message" + (failed ? " error" : " ok");
    result.textContent = op.error || (running ? "" : failed
      ? t("What failed is listed above; the version in use was kept.")
      : t("The newest verified versions are installed."));
    $("tools-update-retry").hidden = running || !failed;
    $("tools-update-hide").hidden = !running;
    $("tools-update-done").hidden = running;
    if (!running && opHidden && !op.told) {
      op.told = true;
      record(failed ? t("An update finished with problems") : t("Everything is up to date"), "", failed ? "error" : "ok");
    }
  }

  // The sidebar's Update: check everything now, install what is newer.
  async function updateEverything() {
    if (op && !op.error && (op.checking || [...document.querySelectorAll("#tools-update-steps [data-state]")].some((row) => !TERMINAL.has(row.dataset.state)))) {
      showProgress(); // one operation at a time: a second press shows it
      return;
    }
    if (offerShown) closeOffer();
    startOperation({ checking: true, opener: document.activeElement });
    const { ok, body } = await api("/api/v1/tools/update", { method: "POST", body: {} });
    if (!op) return;
    op.checking = false;
    if (!ok) {
      op.error = body.error || t("The update could not be started.");
      saveOp();
      renderProgress();
      return;
    }
    op.steps = body.steps || [];
    for (const step of op.steps) op.targets[step.tool] = step.to || step.from || "";
    saveOp();
    apply(body.status);
    schedule(FAST_MS);
  }

  $("sidebar-update")?.addEventListener("click", () => updateEverything());
  $("tools-update-hide").addEventListener("click", hideProgress);
  $("tools-update-retry").addEventListener("click", () => { endOperation(); updateEverything(); });
  $("tools-update-done").addEventListener("click", () => {
    const opener = op?.opener;
    endOperation();
    $("tools-update-modal").hidden = true;
    if (opener?.isConnected) opener.focus(); else $("sidebar-update")?.focus();
  });

  // --- Settings ---------------------------------------------------------------

  // The Companion's own row: how this copy is installed, what an update of
  // it does, and how the last one ended.
  function selfLines(tool) {
    const self = tool.self || {};
    const lines = [];
    const muted = (text) => el("p", { className: "muted", text });
    if (self.mode === "portable") lines.push(muted(t("Updated by replacing its own files in {root}; nothing else in that folder is touched.", { root: self.root })));
    if (self.mode === "manual") lines.push(muted(t("Updates are downloaded and checked; you put them in place: {why}.", { why: self.reason })));
    if (self.mode === "development") {
      lines.push(muted(t("Not updated automatically: {why}.", { why: self.reason })));
      if (self.manual) {
        lines.push(el("p", { children: [
          document.createTextNode(t("The newest release is {version}. To switch to it, download it yourself: ", { version: self.manual.version })),
          el("a", { text: t("release {version}", { version: self.manual.version }), attrs: { href: self.manual.notes_url, target: "_blank", rel: "noopener noreferrer" } }),
        ] }));
      }
    }
    const last = self.last;
    if (last) {
      const words = {
        updated: t("Last update: {from} → {to}, complete.", last),
        activated: t("Last update: {from} → {to} is in place; it is confirmed when the Companion next starts.", last),
        rolled_back: t("Last update: {from} → {to} was taken back out: {detail}", { ...last, detail: last.detail || "" }),
        failed: t("Last update: {from} → {to} was not installed: {detail}", { ...last, detail: last.detail || "" }),
        recovery_needed: t("Last update: {from} → {to} was interrupted and the folder needs recovery: {detail}", { ...last, detail: last.detail || "" }),
      };
      lines.push(el("p", { className: last.outcome === "updated" || last.outcome === "activated" ? "muted" : "message error", text: words[last.outcome] || last.outcome }));
    }
    return lines;
  }

  // After the page reloaded across an update restart, say how it went.
  function reportSelfUpdate() {
    let flagged = null;
    try { flagged = window.sessionStorage.getItem("aucom:self-update"); } catch { return; }
    if (!flagged) return;
    const self = (status?.tools || []).find(isSelf);
    const last = self?.self?.last;
    // "activated" is the old side's word; the new side's "updated" follows
    // within a moment of it serving. Wait for an outcome, at most a minute.
    if ((!last || last.outcome === "activated") && Date.now() - Number(flagged) < 60000) return;
    try { window.sessionStorage.removeItem("aucom:self-update"); } catch { /* storage refused */ }
    if (!last || last.outcome === "activated") return;
    if (last.outcome === "updated") record(t("Auto-Pigeon Companion updated from {from} to {to}.", last), "", "ok");
    else record(t("The update to {to} was taken back out; Auto-Pigeon Companion {from} is running.", last), last.detail || "", "error");
  }

  function renderSettings() {
    const list = $("tools-settings-list");
    if (!list) return;
    list.replaceChildren();
    if (!status?.available) {
      $("tools-settings-state").textContent = t("Automatic build-tool setup is not running in this Companion.");
      $("tools-check").hidden = true;
      $("tools-reset-skipped").hidden = true;
      return;
    }
    $("tools-settings-state").textContent = t("For {target}, {channel} channel, in {root}.", {
      target: status.target, channel: status.channel, root: status.root }) + (status.root_error ? " " + status.root_error : "");
    $("tools-check").hidden = !status.enabled;
    let skipped = 0;
    for (const tool of status.tools || []) {
      skipped += (tool.skipped || []).length;
      const item = el("li", { className: "tools-row", children: [
        el("strong", { text: tool.title }), document.createTextNode(" — " + (isSelf(tool) && tool.phase === "ready"
          ? t("Running {version}", { version: tool.version }) : phaseLine(tool))),
      ] });
      if (isSelf(tool)) selfLines(tool).forEach((line) => item.append(line));
      if (tool.detail) item.append(el("p", { className: "muted", text: tool.detail }));
      if (tool.repair) item.append(el("p", { className: "message", text: t("Being replaced by the newest verified release: {why}", { why: tool.repair }) }));
      if (tool.offer) item.append(el("p", { text: t("Version {version} is available.", { version: tool.offer.version }) }));
      // A chosen update that failed: said here, quietly, with what to do. The
      // version in use is untouched (it is the line above).
      if (tool.failure && tool.active && tool.failure.version !== tool.active.version) {
        item.append(el("p", { className: "message error", text: t("The update to {version} failed: {why}", {
          version: tool.failure.version, why: tool.failure.error }) }));
        const retry = el("button", { text: t("Retry"), className: "secondary", attrs: { type: "button" } });
        retry.addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
          const { ok, body } = await api(`/api/v1/tools/${encodeURIComponent(tool.id)}/retry`, { method: "POST", body: {} });
          if (ok) apply(body); else setMessage("tools-check-message", body.error || "", "error");
          schedule(FAST_MS);
        }));
        const row = el("div", { className: "row", children: [retry] });
        if (tool.pending) {
          const drop = el("button", { text: t("Cancel the update"), className: "secondary", attrs: { type: "button" } });
          drop.addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
            const { ok, body } = await api(`/api/v1/tools/${encodeURIComponent(tool.id)}/cancel-pending`, { method: "POST", body: {} });
            if (ok) apply(body); else setMessage("tools-check-message", body.error || "", "error");
          }));
          row.append(drop);
        }
        item.append(row);
      } else if (tool.pending?.manual) {
        item.append(el("p", { className: "message", text: t("Downloaded; replace it yourself.") + " " + tool.pending.manual }));
      } else if (tool.pending?.restarting) {
        item.append(el("p", { className: "muted", text: t("Restarting to install version {version}…", { version: tool.pending.version }) }));
      } else if (tool.pending?.waiting?.length) {
        // What it waits for, as the coordinator last saw it — no transfer,
        // no progress: the download is over (01_NEW_368 §4).
        item.append(el("p", { className: "muted", text: tool.pending.staged
          ? t("Version {version} is downloaded and checked; it is installed once this ends: {work}.", { version: tool.pending.version, work: tool.pending.waiting.join("; ") })
          : t("Version {version} waits: {work}.", { version: tool.pending.version, work: tool.pending.waiting.join("; ") }) }));
      } else if (tool.pending) {
        item.append(el("p", { className: "muted", text: tool.pending.intent === "on_exit"
          ? (tool.pending.staged
            ? t("Version {version} is downloaded and checked; it is installed when the Companion closes.", { version: tool.pending.version })
            : t("Version {version} will be installed when the Companion closes.", { version: tool.pending.version }))
          : t("Installing version {version}…", { version: tool.pending.version }) }));
      }
      if (tool.pending && !tool.failure) {
        const drop = el("button", { text: t("Cancel the update"), className: "secondary", attrs: { type: "button" } });
        drop.addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
          const { ok, body } = await api(`/api/v1/tools/${encodeURIComponent(tool.id)}/cancel-pending`, { method: "POST", body: {} });
          if (ok) apply(body); else setMessage("tools-check-message", body.error || "", "error");
        }));
        if (!tool.pending.restarting) item.append(el("div", { className: "row", children: [drop] }));
      }
      for (const s of tool.skipped || []) item.append(el("p", { className: "muted", text: t("Not offered again: {version}", { version: s.version }) }));
      for (const profile of status.profiles?.[tool.id] || []) {
        if (!profile.custom || !tool.active) continue;
        const use = el("button", { text: t("Use the Companion's {version} for {name}", { version: tool.active.version, name: profile.name }),
          className: "secondary", attrs: { type: "button" } });
        use.addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
          const { ok, body } = await api(`/api/v1/profiles/${encodeURIComponent(profile.id)}/use-managed`, { method: "POST", body: {} });
          setMessage("tools-check-message", ok ? t("{name} now uses the Companion's installation.", { name: profile.name }) : body.error || "", ok ? "ok" : "error");
          poll();
        }));
        item.append(el("p", { className: "muted", text: t("{name} uses programs you chose; it is left as it is.", { name: profile.name }) }), use);
      }
      const details = el("details", { children: [el("summary", { text: t("Details") }), el("dl", { className: "paths", children: detailsList(tool) })] });
      item.append(details);
      list.append(item);
    }
    $("tools-reset-skipped").hidden = skipped === 0;
  }

  $("tools-check").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    setMessage("tools-check-message", t("Checking…"), "busy");
    const { ok, body } = await api("/api/v1/tools/check", { method: "POST", body: {} });
    if (!ok) { setMessage("tools-check-message", body.error || "", "error"); return; }
    // One sentence per tool: the outcomes are in the tools' order.
    const outcomes = body.outcomes || [];
    const tools = body.status?.tools || [];
    const lines = outcomes.map((o, i) => {
      const title = tools[i]?.title || "";
      if (o.checked) return t("{tool}: checked.", { tool: title });
      if (o.retry_at) return t("{tool}: not checked ({why}); try again after {at}.", { tool: title, why: o.message, at: when(o.retry_at) });
      return t("{tool}: the check failed: {why}", { tool: title, why: o.message });
    });
    setMessage("tools-check-message", lines.join(" "), outcomes.some((o) => o.checked) ? "ok" : "error");
    askedToCheck = true;
    apply(body.status);
    askedToCheck = false;
  }));

  $("tools-reset-skipped").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    const { ok, body } = await api("/api/v1/tools/skipped/reset", { method: "POST", body: {} });
    if (ok) apply(body); else setMessage("tools-check-message", body.error || "", "error");
  }));

  // --- polling ----------------------------------------------------------------

  function needsFast() {
    if (op && $("tools-update-done").hidden) return true; // an Update is running
    return (status?.tools || []).some((tool) => ["checking", "downloading", "verifying"].includes(tool.phase) ||
      (tool.pending && (!tool.pending.staged || tool.pending.waiting?.length))) || status?.setting_up ||
      (status?.checks || []).length > 0;
  }

  function apply(body) {
    if (!body) return;
    status = body;
    const ready = {};
    let became = false;
    for (const tool of status.tools || []) {
      ready[tool.id] = tool.phase === "ready" ? tool.version : "";
      if (ready[tool.id] && ready[tool.id] !== wasReady[tool.id] && wasReady[tool.id] !== undefined) became = true;
    }
    wasReady = ready;
    const notices = status.notices || [];
    if (seenNotices === null) {
      seenNotices = new Set(notices.map((n) => n.id));
    } else {
      for (const notice of notices) {
        if (seenNotices.has(notice.id)) continue;
        seenNotices.add(notice.id);
        record(notice.text, "", "ok");
      }
    }
    renderSetup();
    renderSettings();
    maybeOffer();
    reportSelfUpdate();
    if (became) {
      // Setup or an update finished: what was "needs setup" may be ready now.
      // Re-check, never start: a pending build request goes back to review.
      document.dispatchEvent(new CustomEvent("aucom:tools-ready"));
      for (const name of ["play", "profiles"]) {
        const area = $("area-" + name);
        if (area && !area.hidden) window.AUCOM.areas[name]?.refresh?.();
      }
    }
    askToInstall();
    tellInstalled();
  }

  function schedule(ms) {
    window.clearTimeout(timer);
    timer = window.setTimeout(poll, ms);
  }

  async function poll() {
    const { ok, body } = await api("/api/v1/tools");
    if (ok) {
      // A page that reloaded across the Companion's restart takes up the
      // operation it was following and confirms how it ended.
      if (!op && !resumed) {
        resumed = true;
        const kept = loadOp();
        if (kept) {
          op = { ...kept, restarting: false };
          status = body;
          showProgress();
        }
      }
      apply(body);
    }
    settleFirstLook();
    schedule(needsFast() ? FAST_MS : SLOW_MS);
  }
  let resumed = false;

  window.addEventListener("focus", () => poll());
  window.AUCOM.toolsStatus = () => status;
  poll();
})();
