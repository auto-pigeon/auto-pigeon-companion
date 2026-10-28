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
  let offerShown = null; // { tool, identity }
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

  // --- the setup card --------------------------------------------------------

  function renderSetup() {
    const card = $("tools-setup");
    const tools = status?.tools || [];
    const waiting = tools.filter((tool) => tool.pending?.staged && tool.pending.waiting?.length && !hiddenWaiting[tool.id + tool.pending.version]);
    const settingUp = Boolean(status?.available && status.setting_up);
    card.hidden = !settingUp && waiting.length === 0;
    if (card.hidden) return;
    $("tools-setup-title").textContent = settingUp ? t("Setting up build tools") : t("Build tools update ready");
    const list = $("tools-setup-list");
    list.replaceChildren();
    for (const tool of tools) {
      if (settingUp) {
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
    $("tools-setup-retry").hidden = !(settingUp && failed);
    const details = $("tools-setup-details");
    details.replaceChildren();
    for (const tool of tools) {
      details.append(el("dt", { text: tool.title, className: "tools-details-head" }), el("dd", { text: "" }), ...detailsList(tool));
    }
  }

  $("tools-setup-retry").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    setMessage("tools-setup-message", "");
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

  function maybeOffer() {
    const modal = $("tools-update-modal");
    const tools = status?.tools || [];
    if (offerShown) {
      const tool = tools.find((x) => x.id === offerShown.tool);
      // Answered elsewhere — another tab, the terminal — or replaced by a
      // newer check: this dialog is about bytes no longer on offer.
      if (!tool?.offer || !sameIdentity(identityOf(tool.offer), offerShown.identity)) closeOffer();
      return;
    }
    if (!modal.hidden || document.querySelector(".auth-modal:not([hidden])")) return;
    const tool = tools.find((x) => x.offer && x.active);
    if (!tool) return;
    openOffer(tool);
  }

  function openOffer(tool) {
    const offer = tool.offer;
    offerShown = { tool: tool.id, identity: identityOf(offer), opener: document.activeElement };
    $("tools-update-heading").textContent = t("Update {tool}?", { tool: tool.title });
    $("tools-update-intro").textContent = t("{tool} {from} is installed. Version {to} is available.", {
      tool: tool.title, from: tool.active.version, to: offer.version });
    const notes = $("tools-update-notes");
    notes.href = offer.notes_url;
    notes.textContent = t("Release notes for {version}", { version: offer.version });
    $("tools-update-skip").checked = false;
    $("tools-update-skip-label").textContent = t("Don't ask again for this version ({version})", { version: offer.version });
    const details = $("tools-update-details");
    details.replaceChildren(
      el("dt", { text: t("Download size") }), el("dd", { text: bytes(offer.size) }),
      el("dt", { text: t("Installed in") }), el("dd", { text: t("the Companion's own tools folder, beside {from}, which is kept", { from: tool.active.version }) }),
      el("dt", { text: t("SHA-256") }), el("dd", { text: offer.sha256, className: "mono" }),
      el("dt", { text: t("Qualification") }), el("dd", { text: offer.qualification || "" }),
    );
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
      const { ok, status: code, body } = await api(`/api/v1/tools/${encodeURIComponent(shown.tool)}/decide`, {
        method: "POST",
        body: { decision, ...shown.identity, dont_ask: decision === "cancel" && $("tools-update-skip").checked },
      });
      if (!ok) {
        setMessage("tools-update-message", code === 409 ? t("This update is no longer the one on offer.") : body.error || "", "error");
        if (code === 409) { closeOffer(); poll(); }
        return;
      }
      if (decision === "now") record(t("Updating {tool} to {version}", { tool: shown.tool, version: shown.identity.version }), "", "busy");
      if (decision === "on_exit") record(t("{tool} {version} will be installed when the Companion closes", { tool: shown.tool, version: shown.identity.version }), "", "ok");
      closeOffer();
      apply(body);
    });
  }

  $("tools-update-now").addEventListener("click", (event) => decide("now", event.currentTarget));
  $("tools-update-later").addEventListener("click", (event) => decide("on_exit", event.currentTarget));
  $("tools-update-cancel").addEventListener("click", (event) => decide("cancel", event.currentTarget));
  // The X and Escape are Cancel WITHOUT "don't ask again", whatever the box
  // says: only Cancel confirms the box.
  const dismiss = () => {
    $("tools-update-skip").checked = false;
    decide("cancel", $("tools-update-close"));
  };
  $("tools-update-close").addEventListener("click", dismiss);
  document.querySelector('[data-dismiss="tools-update"]').addEventListener("click", dismiss);
  $("tools-update-modal").addEventListener("keydown", (event) => {
    if (event.key === "Escape") dismiss();
    trapFocus(event, $("tools-update-dialog"), "button, input, a[href], summary");
  });

  // --- Settings ---------------------------------------------------------------

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
      target: status.target, channel: status.channel, root: status.root });
    $("tools-check").hidden = !status.enabled;
    let skipped = 0;
    for (const tool of status.tools || []) {
      skipped += (tool.skipped || []).length;
      const item = el("li", { className: "tools-row", children: [
        el("strong", { text: tool.title }), document.createTextNode(" — " + phaseLine(tool)),
      ] });
      if (tool.detail) item.append(el("p", { className: "muted", text: tool.detail }));
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
      } else if (tool.pending && !tool.pending.waiting?.length) {
        item.append(el("p", { className: "muted", text: tool.pending.intent === "on_exit"
          ? t("Version {version} will be installed when the Companion closes.", { version: tool.pending.version })
          : t("Installing version {version}…", { version: tool.pending.version }) }));
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
    apply(body.status);
  }));

  $("tools-reset-skipped").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    const { ok, body } = await api("/api/v1/tools/skipped/reset", { method: "POST", body: {} });
    if (ok) apply(body); else setMessage("tools-check-message", body.error || "", "error");
  }));

  // --- polling ----------------------------------------------------------------

  function needsFast() {
    return (status?.tools || []).some((tool) => ["checking", "downloading", "verifying"].includes(tool.phase) ||
      (tool.pending && (!tool.pending.staged || tool.pending.waiting?.length))) || status?.setting_up;
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
    if (became) {
      // Setup or an update finished: what was "needs setup" may be ready now.
      // Re-check, never start: a pending build request goes back to review.
      document.dispatchEvent(new CustomEvent("aucom:tools-ready"));
      for (const name of ["play", "profiles"]) {
        const area = $("area-" + name);
        if (area && !area.hidden) window.AUCOM.areas[name]?.refresh?.();
      }
    }
  }

  function schedule(ms) {
    window.clearTimeout(timer);
    timer = window.setTimeout(poll, ms);
  }

  async function poll() {
    const { ok, body } = await api("/api/v1/tools");
    if (ok) apply(body);
    schedule(needsFast() ? FAST_MS : SLOW_MS);
  }

  window.addEventListener("focus", () => poll());
  window.AUCOM.toolsStatus = () => status;
  poll();
})();
