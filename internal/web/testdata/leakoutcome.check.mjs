// NEW_508A: what a finished leak test established, lifted out of the shipped
// build.js and run on the five kinds of run. The headline and the sentence
// both read this one function; a process that exited 0 is not a verdict.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/build.js", import.meta.url), "utf8");
const start = source.indexOf("  function leakOutcome(manifest, leak) {");
const end = source.indexOf("  // --- end of the leak outcome");
assert.ok(start > 0 && end > start, "build.js no longer defines leakOutcome where this check looks");
const t = (text) => text;
const { leakOutcome, leakHeadline } = new Function("t", source.slice(start, end) + "\nreturn { leakOutcome, leakHeadline };")(t);

const compile = (state, extra = {}) => ({ id: "compile", state, ...extra });
const file = (name, extra = {}) => ({ name, path: "/builds/x/" + name, ...extra });
const q1 = { compile_step: "compile", pointfile_output: "pts", portal_output: "prt" };
const read = (outcome, evidence = []) => ({ compile_step: "compile", pointfile_output: "lin", diagnostic: { outcome, evidence } });

const cases = [
  // A valid point file: a leak, whatever the process or the step did.
  ["a leak route was written (Quake 1)", { steps: [compile("failed")], outputs: [file("pts")] }, q1, "leak", "Leak found"],
  ["a leak route, exit 0 and a succeeded step", { steps: [compile("succeeded")], outputs: [file("pts"), file("prt")] }, q1, "leak", "Leak found"],
  ["the adapter read a leak (Quake III: exit 0, failed step)", { steps: [compile("failed")], outputs: [file("lin")] }, read("leak", ["route"]), "leak", "Leak found"],
  // A qualified no-leak.
  ["the adapter's no_leak", { steps: [compile("succeeded")], outputs: [] }, read("no_leak"), "sealed", "No leak found"],
  ["qbsp's portal file and no route (Quake 1)", { steps: [compile("succeeded")], outputs: [file("prt"), file("pts", { missing: true })] }, q1, "sealed", "No leak found"],
  // Nothing to flood from: not tested, never "sealed".
  ["no interior", { steps: [compile("succeeded")], outputs: [] }, read("no_interior"), "unknown", "Couldn’t determine whether the map leaks"],
  // The defect: a successful process with no verdict, no route and no portal file.
  ["a succeeded compile with no evidence at all", { steps: [compile("succeeded")], outputs: [] }, { compile_step: "compile" }, "unknown", "Couldn’t determine whether the map leaks"],
  ["a succeeded compile whose portal file is missing", { steps: [compile("succeeded")], outputs: [file("prt", { missing: true })] }, q1, "unknown", "Couldn’t determine whether the map leaks"],
  ["a pipeline that declares no portal file", { steps: [compile("succeeded")], outputs: [file("bsp")] }, { compile_step: "compile", pointfile_output: "pts" }, "unknown", "Couldn’t determine whether the map leaks"],
  ["a result that could not be read", { steps: [compile("succeeded")], outputs: [] }, { compile_step: "compile", unreadable: "the log is gone" }, "unknown", "Couldn’t determine whether the map leaks"],
  // Seen live (NEW_508B): unreadable AND the compile never ran or failed is a
  // test that could not finish; unreadable after a finished compile is not.
  ["nothing to read because the build was refused before compiling", { steps: [compile("", { skipped: true })], outputs: [], error: "content refused" }, { compile_step: "compile", unreadable: "the build has no pinned AUB map source" }, "failed", "Leak test couldn’t finish"],
  ["nothing to read and the compile failed", { steps: [compile("failed")], outputs: [] }, { compile_step: "compile", unreadable: "the log is gone" }, "failed", "Leak test couldn’t finish"],
  ["a finished compile of a local file: evidence nobody is qualified to read", { steps: [compile("succeeded")], outputs: [file("prt")] }, { compile_step: "compile", pointfile_output: "pts", unreadable: "the build has no pinned AUB map source" }, "unknown", "Couldn’t determine whether the map leaks"],
  // A genuine failure.
  ["the compile failed and wrote nothing", { steps: [compile("failed")], outputs: [] }, q1, "failed", "Leak test couldn’t finish"],
  ["the compile never ran", { steps: [compile("", { skipped: true })], outputs: [], error: "the converter refused the map" }, q1, "failed", "Leak test couldn’t finish"],
  ["no compile step at all", { steps: [], outputs: [] }, q1, "failed", "Leak test couldn’t finish"],
  ["the adapter says the run was cut short", { steps: [compile("cancelled")], outputs: [] }, read("incomplete", ["cancelled"]), "failed", "Leak test couldn’t finish"],
];
for (const [name, manifest, leak, kind, title] of cases) {
  assert.equal(leakOutcome(manifest, leak), kind, name);
  assert.deepEqual(leakHeadline(manifest, leak), { title, kind }, name + " (headline)");
}
console.log(`leakoutcome.check.mjs: ${cases.length} runs, one outcome each`);
