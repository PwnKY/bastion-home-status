import test from 'node:test';
import assert from 'node:assert/strict';

import { createDemoSnapshot, DEFAULT_SCENARIO, SCENARIOS } from '../src/data.js';

const SERVICE_GROUPS = ['network', 'access', 'application', 'monitoring'];
const VALID_STATUSES = ['healthy', 'degraded', 'unknown', 'untested', 'maintenance', 'down'];
const REQUIRED_SERVICE_FIELDS = [
  'id',
  'name',
  'subtitle',
  'group',
  'status',
  'latencyMs',
  'availability24h',
  'availability7d',
  'coverage',
  'observedAt',
  'probeLabel',
  'scope',
  'summary',
  'history',
  'detail',
];

function isIso(value) {
  return typeof value === 'string' && Number.isFinite(Date.parse(value));
}

test('SCENARIOS 提供三个演示场景元数据', () => {
  assert.equal(SCENARIOS.length, 3);
  assert.deepEqual(
    SCENARIOS.map((scenario) => scenario.id),
    ['healthy', 'degraded', 'lost'],
  );
  for (const scenario of SCENARIOS) {
    assert.equal(typeof scenario.label, 'string');
    assert.ok(scenario.label.length > 0);
  }
});

test('默认场景为 degraded，且 IPv6 默认降级', () => {
  const snapshot = createDemoSnapshot();
  assert.equal(DEFAULT_SCENARIO, 'degraded');
  assert.equal(snapshot.scenario, 'degraded');
  const ipv6 = snapshot.services.find((service) => service.id === 'ipv6');
  assert.equal(ipv6.status, 'degraded');
});

test('快照顶层结构符合演示契约', () => {
  const now = 1_760_000_000_000;
  const snapshot = createDemoSnapshot('degraded', now);
  assert.equal(snapshot.source, 'demo');
  assert.equal(snapshot.synthetic, true);
  assert.ok(snapshot.disclaimer.includes('合成'));
  assert.equal(snapshot.scenario, 'degraded');
  assert.equal(snapshot.sampledAt, new Date(now).toISOString());

  assert.ok(isIso(snapshot.collector.lastHeartbeatAt));
  assert.equal(snapshot.collector.intervalSeconds, 30);
  assert.equal(snapshot.collector.staleAfterSeconds, 90);

  assert.ok(Array.isArray(snapshot.metrics.latencyTrend));
  assert.ok(snapshot.metrics.latencyTrend.length > 0);
  for (const point of snapshot.metrics.latencyTrend) {
    assert.ok(isIso(point.at));
    assert.equal(typeof point.value, 'number');
    assert.ok(Number.isFinite(point.value));
  }
  for (const point of snapshot.metrics.coverageTrend) {
    assert.ok(isIso(point.at));
    assert.equal(typeof point.value, 'number');
  }

  assert.ok(Array.isArray(snapshot.incidents));
  assert.ok(snapshot.incidents.length > 0);
  for (const incident of snapshot.incidents) {
    assert.equal(typeof incident.id, 'string');
    assert.equal(typeof incident.title, 'string');
    assert.equal(typeof incident.description, 'string');
    assert.ok(['resolved', 'investigating', 'info'].includes(incident.status));
    assert.ok(isIso(incident.startedAt));
    assert.ok(incident.resolvedAt === null || isIso(incident.resolvedAt));
    assert.ok(Array.isArray(incident.affectedServiceIds));
  }
});

test('服务覆盖要求的分组与条目', () => {
  const snapshot = createDemoSnapshot('degraded');
  assert.ok(snapshot.services.length >= 10 && snapshot.services.length <= 12);

  const ids = snapshot.services.map((service) => service.id);
  for (const required of [
    'gateway',
    'dns',
    'ipv4',
    'ipv6',
    'proxy',
    'headscale-base',
    'headscale-control',
    'tailscale-path',
    'derp-base',
    'tunnel-ready',
    'home-app',
    'collectors',
  ]) {
    assert.ok(ids.includes(required), `缺少服务 ${required}`);
  }
  assert.notEqual('headscale-base', 'headscale-control');
});

test('每个服务字段与类型符合契约', () => {
  const now = 1_760_000_000_000;
  const snapshot = createDemoSnapshot('degraded', now);

  for (const service of snapshot.services) {
    for (const field of REQUIRED_SERVICE_FIELDS) {
      assert.ok(field in service, `服务 ${service.id} 缺少字段 ${field}`);
    }
    assert.ok(SERVICE_GROUPS.includes(service.group), `未知分组 ${service.group}`);
    assert.ok(VALID_STATUSES.includes(service.status), `未知状态 ${service.status}`);
    assert.ok(service.latencyMs === null || typeof service.latencyMs === 'number');
    assert.ok(service.availability24h === null || typeof service.availability24h === 'number');
    assert.ok(service.availability7d === null || typeof service.availability7d === 'number');
    assert.ok(service.coverage === null || typeof service.coverage === 'number');
    assert.ok(isIso(service.observedAt));

    assert.equal(service.history.length, 48);
    for (const entry of service.history) {
      assert.ok(isIso(entry.at));
      assert.ok(VALID_STATUSES.includes(entry.status));
    }

    assert.ok(Array.isArray(service.detail));
    assert.ok(service.detail.length > 0);
    for (const row of service.detail) {
      assert.equal(typeof row.label, 'string');
      assert.equal(typeof row.value, 'string');
    }
  }
});

test('healthy 场景除业务未测试外全部正常', () => {
  const snapshot = createDemoSnapshot('healthy');
  for (const service of snapshot.services) {
    if (service.id === 'home-app') {
      assert.equal(service.status, 'untested');
    } else {
      assert.equal(service.status, 'healthy', `${service.id} 应为 healthy`);
    }
  }
});

test('lost 场景心跳与家侧观测超过 90 秒阈值', () => {
  const now = 1_760_000_000_000;
  const snapshot = createDemoSnapshot('lost', now);
  const heartbeatAge = now - Date.parse(snapshot.collector.lastHeartbeatAt);
  assert.ok(heartbeatAge > 90 * 1000, `心跳年龄 ${heartbeatAge} 应大于 90s`);

  for (const service of snapshot.services) {
    const age = now - Date.parse(service.observedAt);
    if (['gateway', 'dns', 'ipv4', 'ipv6', 'proxy', 'collectors'].includes(service.id)) {
      assert.ok(age > 90 * 1000, `${service.id} 家侧观测应过期`);
    }
  }
});

test('演示数据不含真实 IP、节点名或凭据字段', () => {
  for (const scenario of SCENARIOS.map((item) => item.id)) {
    const text = JSON.stringify(createDemoSnapshot(scenario));
    // 通用隐私规则：不内嵌任何真实地址或凭据取值。
    assert.doesNotMatch(text, /\b(?:\d{1,3}\.){3}\d{1,3}\b/, '不应包含 IP 地址');
    assert.doesNotMatch(text, /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/, '不应包含私钥块');
    assert.doesNotMatch(
      text,
      /\bssh-(?:rsa|ed25519|dss)\s+[A-Za-z0-9+/]{20,}={0,3}/,
      '不应包含 SSH 公钥材料',
    );
    assert.doesNotMatch(
      text,
      /\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b|\bAKIA[0-9A-Z]{16}\b|\bxox[baprs]-[A-Za-z0-9-]{10,}\b/,
      '不应包含 API 令牌',
    );
    const keys = new Set();
    const collect = (value) => {
      if (Array.isArray(value)) value.forEach(collect);
      else if (value && typeof value === 'object') {
        for (const [key, child] of Object.entries(value)) {
          keys.add(key);
          collect(child);
        }
      }
    };
    collect(createDemoSnapshot(scenario));
    for (const key of keys) {
      assert.doesNotMatch(key, /secret|token|password|passwd|private|credential/i);
    }
  }
});

test('多次调用返回相互独立的快照对象', () => {
  const first = createDemoSnapshot('healthy');
  const second = createDemoSnapshot('healthy');
  assert.notEqual(first, second);
  assert.notEqual(first.services[0], second.services[0]);
  assert.notEqual(first.metrics.latencyTrend, second.metrics.latencyTrend);

  first.services[0].status = 'down';
  first.metrics.latencyTrend[0].value = 9999;
  const third = createDemoSnapshot('healthy');
  assert.equal(third.services[0].status, 'healthy');
  assert.notEqual(third.metrics.latencyTrend[0].value, 9999);
});

test('未知场景名回退到默认场景', () => {
  const snapshot = createDemoSnapshot('does-not-exist');
  assert.equal(snapshot.scenario, DEFAULT_SCENARIO);
});

test('availability7d 在只有 24h 历史时显式为空', () => {
  const now = 1_760_000_000_000;
  for (const scenario of SCENARIOS.map((item) => item.id)) {
    const snapshot = createDemoSnapshot(scenario, now);
    for (const service of snapshot.services) {
      assert.equal(service.availability7d, null, `${scenario}/${service.id} availability7d 应为 null`);
    }
  }
});

test('history 最后样本与最新状态一致，lost 家侧缺口记为 unknown', () => {
  const now = 1_760_000_000_000;
  for (const scenario of SCENARIOS.map((item) => item.id)) {
    const snapshot = createDemoSnapshot(scenario, now);
    for (const service of snapshot.services) {
      const last = service.history[service.history.length - 1];
      const isPublic = service.id === 'headscale-base' || service.id === 'derp-base';
      const expected =
        service.status === 'untested'
          ? 'untested'
          : scenario === 'lost' && !isPublic
            ? 'unknown'
            : service.status;
      assert.equal(last.status, expected, `${scenario}/${service.id} 最后样本`);
    }
  }
});

test('合成历史包含历史故障，可用率不全是 100%', () => {
  const snapshot = createDemoSnapshot('healthy', 1_760_000_000_000);
  const values = snapshot.services
    .filter((service) => service.availability24h !== null)
    .map((service) => service.availability24h);
  assert.ok(values.length > 0);
  assert.ok(values.some((value) => value < 100), '至少一项 24h 可用率应低于 100%');
});

test('coverage 反映未知/未测试样本，且不进入可用率分母', () => {
  const snapshot = createDemoSnapshot('lost', 1_760_000_000_000);
  const collectors = snapshot.services.find((service) => service.id === 'collectors');
  assert.ok(collectors.coverage < 1, 'collectors 覆盖率应低于 1');
  assert.ok(collectors.availability24h !== null && collectors.availability24h <= 100);
});
