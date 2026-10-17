// Q2_020: Build & Run's "name in the game" follows the chosen map until the
// person names it themselves.
//
// Seen live on the release candidate: choosing one map and then another kept
// the FIRST map's suggested name, so the second map was compiled, installed as
// aup-<first map> and started with +map <first map>. Runs the function play.js
// ships — lifted out of the file itself, not copied — against the page's own
// order of events: a map is chosen, its files arrive, the name is suggested.
//
//   node mapname.check.mjs [path/to/play.js]

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const file = process.argv[2] || new URL("../assets/play.js", import.meta.url);
const source = readFileSync(file, "utf8");
// Before the repair the function had no remembered suggestion; lifting from the
// function itself lets this check run against that file and show the failure.
let start = source.indexOf("let suggestedMapName");
const declared = start >= 0;
if (!declared) start = source.indexOf("function suggestMapName()");
const end = source.indexOf("// Auto-build on the chosen map");
assert.ok(start > 0 && end > start, "play.js no longer defines suggestMapName where this check looks");

function page() {
  const field = { value: "" };
  const state = { sourceFile: "", map: null, mapName: "" };
  const $ = (id) => {
    assert.equal(id, "play-map-name", `suggestMapName reached for #${id}`);
    return field;
  };
  const suggest = new Function("$", "state",
    (declared ? "" : 'let suggestedMapName = "";\n') + source.slice(start, end) + "\nreturn suggestMapName;")($, state);
  // What the page does when a map's revision and files have arrived.
  const choose = (path, displayName) => {
    state.map = { display_name: displayName };
    state.sourceFile = path;
    suggest();
  };
  // What the field's own input listener does.
  const type = (text) => {
    field.value = text;
    state.mapName = text.trim();
  };
  return { field, state, choose, type };
}

// The first map names the field.
{
  const p = page();
  p.choose("Arena One.map", "Arena One");
  assert.equal(p.field.value, "arena_one");
  assert.equal(p.state.mapName, "arena_one");

  // Another map: the page's own suggestion is replaced, not kept.
  p.choose("q2020_l1.map", "q2020_l1");
  assert.equal(p.field.value, "q2020_l1", "a second map kept the first map's suggested name");
  assert.equal(p.state.mapName, "q2020_l1");

  // And back again.
  p.choose("Arena One.map", "Arena One");
  assert.equal(p.field.value, "arena_one");
}

// A name the person typed is theirs: another map does not overwrite it.
{
  const p = page();
  p.choose("Arena One.map", "Arena One");
  p.type("dm1");
  p.choose("q2020_l1.map", "q2020_l1");
  assert.equal(p.field.value, "dm1");
  assert.equal(p.state.mapName, "dm1");

  // Cleared by the person: the next map suggests again.
  p.type("");
  p.choose("q2020_l1.map", "q2020_l1");
  assert.equal(p.field.value, "q2020_l1");
}

// A name restored from a link or a reload is the person's too.
{
  const p = page();
  p.type("mymap");
  p.choose("q2020_l1.map", "q2020_l1");
  assert.equal(p.field.value, "mymap");
}

// No file and no display name: nothing to suggest, and the field is left alone.
{
  const p = page();
  p.choose("", "");
  assert.equal(p.field.value, "");
}

console.log("ok: the name in the game follows the chosen map until the person names it");
