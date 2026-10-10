// Changing the tool a pipeline stage uses: a comparison, not a reset.
//
// The stage's Tool choice used to assign the new tool and empty the stage —
// `step.inputs = {}; step.options = {}` — so choosing another compiler that
// does the same job silently destroyed where each input came from and every
// parameter (NEW_323 found it live and had to rebuild a pipeline by hand;
// NEW_323A). A choice is now COMPARED with what the stage holds:
//
//   - an input the new action declares under the same name, taking the kind
//     of file its source produces, keeps its source;
//   - an input the new action declares under ANOTHER name is carried over
//     only when the two actions' own declarations say it is the same thing:
//     exactly one input of that role on each side. The role is the
//     declaration. Position is never used, and two candidates are a question
//     for the person, not a guess;
//   - a parameter the new action declares under the same name keeps its value
//     when that value passes the new declaration (type, range, choices);
//   - the stage's id, title, place in the order and its own arguments are not
//     the tool's and are never touched;
//   - what later stages and the pipeline's results read from this stage is
//     kept when the new action still produces it, and renamed by the same
//     one-role-each-side rule when it produces it under another name.
//
// Everything else is a FINDING: the exact field, its value and why the new
// tool cannot take it. A plan with findings changes nothing until the person
// has read them and chosen; cancelling leaves the stage exactly as it was.
// Nothing discarded is remembered anywhere: choosing the old tool again does
// not bring back a value somebody agreed to drop.
//
// This file decides nothing about validity. The Companion's own
// PipelineProfile.Resolve is the authority when the profile is composed,
// installed and run; this is what keeps a draft from being emptied on the way
// there. It touches no DOM, so testdata/stageswitch.check.mjs runs it as is.

"use strict";

(() => {
  const STDOUT = "stdout"; // every stage's own output text (profile.StepLogOutput)

  const byName = (list, name) => (list || []).find((item) => item.name === name);
  const withRole = (list, role) => (list || []).filter((item) => item.role === role);
  const enumValues = (option) => (option.values || []).map((value) => (typeof value === "string" ? value : value.value));

  // optionProblem mirrors profile.OptionSpec.Check: why `value` is not a value
  // of `option`, or "" when it is.
  function optionProblem(option, value) {
    switch (option.type) {
      case "bool":
        return value === "true" || value === "false" ? "" : `it is a yes/no parameter here, and "${value}" is neither true nor false`;
      case "integer": {
        if (!/^[+-]?\d+$/.test(value)) return `it is a whole number here, and "${value}" is not one`;
        const n = Number(value);
        if (option.minimum != null && n < option.minimum) return `${value} is below its minimum of ${option.minimum} here`;
        if (option.maximum != null && n > option.maximum) return `${value} is above its maximum of ${option.maximum} here`;
        return "";
      }
      case "enum":
        return enumValues(option).includes(value) ? "" : `"${value}" is not one of its choices here (${enumValues(option).join(", ")})`;
      case "text": {
        const max = option.max_length ?? 128;
        if (value.length > max) return `it is longer than the ${max} characters allowed here`;
        if (/^\.+$/.test(value)) return `"${value}" names a folder rather than being a name`;
        return /^[A-Za-z0-9._-]*$/.test(value) ? "" : "a text parameter may contain letters, digits, . _ and - only";
      }
      default:
        return `its type here (${option.type}) is not one this page knows`;
    }
  }

  // plan compares a stage with the action it would be run by.
  //
  //   step        the stage: { id, tool, capability, inputs: {name: source},
  //               options: {name: value} }
  //   from        the action that runs it now, or undefined when nothing
  //               installed provides it (its declarations are then unknown)
  //   to          the action it would be run by: { profileId, capability,
  //               inputs, outputs, options }; undefined for "no tool"
  //   sourceRoles what each source before this stage produces: {ref: role}
  //   readers     who reads this stage's outputs: [{ ref, label }]
  //
  // It returns what would be kept, what would be renamed and what cannot be
  // carried — and changes nothing.
  function plan({ step, from, to, sourceRoles = {}, readers = [] }) {
    const tool = to?.profileId || "";
    const capability = to?.capability || "";
    const result = {
      tool, capability, noop: false,
      inputs: {}, options: {}, outputs: {},
      kept: [], renamed: [], findings: [], needs: [],
    };
    // The same choice again is no change at all. A stage that names no tool
    // and has exactly one provider IS that provider.
    if (capability === (step.capability || "") && (tool === (step.tool || "") || tool === (from?.profileId || ""))) {
      result.noop = true;
      result.inputs = { ...step.inputs };
      result.options = { ...step.options };
      return result;
    }
    const target = to?.title ? `${to.profileName || tool}: ${to.title}` : tool || "no tool";

    // --- inputs -------------------------------------------------------------
    const taken = new Set();
    const bound = Object.entries(step.inputs || {}).filter(([, source]) => source);
    for (const [name] of bound) if (byName(to?.inputs, name)) taken.add(name);
    for (const [name, source] of bound) {
      const field = `input ${name}`;
      const produced = sourceRoles[source];
      const same = byName(to?.inputs, name);
      if (same) {
        // A source this page cannot place (a stage that is gone) is already
        // the draft's own problem, shown where it is; the tool change did not
        // cause it and does not hide it.
        if (produced && same.role && produced !== same.role) {
          result.findings.push({ field, kind: "input_type", value: source,
            reason: `${target} takes a ${same.role} as ${name}, and ${source} is a ${produced}` });
          continue;
        }
        result.inputs[name] = source;
        result.kept.push(`${field} ← ${source}`);
        continue;
      }
      const role = byName(from?.inputs, name)?.role || produced;
      const candidates = role ? withRole(to?.inputs, role).filter((input) => !taken.has(input.name)) : [];
      const declaredOnce = !from || withRole(from.inputs, role).length === 1;
      if (role && declaredOnce && candidates.length === 1 && withRole(to.inputs, role).length === 1) {
        const renamed = candidates[0].name;
        taken.add(renamed);
        result.inputs[renamed] = source;
        result.renamed.push(`${field} is ${renamed} in ${target} (both are the one ${role} input) ← ${source}`);
        continue;
      }
      result.findings.push({ field, kind: "input_removed", value: source,
        reason: candidates.length > 1 || (role && withRole(to?.inputs, role).length > 1)
          ? `${target} has no input named ${name}, and more than one of its inputs takes a ${role} — say which by hand`
          : `${target} has no input named ${name}${role ? ` and none that takes a ${role}` : ""}` });
    }
    for (const input of to?.inputs || []) {
      if (input.required && !result.inputs[input.name]) result.needs.push(input.name);
    }

    // --- parameters ---------------------------------------------------------
    for (const [name, value] of Object.entries(step.options || {})) {
      if (value === "" || value == null) continue;
      const field = `parameter ${name}`;
      const option = byName(to?.options, name);
      if (!option) {
        result.findings.push({ field, kind: "option_removed", value, reason: `${target} has no parameter named ${name}` });
        continue;
      }
      const problem = optionProblem(option, String(value));
      if (problem) {
        const before = byName(from?.options, name);
        result.findings.push({ field, kind: before && before.type !== option.type ? "option_type" : "option_value", value, reason: problem });
        continue;
      }
      result.options[name] = value;
      result.kept.push(`${field} = ${value}`);
    }

    // --- what the rest of the pipeline reads from this stage ----------------
    const prefix = step.id + ".";
    const read = new Map(); // output name → labels of what reads it
    for (const reader of readers) {
      if (!reader.ref?.startsWith(prefix)) continue;
      const output = reader.ref.slice(prefix.length);
      read.set(output, [...(read.get(output) || []), reader.label]);
    }
    for (const [output, labels] of read) {
      if (output === STDOUT && !byName(from?.outputs, STDOUT)) continue; // the stage's own text, whatever runs it
      const field = `output ${output}`;
      const before = byName(from?.outputs, output);
      const same = byName(to?.outputs, output);
      if (same) {
        if (before?.role && same.role && before.role !== same.role) {
          result.findings.push({ field, kind: "output_type", value: labels.join("; "),
            reason: `${target} produces a ${same.role} as ${output}, not a ${before.role}; read by ${labels.join("; ")}` });
        }
        continue;
      }
      const role = before?.role;
      const candidates = role ? withRole(to?.outputs, role) : [];
      if (role && withRole(from.outputs, role).length === 1 && candidates.length === 1) {
        result.outputs[output] = candidates[0].name;
        result.renamed.push(`${field} is ${candidates[0].name} in ${target} (both are the one ${role} output); ${labels.join("; ")} will read it`);
        continue;
      }
      result.findings.push({ field, kind: "output_removed", value: labels.join("; "),
        reason: `${target} does not produce ${output}; read by ${labels.join("; ")}` });
    }
    return result;
  }

  // apply makes the stage what the plan says. `rewrite(oldRef, newRef)` is
  // called for each output the new action produces under another name, so the
  // caller can repoint what reads it. A reader of an output the new action
  // does not produce at all is left reading it: the page shows that reference
  // as not produced, and the Companion refuses it, rather than this emptying
  // somebody else's field.
  function apply(step, result, rewrite = () => {}) {
    if (result.noop) return;
    step.tool = result.tool;
    step.capability = result.capability;
    step.inputs = { ...result.inputs };
    step.options = { ...result.options };
    for (const [before, after] of Object.entries(result.outputs)) rewrite(`${step.id}.${before}`, `${step.id}.${after}`);
  }

  window.AUCOM.stageSwitch = { plan, apply, optionProblem };
})();
