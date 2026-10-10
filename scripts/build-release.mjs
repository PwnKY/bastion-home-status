// Build generic artifacts only. Never reads private deployment configuration.
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync, readdirSync, copyFileSync, mkdtempSync, rmSync } from 'node:fs';
import path from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const root = fileURLToPath(new URL('../', import.meta.url));
const output = path.join(root, 'release');
const env = { ...process.env, CGO_ENABLED: '0', GOOS: 'linux', GOARCH: 'amd64', GOMAXPROCS: '2' };
const run = (cmd, args, cwd = root, extra = {}) => execFileSync(cmd, args, { cwd, env, stdio: 'inherit', ...extra });
const capture = (cmd, args, cwd = root) => execFileSync(cmd, args, { cwd, env, encoding: 'utf8' }).trim();
function licenses(cwd, destination) {
  mkdirSync(destination, { recursive: true });
  const modules = capture('go', ['list', '-m', '-f', '{{.Path}}|{{.Version}}|{{.Dir}}', 'all'], cwd).split('\n');
  for (const line of modules) {
    const [name, version, directory] = line.split('|');
    if (!directory || path.resolve(directory) === path.resolve(root)) continue;
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (!entry.isFile() || !/^(LICENSE|COPYING|NOTICE)([._-]|$)/i.test(entry.name)) continue;
      copyFileSync(path.join(directory, entry.name), path.join(destination, `${name.replaceAll('/', '_')}@${version}_${entry.name}`));
    }
  }
  const goroot = capture('go', ['env', 'GOROOT'], cwd);
  copyFileSync(path.join(goroot, 'LICENSE'), path.join(destination, 'Go-LICENSE'));
}
mkdirSync(path.join(output, 'app'), { recursive: true });
for (const name of ['bastion-server', 'bastion-agent']) run('go', ['build', '-p', '2', '-trimpath', '-ldflags=-s -w', '-o', path.join(output, 'app', name), `./cmd/${name}`]);
licenses(root, path.join(output, 'app/licenses'));
run('tar', ['-czf', 'release/bastion-app-linux-amd64.tar.gz', '-C', 'release/app', 'bastion-server', 'bastion-agent', 'licenses']);
run(process.execPath, [path.join(root, 'frontend/node_modules/vite/bin/vite.js'), 'build'], path.join(root, 'frontend'), { env: { ...process.env, VITE_ALLOW_DEMO: 'false' } });
run('tar', ['-czf', 'release/bastion-frontend.tar.gz', '-C', 'frontend/dist', '.']);

// Vendor's current binary uses an older Go SDK. Rebuild the same Caddy release with
// the patched SDK and patched x/net in an isolated temporary module, not on servers.
const temporary = mkdtempSync(path.join(tmpdir(), 'bastion-caddy-build-'));
try {
  writeFileSync(path.join(temporary, 'go.mod'), 'module bastion-caddy-build\n\ngo 1.26.9\n');
  run('go', ['get', 'github.com/caddyserver/caddy/v2/cmd/caddy@v2.11.7'], temporary);
  run('go', ['get', 'golang.org/x/net@v0.61.0'], temporary);
  mkdirSync(path.join(output, 'caddy-package'), { recursive: true });
  run('go', ['build', '-p', '2', '-trimpath', '-tags=nobadger,nomysql,nopgx', '-ldflags=-s -w', '-o', path.join(output, 'caddy-package/caddy'), 'github.com/caddyserver/caddy/v2/cmd/caddy'], temporary);
  licenses(temporary, path.join(output, 'caddy-package/licenses'));
  run('tar', ['-czf', 'release/caddy-bastion-linux-amd64.tar.gz', '-C', 'release/caddy-package', 'caddy', 'licenses']);
} finally { rmSync(temporary, { recursive: true, force: true }); }
const assets = ['bastion-app-linux-amd64.tar.gz', 'bastion-frontend.tar.gz', 'caddy-bastion-linux-amd64.tar.gz'];
writeFileSync(path.join(output, 'SHA256SUMS'), assets.map((name) => `${createHash('sha256').update(readFileSync(path.join(output, name))).digest('hex')}  ${name}`).join('\n') + '\n');
console.log('Generic Linux artifacts built; private config/state were not packaged.');
