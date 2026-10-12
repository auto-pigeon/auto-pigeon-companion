// The build panel's own arithmetic, run without a page: how a span is said, how
// a run of near-identical output lines is folded, and how the findings of every
// stage become one list. The functions are lifted out of build.js itself.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/build.js", import.meta.url), "utf8");
const start = source.indexOf("function duration(ms)");
const end = source.indexOf("function renderProblems(manifest)");
const sticky = source.indexOf("// What somebody opened or closed stays that way.");
const folds = source.indexOf("// foldRepeats shortens a run");
assert.ok(start > 0 && sticky > start && folds > sticky && end > folds, "build.js no longer defines the panel's helpers where this check looks");
const t = (text, slots = {}) => text.replace(/\{([a-zA-Z_.]+)\}/g, (_, key) => String(slots[key]));
// The <details> memory between the two is the page's; everything else is arithmetic.
const body = source.slice(start, sticky) + source.slice(folds, end);
const { duration, foldRepeats, problemGroups } = new Function("t", body + "\nreturn { duration, foldRepeats, problemGroups };")(t);

// A span is said the way it is read: the lighting pass that printed "708399 ms".
assert.equal(duration(640), "640 ms");
assert.equal(duration(9676), "9.7 s");
assert.equal(duration(15561), "16 s");
assert.equal(duration(708399), "11 min 48 s");
assert.equal(duration(3_712_000), "1 h 1 min");
assert.equal(duration(NaN), "");

// Forty warnings that differ only in their coordinates are two lines and a count; nothing else moves.
const lights = Array.from({ length: 40 }, (_, i) => `WARNING: light at (${2000 + i * 8} -1392 -${56 + i}) has missing target`);
const log = ["--- SetupLights ---", ...lights, "       52 point lights", "       56 spotlights", "--- TraceGrid ---", "0...1...2...3"].join("\n");
const folded = foldRepeats(log);
assert.equal(folded.hidden, 38);
assert.deepEqual(folded.text.split("\n"), ["--- SetupLights ---", lights[0], lights[1], "    … 38 more lines like the two above",
  "       52 point lights", "       56 spotlights", "--- TraceGrid ---", "0...1...2...3"]);
// Three alike are left alone, and so are blank lines, however many.
assert.equal(foldRepeats(lights.slice(0, 3).join("\n")).hidden, 0);
assert.equal(foldRepeats("a\n\n\n\n\n\nb").text, "a\n\n\n\n\n\nb");
// Different sentences are never folded together, even with numbers in them.
assert.equal(foldRepeats("writing 1 file\nreading 2 files\nwriting 3 files\nreading 4 files").hidden, 0);

// Every stage's errors and warnings, the same finding once with its count; errors first, the fatal one first of all; info is not a problem.
const manifest = { steps: [
  { id: "compile", title: "Compile the map", diagnostics: [
    { severity: "info", message: "A shader script was read." },
    { severity: "warning", class: "shader_image_missing", message: "A shader has no image.", raw: "WARNING: Couldn't find image for shader textures/NULL", hint: "Check the base game data." },
    { severity: "warning", class: "shader_image_missing", message: "A shader has no image.", raw: "WARNING: Couldn't find image for shader textures/x" },
    { severity: "warning", class: "shader_image_missing", message: "A shader has no image.", raw: "WARNING: Couldn't find image for shader textures/NULL" } ] },
  { id: "light", title: "Compute lighting", diagnostics: [
    { severity: "warning", class: "shader_image_missing", message: "A shader has no image.", raw: "WARNING: Couldn't find image for shader textures/x" },
    { severity: "error", class: "leak", message: "The map leaks.", raw: "**** leaked ****", fatal: true },
    { severity: "error", class: "model_missing", message: "A model is missing." }, { severity: "error", class: "model_missing", message: "A model is missing." } ] } ] };
const groups = problemGroups(manifest);
assert.deepEqual(groups.map((g) => [g.severity, g.message, g.count, g.fatal, g.stages]), [
  ["error", "The map leaks.", 1, true, ["Compute lighting"]],
  ["error", "A model is missing.", 2, false, ["Compute lighting"]],
  ["warning", "A shader has no image.", 4, false, ["Compile the map", "Compute lighting"]],
]);
assert.deepEqual(groups[2].raws, ["WARNING: Couldn't find image for shader textures/NULL", "WARNING: Couldn't find image for shader textures/x"]);
assert.equal(groups[2].hint, "Check the base game data.");
assert.deepEqual(problemGroups({ steps: [{ id: "a", diagnostics: [{ severity: "info", message: "x" }] }, { id: "b" }] }), []);
console.log("build panel: spans, folded output and grouped findings are as the page shows them");
