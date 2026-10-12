import test from 'node:test';
import assert from 'node:assert/strict';
import { CHECK_GROUPS, checkGroupFor, groupServices, summarizeCheckGroups, parseCheckRoute } from '../src/check-groups.js';
import { createGroupPreview } from '../src/group-preview.js';
import { deriveView } from '../src/model.js';

const now = Date.parse('2026-01-10T12:00:00Z');
test('current 60 checks are partitioned exactly once into eight stable display groups', () => {
  const ids = CHECK_GROUPS.flatMap((group) => group.services);
  assert.equal(ids.length, 60); assert.equal(new Set(ids).size, 60);
  assert.deepEqual(CHECK_GROUPS.map((group) => group.services.length), [6, 7, 7, 9, 10, 7, 11, 3]);
  const snapshot = createGroupPreview('degraded', now);
  const before = structuredClone(snapshot);
  const groups = summarizeCheckGroups(deriveView(snapshot, now).services);
  assert.equal(groups.reduce((sum, group) => sum + group.total, 0), 60);
  assert.deepEqual(snapshot, before);
  assert.equal(checkGroupFor(snapshot.services.find((item) => item.id === 'dns')), 'dns');
  assert.equal(checkGroupFor(snapshot.services.find((item) => item.id === 'gateway')), 'infrastructure');
  assert.equal(groupServices(snapshot.services, 'media').length, 10);
});
test('unknown, untested, maintenance and empty groups never get a green aggregate', () => {
  for (const status of ['unknown', 'untested', 'maintenance']) {
    const groups = summarizeCheckGroups([{ id: 'dns', status }, { id: 'dns-local-tcp', status: 'healthy' }]);
    assert.equal(groups.find((item) => item.id === 'dns').status, status);
  }
  const groups = summarizeCheckGroups([{ id: 'dns', status: 'healthy', stale: true }]);
  assert.equal(groups.find((item) => item.id === 'dns').status, 'unknown');
  assert.equal(groups.find((item) => item.id === 'network').status, 'unknown');
  assert.equal(summarizeCheckGroups([{ id: 'dns', status: 'invalid' }]).find((item) => item.id === 'dns').counts.unknown, 1);
});
test('new IDs have a safe fallback and are never silently lost', () => {
  const entries = [{ id: 'new-probe', group: 'network', status: 'healthy' }, { id: 'new-task', group: 'application', status: 'unknown' }, { id: '__proto__', group: '__proto__', status: 'unknown' }];
  const result = summarizeCheckGroups(entries);
  assert.equal(result.reduce((n, group) => n + group.total, 0), entries.length);
  assert.equal(checkGroupFor(entries[2]), 'monitoring');
  assert.equal(groupServices(entries, 'all'), entries);
});
test('category deep links are bounded and support all-items escape hatch', () => {
  assert.deepEqual(parseCheckRoute('#services/dns'), { page: 'services', category: 'dns' });
  assert.equal(parseCheckRoute('#services/all').category, 'all');
  assert.equal(parseCheckRoute('#services/invalid').category, null);
  assert.equal(parseCheckRoute('#paths/dns').category, null);
});
test('expanded preview is synthetic, contains no retired placeholders, and expires independently', () => {
  const snapshot = createGroupPreview('degraded', now);
  assert.equal(snapshot.synthetic, true); assert.equal(snapshot.source, 'demo');
  assert.equal(snapshot.collectors.length, 8);
  assert.ok(snapshot.services.every((item) => !['home-app', 'ipv6-baseline'].includes(item.id)));
  const expired = deriveView(snapshot, now + 100000);
  assert.ok(expired.collectors.every((item) => item.stale));
  assert.ok(expired.services.every((item) => item.status === 'unknown' && item.latencyMs === null));
  assert.ok(summarizeCheckGroups(expired.services).every((item) => item.status === 'unknown'));
  const healthy = createGroupPreview('healthy', now);
  assert.equal(healthy.services.find((item) => item.id === 'media-volume').usagePercent, 18);
  assert.equal(deriveView(healthy, now).overall.claimsAllBusinessTested, false);
});
