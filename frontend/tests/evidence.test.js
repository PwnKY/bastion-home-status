import test from 'node:test';
import assert from 'node:assert/strict';
import { emptyLiveSnapshot, validateSnapshot, unavailableSnapshot } from '../src/api.js';
import { evidencePresentation } from '../src/presentation.js';
import { deriveView } from '../src/model.js';

function fixture() {
  const snapshot = emptyLiveSnapshot();
  snapshot.serverTime = '2026-01-01T00:00:00Z';
  snapshot.apiAvailable = true;
  snapshot.collector = { id: 'home', heartbeatAgeSeconds: 0, staleAfterSeconds: 90 };
  snapshot.collectors = [{ ...snapshot.collector, kind: 'family', label: '家庭' }];
  snapshot.services[0] = { ...snapshot.services[0], collectorId: 'home', observedAt: snapshot.serverTime, observedAgeSeconds: 0, staleAfterSeconds: 90, status: 'healthy', latencyMs: 10, evidence: { kind: 'peer_success', at: snapshot.serverTime, historical: false } };
  return snapshot;
}

test('可选证据兼容旧响应，拒绝原始日志、身份与无效时间', () => {
  const snapshot = fixture();
  assert.ok(validateSnapshot(snapshot));
  for (const evidence of [{ kind: 'raw_log', at: snapshot.serverTime, historical: true }, { kind: 'job_success', at: 'private text', historical: true }, { kind: 'job_success', at: snapshot.serverTime, historical: 'yes' }, { kind: 'job_success', at: '1900-01-01T00:00:00Z', historical: true }, { kind: 'peer_success', at: snapshot.serverTime, historical: false, peer: 'private' }]) {
    snapshot.services[0].evidence = evidence;
    assert.throws(() => validateSnapshot(snapshot));
  }
  delete snapshot.services[0].evidence;
  assert.ok(validateSnapshot(snapshot));
  assert.equal(evidencePresentation(snapshot.services[0]), null);
});

test('来源记录不补出当前健康、路径、RTT；过期和接口失联只作参考', () => {
  const snapshot = fixture();
  const now = Date.parse(snapshot.serverTime);
  const current = deriveView(snapshot, now).services[0];
  assert.equal(evidencePresentation(current).qualifier, '来源记录');
  for (const view of [deriveView(snapshot, now + 90000), deriveView(unavailableSnapshot(snapshot), now)]) {
    const entry = view.services[0];
    assert.equal(entry.status, 'unknown');
    assert.equal(entry.latencyMs, null);
    assert.equal(entry.pathMode, 'unknown');
    assert.equal(evidencePresentation(entry).qualifier, '仅作参考');
    assert.equal(evidencePresentation(entry).at, snapshot.serverTime);
  }
});

test('跨启动任务完成和旧节点成功记录仍可读，不冒充当前结果', () => {
  for (const kind of ['job_success', 'job_failure', 'peer_success', 'peer_failure', 'control_message']) {
    const entry = { status: 'unknown', evidence: { kind, at: '2026-01-01T00:00:00Z', historical: true } };
    const before = JSON.stringify(entry);
    const note = evidencePresentation(entry);
    assert.equal(note.qualifier, '仅作参考');
    assert.equal(JSON.stringify(entry), before);
    assert.ok(note.label);
  }
});
