import './styles.css';
import { createDemoSnapshot, SCENARIOS } from './data.js';
import { deriveView, formatLatency, getHistoryStats } from './model.js';
import { createClock, emptyLiveSnapshot, fetchSnapshot, unavailableSnapshot } from './api.js';
import { trendSegments } from './trend.js';
import { filterServices, previewServices, latencyParts, exitBaseline } from './presentation.js';

// Browsers read same-origin summaries only. Device interfaces and credentials stay private.
const app = document.querySelector('#app');
const dialog = document.querySelector('#service-dialog');
const NAV = [
  { id: 'overview', label: '网络总览', icon: 'grid', caption: 'OVERVIEW' },
  { id: 'services', label: '服务观测', icon: 'layers', caption: 'SERVICES' },
  { id: 'paths', label: '连接路径', icon: 'route', caption: 'CONNECTIVITY' },
  { id: 'events', label: '事件时间线', icon: 'clock', caption: 'TIMELINE' },
];
const GROUPS = [
  { id: 'all', label: '全部服务' },
  { id: 'network', label: '家庭网络' },
  { id: 'access', label: '远程接入' },
  { id: 'application', label: '家庭业务' },
  { id: 'monitoring', label: '采集健康' },
];
const STATUS = {
  healthy: { label: '正常', short: '正常', class: 'healthy' },
  degraded: { label: '降级', short: '降级', class: 'degraded' },
  down: { label: '故障', short: '故障', class: 'down' },
  unknown: { label: '未知', short: '未知', class: 'unknown' },
  untested: { label: '未测试', short: '未测', class: 'untested' },
  maintenance: { label: '维护中', short: '维护', class: 'maintenance' },
};
const PATHS = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  layers: '<path d="m12 3 9 5-9 5-9-5 9-5Z"/><path d="m3 12 9 5 9-5M3 16l9 5 9-5"/>',
  route: '<circle cx="5" cy="5" r="2"/><circle cx="19" cy="19" r="2"/><path d="M7 5h8a4 4 0 0 1 0 8H9a4 4 0 0 0 0 8h8"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  home: '<path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V10Z"/><path d="M9 21V12h6v9"/>',
  arrow: '<path d="M5 12h14m-5-5 5 5-5 5"/>',
  diagonal: '<path d="M6 18 18 6M6 6h12v12"/>',
  chevron: '<path d="m9 5 7 7-7 7"/>',
  down: '<path d="m7 10 5 5 5-5"/>',
  refresh: '<path d="M20 7v5h-5M4 17v-5h5"/><path d="M6 7a7 7 0 0 1 11-2l3 3M4 16l3 3a7 7 0 0 0 11-2"/>',
  globe: '<circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18"/>',
  shield: '<path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6l8-3Z"/><path d="m8 12 3 3 5-6"/>',
  activity: '<path d="M2 12h5l3-8 4 16 3-8h5"/>',
  server: '<rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01M12 6.5h5M12 17.5h5"/>',
  link: '<path d="m9 15 6-6M8 16l-2 2a4 4 0 0 1-6-6l4-4a4 4 0 0 1 6 0M16 8l2-2a4 4 0 0 1 6 6l-4 4a4 4 0 0 1-6 0" transform="translate(2 0) scale(.83 1)"/>',
  cloud: '<path d="M6 19a5 5 0 0 1-1-10 7 7 0 0 1 13-2 6 6 0 0 1 0 12H6Z"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/>',
  close: '<path d="m6 6 12 12M6 18 18 6"/>',
  check: '<path d="m5 12 4 4L19 6"/>',
  alert: '<path d="m12 3 10 18H2L12 3Z"/><path d="M12 9v4M12 17h.01"/>',
  pause: '<path d="M9 5v14M15 5v14"/>',
  pulse: '<path d="M3 12h3l3-7 5 14 3-7h4"/>',
};
const SERVICE_ICONS = { gateway: 'server', dns: 'globe', ipv4: 'globe', ipv6: 'globe', 'ipv4-baseline': 'globe', 'ipv6-baseline': 'globe', 'ipv6-icmp': 'activity', proxy: 'route', 'headscale-base': 'shield', 'headscale-control': 'link', 'tailscale-path': 'route', 'derp-base': 'server', 'tunnel-ready': 'cloud', 'home-app': 'home', collectors: 'pulse' };
const e = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
const icon = (name, extra = '') => `<svg class="icon ${extra}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${PATHS[name] || PATHS.activity}</svg>`;
const fmt = (value, options) => value && Number.isFinite(Date.parse(value)) ? new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', ...options }).format(new Date(value)) : '—';
const time = (value) => fmt(value, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
const dateTime = (value) => fmt(value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false });
const percent = (value) => value === null || value === undefined ? '—' : `${Number(value).toFixed(1)}%`;
const statusOf = (status) => STATUS[status] || STATUS.unknown;
const badge = (status, text) => `<span class="status-badge ${statusOf(status).class}"><span class="status-dot"></span>${e(text || statusOf(status).label)}</span>`;
const ageText = (value) => {
  if (!value || !Number.isFinite(Date.parse(value))) return '尚无观测';
  const age = Math.max(0, Math.floor((getNow() - Date.parse(value)) / 1000));
  return age < 60 ? `${age} 秒前` : age < 3600 ? `${Math.floor(age / 60)} 分钟前` : `${Math.floor(age / 3600)} 小时前`;
};
const age = (value) => `<span data-age="${e(value)}" title="${e(dateTime(value))}（北京时间）">${ageText(value)}</span>`;

const isDemo = import.meta.env.VITE_ALLOW_DEMO === 'true' && new URLSearchParams(location.search).get('demo') === '1';
const state = { scenario: 'degraded', page: 'overview', group: 'network', statusFilter: 'all', search: '', range: '24h', eventFilter: 'all', menuOpen: false, collectorsOpen: false, nextStatusFilter: null };
let snapshot = isDemo ? createDemoSnapshot(state.scenario) : emptyLiveSnapshot();
let liveClock = createClock(snapshot);
const getNow = () => isDemo ? Date.now() : liveClock();
let apiState = isDemo ? 'demo' : 'loading';
let fetching = false;
let history7d = null;
let view = deriveView(snapshot, getNow());
let toastTimer;
let dialogReturnFocus = null;
let previousSignature = '';
// Keyboard actions remain immediate. Only pointer presses get subtle feedback.
document.documentElement.dataset.input = 'keyboard';
function inputMode(value) {
  if (document.documentElement.dataset.input !== value) document.documentElement.dataset.input = value;
}
document.addEventListener('pointerdown', () => inputMode('pointer'), { capture: true });
document.addEventListener('keydown', () => inputMode('keyboard'), { capture: true });
const mobileQuery = window.matchMedia('(max-width: 900px)');

function service(id) { return view.services.find((entry) => entry.id === id) ?? { id, name: '尚未配置', status: 'untested', stale: true, latencyMs: null, history: [], summary: '此检测项尚未接入真实观测。', detail: [], scope: '未配置' }; }
function toast(message) {
  const target = document.querySelector('#toast');
  target.textContent = message;
  target.classList.add('visible');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => target.classList.remove('visible'), 3200);
}

function sparkline(points, color = 'var(--green)', area = false) {
  if (!isDemo) {
    const values = points.map((point) => point.value).filter(Number.isFinite);
    if (!values.length) return '<div class="quiet-line"></div>';
    const max = Math.max(1, ...values) * 1.15;
    return `<svg class="sparkline" viewBox="0 0 160 48" aria-hidden="true">${trendSegments(points, { width: 160, height: 48, padding: 0, max }).map((part) => part.length === 1 ? `<circle cx="${part[0][0]}" cy="${part[0][1]}" r="2" fill="${color}"/>` : `<polyline points="${part.map((point) => point.join(',')).join(' ')}" fill="none" stroke="${color}" stroke-width="1.5"/>`).join('')}</svg>`;
  }
  const values = points.map((point) => typeof point === 'number' ? point : point.value);
  const min = Math.min(...values) * .75;
  const max = Math.max(...values) * 1.15;
  const coords = values.map((value, index) => `${(index / Math.max(1, values.length - 1) * 160).toFixed(1)},${(42 - (value - min) / (max - min || 1) * 32).toFixed(1)}`).join(' ');
  return `<svg class="sparkline" viewBox="0 0 160 48" preserveAspectRatio="none" aria-hidden="true">${area ? `<polygon points="0,48 ${coords} 160,48" fill="${color}" opacity=".07"/>` : ''}<polyline points="${coords}" fill="none" stroke="${color}" stroke-width="1.5" vector-effect="non-scaling-stroke"/></svg>`;
}

function shell(content) {
  const active = NAV.find((item) => item.id === state.page);
  return `<aside class="sidebar ${state.menuOpen ? 'is-open' : ''}" aria-label="主要导航" ${mobileQuery.matches && !state.menuOpen ? 'inert' : ''}>
    <a href="#overview" class="brand" aria-label="Bastion 网络总览"><span class="brand-symbol">${icon('home')}</span><span>bastion<span class="brand-period">.</span></span></a>
    <div class="workspace"><span class="workspace-icon">${icon('home')}</span><div><strong>家庭网络</strong><span>PERSONAL WORKSPACE</span></div><span class="workspace-dot"></span></div>
    <span class="nav-label">观察你的连接</span>
    <nav>${NAV.map((item) => `<a href="#${item.id}" class="nav-item ${state.page === item.id ? 'active' : ''}" ${state.page === item.id ? 'aria-current="page"' : ''}>${icon(item.icon)}<span>${item.label}</span>${item.id === 'events' && view.incidents.some((entry) => entry.status === 'investigating') ? '<span class="nav-indicator"></span>' : ''}</a>`).join('')}</nav>
    <div class="sidebar-bottom"><div class="local-label"><span class="status-dot"></span>${isDemo ? '本地视觉原型' : '只读观测摘要'}<span class="mono">v0.2</span></div><p>${isDemo ? '不连接真实设备<br>不执行网络诊断' : '设备管理接口不公开<br>不在浏览器保存采集凭据'}</p><div class="sidebar-signature">A QUIETER VIEW OF YOUR NETWORK.</div></div>
  </aside>
  ${state.menuOpen ? '<button class="menu-scrim" data-action="close-menu" aria-label="关闭导航"></button>' : ''}
  <div class="workspace-body" ${mobileQuery.matches && state.menuOpen ? 'inert' : ''}><header class="topbar"><div class="breadcrumb"><button class="icon-button mobile-menu" data-action="menu" data-focus="menu" aria-label="切换导航" aria-expanded="${state.menuOpen}">${icon('grid')}</button><span class="breadcrumb-home">工作空间</span><span class="slash">/</span><span>${active.label}</span></div><div class="topbar-right"><span class="timezone mono">UTC+8</span><span class="demo-badge" data-mode="${isDemo ? 'demo' : apiState}"><span></span>${isDemo ? '演示模式' : apiState === 'error' ? '接口不可达' : apiState === 'loading' ? '连接中' : '真实观测'}</span><span class="avatar" aria-label="只读工作空间">B</span></div></header>
  <main id="main-content" tabindex="-1"><section class="page-heading"><div><div class="eyebrow">${active.caption}<span class="eyebrow-line"></span>HOME NETWORK</div><h1>${state.page === 'overview' ? '每一段连接，都有迹可循。' : active.label}</h1><p>${{ overview: '从家庭出口到远程接入，用清晰的观测代替猜测。', services: '健康、可达与业务可用，是三个不同的答案。', paths: '把控制连接、数据链路和业务访问分开看。', events: '留下证据，才能看清每一次中断与恢复。' }[state.page]}</p></div><div class="heading-actions">${isDemo ? `<label class="scenario-control"><span class="sr-only">演示场景</span><select data-control="scenario" data-focus="scenario" aria-label="演示场景">${SCENARIOS.map((item) => `<option value="${item.id}" ${item.id === state.scenario ? 'selected' : ''}>${e(item.label.replace('（演示）', ''))}</option>`).join('')}</select>${icon('down')}</label>` : ''}<button class="button refresh-button" data-action="refresh" data-focus="refresh">${icon('refresh')}<span>${isDemo ? '刷新演示' : '刷新快照'}</span></button></div></section>
  <div class="demo-notice">${icon('info')}<span>${isDemo ? '当前为合成演示快照，不代表真实网络状态。' : apiState === 'error' ? '数据服务不可达：当前显示未知，保留已有历史。' : apiState === 'loading' ? '正在连接数据服务；尚未取得真实观测。' : '真实观测摘要；本机健康不代表公网回家、播放或下载已验证。'}<span class="demo-notice-extra">${isDemo ? '没有连接设备，也没有登录后的管理数据。' : '未知不等于家庭业务故障；检测结论仅覆盖各自范围。'}</span></span><span class="snapshot-time mono">快照 ${time(snapshot.sampledAt)}</span></div>
  ${content}
  <footer class="page-footer"><span><span class="footer-mark">b.</span>状态有边界，观测有时间。</span><span>${isDemo ? '合成数据 · 北京时间 · 无后端连接' : '公开摘要 · 北京时间 · 隐藏管理数据'}</span></footer></main></div>`;
}

function stats() {
  const counts = view.statusCounts;
  const observed = counts.healthy + counts.degraded + counts.down;
  const overallText = view.collector.stale ? '观测数据缺失' : view.overall.status === 'healthy' ? '已测链路正常' : view.overall.status === 'unknown' ? '当前状态未知' : view.overall.status === 'down' ? '发现服务故障' : '部分观测需关注';
  const tone = view.collector.stale ? 'unknown' : view.overall.status;
  const ipv4 = service(view.services.some((entry) => entry.id === 'ipv4-baseline') ? 'ipv4-baseline' : 'ipv4');
  const ipv4Measure = latencyParts(ipv4.latencyMs);
  return `<section class="stats-grid" aria-label="状态摘要">
    <article class="stat-card"><div class="stat-label">当前概况${icon('activity')}</div><div class="stat-value stat-text ${tone}"><span class="large-dot"></span><span class="stat-copy">${overallText}</span></div><div class="stat-foot">${counts.healthy} 正常<span class="separator">/</span>${counts.down} 故障<span class="separator">/</span>${counts.degraded} 降级</div><div class="stat-decoration" aria-hidden="true">${Array.from({ length: 16 }, (_, i) => `<i class="${isDemo ? i > 11 ? tone : 'healthy' : view.services[i % view.services.length]?.status ?? 'unknown'}"></i>`).join('')}</div></article>
    <article class="stat-card"><div class="stat-label">${ipv4.id === 'ipv4-baseline' ? '国内 IPv4 DNS 基准' : 'IPv4 HTTPS 耗时'}${icon('globe')}</div><div class="stat-value mono">${ipv4Measure.value}<span class="stat-unit">${ipv4Measure.unit}</span></div><div class="stat-foot">${ipv4.stale ? '历史结果已过期' : isDemo ? '家庭侧请求 · 合成样本' : ipv4.id === 'ipv4-baseline' ? '指定 DNS 应答 · 非 ping RTT' : 'HTTPS 完整请求 · 非 ping RTT'}</div>${ipv4.stale ? '<div class="quiet-line"></div>' : sparkline(isDemo ? snapshot.metrics.latencyTrend : ipv4.history, 'var(--green)', true)}</article>
    <article class="stat-card"><div class="stat-label">当前观测覆盖${icon('layers')}</div><div class="stat-value mono">${observed}<span class="stat-denominator">/ ${view.services.length}</span></div><div class="stat-foot">${counts.untested} 项未测试${counts.unknown ? ` · ${counts.unknown} 项未知` : ' · 不把未测算成失败'}</div><div class="coverage-track">${view.services.map((entry) => `<i class="${entry.status}" title="${e(entry.name)}：${statusOf(entry.status).label}"></i>`).join('')}</div></article>
    <article class="stat-card"><div class="stat-label">家庭采集心跳${icon('pulse')}</div><div class="stat-value stat-text ${view.collector.stale ? 'unknown' : 'healthy'}">${view.collector.stale ? icon('pause') : icon('check')}${view.collector.stale ? view.collector.lastHeartbeatAt ? '观测已过期' : '尚无心跳' : isDemo ? '快照内有效' : '持续上报'}</div><div class="stat-foot">${age(view.collector.lastHeartbeatAt)}<span class="separator">/</span>${view.collector.staleAfterSeconds} 秒过期</div><div class="heartbeat-rule"><span class="mono">${view.collector.intervalSeconds}s INTERVAL</span><span class="${view.collector.stale ? 'unknown' : 'healthy'}">${view.collector.stale ? 'STALE' : 'SAMPLE'}</span></div></article>
  </section>`;
}

function tabs() {
  const options = state.page === 'overview' ? GROUPS.filter((entry) => ['network', 'access'].includes(entry.id)) : GROUPS;
  return `<div class="group-tabs" role="group" aria-label="服务分组">${options.map((group) => `<button class="tab ${state.group === group.id ? 'active' : ''}" data-group="${group.id}" data-focus="group-${group.id}" aria-pressed="${state.group === group.id}">${group.label}<span>${group.id === 'all' ? view.services.length : view.services.filter((entry) => entry.group === group.id).length}</span></button>`).join('')}</div>`;
}

function historyStrip(entry) {
  return `<div class="uptime-strip" role="img" aria-label="${e(entry.name)}过去24小时状态；${entry.history.filter((point) => point.status === 'unknown').length}个未知样本">${entry.history.map((point) => `<span class="${point.status}" title="${e(dateTime(point.at))} · ${statusOf(point.status).label}"></span>`).join('')}</div>`;
}

function serviceRows() {
  const filtered = filterServices(view.services, state);
  const visible = state.page === 'overview' ? previewServices(filtered) : filtered;
  return visible.length ? visible.map((entry) => `<button class="service-row" data-service="${e(entry.id)}" data-focus="service-${e(entry.id)}" aria-label="查看${e(entry.name)}详情"><span class="service-identity"><span class="service-icon">${icon(SERVICE_ICONS[entry.id])}</span><span><strong>${e(entry.name)}</strong><small>${entry.stale && entry.status !== 'untested' ? '观测已过期 · 当前结论未知' : e(!isDemo ? entry.subtitle || entry.scope : { gateway: '设备与接口', dns: '真实解析应答', ipv4: '直出路径 · IPv4', ipv6: '独立观测 · IPv6', proxy: '明确代理路径', 'headscale-base': '基础接口，不代表节点同步', 'headscale-control': '家庭侧控制会话', 'tailscale-path': '节点间实测，不代表业务', 'derp-base': '仅基础可达性', 'tunnel-ready': '就绪连接，不代表回源', 'home-app': '端到端访问尚未验证', collectors: '采集链路与心跳' }[entry.id] || entry.subtitle || entry.scope)}</small></span></span><span class="service-state">${badge(entry.status)}</span><span class="service-latency mono" title="${!entry.stale && entry.usagePercent != null ? '文件系统使用率' : '此项检测耗时'}">${!entry.stale && entry.usagePercent != null ? percent(entry.usagePercent) : formatLatency(entry.latencyMs)}</span><span class="service-history">${historyStrip(entry)}<span class="history-caption"><span>24h</span><span class="mono">${percent(entry.availability24h)}</span></span></span><span class="row-arrow">${icon('chevron')}</span></button>`).join('') : `<div class="empty-state">${icon('search')}<h3>没有匹配的观测项</h3><p>试试其他名称、状态或分组。</p><button class="button" data-action="clear-filters">清除筛选</button></div>`;
}

function servicePanel(full = false) {
  return `<section class="panel service-panel"><div class="panel-heading"><div><h2>服务观测<span class="count-badge">${view.services.length}</span></h2><p>每一项状态，都有自己的检测边界</p></div>${full ? `<span class="tiny-label">${isDemo ? 'SYNTHETIC' : 'LIVE'} OBSERVATIONS</span>` : '<a href="#services" class="text-link">查看全部' + icon('arrow') + '</a>'}</div>${tabs()}${full ? `<div class="service-filters"><label class="search-field">${icon('search')}<input type="search" placeholder="搜索服务…" aria-label="搜索服务" data-control="search" data-focus="search" value="${e(state.search)}" autocapitalize="none" spellcheck="false" enterkeyhint="search" /></label><label class="filter-select"><span class="sr-only">筛选状态</span><select data-control="status" data-focus="status" aria-label="筛选状态"><option value="all">所有状态</option>${Object.entries(STATUS).map(([key, value]) => `<option value="${key}" ${state.statusFilter === key ? 'selected' : ''}>${value.label}</option>`).join('')}</select>${icon('down')}</label></div>` : ''}<div class="table-labels"><span>服务 / 检测范围</span><span>当前状态</span><span>${isDemo ? '延迟' : '测量值'}</span><span>24h 可用率</span><span></span></div><div id="service-rows">${serviceRows()}</div>${full ? `<div class="service-result-count" role="status" aria-live="polite">${filterServices(view.services, state).length} 项匹配 · 共 ${view.services.length} 项</div>` : `<div class="overview-list-footer"><span>优先显示需关注项 · 最多 8 项</span><a href="#services" class="text-link">完整观测列表 ${icon('arrow')}</a></div>`}<div class="panel-footnote">${icon('info')}降级计为可用；未知、未测与维护不计入可用率分母。</div></section>`;
}

function topology() {
  const entries = ['gateway', 'headscale-control', 'tunnel-ready', 'tailscale-path'].map(service);
  const [gateway, control, tunnel, data] = entries;
  const color = (entry) => ({ healthy: '#9bc5a6', degraded: '#d9ad70', unknown: '#626771' }[entry.status] || '#626771');
  return `<div class="topology-map"><svg viewBox="0 0 440 240" role="img" aria-label="家庭网关分别连接控制端、隧道与用户数据链路，颜色表示各自观测状态"><defs><pattern id="topology-grid" width="16" height="16" patternUnits="userSpaceOnUse"><circle cx="1" cy="1" r=".65" fill="#30343a"/></pattern></defs><rect width="440" height="240" fill="url(#topology-grid)" opacity=".55"/>
    <path d="M120 120h52q18 0 18-18V58q0-14 16-14h69M120 120h155M120 120h52q18 0 18 18v44q0 14 16 14h69" fill="none" stroke="#353940" stroke-width="1.5"/>
    <path d="M120 120h52q18 0 18-18V58q0-14 16-14h69" fill="none" stroke="${color(control)}" stroke-width="1.4" stroke-dasharray="4 6" opacity=".7"/>
    <path d="M120 120h155" fill="none" stroke="${color(tunnel)}" stroke-width="1.4" opacity=".7"/>
    <path d="M120 120h52q18 0 18 18v44q0 14 16 14h69" fill="none" stroke="${color(data)}" stroke-width="1.4" stroke-dasharray="4 6" opacity=".7"/>
    <rect x="25" y="89" width="96" height="62" rx="12" fill="#181b1e" stroke="#353a3f"/>
    <path d="m64 113 9-7 9 7v12H64Z" fill="none" stroke="${color(gateway)}" stroke-width="1.5"/><path d="M70 125v-8h6v8" fill="none" stroke="${color(gateway)}" stroke-width="1.5"/>
    <circle cx="120" cy="120" r="3" fill="${color(gateway)}"/>
    <text x="73" y="172" text-anchor="middle" fill="#a7adb5" font-size="11">家庭入口</text>
    ${[{ y: 23, label: '控制连接', entry: control }, { y: 99, label: 'Cloudflare Tunnel', entry: tunnel }, { y: 175, label: 'Tailscale 数据链路', entry: data }].map(({ y, label, entry }) => `<rect x="275" y="${y}" width="144" height="42" rx="9" fill="#181b1e" stroke="#30353b"/><circle cx="291" cy="${y + 21}" r="3" fill="${color(entry)}"/><text x="303" y="${y + 25}" fill="#b9bec5" font-size="10.5">${label}</text>`).join('')}
  </svg><div class="topology-label mono">CONTROL ≠ DATA ≠ BUSINESS</div></div>`;
}

function pathPanel(full = false) {
  const control = service('headscale-control');
  const path = service('tailscale-path');
  const tunnel = service('tunnel-ready');
  const pathLabel = path.stale ? '当前路径未知' : isDemo ? path.status === 'healthy' ? '本轮已验证直连' : '本轮仅中继可达' : ({ direct: '探测路径为直接连接', relay: '探测路径经过中继', mixed: '已观察到混合路径' }[path.pathMode] ?? '尚无端到端路径证据');
  const pathBadge = path.stale || (!isDemo && path.pathMode === 'unknown') ? '未知' : isDemo ? path.status === 'healthy' ? '直连' : '中继' : ({ direct: '直连', relay: '中继', mixed: '混合' }[path.pathMode] ?? '未知');
  return `<section class="panel path-panel"><div class="panel-heading"><div><h2>回家链路${icon('route', 'heading-icon')}</h2><p>不同连接，独立判断</p></div>${full ? `<span class="tiny-label">${isDemo ? 'DEMO TOPOLOGY' : '结构示意 · 非逐节点验证'}</span>` : '<a href="#paths" class="text-link" aria-label="查看连接路径">' + icon('diagonal') + '</a>'}</div>${topology()}<div class="path-observations"><button class="path-observation" data-service="headscale-control" data-focus="path-headscale-control"><span class="path-observation-icon">${icon('link')}</span><div><strong>控制连接</strong><span>${control.status === 'untested' ? '尚未验证控制同步' : control.stale || control.status === 'unknown' ? '没有新鲜的同步观测' : control.status === 'healthy' ? '本轮控制观测正常' : '指定控制探测需关注'}</span></div>${badge(control.status)}</button><button class="path-observation" data-service="tailscale-path" data-focus="path-tailscale-path"><span class="path-observation-icon">${icon('route')}</span><div><strong>数据链路</strong><span>${pathLabel}</span></div>${badge(path.status, pathBadge)}</button><button class="path-observation" data-service="tunnel-ready" data-focus="path-tunnel-ready"><span class="path-observation-icon">${icon('cloud')}</span><div><strong>隧道就绪</strong><span>${tunnel.stale ? '尚无新鲜就绪观测' : isDemo ? '合成样本：4 条就绪连接' : tunnel.readyConnections == null ? '就绪接口响应；连接数量尚无观测' : `${tunnel.readyConnections} 条就绪连接`}</span></div>${badge(tunnel.status)}</button></div><div class="panel-footnote">${icon('info')}链路可达不代表家庭应用已验证可用。</div></section>`;
}

function chartPanel() {
  const values = isDemo ? snapshot.metrics.latencyTrend : state.range === '7d' ? history7d ?? [] : service('ipv4').history;
  const width = 760, height = 150, padding = 18;
  const max = isDemo ? 80 : Math.max(80, ...values.filter((point) => Number.isFinite(point.value)).map((point) => Math.ceil(point.value / 20) * 20));
  const coords = values.map((point, index) => `${(padding + index / Math.max(1, values.length - 1) * (width - padding * 2)).toFixed(1)},${(height - 12 - point.value / max * (height - 24)).toFixed(1)}`).join(' ');
  const labels = state.range === '7d' ? ['7 天前', '5 天前', '3 天前', '1 天前', '快照时间'] : ['24h 前', '18h 前', '12h 前', '6h 前', '快照时间'];
  const hasTrend = values.some((point) => Number.isFinite(point.value));
  const liveLines = trendSegments(values, { width, height, padding, max }).map((segment) => segment.length === 1 ? `<circle cx="${segment[0][0]}" cy="${segment[0][1]}" r="2.5" fill="#9bc5a6"/>` : `<polyline points="${segment.map((point) => point.join(',')).join(' ')}" fill="none" stroke="#9bc5a6" stroke-width="1.6" vector-effect="non-scaling-stroke"/>`).join('');
  const empty = isDemo ? state.range === '7d' : !hasTrend;
  return `<section class="panel chart-panel"><div class="panel-heading"><div><h2>IPv4 HTTPS 历史</h2><p>${isDemo ? '合成历史样本' : '指定网页请求 · 缺口断线'} · ms · 非当前测量</p></div><div class="segmented" role="group" aria-label="历史范围"><button data-range="24h" data-focus="range-24h" class="${state.range === '24h' ? 'active' : ''}" aria-pressed="${state.range === '24h'}">24 小时</button><button data-range="7d" data-focus="range-7d" class="${state.range === '7d' ? 'active' : ''}" aria-pressed="${state.range === '7d'}">7 天</button></div></div>${empty ? '<div class="chart-empty">' + icon('clock') + `<h3>${state.range === '7d' ? '还没有 7 天观测数据' : '还没有历史观测数据'}</h3><p>历史自采集上线开始，不补造过去记录。</p></div>` : `<div class="latency-chart"><div class="chart-y-labels mono"><span>${max}</span><span>${max / 2}</span><span>0</span></div><div class="chart-inner"><svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" role="img" aria-label="${state.range === '7d' ? '7 天' : '24 小时'}${isDemo ? '合成' : '真实'}IPv4 HTTPS 请求耗时"><defs><linearGradient id="chart-fill" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="#9bc5a6" stop-opacity=".12"/><stop offset="100%" stop-color="#9bc5a6" stop-opacity="0"/></linearGradient></defs>${[18, 78, 138].map((y) => `<path d="M0 ${y}H760" stroke="#262b30" stroke-width="1" stroke-dasharray="3 5"/>`).join('')}${isDemo ? `<polygon points="${padding},${height} ${coords} ${width - padding},${height}" fill="url(#chart-fill)"/><polyline points="${coords}" fill="none" stroke="#9bc5a6" stroke-width="1.6" vector-effect="non-scaling-stroke"/>` : liveLines}</svg><div class="chart-x-labels mono">${labels.map((label) => `<span>${label}</span>`).join('')}</div></div></div>`}<div class="chart-legend"><span><i class="legend-line"></i>历史 HTTPS 请求</span><span>${values.length} 个窗口点${isDemo ? ' · 合成' : ' · 可含未知'}<span class="separator">·</span>快照 ${time(snapshot.sampledAt)}</span></div></section>`;
}

function incidentCards(compact = false) {
  const filtered = view.incidents.filter((entry) => state.eventFilter === 'all' || entry.status === state.eventFilter);
  if (!filtered.length) return '<div class="empty-state">' + icon('check') + `<h3>这个筛选下没有事件</h3><p>${isDemo ? '切换其他事件状态查看合成记录。' : '事件只从实际采集开始记录。'}</p></div>`;
  return filtered.slice(0, compact ? 2 : undefined).map((entry) => `<article class="incident ${entry.status}"><div class="incident-marker">${icon(entry.status === 'resolved' ? 'check' : entry.status === 'investigating' ? 'activity' : 'info')}</div><div class="incident-body"><div class="incident-heading"><h3>${e(entry.title.replace('（演示）', ''))}</h3><span class="incident-status">${{ resolved: '已恢复', investigating: '观察中', info: '记录' }[entry.status]}</span></div><p>${e(entry.description.replace('合成事件：', '').replace('；仅用于演示。', '。'))}</p><div class="incident-meta mono"><span>${dateTime(entry.startedAt)}</span>${entry.resolvedAt ? `<span class="incident-duration">持续 ${Math.round((Date.parse(entry.resolvedAt) - Date.parse(entry.startedAt)) / 60000)} 分钟</span>` : '<span>尚无恢复记录</span>'}</div>${!compact ? `<div class="incident-targets">${entry.affectedServiceIds.map((id) => `<button data-service="${e(id)}" data-focus="incident-${e(entry.id)}-${e(id)}">${e(service(id)?.name || id)}${icon('diagonal')}</button>`).join('')}</div>` : ''}</div></article>`).join('');
}

function incidentPanel(compact = true) {
  return `<section class="panel incident-panel"><div class="panel-heading"><div><h2>最近事件<span class="count-badge">${view.incidents.length}</span></h2><p>${isDemo ? '合成记录，不是历史故障的真实回放' : '后端记录的异常与恢复；缺失观测不当作业务故障'}</p></div>${compact ? '<a href="#events" class="text-link">全部事件' + icon('arrow') + '</a>' : `<span class="tiny-label">${isDemo ? 'SYNTHETIC' : 'OBSERVED'} EVENTS</span>`}</div>${!compact ? `<div class="event-tabs segmented" role="group" aria-label="事件状态">${[{ id: 'all', label: '全部' }, { id: 'investigating', label: '观察中' }, { id: 'resolved', label: '已恢复' }, { id: 'info', label: '记录' }].map((item) => `<button data-event-filter="${item.id}" data-focus="event-${item.id}" class="${state.eventFilter === item.id ? 'active' : ''}" aria-pressed="${state.eventFilter === item.id}">${item.label}</button>`).join('')}</div>` : ''}<div class="incident-list">${incidentCards(compact)}</div></section>`;
}

function boundaryCard() {
  return `<section class="boundary-card"><div class="boundary-icon">${icon('shield')}</div><div><h3>看清状态，也看清边界。</h3><p>基础健康 ≠ 节点同步，直连成功 ≠ 应用可用。未知只是缺少证据，不是故障的另一种说法。</p></div><span class="tiny-label">OBSERVE, NOT ASSUME.</span></section>`;
}

function attentionBar() {
  const counts = view.statusCounts;
  const confirmed = counts.down + counts.degraded;
  return `<section class="attention-bar" aria-label="观测关注项"><span class="attention-heading">${icon(confirmed ? 'alert' : 'info')}<strong>${confirmed ? `${confirmed} 项检测需要关注` : counts.unknown ? '部分观测缺少新鲜证据' : '已测范围内未发现异常'}</strong></span><div class="attention-actions">${['down', 'degraded', 'unknown'].filter((status) => counts[status] > 0).map((status) => `<button class="attention-chip ${status}" data-quick-status="${status}" data-focus="attention-${status}" aria-label="筛选${counts[status]}项${statusOf(status).label}观测">${counts[status]} ${statusOf(status).label}${icon('arrow')}</button>`).join('')}</div><span class="attention-note">未知 ≠ 故障 · ${counts.untested} 项未测</span></section>`;
}
function exitMeasurements() {
  if (!view.services.some((entry) => ['ipv4-baseline', 'ipv6-baseline', 'ipv6-icmp'].includes(entry.id))) return '';
  const hasPing = view.services.some((entry) => entry.id === 'ipv6-icmp');
  return `<section class="exit-measurements" aria-label="IPv4 与 IPv6 独立测量"><div class="exit-heading"><h2>出口观测</h2><p>${hasPing ? 'DNS、ICMP 与 HTTPS 独立测量；Ping 不代表 DNS 或业务成功。' : 'DNS 应答与网页请求分别测量，不是 ping，也不证明完整路由。'}</p></div><div class="exit-grid">${['ipv4', 'ipv6'].map((family) => `<article class="exit-family"><span class="exit-family-label mono">${family.toUpperCase()}</span><div class="exit-family-probes">${[exitBaseline(view.services, family), { id: family, label: 'HTTPS 请求' }].map(({ id, label }) => { const entry = service(id); return `<button class="exit-probe" data-timing-service="${id}" data-focus="timing-${id}" aria-label="查看${family.toUpperCase()} ${label}详情"><span class="exit-probe-heading">${label}${icon('diagonal')}</span><span class="exit-probe-value mono">${formatLatency(entry.latencyMs)}</span>${badge(entry.status)}</button>`; }).join('')}</div></article>`).join('')}</div></section>`;
}
function collectorSummary() {
  if (isDemo) return '';
  const fresh = view.collectors.filter((entry) => !entry.stale).length;
  return `<details class="collector-summary" data-collectors ${state.collectorsOpen ? 'open' : ''}><summary data-focus="collectors"><span>${icon('pulse')}采集来源</span><span class="collector-tally mono">${fresh} / ${view.collectors.length} 新鲜</span><span class="collector-hint">展开来源${icon('down')}</span></summary><div class="collector-grid">${view.collectors.map((entry) => `<div class="collector-item"><span>${badge(entry.stale ? 'unknown' : 'healthy', entry.stale ? '过期' : '新鲜')}<strong>${e(entry.label)}</strong></span><small>${age(entry.lastHeartbeatAt)}</small></div>`).join('') || '<p>尚未登记采集来源</p>'}</div></details>`;
}
function overview() {
  return `${stats()}${attentionBar()}${collectorSummary()}${exitMeasurements()}<div class="overview-grid"><div class="overview-main">${servicePanel()}${chartPanel()}</div><div class="overview-aside">${pathPanel()}${incidentPanel()}</div></div>${boundaryCard()}`;
}
function servicesPage() {
  return `${stats()}${attentionBar()}${collectorSummary()}${servicePanel(true)}<div class="legend-bar"><span>状态图例</span>${Object.entries(STATUS).map(([key, value]) => `<span><i class="legend-square ${key}"></i>${value.label}</span>`).join('')}<span class="legend-explanation">${isDemo ? '24h 为等间隔合成样本统计；7 天数据尚未提供。' : '可用率仅基于已测窗口点；缺失区间保留未知，覆盖率另列。'}</span></div>${boundaryCard()}`;
}
function pathsPage() {
  const unknown = service('tailscale-path').stale || service('tailscale-path').status === 'unknown';
  return `<div class="paths-layout">${pathPanel(true)}<section class="panel evidence-panel"><div class="panel-heading"><div><h2>出口路径对照</h2><p>每种路径有独立观测，不自动推断根因</p></div>${icon('globe', 'heading-icon')}</div><div class="route-list">${[{ id: 'ipv4', name: isDemo ? '真实地址直出' : '指定 IPv4 目标', route: '家庭入口 → 网关 → IPv4 公网', note: '与 DNS / Fake-IP 路径分离' }, { id: 'ipv6', name: 'IPv6 独立出口', route: '家庭入口 → 网关 → IPv6 公网', note: '异常不连带判 IPv4 故障' }, { id: 'proxy', name: '明确代理出口', route: '家庭入口 → 代理 → 目标服务', note: isDemo ? '仅表示合成路径，并非真实抓包证据' : '仅代表指定代理请求，非流量抓包证明' }].map((item, index) => `<button class="route-card" data-service="${item.id}" data-focus="route-${item.id}"><span class="route-number mono">0${index + 1}</span><div><div class="route-title"><strong>${item.name}</strong>${badge(service(item.id).status)}</div><p class="route-chain">${item.route}</p><small>${item.note}</small></div>${icon('diagonal')}</button>`).join('')}</div><div class="evidence-note">${icon('info')}没有实测路径依据时，只能显示配置声明，不能把请求成功当作路由证明。</div></section></div><section class="panel path-boundaries"><div class="panel-heading"><div><h2>三个问题，三个答案</h2><p>${isDemo ? '演示分层判定' : '基于独立观测'}，业务结论保持独立</p></div></div><div class="boundary-columns"><article><span class="mono">01 / CONTROL</span><h3>控制端正常吗？</h3>${badge(service('headscale-base').status)}<p>基础接口能否响应；不证明家庭控制会话同步正常。</p></article><article><span class="mono">02 / DATA</span><h3>节点之间可达吗？</h3>${badge(service('tailscale-path').status)}<p>${unknown ? '缺少新鲜的链路证据，当前路径未知。' : isDemo ? '合成节点探测有响应；不证明家庭子网或 ACL 正常。' : '仅覆盖该节点探测；不证明家庭子网、ACL 或业务正常。'}</p></article><article><span class="mono">03 / BUSINESS</span><h3>应用真的可用吗？</h3>${badge('untested')}<p>尚无外部端到端业务探针，不宣称用户必然可以回家。</p></article></div></section>${boundaryCard()}`;
}
function eventsPage() {
  return `<div class="event-summary"><div class="event-summary-copy">${icon('clock')}<span>每次异常都值得记录，但不是每条错误都代表一次独立故障。</span></div><span class="mono">${view.incidents.filter((entry) => entry.status === 'investigating').length} OBSERVING · ${view.incidents.filter((entry) => entry.status === 'resolved').length} RESOLVED</span></div>${incidentPanel(false)}${boundaryCard()}`;
}

function render({ preserveFocus = false } = {}) {
  const active = document.activeElement;
  const focusId = preserveFocus && app.contains(active) ? active?.dataset.focus : null;
  const selectionStart = active instanceof HTMLInputElement ? active.selectionStart : null;
  const selectionEnd = active instanceof HTMLInputElement ? active.selectionEnd : null;
  view = deriveView(snapshot, getNow());
  previousSignature = view.services.map((entry) => entry.status).join(',') + view.collector.stale + view.collectors.map((entry) => entry.stale).join(',');
  app.innerHTML = shell({ overview, services: servicesPage, paths: pathsPage, events: eventsPage }[state.page]());
  document.body.classList.toggle('menu-open', mobileQuery.matches && state.menuOpen);
  document.title = `${NAV.find((entry) => entry.id === state.page).label} · Bastion`;
  if (focusId) {
    const target = app.querySelector(`[data-focus="${CSS.escape(focusId)}"]`) ?? app.querySelector('#main-content');
    target?.focus({ preventScroll: true });
    if (target instanceof HTMLInputElement && target.type === 'search' && selectionStart !== null) target.setSelectionRange(selectionStart, selectionEnd);
  }
}

function openDetails(id) {
  view = deriveView(snapshot, getNow());
  const entry = service(id);
  if (!entry) return;
  const restoreCloseFocus = dialog.open && dialog.contains(document.activeElement);
  if (!dialog.open) dialogReturnFocus = document.activeElement?.dataset.focus ?? null;
  dialog.dataset.service = id;
  const stats = getHistoryStats(entry.history);
  const meaning = entry.status === 'untested' ? '尚未接入端到端业务检测。没有结果，不等于正常，也不等于故障。' : entry.stale ? '这条观测已经过期。历史上报值保留供参考，当前状态只能显示未知。' : entry.summary.replace('合成演示数据：', '');
  dialog.innerHTML = `<div class="dialog-top"><span class="eyebrow">OBSERVATION DETAILS</span><button class="icon-button" data-action="close-dialog" aria-label="关闭详情">${icon('close')}</button></div><div class="dialog-identity"><span class="dialog-icon">${icon(SERVICE_ICONS[id])}</span><div><h2 id="dialog-title">${e(entry.name)}</h2><p>${e(entry.scope)}</p></div></div><div class="dialog-status">${badge(entry.status)}<span class="mono">${!entry.stale && entry.usagePercent != null ? percent(entry.usagePercent) + ' 使用率' : formatLatency(entry.latencyMs)}</span></div><p class="dialog-summary">${e(meaning)}</p><dl class="detail-grid"><div><dt>观测来源</dt><dd>${e(entry.probeLabel)}</dd></div><div><dt>最近观测 · 北京时间</dt><dd class="mono">${dateTime(entry.observedAt)}<small>${entry.status === 'untested' ? '未产生业务测试结果' : entry.stale ? '超过有效期 · 不是当前结果' : isDemo ? '快照内结果 · 不代表真实状态' : '真实观测 · 结论仅限此检测范围'}</small></dd></div><div><dt>24h 可用率</dt><dd class="mono">${percent(stats.uptimePercent)}<small>分母：${stats.observedCount} 个已测样本</small></dd></div><div><dt>24h 数据覆盖率</dt><dd class="mono">${stats.coverageRatio === null ? '—' : percent(stats.coverageRatio * 100)}<small>已测 / 非维护样本</small></dd></div>${entry.stale && entry.status !== 'untested' ? `<div><dt>历史上报状态</dt><dd>${statusOf(entry.reportedStatus).label}<small>不用于当前结论</small></dd></div><div><dt>历史上报延迟</dt><dd class="mono">${formatLatency(entry.reportedLatencyMs)}<small>不作为当前延迟</small></dd></div>` : ''}</dl><div class="dialog-history"><span class="tiny-label">24H SAMPLE HISTORY</span>${historyStrip(entry)}<div class="history-caption"><span>24h 前</span><span>快照时间</span></div></div><div class="detail-limits"><h3>检测边界</h3>${entry.detail.filter((row) => ['结论限制', '补充限制'].includes(row.label)).map((row) => `<p>${icon('info')}${e(row.value)}</p>`).join('')}</div><div class="dialog-footer">${icon('shield')}${isDemo ? '仅合成公开摘要，不包含真实节点、地址或管理数据。' : '仅公开脱敏摘要；不含设备地址、采集凭据或原始日志。'}</div>`;
  if (!dialog.open) dialog.showModal();
  if (restoreCloseFocus) dialog.querySelector('[data-action="close-dialog"]').focus({ preventScroll: true });
}

app.addEventListener('click', (event) => {
  const target = event.target.closest('button');
  if (!target) return;
  if (target.dataset.service || target.dataset.timingService) return openDetails(target.dataset.service || target.dataset.timingService);
  if (target.dataset.quickStatus) {
    if (state.page === 'services') { state.statusFilter = target.dataset.quickStatus; state.group = 'all'; state.search = ''; render(); app.querySelector('[data-control="status"]').focus({ preventScroll: true }); }
    else { state.nextStatusFilter = target.dataset.quickStatus; location.hash = 'services'; }
    return;
  }
  if (target.dataset.group) { state.group = target.dataset.group; render({ preserveFocus: true }); return; }
  if (target.dataset.range) { state.range = target.dataset.range; render({ preserveFocus: true }); if (!isDemo && state.range === '7d') refreshHistory(); return; }
  if (target.dataset.eventFilter) { state.eventFilter = target.dataset.eventFilter; render({ preserveFocus: true }); return; }
  switch (target.dataset.action) {
    case 'refresh': if (isDemo) { snapshot = createDemoSnapshot(state.scenario); render({ preserveFocus: true }); toast('已生成新的合成演示快照 · 未请求真实设备'); } else { refreshLive(true); } break;
    case 'clear-filters': state.search = ''; state.statusFilter = 'all'; state.group = 'all'; render(); break;
    case 'menu': state.menuOpen = !state.menuOpen; render(); if (state.menuOpen) app.querySelector('.sidebar .brand').focus(); break;
    case 'close-menu': closeMenu(); break;
  }
});
app.addEventListener('change', (event) => {
  if (isDemo && event.target.dataset.control === 'scenario') {
    state.scenario = event.target.value;
    snapshot = createDemoSnapshot(state.scenario);
    render({ preserveFocus: true });
    toast(`已切换至${SCENARIOS.find((item) => item.id === state.scenario).label} · 合成数据`);
  } else if (event.target.dataset.control === 'status') {
    state.statusFilter = event.target.value;
    render({ preserveFocus: true });
  }
});
app.addEventListener('input', (event) => {
  if (event.target.dataset.control === 'search') {
    state.search = event.target.value;
    document.querySelector('#service-rows').innerHTML = serviceRows();
    document.querySelector('.service-result-count').textContent = `${filterServices(view.services, state).length} 项匹配 · 共 ${view.services.length} 项`;
  }
});
app.addEventListener('toggle', (event) => {
  if (event.target.matches('[data-collectors]')) state.collectorsOpen = event.target.open;
}, true);
dialog.addEventListener('close', () => {
  if (dialogReturnFocus) app.querySelector(`[data-focus="${CSS.escape(dialogReturnFocus)}"]`)?.focus({ preventScroll: true });
  dialogReturnFocus = null;
});
dialog.addEventListener('click', (event) => {
  if (event.target.closest('[data-action="close-dialog"]')) dialog.close();
  if (event.target === dialog) {
    const rect = dialog.getBoundingClientRect();
    if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) dialog.close();
  }
});
function closeMenu() {
  state.menuOpen = false;
  render();
  app.querySelector('[data-action="menu"]').focus({ preventScroll: true });
}
window.addEventListener('keydown', (event) => {
  if (!state.menuOpen || !mobileQuery.matches) return;
  if (event.key === 'Escape') { closeMenu(); return; }
  if (event.key === 'Tab') {
    const links = [...app.querySelectorAll('.sidebar a')];
    const first = links[0], last = links.at(-1);
    if (event.shiftKey && (document.activeElement === first || !app.querySelector('.sidebar').contains(document.activeElement))) {
      event.preventDefault(); last.focus();
    } else if (!event.shiftKey && (document.activeElement === last || !app.querySelector('.sidebar').contains(document.activeElement))) {
      event.preventDefault(); first.focus();
    }
  }
});
mobileQuery.addEventListener('change', () => {
  const wasOpen = state.menuOpen;
  state.menuOpen = false;
  render({ preserveFocus: true });
  if (wasOpen) app.querySelector(mobileQuery.matches ? '[data-action="menu"]' : '#main-content').focus({ preventScroll: true });
});
function navigate(initial = false) {
  const requested = window.location.hash.slice(1);
  state.page = NAV.some((entry) => entry.id === requested) ? requested : 'overview';
  state.group = state.page === 'overview' ? 'network' : 'all';
  state.search = '';
  state.statusFilter = state.page === 'services' ? state.nextStatusFilter ?? 'all' : 'all';
  state.nextStatusFilter = null;
  state.eventFilter = 'all';
  state.menuOpen = false;
  if (dialog.open) { dialogReturnFocus = null; dialog.close(); }
  render();
  if (!initial) {
    window.scrollTo({ top: 0, behavior: 'instant' });
    document.querySelector('#main-content').focus({ preventScroll: true });
  }
}
window.addEventListener('hashchange', () => navigate());
async function refreshHistory() {
  try {
    const response = await fetch('/api/v1/history?service=ipv4&range=7d', { cache: 'no-store', signal: AbortSignal.timeout(8000) });
    if (!response.ok) throw new Error('history unavailable');
    const result = await response.json();
    if (!Array.isArray(result.points) || result.points.length > 336 || result.points.some((point) => !Number.isFinite(Date.parse(point.at)) || (point.value != null && !Number.isFinite(point.value)))) throw new Error('invalid history');
    history7d = result.points;
  } catch { history7d = null; }
  render({ preserveFocus: true });
}
async function refreshLive(manual = false) {
  if (fetching) return;
  fetching = true;
  try {
    snapshot = await fetchSnapshot({ signal: AbortSignal.timeout(8000) });
    liveClock = createClock(snapshot);
    apiState = 'live';
    if (manual) toast('真实状态快照已更新');
  } catch {
    snapshot = unavailableSnapshot(snapshot);
    apiState = 'error';
    if (manual) toast('数据服务不可达；未切换为演示数据');
  } finally { fetching = false; render({ preserveFocus: true }); if (dialog.open) openDetails(dialog.dataset.service); }
}
navigate(true);
if (!isDemo) { refreshLive(); setInterval(() => { refreshLive(); if (state.range === '7d') refreshHistory(); }, 10000); }
setInterval(() => {
  const next = deriveView(snapshot, getNow());
  const signature = next.services.map((entry) => entry.status).join(',') + next.collector.stale + next.collectors.map((entry) => entry.stale).join(',');
  if (signature !== previousSignature) {
    render({ preserveFocus: true });
    if (dialog.open) openDetails(dialog.dataset.service);
  }
  document.querySelectorAll('[data-age]').forEach((node) => { node.textContent = ageText(node.dataset.age); });
}, 1000);
