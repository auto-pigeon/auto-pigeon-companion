// A leak request from the editor, in the real page against the real server
// (NEW_380): it opens ready to Build, with no Review step, and the one Build
// button is always aimed at the request the notice shows.
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

  // Nothing may ever show one request and be aimed at another.
  let aimedWrong = "";
  const watchAim = setInterval(() => {
    const showing = /saved revision (\d+)/.exec(notice());
    const aimed = /revision (\d+)$/.exec(label());
    if (showing && aimed && showing[1] !== aimed[1] && enabled()) aimedWrong = `${notice()} / ${label()}`;
  }, 10);

  document.addEventListener("aucom:leak-form", ({ detail }) => log(`form: ${detail.request_id?.slice(0, 4)} ${detail.state} ${detail.why || ""}`));

  const scenarios = {
    async ready() {
      await waitFor("the notice", () => /saved revision 1/.test(notice()));
      await waitFor("Build to be enabled", enabled);
      record("the page opened on Build", !document.getElementById("area-play").hidden, window.location.hash);
      await sleep(600);
      record("no question stands in front of Build", !document.querySelector("dialog[open]"), document.querySelector("dialog[open] h2")?.textContent || "none");
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
      record("the verdict is shown", Boolean(document.querySelector("#build-leak .leak-verdict")?.textContent), document.querySelector("#build-leak .leak-verdict")?.textContent);
      record("the spent request leaves no Build behind it", !enabled() && !label(), label());
    },

    // A's revision is in the air when B replaces it; A's answer lands afterwards.
    async "held-answer"() {
      const release = hold("/api/v1/leak-test/pending");
      await waitFor("A's notice", () => /saved revision 1/.test(notice()));
      await control("receive-b");
      await waitFor("B's notice", () => /saved revision 2/.test(notice()));
      record("nothing was aimed at A", !enabled(), label());
      release();
      await waitFor("Build for B", enabled);
      record("the form is B's", /revision 2$/.test(label()), label());
      button().click();
      await finished();
      const press = presses.at(-1);
      record("the press is B's", presses.length === 1 && press.leak_request_id === "b".repeat(32) && press.inputs.source_map.includes("-rev-2"), JSON.stringify(press));
      record("one build", await count() === 1, `${await count()}`);
    },

    // A is ready to Build when B arrives.
    async "replaced-when-ready"() {
      await waitFor("Build for A", enabled);
      await control("receive-b");
      await waitFor("B's notice", () => /saved revision 2/.test(notice()));
      await waitFor("the form to be B's and checked", () => /revision 2$/.test(label()) && enabled());
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
