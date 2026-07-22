import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// Single source of truth for the release number: the SAME file the Go server embeds
// (server/version/VERSION). Read it at build time and expose it as __APP_VERSION__ so
// the web UI never hardcodes a version that drifts from the server.
const APP_VERSION = readFileSync(
  fileURLToPath(new URL('../server/version/VERSION', import.meta.url)),
  'utf-8',
).trim();

export default defineConfig({
  define: {
    __APP_VERSION__: JSON.stringify(APP_VERSION),
  },
  plugins: [react()],
  resolve: {
    alias: {
      // @xterm/headless ships a broken `module` field (points at a nonexistent
      // lib/xterm.mjs); point the bundler at the real ESM build.
      '@xterm/headless': '@xterm/headless/lib-headless/xterm-headless.mjs',
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:33033',
      '/ws': {
        target: 'ws://localhost:33033',
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    rollupOptions: {
      output: {
        manualChunks: {
          wterm: ['@wterm/react', '@wterm/dom', '@wterm/core'],
          react: ['react', 'react-dom', 'react-router-dom'],
        },
      },
    },
  },
});
