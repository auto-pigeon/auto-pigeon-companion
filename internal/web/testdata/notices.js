// The operational-notice banner, driven inside the real page (241).
//
// Loaded by internal/web/notices_browser_test.go after the application's own
// scripts. It asserts on what the DOM and the page's storage say — never on a
// fetch the banner made — so it fails when the banner renders nothing, even if
// every route answered.

"use strict";

(async () => {
  const steps = [];
  let fatal = "";
  const log = (text) => {
    fetch("/journey/log", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: String(text) }),
    }).catch(() => {});
  };
  const record = (step, ok, detail) => {
    steps.push({ step, ok: Boolean(ok), detail: String(detail || "") });
    log(`${ok ? "ok  " : "FAIL"} ${step} — ${detail || ""}`);
  };
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  async function waitFor(what, predicate, timeout = 20000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      let value;
      try {
        value = predicate();
      } catch {
        value = null;
      }
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
      await sleep(120);
    }
  }
  const $ = (id) => document.getElementById(id);
  const token = document.querySelector('meta[name="aucom-api-token"]').content;
  const local = (path, body) =>
    fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-AUCOM-Token": token },
      body: JSON.stringify(body || {}),
    });
  const item = (title) => [...$("notice-list").children].find((li) => li.textContent.includes(title)) || null;
  const cached = () => {
    try {
      return window.localStorage.getItem("aucom.notices.cache/1") || "";
    } catch {
      return "";
    }
  };

  try {
    const settings = await (await fetch("/journey/config")).json();
    const { publicTitle, privateTitle, email, password } = settings;

    await waitFor("the notice banner", () => !$("notice-banner").hidden && item(publicTitle));
    record(
      "the banner shows the public notice, by the SERVER's clock (it is hours off this one)",
      item(publicTitle),
      $("notice-list").textContent.slice(0, 120)
    );
    const publicItem = item(publicTitle);
    record(
      "a notice's body is text: markup in it is shown, never rendered",
      publicItem.textContent.includes("<b>not markup</b>") && !publicItem.querySelector("b"),
      publicItem.querySelector(".notice__body")?.textContent
    );
    record(
      "the banner states what notices cannot do",
      $("notice-limit").textContent.includes("unplanned outage") && !$("notice-limit").hidden,
      $("notice-limit").textContent.trim()
    );
    record("the public answer is cached for a later start", cached().includes(publicTitle), cached().slice(0, 80));

    const login = await local("/api/auth/login", { email, password });
    record("signing in through the local server", login.ok, `HTTP ${login.status}`);
    await window.AUCOM.refreshStatus();
    await waitFor("the account-only notice after signing in", () => item(privateTitle));
    const privateItem = item(privateTitle);
    record(
      "signing in shows the account-only notice at once, without waiting for a poll",
      privateItem,
      $("notice-list").textContent.slice(0, 160)
    );
    record(
      "a critical notice offers no Dismiss and is announced",
      !privateItem.querySelector("button") && privateItem.getAttribute("role") === "alert",
      privateItem.outerHTML.slice(0, 160)
    );
    record(
      "the critical notice is listed first",
      $("notice-list").firstElementChild === privateItem,
      [...$("notice-list").children].map((li) => li.dataset.noticeId).join(", ")
    );
    record(
      "an account-only notice never reaches the page's storage",
      !cached().includes(privateTitle) && cached().includes(publicTitle),
      cached().slice(0, 120)
    );

    const dismiss = [...item(publicTitle).querySelectorAll("button")].find((b) => b.textContent.includes("Dismiss"));
    dismiss.click();
    await waitFor("the dismissed notice to go", () => !item(publicTitle));
    record("Dismiss hides the notice for this account", !item(publicTitle) && item(privateTitle), $("notice-list").textContent.slice(0, 80));

    const logout = await local("/api/auth/logout");
    record("signing out through the local server", logout.ok, `HTTP ${logout.status}`);
    await window.AUCOM.refreshStatus();
    await waitFor("the account-only notice to go on sign-out", () => !item(privateTitle));
    record("signing out removes the account-only notice at once", !item(privateTitle), $("notice-list").textContent.slice(0, 80));
    await waitFor("the public notice for nobody", () => item(publicTitle));
    record(
      "a dismissal is filed under the account: signed out, the notice is shown again",
      item(publicTitle),
      $("notice-list").textContent.slice(0, 80)
    );
    record(
      "only the contract's telemetry names are counted",
      Object.keys(window.AUCOM.notices.counters).sort().join(",") ===
        "notices.fetch_failed,notices.response_invalid,notices.stale_cache_used",
      JSON.stringify(window.AUCOM.notices.counters)
    );
  } catch (err) {
    fatal = err && err.message ? err.message : String(err);
    log("fatal: " + fatal);
  }

  await fetch("/journey/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error: fatal }),
  });
})();
