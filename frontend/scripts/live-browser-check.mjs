// Local-only integration: real Go/SQLite HTTP server + Chrome. Inputs are generated fixtures,
// not real household observations. Credentials/config/database live outside the repository.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, writeFile, rm, mkdir } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
import { tmpdir } from 'node:os';
import path from 'node:path';
import net from 'node:net';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { BASE_SERVICES } from '../src/data.js';

const root = fileURLToPath(new URL('../', import.meta.url));
const repo = path.resolve(root, '..');
const chromePath = process.env.CHROME_PATH || ['C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Google/Chrome/Application/chrome.exe', '/usr/bin/google-chrome', '/usr/bin/chromium'].find(existsSync);
if (!chromePath) throw new Error('Set CHROME_PATH; no MCP or user browser profile is used.');
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function port() { const s = net.createServer(); await new Promise((resolve) => s.listen(0, '127.0.0.1', resolve)); const n = s.address().port; await new Promise((resolve) => s.close(resolve)); return n; }
async function retry(fn) { let error; for (let i = 0; i < 100; i++) { try { return await fn(); } catch (e) { error = e; await wait(100); } } throw error; }
const temp = await mkdtemp(path.join(tmpdir(), 'bastion-live-check-'));
const backendPort = await port(), sitePort = await port(), debugPort = await port();
const backendOrigin = `http://127.0.0.1:${backendPort}`, origin = `http://127.0.0.1:${sitePort}`;
const token = randomBytes(32).toString('hex');
const agentTokens = [token, ...Array.from({ length: 7 }, () => randomBytes(32).toString('hex'))];
const expandedServices = [...BASE_SERVICES,
  { id: 'ipv4-baseline', name: 'IPv4 DNS 查询夹具', group: 'network' },
  { id: 'ipv6-baseline', name: 'IPv6 DNS 查询夹具', group: 'network' },
  { id: 'ipv6-icmp', name: 'IPv6 ICMP Echo 夹具', group: 'network' },
  ...Array.from({ length: 46 }, (_, index) => ({ id: `resource-fixture-${index}`, name: `较长的只读资源观测名称 ${index + 1}`, group: index % 2 ? 'monitoring' : 'network' })),
];
const secretFile = path.join(temp, 'credential');
const configFile = path.join(temp, 'config.private.json');
const binary = path.join(temp, process.platform === 'win32' ? 'backend.exe' : 'backend');
await writeFile(secretFile, token, { mode: 0o600 });
await Promise.all(agentTokens.slice(1).map((credential, i) => writeFile(path.join(temp, `credential-${i + 1}`), credential, { mode: 0o600 })));
await writeFile(configFile, JSON.stringify({
  listen: `127.0.0.1:${backendPort}`, database: path.join(temp, 'state.sqlite'), retentionDays: 30,
  agents: agentTokens.map((_, i) => ({ id: i === 0 ? 'home' : `source-fixture-${i}`, label: `独立来源测试夹具 ${i + 1}`, kind: 'family', secretFile: i === 0 ? secretFile : path.join(temp, `credential-${i}`), intervalSeconds: 30, staleAfterSeconds: 90 })),
  services: expandedServices.map((entry) => ({ id: entry.id, name: entry.name, group: entry.group, subtitle: '本地测试夹具，非真实部署', collectorId: 'home', scope: entry.id === 'tailscale-path' ? '本机 TSMP 测试夹具；不证明子网、应用或直连路径' : '本机 Go/SQLite 集成测试输入；不代表家庭业务', staleAfterSeconds: 90 })),
}), { mode: 0o600 });
let backend, server, chrome, ws;
let seq = 0, passed = 0;
const pending = new Map(), errors = [], urls = new Set();
function check(name, ok) { assert.ok(ok, name); console.log(`PASS LIVE ${++passed} ${name}`); }
function command(method, params = {}) { const id = ++seq; return new Promise((resolve, reject) => { const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 12000); pending.set(id, { resolve, reject, timer }); ws.send(JSON.stringify({ id, method, params })); }); }
async function evaluate(expression) { const r = await command('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }); if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text); return r.result.value; }
async function click(selector) { await evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`); await wait(100); }
async function route(hash) { await evaluate(`location.hash=${JSON.stringify(hash)}`); await wait(100); }
async function refresh() {
  // Wait for the next render, rather than accepting the previous 'live' badge.
  await evaluate("document.querySelector('.demo-badge').textContent='等待本次刷新'");
  await click('[data-action=refresh]');
  await retry(async () => { assert.equal(await evaluate("document.querySelector('.demo-badge').textContent"), '真实观测'); });
}
async function post(endpoint, payload, credential = token) { const response = await fetch(`${backendOrigin}/api/v1/${endpoint}`, { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${credential}` }, body: JSON.stringify(payload) }); assert.equal(response.status, 200); }
const id = () => randomBytes(16).toString('hex');
async function sample(serviceId, status = 'healthy', extra = {}) { await post('ingest', { id: id(), replay: false, samples: [{ id: id(), serviceId, status, code: status === 'healthy' ? 'ok' : 'http_error', ageSeconds: 0, latencyMs: status === 'healthy' ? 18 : null, ...extra }] }); await wait(5); }
try {
  execFileSync('go', ['build', '-o', binary, './cmd/bastion-server'], { cwd: repo, stdio: 'pipe' });
  backend = spawn(binary, ['-config', configFile], { cwd: temp, stdio: 'pipe' });
  await retry(async () => { assert.equal((await fetch(`${backendOrigin}/healthz`)).status, 200); });
  server = spawn(process.execPath, [path.join(root, 'node_modules/vite/bin/vite.js'), 'preview', '--host', '127.0.0.1', '--port', String(sitePort), '--strictPort'], { cwd: root, env: { ...process.env, BASTION_DEV_API: backendOrigin }, stdio: 'pipe' });
  chrome = spawn(chromePath, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', `--remote-debugging-port=${debugPort}`, '--remote-debugging-address=127.0.0.1', `--user-data-dir=${path.join(temp, 'chrome')}`, 'about:blank'], { stdio: 'ignore' });
  await retry(async () => { assert.equal((await fetch(origin)).status, 200); });
  const targets = await retry(async () => (await fetch(`http://127.0.0.1:${debugPort}/json`)).json());
  ws = new WebSocket(targets.find((entry) => entry.type === 'page').webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { ws.addEventListener('open', resolve, { once: true }); ws.addEventListener('error', reject, { once: true }); });
  ws.addEventListener('message', ({ data }) => { const message = JSON.parse(data); if (message.id) { const p = pending.get(message.id); if (!p) return; clearTimeout(p.timer); pending.delete(message.id); message.error ? p.reject(new Error(message.error.message)) : p.resolve(message.result); } else if (message.method === 'Runtime.exceptionThrown') errors.push(message.params.exceptionDetails.text); else if (message.method === 'Network.requestWillBeSent') urls.add(message.params.request.url); });
  await command('Page.enable'); await command('Runtime.enable'); await command('Network.enable');
  await command('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await command('Page.navigate', { url: `${origin}/?demo=1` });
  await retry(async () => assert.equal(await evaluate("document.querySelector('.demo-badge')?.textContent"), '真实观测'));
  check('生产默认真实 API，URL 演示参数也不能启用选择器', await evaluate("!document.querySelector('[data-control=scenario]') && document.querySelector('.demo-notice').textContent.includes('真实观测')"));
  check('无采集数据时仅展示未知分组，不铺开未取得的测量', await evaluate("document.querySelectorAll('.check-group-card').length===8 && !document.querySelector('.service-row') && document.querySelector('[data-focus=category-network] .status-badge').textContent==='未知'"));
  await route('services/all');
  check('无采集数据时显示未知，不冒出绿色延迟', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='未知' && document.querySelector('[data-service=ipv4] .service-latency').textContent==='—'"));
  for (const credential of agentTokens) await post('heartbeat', { id: id() }, credential);
  await post('ingest', { id: id(), replay: false, samples: expandedServices.filter((entry) => !['ipv4', 'tunnel-ready', 'tailscale-path', 'home-app'].includes(entry.id)).map((entry) => ({ id: id(), serviceId: entry.id, status: 'healthy', code: 'ok', ageSeconds: 0, latencyMs: 12.345 })) });
  await sample('ipv4');
  await sample('tunnel-ready', 'healthy', { readyConnections: 2 });
  await sample('tailscale-path', 'healthy', { pathMode: 'unknown' });
  await sample('home-app', 'untested', { code: 'untested', latencyMs: null });
  await refresh();
  check('认证上报经 SQLite 后真实显示测量值', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='正常' && document.querySelector('[data-service=ipv4] .service-latency').textContent==='18 ms'"));
  await route('overview');
  check('61 项观测总览只显示八组，完整来源默认折叠', await evaluate("document.querySelectorAll('.check-group-card').length===8 && !document.querySelector('.service-row') && !document.querySelector('[data-collectors]').open && document.querySelector('.collector-tally').textContent.includes('8 / 8')"));
  check('61 项全部归组，包含新 ID 的兜底项', await evaluate("document.querySelector('[data-focus=all-checks]').textContent.includes('61')"));
  await route('services/network'); await click('[data-group-history] summary');
  check('网络组内宽幅观测与历史，出口标签横向易读', await evaluate("document.querySelector('.service-panel').getBoundingClientRect().width>900 && document.querySelector('.chart-panel').getBoundingClientRect().width>900 && getComputedStyle(document.querySelector('.exit-family-label')).writingMode==='horizontal-tb' && parseFloat(getComputedStyle(document.querySelector('.exit-probe-value')).fontSize)>=32"));
  check('IPv4 DNS、IPv6 ICMP 和 HTTPS 四项测量独立显示', await evaluate("document.querySelectorAll('.exit-probe').length===4 && document.querySelector('.exit-measurements').textContent.includes('ICMP Ping 基准') && document.querySelector('.exit-measurements').textContent.includes('Ping 不代表 DNS')"));
  for (let i = 0; i < 3; i++) await sample('ipv6-baseline', 'down', { code: 'dns_error' });
  await refresh();
  check('DNS 故障保留，不传播到独立 ICMP/HTTPS 观测', await evaluate("document.querySelector('[data-service=ipv6-baseline] .status-badge').textContent==='故障' && document.querySelector('[data-timing-service=ipv6-icmp] .status-badge').textContent==='正常' && document.querySelector('[data-timing-service=ipv6] .status-badge').textContent==='正常'"));
  for (let i = 0; i < 3; i++) await sample('ipv6-icmp', 'down', { code: 'icmp_error' });
  await refresh();
  check('ICMP 超时撤销 RTT，不切换到 DNS 或撤销 HTTPS 结论', await evaluate("document.querySelector('[data-timing-service=ipv6-icmp] .status-badge').textContent==='故障' && document.querySelector('[data-timing-service=ipv6-icmp] .exit-probe-value').textContent==='—' && !document.querySelector('[data-timing-service=ipv6-baseline]') && document.querySelector('[data-timing-service=ipv6] .status-badge').textContent==='正常'"));
  await sample('ipv6-icmp'); await sample('ipv6-icmp'); await refresh();
  check('ICMP 两次成功后恢复，旧 DNS 故障独立保留', await evaluate("document.querySelector('[data-timing-service=ipv6-icmp] .status-badge').textContent==='正常' && document.querySelector('[data-service=ipv6-baseline] .status-badge').textContent==='故障'"));
  await route('overview');
  check('概况故障计数及所属网络组清晰', await evaluate("document.querySelector('.stat-foot').textContent.includes('1 故障') && document.querySelector('[data-focus=category-network] .status-badge').textContent==='故障'"));
  await click('[data-quick-status=down]');
  check('关注项快捷入口进入完整列表并应用故障筛选', await evaluate("location.hash==='#services/all' && document.querySelector('[data-control=status]').value==='down' && document.querySelectorAll('.service-row').length===1"));
  await evaluate("location.hash='overview'"); await wait(100);
  await click('[data-collectors] summary'); await refresh();
  check('来源展开状态在真实快照刷新后保留', await evaluate("document.querySelector('[data-collectors]').open && document.querySelectorAll('.collector-item').length===8"));
  await route('services/all');
  await evaluate("document.querySelector('[data-service=ipv4]').focus()"); await refresh();
  check('快照刷新后服务按钮键盘焦点保留', await evaluate("document.activeElement.dataset.focus==='service-ipv4'"));
  await route('paths');
  check('连接数显示上报的 2 条，不硬编码 4', await evaluate("document.querySelector('.path-observations [data-service=tunnel-ready]').textContent.includes('2 条就绪连接')"));
  check('没有路径证据时不冒充直连或中继，也不把未知标签涂绿', await evaluate("document.querySelector('.path-observations [data-service=tailscale-path]').textContent.includes('暂无新鲜节点探测') && document.querySelector('.path-observations [data-service=tailscale-path] .status-badge').classList.contains('unknown')"));
  const oldRecord = new Date(Date.now() - 2 * 3600000).toISOString();
  await sample('tailscale-path', 'unknown', { code: 'peer_no_evidence', pathMode: 'unknown', latencyMs: null, evidence: { kind: 'peer_success', at: oldRecord, historical: true } });
  await sample('resource-fixture-0', 'unknown', { code: 'job_previous_boot', latencyMs: null, evidence: { kind: 'job_success', at: oldRecord, historical: true } });
  await sample('headscale-control', 'healthy', { code: 'control_fresh', latencyMs: null, ageSeconds: 10, checkedAgeSeconds: 0, evidence: { kind: 'control_message', at: new Date(Date.now()-10000).toISOString(), historical: false } });
  await refresh();
  check('旧节点成功记录仅作参考，当前路径仍未知', await evaluate("document.querySelector('.path-observations [data-service=tailscale-path]').textContent.includes('仅作参考') && document.querySelector('.path-observations [data-service=tailscale-path] .status-badge').textContent==='未知'"));
  check('控制文案仅证明近期通信，不再声称本轮已同步', await evaluate("document.querySelector('.path-observations [data-service=headscale-control]').textContent.includes('近期有控制消息；不等于完全同步') && !document.querySelector('.path-panel').textContent.includes('本轮已同步')"));
  await route('services/all');
  check('列表不把历史节点成功变为当前 RTT', await evaluate("document.querySelector('[data-service=tailscale-path] .service-latency').textContent==='—'"));
  await click('.service-row[data-service=resource-fixture-0]');
  check('跨启动完成记录仍未知，详情区分观测与来源时间', await evaluate("document.querySelector('dialog .status-badge').textContent==='未知' && document.querySelector('dialog').textContent.includes('来源证据时间') && document.querySelector('dialog').textContent.includes('任务完成 · 仅作参考') && document.querySelector('dialog').textContent.includes('源主机时钟记录，不用于计算当前新鲜度')"));
  await click('[data-action=close-dialog]');
  await click('[data-service=ipv4]');
  check('详情标注真实 API 测量范围与稀疏覆盖', await evaluate("document.querySelector('dialog').textContent.includes('分母：1 个已测样本') && document.querySelector('dialog').textContent.includes('真实观测')"));
  await refresh();
  await click('[data-action=close-dialog]');
  check('详情更新后关闭返回原服务按钮', await evaluate("document.activeElement?.dataset.focus==='service-ipv4' || document.activeElement===document.querySelector('[data-service=ipv4]')"));
  for (let i = 0; i < 3; i++) await sample('resource-fixture-45', 'down');
  await refresh();
  await evaluate("document.querySelector('[data-service=resource-fixture-45]').focus()");
  await click('[data-service=resource-fixture-45]');
  await sample('resource-fixture-45'); await sample('resource-fixture-45'); await refresh();
  await click('[data-action=close-dialog]');
  check('固定组内项目恢复后仍保留行及可见焦点', await evaluate("document.querySelector('.service-row[data-service=resource-fixture-45] .status-badge').textContent==='正常' && document.activeElement.dataset.focus==='service-resource-fixture-45'"));
  await route('services/network');
  if (!await evaluate("document.querySelector('[data-group-history]').open")) await click('[data-group-history] summary');
  await click('[data-range="7d"]');
  await retry(async () => assert.ok(await evaluate("document.querySelector('.chart-panel').textContent.includes('336 个窗口点')")));
  check('7 天取独立历史端点，缺失窗口不补造', urls.has(`${origin}/api/v1/history?service=ipv4&range=7d`));
  await click('[data-range="24h"]');
  await evaluate('window.__now=Date.now;Date.now=()=>window.__now()+3600000');
  await wait(1100);
  check('用户墙钟跳变不把新鲜服务器观测误判过期', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='正常'"));
  await evaluate('Date.now=window.__now');
  for (let i = 0; i < 3; i++) await sample('ipv4', 'down');
  await refresh();
  check('连续三次失败后故障', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='故障'"));
  await route('events');
  check('产生真实持久事件', await evaluate("document.querySelectorAll('.incident').length>0"));
  await route('services/all');
  await sample('ipv4'); await sample('ipv4'); await refresh();
  check('连续成功后恢复', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='正常'"));
  await click('[data-service=ipv4]');
  await click('[data-action=close-dialog]'); await route('observations/ipv4');
  await retry(async () => assert.ok(await evaluate("document.querySelectorAll('.observation-row').length>=4")));
  check('真实逐次记录保留单次失败，不由当前恢复覆盖', await evaluate("document.querySelector('.observation-summary').textContent.includes('3 单次失败') && document.querySelector('.observation-panel .panel-heading').textContent.includes('不是已确认故障') && document.querySelector('.observation-row')"));
  check('原始记录经已认证历史读路由，未给浏览器采集凭据', [...urls].some(url => url.includes('/api/v1/history?') && url.includes('resolution=raw')));
  await route('services/all'); await click('[data-service=ipv4]');
  backend.kill(); await wait(300);
  await click('[data-action=refresh]');
  await retry(async () => assert.equal(await evaluate("document.querySelector('.demo-badge').textContent"), '接口不可达'));
  check('后端不可达时撤销当前延迟，不恢复演示绿灯', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='未知' && document.querySelector('[data-service=ipv4] .service-latency').textContent==='—' && !document.querySelector('[data-control=scenario]')"));
  check('已打开详情同步变未知，历史保留', await evaluate("document.querySelector('dialog .status-badge').textContent==='未知' && document.querySelector('dialog').textContent.includes('历史上报延迟')"));
  await click('[data-action=close-dialog]');
  await route('paths');
  check('API 失联保留旧节点来源记录，不补造新路径', await evaluate("document.querySelector('.path-observations [data-service=tailscale-path]').textContent.includes('仅作参考') && document.querySelector('.path-observations [data-service=tailscale-path] .status-badge').classList.contains('unknown')"));
  await route('services/all');
  backend = spawn(binary, ['-config', configFile], { cwd: temp, stdio: 'pipe' });
  await retry(async () => assert.equal((await fetch(`${backendOrigin}/healthz`)).status, 200));
  await refresh();
  check('后端重启后读取原 SQLite，不丢状态', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='正常'"));
  await sample('tailscale-path', 'healthy', { code: 'tsmp_ok', latencyMs: 16, pathMode: 'unknown' });
  await sample('tailscale-path', 'healthy', { code: 'tsmp_ok', latencyMs: 17, pathMode: 'unknown' }); await refresh();
  await route('paths');
  check('TSMP 数据通道可达与直连路径未知分开呈现', await evaluate("document.querySelector('.path-observations [data-service=tailscale-path] .status-badge').textContent==='可达' && document.querySelector('.path-observations [data-service=tailscale-path]').textContent.includes('直连／中继路径未判定')"));
  await route('events'); check('后端重启后持久事件仍保留', await evaluate("document.querySelectorAll('.incident').length>0"));
  for (const width of [1440, 1024, 768, 390, 320]) {
    await command('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 500 });
    for (const page of ['overview', 'services', 'services/network', 'services/all', 'paths', 'observations', 'events']) { await evaluate(`location.hash=${JSON.stringify(page)}`); await wait(80); check(`${width}px ${page} 61项真实接口模式无横向溢出`, await evaluate('document.documentElement.scrollWidth<=innerWidth+1')); }
    if (width === 390) { await evaluate("location.hash='services/all'"); await wait(80); check('完整列表保持 61 项、长名称与筛选计数可读', await evaluate("document.querySelectorAll('.service-row').length===61 && document.querySelector('.service-result-count').textContent.includes('61 项匹配')")); }
  }
  check('无 JavaScript 运行错误', errors.length === 0);
  check('浏览器不保存采集凭据，也不连接设备地址', !(await evaluate('document.documentElement.outerHTML')).includes(token) && await evaluate('localStorage.length===0 && sessionStorage.length===0') && [...urls].every((url) => url.startsWith(origin) || url.startsWith('data:')));
  await command('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await evaluate("location.hash='overview'"); await wait(100);
  await mkdir(path.join(root, 'artifacts'), { recursive: true });
  await evaluate("document.querySelector('#toast').classList.remove('visible'); document.querySelector('[data-collectors]').open=false");
  const layout = await command('Page.getLayoutMetrics');
  const shot = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width: 1440, height: Math.ceil(layout.cssContentSize.height), scale: 1 } });
  await writeFile(path.join(root, 'artifacts/live-integration-local.png'), Buffer.from(shot.data, 'base64'));
  const viewportShot = await command('Page.captureScreenshot', { format: 'png', clip: { x: 0, y: 0, width: 1440, height: 1000, scale: 1 } });
  await writeFile(path.join(root, 'artifacts/live-redesign-desktop-viewport.png'), Buffer.from(viewportShot.data, 'base64'));
  await command('Emulation.setTouchEmulationEnabled', { enabled: true });
  await command('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await wait(100);
  const mobileLayout = await command('Page.getLayoutMetrics');
  const mobileShot = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width: 390, height: Math.ceil(mobileLayout.cssContentSize.height), scale: 1 } });
  await writeFile(path.join(root, 'artifacts/live-expanded-mobile.png'), Buffer.from(mobileShot.data, 'base64'));
  const mobileViewportShot = await command('Page.captureScreenshot', { format: 'png', clip: { x: 0, y: 0, width: 390, height: 844, scale: 1 } });
  await writeFile(path.join(root, 'artifacts/live-redesign-mobile-viewport.png'), Buffer.from(mobileViewportShot.data, 'base64'));
  console.log(`\n${passed} live API browser checks passed (local generated fixtures only).`);
} finally {
  if (ws?.readyState === WebSocket.OPEN) { try { await command('Browser.close'); } catch {} ws.close(); }
  backend?.kill(); server?.kill(); chrome?.kill(); await wait(500);
  await rm(temp, { recursive: true, force: true, maxRetries: 5 }).catch(() => {});
}
