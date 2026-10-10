import { defineConfig } from 'vite';

// Dev/preview only; this address is never embedded in the browser bundle.
const target = process.env.BASTION_DEV_API;
const proxy = target ? { '/api': { target, changeOrigin: false, timeout: 8000 } } : {};
export default defineConfig({ server: { proxy }, preview: { proxy } });
