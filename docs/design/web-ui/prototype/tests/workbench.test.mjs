import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  seedSessions,
  seedEvents,
  ancestors,
  eventTarget,
  escalation,
  freshSession,
} from '../app/workbench-data.ts';
test('hierarchy uses parent relations and keeps independent roots separate', () => {
  assert.deepEqual(ancestors(seedSessions, 'compat'), ['release', 'config']);
  assert.deepEqual(ancestors(seedSessions, 'docs'), []);
  assert.deepEqual(ancestors(seedSessions, 'examples'), ['docs']);
});
test('escalation preserves receiver and originating session', () => {
  const e = seedEvents.find(escalation);
  assert.equal(e.session, 'release');
  assert.equal(eventTarget(e), 'runtime');
  assert.equal(
    escalation(seedEvents.find((e) => e.type === 'plect.terminal.done')),
    false,
  );
});
test('event drill-down targets resolve to recorded objects', () => {
  for (const e of seedEvents) {
    const s = seedSessions.find((s) => s.id === e.session);
    assert.ok(s);
    if (e.task) assert.ok(s.tasks.some((t) => t.id === e.task));
    if (e.node) assert.ok(s.nodes.some((n) => n.id === e.node));
    if (e.related) assert.ok(seedSessions.some((s) => s.id === e.related));
  }
});
test('completion remains separate from session runtime and lifecycle', () => {
  const s = seedSessions.find((s) => s.id === 'compat');
  assert.equal(s.run, 'down');
  assert.equal(s.tasks[0].status, 'produced');
  assert.equal(s.tasks[0].completion, 'satisfied');
  for (const s of seedSessions)
    for (const t of s.tasks)
      assert.equal(
        t.completion === 'satisfied',
        t.checks.every((c) => c.status === 'satisfied'),
      );
});
test('new local draft session invents no execution or completion records', () => {
  const s = freshSession('new', 'urn:sample');
  assert.equal(s.run, 'down');
  assert.equal(s.resource, 'urn:sample');
  assert.deepEqual(s.tasks, []);
  assert.deepEqual(s.nodes, []);
});
