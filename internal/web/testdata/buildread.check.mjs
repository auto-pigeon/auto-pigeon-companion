// Q3_012A: the build result says WHICH saved revision it built and WHICH `.map`
// bytes the compiler was handed.
//
// Runs the function build.js ships — lifted out of the file itself, not copied
// — over a manifest shaped as the runner writes one: a map saved to the account
// three times whose APMap still calls itself revision 0, a package it is bound
// to, and for contrast an APMap file and a plain `.map` file from this machine.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/build.js", import.meta.url), "utf8");
const start = source.indexOf("function readLines(manifest)");
const end = source.indexOf("// reattach opens the build");
assert.ok(start > 0 && end > start, "build.js no longer defines readLines where this check looks");
const t = (text, slots = {}) => text.replace(/\{([a-zA-Z_.]+)\}/g, (_, key) => String(slots[key]));
const readLines = new Function("t", source.slice(start, end) + "\nreturn readLines;")(t);

const apmap = "sha256:" + "9".repeat(64), map = "sha256:" + "6".repeat(64), record = "sha256:" + "c".repeat(64), pack = "sha256:" + "a".repeat(64);
const conversion = { document_id: "room:full_map", document_revision: 0, source_sha256: apmap, manifest: { shaders: 4, models: 2, warnings: 0, sha256: record } };
const saved = {
  inputs: [{ name: "source_map", sha256: map, conversion,
    source: { backend: "http://aub.example", asset_type: "map", asset_id: "ske0lwo5s5mbd0u", display_name: "room", revision_id: "k5rlmolcyf33u88", revision: 3, content_sha256: apmap.slice(7) } }],
  game_data: { base_game: "baseq3", roots: [], packages: [{ root: "baseq3", archive_name: "zz.pk3", sha256: pack, staged: true }], findings: [] },
};
const lines = readLines(saved);
assert.deepEqual(lines.slice(0, 4), [
  "source_map: from your account — map room (ske0lwo5s5mbd0u), account revision 3, revision id k5rlmolcyf33u88",
  `source_map: converted from the map document room:full_map (revision 0), ${apmap}`,
  `source_map: the .map this build compiled — ${map}`,
  `Conversion record: 4 shader(s), 2 model(s), 0 warning(s) — ${record}`,
]);
assert.ok(lines.includes(`Bound package baseq3/zz.pk3 — ${pack} (staged)`), "the bound package's digest is still listed");

// A type that keeps a counter and no per-version row has no revision id to print.
const counted = readLines({ inputs: [{ name: "source_map", sha256: map, source: { asset_type: "map", asset_id: "abc", revision: 7 } }] });
assert.deepEqual(counted, ["source_map: from your account — map abc (abc), account revision 7"]);

// An APMap FILE: no account line — nothing was fetched — and its `.map` digest all the same.
const file = readLines({ inputs: [{ name: "source_map", sha256: map, conversion: { source_name: "room.apmap", document_revision: 0, source_sha256: apmap } }] });
assert.deepEqual(file, [`source_map: converted from the map document room.apmap (revision 0), ${apmap}`, `source_map: the .map this build compiled — ${map}`]);

// A plain `.map` file was converted from nothing and fetched from nowhere.
assert.deepEqual(readLines({ inputs: [{ name: "source_map", sha256: map }] }), []);
console.log("what this build read:", lines.length, "lines for a saved revision");
