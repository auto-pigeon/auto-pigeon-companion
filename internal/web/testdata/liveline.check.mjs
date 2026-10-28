// NEW_365: the one line the Jobs page and the Activity drawer say about a
// running tool, lifted out of the shipped core.js and run on live readings.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const core = readFileSync(new URL("../assets/core.js", import.meta.url), "utf8");
const start = core.indexOf("function mebibytes(n)");
const end = core.indexOf("// listOf joins names");
const durationStart = core.indexOf("function duration(ms)");
const durationEnd = core.indexOf("function when(value)");
assert.ok(start > 0 && end > start && durationStart > 0, "core.js no longer defines liveLine where this check looks");
const AUCOM = { t: (text, slots = {}) => text.replace(/\{([a-zA-Z_.]+)\}/g, (_, k) => String(slots[k])) };
const { liveLine } = new Function("AUCOM", core.slice(durationStart, durationEnd) + core.slice(start, end) + "\nreturn { liveLine };")(AUCOM);

const now = "2026-10-28T17:00:20Z";
const live = (resources, extra = {}) => ({ now, supervised: true, last_output_at: "2026-10-28T17:00:18Z", resources, ...extra });

// A busy tool: stage, the tool's own phase and percentage, CPU over 100%, RAM.
let line = liveLine(live({ state: "live", cpu_percent: 285.4, rss_bytes: 640 * 2 ** 20, logical_cpus: 8, sampled_at: "2026-10-28T17:00:20Z" },
  { progress: { phase: "Full VIS", percent: 37 } }), { stage: "VIS" });
assert.equal(line.text, "VIS · Full VIS 37% · CPU 285% · RAM 640 MiB · last output 2 s ago");
assert.match(line.title, /100% is one logical CPU.*8 logical CPUs/);

// Before two samples: measuring, never a zero.
line = liveLine(live({ state: "measuring", rss_bytes: 3 * 2 ** 30, sampled_at: now }), { stage: "LIGHT" });
assert.equal(line.text, "LIGHT · CPU measuring… · RAM 3.0 GiB · last output 2 s ago");

// Twenty quiet seconds: output inactivity in words, never "hung" or "healthy".
line = liveLine(live({ state: "live", cpu_percent: 0, rss_bytes: 2 ** 20, sampled_at: now }, { last_output_at: "2026-10-28T17:00:00Z" }), { stage: "VIS" });
assert.equal(line.text, "VIS · CPU 0% · RAM 1 MiB · running; no output for 20 s");
assert.doesNotMatch(line.text, /hung|healthy|stuck/i);

// A reading the viewer has not had refreshed: said how old it is.
line = liveLine(live({ state: "live", cpu_percent: 99, rss_bytes: 2 ** 20, sampled_at: "2026-10-28T17:00:10Z" }));
assert.match(line.text, /reading 10 s old/);

// Not measurable here: said, not zero.
line = liveLine(live({ state: "unavailable", reason: "CPU and memory are not measured on this platform", sampled_at: now }));
assert.match(line.text, /CPU and RAM not available here/);
assert.equal(line.title, "CPU and memory are not measured on this platform");

// Recorded as running by a Companion that is not supervising it.
line = liveLine({ now, supervised: false }, { stage: "QBSP" });
assert.equal(line.text, "QBSP · recorded as running; this Companion is not supervising it");

// Finished: the peaks only, no live figure, no last-output age.
line = liveLine({ now, supervised: false, resources: { state: "finished", peak_rss_bytes: 141 * 2 ** 20, peak_cpu_percent: 401 } }, { running: false });
assert.equal(line.text, "peak RAM 141 MiB · peak CPU 401%");
console.log("live line: stage, the tool's own progress, CPU per logical CPU, RAM, quiet output, never a verdict");
