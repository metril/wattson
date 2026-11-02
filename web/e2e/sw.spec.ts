import { expect, importRamp, setupAdmin, stubMapNetwork, test as base, VECTOR_STYLE } from './fixtures';

// The service worker caches map tiles. Opt in here only (the config blocks it elsewhere);
// 127.0.0.1 is a secure context, so the production build registers it.
const test = base.extend<{ routeId: number }>({
  routeId: async ({ context, baseURL }, use) => {
    const cookies = await setupAdmin(baseURL);
    await context.addCookies(cookies);
    // On the context, so the service worker's own fetches are answered too. The style has a vector source:
    // MapLibre's worker fetches and decodes its tiles, which is the path that must work offline.
    await stubMapNetwork(context, VECTOR_STYLE);
    await use(await importRamp(baseURL, cookies));
  },
});
test.use({ serviceWorkers: 'allow' });

test('prefetch saves the route corridor, and MapLibre still loads vector tiles and terrain offline', async ({ page, context, routeId }, info) => {
  test.skip(info.project.name !== 'tablet', 'one project covers the service worker');
  test.setTimeout(90_000);

  await page.goto(`/?route=${routeId}`);
  await page.getByRole('button', { name: 'Connect' }).click();
  const state = page.getByTestId('ride-state');
  await expect(state).toHaveText('ready', { timeout: 5_000 });

  // Choosing the route in the ready state starts the save (once the worker controls the page).
  await expect(page.getByText('Map saved for this route')).toBeVisible({ timeout: 30_000 });
  const entries = await page.evaluate(async () => (await (await caches.open('wattson-tiles-v1')).keys()).length);
  expect(entries).toBeGreaterThan(0);

  // The internet drops. (context.setOffline would also cut the page's WebSocket to this local server,
  // so only the tile hosts go dark; the style and TileJSON stay up, as a cached style would.)
  let blocked = 0;
  await context.route(/^https:\/\/(tiles\.openfreemap\.org\/(?!styles\/|planet)|s3\.amazonaws\.com\/)/, (route) => {
    blocked++;
    return route.abort('internetdisconnected');
  });
  await page.getByRole('button', { name: 'Start' }).click();
  await expect(state).toHaveText('riding');
  const map = page.getByTestId('route-map');
  await expect(map).toHaveAttribute('data-pos', /^-?[\d.]+,-?[\d.]+$/, { timeout: 15_000 });
  const p1 = await map.getAttribute('data-pos');
  await expect.poll(() => map.getAttribute('data-pos'), { timeout: 6_000 }).not.toBe(p1);
  await expect(page.locator('.maplibregl-canvas')).toBeVisible();

  // A fresh map (the 3D view) has to load the saved vector and DEM tiles through its own worker, with the tile hosts dark.
  await page.getByRole('button', { name: 'View' }).click();
  await page.getByRole('radio', { name: '3D flyover' }).click();
  const view = page.getByTestId('route-3d');
  await expect(view).toBeVisible();
  await view.evaluate((el) => {
    const w = el as any;
    w.__errors = [];
    w.__map.on('error', (e: { error?: Error }) => w.__errors.push(String(e.error?.message ?? e.error)));
  });
  await expect
    .poll(() => view.evaluate((el) => { const m = (el as any).__map; return !!m.getSource('terrain') && m.isSourceLoaded('openmaptiles') && m.isSourceLoaded('terrain'); }), { timeout: 20_000 })
    .toBe(true);
  expect(await view.evaluate((el) => (el as any).__errors), 'no tile failed to load').toEqual([]);
  // MapLibre's worker is a same-origin script (not a blob:), so the service worker controls its fetches in every browser.
  const workers = page.workers().map((w) => w.url());
  expect(workers.length).toBeGreaterThan(0);
  for (const u of workers) expect(u).toMatch(/\/assets\/mlworker-[\w-]+\.js$/);

  // A saved tile still loads (from the worker's cache); one never saved does not.
  const probe = await page.evaluate(async () => {
    const cache = await caches.open('wattson-tiles-v1');
    const saved = (await cache.keys())[0].url;
    const ok = await fetch(saved).then((r) => r.ok, () => false);
    const unsaved = await fetch('https://s3.amazonaws.com/elevation-tiles-prod/terrarium/2/1/1.png').then((r) => r.ok, () => false);
    return { ok, unsaved };
  });
  expect(probe).toEqual({ ok: true, unsaved: false });
  expect(blocked, 'the outage was really in effect').toBeGreaterThan(0);
  await expect(page.locator('[role=alert]')).toHaveCount(0);
  await expect(state).toHaveText('riding');
});
