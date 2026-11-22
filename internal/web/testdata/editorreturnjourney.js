// AUCOM #37 and #49 (NEW_633), in a real browser against the real page and
// server with the fixture compiler: a returned leak result offers the way back
// to the editor only at the address the server's receipt gave for exactly this
// request; without one the sentence says how to find it; a foreign one is said
// to have been refused. The stage row names the program that ran. `late` is a
// fresh page opened after the return finished: the same control comes back
// from what the Companion kept, not from what the earlier page knew.

"use strict";

(async () => {
  const steps = [];
  let fatal = "";
  const log = (text) => {
    fetch("/journey/log", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text: String(text) }) }).catch(() => {});
  };
  const record = (step, ok, detail) => {
    steps.push({ step, ok, detail: String(detail || "") });
    log(`${ok ? "ok  " : "FAIL"} ${step} — ${detail || ""}`);
  };
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  window.addEventListener("error", (event) => log(`page error: ${event.message} (${event.filename}:${event.lineno})`));
  window.addEventListener("unhandledrejection", (event) => log(`page rejection: ${event.reason?.message || event.reason}`));

  async function waitFor(what, predicate, timeout = 30000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      const value = predicate();
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}; the return line says: ${returnLine()?.textContent || "nothing"}`);
      await sleep(25);
    }
  }

  const notice = () => document.getElementById("leak-request")?.textContent || "";
  const button = () => document.getElementById("build-start");
  const enabled = () => { const b = button(); return b && !b.hidden && !b.disabled; };
  const returnLine = () => document.querySelector("#build-leak .leak-return");
  const settled = () => /Sent to the editor for saved revision/.test(returnLine()?.textContent || "");
  const control = () => returnLine()?.querySelector("a.leak-return__editor") || null;

  function checkReturn(settings) {
    const link = control();
    const said = returnLine().textContent;
    if (settings.expect === "verified") {
      record("the way back is offered, at the address the server gave for this request",
        Boolean(link) && link.getAttribute("href") === settings.editor_url, link ? link.getAttribute("href") : said);
      record("its words follow the verdict", link?.textContent === settings.control_text, link?.textContent || "no control");
      record("it opens apart and gives the editor no hold on this page",
        link?.getAttribute("target") === "_blank" && /noopener/.test(link?.getAttribute("rel") || "") && /noreferrer/.test(link?.getAttribute("rel") || ""),
        `${link?.getAttribute("target")} ${link?.getAttribute("rel")}`);
      record("the switch-tabs sentence is not also shown", !/Switch to its tab/.test(said), said);
    } else {
      record("nothing to open without a verified address", !link && !returnLine().querySelector("a"), said);
      record("the sentence says how to find the result", /Switch to its tab to see it/.test(said), said);
      record(settings.expect === "refused" ? "a refused address is said to have been refused" : "a missing address says nothing about a refusal",
        settings.expect === "refused" ? /was not opened/.test(said) : !/was not opened/.test(said), said);
    }
  }

  const scenarios = {
    async returned(settings) {
      await waitFor("the notice", () => /saved revision 1/.test(notice()));
      await waitFor("Build to be enabled", enabled);
      button().click();
      await waitFor("the result to be returned", settled, 40000);
      checkReturn(settings);
      if (settings.stage_tool) {
        const tools = [...document.querySelectorAll(".stage-tool")];
        const row = tools.find((node) => node.textContent === settings.stage_tool);
        record("the stage row names the program that ran", Boolean(row), JSON.stringify(tools.map((node) => node.textContent)));
        record("the profile is named as the profile", /^Profile: .*, written for /.test(row?.getAttribute("title") || ""), row?.getAttribute("title") || "no title");
      }
    },
    async late(settings) {
      // Nothing was done in this page: the build finished and its result was
      // returned before the page existed.
      record("the page starts with no build open", !returnLine(), "");
      window.AUCOM.areas.build.openBuild(settings.build_id, settings.state);
      await waitFor("the finished build's return line", settled);
      checkReturn(settings);
    },
  };

  try {
    const settings = await (await fetch("/journey/config")).json();
    await scenarios[settings.scenario](settings);
  } catch (error) {
    fatal = error.message;
    log(`stopped: ${error.message}`);
  }
  await fetch("/journey/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error: fatal }),
  });
})();
