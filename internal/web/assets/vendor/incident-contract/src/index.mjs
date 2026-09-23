// @auto-pigeon/incident-contract — the shared incident envelope, taxonomy, correlation-id
// convention and central redaction.
//
// Nothing here starts, connects to, or talks to anything. It is a contract plus the reference
// implementation of the rules that contract needs: an SDK choice, a DSN and a transport belong to
// the component doing the reporting.

export { SCHEMA_VERSION, BUG_REPORT_URL, envelopeSchema, codesDocument, redactionDocument } from "./rules.mjs";

export { INCIDENT_CODES, isIncidentCode, incidentCode, codesForComponent } from "./codes.mjs";

export {
  CORRELATION_HEADER,
  CORRELATION_HEADER_LOWER,
  CORRELATION_FIELD,
  CORRELATION_TAG,
  isCorrelationId,
  newCorrelationId,
  correlationIdFromHeaders,
  correlationHeaders,
} from "./correlation.mjs";

export { redactText, redactValue, redactIncident, isDroppedKey, isDeniedKey } from "./redact.mjs";

export { validateIncident } from "./validate.mjs";

export {
  newIncidentId,
  newTransportEventId,
  formatOccurredAt,
  createIncident,
  toSentryEvent,
  toDiagnosticText,
} from "./envelope.mjs";

export {
  BUG_REPORT_SCHEMA,
  BUG_REPORT_REPOSITORY,
  BUG_REPORT_LIMITS,
  bugReportRules,
  bugReportSchema,
  sanitizeReportText,
  coarseBrowser,
  coarseOs,
  buildBugReport,
  canonicalReportJson,
  reportJsonDownload,
  validateBugReport,
  renderReportText,
  renderIssue,
  prefilledIssueUrl,
  reportDownloadNames,
} from "./bugReport.mjs";

export { stackFrames, exceptionType } from "./frames.mjs";
