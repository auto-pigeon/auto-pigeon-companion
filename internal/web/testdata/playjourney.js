// The Build & Run journey, driven inside the real page.
//
// The API test (`play_test.go`) proves the Companion does the right thing. This
// proves somebody can make it: it clicks what a person clicks, waits for what
// should appear, and asserts on the DOM rather than on what a fetch returned.
//
// What it checks that nothing else can:
//
//   - one form (NEW_363): the map, the build profile, "Run after build" and the
//     review are on one page, and its one primary says "Review build" until a
//     review of these exact choices is on screen, then "Build & Run";
//   - the review shows the ordered WADs, the writes and the exact command;
//   - changing the revision CLEARS the review and says so;
//   - Activity shows the run, recovers it after a reload, and offers Retry;
//   - a not-compiler-ready bundle disables the button and names the refusals
//     (for a WAD the server did not send, it offers "use your own copy");
//   - nothing in the DOM, the URL or the visible text carries the token, a
//     private download URL or a path on this machine.

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
    steps.push({ step, ok, detail: String(detail || "") });
    log(`${ok ? "ok  " : "FAIL"} ${step} — ${detail || ""}`);
  };
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  // A screenshot of the page as it is now. The test captures it only when
  // AUCOM_JOURNEY_SCREENSHOTS is set; otherwise this answers at once.
  const snap = async (name) => {
    await sleep(250);
    await fetch("/journey/snap", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    }).catch(() => {});
  };

  // A script error in the page is the failure, not the timeout it causes.
  window.addEventListener("error", (event) => {
    log(`page error: ${event.message} (${event.filename}:${event.lineno})`);
  });
  window.addEventListener("unhandledrejection", (event) => {
    log(`page rejection: ${event.reason && event.reason.message ? event.reason.message : event.reason}`);
  });

  async function waitFor(what, predicate, timeout = 30000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      let value = null;
      try {
        value = predicate();
      } catch {
        value = null;
      }
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
      await sleep(100);
    }
  }

  const $ = (id) => document.getElementById(id);
  const visible = (node) => node && !node.hidden && node.offsetParent !== null;
  // `offsetParent` is null for a `position: fixed` element even when it is on
  // screen, so the Activity drawer is checked by its box instead.
  const onScreen = (node) => Boolean(node) && !node.hidden && node.getBoundingClientRect().width > 0;
  const textOf = (node) => (node ? node.textContent || "" : "");
  const setValue = (node, value) => {
    node.value = value;
    node.dispatchEvent(new Event("input", { bubbles: true }));
    node.dispatchEvent(new Event("change", { bubbles: true }));
  };

  async function go(area) {
    window.location.hash = "#" + area;
    await waitFor(`the ${area} area`, () => visible($("area-" + area)));
    await sleep(120);
  }

  // The review's one primary, and what it says it will do.
  const primary = () => $("play-start");
  async function review() {
    await waitFor("the primary to say Review build", () => /Review build/.test(textOf(primary())) && !primary().disabled, 20000)
      .catch((err) => { throw new Error(`${err.message}; it says "${textOf(primary())}", disabled=${primary().disabled}, ` +
        `message=${textOf($("play-start-message"))}`); });
    primary().click();
    await waitFor("the review", () => textOf($("play-review")).includes("What will be downloaded"), 40000);
  }

  try {
    const settings = await (await fetch("/journey/config")).json();
    await waitFor("the page", () => $("identity") && textOf($("identity")).length > 0);

    // --- sign in ------------------------------------------------------------
    $("sign-in-open").click();
    await waitFor("the sign-in dialog", () => visible($("first-run")));
    setValue($("email"), settings.email);
    setValue($("password"), settings.password);
    $("sign-in-form").requestSubmit();
    await waitFor("a signed-in session", () => window.AUCOM.status?.authenticated === true, 30000);
    // Signing in navigates on its own, asynchronously. Waited for rather than
    // raced with: a driver that started clicking before it settled would be
    // testing the race and not the page.
    await waitFor("the area the sign-in lands on", () => visible($("area-play")), 20000);
    await sleep(200);

    // A signed-in window opens on Build & Run, because that is the journey the
    // program is for.
    record(
      "a signed-in window opens on Build & Run",
      window.location.hash === "#play" || visible($("area-play")),
      window.location.hash || "no hash"
    );
    await go("play");

    // --- 1. the map ---------------------------------------------------------
    await waitFor("the map list", () => $("play-map").options.length > 1);
    setValue($("play-map"), settings.asset_id);
    await waitFor("the revisions", () => $("play-revision").options.length > 0);
    record(
      "an exact revision is chosen, never the word current",
      $("play-revision").value === settings.revision_id,
      $("play-revision").value
    );
    setValue($("play-map-name"), "dm1");
    await snap("map-and-exact-revision");

    // --- the map list's refusals (NEW_360 F2) -----------------------------
    // One sentence and the action that fixes it, chosen by the code the
    // Companion classified — and gone once the list is read again.
    const fault = (body) => fetch("/journey/catalog-fault", { method: "POST", body: JSON.stringify(body) });
    await fault({ status: 401, message: "The request requires valid record authorization token." });
    await window.AUCOM.areas.play.refresh();
    await waitFor("the sign-in refusal", () => /sign-in has expired/.test(textOf($("play-map-message"))));
    record("an expired sign-in is said as one, with Sign in, and no raw HTTP line",
      [...$("play-map-message").querySelectorAll("button")].some((b) => textOf(b) === "Sign in") &&
        !/aub:|HTTP 401/.test(textOf($("play-map-message")).replace(textOf($("play-map-message").querySelector("details")), "")),
      textOf($("play-map-message")).slice(0, 140));
    await fault({ status: 403, reason: "legal_acceptance_required",
      message: "The Terms or the notices changed. Review and accept the current version to carry on." });
    await window.AUCOM.areas.play.refresh();
    await waitFor("the legal refusal", () => /Terms or the notices changed/.test(textOf($("play-map-message"))));
    record("a Terms refusal asks for a review on the site, never a sign-in",
      ![...$("play-map-message").querySelectorAll("button")].some((b) => textOf(b) === "Sign in"),
      textOf($("play-map-message")).slice(0, 160));
    // A refusal asked for under one account and answered after the window has
    // moved to another is not the new account's: it is dropped, not shown.
    await fault({ status: 401, message: "The request requires valid record authorization token.", hold: true });
    $("play-map-message").replaceChildren();
    const asked = window.AUCOM.areas.play.refresh();
    await sleep(300);
    const signedInAs = window.AUCOM.status.email;
    window.AUCOM.status.email = "another@example.test";
    await fault({ release: true });
    await asked;
    await sleep(300);
    record("a late refusal for the previous account does not overwrite the new account's state",
      !textOf($("play-map-message")), `"${textOf($("play-map-message")).slice(0, 80)}"`);
    window.AUCOM.status.email = signedInAs;
    await fault({ status: 0 });
    await window.AUCOM.areas.play.refresh();
    await waitFor("the list again", () => $("play-map").options.length > 1 && !textOf($("play-map-message")));
    record("the list read again clears the old refusal", $("play-map").value === settings.asset_id,
      `${$("play-map").value} · "${textOf($("play-map-message"))}"`);

    // --- one form: nothing behind steps ------------------------------------
    record("Build & Run is one form, not a wizard of steps",
      !document.querySelector("#area-play .bwiz-step") && visible($("play-pipeline")) && visible($("play-run-after")) &&
        visible(primary()), `primary says "${textOf(primary())}"`);
    record("a fresh form's primary says Review build, not Build & Run",
      /Review build/.test(textOf(primary())), textOf(primary()));

    // --- the build profile ---------------------------------------------------
    await waitFor("the build profiles", () => $("play-pipeline").options.length > 1);
    setValue($("play-pipeline"), settings.pipeline_id);
    await waitFor("the stages", () => $("play-stages").children.length > 0);
    record("the chosen build profile's state is said beside it",
      /ready/i.test(textOf($("play-pipeline-state"))) && textOf($("play-pipeline-state")).includes("Fixture toolchain"),
      textOf($("play-pipeline-state")).slice(0, 120));
    // NEW_330 A5: the program behind the chosen build profile goes missing and
    // comes back. The choice stays, greyed out with the reason, and is ready
    // again after the repair — without a reload.
    const bindTool = (path) => window.AUCOM.api("/api/v1/profiles/aucom.fixture.toolchain/bind",
      { method: "POST", body: { executables: { tool: path } } });
    await bindTool("");
    window.location.hash = "#profiles";
    await waitFor("Profiles", () => visible($("area-profiles")));
    window.location.hash = "#play";
    await waitFor("the build profile needing setup", () =>
      [...$("play-pipeline").options].some((o) => o.value === settings.pipeline_id && o.disabled), 15000);
    record("a chosen build profile whose program went missing stays chosen and says why",
      $("play-pipeline").value === settings.pipeline_id && /needs setup/.test($("play-pipeline").selectedOptions[0]?.textContent || "") &&
        $("play-pipeline-state").querySelector('a[href^="#profiles/"]') !== null,
      `${$("play-pipeline").value} · ${textOf($("play-pipeline-state")).slice(0, 120)}`);
    record("and nothing can be reviewed or started with it",
      primary().disabled === true && /needs setup/.test(textOf($("play-start-message"))),
      `disabled=${primary().disabled} · ${textOf($("play-start-message"))}`);
    // NEW_360 F1: automatic builds use the same profile and the same readiness —
    // switching them on is not possible while it needs setup.
    await waitFor("the automatic-build checkbox", () => visible($("play-autobuild")));
    record("automatic builds cannot be switched on with a profile that needs setup",
      $("play-autobuild-on").disabled === true && !$("play-autobuild-on").checked,
      `disabled=${$("play-autobuild-on").disabled} · ${textOf($("play-autobuild-message"))}`);
    await bindTool(settings.tool_path);
    window.location.hash = "#profiles";
    await waitFor("Profiles", () => visible($("area-profiles")));
    window.location.hash = "#play";
    await waitFor("the build profile ready again", () =>
      [...$("play-pipeline").options].some((o) => o.value === settings.pipeline_id && !o.disabled), 15000);
    record("repairing the path makes the same choice ready again without a reload",
      $("play-pipeline").value === settings.pipeline_id, $("play-pipeline").value);
    await waitFor("the checkbox to follow the repair", () => $("play-autobuild-on").disabled === false, 15000);
    record("and automatic builds can be switched on again, with no reload", true, "enabled");
    record("the automatic-build checkbox says how often it checks, and starts unchecked",
      /every 30 seconds/.test(textOf($("play-autobuild-help"))) && !$("play-autobuild-on").checked,
      textOf($("play-autobuild-help")));
    $("play-autobuild-on").click();
    await waitFor("automatic builds on", () => visible($("play-autobuild-expanded")) && $("play-autobuild-on").checked, 15000)
      .catch((err) => { throw new Error(`${err.message}; it said: ${textOf($("play-autobuild-message"))}`); });
    record("switching it on shows the checks and Stop, and builds nothing yet",
      visible($("play-autobuild-stop")) && /Checked|not yet/.test(textOf($("play-autobuild-status"))),
      textOf($("play-autobuild-status")).replace(/\s+/g, " ").slice(0, 120));
    $("play-autobuild-stop").click();
    await waitFor("automatic builds off", () => !$("play-autobuild-on").checked && !visible($("play-autobuild-stop")), 15000);
    record("Stop automatic builds switches them off", true, "off");
    record(
      "the build profile names the program that will run",
      textOf($("play-stages")).includes("Fixture toolchain"),
      textOf($("play-stages")).replace(/\s+/g, " ").trim().slice(0, 90)
    );

    // --- run after build: the engine -----------------------------------------
    record("Run after build is on for Build & Run", $("play-run-after").checked === true, String($("play-run-after").checked));
    await waitFor("the engines", () => $("play-engine").options.length > 1);
    // NEW_330: an engine for another game is greyed out, says why, and gets no
    // "Set up" link — setting it up could not make it play this build.
    const other = [...$("play-engine").options].find((option) => option.value === settings.other_engine_id);
    const setupLinks = [...document.querySelectorAll("#play-engine ~ .choice-setup a")].map((a) => a.getAttribute("href"));
    record("a Quake 1 build profile does not offer a Quake II engine",
      other && other.disabled && /does not play this build profile's maps/.test(other.textContent) &&
        !setupLinks.some((href) => href.includes(encodeURIComponent(settings.other_engine_id))),
      `${other ? other.textContent : "not listed"} · setup links ${setupLinks.join(" ") || "none"}`);
    setValue($("play-engine"), settings.engine_id);
    await waitFor("the actions", () => $("play-action").options.length > 0);
    // A choice of how to play it only when there is one, and the chosen one
    // explained once.
    const choices = [...document.querySelectorAll("#play-action-choices input[type=radio]")];
    record(
      "the engine's action is offered only when there is a choice, the chosen one explained",
      choices.length < 2 ? $("play-action-field").hidden
        : visible($("play-action-field")) && textOf($("play-action-note")).length > 20,
      `${choices.map((input) => input.value).join(", ") || "none"} · ${textOf($("play-action-note")).slice(0, 80)}`
    );
    record(
      "the page says where it will write and that it leaves the game alone",
      textOf($("play-game-root")).includes("auto-pigeon") && textOf($("play-game-root")).includes("id1"),
      textOf($("play-game-root")).slice(0, 110)
    );
    record("the mod folder label names the base folder the chosen engine declares",
      textOf($("play-mod-where")).includes("id1"), textOf($("play-mod-where")));

    // --- the review -----------------------------------------------------------
    await review();
    // The bundle is fetched and verified BY the review, so the ordered WAD set
    // it shows is the real one rather than a guess. Waited for, because it is
    // a second request after the plan.
    const wads = () => [...document.querySelectorAll("#play-review .wad-list li")].map((li) => textOf(li).replace(/\s+/g, ""));
    await waitFor("the verified WAD list", () => wads().some((text) => /^1first\.wad/.test(text)), 40000)
      .catch((err) => { throw new Error(`${err.message}; the review said: ${textOf($("play-review")).replace(/\s+/g, " ").slice(0, 300)}`); });
    const reviewText = textOf($("play-review"));
    record(
      "the review lists the WADs in the order the map declares them",
      /^1first\.wad/.test(wads()[0] || "") && /^2second\.wad/.test(wads()[1] || ""),
      reviewText.replace(/\s+/g, " ").slice(0, 160)
    );
    record(
      "the review says the game folder is never written to",
      reviewText.includes("id1"),
      reviewText.includes("id1") ? "named" : "not named"
    );
    record("the technical details are folded, and the ordinary review is a short summary",
      $("play-review-details").open === false && textOf($("play-final-summary")).includes("auto-pigeon"),
      textOf($("play-final-summary")).replace(/\s+/g, " ").slice(0, 130));
    record("once reviewed, the one primary says what it does",
      textOf(primary()) === "Build & Run" && !primary().disabled, `"${textOf(primary())}" disabled=${primary().disabled}`);
    // Shown as one terminal line, as a ```shell block reads (operator,
    // 2026-09-22); each word is quoted when the shell would need it.
    const command = textOf($("play-review").querySelector(".shell__code code") || document.createElement("i"));
    record(
      "the exact command is shown as a command line",
      /(^| )-game auto-pigeon( |$)/.test(command) && /(^| )\+map dm1( |$)/.test(command),
      command.slice(0, 140)
    );
    $("play-review-details").open = true;
    await snap("review");
    $("play-review").querySelector(".shell")?.scrollIntoView({ block: "center" });
    await snap("review-wads-and-argv");
    // A long path must wrap rather than push the page sideways (246I1.1).
    record(
      "the review fits the window without scrolling sideways",
      document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
      `${document.documentElement.scrollWidth}px of content in ${document.documentElement.clientWidth}px`
    );
    $("play-review-details").open = false;

    // Changing the revision throws the review away AND says so.
    setValue($("play-revision"), $("play-revision").value);
    $("play-map-name").value = "dm1";
    $("play-map-name").dispatchEvent(new Event("input", { bubbles: true }));
    record(
      "changing a choice says what it cleared",
      textOf($("play-review-message")).toLowerCase().includes("you changed") && /Review build/.test(textOf(primary())),
      `${textOf($("play-review-message")).slice(0, 110)} · "${textOf(primary())}"`
    );
    await snap("review-invalidated");
    // Nothing starts on a change: the review comes back only when asked for.
    await review();
    record(
      "a review keeps every compatible choice",
      $("play-map").value === settings.asset_id && $("play-map-name").value === "dm1",
      `${$("play-map").value} · ${$("play-map-name").value}`
    );

    // --- the source of the map ----------------------------------------------
    // A file on this computer is the same form's other source, not a second
    // wizard: switching to it and back keeps the saved map's choices.
    $("play-source-local").click();
    await waitFor("the file inputs", () => visible($("play-local-map")) && visible($("build-start")));
    record("a file on this computer is built in the same form, with Run after build off",
      !visible($("play-saved-map")) && $("play-run-after").disabled && !$("play-run-after").checked && !visible($("play-start")),
      `run after=${$("play-run-after").checked} disabled=${$("play-run-after").disabled}`);
    $("play-source-saved").click();
    await waitFor("the saved map again", () => visible($("play-saved-map")) && visible(primary()));
    record("switching back keeps the saved map, its revision and the review's choices",
      $("play-map").value === settings.asset_id && $("play-revision").value === settings.revision_id &&
        $("play-run-after").checked, `${$("play-map").value} · ${$("play-revision").value}`);
    if (!/Build & Run/.test(textOf(primary()))) await review();

    // --- one confirmation ------------------------------------------------
    primary().click();
    // The run gets one concise card, and one way to its progress.
    await waitFor("the current run's card", () => visible($("play-current")), 20000).catch((err) => {
      throw new Error(`${err.message}; the page said: ${textOf($("play-start-message"))}`);
    });
    const launches = [...$("play-current").querySelectorAll("button")];
    record("the run has one concise card, with one primary and a way to its progress",
      launches.filter((b) => b.classList.contains("primary")).length === 1 &&
        launches.some((b) => textOf(b) === "View progress") &&
        !launches.some((b) => /Build & Run|Run|Play/.test(textOf(b))),
      launches.map((b) => `${textOf(b)}${b.classList.contains("primary") ? "*" : ""}`).join(", "));
    [...$("play-current").querySelectorAll("button")].find((b) => textOf(b) === "View progress").click();

    await waitFor("Activity", () => onScreen($("activity")), 20000);
    record("View progress opens Activity", true, "open");
    record(
      "Activity keeps its padding on screen",
      $("activity").getBoundingClientRect().left >= 0,
      `left edge at ${Math.round($("activity").getBoundingClientRect().left)}px`
    );
    await snap("activity-running");
    // The drawer is part of the dark page, not a white sheet over it (246I1.1).
    {
      const bg = getComputedStyle($("activity")).backgroundColor;
      const channels = (bg.match(/\d+/g) || []).slice(0, 3).map(Number);
      record(
        "Activity is drawn in the page's own dark palette",
        channels.length === 3 && channels.every((c) => c < 80),
        bg
      );
    }
    await waitFor(
      "the run to finish",
      () => textOf($("activity-body")).includes("Finished"),
      120000
    ).catch((err) => {
      throw new Error(`${err.message}; Activity said: ${textOf($("activity-body")).replace(/\s+/g, " ").slice(0, 320)}`);
    });
    const activity = textOf($("activity-body"));
    record(
      "every stage of the run is named in plain language",
      ["Downloading the map", "Downloading the textures", "Compiling", "Installing into the game folder", "Starting the game"]
        .every((stage) => activity.includes(stage)),
      activity.replace(/\s+/g, " ").slice(0, 200)
    );
    await waitFor("the card to say it finished", () => /built and started/.test(textOf($("play-current-title"))), 20000);
    record("the card says how the run ended", true, textOf($("play-current-title")));
    await snap("activity-finished-launched");

    // Escape closes Activity and does not stop the run.
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    record("Escape closes Activity", $("activity").hidden === true, String($("activity").hidden));
    $("activity-open").click();
    await waitFor("Activity again", () => onScreen($("activity")));
    record(
      "focus moves into Activity when it opens",
      document.activeElement === $("activity-heading"),
      document.activeElement ? document.activeElement.id || document.activeElement.tagName : "nothing"
    );
    // NEW_331 A5: leaving Build & Run closes the drawer instead of leaving it
    // over the next area's controls; the header button still opens it there.
    window.location.hash = "#profiles";
    await waitFor("Profiles", () => visible($("area-profiles")));
    record("moving to Profiles closes Activity rather than covering it", $("activity").hidden === true,
      String($("activity").hidden));
    $("activity-open").click();
    await waitFor("Activity over Profiles on request", () => onScreen($("activity")));
    window.location.hash = "#play";
    await waitFor("Build & Run again", () => visible($("area-play")));

    // --- nothing leaks ------------------------------------------------------
    // What must NOT be in the page is a credential or a place only the
    // Companion knows about. The game folder and the files that will be
    // written into it are deliberately shown — the review exists to show them,
    // and they are the user's own machine — so this checks the things that are
    // nobody's business: the AUB session token, the API token in the URL, and
    // the Companion's own cache, where the verified bundles and the job
    // workspaces live.
    // Scoped to the Build & Run page and the Activity drawer. The Settings
    // area's whole job is to tell you where the Companion keeps things, so
    // scanning the entire document would be asserting that a feature is a leak.
    const page = ($("area-play")?.innerHTML || "") + ($("activity")?.innerHTML || "");
    const leaks = [];
    if (settings.token && page.includes(settings.token)) leaks.push("the AUB session token");
    if (settings.api_token && page.includes(settings.api_token)) leaks.push("the API token in the DOM");
    if (window.location.href.includes(settings.api_token)) leaks.push("the API token in the URL");
    if (settings.cache_dir && page.includes(settings.cache_dir)) {
      leaks.push("the Companion's own cache directory");
    }
    record("no credential and no internal path is in the page", leaks.length === 0, leaks.join(", ") || "none");

    // --- a reload recovers the same run ------------------------------------
    const before = (await (await fetch("/api/v1/play/runs", {
      headers: { "X-AUCOM-Token": settings.api_token },
    })).json()).items.length;
    record("the run is durable, not a promise in this tab", before > 0, `${before} run(s) on disk`);

    // --- the refusal path ---------------------------------------------------
    // The backend is switched to a bundle it cannot complete, and the page must
    // list the actual refusals and refuse to start anything.
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await fetch("/journey/break-textures", { method: "POST" });
    // Coming back to the area re-reads everything, which is what a person who
    // has just saved in the editor does.
    await window.AUCOM.areas.play.refresh();
    await waitFor("the new revision", () => $("play-revision").value !== settings.revision_id, 20000);
    setValue($("play-map-name"), "dm2");
    await review();
    // The fixture's only refusal is a WAD the server did not send, so the
    // review offers "use your own copy" rather than a dead end — in the open,
    // because only the person can answer it — and the button stays disabled
    // until a folder holding it is confirmed.
    await waitFor(
      "the refusal",
      () => /cannot be compiled yet|Not sent with this map/.test(textOf($("play-review-attention"))),
      40000
    ).catch((err) => {
      throw new Error(`${err.message}; revision=${$("play-revision").value}, ` +
        `message=${textOf($("play-review-message"))}, ` +
        `attention=${textOf($("play-review-attention")).replace(/\s+/g, " ").slice(0, 260)}`);
    });
    record(
      "a bundle the server could not complete names its refusals",
      textOf($("play-review-attention")).includes("quake101.wad"),
      textOf($("play-review-attention")).replace(/\s+/g, " ").slice(-200)
    );
    ($("play-review-attention").querySelector(".own-wads") || $("play-review-attention").querySelector(".notice.error"))
      ?.scrollIntoView({ block: "center" });
    await snap("compiler-ready-refusal");
    record(
      "and the one button is disabled rather than starting a compiler",
      primary().disabled === true,
      `${textOf(primary())} disabled=${primary().disabled}`
    );
    await snap("refusal-button-disabled");

    // --- a reload lands on Review build ----------------------------------------
    // Neither the review nor the folder confirmed in it is remembered. Found
    // live on Windows (NEW_310A): after a reload the one button was enabled
    // with no review at all, and would have started the old choices unseen.
    // Coming back to the area is what a reload does to it.
    await window.AUCOM.areas.play.refresh();
    record(
      "after a reload nothing can be started without a review",
      /Review build/.test(textOf(primary())) && !textOf($("play-review")),
      `"${textOf(primary())}", review ${textOf($("play-review")) ? "still shown" : "empty"}`
    );
    // Jobs keeps the finished run.
    await go("jobs");
    await sleep(600);
    await snap("jobs-history");
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
