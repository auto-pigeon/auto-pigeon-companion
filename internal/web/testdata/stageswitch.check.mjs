import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import assert from 'node:assert/strict';

// The shipped stageswitch.js and scratch.js, run as they are. The table below
// is the compatibility policy; the second half drives the stage's real Tool
// choice through scratch.js's own handlers and reads the request it would post.

const asset = (name) => readFileSync(new URL('../assets/' + name, import.meta.url), 'utf8');

// --- a three-stage pipeline and the tools that could run it -----------------

const port = (name, role, required = true) => ({name, role, required});
const tools = {
  // The tool the pipeline was written with.
  stock: {
    'q1.bsp.compile': {inputs: [port('map', 'q1.map'), port('wad', 'q1.wad', false)], outputs: [port('bsp', 'q1.bsp'), port('prt', 'q1.prt')],
      options: [{name: 'threads', type: 'integer', minimum: 1, maximum: 64}, {name: 'leak_mode', type: 'enum', values: [{value: 'stop'}, {value: 'continue'}]}, {name: 'verbose', type: 'bool'}, {name: 'label', type: 'text'}]},
    'q1.bsp.vis': {inputs: [port('bsp', 'q1.bsp'), port('prt', 'q1.prt')], outputs: [port('bsp', 'q1.bsp')], options: [{name: 'level', type: 'integer', minimum: 0, maximum: 4}]},
    'q1.bsp.light': {inputs: [port('bsp', 'q1.bsp')], outputs: [port('bsp', 'q1.bsp'), port('lit', 'q1.lit')], options: [{name: 'extra', type: 'bool'}]},
  },
  // Another build of the same compiler: every declaration identical.
  fork: {
    'q1.bsp.compile': {inputs: [port('map', 'q1.map'), port('wad', 'q1.wad', false)], outputs: [port('bsp', 'q1.bsp'), port('prt', 'q1.prt')],
      options: [{name: 'threads', type: 'integer', minimum: 1, maximum: 64}, {name: 'leak_mode', type: 'enum', values: [{value: 'stop'}, {value: 'continue'}]}, {name: 'verbose', type: 'bool'}, {name: 'label', type: 'text'}]},
  },
  // Same job, its own names: `source` for the map and `out` for the BSP.
  renamed: {
    'q1.bsp.compile': {inputs: [port('source', 'q1.map'), port('wad', 'q1.wad', false)], outputs: [port('out', 'q1.bsp'), port('prt', 'q1.prt')],
      options: [{name: 'threads', type: 'integer', minimum: 1, maximum: 64}]},
  },
  // `map` here is a different kind of file.
  othertype: {'q1.bsp.compile': {inputs: [port('map', 'q3.map')], outputs: [port('bsp', 'q1.bsp'), port('prt', 'q1.prt')], options: []}},
  // No wad input, no prt output, and a required input the stage never had.
  smaller: {'q1.bsp.compile': {inputs: [port('map', 'q1.map'), port('entities', 'q1.ent')], outputs: [port('bsp', 'q1.bsp')], options: []}},
  // `threads` is yes/no, and leak_mode has other choices.
  retyped: {'q1.bsp.compile': {inputs: [port('map', 'q1.map'), port('wad', 'q1.wad', false)], outputs: [port('bsp', 'q1.bsp'), port('prt', 'q1.prt')],
    options: [{name: 'threads', type: 'bool'}, {name: 'leak_mode', type: 'enum', values: [{value: 'stop'}, {value: 'ignore'}]}, {name: 'verbose', type: 'bool'}, {name: 'label', type: 'text'}]}},
  // Two inputs take a map: the role alone cannot say which.
  twomaps: {'q1.bsp.compile': {inputs: [port('first', 'q1.map'), port('second', 'q1.map')], outputs: [port('bsp', 'q1.bsp'), port('prt', 'q1.prt')], options: []}},
  // A different job altogether.
  packer: {'q1.pak.pack': {inputs: [port('files', 'q1.loose')], outputs: [port('pak', 'q1.pak')], options: [{name: 'name', type: 'text'}]}},
};

const providerList = () => Object.entries(tools).flatMap(([id, actions]) => Object.entries(actions).map(([capability, action]) => ({
  capability, title: capability, actionId: capability.split('.').pop(), profileId: id, profileName: id.toUpperCase(), ready: id !== 'packer', ...action})));

function pipeline() {
  return {
    inputs: [{name: 'map', title: 'Map', role: 'q1.map', extensions: ['.map'], required: true}, {name: 'wad', title: 'WAD', role: 'q1.wad', extensions: ['.wad'], required: false}],
    steps: [
      {id: 'qbsp', title: 'Compile', capability: 'q1.bsp.compile', tool: 'stock', inputs: {map: 'pipeline.map', wad: 'pipeline.wad'},
        options: {threads: '8', leak_mode: 'continue', verbose: 'true', label: 'beta-1'}, arguments: ['-nopercent', '-wadpath $(HOME); rm -rf /']},
      {id: 'vis', title: 'Vis', capability: 'q1.bsp.vis', tool: 'stock', inputs: {bsp: 'qbsp.bsp', prt: 'qbsp.prt'}, options: {level: '4'}, arguments: []},
      {id: 'light', title: 'Light', capability: 'q1.bsp.light', tool: 'stock', inputs: {bsp: 'vis.bsp'}, options: {extra: 'true'}, arguments: ['-bounce']},
    ],
    outputs: [{name: 'bsp', title: 'BSP', role: 'q1.bsp', from: 'light.bsp'}, {name: 'raw', title: 'Unlit', role: 'q1.bsp', from: 'qbsp.bsp', optional: true}],
  };
}

// --- a DOM just large enough for scratch.js ---------------------------------

function page() {
  const byId = new Map();
  class Node {
    constructor(tag = 'div') { this.tag = tag; this.children = []; this.attrs = {}; this.dataset = {}; this.listeners = {}; this.value = ''; this.className = ''; this.textContent = ''; this.disabled = false; }
    get options() { return this.children.filter((child) => child.tag === 'option'); }
    append(...items) { for (const item of items) this.children.push(item); }
    replaceChildren(...items) { this.children = [...items]; }
    setAttribute(key, value) { this.attrs[key] = String(value); if (key === 'value') this.value = String(value); if (key.startsWith('data-')) this.dataset[key.slice(5)] = String(value); if (key === 'class') this.className = String(value); }
    getAttribute(key) { return this.attrs[key]; }
    removeAttribute(key) { delete this.attrs[key]; }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    fire(type) { for (const fn of this.listeners[type] || []) fn({target: this, currentTarget: this}); }
    contains(node) { return this === node || this.all().includes(node); }
    all() { return this.children.flatMap((child) => (child instanceof Node ? [child, ...child.all()] : [])); }
    querySelectorAll(selector) {
      const tags = selector.split(',').map((part) => part.trim());
      return this.all().filter((node) => tags.includes(node.tag));
    }
    querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
    focus() { document.activeElement = this; }
    text() { return [this.textContent, ...this.children.map((child) => (child instanceof Node ? child.text() : String(child)))].join(' '); }
  }
  const $ = (id) => { if (!byId.has(id)) byId.set(id, new Node()); return byId.get(id); };
  const el = (tag, {className = '', text = '', attrs = {}, children = []} = {}) => {
    const node = new Node(tag); node.className = className; node.textContent = text;
    for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, value);
    node.append(...children); return node;
  };
  const document = {activeElement: null, createTextNode: (text) => text};
  let list = providerList();
  const AUCOM = {$, el, api: async () => ({ok: true, body: {items: Object.entries(tools).map(([id, actions]) => ({id, kind: 'tool', name: id.toUpperCase(), readiness: {ready: id !== 'packer'},
    actions: Object.entries(actions).map(([capability, action]) => ({id: capability.split('.').pop(), title: capability, capability, ...action}))}))}})};
  const context = {window: {AUCOM}, document, console};
  runInNewContext(asset('stageswitch.js'), context);
  runInNewContext(asset('scratch.js'), context);
  $('wizard-kind').value = 'pipeline';
  const scratch = AUCOM.scratch;
  const body = $('scratch-body');
  // The stage cards, each with its Tool select and whatever review is open.
  const cards = () => body.all().filter((node) => node.className === 'panel scratch-card');
  const toolSelect = (index) => cards()[index].querySelectorAll('select').find((node) => node.dataset.switch === 'tool');
  const button = (index, which) => cards()[index].querySelectorAll('button').find((node) => node.dataset.switch === which);
  return {AUCOM, scratch, body, cards, toolSelect, button, document,
    async open(doc = pipeline()) { await scratch.refresh(); scratch.state.pipeline = doc; scratch.render(); return doc; },
    choose(index, tool, capability) { const node = toolSelect(index); node.value = tool ? `${tool} ${capability}` : ''; document.activeElement = node; node.fire('change'); },
    // Across the vm boundary an array is another realm's Array; compare plain data.
    plan(args) { return JSON.parse(JSON.stringify(AUCOM.stageSwitch.plan(args))); }, list};
}

let count = 0;
async function test(name, fn) { await fn(); count++; console.log('PASS ' + name); }

const sourceRoles = {'pipeline.map': 'q1.map', 'pipeline.wad': 'q1.wad'};
const readers = [{ref: 'qbsp.bsp', label: 'stage vis, input bsp'}, {ref: 'qbsp.prt', label: 'stage vis, input prt'}, {ref: 'qbsp.bsp', label: 'result raw'}];
const provider = (id, capability = 'q1.bsp.compile') => providerList().find((item) => item.profileId === id && item.capability === capability);
const compare = (to, overrides = {}) => page().plan({step: {...pipeline().steps[0], ...overrides}, from: provider('stock'), to, sourceRoles, readers});
const kinds = (result) => result.findings.map((finding) => `${finding.kind}:${finding.field}`).sort();

// --- the table --------------------------------------------------------------

await test('identical capability from another tool keeps every binding and parameter', () => {
  const result = compare(provider('fork'));
  assert.deepEqual(result.findings, []);
  assert.deepEqual(result.inputs, {map: 'pipeline.map', wad: 'pipeline.wad'});
  assert.deepEqual(result.options, {threads: '8', leak_mode: 'continue', verbose: 'true', label: 'beta-1'});
  assert.deepEqual(result.outputs, {});
  assert.equal(result.tool, 'fork'); assert.equal(result.noop, false);
});

await test('the same tool chosen again is no change', () => {
  const result = compare(provider('stock'));
  assert.equal(result.noop, true); assert.deepEqual(result.findings, []);
  // …and so is the one provider of a stage that names no tool.
  const only = page().plan({step: {...pipeline().steps[1], tool: ''}, from: provider('stock', 'q1.bsp.vis'), to: provider('stock', 'q1.bsp.vis')});
  assert.equal(only.noop, true);
});

await test('a port declared under another name is carried by its role, on both sides', () => {
  const result = compare(provider('renamed'), {options: {threads: '8'}});
  assert.deepEqual(result.findings, []);
  assert.deepEqual(result.inputs, {source: 'pipeline.map', wad: 'pipeline.wad'});
  assert.deepEqual(result.outputs, {bsp: 'out'});
  assert.equal(result.renamed.length, 2);
  assert.match(result.renamed.join('|'), /input map is source/);
});

await test('an input of another type is a finding, not a silent keep', () => {
  const result = compare(provider('othertype'), {options: {}});
  assert.deepEqual(kinds(result), ['input_removed:input wad', 'input_type:input map']);
  assert.match(result.findings.find((f) => f.kind === 'input_type').reason, /takes a q3\.map as map, and pipeline\.map is a q1\.map/);
  assert.deepEqual(result.inputs, {});
});

await test('a removed optional input and output are named, and a new required input is asked for', () => {
  const result = compare(provider('smaller'), {options: {}});
  assert.deepEqual(kinds(result), ['input_removed:input wad', 'output_removed:output prt']);
  assert.match(result.findings.find((f) => f.kind === 'output_removed').reason, /read by stage vis, input prt/);
  assert.deepEqual(result.inputs, {map: 'pipeline.map'});
  assert.deepEqual(result.needs, ['entities']);
});

await test('a removed REQUIRED input is named with the source it had', () => {
  const result = page().plan({step: pipeline().steps[1], from: provider('stock', 'q1.bsp.vis'), to: provider('stock', 'q1.bsp.light'),
    sourceRoles: {...sourceRoles, 'qbsp.bsp': 'q1.bsp', 'qbsp.prt': 'q1.prt'}, readers: [{ref: 'vis.bsp', label: 'stage light, input bsp'}]});
  assert.deepEqual(kinds(result), ['input_removed:input prt', 'option_removed:parameter level']);
  assert.equal(result.findings.find((f) => f.field === 'input prt').value, 'qbsp.prt');
  assert.deepEqual(result.inputs, {bsp: 'qbsp.bsp'});
});

await test('the same parameter name with another type keeps only a value that is still one', () => {
  const result = compare(provider('retyped'));
  assert.deepEqual(kinds(result), ['option_type:parameter threads', 'option_value:parameter leak_mode']);
  assert.deepEqual(result.options, {verbose: 'true', label: 'beta-1'});
  // "true" IS a value of a yes/no parameter, whatever it was before.
  assert.deepEqual(kinds(compare(provider('retyped'), {options: {threads: 'true'}})), []);
});

await test('an enum value the new tool does not offer is named with the choices it does', () => {
  const finding = compare(provider('retyped'), {options: {leak_mode: 'continue'}}).findings[0];
  assert.equal(finding.kind, 'option_value'); assert.equal(finding.value, 'continue');
  assert.match(finding.reason, /not one of its choices here \(stop, ignore\)/);
  assert.deepEqual(compare(provider('retyped'), {options: {leak_mode: 'stop'}}).options, {leak_mode: 'stop'});
});

await test('an integer outside the new range is refused, inside it is kept', () => {
  const narrow = {...provider('fork'), options: [{name: 'threads', type: 'integer', minimum: 1, maximum: 4}]};
  assert.match(compare(narrow, {options: {threads: '8'}}).findings[0].reason, /above its maximum of 4/);
  assert.deepEqual(compare(narrow, {options: {threads: '4'}}).options, {threads: '4'});
});

await test('two inputs of the role are a question, never a positional guess', () => {
  const result = compare(provider('twomaps'), {options: {}, inputs: {map: 'pipeline.map'}});
  assert.deepEqual(kinds(result), ['input_removed:input map']);
  assert.match(result.findings[0].reason, /more than one of its inputs takes a q1\.map/);
  assert.deepEqual(result.inputs, {});
});

await test('an output read downstream that changed its id is repointed only when declared once each side', () => {
  const result = compare(provider('renamed'), {options: {}});
  assert.deepEqual(result.outputs, {bsp: 'out'});
  const two = {...provider('renamed'), outputs: [port('a', 'q1.bsp'), port('b', 'q1.bsp'), port('prt', 'q1.prt')]};
  assert.deepEqual(kinds(compare(two, {options: {}, inputs: {wad: 'pipeline.wad'}})), ['output_removed:output bsp']);
});

await test('a tool nothing installed provides is compared by name and by what the source produces', () => {
  const result = page().plan({step: pipeline().steps[0], from: undefined, to: provider('renamed'), sourceRoles, readers});
  // No declaration to read the old `map` from: its source's own role says it.
  assert.deepEqual(result.inputs, {source: 'pipeline.map', wad: 'pipeline.wad'});
  // …but nothing says what the missing tool's `bsp` was, so nothing is guessed.
  assert.deepEqual(kinds(result), ['option_removed:parameter label', 'option_removed:parameter leak_mode', 'option_removed:parameter verbose',
    'output_removed:output bsp']);
});

await test('choosing no tool lists everything the stage holds', () => {
  const result = compare(undefined);
  assert.equal(result.tool, ''); assert.equal(result.findings.filter((f) => f.kind.startsWith('input')).length, 2);
  assert.equal(result.findings.filter((f) => f.kind.startsWith('option')).length, 4);
});

// --- the stage's real Tool choice -------------------------------------------

const posted = (h) => JSON.parse(JSON.stringify(h.scratch.request()));

await test('UI: another tool with the same capability keeps wiring, parameters, arguments and the graph', async () => {
  const h = page(); await h.open(); const before = posted(h);
  h.choose(0, 'fork', 'q1.bsp.compile');
  const after = posted(h);
  assert.equal(after.steps[0].tool, 'fork');
  assert.deepEqual({...after.steps[0], tool: 'stock'}, before.steps[0]);
  assert.deepEqual(after.steps.slice(1), before.steps.slice(1));
  assert.deepEqual(after.outputs, before.outputs); assert.deepEqual(after.inputs, before.inputs);
  // Arguments are this stage's own words: one box each, exactly as typed, shell syntax and all.
  assert.deepEqual(JSON.parse(JSON.stringify(h.scratch.stageTokens()))[0], {stage: 'qbsp', arguments: ['-nopercent', '-wadpath $(HOME); rm -rf /']});
  assert.equal(h.scratch.pendingReview(), '');
  assert.match(h.cards()[0].text(), /Tool changed\. Kept: input map ← pipeline\.map/);
});

await test('UI: choosing the same tool again changes nothing and says nothing', async () => {
  const h = page(); await h.open(); const before = posted(h);
  h.choose(0, 'stock', 'q1.bsp.compile');
  assert.deepEqual(posted(h), before); assert.doesNotMatch(h.cards()[0].text(), /Tool changed/);
});

await test('UI: a renamed output repoints the later stage and the result that read it', async () => {
  const h = page(); await h.open();
  h.choose(0, 'renamed', 'q1.bsp.compile');
  // threads is the one parameter both declare; the other three wait for an answer.
  assert.equal(h.scratch.pendingReview(), 'qbsp');
  h.button(0, 'change').fire('click');
  const after = posted(h);
  assert.deepEqual(after.steps[0].inputs, {source: 'pipeline.map', wad: 'pipeline.wad'});
  assert.deepEqual(after.steps[0].options, {threads: '8'});
  assert.deepEqual(after.steps[1].inputs, {bsp: 'qbsp.out', prt: 'qbsp.prt'});
  assert.equal(after.outputs[1].from, 'qbsp.out'); assert.equal(after.outputs[0].from, 'light.bsp');
});

await test('UI: an incompatible tool opens a review, and the draft is byte-for-byte unchanged until answered', async () => {
  const h = page(); await h.open(); const before = posted(h); const tokens = JSON.stringify(h.scratch.stageTokens());
  h.choose(0, 'packer', 'q1.pak.pack');
  assert.equal(h.scratch.pendingReview(), 'qbsp');
  assert.deepEqual(posted(h), before);
  const review = h.cards()[0].text();
  for (const field of ['input map (pipeline.map)', 'input wad (pipeline.wad)', 'parameter threads (8)', 'parameter leak_mode (continue)', 'output bsp', 'output prt'])
    assert.ok(review.includes(field), 'the review names ' + field);
  assert.match(review, /Nothing has changed yet/);
  // The Tool choice still shows the tool the stage has.
  assert.equal(h.toolSelect(0).value, 'stock q1.bsp.compile');
  assert.equal(h.document.activeElement, h.button(0, 'keep'));

  h.button(0, 'keep').fire('click');
  assert.deepEqual(posted(h), before); assert.equal(JSON.stringify(h.scratch.stageTokens()), tokens);
  assert.equal(h.scratch.pendingReview(), ''); assert.equal(h.button(0, 'keep'), undefined);
  assert.equal(h.document.activeElement, h.toolSelect(0));
});

await test('UI: accepting the review revises the one stage, shows what is now incomplete, and remembers nothing', async () => {
  const h = page(); await h.open(); const before = posted(h);
  h.choose(0, 'packer', 'q1.pak.pack'); h.button(0, 'change').fire('click');
  const after = posted(h);
  assert.deepEqual(after.steps[0], {id: 'qbsp', title: 'Compile', capability: 'q1.pak.pack', tool: 'packer', inputs: {}, options: {}});
  // Later stages still read what is no longer produced, and the page says so.
  assert.deepEqual(after.steps.slice(1), before.steps.slice(1));
  assert.match(h.cards()[0].text(), /This stage is not complete: files is required and not supplied/);
  assert.match(h.cards()[1].text(), /bsp reads qbsp\.bsp, which nothing before this stage produces/);
  assert.match(h.cards()[1].text(), /qbsp\.bsp — not available here/);
  // Switching back does not resurrect what was agreed to be removed.
  h.choose(0, 'stock', 'q1.bsp.compile');
  assert.deepEqual(posted(h).steps[0], {id: 'qbsp', title: 'Compile', capability: 'q1.bsp.compile', tool: 'stock', inputs: {}, options: {}});
  assert.match(h.cards()[0].text(), /map is required and not supplied/);
});

await test('UI: cancel, then a compatible switch and back, ends where it began', async () => {
  const h = page(); await h.open(); const before = posted(h);
  h.choose(0, 'packer', 'q1.pak.pack'); h.button(0, 'keep').fire('click');
  h.choose(0, 'fork', 'q1.bsp.compile'); h.choose(0, 'stock', 'q1.bsp.compile');
  assert.deepEqual(posted(h), before);
});

await test('UI: a tool that needs setup is said to, beside the stage it runs', async () => {
  const h = page(); const doc = pipeline();
  doc.steps.push({id: 'pack', title: 'Pack', capability: 'q1.pak.pack', tool: 'packer', inputs: {}, options: {}, arguments: []});
  await h.open(doc);
  assert.match(h.cards()[3].text(), /still needs setup in Profiles/);
  assert.doesNotMatch(h.cards()[0].text(), /still needs setup/);
});

await test('UI: a stage whose tool is not installed keeps its fields and is named incomplete', async () => {
  const h = page(); const doc = pipeline(); doc.steps[0].tool = 'gone';
  await h.open(doc); const before = posted(h);
  assert.match(h.cards()[0].text(), /No installed tool provides q1\.bsp\.compile as gone/);
  assert.equal(h.toolSelect(0).value, 'gone q1.bsp.compile');
  h.choose(0, 'gone', 'q1.bsp.compile');
  assert.deepEqual(posted(h), before);
});

console.log(`${count} checks passed`);
