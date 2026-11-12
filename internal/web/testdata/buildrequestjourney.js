// The editor's build request through the own-WADs folder step, in the real page
// against the real server (NEW_502, issue #33). Once, live, confirming "Use this
// folder" in the review of an adopted request showed "This build is no longer
// the one the editor asked for" and the request was lost at the last step.
//
// What this proves, in the order a person meets it:
//
//   - the request opens in Build & Run and the textures name a WAD the
//     deployment did not send;
//   - a second editor request arriving meanwhile waits on its own card and
//     never takes the form (the single-slot replacement is what dropped it);
//   - a folder that lacks the WAD is refused and the review stays on the
//     request — nothing is dropped, nothing starts;
//   - the right folder makes it ready, and ONE press starts it, naming the
//     request, on the first attempt; a second press starts nothing more;
//   - the other request is still pending afterwards, and was never opened by
//     itself.

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

  const $ = (id) => document.getElementById(id);
  const textOf = (node) => (node ? node.textContent.replace(/\s+/g, " ").trim() : "");
  const cards = () => [...document.querySelectorAll("#build-request .leak-request__card")];
  const cardFor = (id) => cards().find((card) => card.dataset.requestId === id);
  const primary = () => $("play-start");
  const dropped = () => !$("play-request-note").hidden && /no longer the one the editor asked for/.test(textOf($("play-request-note")));

  async function waitFor(what, predicate, timeout = 30000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      const value = predicate();
      if (value) return value;
      if (Date.now() > deadline) {
        throw new Error(`timed out waiting for ${what}; cards: ${cards().map(textOf).join(" || ")}; ` +
          `attention: ${textOf($("play-review-attention")).slice(0, 300)}; start: ${textOf($("play-start-message"))}`);
      }
      await sleep(25);
    }
  }

  // Every start the page sends, with what the server answered.
  const pageFetch = window.fetch.bind(window);
  const starts = [];
  window.fetch = async (input, init) => {
    const path = String(typeof input === "string" ? input : input.url);
    const response = await pageFetch(input, init);
    if (path.endsWith("/api/v1/play/runs") && init?.method === "POST") {
      starts.push({ body: JSON.parse(init.body), status: response.status });
    }
    return response;
  };
  const control = (name) => pageFetch(`/journey/${name}`, { method: "POST" }).then((response) => {
    if (!response.ok) throw new Error(`/journey/${name} answered ${response.status}`);
  });
  const setValue = (input, value) => {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new Event("change", { bubbles: true }));
  };

  try {
    const settings = await (await pageFetch("/journey/config")).json();
    const first = settings.first_request;

    await waitFor("the request's card", () => /Ready in Build & Run/.test(textOf(cardFor(first))));
    record("the request opened in Build & Run", !$("area-play").hidden && /Build requested by the editor/.test(textOf(cardFor(first))), textOf(cardFor(first)));
    // The fixture's pipeline is not the game's preferred one: chosen by hand,
    // which is not a change of what the editor asked for.
    if ($("play-pipeline").value !== settings.pipeline_id) {
      await waitFor("the fixture pipeline in the list", () => [...$("play-pipeline").options].some((o) => o.value === settings.pipeline_id));
      setValue($("play-pipeline"), settings.pipeline_id);
    }
    await waitFor("the review", () => textOf($("play-review")).includes("What will be downloaded"), 40000);
    $("play-check-textures")?.click();
    await waitFor("the own-WADs offer", () => $("play-own-wads-dir") && /Not sent with this map's textures/.test(textOf($("play-review-attention"))), 40000);
    record("the textures name the WAD the deployment did not send", /second\.wad/.test(textOf($("play-review-attention"))), textOf($("play-review-attention")).slice(0, 160));

    // A second editor request arrives while this one is at the folder step.
    await control("receive-second");
    await waitFor("the second request's card", () => cardFor(settings.second_request)?.querySelector('button[data-action="open"]'));
    await sleep(600);
    record("the second request waits on its own card", /Waiting: the form is open on another request/.test(textOf(cardFor(settings.second_request))), textOf(cardFor(settings.second_request)));
    record("and the form stays on the first", /Ready in Build & Run/.test(textOf(cardFor(first))) && !dropped(), textOf($("play-request-note")));

    // A folder without the WAD: refused, and the review stays on the request.
    setValue($("play-own-wads-dir"), settings.wrong_folder);
    [...$("play-review-attention").querySelectorAll("button")].find((b) => b.textContent === "Use this folder").click();
    await waitFor("the refusal of that folder", () => /This folder does not have every WAD the map needs/.test(textOf($("play-review-attention"))));
    record("a folder without the WAD starts nothing and drops nothing", primary().disabled && !dropped() && starts.length === 0,
      `disabled=${primary().disabled} dropped=${dropped()} starts=${starts.length}`);

    // The right folder.
    setValue($("play-own-wads-dir"), settings.own_folder);
    [...$("play-review-attention").querySelectorAll("button")].find((b) => b.textContent === "Use this folder").click();
    await waitFor("the folder to be used", () => /Ready: the build will use your own copy/.test(textOf($("play-review-attention"))));
    await waitFor("the button", () => !primary().disabled, 30000);
    record("confirming the folder keeps the request", !dropped() && /Ready in Build & Run/.test(textOf(cardFor(first))), textOf($("play-request-note")));

    primary().click();
    await waitFor("the start", () => starts.length >= 1, 20000);
    const start = starts[0];
    record("one press starts it on the first attempt, naming the request",
      start.status === 202 && start.body.build_request_id === first && start.body.own_wads_dir === settings.own_folder,
      JSON.stringify({ status: start.status, build_request_id: start.body.build_request_id, own: start.body.own_wads_dir }));
    await waitFor("the first card to go", () => !cardFor(first), 20000);
    primary().click();
    await sleep(800);
    record("a second press does not start the request again",
      starts.filter((item) => item.body.build_request_id === first && item.status === 202).length === 1,
      JSON.stringify(starts.map((item) => [item.status, item.body.build_request_id || ""])));
    record("the other request is still pending and was not opened by itself",
      Boolean(cardFor(settings.second_request)?.querySelector('button[data-action="open"]')), textOf(cardFor(settings.second_request)));
    record("Activity lists it", document.querySelectorAll("#activity-requests .activity-item").length === 1,
      `${document.querySelectorAll("#activity-requests .activity-item").length} row(s)`);
  } catch (error) {
    fatal = error.message;
    log(`stopped: ${error.message}`);
  }
  await pageFetch("/journey/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error: fatal }),
  });
})();
