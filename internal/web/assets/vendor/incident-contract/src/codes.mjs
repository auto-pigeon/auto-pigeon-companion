// The canonical incident taxonomy, read from schema/incident-codes.json.

import { codesDocument } from "./rules.mjs";

/** Every canonical code, in the order the contract file lists them. */
export const INCIDENT_CODES = Object.freeze(
  codesDocument.codes.map((entry) => Object.freeze({ ...entry })),
);

const byCode = new Map(INCIDENT_CODES.map((entry) => [entry.code, entry]));

/** Is this string a code the contract knows about? */
export function isIncidentCode(code) {
  return typeof code === "string" && byCode.has(code);
}

/**
 * The contract entry for a code, or `undefined`.
 *
 * Returns rather than throws. An unknown code is a real thing that happens — a component built
 * against a newer contract reporting into an older reader — and a lookup that threw would turn a
 * telemetry record into an outage in the code reading it.
 */
export function incidentCode(code) {
  return byCode.get(code);
}

/** Every code a given component is the canonical reporter for. */
export function codesForComponent(component) {
  return INCIDENT_CODES.filter((entry) => entry.component === component);
}
