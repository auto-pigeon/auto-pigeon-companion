// A leak request from the editor, in the real page against the real server
// (NEW_380): it opens ready to Build, with no Review step, and the one Build
// button is always aimed at the request whose card says it is in the form.
// NEW_502 (issue #35): a second request joins the first on a card of its own
// and never takes the form from it.
//
// Each scenario is a page of its own. `hold` keeps an answer in the air, which is
// the ordering a person cannot schedule by hand. Every build is counted from the
// server, never from the page.

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

  async function waitFor(what, predicate, timeout = 20000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      const value = predicate();
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}; the notice says: ${notice()}`);
      await sleep(25);
    }
  }

  // Everything the page sends goes through here: builds are remembered, and a
  // path can be held until a test lets it go.
  const pageFetch = window.fetch.bind(window);
  const presses = [];
  const holds = new Map();
  window.fetch = async (input, init) => {
    const path = String(typeof input === "string" ? input : input.url);
    if (path.endsWith("/api/v1/build/runs") && init?.method === "POST") presses.push(JSON.parse(init.body));
    for (const [match, gate] of holds) if (path.includes(match)) await gate.promise;
    return pageFetch(input, init);
  };
  const hold = (match) => {
    let release;
    const promise = new Promise((resolve) => { release = resolve; });
    holds.set(match, { promise });
    return () => { holds.delete(match); release(); };
  };

  const control = (name) => pageFetch(`/journey/${name}`, { method: "POST" }).then((response) => {
    if (!response.ok) throw new Error(`/journey/${name} answered ${response.status}`);
  });
  const notice = () => document.getElementById("leak-request")?.textContent || "";
  const cards = () => [...document.querySelectorAll("#leak-request .leak-request__card")];
  const cardFor = (revision) => cards().find((card) => new RegExp(`saved revision ${revision} `).test(card.textContent));
  // The card of the request in the Build form, chosen by that request's id —
  // the one the page itself put in the form (window.AUCOM.leakRequestInForm,
  // the notice's own record of it). Never by what a card lacks: a card refused
  // for a moved revision has no "Open this request" either, and the first such
  // card was taken for the form's (NEW_624, issue #51).
  const inForm = () => {
    const id = window.AUCOM.leakRequestInForm?.();
    return id ? cards().find((card) => card.dataset.requestId === id) : undefined;
  };
  // The guess inForm replaced, kept only to show it would have been wrong.
  const guessedInForm = () => cards().find((card) => !card.querySelector('button[data-action="open"]') && /saved revision/.test(card.textContent));
  // A card the server refused: an error, and nothing on it but Dismiss.
  const refused = (card) => Boolean(card?.querySelector(".leak-request__state.error")) &&
    [...card.querySelectorAll("button")].every((item) => item.dataset.action === "dismiss");
  const button = () => document.getElementById("build-start");
  const label = () => document.getElementById("build-label")?.value || "";
  const enabled = () => { const b = button(); return b && !b.hidden && !b.disabled; };
  const named = (text) => [...document.querySelectorAll("#leak-request button")].find((item) => item.textContent === text);
  const builds = async () => {
    const { body } = await window.AUCOM.api("/api/v1/build/runs");
    return body.items || body.builds || body || [];
  };
  const count = async () => (await builds()).length;
  const finished = async () => {
    await waitFor("the build to finish", () => document.querySelector("#build-leak .leak-verdict"), 30000);
  };

  // Nothing may ever show one request and be aimed at another. aimMismatch is
  // the check; the watch runs it every 10 ms, and a scenario runs it itself at
  // the moment it is about, since a click in the same turn leaves the watch no
  // turn to look (NEW_624, #51).
  let aimedWrong = "";
  const aimMismatch = () => {
    const showing = /saved revision (\d+)/.exec(inForm()?.textContent || "");
    const aimed = /revision (\d+)$/.exec(label());
    return showing && aimed && showing[1] !== aimed[1] && enabled() ? `${notice()} / ${label()}` : "";
  };
  const checkAim = () => { const wrong = aimMismatch(); if (wrong) aimedWrong = wrong; };
  const watchAim = setInterval(checkAim, 10);

  document.addEventListener("aucom:leak-form", ({ detail }) => log(`form: ${detail.request_id?.slice(0, 4)} ${detail.state} ${detail.why || ""}`));

  const scenarios = {
    async ready() {
      await waitFor("the notice", () => /saved revision 1/.test(notice()));
      await waitFor("Build to be enabled", enabled);
      record("the page opened on Build", !document.getElementById("area-play").hidden, window.location.hash);
      await sleep(600);
      record("no question stands in front of Build", !document.querySelector("dialog[open]"), document.querySelector("dialog[open] h2")?.textContent || "none");
      // NEW_508: the request is a card at the top of Build & Run, beside the
      // map it is about — not a strip above every area.
      record("the request is in Build & Run and takes no room under the header",
        document.getElementById("area-play").contains(document.getElementById("leak-request")) &&
          document.getElementById("leak-request").checkVisibility() &&
          Math.round(document.querySelector(".app-body").getBoundingClientRect().top - document.querySelector(".app-header").getBoundingClientRect().bottom) === 0,
        `visible ${document.getElementById("leak-request").checkVisibility()}`);
      record("Activity counts the request and offers the way back",
        document.getElementById("activity-count").textContent === "1" &&
          Boolean(document.querySelector('#activity-requests button[data-request="leak:' + "a".repeat(32) + '"]')) &&
          Boolean(document.querySelector('#activity-requests button[data-request="leak:' + "a".repeat(32) + ':dismiss"]')),
        `count "${document.getElementById("activity-count").textContent}"`);
      record("there is no Review step", !/review/i.test(notice()) && ![...document.querySelectorAll("#leak-request button")].some((b) => /review/i.test(b.textContent)), notice());
      record("the button says Build", button().textContent === "Build", button().textContent);
      record("it says the pipeline and that nothing runs yet", /Fixture Quake 1 leak test/.test(notice()) && /nothing runs until you press Build/i.test(notice()), notice());
      record("opening it started nothing", await count() === 0 && presses.length === 0, `${await count()} build(s)`);
      button().click();
      button().click();
      await finished();
      record("two clicks are one press", presses.length === 1, `${presses.length} press(es)`);
      record("one press is one build", await count() === 1, `${await count()} build(s)`);
      await waitFor("the notice to go", () => !notice());
      record("the result leads with its headline (NEW_508)",
        ["Leak found", "No leak found", "Couldn’t determine whether the map leaks", "Leak test couldn’t finish"]
          .includes(document.querySelector("#build-leak .leak-headline")?.textContent),
        document.querySelector("#build-leak .leak-headline")?.textContent);
      record("the verdict is shown", Boolean(document.querySelector("#build-leak .leak-verdict")?.textContent), document.querySelector("#build-leak .leak-verdict")?.textContent);
      record("the spent request leaves no Build behind it", !enabled() && !label(), label());
    },

    // A's revision is in the air when B arrives; A's answer lands afterwards,
    // on A's card. B waits on its own card and is built only once opened.
    // A is asked for only once the hold is in place (NEW_624, #51), so its
    // answer is the one given after B's save: refused, its revision moved.
    async "held-answer"() {
      const release = hold("/api/v1/leak-test/pending");
      await control("receive-a");
      await waitFor("A's notice", () => /saved revision 1/.test(notice()));
      await control("receive-b");
      await waitFor("B's card beside A's", () => cardFor(1) && cardFor(2));
      record("nothing was aimed at A", !enabled(), label());
      release();
      await waitFor("B to wait on its own card", () => cardFor(2)?.querySelector('button[data-action="open"]'));
      record("the second link did not take the form", !/revision 2$/.test(label()), label());
      cardFor(2).querySelector('button[data-action="open"]').click();
      await waitFor("Build for B", () => enabled() && /revision 2$/.test(label()));
      record("the form is B's", /revision 2$/.test(label()), label());
      // Issue #51's page: A's card refused for its moved revision, offering only
      // Dismiss, while B is in the form and Build is aimed at B.
      await waitFor("A refused for its moved revision", () => refused(cardFor(1)));
      record("A is refused on its own card while B is in the form", refused(cardFor(1)) && enabled() && /revision 2$/.test(label()), cardFor(1)?.textContent);
      // Which card is the form's, by identity, with A's refused card on screen.
      record("the card in the form is B's, by its request id", inForm() === cardFor(2) && inForm()?.dataset.requestId === "b".repeat(32),
        inForm()?.dataset.requestId || "none");
      record("A's refused card is not taken for the form's", inForm() !== cardFor(1), inForm()?.dataset.requestId || "none");
      record("the old guess would have taken A's card (issue #51)", guessedInForm() === cardFor(1), guessedInForm()?.dataset.requestId || "none");
      checkAim();
      // Negative control: a Build really aimed at a revision the notice is not
      // showing as in the form is still caught. One synchronous turn, so the
      // watch cannot run on the altered label, and the label is put back.
      const field = document.getElementById("build-label");
      const kept = field.value;
      field.value = kept.replace(/revision 2$/, "revision 1");
      const caught = aimMismatch();
      field.value = kept;
      record("a Build aimed at A while the form shows B is caught", Boolean(caught) && field.value === kept, caught || "not caught");
      button().click();
      await finished();
      const press = presses.at(-1);
      record("the press is B's", presses.length === 1 && press.leak_request_id === "b".repeat(32) && press.inputs.source_map.includes("-rev-2"), JSON.stringify(press));
      record("one build", await count() === 1, `${await count()}`);
      await waitFor("B's card to go", () => !cardFor(2));
      record("A is still pending on its own card", Boolean(cardFor(1)), notice());
    },

    // A is ready to Build when B arrives: the form stays A's, B waits; opening
    // B moves the form to B and leaves A pending.
    async "second-when-ready"() {
      await waitFor("Build for A", enabled);
      await control("receive-b");
      await waitFor("B's card", () => cardFor(2)?.querySelector('button[data-action="open"]'));
      await sleep(600);
      record("the open form stays on A", /revision 1$/.test(label()) && enabled(), label());
      record("Activity counts both", document.getElementById("activity-count").textContent === "2",
        `count "${document.getElementById("activity-count").textContent}"`);
      cardFor(2).querySelector('button[data-action="open"]').click();
      await waitFor("the form to be B's and checked", () => /revision 2$/.test(label()) && enabled());
      record("A waits on its own card", Boolean(cardFor(1)?.querySelector('button[data-action="open"]')), cardFor(1)?.textContent);
      button().click();
      await finished();
      record("the press is B's", presses.length === 1 && presses[0].leak_request_id === "b".repeat(32), JSON.stringify(presses));
    },

    // Nothing bound: the notice says what, and setting it up enables Build with
    // no reload, no second request and no Review.
    async "setup-recovery"() {
      await waitFor("the setup notice", () => /needs setup/i.test(notice()));
      record("it names the fix", Boolean(document.querySelector("#leak-request a[href^='#profiles']")), notice());
      record("Build is not offered", !enabled(), "");
      await control("approve");
      await waitFor("Build after setup", enabled, 30000);
      record("nothing started by itself", await count() === 0, `${await count()} build(s)`);
      button().click();
      await finished();
      record("one press, one build", presses.length === 1 && await count() === 1, `${presses.length}`);
    },

    // A save after the click: the press is refused by the server, nothing runs,
    // and the notice says so.
    async "saved-since"() {
      await waitFor("Build", enabled);
      await control("new-save");
      button().click();
      await waitFor("the refusal", () => /cannot be used/.test(notice()));
      record("nothing was built", await count() === 0, `${await count()} build(s)`);
      record("Build is not offered", !enabled(), notice());
    },

    // Another pipeline chosen in the form: that is not the leak test any more.
    async "form-changed"() {
      await waitFor("Build", enabled);
      const select = document.getElementById("build-pipeline");
      const other = [...select.options].find((option) => option.value && option.value !== "aucom.fixture.q1-leak");
      if (!other) throw new Error(`no other pipeline to choose: ${[...select.options].map((option) => option.value)}`);
      other.disabled = false;
      select.value = other.value;
      select.dispatchEvent(new Event("change", { bubbles: true }));
      await waitFor("the notice to say so", () => /no longer builds this leak test/.test(notice()));
      named("Open leak test").click();
      await waitFor("Build for the leak test", () => enabled() && select.value === "aucom.fixture.q1-leak");
      button().click();
      await finished();
      record("the press is the leak test's", presses.length === 1 && presses[0].leak_request_id === "a".repeat(32), JSON.stringify(presses));
    },
  };

  try {
    const settings = await (await pageFetch("/journey/config")).json();
    await scenarios[settings.scenario]();
    record("Build was never aimed at a request the notice was not showing", !aimedWrong, aimedWrong);
  } catch (error) {
    fatal = error.message;
    log(`stopped: ${error.message}`);
  }
  clearInterval(watchAim);
  await pageFetch("/journey/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error: fatal }),
  });
})();
