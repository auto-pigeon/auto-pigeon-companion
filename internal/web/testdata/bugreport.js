// Report a bug — type, area and labels (NEW_247H), in AUP's corrected two
// windows (NEW_253), driven inside the real page.
//
// Loaded by internal/web/bugreport_browser_test.go after the application's own
// scripts. It asserts on what the DOM says — the controls, their labels and
// hints, the titles, the review's three labels and the prefilled link, the
// footers' order and placement, the consent gate and what was POSTed — so it
// fails when the dialog draws the wrong thing even if every route answered.
//
// The server route is exercised against a stand-in: the test server has no
// Auto-Pigeon backend, so for the send journey the driver answers
// GET /api/v1/bug-reports/status as "available", says the person is signed
// in, and answers the POST itself — counting every POST, so "Close never
// sends" and "Send sends exactly once" are observations, not assumptions.

"use strict";

(async () => {
  const steps = [];
  const realFetch = window.fetch.bind(window);
  const log = (text) => {
    realFetch("/journey/log", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: String(text) }),
    }).catch(() => {});
  };
  const record = (step, ok, detail) => {
    steps.push({ step, ok: Boolean(ok), detail: String(detail || "") });
    log(`${ok ? "ok  " : "FAIL"} ${step} — ${detail || ""}`);
  };
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  async function waitFor(what, predicate, timeout = 20000) {
    const deadline = Date.now() + timeout;
    for (;;) {
      let value;
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
  const labelOf = (id) => document.querySelector(`label[for="${id}"]`)?.textContent.trim();
  const choose = (select, value) => {
    select.value = value;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  };
  const type = (id, value) => {
    const input = $(id);
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  };
  const title = () => $("bug-title").textContent.trim();
  const review = () => $("bug-review");
  const labels = () => [...($("bug-labels")?.children || [])].map((li) => li.textContent);
  const prefilled = () => {
    const link = $("bug-open-issue");
    return link && link.href && link.href.startsWith("https://github.com/") ? new URL(link.href) : null;
  };
  const terms = () => [...$("bug-body").querySelectorAll("dt")].map((dt) => dt.textContent);
  // a precedes b in the document (and so in the keyboard order).
  const before = (a, b) => Boolean(a && b && (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING));
  const inOrder = (...nodes) => nodes.every((node, i) => i === 0 || before(nodes[i - 1], node));

  // The stand-in for the server route (see the header).
  let posts = [];
  let stubbed = false;
  window.fetch = async (input, init = {}) => {
    const url = typeof input === "string" ? input : input.url;
    const method = (init.method || "GET").toUpperCase();
    if (url.startsWith("/api/v1/bug-reports") && url.split("?")[0] === "/api/v1/bug-reports" && method === "POST") {
      posts.push(JSON.parse(init.body || "{}"));
      if (stubbed) {
        return new Response(JSON.stringify({ number: 7, url: "https://github.com/auto-pigeon/bug-reports/issues/7" }),
          { status: 201, headers: { "Content-Type": "application/json" } });
      }
    }
    if (stubbed && url.split("?")[0] === "/api/v1/bug-reports/status") {
      return new Response(JSON.stringify({ server_route: "available" }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    const response = await realFetch(input, init);
    if (stubbed && url.split("?")[0] === "/api/v1/status" && response.ok) {
      const body = await response.json();
      return new Response(JSON.stringify({ ...body, authenticated: true }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    return response;
  };
  const signIn = () => {
    stubbed = true;
    window.AUCOM.status = { ...(window.AUCOM.status || {}), authenticated: true };
  };
  const signOut = () => {
    stubbed = false;
    window.AUCOM.status = { ...(window.AUCOM.status || {}), authenticated: false };
  };

  let error = "";
  try {
    await waitFor("the page's bug-report module", () => window.AUCOM && window.AUCOM.reportBug);
    const token = document.querySelector('meta[name="aucom-api-token"]').content;
    const raised = await (await realFetch("/api/v1/bug-reports/incidents", { headers: { Accept: "application/json", "X-AUCOM-Token": token } })).json();
    const jobIncident = raised.incidents.find((entry) => entry.code === "aucom.job_failed");
    const readiness = raised.incidents.find((entry) => entry.code === "aucom.readiness_failed");
    record("the Companion offers the incidents it raised", jobIncident && readiness, raised.incidents.map((entry) => entry.code).join(", "));

    // Other modals the page may have opened on its own (no server chosen) are closed first.
    for (const open of document.querySelectorAll("dialog[open]")) open.close();

    // --- window 1, a cold report, from the footer ----------------------------
    $("bug-report-open").click();
    await waitFor("the compose step", () => $("bug-type-bug") && $("bug-dialog").open);
    record("cold: the title is Report a bug", title() === "Report a bug", title());
    const radios = [...document.querySelectorAll('input[name="bug-type"]')];
    record("cold: Type is a radio group, Bug preselected, Feature request not",
      radios.map((r) => r.value).join(",") === "bug,feature_request" && $("bug-type-bug").checked && !$("bug-type-feature_request").checked,
      radios.map((r) => `${r.value}:${r.checked}`).join(" "));
    const areas = [...$("bug-area").options].map((option) => option.value);
    record("cold: only the Companion's areas are offered, none chosen",
      areas.join(",") === ",companion,compile_run,import_export,documentation,other" && $("bug-area").value === "", areas.join(","));
    record("cold: Review is refused until the area is chosen, and says so",
      review().disabled && $("bug-review-why").textContent.trim() === "Choose the area this report is about first." && !$("bug-review-why").hidden,
      $("bug-review-why").textContent);
    record("cold: the area is focused first", document.activeElement === $("bug-area"), document.activeElement?.id);
    const fact = $("bug-application");
    record("the application is a fixed fact, not a control",
      fact && fact.textContent.includes("AUCOM") && fact.textContent.includes("Every report from the Companion is filed as AUCOM.") && !fact.querySelector("input, select, button"),
      fact?.textContent);
    record("cold: the incidents are offered as a choice, not chosen", $("bug-incident") && $("bug-incident").value === "",
      [...($("bug-incident")?.options || [])].map((option) => option.textContent).join(" | "));
    record("a bug's field labels and hints by default",
      labelOf("bug-field-steps") === "Steps to reproduce" && labelOf("bug-field-actual") === "Actual result"
        && $("bug-hint-summary").textContent === "One line. Required." && $("bug-hint-steps").textContent === "What you did, in order. Optional.",
      [labelOf("bug-field-steps"), labelOf("bug-field-expected"), labelOf("bug-field-actual"), $("bug-hint-steps").textContent].join(" / "));
    const composeFoot = $("bug-body").querySelector(".bug-foot");
    const composeClose = $("bug-foot-close");
    record("window 1: the order is warning, classification, summary, steps, expected, actual, note, footer",
      inOrder($("bug-warning"), $("bug-incident"), $("bug-classification"), $("bug-field-summary"), $("bug-field-steps"),
        $("bug-field-expected"), $("bug-field-actual"), $("bug-review-why"), $("bug-collected"), composeFoot),
      "");
    record("window 1: the classification band is Application, Type, Area",
      inOrder($("bug-application"), $("bug-type-bug"), $("bug-type-feature_request"), $("bug-area")), "");
    {
      const foot = composeFoot.getBoundingClientRect();
      const close = composeClose.getBoundingClientRect();
      const next = review().getBoundingClientRect();
      record("window 1 footer: Close bottom left, Review report bottom right, in that keyboard order",
        before(composeClose, review()) && Math.abs(close.left - foot.left) < 2 && Math.abs(next.right - foot.right) < 2 && review().textContent === "Review report",
        `foot ${Math.round(foot.left)}–${Math.round(foot.right)}, close ${Math.round(close.left)}, review ${Math.round(next.right)}`);
    }

    $("bug-type-feature_request").click();
    record("a feature request relabels the fields, the hints and the title",
      labelOf("bug-field-summary") === "Summary" && labelOf("bug-field-steps") === "Use case" && labelOf("bug-field-expected") === "Desired result"
        && labelOf("bug-field-actual") === "Current limitation" && $("bug-hint-steps").textContent === "What you are trying to do, and why. Optional."
        && title() === "Request a feature",
      [labelOf("bug-field-summary"), labelOf("bug-field-steps"), labelOf("bug-field-expected"), labelOf("bug-field-actual"), title()].join(" / "));
    record("a type alone is not enough to review", review().disabled, $("bug-review-why").textContent);
    choose($("bug-area"), "documentation");
    record("an area without a summary: Review still refused, and says why",
      review().disabled && $("bug-review-why").textContent === "Write a one-line summary first.", $("bug-review-why").textContent);

    type("bug-field-summary", "Explain the build pipelines");
    type("bug-field-steps", "Reading the manual; my key is ghp_abcdefghijklmnopqrstuvwxyz0123");
    record("a type, an area and a summary enable Review", !review().disabled && $("bug-review-why").textContent === "" && $("bug-review-why").hidden,
      $("bug-review-why").textContent);

    // --- window 2 -------------------------------------------------------------
    signIn();
    review().click();
    await waitFor("the review step", () => $("bug-labels"));
    record("review: the title is Review the feature request", title() === "Review the feature request", title());
    record("review: the three labels the report carries, as chips", labels().join("|") === "AUCOM|Feature request|Area: Documentation", labels().join(" | "));
    record("review: the field list starts with the labels and carries a feature request's headings",
      terms()[0] === "GitHub labels" && terms().includes("Use case") && terms().includes("Current limitation") && !terms().includes("Steps to reproduce")
        && terms().includes("Browser") && terms().includes("Recent activity"),
      terms().join(", "));
    const headings = [...$("bug-body").querySelectorAll("h3")].map((h) => h.textContent);
    record("review: Exact text, then one How to file it section", headings.join("|") === "Exact text|How to file it" && $("bug-text").textContent.includes("Use case"),
      headings.join(" | "));
    {
      const row = [$("bug-download-txt"), $("bug-download-json"), $("bug-open-issue")];
      const middles = row.map((node) => { const r = node.getBoundingClientRect(); return (r.top + r.bottom) / 2; });
      const lefts = row.map((node) => node.getBoundingClientRect().left);
      record("review: Download .txt, Download .json and Open prefilled GitHub issue, in one row, in that order",
        inOrder(...row) && Math.max(...middles) - Math.min(...middles) < 4 && lefts[0] < lefts[1] && lefts[1] < lefts[2]
          && row.map((node) => node.textContent).join("|") === "Download .txt|Download .json|Open prefilled GitHub issue",
        middles.map(Math.round).join(","));
    }
    let url = prefilled();
    record("the prefilled issue carries the same three labels", url && url.searchParams.get("labels") === "AUCOM,Feature request,Area: Documentation",
      url?.searchParams.get("labels"));
    record("the prefilled issue carries the redacted text, not the key", url && !url.searchParams.get("body").includes("ghp_") && url.searchParams.get("body").includes("Use case"),
      url ? `${url.href.length} characters` : "no link");
    await waitFor("the send group", () => !$("bug-send-group").hidden);
    record("review: the one route line says the server can file it", $("bug-route").textContent === "Auto-Pigeon's server can file this report for you, as your signed-in account.",
      $("bug-route").textContent);
    {
      const foot = $("bug-body").querySelector(".bug-foot");
      const close = $("bug-foot-close");
      const edit = $("bug-edit");
      const ack = $("bug-ack");
      const send = $("bug-send");
      const consent = ack.closest("label");
      record("window 2 footer: Close, Edit, the consent tick, Send — in that keyboard order",
        foot.contains(close) && foot.contains(send) && inOrder(close, edit, ack, send) && send.textContent === "Send through Auto-Pigeon" && edit.textContent === "Edit",
        [close, edit, ack, send].map((node) => node.id).join(" → "));
      record("the consent label reads first and names what it gates",
        consent && consent.firstElementChild?.tagName === "SPAN" && consent.lastElementChild === ack
          && consent.textContent.trim() === "I understand this report will be public on GitHub, for anyone to read.",
        consent?.textContent);
      const f = foot.getBoundingClientRect();
      const c = close.getBoundingClientRect();
      const s = send.getBoundingClientRect();
      const a = ack.getBoundingClientRect();
      // As in AUP: the consent sentence ends in its tick hard right, and Send is hard right too —
      // beside the tick when the row has room, otherwise directly under it.
      record("window 2 footer: Close and Edit hard left; the tick and Send together hard right",
        Math.abs(c.left - f.left) < 2 && Math.abs(s.right - f.right) < 2
          && ((a.right <= s.left && s.left - a.right < 40) || (Math.abs(a.right - f.right) < 4 && a.bottom <= s.top + 2 && s.top - a.bottom < 40)),
        `foot ${Math.round(f.left)}–${Math.round(f.right)}, close ${Math.round(c.left)}, tick ${Math.round(a.right)}, send ${Math.round(s.left)}–${Math.round(s.right)}`);
      record("unticked, Send is disabled and says why", send.disabled && send.title === "Confirm that you understand the report is public first.", send.title);

      // A narrow dialog (the window cannot be resized from here, so the
      // dialog is): the pair wraps under Close/Edit and stays hard right.
      const dialog = $("bug-dialog");
      dialog.style.inlineSize = "22rem";
      await sleep(50);
      const nf = foot.getBoundingClientRect();
      const nc = close.getBoundingClientRect();
      const ns = send.getBoundingClientRect();
      const na = ack.getBoundingClientRect();
      record("narrow: the tick and Send wrap under Close/Edit, still hard right, nothing overflows",
        ns.top >= nc.bottom && na.top >= nc.bottom - 1 && Math.abs(ns.right - nf.right) < 2 && Math.abs(nc.left - nf.left) < 2
          && foot.scrollWidth <= foot.clientWidth + 1,
        `foot ${Math.round(nf.left)}–${Math.round(nf.right)}, close bottom ${Math.round(nc.bottom)}, tick top ${Math.round(na.top)}, send ${Math.round(ns.top)} ${Math.round(ns.right)}`);
      dialog.style.inlineSize = "";
    }

    $("bug-edit").click();
    await waitFor("back to compose", () => $("bug-area"));
    record("Edit keeps a deliberate Feature request, the area and the words",
      $("bug-type-feature_request").checked && !$("bug-type-bug").checked && $("bug-area").value === "documentation"
        && $("bug-field-summary").value === "Explain the build pipelines" && title() === "Request a feature",
      `${$("bug-type-feature_request").checked} ${$("bug-area").value} ${title()}`);
    review().click();
    await waitFor("the review step again", () => $("bug-labels"));
    record("…and reviewing again is still a feature request", labels().join("|") === "AUCOM|Feature request|Area: Documentation" && title() === "Review the feature request",
      labels().join(" | "));
    await waitFor("the send group again", () => !$("bug-send-group").hidden);
    $("bug-send").click();
    await sleep(200);
    record("unticked, pressing Send sends nothing", posts.length === 0, `${posts.length} POSTs`);
    $("bug-foot-close").click();
    await sleep(200);
    record("Close on window 2 closes and sends nothing", !$("bug-dialog").open && posts.length === 0, `${posts.length} POSTs`);

    $("bug-report-open").click();
    await waitFor("a fresh compose step", () => $("bug-type-bug") && $("bug-dialog").open);
    record("a new report starts fresh: Bug, no area, no words",
      $("bug-type-bug").checked && $("bug-area").value === "" && $("bug-field-summary").value === "" && title() === "Report a bug", title());
    $("bug-foot-close").click();
    await sleep(200);
    record("Close on window 1 closes and sends nothing", !$("bug-dialog").open && posts.length === 0, `${posts.length} POSTs`);

    // --- the send itself -----------------------------------------------------
    $("bug-report-open").click();
    await waitFor("compose for the send", () => $("bug-type-feature_request") && $("bug-dialog").open);
    $("bug-type-feature_request").click();
    choose($("bug-area"), "other");
    type("bug-field-summary", "Let me pick a theme");
    review().click();
    await waitFor("the review for the send", () => $("bug-labels") && !$("bug-send-group").hidden);
    $("bug-ack").click();
    record("ticked, Send is enabled", !$("bug-send").disabled && !$("bug-send").hasAttribute("title"), "");
    $("bug-send").click();
    $("bug-send").click();
    await waitFor("the send's result", () => $("bug-result").textContent.includes("#7"));
    await sleep(200);
    record("ticked, Send sends exactly once", posts.length === 1, `${posts.length} POSTs`);
    record("what was sent is the reviewed feature request, confirmed",
      posts[0]?.confirm === true && posts[0]?.document?.report_type === "feature_request" && posts[0]?.document?.area === "other"
        && posts[0]?.document?.user?.summary === "Let me pick a theme",
      JSON.stringify({ type: posts[0]?.document?.report_type, area: posts[0]?.document?.area, confirm: posts[0]?.confirm }));
    record("filed: the result says so, Send and Edit are withdrawn",
      $("bug-result").textContent.startsWith("Filed as issue #7.") && $("bug-send-group").hidden && !$("bug-send") && !$("bug-edit") && $("bug-foot-close"),
      $("bug-result").textContent);
    $("bug-foot-close").click();
    signOut();

    // --- a report about a failed job -----------------------------------------
    await window.AUCOM.reportBug({ incident: jobIncident });
    await waitFor("the compose step for the incident", () => $("bug-type-bug") && $("bug-dialog").open);
    record("incident: a bug, preselected", $("bug-type-bug").checked && !$("bug-type-feature_request").checked, "");
    record("incident: the contract's area for a failed job, preselected", $("bug-area").value === "compile_run", $("bug-area").value);
    record("incident: the area says it was suggested and can be changed", $("bug-suggested") && !$("bug-suggested").hidden && $("bug-suggested").textContent.trim() !== "",
      $("bug-suggested")?.textContent);
    record("incident: the one-line note names the incident", $("bug-about") && $("bug-about").textContent.includes("aucom.job_failed"), $("bug-about")?.textContent);
    record("incident: only the summary is still missing", review().disabled && $("bug-review-why").textContent === "Write a one-line summary first.",
      $("bug-review-why").textContent);
    choose($("bug-area"), "companion");
    record("incident: a changed area is no longer called the suggestion", $("bug-suggested").hidden, $("bug-suggested").textContent);
    type("bug-field-summary", "The build stopped");
    review().click();
    await waitFor("the review step for the incident", () => $("bug-labels"));
    record("the corrected area is what the report carries", labels().join("|") === "AUCOM|Bug|Area: Companion", labels().join(" | "));
    record("review: a bug's title, headings and the incident",
      title() === "Review the bug report" && terms().includes("Steps to reproduce") && terms().includes("Incident"), `${title()}: ${terms().join(", ")}`);
    url = prefilled();
    record("the prefilled issue carries the corrected labels", url && url.searchParams.get("labels") === "AUCOM,Bug,Area: Companion", url?.searchParams.get("labels"));
    await waitFor("the route line", () => $("bug-route").textContent !== "Checking whether Auto-Pigeon's server can file reports…");
    record("no server route: the route line says so and no Send is offered",
      $("bug-route").textContent === "Filing through Auto-Pigeon's server is not enabled on this deployment. Download the report or use the prefilled GitHub issue."
        && $("bug-send-group").hidden && !$("bug-ack") && !$("bug-send"),
      $("bug-route").textContent);
    $("bug-edit").click();
    await waitFor("back to compose", () => $("bug-area"));
    record("Edit keeps the choices", $("bug-area").value === "companion" && $("bug-type-bug").checked && $("bug-field-summary").value === "The build stopped",
      $("bug-area").value);

    choose($("bug-incident"), readiness.incident_id);
    await waitFor("the readiness incident", () => $("bug-area") && $("bug-incident").value === readiness.incident_id);
    record("choosing the readiness failure starts where the contract says", $("bug-area").value === "companion" && $("bug-type-bug").checked, $("bug-area").value);
    choose($("bug-incident"), "");
    await waitFor("a cold report again", () => $("bug-incident").value === "");
    record("choosing nothing in particular makes it cold: a Bug, no area, Review refused",
      $("bug-area").value === "" && $("bug-type-bug").checked && review().disabled, $("bug-review-why").textContent);
    $("bug-dialog").close();
    record("nothing was sent outside the one ticked Send", posts.length === 1, `${posts.length} POSTs`);
  } catch (err) {
    error = String(err && err.stack ? err.stack : err);
  }
  window.fetch = realFetch;
  await realFetch("/journey/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error }),
  });
})();
