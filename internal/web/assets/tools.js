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

  const isSelf = (tool) => tool.kind === "application";

  // A dialog never opens over another one — a profile's review, the sign-in,
  // Quit — or over a file picker the page opened: the offer waits, with the
  // badge in the header, for a moment when nothing else is asking.
  function busyElsewhere() {
    return Boolean(document.querySelector(".auth-modal:not([hidden]):not(#tools-update-modal)") ||
      document.querySelector("dialog[open]") || window.AUCOM.pickingFile);
  }

  function offered() {
    return (status?.tools || []).filter((x) => x.offer && x.active);
  }

  function maybeOffer() {
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
    if (busyElsewhere()) return;
    openOffer(list);
  }

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
      ? t("Don't ask again for this version ({version})", { version: one.offer.version })
      : t("Don't ask again for these versions");
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
        details.append(el("dt", { text: t("Bundled extractor") }), el("dd", { text: tool.offer.sidecar.version + " (protocol " + tool.offer.sidecar.protocol + ")" }));
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
      target: status.target, channel: status.channel, root: status.root });
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
      } else if (tool.pending && !tool.pending.waiting?.length) {
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
