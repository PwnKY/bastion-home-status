import test from 'node:test';
import assert from 'node:assert/strict';
import { validateObservations, observationCounts, observationGaps, demoObservations, beijingInput, parseBeijingInput } from '../src/observations.js';
const from = '2026-01-10T00:00:00Z', to = '2026-01-10T01:00:00Z';
const page = () => demoObservations('ipv4', from, to);
test('raw failure survives with no latency and counts are not confirmed faults', () => {
  const p = validateObservations(page(), 'ipv4');
  assert.equal(observationCounts(p.observations).down, 1);
  assert.equal(p.observations.find(r => r.status === 'down').latencyMs, null);
  assert.equal(observationGaps(p.observations).length, 1);
});
test('raw response contract rejects unknown fields, unsafe cursors and false latency', () => {
  for (const change of [p => p.observations[0].secret = 'credential', p => p.observations[0].latencyMs = Infinity, p => p.observations[0].status = 'bad', p => p.nextCursor = 'http://bad', p => p.truncated = true, p => p.serviceId = 'other', p => p.observations[0].pathMode = 'fake']) {
    const p = page(); change(p); assert.throws(() => validateObservations(p, 'ipv4'));
  }
});
test('unlocatable replay remains historical and is never assigned a source time', () => {
  const p = page(); p.observations[0].observedAt = null; p.observations[0].expiresAt = null; p.observations[0].replay = true;
  assert.equal(validateObservations(p, 'ipv4').observations[0].observedAt, null);
  assert.equal(observationCounts(p.observations).replay, 1);
  p.observations[0].replay = false; assert.throws(() => validateObservations(p, 'ipv4'));
});
test('Beijing custom range is independent of browser timezone and rejects invalid dates', () => {
  assert.equal(beijingInput('2026-01-10T00:00:00Z'), '2026-01-10T08:00:00');
  assert.equal(parseBeijingInput('2026-01-10T08:00'), '2026-01-10T00:00:00.000Z');
  assert.equal(parseBeijingInput('2026-02-31T08:00:00'), null);
  assert.equal(parseBeijingInput('not-time'), null);
});
test('overlapping evidence validity does not fabricate a gap', () => {
  const rows = page().observations.slice(0, 3).reverse();
  rows[0].expiresAt = to; rows[1].expiresAt = rows[1].observedAt;
  assert.equal(observationGaps(rows).length, 0);
});
test('empty window is empty, not a synthetic green point', () => {
  const p = page(); p.observations = [];
  assert.equal(validateObservations(p, 'ipv4').observations.length, 0);
  assert.equal(observationGaps([]).length, 0);
});
