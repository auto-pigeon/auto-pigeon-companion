// The first-run journey, driven inside the real page.
//
// This file is loaded by internal/web/browser_test.go, appended to the real
// index.html on the real origin. It runs after the application's own scripts,
// so it drives them the way a person does: it finds the element somebody would
// click, clicks it, and waits for what should appear.
//
// It asserts on what the DOM says, never on what a fetch returned. A step that
// checked the response would be checking the API again — TestFirstRunJourney
// already does that, and it would pass while the page rendered nothing.

"use strict";

(async () => {
  const steps = [];
  let fatal = "";

  const log = (text) => {
    // Best effort: the log channel is a convenience for a failing run, and a
    // failure to send one must never end the journey.
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

  // waitFor polls a predicate. Polling rather than observing, because what is
  // being waited for is often several asynchronous refreshes deep and a
  // MutationObserver would fire on each of them.
  async function waitFor(what, predicate, timeout = 30000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      let value;
      try {
        value = predicate();
      } catch (err) {
        value = null;
      }
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
      await sleep(120);
    }
  }

  const visible = (node) => node && !node.hidden && node.offsetParent !== null;
  const $ = (id) => document.getElementById(id);
  const textOf = (node) => (node ? node.textContent || "" : "");

  // buttonIn finds a button by its label inside a container, which is how a
  // person finds one.
  function buttonIn(container, label) {
    for (const button of container.querySelectorAll("button")) {
      if (button.textContent.trim().toLowerCase().includes(label.toLowerCase())) return button;
    }
    return null;
  }

  function rowContaining(container, text) {
    for (const row of container.children) {
      if (row.textContent.includes(text)) return row;
    }
    return null;
  }

  // overflowed collects any area whose content is wider than the window. A page
  // that scrolls sideways is a page whose controls are off the edge, which at
  // 420 pixels is most of them.
  const overflowed = [];

  async function go(area) {
    document.querySelector(`.area-tab[data-area="${area}"]`).click();
    await waitFor(`the ${area} area`, () => visible($("area-" + area)));
    await sleep(60);
    const overflow = document.documentElement.scrollWidth - document.documentElement.clientWidth;
    if (overflow > 2) {
      // Name what is too wide: the deepest elements whose own content is wider
      // than the window, so a failure says what to fix.
      const limit = document.documentElement.clientWidth;
      const wide = [...$("area-" + area).querySelectorAll("*")].filter((node) =>
        node.scrollWidth > limit && [...node.children].every((child) => child.scrollWidth <= limit)
      );
      const names = wide.slice(0, 4).map((node) =>
        `${node.tagName.toLowerCase()}${node.id ? "#" + node.id : ""}${node.className ? "." + node.className : ""}(${node.scrollWidth}px: ${textOf(node).trim().slice(0, 40)})`
      );
      overflowed.push(`${area} by ${overflow}px after step ${steps.length} (${names.join("; ") || "nothing named"})`);
    }
  }

  function setValue(node, value) {
    node.value = value;
    node.dispatchEvent(new Event("input", { bubbles: true }));
    node.dispatchEvent(new Event("change", { bubbles: true }));
  }

  try {
    const settings = await (await fetch("/journey/config")).json();

    // --- 1. the page loads, and says this machine is not signed in ----------
    await waitFor("the application to boot", () => window.AUCOM && window.AUCOM.status.version);
    record("the page loads", true, `version ${window.AUCOM.status.version}`);

    // NEW_244D: a fresh, signed-out machine opens on Build with nothing in the
    // way. Signing in is for the Library, and is a dialog a person opens.
    await waitFor("the first area", () => document.querySelector('.area-tab[aria-current="page"]'));
    record(
      "a fresh signed-out machine opens on Build, not on a sign-in form",
      visible($("area-build")) && !visible($("first-run")),
      `current area ${document.querySelector('.area-tab[aria-current="page"]').dataset.area}`
    );
    await go("library");
    record(
      "the Library says it is the one area that needs an account",
      visible($("library-signed-out")) && textOf($("library-signed-out")).includes("work signed out"),
      textOf($("library-signed-out")).replace(/\s+/g, " ").trim().slice(0, 90)
    );
    $("library-sign-in").click();
    await waitFor("the sign-in dialog", () => visible($("first-run")));
    record(
      "signing in is a dialog, focused on its first field",
      $("first-run").getAttribute("role") === "dialog" && document.activeElement === $("email"),
      textOf($("first-run-why")).slice(0, 80)
    );
    $("email").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await waitFor("the dialog to close on Escape", () => !visible($("first-run")));
    record("the dialog closes on Escape and gives focus back", document.activeElement === $("library-sign-in"),
      document.activeElement ? document.activeElement.id : "nothing focused");
    $("sign-in-open").click();
    await waitFor("the sign-in dialog again", () => visible($("first-run")));

    // The keyboard path is real: the skip link is the first focusable thing,
    // and every area is reachable as a button.
    // Named rather than counted: a bare number told the next person that
    // something changed, not what. The About area joined this list when the
    // gallery's About prose became a global area (AUP/AUCOM 243E §B).
    const AREAS = ["library", "build", "run", "profiles", "jobs", "settings", "about"];
    const tabs = [...document.querySelectorAll(".area-tab")];
    const named = tabs.map((tab) => tab.dataset.area);
    record(
      "every area is a keyboard-reachable control",
      named.join(",") === AREAS.join(",") && tabs.every((tab) => tab.tagName === "BUTTON"),
      named.join(", ")
    );

    // --- 2. sign in ---------------------------------------------------------
    setValue($("email"), settings.email);
    setValue($("password"), settings.password);
    $("sign-in-form").requestSubmit();
    await waitFor("the sign-in panel to go away", () => !visible($("first-run")));
    record("signing in", textOf($("account-email")).includes(settings.email), textOf($("account-email")));

    // --- 3. the library lists the account's assets --------------------------
    await go("library");
    const card = await waitFor("the asset card", () =>
      rowContaining($("library-assets"), settings.asset_name)
    );
    record("the library lists the account's maps", true, settings.asset_name);

    buttonIn(card, "Choose a revision").click();
    // The first child while it is loading is a placeholder, so this waits for a
    // row that actually offers the action rather than for any row at all.
    const revisionRow = await waitFor("the revision list", () => {
      const row = $("library-revisions").children[0];
      return row && buttonIn(row, "Download") ? row : null;
    });
    record("revisions are listed", true, textOf(revisionRow).trim().slice(0, 60));

    // --- 4. download an exact revision, and see the durable record ----------
    buttonIn(revisionRow, "Download").click();
    await waitFor(
      "the download to finish",
      () => {
        const message = revisionRow.querySelector(".message");
        return message && message.classList.contains("ok");
      },
      60000
    );
    // Matched on the asset id: a revision record carries the id for certain and
    // the display name only when the backend recorded one.
    const cachedRow = await waitFor("the cached list", () =>
      rowContaining($("cached-list"), settings.asset_id)
    );
    record(
      "the download leaves a record that is read back from disk",
      true,
      textOf(cachedRow).trim().slice(0, 90)
    );

    buttonIn(cachedRow, "Use in a build").click();
    record("a revision can be chosen for a build", Boolean(window.AUCOM.chosenRevision),
      window.AUCOM.chosenRevision ? window.AUCOM.chosenRevision.revision_id : "nothing chosen");

    // --- 5. the toolchain has to be reviewed and approved -------------------
    await go("profiles");
    const toolCard = await waitFor("the toolchain in the profile list", () =>
      rowContaining($("profiles-list"), "Fixture toolchain")
    );
    record(
      "a profile nobody vouched for is marked local and not approved",
      textOf(toolCard).includes("local") && textOf(toolCard).includes("not approved"),
      textOf(toolCard).replace(/\s+/g, " ").trim().slice(0, 110)
    );

    buttonIn(toolCard, "Review").click();
    await waitFor("the review panel", () => visible($("profile-detail-panel")) &&
      textOf($("profile-detail")).includes("What it asks to be allowed to do"));
    const approve = await waitFor("the approve button", () => buttonIn($("profile-detail"), "approve it"));
    record(
      "the review shows what the profile asks for before it can be approved",
      textOf($("profile-detail")).includes("has not been reviewed yet") ||
        textOf($("profile-detail")).includes("has not been approved on this machine"),
      textOf($("profile-detail")).replace(/\s+/g, " ").slice(-200)
    );

    approve.click();
    await waitFor("the approval to be recorded", () =>
      textOf($("profile-detail")).includes("Approved on")
    );
    record("approving a local profile", true, "recorded against its digest");

    // The tool's program has to be pointed at, the same way an engine's is.
    const toolField = await waitFor("the tool's path field", () =>
      $("profile-exe-tool")
    );
    setValue(toolField, settings.tool_path);
    buttonIn($("profile-detail"), "Save these paths").click();
    await waitFor("the paths to be recorded", () => {
      const messages = [...$("profile-detail").querySelectorAll(".message.ok")];
      return messages.some((node) => node.textContent.includes("Recorded"));
    });
    record("recording where a tool is on this machine", true, settings.tool_path);

    // --- 6. build -----------------------------------------------------------
    await go("build");
    await waitFor("the pipeline list", () => $("build-pipeline").options.length > 0);
    setValue($("build-pipeline"), settings.pipeline_id);
    await waitFor("the pipeline's stages", () => $("build-stages").children.length > 0);
    record(
      "the build area names the tool that will run each stage",
      textOf($("build-stages")).includes("Fixture toolchain"),
      textOf($("build-stages")).replace(/\s+/g, " ").trim().slice(0, 120)
    );

    const inputSource = await waitFor("the input control", () => $("build-input-source_map-source"));
    setValue(inputSource, "asset");
    setValue($("build-label"), "the browser journey");

    $("build-preview").click();
    await waitFor("the command preview", () => visible($("build-preview-out")) &&
      $("build-preview-out").querySelector("pre"));
    record(
      "the exact command is shown before anything runs",
      textOf($("build-preview-out")).includes(settings.tool_path),
      textOf($("build-preview-out").querySelector("pre")).slice(0, 120)
    );

    $("build-start").click();
    await waitFor("the build panel", () => visible($("build-current-panel")), 60000)
      .catch((err) => { throw new Error(`${err.message}; the build area said: ${textOf($("build-message"))}`); });
    await waitFor(
      "the build to finish",
      () => {
        const message = $("build-message");
        return message.classList.contains("ok") && message.textContent.includes("succeeded");
      },
      120000
    ).catch((err) => {
      throw new Error(`${err.message}; the build area said: ${textOf($("build-message"))} | ${textOf($("build-progress")).replace(/\s+/g, " ").slice(0, 300)}`);
    });
    record("the build succeeds", true, textOf($("build-progress")).replace(/\s+/g, " ").trim().slice(0, 120));
    record(
      "the finished build lists its artifacts",
      textOf($("build-progress")).includes("Artifacts"),
      textOf($("build-progress")).includes("Artifacts") ? "listed" : "no artifacts shown"
    );

    // The artifact. It is a button and not a link, because every API route
    // wants the token in a header and a plain navigation cannot send one — so
    // this checks the file actually comes back rather than that a link exists.
    const artifactRow = [...$("build-progress").children].find((row) =>
      row.textContent.includes("Artifacts")
    );
    const artifactButton = artifactRow && artifactRow.querySelector("button");
    if (!artifactButton) throw new Error("the finished build offers no artifact to fetch");
    artifactButton.click();
    await waitFor("the artifact fetch to answer", () =>
      artifactRow.textContent.includes("saved as") ||
      artifactRow.textContent.includes("could not be fetched")
    );
    record(
      "an artifact can actually be fetched from the page",
      artifactRow.textContent.includes("saved as"),
      artifactRow.textContent.replace(/\s+/g, " ").trim().slice(0, 100)
    );

    const historyRow = await waitFor("the build history", () =>
      rowContaining($("build-history"), "the browser journey")
    );
    record("the build is in the history read back from disk", true, textOf(historyRow).trim().slice(0, 90));

    // --- 7. set the engine up, and start it ---------------------------------
    await go("run");
    await waitFor("the engine list", () => $("run-engine").options.length > 0);
    setValue($("run-engine"), settings.engine_id);
    await waitFor("the engine detail", () => textOf($("run-engine-detail")).includes("The profile"));
    record(
      "an engine that is not set up says what is stopping it",
      textOf($("run-engine-detail")).includes("Before this can start"),
      textOf($("run-engine-detail")).replace(/\s+/g, " ").slice(0, 140)
    );

    setValue(await waitFor("the engine path field", () => $("run-exe-engine")), settings.tool_path);
    setValue(await waitFor("the game root field", () => $("run-root-game_root")), settings.game_root);
    setValue(await waitFor("the content root field", () => $("run-root-content_root")), settings.content);
    $("run-approve").checked = true;
    $("run-save-binding").click();
    await waitFor("the setup to be recorded", () => $("run-setup-message").classList.contains("ok"));
    record("recording the engine setup", true, textOf($("run-setup-message")));

    await waitFor("the engine to become ready", () =>
      !textOf($("run-engine-detail")).includes("Before this can start")
    );
    setValue($("run-action"), "play_map");
    setValue($("run-map"), "e1m1");
    setValue($("run-mod"), "id1");
    record(
      "the run area says what kind of session this is",
      textOf($("run-session-note")).length > 0,
      textOf($("run-session-note"))
    );

    $("run-preview").click();
    await waitFor("the launch preview", () => visible($("run-preview-out")) &&
      $("run-preview-out").querySelector("pre"));
    record(
      "the exact launch command is shown",
      textOf($("run-preview-out")).includes("-basedir"),
      textOf($("run-preview-out").querySelector("pre")).slice(0, 140)
    );

    $("run-launch").click();
    await waitFor("the launch to be accepted", () => $("run-message").classList.contains("ok"), 60000);
    record("starting the engine", true, textOf($("run-message")));

    // --- 8. the jobs area holds the durable record --------------------------
    await go("jobs");
    await waitFor("the job list", () => $("jobs-list").children.length > 0);
    record(
      "every run is in the job list",
      $("jobs-list").children.length >= 2,
      `${$("jobs-list").children.length} job(s)`
    );
    record(
      "this window's actions are kept as a list rather than a message that vanishes",
      $("activity-log").children.length >= 3,
      `${$("activity-log").children.length} entries`
    );

    const jobRow = $("jobs-list").children[0];
    buttonIn(jobRow, "Open").click();
    await waitFor("the job detail", () => visible($("job-detail-panel")) &&
      textOf($("job-detail")).includes("The command that ran"));
    // The panel offers Stop while the job is still going and Run again once it
    // has finished, so this waits for the job to reach a state where retry is
    // the question — the page keeps polling it until then.
    await waitFor("the job to finish", () => buttonIn($("job-detail"), "Run this again"), 60000);
    record(
      "retry says that it makes a new job rather than replacing this one",
      textOf($("job-detail")).includes("makes a NEW job"),
      textOf($("job-detail")).replace(/\s+/g, " ").slice(-160)
    );

    // --- 9. the whole flow completed at this window size --------------------
    record(
      `the journey completes at ${window.innerWidth}px without scrolling sideways`,
      overflowed.length === 0,
      overflowed.length === 0
        ? `${document.documentElement.clientWidth}px wide, no area overflowed`
        : "these areas overflowed: " + overflowed.join(", ")
    );

    // --- 10. and it can be completed from the keyboard alone ----------------
    // Every control the journey used is a native button, input or select, so
    // the browser puts all of them in the tab order; the check is that none of
    // them was made unreachable by a negative tabindex or a div-with-a-click.
    const controls = [
      "sign-in", "library-refresh", "build-pipeline", "build-preview", "build-start",
      "run-engine", "run-action", "run-save-binding", "run-launch", "jobs-refresh",
    ].map((id) => $(id));
    const unreachable = controls.filter(
      (node) => !node || node.tabIndex < 0 || !["BUTTON", "INPUT", "SELECT", "TEXTAREA"].includes(node.tagName)
    );
    record(
      "the controls the journey used are all keyboard-reachable",
      unreachable.length === 0,
      unreachable.length === 0 ? `${controls.length} controls` : unreachable.map((n) => n && n.id).join(", ")
    );

    // Tab order, in the document order a browser walks. The skip link has to be
    // first — a keyboard user should not have to walk six area tabs to reach
    // the page — and the whole build flow has to be inside the walk rather than
    // only inside the DOM.
    await go("build");
    const focusable = [...document.querySelectorAll(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )].filter((node) => node.offsetParent !== null || node.classList.contains("skip-link"));
    const order = focusable.map((node) => node.id).filter(Boolean);
    record(
      "the first thing in the tab order is the skip link",
      focusable[0] && focusable[0].classList.contains("skip-link"),
      focusable[0] ? focusable[0].className || focusable[0].tagName : "nothing focusable"
    );
    const wanted = ["build-pipeline", "build-preview", "build-start"];
    const positions = wanted.map((id) => order.indexOf(id));
    record(
      "the build flow is reachable by tabbing, in the order it is used",
      positions.every((index, i) => index >= 0 && (i === 0 || index > positions[i - 1])),
      wanted.map((id, i) => `${id}@${positions[i]}`).join(", ")
    );

    // --- the About area -----------------------------------------------------
    //
    // The words are the gallery's, compiled into the artefact this binary
    // serves; what a browser can add is that the area fills in at all, and that
    // a link this program cannot honour is NAMED rather than pointed at an
    // origin it was never told about — nothing here has a built-in address for
    // another component, so a route link would have to be a guess.
    await go("about");
    await waitFor("the About content", () => $("about-body").children.length > 0);
    const aboutText = textOf($("about-body"));
    record(
      "the About area renders the published prose",
      aboutText.includes("Auto-Pigeon is a level editor") && aboutText.includes("Published digest"),
      aboutText.slice(0, 80)
    );
    const aboutLinks = [...$("about-body").querySelectorAll("a")].map((a) => a.getAttribute("href"));
    record(
      "no link in the About area leaves for a host this program was not told about",
      aboutLinks.every((href) => href.startsWith("#about-") || /^https?:/i.test(href)),
      aboutLinks.join(", ") || "no links"
    );

    // Switching area moves focus to the heading, so a keyboard user lands on
    // the content that just changed rather than being left behind in it.
    await go("build");
    record(
      "switching area moves focus to the new heading",
      document.activeElement === $("area-heading"),
      document.activeElement ? document.activeElement.id || document.activeElement.tagName : "nothing focused"
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
