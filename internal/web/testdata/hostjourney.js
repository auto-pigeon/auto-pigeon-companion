// Hosting from Build & Run, driven inside the real page (NEW_247A2).
//
// What it checks:
//
//   - a cold page offers a hosted game as Private, and the review asks for
//     Private, without anybody touching "Who can see it";
//   - choosing Everyone shows "Help to connect" beside the listing fields, at
//     the gallery's /help/host-a-game, in a new tab with noopener noreferrer,
//     reachable by keyboard — or, when the server named no gallery, a sentence
//     saying help is unavailable, never a guessed link;
//   - changing the map's name, the engine or the action keeps the explicit
//     choice;
//   - the review shows the same link and the exact endpoint;
//   - Activity shows it while the public game is listed, and not once the
//     listing has ended;
//   - the next launch is Private again, and neither its review nor its Activity
//     card offers the link.
//
// The Go side (hostbrowser_test.go) asserts what the page SENT: the preview's
// and each registration's visibility.

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
  const snap = async (name) => {
    await sleep(250);
    await fetch("/journey/snap", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    }).catch(() => {});
  };
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
  const onScreen = (node) => Boolean(node) && !node.hidden && node.getBoundingClientRect().width > 0;
  const textOf = (node) => (node ? node.textContent || "" : "");
  const squash = (text) => String(text).replace(/\s+/g, " ").trim();
  const setValue = (node, value) => {
    node.value = value;
    node.dispatchEvent(new Event("input", { bubbles: true }));
    node.dispatchEvent(new Event("change", { bubbles: true }));
  };
  // NEW_365: the check runs by itself and the one primary always says
  // "Build & Run"; it is enabled once these exact choices have been checked.
  async function reviewBuild() {
    await waitFor("the check of these choices", () => textOf($("play-review")).includes("What will be downloaded"), 40000);
  }
  // A game listed for everyone is the one decision a press asks about, in a
  // focused dialog with the same action at its bottom right.
  let cancelledOnce = false;
  async function buildAndRun() {
    await waitFor("the primary to say Build & Run", () => textOf($("play-start")) === "Build & Run" && !$("play-start").disabled, 40000);
    $("play-start").click();
    await sleep(150);
    let dialog = document.querySelector("dialog.modal[open]");
    if (dialog && !cancelledOnce) {
      // Cancel first: nothing starts, the dialog goes, the button is as it was.
      cancelledOnce = true;
      const before = (await api("/api/v1/play/runs")).items.length;
      [...dialog.querySelectorAll(".modal-actions button")].find((b) => textOf(b) === "Cancel").click();
      await sleep(300);
      const after = (await api("/api/v1/play/runs")).items.length;
      record("cancelling the listing decision starts nothing and keeps the button",
        before === after && !document.querySelector("dialog.modal[open]") && textOf($("play-start")) === "Build & Run" && !$("play-start").disabled,
        `${before} → ${after} runs`);
      $("play-start").click();
      await sleep(150);
      dialog = document.querySelector("dialog.modal[open]");
    }
    if (dialog) {
      record("listing for everyone is asked in a dialog whose action says Build & Run",
        textOf(dialog.querySelector(".modal-actions .primary")) === "Build & Run" && Boolean(dialog.querySelector(".modal-close")),
        textOf(dialog).replace(/\s+/g, " ").slice(0, 140));
      dialog.querySelector(".modal-actions .primary").click();
    }
  }
  const helpIn = (root) => (root ? root.querySelector("[data-help-to-connect]") : null);
  // The review's Live Games card, found by its heading: several cards share a
  // class. It is in the open, beside the summary: the listing is the person's.
  const listingCard = () => [...$("play-review-attention").querySelectorAll(".review-card")]
    .find((card) => textOf(card.querySelector("h4")) === "Live Games listing") || null;
  const api = async (path, options = {}) => {
    const response = await fetch(path, {
      ...options,
      headers: { "Content-Type": "application/json", "X-AUCOM-Token": settings.api_token },
    });
    return response.json();
  };
  let settings = {};

  // The link, or the sentence saying there is none — whichever this server
  // should produce — checked the same way in all three places.
  function checkHelp(where, root) {
    const help = helpIn(root);
    if (!settings.gallery) {
      record(
        `${where}: with no gallery named, help is said to be unavailable, not guessed`,
        help && help.dataset.helpToConnect === "unavailable" && !root.querySelector("a[data-help-to-connect]") &&
          /not available/.test(textOf(help)),
        help ? squash(textOf(help)) : "nothing shown"
      );
      return;
    }
    const want = settings.gallery.replace(/\/+$/, "") + "/help/host-a-game";
    const rel = (help?.getAttribute("rel") || "").split(/\s+/);
    record(
      `${where}: "Help to connect" opens the gallery's help page in a new tab`,
      help && help.tagName === "A" && textOf(help).trim() === "Help to connect" &&
        help.getAttribute("href") === want && help.getAttribute("target") === "_blank" &&
        rel.includes("noopener") && rel.includes("noreferrer"),
      help ? `${help.tagName} ${help.getAttribute("href")} target=${help.getAttribute("target")} rel=${help.getAttribute("rel")}` : "no link"
    );
  }

  try {
    settings = await (await fetch("/journey/config")).json();
    await waitFor("the page", () => $("identity") && textOf($("identity")).length > 0);

    // --- sign in ------------------------------------------------------------
    $("sign-in-open").click();
    await waitFor("the sign-in dialog", () => visible($("first-run")));
    setValue($("email"), settings.email);
    setValue($("password"), settings.password);
    $("sign-in-form").requestSubmit();
    await waitFor("a signed-in session", () => window.AUCOM.status?.authenticated === true, 30000);
    await waitFor("Build & Run", () => visible($("area-play")), 20000);
    await sleep(200);
    window.location.hash = "#play";
    await waitFor("the play area", () => visible($("area-play")));

    // --- 1–3: a map, a build profile, an engine that hosts -------------------
    await waitFor("the map list", () => $("play-map").options.length > 1);
    setValue($("play-map"), settings.asset_id);
    await waitFor("the revisions", () => $("play-revision").options.length > 0);
    setValue($("play-map-name"), "dm1");
    await waitFor("the build profiles", () => $("play-pipeline").options.length > 1);
    setValue($("play-pipeline"), settings.pipeline_id);
    await waitFor("the engines", () => $("play-engine").options.length > 1);
    setValue($("play-engine"), settings.engine_id);
    await waitFor("the host action", () => $("play-action-host_listen"));
    $("play-action-host_listen").click();
    await waitFor("the Live Games block", () => visible($("play-listing")));
    // The suggested address is this machine's route to the fixture server, a
    // loopback one, which can only ever be listed privately. A public address
    // is what somebody who wants to be reachable types.
    await waitFor("the suggested port", () => $("play-listing-port").value !== "");
    setValue($("play-listing-host"), "203.0.113.4");

    // --- Private is the default -------------------------------------------
    const select = $("play-listing-visibility");
    record(
      "a cold page lists a hosted game as Private",
      $("play-list-it").checked && select.value === "private" &&
        !select.querySelector('option[value="public"]').disabled,
      `listed=${$("play-list-it").checked} visibility=${select.value}`
    );
    record(
      "no help link while the game is Private",
      $("play-listing-help").hidden && !helpIn($("play-listing-help")),
      `hidden=${$("play-listing-help").hidden}`
    );
    await snap("configure-private-default");

    // The review of the untouched choice: Private, and no link.
    await reviewBuild();
    await waitFor("the listing card", () => /Players connect to/.test(textOf(listingCard())), 40000);
    record(
      "the review of an untouched listing offers no help link",
      !helpIn($("play-review-attention")) && /only you/.test(textOf(listingCard())),
      squash(textOf(listingCard())).slice(0, 160)
    );

    // --- Everyone, chosen ----------------------------------------------------
    setValue(select, "public");
    await waitFor("the help beside the listing fields", () => visible($("play-listing-help")));
    checkHelp("beside the listing fields", $("play-listing-help"));
    const link = $("play-listing-help").querySelector("a");
    if (link) {
      link.focus();
      record("the help link is reachable by keyboard", document.activeElement === link,
        document.activeElement ? document.activeElement.tagName : "nothing");
    }
    record(
      "the page does not scroll sideways with the help shown",
      document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
      `${document.documentElement.scrollWidth}px of content in ${document.documentElement.clientWidth}px`
    );
    $("play-listing-help").scrollIntoView({ block: "center" });
    await snap("configure-public-help");

    // Changing other choices keeps the explicit one.
    setValue($("play-map-name"), "dm1x");
    setValue($("play-map-name"), "dm1");
    setValue($("play-engine"), settings.engine_id);
    await waitFor("the host action again", () => $("play-action-host_dedicated"));
    $("play-action-host_dedicated").click();
    $("play-action-host_listen").click();
    await sleep(200);
    record(
      "changing the name, the engine or the action keeps Everyone",
      select.value === "public" && visible($("play-listing-help")),
      `visibility=${select.value}`
    );

    // --- the review -----------------------------------------------------------
    await reviewBuild();
    const card = await waitFor("the public listing card", () => {
      const found = listingCard();
      return found && /Players connect to/.test(textOf(found)) && helpIn(found) ? found : null;
    }, 40000).catch((err) => { throw new Error(`${err.message}; the review said: ${squash(textOf(listingCard())).slice(0, 300)}`); });
    record(
      "the review names the exact endpoint players connect to",
      textOf(card).includes("203.0.113.4:" + $("play-listing-port").value),
      squash(textOf(card)).slice(0, 200)
    );
    checkHelp("in the review", card);
    card.scrollIntoView({ block: "center" });
    await snap("review-public-help");

    // --- launch ---------------------------------------------------------------
    record(
      "the summary says who will see it",
      textOf($("play-final-summary")).includes("Everyone"),
      squash(textOf($("play-final-summary"))).slice(-120)
    );
    await buildAndRun();
    await waitFor("the run's card", () => visible($("play-current")), 20000);
    [...$("play-current").querySelectorAll("button")].find((b) => textOf(b) === "View progress").click();
    await waitFor("Activity", () => onScreen($("activity")), 20000);
    await waitFor("the public game listed", () => /Listed in Live Games/.test(textOf($("activity-body"))), 120000)
      .catch((err) => { throw new Error(`${err.message}; Activity said: ${squash(textOf($("activity-body"))).slice(0, 400)}`); });
    const current = () => $("activity-body").querySelector(".activity-run");
    checkHelp("in Activity while the public game is listed", current());
    await snap("activity-public-help");

    // --- the next launch is Private -------------------------------------------
    record(
      "after a launch, Who can see it is Private again",
      select.value === "private" && $("play-listing-help").hidden,
      `visibility=${select.value}`
    );
    record(
      "and the summary says so before anything else is pressed",
      textOf($("play-final-summary")).includes("Only you"),
      squash(textOf($("play-final-summary"))).slice(-120)
    );

    // Stop the game; its listing ends, and so does the help.
    const runs = (await api("/api/v1/play/runs")).items || [];
    const first = runs.find((run) => run.launch?.job_id);
    if (!first) throw new Error("no launched run to stop");
    await api(`/api/v1/jobs/${encodeURIComponent(first.launch.job_id)}/cancel`, { method: "POST" });
    await waitFor("the listing to end", () => /listing has ended/.test(textOf(current())), 60000)
      .catch((err) => { throw new Error(`${err.message}; Activity said: ${squash(textOf($("activity-body"))).slice(0, 400)}`); });
    record("an ended listing offers no help link", !helpIn(current()), squash(textOf(current())).slice(0, 160));

    // Second launch, nothing chosen: Private, and no link anywhere.
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await reviewBuild();
    await waitFor("the private listing card", () => {
      const next = listingCard();
      return next && /Players connect to/.test(textOf(next)) && /only you/.test(textOf(next)) ? next : null;
    }, 40000).catch((err) => { throw new Error(`${err.message}; the review said: ${squash(textOf(listingCard())).slice(0, 300)}`); });
    record("the next review asks for Private and offers no help link", !helpIn($("play-review-attention")),
      squash(textOf(listingCard())).slice(0, 160));
    await buildAndRun();
    await waitFor("the second game listed", () => {
      const run = current();
      return run && /\(private\)/.test(textOf(run)) ? run : null;
    }, 120000).catch((err) => { throw new Error(`${err.message}; Activity said: ${squash(textOf($("activity-body"))).slice(0, 400)}`); });
    record("a Private game's Activity card offers no help link", !helpIn(current()), squash(textOf(current())).slice(0, 160));
    await snap("activity-private-no-help");

    const again = ((await api("/api/v1/play/runs")).items || []).find((run) => run.launch?.job_id && run.id !== first.id);
    if (again) {
      await api(`/api/v1/jobs/${encodeURIComponent(again.launch.job_id)}/cancel`, { method: "POST" });
      await waitFor("the second listing to end", () => /listing has ended/.test(textOf(current())), 60000);
    }
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
