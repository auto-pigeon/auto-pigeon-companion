// NEW_265: Build & Run lists the most recently SAVED map first.
//
// Runs the two functions play.js ships — lifted out of the file itself, not
// copied — against the cases the prompt names: names that sort opposite to
// their save times, an older map newly edited, ties, legacy rows with no time,
// and several catalog pages arriving in any order.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/play.js", import.meta.url), "utf8");
const start = source.indexOf("function savedAt(map)");
const end = source.indexOf("window.AUCOM.newestSavedFirst");
assert.ok(start > 0 && end > start, "play.js no longer defines savedAt/newestSavedFirst where this check looks");
const newestSavedFirst = new Function(source.slice(start, end) + "\nreturn newestSavedFirst;")();

const map = (id, name, created) => ({
  asset_id: id, display_name: name,
  current_revision: created === undefined ? undefined : { revision: 1, created_at: created },
});

// Names sorting opposite to save times: Zebra was saved last.
const alpha = map("a1", "Alpha", "2026-09-01T10:00:00Z");
const zebra = map("z1", "Zebra", "2026-09-28T09:00:00Z");
// An older map, newly edited: created long ago, its current revision is new.
const old = map("o1", "Old Keep", "2026-09-28T09:30:00.123456Z");
// Two saved in the same instant, and two legacy rows with no time.
const tieB = map("t2", "Twin", "2026-09-10T00:00:00Z");
const tieA = map("t1", "Twin", "2026-09-10T00:00:00Z");
const legacyB = map("l2", "Legacy B");
const legacyA = map("l1", "Legacy A", "");

const expected = ["o1", "z1", "t1", "t2", "a1", "l1", "l2"];

// Several pages, concatenated in every order they could have arrived in.
const pages = [[alpha, legacyB], [zebra, tieB], [old, tieA, legacyA]];
const orders = [[0, 1, 2], [2, 1, 0], [1, 0, 2], [2, 0, 1]];
for (const order of orders) {
  const items = order.flatMap((i) => pages[i]);
  items.sort(newestSavedFirst);
  assert.deepEqual(items.map((m) => m.asset_id), expected, `pages in order ${order}`);
}
console.log("newest saved first:", expected.join(" "));
