import { BASE_SERVICES } from './data.js';

const statuses = new Set(['healthy', 'degraded', 'down', 'unknown', 'untested', 'maintenance']);
const optionalNumber = (value, max) => value == null || (Number.isFinite(value) && value >= 0 && value <= max);
const text = (value, max = 300) => typeof value === 'string' && value.length <= max;
const validPoint = (point) => point && statuses.has(point.status) && Number.isFinite(Date.parse(point.at)) && optionalNumber(point.value, 120000);

// A missing API gets an honest empty shell, never synthetic health/history.
export function emptyLiveSnapshot() {
  return {
    version: 1, source: 'live', synthetic: false, apiAvailable: false,
    sampledAt: new Date().toISOString(), serverTime: null,
    collector: { lastHeartbeatAt: null, intervalSeconds: 30, staleAfterSeconds: 90 },
    collectors: [], incidents: [], metrics: { latencyTrend: [], coverageTrend: [] },
    services: BASE_SERVICES.map((entry) => ({
      id: entry.id, name: entry.name, group: entry.group,
      subtitle: entry.subtitle.replace('（合成）', ''), scope: entry.scope,
      status: entry.id === 'home-app' ? 'untested' : 'unknown',
      observedAt: null, latencyMs: null, availability24h: null, availability7d: null,
      probeLabel: '尚未取得真实观测', history: [], detail: [], pathMode: 'unknown',
      summary: '数据服务未提供新鲜结果；不据此判断家庭出口故障。',
    })),
  };
}

export function validateSnapshot(value) {
  if (!value || value.version !== 1 || value.source !== 'live' || value.synthetic !== false
    || !Number.isFinite(Date.parse(value.serverTime)) || !Array.isArray(value.services)
    || value.services.length > 64 || !Array.isArray(value.collectors) || value.collectors.length > 20
    || !Array.isArray(value.incidents) || value.incidents.length > 100 || !value.collector || !value.metrics
    || !Array.isArray(value.metrics.latencyTrend) || value.metrics.latencyTrend.length > 336)  {
    throw new Error('invalid snapshot');
  }
  const ids = new Set();
  for (const entry of value.services) {
    if (!entry || typeof entry.id !== 'string' || !/^[a-zA-Z0-9_-]{1,96}$/.test(entry.id)
      || ids.has(entry.id) || !statuses.has(entry.status) || typeof entry.name !== 'string'
      || !text(entry.subtitle) || !text(entry.scope) || !text(entry.summary) || !text(entry.probeLabel)
      || !['network', 'access', 'application', 'monitoring'].includes(entry.group)
      || !optionalNumber(entry.latencyMs, 120000) || !optionalNumber(entry.observedAgeSeconds, 7776000)
      || !optionalNumber(entry.usagePercent, 100) || !optionalNumber(entry.readyConnections, 1000)
      || (entry.readyConnections != null && !Number.isInteger(entry.readyConnections))
      || (entry.pathMode != null && !['unknown', 'direct', 'relay', 'mixed'].includes(entry.pathMode))
      || !Array.isArray(entry.history) || entry.history.length > 336 || !Array.isArray(entry.detail) || entry.detail.length > 20
      || entry.detail.some((row) => !row || !text(row.label) || !text(row.value))) {
      throw new Error('invalid service');
    }
    ids.add(entry.id);
    for (const point of entry.history) {
      if (!validPoint(point)) throw new Error('invalid history');
    }
  }
  for (const entry of value.collectors) {
    if (!entry || !text(entry.id, 96) || !text(entry.label) || !['family', 'public'].includes(entry.kind) || !optionalNumber(entry.heartbeatAgeSeconds, 7776000) || !Number.isFinite(entry.staleAfterSeconds) || entry.staleAfterSeconds <= 0) throw new Error('invalid collector');
  }
  for (const entry of value.incidents) {
    if (!entry || !text(entry.title) || !text(entry.description) || !Number.isFinite(Date.parse(entry.startedAt)) || !['resolved', 'investigating', 'info'].includes(entry.status) || !Array.isArray(entry.affectedServiceIds) || entry.affectedServiceIds.length > 64) throw new Error('invalid incident');
  }
  for (const point of value.metrics.latencyTrend ?? []) {
    if (!Number.isFinite(point.value) || point.value < 0 || !Number.isFinite(Date.parse(point.at))) throw new Error('invalid trend');
  }
  return { ...value, apiAvailable: true };
}

export async function fetchSnapshot({ fetcher = fetch, signal } = {}) {
  const response = await fetcher('/api/v1/status', { cache: 'no-store', credentials: 'same-origin', signal });
  if (!response.ok) throw new Error('data unavailable');
  return validateSnapshot(await response.json());
}

export function unavailableSnapshot(previous) {
  return { ...previous, apiAvailable: false };
}

// Anchor live ageing to server time + monotonic elapsed time, not the user's wall clock.
export function createClock(snapshot, monotonicNow = () => performance.now()) {
  const start = monotonicNow();
  const server = Date.parse(snapshot.serverTime);
  return () => Number.isFinite(server) ? server + Math.max(0, monotonicNow() - start) : Date.now();
}
