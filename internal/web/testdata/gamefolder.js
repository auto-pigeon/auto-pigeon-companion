// The Game folder suggested from the engine executable, driven inside the real
// page (HITL 2026-11-12, after a fresh install): choosing an engine — typed into
// its field, or found by "Use this folder" — fills Game folder with the
// directory holding the PROFILE's data folder and a real archive, says what it
// found, and records nothing until Save these paths.
//
// The page is reloaded once, to prove what Save recorded is what comes back, so
// the steps so far ride across the reload in sessionStorage. That is the
// DRIVER's state; the page under test stores nothing.

"use strict";

(async () => {
  const KEY = "aucom-gamefolder-journey";
  const saved = JSON.parse(sessionStorage.getItem(KEY) || "{}");
  const steps = saved.steps || [];
  const phase = saved.phase || 1;
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

  async function waitFor(what, predicate, timeout = 20000) {
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
      await sleep(80);
    }
  }
  const $ = (id) => document.getElementById(id);
  const visible = (node) => node && !node.hidden && node.offsetParent !== null;
  const textOf = (node) => (node ? node.textContent || "" : "");
  function setValue(node, value) {
    node.value = value;
    node.dispatchEvent(new Event("input", { bubbles: true }));
    node.dispatchEvent(new Event("change", { bubbles: true }));
  }
  function buttonIn(container, label) {
    for (const button of container.querySelectorAll("button")) {
      if (button.textContent.trim().toLowerCase().includes(label.toLowerCase())) return button;
    }
    return null;
  }
  async function control(path) {
    const response = await fetch(path, { method: "POST" });
    return response.status === 204;
  }
  // Answers from the near-engine lookup arrive this many ms late while set, so
  // a stale answer can be made to land after the thing it was for has changed.
  let detectDelay = 0;
  const realFetch = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const url = typeof input === "string" ? input : input.url;
    const answer = realFetch(input, init);
    if (detectDelay && url.includes("/api/v1/engines/detect")) {
      const delay = detectDelay;
      const response = await answer;
      await sleep(delay);
      return response;
    }
    return answer;
  };

  async function openEngine(name) {
    document.querySelector('.area-tab[data-area="profiles"]').click();
    await waitFor("Profiles", () => visible($("area-profiles")));
    setValue($("profiles-kind"), "engine");
    const row = await waitFor(`${name} in Profiles`, () =>
      [...$("profiles-list").children].find((r) => r.querySelector(".profile-row__name")?.textContent === name));
    row.querySelector("a.profile-row__name").click();
    await waitFor(`${name}'s page`, () =>
      visible($("profile-detail-panel")) && $("profile-detail-title").textContent === name && $("profile-exe-engine") && $("profile-root-game_root"));
    await sleep(100);
  }
  const game = () => $("profile-root-game_root");
  const suggestion = () => textOf($("profile-game-suggestion"));

  try {
    const s = await (await fetch("/journey/config")).json();
    await waitFor("the application to boot", () => window.AUCOM && window.AUCOM.status && window.AUCOM.status.version);

    if (phase === 1) {
      await openEngine(s.q1_name);
      record("Game folder starts empty for an engine nothing has been recorded for", game().value === "", game().value);

      // Path 1: the engine typed (or browsed) into its own field.
      setValue($("profile-exe-engine"), s.q1_exe);
      await waitFor("the suggestion", () => game().value !== "" && suggestion().includes("Found"));
      record(
        "choosing the engine fills Game folder with the folder holding id1/pak0.pak, and says so",
        game().value === s.q1_root && suggestion().includes(`Found ${s.q1_evidence} beside the engine.`),
        `${game().value} · ${suggestion()}`
      );
      record("a suggestion alone records nothing (the binding has no Game folder)", await control("/journey/assert-unbound"), "checked server-side");

      // A different engine with no game beside it: the unsaved suggestion is
      // dropped, and the page says which data folder it looked for.
      setValue($("profile-exe-engine"), s.bare_exe);
      await waitFor("the no-game answer", () => suggestion().includes("No id1 folder"));
      record(
        "changing the engine drops the unsaved suggestion and names the data folder it looked for",
        game().value === "" && /No id1 folder with pak0\.pak or pak1\.pak was found/.test(suggestion()),
        `"${game().value}" · ${suggestion()}`
      );

      // A late answer for the previous engine never lands on the new one.
      detectDelay = 1200;
      setValue($("profile-exe-engine"), s.q1_exe);
      await sleep(150);
      setValue($("profile-exe-engine"), s.bare_exe);
      await sleep(3000);
      detectDelay = 0;
      record(
        "an answer that arrives after the engine changed again is discarded",
        game().value === "" && !suggestion().includes("Found"),
        `"${game().value}" · ${suggestion()}`
      );

      // Path 2: "Use this folder" finds the programs, and suggests the game.
      setValue($("profile-folder"), s.q1_root);
      $("profile-use-folder").click();
      await waitFor("the folder to be used", () => /Found all 1 programs/.test(textOf($("profile-use-folder").closest(".row-actions").nextElementSibling)));
      await waitFor("the suggestion after Use this folder", () => game().value === s.q1_root);
      record(
        "Use this folder records the engine and suggests the Game folder beside it",
        $("profile-exe-engine").value === s.q1_exe && suggestion().includes(`Found ${s.q1_evidence} beside the engine.`),
        `${$("profile-exe-engine").value} · ${suggestion()}`
      );
      record("after Use this folder the Game folder is still only a suggestion", await control("/journey/assert-unbound"), "checked server-side");

      buttonIn($("profile-detail"), "Save these paths").click();
      await waitFor("the paths to be recorded", () => textOf($("profile-detail")).includes("Recorded."));
      record("Save these paths records the suggested Game folder", await control("/journey/assert-bound-suggested"), "checked server-side");

      sessionStorage.setItem(KEY, JSON.stringify({ steps, phase: 2 }));
      location.reload();
      return;
    }

    // ---- phase 2: after a reload -------------------------------------------------------
    await openEngine(s.q1_name);
    record("after a reload, Game folder shows what Save recorded", game().value === s.q1_root, game().value);

    // A saved Game folder is never replaced by a suggestion.
    setValue($("profile-exe-engine"), s.other_q1_exe);
    await waitFor("the kept-folder answer", () => suggestion().includes("is kept"));
    record(
      "choosing another engine keeps the saved Game folder, and says what it found",
      game().value === s.q1_root && suggestion().includes(`your Game folder ${s.q1_root} is kept`),
      `${game().value} · ${suggestion()}`
    );
    // Nor a Game folder typed by hand.
    setValue(game(), s.manual);
    setValue($("profile-exe-engine"), s.q1_exe);
    await waitFor("the kept-folder answer for the typed folder", () => suggestion().includes(`your Game folder ${s.manual} is kept`));
    record("a Game folder typed by hand is kept when the engine changes", game().value === s.manual, game().value);
    buttonIn($("profile-detail"), "Save these paths").click();
    await waitFor("the paths to be recorded", () => textOf($("profile-detail")).includes("Recorded."));
    record("the typed Game folder is what Save records", await control("/journey/assert-bound-manual"), "checked server-side");

    // The profile decides the game: a Quake II engine under bin/ finds baseq2 above it.
    await openEngine(s.q2_name);
    setValue($("profile-exe-engine"), s.q2_exe);
    await waitFor("the Quake II suggestion", () => game().value !== "");
    record(
      "a Quake II profile looks for baseq2/pak0.pak, above the engine's bin folder",
      game().value === s.q2_root && suggestion().includes(`Found ${s.q2_evidence} near the engine.`),
      `${game().value} · ${suggestion()}`
    );
    // Quake 1 data is not Quake II data.
    setValue($("profile-exe-engine"), s.q1_exe);
    await waitFor("the Quake II no-game answer", () => suggestion().includes("No baseq2 folder"));
    record("Quake 1 data beside the engine is not offered to a Quake II profile", game().value === "", suggestion());

    // Look for installed games is the selected profile's game too.
    buttonIn($("profile-detail"), "Look for installed games").click();
    await waitFor("the look answer", () => /No installed game|found baseq2/.test(textOf($("profile-detail"))));
    record(
      "Look for installed games looks for this profile's game",
      textOf($("profile-detail")).includes("looked for baseq2 with pak0.pak"),
      textOf(document.querySelector('[aria-label="Installed games found"]'))
    );

    // Leaving the profile while a lookup is in flight: nothing lands anywhere.
    detectDelay = 1200;
    setValue($("profile-exe-engine"), s.q2_exe);
    await sleep(100);
    await openEngine(s.q1_name);
    await sleep(2500);
    detectDelay = 0;
    record(
      "a lookup still running when another profile opens fills nothing in",
      game().value === s.manual && !suggestion().includes("baseq2"),
      `${game().value} · ${suggestion()}`
    );
    record("and the Quake II engine's Game folder was never recorded", await control("/journey/assert-q2-unbound"), "checked server-side");
  } catch (error) {
    fatal = error && error.message ? error.message : String(error);
    log("fatal: " + fatal);
  }
  sessionStorage.removeItem(KEY);
  await fetch("/journey/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error: fatal }),
  });
})();
