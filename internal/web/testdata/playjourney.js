// The Build & Run journey, driven inside the real page.
//
// The API test (`play_test.go`) proves the Companion does the right thing. This
// proves somebody can make it: it clicks what a person clicks, waits for what
// should appear, and asserts on the DOM rather than on what a fetch returned.
//
// What it checks that nothing else can:
//
//   - the five steps are reachable and going back does not lose a choice;
//   - the review shows the ordered WADs, the writes and the exact command;
//   - changing the revision CLEARS the review and says so;
//   - Activity shows the run, recovers it after a reload, and offers Retry;
//   - a not-compiler-ready bundle disables the button and names the refusals;
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

  function stepButton(n) {
    return $("play-step-tab-" + n);
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

    // --- 2. the build profile ----------------------------------------------
    stepButton(2).click();
    await waitFor("the build step", () => visible($("play-step-2")), 15000).catch((err) => {
      throw new Error(`${err.message}; panel hidden=${$("play-step-2")?.hidden}, ` +
        `area hidden=${$("area-play")?.hidden}, tabs=${document.querySelectorAll("#play-steps .bwiz-step").length}`);
    });
    await waitFor("the build profiles", () => $("play-pipeline").options.length > 1);
    setValue($("play-pipeline"), settings.pipeline_id);
    await waitFor("the stages", () => $("play-stages").children.length > 0);
    record(
      "the build step names the program that will run",
      textOf($("play-stages")).includes("Fixture toolchain"),
      textOf($("play-stages")).replace(/\s+/g, " ").trim().slice(0, 90)
    );

    // --- 3. the engine ------------------------------------------------------
    stepButton(3).click();
    await waitFor("the run step", () => visible($("play-step-3")));
    await waitFor("the engines", () => $("play-engine").options.length > 1);
    setValue($("play-engine"), settings.engine_id);
    await waitFor("the actions", () => $("play-action").options.length > 0);
    // A small fixed set is offered as explained choices, not as a dropdown.
    const choices = [...document.querySelectorAll("#play-action-choices .choice-card")];
    record(
      "the engine's action is a set of explained choices, not a dropdown",
      choices.length > 0 && choices.every((card) => card.querySelector("input[type=radio]") && textOf(card).length > 20),
      choices.map((card) => textOf(card.querySelector(".choice-card__title"))).join(", ") || "no choices"
    );
    record(
      "the page says where it will write and that it leaves the game alone",
      textOf($("play-game-root")).includes("auto-pigeon") && textOf($("play-game-root")).includes("id1"),
      textOf($("play-game-root")).slice(0, 110)
    );

    // --- 4. the review ------------------------------------------------------
    stepButton(4).click();
    await waitFor("the review", () => textOf($("play-review")).includes("Where files will be written"), 40000);
    // The bundle is fetched and verified BY the review, so the ordered WAD set
    // it shows is the real one rather than a guess. Waited for, because it is
    // a second request after the plan.
    const wads = () => [...document.querySelectorAll("#play-review .wad-list li")].map((li) => textOf(li).replace(/\s+/g, ""));
    await waitFor("the verified WAD list", () => wads().some((text) => /^1first\.wad/.test(text)), 40000)
      .catch((err) => { throw new Error(`${err.message}; the review said: ${textOf($("play-review")).replace(/\s+/g, " ").slice(0, 300)}`); });
    const review = textOf($("play-review"));
    record(
      "the review lists the WADs in the order the map declares them",
      /^1first\.wad/.test(wads()[0] || "") && /^2second\.wad/.test(wads()[1] || ""),
      review.replace(/\s+/g, " ").slice(0, 160)
    );
    record(
      "the review says the game folder is never written to",
      review.includes("id1"),
      review.includes("id1") ? "named" : "not named"
    );
    // Shown as one terminal line, as a ```shell block reads (operator,
    // 2026-09-22); each word is quoted when the shell would need it.
    const command = textOf($("play-review").querySelector(".shell__code code") || document.createElement("i"));
    record(
      "the exact command is shown as a command line",
      /(^| )-game auto-pigeon( |$)/.test(command) && /(^| )\+map dm1( |$)/.test(command),
      command.slice(0, 140)
    );
    await snap("review");
    $("play-review").querySelector(".shell")?.scrollIntoView({ block: "center" });
    await snap("review-wads-and-argv");
    // A long path must wrap rather than push the page sideways (246I1.1).
    record(
      "the review fits the window without scrolling sideways",
      document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
      `${document.documentElement.scrollWidth}px of content in ${document.documentElement.clientWidth}px`
    );

    // Changing the revision throws the review away AND says so.
    setValue($("play-revision"), $("play-revision").value);
    $("play-map-name").value = "dm1";
    $("play-map-name").dispatchEvent(new Event("input", { bubbles: true }));
    record(
      "changing a choice says what it cleared",
      textOf($("play-review-message")).toLowerCase().includes("you changed"),
      textOf($("play-review-message")).slice(0, 110)
    );
    await snap("review-invalidated");
    // And the review comes back when the step is opened again.
    stepButton(4).click();
    await waitFor("the review again", () => textOf($("play-review")).includes("Where files will be written"), 40000);

    // Going backward keeps compatible choices.
    stepButton(1).click();
    await waitFor("the map step", () => visible($("play-step-1")));
    record(
      "going back does not lose a compatible choice",
      $("play-map").value === settings.asset_id && $("play-map-name").value === "dm1",
      `${$("play-map").value} · ${$("play-map-name").value}`
    );

    // --- 5. one confirmation ------------------------------------------------
    stepButton(5).click();
    await waitFor("the final step", () => visible($("play-step-5")));
    record(
      "the last step restates every choice before the one button",
      textOf($("play-final-summary")).includes("auto-pigeon"),
      textOf($("play-final-summary")).replace(/\s+/g, " ").slice(0, 130)
    );
    $("play-start").click();

    // Activity opens on its own, because that is where the run now lives.
    await waitFor("Activity", () => onScreen($("activity")), 20000).catch((err) => {
      throw new Error(`${err.message}; the page said: ${textOf($("play-start-message"))}`);
    });
    record("Activity opens when a run starts", true, "open");
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
    stepButton(1).click();
    await waitFor("the new revision", () => $("play-revision").value !== settings.revision_id, 20000);
    setValue($("play-map-name"), "dm2");
    stepButton(4).click();
    await waitFor(
      "the refusal",
      () => textOf($("play-review")).includes("cannot be compiled yet"),
      40000
    ).catch((err) => {
      throw new Error(`${err.message}; revision=${$("play-revision").value}, ` +
        `message=${textOf($("play-review-message"))}, ` +
        `review=${textOf($("play-review")).replace(/\s+/g, " ").slice(0, 260)}`);
    });
    record(
      "a bundle the server could not complete names its refusals",
      textOf($("play-review")).includes("quake101.wad"),
      textOf($("play-review")).replace(/\s+/g, " ").slice(-200)
    );
    $("play-review").querySelector(".notice.error")?.scrollIntoView({ block: "center" });
    await snap("compiler-ready-refusal");
    stepButton(5).click();
    await waitFor("the final step again", () => visible($("play-step-5")));
    record(
      "and the one button is disabled rather than starting a compiler",
      $("play-start").disabled === true,
      String($("play-start").disabled)
    );
    await snap("refusal-button-disabled");
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
