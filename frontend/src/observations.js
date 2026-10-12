// Raw single-probe history is independent of confirmed/current state.
const STATES = new Set(['healthy', 'degraded', 'down', 'unknown', 'untested', 'maintenance']);
const CODES = /^[a-z_]{1,48}$/;
const stamp = (v) => typeof v === 'string' && v.length <= 40 && Number.isFinite(Date.parse(v));
export function validateObservations(value, serviceId) {
  if (!value || value.serviceId !== serviceId || !stamp(value.from) || !stamp(value.to)
    || Date.parse(value.to) < Date.parse(value.from) || Date.parse(value.to) - Date.parse(value.from) > 7 * 86400000
    || !Array.isArray(value.observations) || value.observations.length > 1000
    || typeof value.nextCursor !== 'string' || value.nextCursor.length > 512 || !/^[\w-]*$/.test(value.nextCursor)
    || typeof value.truncated !== 'boolean' || value.truncated !== Boolean(value.nextCursor)) throw new Error('invalid observation page');
  const allowed = new Set(['observedAt', 'receivedAt', 'expiresAt', 'status', 'code', 'summary', 'latencyMs', 'pathMode', 'replay', 'evidence']);
  for (const row of value.observations) {
    if (!row || Object.keys(row).some((key) => !allowed.has(key)) || !STATES.has(row.status)
      || !CODES.test(row.code) || typeof row.summary !== 'string' || row.summary.length > 400
      || typeof row.replay !== 'boolean' || !stamp(row.receivedAt)
      || row.observedAt !== null && !stamp(row.observedAt) || row.expiresAt !== null && !stamp(row.expiresAt)
      || row.observedAt === null && !row.replay
      || row.latencyMs !== null && (!Number.isFinite(row.latencyMs) || row.latencyMs < 0 || row.latencyMs > 120000)
      || !['unknown', 'direct', 'relay', 'mixed', undefined].includes(row.pathMode)
      || !['healthy', 'degraded'].includes(row.status) && row.latencyMs !== null) throw new Error('invalid observation record');
    if (row.evidence && (Object.keys(row.evidence).some((key) => !['kind', 'at', 'historical'].includes(key))
      || !['job_success', 'job_failure', 'peer_success', 'peer_failure', 'control_message'].includes(row.evidence.kind)
      || !stamp(row.evidence.at) || typeof row.evidence.historical !== 'boolean')) throw new Error('invalid observation evidence');
  }
  return value;
}
export function beijingInput(value) {
  return stamp(value) ? new Date(Date.parse(value) + 8 * 3600000).toISOString().slice(0, 19) : '';
}
export function parseBeijingInput(value) {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(value)) return null;
  const normalized = value.length === 16 ? value + ':00' : value;
  const ms = Date.parse(normalized + '+08:00');
  if (!Number.isFinite(ms)) return null;
  const result = new Date(ms).toISOString();
  return beijingInput(result) === normalized ? result : null;
}
export function observationGaps(rows) {
  // Only annotate gaps between located records in this page. Expiry is an
  // evidence-validity boundary, NOT proof of downtime or duration-weighted SLA.
  const located = rows.filter((r) => r.observedAt !== null).slice().sort((a, b) => Date.parse(a.observedAt) - Date.parse(b.observedAt));
  const gaps = [];
  let coveredUntil = NaN;
  for (const row of located) {
    const at = Date.parse(row.observedAt), end = Date.parse(row.expiresAt);
    if (Number.isFinite(coveredUntil) && coveredUntil < at) gaps.push({ from: new Date(coveredUntil).toISOString(), to: row.observedAt });
    coveredUntil = Number.isFinite(end) ? Math.max(Number.isFinite(coveredUntil) ? coveredUntil : end, end) : NaN;
  }
  return gaps;
}
export function observationCounts(rows) {
  return rows.reduce((counts, row) => { counts[row.status]++; counts.replay += Number(row.replay); return counts; }, { healthy: 0, degraded: 0, down: 0, unknown: 0, untested: 0, maintenance: 0, replay: 0 });
}
export function demoObservations(serviceId, from, to) {
  const end = Date.parse(to), start = Date.parse(from);
  const observations = Array.from({ length: Math.min(120, Math.floor((end - start) / 30000)) }, (_, i) => {
    const at = end - i * 30000;
    return { observedAt: new Date(at).toISOString(), receivedAt: new Date(at + 100).toISOString(), expiresAt: new Date(at + 90000).toISOString(), status: i === 5 ? 'down' : 'healthy', code: i === 5 ? 'http_error' : 'ok', summary: '合成逐次观测；不代表真实设备。', latencyMs: i === 5 ? null : 15 + i % 20, replay: false, pathMode: 'unknown' };
  }).filter((_, i) => i < 20 || i > 26);
  return { serviceId, from, to, observations, nextCursor: '', truncated: false };
}
