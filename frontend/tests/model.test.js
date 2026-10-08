import test from 'node:test';
import assert from 'node:assert/strict';

import {
  deriveView,
  getHistoryStats,
  formatLatency,
  EXCLUDED_FROM_UPTIME,
} from '../src/model.js';
import { createDemoSnapshot } from '../src/data.js';

const NOW = 1_760_000_000_000;
const STALE_SECONDS = 90;

function isoAt(ms) {
  return new Date(ms).toISOString();
}

function makeService(overrides = {}) {
  return {
    id: 'svc',
    group: 'network',
    status: 'healthy',
    latencyMs: 42,
    observedAt: isoAt(NOW - 1000),
    ...overrides,
  };
}

function makeSnapshot(overrides = {}) {
  return {
    sampledAt: isoAt(NOW),
    source: 'demo',
    synthetic: true,
    scenario: 'unit',
    collector: {
      lastHeartbeatAt: isoAt(NOW - 1000),
      intervalSeconds: 30,
      staleAfterSeconds: STALE_SECONDS,
    },
    services: [makeService()],
    ...overrides,
  };
}

test('observedAt 恰好 90 秒即过期，89.999 秒仍新鲜', () => {
  const staleView = deriveView(
    makeSnapshot({ services: [makeService({ observedAt: isoAt(NOW - 90_000) })] }),
    NOW,
  );
  const staleService = staleView.services[0];
  assert.equal(staleService.stale, true);
  assert.equal(staleService.status, 'unknown');
  assert.equal(staleService.reportedStatus, 'healthy');
  assert.equal(staleService.latencyMs, null);
  assert.equal(staleService.reportedLatencyMs, 42);
  assert.equal(staleService.observedAgeSeconds, 90);

  const freshView = deriveView(
    makeSnapshot({ services: [makeService({ observedAt: isoAt(NOW - 89_999) })] }),
    NOW,
  );
  const freshService = freshView.services[0];
  assert.equal(freshService.stale, false);
  assert.equal(freshService.status, 'healthy');
  assert.equal(freshService.latencyMs, 42);
});

test('deriveView 不修改输入快照（深拷贝派生）', () => {
  const input = createDemoSnapshot('degraded', NOW);
  const before = structuredClone(input);
  deriveView(input, NOW + 10 * 60 * 1000);
  assert.deepEqual(input, before);
});

test('没有任何已评估服务时整体为 unknown', () => {
  const view = deriveView(
    makeSnapshot({
      services: [
        makeService({ id: 'a', status: 'unknown', observedAt: isoAt(NOW) }),
        makeService({
          id: 'b',
          group: 'application',
          status: 'untested',
          latencyMs: null,
          observedAt: isoAt(NOW),
        }),
        makeService({ id: 'c', status: 'maintenance', observedAt: isoAt(NOW) }),
      ],
    }),
    NOW,
  );
  assert.equal(view.overall.status, 'unknown');
  assert.equal(view.overall.allServicesAssessed, false);
  assert.equal(view.statusCounts.unknown, 1);
  assert.equal(view.statusCounts.untested, 1);
  assert.equal(view.statusCounts.maintenance, 1);
});

test('缺失/非法 observedAt 视为过期而非新鲜', () => {
  for (const bad of [null, undefined, 'not-a-date', '', {}]) {
    const view = deriveView(
      makeSnapshot({ services: [makeService({ observedAt: bad })] }),
      NOW,
    );
    assert.equal(view.services[0].stale, true, `observedAt=${String(bad)}`);
    assert.equal(view.services[0].status, 'unknown');
    assert.equal(view.services[0].observedAgeMs, null);
    assert.equal(view.services[0].latencyMs, null);
  }
});

test('缺失/非法心跳视为 stale', () => {
  for (const bad of [null, undefined, 'nope', {}]) {
    const view = deriveView(
      makeSnapshot({ collector: { lastHeartbeatAt: bad, staleAfterSeconds: STALE_SECONDS } }),
      NOW,
    );
    assert.equal(view.collector.stale, true, `heartbeat=${String(bad)}`);
    assert.equal(view.collector.heartbeatAgeMs, null);
  }

  const boundary = deriveView(
    makeSnapshot({
      collector: { lastHeartbeatAt: isoAt(NOW - 90_000), staleAfterSeconds: STALE_SECONDS },
    }),
    NOW,
  );
  assert.equal(boundary.collector.stale, true);
  assert.equal(boundary.collector.heartbeatAgeSeconds, 90);
});

test('untested 在时间戳过期时仍保持 untested', () => {
  const view = deriveView(
    makeSnapshot({
      services: [
        makeService({
          id: 'home-app',
          group: 'application',
          status: 'untested',
          latencyMs: null,
          observedAt: isoAt(NOW - 10 * 60 * 1000),
        }),
      ],
    }),
    NOW,
  );
  const service = view.services[0];
  assert.equal(service.stale, true);
  assert.equal(service.status, 'untested');
  assert.equal(service.reportedStatus, 'untested');
  assert.equal(view.overall.status, 'unknown');
});

test('healthy 演示场景家庭应用保持 untested', () => {
  const view = deriveView(createDemoSnapshot('healthy', NOW), NOW);
  const app = view.services.find((service) => service.id === 'home-app');
  assert.equal(app.status, 'untested');
  assert.equal(app.reportedStatus, 'untested');
  assert.equal(app.stale, false);
  assert.equal(view.overall.businessTested, false);
});

test('lost 场景公网采集器保持新鲜，家侧观测置为未知', () => {
  const view = deriveView(createDemoSnapshot('lost', NOW), NOW);
  const byId = (id) => view.services.find((service) => service.id === id);

  for (const id of ['headscale-base', 'derp-base']) {
    assert.equal(byId(id).stale, false, `${id} 应保持新鲜`);
    assert.equal(byId(id).status, 'healthy', `${id} 应保持 healthy`);
  }

  const gateway = byId('gateway');
  assert.equal(gateway.stale, true);
  assert.equal(gateway.status, 'unknown');
  assert.equal(gateway.reportedStatus, 'healthy');
  assert.equal(gateway.latencyMs, null);
  assert.equal(gateway.reportedLatencyMs, 4);
  assert.equal(view.overall.status, 'degraded');
  assert.ok(view.overall.staleCount > 0);
});

test('getHistoryStats 空输入与覆盖率语义', () => {
  for (const empty of [[], null, undefined, 'nope']) {
    const stats = getHistoryStats(empty);
    assert.equal(stats.total, 0);
    assert.equal(stats.uptimeDenominator, 0);
    assert.equal(stats.uptimePercent, null);
    assert.equal(stats.degradePercent, null);
    assert.equal(stats.coverageRatio, null);
  }

  const stats = getHistoryStats([
    { status: 'healthy' },
    { status: 'healthy' },
    { status: 'healthy' },
    { status: 'degraded' },
    { status: 'down' },
    { status: 'unknown' },
    { status: 'untested' },
    { status: 'maintenance' },
    { status: 'bogus' },
  ]);
  assert.equal(stats.total, 9);
  assert.equal(stats.expectedCount, 8);
  assert.equal(stats.observedCount, 5);
  assert.equal(stats.uptimeDenominator, 5);
  assert.equal(stats.uptimePercent, 80);
  assert.equal(stats.degradePercent, 20);
  assert.equal(stats.coverageRatio, 0.625);
});

test('EXCLUDED_FROM_UPTIME 明确排除未知/未测试/维护', () => {
  assert.deepEqual([...EXCLUDED_FROM_UPTIME].sort(), ['maintenance', 'unknown', 'untested']);
});

test('formatLatency 安全处理空值并区分毫秒/秒', () => {
  assert.equal(formatLatency(null), '—');
  assert.equal(formatLatency(undefined), '—');
  assert.equal(formatLatency(Number.NaN), '—');
  assert.equal(formatLatency(Number.POSITIVE_INFINITY), '—');
  assert.equal(formatLatency(-1), '—');
  assert.equal(formatLatency('abc'), '—');
  assert.equal(formatLatency(0), '0 ms');
  assert.equal(formatLatency(42.4), '42 ms');
  assert.equal(formatLatency(999), '999 ms');
  assert.equal(formatLatency(1000), '1.00 s');
  assert.equal(formatLatency(1500), '1.50 s');
  assert.equal(formatLatency(12345), '12.35 s');
});
