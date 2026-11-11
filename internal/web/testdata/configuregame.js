// The first-run order of the two blocking questions, driven inside the real page
// (NEW_374 L3): Install, then Configure a game, one at a time and never late.
//
// Every scenario runs in a page of its own, because what it is about — "asked
// once per page", "the person's Cancel of this session" — is the page's state.
// The server is real; `hold` keeps an answer of /api/v1/engines in the air,
// which is the stale callback a person cannot schedule by hand.

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
  window.addEventListener("error", (event) => log(`page error: ${event.message} (${event.filename}:${event.lineno})`));
  window.addEventListener("unhandledrejection", (event) => log(`page rejection: ${event.reason?.message || event.reason}`));

  async function waitFor(what, predicate, timeout = 20000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      const value = predicate();
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
      await sleep(25);
    }
  }

  const INSTALL = "Install the Auto-Pigeon build tools";
  const INSTALLED = "Build tools installed";
  const CONFIGURE = "Configure a game";
  // Longer than the 1.5 s retry this replaced: a modal that was going to come
  // back by a timer comes back inside this.
  const QUIET_MS = 2200;

  const openDialogs = () => [...document.querySelectorAll("dialog[open]")];
  const titleOf = (dialog) => dialog.querySelector("h2")?.textContent || "";
  const dialogNamed = (title) => openDialogs().find((dialog) => titleOf(dialog) === title);
  const press = (dialog, label) => {
    const button = [...dialog.querySelectorAll("button")].find((candidate) => candidate.textContent === label);
    if (!button) throw new Error(`"${titleOf(dialog)}" has no ${label} button`);
    button.click();
  };
  const control = (name) => fetch(`/journey/${name}`, { method: "POST" }).then((response) => {
    if (!response.ok) throw new Error(`/journey/${name} answered ${response.status}`);
  });
  const go = async (hash) => {
    window.location.hash = hash;
    await sleep(0);
  };

  // Everything the page ever showed, in order, and how many at once.
  const seen = [];
  let mostOpen = 0;
  new MutationObserver(() => {
    const open = openDialogs();
    mostOpen = Math.max(mostOpen, open.length);
    for (const dialog of open) {
      if (!dialog.dataset.journeySeen) {
        dialog.dataset.journeySeen = "1";
        seen.push(titleOf(dialog));
      }
    }
  }).observe(document.documentElement, { subtree: true, childList: true, attributes: true, attributeFilter: ["open"] });
  const times = (title) => seen.filter((name) => name === title).length;

  // An answer of the engines list, held until released.
  let holding = false;
  let held = 0;
  let release = () => {};
  const page = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const answer = await page(input, init);
    if (holding && String(input).startsWith("/api/v1/engines")) {
      held += 1;
      await new Promise((resolve) => { const before = release; release = () => { before(); resolve(); }; });
    }
    return answer;
  };

  const quiet = async (step, title) => {
    await sleep(QUIET_MS);
    record(step, !dialogNamed(title) && times(title) === 0, `shown ${times(title)} time(s); page showed: ${seen.join(" > ")}`);
  };
  const installed = async () => {
    const notice = await waitFor(`"${INSTALLED}"`, () => dialogNamed(INSTALLED));
    record("the installed notice names the folder", (notice.querySelector("code")?.textContent || "").endsWith("tools"),
      notice.querySelector("code")?.textContent);
    return notice;
  };
  // NEW_508A: "Configure a game" is no longer a dialog that opens by itself.
  // It is a line in "Run after build", beside the choice that needs a game,
  // shown while that is ticked for a saved map and no engine can start one.
  const gameLine = () => document.getElementById("play-no-game");
  const lineShown = () => Boolean(gameLine()) && gameLine().checkVisibility();
  // These pages open signed out, on a file on this computer: that build
  // starts no game, so it says nothing about one. The line belongs to a saved
  // map with Run after build ticked, which is where each check goes first.
  const toSavedMap = async () => {
    const saved = document.getElementById("play-source-saved");
    if (!saved.checked) {
      record("a file on this computer needs no game: no line, no dialog", !lineShown() && times(CONFIGURE) === 0,
        `line shown: ${lineShown()}`);
      saved.click();
      await sleep(0);
    }
  };
  const saidInline = async (step) => {
    await toSavedMap();
    await waitFor("the line about the game", lineShown);
    const link = gameLine().querySelector('a[href="#profiles"]');
    record(step, lineShown() && Boolean(link) && link.textContent === CONFIGURE && times(CONFIGURE) === 0 && openDialogs().length === 0,
      `line: ${gameLine().textContent.trim().slice(0, 90)}; dialogs: ${seen.join(" > ")}`);
  };
  // Coming back to Build & Run says it in the form again, and opens nothing.
  const offeredOnceOnReturn = async () => {
    await go("#profiles");
    await sleep(200);
    await go("#play");
    await saidInline("coming back says it beside Run after build, with no dialog");
    const run = document.getElementById("play-run-after");
    run.click();
    await sleep(300);
    record("a build with no run needs no game: unticked, the line goes", !lineShown() && !run.checked, String(lineShown()));
    run.click();
    await waitFor("the line again", lineShown);
    record("and no dialog ever opened for it", times(CONFIGURE) === 0, `shown ${times(CONFIGURE)} time(s)`);
  };

  const scenarios = {
    // Install > installed notice > Configure a game, each after the last.
    async "after-install"() {
      const install = await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL));
      record("Install asks first", !dialogNamed(CONFIGURE), `open: ${openDialogs().map(titleOf).join(", ")}`);
      press(install, "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await sleep(QUIET_MS);
      record("nothing is piled on while it installs", openDialogs().length === 0, `open: ${openDialogs().map(titleOf).join(", ")}`);
      await control("tools-step");
      const notice = await installed();
      record("the notice stands alone", openDialogs().length === 1, `open: ${openDialogs().map(titleOf).join(", ")}`);
      press(notice, "OK");
      await quiet("no second question follows the install", CONFIGURE);
      await saidInline("the missing game is said beside Run after build");
      record("the line takes no focus", !gameLine().contains(document.activeElement), document.activeElement?.tagName);
    },

    // Open Profiles still does what it says.
    async "open-profiles"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await control("tools-step");
      press(await installed(), "OK");
      await toSavedMap();
      await waitFor("the line about the game", lineShown);
      gameLine().querySelector('a[href="#profiles"]').click();
      await waitFor("Profiles", () => window.location.hash === "#profiles" && !document.getElementById("area-profiles").hidden);
      record("Configure a game opens Profiles", openDialogs().length === 0, window.location.hash);
    },

    // A person who said no is not asked the next thing at once.
    async "install-cancelled"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Cancel");
      await quiet("a Cancel brings up nothing", CONFIGURE);
      const tools = (await window.AUCOM.api("/api/v1/tools")).body;
      record("and installs nothing", tools.tools.some((tool) => tool.awaiting_install) && !tools.setting_up,
        JSON.stringify(tools.tools.map((tool) => [tool.id, tool.phase, Boolean(tool.awaiting_install)])));
      await offeredOnceOnReturn();
    },

    async "install-failed"() {
      await control("tools-fail");
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await control("tools-step");
      await waitFor("the failure", () => window.AUCOM.toolsStatus()?.tools?.some((tool) => tool.phase === "failed"));
      await quiet("a failure brings up nothing", CONFIGURE);
      record("no notice of an install that did not happen", times(INSTALLED) === 0, seen.join(" > "));
      await offeredOnceOnReturn();
    },

    // The install ends while the person is somewhere else.
    async "left-while-installing"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await go("#profiles");
      await control("tools-step");
      press(await installed(), "OK");
      const focused = document.activeElement;
      await quiet("nothing follows them out of Build & Run", CONFIGURE);
      record("the focus stays where it is", document.activeElement === focused, document.activeElement?.tagName);
      await offeredOnceOnReturn();
    },

    // The list answers after the person left: a stale callback opens nothing.
    async "stale-answer"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await control("tools-step");
      const notice = await installed();
      holding = true;
      press(notice, "OK");
      // The question about the game is asked for a saved map with Run after
      // build ticked: choosing one is what sends the request held here.
      document.getElementById("play-source-saved").click();
      await waitFor("the engines answer to be held", () => held > 0);
      await go("#profiles");
      await sleep(100);
      holding = false;
      release();
      await quiet("a late answer opens nothing", CONFIGURE);
      await offeredOnceOnReturn();
    },

    // A game was set up while the question waited.
    async "configured-elsewhere"() {
      const install = await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL));
      await control("bind-engine");
      press(install, "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await control("tools-step");
      press(await installed(), "OK");
      await quiet("a game that is ready needs no question", CONFIGURE);
      document.getElementById("play-source-saved").click();
      await sleep(600);
      record("and no line, on a saved map either", !lineShown(), String(lineShown()));
    },

    // The editor's request for a build with no run replaced what was asked for.
    async "changed-request"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await control("tools-step");
      const notice = await installed();
      holding = true;
      press(notice, "OK");
      // The question about the game is asked for a saved map with Run after
      // build ticked: choosing one is what sends the request held here.
      document.getElementById("play-source-saved").click();
      await waitFor("the engines answer to be held", () => held > 0);
      const settings = await (await fetch("/journey/config")).json();
      const adopted = window.AUCOM.areas.play.adoptBuildRequest({
        request_id: "journey-request", asset_id: settings.asset_id, revision_id: settings.revision_id, run_after: false,
      });
      await sleep(100);
      holding = false;
      release();
      await adopted;
      await quiet("a build with no run needs no game", CONFIGURE);
      document.getElementById("play-source-saved").click();
      await sleep(600);
      record("and no line, on a saved map either", !lineShown(), String(lineShown()));
    },

    // Another question is open when the answer lands: it waits for that one to
    // close, and no timer does the waiting.
    async "other-dialog"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await control("tools-step");
      const notice = await installed();
      holding = true;
      press(notice, "OK");
      // The question about the game is asked for a saved map with Run after
      // build ticked: choosing one is what sends the request held here.
      document.getElementById("play-source-saved").click();
      await waitFor("the engines answer to be held", () => held > 0);
      window.AUCOM.confirmModal({ title: "Another question", body: "One at a time.", confirm: "Yes", cancel: "No" });
      await sleep(0);
      holding = false;
      release();
      await sleep(QUIET_MS);
      record("it does not open over another", !dialogNamed(CONFIGURE) && openDialogs().length === 1,
        `open: ${openDialogs().map(titleOf).join(", ")}`);
      press(dialogNamed("Another question"), "No");
      await quiet("and nothing follows the one that was open", CONFIGURE);
      await saidInline("the line was there all along, under the other question");
    },

    // The install ends under another question: the notice waits for it.
    async "notice-waits"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      window.AUCOM.confirmModal({ title: "Another question", body: "One at a time.", confirm: "Yes", cancel: "No" });
      await control("tools-step");
      await waitFor("the tools to be in", () => window.AUCOM.toolsStatus()?.tools?.every((tool) => tool.phase === "ready"));
      await sleep(300);
      record("the notice does not open over another", openDialogs().length === 1, `open: ${openDialogs().map(titleOf).join(", ")}`);
      press(dialogNamed("Another question"), "No");
      press(await installed(), "OK");
      await quiet("nothing follows the notice", CONFIGURE);
      record("the order holds", seen.join(" > ").endsWith(`Another question > ${INSTALLED}`), seen.join(" > "));
    },

    // Nothing to install: no question at all — the line, in the form.
    async "nothing-to-install"() {
      // The page's own start-up first: the source is chosen once it has read
      // who is signed in.
      await sleep(1500);
      await saidInline("with nothing to install the first start opens no dialog");
      record("and the page asked nothing", seen.length === 0, seen.join(" > "));
    },
  };

  try {
    const settings = await (await fetch("/journey/config")).json();
    await scenarios[settings.scenario]();
    record("never two dialogs at once", mostOpen <= 1, `most open at once: ${mostOpen}; page showed: ${seen.join(" > ")}`);
  } catch (error) {
    fatal = error.message;
    log(`stopped: ${error.message}; page showed: ${seen.join(" > ")}; open: ${openDialogs().map(titleOf).join(", ")}`);
  }
  await page("/journey/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error: fatal }),
  });
})();
