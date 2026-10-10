import test from 'node:test';
import assert from 'node:assert/strict';
import { emptyLiveSnapshot, fetchSnapshot, createClock, unavailableSnapshot, validateSnapshot } from '../src/api.js';
import { deriveView } from '../src/model.js';
import { trendSegments } from '../src/trend.js';

function fixture() {
  const snapshot = emptyLiveSnapshot();
  const now = new Date().toISOString();
  snapshot.serverTime = now;
  snapshot.sampledAt = now;
  snapshot.apiAvailable = true;
  snapshot.collector = { id: 'home', lastHeartbeatAt: now, heartbeatAgeSeconds: 0, staleAfterSeconds: 90, intervalSeconds: 30 };
  snapshot.collectors = [{ ...snapshot.collector, kind: 'family', label: '家庭侧' }, { id: 'public', kind: 'public', label: '公网侧', heartbeatAgeSeconds: 0, staleAfterSeconds: 90, intervalSeconds: 30 }];
  snapshot.services[0] = { ...snapshot.services[0], status: 'healthy', collectorId: 'home', observedAt: now, observedAgeSeconds: 0, staleAfterSeconds: 90, latencyMs: 12 };
  snapshot.services[1] = { ...snapshot.services[1], status: 'healthy', collectorId: 'public', observedAt: now, observedAgeSeconds: 0, staleAfterSeconds: 300, latencyMs: 14 };
  return snapshot;
}

test('API 缺失时没有合成健康、连接数或历史', () => {
  const snapshot = emptyLiveSnapshot();
  assert.equal(snapshot.synthetic, false);
  assert.equal(snapshot.apiAvailable, false);
  assert.ok(snapshot.services.every((entry) => ['unknown', 'untested'].includes(entry.status) && entry.history.length === 0 && entry.latencyMs === null));
  assert.equal(snapshot.metrics.latencyTrend.length, 0);
});
test('同域 API 返回真实版本契约，失败直接拒绝而非生成演示', async () => {
  const snapshot = fixture();
  const result = await fetchSnapshot({ fetcher: async (url, options) => {
    assert.equal(url, '/api/v1/status');
    assert.equal(options.cache, 'no-store');
    return { ok: true, json: async () => snapshot };
  } });
  assert.equal(result.apiAvailable, true);
  await assert.rejects(fetchSnapshot({ fetcher: async () => ({ ok: false }) }));
  await assert.rejects(fetchSnapshot({ fetcher: async () => ({ ok: true, json: async () => ({ synthetic: true }) }) }));
});
test('接口不可达保留历史上报、但当前状态与延迟撤销', () => {
  const snapshot = fixture();
  const view = deriveView(unavailableSnapshot(snapshot), Date.parse(snapshot.serverTime));
  assert.equal(view.services[0].status, 'unknown');
  assert.equal(view.services[0].latencyMs, null);
  assert.equal(view.services[0].reportedStatus, 'healthy');
  assert.equal(view.services[0].reportedLatencyMs, 12);
  assert.equal(view.collector.stale, true);
});
test('多采集器新鲜度独立，探针也有独立有效期', () => {
  const snapshot = fixture();
  snapshot.collectors[0].heartbeatAgeSeconds = 90;
  snapshot.collector.heartbeatAgeSeconds = 90;
  const now = Date.parse(snapshot.serverTime);
  const view = deriveView(snapshot, now);
  assert.equal(view.services[0].status, 'unknown');
  assert.equal(view.services[1].status, 'healthy');
  snapshot.collectors[1].staleAfterSeconds = 600;
  assert.equal(deriveView(snapshot, now + 300000).services[1].status, 'unknown');
});
test('实时老化使用服务器基准加单调时钟，不依赖用户墙钟', () => {
  const snapshot = fixture();
  let mono = 1000;
  const now = createClock(snapshot, () => mono);
  const base = now();
  mono += 90000;
  assert.equal(now(), base + 90000);
  assert.equal(deriveView(snapshot, now()).services[0].status, 'unknown');
});
test('未知枚举和恶意状态类拒绝进入 HTML', () => {
  const snapshot = fixture();
  snapshot.services[0].status = 'healthy" onclick="alert(1)';
  assert.throws(() => validateSnapshot(snapshot));
});
test('历史 SVG 按缺口断开，不连出不存在的探测结果', () => {
  const segments = trendSegments([{ value: 10 }, { value: 12 }, {}, { value: 15 }, { value: null }, { value: 18 }]);
  assert.deepEqual(segments.map((segment) => segment.length), [2, 1, 1]);
  assert.deepEqual(trendSegments([]), []);
});
