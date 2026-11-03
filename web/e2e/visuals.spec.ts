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
  test.skip(info.project.name === 'phone' && !info.title.includes('combined'), 'needs room for the map; tablet and tv cover the views');
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

/** Presses Harder to the bias limit: the simulated bike then covers the route several times faster. */
async function pedalHarder(page: import('@playwright/test').Page) {
  const harder = page.getByRole('button', { name: 'Harder' });
  for (let i = 0; i < 12; i++) await harder.click();
}

async function chooseView(page: import('@playwright/test').Page, name: '2D map' | '3D flyover' | 'Street photos' | 'Photos + map' | 'Photos | map') {
  await page.getByRole('button', { name: 'View' }).click();
  await page.getByRole('radio', { name }).click();
}

test('3D flyover pitches the camera, is remembered per device and 2D on another one', async ({ page, browser, routeId, baseURL }) => {
  test.setTimeout(90_000);
  await ride(page, routeId);

  await chooseView(page, '3D flyover');
  const view = page.getByTestId('route-3d');
  await expect(view).toBeVisible();
  // The live MapLibre camera, not just the attribute that mirrors it.
  const livePitch = () =>
    view.evaluate((el) => (el as HTMLElement & { __map?: { getPitch(): number } }).__map?.getPitch() ?? 0);
  await expect.poll(livePitch, { timeout: 10_000 }).toBeGreaterThan(50);
  await expect.poll(async () => Number((await view.getAttribute('data-pitch')) ?? 0)).toBeGreaterThan(50);
  // ... and it renders in perspective: the ground in the top half of the view spans far more than the bottom half.
  const span = await view.evaluate((el) => {
    const m = (el as HTMLElement & { __map?: any }).__map;
    const c = m.getCanvas();
    const w = c.clientWidth;
    const h = c.clientHeight;
    const d = (p: any, q: any) => Math.hypot((p.lng - q.lng) * 78000, (p.lat - q.lat) * 111000);
    const mid = m.unproject([w / 2, h / 2]);
    return d(m.unproject([w / 2, 0]), mid) / d(mid, m.unproject([w / 2, h]));
  });
  expect(span, 'perspective: the top half shows far more ground').toBeGreaterThan(1.5);
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

test('fallback simulation: a slow display drops 3D to 2D and says why', async ({ page, routeId }, info) => {
  test.skip(info.project.name !== 'tablet', 'one project covers the fallback');
  test.setTimeout(30_000);
  // The test hook shortens the warm-up and windows, and a threshold no display reaches makes every window slow.
  await page.addInitScript(() => {
    (window as unknown as { __wattsonFps: object }).__wattsonFps = { warmupMs: 200, windowMs: 300, threshold: 1e6 };
  });
  await ride(page, routeId);

  await chooseView(page, '3D flyover');
  await expect(page.getByTestId('route-3d')).toBeVisible();
  // Full tier steps down to lite, then lite falls back to 2D.
  await expect(page.getByText(/3D was too slow on this display/)).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('route-3d')).toHaveCount(0);
  await expect(page.locator('.maplibregl-canvas')).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem('wattson.view'))).toBe('2d');
  expect(await page.evaluate(() => localStorage.getItem('wattson.view.tier'))).toBe('lite');
});

test('reduced motion turns the 3D option off', async ({ page, routeId }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await ride(page, routeId);
  await page.getByRole('button', { name: 'View' }).click();
  const option = page.getByRole('radio', { name: /3D flyover/ });
  await expect(option).toBeDisabled();
  await expect(page.getByText('Off because reduced motion is on')).toBeVisible();
});

test('street photos follow the ride, never touch it from the browser and survive an outage', async ({
  page,
  routeId,
  fakeMapillary,
}) => {
  test.setTimeout(60_000);
  const direct: string[] = [];
  page.on('request', (r) => {
    if (r.url().startsWith(fakeMapillary.url)) direct.push(r.url());
  });
  await ride(page, routeId);
  await pedalHarder(page);

  // The server builds the photo list in the background; the option enables once it is ready.
  await page.getByRole('button', { name: 'View' }).click();
  const option = page.getByRole('radio', { name: /Street photos/ });
  await expect(option).toBeEnabled({ timeout: 20_000 });
  await option.click();

  const photo = page.getByTestId('photo-view');
  await expect(photo).toHaveAttribute('data-photo-id', /^\d+$/, { timeout: 10_000 });
  const first = await photo.getAttribute('data-photo-id');
  await expect(page.getByTestId('photo-credit')).toHaveCount(0);

  await expect.poll(() => photo.getAttribute('data-photo-id'), { timeout: 15_000 }).not.toBe(first);

  // Mapillary goes away: the panel says so and keeps the last photo.
  fakeMapillary.fail = true;
  await expect(page.getByText('Street photos unavailable right now')).toBeVisible({ timeout: 30_000 });
  await expect(photo).toHaveAttribute('data-photo-id', /^\d+$/);

  expect(direct, 'the browser only talks to the Wattson server').toEqual([]);
});

test('street photos crossfade between two blob-loaded layers in a real browser', async ({ page, routeId }) => {
  test.setTimeout(60_000);
  await ride(page, routeId);
  await pedalHarder(page);
  await page.getByRole('button', { name: 'View' }).click();
  const option = page.getByRole('radio', { name: /Street photos/ });
  await expect(option).toBeEnabled({ timeout: 20_000 });
  await option.click();
  const photo = page.getByTestId('photo-view');
  await expect(photo).toHaveAttribute('data-photo-id', /^\d+$/, { timeout: 10_000 });

  // Sample both layers every frame until a swap has shown the new photo fading in over the old one and finished.
  const seen = await photo.evaluate(
    (root) =>
      new Promise<{ mid: boolean; blobs: boolean; settled: boolean }>((done) => {
        const [a, b] = Array.from(root.querySelectorAll('img'));
        let mid = false;
        const give_up = performance.now() + 25_000;
        const tick = () => {
          const oa = Number(getComputedStyle(a).opacity);
          const ob = Number(getComputedStyle(b).opacity);
          if (oa > 0 && ob > 0 && (oa < 1 || ob < 1)) mid = true;
          const shown = [a, b].filter((i) => Number(getComputedStyle(i).opacity) === 1 && i.getAttribute('src'));
          if (mid && shown.length === 1 && [oa, ob].includes(0)) {
            done({ mid, blobs: shown.every((i) => i.currentSrc.startsWith('blob:') && i.naturalWidth === 64), settled: true });
            return;
          }
          if (performance.now() > give_up) {
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
  // The rail applies once the route has loaded; measure after it settles.
  await expect
    .poll(async () => (await page.getByTestId('route-map').boundingBox())?.height ?? 0, { message: 'the view is tall, not a strip' })
    .toBeGreaterThan(650);
  const map = (await page.getByTestId('route-map').boundingBox())!;
  const view = (await page.getByRole('button', { name: 'View' }).boundingBox())!;
  const rail = (await page.getByRole('button', { name: 'Pause' }).boundingBox())!;
  expect(view.y + view.height, 'View sits above the view').toBeLessThanOrEqual(map.y);
  expect(rail.x, 'controls are in a right-hand rail').toBeGreaterThan(map.x + map.width);
  expect(rail.y + rail.height, 'inside the screen with its overscan margin').toBeLessThanOrEqual(1080 * 0.95 + 1);
});

test('combined photo and map views show both, swap, and stack on a phone', async ({ page, routeId }, info) => {
  test.setTimeout(60_000);
  await ride(page, routeId);
  await pedalHarder(page);
  if (info.project.name === 'phone') await page.getByRole('button', { name: 'Map', exact: true }).click();

  await page.getByRole('button', { name: 'View' }).click();
  await expect(page.getByRole('radio', { name: /Photos \+ map/ })).toBeEnabled({ timeout: 20_000 });
  await page.getByRole('radio', { name: 'Photos + map' }).click();
  const photo = page.getByTestId('photo-view');
  const map = page.locator('.maplibregl-map');
  await expect(photo).toHaveAttribute('data-photo-id', /^\d+$/, { timeout: 10_000 });
  await expect(map).toBeVisible();
  const area = async (l: import('@playwright/test').Locator) => {
    const b = (await l.boundingBox())!;
    return b.width * b.height;
  };
  expect(await area(photo)).toBeGreaterThan(await area(map));
  // No on-screen attribution anywhere.
  await expect(page.locator('.maplibregl-ctrl-attrib')).toHaveCount(0);
  await expect(page.getByTestId('photo-credit')).toHaveCount(0);
  // The zoom buttons exist but are hidden in the inset.
  await expect(map.locator('.maplibregl-ctrl-zoom-in')).toBeAttached();
  await expect(map.locator('.maplibregl-ctrl-zoom-in')).toBeHidden();
  const swap = page.getByTestId('inset-swap');
  await swap.focus();
  await page.keyboard.press('Enter');
  await expect.poll(async () => (await area(map)) > (await area(photo))).toBe(true);
  // MapLibre's canvas follows its container after the swap.
  await expect
    .poll(async () => {
      const [c, m] = [await map.locator('canvas').first().boundingBox(), await map.boundingBox()];
      return Math.abs(c!.width - m!.width) < 6;
    })
    .toBe(true);

  // the View menu is still open from choosing Photos + map
  await page.getByRole('radio', { name: 'Photos | map' }).click();
  await expect(swap).toHaveCount(0);
  const [pb, mb] = [(await photo.boundingBox())!, (await map.boundingBox())!];
  if (info.project.name === 'phone') expect(mb.y).toBeGreaterThanOrEqual(pb.y + pb.height - 1);
  else expect(mb.x).toBeGreaterThanOrEqual(pb.x + pb.width - 1);
  expect(await page.locator('.maplibregl-canvas').count()).toBe(1);
});


// ---- 3D detail, orbit, heading-up and 3D in the combined views ----

const BUILDING_STYLE = JSON.stringify({
  version: 8,
  sources: { openmaptiles: { type: 'vector', url: 'https://tiles.openfreemap.org/planet' } },
  layers: [
    { id: 'bg', type: 'background', paint: { 'background-color': '#16242a' } },
    { id: 'building', type: 'fill-extrusion', source: 'openmaptiles', 'source-layer': 'building', paint: { 'fill-extrusion-height': 10 } },
  ],
});

test('3D detail Full / Lite toggles the building layer, and is remembered', async ({ page, routeId }, info) => {
  test.skip(info.project.name !== 'tv', 'one project covers it');
  test.setTimeout(120_000);
  await stubMapNetwork(page, BUILDING_STYLE);
  await ride(page, routeId);
  await chooseView(page, '3D flyover');
  const vis = () =>
    page.getByTestId('route-3d').evaluate((el) => {
      const m = (el as any).__map;
      return m?.getLayer('building') ? (m.getLayoutProperty('building', 'visibility') ?? 'visible') : 'pending';
    });
  await page.getByRole('radio', { name: 'Full (buildings)' }).click();
  await expect.poll(vis, { timeout: 30_000 }).toBe('visible');
  await page.getByRole('radio', { name: 'Lite' }).click();
  await expect.poll(vis, { timeout: 30_000 }).toBe('none');
  expect(await page.evaluate(() => localStorage.getItem('wattson.view.tier'))).toBe('lite');
  await page.getByRole('radio', { name: 'Full (buildings)' }).click();
  await expect.poll(vis, { timeout: 30_000 }).toBe('visible');
  // Remembered across a reload.
  await page.getByRole('radio', { name: 'Lite' }).click();
  await page.reload();
  await expect(page.getByTestId('route-3d')).toBeVisible({ timeout: 10_000 });
  await expect.poll(vis, { timeout: 30_000 }).toBe('none');
});

test('3D flyover orbits with Left/Right on focus and eases back; Up/Down still leave', async ({ page, routeId }, info) => {
  test.skip(info.project.name !== 'tv', 'a remote-control flow; one project covers it');
  test.setTimeout(60_000);
  // Software WebGL in CI can look slow; these tests are about input and layout, not the slow-display fallback.
  await page.addInitScript(() => {
    (window as unknown as { __wattsonFps: object }).__wattsonFps = { threshold: 0 };
  });
  await ride(page, routeId);
  await chooseView(page, '3D flyover');
  const view = page.getByTestId('route-3d');
  await expect(view).toBeVisible();
  await expect.poll(() => view.getAttribute('data-pitch')).not.toBeNull();
  const orbit = async () => Number((await view.getAttribute('data-orbit')) ?? 0);
  await view.focus();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await expect.poll(orbit).toBe(30);
  await expect(view).toBeFocused();
  // After about five seconds the camera is back behind the rider.
  await expect.poll(orbit, { timeout: 30_000 }).toBe(0);
  // Drag turns it too.
  const b = (await view.boundingBox())!;
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
  await page.mouse.down();
  await page.mouse.move(b.x + b.width / 2 + 120, b.y + b.height / 2, { steps: 4 });
  await expect.poll(async () => Math.abs(await orbit())).toBeGreaterThan(5);
  await page.mouse.up();
  // The D-pad can leave the view upwards (focus moves on; the level is not changed by it).
  await view.focus();
  await page.keyboard.press('ArrowUp');
  await expect(view).not.toBeFocused();
});

test('combined views can use the 3D flyover as the map, swap it and fall back', async ({ page, routeId }, info) => {
  test.setTimeout(90_000);
  // Software WebGL in CI can look slow; these tests are about input and layout, not the slow-display fallback.
  await page.addInitScript(() => {
    (window as unknown as { __wattsonFps: object }).__wattsonFps = { threshold: 0 };
  });
  await ride(page, routeId);
  await pedalHarder(page);
  if (info.project.name === 'phone') await page.getByRole('button', { name: 'Map', exact: true }).click();
  await page.getByRole('button', { name: 'View' }).click();
  await expect(page.getByRole('radio', { name: /Photos \+ map/ })).toBeEnabled({ timeout: 20_000 });
  await page.getByRole('radio', { name: 'Photos + map' }).click();
  await page.getByRole('radio', { name: 'Terrain (3D)' }).click();
  expect(await page.evaluate(() => localStorage.getItem('wattson.view.comboMap'))).toBe('3d');

  const photo = page.getByTestId('photo-view');
  const flyover = page.getByTestId('route-3d');
  await expect(photo).toHaveAttribute('data-photo-id', /^\d+$/, { timeout: 10_000 });
  await expect(flyover).toBeVisible();
  await expect(page.locator('.maplibregl-canvas')).toHaveCount(1); // the photo is not a map
  const area = async (l: import('@playwright/test').Locator) => {
    const b = (await l.boundingBox())!;
    return b.width * b.height;
  };
  expect(await area(photo)).toBeGreaterThan(await area(flyover));
  const livePitch = () => flyover.evaluate((el) => (el as any).__map?.getPitch() ?? 0);
  await expect.poll(livePitch, { timeout: 10_000 }).toBeGreaterThan(50);

  // Swap: the flyover becomes large, the same map (no remount), and its canvas follows its panel.
  const swap = page.getByTestId('inset-swap');
  await swap.focus();
  await page.keyboard.press('Enter');
  await expect.poll(async () => (await area(flyover)) > (await area(photo)), { timeout: 15_000 }).toBe(true);
  await expect(page.locator('.maplibregl-canvas')).toHaveCount(1);
  await expect
    .poll(async () => {
      const [c, m] = [await flyover.locator('canvas').first().boundingBox(), await flyover.boundingBox()];
      return Math.abs(c!.width - m!.width) < 6 && Math.abs(c!.height - m!.height) < 6;
    }, { timeout: 15_000 })
    .toBe(true);

  // Split: side by side (stacked on a phone), still one canvas.
  await page.getByRole('radio', { name: 'Photos | map' }).click();
  await expect(flyover).toBeVisible();
  await expect(page.locator('.maplibregl-canvas')).toHaveCount(1);
  const [pb, fb] = [(await photo.boundingBox())!, (await flyover.boundingBox())!];
  if (info.project.name === 'phone') expect(fb.y).toBeGreaterThanOrEqual(pb.y + pb.height - 1);
  else expect(fb.x).toBeGreaterThanOrEqual(pb.x + pb.width - 1);
  await expect
    .poll(async () => {
      const [c, m] = [await flyover.locator('canvas').first().boundingBox(), await flyover.boundingBox()];
      return Math.abs(c!.width - m!.width) < 6;
    })
    .toBe(true);

  // Flat (2D) puts the 2D map back beside the photo.
  await page.getByRole('radio', { name: 'Flat (2D)' }).click();
  await expect(flyover).toHaveCount(0);
  await expect(page.locator('.maplibregl-map')).toBeVisible();
  await expect(page.locator('.maplibregl-canvas')).toHaveCount(1);
});

test('combined view: a slow 3D map falls back to the 2D map and keeps the photo', async ({ page, routeId }, info) => {
  test.skip(info.project.name !== 'tablet', 'one project covers the fallback');
  test.setTimeout(60_000);
  await page.addInitScript(() => {
    (window as unknown as { __wattsonFps: object }).__wattsonFps = { warmupMs: 200, windowMs: 300, threshold: 1e6 };
  });
  await ride(page, routeId);
  await pedalHarder(page);
  await page.getByRole('button', { name: 'View' }).click();
  await expect(page.getByRole('radio', { name: /Photos \| map/ })).toBeEnabled({ timeout: 20_000 });
  await page.getByRole('radio', { name: 'Photos | map' }).click();
  await page.getByRole('radio', { name: 'Terrain (3D)' }).click();
  await expect(page.getByText(/3D was too slow on this display, so the map switched to 2D/)).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId('route-3d')).toHaveCount(0);
  await expect(page.locator('.maplibregl-canvas')).toBeVisible();
  await expect(page.getByTestId('photo-view')).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem('wattson.view'))).toBe('split');
});

const EAST_GPX = `<?xml version="1.0"?><gpx version="1.1" creator="t" xmlns="http://www.topografix.com/GPX/1/1"><trk><name>East</name><trkseg>
${Array.from({ length: 12 }, (_, i) => `<trkpt lat="45.0000" lon="${(7 + i * 0.001).toFixed(4)}"><ele>${100 + i}</ele></trkpt>`).join('')}
</trkseg></trk></gpx>`;

test('2D map: heading up turns the map to the riding direction; the compass resets to north', async ({ page, routeId, baseURL }, info) => {
  test.skip(info.project.name !== 'tablet', 'one project covers it');
  test.setTimeout(60_000);
  void routeId;
  const cookies = await page.context().cookies();
  const fd = new FormData();
  fd.append('file', new Blob([EAST_GPX]), 'east.gpx');
  const res = await fetch(`${baseURL}/api/routes/import`, {
    method: 'POST',
    headers: { cookie: cookies.map((c) => `${c.name}=${c.value}`).join('; ') },
    body: fd,
  });
  const east = (await res.json()).id as number;
  await ride(page, east);
  const mapBox = page.locator('[data-testid="route-map"] .maplibregl-map').first();
  await expect(mapBox).toBeVisible();
  // The map handle lives on the container element MapView renders.
  const live = () => mapBox.evaluate((el) => (el as any).__map?.getBearing() ?? NaN);
  await expect.poll(live).toBe(0); // north up by default
  await expect(page.locator('.maplibregl-ctrl-compass')).toBeVisible();

  await page.getByRole('button', { name: 'View' }).click();
  await page.getByRole('radio', { name: 'Heading up' }).click();
  await expect.poll(live, { timeout: 15_000 }).toBeGreaterThan(80); // the route runs east
  expect(await page.evaluate(() => localStorage.getItem('wattson.view.headingUp'))).toBe('1');

  // The compass turns the map back to north and ends heading-up.
  await page.locator('.maplibregl-ctrl-compass').click();
  await expect.poll(live, { timeout: 5_000 }).toBeCloseTo(0, 0);
  await expect(page.getByRole('radio', { name: 'North up' })).toBeChecked();
  expect(await page.evaluate(() => localStorage.getItem('wattson.view.headingUp'))).toBe('0');
  await page.waitForTimeout(2500); // a few ticks later it stays north up
  expect(await live()).toBeCloseTo(0, 0);
});
