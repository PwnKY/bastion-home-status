// Opt-in local visual fixture. Generated times/measurements only, never copied
// from the private runtime. Production cannot enable this via a URL parameter.
import { createDemoSnapshot } from './data.js';
import { CHECK_GROUPS } from './check-groups.js';
import { getHistoryStats } from './model.js';

const NAMES = {
  ipv4: 'IPv4 HTTPS 耗时', ipv6: 'IPv6 HTTPS 耗时', 'ipv4-baseline': '国内 IPv4 DNS 基准', 'ipv6-icmp': '国内 IPv6 Ping 基准', 'lan-router-ping': '局域网路由器 Ping', 'routeros-ssh': '网关管理握手',
  dns: 'DNS 解析', 'dns-local-response': 'DNS 本机应答', 'mosdns-process': 'DNS 服务进程', 'httpdns-process': '上游解析适配进程', 'mosdns-metrics': 'DNS 指标接口', 'dns-local-tcp': 'DNS 本机 TCP 应答', 'dns-rules-job': 'DNS 规则更新任务',
  proxy: '代理出口', 'proxy-process': '代理服务进程', 'outbound-dns-process': '代理上游 DNS 进程', 'ssh-relay-process': 'SSH 中转进程', 'proxy-control': '代理控制摘要接口', 'outbound-dns': '代理上游 DNS 应答', 'cdn-job': '内容分发优化任务',
  'headscale-base': 'Headscale 基础健康', 'headscale-control': '近期控制通信', 'tailscale-path': '节点数据链路', 'derp-base': 'DERP 基础可达', 'tunnel-ready': 'Cloudflare 隧道就绪', 'tailscale-client': 'Tailscale 本机状态', 'peer-poll': '节点状态轮询', 'peer-pinger-process': '既有节点探测进程', 'ddns-job': '动态地址更新任务',
  'jellyfin-health': 'Jellyfin 本机健康', 'media-volume': '共享媒体空间', 'jellyfin-lan': '媒体接口家庭侧可达', 'jellyfin-process': '媒体服务进程', 'samba-process': '文件共享进程', 'samba-port': '文件共享端口', 'poster-job': '媒体海报整理任务', 'metatube-health': '媒体元数据接口', 'movie-view-job': '媒体视图同步任务', 'series-sync-job': '剧集视图同步任务',
  'download-process': '下载网页进程', 'download-container': '下载容器运行时', 'download-web': '下载网页可达', 'qbittorrent-process': '任务下载进程', 'qbittorrent-web': '任务下载网页可达', 'vertex-web': '任务管理网页可达', 'quota-job': '任务配额整理',
  gateway: '网关虚拟机运行态', 'pve-node': '虚拟化管理状态', 'dns-guest': 'DNS 容器运行态', 'media-guest': '媒体容器运行态', 'download-guest': '下载容器运行态', 'proxy-guest': '代理容器运行态', 'gate-guest': '接入容器运行态', 'pt-guest': '任务容器运行态', 'pve-root-space': '虚拟化系统容量', 'thin-guard-job': '精简池保护任务', 'portainer-api': '容器管理基础接口',
  collectors: '家庭采集链路', 'backend-link': '边缘至数据节点', 'edge-space': '边缘磁盘容量',
};
export function createGroupPreview(scenario = 'degraded', now = Date.now()) {
  const base = createDemoSnapshot(scenario, now);
  const collectors = Array.from({ length: 8 }, (_, index) => ({ id: `preview-source-${index}`, label: `来源 ${index + 1}（合成）`, kind: index === 7 ? 'public' : 'family', intervalSeconds: 30, staleAfterSeconds: 90, heartbeatAgeSeconds: scenario === 'lost' && index !== 7 ? 300 : 12, lastHeartbeatAt: new Date(now - (scenario === 'lost' && index !== 7 ? 300000 : 12000)).toISOString() }));
  const services = CHECK_GROUPS.flatMap((group) => group.services.map((id, index) => {
    const template = base.services.find((entry) => entry.id === id) ?? base.services[0];
    const unknown = scenario !== 'healthy' && ['tailscale-path', 'poster-job'].includes(id);
    const status = unknown ? 'unknown' : scenario !== 'healthy' && id === 'media-volume' ? 'degraded' : 'healthy';
    const sourceIndex = ['headscale-base', 'derp-base', 'backend-link', 'edge-space'].includes(id) ? 7 : index % 7;
    const capacity = ['media-volume', 'edge-space', 'pve-root-space'].includes(id);
    const timed = ['ipv4', 'ipv6', 'ipv4-baseline', 'ipv6-icmp', 'lan-router-ping', 'dns', 'dns-local-response', 'dns-local-tcp', 'proxy', 'backend-link'].includes(id);
    const latencyMs = timed ? 3 + (index * 7) % 40 : null;
    const history = template.history.map((point, n) => ({ at: point.at, status: n < 5 ? 'unknown' : status, value: timed && status === 'healthy' && n >= 5 ? latencyMs : null }));
    const stats = getHistoryStats(history);
    const evidence = unknown ? { kind: id === 'poster-job' ? 'job_success' : 'peer_success', at: new Date(now - 86400000).toISOString(), historical: true } : undefined;
    return { ...template, id, name: NAMES[id], subtitle: group.description, group: ['media', 'downloads'].includes(group.id) ? 'application' : group.id === 'access' ? 'access' : ['infrastructure', 'monitoring'].includes(group.id) ? 'monitoring' : 'network', collectorId: collectors[sourceIndex].id, probeLabel: collectors[sourceIndex].label, staleAfterSeconds: 90, status, latencyMs: status === 'unknown' ? null : latencyMs, usagePercent: capacity ? (id === 'media-volume' && scenario !== 'healthy' ? 92 : 18) : undefined, readyConnections: id === 'tunnel-ready' ? 2 : undefined, pathMode: 'unknown', observedAt: collectors[sourceIndex].lastHeartbeatAt, scope: '合成视觉预览；只展示该检测的范围，不证明完整业务可用。', summary: unknown ? '合成样本：缺少新鲜证据，旧记录仅作参考。' : '合成样本；不是实际设备检测结果。', evidence, history, availability24h: stats.uptimePercent, availability7d: null, coverage: stats.coverageRatio, detail: [{ label: '结论限制', value: '合成视觉预览，不连接真实设备。' }, { label: '补充限制', value: '分组不会改变单项检测、来源时间或新鲜度。' }] };
  }));
  return { ...base, services, collectors, collector: collectors[0], disclaimer: '60 项 / 8 来源合成视觉预览，非线上状态。', metrics: { ...base.metrics, latencyTrend: services.find((entry) => entry.id === 'ipv4').history }, incidents: scenario === 'healthy' ? [] : [{ id: 'preview-capacity', title: '共享容量需关注（演示）', description: '合成容量预警；不意味着媒体播放已经中断。', status: 'investigating', startedAt: new Date(now - 7200000).toISOString(), resolvedAt: null, affectedServiceIds: ['media-volume'] }] };
}
