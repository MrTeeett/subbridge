import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ConfigPicker} from '../ui/config-picker.js';
import {observedSelection, presenceIndex, presenceKey, recipientKey} from '../ui/panel-presence.js';

const reference = {source_id: 'default', node_id: 'a'.repeat(64)};
const change = selected => ({...reference, selected});
function picker(present) {
  const presence = {available: true, clients: 2, nodes: [{...reference, present}]};
  return Object.assign(Object.create(ConfigPicker.prototype), {
    groups: [{id: reference.source_id, nodes: [{id: reference.node_id}]}],
    selection: observedSelection(presence), presence, presenceIndex: presenceIndex(presence),
    requirePresence: true, editedNodes: new Set(), editedSources: new Set(), allEdited: false,
  });
}

test('Apply includes a checked config missing from the second recipient', () => {
  assert.deepEqual(picker(1).changes(), [change(true)]);
});
test('Apply leaves a config already present for every recipient untouched', () => {
  assert.deepEqual(picker(2).changes(), []);
});
test('Apply does not restore a config removed from all recipients', () => {
  assert.deepEqual(picker(0).changes(), []);
});
test('Explicitly unchecking a partial config removes it from the saved recipients', () => {
  const value = picker(1);
  value.selection = {sources: [], nodes: []};
  value.editedNodes.add(presenceKey(reference.source_id, reference.node_id));
  assert.deepEqual(value.changes(), [change(false)]);
});
test('Subscription URL mode retains full selection semantics', () => {
  const value = picker(1);
  value.requirePresence = false;
  assert.equal(value.changes(), undefined);
});
test('Recipient ordering does not count as changing the saved recipients', () => {
  assert.equal(recipientKey({emails: ['first', 'second'], inbound_ids: [2, 1]}), recipientKey({emails: ['second', 'first'], inbound_ids: [1, 2], all_clients: false}));
  assert.equal(recipientKey(null), recipientKey({emails: [], inbound_ids: []}));
});
test('Adding a client or inbound, or enabling all clients, changes the recipients', () => {
  const initial = recipientKey({emails: ['first']});
  for (const value of [{emails: ['first', 'second']}, {emails: ['first'], inbound_ids: [2]}, {emails: ['first'], all_clients: true}]) {
    assert.notEqual(recipientKey(value), initial);
  }
});
