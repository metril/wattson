import { expect, importRamp, setupAdmin, stubMapNetwork, test as base, useFakeMapillary } from './fixtures';

// Ride views in a real browser: 3D flyover, its fallback, reduced motion and street photos.
// Everything external is local: the map style and DEM are stubbed, Mapillary is the worker's fake.
const test = base.extend<{ routeId: number }>({
  routeId: async ({ context, baseURL, fakeMapillary }, use) => {
    fakeMapillary.fail = false;
    fakeMapillary.requests.length = 0;
    const cookies = await setupAdmin(baseURL);
    await useFakeMapillary(baseURL, cookies, fakeMapillary);
    await context.addCookies(cookies);
    await stubMapNetwork(context);
    await use(await importRamp(baseURL, cookies));
  },
});

test.beforeEach(({}, info) => {
  test.skip(info.project.name === 'phone', 'needs room for the map; tablet and tv cover the views');
});

/** Connects the simulator, starts the route and waits for the ride. */
async function ride(page: import('@playwright/test').Page, routeId: number) {
  await page.goto(`/?route=${routeId}`);
  await page.getByRole('button', { name: 'Connect' }).click();
  const state = page.getByTestId('ride-state');
  await expect(state).toHaveText('ready', { timeout: 5_000 });
  await page.getByRole('button', { name: 'Start' }).click();
  await expect(state).toHaveText('riding');
}

async function chooseView(page: import('@playwright/test').Page, name: '2D map' | '3D flyover' | 'Street photos') {
  await page.getByRole('button', { name: 'View' }).click();
  await page.getByRole('radio', { name }).click();
}

test('3D flyover pitches the camera, is remembered per device and 2D on another one', async ({ page, browser, routeId, baseURL }) => {
  test.setTimeout(90_000);
  await ride(page, routeId);

  await chooseView(page, '3D flyover');
  const view = page.getByTestId('route-3d');
  await expect(view).toBeVisible();
  await expect.poll(async () => Number((await view.getAttribute('data-pitch')) ?? 0), { timeout: 10_000 }).toBeGreaterThan(50);
  expect(await page.evaluate(() => localStorage.getItem('wattson.view'))).toBe('3d');

  // The same device after a reload.
  await page.reload();
  await expect(page.getByTestId('route-3d')).toBeVisible({ timeout: 10_000 });
  await expect(page.locator('.maplibregl-canvas')).toHaveCount(1);

  // Another device (its own storage) still gets the 2D map.
  const other = await browser.newContext({ baseURL, viewport: page.viewportSize() ?? undefined, serviceWorkers: 'block' });
  try {
    await other.addCookies(await page.context().cookies());
    await stubMapNetwork(other);
    const p2 = await other.newPage();
    await p2.goto('/');
    await expect(p2.getByTestId('route-map')).toBeVisible({ timeout: 10_000 });
    await expect(p2.locator('.maplibregl-canvas')).toBeVisible();
    await expect(p2.getByTestId('route-3d')).toHaveCount(0);
  } finally {
    await other.close();
  }
});

test('fallback simulation: a ~10 fps display drops 3D to 2D and says why', async ({ page, routeId }) => {
  test.setTimeout(120_000);
  await page.addInitScript(() => {
    // Every animation frame arrives 100 ms apart, like a very slow display.
    window.requestAnimationFrame = (cb) => window.setTimeout(() => cb(performance.now()), 100);
    window.cancelAnimationFrame = (id) => window.clearTimeout(id);
  });
  await ride(page, routeId);

  await chooseView(page, '3D flyover');
  await expect(page.getByTestId('route-3d')).toBeVisible();
  // Two warm-ups (full tier, then lite) and their windows.
  await expect(page.getByText(/3D was too slow on this display/)).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId('route-3d')).toHaveCount(0);
  await expect(page.locator('.maplibregl-canvas')).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem('wattson.view'))).toBe('2d');
});

test('reduced motion turns the 3D option off', async ({ page, routeId }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await ride(page, routeId);
  await page.getByRole('button', { name: 'View' }).click();
  const option = page.getByRole('radio', { name: /3D flyover/ });
  await expect(option).toBeDisabled();
  await expect(page.getByText('Off because reduced motion is on')).toBeVisible();
});

test('street photos follow the ride, credit Mapillary, never touch it from the browser and survive an outage', async ({
  page,
  routeId,
  fakeMapillary,
}) => {
  test.setTimeout(120_000);
  const direct: string[] = [];
  page.on('request', (r) => {
    if (r.url().startsWith(fakeMapillary.url)) direct.push(r.url());
  });
  await ride(page, routeId);

  // The server builds the photo list in the background; the option enables once it is ready.
  await page.getByRole('button', { name: 'View' }).click();
  const option = page.getByRole('radio', { name: /Street photos/ });
  await expect(option).toBeEnabled({ timeout: 20_000 });
  await option.click();

  const photo = page.getByTestId('photo-view');
  await expect(photo).toHaveAttribute('data-photo-id', /^\d+$/, { timeout: 10_000 });
  const first = await photo.getAttribute('data-photo-id');
  await expect(photo).toContainText('on Mapillary, CC BY-SA 4.0');
  await expect(photo).toContainText('e2e');

  await expect.poll(() => photo.getAttribute('data-photo-id'), { timeout: 30_000 }).not.toBe(first);

  // Mapillary goes away: the panel says so and keeps the last photo.
  fakeMapillary.fail = true;
  await expect(page.getByText('Street photos unavailable right now')).toBeVisible({ timeout: 45_000 });
  await expect(photo).toHaveAttribute('data-photo-id', /^\d+$/);

  expect(direct, 'the browser only talks to the Wattson server').toEqual([]);
});

test('street photos crossfade between two blob-loaded layers in a real browser', async ({ page, routeId }) => {
  test.setTimeout(90_000);
  await ride(page, routeId);
  await page.getByRole('button', { name: 'View' }).click();
  const option = page.getByRole('radio', { name: /Street photos/ });
  await expect(option).toBeEnabled({ timeout: 20_000 });
  await option.click();
  const photo = page.getByTestId('photo-view');
  await expect(photo).toHaveAttribute('data-photo-id', /^\d+$/, { timeout: 10_000 });

  // Sample both layers every frame for a while: a swap shows the new photo fading in over the old one.
  const seen = await photo.evaluate(
    (root) =>
      new Promise<{ mid: boolean; blobs: boolean; settled: boolean }>((done) => {
        const [a, b] = Array.from(root.querySelectorAll('img'));
        let mid = false;
        const end = performance.now() + 20_000;
        const tick = () => {
          const oa = Number(getComputedStyle(a).opacity);
          const ob = Number(getComputedStyle(b).opacity);
          if (oa > 0 && ob > 0 && (oa < 1 || ob < 1)) mid = true;
          if (mid && performance.now() > end - 1500) {
            const shown = [a, b].filter((i) => Number(getComputedStyle(i).opacity) === 1 && i.getAttribute('src'));
            done({ mid, blobs: shown.every((i) => i.currentSrc.startsWith('blob:') && i.naturalWidth === 64), settled: shown.length >= 1 });
            return;
          }
          if (performance.now() > end + 20_000) {
            done({ mid, blobs: false, settled: false });
            return;
          }
          requestAnimationFrame(tick);
        };
        requestAnimationFrame(tick);
      }),
  );
  expect(seen).toEqual({ mid: true, blobs: true, settled: true });
});

test('on a TV the view fills the left of a rail layout and View is in the top bar', async ({ page, routeId }, info) => {
  test.skip(info.project.name !== 'tv', 'the TV layout');
  await ride(page, routeId);
  const map = (await page.getByTestId('route-map').boundingBox())!;
  const view = (await page.getByRole('button', { name: 'View' }).boundingBox())!;
  const rail = (await page.getByRole('button', { name: 'Pause' }).boundingBox())!;
  expect(map.height, 'the view is tall, not a strip').toBeGreaterThan(650);
  expect(view.y + view.height, 'View sits above the view').toBeLessThanOrEqual(map.y);
  expect(rail.x, 'controls are in a right-hand rail').toBeGreaterThan(map.x + map.width);
  expect(rail.y + rail.height, 'inside the screen with its overscan margin').toBeLessThanOrEqual(1080 * 0.95 + 1);
});
