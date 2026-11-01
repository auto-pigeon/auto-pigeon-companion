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
  // The normal flow: coming back to Build & Run offers it, once.
  const offeredOnceOnReturn = async () => {
    await go("#profiles");
    await sleep(200);
    await go("#play");
    const dialog = await waitFor(`"${CONFIGURE}" on return`, () => dialogNamed(CONFIGURE));
    record("coming back offers the next step", times(CONFIGURE) === 1, `shown ${times(CONFIGURE)} time(s)`);
    press(dialog, "Cancel");
    await go("#profiles");
    await sleep(200);
    await go("#play");
    await sleep(400);
    record("and only once", times(CONFIGURE) === 1 && !dialogNamed(CONFIGURE), `shown ${times(CONFIGURE)} time(s)`);
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
      const closed = performance.now();
      const configure = await waitFor(`"${CONFIGURE}" straight after`, () => dialogNamed(CONFIGURE));
      record("Configure a game follows directly", true, `${Math.round(performance.now() - closed)} ms after the notice closed`);
      record("the Configure question takes the focus", configure.contains(document.activeElement), document.activeElement?.tagName);
      press(configure, "Cancel");
      await go("#profiles");
      await sleep(200);
      await go("#play");
      await sleep(400);
      record("it is asked once per page", times(CONFIGURE) === 1, `shown ${times(CONFIGURE)} time(s)`);
    },

    // Open Profiles still does what it says.
    async "open-profiles"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await control("tools-step");
      press(await installed(), "OK");
      press(await waitFor(`"${CONFIGURE}"`, () => dialogNamed(CONFIGURE)), "Open Profiles");
      await waitFor("Profiles", () => window.location.hash === "#profiles" && !document.getElementById("area-profiles").hidden);
      record("Open Profiles opens Profiles", openDialogs().length === 0, window.location.hash);
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
    },

    // The editor's request for a build with no run replaced what was asked for.
    async "changed-request"() {
      press(await waitFor(`"${INSTALL}"`, () => dialogNamed(INSTALL)), "Install");
      await waitFor("the install to be accepted", () => window.AUCOM.toolsStatus()?.setting_up);
      await control("tools-step");
      const notice = await installed();
      holding = true;
      press(notice, "OK");
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
      await waitFor("the engines answer to be held", () => held > 0);
      window.AUCOM.confirmModal({ title: "Another question", body: "One at a time.", confirm: "Yes", cancel: "No" });
      await sleep(0);
      holding = false;
      release();
      await sleep(QUIET_MS);
      record("it does not open over another", !dialogNamed(CONFIGURE) && openDialogs().length === 1,
        `open: ${openDialogs().map(titleOf).join(", ")}`);
      press(dialogNamed("Another question"), "No");
      await waitFor(`"${CONFIGURE}" after the other one`, () => dialogNamed(CONFIGURE));
      record("it follows the one that was open", times(CONFIGURE) === 1, `shown ${times(CONFIGURE)} time(s)`);
    },

    // Nothing to install: asked at once, as before.
    async "nothing-to-install"() {
      await waitFor(`"${CONFIGURE}"`, () => dialogNamed(CONFIGURE));
      record("with nothing to install it is the first question", times(INSTALL) === 0, seen.join(" > "));
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
