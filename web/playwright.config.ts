import { defineConfig, devices } from '@playwright/test';

// Every worker runs its own `wattson serve` (see e2e/fixtures.ts), so tests
// are fully parallel. Build the binary first: `make build`.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  timeout: 60_000,
  expect: { timeout: 5_000 },
  use: {
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    // The service worker caches map tiles; only sw.spec.ts opts in, so no other test sees a cached tile.
    serviceWorkers: 'block',
    // MapLibre needs WebGL2; headless CI has no GPU, so use the software rasteriser (and ignore the blocklist that hides it).
    launchOptions: { args: ['--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist'] },
  },
  projects: [
    // workouts.spec.ts is one flow on the tablet project only; the 3D, photo and service worker specs need room.
    {
      name: 'phone',
      testIgnore: /workouts\.spec|visuals\.spec|sw\.spec|screenshots\.spec/,
      use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 }, hasTouch: true },
    },
    // layout.spec.ts sets its own viewports, so only the phone project runs it.
    {
      name: 'tablet',
      testIgnore: /layout\.spec|screenshots\.spec/,
      use: { ...devices['Desktop Chrome'], viewport: { width: 1024, height: 768 }, hasTouch: true },
    },
    { name: 'tv', testIgnore: /layout\.spec|workouts\.spec|sw\.spec|screenshots\.spec/, use: { ...devices['Desktop Chrome'], viewport: { width: 1920, height: 1080 } } },
    // The user guide's pictures, with real map tiles (needs internet). Only `npm run screenshots` (SCREENSHOTS=1) adds it,
    // so a plain `playwright test` (and CI) never runs it.
    ...(process.env.SCREENSHOTS ? [{ name: 'docs', testMatch: /screenshots\.spec/, use: { ...devices['Desktop Chrome'] } }] : []),
  ],
});
