import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { apiAs, expect, setupAdmin, test as base } from './fixtures';
import { levelText } from './helpers';

const STYLE = readFileSync(resolve(import.meta.dirname, 'fixtures/blank-style.json'), 'utf8');

// Everything a route test needs, in place before the page loads: a signed-in
// admin, the worker's fake BRouter and map-regions dir in Settings, and a
// local stand-in for the hosted map style (the suite never touches the network).
const test = base.extend<{ env: void }>({
  env: [
    async ({ page, context, baseURL, fakeBRouter, segmentsDir }, use) => {
      const cookies = await setupAdmin(baseURL);
      await apiAs(baseURL, cookies, 'PATCH', '/api/settings', {
        brouter: { url: fakeBRouter.url, segments_dir: segmentsDir },
      });
      await context.addCookies(cookies);
      fakeBRouter.queries.length = 0;
      await page.route('https://tiles.openfreemap.org/**', (route) =>
        route.request().url().endsWith('/styles/liberty')
          ? route.fulfill({ status: 200, contentType: 'application/json', body: STYLE })
          : route.fulfill({ status: 404, body: '' }),
      );
      await use();
    },
    { auto: true },
  ],
});

test('draw a route, save it and ride it: the level rises before the climb', async ({ page, fakeBRouter }, info) => {
  test.skip(info.project.name !== 'tablet', 'the map needs room; one project covers the flow');
  test.setTimeout(90_000);

  // 1. Draw two waypoints.
  await page.goto('/routes/new/draw?c=7.5,45.5&z=13');
  const map = page.getByTestId('draw-map');
  await expect(map).toBeVisible();
  const box = (await map.boundingBox())!;
  const at = (fx: number, fy: number) => ({ position: { x: box.width * fx, y: box.height * fy } });
  await map.click(at(0.3, 0.4));
  await expect(page.getByText('1 / 30 waypoints')).toBeVisible();
  await map.click(at(0.6, 0.6));
  await expect(page.getByText('2 / 30 waypoints')).toBeVisible();
  await expect(page.getByText('Distance', { exact: true })).toBeVisible({ timeout: 5_000 });
  expect(fakeBRouter.queries).toHaveLength(1);
  const q = fakeBRouter.queries[0];
  expect(q.get('lonlats')!.split('|')).toHaveLength(2);
  expect(q.get('profile')).toBe('trekking');

  // 2. Save it.
  await page.getByLabel('Route name').fill('E2E climb');
  await page.getByRole('button', { name: 'Save route' }).click();
  await expect(page).toHaveURL(/\/routes\/\d+$/);
  await expect(page.getByRole('heading', { name: 'E2E climb' })).toBeVisible();
  await expect(page.getByRole('img', { name: /Elevation profile/ })).toBeVisible();

  // 3. Ride it on the simulator.
  await page.getByRole('link', { name: 'Ride this route' }).click();
  await expect(page).toHaveURL(/\/\?route=\d+$/);
  await expect(page.getByLabel('Rider')).toHaveValue(/\d+/);
  await expect(page.getByLabel('Rider').locator('option:checked')).toHaveText('admin');
  await page.getByRole('button', { name: 'Connect' }).click();
  const state = page.getByTestId('ride-state');
  await expect(state).toHaveText('ready', { timeout: 5_000 });
  await expect(page.getByLabel('Ride', { exact: true })).toHaveValue(/^route:\d+$/);
  await page.getByRole('button', { name: 'Start' }).click();
  await expect(state).toHaveText('riding');

  // 4. The level climbs before the ramp starts at 60 m.
  const pos = page.getByTestId('route-pos');
  const deadline = Date.now() + 20_000;
  let level = 0;
  let metres = Infinity;
  while (Date.now() < deadline) {
    level = Number(/^L(\d+)/.exec((await levelText(page).textContent()) ?? '')?.[1] ?? 0);
    metres = Number((await pos.getAttribute('data-distance-m')) ?? 'NaN');
    if (level >= 6) break;
    await page.waitForTimeout(250);
  }
  expect(level, 'level reached 6 within 20 s').toBeGreaterThanOrEqual(6);
  expect(metres, 'metres ridden when the level rose').toBeLessThan(60);

  // 5. The marker moves.
  const route = page.getByTestId('route-map');
  const p1 = await route.getAttribute('data-pos');
  expect(p1).toMatch(/^-?[\d.]+,-?[\d.]+$/);
  await page.waitForTimeout(3_000);
  expect(await route.getAttribute('data-pos')).not.toBe(p1);

  // 6. Stop takes two taps.
  await page.getByRole('button', { name: 'Stop', exact: true }).click();
  await page.getByRole('button', { name: 'Tap again to stop' }).click();
  await expect(state).toHaveText('ready', { timeout: 6_000 });
});
