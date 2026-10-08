// 演示数据层（demo only）。全部为合成数据：不含真实 IP、节点名、域名或任何凭据字段。
// 该模块不读取环境、不发起网络请求、不连接任何后端；仅用于前端脚手架与单元测试。
import { getHistoryStats } from './model.js';

/** 可选演示场景（仅元数据，便于父代理直接渲染选择器）。 */
export const SCENARIOS = [
  { id: 'healthy', label: '正常（演示）' },
  { id: 'degraded', label: '降级（演示）' },
  { id: 'lost', label: '采集器失联（演示）' },
];

export const DEFAULT_SCENARIO = 'degraded';

const HISTORY_LENGTH = 48; // 24 小时 × 每 30 分钟一个样本
const SAMPLE_INTERVAL_MS = 30 * 60 * 1000;
const COLLECTOR_INTERVAL_SECONDS = 30;
const COLLECTOR_STALE_AFTER_SECONDS = 90;
const FRESH_FAMILY_AGE_MS = 12 * 1000;
const FRESH_PUBLIC_AGE_MS = 20 * 1000;
const LOST_AGE_MS = 300 * 1000; // 5 分钟前，超过 90 秒阈值
const LOST_PUBLIC_AGE_MS = 18 * 1000; // 公网采集器独立观测仍然新鲜

const DEMO_DISCLAIMER =
  '全部为合成演示数据（source=demo），不代表任何真实设备、网络、链路或业务状态。';
const DEMO_PROBE = {
  family: '家庭采集器（合成）',
  public: '公网采集器（合成）',
  collector: '心跳监测（合成）',
};

const BASE_SERVICES = [
  {
    id: 'gateway',
    name: '家庭网关',
    subtitle: '网关资源、接口与默认路由（只读）',
    group: 'network',
    entity: 'family',
    scope: '家庭网关 / 设备与接口观测',
    limits: ['只读采集，不开放 REST/SNMP/管理端口', '不暴露完整配置或连接明细'],
  },
  {
    id: 'dns',
    name: 'DNS 解析',
    subtitle: '真实解析应答与耗时',
    group: 'network',
    entity: 'family',
    scope: '家庭 DNS / 解析语义',
    limits: ['区分系统解析与直接解析', 'Fake-IP 不单独判定为错误'],
  },
  {
    id: 'ipv4',
    name: 'IPv4 出口',
    subtitle: 'IPv4 直出与代理路径对照',
    group: 'network',
    entity: 'family',
    scope: '家庭出口 / IPv4 对照',
    limits: ['与 IPv6 分开判定', '单目标失败不直接判全网中断'],
  },
  {
    id: 'ipv6',
    name: 'IPv6 出口',
    subtitle: 'IPv6 独立判定（不与 IPv4 合并）',
    group: 'network',
    entity: 'family',
    scope: '家庭出口 / IPv6 对照',
    limits: ['IPv6 降级不等同于完全断开', 'IPv4 不受 IPv6 异常牵连'],
  },
  {
    id: 'proxy',
    name: '代理出口',
    subtitle: '明确经过代理的对照路径',
    group: 'network',
    entity: 'family',
    scope: '家庭出口 / 代理路径',
    limits: ['保留解析答案与实际连接目标', '一次 HTTP 成功不推断走了代理'],
  },
  {
    id: 'headscale-base',
    name: 'Headscale 基础健康',
    subtitle: '服务端基础健康接口（合成）',
    group: 'access',
    entity: 'public',
    scope: '公网服务 / 控制端基础健康',
    limits: ['基础健康通过不代表节点同步正常', '与家庭控制连接分开判定'],
  },
  {
    id: 'headscale-control',
    name: 'Headscale 家庭控制连接',
    subtitle: '家庭侧控制连接与同步证据',
    group: 'access',
    entity: 'family',
    scope: '远程接入 / 控制层',
    limits: ['控制连接异常不直接判 Tailscale 全部链路中断', '需结合同步与数据层证据'],
  },
  {
    id: 'tailscale-path',
    name: 'Tailscale 数据链路',
    subtitle: '实测数据路径（直连 / 中继）',
    group: 'access',
    entity: 'family',
    scope: '远程接入 / 实测数据层',
    limits: ['仅中继可达时标记直连能力降级', '链路可达不代表子网路由或业务正常'],
  },
  {
    id: 'derp-base',
    name: 'DERP 基础可达',
    subtitle: '仅基础可达性，不代表中继转发已验证',
    group: 'access',
    entity: 'public',
    scope: '公网服务 / DERP 基础接口',
    limits: ['仅基础可达性', '不宣称中继转发能力已验证'],
  },
  {
    id: 'tunnel-ready',
    name: 'Cloudflare Tunnel 就绪',
    subtitle: '就绪连接数（不等于回源业务可用）',
    group: 'access',
    entity: 'family',
    scope: '远程接入 / Tunnel 就绪',
    limits: ['就绪连接不等于具体回源业务可用', 'Tunnel 与回源分别展示'],
  },
  {
    id: 'home-app',
    name: '家庭应用',
    subtitle: '具体回家业务（当前未测试）',
    group: 'application',
    entity: 'family',
    scope: '家庭业务 / 端到端可用性',
    limits: ['尚未测试', '不代表可用或不可用'],
  },
  {
    id: 'collectors',
    name: '采集器健康',
    subtitle: '心跳、数据新鲜度与时间同步',
    group: 'monitoring',
    entity: 'collector',
    scope: '监测健康 / 采集链路',
    limits: ['心跳过期显示未知，不批量伪造服务故障', '补传不能伪装成刚完成的检测'],
  },
];

// 每个场景下各服务的“上报状态”。lost 场景保留最后一次已知上报值，
// 真正的“未知”由 model.deriveView 依据 observedAt 年龄派生，避免把失联直接写成 down。
const STATUS_MATRIX = {
  healthy: {
    gateway: 'healthy',
    dns: 'healthy',
    ipv4: 'healthy',
    ipv6: 'healthy',
    proxy: 'healthy',
    'headscale-base': 'healthy',
    'headscale-control': 'healthy',
    'tailscale-path': 'healthy',
    'derp-base': 'healthy',
    'tunnel-ready': 'healthy',
    'home-app': 'untested',
    collectors: 'healthy',
  },
  degraded: {
    gateway: 'healthy',
    dns: 'healthy',
    ipv4: 'healthy',
    ipv6: 'degraded',
    proxy: 'degraded',
    'headscale-base': 'healthy',
    'headscale-control': 'degraded',
    'tailscale-path': 'degraded',
    'derp-base': 'healthy',
    'tunnel-ready': 'healthy',
    'home-app': 'untested',
    collectors: 'healthy',
  },
  lost: {
    gateway: 'healthy',
    dns: 'healthy',
    ipv4: 'healthy',
    ipv6: 'degraded',
    proxy: 'degraded',
    'headscale-base': 'healthy',
    'headscale-control': 'degraded',
    'tailscale-path': 'degraded',
    'derp-base': 'healthy',
    'tunnel-ready': 'healthy',
    'home-app': 'untested',
    collectors: 'unknown',
  },
};

const LATENCY_MATRIX = {
  healthy: {
    gateway: 3,
    dns: 9,
    ipv4: 14,
    ipv6: 38,
    proxy: 52,
    'headscale-base': 130,
    'headscale-control': 95,
    'tailscale-path': 42,
    'derp-base': 48,
    'tunnel-ready': 26,
    'home-app': null,
    collectors: 1,
  },
  degraded: {
    gateway: 4,
    dns: 11,
    ipv4: 16,
    ipv6: 240,
    proxy: 190,
    'headscale-base': 140,
    'headscale-control': 310,
    'tailscale-path': 230,
    'derp-base': 55,
    'tunnel-ready': 31,
    'home-app': null,
    collectors: 2,
  },
  lost: {
    gateway: 4,
    dns: 11,
    ipv4: 16,
    ipv6: 240,
    proxy: 190,
    'headscale-base': 138,
    'headscale-control': 310,
    'tailscale-path': 230,
    'derp-base': 53,
    'tunnel-ready': 31,
    'home-app': null,
    collectors: null,
  },
};

const SEED = { healthy: 101, degraded: 202, lost: 303 };

function mulberry32(seed) {
  let state = seed >>> 0;
  return function next() {
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function iso(ms) {
  return new Date(ms).toISOString();
}

function historyMixFor(status) {
  switch (status) {
    case 'healthy':
      // 历史中保留少量历史故障，避免可用率永远显示 100%（当前 healthy 不矛盾：已恢复）。
      return { healthy: 44, degraded: 2, down: 2 };
    case 'degraded':
      return { degraded: 38, healthy: 8, down: 2 };
    case 'down':
      return { down: 30, degraded: 10, healthy: 8 };
    case 'maintenance':
      return { maintenance: 12, healthy: 30, degraded: 6 };
    case 'untested':
      return { untested: 48 };
    case 'unknown':
      return { healthy: 33, down: 5, unknown: 10 };
    default:
      return { healthy: 48 };
  }
}

/**
 * 生成 24 小时历史。最后一个样本固定为 latestStatus，确保与“最新上报/派生状态”一致；
 * 其余样本在给定混合上确定性打乱。lost 场景会把最近的观测缺口标为 unknown，而不是 down。
 */
function buildHistory(now, status, seed, latestStatus = status) {
  const mix = historyMixFor(status);
  const pool = [];
  for (const [key, count] of Object.entries(mix)) {
    for (let i = 0; i < count; i += 1) pool.push(key);
  }
  while (pool.length < HISTORY_LENGTH) pool.push(latestStatus);
  const samples = pool.slice(0, HISTORY_LENGTH);
  const rng = mulberry32(seed);
  // 只打乱前 HISTORY_LENGTH-1 个，最后一个固定为 latestStatus。
  for (let i = HISTORY_LENGTH - 2; i > 0; i -= 1) {
    const j = Math.floor(rng() * (i + 1));
    const tmp = samples[i];
    samples[i] = samples[j];
    samples[j] = tmp;
  }
  samples[HISTORY_LENGTH - 1] = latestStatus;
  const start = now - (HISTORY_LENGTH - 1) * SAMPLE_INTERVAL_MS;
  return samples.map((entryStatus, index) => ({
    at: iso(start + index * SAMPLE_INTERVAL_MS),
    status: entryStatus,
  }));
}

function summaryFor(status) {
  switch (status) {
    case 'healthy':
      return '合成演示数据：该项显示为正常。';
    case 'degraded':
      return '合成演示数据：该项降级，功能可用但未达正常水平。';
    case 'down':
      return '合成演示数据：该项标记为故障。';
    case 'maintenance':
      return '合成演示数据：处于维护窗口内。';
    case 'untested':
      return '合成演示数据：该项尚未测试，不代表可用或不可用。';
    case 'unknown':
    default:
      return '合成演示数据：缺少新鲜观测，结论未知；这不代表 WAN 或业务已中断。';
  }
}

function probeLabelFor(entity) {
  return DEMO_PROBE[entity] ?? DEMO_PROBE.family;
}

function availabilityFor(history, status) {
  const stats = getHistoryStats(history);
  if (status === 'untested') {
    return { availability24h: null, availability7d: null, coverage: stats.coverageRatio };
  }
  return {
    availability24h: stats.uptimePercent,
    // 演示历史只有 24 小时；没有 7 天样本时显式置空，而不是把 24h 值复制成 7d 分母。
    availability7d: null,
    coverage: stats.coverageRatio,
  };
}

function buildService(now, scenario, def, index) {
  const status = STATUS_MATRIX[scenario][def.id];
  // lost 场景：家侧/采集侧观测过期，最近的观测缺口记为 unknown（而非 down）；
  // 独立公网采集器仍然新鲜，保留其上报状态。untested 始终保持 untested。
  const staleByScenario = scenario === 'lost' && def.entity !== 'public';
  const latestHistoryStatus =
    status === 'untested' ? 'untested' : staleByScenario ? 'unknown' : status;
  const history = buildHistory(now, status, SEED[scenario] + index * 17, latestHistoryStatus);
  const stats = getHistoryStats(history);
  const ageMs = def.entity === 'public'
    ? (scenario === 'lost' ? LOST_PUBLIC_AGE_MS : FRESH_PUBLIC_AGE_MS)
    : (scenario === 'lost' ? LOST_AGE_MS : FRESH_FAMILY_AGE_MS);
  const observedAt = iso(now - ageMs);
  const availability = availabilityFor(history, status);

  return {
    id: def.id,
    name: def.name,
    subtitle: def.subtitle,
    group: def.group,
    status,
    latencyMs: LATENCY_MATRIX[scenario][def.id] ?? null,
    availability24h: availability.availability24h,
    availability7d: availability.availability7d,
    coverage: availability.coverage,
    observedAt,
    probeLabel: probeLabelFor(def.entity),
    scope: def.scope,
    summary: summaryFor(status),
    history,
    detail: [
      { label: '状态来源', value: '合成演示数据（source=demo）' },
      { label: '检测范围', value: def.scope },
      { label: '最近观测', value: observedAt },
      { label: '结论限制', value: def.limits[0] },
      { label: '补充限制', value: def.limits[1] },
      { label: '降级占比（24h）', value: stats.degradePercent === null ? '—' : `${stats.degradePercent}%` },
    ],
  };
}

function buildLatencyTrend(now, scenario) {
  const base = scenario === 'healthy' ? 12 : scenario === 'lost' ? 30 : 28;
  const rng = mulberry32(SEED[scenario] + 7);
  const start = now - (HISTORY_LENGTH - 1) * SAMPLE_INTERVAL_MS;
  return Array.from({ length: HISTORY_LENGTH }, (_, index) => ({
    at: iso(start + index * SAMPLE_INTERVAL_MS),
    value: Math.round(base + rng() * base * 0.6),
  }));
}

function buildCoverageTrend(now, scenario) {
  const start = now - (HISTORY_LENGTH - 1) * SAMPLE_INTERVAL_MS;
  return Array.from({ length: HISTORY_LENGTH }, (_, index) => {
    const at = iso(start + index * SAMPLE_INTERVAL_MS);
    if (scenario === 'lost' && index >= HISTORY_LENGTH - 2) {
      return { at, value: 0 };
    }
    return { at, value: 1 };
  });
}

function buildCollector(scenario, now) {
  const ageMs = scenario === 'lost' ? LOST_AGE_MS : FRESH_FAMILY_AGE_MS;
  return {
    lastHeartbeatAt: iso(now - ageMs),
    intervalSeconds: COLLECTOR_INTERVAL_SECONDS,
    staleAfterSeconds: COLLECTOR_STALE_AFTER_SECONDS,
  };
}

function buildIncidents(scenario, now) {
  const day = 24 * 60 * 60 * 1000;
  if (scenario === 'healthy') {
    return [
      {
        id: 'demo-inc-001',
        title: 'DNS 解析抖动已恢复（演示）',
        description: '合成事件：历史解析抖动已恢复，仅用于演示事故时间线。',
        status: 'resolved',
        startedAt: iso(now - 3 * day),
        resolvedAt: iso(now - 3 * day + 40 * 60 * 1000),
        affectedServiceIds: ['dns'],
      },
    ];
  }
  if (scenario === 'degraded') {
    return [
      {
        id: 'demo-inc-002',
        title: 'IPv6 出口降级（演示）',
        description: '合成事件：IPv6 路径降级，IPv4 未受牵连；仅用于演示。',
        status: 'investigating',
        startedAt: iso(now - 6 * 60 * 60 * 1000),
        resolvedAt: null,
        affectedServiceIds: ['ipv6', 'proxy'],
      },
      {
        id: 'demo-inc-003',
        title: '控制连接短时异常（演示）',
        description: '合成事件：家庭控制连接出现短时异常后恢复。',
        status: 'resolved',
        startedAt: iso(now - 2 * day),
        resolvedAt: iso(now - 2 * day + 25 * 60 * 1000),
        affectedServiceIds: ['headscale-control'],
      },
    ];
  }
  return [
    {
      id: 'demo-inc-004',
      title: '采集器心跳缺失（演示）',
      description: '合成事件：家庭采集器心跳超过阈值，家侧观测置为未知，不判定 WAN 中断。',
      status: 'investigating',
      startedAt: iso(now - LOST_AGE_MS),
      resolvedAt: null,
      affectedServiceIds: ['collectors', 'gateway', 'dns', 'ipv4', 'ipv6', 'proxy'],
    },
    {
      id: 'demo-inc-005',
      title: '历史 IPv6 降级记录（演示）',
      description: '合成事件：采集器失联前的 IPv6 降级记录，保留供时间线演示。',
      status: 'info',
      startedAt: iso(now - 8 * 60 * 60 * 1000),
      resolvedAt: null,
      affectedServiceIds: ['ipv6'],
    },
  ];
}

function normalizeScenario(scenario) {
  return SCENARIOS.some((item) => item.id === scenario) ? scenario : DEFAULT_SCENARIO;
}

/**
 * 生成一份完整演示快照。纯函数：不读环境、不访问网络，仅依据 now 生成确定性数据。
 * @param {'healthy'|'degraded'|'lost'} [scenario='degraded']
 * @param {number} [now=Date.now()]
 */
export function createDemoSnapshot(scenario = DEFAULT_SCENARIO, now = Date.now()) {
  const resolved = normalizeScenario(scenario);
  const services = BASE_SERVICES.map((def, index) => buildService(now, resolved, def, index));

  return {
    sampledAt: iso(now),
    source: 'demo',
    synthetic: true,
    disclaimer: DEMO_DISCLAIMER,
    scenario: resolved,
    collector: buildCollector(resolved, now),
    services,
    metrics: {
      latencyTrend: buildLatencyTrend(now, resolved),
      coverageTrend: buildCoverageTrend(now, resolved),
    },
    incidents: buildIncidents(resolved, now),
  };
}
