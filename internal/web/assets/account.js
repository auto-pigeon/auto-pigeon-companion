// The account: whether this computer's profiles match the account's, and
// Sync profiles (NEW_244D, operator: "signed in all the time, offline as a
// fallback, and syncing must be EASY and DISCOVERABLE when the connection
// comes back"; NEW_508: said only when there is something to do).
//
// What this file never decides: whether the account is reachable or what is in
// sync. It asks GET /api/v1/account — see internal/web/account.go — and draws
// the answer. The sync itself is a review (a dry run the Companion computes)
// followed by the person pressing Sync profiles.
//
// Where it is shown (NEW_508):
//
//   - Nothing to sync, account reachable: NOTHING in the header. The Account
//     menu says "Profiles match your account." to whoever opens it. There is
//     no standing "Synced" light.
//   - Something to sync, or a conflict: one chip in the header, "Sync
//     profiles · Send 2 · Get 1", which opens the review. It replaced the
//     full-width "back online" bar; a chip takes no room from the page.
//   - Account unreachable: said in the Account menu, with Check connection.
//     Building and running do not need it, so the header stays quiet.
//
// "Sync" always names its object here. A map is downloaded (library.js) and a
// tool or the app is checked for updates (tools.js, Settings); neither is
// called a sync, and none of the three shares state with the others.

"use strict";

(() => {
  const { $, el, api, setMessage, t, withBusy, record, announce } = window.AUCOM;

  const CHECK_EVERY_MS = 60000;
  let timer = null;

  // "Send 2 profiles · Get 1 profile": the whole difference in a few words.
  function summary(up, down, conflicts = 0) {
    const parts = [];
    if (up) parts.push(up === 1 ? t("Send 1 profile") : t("Send {n} profiles", { n: up }));
    if (down) parts.push(down === 1 ? t("Get 1 profile") : t("Get {n} profiles", { n: down }));
    if (conflicts) parts.push(conflicts === 1 ? t("1 conflict") : t("{n} conflicts", { n: conflicts }));
    return parts.join(" · ");
  }

  function draw(status) {
    const pending = status.to_upload + status.to_download;
    const wants = status.online && (pending > 0 || status.conflicts > 0);
    const chip = $("account-button");
    chip.hidden = !wants;
    if (wants) {
      const short = [];
      if (status.to_upload) short.push(t("Send {n}", { n: status.to_upload }));
      if (status.to_download) short.push(t("Get {n}", { n: status.to_download }));
      if (status.conflicts) short.push(status.conflicts === 1 ? t("1 conflict") : t("{n} conflicts", { n: status.conflicts }));
      $("account-label").textContent = t("Sync profiles") + " · " + short.join(" · ");
      $("account-dot").className = "status-dot " + (status.conflicts ? "conflict" : "pending");
      chip.title = summary(status.to_upload, status.to_download, status.conflicts);
    }
    const note = !status.online
      ? t("Your account can't be reached: {reason}. Building and running still work.", { reason: status.offline_reason || t("no answer") })
      : wants ? summary(status.to_upload, status.to_download, status.conflicts)
        : t("Profiles match your account.");
    $("account-sync-note").textContent = note;
    $("account-sync-note").hidden = false;
    $("account-server").textContent = status.server_label ? ` · ${status.server_label}` : "";
    $("account-sync").hidden = !wants;
    $("account-check").hidden = status.online;
  }

  // check reads the account's answer and draws it. One at a time: the menu
  // opening, the window coming back and the minute's tick may all ask at once.
  let checking = null;
  function check() {
    if (checking) return checking;
    checking = (async () => {
      const { ok, body } = await api("/api/v1/account");
      checking = null;
      if (!ok || !body.signed_in) return null;
      draw(body);
      return body;
    })();
    return checking;
  }

  function startChecking() {
    if (timer) return;
    check();
    timer = window.setInterval(check, CHECK_EVERY_MS);
  }

  function stopChecking() {
    if (timer) window.clearInterval(timer);
    timer = null;
    $("account-button").hidden = true;
    for (const id of ["account-sync-note", "account-sync", "account-check"]) $(id).hidden = true;
  }

  // --- the header chip and the Account menu's entries --------------------------

  $("account-button").addEventListener("click", (event) => openSync(event.currentTarget));
  $("account-check").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      const status = await check();
      if (status) announce(status.online ? t("Your account is reachable.") : t("Your account can't be reached."));
    })
  );
  $("account-sync").addEventListener("click", () => openSync($("user-button")));

  // --- Sync profiles -----------------------------------------------------------
  //
  // The review is the server's dry run: names and directions, what an upload
  // discloses and what a download asks permission for. `reviewed` is the dry
  // run's own fingerprint of that list; pressing Sync profiles sends it back,
  // and a list that changed meanwhile is shown again instead of being acted on.

  const DIRECTION = {
    upload: "Send to your account",
    download: "Get on this computer",
  };
  const RESULT = { done: "Done", failed: "Failed", conflict: "Conflict" };
  let reviewed = "";
  let syncOpener = null;

  function drawOutcomes(outcomes) {
    const list = $("sync-list");
    list.replaceChildren();
    for (const outcome of outcomes) {
      const head = el("div", { className: "row-head" });
      head.append(el("strong", { text: outcome.name || t("A profile") }));
      const planned = outcome.result === "planned";
      const state = planned ? "queued" : outcome.result === "done" ? "succeeded" : "failed";
      head.append(el("span", { className: "badge " + state,
        text: t(planned ? DIRECTION[outcome.action] || outcome.action : RESULT[outcome.result] || outcome.result) }));
      const item = el("li", { children: [head] });
      if (outcome.result === "conflict") {
        // The server's own sentence says which versions; this says what to do.
        item.append(el("p", { text: t("Both copies changed. Save the one you want to keep as a new version.") }));
        if (outcome.error) item.append(el("details", { children: [el("summary", { text: t("Details") }), el("p", { className: "mono", text: outcome.error })] }));
      } else if (outcome.error) {
        item.append(el("p", { className: "message error", text: outcome.error }));
      }
      if (planned && outcome.action === "download" && (outcome.permissions || []).length) {
        item.append(el("p", { className: "muted", text: t("It asks for these permissions on this computer:") }),
          el("ul", { className: "sync-permissions", children: outcome.permissions.map((text) => el("li", { text })) }));
      }
      if (planned && outcome.action === "upload" && outcome.disclosures > 0) {
        item.append(el("p", { className: "muted", text: outcome.disclosures === 1
          ? t("Sending it shares 1 detail of the profile with your account; no path on this computer is sent.")
          : t("Sending it shares {n} details of the profile with your account; no path on this computer is sent.", { n: outcome.disclosures }) }));
      }
      list.append(item);
    }
  }

  function counts(outcomes) {
    const of = (action) => outcomes.filter((outcome) => outcome.action === action).length;
    return summary(of("upload"), of("download"), outcomes.filter((outcome) => outcome.result === "conflict").length);
  }

  function showReview(body, changed) {
    const outcomes = body.outcomes || [];
    const actionable = outcomes.filter((outcome) => outcome.action !== "none");
    reviewed = body.reviewed || "";
    $("sync-intro").textContent = outcomes.length ? counts(outcomes) : t("Profiles match your account.");
    drawOutcomes(outcomes);
    $("sync-run").textContent = t("Sync profiles");
    $("sync-run").disabled = actionable.length === 0;
    setMessage("sync-message", changed ? t("The list changed since you read it. Read it again, then press Sync profiles.") : "", changed ? "error" : "");
  }

  async function openSync(opener) {
    if ($("sync-modal").hidden) syncOpener = opener || document.activeElement;
    $("sync-modal").hidden = false;
    $("sync-run").disabled = true;
    $("sync-run").textContent = t("Sync profiles");
    setMessage("sync-message", "");
    $("sync-intro").textContent = t("Checking what differs between this computer and your account…");
    $("sync-list").replaceChildren();
    $("sync-close").focus();
    const { ok, body } = await api("/api/v1/account/sync", { method: "POST", body: { dry_run: true } });
    if (!ok) {
      $("sync-intro").textContent = "";
      setMessage("sync-message", body.error || t("could not reach your account"), "error");
      return;
    }
    showReview(body, false);
    if (!$("sync-run").disabled) $("sync-run").focus();
  }

  function closeSync() {
    if ($("sync-modal").hidden) return;
    $("sync-modal").hidden = true;
    // Back to what opened it, or to the next thing there is when that has
    // gone (the chip leaves the header once everything is synced).
    const back = syncOpener && document.contains(syncOpener) && !syncOpener.hidden ? syncOpener : $("user-button");
    syncOpener = null;
    back.focus();
  }

  $("sync-run").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      setMessage("sync-message", t("Syncing…"), "busy");
      const { ok, status, body } = await api("/api/v1/account/sync", { method: "POST", body: { confirmed: true, reviewed } });
      if (!ok && status === 409 && body.changed) {
        // What would be synced is no longer what was read: nothing was sent
        // or installed, and the new list is the review.
        showReview(body, true);
        return;
      }
      if (!ok) {
        setMessage("sync-message", body.error || t("the sync did not complete"), "error");
        return;
      }
      const outcomes = body.outcomes || [];
      drawOutcomes(outcomes);
      const failed = outcomes.filter((outcome) => outcome.result === "failed").length;
      const done = outcomes.filter((outcome) => outcome.result === "done").length;
      $("sync-intro").textContent = done === 1 ? t("1 profile synced.") : t("{n} profiles synced.", { n: done });
      setMessage("sync-message", failed ? (failed === 1 ? t("1 profile could not be synced; the reason is under it.")
        : t("{n} profiles could not be synced; the reason is under each.", { n: failed })) : "", failed ? "error" : "");
      record(t("Synced profiles with your account"), done === 1 ? t("1 profile synced.") : t("{n} profiles synced.", { n: done }), failed ? "failed" : "ok");
      // A failure may be worth another try, through a fresh review; a sync
      // that worked has nothing left to press.
      reviewed = "";
      $("sync-run").disabled = true;
      $("sync-retry").hidden = failed === 0;
      if (body.status) draw(body.status);
      window.AUCOM.areas.profiles?.refresh?.();
    })
  );
  $("sync-retry").addEventListener("click", () => { $("sync-retry").hidden = true; openSync(); });
  $("sync-cancel").addEventListener("click", closeSync);
  $("sync-close").addEventListener("click", closeSync);
  document.querySelector('[data-dismiss="sync"]').addEventListener("click", closeSync);
  $("sync-modal").addEventListener("keydown", (event) => {
    if (event.key === "Escape") { closeSync(); return; }
    window.AUCOM.trapFocus?.(event, $("sync-dialog"));
  });

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
