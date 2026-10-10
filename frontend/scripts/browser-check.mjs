// Dependency-free local Chrome/CDP smoke checks. Never uses MCP or a user's browser profile.
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import assert from 'node:assert/strict';

const root = fileURLToPath(new URL('../', import.meta.url));
const chromePath = process.env.CHROME_PATH || [
  'C:/Program Files/Google/Chrome/Application/chrome.exe',
  'C:/Program Files (x86)/Google/Chrome/Application/chrome.exe',
  '/usr/bin/google-chrome', '/usr/bin/chromium', '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].find(existsSync);
if (!chromePath) throw new Error('Chrome 未找到；请设置 CHROME_PATH。检查只使用本机浏览器，不安装或调用 MCP。');
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function port() {
  const server = net.createServer();
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const assigned = server.address().port;
  await new Promise((resolve) => server.close(resolve));
  return assigned;
}
async function retry(fn) {
  let last;
  for (let i = 0; i < 80; i++) { try { return await fn(); } catch (error) { last = error; await wait(100); } }
  throw last;
}
const serverPort = await port();
const debugPort = await port();
const origin = `http://127.0.0.1:${serverPort}`;
const profile = await mkdtemp(path.join(tmpdir(), 'bastion-ui-check-'));
const server = spawn(process.execPath, [path.join(root, 'node_modules/vite/bin/vite.js'), 'preview', '--host', '127.0.0.1', '--port', String(serverPort), '--strictPort'], { cwd: root, stdio: 'pipe' });
const chrome = spawn(chromePath, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', `--remote-debugging-port=${debugPort}`, '--remote-debugging-address=127.0.0.1', `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' });
let ws;
let seq = 0;
const pending = new Map();
const errors = [];
const urls = new Set();
let passed = 0;
function check(name, condition) { assert.ok(condition, name); passed++; console.log(`PASS ${String(passed).padStart(2, '0')} ${name}`); }
function command(method, params = {}) {
  const id = ++seq;
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 12000);
    pending.set(id, { resolve, reject, timeout });
    ws.send(JSON.stringify({ id, method, params }));
  });
}
async function evaluate(expression) {
  const result = await command('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text + ' ' + result.exceptionDetails.exception?.description);
  return result.result.value;
}
async function click(selector) { await evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`); await wait(70); }
async function scenario(id) {
  await evaluate(`(() => { const s=document.querySelector('[data-control="scenario"]'); s.value=${JSON.stringify(id)}; s.dispatchEvent(new Event('change',{bubbles:true})); })()`);
  await wait(70);
}
async function route(name) { await evaluate(`location.hash=${JSON.stringify(name)}`); await wait(100); }
async function screenshot(name, width, height) {
  await command('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 500 });
  await wait(120);
  await evaluate('document.fonts.ready.then(() => true)');
  await evaluate("document.querySelector('#toast').classList.remove('visible')");
  const layout = await command('Page.getLayoutMetrics');
  const content = layout.cssContentSize;
  const modalOpen = await evaluate("document.querySelector('dialog').open");
  const image = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: !modalOpen, clip: { x: 0, y: 0, width, height: modalOpen ? height : Math.ceil(content.height), scale: 1 } });
  await writeFile(path.join(root, 'artifacts', name), Buffer.from(image.data, 'base64'));
}
try {
  await retry(async () => { const response = await fetch(origin); if (!response.ok) throw new Error('preview server pending'); });
  const targets = await retry(async () => { const response = await fetch(`http://127.0.0.1:${debugPort}/json`); return response.json(); });
  const target = targets.find((entry) => entry.type === 'page');
  ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { ws.addEventListener('open', resolve, { once: true }); ws.addEventListener('error', reject, { once: true }); });
  ws.addEventListener('message', ({ data }) => {
    const message = JSON.parse(data);
    if (message.id) {
      const item = pending.get(message.id);
      if (!item) return;
      clearTimeout(item.timeout); pending.delete(message.id);
      message.error ? item.reject(new Error(message.error.message)) : item.resolve(message.result);
    } else if (message.method === 'Runtime.exceptionThrown') errors.push(message.params.exceptionDetails.exception?.description || message.params.exceptionDetails.text);
    else if (message.method === 'Log.entryAdded' && message.params.entry.level === 'error') errors.push(message.params.entry.text);
    else if (message.method === 'Network.requestWillBeSent') urls.add(message.params.request.url);
  });
  await command('Page.enable'); await command('Runtime.enable'); await command('Network.enable'); await command('Log.enable');
  await command('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
  await command('Page.navigate', { url: `${origin}/?demo=1` }); await wait(500);
  check('总览加载且始终标注演示模式', await evaluate("document.querySelector('h1').textContent.includes('连接') && document.querySelector('.demo-notice').textContent.includes('合成演示')"));
  check('黑色主题，无外部字体或资源', await evaluate("getComputedStyle(document.documentElement).backgroundColor === 'rgb(13, 15, 17)'"));
  check('桌面不显示手机导航按钮', await evaluate("getComputedStyle(document.querySelector('.mobile-menu')).display==='none'"));
  check('默认 IPv6 为降级，不连带误判 IPv4', await evaluate("document.querySelector('[data-service=ipv6] .status-badge').textContent === '降级' && document.querySelector('[data-service=ipv4] .status-badge').textContent === '正常'"));
  await mkdir(path.join(root, 'artifacts'), { recursive: true });
  await screenshot('overview-desktop.png', 1440, 1100);
  await click('[data-range="7d"]');
  check('7天显示缺少数据，而非冒用24h样本', await evaluate("document.querySelector('.chart-empty').textContent.includes('还没有 7 天')"));
  await click('[data-range="24h"]');
  await click('[data-service="ipv6"]');
  check('服务详情为原生模态框，含检测边界', await evaluate("document.querySelector('dialog').open && document.querySelector('dialog').textContent.includes('检测边界')"));
  await command('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  check('Escape 能关闭模态详情', await evaluate("!document.querySelector('dialog').open"));
  await scenario('healthy');
  check('正常场景不宣称所有家庭业务可用', await evaluate("document.querySelector('.stat-text').textContent.includes('已测链路正常')"));
  await route('services');
  check('全部服务页面显示12项且业务仍未测试', await evaluate("document.querySelectorAll('.service-row').length === 12 && document.querySelector('[data-service=home-app] .status-badge').textContent === '未测试'"));
  await evaluate("(() => { const s=document.querySelector('[data-control=status]');s.value='untested';s.dispatchEvent(new Event('change',{bubbles:true})); })()");
  check('状态筛选有效', await evaluate("document.querySelectorAll('.service-row').length === 1"));
  await evaluate("(() => { const s=document.querySelector('[data-control=status]');s.value='all';s.dispatchEvent(new Event('change',{bubbles:true})); const i=document.querySelector('[data-control=search]');i.value='DNS';i.dispatchEvent(new Event('input',{bubbles:true})); })()");
  check('服务名称搜索有效且保留输入', await evaluate("document.querySelectorAll('.service-row').length === 1 && document.querySelector('[data-control=search]').value==='DNS'"));
  await scenario('lost');
  await route('overview');
  check('采集失联显示未知，不显示家庭故障', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='未知' && document.querySelector('[data-service=ipv4] .service-latency').textContent==='—' && document.querySelector('.stat-text').textContent.includes('观测数据缺失')"));
  await route('services');
  check('失联时公网独立观测仍正常，业务仍未测试', await evaluate("document.querySelector('[data-service=headscale-base] .status-badge').textContent==='正常' && document.querySelector('[data-service=home-app] .status-badge').textContent==='未测试'"));
  await click('[data-service="gateway"]');
  check('过期详情区分当前结论与历史延迟', await evaluate("document.querySelector('dialog').textContent.includes('历史上报延迟') && document.querySelector('dialog').textContent.includes('不作为当前延迟')"));
  await click('[data-action="close-dialog"]');
  await route('paths');
  check('链路页区分 CONTROL / DATA / BUSINESS', await evaluate("document.querySelectorAll('.boundary-columns article').length === 3"));
  await route('events');
  check('事件筛选有效', await evaluate("document.querySelectorAll('.incident').length === 2"));
  await click('[data-event-filter="resolved"]');
  check('空事件筛选显示空态', await evaluate("document.querySelector('.empty-state').textContent.includes('没有事件')"));
  await scenario('degraded'); await route('overview');
  // Confirm staleness uses elapsed observation time, including an already-open dialog.
  await click('[data-service="ipv4"]');
  await evaluate('window.__originalNow=Date.now; Date.now=()=>window.__originalNow()+100000');
  await wait(1150);
  check('停留超过有效期自动未知，未假装后台实时刷新', await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='未知'"));
  check('已打开详情也自动更新为未知，历史延迟单独标记', await evaluate("document.querySelector('dialog .dialog-status .status-badge').textContent==='未知' && document.querySelector('dialog .dialog-status .mono').textContent==='—' && document.querySelector('dialog').textContent.includes('历史上报延迟')"));
  await click('[data-action="close-dialog"]');
  await evaluate('Date.now=window.__originalNow'); await click('[data-action="refresh"]');
  check('刷新只生成新的演示快照并提示', await evaluate("document.querySelector('#toast').textContent.includes('未请求真实设备') && document.querySelector('[data-service=ipv4] .status-badge').textContent==='正常'"));
  for (const width of [1440, 1024, 768, 390, 320]) {
    await command('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 500 });
    await wait(80);
    for (const page of ['overview', 'services', 'paths', 'events']) {
      await route(page);
      check(`${width}px ${page} 无横向溢出`, await evaluate('document.documentElement.scrollWidth <= innerWidth + 1'));
    }
  }
  await scenario('degraded'); await route('overview');
  await screenshot('overview-mobile.png', 390, 844);
  check('关闭的手机侧栏不进入键盘焦点序列', await evaluate("document.querySelector('.sidebar').inert"));
  await click('[data-action="menu"]');
  check('手机导航可展开', await evaluate("document.querySelector('.sidebar').classList.contains('is-open')"));
  check('手机导航打开时背景不可操作且聚焦侧栏', await evaluate("document.querySelector('.workspace-body').inert && document.querySelector('.sidebar').contains(document.activeElement)"));
  await evaluate("document.querySelector('.sidebar nav a:last-child').focus()");
  await command('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
  await command('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
  check('手机导航键盘焦点保持在抽屉内', await evaluate("document.activeElement===document.querySelector('.sidebar .brand')"));
  await click('[data-action="close-menu"]');
  check('手机导航遮罩可关闭', await evaluate("!document.querySelector('.sidebar').classList.contains('is-open')"));
  check('手机导航关闭后焦点回到菜单按钮', await evaluate("document.activeElement===document.querySelector('[data-action=menu]') && !document.querySelector('.workspace-body').inert"));
  await command('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
  await route('paths'); await screenshot('paths-desktop.png', 1440, 1100);
  await route('services'); await click('[data-service="ipv6"]'); await screenshot('service-detail.png', 1440, 1100);
  check('页面无浏览器运行错误', errors.length === 0);
  check('仅请求本地静态资产，无设备/API/外部请求', [...urls].every((url) => url.startsWith(origin) || url.startsWith('data:')));
  console.log(`\n${passed} browser checks passed. Screenshots: frontend/artifacts/`);
} finally {
  if (ws?.readyState === WebSocket.OPEN) { try { await command('Browser.close'); } catch {} ws.close(); }
  server.kill();
  chrome.kill();
  await wait(300);
  await rm(profile, { recursive: true, force: true, maxRetries: 3 }).catch(() => {});
}
