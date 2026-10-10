// 公开发布内容守卫：扫描明确选定的公开源文件与文档，拦截不应随公开发布的内容。
//
// 规则只描述“敏感数据的类别”，不内嵌本网络的任何真实取值（不含真实地址、域名、
// 主机名或凭据），因此本文件本身也可以公开。正则覆盖：非回环 IP、私钥块、SSH 公钥
// 材料、常见 API 令牌、Tailscale 尾网主机名与私钥文件名。
//
// 版本号（如 6.4.3、0.25.12）、package-lock 的 registry URL 与 integrity 哈希均不会
// 命中这些规则，避免误报。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const frontendRoot = fileURLToPath(new URL('..', import.meta.url));
const repoRoot = path.resolve(frontendRoot, '..');
const SELF = fileURLToPath(import.meta.url);

// 仅扫描文本类公开源与文档；磁盘镜像、依赖、构建产物与运行态文件不在范围内。
const TEXT_EXTENSIONS = new Set([
  '.md', '.js', '.mjs', '.cjs', '.json', '.html', '.css', '.svg', '.txt', '.yml', '.yaml', '.toml', '.go', '.service',
]);
const SKIP_DIRS = new Set(['.git', 'node_modules', 'dist', '.vite', 'artifacts', 'coverage']);
const ROOT_FILES = [
  'README.md',
  'home-network-status-project.md',
  'home-network-status-implementation-plan.md',
  'go.mod', 'go.sum', 'deploy/Caddyfile.example',
];
const SCAN_ROOTS = ['frontend', 'cmd', 'internal', 'deploy', 'docs', 'scripts', '.github'];

// IP 字面量（IPv4 / IPv6）。回环与未指定地址由 allow 回调放行。
const IPV4 = /\b\d{1,3}(?:\.\d{1,3}){3}\b/g;
const IPV6 = /\b(?:[0-9a-fA-F]{1,4}:){3,}[0-9a-fA-F]{1,4}\b/g;

function isAllowedIpv4(value) {
  const octets = value.split('.').map(Number);
  if (octets.length !== 4 || octets.some((n) => !Number.isInteger(n) || n < 0 || n > 255)) {
    return true; // 不是合法 IPv4（如版本号），忽略
  }
  return octets[0] === 127 || value === '0.0.0.0';
}

const RULES = [
  { id: 'ipv4', label: '非回环 IPv4 地址', pattern: IPV4, allow: isAllowedIpv4 },
  { id: 'ipv6', label: '非回环 IPv6 地址', pattern: IPV6, allow: (value) => value === '::1' },
  {
    id: 'private-key-block',
    label: '私钥块',
    pattern: /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/g,
  },
  {
    id: 'ssh-key-material',
    label: 'SSH 公钥材料',
    pattern: /\bssh-(?:rsa|ed25519|dss)\s+[A-Za-z0-9+/]{20,}={0,3}/g,
  },
  {
    id: 'token',
    label: 'API 令牌',
    pattern:
      /\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b|\bAKIA[0-9A-Z]{16}\b|\bxox[baprs]-[A-Za-z0-9-]{10,}\b/g,
  },
  {
    id: 'tailnet-hostname',
    label: '尾网主机名',
    pattern: /[A-Za-z0-9-]+\.ts\.net\b/g,
  },
  {
    id: 'private-key-filename',
    label: '私钥文件名',
    pattern: /\bid_(?:rsa|ed25519|ecdsa)\b/g,
  },
];

/** 返回文本中命中规则的片段；空数组表示通过。 */
function findViolations(text) {
  const found = [];
  for (const rule of RULES) {
    for (const match of text.matchAll(rule.pattern)) {
      if (rule.allow && rule.allow(match[0])) continue;
      found.push({ rule: rule.id, label: rule.label, value: match[0] });
    }
  }
  return found;
}

function collectFiles() {
  const files = [];
  for (const rel of ROOT_FILES) files.push(path.join(repoRoot, rel));

  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const abs = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (SKIP_DIRS.has(entry.name)) continue;
        walk(abs);
      } else if (entry.isFile()) {
        if (abs === SELF) continue; // 本守卫文件只含通用正则，自扫描会误报
        if (!TEXT_EXTENSIONS.has(path.extname(entry.name).toLowerCase())) continue;
        files.push(abs);
      }
    }
  };
  for (const root of SCAN_ROOTS) walk(path.join(repoRoot, root));
  return files;
}

test('公开源文件与文档不含地址、私钥或令牌', () => {
  const offenders = [];
  for (const file of collectFiles()) {
    const text = readFileSync(file, 'utf8');
    for (const violation of findViolations(text)) {
      offenders.push(`${path.relative(repoRoot, file)}: ${violation.label} (${violation.rule})`);
    }
  }
  assert.deepEqual(
    offenders,
    [],
    `公开文件出现疑似敏感内容，请脱敏后再发布：\n${offenders.join('\n')}`,
  );
});

test('根规划文档带有公开脱敏声明', () => {
  const marker = '公开版，经过脱敏，不含实际部署清单';
  for (const rel of ['home-network-status-project.md', 'home-network-status-implementation-plan.md']) {
    const text = readFileSync(path.join(repoRoot, rel), 'utf8');
    assert.ok(text.includes(marker), `${rel} 缺少脱敏声明`);
  }
});

test('守卫能识别伪造的敏感样本，且放行回环与版本号', () => {
  // 样本在运行时拼装，源码中不出现完整字面量。
  const sample = [
    [203, 0, 113, 9].join('.'), // 文档保留地址，仅用于自检
    ['device', 'tailnet', 'ts', 'net'].join('.'),
    ['-----BEGIN', 'PRIVATE', 'KEY-----'].join(' '),
    ['ssh-rsa', 'AAAAB3NzaC1yc2EAAAADAQABAAABgQDexamplefakefakefake'].join(' '),
    ['ghp', 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'].join('_'),
    'id_ed25519',
  ].join('\n');
  const detected = new Set(findViolations(sample).map((violation) => violation.rule));
  for (const rule of [
    'ipv4',
    'tailnet-hostname',
    'private-key-block',
    'ssh-key-material',
    'token',
    'private-key-filename',
  ]) {
    assert.ok(detected.has(rule), `未识别 ${rule}`);
  }

  const benign = [
    'http://127.0.0.1:5173/',
    'https://registry.npmjs.org/vite/-/vite-6.4.3.tgz',
    'https://registry.npmmirror.com/esbuild/-/esbuild-0.25.12.tgz',
    'vite 6.4.3 / rollup 4.62.3 / postcss 8.5.24',
  ].join('\n');
  assert.deepEqual(findViolations(benign), [], '回环地址与版本号不应被拦截');
});
