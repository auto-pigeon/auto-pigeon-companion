import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import assert from 'node:assert/strict';

// The shipped `executionChoices` of core.js, run as it is, over a list that
// behaves like a <select>: a value is kept only when an option carries it.
// What is asked is what Build & Run does with the pipeline a user chose once
// it stops being ready (NEW_323E).

const core = readFileSync(new URL('../assets/core.js', import.meta.url), 'utf8');
const start = core.indexOf('function executionChoices(');
const end = core.indexOf('\nfunction setupActions(');
assert.ok(start > 0 && end > start, 'core.js no longer holds executionChoices before setupActions');

function select() {
  let options = [], value = '';
  return {
    parentNode: null,
    replaceChildren(...children) { options = [...children]; value = ''; },
    append(child) { options.push(child); },
    get value() { return value; },
    set value(next) { value = options.some((option) => option.attrs.value === next) ? next : ''; },
    get options() { return options; },
  };
}
const context = {el: (tag, properties) => ({tag, ...properties, disabled: false}), setupActions: () => {}};
runInNewContext(core.slice(start, end) + '\nthis.executionChoices = executionChoices;', context);
const choose = (list, items, previous, retain) =>
  context.executionChoices(list, items, previous, (item) => item.ready, (item) => item.name, 'Choose…', true, retain);

const ready = (id) => ({id, name: id, ready: true});
const waiting = (id) => ({id, name: id, ready: false});

// 1. the user's pipeline lost its readiness: it stays selected, greyed, and no other is put there
{
  const list = select();
  const items = [ready('first'), waiting('mine'), ready('other')];
  assert.equal(choose(list, items, 'mine', true), 'mine', 'the chosen pipeline is no longer the selection');
  assert.equal(list.options.find((option) => option.attrs.value === 'mine').disabled, true, 'a pipeline that needs setup is offered as if ready');
  // and a second refresh, reading the list as the page does, still holds it
  assert.equal(choose(list, items, list.value, true), 'mine', 'the second refresh replaced the chosen pipeline');
}
// 2. what happened before: the list was emptied, and the NEXT refresh chose the first ready pipeline
{
  const list = select();
  const items = [ready('first'), waiting('mine')];
  assert.equal(choose(list, items, 'mine', false), '', 'without retain the unready choice is emptied');
  assert.equal(choose(list, items, list.value, false), 'first', 'and the following refresh fills the empty list with the first ready one');
}
// 3. nothing was ever chosen: the first ready pipeline is still offered
assert.equal(choose(select(), [waiting('a'), ready('b')], '', true), 'b');
// 4. the chosen pipeline is gone altogether: nothing is selected, and nothing is substituted
assert.equal(choose(select(), [ready('a')], 'removed', true), '');
// 5. a ready choice is kept as before
assert.equal(choose(select(), [ready('a'), ready('b')], 'b', true), 'b');

console.log('pipelinechoice: ok');
