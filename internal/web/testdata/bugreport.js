// Report a bug — type, area and labels (NEW_247H), driven inside the real page.
//
// Loaded by internal/web/bugreport_browser_test.go after the application's own
// scripts. It asserts on what the DOM says — the controls, their labels, the
// review's three labels and the prefilled link — so it fails when the dialog
// draws the wrong thing even if every route answered.

"use strict";

(async () => {
  const steps = [];
  const log = (text) => {
    fetch("/journey/log", {
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
  const review = () => $("bug-body").querySelector('button[type="submit"]');
  const labels = () => [...($("bug-labels")?.children || [])].map((li) => li.textContent);
  const prefilled = () => {
    const link = [...$("bug-body").querySelectorAll("a.button-link")].find((a) => a.href.startsWith("https://github.com/"));
    return link ? new URL(link.href) : null;
  };
  const terms = () => [...$("bug-body").querySelectorAll("dt")].map((dt) => dt.textContent);

  let error = "";
  try {
    await waitFor("the page's bug-report module", () => window.AUCOM && window.AUCOM.reportBug);
    const token = document.querySelector('meta[name="aucom-api-token"]').content;
    const raised = await (await fetch("/api/v1/bug-reports/incidents", { headers: { Accept: "application/json", "X-AUCOM-Token": token } })).json();
    const jobIncident = raised.incidents.find((entry) => entry.code === "aucom.job_failed");
    const readiness = raised.incidents.find((entry) => entry.code === "aucom.readiness_failed");
    record("the Companion offers the incidents it raised", jobIncident && readiness, raised.incidents.map((entry) => entry.code).join(", "));

    // Other modals the page may have opened on its own (no server chosen) are closed first.
    for (const open of document.querySelectorAll("dialog[open]")) open.close();

    // --- a cold report, from the footer -------------------------------------
    $("bug-report-open").click();
    await waitFor("the compose step", () => $("bug-type-bug"));
    const radios = [...document.querySelectorAll('input[name="bug-type"]')];
    record("cold: the two types are offered, neither chosen", radios.map((r) => r.value).join(",") === "bug,feature_request" && radios.every((r) => !r.checked),
      radios.map((r) => `${r.value}:${r.checked}`).join(" "));
    const areas = [...$("bug-area").options].map((option) => option.value);
    record("cold: only the Companion's areas are offered, none chosen",
      areas.join(",") === ",companion,compile_run,import_export,documentation,other" && $("bug-area").value === "", areas.join(","));
    record("cold: Review is refused until both are chosen, and says why", review().disabled && $("bug-review-why").textContent.trim() !== "",
      $("bug-review-why").textContent);
    const fact = $("bug-body").querySelector(".bug-fact");
    record("the application is a fixed fact, not a control",
      fact && fact.textContent.includes("AUCOM") && !fact.querySelector("input, select, button"), fact?.textContent);
    record("cold: the incidents are offered as a choice, not chosen", $("bug-incident") && $("bug-incident").value === "",
      [...($("bug-incident")?.options || [])].map((option) => option.textContent).join(" | "));
    record("a bug's field labels by default", labelOf("bug-field-steps") === "Steps to reproduce" && labelOf("bug-field-actual") === "Actual result",
      [labelOf("bug-field-steps"), labelOf("bug-field-expected"), labelOf("bug-field-actual")].join(" / "));

    $("bug-type-feature_request").click();
    record("a feature request relabels the fields",
      labelOf("bug-field-summary") === "Summary" && labelOf("bug-field-steps") === "Use case" && labelOf("bug-field-expected") === "Desired result" && labelOf("bug-field-actual") === "Current limitation",
      [labelOf("bug-field-summary"), labelOf("bug-field-steps"), labelOf("bug-field-expected"), labelOf("bug-field-actual")].join(" / "));
    record("a type alone is not enough to review", review().disabled, $("bug-review-why").textContent);
    choose($("bug-area"), "documentation");
    record("a type and an area enable Review", !review().disabled && $("bug-review-why").textContent === "", $("bug-review-why").textContent);

    type("bug-field-summary", "Explain the build pipelines");
    type("bug-field-steps", "Reading the manual; my key is ghp_abcdefghijklmnopqrstuvwxyz0123");
    review().click();
    await waitFor("the review step", () => $("bug-labels"));
    record("review: the three labels the report carries", labels().join("|") === "AUCOM|Feature request|Area: Documentation", labels().join(" | "));
    record("review: a feature request's headings", terms().includes("Use case") && terms().includes("Current limitation") && !terms().includes("Steps to reproduce"),
      terms().join(", "));
    let url = prefilled();
    record("the prefilled issue carries the same three labels", url && url.searchParams.get("labels") === "AUCOM,Feature request,Area: Documentation",
      url?.searchParams.get("labels"));
    record("the prefilled issue carries the redacted text, not the key", url && !url.searchParams.get("body").includes("ghp_") && url.searchParams.get("body").includes("Use case"),
      url ? `${url.href.length} characters` : "no link");
    $("bug-dialog").close();

    // --- a report about a failed job -----------------------------------------
    await window.AUCOM.reportBug({ incident: jobIncident });
    await waitFor("the compose step for the incident", () => $("bug-type-bug") && $("bug-dialog").open);
    record("incident: a bug, preselected", $("bug-type-bug").checked && !$("bug-type-feature_request").checked, "");
    record("incident: the contract's area for a failed job, preselected", $("bug-area").value === "compile_run", $("bug-area").value);
    record("incident: the suggestion says it can be changed", $("bug-suggested") && $("bug-suggested").textContent.trim() !== "", $("bug-suggested")?.textContent);
    record("incident: Review is available at once", !review().disabled, "");
    choose($("bug-area"), "companion");
    type("bug-field-summary", "The build stopped");
    review().click();
    await waitFor("the review step for the incident", () => $("bug-labels"));
    record("the corrected area is what the report carries", labels().join("|") === "AUCOM|Bug|Area: Companion", labels().join(" | "));
    record("review: a bug's headings and the incident", terms().includes("Steps to reproduce") && terms().includes("Incident"), terms().join(", "));
    url = prefilled();
    record("the prefilled issue carries the corrected labels", url && url.searchParams.get("labels") === "AUCOM,Bug,Area: Companion", url?.searchParams.get("labels"));
    const edit = [...$("bug-body").querySelectorAll("button")].find((button) => button.textContent === "Edit");
    edit.click();
    await waitFor("back to compose", () => $("bug-area"));
    record("Edit keeps the choices", $("bug-area").value === "companion" && $("bug-type-bug").checked && $("bug-field-summary").value === "The build stopped",
      $("bug-area").value);

    choose($("bug-incident"), readiness.incident_id);
    await waitFor("the readiness incident", () => $("bug-area") && $("bug-incident").value === readiness.incident_id);
    record("choosing the readiness failure starts where the contract says", $("bug-area").value === "companion" && $("bug-type-bug").checked, $("bug-area").value);
    choose($("bug-incident"), "");
    await waitFor("a cold report again", () => $("bug-incident").value === "");
    record("choosing nothing in particular makes it cold: nothing chosen, Review refused",
      $("bug-area").value === "" && !$("bug-type-bug").checked && review().disabled, $("bug-review-why").textContent);
    $("bug-dialog").close();
  } catch (err) {
    error = String(err && err.stack ? err.stack : err);
  }
  await fetch("/journey/report", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ steps, error }),
  });
})();
