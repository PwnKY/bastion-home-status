// Local generated fixtures + isolated Chrome/CDP only. Never user profiles/MCP.
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import assert from 'node:assert/strict';
const root = fileURLToPath(new URL('../', import.meta.url));
const chromePath = process.env.CHROME_PATH || ['C:/Program Files/Google/Chrome/Application/chrome.exe','C:/Program Files (x86)/Google/Chrome/Application/chrome.exe','/usr/bin/google-chrome','/usr/bin/chromium','/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'].find(existsSync);
if(!chromePath) throw new Error('Chrome missing; set CHROME_PATH. No MCP/profile reuse.');
const wait=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function port(){const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p;}
async function retry(fn){let last;for(let i=0;i<80;i++){try{return await fn();}catch(e){last=e;await wait(100);}}throw last;}
const serverPort=await port(),debugPort=await port(),origin=`http://127.0.0.1:${serverPort}`;
const profile=await mkdtemp(path.join(tmpdir(),'bastion-group-check-'));
const server=spawn(process.execPath,[path.join(root,'node_modules/vite/bin/vite.js'),'preview','--host','127.0.0.1','--port',String(serverPort),'--strictPort'],{cwd:root,stdio:'pipe'});
const chrome=spawn(chromePath,['--headless=new','--disable-gpu','--no-first-run','--no-default-browser-check','--disable-background-networking',`--remote-debugging-port=${debugPort}`,'--remote-debugging-address=127.0.0.1',`--user-data-dir=${profile}`,'about:blank'],{stdio:'ignore'});
let ws,seq=0,passed=0;const pending=new Map(),errors=[],urls=new Set();
function check(name,ok){assert.ok(ok,name);console.log(`PASS ${++passed} ${name}`);}
function command(method,params={}){const id=++seq;return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{pending.delete(id);reject(new Error(`CDP timeout ${method}`));},12000);pending.set(id,{resolve,reject,timer});ws.send(JSON.stringify({id,method,params}));});}
async function evaluate(expression){const r=await command('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw new Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result.value;}
async function click(selector){await evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`);await wait(100);}
async function route(hash){await evaluate(`location.hash=${JSON.stringify(hash)}`);await wait(100);}
async function scenario(id){await evaluate(`(()=>{const s=document.querySelector('[data-control=scenario]');s.value=${JSON.stringify(id)};s.dispatchEvent(new Event('change',{bubbles:true}));})()`);await wait(100);}
async function viewport(width,height=900){await command('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:width<500});await wait(100);}
async function screenshot(name,width,height){await viewport(width,height);await evaluate('document.fonts.ready.then(()=>true)');await evaluate("document.querySelector('#toast').classList.remove('visible')");const layout=await command('Page.getLayoutMetrics');const modal=await evaluate('document.querySelector("dialog").open');const image=await command('Page.captureScreenshot',{format:'png',captureBeyondViewport:!modal,clip:{x:0,y:0,width,height:modal?height:Math.ceil(layout.cssContentSize.height),scale:1}});await writeFile(path.join(root,'artifacts',name),Buffer.from(image.data,'base64'));}
try{
 await retry(async()=>assert.equal((await fetch(origin)).status,200));
 const targets=await retry(async()=>(await fetch(`http://127.0.0.1:${debugPort}/json`)).json());ws=new WebSocket(targets.find(e=>e.type==='page').webSocketDebuggerUrl);
 await new Promise((resolve,reject)=>{ws.addEventListener('open',resolve,{once:true});ws.addEventListener('error',reject,{once:true});});
 ws.addEventListener('message',({data})=>{const m=JSON.parse(data);if(m.id){const p=pending.get(m.id);if(!p)return;clearTimeout(p.timer);pending.delete(m.id);m.error?p.reject(new Error(m.error.message)):p.resolve(m.result);}else if(m.method==='Runtime.exceptionThrown')errors.push(m.params.exceptionDetails.text);else if(m.method==='Network.requestWillBeSent')urls.add(m.params.request.url);});
 await command('Page.enable');await command('Runtime.enable');await command('Network.enable');await viewport(1440,1000);
 await command('Page.navigate',{url:`${origin}/?demo=1&catalog=expanded`});await wait(500);
 check('60-item preview is explicitly synthetic',await evaluate("document.querySelector('.demo-notice').textContent.includes('合成演示') && document.querySelector('[data-control=scenario]')"));
 check('overview shows eight group entry points, no giant table/chart/path pile',await evaluate("document.querySelectorAll('.check-group-card').length===8 && !document.querySelector('.service-row') && !document.querySelector('.chart-panel') && !document.querySelector('.path-panel')"));
 check('all current checks counted without retired placeholders',await evaluate("document.querySelector('[data-focus=all-checks]').textContent.includes('60') && !document.body.textContent.includes('家庭应用')"));
 check('black theme and readable compact summaries retained',await evaluate("getComputedStyle(document.documentElement).backgroundColor==='rgb(13, 15, 17)' && document.querySelectorAll('.stat-card').length===3"));
 check('desktop menu remains hidden',await evaluate("getComputedStyle(document.querySelector('.mobile-menu')).display==='none'"));
 check('unknown group is not green and includes explicit unknown count',await evaluate("document.querySelector('[data-focus=category-access] .status-badge').classList.contains('unknown') && document.querySelector('[data-focus=category-access]').textContent.includes('1 未知')"));
 await mkdir(path.join(root,'artifacts'),{recursive:true});await screenshot('grouped-overview-desktop.png',1440,1000);
 await click('[data-focus=category-dns]');
 check('category link has a deep URL and only its seven checks',await evaluate("location.hash==='#services/dns' && document.querySelector('h1').textContent==='DNS 解析' && document.querySelectorAll('.service-row').length===7 && !document.querySelector('[data-service=ipv4]')"));
 await screenshot('grouped-dns-desktop.png',1440,1000);
 await evaluate("document.querySelector('[data-service=dns]').focus()");await click('[data-service=dns]');
 check('individual detail still exposes scope/source/history, not settings',await evaluate("document.querySelector('dialog').open && document.querySelector('dialog').textContent.includes('检测边界') && document.querySelector('dialog').textContent.includes('观测来源')"));
 await command('Input.dispatchKeyEvent',{type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27});await command('Input.dispatchKeyEvent',{type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27});
 check('Esc closes detail and restores its row focus',await evaluate("!document.querySelector('dialog').open && document.activeElement.dataset.focus==='service-dns'"));
 await evaluate("(()=>{const i=document.querySelector('[data-control=search]');i.value='TCP';i.dispatchEvent(new Event('input',{bubbles:true}));})()");
 check('search is scoped to group and count',await evaluate("document.querySelectorAll('.service-row').length===1 && document.querySelector('[data-service=dns-local-tcp]') && document.querySelector('.service-result-count').textContent.includes('本组 7 项')"));
 await click('[data-focus=back-groups]');
 check('return to groups restores originating card focus',await evaluate("location.hash==='#services' && document.activeElement.dataset.focus==='category-dns' && !document.querySelector('.service-row')"));
 await click('[data-focus=all-checks]');
 check('all-items is an explicit secondary view with 60 rows',await evaluate("location.hash==='#services/all' && document.querySelectorAll('.service-row').length===60"));
 await evaluate("(()=>{const s=document.querySelector('[data-control=status]');s.value='unknown';s.dispatchEvent(new Event('change',{bubbles:true}));})()");
 check('global unknown filter returns two missing-evidence items',await evaluate("document.querySelectorAll('.service-row').length===2 && document.querySelector('[data-service=tailscale-path]')"));
 await route('overview');await click('[data-quick-status=unknown]');
 check('attention shortcut opens all-items with explicit filter',await evaluate("location.hash==='#services/all' && document.querySelector('[data-control=status]').value==='unknown' && document.querySelectorAll('.service-row').length===2"));
 await route('services/network');
 check('network detail has six distinct checks; extra history collapsed',await evaluate("document.querySelectorAll('.service-row').length===6 && !document.querySelector('[data-group-history]').open"));
 await click('[data-group-history] summary');await click('[data-range="7d"]');
 check('missing 7d history is not borrowed from 24h',await evaluate("document.querySelector('.chart-empty').textContent.includes('还没有 7 天')"));
 await click('[data-action=refresh]');
 check('manual refresh preserves expanded history',await evaluate("document.querySelector('[data-group-history]').open"));
 await click('[data-range="24h"]');
 await route('services/access');await click('[data-service=tailscale-path]');
 check('old peer reference has no current RTT or green conclusion',await evaluate("document.querySelector('dialog .status-badge').textContent==='未知' && document.querySelector('dialog .dialog-status .mono').textContent==='—' && document.querySelector('dialog').textContent.includes('仅作参考')"));
 await click('[data-action=close-dialog]');await scenario('healthy');await route('overview');
 check('healthy summary still limits its conclusion to measured scope',await evaluate("document.querySelector('.stat-text').textContent.includes('已测范围内正常')"));
 await scenario('lost');
 check('source loss makes household groups unknown, not faults',await evaluate("document.querySelector('.stat-text').textContent.includes('观测数据缺失') && document.querySelector('[data-focus=category-network] .status-badge').textContent==='未知'"));
 await route('services/all');
 check('source loss revokes RTT but independent edge checks stay fresh',await evaluate("document.querySelector('[data-service=ipv4] .service-latency').textContent==='—' && document.querySelector('[data-service=headscale-base] .status-badge').textContent==='正常'"));
 await scenario('degraded');await route('services/network');await evaluate("document.querySelector('[data-service=ipv4]').focus()");await click('[data-service=ipv4]');
 await evaluate('window.__originalNow=Date.now;Date.now=()=>window.__originalNow()+100000');await wait(1200);
 check('time expiry updates row and already-open detail without fake refresh',await evaluate("document.querySelector('[data-service=ipv4] .status-badge').textContent==='未知' && document.querySelector('dialog .dialog-status .mono').textContent==='—' && document.querySelector('dialog').textContent.includes('历史上报延迟')"));
 await click('[data-action=close-dialog]');await evaluate('Date.now=window.__originalNow');await click('[data-action=refresh]');
 await route('paths');check('CONTROL/DATA/BUSINESS separation remains',await evaluate("document.querySelectorAll('.boundary-columns article').length===3"));
 await route('events');await click('[data-event-filter=resolved]');check('events retain filtered empty state',await evaluate("document.querySelector('.empty-state').textContent.includes('没有事件')"));
 for(const width of [1440,1024,768,390,320]){
  await viewport(width);
  for(const page of ['overview','services','services/dns','services/all','paths','observations','events']){
   await route(page);check(`${width}px ${page} no horizontal overflow`,await evaluate('document.documentElement.scrollWidth<=innerWidth+1'));
   check(`${width}px ${page} visible text >=13px`,await evaluate("(()=>{const w=document.createTreeWalker(document.querySelector('#app'),NodeFilter.SHOW_TEXT);let n;while(n=w.nextNode()){if(!n.textContent.trim())continue;const e=n.parentElement,r=e.getBoundingClientRect();if(r.width&&r.height&&r.right>0&&r.bottom>0&&getComputedStyle(e).visibility==='visible'&&parseFloat(getComputedStyle(e).fontSize)<13)return false;}return getComputedStyle(document.documentElement).fontSize==='16px';})()"));
  }
 }
 await viewport(390,844);await command('Emulation.setTouchEmulationEnabled',{enabled:true});await route('overview');await screenshot('grouped-overview-mobile.png',390,844);
 check('mobile cards follow DOM order and have usable touch height',await evaluate("(()=>{const cards=[...document.querySelectorAll('.check-group-card')];const tops=cards.map(c=>c.getBoundingClientRect().top);return cards.every(c=>c.getBoundingClientRect().height>=44)&&tops.every((n,i)=>i===0||n>tops[i-1]);})()"));
 await route('services/dns');await screenshot('grouped-dns-mobile.png',390,844);
 check('touch inputs >=16px and zoom/safe-area allowed',await evaluate("parseFloat(getComputedStyle(document.querySelector('[data-control=search]')).fontSize)>=16 && !/user-scalable=no|maximum-scale/.test(document.querySelector('meta[name=viewport]').content) && document.querySelector('meta[name=viewport]').content.includes('viewport-fit=cover')"));
 check('hover rules all gated by mouse capability',await evaluate("(()=>{let bad=0;function walk(rules,g=false){for(const r of rules){const n=g||(r.conditionText?.includes('hover: hover')&&r.conditionText?.includes('pointer: fine'));if(r.selectorText?.includes(':hover')&&!n)bad++;if(r.cssRules)walk(r.cssRules,n);}}for(const s of document.styleSheets)walk(s.cssRules);return !bad;})()"));
 check('closed mobile nav is inert',await evaluate("document.querySelector('.sidebar').inert"));await click('[data-action=menu]');
 check('mobile menu locks background and owns focus',await evaluate("document.querySelector('.workspace-body').inert && document.querySelector('.sidebar').contains(document.activeElement)"));
 await evaluate("document.querySelector('.sidebar nav a:last-child').focus()");await command('Input.dispatchKeyEvent',{type:'keyDown',key:'Tab',code:'Tab',windowsVirtualKeyCode:9});await command('Input.dispatchKeyEvent',{type:'keyUp',key:'Tab',code:'Tab',windowsVirtualKeyCode:9});
 check('menu Tab wraps inside drawer',await evaluate("document.activeElement===document.querySelector('.sidebar .brand')"));await click('[data-action=close-menu]');
 check('closing nav restores menu focus',await evaluate("document.activeElement===document.querySelector('[data-action=menu]') && !document.querySelector('.workspace-body').inert"));
 await command('Emulation.setTouchEmulationEnabled',{enabled:false});await viewport(1440);
 await evaluate("document.querySelector('[data-action=refresh]').focus()");await command('Input.dispatchKeyEvent',{type:'keyDown',key:'Enter',code:'Enter',windowsVirtualKeyCode:13});await command('Input.dispatchKeyEvent',{type:'keyUp',key:'Enter',code:'Enter',windowsVirtualKeyCode:13});
 check('keyboard activation has immediate feedback, no transition',await evaluate("document.documentElement.dataset.input==='keyboard' && getComputedStyle(document.querySelector('[data-action=refresh]')).transitionDuration==='0s'"));
 await command('Emulation.setEmulatedMedia',{features:[{name:'prefers-reduced-motion',value:'reduce'}]});await evaluate("document.documentElement.dataset.input='pointer'");
 check('reduced motion removes press transition',await evaluate("parseFloat(getComputedStyle(document.querySelector('[data-action=refresh]')).transitionDuration)<.001"));
 await command('Emulation.setEmulatedMedia',{features:[]});await viewport(720,550);
 for(const page of ['overview','services/dns','services/all','paths','observations','events']){await route(page);check(`720px zoom-equivalent ${page} reflows`,await evaluate('document.documentElement.scrollWidth<=innerWidth+1'));}
 await route('services/access');await click('[data-service=tailscale-path]');await screenshot('grouped-reference-detail.png',1440,1000);
 await click('[data-action=close-dialog]'); await route('observations/ipv4');
 check('raw history exposes single failure without confirmed-downtime claims',await evaluate("document.querySelector('.observation-summary').textContent.includes('1 单次失败') && document.querySelector('.observation-row .status-badge') && document.querySelector('.observation-note').textContent.includes('不据此计算持续时间 SLA')"));
 check('evidence gaps stay unknown rather than fabricated normal',await evaluate("document.querySelector('.observation-gaps summary').textContent.includes('证据空缺（不等于故障）')"));
 await viewport(1440,1000);const rawImage=await command('Page.captureScreenshot',{format:'png',clip:{x:0,y:0,width:1440,height:1000,scale:1}});await writeFile(path.join(root,'artifacts/observations-desktop.png'),Buffer.from(rawImage.data,'base64'));
 check('no JS runtime errors',errors.length===0);check('no device/API/external requests in synthetic preview',[...urls].every(url=>url.startsWith(origin)||url.startsWith('data:')));
 console.log(`\n${passed} grouped browser checks passed; generated screenshots only.`);
}finally{
 if(ws?.readyState===WebSocket.OPEN){try{await command('Browser.close');}catch{}ws.close();}
 server.kill();chrome.kill();await wait(300);await rm(profile,{recursive:true,force:true,maxRetries:3}).catch(()=>{});
}
