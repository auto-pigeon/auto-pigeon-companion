// NEW_507A (#21): the Quake II leak sentence, lifted out of the shipped
// build.js. It names the qbsp release that ran and the releases the reading
// was measured on — never the historical 1.4518 baseline on its own.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/build.js", import.meta.url), "utf8");
const start = source.indexOf("  function leakVerdict(manifest, leak) {");
const end = source.indexOf("\n  }\n", start);
assert.ok(start > 0 && end > start, "build.js no longer defines leakVerdict where this check looks");
const t = (text, values) => text.replace(/\{(\w+)\}/g, (whole, name) => (values && name in values ? String(values[name]) : whole));
const { leakVerdict } = new Function("t", source.slice(start, end + 4) + "\nreturn { leakVerdict };")(t);

const measured = ["1.4518", "1.4522", "1.4524", "1.4533", "1.4535", "1.4556", "1.4559"];
const manifest = (state, exit) => ({ steps: [{ id: "compile", state, exit_code: exit }] });
const q2 = (outcome, evidence, extra = {}) => ({ compiler: "ericw-qbsp-q2", compile_step: "compile",
  qualified_releases: measured, diagnostic: { outcome, evidence, route_points: 10 }, ...extra });

// Qualified 1.4559, sealed.
let s = leakVerdict(manifest("succeeded", 0), q2("no_leak", ["fill_completed", "finished", "bsp_written", "bsp_ibsp38"], { compiler_release: "1.4559" }));
assert.match(s, /found no leak/);
assert.match(s, /release 1\.4559 of the Auto-Pigeon build/);
assert.doesNotMatch(s, /1\.4518/);
assert.doesNotMatch(s, /not a release this reading was measured on/);

// Qualified 1.4559, a leak with its route.
s = leakVerdict(manifest("failed", 1), q2("leak", ["occupant_reached", "leak_file_reported", "route"], { compiler_release: "1.4559" }));
assert.match(s, /found a leak .* 10 points/);
assert.match(s, /release 1\.4559/);

// An unmeasured release: no verdict, named, and the measured list given.
s = leakVerdict(manifest("succeeded", 0), q2("incomplete", ["fill_completed", "finished", "version_unqualified"], { compiler_release: "1.4560" }));
assert.match(s, /No verdict/);
assert.match(s, /This qbsp \(1\.4560\) is not a release this reading was measured on/);
assert.match(s, /releases 1\.4518, 1\.4522, 1\.4524, 1\.4533, 1\.4535, 1\.4556, 1\.4559/);
assert.doesNotMatch(s, /Read as measured/);

// A compiler failure on a qualified release is no verdict and claims no measurement.
s = leakVerdict(manifest("failed", 1), q2("incomplete", ["fatal_error"], { compiler_release: "1.4559" }));
assert.match(s, /No verdict.*fatal_error/);
assert.doesNotMatch(s, /Read as measured/);

// A run that named no release.
s = leakVerdict(manifest("succeeded", 0), q2("incomplete", ["version_unqualified"]));
assert.match(s, /did not say which release it is/);

console.log("leakverdictq2.check.mjs: 5 sentences ok");
