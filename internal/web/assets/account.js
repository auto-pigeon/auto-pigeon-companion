// The account button, its menu, the back-online banner and Sync to cloud
// (NEW_244D, operator: "signed in all the time, offline as a fallback, and
// syncing must be EASY and DISCOVERABLE when the connection comes back").
//
// What this file never decides: whether the account is reachable or what is in
// sync. It asks GET /api/v1/account — see internal/web/account.go — and draws
// the answer. The sync itself is a review (a dry run the Companion computes)
// followed by the person pressing Sync.

"use strict";

(() => {
  const { $, el, api, setMessage, t, withBusy, record, announce } = window.AUCOM;

  const CHECK_EVERY_MS = 60000;
  let timer = null;
  let last = null; // the previous account answer, for the offline -> online edge
  let dismissedAt = 0;

  function setDot(kind) {
    $("account-dot").className = "status-dot " + kind;
  }

  function draw(status) {
    const pending = status.to_upload + status.to_download;
    let label;
    let kind;
    let note;
    if (!status.online) {
      label = "Offline";
      kind = "offline";
      note = t("Your account cannot be reached: {reason}. You can keep building; sync when you are back.", { reason: status.offline_reason || t("no answer") });
    } else if (status.conflicts > 0) {
      label = "Sync conflict";
      kind = "conflict";
      note = t("{n} profile(s) differ between this computer and your account under the same version.", { n: status.conflicts });
    } else if (pending > 0) {
      label = t("Sync needed ({n})", { n: pending });
      kind = "pending";
      note = t("{up} to upload, {down} to download.", { up: status.to_upload, down: status.to_download });
    } else {
      label = "Synced";
      kind = "synced";
      note = "Your profiles on this computer and in your account are the same.";
    }
    $("account-label").textContent = label;
    $("account-button").title = note;
    setDot(kind);
    $("account-sync-note").textContent = note;
    $("account-server").textContent = status.server_label ? ` · ${status.server_label}` : "";
    $("account-sync").disabled = !status.online;

    // Back online, or signed in, with something waiting: say so once, with the
    // action beside it. Not repeated for five minutes after "Later".
    const cameBack = last && !last.online && status.online;
    const firstLook = !last && status.online;
    if ((cameBack || firstLook) && (pending > 0 || status.conflicts > 0) && Date.now() - dismissedAt > 300000) {
      $("reconnect-text").textContent = cameBack
        ? t("You are back online. {n} profile(s) are waiting to sync with your account.", { n: pending })
        : t("{n} profile(s) on this computer and in your account are not in sync.", { n: pending });
      $("reconnect-banner").hidden = false;
    }
    if (status.online && pending === 0 && status.conflicts === 0) $("reconnect-banner").hidden = true;
    last = status;
  }

  async function check() {
    const { ok, body } = await api("/api/v1/account");
    if (!ok || !body.signed_in) return null;
    draw(body);
    return body;
  }

  function startChecking() {
    if (timer) return;
    check();
    timer = window.setInterval(check, CHECK_EVERY_MS);
  }

  function stopChecking() {
    if (timer) window.clearInterval(timer);
    timer = null;
    last = null;
    $("reconnect-banner").hidden = true;
  }

  // --- the menu --------------------------------------------------------------

  function setMenu(open) {
    $("account-dropdown").hidden = !open;
    $("account-button").setAttribute("aria-expanded", String(open));
    if (open) $("account-dropdown").querySelector("button:not([disabled]), a:not([hidden])")?.focus();
  }

  $("account-button").addEventListener("click", () => setMenu($("account-dropdown").hidden));
  document.addEventListener("click", (event) => {
    if (!$("account-menu").contains(event.target)) setMenu(false);
  });
  $("account-dropdown").addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      setMenu(false);
      $("account-button").focus();
    }
  });
  $("account-check").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      const status = await check();
      if (status) announce(status.online ? "Your account is reachable." : "Your account cannot be reached.");
    })
  );
  $("account-sync").addEventListener("click", () => {
    setMenu(false);
    openSync();
  });
  $("reconnect-sync").addEventListener("click", () => openSync());
  $("reconnect-dismiss").addEventListener("click", () => {
    dismissedAt = Date.now();
    $("reconnect-banner").hidden = true;
  });

  // --- Sync to cloud ---------------------------------------------------------

  const words = {
    upload: "Upload to your account",
    download: "Download to this computer",
    none: "Not synced",
  };

  function drawOutcomes(outcomes) {
    const list = $("sync-list");
    list.replaceChildren();
    if (outcomes.length === 0) {
      list.append(el("li", { className: "muted", text: "Nothing to sync: this computer and your account hold the same profiles." }));
      return;
    }
    for (const outcome of outcomes) {
      const head = el("div", { className: "row-head" });
      head.append(el("strong", { text: outcome.name || "A profile" }));
      const state = outcome.result === "planned" ? outcome.action : outcome.result;
      head.append(el("span", { className: "badge " + (state === "done" ? "succeeded" : state === "failed" || state === "conflict" ? "failed" : "queued"),
        text: outcome.result === "planned" ? words[outcome.action] || outcome.action : outcome.result }));
      const item = el("li", { children: [head] });
      if (outcome.action === "download" && (outcome.permissions || []).length) {
        item.append(el("p", { className: "muted", text: "Once downloaded, this computer approves it to:" }));
        const perms = el("ul");
        for (const permission of outcome.permissions) perms.append(el("li", { text: permission }));
        item.append(perms);
      }
      if (outcome.error) item.append(el("p", { className: "message error", text: outcome.error }));
      list.append(item);
    }
  }

  async function openSync() {
    $("reconnect-banner").hidden = true;
    $("sync-modal").hidden = false;
    $("sync-run").disabled = true;
    setMessage("sync-message", "");
    $("sync-intro").textContent = "Checking what differs between this computer and your account…";
    $("sync-list").replaceChildren();
    $("sync-close").focus();
    const { ok, body } = await api("/api/v1/account/sync", { method: "POST", body: { dry_run: true } });
    if (!ok) {
      $("sync-intro").textContent = "";
      setMessage("sync-message", body.error || "could not reach your account", "error");
      return;
    }
    const actionable = (body.outcomes || []).filter((outcome) => outcome.action !== "none");
    $("sync-intro").textContent = actionable.length
      ? t("This will change {n} profile(s). Read the list, then press Sync.", { n: actionable.length })
      : "Nothing needs to move.";
    drawOutcomes(body.outcomes || []);
    $("sync-run").disabled = actionable.length === 0;
  }

  function closeSync() {
    $("sync-modal").hidden = true;
  }

  $("sync-run").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      setMessage("sync-message", "Syncing…", "busy");
      const { ok, body } = await api("/api/v1/account/sync", { method: "POST", body: { confirmed: true } });
      if (!ok) {
        setMessage("sync-message", body.error || "the sync did not complete", "error");
        return;
      }
      drawOutcomes(body.outcomes || []);
      const failed = (body.outcomes || []).filter((outcome) => outcome.result === "failed").length;
      setMessage("sync-message", failed ? t("{n} profile(s) could not be synced; the reason is under each.", { n: failed }) : t("Synced."), failed ? "error" : "ok");
      record("Synced profiles with your account", `${(body.outcomes || []).length} change(s)`, failed ? "failed" : "ok");
      $("sync-run").disabled = true;
      if (body.status) draw(body.status);
      window.AUCOM.areas.profiles?.refresh?.();
    })
  );
  $("sync-cancel").addEventListener("click", closeSync);
  $("sync-close").addEventListener("click", closeSync);
  document.querySelector('[data-dismiss="sync"]').addEventListener("click", closeSync);
  $("sync-modal").addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeSync();
  });

  // Coming back: the browser says when the network returns and when the window
  // is looked at again, and both are a reason to ask now rather than in a minute.
  window.addEventListener("online", () => { if (timer) check(); });
  window.addEventListener("focus", () => { if (timer) check(); });

  window.AUCOM.account = {
    statusChanged(body) {
      if (body.authenticated) startChecking();
      else stopChecking();
      const site = window.AUCOM.officialSite(body);
      $("account-site").hidden = !site;
      if (site) {
        $("account-site").href = site.url;
        $("account-site").textContent = t("Open {site}", { site: site.host });
      }
    },
    check,
  };
})();
