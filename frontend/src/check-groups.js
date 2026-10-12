// Display-only grouping. Never alters backend ownership, status or evidence.
export const CHECK_GROUPS = [
  { id: 'network', label: '出口与局域网', icon: 'globe', description: 'IPv4 / IPv6、Ping 与网关管理', services: ['ipv4', 'ipv6', 'ipv4-baseline', 'ipv6-icmp', 'lan-router-ping', 'routeros-ssh'] },
  { id: 'dns', label: 'DNS 解析', icon: 'search', description: '解析应答、服务与规则更新', services: ['dns', 'dns-local-response', 'mosdns-process', 'httpdns-process', 'mosdns-metrics', 'dns-local-tcp', 'dns-rules-job'] },
  { id: 'proxy', label: '代理与上游', icon: 'route', description: '代理出口、上游解析与优化任务', services: ['proxy', 'proxy-process', 'outbound-dns-process', 'ssh-relay-process', 'proxy-control', 'outbound-dns', 'cdn-job'] },
  { id: 'access', label: '控制与接入', icon: 'shield', description: '控制消息、节点链路与隧道', services: ['headscale-base', 'headscale-control', 'tailscale-path', 'derp-base', 'tunnel-ready', 'tailscale-client', 'peer-poll', 'peer-pinger-process', 'ddns-job'] },
  { id: 'media', label: '媒体与共享', icon: 'home', description: '媒体服务、文件共享与整理任务', services: ['jellyfin-health', 'media-volume', 'jellyfin-lan', 'jellyfin-process', 'samba-process', 'samba-port', 'poster-job', 'metatube-health', 'movie-view-job', 'series-sync-job'] },
  { id: 'downloads', label: '下载与任务', icon: 'down', description: '下载服务、任务管理与配额', services: ['download-process', 'download-container', 'download-web', 'qbittorrent-process', 'qbittorrent-web', 'vertex-web', 'quota-job'] },
  { id: 'infrastructure', label: '虚拟化与存储', icon: 'server', description: '宿主、虚拟资源与系统容量', services: ['gateway', 'pve-node', 'dns-guest', 'media-guest', 'download-guest', 'proxy-guest', 'gate-guest', 'pt-guest', 'pve-root-space', 'thin-guard-job', 'portainer-api'] },
  { id: 'monitoring', label: '采集与面板', icon: 'pulse', description: '采集心跳、后端连接与边缘容量', services: ['collectors', 'backend-link', 'edge-space'] },
];
const explicit = new Map(CHECK_GROUPS.flatMap((group) => group.services.map((id) => [id, group.id])));
const fallback = { network: 'network', access: 'access', application: 'media', monitoring: 'monitoring' };
export function checkGroupFor(service) {
  return explicit.get(service.id) ?? (Object.hasOwn(fallback, service.group) ? fallback[service.group] : 'monitoring');
}
export function groupServices(services, groupId) {
  return groupId === 'all' ? services : services.filter((service) => checkGroupFor(service) === groupId);
}
export function summarizeCheckGroups(services) {
  return CHECK_GROUPS.map((group) => {
    const items = groupServices(services, group.id);
    const counts = { healthy: 0, degraded: 0, down: 0, unknown: 0, untested: 0, maintenance: 0 };
    for (const item of items) {
      const status = item.stale && item.status !== 'untested' ? 'unknown' : item.status;
      counts[Object.hasOwn(counts, status) ? status : 'unknown']++;
    }
    const status = ['down', 'degraded', 'unknown', 'untested', 'maintenance'].find((key) => counts[key])
      ?? (items.length ? 'healthy' : 'unknown');
    return { ...group, items, counts, status, total: items.length };
  });
}
export function parseCheckRoute(hash) {
  const [page, group] = hash.replace(/^#/, '').split('/');
  return { page, category: page === 'services' && (group === 'all' || CHECK_GROUPS.some((item) => item.id === group)) ? group : null };
}
