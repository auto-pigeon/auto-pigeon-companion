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
 * Where a new report starts. A cold report (no incident) starts with NOTHING
 * chosen — the person must pick a type and an area. A report about an
 * incident starts as the contract says: its incident report type (`bug`) and
 * `suggestBugReportArea`, which reads only the incident's code and subsystem.
 * Both stay changeable.
 */
export function initialClassification(incident) {
  if (!incident) return { reportType: "", area: "" };
  return {
    reportType: bugReportRules.incident_report_type,
    area: suggestBugReportArea(APPLICATION, incident) ?? "",
  };
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
