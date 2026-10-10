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
  services: expandedServices.map((entry) => ({ id: entry.id, name: entry.name, group: entry.group, subtitle: '本地测试夹具，非真实部署', collectorId: 'home', scope: '本机 Go/SQLite 集成测试输入；不代表家庭业务', staleAfterSeconds: 90 })),
}), { mode: 0o600 });
let backend, server, chrome, ws;
let seq = 0, passed = 0;
const pending = new Map(), errors = [], urls = new Set();
function check(name, ok) { assert.ok(ok, name); console.log(`PASS LIVE ${++passed} ${name}`); }
function command(method, params = {}) { const id = ++seq; return new Promise((resolve, reject) => { const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 12000); pending.set(id, { resolve, reject, timer }); ws.send(JSON.stringify({ id, method, params })); }); }
async function evaluate(expression) { const r = await command('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }); if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text); return r.result.value; }
async function click(selector) { await evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`); await wait(100); }
async function refresh() { await click('[data-action=refresh]'); await retry(async () => { assert.equal(await evaluate("document.querySelector('.demo-badge').textContent"), '真实观测'); }); }
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
  check('无采集数据时显示未知，不冒出绿色延迟', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='未知' && document.querySelector('[data-service=ipv4] .service-latency').textContent==='—'"));
  for (const credential of agentTokens) await post('heartbeat', { id: id() }, credential);
  await post('ingest', { id: id(), replay: false, samples: expandedServices.filter((entry) => !['ipv4', 'tunnel-ready', 'tailscale-path', 'home-app'].includes(entry.id)).map((entry) => ({ id: id(), serviceId: entry.id, status: 'healthy', code: 'ok', ageSeconds: 0, latencyMs: 12.345 })) });
  await sample('ipv4');
  await sample('tunnel-ready', 'healthy', { readyConnections: 2 });
  await sample('tailscale-path', 'healthy', { pathMode: 'unknown' });
  await sample('home-app', 'untested', { code: 'untested', latencyMs: null });
  await refresh();
  check('认证上报经 SQLite 后真实显示测量值', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='正常' && document.querySelector('[data-service=ipv4] .service-latency').textContent==='18 ms'"));
  check('60 项观测总览最多 8 行，完整来源默认折叠', await evaluate("document.querySelectorAll('.service-row').length===8 && !document.querySelector('[data-collectors]').open && document.querySelector('.collector-tally').textContent.includes('8 / 8')"));
  check('IPv4/IPv6 DNS 与 HTTPS 四项测量独立显示', await evaluate("document.querySelectorAll('.exit-probe').length===4 && document.querySelector('.exit-measurements').textContent.includes('不是 ping') && document.querySelector('.stat-label').textContent.includes('当前概况')"));
  for (let i = 0; i < 3; i++) await sample('ipv6-baseline', 'down', { code: 'dns_error' });
  await refresh();
  check('DNS 基准失败不误判独立 IPv6 HTTPS，缺失测量不冒充旧值', await evaluate("document.querySelector('[data-timing-service=ipv6-baseline] .status-badge').textContent==='故障' && document.querySelector('[data-timing-service=ipv6-baseline] .exit-probe-value').textContent==='—' && document.querySelector('[data-timing-service=ipv6] .status-badge').textContent==='正常'"));
  check('概况故障计数清晰且需关注项置顶', await evaluate("document.querySelector('.stat-foot').textContent.includes('1 故障') && document.querySelector('.service-row').dataset.service==='ipv6-baseline'"));
  await click('[data-quick-status=down]');
  check('关注项快捷入口进入完整列表并应用故障筛选', await evaluate("location.hash==='#services' && document.querySelector('[data-control=status]').value==='down' && document.querySelectorAll('.service-row').length===1"));
  await evaluate("location.hash='overview'"); await wait(100);
  await click('[data-collectors] summary'); await refresh();
  check('来源展开状态在真实快照刷新后保留', await evaluate("document.querySelector('[data-collectors]').open && document.querySelectorAll('.collector-item').length===8"));
  await evaluate("document.querySelector('[data-service=ipv4]').focus()"); await refresh();
  check('快照刷新后服务按钮键盘焦点保留', await evaluate("document.activeElement.dataset.focus==='service-ipv4'"));
  check('连接数显示上报的 2 条，不硬编码 4', await evaluate("document.querySelector('.path-observations [data-service=tunnel-ready]').textContent.includes('2 条就绪连接')"));
  check('没有路径证据时不冒充直连或中继', await evaluate("document.querySelector('.path-observations [data-service=tailscale-path]').textContent.includes('尚无端到端路径证据')"));
  await click('[data-service=ipv4]');
  check('详情标注真实 API 测量范围与稀疏覆盖', await evaluate("document.querySelector('dialog').textContent.includes('分母：1 个已测样本') && document.querySelector('dialog').textContent.includes('真实观测')"));
  await refresh();
  await click('[data-action=close-dialog]');
  check('详情更新后关闭返回原服务按钮', await evaluate("document.activeElement.dataset.focus==='service-ipv4'"));
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
  check('连续三次失败后故障，产生真实持久事件', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='故障' && document.querySelectorAll('.incident').length>0"));
  await sample('ipv4'); await sample('ipv4'); await refresh();
  check('连续成功后恢复', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='正常'"));
  await click('[data-service=ipv4]');
  backend.kill(); await wait(300);
  await click('[data-action=refresh]');
  await retry(async () => assert.equal(await evaluate("document.querySelector('.demo-badge').textContent"), '接口不可达'));
  check('后端不可达时撤销当前延迟，不恢复演示绿灯', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='未知' && document.querySelector('[data-service=ipv4] .service-latency').textContent==='—' && !document.querySelector('[data-control=scenario]')"));
  check('已打开详情同步变未知，历史保留', await evaluate("document.querySelector('dialog .status-badge').textContent==='未知' && document.querySelector('dialog').textContent.includes('历史上报延迟')"));
  await click('[data-action=close-dialog]');
  backend = spawn(binary, ['-config', configFile], { cwd: temp, stdio: 'pipe' });
  await retry(async () => assert.equal((await fetch(`${backendOrigin}/healthz`)).status, 200));
  await refresh();
  check('后端重启后读取原 SQLite，不丢状态与事件', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='正常' && document.querySelectorAll('.incident').length>0"));
  for (const width of [1440, 1024, 768, 390, 320]) {
    await command('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 500 });
    for (const page of ['overview', 'services', 'paths', 'events']) { await evaluate(`location.hash=${JSON.stringify(page)}`); await wait(80); check(`${width}px ${page} 60项真实接口模式无横向溢出`, await evaluate('document.documentElement.scrollWidth<=innerWidth+1')); }
    if (width === 390) { await evaluate("location.hash='services'"); await wait(80); check('完整列表保持 60 项、长名称与筛选计数可读', await evaluate("document.querySelectorAll('.service-row').length===60 && document.querySelector('.service-result-count').textContent.includes('60 项匹配')")); }
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
  await command('Emulation.setTouchEmulationEnabled', { enabled: true });
  await command('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await wait(100);
  const mobileLayout = await command('Page.getLayoutMetrics');
  const mobileShot = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width: 390, height: Math.ceil(mobileLayout.cssContentSize.height), scale: 1 } });
  await writeFile(path.join(root, 'artifacts/live-expanded-mobile.png'), Buffer.from(mobileShot.data, 'base64'));
  console.log(`\n${passed} live API browser checks passed (local generated fixtures only).`);
} finally {
  if (ws?.readyState === WebSocket.OPEN) { try { await command('Browser.close'); } catch {} ws.close(); }
  backend?.kill(); server?.kill(); chrome?.kill(); await wait(500);
  await rm(temp, { recursive: true, force: true, maxRetries: 5 }).catch(() => {});
}
