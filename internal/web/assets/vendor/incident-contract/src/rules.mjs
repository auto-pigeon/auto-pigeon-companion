// The contract data, loaded once.
//
// The JSON files under ../schema are the AUTHORITY, and this module is a reader for them — not a
// second copy. AUB and AUE are Go, the observability tooling in auto-pigeon-tools is Python, and
// the two browser surfaces are TypeScript; a taxonomy or a redaction rule written down in
// JavaScript would have to be written down twice more, and three sincere transcriptions of one
// list is exactly the drift this package exists to remove.
//
// Import attributes (`with { type: "json" }`) are used rather than `fs.readFileSync`, because this
// module has to work in a browser bundle as well as in node. A bundler resolves the JSON at build
// time; node parses it at load time. Neither needs a filesystem.

import codesDocument from "../schema/incident-codes.json" with { type: "json" };
import redactionDocument from "../schema/redaction-rules.json" with { type: "json" };
import envelopeSchema from "../schema/incident-envelope-1.0.schema.json" with { type: "json" };

export { codesDocument, redactionDocument, envelopeSchema };

/** The version of the envelope contract this package implements. */
export const SCHEMA_VERSION = "1.0";

/**
 * The one public place an Auto-Pigeon bug report is filed.
 *
 * It lives in the shared contract rather than in each application, for the reason every address in
 * this workspace lives in one place: a second copy is a second answer, and this one is printed to
 * users. Nothing here opens it, submits to it, or creates an issue — a report is something a person
 * chooses to send after reading what it contains.
 */
export const BUG_REPORT_URL = "https://github.com/auto-pigeon/bug-reports/issues";
