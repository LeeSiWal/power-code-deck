import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
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
