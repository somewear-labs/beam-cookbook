import { resolve } from 'path';
import { defineConfig, externalizeDepsPlugin } from 'electron-vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  main: {
    plugins: [externalizeDepsPlugin()],
    define: {
      __MAPBOX_TOKEN__: JSON.stringify(process.env.MAPBOX_ACCESS_TOKEN ?? '')
    }
  },
  preload: {
    plugins: [externalizeDepsPlugin()]
  },
  renderer: {
    plugins: [react()],
    resolve: {
      alias: { '@renderer': resolve('src/renderer/src') }
    }
  }
});
