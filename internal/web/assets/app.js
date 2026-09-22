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
  const { $, el, api, setMessage, announce, record, wireCompatibilityReport } = window.AUCOM;

  // The compatibility-report panel is wired once and lives outside the areas,
  // because every area can open it. See core.js.
  wireCompatibilityReport();

  // `about` is last because it is the one area that is not a task: somebody
  // looking for a job should never have to pass the prose about the program to
  // reach it.
  const areaNames = ["play", "library", "build", "run", "games", "profiles", "new-profile", "jobs", "settings", "about"];
  const titles = {
    play: "Build & Run",
    library: "My Maps",
    build: "Build",
    run: "Run",
    games: "Live Games",
    profiles: "Profiles",
    "new-profile": "New profile",
    jobs: "Jobs",
    settings: "Settings",
    about: "About",
  };

  // The area a fresh page opens on. Signed out, that is Build: local work needs
  // no account, and opening on the one area that does told a first-time user
  // otherwise (NEW_244D).
  // Build & Run is the journey the program is for, so a signed-in window opens
  // on it. Signed out it is not usable — the map comes from the account — and
  // Build still is, so that is where a first run lands (NEW_244D).
  function defaultArea() {
    return window.AUCOM.status?.authenticated ? "play" : "build";
  }

  function show(area, { focus = true } = {}) {
    // An area may carry one argument after a slash — `#games/<id>` is one game —
    // so a reload comes back to the same game rather than to the list.
    let argument = "";
    if (typeof area === "string" && area.includes("/")) {
      [area, argument] = [area.slice(0, area.indexOf("/")), area.slice(area.indexOf("/") + 1)];
    }
    if (!areaNames.includes(area)) area = defaultArea();
    for (const name of areaNames) {
      $("area-" + name).hidden = name !== area;
    }
    for (const tab of document.querySelectorAll(".area-tab")) {
      if (tab.dataset.area === area) tab.setAttribute("aria-current", "page");
      else tab.removeAttribute("aria-current");
    }
    // A report opened from the Build area is about the Build area. Leaving it
    // on screen after a move to Settings would be a form whose context the user
    // can no longer see.
    const feedback = $("feedback-panel");
    if (feedback) feedback.hidden = true;
    $("area-heading").textContent = titles[area];
    $("profiles-new").hidden = area !== "profiles";
    if (focus) $("area-heading").focus();
    const hash = "#" + area + (argument ? "/" + argument : "");
    if (window.location.hash !== hash) {
      window.history.replaceState(null, "", hash);
    }
    window.AUCOM.areas[area]?.refresh?.(argument);
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
    const site = officialSite(body);
    const backend = site ? site.host : body.aub_base_url ? "development server" : "no Auto-Pigeon server chosen";
    // The version is shown only in its frozen `1.<commit-count>` shape, as AUP
    // and AUG show it: an unstamped build's "unknown" is not a version anybody
    // should read out, so that segment is simply absent.
    const version = /^1\.\d+$/.test(body.version || "") ? `Version ${body.version} · ` : "";
    $("identity").textContent = `${version}${body.platform} · ${backend}${body.debug ? " · debug mode" : ""}`;
    renderBackendChoice($("sign-in-backend"), body);
    $("identity").className = "muted";
    $("account-email").textContent = body.authenticated ? body.email || "signed in" : "";
    $("account-signed-out").hidden = Boolean(body.authenticated);
    $("account-signed-out").textContent = body.session_expired ? "session expired" : "not signed in";
    $("account-menu").hidden = !body.authenticated;
    window.AUCOM.account?.statusChanged?.(body);
    // The notice banner (notices.mjs, a module) re-evaluates at once on a
    // sign-in or a sign-out, rather than at its next poll.
    document.dispatchEvent(new CustomEvent("aucom:status", { detail: { authenticated: Boolean(body.authenticated) } }));

    $("sign-in-open").hidden = Boolean(body.authenticated);
    $("library-signed-out").hidden = Boolean(body.authenticated);
    if (body.authenticated) {
      closeSignIn({ restoreFocus: false });
    } else {
      const site = officialSite(body);
      // An expired session is said as such: "sign in" alone reads like the
      // Companion forgot you, when it is the server's session that ran out.
      $("first-run-why").textContent = body.session_expired
        ? `Your session${body.email ? ` for ${body.email}` : ""} has expired. Sign in again.`
        : site
          ? `Sign in to your account on ${site.host}.`
          : body.aub_base_url
            ? "Sign in to your account on the development server."
            : "Choose which Auto-Pigeon your account is on, then sign in.";
      renderSignInHelp(site);
    }

    $("extractor-state").textContent = !body.aue_available
      ? "Map inspection is not installed on this machine. It is a separate, optional program." +
        (body.debug ? " Debug mode: set AUCOM_AUE_BINARY to a local build." : "")
      : body.aue_verified
        ? `An extractor is available and was verified against the signed catalogue (${body.aue_provenance}).`
        : `An extractor is available but is an UNVERIFIED developer override (${body.aue_provenance}). ` +
          "Nothing has checked these bytes.";
    $("extractor-version").disabled = !body.aue_available;
  }
  window.AUCOM.refreshStatus = refreshStatus;

  // officialSite is the official deployment in use, as {url, host}, or null.
  // A person is shown `auto-pigeon.com`, never an address with a port — a
  // development server is called that, and its address is in Settings.
  function officialSite(body) {
    const current = (body.aub_base_url || "").replace(/\/+$/, "");
    const backend = (body.backends || []).find((item) => item.url === current);
    return backend ? { url: backend.url, host: backend.url.replace(/^https?:\/\//, ""), label: backend.label } : null;
  }
  window.AUCOM.officialSite = officialSite;

  // renderSignInHelp: the Companion cannot create an account or reset a
  // password; both happen on the Auto-Pigeon site the account is on.
  function renderSignInHelp(site) {
    const link = (text, href) => el("a", { text, attrs: { href, target: "_blank", rel: "noopener noreferrer" } });
    const signup = $("sign-in-signup");
    const reset = $("sign-in-reset");
    signup.replaceChildren();
    reset.replaceChildren();
    if (site) {
      signup.append(document.createTextNode("No account yet? Accounts are created on "), link(site.host, site.url), document.createTextNode(", not in the Companion."));
      reset.append(document.createTextNode("Forgot your password? Passwords are reset on "), link(site.host, site.url), document.createTextNode("."));
    } else {
      signup.textContent = "Accounts are created on the Auto-Pigeon site, not in the Companion.";
      reset.textContent = "Passwords are reset on the Auto-Pigeon site.";
    }
  }

  // renderBackendChoice draws the official servers as a choice. An address that
  // is not one of them (a development server, set with --debug or by an
  // override) is shown as itself and cannot be changed from here.
  function renderBackendChoice(select, body) {
    const current = (body.aub_base_url || "").replace(/\/+$/, "");
    select.replaceChildren(el("option", { text: "Choose…", attrs: { value: "" } }));
    for (const backend of body.backends || []) {
      select.append(el("option", { text: `${backend.label} (${backend.url.replace(/^https:\/\//, "")})`, attrs: { value: backend.url } }));
    }
    const official = (body.backends || []).some((backend) => backend.url === current);
    if (current && !official) {
      select.append(el("option", { text: `Development server (${current})`, attrs: { value: current } }));
    }
    select.value = current;
    select.disabled = Boolean(current && !official && !body.debug);
  }
  window.AUCOM.renderBackendChoice = renderBackendChoice;

  $("sign-in-backend").addEventListener("change", async (event) => {
    const chosen = event.currentTarget.value;
    const settings = window.AUCOM.settings || {};
    const { ok, body } = await api("/api/v1/settings", {
      method: "PUT",
      body: {
        aub_base_url: chosen,
        port: settings.port ?? 0,
        job_concurrency: settings.job_concurrency ?? 0,
        game_roots: settings.game_roots || undefined,
      },
    });
    if (!ok) {
      setMessage("sign-in-message", body.error || "could not choose that server", "error");
      return;
    }
    window.AUCOM.settings = body;
    setMessage("sign-in-message", chosen ? "" : "Choose a server to sign in to.", "");
    await refreshStatus();
  });

  // --- the sign-in dialog ----------------------------------------------------

  let signInOpener = null;

  function openSignIn(opener) {
    signInOpener = opener || document.activeElement;
    $("sign-in-modal").hidden = false;
    $("email").focus();
  }

  function closeSignIn({ restoreFocus = true } = {}) {
    if ($("sign-in-modal").hidden) return;
    $("sign-in-modal").hidden = true;
    $("password").value = "";
    if (restoreFocus && signInOpener && document.contains(signInOpener)) signInOpener.focus();
    signInOpener = null;
  }
  window.AUCOM.openSignIn = openSignIn;
  window.AUCOM.showArea = show;

  $("stale-page-reload").addEventListener("click", () => window.location.reload());
  $("sign-in-open").addEventListener("click", (event) => openSignIn(event.currentTarget));
  $("library-sign-in").addEventListener("click", (event) => openSignIn(event.currentTarget));
  $("sign-in-close").addEventListener("click", () => closeSignIn());
  document.querySelector('[data-dismiss="sign-in"]').addEventListener("click", () => closeSignIn());
  $("sign-in-modal").addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      closeSignIn();
      return;
    }
    // Focus stays inside an open dialog: Tab past the last control comes back
    // to the first, and Shift+Tab the other way.
    if (event.key !== "Tab") return;
    const focusable = [...$("first-run").querySelectorAll("button:not([disabled]), input:not([disabled])")];
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  });

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
    // The journey the program is for, not the list of things it could do.
    // `defaultArea` is the ONE place that decides where a window opens, so
    // signing in and reloading cannot disagree about it (`AUCOM/AUE/AUT 246I1`
    // moved it to Build & Run; before that it was My Maps, hard-coded here).
    show(defaultArea());
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

  $("go-to-settings").addEventListener("click", () => {
    closeSignIn({ restoreFocus: false });
    show("settings");
  });

  // --- boot -----------------------------------------------------------------

  // A page opened for one job — New profile, or one profile's configuration
  // page, each in its own tab — shows that page and not the area navigation.
  // `?view=profile#profiles/<id>` is a URL that survives a reload: the query
  // drops the navigation and the hash names the profile, which `show` hands to
  // `areas.profiles.refresh(argument)` (AUCOM/AUT 246I defect 2).
  const view = new URLSearchParams(window.location.search).get("view");
  if (view === "new-profile" || view === "profile") {
    document.body.classList.add("single-view");
  }

  // Categories that decide what is drawn below them are tabs.
  for (const id of ["profiles-kind", "wizard-kind", "scratch-kind", "jobs-state", "library-type"]) {
    window.AUCOM.tabsFor(id);
  }

  (async () => {
    // Settings first: the path fields ask it whether this machine has a file
    // chooser, and a field drawn before that answer arrives would offer a
    // Browse button that opens nothing.
    await window.AUCOM.areas.settings.refresh();
    await refreshStatus();
    window.AUCOM.renderActivity();
    show(window.location.hash.replace(/^#/, "") || defaultArea(), { focus: false });
  })();
})();
