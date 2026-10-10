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

export function latencyParts(value) {
  const formatted = formatLatency(value);
  if (formatted === '—') return { value: '—', unit: '暂无新鲜结果' };
  const [amount, unit] = formatted.split(' ');
  return { value: amount, unit };
}
