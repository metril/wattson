/// <reference types="vitest/config" />
import { writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build, defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// go:embed requires internal/web/dist to exist even when the Go binary is
// built before `npm run build` has produced any assets. Vite's
// `emptyOutDir: true` wipes that directory (and the tracked `.gitkeep`
// placeholder in it) on every build, so restore the placeholder once the
// bundle is written.
function restoreGitkeep(): Plugin {
  return {
    name: 'restore-dist-gitkeep',
    closeBundle() {
      writeFileSync(resolve(import.meta.dirname, '../internal/web/dist/.gitkeep'), '');
    },
  };
}

// The service worker is a classic script at a fixed URL (/sw.js, never hashed)
// that cannot import chunks, so it is bundled on its own as a single IIFE
// after the app build.
function serviceWorker(): Plugin {
  const root = import.meta.dirname;
  return {
    name: 'wattson-service-worker',
    apply: 'build',
    async closeBundle() {
      await build({
        configFile: false,
        root,
        publicDir: false,
        logLevel: 'warn',
        build: {
          outDir: resolve(root, '../internal/web/dist'),
          emptyOutDir: false,
          copyPublicDir: false,
          target: 'es2020',
          lib: { entry: resolve(root, 'src/sw/sw.ts'), formats: ['iife'], name: 'wattsonSW', fileName: () => 'sw.js' },
        },
      });
    },
  };
}

const backend = 'http://localhost:8080';

const LAZY_NAMES: Record<string, string> = { Ride3D: 'ride3d', PhotoView: 'photo', EChart: 'echarts' };

export default defineConfig({
  plugins: [react(), tailwindcss(), serviceWorker(), restoreGitkeep()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    // The Go binary embeds internal/web/dist via go:embed.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    // MapLibre is its own lazy chunk (~1 MB); the entry bundle is gated by npm run size.
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('/node_modules/maplibre-gl/')) return 'maplibre';
          // Rollup's CommonJS interop helper is shared by MapLibre and CJS deps in the entry; left alone it lands
          // in the maplibre chunk and index.html then preloads all of MapLibre on every page.
          if (id.includes('commonjsHelpers')) return 'cjs-helpers';
        },
        // Ride3D and PhotoView are dynamic imports; naming them here (not in manualChunks, which
        // would pull shared code such as React into them and make index.html preload them)
        // keeps them lazy. scripts/check-size.mjs budgets these names.
        chunkFileNames: (chunk) => `assets/${LAZY_NAMES[chunk.name] ?? chunk.name}-[hash].js`,
        // MapLibre's worker is loaded by URL (lib/maplibre.ts): a same-origin file, not a blob:. It gets its own
        // budgeted name so the maplibre chunk's size check does not count it.
        assetFileNames: (asset) =>
          (asset.names ?? []).some((n) => n.includes('maplibre-gl-csp-worker')) ? 'assets/mlworker-[hash].js' : 'assets/[name]-[hash][extname]',
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': backend,
      '/healthz': backend,
      '/docs': backend,
      '/openapi.json': backend,
      '/ws': { target: backend, ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['./src/test-setup.ts'],
  },
});
