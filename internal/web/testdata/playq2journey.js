// The Quake II Build & Run journey, driven inside the real page (Q2_020).
//
// play_test.go's Quake II journey proves the coordinator packages, installs and
// accepts the engine's word. This proves somebody can get there: a Quake II
// build profile offers only Quake II engines and "Play it", the review shows
// the package and asks about the content folder's own files — and does NOT
// fetch a Quake 1 texture bundle, whose refusals would otherwise sit in the
// review and disable Start (seen live on the 1.268 candidate) — and the run's
// details say what the engine said.

"use strict";

(async () => {
  const steps = [];
  let fatal = "";
  const log = (text) => {
    fetch("/journey/log", { method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: String(text) }) }).catch(() => {});
  };
  const record = (step, ok, detail) => {
    steps.push({ step, ok, detail: String(detail || "") });
    log(`${ok ? "ok  " : "FAIL"} ${step} — ${detail || ""}`);
  };
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  window.addEventListener("error", (event) => log(`page error: ${event.message} (${event.filename}:${event.lineno})`));
  async function waitFor(what, predicate, timeout = 30000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      let value = null;
      try { value = await predicate(); } catch { value = null; }
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
      await sleep(100);
    }
  }
  const $ = (id) => document.getElementById(id);
  const visible = (node) => node && !node.hidden && node.offsetParent !== null;
  const textOf = (node) => (node ? node.textContent || "" : "");
  const setValue = (node, value) => {
    node.value = value;
    node.dispatchEvent(new Event("input", { bubbles: true }));
    node.dispatchEvent(new Event("change", { bubbles: true }));
  };
  const stepButton = (n) => $("play-step-tab-" + n);

  try {
    const settings = await (await fetch("/journey/config")).json();
    await waitFor("the page", () => $("identity") && textOf($("identity")).length > 0);
    $("sign-in-open").click();
    await waitFor("the sign-in dialog", () => visible($("first-run")));
    setValue($("email"), settings.email);
    setValue($("password"), settings.password);
    $("sign-in-form").requestSubmit();
    await waitFor("a signed-in session", () => window.AUCOM.status?.authenticated === true);
    await waitFor("Build & Run", () => visible($("area-play")), 20000);
    window.location.hash = "#play";
    await sleep(200);

    await waitFor("the map list", () => $("play-map").options.length > 1);
    setValue($("play-map"), settings.asset_id);
    await waitFor("the revisions", () => $("play-revision").options.length > 0);
    setValue($("play-map-name"), "q2room");

    stepButton(2).click();
    await waitFor("the build profiles", () => $("play-pipeline").options.length > 1);
    setValue($("play-pipeline"), settings.pipeline_id);
    await waitFor("the stages", () => $("play-stages").children.length > 0);

    stepButton(3).click();
    await waitFor("the engines", () => $("play-engine").options.length > 1);
    const q1 = [...$("play-engine").options].find((option) => option.value === settings.other_engine_id);
    record("a Quake II build profile offers only Quake II engines",
      q1 && q1.disabled && /not a Quake II engine/.test(q1.textContent), q1 ? q1.textContent : "the Quake 1 engine is not listed");
    setValue($("play-engine"), settings.engine_id);
    await waitFor("the actions", () => $("play-action").options.length > 0);
    const cards = [...document.querySelectorAll("#play-action-choices input[type=radio]")].map((input) => input.value);
    record("only Play it is offered for a Quake II map", cards.join(",") === "play_map", cards.join(","));
    record("the page names the package folder and leaves baseq2 alone",
      textOf($("play-game-root")).includes("aup-q2room") && textOf($("play-game-root")).includes("baseq2"),
      textOf($("play-game-root")).slice(0, 140));

    stepButton(4).click();
    await waitFor("the review", () => textOf($("play-review")).includes("Where files will be written"), 40000);
    await waitFor("the package card", () => $("play-grant-basis"), 10000);
    // Long enough for the second request a Quake 1 review makes for its WADs.
    await sleep(3000);
    const review = textOf($("play-review"));
    record("the review of a Quake II run fetches no texture bundle",
      !document.querySelector("#play-review .wad-list") && !/Not sent with this map's textures/.test(review) &&
        review.includes(".wal") && !textOf($("play-review-message")),
      review.replace(/\s+/g, " ").slice(0, 200));
    record("the review names the package it will install",
      review.includes("aup-q2room") && review.includes("q2room.pak"), review.match(/aup-q2room[^ ]*/)?.[0] || "");
    setValue($("play-grant-basis"), "own_work");

    stepButton(5).click();
    await waitFor("the final step", () => visible($("play-step-5")));
    record("the last step restates the answer about your own files and can start",
      textOf($("play-final-summary")).includes("It is my own work") && !$("play-start").disabled,
      `${textOf($("play-final-summary")).replace(/\s+/g, " ").slice(0, 160)} · start disabled=${$("play-start").disabled}`);
    $("play-start").click();

    const run = await waitFor("the run to finish", async () => {
      const { ok, body } = await window.AUCOM.api("/api/v1/play/runs");
      const newest = ok && (body.items || [])[0];
      return newest && !newest.active ? newest : null;
    }, 90000);
    record("the run succeeds on the engine's word that it opened the package",
      run.state === "succeeded" && run.launch?.map_load === "accepted" && run.package?.archive === "q2room.pak",
      `${run.state} at ${run.failed_at || "-"}: ${run.error || run.launch?.message || ""}`);
    // Technical details say it too.
    // The drawer polls; the card is drawn again once it has the finished run.
    const details = await waitFor("the run's card to show its launch",
      () => /accepted/.test(textOf(document.querySelector(".activity-run details"))) &&
        textOf(document.querySelector(".activity-run details")), 20000)
      .catch(() => textOf(document.querySelector(".activity-run details")));
    record("the run's details show the package and the engine's word",
      details.includes("q2room.pak") && details.includes("accepted"), details.replace(/\s+/g, " ").slice(-260));

    // Stop the engine this journey started, through the package panel.
    const latest = await window.AUCOM.api(`/api/v1/q2/installs/${encodeURIComponent(run.installation.id)}/runs`);
    if (latest.ok && latest.body.run) {
      await window.AUCOM.api(`/api/v1/q2/runs/${encodeURIComponent(latest.body.run.id)}/cancel`, { method: "POST" });
    }
  } catch (err) {
    fatal = err && err.message ? err.message : String(err);
    log("fatal: " + fatal);
  }
  await fetch("/journey/report", { method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error: fatal }) });
})();
