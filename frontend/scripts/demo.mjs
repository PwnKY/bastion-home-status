// Opt-in demo build only. Normal builds cannot activate demo with a query parameter.
import { spawn, execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const root = fileURLToPath(new URL('../', import.meta.url));
const vite = path.join(root, 'node_modules/vite/bin/vite.js');
const env = { ...process.env, VITE_ALLOW_DEMO: 'true' };
const mode = process.argv[2] ?? 'check';
if (mode === 'dev') {
  const child = spawn(process.execPath, [vite, '--host', '127.0.0.1'], { cwd: root, env, stdio: 'inherit' });
  child.on('exit', (code) => { process.exitCode = code ?? 1; });
} else if (['build', 'check'].includes(mode)) {
  execFileSync(process.execPath, [vite, 'build'], { cwd: root, env, stdio: 'inherit' });
  if (mode === 'check') {
    try { execFileSync(process.execPath, [path.join(root, 'scripts/browser-check.mjs')], { cwd: root, stdio: 'inherit' }); }
    finally { execFileSync(process.execPath, [vite, 'build'], { cwd: root, env: { ...process.env, VITE_ALLOW_DEMO: 'false' }, stdio: 'inherit' }); }
  }
} else { throw new Error('Expected dev, build or check'); }
