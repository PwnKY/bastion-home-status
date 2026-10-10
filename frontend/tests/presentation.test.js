import test from 'node:test';
import assert from 'node:assert/strict';
import { filterServices, previewServices, latencyParts, exitBaseline } from '../src/presentation.js';

const entries = Array.from({ length: 60 }, (_, i) => ({ id: `fixture-${i}`, name: `观测 ${i}`, subtitle: '只读测试夹具', group: i % 2 ? 'access' : 'network', status: 'healthy' }));

test('full filtering retains backend order and includes both name and scope label', () => {
  const original = structuredClone(entries);
  const result = filterServices(entries, { group: 'network', search: '观测 2' });
  assert.deepEqual(result.map((entry) => entry.id), ['fixture-2', 'fixture-20', 'fixture-22', 'fixture-24', 'fixture-26', 'fixture-28']);
  assert.equal(filterServices(entries, { search: '只读测试' }).length, 60);
  assert.deepEqual(entries, original);
});

test('bounded overview prioritizes failures without changing any conclusion or input', () => {
  const list = structuredClone(entries);
  list[59].status = 'down'; list[57].status = 'degraded'; list[56].status = 'unknown'; list[55].status = 'untested';
  const original = structuredClone(list);
  const preview = previewServices(list);
  assert.equal(preview.length, 8);
  assert.deepEqual(preview.slice(0, 3).map((entry) => entry.id), ['fixture-59', 'fixture-57', 'fixture-56']);
  assert.deepEqual(preview.slice(3).map((entry) => entry.id), ['fixture-0', 'fixture-1', 'fixture-2', 'fixture-3', 'fixture-4']);
  assert.deepEqual(list, original);
  assert.equal(filterServices(list, { statusFilter: 'down' }).length, 1);
  assert.equal(filterServices(list).length, 60);
});

test('overview safely handles empty and smaller lists, keeping equal-status order', () => {
  assert.deepEqual(previewServices([]), []);
  assert.deepEqual(previewServices(entries.slice(0, 3)), entries.slice(0, 3));
});

test('ICMP baseline is a distinct scope, never falls back to DNS on failure', () => {
  const list = [{ id: 'ipv6-baseline', status: 'healthy' }, { id: 'ipv6-icmp', status: 'down', latencyMs: null }];
  assert.deepEqual(exitBaseline(list, 'ipv6'), { id: 'ipv6-icmp', label: 'ICMP Ping 基准' });
  assert.deepEqual(exitBaseline(list, 'ipv4'), { id: 'ipv4-baseline', label: '国内 DNS 基准' });
  assert.deepEqual(exitBaseline(list.slice(0, 1), 'ipv6'), { id: 'ipv6-baseline', label: '国内 DNS 基准' });
  assert.deepEqual(exitBaseline([], 'ipv6'), { id: 'ipv6-baseline', label: '国内 DNS 基准' });
});

test('measurements are compact, carry units, and never invent a missing result', () => {
  assert.deepEqual(latencyParts(11.161), { value: '11', unit: 'ms' });
  assert.deepEqual(latencyParts(2345), { value: '2.35', unit: 's' });
  for (const value of [null, undefined, NaN, -1]) assert.deepEqual(latencyParts(value), { value: '—', unit: '暂无新鲜结果' });
});
