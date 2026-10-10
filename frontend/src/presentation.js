// Pure presentation helpers: never alter probe status, freshness or historical evidence.
import { formatLatency } from './model.js';

export function filterServices(services, { group = 'all', statusFilter = 'all', search = '' } = {}) {
  const query = search.trim().toLowerCase();
  return services.filter((entry) => (group === 'all' || entry.group === group)
    && (statusFilter === 'all' || entry.status === statusFilter)
    && (!query || `${entry.name} ${entry.subtitle ?? ''}`.toLowerCase().includes(query)));
}

export function previewServices(services, limit = 8) {
  const order = { down: 0, degraded: 1, unknown: 2, healthy: 3, untested: 4, maintenance: 5 };
  // Only the bounded overview is ranked. The full list keeps its stable backend order.
  return services.map((entry, index) => ({ entry, index }))
    .sort((a, b) => (order[a.entry.status] ?? 6) - (order[b.entry.status] ?? 6) || a.index - b.index)
    .slice(0, limit).map(({ entry }) => entry);
}

// Evidence is a source-clock reference. It never supplies status or current RTT.
export function evidencePresentation(entry) {
  const evidence = entry?.evidence;
  const labels = { job_success: '任务完成', job_failure: '任务失败', peer_success: '节点探测成功', peer_failure: '节点探测失败', control_message: '控制观测窗口起点' };
  if (!evidence || !Object.hasOwn(labels, evidence.kind) || !Number.isFinite(Date.parse(evidence.at))) return null;
  const referenceOnly = evidence.historical || entry.stale || !['healthy', 'degraded', 'down'].includes(entry.status);
  return { label: labels[evidence.kind], at: evidence.at, qualifier: referenceOnly ? '仅作参考' : '来源记录' };
}

export function exitBaseline(services, family) {
  // Explicit scope choice, never a success-based fallback or history substitution.
  const ping = services.find((entry) => entry.id === `${family}-icmp`);
  return ping ? { id: ping.id, label: 'ICMP Ping 基准' } : { id: `${family}-baseline`, label: '国内 DNS 基准' };
}

export function latencyParts(value) {
  const formatted = formatLatency(value);
  if (formatted === '—') return { value: '—', unit: '暂无新鲜结果' };
  const [amount, unit] = formatted.split(' ');
  return { value: amount, unit };
}
