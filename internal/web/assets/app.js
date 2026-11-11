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
  const { $, el, api, setMessage, announce, record, wireCompatibilityReport, t } = window.AUCOM;

  // The compatibility-report panel is wired once and lives outside the areas,
  // because every area can open it. See core.js.
  wireCompatibilityReport();

  // About is not an area any more: it opens from the footer (operator,
  // 2026-09-22), and Settings is reached from the cog, top right.
  const areaNames = ["play", "library", "builds", "run", "games", "profiles", "new-profile", "jobs", "settings"];
  // Routes that are a view of another area (NEW_363). Build & Run is one form
  // now: the old Build wizard's `#build` opens it on "File on this computer",
  // and `#autobuild` on its automatic-build checkbox. Saved links keep
  // working; there is no second build journey behind them.
  const aliases = {
    build: ["play", "local"],
    autobuild: ["play", "autobuild"],
  };
  const titles = {
    play: "Build & Run",
    library: "My Maps",
    builds: "Builds on this machine",
    run: "Run",
    games: "Live Games",
    profiles: "Profiles",
    "new-profile": "New profile",
    jobs: "Jobs",
    settings: "Settings",
  };

  // The area a fresh page opens on: Build & Run, the journey the program is
  // for. Signed out it opens on a file on this computer, which needs no
  // account (NEW_244D, NEW_363).
  function defaultArea() {
    return window.AUCOM.status?.authenticated ? "play" : "play/local";
  }

  let shownArea = "";
  window.AUCOM.areaMoves = 0;

  function show(area, { focus = true } = {}) {
    // An area may carry one argument after a slash — `#games/<id>` is one game —
    // so a reload comes back to the same game rather than to the list.
    let argument = "";
    if (typeof area === "string" && area.includes("/")) {
      [area, argument] = [area.slice(0, area.indexOf("/")), area.slice(area.indexOf("/") + 1)];
    }
    if (aliases[area]) [area, argument] = aliases[area];
    if (!areaNames.includes(area)) {
      [area, argument = ""] = defaultArea().split("/");
    }
    // A move to another area counts as one: a question a person turned down
    // waits for the next time they come here (play.js askForGame).
    if (shownArea && area !== shownArea) window.AUCOM.areaMoves += 1;
    shownArea = area;
    for (const name of areaNames) {
      $("area-" + name).hidden = name !== area;
    }
    for (const tab of document.querySelectorAll(".area-tab")) {
      if (tab.dataset.area === area) tab.setAttribute("aria-current", "page");
      else tab.removeAttribute("aria-current");
    }
    // Builds on this machine is a sub-item, shown only while it is open.
    $("builds-tab").hidden = area !== "builds";
    // A report opened from the Build area is about the Build area. Leaving it
    // on screen after a move to Settings would be a form whose context the user
    // can no longer see.
    const feedback = $("feedback-panel");
    if (feedback) feedback.hidden = true;
    // The Activity drawer is Build & Run's, drawn over the right of the page:
    // left open after a move to Profiles it covered "Save these paths" and
    // "Add argument" (NEW_331 A5). It closes on the way out, without taking
    // focus anywhere; the Activity button in the header opens it again from
    // any area, and closing it never stops a run.
    if (area !== "play" && $("activity") && !$("activity").hidden) {
      $("activity").hidden = true;
      $("activity-open").setAttribute("aria-expanded", "false");
    }
    $("area-heading").textContent = t(titles[area]);
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
    // The version and the platform are the footer's, bottom right.
    window.AUCOM.footer?.statusChanged?.(body);
    $("identity").className = "site-footer__version";
    renderBackendChoice($("sign-in-backend"), body);
    $("account-email").textContent = body.authenticated ? body.email || "signed in" : "";
    $("account-signed-out").hidden = Boolean(body.authenticated);
    $("user-sign-in").hidden = Boolean(body.authenticated);
    $("sign-out").hidden = !body.authenticated;
    $("user-button").title = body.authenticated ? t("Signed in as {email}", { email: body.email || t("you") }) : t("Not signed in");
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
            : "Choose which Auto-Pigeon your account is on, then sign in." +
              ((body.aub_ignored || []).length ? ` Ignored: ${body.aub_ignored.join("; ")}. ${body.aub_ignored_why}` : "");
      renderSignInHelp(site);
    }

    $("converter-state").textContent = !body.converter_available
      ? "Map conversion is not available: no converter was shipped beside this Companion. It is a separate program in the release." +
        (body.debug ? " Debug mode: set AUCOM_MAPIO_BINARY to a local build." : "")
      : body.converter_verified
        ? "The map converter shipped with this Companion is available, and matches its release's bundle manifest."
        : body.converter_provenance === "developer_override"
          ? "A map converter is available but is an UNVERIFIED developer override. Nothing has checked these bytes."
          : "The map converter shipped beside this Companion is available; no bundle manifest lists it, so nothing checked its bytes.";
    $("converter-version").disabled = !body.converter_available;
  }
  window.AUCOM.refreshStatus = refreshStatus;

  // officialSite is the official deployment in use, as {url, host}, or null.
  // A person is shown `auto-pigeon.com`, never an address with a port — a
  // development server is called that, and its address is in Settings.
  function officialBackend(body) {
    const current = (body.aub_base_url || "").replace(/\/+$/, "");
    return (body.backends || []).find((item) => item.url === current || item.api_url === current);
  }

  function officialSite(body) {
    const backend = officialBackend(body);
    return backend ? { url: backend.url, host: backend.url.replace(/^https?:\/\//, ""), label: backend.label } : null;
  }
  window.AUCOM.officialSite = officialSite;

  // renderSignInHelp says only what is true of the site today (NEW_508, issue
  // #1): accounts are created there, in its own sign-up dialog, so the link
  // is to the site. The site has NO password reset, so nothing here promises
  // one — the sentence that did sent people to a page that could not help.
  // With no official site in use (a development server) there is no address
  // to offer and the list stays empty.
  function renderSignInHelp(site) {
    const signup = $("sign-in-signup");
    const reset = $("sign-in-reset");
    signup.replaceChildren();
    reset.replaceChildren();
    reset.hidden = true;
    signup.hidden = !site;
    $("sign-in-help").hidden = !site;
    if (!site) return;
    signup.append(document.createTextNode(t("No account yet?") + " "),
      el("a", { text: t("Create one on {site}", { site: site.host }), attrs: { href: site.url, target: "_blank", rel: "noopener noreferrer" } }),
      document.createTextNode("."));
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
    const official = officialBackend(body);
    if (current && !official) {
      select.append(el("option", { text: `Development server (${current})`, attrs: { value: current } }));
    }
    select.value = official?.url ?? current;
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
    window.AUCOM.trapFocus(event, $("first-run"), "button, input");
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

  // --- Activity: the count, and the way back to an editor's request -----------
  //
  // Activity is always in the header (NEW_508). Its count is the number of
  // entries that want something from the person: an editor's request, an
  // update that waits, a tool that failed to set up, a service notice. Work
  // that is merely going on — a build, tools installing — is said as
  // "running", and an idle Companion shows the bare word and nothing else.
  //
  // The entries themselves are owned by the files that know them (tools.js,
  // notices.mjs, play.js); this only counts what they show, by watching the
  // few nodes involved rather than polling.

  const requestCards = [
    { box: $("leak-request"), key: "leak" },
    { box: $("build-request"), key: "build" },
  ];
  let announcedEntries = "";

  function requestPointers() {
    const host = $("activity-requests");
    const rows = [];
    for (const { box, key } of requestCards) {
      if (box.hidden) continue;
      const title = box.querySelector(".leak-request__title")?.textContent || "";
      const what = box.querySelector(".leak-request__what")?.textContent || "";
      if (!title) continue;
      const open = el("button", { text: t("Open in Build & Run"), className: "secondary", attrs: { type: "button", "data-request": key } });
      open.addEventListener("click", () => {
        window.AUCOM.closeActivity?.({ focus: false });
        show("play", { focus: false });
        box.scrollIntoView({ block: "nearest" });
        (box.querySelector("button.primary, button") || $("area-heading")).focus();
      });
      rows.push(el("div", { className: "activity-item", attrs: { "data-request": key }, children: [
        el("p", { className: "activity-item__title", text: title }),
        ...(what ? [el("p", { className: "activity-item__what", text: what })] : []),
        el("div", { className: "row-actions", children: [open] }),
      ] }));
    }
    const signature = rows.map((row) => row.textContent).join("|");
    if (host.dataset.signature !== signature) {
      // Rebuilt only when what it says changed, so a focused button in it
      // survives the requests' two-second watch.
      const focused = host.contains(document.activeElement) ? document.activeElement.dataset.request : "";
      host.dataset.signature = signature;
      host.replaceChildren(...rows);
      if (focused) host.querySelector(`button[data-request="${focused}"]`)?.focus();
    }
    host.hidden = rows.length === 0;
    return rows.length;
  }

  // A link the person has just followed in the editor brings them to Build &
  // Run, where its card is — whatever the request turns out to need (a
  // sign-in, a pipeline, nothing). It never moves a page that is in the
  // middle of something: an open dialog keeps the page where it is, and a
  // request found waiting by a page opened later is only counted in Activity.
  window.AUCOM.requestArrived = (request) => {
    const age = Date.now() - Date.parse(request?.received_at || "");
    if (!(age >= 0 && age < 15000)) return;
    if (document.querySelector("dialog[open], .auth-modal:not([hidden])")) return;
    if (shownArea !== "play") show("play", { focus: false });
  };

  function activityBadge() {
    const requests = requestPointers();
    const setup = $("tools-setup");
    const setupWants = !setup.hidden && setup.dataset.attention === "1";
    const notices = $("notice-banner").hidden ? 0 : $("notice-list").children.length;
    const wants = requests + ($("tools-update-badge").hidden ? 0 : 1) + (setupWants ? 1 : 0) + notices;
    const runs = window.AUCOM.activityRuns || { active: false };
    const busy = runs.active || (!setup.hidden && setup.dataset.attention === "0");
    const pill = $("activity-count");
    pill.hidden = !wants && !busy;
    pill.textContent = wants ? String(wants) : busy ? t("running") : "";
    pill.className = "pill" + (wants ? " pill--wants" : "");
    $("activity-open").title = wants
      ? (wants === 1 ? t("Activity: 1 entry needs you") : t("Activity: {n} entries need you", { n: wants }))
      : busy ? t("Activity: work in progress") : t("Activity");
    $("activity-items").hidden = !wants && setup.hidden;
    // Said once per change of what is waiting, politely, and never by a poll
    // that found the same things again.
    const now = [requests, !$("tools-update-badge").hidden, setupWants, notices].join("/");
    if (now !== announcedEntries) {
      const grew = announcedEntries !== "" && wants > Number(pill.dataset.wants || 0);
      announcedEntries = now;
      if (grew && $("activity").hidden) announce(wants === 1 ? t("Activity: 1 entry needs you") : t("Activity: {n} entries need you", { n: wants }));
    }
    pill.dataset.wants = String(wants);
  }
  window.AUCOM.activityBadge = activityBadge;

  const watcher = new MutationObserver(activityBadge);
  for (const node of [$("leak-request"), $("build-request"), $("tools-setup"), $("notice-banner"), $("tools-update-badge")]) {
    watcher.observe(node, { attributes: true, attributeFilter: ["hidden", "data-attention"], childList: true, subtree: node.id !== "tools-setup" });
  }
  activityBadge();

  // --- boot -----------------------------------------------------------------

  // A page opened for one job — New profile, or one profile's configuration
  // page, each in its own tab — shows that page and not the area navigation.
  // `?view=profile#profiles/<id>` is a URL that survives a reload: the query
  // drops the navigation and the hash names the profile, which `show` hands to
  // `areas.profiles.refresh(argument)` (AUCOM/AUT 246I defect 2).
  const view = new URLSearchParams(window.location.search).get("view");
  if (view === "new-profile") {
    document.body.classList.add("single-view");
  } else if (view === "profile") {
    // An older link to a profile's page, from when Configure opened its own
    // tab: the same page, now a sub-page of Profiles with the navigation kept.
    window.history.replaceState(null, "", window.location.pathname + window.location.hash);
  }

  // Categories that decide what is drawn below them are tabs.
  for (const id of ["library-view", "profiles-kind", "wizard-kind", "scratch-kind", "jobs-state"]) {
    window.AUCOM.tabsFor(id);
  }

  (async () => {
    // Settings first: the path fields ask it whether this machine has a file
    // chooser, and a field drawn before that answer arrives would offer a
    // Browse button that opens nothing.
    await window.AUCOM.areas.settings.refresh();
    window.AUCOM.applySavedLanguage?.();
    await refreshStatus();
    window.AUCOM.renderActivity();
    show(window.location.hash.replace(/^#/, "") || defaultArea(), { focus: false });
  })();
})();
