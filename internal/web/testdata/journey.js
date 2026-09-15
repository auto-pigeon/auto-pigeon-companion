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

    // More than one page: the first says so, and Show more brings the rest.
    await waitFor("the more-than-one-page note", () => visible($("library-more")));
    record(
      "My Maps says the account has more than the first page",
      textOf($("library-message")).includes("Your account has more"),
      textOf($("library-message"))
    );
    $("library-more").click();
    await waitFor("the second page", () => rowContaining($("library-assets"), "Older Coast"));
    record(
      "Show more adds the next page and then goes away",
      !visible($("library-more")) && rowContaining($("library-assets"), settings.asset_name) !== null,
      textOf($("library-message"))
    );

    // NEW_244D: the card downloads the LATEST revision; older ones are folded
    // away. And no id of any kind is shown to the person.
    record(
      "a map is shown by its name, with no id",
      !textOf(card).includes(settings.asset_id) && textOf(card).includes("Latest"),
      textOf(card).replace(/\s+/g, " ").trim().slice(0, 90)
    );
    buttonIn(card, "Download latest").click();
    await waitFor(
      "the download to finish",
      () => {
        const message = card.querySelector(".message");
        return message && message.classList.contains("ok");
      },
      60000
    );
    const cachedRow = await waitFor("the cached list", () =>
      rowContaining($("cached-list"), settings.asset_name)
    );
    record(
      "the download leaves a record that is read back from disk, without an id",
      !textOf(cachedRow).includes(settings.asset_id),
      textOf(cachedRow).trim().slice(0, 90)
    );

    buttonIn(cachedRow, "Use in a build").click();
    record("a revision can be chosen for a build", Boolean(window.AUCOM.chosenRevision),
      window.AUCOM.chosenRevision ? window.AUCOM.chosenRevision.revision_id : "nothing chosen");
    await waitFor("Build to open on the chosen map", () => visible($("area-build")) && visible($("build-step-2")));
    record(
      "Use in a build opens Build at the map step with the revision in the map field",
      $("build-input-source_map-source")?.value === "asset" && textOf($("build-step-2")).includes("downloaded to this computer"),
      `${$("build-input-source_map-source")?.value} · ${textOf($("build-step-summary-2"))}`
    );

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
    // The digest is what the approval is recorded against, and it stays on the
    // wire: neither it nor the profile's id is something a person reads.
    const profilesText = textOf($("area-profiles"));
    record(
      "Profiles shows no profile id and no digest",
      !profilesText.includes("aucom.fixture.toolchain") && !/sha256|[0-9a-f]{12}/.test(profilesText),
      (profilesText.match(/.{0,80}(aucom\.fixture\.toolchain|sha256\S*|[0-9a-f]{12}\S*).{0,40}/) || ["none shown"])[0]
    );

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

    // A map already chosen survives choosing another pipeline that also takes
    // one, and choosing this one again (NEW_244D rehearsal: it was emptied).
    const typedMap = await waitFor("the map path field", () => $("build-input-source_map"));
    setValue(typedMap, "/a map chosen before switching.map");
    const other = [...$("build-pipeline").options].find((option) => option.value !== settings.pipeline_id && /^Quake 1/.test(option.text));
    if (other) {
      setValue($("build-pipeline"), other.value);
      setValue($("build-pipeline"), settings.pipeline_id);
    }
    record(
      "a chosen map survives switching pipeline and back",
      Boolean(other) && $("build-input-source_map")?.value === "/a map chosen before switching.map",
      other ? String($("build-input-source_map")?.value) : "no second Quake 1 pipeline to switch to"
    );
    setValue($("build-input-source_map"), "");

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
        const message = $("build-result");
        return message.classList.contains("ok") && message.textContent.includes("succeeded");
      },
      120000
    ).catch((err) => {
      throw new Error(`${err.message}; the build area said: ${textOf($("build-result"))} | ${textOf($("build-progress")).replace(/\s+/g, " ").slice(0, 300)}`);
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
    record("a finished build with a level offers Play this build", visible($("build-play")), textOf($("build-play")));

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
    const runText = textOf($("run-engine-detail"));
    record(
      "what is stopping it points at the setup form, not at a command, an id or a placeholder",
      !runText.includes("`companion ") && !runText.includes(settings.engine_id) && !runText.includes("{platform."),
      (runText.match(new RegExp(".{0,80}(`companion |\\{platform\\.|" + settings.engine_id.replace(/\./g, "\\.") + ").{0,40}")) || ["clean"])[0]
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

    // Play this build: Run offers the build just made, and Start stages its
    // level as <mod>/maps/<map>.bsp before the engine starts (NEW_244D).
    // Start from an engine that is NOT set up, as a person arriving from Build
    // would find the list: Play this build must pick the one that can play.
    const notReady = [...$("run-engine").options].find((option) => option.value !== settings.engine_id);
    if (notReady) setValue($("run-engine"), notReady.value);
    await window.AUCOM.areas.run.chooseBuild(
      [...$("run-build").options].find((option) => option.textContent.includes("the browser journey"))?.value || ""
    );
    const chosenBuild = $("run-build").selectedOptions[0];
    record(
      "Run offers the build just made, with its map and a game directory filled in",
      Boolean($("run-build").value) && $("run-mod").value === "auto-pigeon" && $("run-map").value.length > 0 &&
        $("run-engine").value === settings.engine_id,
      `${chosenBuild ? chosenBuild.textContent : "none"} · mod ${$("run-mod").value} · map ${$("run-map").value}`
    );
    $("run-launch").click();
    await waitFor("the staged launch to be accepted", () =>
      $("run-message").classList.contains("ok") || $("run-message").classList.contains("error"), 60000);
    record(
      "Start copies the build's level into the game directory, then starts the engine",
      $("run-message").classList.contains("ok") &&
        [...document.querySelectorAll("#activity-log li")].some((row) => row.textContent.includes("maps/")),
      textOf($("run-message"))
    );
    setValue($("run-build"), "");
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
    // The build is a wizard: the step buttons come first, then the current
    // step's controls, and the check step holds Check again before Build.
    const tabOrder = () => [...document.querySelectorAll(
      'button:not([disabled]), input:not([disabled]), select:not([disabled])'
    )].filter((node) => node.offsetParent !== null).map((node) => node.id).filter(Boolean);
    $("build-step-tab-1").click();
    const focusedHeading = document.activeElement;
    record(
      "a wizard step's heading takes focus without a focus box round it",
      focusedHeading && focusedHeading.tagName === "H3" && getComputedStyle(focusedHeading).outlineStyle === "none",
      focusedHeading ? `${focusedHeading.tagName} outline ${getComputedStyle(focusedHeading).outlineStyle}` : "nothing focused"
    );
    const first = tabOrder();
    window.AUCOM.areas.build.showStep(3, { check: false, focus: false });
    // Arriving back on Build re-runs a stale check, and a button is disabled
    // while its own request is out.
    await waitFor("the check to settle", () => !$("build-preview").disabled, 30000);
    const third = tabOrder();
    const positions = [
      first.indexOf("build-step-tab-1"), first.indexOf("build-step-tab-4"), first.indexOf("build-pipeline"),
    ];
    record(
      "the build wizard is reachable by tabbing, in the order it is used",
      positions.every((index, i) => index >= 0 && (i === 0 || index > positions[i - 1])) &&
        third.indexOf("build-preview") >= 0 && third.indexOf("build-preview") < third.indexOf("build-history-refresh"),
      `step buttons ${positions.slice(0, 2).join("→")}, pipeline @${positions[2]}; check again @${third.indexOf("build-preview")}`
    );
    $("build-step-tab-1").click();

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
