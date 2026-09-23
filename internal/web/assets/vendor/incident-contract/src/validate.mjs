// On-demand validation of an incident envelope.
//
// ## Why this is not ajv
//
// This package is consumed by a browser bundle, by a node service, and by tooling. Pulling a
// general JSON Schema engine into the editor's bundle to check a fourteen-field object would be a
// large dependency for a small question — and the envelope schema deliberately uses only a handful
// of keywords. So this is a reader for THAT subset, driven by the schema file itself rather than by
// a transcription of it: adding a field to the schema needs no edit here.
//
// The schema remains the authority. `test/validate.test.mjs` runs ajv over the same cases and fails
// if the two disagree, which is the only thing that makes "a small interpreter" safe rather than a
// second opinion. It is the same discipline the workspace applies to its shared cookie rule.
//
// ## AULIBS rule 2.1: validation is on demand, and it RETURNS
//
// Nothing here runs on import, on construction or on a timer, and nothing throws for invalid input.
// A caller asks for a verdict and gets `{ valid, errors }`. An incident is frequently reported from
// a path that is ALREADY failing; a validator that threw would turn a report about a bug into a
// second bug.

import { envelopeSchema } from "./rules.mjs";

function resolveRef(ref) {
  // Only the local `#/$defs/<name>` form the envelope schema uses.
  const name = ref.replace("#/$defs/", "");
  return envelopeSchema.$defs?.[name];
}

function typeOf(value) {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  if (Number.isInteger(value)) return "integer";
  return typeof value;
}

function matchesType(value, expected) {
  if (expected === "integer") return typeof value === "number" && Number.isInteger(value);
  if (expected === "number") return typeof value === "number" && Number.isFinite(value);
  if (expected === "array") return Array.isArray(value);
  if (expected === "object") return value !== null && typeof value === "object" && !Array.isArray(value);
  if (expected === "null") return value === null;
  return typeof value === expected;
}

function check(schema, value, path, errors) {
  if (schema.$ref) {
    const target = resolveRef(schema.$ref);
    if (!target) {
      errors.push({ path, message: `unresolvable $ref ${schema.$ref}` });
      return;
    }
    check(target, value, path, errors);
    return;
  }

  if (schema.const !== undefined && value !== schema.const) {
    errors.push({ path, message: `must be ${JSON.stringify(schema.const)}` });
    return;
  }

  if (schema.enum && !schema.enum.includes(value)) {
    errors.push({ path, message: `must be one of ${schema.enum.join(", ")}` });
    return;
  }

  if (schema.type && !matchesType(value, schema.type)) {
    errors.push({ path, message: `must be ${schema.type}, got ${typeOf(value)}` });
    return;
  }

  if (typeof value === "string") {
    if (schema.minLength !== undefined && value.length < schema.minLength) {
      errors.push({ path, message: `must be at least ${schema.minLength} characters` });
    }
    if (schema.maxLength !== undefined && value.length > schema.maxLength) {
      errors.push({ path, message: `must be at most ${schema.maxLength} characters` });
    }
    if (schema.pattern && !new RegExp(schema.pattern, "u").test(value)) {
      errors.push({ path, message: `must match ${schema.pattern}` });
    }
  }

  if (typeof value === "number") {
    if (schema.minimum !== undefined && value < schema.minimum) {
      errors.push({ path, message: `must be >= ${schema.minimum}` });
    }
    if (schema.maximum !== undefined && value > schema.maximum) {
      errors.push({ path, message: `must be <= ${schema.maximum}` });
    }
  }

  if (matchesType(value, "object")) {
    for (const required of schema.required ?? []) {
      if (!Object.prototype.hasOwnProperty.call(value, required)) {
        errors.push({ path: path ? `${path}.${required}` : required, message: "is required" });
      }
    }
    for (const [key, item] of Object.entries(value)) {
      const childPath = path ? `${path}.${key}` : key;
      const childSchema = schema.properties?.[key];
      if (!childSchema) {
        if (schema.additionalProperties === false) {
          errors.push({ path: childPath, message: "is not a field of this contract" });
        }
        continue;
      }
      check(childSchema, item, childPath, errors);
    }
  }
}

/**
 * Validate an incident envelope against schema/incident-envelope-1.0.schema.json.
 *
 * @returns {{ valid: boolean, errors: Array<{path: string, message: string}> }}
 */
export function validateIncident(incident) {
  const errors = [];
  if (incident === null || typeof incident !== "object" || Array.isArray(incident)) {
    return { valid: false, errors: [{ path: "", message: "an incident must be an object" }] };
  }
  check(envelopeSchema, incident, "", errors);
  return { valid: errors.length === 0, errors };
}
