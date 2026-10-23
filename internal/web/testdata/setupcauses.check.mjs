// NEW_345: what the choices that are not ready need, said once.
//
// Runs core.js's own setupActions / setupRows / setupCauses over a fake DOM.
// Four Quake 1 pipelines waiting for the same compiler printed the same three
// sentences four times, with links to the PIPELINES' pages; a row now names
// the TOOL to set up, from the profile id the server attaches to a problem —
// never from the English of a summary — and the whole list is one closed
// disclosure that a refresh does not snap shut.

import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import assert from "node:assert/strict";

const core = readFileSync(new URL("../assets/core.js", import.meta.url), "utf8");
const start = core.indexOf("function setupActions(");
const end = core.indexOf("\nObject.assign(AUCOM, {");
assert.ok(start > 0 && end > start, "core.js no longer holds setupActions before its exports");

function node(tag) {
  const self = {
    tag, children: [], attrs: {}, textContent: "", className: "", open: false, dataset: {}, parentNode: null,
    append(...items) { for (const item of items) { item.parentNode = self; self.children.push(item); } },
    after(item) { item.parentNode = self.parentNode; self.parentNode.children.push(item); },
    replaceWith(item) { const list = self.parentNode.children; list[list.indexOf(self)] = item; item.parentNode = self.parentNode; },
    remove() { const list = self.parentNode.children; list.splice(list.indexOf(self), 1); },
    querySelector(selector) { return all(self).find((child) => matches(child, selector)) || null; },
    querySelectorAll(selector) { return all(self).filter((child) => matches(child, selector)); },
  };
  return self;
}
const all = (root) => root.children.flatMap((child) => [child, ...all(child)]);
function matches(child, selector) {
  if (selector === ".choice-setup") return child.className.split(" ").includes("choice-setup");
  if (selector === "details[data-item][open]") return child.tag === "details" && child.dataset.item && child.open;
  return false;
}
const text = (root) => [root.textContent, ...all(root).map((child) => child.textContent)].filter(Boolean).join(" | ");
const el = (tag, options = {}) => {
  const made = node(tag);
  if (options.text !== undefined) made.textContent = options.text;
  if (options.className) made.className = options.className;
  for (const [name, value] of Object.entries(options.attrs || {})) {
    made.attrs[name] = value;
    if (name.startsWith("data-")) made.dataset[name.slice(5)] = value;
  }
  for (const child of options.children || []) made.append(child);
  return made;
};
const AUCOM = { t: (english, values = {}) => english.replace(/\{(\w+)\}/g, (_, key) => String(values[key])) };
const context = { el, AUCOM, document: { createTextNode: (value) => Object.assign(node("#text"), { textContent: value }) },
  window: { AUCOM_I18N: { current: () => "en" } }, Intl };
runInNewContext(core.slice(start, end) + "\nthis.setupActions = setupActions; this.setupCauses = setupCauses;", context);

const missing = (step, executable) => ({ fault: "missing_engine", step, executable,
  summary: `${step}: Nothing on this machine says where ericw-tools 2.0.0-alpha11 (Quake 1)'s "${executable}" executable is.`,
  fix: "Choose it under \"Where these programs are on this machine\" in Profiles.",
  profile: "auto-pigeon.ericw-tools.q1", profile_name: "ericw-tools 2.0.0-alpha11 (Quake 1)" });
const pipeline = (id, name, problems) => ({ id, name, readiness: { ready: false, problems } });
const full = (id, name) => pipeline(id, name, [missing("Compile the map", "qbsp"), missing("Compute visibility", "vis"), missing("Compute lighting", "light")]);

// 1. One tool, three programs: one line naming them together, a link to the TOOL.
{
  const [cause, ...rest] = context.setupCauses(full("auto-pigeon.q1.normal", "Quake 1 — normal build"));
  assert.equal(rest.length, 0, "one missing tool was said more than once");
  assert.equal(cause.profile, "auto-pigeon.ericw-tools.q1", "the setup link goes to the pipeline, not to the tool");
  assert.equal(cause.text, "ericw-tools 2.0.0-alpha11 (Quake 1): qbsp, vis, and light not set up yet.");
  // the same fault of every stage is said once, in the tool's own sentence
  const folder = (step) => ({ fault: "unbound_root", step, summary: `${step}: "${step}" needs the installed game folder.`,
    detail: `"${step}" needs the installed game folder.`, profile: "q2.tool", profile_name: "Q2 tool", executable: "qbsp" });
  const [q2] = context.setupCauses(pipeline("q2", "Q2", [folder("Compile the map"), folder("Compute lighting"), { ...missing("Compute visibility", "vis"), profile: "q2.tool", profile_name: "Q2 tool" }]));
  // the tool's own sentence (detail), and the other distinct sentence counted rather than dropped
  assert.equal(q2.text, 'Q2 tool: vis not set up yet. "Compile the map" needs the installed game folder. (and 1 more)');
}
// 2. Two tools stay two lines; another fault keeps the server's own words.
{
  const mixed = pipeline("p", "Mixed", [missing("Compile the map", "qbsp"),
    { fault: "tool_incompatible", summary: "Compute lighting: light 0.18.1 is not the version this profile was written for.", fix: "", profile: "other.tool", profile_name: "Other tool", executable: "light" }]);
  const causes = context.setupCauses(mixed);
  assert.equal(JSON.stringify(causes.map((cause) => cause.profile)), JSON.stringify(["auto-pigeon.ericw-tools.q1", "other.tool"]));
  assert.equal(causes[1].text, "Other tool: light not the version this profile was written for.");
}
// 3. An engine is its own profile; an action is set up on its engine.
{
  const engine = { id: "auto-pigeon.engine.vkquake", name: "vkQuake", readiness: { ready: false, problems: [{ fault: "missing_engine", summary: "Nothing on this machine says where vkQuake's engine is.", fix: "" }] } };
  assert.equal(context.setupCauses(engine)[0].profile, "auto-pigeon.engine.vkquake");
  const action = { id: "play_map", name: "Play a map", problem: "It needs the game folder." };
  const cause = context.setupCauses(action, { setupProfile: () => ({ id: "auto-pigeon.engine.vkquake", name: "vkQuake" }) })[0];
  assert.equal(cause.profile, "auto-pigeon.engine.vkquake", "an action's setup link named the action");
  assert.equal(cause.text, "It needs the game folder.");
  // the Run page's actions carry nothing themselves: their problems are the engine's, per action
  const engineWithActions = { id: "e", name: "Yamagi", action_problems: { host_server: [{ fault: "missing_engine", summary: "Nothing on this machine says where Yamagi's \"server\" executable is.", fix: "" }] } };
  const [hostCause] = context.setupCauses({ id: "host_server", title: "Run a dedicated server" }, {
    setupProfile: () => ({ id: "e", name: "Yamagi" }),
    setupProblems: (action) => engineWithActions.action_problems[action.id] || [],
  });
  assert.equal(hostCause.profile, "e", "an action row has no setup link to its engine");
  assert.match(hostCause.text, /"server" executable/);
}
// 4. Four waiting pipelines under a select: ONE closed disclosure, four rows,
//    every link to the tool, the exact words kept one click on; a refresh keeps
//    it open when the person opened it.
{
  const field = node("div");
  const select = node("select");
  field.append(select);
  const waiting = ["leak test", "fast preview", "final build", "normal build"].map((name, index) => full("q1." + index, "Quake 1 — " + name));
  context.setupActions(select, waiting, { setupLabel: "pipelines" });
  const box = field.querySelector(".choice-setup");
  assert.ok(box && box.tag === "details" && box.open === false, "the list is not one closed disclosure");
  assert.equal(box.children[0].textContent, "Other pipelines (4 need setup)");
  const rows = box.children[1].children;
  assert.equal(rows.length, 4);
  const links = all(box).filter((child) => child.tag === "a");
  assert.equal(links.length, 4, "one setup link per row");
  assert.ok(links.every((link) => link.attrs.href === "#profiles/auto-pigeon.ericw-tools.q1"), "a link does not go to the tool");
  assert.match(text(box), /Compute lighting: Nothing on this machine says where/, "the exact words were dropped");
  box.open = true;
  context.setupActions(select, waiting, { setupLabel: "pipelines" });
  assert.equal(field.querySelector(".choice-setup").open, true, "a refresh closed the list the person opened");
  context.setupActions(select, waiting.slice(0, 1), { setupLabel: "pipelines" });
  assert.equal(field.querySelector(".choice-setup").children[0].textContent, "Other pipelines (1 needs setup)");
  context.setupActions(select, [], {});
  assert.equal(field.querySelector(".choice-setup"), null, "the list stayed with nothing waiting");
}

console.log("setupcauses: one line per tool to set up, links to the tool, one closed list, exact words kept — ok");
