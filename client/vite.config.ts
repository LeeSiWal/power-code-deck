import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
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
