// Navigation and start-up.
//
// Three things here are deliberate:
//
//   - The area is in the URL fragment. A reload comes back to where the user
//     was, and the browser's Back button walks the areas — which is what a
//     person expects from a page in a browser, whatever else it is.
//   - Switching area moves focus to the heading. Without that, a keyboard or
//     screen-reader user presses a tab and is left where they were, in a page
//     whose content has silently changed underneath them.
//   - Nothing in this file caches what the server said. Every area re-reads,
//     because every one of them is showing state the CLI, another window or a
//     finished job may have changed.

"use strict";

(() => {
  const { $, el, api, setMessage, announce, record } = window.AUCOM;

  const areaNames = ["library", "build", "run", "profiles", "jobs", "settings"];
  const titles = {
    library: "Library",
    build: "Build",
    run: "Run",
    profiles: "Profiles",
    jobs: "Jobs",
    settings: "Settings",
  };

  function show(area, { focus = true } = {}) {
    if (!areaNames.includes(area)) area = "library";
    for (const name of areaNames) {
      $("area-" + name).hidden = name !== area;
    }
    for (const tab of document.querySelectorAll(".area-tab")) {
      if (tab.dataset.area === area) tab.setAttribute("aria-current", "page");
      else tab.removeAttribute("aria-current");
    }
    $("area-heading").textContent = titles[area];
    if (focus) $("area-heading").focus();
    if (window.location.hash !== "#" + area) {
      window.history.replaceState(null, "", "#" + area);
    }
    window.AUCOM.areas[area]?.refresh?.();
  }

  for (const tab of document.querySelectorAll(".area-tab")) {
    tab.addEventListener("click", () => show(tab.dataset.area));
  }
  window.addEventListener("hashchange", () => {
    show(window.location.hash.replace(/^#/, ""), { focus: false });
  });

  // --- status and identity ---------------------------------------------------

  async function refreshStatus() {
    const { ok, body } = await api("/api/status");
    if (!ok) {
      $("identity").textContent = body.error;
      $("identity").className = "message error";
      return;
    }
    window.AUCOM.status = body;
    const backend = body.aub_base_url || "no backend address configured";
    $("identity").textContent = `v${body.version} · ${body.platform} · ${backend}`;
    $("identity").className = "muted";
    $("account-email").textContent = body.authenticated ? body.email || "signed in" : "not signed in";
    $("sign-out").hidden = !body.authenticated;

    const firstRun = $("first-run");
    firstRun.hidden = Boolean(body.authenticated);
    if (!body.authenticated) {
      $("first-run-why").textContent = body.aub_base_url
        ? `Your maps and assets live in auto-pigeon-backend at ${body.aub_base_url}. Everything else — ` +
          `builds, engines, jobs — works signed out.`
        : "No backend address is configured yet. Nothing in the Companion has a built-in one, so open " +
          "Settings and fill it in before signing in.";
    }

    $("extractor-state").textContent = !body.aue_available
      ? "No extractor is available. It is a separate program: install it, or set AUCOM_AUE_BINARY to a local build."
      : body.aue_verified
        ? `An extractor is available and was verified against the signed catalogue (${body.aue_provenance}).`
        : `An extractor is available but is an UNVERIFIED developer override (${body.aue_provenance}). ` +
          "Nothing has checked these bytes.";
    $("extractor-version").disabled = !body.aue_available;
  }
  window.AUCOM.refreshStatus = refreshStatus;

  $("sign-in-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = $("sign-in");
    button.disabled = true;
    window.AUCOM.busy("sign-in-message", "Signing in…");
    const { ok, body } = await api("/api/auth/login", {
      method: "POST",
      body: { email: $("email").value, password: $("password").value },
    });
    // Cleared on every outcome, success or failure: a wrong-password retry
    // should not leave the old value sitting in the DOM.
    $("password").value = "";
    button.disabled = false;
    if (!ok) {
      setMessage("sign-in-message", body.error, "error");
      record("Sign-in refused", body.error, "failed");
      return;
    }
    setMessage("sign-in-message", body.warning || `Signed in as ${body.email}.`, body.warning ? "" : "ok");
    record(`Signed in as ${body.email}`, "", "ok");
    await refreshStatus();
    show("library");
  });

  $("sign-out").addEventListener("click", async (event) => {
    await window.AUCOM.withBusy(event.currentTarget, async () => {
      const { ok, body } = await api("/api/auth/logout", { method: "POST" });
      if (!ok) {
        setMessage("sign-in-message", body.error, "error");
        return;
      }
      record("Signed out", "", "ok");
      announce("Signed out.");
      await refreshStatus();
      window.AUCOM.areas.library?.refresh?.();
    });
  });

  $("go-to-settings").addEventListener("click", () => show("settings"));

  // --- boot -----------------------------------------------------------------

  (async () => {
    // Settings first: the path fields ask it whether this machine has a file
    // chooser, and a field drawn before that answer arrives would offer a
    // Browse button that opens nothing.
    await window.AUCOM.areas.settings.refresh();
    await refreshStatus();
    window.AUCOM.renderActivity();
    show(window.location.hash.replace(/^#/, "") || "library", { focus: false });
    // The first-run panel takes focus only when it is the thing the user has
    // to deal with, and only on a real first load.
    if (!window.AUCOM.status.authenticated && !window.location.hash) {
      $("email").focus();
    }
  })();
})();
