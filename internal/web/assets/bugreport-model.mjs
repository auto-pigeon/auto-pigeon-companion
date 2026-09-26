// Report a bug — what the dialog decides, with no DOM (NEW_247H).
//
// Every classification rule is the shared incident contract's, vendored byte
// for byte under vendor/incident-contract: which report types exist, which
// areas the Companion offers, which area an incident starts in, the headings a
// type renders and the three GitHub labels a report carries. This file only
// says which application is asking — AUCOM, always — and turns the person's
// choices into the one document. It keeps no table of its own: a label, an
// area or a heading written here would be a second mapping that could drift
// from the one AUB and the prefilled URL use.
//
// Kept apart from bugreport.mjs so the rules can be exercised by node in a
// Go test (bugreport_model_test.go) without a browser.

import {
  BUG_REPORT_TYPES,
  bugReportAreasFor,
  bugReportHeadings,
  bugReportLabels,
  bugReportRules,
  buildBugReport,
  suggestBugReportArea,
} from "./vendor/incident-contract/src/index.mjs";

/** The reporting application: a fixed fact of the Companion, never a choice. */
export const APPLICATION = "AUCOM";

/**
 * The report types, in the contract's control order, each with the English
 * name the page translates (the contract's own label: "Bug", "Feature request").
 */
export function reportTypeChoices() {
  return BUG_REPORT_TYPES.map((id) => ({ id, name: bugReportRules.report_types[id].label }));
}

/**
 * The areas the Companion offers, in the contract's control order, each with
 * the English name the page translates: the contract's label without its
 * "Area: " prefix ("Compile / Run" for `Area: Compile / Run`).
 */
export function areaChoices() {
  return bugReportAreasFor(APPLICATION).map((id) => ({ id, name: bugReportRules.areas[id].replace(/^Area: /, "") }));
}

/**
 * The type a fresh report starts as (NEW_253): a Bug, as in AUP. The person
 * can still choose Feature request, and that choice is what the report
 * carries; the AREA is never preselected for a cold report.
 */
export const DEFAULT_REPORT_TYPE = "bug";

/**
 * Where a new report starts. A cold report (no incident) starts as a Bug with
 * NO area — the person must pick one before Review. A report about an
 * incident starts as the contract says: its incident report type (`bug`) and
 * `suggestBugReportArea`, which reads only the incident's code and subsystem.
 * Both stay changeable.
 */
export function initialClassification(incident) {
  if (!incident) return { reportType: BUG_REPORT_TYPES.includes(DEFAULT_REPORT_TYPE) ? DEFAULT_REPORT_TYPE : "", area: "" };
  return {
    reportType: bugReportRules.incident_report_type,
    area: suggestBugReportArea(APPLICATION, incident) ?? "",
  };
}

/**
 * The area the contract suggests for an incident, or "" for a cold report —
 * so the form says "suggested" only while the area still is the suggestion.
 */
export function suggestedArea(incident) {
  return incident ? (suggestBugReportArea(APPLICATION, incident) ?? "") : "";
}

/** The four field labels for a type — the contract's headings; a bug's until a type is chosen. */
export function fieldLabels(reportType) {
  return bugReportHeadings(reportType || "bug");
}

/**
 * What still has to be chosen before Review: `[]` when nothing, else some of
 * `"report_type"`, `"area"` — never a guess in their place.
 */
export function missingChoices({ reportType, area }) {
  const missing = [];
  if (!reportType || !BUG_REPORT_TYPES.includes(reportType)) missing.push("report_type");
  if (!area || !bugReportAreasFor(APPLICATION).includes(area)) missing.push("area");
  return missing;
}

/**
 * The one-line hint under each field, in AUP's words (NEW_253). English: the
 * page translates it. A feature request's hints ask for a use case, a desired
 * result and the current limitation; a bug's (and an untyped report's) ask for
 * the steps, the expected and the actual result.
 */
const FIELD_HINTS = {
  bug: {
    summary: "One line. Required.",
    steps: "What you did, in order. Optional.",
    expected: "What you expected to happen. Optional.",
    actual: "What happened instead. Optional.",
  },
  feature_request: {
    summary: "One line. Required.",
    steps: "What you are trying to do, and why. Optional.",
    expected: "What you would like Auto-Pigeon to do. Optional.",
    actual: "What Auto-Pigeon does today, or what stops you. Optional.",
  },
};

export function fieldHints(reportType) {
  return { ...(FIELD_HINTS[reportType] || FIELD_HINTS.bug) };
}

/** The dialog's title for a step and a type, in English: AUP's four titles. */
export function dialogTitle(step, reportType) {
  const feature = reportType === "feature_request";
  if (step === "review") return feature ? "Review the feature request" : "Review the bug report";
  return feature ? "Request a feature" : "Report a bug";
}

/**
 * Why Review cannot be pressed yet, in the order the form asks — the
 * classification first, then the summary — as English the page translates,
 * or "" when it can be. The same rule as AUP's `blocked`.
 */
export function reviewBlocked({ reportType, area, summary }) {
  const missing = missingChoices({ reportType, area });
  if (missing.length === 2) return "Choose a type and an area first.";
  if (missing.includes("report_type")) return "Choose whether this is a bug or a feature request first.";
  if (missing.includes("area")) return "Choose the area this report is about first.";
  if (!String(summary || "").trim()) return "Write a one-line summary first.";
  return "";
}

/**
 * What the review's route line says about sending through Auto-Pigeon, and
 * whether the consent tick and Send are offered at all. `route` is what
 * GET /api/v1/bug-reports/status answered ("available", "unavailable",
 * "unknown") or "checking" while it has not; Send is offered only when AUB
 * says the route is configured AND the person is signed in, and never again
 * once the report reached a final outcome.
 */
export function serverRoute({ route, authenticated, final = false }) {
  const state = route === "checking" ? "checking"
    : route === "unavailable" ? "unavailable"
      : route !== "available" ? "unreachable"
        : authenticated ? "available" : "sign_in";
  return { state, sendOffered: state === "available" && !final };
}

/** The route line for each state, in AUP's words. */
export const ROUTE_LINES = {
  checking: "Checking whether Auto-Pigeon's server can file reports…",
  available: "Auto-Pigeon's server can file this report for you, as your signed-in account.",
  sign_in: "Auto-Pigeon's server can file reports, but only for a signed-in account. Sign in to use it, or use the options above.",
  unavailable: "Filing through Auto-Pigeon's server is not enabled on this deployment. Download the report or use the prefilled GitHub issue.",
  unreachable: "Auto-Pigeon's server could not be reached, so it cannot file this report. Download it or use the prefilled GitHub issue.",
};

/**
 * The ONE document, from the person's choices. The component is always
 * AUCOM; the type and area are exactly what the controls hold (an empty one is
 * passed as absent, so the contract refuses it rather than defaulting it).
 * `incident` is an entry from GET /api/v1/bug-reports/incidents, whose fields
 * are the contract's incident fields.
 */
export function buildReport({ reportType, area, fields, incident, reportId, release, environment, client, now }) {
  return buildBugReport({
    component: APPLICATION,
    reportType: reportType || undefined,
    area: area || undefined,
    release,
    environment,
    reportId,
    now,
    incident: incident || undefined,
    user: { ...fields },
    client,
  });
}

/** The three labels a built document will carry: the contract's derivation, nothing else. */
export function reportLabels(document) {
  return bugReportLabels(document);
}
